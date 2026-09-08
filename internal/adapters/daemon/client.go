package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync/atomic"

	"github.com/andocodes/cassie/internal/domain/runtime"
)

type Info struct {
	PID      int
	Protocol int
}

// Client is safe for concurrent use. Each operation uses a new local
// connection, which keeps request cancellation and log streams independent.
type Client struct {
	endpoint endpoint
	nextID   atomic.Uint64
}

func Dial(ctx context.Context, stateDir string) (*Client, error) {
	value, err := readEndpoint(stateDir)
	if err != nil {
		return nil, err
	}
	client := &Client{endpoint: value}
	if _, err := client.Ping(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

func (c *Client) Ping(ctx context.Context) (Info, error) {
	var result pingResult
	if err := c.call(ctx, methodPing, struct{}{}, &result); err != nil {
		return Info{}, err
	}
	return Info{PID: result.PID, Protocol: result.Protocol}, nil
}

func (c *Client) Start(ctx context.Context, spec runtime.ProcessSpec) (runtime.Process, error) {
	var result startResult
	if err := c.call(ctx, methodStart, spec, &result); err != nil {
		return runtime.Process{}, err
	}
	return result.Process, nil
}

func (c *Client) Stop(ctx context.Context, id string) error {
	return c.call(ctx, methodStop, stopParams{ID: id}, &struct{}{})
}

func (c *Client) Process(ctx context.Context, id string) (runtime.Process, error) {
	var result getResult
	if err := c.call(ctx, methodGet, getParams{ID: id}, &result); err != nil {
		return runtime.Process{}, err
	}
	return result.Process, nil
}

func (c *Client) Processes(ctx context.Context, limit int) ([]runtime.Process, error) {
	var result listResult
	if err := c.call(ctx, methodList, listParams{Limit: limit}, &result); err != nil {
		return nil, err
	}
	return result.Processes, nil
}

func (c *Client) Tail(ctx context.Context, id string, limit int64) ([]byte, error) {
	var result tailResult
	if err := c.call(ctx, methodTail, logParams{ID: id, Limit: limit}, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// Watch returns a race-free initial log tail followed by live chunks. Cancelling
// ctx closes the connection without affecting the managed process.
func (c *Client) Watch(ctx context.Context, id string, limit int64) ([]byte, <-chan []byte, <-chan error, error) {
	connection, decoder, _, requestID, err := c.open(ctx, methodWatch, logParams{ID: id, Limit: limit})
	if err != nil {
		return nil, nil, nil, err
	}
	initialDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-initialDone:
		}
	}()
	var first response
	if err := decoder.Decode(&first); err != nil {
		close(initialDone)
		_ = connection.Close()
		return nil, nil, nil, fmt.Errorf("read daemon log stream: %w", err)
	}
	if err := validateResponse(first, requestID); err != nil {
		close(initialDone)
		_ = connection.Close()
		return nil, nil, nil, err
	}
	var initial tailResult
	if err := json.Unmarshal(first.Result, &initial); err != nil {
		close(initialDone)
		_ = connection.Close()
		return nil, nil, nil, fmt.Errorf("decode daemon log tail: %w", err)
	}
	close(initialDone)
	chunks := make(chan []byte, 64)
	errorsChannel := make(chan error, 1)
	streamDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-streamDone:
		}
	}()
	go func() {
		defer close(chunks)
		defer close(errorsChannel)
		defer connection.Close()
		defer close(streamDone)
		for {
			var event response
			if err := decoder.Decode(&event); err != nil {
				if ctx.Err() == nil {
					errorsChannel <- fmt.Errorf("read daemon log event: %w", err)
				}
				return
			}
			if event.Version != protocolVersion {
				errorsChannel <- fmt.Errorf("unsupported daemon protocol %d", event.Version)
				return
			}
			switch event.Event {
			case eventLog:
				select {
				case chunks <- event.Data:
				case <-ctx.Done():
					return
				}
			case eventLogClosed:
				return
			default:
				errorsChannel <- fmt.Errorf("unknown daemon event %q", event.Event)
				return
			}
		}
	}()
	return initial.Data, chunks, errorsChannel, nil
}

