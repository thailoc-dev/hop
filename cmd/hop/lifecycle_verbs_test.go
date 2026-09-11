package main

import (
	"strings"
	"testing"

	"github.com/thailoc-dev/hop/internal/control"
	"github.com/thailoc-dev/hop/internal/store"
	"github.com/thailoc-dev/hop/internal/tunnel"
)

// ---- auto-naming --------------------------------------------------------

func TestAutoNameDerivesFromContainerAndPort(t *testing.T) {
	got := autoName(redisSpec(), nil)
	if got != "tracker_redis_staging-46379" {
		t.Fatalf("got %q", got)
	}
	if err := validateName(got); err != nil {
		t.Fatalf("auto name is not a valid name: %v", err)
	}
}

func TestAutoNameSanitisesAndTruncates(t *testing.T) {
	ugly := tunnel.Spec{Container: "My.Weird Container/NAME!!", LocalPort: 27018}
	got := autoName(ugly, nil)
	if err := validateName(got); err != nil {
		t.Fatalf("%q is not valid: %v", got, err)
	}
	if !strings.HasSuffix(got, "-27018") {
		t.Fatalf("port suffix lost: %q", got)
	}

	long := tunnel.Spec{Container: strings.Repeat("abcdefghij", 8), LocalPort: 27018}
	got = autoName(long, nil)
	if err := validateName(got); err != nil {
		t.Fatalf("%q from a long container name is not valid: %v", got, err)
	}
	if len(got) > 40 {
		t.Fatalf("%d chars", len(got))
	}
}

func TestAutoNameReusesTheNameWhenItIsTheSameTunnel(t *testing.T) {
	existing := redisSpec()
	existing.Name = "tracker_redis_staging-46379"
	cat := map[string]tunnel.Spec{existing.Name: existing}

	if got := autoName(redisSpec(), cat); got != "tracker_redis_staging-46379" {
		t.Fatalf("got %q, want the existing entry reused", got)
	}
}

func TestAutoNameAvoidsCollidingWithADifferentTunnel(t *testing.T) {
	other := redisSpec()
	other.Host = "somewhere-else"
	cat := map[string]tunnel.Spec{"tracker_redis_staging-46379": other}

	got := autoName(redisSpec(), cat)
	if got == "tracker_redis_staging-46379" {
		t.Fatal("reused a name that points at a different tunnel")
	}
	if err := validateName(got); err != nil {
		t.Fatalf("%q: %v", got, err)
	}
}

// ---- stop ---------------------------------------------------------------

func TestStopUnnamedTunnelAutoSavesIt(t *testing.T) {
	// The bug the user hit: stopping an unnamed tunnel lost it forever.
	home, p := tempHome(t)
	h := &scriptedHandler{byOp: map[string]control.Response{
		control.OpList: {OK: true, Statuses: []tunnel.Status{healthy(redisSpec())}},
	}}
	serveScripted(t, p, h)

	out, err := runCmd(t, home, "stop", "46379")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	c, _ := store.LoadCatalogue(p.CatalogueFile)
	saved, ok := c.Tunnels["tracker_redis_staging-46379"]
	if !ok {
		t.Fatalf("not saved; catalogue = %v", c.Tunnels)
	}
	if !sameTunnel(saved, redisSpec()) {
		t.Fatalf("saved the wrong spec: %+v", saved)
	}
	if !strings.Contains(out, "tracker_redis_staging-46379") {
		t.Fatalf("the auto name must be printed so the user knows what to start:\n%s", out)
	}
	var removed bool
	for _, r := range h.requests {
		if r.Op == control.OpRemove && r.LocalPort == 46379 {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("ssh forward not stopped; ops %v", h.ops())
	}
}

func TestStopNamedTunnelKeepsItsName(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{byOp: map[string]control.Response{
		control.OpList: {OK: true, Statuses: []tunnel.Status{healthy(named)}},
	}}
	serveScripted(t, p, h)

	out, err := runCmd(t, home, "stop", "redis-stg")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	c, _ := store.LoadCatalogue(p.CatalogueFile)
	if len(c.Tunnels) != 1 {
		t.Fatalf("catalogue gained an auto-name for an already-named tunnel: %v", c.Tunnels)
	}
}

func TestStopAlreadyStoppedIsNotAnError(t *testing.T) {
	// docker stop on a stopped container: fine.
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())

	out, err := runCmd(t, home, "stop", "redis-stg")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "already stopped") {
		t.Fatalf("out = %q", out)
	}
}

func TestStopUnknownIsAnError(t *testing.T) {
	home, _ := tempHome(t)
	if _, err := runCmd(t, home, "stop", "nope"); err == nil {
		t.Fatal("stopping nothing succeeded")
	}
}

func TestDownIsAnAliasOfStop(t *testing.T) {
	home, p := tempHome(t)
	h := &scriptedHandler{byOp: map[string]control.Response{
		control.OpList: {OK: true, Statuses: []tunnel.Status{healthy(redisSpec())}},
	}}
	serveScripted(t, p, h)

	if _, err := runCmd(t, home, "down", "46379"); err != nil {
		t.Fatal(err)
	}

	c, _ := store.LoadCatalogue(p.CatalogueFile)
	if len(c.Tunnels) != 1 {
		t.Fatal("down discarded an unnamed tunnel; it must keep it like stop does")
	}
}

