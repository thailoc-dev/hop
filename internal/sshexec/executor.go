// Package sshexec is one of exactly two packages permitted to spawn a
// process. Everything else goes through the Executor interface, which is what
// makes retry timing, backoff and error classification testable without a VPS.
package sshexec

import "context"

// Result is the outcome of a one-shot remote command.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// ForwardSpec describes a single local-to-container port forward.
//
// RemoteAddr is the container's current IP, resolved immediately before the
// forward starts. It is deliberately not part of any persisted record: it is
// re-resolved on every attempt, because a restarted container changes address.
type ForwardSpec struct {
	Host       string
	RemoteAddr string
	RemotePort int
	LocalPort  int
}

// Proc is a running forward.
type Proc interface {
	// Done is closed when the process exits, for any reason.
	Done() <-chan struct{}
	// Stderr returns everything the process wrote to stderr. It is only
	// guaranteed complete after Done is closed.
	Stderr() string
	// Terminate stops the process: SIGTERM, then SIGKILL if it ignores that.
	Terminate() error
}

// Executor spawns ssh. It is the seam between hop's logic and the outside
// world.
type Executor interface {
	Run(ctx context.Context, host string, cmd ...string) (Result, error)
	ContainerIP(ctx context.Context, host, container string) (string, error)
	StartForward(ctx context.Context, spec ForwardSpec) (Proc, error)
	// Close tears down multiplexed command connections. Forwards are not
	// affected; the caller terminates those itself.
	Close(ctx context.Context)
}
