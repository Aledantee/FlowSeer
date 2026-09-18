package phy_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
)

// TestDiffCoversEveryConfigField is R9's gate: every exported phy.Config field reaches
// phy.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	limit := uint32(5000)
	pdClass := uint8(4)
	seed := phy.Config{
		Ethernet: map[string]phy.Ethernet{
			"1/1/1": {
				SupportedSpeedsBPS:       []uint64{1_000_000_000},
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting: &phy.Setting{
					SpeedBPS:        1_000_000_000,
					Duplex:          phy.Full,
					AutoNegotiation: true,
				},
				Observed: &phy.Observed{
					SpeedBPS: 1_000_000_000,
					Duplex:   phy.Full,
				},
			},
		},
		PoE: &phy.PoE{
			Groups: map[string]phy.Group{
				"g1": {PowerMilliwatts: 370_000},
			},
			Ports: map[string]phy.PsePort{
				"1/1/1": {
					Group:    "g1",
					MaxClass: 4,
					Enabled:  true,
					Limit:    &limit,
					Priority: phy.PriorityHigh,
					PD:       phy.PDAttached,
					PDClass:  &pdClass,
				},
			},
		},
	}

	netsimtest.AssertDiffCoversConfig(t, seed, phy.Config.Normalize, phy.Diff, nil)
}
