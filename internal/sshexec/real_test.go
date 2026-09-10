package sshexec

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestForwardArgsCarryTheKeepalivesAndFailFast(t *testing.T) {
	args := forwardArgs(ForwardSpec{
		Host: "example-backend-dev", RemoteAddr: "172.18.0.4",
		RemotePort: 27017, LocalPort: 27018,
	})

	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-N",
		"-L 127.0.0.1:27018:172.18.0.4:27017",
		"-o ServerAliveInterval=15",
		"-o ServerAliveCountMax=3",
		"-o ExitOnForwardFailure=yes",
		"example-backend-dev",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("forward args %q missing %q", joined, want)
		}
	}
	if last := args[len(args)-1]; last != "example-backend-dev" {
		t.Fatalf("host must be the final argument, got %q", last)
	}
}

func TestForwardBindsLoopbackOnly(t *testing.T) {
	// Binding 0.0.0.0 would expose a production database to the local network.
	args := forwardArgs(ForwardSpec{Host: "h", RemoteAddr: "10.0.0.2", RemotePort: 5432, LocalPort: 5433})

	i := slices.Index(args, "-L")
	if i < 0 || i+1 >= len(args) {
		t.Fatal("no -L argument")
	}
	if got := args[i+1]; !strings.HasPrefix(got, "127.0.0.1:") {
		t.Fatalf("-L %q must bind 127.0.0.1 explicitly", got)
	}
}

func TestCommandArgsMultiplexThroughTheSocketDir(t *testing.T) {
	args := commandArgs("/tmp/hop-501", "host-a")

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-o ControlMaster=auto") {
		t.Fatalf("missing ControlMaster: %q", joined)
	}
	if !strings.Contains(joined, "-o ControlPersist=60") {
		t.Fatalf("missing ControlPersist: %q", joined)
	}
	if !strings.Contains(joined, "ControlPath=/tmp/hop-501/cmd-") {
		t.Fatalf("ControlPath not in the socket dir: %q", joined)
	}
}

func TestCommandArgsSocketNameIsStablePerHost(t *testing.T) {
	a := strings.Join(commandArgs("/tmp/d", "host-a"), " ")
	b := strings.Join(commandArgs("/tmp/d", "host-a"), " ")
	c := strings.Join(commandArgs("/tmp/d", "host-b"), " ")

	if a != b {
		t.Fatal("same host produced two different control paths")
	}
	if a == c {
		t.Fatal("different hosts shared a control path")
	}
}

// withStubSSH puts a fake ssh first on PATH and returns the file it logs
// its argv to.
func withStubSSH(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	argvLog := filepath.Join(dir, "argv.txt")

	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + argvLog + "\n" + script
	path := filepath.Join(dir, "ssh")
	if err := os.WriteFile(path, []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argvLog
}

func TestContainerIPTrimsWhitespace(t *testing.T) {
	withStubSSH(t, "echo '  172.18.0.4  '\n")
	e := New(t.TempDir())

	got, err := e.ContainerIP(context.Background(), "h", "mongo")
	if err != nil {
		t.Fatalf("ContainerIP: %v", err)
	}
	if got != "172.18.0.4" {
		t.Fatalf("got %q, want 172.18.0.4", got)
	}
}

func TestContainerIPRejectsEmptyOutput(t *testing.T) {
	withStubSSH(t, "echo ''\n")
	e := New(t.TempDir())

	_, err := e.ContainerIP(context.Background(), "h", "mongo")
	if err == nil {
		t.Fatal("want an error for an address-less container")
	}
	if !strings.Contains(err.Error(), "no network address") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

func TestStartForwardCapturesStderrAndClosesDone(t *testing.T) {
	withStubSSH(t, "echo 'Permission denied (publickey).' >&2\nexit 255\n")
	e := New(t.TempDir())

	proc, err := e.StartForward(context.Background(), ForwardSpec{
		Host: "h", RemoteAddr: "1.2.3.4", RemotePort: 1, LocalPort: 2,
	})
	if err != nil {
		t.Fatalf("StartForward: %v", err)
	}

	select {
	case <-proc.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done never closed")
	}
	if got := proc.Stderr(); !strings.Contains(got, "Permission denied") {
		t.Fatalf("Stderr() = %q", got)
	}
}

func TestStartForwardPassesTheResolvedAddress(t *testing.T) {
	argvLog := withStubSSH(t, "sleep 30\n")
	e := New(t.TempDir())

	proc, err := e.StartForward(context.Background(), ForwardSpec{
		Host: "h", RemoteAddr: "172.18.0.9", RemotePort: 27017, LocalPort: 27018,
	})
	if err != nil {
		t.Fatalf("StartForward: %v", err)
	}
	t.Cleanup(func() { _ = proc.Terminate() })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(argvLog); err == nil && strings.Contains(string(b), "172.18.0.9") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("stub ssh never saw the resolved address")
}

func TestTerminateStopsAStubbornProcess(t *testing.T) {
	withStubSSH(t, "trap '' TERM\nsleep 30\n")
	e := New(t.TempDir())

	proc, err := e.StartForward(context.Background(), ForwardSpec{Host: "h", RemoteAddr: "1.2.3.4", RemotePort: 1, LocalPort: 2})
	if err != nil {
		t.Fatalf("StartForward: %v", err)
	}

	start := time.Now()
	if err := proc.Terminate(); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	select {
	case <-proc.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("a process ignoring SIGTERM was never killed")
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("escalation to SIGKILL took %v", elapsed)
	}
}
