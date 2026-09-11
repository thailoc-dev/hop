package main

import (
	"strings"
	"testing"
	"time"

	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/locnguyen/hop/internal/store"
	"github.com/locnguyen/hop/internal/tunnel"
)

// scriptedHandler answers each control operation with its own canned
// response, and records every request. recordingHandler (lifecycle_test.go)
// returns one response for everything, which cannot express "list says it is
// not running, then add succeeds, then list says healthy".
type scriptedHandler struct {
	byOp     map[string]control.Response
	requests []control.Request
	// afterAdd, when set, replaces the OpList response once an add has been
	// seen -- so a poll after opening sees the tunnel come up.
	afterAdd *control.Response
	added    bool
}

func (h *scriptedHandler) Handle(req control.Request) control.Response {
	h.requests = append(h.requests, req)
	if req.Op == control.OpAdd {
		h.added = true
	}
	if req.Op == control.OpList && h.added && h.afterAdd != nil {
		return *h.afterAdd
	}
	if resp, ok := h.byOp[req.Op]; ok {
		return resp
	}
	return control.Response{OK: true}
}

func (h *scriptedHandler) ops() []string {
	var out []string
	for _, r := range h.requests {
		out = append(out, r.Op)
	}
	return out
}

func serveScripted(t *testing.T, p hopfs.Paths, h *scriptedHandler) {
	t.Helper()
	l, err := listenUnix(t, p.ControlSock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = control.Serve(l, h) }()
}

func redisSpec() tunnel.Spec {
	return tunnel.Spec{Host: "example-tracker-dev", Container: "tracker_redis_staging",
		RemotePort: 6379, LocalPort: 46379, Env: "stg"}
}

func healthy(spec tunnel.Spec) tunnel.Status {
	return tunnel.Status{Spec: spec, State: tunnel.StateHealthy,
		Since: time.Now(), CreatedAt: time.Now()}
}

func saveNamed(t *testing.T, p hopfs.Paths, name string, spec tunnel.Spec) {
	t.Helper()
	spec.Name = name
	if err := store.SaveCatalogue(p.CatalogueFile, store.Catalogue{
		Tunnels: map[string]tunnel.Spec{name: spec},
	}); err != nil {
		t.Fatalf("seed catalogue: %v", err)
	}
}

func TestOpenSavedSendsTheCatalogueSpecWithItsName(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())

	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{
		byOp:     map[string]control.Response{control.OpList: {OK: true}},
		afterAdd: &control.Response{OK: true, Statuses: []tunnel.Status{healthy(named)}},
	}
	serveScripted(t, p, h)

	out, err := runCmd(t, home, "redis-stg")
	if err != nil {
		t.Fatalf("open: %v\n%s", err, out)
	}

	var add *control.Request
	for i := range h.requests {
		if h.requests[i].Op == control.OpAdd {
			add = &h.requests[i]
		}
	}
	if add == nil {
		t.Fatalf("no add request; ops were %v", h.ops())
	}
	if add.Spec.Name != "redis-stg" || add.Spec.LocalPort != 46379 {
		t.Fatalf("add spec = %+v", add.Spec)
	}
	if !strings.Contains(out, "redis-stg") || !strings.Contains(out, "46379") {
		t.Fatalf("open output does not show the name and port:\n%s", out)
	}
}

func TestOpenSavedAlreadyRunningIsNotAnError(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{byOp: map[string]control.Response{
		control.OpList: {OK: true, Statuses: []tunnel.Status{healthy(named)}},
	}}
	serveScripted(t, p, h)

	out, err := runCmd(t, home, "redis-stg")
	if err != nil {
		t.Fatalf("expected exit 0 for an already-running tunnel: %v", err)
	}
	if !strings.Contains(out, "already running") || !strings.Contains(out, "46379") {
		t.Fatalf("output = %q", out)
	}
	for _, op := range h.ops() {
		if op == control.OpAdd {
			t.Fatal("sent an add for a tunnel that was already running")
		}
	}
}

func TestOpenSavedRecognisesTheSameTunnelRunningUnnamed(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	h := &scriptedHandler{byOp: map[string]control.Response{
		control.OpList: {OK: true, Statuses: []tunnel.Status{healthy(redisSpec())}},
	}}
	serveScripted(t, p, h)

	out, err := runCmd(t, home, "redis-stg")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "already running") {
		t.Fatalf("output = %q", out)
	}
}

func TestOpenSavedUnknownName(t *testing.T) {
	home, _ := tempHome(t)

	_, err := runCmd(t, home, "nope")

	if err == nil {
		t.Fatal("unknown name accepted")
	}
	if !strings.Contains(err.Error(), `no saved tunnel named "nope"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenSavedUnknownNameThatIsAHostHintsAtTheLongForm(t *testing.T) {
	home, _ := tempHome(t)
	writeSSHConfig(t, home, "Host example-backend-dev\n")

	_, err := runCmd(t, home, "example-backend-dev")

	if err == nil {
		t.Fatal("accepted")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no saved tunnel") || !strings.Contains(msg, "<container> <remote-port> <local-port>") {
		t.Fatalf("a host name used alone should explain the four-argument form: %q", msg)
	}
}

func TestNameFlagValidatesBeforeOpening(t *testing.T) {
	home, p := tempHome(t)
	h := &scriptedHandler{}
	serveScripted(t, p, h)

	_, err := runCmd(t, home, "example-tracker-dev", "tracker_redis_staging", "6379", "46379", "--name", "Bad Name")

	if err == nil {
		t.Fatal("invalid --name accepted")
	}
	if len(h.requests) != 0 {
		t.Fatalf("a tunnel was opened before the name was rejected: %v", h.ops())
	}
	if _, statErr := store.LoadCatalogue(p.CatalogueFile); statErr != nil {
		t.Fatalf("catalogue: %v", statErr)
	}
}

func TestNameFlagOpensThenSaves(t *testing.T) {
	home, p := tempHome(t)
	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{
		byOp:     map[string]control.Response{control.OpList: {OK: true}},
		afterAdd: &control.Response{OK: true, Statuses: []tunnel.Status{healthy(named)}},
	}
	serveScripted(t, p, h)

	out, err := runCmd(t, home, "example-tracker-dev", "tracker_redis_staging", "6379", "46379", "--name", "redis-stg")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	c, _ := store.LoadCatalogue(p.CatalogueFile)
	got, ok := c.Tunnels["redis-stg"]
	if !ok {
		t.Fatalf("not saved; catalogue = %+v", c.Tunnels)
	}
	if got.LocalPort != 46379 || got.Host != "example-tracker-dev" {
		t.Fatalf("saved %+v", got)
	}
}

func TestUpIsTheExplicitFormOfTheBareOpen(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{
		byOp:     map[string]control.Response{control.OpList: {OK: true}},
		afterAdd: &control.Response{OK: true, Statuses: []tunnel.Status{healthy(named)}},
	}
	serveScripted(t, p, h)

	if out, err := runCmd(t, home, "up", "redis-stg"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !h.added {
		t.Fatal("up did not open the tunnel")
	}
}

func TestSameTunnelIgnoresNameAndEnv(t *testing.T) {
	a := redisSpec()
	b := redisSpec()
	b.Name, b.Env = "whatever", "prod"
	if !sameTunnel(a, b) {
		t.Fatal("name and env must not affect identity")
	}
	b.LocalPort++
	if sameTunnel(a, b) {
		t.Fatal("a different local port is a different tunnel")
	}
}
