package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildHop compiles the binary under test.
func buildHop(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(shortTempDir(t), "hop")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return binary
}

// stubSSHDir writes a fake ssh that answers docker inspect and then holds a
// forward open until it is killed.
func stubSSHDir(t *testing.T) string {
	t.Helper()
	dir := shortTempDir(t)
	// The remote command arrives as ONE shell-quoted argument, which is what
	// ssh actually receives -- matching on a bare "inspect" word would pass
	// against a broken caller.
	script := `#!/bin/sh
for arg in "$@"; do
  case "$arg" in
    *"docker inspect"*)
      echo "172.18.0.4"
      exit 0
      ;;
  esac
done
# A forward: hold the local port open so the bind check and ls probe succeed.
port=$(printf '%s\n' "$@" | sed -n 's/^127\.0\.0\.1:\([0-9]*\):.*/\1/p' | head -1)
if [ -n "$port" ]; then
  exec nc -l 127.0.0.1 "$port"
fi
sleep 60
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatalf("write stub ssh: %v", err)
	}
	return dir
}

func runHop(t *testing.T, binary, home, stubDir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"NO_COLOR=1",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestEndToEndOpenListAndStop(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test spawns processes; run without -short")
	}
	if _, err := exec.LookPath("nc"); err != nil {
		t.Skip("nc is required to hold the stub forward open")
	}

	binary := buildHop(t)
	stubDir := stubSSHDir(t)
	home := shortTempDir(t)

	t.Cleanup(func() { _, _ = runHop(t, binary, home, stubDir, "down", "--all") })

	// Open a tunnel. The daemon does not exist yet, so this also proves
	// auto-spawn works.
	out, err := runHop(t, binary, home, stubDir,
		"example-backend-dev", "app_mongo_staging", "27017", "45918")
	if err != nil {
		t.Fatalf("open: %v\n%s", err, out)
	}
	if !strings.Contains(out, "45918") {
		t.Fatalf("open output does not mention the local port:\n%s", out)
	}

	// It must appear in ls.
	deadline := time.Now().Add(10 * time.Second)
	var listing string
	for time.Now().Before(deadline) {
		listing, _ = runHop(t, binary, home, stubDir, "ls")
		if strings.Contains(listing, "45918") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(listing, "45918") {
		t.Fatalf("tunnel never appeared in ls:\n%s", listing)
	}
	if !strings.Contains(listing, "stg") {
		t.Fatalf("environment was not inferred:\n%s", listing)
	}

	// The state file must have been written.
	if _, err := os.Stat(filepath.Join(home, ".hop", "state.json")); err != nil {
		t.Fatalf("state file missing: %v", err)
	}

	// Stopping it must empty the list and stop the daemon.
	if out, err := runHop(t, binary, home, stubDir, "down", "45918"); err != nil {
		t.Fatalf("down: %v\n%s", err, out)
	}

	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		listing, _ = runHop(t, binary, home, stubDir, "ls")
		if strings.Contains(listing, "no tunnels") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("tunnel still listed after down:\n%s", listing)
}

func TestEndToEndUsageErrorsExitSixtyFour(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test spawns processes; run without -short")
	}
	binary := buildHop(t)
	home := shortTempDir(t)

	_, err := runHop(t, binary, home, shortTempDir(t), "host", "container", "27017")

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("err = %v, want an ExitError", err)
	}
	if got := exitErr.ExitCode(); got != 64 {
		t.Fatalf("exit code = %d, want 64", got)
	}
}

func TestEndToEndNamedTunnelRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test spawns processes; run without -short")
	}
	if _, err := exec.LookPath("nc"); err != nil {
		t.Skip("nc is required to hold the stub forward open")
	}

	binary := buildHop(t)
	stubDir := stubSSHDir(t)
	home := shortTempDir(t)
	t.Cleanup(func() { _, _ = runHop(t, binary, home, stubDir, "down", "--all") })

	// Open with --name: opens, then saves.
	out, err := runHop(t, binary, home, stubDir,
		"example-backend-dev", "app_mongo_staging", "27017", "45919", "--name", "mongo-stg")
	if err != nil {
		t.Fatalf("open --name: %v\n%s", err, out)
	}
	if !strings.Contains(out, "mongo-stg") {
		t.Fatalf("open output lacks the name:\n%s", out)
	}

	listing, _ := runHop(t, binary, home, stubDir, "ls")
	if !strings.Contains(listing, "mongo-stg") || !strings.Contains(listing, "45919") {
		t.Fatalf("ls:\n%s", listing)
	}

	// down by name.
	if out, err := runHop(t, binary, home, stubDir, "down", "mongo-stg"); err != nil {
		t.Fatalf("down by name: %v\n%s", err, out)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		listing, _ = runHop(t, binary, home, stubDir, "ls")
		if strings.Contains(listing, "saved") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(listing, "saved") {
		t.Fatalf("after down, ls should show the tunnel as saved:\n%s", listing)
	}

	// Reopen with one word, on the same port.
	out, err = runHop(t, binary, home, stubDir, "mongo-stg")
	if err != nil {
		t.Fatalf("reopen by name: %v\n%s", err, out)
	}
	if !strings.Contains(out, "45919") {
		t.Fatalf("reopened on a different port:\n%s", out)
	}
	_, _ = runHop(t, binary, home, stubDir, "down", "mongo-stg")

	// forget, then the name is gone.
	if out, err := runHop(t, binary, home, stubDir, "forget", "mongo-stg"); err != nil {
		t.Fatalf("forget: %v\n%s", err, out)
	}
	out, err = runHop(t, binary, home, stubDir, "mongo-stg")
	if err == nil {
		t.Fatalf("forgotten name still opened:\n%s", out)
	}
	if !strings.Contains(out, `no saved tunnel named "mongo-stg"`) {
		t.Fatalf("unexpected error after forget:\n%s", out)
	}
}
