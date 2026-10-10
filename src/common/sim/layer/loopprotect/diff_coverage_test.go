package loopprotect_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
)

// TestDiffCoversEveryConfigField verifies that every exported loopprotect.Config field
// reaches loopprotect.Diff.
func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "loopprotect", simtest.LoopprotectDiffAllowance())
}
