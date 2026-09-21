package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/thailoc-dev/hop/internal/control"
	"github.com/thailoc-dev/hop/internal/store"
	"github.com/thailoc-dev/hop/internal/tunnel"
)

// A command with the open flags, as openSpec expects.
func openCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "x"}
	cmd.PersistentFlags().Bool("json", false, "")
	cmd.PersistentFlags().Bool("no-color", false, "")
	cmd.PersistentFlags().BoolP("quiet", "q", false, "")
	addTunnelFlags(cmd)
	return cmd
}

func TestOpenSpecSendsTheSpecAndInfersEnv(t *testing.T) {
	home, p := tempHome(t)
	t.Setenv("HOME", home)
	spec := tunnel.Spec{Host: "example-backend-dev", Container: "app_mongo_staging", RemotePort: 27017, LocalPort: 27018}
	up := spec
	up.Env = "stg"
	h := &scriptedHandler{
		byOp:     map[string]control.Response{control.OpList: {OK: true}},
		afterAdd: &control.Response{OK: true, Statuses: []tunnel.Status{healthy(up)}},
	}
	serveScripted(t, p, h)

	cmd := openCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	if err := openSpec(cmd, spec); err != nil {
		t.Fatalf("openSpec: %v\n%s", err, out.String())
	}

	var add *control.Request
	for i := range h.requests {
		if h.requests[i].Op == control.OpAdd {
			add = &h.requests[i]
		}
	}
	if add == nil {
		t.Fatalf("no add; ops %v", h.ops())
	}
	if add.Spec.Env != "stg" {
		t.Fatalf("env not inferred from the container name: %+v", add.Spec)
	}
	if !strings.Contains(out.String(), "27018") {
		t.Fatalf("open line missing:\n%s", out.String())
	}
}

func TestOpenSpecSavesWhenTheSpecCarriesAName(t *testing.T) {
	home, p := tempHome(t)
	t.Setenv("HOME", home)
	spec := redisSpec()
	spec.Name = "redis-stg"
	h := &scriptedHandler{
		byOp:     map[string]control.Response{control.OpList: {OK: true}},
		afterAdd: &control.Response{OK: true, Statuses: []tunnel.Status{healthy(spec)}},
	}
	serveScripted(t, p, h)

	cmd := openCmd()
	cmd.SetOut(&strings.Builder{})
	if err := openSpec(cmd, spec); err != nil {
		t.Fatal(err)
	}

	c, _ := store.LoadCatalogue(p.CatalogueFile)
	if _, ok := c.Tunnels["redis-stg"]; !ok {
		t.Fatalf("not saved: %v", c.Tunnels)
	}
}

func TestOpenSpecRejectsABadNameBeforeOpening(t *testing.T) {
	home, p := tempHome(t)
	t.Setenv("HOME", home)
	h := &scriptedHandler{}
	serveScripted(t, p, h)
	spec := redisSpec()
	spec.Name = "Bad Name"

	cmd := openCmd()
	cmd.SetOut(&strings.Builder{})
	if err := openSpec(cmd, spec); err == nil {
		t.Fatal("accepted an invalid name")
	}
	if len(h.requests) != 0 {
		t.Fatalf("opened before validating: %v", h.ops())
	}
}

func TestOpenTunnelStillParsesThenOpens(t *testing.T) {
	home, p := tempHome(t)
	up := redisSpec()
	up.Name = "redis-stg"
	h := &scriptedHandler{
		byOp:     map[string]control.Response{control.OpList: {OK: true}},
		afterAdd: &control.Response{OK: true, Statuses: []tunnel.Status{healthy(up)}},
	}
	serveScripted(t, p, h)

	out, err := runCmd(t, home, "example-tracker-dev", "tracker_redis_staging", "6379", "46379", "--name", "redis-stg")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !h.added {
		t.Fatal("not opened")
	}
}

func TestNoArgsWithoutATerminalPrintsHelp(t *testing.T) {
	home, _ := tempHome(t)
	out, err := runCmd(t, home)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(out, "Usage:") {
		t.Fatalf("expected help without a terminal:\n%s", out)
	}
	if strings.Contains(out, "not implemented") {
		t.Fatal("the picker ran without a terminal")
	}
}
