package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thailoc-dev/hop/internal/sshexec"
	"github.com/thailoc-dev/hop/internal/sysprobe"
)

func testSpec() Spec {
	return Spec{
		Host: "example-backend-dev", Container: "app_mongo_staging",
		RemotePort: 27017, LocalPort: 27018, Env: "stg",
	}
}

type harness struct {
	tun   *Tunnel
	exec  *sshexec.Fake
	probe *sysprobe.Fake
	clock *FakeClock
	stop  context.CancelFunc
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ex := sshexec.NewFake()
	ex.SetContainerIP("example-backend-dev", "app_mongo_staging", "172.18.0.4")
	pr := sysprobe.NewFake()
	bindsOnStart(ex, pr)
	clk := NewFakeClock(time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC))

	tun := New(testSpec(), Deps{Exec: ex, Probe: pr, Clock: clk, Seed: 1})

	ctx, cancel := context.WithCancel(context.Background())
	go tun.Run(ctx)
	t.Cleanup(cancel)

	return &harness{tun: tun, exec: ex, probe: pr, clock: clk, stop: cancel}
}

func (h *harness) waitState(t *testing.T, want State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.tun.Status().State == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("state = %v, want %v", h.tun.Status().State, want)
}

// waitForwards blocks until n forwards have been started.
//
// Killing h.exec.LastForward() without this races the state machine: until
// the replacement forward exists, LastForward still returns the dead one and
// the kill is silently a no-op.
func (h *harness) waitForwards(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.exec.Forwards()) >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("started %d forwards, want %d", len(h.exec.Forwards()), n)
}

// advanceUntil pushes the fake clock forward repeatedly until cond holds.
//
// A single Advance is not enough: the machine computes its delay, transitions
// to retrying, and only then calls Clock.After. An Advance landing in that
// window registers a timer that is already in the past and never fires.
//
// cond must test something monotonic, such as a forward count. Testing for a
// transient state does not work — the machine may not have reached it yet
// when cond is first evaluated, and may have left it by the next poll.
func (h *harness) advanceUntil(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		h.clock.Advance(31 * time.Second)
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (state = %v)", what, h.tun.Status().State)
}

func TestReachesHealthyAndForwardsTheResolvedAddress(t *testing.T) {
	h := newHarness(t)

	h.waitState(t, StateHealthy)

	forwards := h.exec.Forwards()
	if len(forwards) != 1 {
		t.Fatalf("started %d forwards, want 1", len(forwards))
	}
	if forwards[0].RemoteAddr != "172.18.0.4" {
		t.Fatalf("RemoteAddr = %q", forwards[0].RemoteAddr)
	}
	if forwards[0].LocalPort != 27018 || forwards[0].RemotePort != 27017 {
		t.Fatalf("ports = %d -> %d", forwards[0].LocalPort, forwards[0].RemotePort)
	}
}

// The regression test for the bug that motivates the project.
func TestContainerRestartOnANewAddressIsPickedUp(t *testing.T) {
	h := newHarness(t)
	h.waitState(t, StateHealthy)

	h.exec.SetContainerIP("example-backend-dev", "app_mongo_staging", "172.18.0.99")
	h.exec.LastForward().EmitStderr("Connection closed by remote host")
	h.exec.LastForward().Die()

	h.waitState(t, StateRetrying)
	h.clock.Advance(2 * time.Second)
	h.waitState(t, StateHealthy)

	forwards := h.exec.Forwards()
	if len(forwards) != 2 {
		t.Fatalf("started %d forwards, want 2", len(forwards))
	}
	if forwards[1].RemoteAddr != "172.18.0.99" {
		t.Fatalf("second attempt used %q; the address was not re-resolved",
			forwards[1].RemoteAddr)
	}
}

func TestAuthFailureIsFatalOnTheFirstAttempt(t *testing.T) {
	h := newHarness(t)
	h.waitState(t, StateHealthy)

	h.exec.LastForward().EmitStderr("loc@host: Permission denied (publickey).")
	h.exec.LastForward().Die()

	h.waitState(t, StateFatal)

	h.clock.Advance(time.Minute)
	if got := len(h.exec.Forwards()); got != 1 {
		t.Fatalf("retried a fatal error: %d forwards", got)
	}
	if msg := h.tun.Status().LastError; msg == "" {
		t.Fatal("fatal state carries no explanation")
	}
}

