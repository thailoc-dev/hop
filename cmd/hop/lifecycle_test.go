package main

import (
	"bytes"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/locnguyen/hop/internal/tunnel"
)

// recordingHandler is a daemon stand-in that records requests and replies
// with canned answers.
type recordingHandler struct {
	requests []control.Request
	resp     control.Response
}

func (h *recordingHandler) Handle(req control.Request) control.Response {
	h.requests = append(h.requests, req)
	return h.resp
}

// listenUnix opens the control socket for a fake daemon and closes it when
// the test ends.
func listenUnix(t *testing.T, path string) (net.Listener, error) {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, nil
}

// serveFakeDaemon listens on the paths' control socket and returns the handler.
func serveFakeDaemon(t *testing.T, p hopfs.Paths, resp control.Response) *recordingHandler {
	t.Helper()
	h := &recordingHandler{resp: resp}
	l, err := net.Listen("unix", p.ControlSock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = control.Serve(l, h) }()
	t.Cleanup(func() { _ = l.Close() })
	return h
}

// runCmd executes the root command against a temp HOME so the CLI finds the
// fake daemon rather than the real one.
func runCmd(t *testing.T, home string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HOME", home)

	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

// tempHome returns a short HOME whose .hop dirs exist, and its paths.
func tempHome(t *testing.T) (string, hopfs.Paths) {
	t.Helper()
	home := shortTempDir(t)
	p := hopfs.New(home, 501)
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	return home, p
}

func TestDownSendsRemoveForOnePort(t *testing.T) {
	home, p := tempHome(t)
	h := serveFakeDaemon(t, p, control.Response{OK: true})

	if _, err := runCmd(t, home, "down", "27018"); err != nil {
		t.Fatalf("down: %v", err)
	}

	// down lists first (to resolve a name), then removes.
	got := h.requests[len(h.requests)-1]
	if got.Op != control.OpRemove || got.LocalPort != 27018 || got.All {
		t.Fatalf("request = %+v", got)
	}
}

func TestDownAllSendsTheAllFlag(t *testing.T) {
	home, p := tempHome(t)
	h := serveFakeDaemon(t, p, control.Response{OK: true})

	if _, err := runCmd(t, home, "down", "--all"); err != nil {
		t.Fatalf("down --all: %v", err)
	}

	if !h.requests[0].All {
		t.Fatalf("request = %+v, want All", h.requests[0])
	}
}

func TestDownRequiresAPortOrAll(t *testing.T) {
	home, _ := tempHome(t)

	_, err := runCmd(t, home, "down")

	if err == nil {
		t.Fatal("down with no arguments was accepted")
	}
	if !strings.Contains(err.Error(), "--all") {
		t.Fatalf("error does not mention --all: %v", err)
	}
}

func TestDownWithNoDaemonIsNotAnError(t *testing.T) {
	// Nothing to stop is the state the user asked for.
	home, _ := tempHome(t)

	if _, err := runCmd(t, home, "down", "--all"); err != nil {
		t.Fatalf("down --all against no daemon: %v", err)
	}
}

func TestRestartSendsRestartForThePort(t *testing.T) {
	home, p := tempHome(t)
	h := serveFakeDaemon(t, p, control.Response{OK: true})

	if _, err := runCmd(t, home, "restart", "27018"); err != nil {
		t.Fatalf("restart: %v", err)
	}

	// restart lists first (to resolve a name), then restarts.
	last := h.requests[len(h.requests)-1]
	if last.Op != control.OpRestart || last.LocalPort != 27018 {
		t.Fatalf("request = %+v", last)
	}
}

func TestLogsPrintsEventsNewestLast(t *testing.T) {
	home, p := tempHome(t)
	base := time.Date(2026, 9, 10, 14, 22, 0, 0, time.UTC)
	serveFakeDaemon(t, p, control.Response{OK: true, Events: []tunnel.Event{
		{At: base, State: tunnel.StateResolving},
		{At: base.Add(time.Second), State: tunnel.StateConnecting},
		{At: base.Add(2 * time.Second), State: tunnel.StateRetrying, Message: "Connection refused"},
	}})

	out, err := runCmd(t, home, "logs", "27018")
	if err != nil {
		t.Fatalf("logs: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "resolving") || !strings.Contains(lines[2], "retrying") {
		t.Fatalf("wrong order:\n%s", out)
	}
	if !strings.Contains(lines[2], "Connection refused") {
		t.Fatalf("message not rendered:\n%s", out)
	}
	if !strings.Contains(lines[0], "14:22:00") {
		t.Fatalf("no timestamp:\n%s", out)
	}
}

func TestLogsRejectsANonNumericPort(t *testing.T) {
	home, _ := tempHome(t)

	if _, err := runCmd(t, home, "logs", "mongo"); err == nil {
		t.Fatal("accepted a non-numeric port")
	}
}

func TestRenderEventsIsEmptyWithoutEvents(t *testing.T) {
	var buf bytes.Buffer
	renderEvents(&buf, nil, false)
	if !strings.Contains(buf.String(), "no events") {
		t.Fatalf("output = %q", buf.String())
	}
}

func TestDaemonPathsUseTheTempHome(t *testing.T) {
	home, p := tempHome(t)
	if filepath.Dir(p.ControlSock) != filepath.Join(home, ".hop") {
		t.Fatalf("control socket at %q escaped the temp home", p.ControlSock)
	}
}

func TestLsIncludesSavedTunnelsWithoutADaemon(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())

	out, err := runCmd(t, home, "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "redis-stg") || !strings.Contains(out, "saved") {
		t.Fatalf("saved tunnel missing from ls with no daemon:\n%s", out)
	}
}

func TestLsJSONIncludesSavedRows(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())

	out, err := runCmd(t, home, "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var rows []tunnel.Status
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(rows) != 1 || rows[0].State != tunnel.StateSaved || rows[0].Spec.Name != "redis-stg" {
		t.Fatalf("rows = %+v", rows)
	}
}
