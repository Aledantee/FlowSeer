package phy_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
)

func TestNegotiate(t *testing.T) {
	cases := []struct {
		name string
		a    phy.Ethernet
		b    phy.Ethernet
		top  uint64
		want phy.Link
	}{
		{
			name: "two auto ends resolve to highest shared speed at full duplex",
			a: phy.Ethernet{
				SupportedSpeedsBPS:       gigabitCapable,
				AutoNegotiationSupported: true,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			b: phy.Ethernet{
				SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000, 10_000_000_000},
				AutoNegotiationSupported: true,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			top:  0,
			want: phy.Link{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
		},
		{
			name: "cable top speed caps auto negotiation",
			a: phy.Ethernet{
				SupportedSpeedsBPS:       gigabitCapable,
				AutoNegotiationSupported: true,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			b: phy.Ethernet{
				SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000, 10_000_000_000},
				AutoNegotiationSupported: true,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			top:  100_000_000,
			want: phy.Link{SpeedBPS: 100_000_000, Duplex: phy.Full},
		},
		{
			name: "forced speed against auto resolves to the forced speed",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				SupportedSpeedsBPS:       gigabitCapable,
				AutoNegotiationSupported: true,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			top:  0,
			want: phy.Link{SpeedBPS: 100_000_000, Duplex: phy.Full},
		},
		{
			name: "forced ends with different speeds fail with speed mismatch",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			top:  0,
			want: phy.Link{Reason: phy.ReasonSpeedMismatch},
		},
		{
			name: "forced speed exceeding cable top speed fails with speed mismatch",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			top:  100_000_000,
			want: phy.Link{Reason: phy.ReasonSpeedMismatch},
		},
		{
			name: "host against port forced to 10 resolves to 10",
			a:    phy.Ethernet{},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 10_000_000, Duplex: phy.Full},
			},
			top:  0,
			want: phy.Link{SpeedBPS: 10_000_000, Duplex: phy.Full},
		},
		{
			name: "two hosts on one cable resolve to 1000 full",
			a:    phy.Ethernet{},
			b:    phy.Ethernet{},
			top:  0,
			want: phy.Link{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
		},
		{
			name: "two hosts over cable with top speed take cable top speed",
			a:    phy.Ethernet{},
			b:    phy.Ethernet{},
			top:  100_000_000,
			want: phy.Link{SpeedBPS: 100_000_000, Duplex: phy.Full},
		},
		{
			name: "two forced ends that agree resolve to their configured speed and duplex",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			top:  0,
			want: phy.Link{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
		},
		{
			name: "forced end with speed 0 fails with speed mismatch",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 0, Duplex: phy.Full},
			},
			b:    phy.Ethernet{},
			top:  0,
			want: phy.Link{Reason: phy.ReasonSpeedMismatch},
		},
		{
			name: "auto setting on end without auto-negotiation support fails with speed mismatch",
			a: phy.Ethernet{
				AutoNegotiationSupported: false,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			b:    phy.Ethernet{},
			top:  0,
			want: phy.Link{Reason: phy.ReasonSpeedMismatch},
		},
		{
			name: "forced speed unsupported by auto peer fails with speed mismatch",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 10_000_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				SupportedSpeedsBPS:       gigabitCapable,
				AutoNegotiationSupported: true,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			top:  0,
			want: phy.Link{Reason: phy.ReasonSpeedMismatch},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := phy.Negotiate(tc.a, tc.b, tc.top)
			if got != tc.want {
				t.Errorf("Negotiate() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
