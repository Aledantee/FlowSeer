//go:build snmp_integration_t1

package integration

import (
	"context"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/snmp"
	"go.aledante.io/FlowSeer/src/common/snmp/test/integration/testenv"
)

// t1DialV2c dials the live T1 snmpd agent via snmp.NewSession with
// SNMPv2c credentials and a t.Cleanup-registered Close. Extra opts
// are appended to the base set so callers can override timeouts or
// add per-test overrides.
func t1DialV2c(t *testing.T, opts ...snmp.Option) snmp.Session {
	t.Helper()
	if testenv.Target() == "" {
		t.Fatal("testenv.Target() is empty; t1 TestMain did not seed the target")
	}
	base := []snmp.Option{
		snmp.WithCommunity("public"),
		snmp.WithMinSecurity(snmp.MinSecurityNoAuth),
		snmp.WithTimeout(2 * time.Second),
		snmp.WithRetries(2),
	}
	sess, err := snmp.NewSession(context.Background(), testenv.Target(), snmp.V2c, append(base, opts...)...)
	if err != nil {
		t.Fatalf("t1DialV2c: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// t1DialV3 dials the live T1 snmpd agent via snmp.NewSession with the
// supplied USM credentials. Unlike t1DialV2c it returns (sess, err)
// directly so callers can assert on Dial-time rejection — the
// Priv3DES path in the USM matrix relies on this.
//
// When Dial succeeds, Close is registered with t.Cleanup so tests do
// not have to manage session lifetime themselves.
func t1DialV3(t *testing.T, usm snmp.USMConfig, opts ...snmp.Option) (snmp.Session, error) {
	t.Helper()
	if testenv.Target() == "" {
		t.Fatal("testenv.Target() is empty; t1 TestMain did not seed the target")
	}
	base := []snmp.Option{
		snmp.WithUSM(usm),
		snmp.WithMinSecurity(snmp.MinSecurityNoAuth),
		snmp.WithTimeout(2 * time.Second),
		snmp.WithRetries(2),
	}
	sess, err := snmp.NewSession(context.Background(), testenv.Target(), snmp.V3, append(base, opts...)...)
	if err == nil && sess != nil {
		t.Cleanup(func() { _ = sess.Close() })
	}
	return sess, err
}
