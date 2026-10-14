package stream

import (
	"bytes"
	"math/bits"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
)

// Source yields encodable, independent frames at nondecreasing offsets from
// the consumer epoch. Frames share no mutable tags or payload with other yields
// or cursors. An iterator is not safe for concurrent Next calls.
type Source interface {
	// Next returns the next offset from the consumer epoch and its frame.
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
	rng       splitMix64
}

func (s *specSource) Next() (time.Duration, ethernet.Frame, bool) {
	if s.n >= s.spec.Count {
		return 0, ethernet.Frame{}, false
	}
	hi, lo := bits.Mul64(uint64(s.n), s.numerator)
	base, _ := bits.Div64(hi, lo, s.rate)
	gap := uint64(s.n/s.spec.Burst) * uint64(s.spec.Gap)
	frame := copyFrame(s.spec.Frame)
	for _, variation := range s.spec.Variations {
		frame = variation.apply(s.n, frame, &s.rng)
	}
	s.n++
	return s.spec.Start + time.Duration(base+gap), frame, true
}

func (s *specSource) Clone() Source {
	clone := *s
	return &clone
}

func copyFrame(frame ethernet.Frame) ethernet.Frame {
	frame.Tags = slices.Clone(frame.Tags)
	frame.Payload = bytes.Clone(frame.Payload)
	return frame
}
