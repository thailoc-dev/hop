package main

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/thailoc-dev/hop/internal/control"
	"github.com/thailoc-dev/hop/internal/hopfs"
)

// shortTempDir returns a temp directory with a short path.
//
// t.TempDir() embeds the test name, and a long one pushes a socket path past
// the 104-byte sun_path limit — "bind: invalid argument", with no hint as to
// why. Anything that builds a socket path must use this instead.
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

func tempPaths(t *testing.T) hopfs.Paths {
	t.Helper()
	p := hopfs.New(shortTempDir(t), os.Getuid())
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	return p
}

type pingHandler struct{}

func (pingHandler) Handle(control.Request) control.Response { return control.Response{OK: true} }

func TestConnectReachesALiveDaemon(t *testing.T) {
	p := tempPaths(t)
	l, err := net.Listen("unix", p.ControlSock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = l.Close() }()
	go func() { _ = control.Serve(l, pingHandler{}) }()

	c, err := connect(p)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	resp, err := c.Send(control.Request{Op: control.OpPing})
	if err != nil || !resp.OK {
		t.Fatalf("ping failed: resp=%+v err=%v", resp, err)
	}
}

func TestConnectRemovesAStaleSocketBeforeSpawning(t *testing.T) {
	p := tempPaths(t)

	l, err := net.Listen("unix", p.ControlSock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	if ul, ok := l.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	_ = l.Close()

	if _, err := os.Stat(p.ControlSock); err != nil {
		t.Skipf("cannot stage a stale socket on this platform: %v", err)
	}

	if err := clearStaleSocket(p); err != nil {
		t.Fatalf("clearStaleSocket: %v", err)
	}
	if _, err := os.Stat(p.ControlSock); !os.IsNotExist(err) {
		t.Fatal("stale socket was not removed")
	}
}

func TestClearStaleSocketLeavesALiveSocketAlone(t *testing.T) {
	p := tempPaths(t)
	l, err := net.Listen("unix", p.ControlSock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = l.Close() }()
	go func() { _ = control.Serve(l, pingHandler{}) }()

	if err := clearStaleSocket(p); err != nil {
		t.Fatalf("clearStaleSocket: %v", err)
	}
	if _, err := os.Stat(p.ControlSock); err != nil {
		t.Fatal("a live daemon's socket was removed")
	}
}

func TestSpawnLockSerialisesConcurrentCallers(t *testing.T) {
	p := tempPaths(t)

	var mu sync.Mutex
	held := 0
	maxHeld := 0

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := acquireSpawnLock(p)
			if err != nil {
				return
			}
			mu.Lock()
			held++
			if held > maxHeld {
				maxHeld = held
			}
			mu.Unlock()

			mu.Lock()
			held--
			mu.Unlock()
			_ = release()
		}()
	}
	wg.Wait()

	if maxHeld > 1 {
		t.Fatalf("%d callers held the spawn lock at once", maxHeld)
	}
}

func TestRotateLogMovesAnOversizedFileAside(t *testing.T) {
	p := tempPaths(t)
	if err := os.WriteFile(p.DaemonLog, make([]byte, 200), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := rotateLog(p.DaemonLog, 100); err != nil {
		t.Fatalf("rotateLog: %v", err)
	}

	if _, err := os.Stat(p.DaemonLog); !os.IsNotExist(err) {
		t.Fatal("oversized log was not moved aside")
	}
	if _, err := os.Stat(p.DaemonLog + ".1"); err != nil {
		t.Fatalf("previous log was discarded rather than kept: %v", err)
	}
}

func TestRotateLogLeavesASmallFileAlone(t *testing.T) {
	p := tempPaths(t)
	if err := os.WriteFile(p.DaemonLog, make([]byte, 10), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := rotateLog(p.DaemonLog, 100); err != nil {
		t.Fatalf("rotateLog: %v", err)
	}

	if _, err := os.Stat(p.DaemonLog); err != nil {
		t.Fatal("a log under the limit was rotated")
	}
}

func TestRotateLogIsFineWithNoFile(t *testing.T) {
	p := tempPaths(t)
	if err := rotateLog(p.DaemonLog, 100); err != nil {
		t.Fatalf("rotateLog on a missing file: %v", err)
	}
}

func TestDaemonLogPathIsInsideTheHopRoot(t *testing.T) {
	p := tempPaths(t)
	if got := filepath.Dir(p.DaemonLog); got != p.Root {
		t.Fatalf("daemon log at %q, want it under %q", p.DaemonLog, p.Root)
	}
}
