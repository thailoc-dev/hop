package complete

import (
	"context"
	"fmt"
	"strings"
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

// Fetch runs the container lookup with no completion ceiling and writes the
// result to the cache. It is what the detached warmer calls.
func Fetch(ctx context.Context, ex sshexec.Executor, cache *Cache, host string) error {
	result, err := ex.Run(ctx, host, "docker", "ps", "--format", PSFormat)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("docker ps on %s: %s", host, strings.TrimSpace(result.Stderr))
	}

	containers := ParsePS(result.Stdout)

	// Output that yields nothing is a parsing failure, not an empty host: a
	// host with no containers prints nothing at all. Caching the empty result
	// would serve "no containers" for the whole TTL and make the breakage look
	// like a fact about the host.
	if len(containers) == 0 && strings.TrimSpace(result.Stdout) != "" {
		return fmt.Errorf("could not parse docker ps output from %s: %q",
			host, firstLine(result.Stdout))
	}

	return cache.Put(host, containers)
}

// firstLine keeps an error message to one readable line.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Containers lists the containers running on a host, for completion.
//
// It never returns an error: a completion function has nowhere to display one.
// Every failure degrades to the best available answer — a stale cache entry,
// or nothing.
//
// warm schedules a background fetch that is not bound by Timeout. It exists
// because a FIRST connection to a host costs seconds, not milliseconds: ssh
// must do TCP, key exchange and authentication before docker even runs. With
// only the foreground path, the ceiling is missed, nothing is cached, and the
// next press is equally cold — completion could never succeed on any press.
// Scheduling the fetch out of band is what lets the cache bootstrap, while the
// ceiling here stays exactly as it was.
func Containers(ctx context.Context, ex sshexec.Executor, cache *Cache, host string, warm func(string)) []Container {
	if fresh, ok := cache.Get(host); ok {
		return fresh
	}

	lookupCtx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	type outcome struct {
		containers []Container
		ok         bool
	}
	results := make(chan outcome, 1)

	go func() {
		result, err := ex.Run(lookupCtx, host, "docker", "ps", "--format", PSFormat)
		if err != nil || result.ExitCode != 0 {
			results <- outcome{}
			return
		}
		results <- outcome{containers: ParsePS(result.Stdout), ok: true}
	}()

	select {
	case got := <-results:
		if got.ok {
			_ = cache.Put(host, got.containers) // a cache write failure is not fatal
			return got.containers
		}

	case <-lookupCtx.Done():
	}

	// The foreground lookup did not deliver. Schedule the out-of-band fetch so
	// the next press has an answer, then give back whatever is on hand.
	scheduleWarm(cache, host, warm)

	if stale, ok := cache.GetStale(host); ok {
		return stale
	}
	return nil
}

func scheduleWarm(cache *Cache, host string, warm func(string)) {
	if warm == nil || !cache.ShouldWarm(host) {
		return
	}
	_ = cache.MarkWarming(host)
	warm(host)
}
