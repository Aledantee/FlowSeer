package lag_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
)

// TestDiffCoversEveryConfigField verifies that every exported lag.Config field reaches
// lag.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "lag", simtest.LagDiffAllowance())
}
