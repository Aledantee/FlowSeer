package stream

import (
	"math/bits"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
)

// Source yields frames at offsets relative to the stream's start. An iterator
// is not safe for concurrent Next calls; Clone gives another cursor.
type Source interface {
	// Next returns the next offset from the stream's start and its frame.
	// After exhaustion, ok stays false.
	Next() (at time.Duration, frame ethernet.Frame, ok bool)
	// Clone returns an independent iterator at the same cursor.
	Clone() Source
}

type specSource struct {
	spec      Spec
	n         int
	numerator uint64
	rate      uint64
}

func (s *specSource) Next() (time.Duration, ethernet.Frame, bool) {
	if s.n >= s.spec.Count {
		return 0, ethernet.Frame{}, false
	}
	hi, lo := bits.Mul64(uint64(s.n), s.numerator)
	base, _ := bits.Div64(hi, lo, s.rate)
	gap := uint64(s.n/s.spec.Burst) * uint64(s.spec.Gap)
	s.n++
	return time.Duration(base + gap), s.spec.Frame, true
}

func (s *specSource) Clone() Source {
	clone := *s
	return &clone
}
