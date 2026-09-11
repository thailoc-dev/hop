package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thailoc-dev/hop/internal/tunnel"
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

func TestLoadCatalogueIsEmptyForAMissingFile(t *testing.T) {
	c, err := LoadCatalogue(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("LoadCatalogue: %v", err)
	}
	if len(c.Tunnels) != 0 {
		t.Fatalf("got %d tunnels, want 0", len(c.Tunnels))
	}
	if c.Tunnels == nil {
		t.Fatal("Tunnels map is nil; callers would panic on insert")
	}
}

func TestSaveCatalogueThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnels.json")
	want := Catalogue{Version: 1, Tunnels: map[string]tunnel.Spec{
		"redis-stg": {Host: "example-tracker-dev", Container: "tracker_redis_staging", RemotePort: 6379, LocalPort: 46379, Env: "stg"},
		"mongo-dev": {Host: "example-backend-dev", Container: "app_mongo_staging", RemotePort: 27017, LocalPort: 27018, Env: "dev"},
	}}

	if err := SaveCatalogue(path, want); err != nil {
		t.Fatalf("SaveCatalogue: %v", err)
	}
	got, err := LoadCatalogue(path)
	if err != nil {
		t.Fatalf("LoadCatalogue: %v", err)
	}

	if len(got.Tunnels) != 2 {
		t.Fatalf("got %d tunnels", len(got.Tunnels))
	}
	if got.Tunnels["redis-stg"].LocalPort != 46379 {
		t.Fatalf("redis-stg = %+v", got.Tunnels["redis-stg"])
	}
}

func TestLoadCatalogueSetsEachSpecsName(t *testing.T) {
	// The key is the name. Callers should not have to remember to copy it.
	path := filepath.Join(t.TempDir(), "tunnels.json")
	_ = SaveCatalogue(path, Catalogue{Tunnels: map[string]tunnel.Spec{
		"redis-stg": {Host: "h", Container: "c", RemotePort: 1, LocalPort: 2},
	}})

	got, _ := LoadCatalogue(path)

	if got.Tunnels["redis-stg"].Name != "redis-stg" {
		t.Fatalf("Name = %q, want the map key", got.Tunnels["redis-stg"].Name)
	}
}

func TestLoadCatalogueRecoversFromCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnels.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"tunnels":{"x"`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := LoadCatalogue(path)
	if err != nil {
		t.Fatalf("LoadCatalogue must not fail on corrupt input: %v", err)
	}
	if len(got.Tunnels) != 0 {
		t.Fatalf("got %d tunnels from corrupt input", len(got.Tunnels))
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatal("corrupt catalogue was discarded rather than preserved")
	}
}

func TestSaveCatalogueWritesAnEmptyMapNotNull(t *testing.T) {
	// A nil map marshals as null, which a later LoadCatalogue would turn back
	// into a nil map. Keep the on-disk shape stable.
	path := filepath.Join(t.TempDir(), "tunnels.json")
	if err := SaveCatalogue(path, Catalogue{}); err != nil {
		t.Fatalf("SaveCatalogue: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"tunnels": {}`) {
		t.Fatalf("empty catalogue serialised as:\n%s", data)
	}
}

func TestStateFileStillRoundTripsAfterGeneralising(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Save(path, File{Tunnels: []tunnel.Spec{spec(27018)}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil || len(got.Tunnels) != 1 || got.Version != CurrentVersion {
		t.Fatalf("Load = %+v, %v", got, err)
	}
}
