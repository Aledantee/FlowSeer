package port_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/internal/simtest"
)

// TestDiffCoversEveryConfigField includes Name's lifecycle pair and rebuilds both
// opaque tables with a fixed LAG companion so LagParent remains valid.
func TestDiffCoversEveryConfigField(t *testing.T) {
	simtest.AssertMappedDiffCoverage(t, "port")
}
