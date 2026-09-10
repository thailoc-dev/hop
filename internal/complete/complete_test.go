package complete

import (
	"context"
	"testing"
	"time"

	"github.com/locnguyen/hop/internal/sshexec"
)

func TestContainersQueriesAndCaches(t *testing.T) {
	ex := sshexec.NewFake()
	ex.SetRunResult(sshexec.Result{Stdout: "mongo\t0.0.0.0:27018->27017/tcp\n"}, nil)
	cache := newCache(t, time.Minute)

	got := Containers(context.Background(), ex, cache, "host-a")

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

	got := Containers(context.Background(), ex, cache, "host-a")

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
	got := Containers(context.Background(), ex, cache, "host-a")
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

	got := Containers(context.Background(), ex, cache, "host-a")

	if len(got) != 1 || got[0].Name != "stale-but-useful" {
		t.Fatalf("got %+v, want the stale entry rather than nothing", got)
	}
}

func TestContainersReturnsNothingOnError(t *testing.T) {
	ex := sshexec.NewFake()
	ex.SetRunResult(sshexec.Result{Stderr: "Permission denied", ExitCode: 255}, nil)

	if got := Containers(context.Background(), ex, newCache(t, time.Minute), "host-a"); got != nil {
		t.Fatalf("got %+v, want nothing when docker ps failed", got)
	}
}
