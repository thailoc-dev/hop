package tunnel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/locnguyen/hop/internal/sshexec"
	"github.com/locnguyen/hop/internal/sysprobe"
)

const (
	// unknownRetryCap bounds retries for failures we could not classify.
	// Retrying an unrecognised error forever would hide a real problem behind
	// a tunnel that looks busy; ten attempts is enough to ride out a fluke.
	unknownRetryCap = 10

	// healthyResetAfter is how long a tunnel must stay up before its backoff
	// returns to one second, so a single flap does not pin it at the cap.
	healthyResetAfter = 60 * time.Second

	// eventRing is how many transitions are kept for `hop logs`.
	eventRing = 200

	// bindPollInterval and bindTimeout govern the wait for ssh to bind the
	// local port. Starting the ssh process is not the same as having a working
	// forward: a real connection needs TCP, key exchange and authentication
	// first, which takes seconds.
	bindPollInterval = 100 * time.Millisecond
	bindTimeout      = 30 * time.Second
)

// State is where a tunnel is in its lifecycle.
type State string

const (
	StateResolving  State = "resolving"
	StateConnecting State = "connecting"
	StateHealthy    State = "healthy"
	StateDegraded   State = "degraded"
	StateRetrying   State = "retrying"
	StateFatal      State = "fatal"
	StateStopped    State = "stopped"
)

// Spec is the persisted definition of a tunnel: everything needed to rebuild
// it, and nothing that changes while it runs. The container's IP is
// deliberately absent — it is re-resolved on every attempt.
type Spec struct {
	Host       string `json:"host"`
	Container  string `json:"container"`
	RemotePort int    `json:"remote_port"`
	LocalPort  int    `json:"local_port"`
	Env        string `json:"env"`
	Autostart  bool   `json:"autostart"` // reserved for launchd support
}

// Status is a point-in-time view for `hop ls`.
type Status struct {
	Spec        Spec      `json:"spec"`
	State       State     `json:"state"`
	Since       time.Time `json:"since"`
	Retries     int       `json:"retries"`
	LastError   string    `json:"last_error,omitempty"`
	ContainerIP string    `json:"container_ip,omitempty"`
}

// Event is one transition, kept for `hop logs`.
type Event struct {
	At      time.Time `json:"at"`
	State   State     `json:"state"`
	Message string    `json:"message,omitempty"`
}

// Deps are the injected collaborators. Every one of them has a fake, which is
// what lets this package's tests run with no VPS and no real waiting.
type Deps struct {
	Exec  sshexec.Executor
	Probe sysprobe.Prober
	Clock Clock
	Seed  int64
}

// Tunnel supervises a single port forward.
type Tunnel struct {
	spec Spec
	deps Deps
	back *Backoff

	mu          sync.Mutex
	state       State
	since       time.Time
	retries     int
	lastErr     string
	containerIP string
	events      []Event
	unknownRun  int

	recycle chan struct{}
	stop    chan struct{}
	stopOne sync.Once
}

func New(spec Spec, deps Deps) *Tunnel {
	if deps.Clock == nil {
		deps.Clock = RealClock()
	}
	return &Tunnel{
		spec:    spec,
		deps:    deps,
		back:    NewBackoff(deps.Seed),
		state:   StateResolving,
		since:   deps.Clock.Now(),
		recycle: make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}
}

func (t *Tunnel) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Status{
		Spec: t.spec, State: t.state, Since: t.since,
		Retries: t.retries, LastError: t.lastErr, ContainerIP: t.containerIP,
	}
}

func (t *Tunnel) Events() []Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Event(nil), t.events...)
}

// Stop ends the tunnel for good.
func (t *Tunnel) Stop() { t.stopOne.Do(func() { close(t.stop) }) }

// Recycle tears the forward down and rebuilds it. The supervisor calls this
// after a system wake or a network change, when the existing connection is
// probably dead but ssh has not noticed yet.
func (t *Tunnel) Recycle() {
	select {
	case t.recycle <- struct{}{}:
	default: // one pending recycle is as good as two
	}
}

// MarkDegraded records that a liveness probe failed and queues a recycle, so
// a tunnel whose forward stopped accepting connections does not sit there
// reporting healthy. Called by `hop ls`, which is the only prober.
func (t *Tunnel) MarkDegraded(reason string) {
	t.transition(StateDegraded, reason)
	t.Recycle()
}

func (t *Tunnel) transition(s State, message string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.state = s
	t.since = t.deps.Clock.Now()
	if message != "" {
		t.lastErr = message
	}
	if s == StateHealthy {
		t.lastErr = ""
	}

	t.events = append(t.events, Event{At: t.deps.Clock.Now(), State: s, Message: message})
	if len(t.events) > eventRing {
		t.events = t.events[len(t.events)-eventRing:]
	}
}

