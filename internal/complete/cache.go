package complete

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CacheTTL is how long a container listing stays fresh. Containers do not
// come and go every few seconds, and a minute is short enough that a newly
// started one shows up before anyone notices it missing.
const CacheTTL = 60 * time.Second

// Cache stores one container listing per host on disk, so repeated Tab
// presses do not each cost a round trip.
type Cache struct {
	Dir string
	TTL time.Duration
}

type cacheEntry struct {
	FetchedAt  time.Time   `json:"fetched_at"`
	Containers []Container `json:"containers"`
}

func (c *Cache) path(host string) string {
	sum := sha256.Sum256([]byte(host))
	return filepath.Join(c.Dir, hex.EncodeToString(sum[:8])+".json")
}

// Get returns a listing only if it is still fresh.
func (c *Cache) Get(host string) ([]Container, bool) {
	entry, ok := c.read(host)
	if !ok {
		return nil, false
	}
	if time.Since(entry.FetchedAt) > c.TTL {
		return nil, false
	}
	return entry.Containers, true
}

// GetStale returns a listing regardless of age. Used when the live lookup
// timed out: a slightly out-of-date completion beats none at all.
func (c *Cache) GetStale(host string) ([]Container, bool) {
	entry, ok := c.read(host)
	if !ok {
		return nil, false
	}
	return entry.Containers, true
}

func (c *Cache) read(host string) (cacheEntry, bool) {
	data, err := os.ReadFile(c.path(host))
	if err != nil {
		return cacheEntry{}, false
	}
	var entry cacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return cacheEntry{}, false // a corrupt entry is simply a miss
	}
	return entry, true
}

func (c *Cache) Put(host string, containers []Container) error {
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	data, err := json.Marshal(cacheEntry{FetchedAt: time.Now(), Containers: containers})
	if err != nil {
		return fmt.Errorf("encode cache entry: %w", err)
	}
	if err := os.WriteFile(c.path(host), data, 0o600); err != nil {
		return fmt.Errorf("write cache entry: %w", err)
	}
	return nil
}
