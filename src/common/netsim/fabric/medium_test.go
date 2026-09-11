package fabric_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
)

func TestMediumVelocityFactor(t *testing.T) {
	tests := []struct {
		name   string
		medium fabric.Medium
		want   float64
	}{
		{name: "twisted pair", medium: fabric.TwistedPair, want: 0.64},
		{name: "empty medium defaults to twisted pair", medium: "", want: 0.64},
		{name: "multimode fiber", medium: fabric.MultimodeFiber, want: 0.67},
		{name: "singlemode fiber", medium: fabric.SinglemodeFiber, want: 0.67},
		{name: "twinax", medium: fabric.Twinax, want: 0.77},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.medium.VelocityFactor()
			if got != tc.want {
				t.Errorf("%q.VelocityFactor() = %v, want %v", tc.medium, got, tc.want)
			}
		})
	}
}

func TestMediumReach(t *testing.T) {
	tests := []struct {
		name     string
		medium   fabric.Medium
		speedBPS uint64
		want     float64
	}{
		// Twisted pair rows: 100 m at 10 M, 100 M, 1 G, 10 G; 0 above 10 G.
		{name: "twisted pair 10 Mbps", medium: fabric.TwistedPair, speedBPS: 10_000_000, want: 100},
		{name: "twisted pair 100 Mbps", medium: fabric.TwistedPair, speedBPS: 100_000_000, want: 100},
		{name: "twisted pair 1 Gbps", medium: fabric.TwistedPair, speedBPS: 1_000_000_000, want: 100},
		{name: "twisted pair 10 Gbps", medium: fabric.TwistedPair, speedBPS: 10_000_000_000, want: 100},
		{name: "twisted pair 25 Gbps no row", medium: fabric.TwistedPair, speedBPS: 25_000_000_000, want: 0},
		{name: "twisted pair 40 Gbps no row", medium: fabric.TwistedPair, speedBPS: 40_000_000_000, want: 0},

		// Empty medium behaving as twisted pair.
		{name: "empty medium 10 Mbps", medium: "", speedBPS: 10_000_000, want: 100},
		{name: "empty medium 100 Mbps", medium: "", speedBPS: 100_000_000, want: 100},
		{name: "empty medium 1 Gbps", medium: "", speedBPS: 1_000_000_000, want: 100},
		{name: "empty medium 10 Gbps", medium: "", speedBPS: 10_000_000_000, want: 100},
		{name: "empty medium 25 Gbps no row", medium: "", speedBPS: 25_000_000_000, want: 0},

		// Multimode fiber rows: 550 m at 1 G, 300 m at 10 G; 0 at other speeds.
		{name: "multimode fiber 10 Mbps no row", medium: fabric.MultimodeFiber, speedBPS: 10_000_000, want: 0},
		{name: "multimode fiber 100 Mbps no row", medium: fabric.MultimodeFiber, speedBPS: 100_000_000, want: 0},
		{name: "multimode fiber 1 Gbps", medium: fabric.MultimodeFiber, speedBPS: 1_000_000_000, want: 550},
		{name: "multimode fiber 10 Gbps", medium: fabric.MultimodeFiber, speedBPS: 10_000_000_000, want: 300},
		{name: "multimode fiber 25 Gbps no row", medium: fabric.MultimodeFiber, speedBPS: 25_000_000_000, want: 0},

		// Singlemode fiber rows: 5000 m at 1 G, 10000 m at 10 G; 0 at other speeds.
		{name: "singlemode fiber 10 Mbps no row", medium: fabric.SinglemodeFiber, speedBPS: 10_000_000, want: 0},
		{name: "singlemode fiber 100 Mbps no row", medium: fabric.SinglemodeFiber, speedBPS: 100_000_000, want: 0},
		{name: "singlemode fiber 1 Gbps", medium: fabric.SinglemodeFiber, speedBPS: 1_000_000_000, want: 5000},
		{name: "singlemode fiber 10 Gbps", medium: fabric.SinglemodeFiber, speedBPS: 10_000_000_000, want: 10000},
		{name: "singlemode fiber 40 Gbps no row", medium: fabric.SinglemodeFiber, speedBPS: 40_000_000_000, want: 0},

		// Twinax rows: 15 m at 10 G; 0 at other speeds.
		{name: "twinax 10 Mbps no row", medium: fabric.Twinax, speedBPS: 10_000_000, want: 0},
		{name: "twinax 100 Mbps no row", medium: fabric.Twinax, speedBPS: 100_000_000, want: 0},
		{name: "twinax 1 Gbps no row", medium: fabric.Twinax, speedBPS: 1_000_000_000, want: 0},
		{name: "twinax 10 Gbps", medium: fabric.Twinax, speedBPS: 10_000_000_000, want: 15},
		{name: "twinax 25 Gbps no row", medium: fabric.Twinax, speedBPS: 25_000_000_000, want: 0},
		{name: "twinax 100 Gbps no row", medium: fabric.Twinax, speedBPS: 100_000_000_000, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.medium.Reach(tc.speedBPS)
			if got != tc.want {
				t.Errorf("%q.Reach(%d) = %v, want %v", tc.medium, tc.speedBPS, got, tc.want)
			}
		})
	}
}

func TestPropagation(t *testing.T) {
	tests := []struct {
		name         string
		lengthMeters float64
		medium       fabric.Medium
		want         time.Duration
	}{
		{
			name:         "300 m twisted pair",
			lengthMeters: 300,
			medium:       fabric.TwistedPair,
			want:         1564 * time.Nanosecond,
		},
		{
			name:         "100 m twisted pair",
			lengthMeters: 100,
			medium:       fabric.TwistedPair,
			want:         521 * time.Nanosecond,
		},
		{
			name:         "300 m multimode",
			lengthMeters: 300,
			medium:       fabric.MultimodeFiber,
			want:         1494 * time.Nanosecond,
		},
		{
			name:         "10000 m singlemode",
			lengthMeters: 10000,
			medium:       fabric.SinglemodeFiber,
			want:         49786 * time.Nanosecond,
		},
		{
			name:         "3 m twinax",
			lengthMeters: 3,
			medium:       fabric.Twinax,
			want:         13 * time.Nanosecond,
		},
		{
			name:         "0 m length is zero duration",
			lengthMeters: 0,
			medium:       fabric.TwistedPair,
			want:         0,
		},
		{
			name:         "negative length is zero duration",
			lengthMeters: -5,
			medium:       fabric.TwistedPair,
			want:         0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fabric.Propagation(tc.lengthMeters, tc.medium)
			if got != tc.want {
				t.Errorf("Propagation(%v, %q) = %v, want %v", tc.lengthMeters, tc.medium, got, tc.want)
			}
		})
	}
}
