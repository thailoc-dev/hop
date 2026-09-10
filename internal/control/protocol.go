// Package control is the wire between the hop CLI and the supervisor it
// spawns. One JSON object per line, request then response, connection closed.
// No RPC framework: the whole protocol is seven operations.
package control

import "github.com/locnguyen/hop/internal/tunnel"

// Operations.
const (
	OpPing         = "ping"
	OpAdd          = "add"
	OpList         = "list"
	OpRemove       = "remove"
	OpRestart      = "restart"
	OpEvents       = "events"
	OpMarkDegraded = "mark-degraded"
)

// Request is a single command to the supervisor.
type Request struct {
	Op        string       `json:"op"`
	Spec      *tunnel.Spec `json:"spec,omitempty"`
	LocalPort int          `json:"local_port,omitempty"`
	All       bool         `json:"all,omitempty"`
	Limit     int          `json:"limit,omitempty"`
	Reason    string       `json:"reason,omitempty"`
}

// Response is the supervisor's reply. Error carries application-level
// rejections; transport failures surface as a Go error from Send instead.
type Response struct {
	OK       bool            `json:"ok"`
	Error    string          `json:"error,omitempty"`
	Statuses []tunnel.Status `json:"statuses,omitempty"`
	Events   []tunnel.Event  `json:"events,omitempty"`
}

// Handler serves requests. The supervisor implements it.
type Handler interface {
	Handle(Request) Response
}
