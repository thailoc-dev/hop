package tunnel

import (
	"sync"
	"time"
)

// Clock is the only source of time in this package, so every retry delay and
// sleep-detection threshold is exercised in tests without real waiting.
//
// Now and Mono are separate on purpose. Sleep detection compares them: wall
// time keeps running while a laptop is suspended, monotonic time does not, so
// a growing gap between the two is how hop notices the lid was closed.
type Clock interface {
	Now() time.Time
	Mono() time.Duration
	After(d time.Duration) <-chan time.Time
}

type realClock struct{ start time.Time }

// RealClock returns the production Clock.
func RealClock() Clock { return &realClock{start: time.Now()} }

func (c *realClock) Now() time.Time                         { return time.Now() }
func (c *realClock) Mono() time.Duration                    { return time.Since(c.start) }
func (c *realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// FakeClock is a Clock whose time only moves when a test moves it.
type FakeClock struct {
	mu      sync.Mutex
	start   time.Time
	wall    time.Duration // elapsed wall time since start
	mono    time.Duration // elapsed monotonic time since start
	waiters []fakeWaiter
}

type fakeWaiter struct {
	at time.Duration // mono deadline
	ch chan time.Time
}

func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{start: start}
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.start.Add(c.wall)
}

func (c *FakeClock) Mono() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mono
}

func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, fakeWaiter{at: c.mono + d, ch: ch})
	return ch
}

// Advance moves wall and monotonic time together: ordinary elapsed time.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.wall += d
	c.mono += d
	fired := c.collectExpired()
	now := c.start.Add(c.wall)
	c.mu.Unlock()

	for _, ch := range fired {
		ch <- now
	}
}

// Sleep simulates system suspend: wall time jumps, monotonic time does not.
func (c *FakeClock) Sleep(d time.Duration) {
	c.mu.Lock()
	c.wall += d
	c.mu.Unlock()
}

// collectExpired must be called with c.mu held.
func (c *FakeClock) collectExpired() []chan time.Time {
	var fired []chan time.Time
	var pending []fakeWaiter
	for _, w := range c.waiters {
		if w.at <= c.mono {
			fired = append(fired, w.ch)
			continue
		}
		pending = append(pending, w)
	}
	c.waiters = pending
	return fired
}
