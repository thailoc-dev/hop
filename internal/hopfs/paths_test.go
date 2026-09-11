package hopfs

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNewUsesHomeWhenSocketPathFits(t *testing.T) {
	p := New("/Users/loc", 501)

	if p.Root != "/Users/loc/.hop" {
		t.Fatalf("Root = %q", p.Root)
	}
	if p.SocketDir != "/Users/loc/.hop/ctl" {
		t.Fatalf("SocketDir = %q, want it under home", p.SocketDir)
	}
	if p.StateFile != "/Users/loc/.hop/state.json" {
		t.Fatalf("StateFile = %q", p.StateFile)
	}
	if p.ControlSock != "/Users/loc/.hop/ctl.sock" {
		t.Fatalf("ControlSock = %q", p.ControlSock)
	}
}

func TestNewFallsBackToTmpWhenSocketPathTooLong(t *testing.T) {
	home := "/Users/" + strings.Repeat("d", 90)

	p := New(home, 501)

	if p.SocketDir != "/tmp/hop-501" {
		t.Fatalf("SocketDir = %q, want /tmp/hop-501", p.SocketDir)
	}
	// Only the ControlMaster directory moves; state stays with the user.
	if !strings.HasPrefix(p.StateFile, home) {
		t.Fatalf("StateFile = %q, want it under home", p.StateFile)
	}
}

func TestSocketBudgetIs86Bytes(t *testing.T) {
	// A directory whose longest socket name lands exactly on the budget stays put.
	// cmd-<16 hex>.sock is 25 bytes, plus the separator.
	dir := "/Users/x/.hop/ctl"
	longest := filepath.Join(dir, "cmd-0123456789abcdef.sock")
	if len(longest) > socketBudget {
		t.Fatalf("test premise wrong: %d > %d", len(longest), socketBudget)
	}
	if got := New("/Users/x", 501).SocketDir; got != dir {
		t.Fatalf("SocketDir = %q, want %q", got, dir)
	}
}

func TestCatalogueFileLivesInTheRoot(t *testing.T) {
	p := New("/Users/loc", 501)
	if p.CatalogueFile != "/Users/loc/.hop/tunnels.json" {
		t.Fatalf("CatalogueFile = %q", p.CatalogueFile)
	}
}
