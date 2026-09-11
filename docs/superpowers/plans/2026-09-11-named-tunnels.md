# Named Tunnels Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a tunnel that has been opened before be reopened with one word — `hop redis-stg` — via a CLI-owned catalogue of saved definitions.

**Architecture:** Saved definitions live in `~/.hop/tunnels.json`, written by the CLI through the existing atomic-write path and never read by the daemon. The daemon learns one new field, `Name` on the spec, which rides in the existing `add` request. The bare form gains a one-argument clause (a saved name); every port-addressed command resolves a name to a port client-side from the daemon's `list` response. No new control operation, no state-machine or lifecycle change.

**Tech Stack:** Go 1.25, cobra, standard library. Same as the base project.

**Spec:** `docs/superpowers/specs/2026-09-11-named-tunnels-design.md`

## Global Constraints

- This is a **public repository**. Never commit a real hostname, container name or IP. Use the placeholder vocabulary: `example-backend-dev`, `example-tracker-dev`, `example-api`, `app_mongo_staging`, `app_redis`, `tracker_redis_staging`, `api_redis`.
- **No new dependency.** `cobra` remains the only one.
- **No new control operation.** `control.Request.Op` keeps exactly its seven values.
- The process-spawning rule is unchanged: `exec.Command` only in `internal/sshexec`, `internal/sysprobe`, `cmd/hop/connect.go`. `make check-spawn` enforces it.
- Names match `^[a-z0-9][a-z0-9_-]{0,39}$`, are not reserved words, and contain at least one non-digit. Copied from the spec; every task that touches names uses `validateName` from Task 2.
- `up`, `save`, `forget` are reserved words. `TestReservedWordsCoverEverySubcommand` (already in `cmd/hop/tunnel_test.go`) enforces that every subcommand is reserved.
- Tests that build socket paths use `shortTempDir`, never `t.TempDir()` (104-byte `sun_path` limit).
- `go test ./... -short -race` stays hermetic. Every task ends with `make check` green.
- **The final task runs `make install` and verifies `shasum ~/.local/bin/hop bin/hop` matches.** A change is not delivered until the installed binary is the tested one.
- Use the repository's configured git identity. Never pass `-c user.email`.

---

### Task 1: Name on the spec, CreatedAt on status, and the catalogue store

**Files:**
- Modify: `internal/tunnel/tunnel.go` — `Spec.Name`, `Status.CreatedAt`, `StateSaved`
- Modify: `internal/hopfs/paths.go` — `CatalogueFile`
- Modify: `internal/store/store.go` — generalise the atomic read/write; add `Catalogue`
- Test: `internal/tunnel/tunnel_test.go`, `internal/hopfs/paths_test.go`, `internal/store/store_test.go`

**Interfaces:**
- Consumes: existing `store.Load`/`Save` (kept, now thin wrappers), `tunnel.New`.
- Produces:
  - `tunnel.Spec.Name string` (`json:"name,omitempty"`)
  - `tunnel.Status.CreatedAt time.Time` (`json:"created_at"`), set once in `tunnel.New`
  - `tunnel.StateSaved State = "saved"` — used only by the CLI for catalogue rows
  - `hopfs.Paths.CatalogueFile string` (`~/.hop/tunnels.json`)
  - `store.Catalogue{Version int; Tunnels map[string]tunnel.Spec}`
  - `store.LoadCatalogue(path string) (Catalogue, error)` — missing file → empty; corrupt → moved to `.corrupt`, empty; every returned spec has `Name` set to its key
  - `store.SaveCatalogue(path string, c Catalogue) error` — atomic; nil map is written as `{}`

- [ ] **Step 1: Write the failing tests**

Append to `internal/tunnel/tunnel_test.go`:

```go
func TestCreatedAtIsSetOnceAndSurvivesTransitions(t *testing.T) {
	h := newHarness(t)
	h.waitState(t, StateHealthy)
	created := h.tun.Status().CreatedAt
	if created.IsZero() {
		t.Fatal("CreatedAt is zero")
	}

	// A reconnect moves Since but must not move CreatedAt: `hop save` picks
	// the most recently OPENED tunnel, and a reconnect is not an open.
	h.clock.Advance(time.Minute)
	h.exec.LastForward().EmitStderr("Connection closed by remote host")
	h.exec.LastForward().Die()
	h.waitState(t, StateRetrying)

	if got := h.tun.Status().CreatedAt; !got.Equal(created) {
		t.Fatalf("CreatedAt moved from %v to %v on a state transition", created, got)
	}
	if h.tun.Status().Since.Equal(created) {
		t.Fatal("Since did not move; the test premise is wrong")
	}
}

func TestSpecNameRoundTripsThroughJSON(t *testing.T) {
	in := Spec{Host: "h", Container: "c", RemotePort: 1, LocalPort: 2, Name: "redis-stg"}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Spec
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Name != "redis-stg" {
		t.Fatalf("Name = %q", out.Name)
	}
	// Unnamed specs must not grow a "name": "" field, so existing state files
	// stay byte-identical when nothing was named.
	data, _ = json.Marshal(Spec{Host: "h"})
	if strings.Contains(string(data), `"name"`) {
		t.Fatalf("empty name serialised: %s", data)
	}
}
```

Add `"encoding/json"` to that file's imports (`strings` is already imported).

Append to `internal/hopfs/paths_test.go`:

```go
func TestCatalogueFileLivesInTheRoot(t *testing.T) {
	p := New("/Users/loc", 501)
	if p.CatalogueFile != "/Users/loc/.hop/tunnels.json" {
		t.Fatalf("CatalogueFile = %q", p.CatalogueFile)
	}
}
```

Append to `internal/store/store_test.go`:

```go
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
	// Load/Save are now wrappers over shared read/write helpers; prove they
	// still behave, including the corrupt-file path.
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Save(path, File{Tunnels: []tunnel.Spec{spec(27018)}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil || len(got.Tunnels) != 1 || got.Version != CurrentVersion {
		t.Fatalf("Load = %+v, %v", got, err)
	}
}
```

Add `"strings"` to `store_test.go`'s imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tunnel/ ./internal/hopfs/ ./internal/store/ 2>&1 | tail -6`
Expected: FAIL — `unknown field Name`, `undefined: CatalogueFile`, `undefined: LoadCatalogue`.

- [ ] **Step 3: Write the implementation**

In `internal/tunnel/tunnel.go`:

```go
// Add StateSaved after StateStopped in the State constants:
	// StateSaved is never produced by the state machine. The CLI uses it for
	// catalogue entries that are not currently running, so `hop ls` can show
	// everything in one table.
	StateSaved State = "saved"
```

```go
// Spec gains Name, after Autostart:
	// Name is the catalogue key this tunnel was opened under, or empty. It is
	// display metadata: the daemon never looks it up.
	Name string `json:"name,omitempty"`
```

```go
// Status gains CreatedAt, after Spec:
	// CreatedAt is when the tunnel was opened. Unlike Since it never moves,
	// which is what "most recently opened" needs to mean.
	CreatedAt time.Time `json:"created_at"`
```

Add a `createdAt time.Time` field to the `Tunnel` struct next to `since`, set it in `New` (`createdAt: deps.Clock.Now(),`), and include it in `Status()`:

```go
	return Status{
		Spec: t.spec, State: t.state, Since: t.since, CreatedAt: t.createdAt,
		Retries: t.retries, LastError: t.lastErr, ContainerIP: t.containerIP,
	}
```

In `internal/hopfs/paths.go`, add to `Paths`:

```go
	CatalogueFile string // ~/.hop/tunnels.json — saved, named tunnels
```

and in `New`:

```go
		CatalogueFile: filepath.Join(root, "tunnels.json"),
```

Replace `internal/store/store.go` entirely:

