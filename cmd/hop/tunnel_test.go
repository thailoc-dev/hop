package main

import (
	"strings"
	"testing"

	"github.com/locnguyen/hop/internal/tunnel"
)

func TestParseTunnelArgs(t *testing.T) {
	spec, err := parseTunnelArgs([]string{
		"example-backend-dev", "app_mongo_staging", "27017", "27018",
	})
	if err != nil {
		t.Fatalf("parseTunnelArgs: %v", err)
	}

	if spec.Host != "example-backend-dev" || spec.Container != "app_mongo_staging" {
		t.Fatalf("spec = %+v", spec)
	}
	if spec.RemotePort != 27017 || spec.LocalPort != 27018 {
		t.Fatalf("ports = %d, %d", spec.RemotePort, spec.LocalPort)
	}
	if spec.Env != "stg" {
		t.Fatalf("Env = %q, want stg", spec.Env)
	}
}

func TestParseTunnelArgsRejectsNonNumericPorts(t *testing.T) {
	_, err := parseTunnelArgs([]string{"h", "c", "twenty-seven-thousand", "27018"})
	if err == nil {
		t.Fatal("accepted a non-numeric remote port")
	}
	if !strings.Contains(err.Error(), "remote-port") {
		t.Fatalf("error does not say which argument was wrong: %v", err)
	}
}

func TestParseTunnelArgsRejectsOutOfRangePorts(t *testing.T) {
	for _, args := range [][]string{
		{"h", "c", "0", "27018"},
		{"h", "c", "27017", "70000"},
		{"h", "c", "27017", "-1"},
	} {
		if _, err := parseTunnelArgs(args); err == nil {
			t.Fatalf("accepted out-of-range ports: %v", args)
		}
	}
}

func TestParseTunnelArgsRejectsTheWrongArgumentCount(t *testing.T) {
	for _, args := range [][]string{
		{"h", "c", "27017"},
		{"h", "c", "27017", "27018", "extra"},
		{},
	} {
		_, err := parseTunnelArgs(args)
		if err == nil {
			t.Fatalf("accepted %d arguments", len(args))
		}
		if !strings.Contains(err.Error(), "hop <host>") {
			t.Fatalf("unhelpful error: %v", err)
		}
	}
}

func TestReservedWordsCoverEverySubcommand(t *testing.T) {
	root := newRootCmd()
	for _, sub := range root.Commands() {
		name := sub.Name()
		if strings.HasPrefix(name, "__") || name == "help" {
			continue
		}
		if !reservedWords[name] {
			t.Fatalf("subcommand %q is not in reservedWords, so `hop %s ...` "+
				"would be parsed as a hostname", name, name)
		}
	}
}

func TestNewEventsReturnsOnlyTheUnseen(t *testing.T) {
	all := []tunnel.Event{
		{State: tunnel.StateResolving},
		{State: tunnel.StateConnecting},
		{State: tunnel.StateHealthy},
	}

	if got := newEvents(all, 0); len(got) != 3 {
		t.Fatalf("got %d events from a fresh view, want 3", len(got))
	}
	if got := newEvents(all, 2); len(got) != 1 || got[0].State != tunnel.StateHealthy {
		t.Fatalf("got %+v, want just the healthy event", got)
	}
	if got := newEvents(all, 3); got != nil {
		t.Fatalf("got %+v, want nil when everything has been seen", got)
	}
	// A daemon restart can shrink the ring; that must not panic.
	if got := newEvents(all, 99); got != nil {
		t.Fatalf("got %+v, want nil when seen exceeds the ring", got)
	}
}

func TestBareFormIsNotConfusedWithASubcommand(t *testing.T) {
	// A host named like a subcommand must still be reachable via `hop tunnel`.
	spec, err := parseTunnelArgs([]string{"ls", "c", "1", "2"})
	if err != nil {
		t.Fatalf("parseTunnelArgs: %v", err)
	}
	if spec.Host != "ls" {
		t.Fatalf("Host = %q", spec.Host)
	}
}
