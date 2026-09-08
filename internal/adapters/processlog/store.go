package processlog

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

const defaultMaxBytes int64 = 4 << 20

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Store persists a bounded combined stdout and stderr log for each managed
// process. Slow followers never block the child process.
type Store struct {
	root     string
	maxBytes int64
	mu       sync.Mutex
	streams  map[string]*stream
}

type stream struct {
	mu          sync.Mutex
	file        *os.File
	subscribers map[chan []byte]struct{}
	closed      bool
	maxBytes    int64
}

type writer struct {
	store  *Store
	id     string
	stream *stream
	once   sync.Once
}

func New(root string, maxBytes int64) *Store {
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	return &Store{root: root, maxBytes: maxBytes, streams: make(map[string]*stream)}
}

func (s *Store) Open(id string) (io.WriteCloser, string, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, "", fmt.Errorf("create process log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("open process log: %w", err)
	}
	value := &stream{
		file:        file,
		subscribers: make(map[chan []byte]struct{}),
		maxBytes:    s.maxBytes,
	}
	s.mu.Lock()
	if _, exists := s.streams[id]; exists {
		s.mu.Unlock()
		_ = file.Close()
		return nil, "", fmt.Errorf("process log %q is already open", id)
	}
	s.streams[id] = value
	s.mu.Unlock()
	return &writer{store: s, id: id, stream: value}, path, nil
}

func (w *writer) Write(value []byte) (int, error) {
	w.stream.mu.Lock()
	defer w.stream.mu.Unlock()
	if w.stream.closed {
		return 0, os.ErrClosed
	}
	written, err := w.stream.file.Write(value)
	if err != nil {
		return written, err
	}
	if err := w.stream.compact(); err != nil {
		return written, err
	}
	chunk := append([]byte(nil), value[:written]...)
	for subscriber := range w.stream.subscribers {
		select {
		case subscriber <- chunk:
		default:
		}
	}
	return written, nil
}

func (w *writer) Close() error {
	var err error
	w.once.Do(func() {
		err = w.store.close(w.id, w.stream)
	})
	return err
}

func (s *stream) compact() error {
	info, err := s.file.Stat()
	if err != nil {
		return fmt.Errorf("inspect process log: %w", err)
	}
	if info.Size() <= s.maxBytes {
		return nil
	}
	keep := s.maxBytes * 3 / 4
	buffer := make([]byte, keep)
	if _, err := s.file.ReadAt(buffer, info.Size()-keep); err != nil && err != io.EOF {
		return fmt.Errorf("read process log tail: %w", err)
	}
	if err := s.file.Truncate(0); err != nil {
		return fmt.Errorf("truncate process log: %w", err)
	}
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek process log: %w", err)
	}
	if _, err := s.file.Write(buffer); err != nil {
		return fmt.Errorf("rewrite process log tail: %w", err)
	}
	return nil
}

func (s *Store) Tail(id string, limit int64) ([]byte, error) {
	s.mu.Lock()
	value := s.streams[id]
	s.mu.Unlock()
	if value != nil {
		value.mu.Lock()
		defer value.mu.Unlock()
	}
	return s.tail(id, limit)
}

func (s *Store) tail(id string, limit int64) ([]byte, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open process log: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect process log: %w", err)
	}
	if limit <= 0 || limit > s.maxBytes {
		limit = s.maxBytes
	}
	start := info.Size() - limit
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek process log: %w", err)
	}
	value, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read process log: %w", err)
	}
	return value, nil
}

func (s *Store) Follow(ctx context.Context, id string) (<-chan []byte, error) {
	if _, err := s.path(id); err != nil {
		return nil, err
	}
	s.mu.Lock()
	value := s.streams[id]
	s.mu.Unlock()
	result := make(chan []byte, 64)
	if value == nil {
		close(result)
		return result, nil
	}
	value.mu.Lock()
	if value.closed {
		value.mu.Unlock()
		close(result)
		return result, nil
	}
	value.subscribers[result] = struct{}{}
	value.mu.Unlock()
	go func() {
		<-ctx.Done()
		value.mu.Lock()
		if _, exists := value.subscribers[result]; exists {
			delete(value.subscribers, result)
			close(result)
		}
		value.mu.Unlock()
	}()
	return result, nil
}

// Watch returns an initial tail and subscribes to later writes atomically, so
// clients do not lose output between loading history and following a process.
func (s *Store) Watch(ctx context.Context, id string, limit int64) ([]byte, <-chan []byte, error) {
	if _, err := s.path(id); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	value := s.streams[id]
	s.mu.Unlock()
	if value == nil {
		initial, err := s.Tail(id, limit)
		if err != nil {
			return nil, nil, err
		}
		result := make(chan []byte)
		close(result)
		return initial, result, nil
	}

	value.mu.Lock()
	initial, err := s.tail(id, limit)
	if err != nil {
		value.mu.Unlock()
		return nil, nil, err
	}
	result := make(chan []byte, 64)
	if value.closed {
		value.mu.Unlock()
		close(result)
		return initial, result, nil
	}
	value.subscribers[result] = struct{}{}
	value.mu.Unlock()
	go func() {
		<-ctx.Done()
		value.mu.Lock()
		if _, exists := value.subscribers[result]; exists {
			delete(value.subscribers, result)
			close(result)
		}
		value.mu.Unlock()
	}()
	return initial, result, nil
}

func (s *Store) close(id string, value *stream) error {
	s.mu.Lock()
	if s.streams[id] == value {
		delete(s.streams, id)
	}
	s.mu.Unlock()
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.closed {
		return nil
	}
	value.closed = true
	for subscriber := range value.subscribers {
		delete(value.subscribers, subscriber)
		close(subscriber)
	}
	if err := value.file.Close(); err != nil {
		return fmt.Errorf("close process log: %w", err)
	}
	return nil
}

func (s *Store) path(id string) (string, error) {
	if !validID.MatchString(id) || id == "." || id == ".." {
		return "", fmt.Errorf("invalid process ID %q", id)
	}
	return filepath.Join(s.root, id+".log"), nil
}
