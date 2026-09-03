//go:build snmp_integration_t2

package integration

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/protocol/snmp"
	"go.aledante.io/FlowSeer/src/protocol/snmp/test/integration/testenv"
)

// t2TrapPort is the host-side UDP port the T2 trap listener binds.
// Chosen as a fixed non-standard value so the SR Linux trap-target
// configuration can be templated without a random-port handshake.
// If the port is busy on a developer's machine, override via
// testenv.EnvT2TrapPort.
const t2TrapPort = 12162

// t2TrapHostFromContainer returns the address the SR Linux container
// uses to reach the host's trap listener. Default "host.docker.internal"
// resolves automatically inside Docker Desktop (macOS, Windows).
//
// On Linux Docker Engine, host.docker.internal is NOT a default; the
// caller is expected to set testenv.EnvT2HostFromContainer to the
// docker0 bridge gateway (typically 172.17.0.1) or to the host's
// LAN IP visible from the lab's network.
//
// Per-platform address resolution is handled here in one place
// rather than embedded in topology files; the env-var
// escape hatch lets developers tune for unusual networking setups.
func t2TrapHostFromContainer() string {
	if v := os.Getenv(testenv.EnvT2HostFromContainer); v != "" {
		return v
	}
	return "host.docker.internal"
}

// t2TrapPortResolved returns the configured trap port, honoring the
// testenv.EnvT2TrapPort override. A malformed override surfaces via
// t.Logf so a misconfigured CI env is visible in test output —
// silently falling back to the default would mask the override
// rejection.
func t2TrapPortResolved(t testing.TB) int {
	t.Helper()
	v := os.Getenv(testenv.EnvT2TrapPort)
	if v == "" {
		return t2TrapPort
	}
	var p int
	if _, err := fmt.Sscanf(v, "%d", &p); err != nil || p <= 0 {
		t.Logf("%s=%q is not a positive integer; using default port %d", testenv.EnvT2TrapPort, v, t2TrapPort)
		return t2TrapPort
	}
	return p
}

