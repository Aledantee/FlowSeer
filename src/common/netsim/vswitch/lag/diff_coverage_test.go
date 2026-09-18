package lag_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// TestDiffCoversEveryConfigField is R9's gate: every exported lag.Config field reaches
// lag.Diff. lag.Config.Normalize takes the port table and the switch's base MAC, unlike
// its niladic siblings, so the normalize closure fixes both to the table this test
// builds; lag.Diff itself does not self-normalize, per its own doc comment putting
// normalization on the caller, which is what this closure stands in for.
func TestDiffCoversEveryConfigField(t *testing.T) {
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}).
		Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}
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
	normalize := func(c lag.Config) lag.Config { return c.Normalize(ports, systemID) }

	netsimtest.AssertDiffCoversConfig(t, seed, normalize, lag.Diff, nil)
}
