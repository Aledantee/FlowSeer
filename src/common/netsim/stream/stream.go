package stream

import (
	"fmt"
	"math"
	"math/bits"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
)

// Rate sets the emission rate in frames per second or wire bits per second.
// Exactly one field must be positive.
type Rate struct {
	FramesPerSecond uint64
	BitsPerSecond   uint64
}

// Spec describes a finite stream of Ethernet frames. Frame and its slices are
// immutable while a source uses them. Start belongs to the consumer's epoch;
// a source returns offsets relative to that start.
type Spec struct {
	Frame ethernet.Frame
	Rate  Rate
	// Burst is the number of frames per burst; zero means one.
	Burst int
	// Gap is extra idle time after each burst's line spacing.
	Gap time.Duration
	// Exactly one of Count or Duration must be positive. A duration is rounded
	// up to a frame count at the selected rate.
	Count    int
	Duration time.Duration
	Start    time.Duration
	// Seed initializes deterministic field draws when a variation uses them.
	Seed uint64
}

// Validate reports invalid rates, bounds, frame encoding, or timing that cannot
// fit in a time.Duration. A zero Burst means one frame per burst.
func (s Spec) Validate() error {
	if (s.Rate.FramesPerSecond == 0) == (s.Rate.BitsPerSecond == 0) {
		return fmt.Errorf("stream rate must specify exactly one unit")
	}
	if s.Burst < 0 || s.Gap < 0 {
		return fmt.Errorf("stream burst and gap must be nonnegative")
	}
	if s.Count < 0 || s.Duration < 0 || (s.Count == 0) == (s.Duration == 0) {
		return fmt.Errorf("stream end must specify exactly one positive count or duration")
	}
	if _, err := s.Frame.Encode(); err != nil {
		return fmt.Errorf("encode stream frame: %w", err)
	}

	numerator, rate, err := s.spacing()
	if err != nil {
		return err
	}
	count := s.Count
	if s.Duration != 0 {
		count, err = durationCount(s.Duration, rate, numerator)
		if err != nil {
			return err
		}
	}
	burst := s.Burst
	if burst == 0 {
		burst = 1
	}

	last := uint64(count - 1)
	hi, lo := bits.Mul64(last, numerator)
	if hi >= rate {
		return fmt.Errorf("stream last offset exceeds time.Duration")
	}
	base, _ := bits.Div64(hi, lo, rate)
	if base > math.MaxInt64 {
		return fmt.Errorf("stream last offset exceeds time.Duration")
	}
	bursts := last / uint64(burst)
	if bursts != 0 && uint64(s.Gap) > (math.MaxInt64-base)/bursts {
		return fmt.Errorf("stream last offset exceeds time.Duration")
	}
	return nil
}

// Normalize validates the spec, defaults Burst to one, and converts Duration
// to a ceiling frame count. The returned spec has only Count set as its end.
func (s Spec) Normalize() (Spec, error) {
	if err := s.Validate(); err != nil {
		return Spec{}, err
	}
	if s.Burst == 0 {
		s.Burst = 1
	}
	if s.Duration != 0 {
		numerator, rate, err := s.spacing()
		if err != nil {
			return Spec{}, err
		}
		count, err := durationCount(s.Duration, rate, numerator)
		if err != nil {
			return Spec{}, err
		}
		s.Count = count
		s.Duration = 0
	}
	return s, nil
}

// Clone returns a value copy. Its Frame slices remain shared and must be
// treated as immutable by both copies.
func (s Spec) Clone() Spec { return s }

// Source constructs an independent iterator over a normalized spec. The
// caller adds Start to each offset returned by [Source.Next].
func (s Spec) Source() (Source, error) {
	normalized, err := s.Normalize()
	if err != nil {
		return nil, err
	}
	numerator, rate, err := normalized.spacing()
	if err != nil {
		return nil, err
	}
	return &specSource{spec: normalized, numerator: numerator, rate: rate}, nil
}

func (s Spec) spacing() (numerator, rate uint64, err error) {
	if s.Rate.FramesPerSecond != 0 {
		return uint64(time.Second), s.Rate.FramesPerSecond, nil
	}
	wireOctets := uint64(s.Frame.WireOctets())
	if wireOctets > math.MaxUint64/(8*uint64(time.Second)) {
		return 0, 0, fmt.Errorf("stream frame is too large for wire-bit timing")
	}
	return wireOctets * 8 * uint64(time.Second), s.Rate.BitsPerSecond, nil
}

func durationCount(duration time.Duration, rate, numerator uint64) (int, error) {
	hi, lo := bits.Mul64(uint64(duration), rate)
	if hi >= numerator {
		return 0, fmt.Errorf("stream duration yields too many frames")
	}
	count, remainder := bits.Div64(hi, lo, numerator)
	maxCount := uint64(int(^uint(0) >> 1))
	if count > maxCount || (count == maxCount && remainder != 0) {
		return 0, fmt.Errorf("stream duration yields too many frames")
	}
	if remainder != 0 {
		count++
	}
	return int(count), nil
}