// t2StartTrapListener binds snmp.ListenTraps on the host's
// 0.0.0.0:<t2TrapPort> and registers Close with t.Cleanup. Returns
// the open stream plus the address SR Linux should send to.
//
// The host-side bind goes through [snmp.ListenTraps] — the
// public snmp.ListenTraps constructor, covering the trap path
// without any test-side change.
//
// Source filtering is left at the Backend default (permissive). A
// strict filter scoped to the containerlab bridge subnet would be
// more secure but containerlab's per-deploy subnet is not
// deterministic across runs; the runtime-discovery path lands as a
// follow-up to a static-CIDR override.
func t2StartTrapListener(t *testing.T) (ts *snmp.TrapStream, srLinuxTrapTargetAddr string) {
	t.Helper()
	if testing.Short() {
		t.Skip("live SR Linux traps disabled in short mode")
	}
	port := t2TrapPortResolved(t)
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	stream, err := snmp.ListenTraps(context.Background(), addr)
	if err != nil {
		t.Fatalf("TrapListener bind %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	return stream, fmt.Sprintf("%s:%d", t2TrapHostFromContainer(), port)
}

// srLinuxCliErrorMarkers are case-insensitive substrings the SR Linux
// CLI emits on commit / parse failure. `containerlab exec` propagates
// the shell exit code, not SR Linux's CLI commit status — a
// committed-but-rejected CLI command exits 0 from the shell's
// perspective. Without this check the test would silently proceed
// and surface ErrTrapWaitTimeout 30s later with no actionable
// diagnostic.
var srLinuxCliErrorMarkers = []string{
	"Error",
	"FAILED",
	"failed",
	"syntax error",
}

// assertSRLinuxCliOK fails the test when the CLI output contains a
// rejection marker. Always call this on the output of every execFn
// invocation that runs SR Linux CLI commands.
func assertSRLinuxCliOK(t *testing.T, op, out string) {
	t.Helper()
	for _, marker := range srLinuxCliErrorMarkers {
		if strings.Contains(out, marker) {
			t.Fatalf("SR Linux CLI rejected %s:\n%s", op, out)
		}
	}
}

// t2ConfigureTrapTarget points SR Linux at the host's trap listener
// via the containerlab exec callback. This fixture uses v2c traps with community
// "public"; the SR Linux v3 trap-target scenario is not implemented here.
//
// The SR Linux CLI syntax targets the 24.x release line; if the
// commands are rejected, this function is the iteration target.
func t2ConfigureTrapTarget(t *testing.T, trapTargetAddr string) {
	t.Helper()
	execFn := testenv.T2Exec()
	if execFn == nil {
		t.Fatal("testenv.T2Exec() is nil; t2 TestMain did not seed the exec callback")
	}
	host, port, err := net.SplitHostPort(trapTargetAddr)
	if err != nil {
		t.Fatalf("parse trap-target %q: %v", trapTargetAddr, err)
	}
	cli := fmt.Sprintf(`enter candidate
/ system snmp trap-receiver flowseer-collector {
    admin-state enable
    address %s
    port %s
    network-instance mgmt
    community public
    version v2c
}
commit save
`, host, port)
	out, err := execFn(cli)
	if err != nil {
		t.Fatalf("configure trap-target: %v\noutput: %s", err, out)
	}
	assertSRLinuxCliOK(t, "configure trap-target", out)
}

// TestT2_Trap_ColdStart pins the coldStart-at-boot scenario.
//
// Currently skipped: coldStart is emitted at SR Linux boot, which
// happens before this test sets up the trap listener. Catching it
// requires moving trap-listener setup into the T2 TestMain (so the
// listener exists before containerlab deploy) — a planned follow-up.
func TestT2_Trap_ColdStart(t *testing.T) {
	t.Skip("coldStart fires at SR Linux boot, before tests run; requires trap-listener setup in TestMain before containerlab deploy")
}

// TestT2_Trap_LinkDownLinkUp pins the link-trap scenario against
// the live SR Linux NOS. Sequence:
//
//  1. Bind a host trap listener (via snmp.ListenTraps) on the
//     pinned t2TrapPort.
//  2. Configure SR Linux to send v2c traps to the host's trap
//     address (host.docker.internal:<port> on macOS by default;
//     overridable via testenv.EnvT2HostFromContainer).
//  3. Toggle ethernet-1/1 admin-state disable → expect linkDown.
//  4. Toggle admin-state enable → expect linkUp.
//
// Each WaitForTrap uses a generous 30s window because SR Linux's
// configurable trap-throttling means traps can take several seconds
// to leave the box after the admin-state commit.
//
// The two WaitForTrap calls share one TrapStream. This works because
// WaitForTrap uses the Scanner-style Next()/Current() surface which
// does not signal-stop the stream on consumer exit. See waiter.go's
// ownership contract. A failed wait closes the stream and fails this test.
func TestT2_Trap_LinkDownLinkUp(t *testing.T) {
	ts, srLinuxTrapTarget := t2StartTrapListener(t)
	t2ConfigureTrapTarget(t, srLinuxTrapTarget)

	execFn := testenv.T2Exec()
	const iface = "ethernet-1/1"
	enable := fmt.Sprintf(`enter candidate
/ interface %s admin-state enable
commit save
`, iface)
	restored := false
	t.Cleanup(func() {
		if restored {
			return
		}
		out, err := execFn(enable)
		if err != nil {
			t.Errorf("restore admin-state enable: %v\noutput: %s", err, out)
			return
		}
		assertSRLinuxCliOK(t, "restore admin-state enable", out)
	})

	disable := fmt.Sprintf(`enter candidate
/ interface %s admin-state disable
commit save
`, iface)
	out, err := execFn(disable)
	if err != nil {
		t.Fatalf("admin-state disable: %v\noutput: %s", err, out)
	}
	assertSRLinuxCliOK(t, "admin-state disable", out)

	linkDownOID := snmp.MustOID(1, 3, 6, 1, 6, 3, 1, 1, 5, 3)
	if _, err := WaitForTrap(context.Background(), ts, hasTrapOID(linkDownOID), 30*time.Second); err != nil {
		t.Fatalf("linkDown wait: %v", err)
	}

	out, err = execFn(enable)
	if err != nil {
		t.Fatalf("admin-state enable: %v\noutput: %s", err, out)
	}
	assertSRLinuxCliOK(t, "admin-state enable", out)
	restored = true

	linkUpOID := snmp.MustOID(1, 3, 6, 1, 6, 3, 1, 1, 5, 4)
	if _, err := WaitForTrap(context.Background(), ts, hasTrapOID(linkUpOID), 30*time.Second); err != nil {
		t.Fatalf("linkUp wait: %v", err)
	}
}

// TestT2_Trap_V3 covers v3 USM trap reception from a real NOS.
//
// The package implements and VERIFIES v3 trap *and* inform
// reception against a live external SNMP implementation — see
// TestNetSNMP_V3TrapReception / TestNetSNMP_V3InformReception, which drive the system Net-SNMP
// snmptrap/snmpinform CLIs into a native listener (discovery → time-sync →
// ack handshake included). That is the un-skipped, live-peer proof
// of v3 trap reception, and it runs in plain `go test` wherever
// Net-SNMP is installed — no Docker.
//
// The SR-Linux-sourced variant (a v3 USM trap-target stanza in the
// containerlab template, asserted through a native listener) is the
// remaining NOS-specific coverage. It is not authored blind here because it
// cannot be validated without the SR Linux image; tracked as a follow-up so
// the assertion is not a fabricated, never-run scaffold.
func TestT2_Trap_V3(t *testing.T) {
	t.Skip("native v3 trap/inform reception is verified against live Net-SNMP in package snmp (TestNetSNMP_V3*); SR Linux NOS-sourced v3 trap-target coverage is a tracked follow-up")
}
