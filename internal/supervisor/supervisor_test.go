package supervisor

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/thailoc-dev/hop/internal/control"
	"github.com/thailoc-dev/hop/internal/sshexec"
	"github.com/thailoc-dev/hop/internal/store"
	"github.com/thailoc-dev/hop/internal/sysprobe"
	"github.com/thailoc-dev/hop/internal/tunnel"
)

// bindsOnStart makes the fake pair behave like real ssh: the local port is
// free until a forward starts, bound while it runs, and free again once it
// exits. Without it nothing ever reaches healthy, because a tunnel now waits
// for the port to be bound before saying so.
func bindsOnStart(ex *sshexec.Fake, pr *sysprobe.Fake) {
	ex.SetOnStartForward(func(spec sshexec.ForwardSpec) {
		pr.SetPortBusy(spec.LocalPort, sysprobe.Holder{Command: "ssh", PID: 1})
		proc := ex.LastForward()
		go func() {
			<-proc.Done()
			pr.SetPortFree(spec.LocalPort)
		}()
	})
}

func newSup(t *testing.T) (*Supervisor, *sshexec.Fake, *tunnel.FakeClock, string) {
	t.Helper()
	ex := sshexec.NewFake()
	ex.SetContainerIP("h", "c", "172.18.0.4")
	pr := sysprobe.NewFake()
	bindsOnStart(ex, pr)
	clk := tunnel.NewFakeClock(time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC))
	statePath := filepath.Join(t.TempDir(), "state.json")

	s := New(Config{StatePath: statePath, Exec: ex, Probe: pr, Clock: clk})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Run(ctx) }()
	t.Cleanup(cancel)

	return s, ex, clk, statePath
}