func TestBusyLocalPortFailsFastAndNamesTheHolder(t *testing.T) {
	ex := sshexec.NewFake()
	ex.SetContainerIP("h", "c", "10.0.0.1")
	pr := sysprobe.NewFake()
	pr.SetPortBusy(27018, sysprobe.Holder{Command: "mongod", PID: 4242})
	clk := NewFakeClock(time.Now())

	tun := New(Spec{Host: "h", Container: "c", RemotePort: 27017, LocalPort: 27018},
		Deps{Exec: ex, Probe: pr, Clock: clk, Seed: 1})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tun.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && tun.Status().State != StateFatal {
		time.Sleep(time.Millisecond)
	}

	status := tun.Status()
	if status.State != StateFatal {
		t.Fatalf("state = %v, want fatal", status.State)
	}
	if !strings.Contains(status.LastError, "mongod") || !strings.Contains(status.LastError, "4242") {
		t.Fatalf("error does not name the holder: %q", status.LastError)
	}
	if len(ex.Forwards()) != 0 {
		t.Fatal("spawned ssh despite the port being busy")
	}
}

func TestMissingContainerRetriesIndefinitely(t *testing.T) {
	ex := sshexec.NewFake()
	ex.SetContainerErr("h", "c", errors.New("Error: No such object: c"))
	pr := sysprobe.NewFake()
	bindsOnStart(ex, pr)
	clk := NewFakeClock(time.Now())

	tun := New(Spec{Host: "h", Container: "c", RemotePort: 1, LocalPort: 2},
		Deps{Exec: ex, Probe: pr, Clock: clk, Seed: 1})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tun.Run(ctx)

	for i := 0; i < 20; i++ {
		clk.Advance(31 * time.Second)
		time.Sleep(time.Millisecond)
	}

	if got := tun.Status().State; got == StateFatal {
		t.Fatal("gave up on a missing container; an image pull can take minutes")
	}

	ex.SetContainerIP("h", "c", "10.0.0.5")
	clk.Advance(31 * time.Second)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && tun.Status().State != StateHealthy {
		clk.Advance(time.Second)
		time.Sleep(time.Millisecond)
	}
	if got := tun.Status().State; got != StateHealthy {
		t.Fatalf("state = %v, want healthy once the container returned", got)
	}
}

func TestUnknownErrorsGiveUpAfterTheCap(t *testing.T) {
	h := newHarness(t)
	h.waitState(t, StateHealthy)

	// The cap is on consecutive unclassified failures, so each iteration must
	// actually produce one: wait for the replacement forward before killing it.
	for i := 0; i < unknownRetryCap+1; i++ {
		h.waitForwards(t, i+1)

		proc := h.exec.LastForward()
		proc.EmitStderr("something nobody has ever seen")
		proc.Die()

		// Advance until either the replacement forward starts or the cap is
		// hit. On the final failure no replacement comes: the tunnel goes
		// fatal instead, which is the behaviour under test.
		want := i + 2
		h.advanceUntil(t, func() bool {
			return len(h.exec.Forwards()) >= want || h.tun.Status().State == StateFatal
		}, "the next attempt or the cap")

		if h.tun.Status().State == StateFatal {
			break
		}
	}

	h.waitState(t, StateFatal)
}

func TestRecycleRestartsAHealthyTunnel(t *testing.T) {
	h := newHarness(t)
	h.waitState(t, StateHealthy)
	first := h.exec.LastForward()

	h.tun.Recycle()

	h.waitState(t, StateRetrying)
	h.clock.Advance(2 * time.Second)
	h.waitState(t, StateHealthy)

	if first.TerminateCount() == 0 {
		t.Fatal("Recycle did not terminate the previous forward")
	}
	if got := len(h.exec.Forwards()); got != 2 {
		t.Fatalf("started %d forwards, want 2", got)
	}
}

func TestStopTerminatesTheForward(t *testing.T) {
	h := newHarness(t)
	h.waitState(t, StateHealthy)
	proc := h.exec.LastForward()

	h.tun.Stop()

	h.waitState(t, StateStopped)
	if proc.TerminateCount() == 0 {
		t.Fatal("Stop did not terminate the forward")
	}
}

func TestEventsRecordEveryTransition(t *testing.T) {
	h := newHarness(t)
	h.waitState(t, StateHealthy)

	events := h.tun.Events()
	if len(events) < 3 {
		t.Fatalf("got %d events, want at least resolving/connecting/healthy", len(events))
	}
	if events[0].State != StateResolving {
		t.Fatalf("first event = %v, want resolving", events[0].State)
	}
	if events[len(events)-1].State != StateHealthy {
		t.Fatalf("last event = %v, want healthy", events[len(events)-1].State)
	}
}