// WatchProcesses returns a current snapshot followed by process lifecycle
// events. Apply events as idempotent upserts keyed by Process.ID.
func (c *Client) WatchProcesses(ctx context.Context, limit int) ([]runtime.Process, <-chan runtime.ProcessEvent, <-chan error, error) {
	connection, decoder, _, requestID, err := c.open(ctx, methodEvents, listParams{Limit: limit})
	if err != nil {
		return nil, nil, nil, err
	}
	initialDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-initialDone:
		}
	}()
	var first response
	if err := decoder.Decode(&first); err != nil {
		close(initialDone)
		_ = connection.Close()
		return nil, nil, nil, fmt.Errorf("read daemon process stream: %w", err)
	}
	if err := validateResponse(first, requestID); err != nil {
		close(initialDone)
		_ = connection.Close()
		return nil, nil, nil, err
	}
	var initial listResult
	if err := json.Unmarshal(first.Result, &initial); err != nil {
		close(initialDone)
		_ = connection.Close()
		return nil, nil, nil, fmt.Errorf("decode daemon process snapshot: %w", err)
	}
	close(initialDone)
	events := make(chan runtime.ProcessEvent, 64)
	errorsChannel := make(chan error, 1)
	streamDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-streamDone:
		}
	}()
	go func() {
		defer close(events)
		defer close(errorsChannel)
		defer connection.Close()
		defer close(streamDone)
		for {
			var value response
			if err := decoder.Decode(&value); err != nil {
				if ctx.Err() == nil {
					errorsChannel <- fmt.Errorf("read daemon process event: %w", err)
				}
				return
			}
			if value.Version != protocolVersion {
				errorsChannel <- fmt.Errorf("unsupported daemon protocol %d", value.Version)
				return
			}
			if value.Event != eventProcess || value.Process == nil {
				errorsChannel <- fmt.Errorf("unknown daemon event %q", value.Event)
				return
			}
			select {
			case events <- *value.Process:
			case <-ctx.Done():
				return
			}
		}
	}()
	return initial.Processes, events, errorsChannel, nil
}

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	connection, decoder, _, requestID, err := c.open(ctx, method, params)
	if err != nil {
		return err
	}
	defer connection.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-done:
		}
	}()
	var value response
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("read daemon response: %w", err)
	}
	if err := validateResponse(value, requestID); err != nil {
		return err
	}
	if result != nil && len(value.Result) > 0 {
		if err := json.Unmarshal(value.Result, result); err != nil {
			return fmt.Errorf("decode daemon response: %w", err)
		}
	}
	return nil
}

func (c *Client) open(ctx context.Context, method string, params any) (net.Conn, *json.Decoder, *json.Encoder, string, error) {
	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, c.endpoint.Network, c.endpoint.Address)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("connect to Cassie daemon: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		_ = connection.Close()
		return nil, nil, nil, "", fmt.Errorf("encode daemon request: %w", err)
	}
	requestID := strconv.FormatUint(c.nextID.Add(1), 10)
	value := request{
		Version: protocolVersion,
		ID:      requestID,
		Token:   c.endpoint.Token,
		Method:  method,
		Params:  encoded,
	}
	encoder := json.NewEncoder(connection)
	if err := encoder.Encode(value); err != nil {
		_ = connection.Close()
		return nil, nil, nil, "", fmt.Errorf("send daemon request: %w", err)
	}
	return connection, json.NewDecoder(connection), encoder, requestID, nil
}

func validateResponse(value response, requestID string) error {
	if value.Version != protocolVersion {
		return fmt.Errorf("unsupported daemon protocol %d", value.Version)
	}
	if value.ID != requestID {
		return fmt.Errorf("daemon response ID %q does not match request %q", value.ID, requestID)
	}
	if value.Error != nil {
		return errors.New(value.Error.Message)
	}
	return nil
}
