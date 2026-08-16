//go:build snmp_integration_t2

package integration

import (
	"testing"

	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
)

// TestT2_AssertIfTableDenseRows pins the dense-row contract against
// the live SR Linux NOS: the *same* AssertIfTableDenseRows helper
// used in T1 (and T3) passes against a different agent with the same
// column selection. This is the cross-agent contract test the
// integration suite exists to provide.
//
// USM pair: SHA-256 / AES-256, baked into testenv.SRLinuxUSMConfig.
// The matrix gap vs T1 (which exhausts the full auth/priv
// matrix) is intentional — T1 owns the matrix; T2 proves the chosen
// pair survives against a real NOS end to end.
func TestT2_AssertIfTableDenseRows(t *testing.T) {
	sess := t2DialV3(t)
	AssertIfTableDenseRows(t, sess, ifmib.IfDescr, ifmib.IfOperStatus)
}
