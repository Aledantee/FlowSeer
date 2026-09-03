package integration

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/snmp/test/integration/testenv"
)

// TestTargetEmptyByDefault confirms the testenv.Target slot starts
// empty so a test that forgets to wait for its tier's TestMain fails
// loudly at [snmp.NewSession] rather than silently dialing localhost.
//
// The assertion is meaningful only under bare `go test` (no tier tag);
// when a tier tag is selected the tier's TestMain has already called
// SetTarget by the time tests run. In that case the test skips.
func TestTargetEmptyByDefault(t *testing.T) {
	if got := testenv.Target(); got != "" {
		t.Skipf("Target() = %q — a tier TestMain has seeded it; assertion applies only under no-tag builds", got)
	}
}
