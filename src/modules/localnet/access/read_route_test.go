package access_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
)

// snmpDevice registers "dev-1" reading over a real SNMP walk rather than a
// ReadOverride: this file is about which route a read takes, and an override
// takes none of them.
func snmpDevice(t *testing.T, l *access.Lane, aliased bool, openShell func(context.Context, *edgev1.DeviceCredential, string) (access.ShellSession, error)) {
	t.Helper()
	session := &walkingSession{vbs: interfaceRows("ethernet 1/1/1", snmpDescription, aliased)}
	if err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		OpenSNMP: func(context.Context, *edgev1.DeviceCredential) (access.SNMPSession, error) {
			return access.SNMPSession{Session: session, Close: func() error { return nil }}, nil
		},
		OpenShell: openShell,
	}); err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}
}

// snmpDescription is what the SNMP route reads and shellAdapter never
// returns, so an observation names the route that produced it.
const snmpDescription = "as found on the switch"

func readObservation(t *testing.T, l *access.Lane) (description string, protocol inventoryv1.ManagementProtocol) {
	t.Helper()
	result, err := l.Submit(context.Background(), access.SubmitOptions{DeviceKey: "dev-1", Request: readRequest()})
	if err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
	obs := result.GetObservation()
	return obs.GetDescription(), obs.GetProvenance().GetProtocol()
}

// A read the SNMP route answers does not open the device's shell.
//
// This is the assertion the lab run needed and did not have. Both halves of
// this were individually right: the capability decides its own route and has
// always accepted a nil shell, and the lane holds the factory that can open
// one. Between them the lane opened it first and handed over the result, so
// the decision was made before the code that makes it ran — with a comment
// above it stating the opposite, which is how it survived being read.
//
// Opening it is not a wasted connection, it is a login. Every SNMP read on a
// switch that caps concurrent sessions became an SSH session; and a device
// whose shell cannot be opened at all — this deployment's, whose read
// credential is SNMP material — had no readable interfaces whatsoever.
//
// The weaker assertion is the one to avoid here. "The read succeeds when the
// shell open fails" holds for an implementation that still opens eagerly and
// swallows the error, which is half the fix wearing the whole one's clothes.
// So what is asserted is that no open was attempted.
func TestALaneReadThatSNMPAnswersNeverOpensTheShell(t *testing.T) {
	l := laneWithReporter(t, &recordingReporter{}, noopDeliverer{})
	shell := &fakeShell{}
	snmpDevice(t, l, true, shell.open)

	description, protocol := readObservation(t, l)

	if opened, _, _, _ := shell.snapshot(); len(opened) != 0 {
		t.Errorf("the lane opened %d shell sessions for a read SNMP answered completely", len(opened))
	}
	if description != snmpDescription {
		t.Errorf("observation description = %q, want %q — the read was not answered by the SNMP route", description, snmpDescription)
	}
	if protocol != inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP {
		t.Errorf("observation provenance protocol = %v, want SNMP", protocol)
	}
}

// And a shell that cannot be opened does not cost the lane a read SNMP
// answered.
//
// Measured against the lab ICX7150: every ReadInterface failed with
// agent/device-session while the SNMP walk was returning a complete
// observation of the interface asked about. The failing open is the
// deployment's own — a shell login offered SNMP credential material — so
// this is the live case, not a hypothetical one.
func TestALaneReadSurvivesAShellItCannotOpen(t *testing.T) {
	l := laneWithReporter(t, &recordingReporter{}, noopDeliverer{})
	var attempts atomic.Int64
	snmpDevice(t, l, true, func(context.Context, *edgev1.DeviceCredential, string) (access.ShellSession, error) {
		attempts.Add(1)
		return access.ShellSession{}, errors.New("ssh: unable to authenticate")
	})

	description, _ := readObservation(t, l)

	if description != snmpDescription {
		t.Errorf("observation description = %q, want %q", description, snmpDescription)
	}
	if got := attempts.Load(); got != 0 {
		t.Errorf("the lane attempted %d shell logins for a read SNMP answered completely", got)
	}
}

// The fallback route still exists, and the lane still reaches it.
//
// The partner to both tests above: laziness that never opens the shell at
// all would pass them and would have quietly deleted the SSH route that
// the route-fallback rule requires for a device whose SNMP is incomplete. Here ifAlias is
// unobserved, so the SNMP read is PARTIAL, and the observation must come
// back over the shell.
func TestALaneReadFallsBackToTheShellWhenSNMPIsIncomplete(t *testing.T) {
	l := laneWithReporter(t, &recordingReporter{}, noopDeliverer{})
	shell := &fakeShell{}
	snmpDevice(t, l, false, shell.open)

	description, protocol := readObservation(t, l)

	opened, _, _, closes := shell.snapshot()
	if len(opened) != 1 {
		t.Errorf("the lane opened %d shell sessions for a read only the shell could answer, want 1", len(opened))
	}
	if closes != len(opened) {
		t.Errorf("the lane opened %d shell sessions and closed %d", len(opened), closes)
	}
	if description != "uplink to core" {
		t.Errorf("observation description = %q, want the shell adapter's %q", description, "uplink to core")
	}
	if protocol != inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SSH {
		t.Errorf("observation provenance protocol = %v, want SSH", protocol)
	}
}
