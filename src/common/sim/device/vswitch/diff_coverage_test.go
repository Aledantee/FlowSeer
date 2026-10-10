package vswitch_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
)

// TestDiffCoversEveryConfigField includes all capability branches and the opaque
// port adapter, because delegation must preserve each producer's complete sequence.
func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "vswitch", simtest.BridgeDiffAllowance(), simtest.PhyDiffAllowance(), simtest.LoopprotectDiffAllowance(), simtest.McastDiffAllowance(), simtest.LagDiffAllowance(), simtest.StpDiffAllowance(), simtest.FilterDiffAllowance(), simtest.RoutingDiffAllowance(), simtest.TrafficDiffAllowance(), simtest.PortDiffAllowance(), simtest.VswitchDiffAllowance())
}
