package tunnel

import (
	"testing"
	"time"
)

func TestFakeClockAdvanceMovesWallAndMono(t *testing.T) {
	start := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	c := NewFakeClock(start)

	c.Advance(90 * time.Second)

	if got := c.Now(); !got.Equal(start.Add(90 * time.Second)) {
		t.Fatalf("Now() = %v", got)
	}
	if got := c.Mono(); got != 90*time.Second {
		t.Fatalf("Mono() = %v, want 90s", got)
	}
}

func TestFakeClockSleepMovesWallOnly(t *testing.T) {
	start := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	c := NewFakeClock(start)

	c.Advance(10 * time.Second)
	c.Sleep(2 * time.Hour) // laptop lid closed

	if got := c.Mono(); got != 10*time.Second {
		t.Fatalf("Mono() = %v, want it unchanged at 10s", got)
	}
	wall := c.Now().Sub(start)
	if wall != 2*time.Hour+10*time.Second {
		t.Fatalf("wall elapsed = %v", wall)
	}
}

func TestFakeClockAfterFiresOnAdvance(t *testing.T) {
	c := NewFakeClock(time.Now())

	ch := c.After(5 * time.Second)
	select {
	case <-ch:
		t.Fatal("fired before the clock advanced")
	default:
	}

	c.Advance(5 * time.Second)

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("did not fire after the clock advanced")
	}
}
