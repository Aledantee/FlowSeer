package snmp

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/secret"
)

// usm_netsnmp_test.go is a live-peer integration check for v3 notification
// reception: it drives the system
// Net-SNMP `snmptrap` / `snmpinform` CLIs against the native trap listener
// — a real, independent SNMP implementation, not a mock. It is skipped
// automatically when the CLIs are absent (so unit CI stays self-contained),
// and uses SHA-1 + AES-128 because the bundled Net-SNMP predates SHA-2 /
// AES-192+.
//
// This is the part runnable without Docker/containerlab; the snmpd
// USM polling matrix and the SR Linux NOS trap path run in the Docker-gated
// integration tiers (t1/t2).

func requireNetSNMP(t *testing.T, tool string) string {
	t.Helper()
	path, err := exec.LookPath(tool)
	if err != nil {
		t.Skipf("%s not on PATH; skipping live Net-SNMP check", tool)
	}
	return path
}

const (
	nsAuthPass = "flowseer-auth-pass"
	nsPrivPass = "flowseer-priv-pass"
)

// TestNetSNMP_V3TrapReception sends a real authPriv v3 trap from Net-SNMP's
// snmptrap and asserts the native listener receives it with the sender's
// EngineID/UserName (against a live peer).
func TestNetSNMP_V3TrapReception(t *testing.T) {
	bin := requireNetSNMP(t, "snmptrap")
	senderEngine := mustHex("8000000001a1b2c3d4")
	user := "trapuser"

	reg := USMConfig{
		Username: user, EngineID: senderEngine,
		AuthProtocol: AuthSHA, AuthPassphrase: secret.NewString(nsAuthPass),
		PrivProtocol: PrivAES, PrivPassphrase: secret.NewString(nsPrivPass),
	}
	ts, addr := startTrapListener(t, WithUSMTable([]USMConfig{reg}))

	target := fmt.Sprintf("127.0.0.1:%d", addr.Port)
	// snmptrap -v3 ... <host> <uptime> <trapOID> : net-snmp prepends the
	// canonical sysUpTime.0 / snmpTrapOID.0 bindings.
	cmd := exec.Command(bin,
		"-v3", "-e", "8000000001a1b2c3d4",
		"-u", user, "-l", "authPriv",
		"-a", "SHA", "-A", nsAuthPass,
		"-x", "AES", "-X", nsPrivPass,
		"-Z", "1,1",
		target, "", "1.3.6.1.6.3.1.1.5.1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("snmptrap failed: %v\n%s", err, out)
	}

	tr, ok := nextTrap(ts, 5*time.Second)
	if !ok {
		t.Fatal("native listener did not receive the live v3 trap")
	}
	if tr.Version != V3 {
		t.Fatalf("version = %s, want v3", tr.Version)
	}
	if string(tr.EngineID) != string(senderEngine) || tr.UserName != user {
		t.Fatalf("trap identity: engineID=%x user=%q", tr.EngineID, tr.UserName)
	}
}

// TestNetSNMP_V3InformReception drives Net-SNMP's snmpinform against the
// native authoritative listener: snmpinform discovers our engineID, sends an
// authPriv inform, and the native listener answers discovery + acks
// (against a live peer). snmpinform exiting 0 proves it received the
// Response ack; the surfaced inform proves the receive path.
func TestNetSNMP_V3InformReception(t *testing.T) {
	bin := requireNetSNMP(t, "snmpinform")
	ownEngine := mustHex("80001f8800f100feedface01")
	user := "informuser"

	reg := USMConfig{
		Username: user, EngineID: ownEngine, // registered under OUR engineID
		AuthProtocol: AuthSHA, AuthPassphrase: secret.NewString(nsAuthPass),
		PrivProtocol: PrivAES, PrivPassphrase: secret.NewString(nsPrivPass),
	}

	// Capture the listener so the post-restart quarantine can be lifted (a
	// fresh listener rejects all informs for the first 150s).
	var l *listener
	listenerRegistered = func(_ *TrapStream, ll *listener) { l = ll }
	t.Cleanup(func() { listenerRegistered = nil })

	ts, err := ListenTraps(context.Background(), "127.0.0.1:0",
		WithOwnEngineID(ownEngine), WithUSMTable([]USMConfig{reg}))
	if err != nil {
		t.Fatalf("ListenTraps: %v", err)
	}
	t.Cleanup(func() { _ = ts.Close() })
	if l == nil {
		t.Fatal("listener not captured")
	}
	addr := l.conn.LocalAddr().(*net.UDPAddr)
	l.startedNanos.Store(time.Now().Add(-200 * time.Second).UnixNano()) // lift quarantine

	target := fmt.Sprintf("127.0.0.1:%d", addr.Port)
	cmd := exec.Command(bin,
		"-v3", "-u", user, "-l", "authPriv",
		"-a", "SHA", "-A", nsAuthPass,
		"-x", "AES", "-X", nsPrivPass,
		target, "", "1.3.6.1.6.3.1.1.5.1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("snmpinform failed (no ack received?): %v\n%s", err, out)
	}

	tr, ok := nextTrap(ts, 5*time.Second)
	if !ok {
		t.Fatal("native listener did not surface the live v3 inform")
	}
	if tr.Version != V3 || tr.UserName != user {
		t.Fatalf("inform identity: version=%s user=%q", tr.Version, tr.UserName)
	}
}
