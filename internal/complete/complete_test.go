package complete

import (
	"context"
	"testing"
	"time"

	"github.com/thailoc-dev/hop/internal/sshexec"
)

func TestContainersQueriesAndCaches(t *testing.T) {
	ex := sshexec.NewFake()
	ex.SetRunResult(sshexec.Result{Stdout: "mongo\t0.0.0.0:27018->27017/tcp\n"}, nil)
	cache := newCache(t, time.Minute)

	got := Containers(context.Background(), ex, cache, "host-a", nil)

	if len(got) != 1 || got[0].Name != "mongo" {
		t.Fatalf("got %+v", got)
	}
	if _, ok := cache.Get("host-a"); !ok {
		t.Fatal("result was not cached")
	}
}

func TestContainersServesTheCacheWithoutTouchingTheNetwork(t *testing.T) {
	ex := sshexec.NewFake()
	ex.SetRunResult(sshexec.Result{Stdout: "fresh\t1/tcp\n"}, nil)
	cache := newCache(t, time.Minute)
	_ = cache.Put("host-a", []Container{{Name: "cached"}})

	got := Containers(context.Background(), ex, cache, "host-a", nil)

	if len(got) != 1 || got[0].Name != "cached" {
		t.Fatalf("got %+v, want the cached entry", got)
	}
}

// The test this whole task exists for.
func TestContainersNeverBlocksPastTheTimeout(t *testing.T) {
	ex := sshexec.NewFake()
	ex.SetRunDelay(10 * time.Second) // an unreachable host
	ex.SetRunResult(sshexec.Result{Stdout: "mongo\t1/tcp\n"}, nil)
	cache := newCache(t, time.Minute)

	start := time.Now()
	got := Containers(context.Background(), ex, cache, "host-a", nil)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("blocked for %v; Tab would have frozen the shell", elapsed)
	}
	if got != nil {
		t.Fatalf("got %+v, want nothing when the lookup timed out", got)
	}
}

func TestContainersFallsBackToStaleOnTimeout(t *testing.T) {
	ex := sshexec.NewFake()
	ex.SetRunDelay(10 * time.Second)
	cache := &Cache{Dir: t.TempDir(), TTL: time.Nanosecond}
	_ = cache.Put("host-a", []Container{{Name: "stale-but-useful"}})
	time.Sleep(time.Millisecond)

	got := Containers(context.Background(), ex, cache, "host-a", nil)

	if len(got) != 1 || got[0].Name != "stale-but-useful" {
		t.Fatalf("got %+v, want the stale entry rather than nothing", got)
	}
}

func TestContainersReturnsNothingOnError(t *testing.T) {
	ex := sshexec.NewFake()
	ex.SetRunResult(sshexec.Result{Stderr: "Permission denied", ExitCode: 255}, nil)

	if got := Containers(context.Background(), ex, newCache(t, time.Minute), "host-a", nil); got != nil {
		t.Fatalf("got %+v, want nothing when docker ps failed", got)
	}
}

// recordWarm captures the hosts handed to the background warmer.
func recordWarm(hosts *[]string) func(string) {
	return func(host string) { *hosts = append(*hosts, host) }
}

// The regression test for the cold-start deadlock: a first lookup cannot beat
// the 300ms ceiling, so unless something is scheduled to fill the cache out of
// band, completion can never succeed on any press, ever.
func TestContainersSchedulesAWarmWhenTheLookupTimesOut(t *testing.T) {
	ex := sshexec.NewFake()
	ex.SetRunDelay(10 * time.Second) // a cold connection: ~3s in reality
	cache := newCache(t, time.Minute)

	var warmed []string
	got := Containers(context.Background(), ex, cache, "host-a", recordWarm(&warmed))

	if got != nil {
		t.Fatalf("got %+v, want nothing on this press", got)
	}
	if len(warmed) != 1 || warmed[0] != "host-a" {
		t.Fatalf("warmed = %v, want exactly [host-a]; without this the cache "+
			"never fills and completion is broken forever", warmed)
	}
}

func TestContainersDoesNotWarmOnAFreshCacheHit(t *testing.T) {
	ex := sshexec.NewFake()
	cache := newCache(t, time.Minute)
	_ = cache.Put("host-a", []Container{{Name: "mongo"}})

	var warmed []string
	Containers(context.Background(), ex, cache, "host-a", recordWarm(&warmed))

	if len(warmed) != 0 {
		t.Fatalf("warmed %v despite a fresh cache hit", warmed)
	}
}

func TestContainersDoesNotWarmTwiceInARow(t *testing.T) {
	ex := sshexec.NewFake()
	ex.SetRunDelay(10 * time.Second)
	cache := newCache(t, time.Minute)

	var warmed []string
	Containers(context.Background(), ex, cache, "host-a", recordWarm(&warmed))
	Containers(context.Background(), ex, cache, "host-a", recordWarm(&warmed))

	if len(warmed) != 1 {
		t.Fatalf("warmed %d times, want 1; each keystroke would spawn an ssh", len(warmed))
	}
}

func TestContainersStillHonoursTheCeilingWhileWarming(t *testing.T) {
	ex := sshexec.NewFake()
	ex.SetRunDelay(10 * time.Second)
	cache := newCache(t, time.Minute)

	start := time.Now()
	Containers(context.Background(), ex, cache, "host-a", func(string) {})

	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("blocked for %v; the warm must not extend the ceiling", elapsed)
	}
}

// Fetch is what the detached warmer runs: the same lookup with no ceiling.
func TestFetchWritesTheCacheWithNoDeadline(t *testing.T) {
	ex := sshexec.NewFake()
	ex.SetRunDelay(600 * time.Millisecond) // twice the completion ceiling
	ex.SetRunResult(sshexec.Result{Stdout: "mongo\t27017/tcp\n"}, nil)
	cache := newCache(t, time.Minute)

	if err := Fetch(context.Background(), ex, cache, "host-a"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	got, ok := cache.Get("host-a")
	if !ok {
		t.Fatal("Fetch did not populate the cache")
	}
	if len(got) != 1 || got[0].Name != "mongo" {
		t.Fatalf("cached %+v", got)
	}
}

// Caching an empty list when docker clearly printed something is how a
// parsing failure becomes permanent: the empty result is served for the whole
// TTL and looks exactly like a host with nothing running. That is what the
// unquoted-format bug did for a day.
func TestFetchRefusesToCacheUnparseableOutput(t *testing.T) {
	ex := sshexec.NewFake()
	// Output with no tab separator: what docker returns when the format string
	// loses its \t to the remote shell.
	ex.SetRunResult(sshexec.Result{Stdout: "filter_nginx_stagingt0.0.0.0:80->80/tcp\n"}, nil)
	cache := newCache(t, time.Minute)

	err := Fetch(context.Background(), ex, cache, "host-a")

	if err == nil {
		t.Fatal("accepted output it could not parse")
	}
	if _, ok := cache.GetStale("host-a"); ok {
		t.Fatal("cached an empty list from unparseable output; completion would " +
			"then report 'no containers' for the whole TTL")
	}
}

func TestFetchCachesAGenuinelyEmptyHost(t *testing.T) {
	// A host running no containers is a real answer and must be cached, or
	// every Tab press would re-fetch it.
	ex := sshexec.NewFake()
	ex.SetRunResult(sshexec.Result{Stdout: ""}, nil)
	cache := newCache(t, time.Minute)

	if err := Fetch(context.Background(), ex, cache, "host-a"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	got, ok := cache.Get("host-a")
	if !ok {
		t.Fatal("an empty host was not cached")
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want an empty list", got)
	}
}
