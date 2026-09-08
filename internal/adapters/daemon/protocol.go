package daemon

import (
	"encoding/json"

	"github.com/andocodes/cassie/internal/domain/runtime"
)

const protocolVersion = 1

const (
	methodPing     = "daemon.ping"
	methodStart    = "process.start"
	methodStop     = "process.stop"
	methodGet      = "process.get"
	methodList     = "process.list"
	methodTail     = "logs.tail"
	methodWatch    = "logs.watch"
	methodEvents   = "events.watch"
	eventLog       = "log"
	eventLogClosed = "log.closed"
	eventProcess   = "process"
)

type endpoint struct {
	Version int    `json:"version"`
	Network string `json:"network"`
	Address string `json:"address"`
	Token   string `json:"token"`
}

type request struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Token   string          `json:"token"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	Version int                   `json:"version"`
	ID      string                `json:"id,omitempty"`
	Result  json.RawMessage       `json:"result,omitempty"`
	Error   *wireError            `json:"error,omitempty"`
	Event   string                `json:"event,omitempty"`
	Data    []byte                `json:"data,omitempty"`
	Process *runtime.ProcessEvent `json:"process,omitempty"`
}

type wireError struct {
	Message string `json:"message"`
}

type stopParams struct {
	ID string `json:"id"`
}

type getParams struct {
	ID string `json:"id"`
}

type listParams struct {
	Limit int `json:"limit,omitempty"`
}

type logParams struct {
	ID    string `json:"id"`
	Limit int64  `json:"limit,omitempty"`
}

type pingResult struct {
	PID      int `json:"pid"`
	Protocol int `json:"protocol"`
}

type startResult struct {
	Process runtime.Process `json:"process"`
}

type getResult struct {
	Process runtime.Process `json:"process"`
}

type listResult struct {
	Processes []runtime.Process `json:"processes"`
}

type tailResult struct {
	Data []byte `json:"data"`
}
