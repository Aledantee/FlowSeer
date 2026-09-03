//go:build snmp_integration_t2

package integration

import (
	"context"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/snmp"
	"go.aledante.io/FlowSeer/src/common/snmp/test/integration/testenv"
)

// t2DialV3 dials the live SR Linux NOS via snmp.NewSession with the
// USM credentials testenv.SRLinuxUSMConfig declares (SHA-256 /
// AES-256). Close is registered with
// t.Cleanup.
func t2DialV3(t *testing.T, opts ...snmp.Option) snmp.Session {
	t.Helper()
	if testenv.Target() == "" {
		t.Fatal("testenv.Target() is empty; t2 TestMain did not seed the target")
	}
	base := []snmp.Option{
		snmp.WithUSM(testenv.SRLinuxUSMConfig),
		snmp.WithMinSecurity(snmp.MinSecurityNoAuth),
		snmp.WithTimeout(3 * time.Second),
		snmp.WithRetries(2),
	}
	sess, err := snmp.NewSession(context.Background(), testenv.Target(), snmp.V3, append(base, opts...)...)
	if err != nil {
		t.Fatalf("t2DialV3: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}
