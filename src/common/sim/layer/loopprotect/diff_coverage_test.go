package loopprotect_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
)

// TestDiffCoversEveryConfigField verifies that every exported loopprotect.Config field
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

	simtest.AssertDiffCoversConfig(t, seed, loopprotect.Config.Normalize, loopprotect.Diff, nil)
}
