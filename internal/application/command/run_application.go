package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/andocodes/cassie/internal/domain/catalog"
	"github.com/andocodes/cassie/internal/domain/runtime"
	"github.com/andocodes/cassie/internal/ports"
	"github.com/google/uuid"
)

type RunApplication struct {
	Runner   ports.ProcessRunner
	Secrets  ports.SecretProvider
	Prepare  ports.EnvironmentPreparer
	Sessions ports.SessionStore
	Router   ports.Router
	Env      map[string]string
	Now      func() time.Time
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
}

type RunResult struct {
	SessionID string
	ExitCode  int
}

func (h RunApplication) Handle(ctx context.Context, app catalog.Application) (RunResult, error) {
	if err := app.ValidateRunnable(); err != nil {
		return RunResult{ExitCode: 1}, err
	}
	now := h.Now
	if now == nil {
		now = time.Now
	}
	grace := app.Grace.Duration
	if grace <= 0 {
		grace = 10 * time.Second
	}

	environment := make(map[string]string, len(h.Env))
	for key, value := range h.Env {
		environment[key] = value
	}
	secrets, err := h.Secrets.Load(ctx, app.Secrets)
	if err != nil {
		return RunResult{ExitCode: 1}, err
	}
	for key, value := range secrets {
		environment[key] = value
	}

	session := runtime.Session{
		ID:        uuid.NewString(),
		App:       app.Name,
		Root:      app.Root,
		Status:    runtime.StatusRunning,
		StartedAt: now(),
	}
	prepared := ports.PreparedEnvironment{Values: secrets, Cleanup: func() error { return nil }}
	if h.Prepare != nil {
		prepared, err = h.Prepare.Prepare(ctx, session.ID, app, secrets)
		if err != nil {
			return RunResult{SessionID: session.ID, ExitCode: 1}, err
		}
	}
	for key, value := range prepared.Values {
		environment[key] = value
	}
	prepared.Values = environment
	if err := h.Sessions.Start(ctx, session); err != nil {
		_ = prepared.Cleanup()
		return RunResult{ExitCode: 1}, err
	}

	aliased := app.Port > 0
	if aliased {
		if err := h.Router.Alias(ctx, app.Domain, app.Port); err != nil {
			_ = prepared.Cleanup()
			h.finish(session.ID, runtime.StatusFailed, 1, now())
			return RunResult{SessionID: session.ID, ExitCode: 1}, err
		}
	}

	var runErr error
	for index, item := range app.Commands {
		if index == len(app.Commands)-1 && !aliased {
			item = h.Router.Wrap(app, item)
		}
		if h.Stdout != nil {
			_, _ = fmt.Fprintf(h.Stdout, "\n$ %s\n", item.Run)
		}
		runErr = h.Runner.Run(ctx, ports.Process{
			Command: item,
			Root:    app.Root,
			Env:     prepared.Values,
			Stdin:   h.Stdin,
			Stdout:  h.Stdout,
			Stderr:  h.Stderr,
		})
		if runErr != nil {
			break
		}
	}

	cleanupCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	var cleanupErr error
	for index := len(app.Cleanup) - 1; index >= 0; index-- {
		item := app.Cleanup[index]
		if h.Stdout != nil {
			_, _ = fmt.Fprintf(h.Stdout, "\n$ %s\n", item.Run)
		}
		if err := h.Runner.Run(cleanupCtx, ports.Process{
			Command: item,
			Root:    app.Root,
			Env:     prepared.Values,
			Stdin:   h.Stdin,
			Stdout:  h.Stdout,
			Stderr:  h.Stderr,
		}); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	if aliased {
		if err := h.Router.Remove(cleanupCtx, app.Domain); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	if err := prepared.Cleanup(); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove runtime files: %w", err))
	}

	finalErr := runErr
	if finalErr == nil {
		finalErr = cleanupErr
	} else if cleanupErr != nil && h.Stderr != nil {
		_, _ = fmt.Fprintf(h.Stderr, "cassie: cleanup: %v\n", cleanupErr)
	}
	exitCode := exitCode(finalErr)
	status := runtime.StatusSucceeded
	if finalErr != nil {
		status = runtime.StatusFailed
		if errors.Is(ctx.Err(), context.Canceled) {
			status = runtime.StatusStopped
			exitCode = 130
		}
	}
	h.finish(session.ID, status, exitCode, now())
	return RunResult{SessionID: session.ID, ExitCode: exitCode}, finalErr
}

func (h RunApplication) finish(id string, status runtime.Status, exitCode int, endedAt time.Time) {
	finishCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = h.Sessions.Finish(finishCtx, id, status, exitCode, endedAt)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	return 1
}
