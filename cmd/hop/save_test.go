package main

import (
	"strings"
	"testing"
	"time"

	"github.com/thailoc-dev/hop/internal/control"
	"github.com/thailoc-dev/hop/internal/store"
	"github.com/thailoc-dev/hop/internal/tunnel"
)

func TestSaveWithNoPortTakesTheMostRecentlyOpened(t *testing.T) {
	home, p := tempHome(t)
	older := healthy(redisSpec())
	older.CreatedAt = time.Now().Add(-time.Hour)
	// Reconnected a second ago: Since is newest, CreatedAt is not.
	older.Since = time.Now()

	newerSpec := tunnel.Spec{Host: "example-backend-dev", Container: "app_mongo_staging",
		RemotePort: 27017, LocalPort: 27018, Env: "stg"}
	newer := healthy(newerSpec)
	newer.CreatedAt = time.Now().Add(-time.Minute)
	newer.Since = time.Now().Add(-time.Minute)

	serveScripted(t, p, &scriptedHandler{byOp: map[string]control.Response{
		control.OpList: {OK: true, Statuses: []tunnel.Status{older, newer}},
	}})

	if out, err := runCmd(t, home, "save", "mongo-stg"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	c, _ := store.LoadCatalogue(p.CatalogueFile)
	got := c.Tunnels["mongo-stg"]
	if got.LocalPort != 27018 {
		t.Fatalf("saved port %d; picked by Since instead of CreatedAt", got.LocalPort)
	}
}

func TestSaveWithAPortTakesThatTunnel(t *testing.T) {
	home, p := tempHome(t)
	serveScripted(t, p, &scriptedHandler{byOp: map[string]control.Response{
		control.OpList: {OK: true, Statuses: []tunnel.Status{healthy(redisSpec())}},
	}})

	if out, err := runCmd(t, home, "save", "redis-stg", "46379"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	c, _ := store.LoadCatalogue(p.CatalogueFile)
	if c.Tunnels["redis-stg"].Container != "tracker_redis_staging" {
		t.Fatalf("saved %+v", c.Tunnels["redis-stg"])
	}
}

func TestSaveWithAPortNobodyIsOn(t *testing.T) {
	home, p := tempHome(t)
	serveScripted(t, p, &scriptedHandler{byOp: map[string]control.Response{
		control.OpList: {OK: true, Statuses: []tunnel.Status{healthy(redisSpec())}},
	}})

	_, err := runCmd(t, home, "save", "x-y", "9999")

	if err == nil || !strings.Contains(err.Error(), "no tunnel on local port 9999") {
		t.Fatalf("err = %v", err)
	}
}

func TestSaveWithNothingRunningIsAUsageError(t *testing.T) {
	home, p := tempHome(t)
	serveScripted(t, p, &scriptedHandler{byOp: map[string]control.Response{
		control.OpList: {OK: true},
	}})

	_, err := runCmd(t, home, "save", "redis-stg")

	if err == nil || !strings.Contains(err.Error(), "no tunnels are running") {
		t.Fatalf("err = %v", err)
	}
}

func TestSaveRejectsABadNameBeforeTalkingToTheDaemon(t *testing.T) {
	home, p := tempHome(t)
	h := &scriptedHandler{}
	serveScripted(t, p, h)

	_, err := runCmd(t, home, "save", "46379")

	if err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("all-digit name accepted: %v", err)
	}
	if len(h.requests) != 0 {
		t.Fatal("contacted the daemon before validating the name")
	}
}

func TestSaveOverwritesAnExistingName(t *testing.T) {
	home, p := tempHome(t)
	old := redisSpec()
	old.LocalPort = 1
	saveNamed(t, p, "redis-stg", old)
	serveScripted(t, p, &scriptedHandler{byOp: map[string]control.Response{
		control.OpList: {OK: true, Statuses: []tunnel.Status{healthy(redisSpec())}},
	}})

	if _, err := runCmd(t, home, "save", "redis-stg"); err != nil {
		t.Fatal(err)
	}

	c, _ := store.LoadCatalogue(p.CatalogueFile)
	if c.Tunnels["redis-stg"].LocalPort != 46379 {
		t.Fatalf("not overwritten: %+v", c.Tunnels["redis-stg"])
	}
}

func TestForgetRemovesTheName(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())

	if _, err := runCmd(t, home, "forget", "redis-stg"); err != nil {
		t.Fatal(err)
	}

	c, _ := store.LoadCatalogue(p.CatalogueFile)
	if _, still := c.Tunnels["redis-stg"]; still {
		t.Fatal("still in the catalogue")
	}
}

func TestForgetUnknownNameIsAnError(t *testing.T) {
	home, _ := tempHome(t)
	_, err := runCmd(t, home, "forget", "nope")
	if err == nil || !strings.Contains(err.Error(), `no saved tunnel named "nope"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestForgetDoesNotTouchTheDaemon(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	h := &scriptedHandler{}
	serveScripted(t, p, h)

	_, _ = runCmd(t, home, "forget", "redis-stg")

	if len(h.requests) != 0 {
		t.Fatalf("forget sent %v to the daemon", h.ops())
	}
}

func TestNewestStatus(t *testing.T) {
	a := healthy(redisSpec())
	a.CreatedAt = time.Unix(100, 0)
	b := healthy(redisSpec())
	b.CreatedAt = time.Unix(200, 0)

	got, ok := newestStatus([]tunnel.Status{a, b})
	if !ok || !got.CreatedAt.Equal(b.CreatedAt) {
		t.Fatalf("got %+v, %v", got.CreatedAt, ok)
	}
	if _, ok := newestStatus(nil); ok {
		t.Fatal("ok for an empty list")
	}
}
