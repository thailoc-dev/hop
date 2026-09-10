// Package store persists tunnel definitions across daemon restarts.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/locnguyen/hop/internal/tunnel"
)

// CurrentVersion is bumped when the on-disk shape changes incompatibly.
const CurrentVersion = 1

// File is the whole of hop's persisted state.
type File struct {
	Version int           `json:"version"`
	Tunnels []tunnel.Spec `json:"tunnels"`
}

// Load reads the state file. A missing file is not an error — it is a first
// run. Corrupt state is also not an error: it is moved aside and treated as
// empty, because refusing to start would be a worse failure than losing a
// list that is trivially rebuilt.
func Load(path string) (File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return File{Version: CurrentVersion}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("read %s: %w", path, err)
	}

	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		if renameErr := os.Rename(path, path+".corrupt"); renameErr != nil {
			return File{}, fmt.Errorf("preserve corrupt state: %w", renameErr)
		}
		return File{Version: CurrentVersion}, nil
	}
	return f, nil
}

// Save writes the state file atomically: a temp file in the same directory,
// fsynced, then renamed over the target. rename(2) within a directory is
// atomic, so a crash at any instant leaves either the old file or the new
// one — never a half-written one.
func Save(path string, f File) error {
	if f.Version == 0 {
		f.Version = CurrentVersion
	}

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp state file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod temp state file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install state file: %w", err)
	}
	return nil
}
