package supervisor

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/thailoc-dev/hop/internal/sshexec"
	"github.com/thailoc-dev/hop/internal/sysprobe"
	"github.com/thailoc-dev/hop/internal/tunnel"
)

func newWatchSup(t *testing.T) (*Supervisor, *sshexec.Fake, *sysprobe.Fake, *tunnel.FakeClock) {
	t.Helper()
	ex := sshexec.NewFake()
	ex.SetContainerIP("h", "c", "172.18.0.4")
	pr := sysprobe.NewFake()
	bindsOnStart(ex, pr)
	pr.SetDefaultRoute("192.168.1.1")
	clk := tunnel.NewFakeClock(time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC))

	s := New(Config{
		StatePath: filepath.Join(t.TempDir(), "state.json"),
		Exec:      ex, Probe: pr, Clock: clk,
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Run(ctx) }()
	t.Cleanup(cancel)

	s.Handle(addSpec(27018))
	waitFor(t, func() bool { return ex.LastForward() != nil }, "a forward to start")
	return s, ex, pr, clk
}

// tick advances the clock repeatedly until cond holds, so a watch iteration
// that has not yet registered its timer cannot be missed.
func tick(t *testing.T, clk *tunnel.FakeClock, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		clk.Advance(WatchInterval)
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSystemSleepRecyclesEveryTunnel(t *testing.T) {
	_, ex, _, clk := newWatchSup(t)
	first := ex.LastForward()

	// The lid closes for two hours: wall time jumps, monotonic time does not.
	clk.Sleep(2 * time.Hour)

	tick(t, clk, func() bool { return first.TerminateCount() > 0 },
		"the stale forward to be recycled after sleep")
}

func TestOrdinaryElapsedTimeDoesNotRecycle(t *testing.T) {
	_, ex, _, clk := newWatchSup(t)
	first := ex.LastForward()

	// Ten minutes of ordinary running: wall and monotonic advance together.
	for i := 0; i < 60; i++ {
		clk.Advance(WatchInterval)
		time.Sleep(time.Millisecond)
	}

	if first.TerminateCount() > 0 {
		t.Fatal("recycled a healthy tunnel that merely kept running")
	}
}

func TestNetworkChangeRecyclesEveryTunnel(t *testing.T) {
	_, ex, pr, clk := newWatchSup(t)
	first := ex.LastForward()

	pr.SetDefaultRoute("10.55.0.1") // tethered to a phone

	tick(t, clk, func() bool { return first.TerminateCount() > 0 },
		"the tunnel to be recycled after the default route changed")
}

func TestUnchangedRouteDoesNotRecycle(t *testing.T) {
	_, ex, pr, clk := newWatchSup(t)
	first := ex.LastForward()

	pr.SetDefaultRoute("192.168.1.1") // same as before
	for i := 0; i < 10; i++ {
		clk.Advance(WatchInterval)
		time.Sleep(time.Millisecond)
	}

	if first.TerminateCount() > 0 {
		t.Fatal("recycled on an unchanged default route")
	}
}

func TestGoingOfflineDoesNotRecycleRepeatedly(t *testing.T) {
	// Losing the route entirely is a change, but it must settle rather than
	// recycling on every tick while the machine stays offline.
	_, ex, pr, clk := newWatchSup(t)
	first := ex.LastForward()

	pr.SetDefaultRoute("")
	tick(t, clk, func() bool { return first.TerminateCount() > 0 }, "the first recycle")

	before := len(ex.Forwards())
	for i := 0; i < 10; i++ {
		clk.Advance(WatchInterval)
		time.Sleep(time.Millisecond)
	}
	after := len(ex.Forwards())

	if after > before+1 {
		t.Fatalf("started %d extra forwards while offline", after-before)
	}
}

func TestWatchStopsWithTheSupervisor(t *testing.T) {
	ex := sshexec.NewFake()
	ex.SetContainerIP("h", "c", "1.2.3.4")
	pr := sysprobe.NewFake()
	bindsOnStart(ex, pr)
	clk := tunnel.NewFakeClock(time.Now())
	s := New(Config{StatePath: filepath.Join(t.TempDir(), "state.json"), Exec: ex, Probe: pr, Clock: clk})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Run(ctx); close(done) }()
	s.Handle(addSpec(27018))

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
