package phy_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
)

func TestNegotiate(t *testing.T) {
	gigabit := []uint64{10_000_000, 100_000_000, 1_000_000_000}
	tenGigabit := []uint64{10_000_000, 100_000_000, 1_000_000_000, 10_000_000_000}

	cases := []struct {
		name string
		a    phy.Ethernet
		b    phy.Ethernet
		top  uint64
		want phy.Link
	}{
		// Row 1: mode unreported or forced speed unreported against any
		{
			name: "mode unreported on end A yields unknown with capability-unknown",
			a:    phy.Ethernet{},
			b: phy.Ethernet{
				SupportedSpeedsBPS:       gigabit,
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			want: phy.Link{State: phy.LinkUnknown, Reason: phy.ReasonCapabilityUnknown},
		},
		{
			name: "mode unreported on end B yields unknown with capability-unknown",
			a: phy.Ethernet{
				SupportedSpeedsBPS:       gigabit,
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			b:    phy.Ethernet{},
			want: phy.Link{State: phy.LinkUnknown, Reason: phy.ReasonCapabilityUnknown},
		},
		{
			name: "forced speed 0 on end A yields unknown with capability-unknown",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 0, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full},
			},
			want: phy.Link{State: phy.LinkUnknown, Reason: phy.ReasonCapabilityUnknown},
		},
		{
			name: "forced speed 0 on end B yields unknown with capability-unknown",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 0, Duplex: phy.Full},
			},
			want: phy.Link{State: phy.LinkUnknown, Reason: phy.ReasonCapabilityUnknown},
		},
		{
			name: "mode unreported on end A against forced end yields unknown with capability-unknown",
			a:    phy.Ethernet{},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			want: phy.Link{State: phy.LinkUnknown, Reason: phy.ReasonCapabilityUnknown},
		},
		{
			name: "forced speed 0 on end A against auto end yields unknown with capability-unknown",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 0, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				SupportedSpeedsBPS:       gigabit,
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			want: phy.Link{State: phy.LinkUnknown, Reason: phy.ReasonCapabilityUnknown},
		},

		// Row 2: auto, speeds known against auto, speeds known
		{
			name: "two auto ends resolve to highest common speed at full duplex",
			a: phy.Ethernet{
				SupportedSpeedsBPS:       gigabit,
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			b: phy.Ethernet{
				SupportedSpeedsBPS:       tenGigabit,
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			top: 0,
			want: phy.Link{
				State:    phy.LinkResolved,
				SpeedBPS: 1_000_000_000,
				DuplexA:  phy.Full,
				DuplexB:  phy.Full,
				Source:   phy.SourceNegotiated,
			},
		},
		{
			name: "cable top speed limits auto negotiation",
			a: phy.Ethernet{
				SupportedSpeedsBPS:       gigabit,
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			b: phy.Ethernet{
				SupportedSpeedsBPS:       tenGigabit,
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			top: 100_000_000,
			want: phy.Link{
				State:    phy.LinkResolved,
				SpeedBPS: 100_000_000,
				DuplexA:  phy.Full,
				DuplexB:  phy.Full,
				Source:   phy.SourceNegotiated,
			},
		},
		{
			name: "two auto ends with no common speed fail with speed-mismatch",
			a: phy.Ethernet{
				SupportedSpeedsBPS:       []uint64{10_000_000},
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			b: phy.Ethernet{
				SupportedSpeedsBPS:       []uint64{100_000_000},
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			want: phy.Link{State: phy.LinkFailed, Reason: phy.ReasonSpeedMismatch},
		},

		// Row 3: auto, speeds unreported against auto
		{
			name: "two auto ends with unreported speeds yield unknown",
			a: phy.Ethernet{
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			b: phy.Ethernet{
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			want: phy.Link{State: phy.LinkUnknown, Reason: phy.ReasonCapabilityUnknown},
		},
		{
			name: "auto with speeds unreported against auto with speeds known yields unknown",
			a: phy.Ethernet{
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			b: phy.Ethernet{
				SupportedSpeedsBPS:       gigabit,
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			want: phy.Link{State: phy.LinkUnknown, Reason: phy.ReasonCapabilityUnknown},
		},
		{
			name: "auto with speeds known against auto with speeds unreported yields unknown",
			a: phy.Ethernet{
				SupportedSpeedsBPS:       gigabit,
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			b: phy.Ethernet{
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			want: phy.Link{State: phy.LinkUnknown, Reason: phy.ReasonCapabilityUnknown},
		},

		// Row 4: forced S against forced S
		{
			name: "two forced ends with same speed and duplex resolve",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			want: phy.Link{
				State:    phy.LinkResolved,
				SpeedBPS: 1_000_000_000,
				DuplexA:  phy.Full,
				DuplexB:  phy.Full,
				Source:   phy.SourceNegotiated,
			},
		},
		{
			name: "two forced ends with same speed and half duplex resolve without mismatch",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Half},
			},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Half},
			},
			want: phy.Link{
				State:    phy.LinkResolved,
				SpeedBPS: 100_000_000,
				DuplexA:  phy.Half,
				DuplexB:  phy.Half,
				Source:   phy.SourceNegotiated,
			},
		},
		{
			name: "two forced ends with different stated duplex resolve with duplex-mismatch",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Half},
			},
			want: phy.Link{
				State:    phy.LinkResolved,
				SpeedBPS: 1_000_000_000,
				DuplexA:  phy.Full,
				DuplexB:  phy.Half,
				Source:   phy.SourceNegotiated,
				Reason:   phy.ReasonDuplexMismatch,
			},
		},
		{
			name: "two forced ends where one has unknown duplex resolve without mismatch",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Unknown},
			},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			want: phy.Link{
				State:    phy.LinkResolved,
				SpeedBPS: 1_000_000_000,
				DuplexA:  phy.Unknown,
				DuplexB:  phy.Full,
				Source:   phy.SourceNegotiated,
			},
		},
		{
			name: "two forced ends exceeding cable top speed fail with speed-mismatch",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			top:  100_000_000,
			want: phy.Link{State: phy.LinkFailed, Reason: phy.ReasonSpeedMismatch},
		},

		// Row 5: forced S against forced T != S
		{
			name: "two forced ends with differing speeds fail with speed-mismatch",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			want: phy.Link{State: phy.LinkFailed, Reason: phy.ReasonSpeedMismatch},
		},

		// Row 6: forced S <= 100 Mb/s against auto, speeds known
		{
			name: "forced full 100M against auto speeds known resolves full and half with duplex-mismatch",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				SupportedSpeedsBPS:       gigabit,
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			want: phy.Link{
				State:    phy.LinkResolved,
				SpeedBPS: 100_000_000,
				DuplexA:  phy.Full,
				DuplexB:  phy.Half,
				Source:   phy.SourceNegotiated,
				Reason:   phy.ReasonDuplexMismatch,
			},
		},
		{
			name: "auto speeds known against forced full 100M resolves half and full with duplex-mismatch",
			a: phy.Ethernet{
				SupportedSpeedsBPS:       gigabit,
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full},
			},
			want: phy.Link{
				State:    phy.LinkResolved,
				SpeedBPS: 100_000_000,
				DuplexA:  phy.Half,
				DuplexB:  phy.Full,
				Source:   phy.SourceNegotiated,
				Reason:   phy.ReasonDuplexMismatch,
			},
		},
		{
			name: "forced half 100M against auto speeds known resolves half and half without mismatch",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Half},
			},
			b: phy.Ethernet{
				SupportedSpeedsBPS:       gigabit,
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			want: phy.Link{
				State:    phy.LinkResolved,
				SpeedBPS: 100_000_000,
				DuplexA:  phy.Half,
				DuplexB:  phy.Half,
				Source:   phy.SourceNegotiated,
			},
		},
		{
			name: "forced 100M unsupported by auto speeds known fails with speed-mismatch",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				SupportedSpeedsBPS:       []uint64{10_000_000, 1_000_000_000},
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			want: phy.Link{State: phy.LinkFailed, Reason: phy.ReasonSpeedMismatch},
		},
		{
			name: "forced 100M exceeding cable top speed against auto fails with speed-mismatch",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				SupportedSpeedsBPS:       gigabit,
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			top:  10_000_000,
			want: phy.Link{State: phy.LinkFailed, Reason: phy.ReasonSpeedMismatch},
		},

		// Row 7: forced S <= 100 Mb/s against auto, speeds unreported
		{
			name: "forced 100M against auto with unreported speeds yields unknown",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			want: phy.Link{State: phy.LinkUnknown, Reason: phy.ReasonCapabilityUnknown},
		},
		{
			name: "auto with unreported speeds against forced 100M yields unknown",
			a: phy.Ethernet{
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full},
			},
			want: phy.Link{State: phy.LinkUnknown, Reason: phy.ReasonCapabilityUnknown},
		},

		// Row 8: forced S > 100 Mb/s against auto
		{
			name: "forced 1G against auto speeds known yields unsupported",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				SupportedSpeedsBPS:       gigabit,
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			want: phy.Link{State: phy.LinkUnsupported, Reason: phy.ReasonForcedAgainstAutoUnmodeled},
		},
		{
			name: "auto speeds known against forced 1G yields unsupported",
			a: phy.Ethernet{
				SupportedSpeedsBPS:       gigabit,
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			want: phy.Link{State: phy.LinkUnsupported, Reason: phy.ReasonForcedAgainstAutoUnmodeled},
		},
		{
			name: "forced 1G against auto speeds unreported yields unsupported",
			a: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			want: phy.Link{State: phy.LinkUnsupported, Reason: phy.ReasonForcedAgainstAutoUnmodeled},
		},
		{
			name: "auto speeds unreported against forced 1G yields unsupported",
			a: phy.Ethernet{
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
			b: phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			want: phy.Link{State: phy.LinkUnsupported, Reason: phy.ReasonForcedAgainstAutoUnmodeled},
		},

		// Observed rule
		{
			name: "observed rule resolves unknown link when both ends carry identical observed speed",
			a: phy.Ethernet{
				Observed: &phy.Observed{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				Observed: &phy.Observed{SpeedBPS: 1_000_000_000, Duplex: phy.Half},
			},
			want: phy.Link{
				State:    phy.LinkResolved,
				SpeedBPS: 1_000_000_000,
				DuplexA:  phy.Full,
				DuplexB:  phy.Half,
				Source:   phy.SourceObserved,
			},
		},
		{
			name: "observed rule does not resolve unknown link when observed speeds differ",
			a: phy.Ethernet{
				Observed: &phy.Observed{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				Observed: &phy.Observed{SpeedBPS: 100_000_000, Duplex: phy.Full},
			},
			want: phy.Link{State: phy.LinkUnknown, Reason: phy.ReasonCapabilityUnknown},
		},
		{
			name: "observed rule does not resolve unknown link when observed speeds are zero",
			a: phy.Ethernet{
				Observed: &phy.Observed{SpeedBPS: 0, Duplex: phy.Full},
			},
			b: phy.Ethernet{
				Observed: &phy.Observed{SpeedBPS: 0, Duplex: phy.Full},
			},
			want: phy.Link{State: phy.LinkUnknown, Reason: phy.ReasonCapabilityUnknown},
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