func addSpec(port int) control.Request {
	return control.Request{Op: control.OpAdd, Spec: &tunnel.Spec{
		Host: "h", Container: "c", RemotePort: 27017, LocalPort: port, Env: "stg",
	}}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAddStartsATunnelAndPersistsIt(t *testing.T) {
	s, _, _, statePath := newSup(t)

	resp := s.Handle(addSpec(27018))
	if !resp.OK {
		t.Fatalf("add rejected: %s", resp.Error)
	}

	waitFor(t, func() bool {
		list := s.Handle(control.Request{Op: control.OpList})
		return len(list.Statuses) == 1 && list.Statuses[0].State == tunnel.StateHealthy
	}, "the tunnel to become healthy")

	saved, err := store.Load(statePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(saved.Tunnels) != 1 || saved.Tunnels[0].LocalPort != 27018 {
		t.Fatalf("state file = %+v", saved.Tunnels)
	}
}

func TestAddRejectsADuplicateLocalPort(t *testing.T) {
	s, _, _, _ := newSup(t)
	s.Handle(addSpec(27018))

	resp := s.Handle(addSpec(27018))

	if resp.OK {
		t.Fatal("a second tunnel claimed the same local port")
	}
	if resp.Error == "" {
		t.Fatal("duplicate rejection carries no explanation")
	}
}

func TestRemoveStopsTheTunnelAndPersists(t *testing.T) {
	s, ex, _, statePath := newSup(t)
	s.Handle(addSpec(27018))
	waitFor(t, func() bool { return ex.LastForward() != nil }, "a forward to start")
	proc := ex.LastForward()

	resp := s.Handle(control.Request{Op: control.OpRemove, LocalPort: 27018})
	if !resp.OK {
		t.Fatalf("remove rejected: %s", resp.Error)
	}

	waitFor(t, func() bool { return proc.TerminateCount() > 0 }, "the forward to be terminated")

	saved, _ := store.Load(statePath)
	if len(saved.Tunnels) != 0 {
		t.Fatalf("state file still holds %+v", saved.Tunnels)
	}
}

func TestRemoveUnknownPortIsRejected(t *testing.T) {
	s, _, _, _ := newSup(t)

	resp := s.Handle(control.Request{Op: control.OpRemove, LocalPort: 9999})

	if resp.OK {
		t.Fatal("removing a port with no tunnel reported success")
	}
}

func TestRemoveAllStopsEverything(t *testing.T) {
	s, _, _, _ := newSup(t)
	s.Handle(addSpec(27018))
	s.Handle(addSpec(6380))

	resp := s.Handle(control.Request{Op: control.OpRemove, All: true})
	if !resp.OK {
		t.Fatalf("remove --all rejected: %s", resp.Error)
	}

	list := s.Handle(control.Request{Op: control.OpList})
	if len(list.Statuses) != 0 {
		t.Fatalf("%d tunnels survived remove --all", len(list.Statuses))
	}
}

func TestEmptyClosesWhenTheLastTunnelGoes(t *testing.T) {
	s, _, _, _ := newSup(t)
	s.Handle(addSpec(27018))

	select {
	case <-s.Empty():
		t.Fatal("Empty fired while a tunnel was running")
	default:
	}

	s.Handle(control.Request{Op: control.OpRemove, LocalPort: 27018})

	select {
	case <-s.Empty():
	case <-time.After(2 * time.Second):
		t.Fatal("Empty never fired after the last tunnel was removed")
	}
}

func TestListIsSortedByLocalPort(t *testing.T) {
	s, _, _, _ := newSup(t)
	s.Handle(addSpec(27018))
	s.Handle(addSpec(6380))
	s.Handle(addSpec(15432))

	list := s.Handle(control.Request{Op: control.OpList})

	if len(list.Statuses) != 3 {
		t.Fatalf("got %d statuses", len(list.Statuses))
	}
	for i := 1; i < len(list.Statuses); i++ {
		if list.Statuses[i-1].Spec.LocalPort > list.Statuses[i].Spec.LocalPort {
			t.Fatalf("unsorted: %d before %d",
				list.Statuses[i-1].Spec.LocalPort, list.Statuses[i].Spec.LocalPort)
		}
	}
}

func TestRestartRecyclesWithoutLosingTheTunnel(t *testing.T) {
	s, ex, clk, _ := newSup(t)
	s.Handle(addSpec(27018))
	waitFor(t, func() bool { return ex.LastForward() != nil }, "a forward to start")
	first := ex.LastForward()

	resp := s.Handle(control.Request{Op: control.OpRestart, LocalPort: 27018})
	if !resp.OK {
		t.Fatalf("restart rejected: %s", resp.Error)
	}

	waitFor(t, func() bool { return first.TerminateCount() > 0 }, "the old forward to stop")
	waitFor(t, func() bool {
		clk.Advance(2 * time.Second)
		return len(ex.Forwards()) == 2
	}, "a replacement forward")

	list := s.Handle(control.Request{Op: control.OpList})
	if len(list.Statuses) != 1 {
		t.Fatalf("restart changed the tunnel count to %d", len(list.Statuses))
	}
}

func TestRunRestoresTunnelsFromState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := store.Save(statePath, store.File{Version: 1, Tunnels: []tunnel.Spec{
		{Host: "h", Container: "c", RemotePort: 27017, LocalPort: 27018},
	}}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ex := sshexec.NewFake()
	ex.SetContainerIP("h", "c", "10.0.0.1")
	pr := sysprobe.NewFake()
	bindsOnStart(ex, pr)
	s := New(Config{
		StatePath: statePath, Exec: ex, Probe: pr,
		Clock: tunnel.NewFakeClock(time.Now()),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	waitFor(t, func() bool {
		return len(s.Handle(control.Request{Op: control.OpList}).Statuses) == 1
	}, "the persisted tunnel to be restored")
}

func TestRunReapsOrphanedSSHHoldingOurPorts(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := store.Save(statePath, store.File{Version: 1, Tunnels: []tunnel.Spec{
		{Host: "h", Container: "c", RemotePort: 27017, LocalPort: 27018},
	}}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ex := sshexec.NewFake()
	ex.SetContainerIP("h", "c", "10.0.0.1")
	pr := sysprobe.NewFake()
	// An ssh left behind by a daemon that was killed.
	pr.SetPortBusy(27018, sysprobe.Holder{Command: "ssh", PID: 4242})

	s := New(Config{StatePath: statePath, Exec: ex, Probe: pr, Clock: tunnel.NewFakeClock(time.Now())})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	waitFor(t, func() bool {
		for _, pid := range pr.Killed() {
			if pid == 4242 {
				return true
			}
		}
		return false
	}, "the orphaned ssh to be killed")
}

func TestRunLeavesUnrelatedListenersAlone(t *testing.T) {
	// A local mongod on the port must never be killed: hop did not start it.
	statePath := filepath.Join(t.TempDir(), "state.json")
	_ = store.Save(statePath, store.File{Version: 1, Tunnels: []tunnel.Spec{
		{Host: "h", Container: "c", RemotePort: 27017, LocalPort: 27018},
	}})

	ex := sshexec.NewFake()
	ex.SetContainerIP("h", "c", "10.0.0.1")
	pr := sysprobe.NewFake()
	pr.SetPortBusy(27018, sysprobe.Holder{Command: "mongod", PID: 777})

	s := New(Config{StatePath: statePath, Exec: ex, Probe: pr, Clock: tunnel.NewFakeClock(time.Now())})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	if len(pr.Killed()) != 0 {
		t.Fatalf("killed %v; only orphaned ssh may be reaped", pr.Killed())
	}
}

func TestEventsReturnsPerTunnelHistory(t *testing.T) {
	s, _, _, _ := newSup(t)
	s.Handle(addSpec(27018))
	waitFor(t, func() bool {
		return len(s.Handle(control.Request{Op: control.OpEvents, LocalPort: 27018}).Events) > 0
	}, "events to accumulate")

	resp := s.Handle(control.Request{Op: control.OpEvents, LocalPort: 27018})
	if !resp.OK {
		t.Fatalf("events rejected: %s", resp.Error)
	}
	if resp.Events[0].State != tunnel.StateResolving {
		t.Fatalf("first event = %v, want resolving", resp.Events[0].State)
	}
}
