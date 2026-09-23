package stream_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/stream"
)

func TestSplitMix64Vectors(t *testing.T) {
	cases := []struct {
		seed uint64
		want [5]uint64
	}{
		{0, [5]uint64{0xe220a8397b1dcdaf, 0x6e789e6aa1b965f4, 0x06c45d188009454f, 0xf88bb8a8724c81ec, 0x1b39896a51a8749b}},
		{1, [5]uint64{0x910a2dec89025cc1, 0xbeeb8da1658eec67, 0xf893a2eefb32555e, 0x71c18690ee42c90b, 0x71bb54d8d101b5b9}},
	}
	for _, tc := range cases {
		rng := stream.NewSplitMix64(tc.seed)
		for i, want := range tc.want {
			if got := rng.Next(); got != want {
				t.Errorf("seed %d output %d = %#x, want %#x", tc.seed, i, got, want)
			}
		}
	}
}
