// Package picker is the interactive front door: `hop` with no arguments.
// The model is pure over Sources, which is what lets every stage be driven
// by key messages in tests with no terminal, no daemon and no ssh.
package picker

import (
	"context"
	"time"

	"github.com/thailoc-dev/hop/internal/complete"
	"github.com/thailoc-dev/hop/internal/tunnel"
)

// FetchTimeout bounds the container fetch. Interactive, so far more generous
// than completion's 300ms: a first ssh connection costs seconds and the user
// is watching a spinner, not a frozen shell.
const FetchTimeout = 10 * time.Second

// Sources is everything the picker needs to know about the world.
type Sources interface {
	// Tunnels are running and saved tunnels, as `hop ls` shows them.
	Tunnels() []tunnel.Status
	// Hosts are ssh config aliases, patterns excluded.
	Hosts() []string
	// Containers lists what is running on a host. ctx carries FetchTimeout.
	Containers(ctx context.Context, host string) ([]complete.Container, error)
	// PortFree reports whether a local port can be bound right now.
	PortFree(port int) bool
	// FreePort returns a port the OS considers free.
	FreePort() (int, error)
	// ValidateName applies hop's naming rules.
	ValidateName(name string) error
}

// Result is what the picker hands back.
type Result struct {
	Spec  tunnel.Spec
	Named bool // stage 1 chose a saved tunnel: open it by name
}
