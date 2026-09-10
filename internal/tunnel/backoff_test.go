package tunnel

import (
	"testing"
	"time"
)

func TestBackoffDoublesFromOneSecond(t *testing.T) {
	b := NewBackoff(1)

	wantCentres := []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second,
		8 * time.Second, 16 * time.Second, 30 * time.Second,
	}
	for i, centre := range wantCentres {
		got := b.Next()
		lo := time.Duration(float64(centre) * 0.8)
		hi := time.Duration(float64(centre) * 1.2)
		if got < lo || got > hi {
			t.Fatalf("attempt %d: got %v, want within [%v,%v] of %v", i, got, lo, hi, centre)
		}
	}
}

func TestBackoffCapsAtThirtySeconds(t *testing.T) {
	b := NewBackoff(1)
	for i := 0; i < 20; i++ {
		if d := b.Next(); d > 36*time.Second {
			t.Fatalf("attempt %d exceeded cap: %v", i, d)
		}
	}
}

func TestBackoffJitterVaries(t *testing.T) {
	a, b := NewBackoff(1), NewBackoff(2)
	same := true
	for i := 0; i < 5; i++ {
		if a.Next() != b.Next() {
			same = false
		}
	}
	if same {
		t.Fatal("different seeds produced identical delay sequences")
	}
}

func TestBackoffResetReturnsToBase(t *testing.T) {
	b := NewBackoff(1)
	for i := 0; i < 5; i++ {
		b.Next()
	}

	b.Reset()

	got := b.Next()
	if got < 800*time.Millisecond || got > 1200*time.Millisecond {
		t.Fatalf("after Reset got %v, want ~1s", got)
	}
}
