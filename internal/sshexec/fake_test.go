package sshexec

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFakeContainerIPReturnsConfiguredValue(t *testing.T) {
	f := NewFake()
	f.SetContainerIP("host-a", "mongo", "172.18.0.4")

	got, err := f.ContainerIP(context.Background(), "host-a", "mongo")
	if err != nil {
		t.Fatalf("ContainerIP: %v", err)
	}
	if got != "172.18.0.4" {
		t.Fatalf("got %q, want 172.18.0.4", got)
	}
}

func TestFakeContainerIPReturnsConfiguredError(t *testing.T) {
	f := NewFake()
	sentinel := errors.New("No such object: mongo")
	f.SetContainerErr("host-a", "mongo", sentinel)

	if _, err := f.ContainerIP(context.Background(), "host-a", "mongo"); !errors.Is(err, sentinel) {
		t.Fatalf("got %v, want %v", err, sentinel)
	}
}

func TestFakeContainerIPChangesBetweenCalls(t *testing.T) {
	// This is the restart-to-a-new-address case the whole project exists for.
	f := NewFake()
	f.SetContainerIP("h", "c", "172.18.0.4")
	first, _ := f.ContainerIP(context.Background(), "h", "c")

	f.SetContainerIP("h", "c", "172.18.0.9")
	second, _ := f.ContainerIP(context.Background(), "h", "c")

	if first == second {
		t.Fatal("fake did not observe the reconfigured address")
	}
}

func TestFakeStartForwardRecordsSpec(t *testing.T) {
	f := NewFake()
	spec := ForwardSpec{Host: "h", RemoteAddr: "172.18.0.4", RemotePort: 27017, LocalPort: 27018}

	if _, err := f.StartForward(context.Background(), spec); err != nil {
		t.Fatalf("StartForward: %v", err)
	}

	got := f.Forwards()
	if len(got) != 1 || got[0] != spec {
		t.Fatalf("Forwards() = %+v, want exactly %+v", got, spec)
	}
}

func TestFakeProcDieClosesDone(t *testing.T) {
	f := NewFake()
	proc, _ := f.StartForward(context.Background(), ForwardSpec{Host: "h", LocalPort: 1})

	select {
	case <-proc.Done():
		t.Fatal("Done closed before the process died")
	default:
	}

	f.LastForward().EmitStderr("Connection closed by remote host")
	f.LastForward().Die()

	select {
	case <-proc.Done():
	case <-time.After(time.Second):
		t.Fatal("Done did not close after Die")
	}
	if got := proc.Stderr(); got != "Connection closed by remote host" {
		t.Fatalf("Stderr() = %q", got)
	}
}

func TestFakeProcTerminateIsCounted(t *testing.T) {
	f := NewFake()
	proc, _ := f.StartForward(context.Background(), ForwardSpec{Host: "h", LocalPort: 1})

	if err := proc.Terminate(); err != nil {
		t.Fatalf("Terminate: %v", err)
	}

	if got := f.LastForward().TerminateCount(); got != 1 {
		t.Fatalf("TerminateCount() = %d, want 1", got)
	}
	select {
	case <-proc.Done():
	case <-time.After(time.Second):
		t.Fatal("Terminate did not close Done")
	}
}