func TestDownAllStopsEverythingAndKeepsAll(t *testing.T) {
	home, p := tempHome(t)
	mongo := tunnel.Spec{Host: "example-backend-dev", Container: "app_mongo_staging", RemotePort: 27017, LocalPort: 27018}
	h := &scriptedHandler{byOp: map[string]control.Response{
		control.OpList: {OK: true, Statuses: []tunnel.Status{healthy(redisSpec()), healthy(mongo)}},
	}}
	serveScripted(t, p, h)

	if _, err := runCmd(t, home, "stop", "--all"); err != nil {
		t.Fatal(err)
	}

	c, _ := store.LoadCatalogue(p.CatalogueFile)
	if len(c.Tunnels) != 2 {
		t.Fatalf("stop --all kept %d of 2", len(c.Tunnels))
	}
}

// ---- start --------------------------------------------------------------

func TestStartByPortFindsTheSavedTunnel(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{
		byOp:     map[string]control.Response{control.OpList: {OK: true}},
		afterAdd: &control.Response{OK: true, Statuses: []tunnel.Status{healthy(named)}},
	}
	serveScripted(t, p, h)

	out, err := runCmd(t, home, "start", "46379")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !h.added {
		t.Fatal("start by port did not open the tunnel")
	}
}

func TestStartByUnknownPort(t *testing.T) {
	home, _ := tempHome(t)
	_, err := runCmd(t, home, "start", "9999")
	if err == nil || !strings.Contains(err.Error(), "no saved tunnel on local port 9999") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpIsAnAliasOfStart(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{
		byOp:     map[string]control.Response{control.OpList: {OK: true}},
		afterAdd: &control.Response{OK: true, Statuses: []tunnel.Status{healthy(named)}},
	}
	serveScripted(t, p, h)
	if _, err := runCmd(t, home, "up", "redis-stg"); err != nil || !h.added {
		t.Fatalf("up: %v added=%v", err, h.added)
	}
}

// ---- rm -----------------------------------------------------------------

func TestRmRunningTunnelStopsThenRemoves(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{byOp: map[string]control.Response{
		control.OpList: {OK: true, Statuses: []tunnel.Status{healthy(named)}},
	}}
	serveScripted(t, p, h)

	if _, err := runCmd(t, home, "rm", "redis-stg"); err != nil {
		t.Fatal(err)
	}

	var removed bool
	for _, r := range h.requests {
		if r.Op == control.OpRemove && r.LocalPort == 46379 {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("running tunnel not stopped; ops %v", h.ops())
	}
	c, _ := store.LoadCatalogue(p.CatalogueFile)
	if _, still := c.Tunnels["redis-stg"]; still {
		t.Fatal("still in the catalogue")
	}
}

func TestRmByPortOfAStoppedTunnel(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())

	if _, err := runCmd(t, home, "rm", "46379"); err != nil {
		t.Fatal(err)
	}
	c, _ := store.LoadCatalogue(p.CatalogueFile)
	if len(c.Tunnels) != 0 {
		t.Fatalf("catalogue = %v", c.Tunnels)
	}
}

func TestRmUnknownIsAnError(t *testing.T) {
	home, _ := tempHome(t)
	if _, err := runCmd(t, home, "rm", "nope"); err == nil {
		t.Fatal("rm of nothing succeeded")
	}
}

func TestForgetIsAnAliasOfRm(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	if _, err := runCmd(t, home, "forget", "redis-stg"); err != nil {
		t.Fatal(err)
	}
	c, _ := store.LoadCatalogue(p.CatalogueFile)
	if len(c.Tunnels) != 0 {
		t.Fatal("forget did not remove")
	}
}

// ---- restart ------------------------------------------------------------

func TestRestartStartsAStoppedTunnel(t *testing.T) {
	// docker restart on a stopped container starts it.
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{
		byOp:     map[string]control.Response{control.OpList: {OK: true}},
		afterAdd: &control.Response{OK: true, Statuses: []tunnel.Status{healthy(named)}},
	}
	serveScripted(t, p, h)

	out, err := runCmd(t, home, "restart", "redis-stg")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !h.added {
		t.Fatalf("restart of a stopped tunnel did not start it; ops %v", h.ops())
	}
}

func TestReservedWordsIncludeTheLifecycleVerbs(t *testing.T) {
	for _, w := range []string{"stop", "start", "rm"} {
		if !reservedWords[w] {
			t.Fatalf("%q not reserved", w)
		}
	}
}

func TestRestartByPortStartsAStoppedTunnel(t *testing.T) {
	// Found against a real host: restart by name fell back to the catalogue,
	// restart by port did not, because a bare port resolves without checking
	// that anything is running on it.
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{
		byOp:     map[string]control.Response{control.OpList: {OK: true}},
		afterAdd: &control.Response{OK: true, Statuses: []tunnel.Status{healthy(named)}},
	}
	serveScripted(t, p, h)

	out, err := runCmd(t, home, "restart", "46379")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !h.added {
		t.Fatalf("restart by port did not start the stopped tunnel; ops %v", h.ops())
	}
	for _, r := range h.requests {
		if r.Op == control.OpRestart {
			t.Fatal("sent a daemon restart for a tunnel that was not running")
		}
	}
}

// restart can start a stopped tunnel, so it needs the open flags -- without
// --wait it returned after 0s with the tunnel still resolving.
func TestRestartHasTheOpenFlags(t *testing.T) {
	cmd := newRestartCmd()
	for _, flag := range []string{"wait", "attach", "env"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Fatalf("restart lacks --%s; starting a stopped tunnel would not wait for health", flag)
		}
	}
	if got := cmd.Flags().Lookup("wait").DefValue; got != "10s" {
		t.Fatalf("--wait default = %q, want 10s like every other open", got)
	}
}
