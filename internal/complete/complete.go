package complete

import (
	"context"
	"time"

	"github.com/locnguyen/hop/internal/sshexec"
)

// Timeout is the hard ceiling on any network-backed completion.
//
// This is the most important constant in the package. A shell that hangs on
// Tab is unusable, and there is no recovery from it short of ^C. 300ms is
// comfortably enough for a multiplexed connection to a reachable host and
// short enough to feel instant when it fails.
const Timeout = 300 * time.Millisecond

// Containers lists the containers running on a host, for completion.
//
// It never returns an error: a completion function has nowhere to display one.
// Every failure degrades to the best available answer — a stale cache entry,
// or nothing.
func Containers(ctx context.Context, ex sshexec.Executor, cache *Cache, host string) []Container {
	if fresh, ok := cache.Get(host); ok {
		return fresh
	}

	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	type outcome struct {
		containers []Container
		ok         bool
	}
	results := make(chan outcome, 1)

	go func() {
		result, err := ex.Run(ctx, host, "docker", "ps", "--format", PSFormat)
		if err != nil || result.ExitCode != 0 {
			results <- outcome{}
			return
		}
		results <- outcome{containers: ParsePS(result.Stdout), ok: true}
	}()

	select {
	case got := <-results:
		if !got.ok {
			return nil
		}
		_ = cache.Put(host, got.containers) // a cache write failure is not fatal
		return got.containers

	case <-ctx.Done():
		// Out of time. A stale answer is still better than an empty one.
		if stale, ok := cache.GetStale(host); ok {
			return stale
		}
		return nil
	}
}
