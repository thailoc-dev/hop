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

// shortTempDir returns a temp directory with a short path.
//
// ControlMaster sockets live in these directories, and t.TempDir() embeds the
// test name — long enough here to blow ssh's 104-byte ControlPath limit.
// Anything used as a socket directory must come from this helper.
func shortTempDir(t *testing.T) string {
	t.Helper()
	// "/tmp" explicitly, not TMPDIR: on macOS TMPDIR is itself ~48 bytes,
	// which leaves too little of the 104-byte sun_path budget for a socket
	// name plus the 17-byte suffix ssh appends while binding a ControlMaster.
	dir, err := os.MkdirTemp("/tmp", "hop")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
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
	e := New(shortTempDir(t))

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
	e := New(shortTempDir(t))

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
	e := New(shortTempDir(t))

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
	e := New(shortTempDir(t))

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
	e := New(shortTempDir(t))

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

func TestShellQuoteProtectsRemoteArguments(t *testing.T) {
	// ssh joins its argv with spaces and the REMOTE shell re-parses the result.
	// Anything with a space, backslash or quote must survive that second pass
	// intact, or docker receives arguments nobody wrote.
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "docker", "docker"},
		{"flag", "--format", "--format"},
		{"container name", "app_mongo_staging", "app_mongo_staging"},
		{"backslash-t", `{{.Names}}\t{{.Ports}}`, `'{{.Names}}\t{{.Ports}}'`},
		{"embedded space", "{{.IPAddress}} {{end}}", `'{{.IPAddress}} {{end}}'`},
		{"single quote", "it's", `'it'\''s'`},
		{"empty", "", "''"},
		{"semicolon", "a;rm -rf /", `'a;rm -rf /'`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shellQuote(tc.in); got != tc.want {
				t.Fatalf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRemoteCommandKeepsTheDockerPSFormatIntact(t *testing.T) {
	got := remoteCommand([]string{"docker", "ps", "--format", `{{.Names}}\t{{.Ports}}`})

	want := `docker ps --format '{{.Names}}\t{{.Ports}}'`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRemoteCommandKeepsTheInspectFormatInOnePiece(t *testing.T) {
	// This format contains a space. Unquoted, the remote shell splits it and
	// docker reports "template parsing error: unexpected EOF" — which is how
	// every tunnel failed to resolve its container address.
	got := remoteCommand([]string{
		"docker", "inspect", "-f",
		`{{range.NetworkSettings.Networks}}{{.IPAddress}} {{end}}`,
		"app_mongo_staging",
	})

	want := `docker inspect -f '{{range.NetworkSettings.Networks}}{{.IPAddress}} {{end}}' app_mongo_staging`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRunSendsTheRemoteCommandAsASingleArgument(t *testing.T) {
	// ssh must receive one argument holding the whole quoted command, not a
	// word per token: only then is the remote shell's re-parse harmless.
	argvLog := withStubSSH(t, "exit 0\n")
	e := New(shortTempDir(t))

	if _, err := e.Run(context.Background(), "host-a", "docker", "ps", "--format", `{{.Names}}\t{{.Ports}}`); err != nil {
		t.Fatalf("Run: %v", err)
	}

	logged, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatalf("read argv log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(logged), "\n"), "\n")
	last := lines[len(lines)-1]

	if last != `docker ps --format '{{.Names}}\t{{.Ports}}'` {
		t.Fatalf("ssh received %q as its final argument, want the whole quoted command", last)
	}
}