```go
// Package store persists hop's two JSON files: the running-tunnel state the
// daemon owns, and the catalogue of saved tunnels the CLI owns. Both go
// through the same atomic write and the same corrupt-file recovery.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/locnguyen/hop/internal/tunnel"
)

// CurrentVersion is bumped when an on-disk shape changes incompatibly.
const CurrentVersion = 1

// File is the daemon's running-tunnel state.
type File struct {
	Version int           `json:"version"`
	Tunnels []tunnel.Spec `json:"tunnels"`
}

// Catalogue is the CLI's saved, named tunnels. The daemon never reads it.
type Catalogue struct {
	Version int                    `json:"version"`
	Tunnels map[string]tunnel.Spec `json:"tunnels"`
}

// Load reads the state file. A missing file is a first run; a corrupt one is
// moved aside and treated as empty, because refusing to start would be a worse
// failure than losing a list that is trivially rebuilt.
func Load(path string) (File, error) {
	f := File{Version: CurrentVersion}
	if _, err := readJSON(path, &f); err != nil {
		return File{}, err
	}
	if f.Version == 0 {
		f.Version = CurrentVersion
	}
	return f, nil
}

// Save writes the state file atomically.
func Save(path string, f File) error {
	if f.Version == 0 {
		f.Version = CurrentVersion
	}
	return writeJSON(path, f)
}

// LoadCatalogue reads the saved-tunnel catalogue with the same recovery rules
// as Load. Every returned spec carries its key as Name, so callers never have
// to remember to copy it.
func LoadCatalogue(path string) (Catalogue, error) {
	c := Catalogue{Version: CurrentVersion}
	if _, err := readJSON(path, &c); err != nil {
		return Catalogue{}, err
	}
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	if c.Tunnels == nil {
		c.Tunnels = map[string]tunnel.Spec{}
	}
	for name, spec := range c.Tunnels {
		spec.Name = name
		c.Tunnels[name] = spec
	}
	return c, nil
}

// SaveCatalogue writes the catalogue atomically. A nil map is written as an
// empty object so the file's shape never depends on how the caller built it.
func SaveCatalogue(path string, c Catalogue) error {
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	if c.Tunnels == nil {
		c.Tunnels = map[string]tunnel.Spec{}
	}
	return writeJSON(path, c)
}

// readJSON decodes path into v. It reports found=false, err=nil for a missing
// file, and moves a corrupt file to path+".corrupt" before reporting the same.
func readJSON(path string, v any) (found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		if renameErr := os.Rename(path, path+".corrupt"); renameErr != nil {
			return false, fmt.Errorf("preserve corrupt %s: %w", filepath.Base(path), renameErr)
		}
		return false, nil
	}
	return true, nil
}

// writeJSON encodes v to a temp file in path's directory, fsyncs it, and
// renames it over path. rename(2) within a directory is atomic, so a crash at
// any instant leaves either the old file or the new one — never half of one.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install %s: %w", filepath.Base(path), err)
	}
	return nil
}
```

A subtlety in `readJSON` when the target is a `Catalogue` whose caller pre-set
`Version`: `json.Unmarshal` into a struct with a zero `tunnels` in the file
leaves `Tunnels` nil, which is why `LoadCatalogue` normalises it afterwards.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tunnel/ ./internal/hopfs/ ./internal/store/ -race && make check`
Expected: PASS. The existing `TestSaveLeavesNoTempFilesBehind` still passes — the temp-file prefix changed but the cleanup did not.

- [ ] **Step 5: Commit**

```bash
git add internal/tunnel/tunnel.go internal/tunnel/tunnel_test.go internal/hopfs/ internal/store/
git commit -m "feat(store): saved-tunnel catalogue; Name on Spec and CreatedAt on Status"
```

---

### Task 2: Name validation and the one-argument bare form

**Files:**
- Create: `cmd/hop/names.go`
- Modify: `cmd/hop/tunnel.go` — `reservedWords`, `usageForms`, `parseTunnelArgs`'s error
- Modify: `cmd/hop/root.go` — the `RunE` dispatch
- Test: `cmd/hop/names_test.go`, `cmd/hop/tunnel_test.go`

**Interfaces:**
- Consumes: `reservedWords`, `usageForms`, `parseTunnelArgs`, `openTunnel` (existing).
- Produces:
  - `validateName(name string) error` — nil for a valid name; a `codedError` with `exitUsage` otherwise, whose message names the rule that failed
  - `isPort(arg string) bool` — true for a non-empty all-digit string
  - `openSaved(cmd *cobra.Command, name string) error` — **stub in this task** that returns `fail(exitInternal, "not implemented")`; Task 3 replaces it. The dispatch is tested here; the behaviour there.

- [ ] **Step 1: Write the failing tests**

```go
// cmd/hop/names_test.go
package main

import (
	"strings"
	"testing"
)

