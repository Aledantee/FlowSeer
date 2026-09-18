package loopprotect_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
)

// TestDiffCoversEveryConfigField is R9's gate: every exported loopprotect.Config field
// reaches loopprotect.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	seed := loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {
				Action: loopprotect.Block,
				Recovery: loopprotect.Recovery{
					Mode:     loopprotect.Timer,
					Duration: 30 * time.Second,
				},
				VLANs: []vlan.ID{10},
			},
		},
	}

	netsimtest.AssertDiffCoversConfig(t, seed, loopprotect.Config.Normalize, loopprotect.Diff, nil)
}
