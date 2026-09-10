package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/locnguyen/hop/internal/tunnel"
)

func statuses() []tunnel.Status {
	now := time.Now()
	return []tunnel.Status{
		{
			Spec: tunnel.Spec{Host: "example-backend-dev", Container: "app_mongo_staging",
				RemotePort: 27017, LocalPort: 27018, Env: "stg"},
			State: tunnel.StateHealthy, Since: now.Add(-2 * time.Hour), Retries: 3,
		},
		{
			Spec: tunnel.Spec{Host: "example-backend-prod", Container: "app_mongo",
				RemotePort: 27017, LocalPort: 27019, Env: "prod"},
			State: tunnel.StateRetrying, Since: now, Retries: 12,
			LastError: "Connection refused",
		},
	}
}

func TestRenderTableHasAHeaderAndOneRowPerTunnel(t *testing.T) {
	var buf bytes.Buffer
	renderTable(&buf, statuses(), false)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want header plus two rows:\n%s", len(lines), buf.String())
	}
	for _, column := range []string{"LOCAL", "ENV", "HOST", "CONTAINER", "REMOTE", "STATE", "SINCE", "RETRIES"} {
		if !strings.Contains(lines[0], column) {
			t.Fatalf("header missing %q: %q", column, lines[0])
		}
	}
}

func TestRenderTableShowsPortsAndState(t *testing.T) {
	var buf bytes.Buffer
	renderTable(&buf, statuses(), false)
	out := buf.String()

	for _, want := range []string{"27018", "stg", "app_mongo_staging", "healthy", "2h", "27019", "prod", "retrying"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderTableWithoutColourEmitsNoEscapes(t *testing.T) {
	var buf bytes.Buffer
	renderTable(&buf, statuses(), false)

	if strings.Contains(buf.String(), "\x1b[") {
		t.Fatal("emitted ANSI escapes with colour disabled")
	}
}

func TestRenderTableWithColourMarksProdRed(t *testing.T) {
	var buf bytes.Buffer
	renderTable(&buf, statuses(), true)
	out := buf.String()

	if !strings.Contains(out, "\x1b[31m") {
		t.Fatalf("prod is not red:\n%q", out)
	}
	if !strings.Contains(out, "\x1b[33m") {
		t.Fatalf("stg is not yellow:\n%q", out)
	}
}

func TestRenderTableWithNoTunnelsSaysSo(t *testing.T) {
	var buf bytes.Buffer
	renderTable(&buf, nil, false)

	if !strings.Contains(buf.String(), "no tunnels") {
		t.Fatalf("empty output is unhelpful: %q", buf.String())
	}
}

func TestShortDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{18 * time.Minute, "18m"},
		{2*time.Hour + 14*time.Minute, "2h14m"},
		{50 * time.Hour, "2d2h"},
	}
	for _, tc := range tests {
		if got := shortDuration(tc.in); got != tc.want {
			t.Fatalf("shortDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
