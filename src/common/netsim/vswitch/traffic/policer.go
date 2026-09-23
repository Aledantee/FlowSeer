package traffic

import (
	"fmt"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Bucket is a lazily refilled ingress token bucket whose tokens are octets.
// It starts full and is not safe for concurrent use.
type Bucket struct {
	cfg    Policer
	tokens float64
	last   time.Time
	primed bool
}

// NewBucket returns a full bucket after validating cfg.
func NewBucket(cfg Policer) (*Bucket, error) {
	if cfg.RateBPS > 0 && cfg.BurstOctets < 1 {
		return nil, errs.New().
			Attr("field", "burst_octets").
			Attr("rate_bps", cfg.RateBPS).
			Attr("burst_octets", cfg.BurstOctets).
			Msg("a rate-limited policer requires a positive burst")
	}

	return &Bucket{cfg: cfg, tokens: float64(cfg.BurstOctets)}, nil
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

// RetentionKey returns a canonical encoding of every normalized input the layer's
// runtime state depends on: its own configuration as Diff sees it.
func RetentionKey(cfg Config) string {
	if len(cfg.Mirrors) == 0 && len(cfg.Policers) == 0 && len(cfg.Queues) == 0 {
		return ""
	}
	norm := cfg.Normalize()
	var b strings.Builder
	b.WriteString("config=")
	b.WriteString("mirrors=[")
	for i, m := range norm.Mirrors {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(string(snapshotMirror(m)))
	}
	b.WriteString("];policers=[")
	for i, name := range sortedKeys(norm.Policers) {
		if i > 0 {
			b.WriteByte(',')
		}
		p := norm.Policers[name]
		fmt.Fprintf(&b, "%s:{rate=%d,burst=%d}", name, p.RateBPS, p.BurstOctets)
	}
	b.WriteString("];queues=[")
	for i, name := range sortedKeys(norm.Queues) {
		if i > 0 {
			b.WriteByte(',')
		}
		q := norm.Queues[name]
		b.WriteString(name)
		b.WriteString(":{")
		pcps := unionPCPs(q.MaxRateBPS, q.BufferOctets)
		for j, pcp := range pcps {
			if j > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%d=rate:%d,buffer:%d", pcp, q.MaxRateBPS[pcp], q.BufferOctets[pcp])
		}
		b.WriteByte('}')
	}
	b.WriteString("]")

	return b.String()
}