// bindsOnStart makes the fake pair behave like real ssh: the local port is
// free until the forward starts, then bound.
func bindsOnStart(ex *sshexec.Fake, pr *sysprobe.Fake) {
	ex.SetOnStartForward(func(spec sshexec.ForwardSpec) {
		pr.SetPortBusy(spec.LocalPort, sysprobe.Holder{Command: "ssh", PID: 1})

		// ...and releases it when the process exits, so the next attempt's
		// pre-flight check sees a free port rather than a conflict.
		proc := ex.LastForward()
		go func() {
			<-proc.Done()
			pr.SetPortFree(spec.LocalPort)
		}()
	})
}

// The regression test for a tunnel that reports healthy before ssh has bound
// the local port: `hop ls` then probes a port nothing is listening on, marks
// the tunnel degraded and recycles it, forever.
func TestHealthyWaitsForTheLocalPortToBind(t *testing.T) {
	ex := sshexec.NewFake()
	ex.SetContainerIP("h", "c", "172.18.0.4")
	pr := sysprobe.NewFake()
	clk := NewFakeClock(time.Now())

	// Deliberately do NOT bind on start: ssh is still connecting.
	tun := New(Spec{Host: "h", Container: "c", RemotePort: 27017, LocalPort: 27018},
		Deps{Exec: ex, Probe: pr, Clock: clk, Seed: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tun.Run(ctx)

	// Wait for the forward to have been started.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(ex.Forwards()) == 0 {
		time.Sleep(time.Millisecond)
	}

	// The port is not bound, so the tunnel must not claim to be healthy.
	time.Sleep(30 * time.Millisecond)
	if got := tun.Status().State; got == StateHealthy {
		t.Fatal("reported healthy while the local port was still unbound; " +
			"the ls probe would immediately mark this degraded and recycle it")
	}

	// Once ssh binds the port, it becomes healthy.
	pr.SetPortBusy(27018, sysprobe.Holder{Command: "ssh", PID: 1})
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && tun.Status().State != StateHealthy {
		clk.Advance(bindPollInterval)
		time.Sleep(time.Millisecond)
	}
	if got := tun.Status().State; got != StateHealthy {
		t.Fatalf("state = %v, want healthy once the port was bound", got)
	}
}

func TestForwardThatNeverBindsIsRetried(t *testing.T) {
	ex := sshexec.NewFake()
	ex.SetContainerIP("h", "c", "172.18.0.4")
	pr := sysprobe.NewFake()
	clk := NewFakeClock(time.Now())

	tun := New(Spec{Host: "h", Container: "c", RemotePort: 27017, LocalPort: 27018},
		Deps{Exec: ex, Probe: pr, Clock: clk, Seed: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tun.Run(ctx)

	// Push past the bind deadline without ever binding.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(ex.Forwards()) < 2 {
		clk.Advance(bindPollInterval)
		time.Sleep(time.Millisecond)
	}

	if len(ex.Forwards()) < 2 {
		t.Fatal("a forward that never bound was never retried")
	}
	if first := ex.LastForward(); first == nil {
		t.Fatal("no forward recorded")
	}
}

func TestCreatedAtIsSetOnceAndSurvivesTransitions(t *testing.T) {
	h := newHarness(t)
	h.waitState(t, StateHealthy)
	created := h.tun.Status().CreatedAt
	if created.IsZero() {
		t.Fatal("CreatedAt is zero")
	}

	// A reconnect moves Since but must not move CreatedAt: `hop save` picks
	// the most recently OPENED tunnel, and a reconnect is not an open.
	h.clock.Advance(time.Minute)
	h.exec.LastForward().EmitStderr("Connection closed by remote host")
	h.exec.LastForward().Die()
	h.waitState(t, StateRetrying)

	if got := h.tun.Status().CreatedAt; !got.Equal(created) {
		t.Fatalf("CreatedAt moved from %v to %v on a state transition", created, got)
	}
	if h.tun.Status().Since.Equal(created) {
		t.Fatal("Since did not move; the test premise is wrong")
	}
}

func TestSpecNameRoundTripsThroughJSON(t *testing.T) {
	in := Spec{Host: "h", Container: "c", RemotePort: 1, LocalPort: 2, Name: "redis-stg"}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Spec
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Name != "redis-stg" {
		t.Fatalf("Name = %q", out.Name)
	}
	// Unnamed specs must not grow a "name": "" field, so existing state files
	// stay byte-identical when nothing was named.
	data, _ = json.Marshal(Spec{Host: "h"})
	if strings.Contains(string(data), `"name"`) {
		t.Fatalf("empty name serialised: %s", data)
	}
}
