package runtime

import "time"

type Status string

const (
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusStopped   Status = "stopped"
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
