package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/thailoc-dev/hop/internal/tunnel"
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

func TestRenderTableShowsNameColumnAndSavedRows(t *testing.T) {
	named := redisSpec()
	named.Name = "redis-stg"
	rows := []tunnel.Status{
		healthy(named),
		{Spec: tunnel.Spec{Host: "example-backend-dev", Container: "app_mongo_staging",
			RemotePort: 27017, LocalPort: 27018, Env: "dev", Name: "mongo-dev"},
			State: tunnel.StateSaved},
	}
	var buf bytes.Buffer
	renderTable(&buf, rows, false)
	out := buf.String()

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.HasPrefix(lines[0], "NAME") {
		t.Fatalf("header does not start with NAME: %q", lines[0])
	}
	if !strings.Contains(lines[1], "redis-stg") || !strings.Contains(lines[1], "healthy") {
		t.Fatalf("running row: %q", lines[1])
	}
	if !strings.Contains(lines[2], "mongo-dev") || !strings.Contains(lines[2], "saved") {
		t.Fatalf("saved row: %q", lines[2])
	}
	// A saved row has no runtime: SINCE and RETRIES are dashes, not zeros.
	if strings.HasSuffix(strings.TrimSpace(lines[2]), " 0") {
		t.Fatalf("saved row shows a zero retry count: %q", lines[2])
	}
}

func TestRenderTableSavedStateIsDimmedWithColour(t *testing.T) {
	rows := []tunnel.Status{{Spec: tunnel.Spec{Name: "mongo-dev", Host: "h", Container: "c"}, State: tunnel.StateSaved}}
	var buf bytes.Buffer
	renderTable(&buf, rows, true)
	if !strings.Contains(buf.String(), ansiDim+"saved"+ansiReset) {
		t.Fatalf("saved state not dimmed:\n%q", buf.String())
	}
}

// stripANSI removes colour escapes so visible column positions can be compared.
func stripANSI(s string) string {
	var out strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && r == 'm':
			inEscape = false
		case !inEscape:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// Colour escapes are invisible, so they must not count as width: every
// column has to start at the same visible offset in the header and in each
// row, with colour on and off alike.
func TestRenderTableColumnsAlignWithColourOn(t *testing.T) {
	named := redisSpec()
	named.Name = "redis-stg"
	rows := []tunnel.Status{healthy(named)}

	var plain, coloured bytes.Buffer
	renderTable(&plain, rows, false)
	renderTable(&coloured, rows, true)

	plainLines := strings.Split(strings.TrimRight(plain.String(), "\n"), "\n")
	colourLines := strings.Split(strings.TrimRight(coloured.String(), "\n"), "\n")

	for i := range plainLines {
		got := stripANSI(colourLines[i])
		if got != plainLines[i] {
			t.Fatalf("line %d differs once colour is stripped:\n  plain:    %q\n  coloured: %q",
				i, plainLines[i], got)
		}
	}

	header, row := plainLines[0], stripANSI(colourLines[1])
	if strings.Index(header, "HOST") != strings.Index(row, "example-tracker-dev") {
		t.Fatalf("HOST column misaligned:\n%s\n%s", header, row)
	}
}
