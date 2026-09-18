package mcast_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
)

// TestDiffCoversEveryConfigField is R9's gate: every exported mcast.Config field reaches
// mcast.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	floodUnregistered := true
	seed := mcast.Config{
		VLANs: map[vlan.ID]mcast.VLANSnooping{
			10: {
				FloodUnregistered:       &floodUnregistered,
				FastLeave:               true,
				RouterPorts:             []string{"1/1/1"},
				MembershipInterval:      260 * time.Second,
				RouterPortInterval:      260 * time.Second,
				LastMemberQueryInterval: time.Second,
				LastMemberQueryCount:    2,
			},
		},
	}

	netsimtest.AssertDiffCoversConfig(t, seed, mcast.Config.Normalize, mcast.Diff, nil)
}
