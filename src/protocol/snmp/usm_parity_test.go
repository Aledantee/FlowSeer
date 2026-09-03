//go:build snmp_parity

package snmp

// usm_parity_test.go is an on-demand feature-parity check: it polls a live
// Net-SNMP agent with the NATIVE backend across the auth×priv matrix Net-SNMP
// supports, exercising the polling direction (native client -> Net-SNMP
// agent) that complements the reception tests (Net-SNMP sender -> native
// listener in usm_netsnmp_test.go).
//
// It is gated behind the snmp_parity build tag and the FLOWSEER_PARITY_SNMPD
// env var (host:port of a running snmpd) so it never runs in normal CI.
// Bring up an agent yourself (e.g. net-snmp snmpd with the matching
// createUser/rouser config) and run:
//
//	FLOWSEER_PARITY_SNMPD=127.0.0.1:16161 \
//	  go test -tags snmp_parity -run TestParity -v ./common/snmp/

import (
	"context"
	"os"
	"testing"
	"time"
)

func parityTarget(t *testing.T) string {
	t.Helper()
	addr := os.Getenv("FLOWSEER_PARITY_SNMPD")
	if addr == "" {
		t.Skip("FLOWSEER_PARITY_SNMPD unset; skipping live Net-SNMP parity check")
	}
	return addr
}

var parityMatrix = []struct {
	name string
	user string
	auth AuthProtocol
	ap   string
	priv PrivProtocol
	pp   string
}{
	{"noAuthNoPriv", "noauthnopriv", AuthProtocolNone, "", PrivProtocolNone, ""},
	{"MD5_noPriv", "md5nopriv", AuthMD5, "auth-md5-passphrase", PrivProtocolNone, ""},
	{"SHA_noPriv", "shanopriv", AuthSHA, "auth-sha-passphrase", PrivProtocolNone, ""},
	{"MD5_DES", "md5des", AuthMD5, "auth-md5-passphrase", PrivDES, "priv-des-passphrase"},
	{"SHA_DES", "shades", AuthSHA, "auth-sha-passphrase", PrivDES, "priv-des-passphrase"},
	{"MD5_AES", "md5aes", AuthMD5, "auth-md5-passphrase", PrivAES, "priv-aes-passphrase"},
	{"SHA_AES", "shaaes", AuthSHA, "auth-sha-passphrase", PrivAES, "priv-aes-passphrase"},
}

func parityDial(t *testing.T, target string, m int) Session {
	t.Helper()
	c := parityMatrix[m]
	usm := USMConfig{Username: c.user, AuthProtocol: c.auth, PrivProtocol: c.priv}
	if c.auth != AuthProtocolNone {
		usm.AuthPassphrase = c.ap
	}
	if c.priv != PrivProtocolNone {
		usm.PrivPassphrase = c.pp
	}
	sess, err := NewSession(context.Background(), target, V3, WithUSM(usm),
		WithMinSecurity(MinSecurityNoAuth),
		WithTimeout(2*time.Second), WithRetries(2))
	if err != nil {
		t.Fatalf("Dial %s: %v", c.user, err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// TestParity_Get exercises lazy discovery + authenticated Get on every matrix
// cell against the live agent.
func TestParity_Get(t *testing.T) {
	target := parityTarget(t)
	sysUpTime := MustOID(1, 3, 6, 1, 2, 1, 1, 3, 0)
	for i, c := range parityMatrix {
		t.Run(c.name, func(t *testing.T) {
			sess := parityDial(t, target, i)
			vbs, err := sess.Get(context.Background(), []OID{sysUpTime})
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if len(vbs) != 1 {
				t.Fatalf("got %d varbinds, want 1", len(vbs))
			}
			if _, ok := vbs[0].(TimeTicksVar); !ok {
				t.Fatalf("sysUpTime type = %T, want TimeTicksVar", vbs[0])
			}
			t.Logf("OK %-12s Get sysUpTime.0 = %v", c.name, vbs[0])
		})
	}
}

// TestParity_GetNext_Bulk_Walk exercises GetNext, GetBulk, and a GetNext-driven
// Walk over an authPriv cell (SHA+AES) — the full polling op set.
func TestParity_GetNext_Bulk_Walk(t *testing.T) {
	target := parityTarget(t)
	sess := parityDial(t, target, 6) // SHA_AES
	ctx := context.Background()

	if vbs, err := sess.GetNext(ctx, []OID{MustOID(1, 3, 6, 1, 2, 1, 1, 1)}); err != nil || len(vbs) == 0 {
		t.Fatalf("GetNext: vbs=%d err=%v", len(vbs), err)
	}
	if vbs, err := sess.GetBulk(ctx, 0, 8, []OID{MustOID(1, 3, 6, 1, 2, 1, 1)}); err != nil || len(vbs) == 0 {
		t.Fatalf("GetBulk: vbs=%d err=%v", len(vbs), err)
	}
	w := sess.Walk(ctx, MustOID(1, 3, 6, 1, 2, 1, 1))
	n := 0
	for range w.Iter() {
		n++
	}
	if err := w.Err(); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if n == 0 {
		t.Fatalf("Walk yielded no rows")
	}
	t.Logf("OK SHA_AES GetNext/GetBulk/Walk (walk rows=%d)", n)
}

// TestParity_WrongCredentialsRejected confirms a wrong passphrase does not
// silently succeed (the agent drops; the op times out).
func TestParity_WrongCredentialsRejected(t *testing.T) {
	target := parityTarget(t)
	sess, err := NewSession(context.Background(), target, V3,
		WithUSM(USMConfig{Username: "shaaes", AuthProtocol: AuthSHA, AuthPassphrase: "WRONG-passphrase", PrivProtocol: PrivAES, PrivPassphrase: "priv-aes-passphrase"}),
		WithMinSecurity(MinSecurityNoAuth), WithTimeout(time.Second), WithRetries(1))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer sess.Close()
	if _, err := sess.Get(context.Background(), []OID{MustOID(1, 3, 6, 1, 2, 1, 1, 3, 0)}); err == nil {
		t.Fatalf("Get with wrong auth passphrase unexpectedly succeeded")
	}
}
