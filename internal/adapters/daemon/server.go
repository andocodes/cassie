package daemon

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/andocodes/cassie/internal/domain/runtime"
	"github.com/andocodes/cassie/internal/ports"
)

const maxRequestBytes = 8 << 20
const defaultShutdownTimeout = 15 * time.Second

var ErrAlreadyRunning = errors.New("Cassie daemon is already running")

type Server struct {
	stateDir   string
	supervisor ports.ProcessSupervisor
}

func NewServer(stateDir string, supervisor ports.ProcessSupervisor) *Server {
	return &Server{stateDir: stateDir, supervisor: supervisor}
}

// Serve listens on a per-user local transport until ctx is cancelled. The
// endpoint file and Unix socket are readable only by the current user.
func (s *Server) Serve(ctx context.Context) error {
	ctx, cancelServe := context.WithCancel(ctx)
	defer cancelServe()
	if s.supervisor == nil {
		return fmt.Errorf("daemon process supervisor is required")
	}
	if err := os.MkdirAll(s.stateDir, 0o700); err != nil {
		return fmt.Errorf("create daemon state directory: %w", err)
	}
	lock, err := acquireInstanceLock(filepath.Join(s.stateDir, "daemon.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := s.supervisor.Reconcile(ctx); err != nil {
		return fmt.Errorf("reconcile daemon processes: %w", err)
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	listener, value, cleanup, err := listenLocal(s.stateDir)
	if err != nil {
		return err
	}
	defer cleanup()
	value.Version = protocolVersion
	value.Token = token
	if err := writeEndpoint(s.stateDir, value); err != nil {
		_ = listener.Close()
		return err
	}
	defer os.Remove(endpointPath(s.stateDir))

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	var clients sync.WaitGroup
	defer clients.Wait()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
		defer cancel()
		_ = s.supervisor.Shutdown(shutdownCtx)
	}()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var temporary interface{ Temporary() bool }
			if errors.As(err, &temporary) && temporary.Temporary() {
				continue
			}
			return fmt.Errorf("accept daemon client: %w", err)
		}
		clients.Add(1)
		go func() {
			defer clients.Done()
			defer connection.Close()
			clientCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			s.handleConnection(clientCtx, connection, token)
		}()
	}
}

func (s *Server) handleConnection(ctx context.Context, connection net.Conn, token string) {
	decoder := json.NewDecoder(io.LimitReader(connection, maxRequestBytes))
	encoder := json.NewEncoder(connection)
	var request request
	if err := decoder.Decode(&request); err != nil {
		_ = encoder.Encode(errorResponse("", fmt.Errorf("decode request: %w", err)))
		return
	}
	if request.Version != protocolVersion {
		_ = encoder.Encode(errorResponse(request.ID, fmt.Errorf("unsupported daemon protocol %d", request.Version)))
		return
	}
	if subtle.ConstantTimeCompare([]byte(request.Token), []byte(token)) != 1 {
		_ = encoder.Encode(errorResponse(request.ID, errors.New("daemon authentication failed")))
		return
	}
	s.handleRequest(ctx, encoder, request)
}

func (s *Server) handleRequest(ctx context.Context, encoder *json.Encoder, request request) {
	switch request.Method {
	case methodPing:
		s.writeResult(encoder, request.ID, pingResult{PID: os.Getpid(), Protocol: protocolVersion})
	case methodStart:
		var spec runtime.ProcessSpec
		if !decodeParams(encoder, request, &spec) {
			return
		}
		process, err := s.supervisor.Start(ctx, spec)
		if err != nil {
			_ = encoder.Encode(errorResponse(request.ID, err))
			return
		}
		s.writeResult(encoder, request.ID, startResult{Process: process})
	case methodStop:
		var params stopParams
		if !decodeParams(encoder, request, &params) {
			return
		}
		if err := s.supervisor.Stop(ctx, params.ID); err != nil {
			_ = encoder.Encode(errorResponse(request.ID, err))
			return
		}
		s.writeResult(encoder, request.ID, struct{}{})
	case methodGet:
		var params getParams
		if !decodeParams(encoder, request, &params) {
			return
		}
		process, err := s.supervisor.Process(ctx, params.ID)
		if err != nil {
			_ = encoder.Encode(errorResponse(request.ID, err))
			return
		}
		s.writeResult(encoder, request.ID, getResult{Process: process})
	case methodList:
		var params listParams
		if !decodeParams(encoder, request, &params) {
			return
		}
		processes, err := s.supervisor.Processes(ctx, params.Limit)
		if err != nil {
			_ = encoder.Encode(errorResponse(request.ID, err))
			return
		}
		s.writeResult(encoder, request.ID, listResult{Processes: processes})
	case methodTail:
		var params logParams
		if !decodeParams(encoder, request, &params) {
			return
		}
		value, err := s.supervisor.Tail(ctx, params.ID, params.Limit)
		if err != nil {
			_ = encoder.Encode(errorResponse(request.ID, err))
			return
		}
		s.writeResult(encoder, request.ID, tailResult{Data: value})
	case methodWatch:
		var params logParams
		if !decodeParams(encoder, request, &params) {
			return
		}
		initial, stream, err := s.supervisor.Watch(ctx, params.ID, params.Limit)
		if err != nil {
			_ = encoder.Encode(errorResponse(request.ID, err))
			return
		}
		if !s.writeResult(encoder, request.ID, tailResult{Data: initial}) {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, open := <-stream:
				if !open {
					_ = encoder.Encode(response{Version: protocolVersion, Event: eventLogClosed})
					return
				}
				if err := encoder.Encode(response{Version: protocolVersion, Event: eventLog, Data: chunk}); err != nil {
					return
				}
			}
		}
	case methodEvents:
		var params listParams
		if !decodeParams(encoder, request, &params) {
			return
		}
		initial, stream, err := s.supervisor.WatchProcesses(ctx, params.Limit)
		if err != nil {
			_ = encoder.Encode(errorResponse(request.ID, err))
			return
		}
		if !s.writeResult(encoder, request.ID, listResult{Processes: initial}) {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case event, open := <-stream:
				if !open {
					return
				}
				if err := encoder.Encode(response{Version: protocolVersion, Event: eventProcess, Process: &event}); err != nil {
					return
				}
			}
		}
	default:
		_ = encoder.Encode(errorResponse(request.ID, fmt.Errorf("unknown daemon method %q", request.Method)))
	}
}

func decodeParams(encoder *json.Encoder, request request, target any) bool {
	if len(request.Params) == 0 {
		request.Params = []byte("{}")
	}
	if err := json.Unmarshal(request.Params, target); err != nil {
		_ = encoder.Encode(errorResponse(request.ID, fmt.Errorf("decode %s parameters: %w", request.Method, err)))
		return false
	}
	return true
}

func (s *Server) writeResult(encoder *json.Encoder, id string, value any) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		_ = encoder.Encode(errorResponse(id, fmt.Errorf("encode daemon response: %w", err)))
		return false
	}
	return encoder.Encode(response{Version: protocolVersion, ID: id, Result: encoded}) == nil
}

func errorResponse(id string, err error) response {
	return response{Version: protocolVersion, ID: id, Error: &wireError{Message: err.Error()}}
}

func randomToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create daemon authentication token: %w", err)
	}
	return hex.EncodeToString(value), nil
}
