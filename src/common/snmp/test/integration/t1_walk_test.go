//go:build snmp_integration_t1

package integration

import (
	"testing"

	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
)

// TestT1_AssertIfTableDenseRows pins the dense-row contract against
// the live snmpd agent: a Walk over ifTable with a column subset
// must surface only the requested columns and leave every other
// column at its zero value.
//
// The shared AssertIfTableDenseRows helper does the work; identical
// invocations land in T2 (against SR Linux) and T3 (against snmpsim
// replay) — the cross-agent contract test this suite exists to
// provide.
func TestT1_AssertIfTableDenseRows(t *testing.T) {
	sess := t1DialV2c(t)
	AssertIfTableDenseRows(t, sess, ifmib.IfDescr, ifmib.IfOperStatus)
}
