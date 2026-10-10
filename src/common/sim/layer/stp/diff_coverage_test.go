package stp_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
)

// TestDiffCoversEveryConfigField verifies that every exported stp.Config field reaches
// stp.Diff. MST and PVST are mutually exclusive at construction (Config.Validate), which
// this fixture never calls; Diff and Normalize take the raw struct, so seeding both
// together reaches every leaf of each in one pass.
func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "stp", simtest.StpDiffAllowance())
}
