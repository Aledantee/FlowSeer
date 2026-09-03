//go:build snmp_integration_t3

package integration

import (
	"context"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
	"go.aledante.io/FlowSeer/src/protocol/snmp/test/integration/testenv"
)

// t3DialReplay dials the running snmpsim against the manifest entry's
// snmpsim_context (carried as the v2c community string). Close is
// registered with t.Cleanup.
func t3DialReplay(t *testing.T, e ManifestEntry) snmp.Session {
	t.Helper()
	sess, err := snmp.NewSession(context.Background(), testenv.Target(), snmp.V2c,
		snmp.WithCommunity(e.SnmpsimContext),
		snmp.WithMinSecurity(snmp.MinSecurityNoAuth),
		snmp.WithTimeout(2*time.Second),
		snmp.WithRetries(2),
	)
	if err != nil {
		t.Fatalf("dial replay %s/%s: %v", e.Vendor, e.Device, err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// TestT3_Replay_DenseRows iterates every manifest entry and asserts
// the dense-row contract via the shared helper. Each entry runs
// as a named subtest so a single bad capture surfaces immediately
// without taking down the whole tier.
//
// The same AssertIfTableDenseRows helper used in T1 (against
// snmpd) and T2 (against SR Linux) runs here against a
// snmpsim-replayed corpus — three different agents, one assertion
// path, zero divergence.
func TestT3_Replay_DenseRows(t *testing.T) {
	if testing.Short() {
		t.Skip("snmpsim replay disabled in short mode")
	}
	if len(t3Manifest.Entries) == 0 {
		t.Fatal("t3Manifest is empty; TestMain did not seed it")
	}
	for _, e := range t3Manifest.Entries {
		t.Run(e.Vendor+"/"+e.Device, func(t *testing.T) {
			sess := t3DialReplay(t, e)
			AssertIfTableDenseRows(t, sess, ifmib.IfDescr, ifmib.IfOperStatus)
		})
	}
}
