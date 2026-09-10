package complete

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newCache(t *testing.T, ttl time.Duration) *Cache {
	t.Helper()
	return &Cache{Dir: t.TempDir(), TTL: ttl}
}

func TestCachePutThenGet(t *testing.T) {
	c := newCache(t, time.Minute)
	want := []Container{{Name: "mongo", Ports: []int{27017}}}

	if err := c.Put("host-a", want); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, ok := c.Get("host-a")
	if !ok {
		t.Fatal("Get missed immediately after Put")
	}
	if len(got) != 1 || got[0].Name != "mongo" || got[0].Ports[0] != 27017 {
		t.Fatalf("got %+v", got)
	}
}

func TestCacheMissesAfterTTL(t *testing.T) {
	c := newCache(t, time.Nanosecond)
	_ = c.Put("host-a", []Container{{Name: "mongo"}})

	time.Sleep(time.Millisecond)

	if _, ok := c.Get("host-a"); ok {
		t.Fatal("expired entry was served as fresh")
	}
}

func TestGetStaleServesAnExpiredEntry(t *testing.T) {
	// When the network call times out, a stale answer beats no answer.
	c := newCache(t, time.Nanosecond)
	_ = c.Put("host-a", []Container{{Name: "mongo"}})
	time.Sleep(time.Millisecond)

	got, ok := c.GetStale("host-a")
	if !ok {
		t.Fatal("GetStale refused an expired entry")
	}
	if got[0].Name != "mongo" {
		t.Fatalf("got %+v", got)
	}
}

func TestCacheIsPerHost(t *testing.T) {
	c := newCache(t, time.Minute)
	_ = c.Put("host-a", []Container{{Name: "mongo"}})

	if _, ok := c.Get("host-b"); ok {
		t.Fatal("one host's cache answered for another")
	}
}

func TestCacheIgnoresCorruptEntries(t *testing.T) {
	c := newCache(t, time.Minute)
	_ = c.Put("host-a", []Container{{Name: "mongo"}})

	entries, _ := os.ReadDir(c.Dir)
	if len(entries) != 1 {
		t.Fatalf("expected one cache file, got %v", entries)
	}
	if err := os.WriteFile(filepath.Join(c.Dir, entries[0].Name()), []byte("{oh no"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, ok := c.Get("host-a"); ok {
		t.Fatal("corrupt cache entry was served")
	}
}

func TestCacheGetIsFineWithNoDirectory(t *testing.T) {
	c := &Cache{Dir: filepath.Join(t.TempDir(), "never-created"), TTL: time.Minute}
	if _, ok := c.Get("host-a"); ok {
		t.Fatal("got a hit from a directory that does not exist")
	}
}

func TestShouldWarmIsTrueForAnUnknownHost(t *testing.T) {
	c := newCache(t, time.Minute)
	if !c.ShouldWarm("host-a") {
		t.Fatal("refused to warm a host that has never been fetched")
	}
}

func TestMarkWarmingSuppressesRepeatWarms(t *testing.T) {
	// Every keystroke triggers a completion. Without debouncing, holding Tab
	// would spawn a background ssh per press.
	c := newCache(t, time.Minute)

	if err := c.MarkWarming("host-a"); err != nil {
		t.Fatalf("MarkWarming: %v", err)
	}

	if c.ShouldWarm("host-a") {
		t.Fatal("a warm was already in flight but another was allowed")
	}
}

func TestMarkWarmingIsPerHost(t *testing.T) {
	c := newCache(t, time.Minute)
	_ = c.MarkWarming("host-a")

	if !c.ShouldWarm("host-b") {
		t.Fatal("warming one host suppressed warming another")
	}
}