// Run drives the state machine until ctx is cancelled or Stop is called.
func (t *Tunnel) Run(ctx context.Context) {
	defer t.deps.Exec.Close(context.Background())

	for {
		select {
		case <-ctx.Done():
			t.transition(StateStopped, "")
			return
		case <-t.stop:
			t.transition(StateStopped, "")
			return
		default:
		}

		class, err := t.attempt(ctx)
		if err == nil { // stopped cleanly
			return
		}

		if class.Fatal() {
			t.transition(StateFatal, err.Error())
			t.waitForRecycleOrStop(ctx)
			continue
		}

		t.mu.Lock()
		t.retries++
		if class == ClassUnknown {
			t.unknownRun++
		} else {
			t.unknownRun = 0
		}
		exhausted := t.unknownRun > unknownRetryCap
		t.mu.Unlock()

		if exhausted {
			t.transition(StateFatal,
				fmt.Sprintf("giving up after %d unclassified failures: %v", unknownRetryCap, err))
			t.waitForRecycleOrStop(ctx)
			continue
		}

		delay := t.back.Next()
		t.transition(StateRetrying, err.Error())

		select {
		case <-t.deps.Clock.After(delay):
		case <-t.recycle:
		case <-t.stop:
			t.transition(StateStopped, "")
			return
		case <-ctx.Done():
			t.transition(StateStopped, "")
			return
		}
	}
}

// attempt runs one full resolve-connect-watch cycle. It returns the failure
// class and error, or a nil error if the tunnel was stopped deliberately.
func (t *Tunnel) attempt(ctx context.Context) (Class, error) {
	// Resolve. This runs on EVERY attempt, not just the first: a container
	// that restarted has a new address, and reusing the old one is precisely
	// the bug this program exists to fix.
	t.transition(StateResolving, "")

	ip, err := t.deps.Exec.ContainerIP(ctx, t.spec.Host, t.spec.Container)
	if err != nil {
		class := Classify(err.Error())
		if class == ClassUnknown {
			// An inspect that failed without a recognisable reason is almost
			// always the container being absent or the host being unreachable,
			// both of which are worth retrying.
			class = ClassContainer
		}
		return class, err
	}

	t.mu.Lock()
	t.containerIP = ip
	t.mu.Unlock()

	// Check the local port before spawning ssh, so a conflict produces a
	// message naming the offender rather than raw ssh stderr.
	if !t.deps.Probe.PortFree(t.spec.LocalPort) {
		holder, herr := t.deps.Probe.PortHolder(t.spec.LocalPort)
		if herr != nil {
			return ClassLocalPort, fmt.Errorf("local port %d is already in use", t.spec.LocalPort)
		}
		return ClassLocalPort, fmt.Errorf("local port %d is held by %s (pid %d)",
			t.spec.LocalPort, holder.Command, holder.PID)
	}

	t.transition(StateConnecting, "")

	proc, err := t.deps.Exec.StartForward(ctx, sshexec.ForwardSpec{
		Host: t.spec.Host, RemoteAddr: ip,
		RemotePort: t.spec.RemotePort, LocalPort: t.spec.LocalPort,
	})
	if err != nil {
		return Classify(err.Error()), err
	}

	// Wait for the forward to actually exist before calling it healthy.
	//
	// This is a bind test, not a dial: once ssh has bound the local port,
	// PortFree returns false. Dialling would open and close a connection
	// against the database on every reconnect, which is exactly the log noise
	// the spec refuses to produce.
	if bound, err := t.waitForBind(ctx, proc); !bound {
		_ = proc.Terminate()
		return ClassNetwork, err
	}

	t.transition(StateHealthy, "")
	healthySince := t.deps.Clock.Mono()

	select {
	case <-proc.Done():
		if t.deps.Clock.Mono()-healthySince >= healthyResetAfter {
			t.back.Reset()
		}
		stderr := proc.Stderr()
		if stderr == "" {
			stderr = "ssh exited without output"
		}
		return Classify(stderr), fmt.Errorf("%s", stderr)

	case <-t.recycle:
		_ = proc.Terminate()
		t.back.Reset() // a recycle is not the tunnel's fault
		return ClassNetwork, fmt.Errorf("recycled")

	case <-t.stop:
		_ = proc.Terminate()
		t.transition(StateStopped, "")
		return ClassUnknown, nil

	case <-ctx.Done():
		_ = proc.Terminate()
		t.transition(StateStopped, "")
		return ClassUnknown, nil
	}
}

// waitForBind blocks until ssh has bound the local port, the process exits,
// or the deadline passes.
func (t *Tunnel) waitForBind(ctx context.Context, proc sshexec.Proc) (bool, error) {
	deadline := t.deps.Clock.Mono() + bindTimeout

	for {
		if !t.deps.Probe.PortFree(t.spec.LocalPort) {
			return true, nil
		}
		if t.deps.Clock.Mono() >= deadline {
			return false, fmt.Errorf(
				"ssh did not bind local port %d within %s", t.spec.LocalPort, bindTimeout)
		}

		select {
		case <-proc.Done():
			// Exited before binding: its stderr says why.
			stderr := proc.Stderr()
			if stderr == "" {
				stderr = "ssh exited before the forward was established"
			}
			return false, fmt.Errorf("%s", stderr)
		case <-t.deps.Clock.After(bindPollInterval):
		case <-t.recycle:
			return false, fmt.Errorf("recycled")
		case <-t.stop:
			return false, fmt.Errorf("stopped")
		case <-ctx.Done():
			return false, fmt.Errorf("stopped")
		}
	}
}

// waitForRecycleOrStop parks a fatal tunnel. It stays visible in `hop ls` with
// its reason, and a `hop restart` wakes it.
func (t *Tunnel) waitForRecycleOrStop(ctx context.Context) {
	select {
	case <-t.recycle:
		t.mu.Lock()
		t.unknownRun = 0
		t.mu.Unlock()
		t.back.Reset()
	case <-t.stop:
	case <-ctx.Done():
	}
}
