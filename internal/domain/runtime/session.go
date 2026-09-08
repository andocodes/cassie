package runtime

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrProcessNotFound    = errors.New("managed process not found")
	ErrProcessRunning     = errors.New("managed process is already running")
	ErrProcessNotRunning  = errors.New("managed process is not running")
	ErrProcessExists      = errors.New("managed process already exists")
	ErrApplicationRunning = errors.New("application already has a running process")
)

type Status string

const (
	StatusRunning     Status = "running"
	StatusSucceeded   Status = "succeeded"
	StatusFailed      Status = "failed"
	StatusStopped     Status = "stopped"
	StatusInterrupted Status = "interrupted"
)

type Session struct {
	ID        string
	App       string
	Root      string
	Status    Status
	StartedAt time.Time
	EndedAt   *time.Time
	ExitCode  *int
}

// ProcessSpec describes a process that the Cassie daemon owns. Environment
// values are intentionally absent from Process so they cannot enter persisted
// state or query responses.
type ProcessSpec struct {
	ID      string            `json:"id"`
	App     string            `json:"app"`
	Root    string            `json:"root"`
	Command string            `json:"command"`
	Dir     string            `json:"dir,omitempty"`
	Env     map[string]string `json:"environment,omitempty"`
	Grace   time.Duration     `json:"grace,omitempty"`
}

func (s ProcessSpec) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("process ID is required")
	}
	if s.App == "" {
		return fmt.Errorf("application name is required")
	}
	if s.Root == "" {
		return fmt.Errorf("application root is required")
	}
	if s.Command == "" {
		return fmt.Errorf("process command is required")
	}
	cleaned := filepath.Clean(s.Dir)
	if filepath.IsAbs(s.Dir) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("process directory %q must be relative to the application root", s.Dir)
	}
	return nil
}

// Process is safe to persist and send to clients. It never contains the
// process environment or secret values.
type Process struct {
	ID        string     `json:"id"`
	App       string     `json:"app"`
	Root      string     `json:"root"`
	PID       int        `json:"pid"`
	Status    Status     `json:"status"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	ExitCode  *int       `json:"exitCode,omitempty"`
	LogPath   string     `json:"logPath"`
}

type ProcessEventType string

const (
	ProcessStarted    ProcessEventType = "started"
	ProcessFinished   ProcessEventType = "finished"
	ProcessReconciled ProcessEventType = "reconciled"
)

type ProcessEvent struct {
	Type    ProcessEventType `json:"type"`
	Process Process          `json:"process"`
}
