package mcast_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
)

// TestDiffCoversEveryConfigField verifies that every exported mcast.Config field reaches
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

	normalize := func(c mcast.Config) mcast.Config {
		return c.Normalize(layer.Env{})
	}
	simtest.AssertDiffCoversConfig(t, seed, normalize, mcast.Diff, nil)
}
