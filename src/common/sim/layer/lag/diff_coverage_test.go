package lag_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
)

// TestDiffCoversEveryConfigField verifies that every exported lag.Config field reaches
// lag.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	systemID := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}

	rebalance := 45 * time.Second
	seed := lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				// BalanceSLB and Active, not the zero-value ActiveBackup/Off defaults:
				// perturbing to "" must read as a real change, not normalize back to
				// the seed's own value.
				Mode:              lag.BalanceSLB,
				Primary:           "1/1/1",
				UpDelay:           2 * time.Second,
				DownDelay:         time.Second,
				HashBasis:         7,
				MinLinks:          1,
				RebalanceInterval: &rebalance,
				LACP: lag.LACPConfig{
					Mode:           lag.Active,
					Fast:           true,
					SystemPriority: 32768,
					SystemID:       systemID,
					Key:            1,
					Fallback:       true,
				},
				Members: map[string]lag.Member{
					"1/1/1": {Priority: 128, Key: 1},
				},
			},
		},
	}
	normalize := func(c lag.Config) lag.Config { return c.Normalize(layer.Env{}) }

	simtest.AssertDiffCoversConfig(t, seed, normalize, lag.Diff, nil)
}
