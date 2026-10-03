package stream

// splitMix64 is a deterministic 64-bit generator. Its zero value is seeded
// with zero, and a generator is not safe for concurrent Next calls.
type splitMix64 struct{ state uint64 }

// newSplitMix64 initializes the reference SplitMix64 sequence from seed.
func newSplitMix64(seed uint64) splitMix64 { return splitMix64{state: seed} }

// Next advances the state once and returns the mixed value.
func (s *splitMix64) Next() uint64 {
	s.state += 0x9e3779b97f4a7c15
	z := s.state
	z ^= z >> 30
	z *= 0xbf58476d1ce4e5b9
	z ^= z >> 27
	z *= 0x94d049bb133111eb
	return z ^ (z >> 31)
}
