package main

import (
	"strings"
	"testing"

	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/tunnel"
)

func TestResolveTargetPassesAPortThrough(t *testing.T) {
	_, p := tempHome(t)
	port, err := resolveTarget(p, nil, "46379")
	if err != nil || port != 46379 {
		t.Fatalf("got %d, %v", port, err)
	}
}

func TestResolveTargetMatchesARunningTunnelByName(t *testing.T) {
	_, p := tempHome(t)
	named := redisSpec()
	named.Name = "redis-stg"

	port, err := resolveTarget(p, []tunnel.Status{healthy(named)}, "redis-stg")
	if err != nil || port != 46379 {
		t.Fatalf("got %d, %v", port, err)
	}
}

func TestResolveTargetMatchesAnUnnamedRunningTunnelViaTheCatalogue(t *testing.T) {
	// Opened the long way, saved afterwards: the running row has no Name, but
	// the catalogue says which forward "redis-stg" is.
	_, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())

	port, err := resolveTarget(p, []tunnel.Status{healthy(redisSpec())}, "redis-stg")
	if err != nil || port != 46379 {
		t.Fatalf("got %d, %v", port, err)
	}
}

func TestResolveTargetUnknownName(t *testing.T) {
	_, p := tempHome(t)
	_, err := resolveTarget(p, []tunnel.Status{healthy(redisSpec())}, "nope")
	if err == nil || !strings.Contains(err.Error(), `no running tunnel named "nope"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestDownAcceptsAName(t *testing.T) {
	home, p := tempHome(t)
	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{byOp: map[string]control.Response{
		control.OpList: {OK: true, Statuses: []tunnel.Status{healthy(named)}},
	}}
	serveScripted(t, p, h)

	if _, err := runCmd(t, home, "down", "redis-stg"); err != nil {
		t.Fatal(err)
	}

	var remove *control.Request
	for i := range h.requests {
		if h.requests[i].Op == control.OpRemove {
			remove = &h.requests[i]
		}
	}
	if remove == nil || remove.LocalPort != 46379 {
		t.Fatalf("remove = %+v; ops %v", remove, h.ops())
	}
}

func TestRestartAcceptsAName(t *testing.T) {
	home, p := tempHome(t)
	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{byOp: map[string]control.Response{
		control.OpList: {OK: true, Statuses: []tunnel.Status{healthy(named)}},
	}}
	serveScripted(t, p, h)

	if _, err := runCmd(t, home, "restart", "redis-stg"); err != nil {
		t.Fatal(err)
	}
	for _, r := range h.requests {
		if r.Op == control.OpRestart && r.LocalPort == 46379 {
			return
		}
	}
	t.Fatalf("no restart for 46379; ops %v", h.ops())
}

func TestLogsAcceptsAName(t *testing.T) {
	home, p := tempHome(t)
	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{byOp: map[string]control.Response{
		control.OpList:   {OK: true, Statuses: []tunnel.Status{healthy(named)}},
		control.OpEvents: {OK: true, Events: []tunnel.Event{{State: tunnel.StateHealthy}}},
	}}
	serveScripted(t, p, h)

	out, err := runCmd(t, home, "logs", "redis-stg")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, r := range h.requests {
		if r.Op == control.OpEvents && r.LocalPort == 46379 {
			return
		}
	}
	t.Fatalf("no events request for 46379; ops %v", h.ops())
}

func TestDownByNameWithNoDaemonIsNotAnError(t *testing.T) {
	home, _ := tempHome(t)
	if _, err := runCmd(t, home, "down", "redis-stg"); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestMergeSavedAnnotatesAndAppends(t *testing.T) {
	running := []tunnel.Status{healthy(redisSpec())} // unnamed
	mongo := tunnel.Spec{Host: "example-backend-dev", Container: "app_mongo_staging",
		RemotePort: 27017, LocalPort: 27018, Env: "dev", Name: "mongo-dev"}
	redisNamed := redisSpec()
	redisNamed.Name = "redis-stg"
	catalogue := map[string]tunnel.Spec{"redis-stg": redisNamed, "mongo-dev": mongo}

	got := mergeSaved(running, catalogue)

	if len(got) != 2 {
		t.Fatalf("got %d rows, want running + one saved", len(got))
	}
	if got[0].Spec.Name != "redis-stg" || got[0].State != tunnel.StateHealthy {
		t.Fatalf("running row not annotated from the catalogue: %+v", got[0])
	}
	if got[1].Spec.Name != "mongo-dev" || got[1].State != tunnel.StateSaved {
		t.Fatalf("saved row = %+v", got[1])
	}
}

func TestMergeSavedSortsSavedRowsByName(t *testing.T) {
	catalogue := map[string]tunnel.Spec{
		"zeta":  {Host: "h", Container: "c", RemotePort: 1, LocalPort: 2, Name: "zeta"},
		"alpha": {Host: "h", Container: "c", RemotePort: 1, LocalPort: 3, Name: "alpha"},
	}
	got := mergeSaved(nil, catalogue)
	if got[0].Spec.Name != "alpha" || got[1].Spec.Name != "zeta" {
		t.Fatalf("order = %s, %s", got[0].Spec.Name, got[1].Spec.Name)
	}
}
