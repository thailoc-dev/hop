package tunnel

import (
	"math/rand"
	"time"
)

const (
	backoffBase   = time.Second
	backoffMax    = 30 * time.Second
	backoffJitter = 0.2
)

// Backoff produces the delay before the next reconnection attempt.
//
// Jitter is not decoration: without it every tunnel that dropped during the
// same network blip would retry in the same instant, repeatedly.
type Backoff struct {
	rng     *rand.Rand
	attempt int
}

func NewBackoff(seed int64) *Backoff {
	return &Backoff{rng: rand.New(rand.NewSource(seed))}
}

// Next returns the delay for this attempt and advances the sequence.
func (b *Backoff) Next() time.Duration {
	delay := backoffBase << b.attempt
	if delay > backoffMax || delay <= 0 { // <=0 guards the shift overflowing
		delay = backoffMax
	} else {
		b.attempt++
	}

	spread := 1 + backoffJitter*(2*b.rng.Float64()-1) // [0.8, 1.2]
	return time.Duration(float64(delay) * spread)
}

// Reset returns the sequence to its base delay. The state machine calls this
// after a tunnel has been healthy long enough, so a container that flaps once
// does not leave the tunnel stuck at the 30s cap.
func (b *Backoff) Reset() { b.attempt = 0 }
