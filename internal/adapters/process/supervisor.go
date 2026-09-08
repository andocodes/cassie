package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/andocodes/cassie/internal/domain/runtime"
	"github.com/andocodes/cassie/internal/ports"
)

// Supervisor owns child processes independently of the context used to start
// them. This allows CLI and TUI clients to disconnect without stopping apps.
type Supervisor struct {
	store ports.ProcessStore
	logs  ports.ProcessLogStore
	now   func() time.Time

	mu       sync.Mutex
	active   map[string]*managedProcess
	events   map[chan runtime.ProcessEvent]struct{}
	starting map[string]struct{}
}

type managedProcess struct {
	command  *exec.Cmd
	output   io.WriteCloser
	done     chan struct{}
	grace    time.Duration
	stopping bool
	process  runtime.Process
}

func NewSupervisor(store ports.ProcessStore, logs ports.ProcessLogStore) *Supervisor {
	return &Supervisor{
		store:    store,
		logs:     logs,
		now:      time.Now,
		active:   make(map[string]*managedProcess),
		events:   make(map[chan runtime.ProcessEvent]struct{}),
		starting: make(map[string]struct{}),
	}
}

func (s *Supervisor) Start(ctx context.Context, spec runtime.ProcessSpec) (runtime.Process, error) {
	if err := spec.Validate(); err != nil {
		return runtime.Process{}, err
	}
	if err := ctx.Err(); err != nil {
		return runtime.Process{}, err
	}
	applicationKey := spec.App + "\x00" + spec.Root
	s.mu.Lock()
	if _, exists := s.active[spec.ID]; exists {
		s.mu.Unlock()
		return runtime.Process{}, runtime.ErrProcessRunning
	}
	if _, exists := s.starting[applicationKey]; exists {
		s.mu.Unlock()
		return runtime.Process{}, runtime.ErrApplicationRunning
	}
	for _, active := range s.active {
		if active.process.App == spec.App && active.process.Root == spec.Root {
			s.mu.Unlock()
			return runtime.Process{}, runtime.ErrApplicationRunning
		}
	}
	s.starting[applicationKey] = struct{}{}
	s.mu.Unlock()
	reserved := true
	defer func() {
		if reserved {
			s.mu.Lock()
			delete(s.starting, applicationKey)
			s.mu.Unlock()
		}
	}()
	if _, err := s.store.Process(ctx, spec.ID); err == nil {
		return runtime.Process{}, runtime.ErrProcessExists
	} else if !errors.Is(err, runtime.ErrProcessNotFound) {
		return runtime.Process{}, fmt.Errorf("check managed process ID: %w", err)
	}

	output, logPath, err := s.logs.Open(spec.ID)
	if err != nil {
		return runtime.Process{}, fmt.Errorf("open managed process log: %w", err)
	}
	dir := spec.Root
	if spec.Dir != "" {
		dir = filepath.Join(spec.Root, spec.Dir)
	}
	shell, args := shellCommand(spec.Command)
	command := exec.Command(shell, args...)
	command.Dir = dir
	environment := make(map[string]string, len(spec.Env)+1)
	for key, value := range spec.Env {
		environment[key] = value
	}
	environment["CASSIE_SESSION_ID"] = spec.ID
	command.Env = mergeEnv(os.Environ(), environment)
	command.Stdout = output
	command.Stderr = output
	configureProcess(command)
	if err := command.Start(); err != nil {
		_ = output.Close()
		return runtime.Process{}, fmt.Errorf("start managed process %q: %w", spec.App, err)
	}

	startedAt := s.now()
	process := runtime.Process{
		ID:        spec.ID,
		App:       spec.App,
		Root:      spec.Root,
		PID:       command.Process.Pid,
		Status:    runtime.StatusRunning,
		StartedAt: startedAt,
		LogPath:   logPath,
	}
	if err := s.store.StartProcess(ctx, process); err != nil {
		_ = killManagedProcess(command)
		_ = command.Wait()
		_ = output.Close()
		return runtime.Process{}, fmt.Errorf("persist managed process: %w", err)
	}
	grace := spec.Grace
	if grace <= 0 {
		grace = 10 * time.Second
	}
	managed := &managedProcess{
		command: command,
		output:  output,
		done:    make(chan struct{}),
		grace:   grace,
		process: process,
	}
	s.mu.Lock()
	s.active[spec.ID] = managed
	delete(s.starting, applicationKey)
	reserved = false
	s.mu.Unlock()
	s.publish(runtime.ProcessEvent{Type: runtime.ProcessStarted, Process: process})
	go s.wait(spec.ID, managed)
	return process, nil
}

