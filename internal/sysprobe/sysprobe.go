// Package sysprobe is the second and last package permitted to spawn a
// process. It answers questions about the local machine: who holds a TCP
// port, what the default route is, and it signals orphaned processes.
package sysprobe

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// probeTimeout stops lsof or route wedging a completion or a daemon start.
const probeTimeout = 3 * time.Second

// Holder identifies the process listening on a local port.
type Holder struct {
	Command string
	PID     int
}

// Prober inspects local network state.
type Prober interface {
	// PortFree reports whether a listener can bind 127.0.0.1:port right now.
	PortFree(port int) bool
	// PortHolder names the process listening on port.
	PortHolder(port int) (Holder, error)
	// DefaultRoute returns the current default gateway. A change means the
	// machine moved networks and every tunnel should be recycled.
	DefaultRoute() (string, error)
	// Kill terminates a process by PID. Used only to reap orphaned forwards
	// left by a daemon that did not shut down cleanly.
	Kill(pid int) error
}

type prober struct{}

func New() Prober { return prober{} }

// PortFree binds the port and immediately releases it. Asking the kernel is
// the only answer that is not a guess, and it is the same operation ssh will
// perform a moment later.
func (prober) PortFree(port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

func (prober) PortHolder(port int) (Holder, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "lsof",
		"-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-FpcL").Output()
	if err != nil {
		return Holder{}, fmt.Errorf("lsof for port %d: %w", port, err)
	}

	// -F emits one field per line, prefixed by a type character: p<pid>, c<command>.
	var holder Holder
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			holder.PID, _ = strconv.Atoi(line[1:])
		case 'c':
			holder.Command = line[1:]
		}
	}
	if holder.PID == 0 {
		return Holder{}, fmt.Errorf("nothing is listening on port %d", port)
	}
	return holder, nil
}

func (prober) Kill(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find pid %d: %w", pid, err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("terminate pid %d: %w", pid, err)
	}
	return nil
}

func (prober) DefaultRoute() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "route", "-n", "get", "default").Output()
	if err != nil {
		return "", fmt.Errorf("route get default: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		field, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if found && field == "gateway" {
			return strings.TrimSpace(value), nil
		}
	}
	return "", nil // no default route: offline, which is a valid answer
}
