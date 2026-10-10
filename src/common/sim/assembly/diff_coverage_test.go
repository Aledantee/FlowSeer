package assembly_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
)

// TestDiffCoversEveryConfigField includes the complete delegated switch and raw
// assumption observations. Normalize/Diff coverage does not require construction validity.
func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "assembly", simtest.BridgeDiffAllowance(), simtest.PhyDiffAllowance(), simtest.LoopprotectDiffAllowance(), simtest.McastDiffAllowance(), simtest.LagDiffAllowance(), simtest.StpDiffAllowance(), simtest.FilterDiffAllowance(), simtest.RoutingDiffAllowance(), simtest.TrafficDiffAllowance(), simtest.PortDiffAllowance(), simtest.VswitchDiffAllowance(), simtest.AssemblyDiffAllowance())
}