func (s *Supervisor) wait(id string, managed *managedProcess) {
	err := managed.command.Wait()
	s.mu.Lock()
	stopping := managed.stopping
	if s.active[id] == managed {
		delete(s.active, id)
	}
	s.mu.Unlock()
	_ = managed.output.Close()

	status := runtime.StatusSucceeded
	exitCode := processExitCode(err)
	if stopping {
		status = runtime.StatusStopped
		exitCode = 130
	} else if err != nil {
		status = runtime.StatusFailed
	}
	endedAt := s.now()
	finishCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.store.FinishProcess(finishCtx, id, status, exitCode, endedAt)
	finished := managed.process
	finished.Status = status
	finished.ExitCode = &exitCode
	finished.EndedAt = &endedAt
	s.publish(runtime.ProcessEvent{Type: runtime.ProcessFinished, Process: finished})
	close(managed.done)
}

func (s *Supervisor) Stop(ctx context.Context, id string) error {
	s.mu.Lock()
	managed := s.active[id]
	if managed == nil {
		s.mu.Unlock()
		process, err := s.store.Process(ctx, id)
		if err != nil {
			return err
		}
		if process.Status != runtime.StatusRunning {
			return runtime.ErrProcessNotRunning
		}
		return fmt.Errorf("%w: process belongs to a previous daemon", runtime.ErrProcessNotRunning)
	}
	managed.stopping = true
	s.mu.Unlock()

	if err := interruptManagedProcess(managed.command); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("interrupt managed process: %w", err)
	}
	timer := time.NewTimer(managed.grace)
	defer timer.Stop()
	select {
	case <-managed.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		if err := killManagedProcess(managed.command); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill managed process: %w", err)
		}
	}
	select {
	case <-managed.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Supervisor) Process(ctx context.Context, id string) (runtime.Process, error) {
	return s.store.Process(ctx, id)
}

func (s *Supervisor) Processes(ctx context.Context, limit int) ([]runtime.Process, error) {
	return s.store.Processes(ctx, limit)
}

func (s *Supervisor) Tail(_ context.Context, id string, limit int64) ([]byte, error) {
	return s.logs.Tail(id, limit)
}

func (s *Supervisor) Follow(ctx context.Context, id string) (<-chan []byte, error) {
	return s.logs.Follow(ctx, id)
}

func (s *Supervisor) Watch(ctx context.Context, id string, limit int64) ([]byte, <-chan []byte, error) {
	return s.logs.Watch(ctx, id, limit)
}

// WatchProcesses returns an initial snapshot and later lifecycle events. An
// event may repeat the state in the snapshot; clients should upsert by process
// ID. Registering before the query ensures clients cannot miss a transition.
func (s *Supervisor) WatchProcesses(ctx context.Context, limit int) ([]runtime.Process, <-chan runtime.ProcessEvent, error) {
	stream := make(chan runtime.ProcessEvent, 64)
	s.mu.Lock()
	s.events[stream] = struct{}{}
	s.mu.Unlock()
	processes, err := s.store.Processes(ctx, limit)
	if err != nil {
		s.mu.Lock()
		delete(s.events, stream)
		s.mu.Unlock()
		close(stream)
		return nil, nil, err
	}
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		if _, exists := s.events[stream]; exists {
			delete(s.events, stream)
			close(stream)
		}
		s.mu.Unlock()
	}()
	return processes, stream, nil
}

// Reconcile marks records left running by a previous daemon as interrupted.
// Cassie does not signal an unverified PID because the operating system may
// have reused it.
func (s *Supervisor) Reconcile(ctx context.Context) error {
	processes, err := s.store.RunningProcesses(ctx)
	if err != nil {
		return fmt.Errorf("load running managed processes: %w", err)
	}
	for _, process := range processes {
		s.mu.Lock()
		_, owned := s.active[process.ID]
		s.mu.Unlock()
		if owned {
			continue
		}
		endedAt := s.now()
		if err := s.store.FinishProcess(ctx, process.ID, runtime.StatusInterrupted, 1, endedAt); err != nil {
			return fmt.Errorf("reconcile managed process %s: %w", process.ID, err)
		}
		exitCode := 1
		process.Status = runtime.StatusInterrupted
		process.EndedAt = &endedAt
		process.ExitCode = &exitCode
		s.publish(runtime.ProcessEvent{Type: runtime.ProcessReconciled, Process: process})
	}
	return nil
}

func (s *Supervisor) publish(event runtime.ProcessEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for subscriber := range s.events {
		select {
		case subscriber <- event:
		default:
			// Lifecycle state is recoverable from SQLite. Disconnect a slow
			// subscriber so it must reconnect and receive a fresh snapshot.
			delete(s.events, subscriber)
			close(subscriber)
		}
	}
}

func (s *Supervisor) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	ids := make([]string, 0, len(s.active))
	for id := range s.active {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	var result error
	for _, id := range ids {
		if err := s.Stop(ctx, id); err != nil {
			result = errors.Join(result, fmt.Errorf("stop %s: %w", id, err))
		}
	}
	return result
}

func processExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode()
	}
	return 1
}
