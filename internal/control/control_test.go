package control

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/locnguyen/hop/internal/tunnel"
)

type stubHandler struct{ last Request }

func (h *stubHandler) Handle(req Request) Response {
	h.last = req
	switch req.Op {
	case OpPing:
		return Response{OK: true}
	case OpList:
		return Response{OK: true, Statuses: []tunnel.Status{{
			Spec:  tunnel.Spec{Host: "h", Container: "c", LocalPort: 27018},
			State: tunnel.StateHealthy,
		}}}
	default:
		return Response{OK: false, Error: "unsupported op " + req.Op}
	}
}

// shortTempDir returns a temp directory with a short path.
//
// t.TempDir() embeds the test name, and a long one pushes a socket path past
// the 104-byte sun_path limit — "bind: invalid argument", with no hint as to
// why. Socket paths in tests must not go through t.TempDir().
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

func serveTemp(t *testing.T, h Handler) string {
	t.Helper()
	sock := filepath.Join(shortTempDir(t), "ctl.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = Serve(l, h) }()
	t.Cleanup(func() { _ = l.Close() })
	return sock
}

func TestPingRoundTrips(t *testing.T) {
	sock := serveTemp(t, &stubHandler{})

	c, err := Dial(sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	resp, err := c.Send(Request{Op: OpPing})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !resp.OK {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestListCarriesStatuses(t *testing.T) {
	sock := serveTemp(t, &stubHandler{})
	c, _ := Dial(sock)
	defer func() { _ = c.Close() }()

	resp, err := c.Send(Request{Op: OpList})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(resp.Statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(resp.Statuses))
	}
	if resp.Statuses[0].State != tunnel.StateHealthy {
		t.Fatalf("state = %v", resp.Statuses[0].State)
	}
	if resp.Statuses[0].Spec.LocalPort != 27018 {
		t.Fatalf("spec did not survive the wire: %+v", resp.Statuses[0].Spec)
	}
}

func TestSpecSurvivesTheWire(t *testing.T) {
	h := &stubHandler{}
	sock := serveTemp(t, h)
	c, _ := Dial(sock)
	defer func() { _ = c.Close() }()

	want := tunnel.Spec{Host: "air", Container: "mongo", RemotePort: 27017, LocalPort: 27018, Env: "prod"}
	if _, err := c.Send(Request{Op: OpAdd, Spec: &want}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if h.last.Spec == nil || *h.last.Spec != want {
		t.Fatalf("handler saw %+v, want %+v", h.last.Spec, want)
	}
}

func TestErrorsComeBackAsErrorNotFailure(t *testing.T) {
	sock := serveTemp(t, &stubHandler{})
	c, _ := Dial(sock)
	defer func() { _ = c.Close() }()

	resp, err := c.Send(Request{Op: "nonsense"})
	if err != nil {
		t.Fatalf("transport error for an application-level rejection: %v", err)
	}
	if resp.OK || resp.Error == "" {
		t.Fatalf("resp = %+v, want a populated Error", resp)
	}
}

func TestDialFailsWhenNothingIsListening(t *testing.T) {
	_, err := Dial(filepath.Join(shortTempDir(t), "absent.sock"))
	if err == nil {
		t.Fatal("Dial succeeded against a socket that does not exist")
	}
	if !errors.Is(err, ErrNoDaemon) {
		t.Fatalf("err = %v, want ErrNoDaemon so the caller can auto-spawn", err)
	}
}

func TestDialFailsForAStaleSocket(t *testing.T) {
	// A socket file left by a crashed daemon: present on disk, refusing
	// connections. The caller must be able to tell this apart from a healthy
	// daemon so it can unlink and respawn.
	sock := filepath.Join(shortTempDir(t), "ctl.sock")
	if err := writeStaleSocket(sock); err != nil {
		t.Skipf("cannot stage a stale socket: %v", err)
	}

	if _, err := Dial(sock); !errors.Is(err, ErrNoDaemon) {
		t.Fatalf("err = %v, want ErrNoDaemon", err)
	}
}

func writeStaleSocket(path string) error {
	l, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	// Close the listener without unlinking, leaving an orphaned socket inode.
	if ul, ok := l.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	return l.Close()
}