func TestValidateNameAccepts(t *testing.T) {
	for _, name := range []string{
		"redis-stg", "mongo_dev", "a", "x1", "1a", "pg-prod-replica",
		strings.Repeat("a", 40),
	} {
		if err := validateName(name); err != nil {
			t.Fatalf("validateName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateNameRejects(t *testing.T) {
	tests := []struct {
		name string
		want string // substring of the error
	}{
		{"", "empty"},
		{"Redis", "lower-case"},
		{"redis stg", "lower-case"},
		{"-redis", "start with"},
		{"redis.stg", "lower-case"},
		{strings.Repeat("a", 41), "40"},
		{"ls", "reserved"},
		{"down", "reserved"},
		{"up", "reserved"},
		{"save", "reserved"},
		{"forget", "reserved"},
		// The rule that keeps `hop down 46379` unambiguous.
		{"46379", "port"},
		{"0", "port"},
	}
	for _, tc := range tests {
		err := validateName(tc.name)
		if err == nil {
			t.Fatalf("validateName(%q) accepted", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("validateName(%q) = %q, want it to mention %q", tc.name, err, tc.want)
		}
	}
}

func TestIsPort(t *testing.T) {
	for arg, want := range map[string]bool{
		"46379": true, "1": true, "0": true,
		"redis-stg": false, "": false, "46379a": false, "-1": false,
	} {
		if got := isPort(arg); got != want {
			t.Fatalf("isPort(%q) = %v, want %v", arg, got, want)
		}
	}
}
```

Append to `cmd/hop/tunnel_test.go`:

```go
func TestBareFormDispatchesByArgumentCount(t *testing.T) {
	// One argument is a saved name; four is a spec; anything else is a usage
	// error that names all the forms. openSaved is stubbed until Task 3, so a
	// one-argument call reaching it proves the dispatch without the daemon.
	home := shortTempDir(t)
	t.Setenv("HOME", home)

	_, err := runCmd(t, home, "redis-stg")
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("one argument did not reach openSaved: %v", err)
	}

	for _, args := range [][]string{
		{"h", "c"},
		{"h", "c", "27017"},
		{"h", "c", "27017", "27018", "extra"},
	} {
		_, err := runCmd(t, home, args...)
		if err == nil {
			t.Fatalf("%d arguments accepted", len(args))
		}
		msg := err.Error()
		for _, form := range []string{"hop <name>", "hop <host>"} {
			if !strings.Contains(msg, form) {
				t.Fatalf("%d-argument error %q does not name form %q", len(args), msg, form)
			}
		}
	}
}

func TestReservedWordsIncludeTheNewSubcommands(t *testing.T) {
	for _, word := range []string{"up", "save", "forget"} {
		if !reservedWords[word] {
			t.Fatalf("%q is not reserved; a saved tunnel could shadow it", word)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/hop/ -run 'TestValidateName|TestIsPort|TestBareFormDispatches|TestReservedWordsInclude' 2>&1 | tail -4`
Expected: FAIL — `undefined: validateName`, `undefined: isPort`.

- [ ] **Step 3: Write the implementation**

```go
// cmd/hop/names.go
package main

import (
	"fmt"
	"regexp"
)

// namePattern is the shape of a saved-tunnel name. Lower-case so completion
// and typing never disagree on case; a letter or digit first so a name can
// never be mistaken for a flag.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// isPort reports whether arg is a bare port number: non-empty, all digits.
// Names are required to contain a non-digit precisely so this test is enough
// to tell the two apart wherever both are accepted.
func isPort(arg string) bool {
	if arg == "" {
		return false
	}
	for _, r := range arg {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// validateName applies the spec's naming rules and says which one failed.
func validateName(name string) error {
	switch {
	case name == "":
		return fail(exitUsage, "name is empty")
	case len(name) > 40:
		return fail(exitUsage, "name %q is longer than 40 characters", name)
	case name[0] == '-' || name[0] == '_':
		return fail(exitUsage, "name %q must start with a letter or digit", name)
	case !namePattern.MatchString(name):
		return fail(exitUsage, "name %q must be lower-case letters, digits, - or _", name)
	case reservedWords[name]:
		return fail(exitUsage, "%q is a reserved word and cannot be a tunnel name", name)
	case isPort(name):
		return fail(exitUsage,
			"name %q is all digits and would be mistaken for a port; include a letter, - or _", name)
	}
	return nil
}

// openSaved opens a tunnel from the catalogue by name. Replaced in Task 3.
func openSaved(_ *cobra.Command, _ string) error {
	return fail(exitInternal, "not implemented")
}
```

Add `"github.com/spf13/cobra"` to the imports (and `fmt` is used by nothing yet — remove it if the compiler complains; `fail` builds the messages).

In `cmd/hop/tunnel.go`, extend `reservedWords`:

```go
var reservedWords = map[string]bool{
	"ls": true, "down": true, "logs": true, "restart": true, "tunnel": true,
	"up": true, "save": true, "forget": true,
	"run": true, "shell": true, "push": true, "pull": true, "docker-ip": true,
	"version": true, "help": true, "completion": true,
}
```

and `usageForms`:

```go
const usageForms = "hop <name>                                         open a saved tunnel\n" +
	"       hop <host> <container> <remote-port> <local-port>   open a tunnel\n" +
	"       hop ls | down <name|port> | logs <name|port> | restart <name|port>"
```

In `parseTunnelArgs`, the count error becomes:

```go
	if len(args) != 4 {
		return tunnel.Spec{}, fail(exitUsage,
			"expected 1 argument (a saved name) or 4 (a tunnel spec), got %d\n\nUsage: %s",
			len(args), usageForms)
	}
```

In `cmd/hop/root.go`, the `RunE`:

```go
		RunE: func(cmd *cobra.Command, args []string) error {
			switch len(args) {
			case 0:
				return cmd.Help()
			case 1:
				return openSaved(cmd, args[0])
			default:
				return openTunnel(cmd, args)
			}
		},
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/hop/ -race && make check`
Expected: PASS, including `TestParseTunnelArgsRejectsTheWrongArgumentCount` (its `hop <host>` substring is still present) and `TestReservedWordsCoverEverySubcommand`.

- [ ] **Step 5: Commit**

```bash
git add cmd/hop/names.go cmd/hop/names_test.go cmd/hop/tunnel.go cmd/hop/tunnel_test.go cmd/hop/root.go
git commit -m "feat(cmd): name validation and the one-argument bare form"
```

---

### Task 3: Opening a saved tunnel — `hop <name>`, `hop up`, and `--name`

**Files:**
- Modify: `cmd/hop/names.go` — replace the `openSaved` stub; add `newUpCmd`, `saveToCatalogue`
- Modify: `cmd/hop/tunnel.go` — `--name` flag; `renderOpened` shows the name
- Modify: `cmd/hop/root.go` — register `newUpCmd()`
- Test: `cmd/hop/open_saved_test.go`

**Interfaces:**
- Consumes: `store.LoadCatalogue`/`SaveCatalogue`, `hopfs.Paths.CatalogueFile` (Task 1); `validateName` (Task 2); `connect`, `waitForHealthy`, `renderOpened`, `control.OpAdd`/`OpList` (existing).
- Produces:
  - `openSaved(cmd *cobra.Command, name string) error` — real implementation
  - `saveToCatalogue(paths hopfs.Paths, name string, spec tunnel.Spec) error` — validates, sets `spec.Name`, upserts, writes. Task 4's `hop save` reuses it.
  - `newUpCmd() *cobra.Command`
  - `sameTunnel(a, b tunnel.Spec) bool` — equal host, container, remote and local port
  - a test helper `scriptedHandler` answering each control op differently, in `open_saved_test.go`, reused by Tasks 4 and 5

- [ ] **Step 1: Write the failing tests**

```go
// cmd/hop/open_saved_test.go
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/locnguyen/hop/internal/store"
	"github.com/locnguyen/hop/internal/tunnel"
)

// scriptedHandler answers each control operation with its own canned
// response, and records every request. recordingHandler (lifecycle_test.go)
// returns one response for everything, which cannot express "list says it is
// not running, then add succeeds, then list says healthy".
type scriptedHandler struct {
	byOp     map[string]control.Response
	requests []control.Request
	// afterAdd, when set, replaces the OpList response once an add has been
	// seen -- so a poll after opening sees the tunnel come up.
	afterAdd *control.Response
	added    bool
}

func (h *scriptedHandler) Handle(req control.Request) control.Response {
	h.requests = append(h.requests, req)
	if req.Op == control.OpAdd {
		h.added = true
	}
	if req.Op == control.OpList && h.added && h.afterAdd != nil {
		return *h.afterAdd
	}
	if resp, ok := h.byOp[req.Op]; ok {
		return resp
	}
	return control.Response{OK: true}
}

func (h *scriptedHandler) ops() []string {
	var out []string
	for _, r := range h.requests {
		out = append(out, r.Op)
	}
	return out
}

func serveScripted(t *testing.T, p hopfs.Paths, h *scriptedHandler) {
	t.Helper()
	l, err := listenUnix(t, p.ControlSock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = control.Serve(l, h) }()
}

func redisSpec() tunnel.Spec {
	return tunnel.Spec{Host: "example-tracker-dev", Container: "tracker_redis_staging",
		RemotePort: 6379, LocalPort: 46379, Env: "stg"}
}

func healthy(spec tunnel.Spec) tunnel.Status {
	return tunnel.Status{Spec: spec, State: tunnel.StateHealthy,
		Since: time.Now(), CreatedAt: time.Now()}
}

func saveNamed(t *testing.T, p hopfs.Paths, name string, spec tunnel.Spec) {
	t.Helper()
	spec.Name = name
	if err := store.SaveCatalogue(p.CatalogueFile, store.Catalogue{
		Tunnels: map[string]tunnel.Spec{name: spec},
	}); err != nil {
		t.Fatalf("seed catalogue: %v", err)
	}
}

func TestOpenSavedSendsTheCatalogueSpecWithItsName(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())

	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{
		byOp:     map[string]control.Response{control.OpList: {OK: true}},
		afterAdd: &control.Response{OK: true, Statuses: []tunnel.Status{healthy(named)}},
	}
	serveScripted(t, p, h)

	out, err := runCmd(t, home, "redis-stg")
	if err != nil {
		t.Fatalf("open: %v\n%s", err, out)
	}

	var add *control.Request
	for i := range h.requests {
		if h.requests[i].Op == control.OpAdd {
			add = &h.requests[i]
		}
	}
	if add == nil {
		t.Fatalf("no add request; ops were %v", h.ops())
	}
	if add.Spec.Name != "redis-stg" || add.Spec.LocalPort != 46379 {
		t.Fatalf("add spec = %+v", add.Spec)
	}
	if !strings.Contains(out, "redis-stg") || !strings.Contains(out, "46379") {
		t.Fatalf("open output does not show the name and port:\n%s", out)
	}
}

func TestOpenSavedAlreadyRunningIsNotAnError(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	// Running under the same name.
	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{byOp: map[string]control.Response{
		control.OpList: {OK: true, Statuses: []tunnel.Status{healthy(named)}},
	}}
	serveScripted(t, p, h)

	out, err := runCmd(t, home, "redis-stg")
	if err != nil {
		t.Fatalf("expected exit 0 for an already-running tunnel: %v", err)
	}
	if !strings.Contains(out, "already running") || !strings.Contains(out, "46379") {
		t.Fatalf("output = %q", out)
	}
	for _, op := range h.ops() {
		if op == control.OpAdd {
			t.Fatal("sent an add for a tunnel that was already running")
		}
	}
}

func TestOpenSavedRecognisesTheSameTunnelRunningUnnamed(t *testing.T) {
	// Opened the long way earlier, then saved: identical host/container/ports.
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	h := &scriptedHandler{byOp: map[string]control.Response{
		control.OpList: {OK: true, Statuses: []tunnel.Status{healthy(redisSpec())}},
	}}
	serveScripted(t, p, h)

	out, err := runCmd(t, home, "redis-stg")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "already running") {
		t.Fatalf("output = %q", out)
	}
}

func TestOpenSavedUnknownName(t *testing.T) {
	home, _ := tempHome(t)

	_, err := runCmd(t, home, "nope")

	if err == nil {
		t.Fatal("unknown name accepted")
	}
	if !strings.Contains(err.Error(), `no saved tunnel named "nope"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenSavedUnknownNameThatIsAHostHintsAtTheLongForm(t *testing.T) {
	home, _ := tempHome(t)
	writeSSHConfig(t, home, "Host example-backend-dev\n")

	_, err := runCmd(t, home, "example-backend-dev")

	if err == nil {
		t.Fatal("accepted")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no saved tunnel") || !strings.Contains(msg, "<container> <remote-port> <local-port>") {
		t.Fatalf("a host name used alone should explain the four-argument form: %q", msg)
	}
}

func TestNameFlagValidatesBeforeOpening(t *testing.T) {
	home, p := tempHome(t)
	h := &scriptedHandler{}
	serveScripted(t, p, h)

	_, err := runCmd(t, home, "example-tracker-dev", "tracker_redis_staging", "6379", "46379", "--name", "Bad Name")

	if err == nil {
		t.Fatal("invalid --name accepted")
	}
	if len(h.requests) != 0 {
		t.Fatalf("a tunnel was opened before the name was rejected: %v", h.ops())
	}
	if _, statErr := store.LoadCatalogue(p.CatalogueFile); statErr != nil {
		t.Fatalf("catalogue: %v", statErr)
	}
}

func TestNameFlagOpensThenSaves(t *testing.T) {
	home, p := tempHome(t)
	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{
		byOp:     map[string]control.Response{control.OpList: {OK: true}},
		afterAdd: &control.Response{OK: true, Statuses: []tunnel.Status{healthy(named)}},
	}
	serveScripted(t, p, h)

	out, err := runCmd(t, home, "example-tracker-dev", "tracker_redis_staging", "6379", "46379", "--name", "redis-stg")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	c, _ := store.LoadCatalogue(p.CatalogueFile)
	got, ok := c.Tunnels["redis-stg"]
	if !ok {
		t.Fatalf("not saved; catalogue = %+v", c.Tunnels)
	}
	if got.LocalPort != 46379 || got.Host != "example-tracker-dev" {
		t.Fatalf("saved %+v", got)
	}
}

func TestUpIsTheExplicitFormOfTheBareOpen(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())
	named := redisSpec()
	named.Name = "redis-stg"
	h := &scriptedHandler{
		byOp:     map[string]control.Response{control.OpList: {OK: true}},
		afterAdd: &control.Response{OK: true, Statuses: []tunnel.Status{healthy(named)}},
	}
	serveScripted(t, p, h)

	if out, err := runCmd(t, home, "up", "redis-stg"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !h.added {
		t.Fatal("up did not open the tunnel")
	}
}

func TestSameTunnelIgnoresNameAndEnv(t *testing.T) {
	a := redisSpec()
	b := redisSpec()
	b.Name, b.Env = "whatever", "prod"
	if !sameTunnel(a, b) {
		t.Fatal("name and env must not affect identity")
	}
	b.LocalPort++
	if sameTunnel(a, b) {
		t.Fatal("a different local port is a different tunnel")
	}
}
```

`listenUnix` does not exist yet; add it to `cmd/hop/lifecycle_test.go` next to `serveFakeDaemon` so both helpers share it:

```go
// listenUnix opens the control socket for a fake daemon and closes it when
// the test ends.
func listenUnix(t *testing.T, path string) (net.Listener, error) {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, nil
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/hop/ -run 'TestOpenSaved|TestNameFlag|TestUpIs|TestSameTunnel' 2>&1 | tail -4`
Expected: FAIL — `undefined: sameTunnel`, and `openSaved` returning "not implemented".

- [ ] **Step 3: Write the implementation**

Replace the stub in `cmd/hop/names.go` and add the rest:

```go
// sameTunnel reports whether two specs describe the same forward. Name and Env
// are labels, not identity.
func sameTunnel(a, b tunnel.Spec) bool {
	return a.Host == b.Host && a.Container == b.Container &&
		a.RemotePort == b.RemotePort && a.LocalPort == b.LocalPort
}

// saveToCatalogue validates the name, stamps it on the spec, and upserts.
// Saving an existing name overwrites it: that is how a saved tunnel's ports
// are changed.
func saveToCatalogue(paths hopfs.Paths, name string, spec tunnel.Spec) error {
	if err := validateName(name); err != nil {
		return err
	}
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	c, err := store.LoadCatalogue(paths.CatalogueFile)
	if err != nil {
		return err
	}
	spec.Name = name
	c.Tunnels[name] = spec
	return store.SaveCatalogue(paths.CatalogueFile, c)
}

// openSaved opens a catalogue entry by name. Downstream it is exactly the
// four-argument open: the same add request, the same wait, the same output.
func openSaved(cmd *cobra.Command, name string) error {
	paths, err := hopfs.Default()
	if err != nil {
		return err
	}
	c, err := store.LoadCatalogue(paths.CatalogueFile)
	if err != nil {
		return err
	}

	spec, ok := c.Tunnels[name]
	if !ok {
		return unknownNameError(name)
	}
	if override, _ := cmd.Flags().GetString("env"); override != "" {
		spec.Env = override // this run only; the catalogue is not rewritten
	}

	client, err := connect(paths)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	// Asking for something you already have is not an error.
	list, err := client.Send(control.Request{Op: control.OpList})
	if err != nil {
		return fail(exitInternal, "talk to the daemon: %v", err)
	}
	for _, st := range list.Statuses {
		if st.Spec.Name == name || sameTunnel(st.Spec, spec) {
			cmd.Printf("%s is already running on %d\n", name, st.Spec.LocalPort)
			return nil
		}
	}

	resp, err := client.Send(control.Request{Op: control.OpAdd, Spec: &spec})
	if err != nil {
		return fail(exitInternal, "talk to the daemon: %v", err)
	}
	if !resp.OK {
		return fail(exitFatal, "%s", resp.Error)
	}

	wait, _ := cmd.Flags().GetDuration("wait")
	status, err := waitForHealthy(paths, spec.LocalPort, wait)
	if err != nil {
		return err
	}
	if quiet, _ := cmd.Flags().GetBool("quiet"); !quiet {
		cmd.Print(renderOpened(cmd, status))
	}
	if attach, _ := cmd.Flags().GetBool("attach"); attach {
		return attachTunnel(cmd, paths, spec.LocalPort)
	}
	if status.State != tunnel.StateHealthy {
		return fail(exitNotReady,
			"tunnel is %s after %s; it keeps retrying in the background (hop logs %s)",
			status.State, wait, name)
	}
	return nil
}

// unknownNameError explains a miss. If the name is an ssh host alias, the
// likely mistake is trying to open a host with one argument, so say so.
func unknownNameError(name string) error {
	msg := fmt.Sprintf("no saved tunnel named %q", name)
	if path, err := sshconfig.DefaultPath(); err == nil {
		if hosts, err := sshconfig.Hosts(path); err == nil && slices.Contains(hosts, name) {
			msg += fmt.Sprintf("\n%q is an ssh host; to open a tunnel to it use:\n  hop %s <container> <remote-port> <local-port>",
				name, name)
		}
	}
	return fail(exitUsage, "%s", msg)
}

func newUpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up <name>",
		Short: "Open a saved tunnel (explicit form of `hop <name>`)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return openSaved(cmd, args[0])
		},
	}
	addTunnelFlags(cmd)
	return cmd
}
```

Imports for `names.go`: `fmt`, `regexp`, `slices`, `control`, `hopfs`, `sshconfig`, `store`, `tunnel`, `cobra`.

In `cmd/hop/tunnel.go`, `openTunnel` gains the `--name` handling. After `parseTunnelArgs` and the `--env` override, before connecting:

```go
	// --name is open-then-save. Validate first so a bad name has no side
	// effects; save after the open succeeds so a failed open saves nothing.
	name, _ := cmd.Flags().GetString("name")
	if name != "" {
		if err := validateName(name); err != nil {
			return err
		}
		spec.Name = name
	}
```

and at the end, after the health wait succeeds and before the final `return nil`, replacing the existing tail:

```go
	if status.State != tunnel.StateHealthy {
		return fail(exitNotReady,
			"tunnel is %s after %s; it keeps retrying in the background (hop logs %d)",
			status.State, wait, spec.LocalPort)
	}
	if name != "" {
		if err := saveToCatalogue(paths, name, spec); err != nil {
			return err
		}
	}
	return nil
```

In `addTunnelFlags`:

```go
	cmd.Flags().String("name", "", "save the tunnel under this name once it is up")
```

`renderOpened` shows the name when present:

```go
func renderOpened(cmd *cobra.Command, status tunnel.Status) string {
	label := envLabel(cmd, status.Spec.Env)
	what := status.Spec.Container
	if status.Spec.Name != "" {
		what = fmt.Sprintf("%s (%s)", status.Spec.Name, status.Spec.Container)
	}
	return fmt.Sprintf("  %s %s  →  localhost:%d   %s\n",
		label, what, status.Spec.LocalPort, status.State)
}
```

Register in `root.go`: `root.AddCommand(newUpCmd())`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/hop/ -race && make check`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/hop/
git commit -m "feat(cmd): open saved tunnels by name; --name saves on open"
```

---

### Task 4: `hop save` and `hop forget`

**Files:**
- Create: `cmd/hop/save.go`
- Modify: `cmd/hop/root.go` — register both
- Test: `cmd/hop/save_test.go`

**Interfaces:**
- Consumes: `saveToCatalogue`, `scriptedHandler`, `serveScripted`, `healthy`, `redisSpec` (Task 3); `store.LoadCatalogue`/`SaveCatalogue` (Task 1); `isPort`, `parsePort`.
- Produces: `newSaveCmd()`, `newForgetCmd() *cobra.Command`; `newestStatus(statuses []tunnel.Status) (tunnel.Status, bool)` — by `CreatedAt`.

- [ ] **Step 1: Write the failing tests**

```go
// cmd/hop/save_test.go
package main

import (
	"strings"
	"testing"
	"time"

	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/store"
	"github.com/locnguyen/hop/internal/tunnel"
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
	// Forgetting is about the future. A running instance keeps running.
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/hop/ -run 'TestSave|TestForget|TestNewestStatus' 2>&1 | tail -3`
Expected: FAIL — `undefined: newestStatus`; `save`/`forget` unknown commands.

- [ ] **Step 3: Write the implementation**

```go
// cmd/hop/save.go
package main

import (
	"github.com/locnguyen/hop/internal/control"
	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/locnguyen/hop/internal/store"
	"github.com/locnguyen/hop/internal/tunnel"
	"github.com/spf13/cobra"
)

// newestStatus picks the most recently OPENED tunnel — by CreatedAt, which
// never moves, not by Since, which resets on every reconnect.
func newestStatus(statuses []tunnel.Status) (tunnel.Status, bool) {
	var best tunnel.Status
	found := false
	for _, st := range statuses {
		if !found || st.CreatedAt.After(best.CreatedAt) {
			best, found = st, true
		}
	}
	return best, found
}

func newSaveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "save <name> [local-port]",
		Short: "Save a running tunnel under a name",
		Long: "Saves a running tunnel so it can be reopened with `hop <name>`.\n" +
			"With no port, the most recently opened tunnel is saved.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			// Validate before contacting the daemon, so a bad name costs nothing.
			if err := validateName(name); err != nil {
				return err
			}

			paths, err := hopfs.Default()
			if err != nil {
				return err
			}
			client, err := control.Dial(paths.ControlSock)
			if err != nil {
				return fail(exitUsage, "no tunnels are running; open one first, then save it")
			}
			resp, err := client.Send(control.Request{Op: control.OpList})
			_ = client.Close()
			if err != nil {
				return fail(exitInternal, "talk to the daemon: %v", err)
			}

			var chosen tunnel.Status
			if len(args) == 2 {
				port, err := parsePort(args[1], "local-port")
				if err != nil {
					return err
				}
				found := false
				for _, st := range resp.Statuses {
					if st.Spec.LocalPort == port {
						chosen, found = st, true
					}
				}
				if !found {
					return fail(exitFatal, "no tunnel on local port %d", port)
				}
			} else {
				st, ok := newestStatus(resp.Statuses)
				if !ok {
					return fail(exitUsage, "no tunnels are running; open one first, then save it")
				}
				chosen = st
			}

			if err := saveToCatalogue(paths, name, chosen.Spec); err != nil {
				return err
			}
			if quiet, _ := cmd.Flags().GetBool("quiet"); !quiet {
				cmd.Printf("saved %s: %s %s %d %d\n", name,
					chosen.Spec.Host, chosen.Spec.Container, chosen.Spec.RemotePort, chosen.Spec.LocalPort)
			}
			return nil
		},
	}
}

func newForgetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "forget <name>",
		Short: "Remove a saved tunnel from the catalogue",
		Long: "Removes the name. A running instance of the tunnel keeps running;\n" +
			"forgetting is about the future, not the present.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			paths, err := hopfs.Default()
			if err != nil {
				return err
			}
			c, err := store.LoadCatalogue(paths.CatalogueFile)
			if err != nil {
				return err
			}
			if _, ok := c.Tunnels[name]; !ok {
				return fail(exitUsage, "no saved tunnel named %q", name)
			}
			delete(c.Tunnels, name)
			return store.SaveCatalogue(paths.CatalogueFile, c)
		},
	}
}
```

Register in `root.go`: `root.AddCommand(newSaveCmd(), newForgetCmd())`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/hop/ -race && make check`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/hop/save.go cmd/hop/save_test.go cmd/hop/root.go
git commit -m "feat(cmd): hop save and hop forget"
```

---

### Task 5: Names wherever ports are accepted, and `ls` shows the catalogue

**Files:**
- Create: `cmd/hop/target.go`
- Modify: `cmd/hop/down.go`, `cmd/hop/logs.go`, `cmd/hop/restart.go` — resolve a name-or-port
- Modify: `cmd/hop/ls.go` — merge catalogue rows
- Modify: `cmd/hop/render.go` — `NAME` column; `saved` rows
- Test: `cmd/hop/target_test.go`, `cmd/hop/render_test.go`, `cmd/hop/lifecycle_test.go`

**Interfaces:**
- Consumes: `isPort`, `parsePort`, `sameTunnel`, `store.LoadCatalogue`, `tunnel.StateSaved`, `scriptedHandler`.
- Produces:
  - `resolveTarget(paths hopfs.Paths, running []tunnel.Status, arg string) (int, error)` — a port passes through; a name matches a running tunnel by `Spec.Name`, then by catalogue identity; otherwise `no running tunnel named %q`
  - `mergeSaved(running []tunnel.Status, catalogue map[string]tunnel.Spec) []tunnel.Status` — fills `Name` on unnamed running rows from a matching catalogue entry, then appends non-running catalogue entries as `StateSaved` rows sorted by name
  - `renderTable` gains a leading `NAME` column

- [ ] **Step 1: Write the failing tests**

```go
// cmd/hop/target_test.go
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
	// Nothing running means nothing to stop, name or port alike.
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
		"zeta": {Host: "h", Container: "c", RemotePort: 1, LocalPort: 2, Name: "zeta"},
		"alpha": {Host: "h", Container: "c", RemotePort: 1, LocalPort: 3, Name: "alpha"},
	}
	got := mergeSaved(nil, catalogue)
	if got[0].Spec.Name != "alpha" || got[1].Spec.Name != "zeta" {
		t.Fatalf("order = %s, %s", got[0].Spec.Name, got[1].Spec.Name)
	}
}
```

Append to `cmd/hop/render_test.go`:

```go
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
	if strings.Contains(lines[2], "\t0") || strings.HasSuffix(strings.TrimSpace(lines[2]), " 0") {
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
```

Append to `cmd/hop/lifecycle_test.go`:

```go
func TestLsIncludesSavedTunnelsWithoutADaemon(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())

	out, err := runCmd(t, home, "ls")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "redis-stg") || !strings.Contains(out, "saved") {
		t.Fatalf("saved tunnel missing from ls with no daemon:\n%s", out)
	}
}

func TestLsJSONIncludesSavedRows(t *testing.T) {
	home, p := tempHome(t)
	saveNamed(t, p, "redis-stg", redisSpec())

	out, err := runCmd(t, home, "ls", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var rows []tunnel.Status
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(rows) != 1 || rows[0].State != tunnel.StateSaved || rows[0].Spec.Name != "redis-stg" {
		t.Fatalf("rows = %+v", rows)
	}
}
```

Add `"encoding/json"` to `lifecycle_test.go`'s imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/hop/ -run 'TestResolveTarget|TestDownAccepts|TestRestartAccepts|TestLogsAccepts|TestDownByName|TestMergeSaved|TestRenderTableShowsName|TestRenderTableSaved|TestLsIncludes|TestLsJSON' 2>&1 | tail -4`
Expected: FAIL — `undefined: resolveTarget`, `undefined: mergeSaved`.

- [ ] **Step 3: Write the implementation**

```go
// cmd/hop/target.go
package main

import (
	"sort"

	"github.com/locnguyen/hop/internal/hopfs"
	"github.com/locnguyen/hop/internal/store"
	"github.com/locnguyen/hop/internal/tunnel"
)

// resolveTarget turns a name-or-port argument into a local port.
//
// A bare number is a port and needs no lookup. A name is matched against the
// running tunnels first by the Name they were opened under, then by identity
// against the catalogue -- so a tunnel opened the long way and saved later is
// still addressable by its name.
func resolveTarget(paths hopfs.Paths, running []tunnel.Status, arg string) (int, error) {
	if isPort(arg) {
		return parsePort(arg, "local-port")
	}

	for _, st := range running {
		if st.Spec.Name == arg {
			return st.Spec.LocalPort, nil
		}
	}

	c, err := store.LoadCatalogue(paths.CatalogueFile)
	if err != nil {
		return 0, err
	}
	if saved, ok := c.Tunnels[arg]; ok {
		for _, st := range running {
			if sameTunnel(st.Spec, saved) {
				return st.Spec.LocalPort, nil
			}
		}
	}
	return 0, fail(exitFatal, "no running tunnel named %q", arg)
}

// mergeSaved builds the `hop ls` view: running tunnels, annotated with a
// catalogue name where an unnamed one matches a saved spec, followed by saved
// tunnels that are not running, in state "saved" and sorted by name.
func mergeSaved(running []tunnel.Status, catalogue map[string]tunnel.Spec) []tunnel.Status {
	out := make([]tunnel.Status, 0, len(running)+len(catalogue))
	isRunning := map[string]bool{}

	for _, st := range running {
		if st.Spec.Name == "" {
			for name, saved := range catalogue {
				if sameTunnel(st.Spec, saved) {
					st.Spec.Name = name
					break
				}
			}
		}
		if st.Spec.Name != "" {
			isRunning[st.Spec.Name] = true
		}
		out = append(out, st)
	}

	var saved []tunnel.Status
	for name, spec := range catalogue {
		if isRunning[name] {
			continue
		}
		spec.Name = name
		saved = append(saved, tunnel.Status{Spec: spec, State: tunnel.StateSaved})
	}
	sort.Slice(saved, func(i, j int) bool { return saved[i].Spec.Name < saved[j].Spec.Name })

	return append(out, saved...)
}
```

In `cmd/hop/render.go`, `renderTable` becomes:

```go
func renderTable(w io.Writer, statuses []tunnel.Status, colour bool) {
	if len(statuses) == 0 {
		fmt.Fprintln(w, "no tunnels")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tLOCAL\tENV\tHOST\tCONTAINER\tREMOTE\tSTATE\tSINCE\tRETRIES")

	for _, st := range statuses {
		env := st.Spec.Env
		if env == "" {
			env = "—"
		}

		since, retries := "—", "—"
		state := string(st.State)
		switch st.State {
		case tunnel.StateSaved:
			// Not running: no runtime columns, and the state itself is muted so
			// the eye lands on what is actually up.
			state = paint(state, ansiDim, colour)
		default:
			retries = fmt.Sprintf("%d", st.Retries)
			if st.State == tunnel.StateHealthy && !st.Since.IsZero() {
				since = shortDuration(time.Since(st.Since))
			}
			if st.State == tunnel.StateRetrying && st.LastError != "" {
				state = paint(state, ansiDim, colour)
			}
		}

		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			st.Spec.Name,
			st.Spec.LocalPort,
			paint(env, colourFor(st.Spec.Env), colour),
			st.Spec.Host, st.Spec.Container, st.Spec.RemotePort,
			state, since, retries)
	}
	_ = tw.Flush()
}
```

In `cmd/hop/ls.go`, the `RunE` body becomes:

```go
			paths, err := hopfs.Default()
			if err != nil {
				return err
			}
			asJSON, _ := cmd.Flags().GetBool("json")

			catalogue, err := store.LoadCatalogue(paths.CatalogueFile)
			if err != nil {
				return err
			}

			var running []tunnel.Status
			if client, err := control.Dial(paths.ControlSock); err == nil {
				resp, err := client.Send(control.Request{Op: control.OpList})
				_ = client.Close()
				if err != nil {
					return fail(exitInternal, "talk to the daemon: %v", err)
				}
				running = probeAll(paths, resp.Statuses)
			}
			// No daemon means nothing is running, which is an answer, not a
			// failure -- and the catalogue is still worth showing.

			rows := mergeSaved(running, catalogue.Tunnels)

			if asJSON {
				if rows == nil {
					rows = []tunnel.Status{}
				}
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(rows)
			}
			renderTable(cmd.OutOrStdout(), rows, useColour(cmd))
			return nil
```

Add the `store` import to `ls.go`.

In `cmd/hop/down.go`, the `RunE` body becomes:

```go
			all, _ := cmd.Flags().GetBool("all")
			if !all && len(args) != 1 {
				return fail(exitUsage, "specify a tunnel name or local port, or --all to stop every tunnel")
			}

			paths, err := hopfs.Default()
			if err != nil {
				return err
			}

			// No daemon means nothing is running, which is the requested state.
			client, err := control.Dial(paths.ControlSock)
			if err != nil {
				return nil
			}
			defer func() { _ = client.Close() }()

			req := control.Request{Op: control.OpRemove, All: all}
			if !all {
				list, err := client.Send(control.Request{Op: control.OpList})
				if err != nil {
					return fail(exitInternal, "talk to the daemon: %v", err)
				}
				port, err := resolveTarget(paths, list.Statuses, args[0])
				if err != nil {
					return err
				}
				req.LocalPort = port
				// The connection served one request; open another for the remove.
				_ = client.Close()
				if client, err = control.Dial(paths.ControlSock); err != nil {
					return nil
				}
			}

			resp, err := client.Send(req)
			if err != nil {
				return fail(exitInternal, "talk to the daemon: %v", err)
			}
			if !resp.OK {
				return fail(exitFatal, "%s", resp.Error)
			}
			return nil
```

(The control protocol is one request per connection — see `control.Serve` — which is why the client is re-dialled between the list and the remove.)

Update its `Use` to `"down <name|local-port>"`.

In `cmd/hop/restart.go`, replace the `parsePort` line and the send:

```go
			paths, err := hopfs.Default()
			if err != nil {
				return err
			}
			client, err := connect(paths)
			if err != nil {
				return err
			}
			list, err := client.Send(control.Request{Op: control.OpList})
			_ = client.Close()
			if err != nil {
				return fail(exitInternal, "talk to the daemon: %v", err)
			}
			port, err := resolveTarget(paths, list.Statuses, args[0])
			if err != nil {
				return err
			}

			client, err = connect(paths)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			resp, err := client.Send(control.Request{Op: control.OpRestart, LocalPort: port})
```

Update its `Use` to `"restart <name|local-port>"` and remove the now-unused `parsePort` call at the top of the function.

In `cmd/hop/logs.go`, replace the `parsePort` call at the top of `RunE` with a one-time resolution before the loop:

```go
			paths, err := hopfs.Default()
			if err != nil {
				return err
			}

			var port int
			if isPort(args[0]) {
				if port, err = parsePort(args[0], "local-port"); err != nil {
					return err
				}
			} else {
				client, err := control.Dial(paths.ControlSock)
				if err != nil {
					return fail(exitNoDaemon, "no hop daemon is running")
				}
				list, err := client.Send(control.Request{Op: control.OpList})
				_ = client.Close()
				if err != nil {
					return fail(exitInternal, "talk to the daemon: %v", err)
				}
				if port, err = resolveTarget(paths, list.Statuses, args[0]); err != nil {
					return err
				}
			}
```

and move the existing `paths, err := hopfs.Default()` that followed it, since it is now above. Update `Use` to `"logs <name|local-port>"`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/hop/ -race && make check`
Expected: PASS. `TestRenderTableHasAHeaderAndOneRowPerTunnel` still passes: it checks for column names by substring, and `NAME` is now among them.

- [ ] **Step 5: Commit**

```bash
git add cmd/hop/
git commit -m "feat(cmd): names accepted by down/logs/restart; ls shows the catalogue"
```

---

### Task 6: Completion, end-to-end proof, docs, and delivery

**Files:**
- Modify: `cmd/hop/completion.go` — names at position 0; names for `down`/`logs`/`restart`/`forget`; ports for `save`'s second argument
- Modify: `cmd/hop/save.go`, `cmd/hop/down.go`, `cmd/hop/logs.go`, `cmd/hop/restart.go` — wire the completers
- Modify: `cmd/hop/e2e_test.go` — the named-tunnel round trip
- Modify: `README.md` — document names
- Test: `cmd/hop/completion_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: `completeSavedNames(toComplete string) []string` (with `env container@host` descriptions); `completeTargets(cmd, args, toComplete)` replacing `completeLocalPorts` on the three port-addressed commands — offers running tunnels by name where one exists, else by port.

- [ ] **Step 1: Write the failing tests**

Append to `cmd/hop/completion_test.go`:

```go
func TestCompleteSavedNamesAtPositionZero(t *testing.T) {
	home, p := tempHome(t)
	t.Setenv("HOME", home)
	writeSSHConfig(t, home, "Host example-tracker-dev\n")
	saveNamed(t, p, "redis-stg", redisSpec())

	got, _ := completeTunnelArgs(&cobra.Command{}, nil, "")

	var names, hosts bool
	for _, entry := range got {
		if strings.HasPrefix(entry, "redis-stg\t") && strings.Contains(entry, "tracker_redis_staging@example-tracker-dev") {
			names = true
		}
		if entry == "example-tracker-dev" {
			hosts = true
		}
	}
	if !names {
		t.Fatalf("saved name missing or undescribed: %v", got)
	}
	if !hosts {
		t.Fatalf("hosts no longer offered alongside names: %v", got)
	}
}

func TestCompleteTargetsPrefersNamesOverPorts(t *testing.T) {
	home, p := tempHome(t)
	t.Setenv("HOME", home)
	named := redisSpec()
	named.Name = "redis-stg"
	unnamed := tunnel.Spec{Host: "h", Container: "c", RemotePort: 1, LocalPort: 27018}
	serveFakeDaemon(t, p, control.Response{OK: true, Statuses: []tunnel.Status{
		healthy(named), healthy(unnamed),
	}})

	got, _ := completeTargets(&cobra.Command{}, nil, "")

	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if !strings.HasPrefix(got[0], "redis-stg\t") {
		t.Fatalf("named tunnel offered as %q, want its name", got[0])
	}
	if !strings.HasPrefix(got[1], "27018\t") {
		t.Fatalf("unnamed tunnel offered as %q, want its port", got[1])
	}
}

func TestForgetCompletesSavedNames(t *testing.T) {
	home, p := tempHome(t)
	t.Setenv("HOME", home)
	saveNamed(t, p, "redis-stg", redisSpec())

	out, err := runCmd(t, home, "__complete", "forget", "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !slices.Contains(completionLines(out), "redis-stg") {
		t.Fatalf("forget completion = %q", out)
	}
}
```

Append to `cmd/hop/e2e_test.go`, inside the file's existing pattern:

```go
func TestEndToEndNamedTunnelRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test spawns processes; run without -short")
	}
	if _, err := exec.LookPath("nc"); err != nil {
		t.Skip("nc is required to hold the stub forward open")
	}

	binary := buildHop(t)
	stubDir := stubSSHDir(t)
	home := shortTempDir(t)
	t.Cleanup(func() { _, _ = runHop(t, binary, home, stubDir, "down", "--all") })

	// Open with --name: opens, then saves.
	out, err := runHop(t, binary, home, stubDir,
		"example-backend-dev", "app_mongo_staging", "27017", "45919", "--name", "mongo-stg")
	if err != nil {
		t.Fatalf("open --name: %v\n%s", err, out)
	}
	if !strings.Contains(out, "mongo-stg") {
		t.Fatalf("open output lacks the name:\n%s", out)
	}

	// ls shows the name on the running row.
	listing, _ := runHop(t, binary, home, stubDir, "ls")
	if !strings.Contains(listing, "mongo-stg") || !strings.Contains(listing, "45919") {
		t.Fatalf("ls:\n%s", listing)
	}

	// down by name.
	if out, err := runHop(t, binary, home, stubDir, "down", "mongo-stg"); err != nil {
		t.Fatalf("down by name: %v\n%s", err, out)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		listing, _ = runHop(t, binary, home, stubDir, "ls")
		if strings.Contains(listing, "saved") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(listing, "saved") {
		t.Fatalf("after down, ls should show the tunnel as saved:\n%s", listing)
	}

	// Reopen with one word, on the same port.
	out, err = runHop(t, binary, home, stubDir, "mongo-stg")
	if err != nil {
		t.Fatalf("reopen by name: %v\n%s", err, out)
	}
	if !strings.Contains(out, "45919") {
		t.Fatalf("reopened on a different port:\n%s", out)
	}
	_, _ = runHop(t, binary, home, stubDir, "down", "mongo-stg")

	// forget, then the name is gone.
	if out, err := runHop(t, binary, home, stubDir, "forget", "mongo-stg"); err != nil {
		t.Fatalf("forget: %v\n%s", err, out)
	}
	out, err = runHop(t, binary, home, stubDir, "mongo-stg")
	if err == nil {
		t.Fatalf("forgotten name still opened:\n%s", out)
	}
	if !strings.Contains(out, `no saved tunnel named "mongo-stg"`) {
		t.Fatalf("unexpected error after forget:\n%s", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/hop/ -run 'TestCompleteSavedNames|TestCompleteTargets|TestForgetCompletes' 2>&1 | tail -3`
Expected: FAIL — `undefined: completeTargets`; position 0 lacks names.

- [ ] **Step 3: Write the implementation**

In `cmd/hop/completion.go`:

```go
// completeSavedNames offers catalogue entries, each described so a name is
// recognisable without remembering what it points at.
func completeSavedNames(toComplete string) []string {
	paths, err := hopfs.Default()
	if err != nil {
		return nil
	}
	c, err := store.LoadCatalogue(paths.CatalogueFile)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(c.Tunnels))
	for name := range c.Tunnels {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []string
	for _, name := range names {
		if !strings.HasPrefix(name, toComplete) {
			continue
		}
		spec := c.Tunnels[name]
		env := spec.Env
		if env == "" {
			env = "—"
		}
		out = append(out, fmt.Sprintf("%s\t%s %s@%s", name, env, spec.Container, spec.Host))
	}
	return out
}

// completeTargets offers running tunnels for the commands that address one:
// by name where the tunnel has one, by port otherwise.
func completeTargets(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	const noFiles = cobra.ShellCompDirectiveNoFileComp

	paths, err := hopfs.Default()
	if err != nil {
		return nil, noFiles
	}
	client, err := control.Dial(paths.ControlSock)
	if err != nil {
		return nil, noFiles
	}
	defer func() { _ = client.Close() }()
	resp, err := client.Send(control.Request{Op: control.OpList})
	if err != nil || !resp.OK {
		return nil, noFiles
	}

	catalogue, _ := store.LoadCatalogue(paths.CatalogueFile)
	rows := mergeSaved(resp.Statuses, catalogue.Tunnels)

	var out []string
	for _, st := range rows {
		if st.State == tunnel.StateSaved {
			continue // not running: nothing to stop, restart or read logs from
		}
		env := st.Spec.Env
		if env == "" {
			env = "—"
		}
		if st.Spec.Name != "" {
			if strings.HasPrefix(st.Spec.Name, toComplete) {
				out = append(out, fmt.Sprintf("%s\t%s %s :%d", st.Spec.Name, env, st.Spec.Container, st.Spec.LocalPort))
			}
			continue
		}
		port := strconv.Itoa(st.Spec.LocalPort)
		if strings.HasPrefix(port, toComplete) {
			out = append(out, fmt.Sprintf("%s\t%s %s", port, env, st.Spec.Container))
		}
	}
	return out, noFiles
}

func completeForget(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completeSavedNames(toComplete), cobra.ShellCompDirectiveNoFileComp
}
```

In `completeTunnelArgs`, position 0 becomes:

```go
	case 0:
		out := completeSavedNames(toComplete)
		return append(out, completeHosts(toComplete)...), noFiles
```

Add `sort`, `store`, `tunnel` to the file's imports. `completeLocalPorts` stays for `save`'s second argument:

```go
// in save.go, on the command:
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 1 {
				return completeLocalPorts(cmd, args, toComplete)
			}
			return nil, cobra.ShellCompDirectiveNoFileComp // a new name: nothing to offer
		},
```

`newForgetCmd` gets `ValidArgsFunction: completeForget`. `newDownCmd`, `newRestartCmd`, `newLogsCmd` change `completeLocalPorts` to `completeTargets`. `newUpCmd` gets `ValidArgsFunction: completeForget` too (it completes saved names; the helper name is about what it offers, not who calls it — rename it `completeSavedNamesArg` if that reads better).

- [ ] **Step 4: Run tests to verify they pass**

Run: `make check`
Expected: PASS — including `TestEndToEndNamedTunnelRoundTrip`.

- [ ] **Step 5: Document**

In `README.md`, under **Usage**, add a subsection after "Open a tunnel":

````markdown
### Save it under a name

```bash
hop example-tracker-dev tracker_redis_staging 6379 46379 --name redis-stg
# or, after opening it the long way:
hop save redis-stg            # names the most recently opened tunnel
hop save redis-stg 46379      # or a specific one

hop redis-stg                 # from now on
hop up redis-stg              # same thing, explicit
hop forget redis-stg          # remove the name; a running instance keeps running
```

Names are lower-case letters, digits, `-` and `_`, and must contain a
non-digit so they can never be mistaken for a port. Saving an existing name
overwrites it — that is how you change a saved tunnel's ports.

`down`, `logs` and `restart` accept a name wherever they accept a port, and
`hop ls` shows saved tunnels that are not running as `saved` rows.
````

Update the "Manage tunnels" table's three argument columns from `<local-port>` to `<name|port>`, the Commands block in the top matter, and add `~/.hop/tunnels.json — saved, named tunnels` to the files listing under **How it works**.

- [ ] **Step 6: Deliver**

This is the step that was missing from the last plan.

```bash
make install
shasum ~/.local/bin/hop bin/hop     # the two hashes MUST match
hop version
hop ls                               # shows saved rows, or "no tunnels"
```

Then, against a real host of your own (use your actual host and container —
never commit them):

```bash
hop <host> <container> <remote> <local> --name test-tunnel
hop ls
hop down test-tunnel
hop test-tunnel
hop down test-tunnel
hop forget test-tunnel
```

- [ ] **Step 7: Commit**

```bash
git add cmd/hop/ README.md
git commit -m "feat(complete): saved names in completion; named-tunnel end-to-end; docs"
```

---

## Verification

- [ ] `make check` green: gofmt, spawn rule, vet, `-short -race` suite, end-to-end.
- [ ] `TestEndToEndNamedTunnelRoundTrip` passes: open with `--name`, `ls`, `down` by name, reopen by name on the same port, `forget`, reopen fails correctly.
- [ ] `shasum ~/.local/bin/hop bin/hop` matches after `make install`.
- [ ] Every host and container name in the repo is a placeholder. List them and read the list:
  `git grep -hoE '[a-z]+-[a-z]+-(dev|prod)' | sort -u` and `git grep -hoE '[a-z]+_(mongo|redis|nginx|nextjs)[a-z_]*' | sort -u`. Everything must be recognisably `example-*` / `app_*` / `api_*` / `tracker_*` / `webapp_*` / `filter_*` (a couple of tokenisation artefacts from prose are fine). Anything that looks like a real project is a leak. The names that were scrubbed are deliberately written down nowhere in this repository, including here.
- [ ] `control.Request.Op` still has exactly seven values; `go doc ./internal/control | grep Op` to confirm.
- [ ] A `state.json` from before this change loads: every spec simply has an empty `Name`.
