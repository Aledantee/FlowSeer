package traffic

import "time"

// Bucket is a lazily refilled ingress token bucket whose tokens are octets.
// It starts full and is not safe for concurrent use.
type Bucket struct {
	cfg    Policer
	tokens float64
	last   time.Time
	primed bool
}

// NewBucket returns a full bucket configured by cfg.
func NewBucket(cfg Policer) *Bucket {
	return &Bucket{cfg: cfg, tokens: float64(cfg.BurstOctets)}
}

// Admit refills the bucket through now and takes octets when they fit. A
// refusal consumes no tokens. A zero-rate bucket admits every frame.
func (b *Bucket) Admit(now time.Time, octets int) bool {
	if b.cfg.RateBPS == 0 {
		return true
	}
	if b.primed {
		elapsed := now.Sub(b.last)
		if elapsed > 0 {
			refill := float64(b.cfg.RateBPS) * elapsed.Seconds() / 8
			b.tokens = min(b.tokens+refill, float64(b.cfg.BurstOctets))
		}
	}
	b.last = now
	b.primed = true

	if float64(octets) > b.tokens {
		return false
	}
	b.tokens -= float64(octets)

	return true
}

// Tokens returns the current token count without refilling the bucket.
func (b *Bucket) Tokens() float64 {
	return b.tokens
}

// Clone returns an independent bucket with the same configuration and state.
func (b *Bucket) Clone() *Bucket {
	cp := *b

	return &cp
}
