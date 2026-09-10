package store

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/locnguyen/hop/internal/tunnel"
)

func spec(port int) tunnel.Spec {
	return tunnel.Spec{Host: "h", Container: "c", RemotePort: 27017, LocalPort: port, Env: "stg"}
}

func TestLoadReturnsEmptyForAMissingFile(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Tunnels) != 0 {
		t.Fatalf("got %d tunnels, want 0", len(got.Tunnels))
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	want := File{Version: 1, Tunnels: []tunnel.Spec{spec(27018), spec(6380)}}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Tunnels) != 2 || got.Tunnels[0].LocalPort != 27018 || got.Tunnels[1].LocalPort != 6380 {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestLoadRecoversFromATruncatedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"tunnels":[{"host":"h"`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load must not fail on corrupt state: %v", err)
	}
	if len(got.Tunnels) != 0 {
		t.Fatalf("got %d tunnels from corrupt state", len(got.Tunnels))
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatal("corrupt state was discarded without being preserved for inspection")
	}
}

func TestSaveIsAtomicUnderConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = Save(path, File{Version: 1, Tunnels: []tunnel.Spec{spec(20000 + n)}})
		}(i)
	}
	wg.Wait()

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load after concurrent writes: %v", err)
	}
	if len(got.Tunnels) != 1 {
		t.Fatalf("got %d tunnels, want exactly 1", len(got.Tunnels))
	}
}

func TestSaveLeavesNoTempFilesBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := Save(path, File{Version: 1, Tunnels: []tunnel.Spec{spec(1)}}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatalf("directory contains %v, want only state.json", entries)
	}
}
