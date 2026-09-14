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
		{name: "unspecified medium has no factor", medium: fabric.MediumUnspecified, want: 0},
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
		name       string
		medium     fabric.Medium
		length     float64
		speedBPS   uint64
		wantState  fabric.ReachState
		wantMeters float64
	}{
		{name: "twisted pair 10 Mbps in range", medium: fabric.TwistedPair, length: 100, speedBPS: 10_000_000, wantState: fabric.ReachInRange, wantMeters: 100},
		{name: "twisted pair 100 Mbps in range", medium: fabric.TwistedPair, length: 2, speedBPS: 100_000_000, wantState: fabric.ReachInRange, wantMeters: 100},
		{name: "twisted pair 1 Gbps exceeded", medium: fabric.TwistedPair, length: 100.5, speedBPS: 1_000_000_000, wantState: fabric.ReachExceeded, wantMeters: 100},
		{name: "twisted pair 10 Gbps in range", medium: fabric.TwistedPair, length: 0, speedBPS: 10_000_000_000, wantState: fabric.ReachInRange, wantMeters: 100},
		{name: "twisted pair 25 Gbps has no row", medium: fabric.TwistedPair, length: 1, speedBPS: 25_000_000_000, wantState: fabric.ReachUnknown},
		{name: "twisted pair 40 Gbps has no row even at 0 m", medium: fabric.TwistedPair, length: 0, speedBPS: 40_000_000_000, wantState: fabric.ReachUnknown},

		{name: "unspecified medium 1 Gbps", medium: fabric.MediumUnspecified, length: 2, speedBPS: 1_000_000_000, wantState: fabric.ReachUnknown},
		{name: "unspecified medium 0 m", medium: fabric.MediumUnspecified, length: 0, speedBPS: 10_000_000, wantState: fabric.ReachUnknown},

		{name: "multimode fiber 10 Mbps has no row", medium: fabric.MultimodeFiber, length: 1, speedBPS: 10_000_000, wantState: fabric.ReachUnknown},
		{name: "multimode fiber 100 Mbps has no row", medium: fabric.MultimodeFiber, length: 1, speedBPS: 100_000_000, wantState: fabric.ReachUnknown},
		{name: "multimode fiber 1 Gbps in range", medium: fabric.MultimodeFiber, length: 550, speedBPS: 1_000_000_000, wantState: fabric.ReachInRange, wantMeters: 550},
		{name: "multimode fiber 10 Gbps exceeded", medium: fabric.MultimodeFiber, length: 400, speedBPS: 10_000_000_000, wantState: fabric.ReachExceeded, wantMeters: 300},
		{name: "multimode fiber 25 Gbps has no row", medium: fabric.MultimodeFiber, length: 1, speedBPS: 25_000_000_000, wantState: fabric.ReachUnknown},

		{name: "singlemode fiber 100 Mbps has no row", medium: fabric.SinglemodeFiber, length: 1, speedBPS: 100_000_000, wantState: fabric.ReachUnknown},
		{name: "singlemode fiber 1 Gbps exceeded", medium: fabric.SinglemodeFiber, length: 7000, speedBPS: 1_000_000_000, wantState: fabric.ReachExceeded, wantMeters: 5000},
		{name: "singlemode fiber 10 Gbps in range", medium: fabric.SinglemodeFiber, length: 7000, speedBPS: 10_000_000_000, wantState: fabric.ReachInRange, wantMeters: 10000},
		{name: "singlemode fiber 40 Gbps has no row", medium: fabric.SinglemodeFiber, length: 1, speedBPS: 40_000_000_000, wantState: fabric.ReachUnknown},

		{name: "twinax 1 Gbps has no row", medium: fabric.Twinax, length: 1, speedBPS: 1_000_000_000, wantState: fabric.ReachUnknown},
		{name: "twinax 10 Gbps in range", medium: fabric.Twinax, length: 15, speedBPS: 10_000_000_000, wantState: fabric.ReachInRange, wantMeters: 15},
		{name: "twinax 10 Gbps exceeded", medium: fabric.Twinax, length: 16, speedBPS: 10_000_000_000, wantState: fabric.ReachExceeded, wantMeters: 15},
		{name: "twinax 100 Gbps has no row", medium: fabric.Twinax, length: 1, speedBPS: 100_000_000_000, wantState: fabric.ReachUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state, meters := tc.medium.Reach(tc.length, tc.speedBPS)
			if state != tc.wantState || meters != tc.wantMeters {
				t.Errorf("%q.Reach(%v, %d) = %v, %v; want %v, %v", tc.medium, tc.length, tc.speedBPS, state, meters, tc.wantState, tc.wantMeters)
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
			name:         "unspecified medium is zero duration",
			lengthMeters: 100,
			medium:       fabric.MediumUnspecified,
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
