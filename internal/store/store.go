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

	"github.com/thailoc-dev/hop/internal/tunnel"
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
