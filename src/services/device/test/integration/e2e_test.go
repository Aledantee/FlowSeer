package integration_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	connect "connectrpc.com/connect"

	devicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"

	"google.golang.org/protobuf/proto"
)

// deployment is central, an edge, and a device, assembled and running.
type deployment struct {
	central *central
	agent   *agent
	device  *fakeDevice
	clock   *testClock
	edgeID  string
}

// assemble brings up the whole thing: central, an edge created through the
// admin API, the registry rewritten to list this fixture's device to that
// edge, and an agent enrolled against it.
//
// The restart in the middle is not ceremony. Central draws the edge
// identifier itself and reads its registry once at start, so the registry
// that lists a device to an edge cannot be written until the edge exists.
func assemble(t *testing.T, horizon time.Duration) *deployment {
	t.Helper()
	dir := t.TempDir()
	writeCredentials(t, filepath.Join(dir, "credentials"))

	// A registry naming an edge that does not exist yet. Central serves no
	// device from it, which is all this first start is for.
	registryPath := writeRegistry(t, filepath.Join(dir, "registry.textproto"),
		"0192e6a0-0000-7000-8000-00000000dead", horizon)

	c := newCentral(t, dir, registryPath)
	c.start()

	created, err := c.admin().CreateEdge(context.Background(), connect.NewRequest(&edgev1.CreateEdgeRequest{}))
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	provisioning := created.Msg.GetProvisioning()
	edgeID := created.Msg.GetEdge().GetConfig().GetRef().GetEdge().GetId()
	if edgeID == "" {
		t.Fatal("CreateEdge returned an edge with no identifier")
	}

	// Rewritten and reread. The state directory is untouched, so the
	// certificate, the JetStream store and the edge record all survive.
	c.shutdown()
	writeRegistry(t, registryPath, edgeID, horizon)
	c.start()

	device := newFakeDevice("as found")
	clock := newTestClock()
	a := startAgent(t, dir, provisioning, c.baseURL(), device, clock)

	return &deployment{central: c, agent: a, device: device, clock: clock, edgeID: edgeID}
}

// The agent enrolls against a real central, attaches its bus, and onboards
// the device central's registry lists for it.
//
// Everything past the attachment is what U8e's unit tests could not reach: it
// takes a live hub for the attachment to succeed, and a successful attachment
// for the lane to exist. What proves onboarding happened is the device
// itself — the agent opened a session against the address the registry named,
// which means it listed the device, resolved its endpoint, and acquired a
// credential for the identity probe.
func TestAnAgentOnboardsTheDeviceCentralListsForIt(t *testing.T) {
	d := assemble(t, 30*time.Second)

	deadline := time.Now().Add(60 * time.Second)
	for {
		_, endpoints, _, _, credentials := d.device.snapshot()
		if len(endpoints) > 0 && credentials > 0 {
			want := "172.16.0.6 snmp=172.16.0.6:161 ssh=172.16.0.6:22"
			if endpoints[0] != want {
				t.Errorf("the agent built a session for %q, want %q", endpoints[0], want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the device was never onboarded: %d endpoints, %d credentials acquired", len(endpoints), credentials)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// An operator's read reaches the device through the whole stack and comes
// back, and central learns the device's firmware epoch from it.
//
// This is the round trip in both directions: the call enters central's API,
// central opens a read on the device's record and waits, the edge takes it
// off the dispatch stream it holds open, the lane acquires a credential and
// opens a session, the device answers, and the observation travels back as a
// report that central matches to the waiting call.
//
// The fingerprint is asserted because it arrives by a different route than
// the description does. The description is what the device was asked for;
// the fingerprint is what the lane's own identity probe learned, carried on
// the observation's provenance, and central records it from there. A read
// that returned the right description with no provenance would be a read
// central could not tell you the epoch of.
func TestAnOperatorsReadReachesTheDeviceAndComesBack(t *testing.T) {
	d := assemble(t, 30*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	read, err := d.central.devices().ReadInterface(ctx,
		connect.NewRequest(devicev1.ReadInterfaceRequest_builder{
			Device: deviceRef(), InterfaceName: proto.String(fixtureInterface),
		}.Build()))
	if err != nil {
		t.Fatalf("ReadInterface: %v", err)
	}

	observation := read.Msg.GetInterface()
	if got := observation.GetDescription(); got != "as found" {
		t.Errorf("the read returned the description %q, want %q", got, "as found")
	}
	fingerprint := observation.GetProvenance().GetFirmwareFingerprint()
	if fingerprint == "" {
		t.Error("the observation carries no firmware fingerprint; central cannot say which epoch it was taken under")
	}

	status, err := d.central.devices().GetDeviceAccessStatus(ctx,
		connect.NewRequest(devicev1.GetDeviceAccessStatusRequest_builder{Device: deviceRef()}.Build()))
	if err != nil {
		t.Fatalf("GetDeviceAccessStatus: %v", err)
	}
	if got := status.Msg.GetFirmwareFingerprint(); got != fingerprint {
		t.Errorf("central holds the fingerprint %q, want the %q the observation carried", got, fingerprint)
	}
	if status.Msg.GetUnresolved() != nil {
		t.Errorf("the lane is held by %v after a read; a read must not hold it", status.Msg.GetUnresolved())
	}
}

// waitForFingerprint blocks until central has recorded the firmware
// fingerprint, which every mutation intent must name.
//
// It gets there by issuing a read, which is a workaround for a defect and
// says so rather than looking deliberate: the fingerprint should arrive from
// onboarding, and central records it only from an observation's provenance
// because nothing on the edge sends the report that would carry it. When that
// is fixed this becomes a wait on the status alone.
func (d *deployment) waitForFingerprint(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, err := d.central.devices().ReadInterface(ctx,
			connect.NewRequest(devicev1.ReadInterfaceRequest_builder{
				Device: deviceRef(), InterfaceName: proto.String(fixtureInterface),
			}.Build()))
		cancel()
		if err == nil {
			status, statusErr := d.central.devices().GetDeviceAccessStatus(context.Background(),
				connect.NewRequest(devicev1.GetDeviceAccessStatusRequest_builder{Device: deviceRef()}.Build()))
			if statusErr == nil {
				if fingerprint := status.Msg.GetFirmwareFingerprint(); fingerprint != "" {
					return fingerprint
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("central never recorded a firmware fingerprint for the device (last error: %v)", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// apply records one interface-description intent and returns the mutation as
// admitted. key must be a UUID: the schema constrains the idempotency key to
// one, so a readable string is refused before the handler sees it.
func (d *deployment) apply(t *testing.T, key, description, fingerprint string, validateOnly bool) *accessv1.MutationState {
	t.Helper()
	intent := accessv1.MutationIntent_builder{
		Device:         deviceRef(),
		IdempotencyKey: proto.String(key),
		Actor: accessv1.Actor_builder{
			Operator: accessv1.OperatorRef_builder{Subject: proto.String("e2e-operator")}.Build(),
		}.Build(),
		AccessPolicy:                policyv1.AccessPolicyHandle_builder{Key: proto.String(fixturePolicyKey), Version: proto.Uint64(1)}.Build(),
		ExpectedFirmwareFingerprint: proto.String(fingerprint),
		InterfaceDescription: accessv1.InterfaceDescriptionChange_builder{
			InterfaceName: proto.String(fixtureInterface),
			Description:   proto.String(description),
		}.Build(),
	}.Build()

	response, err := d.central.devices().ApplyInterfaceDescription(context.Background(),
		connect.NewRequest(devicev1.ApplyInterfaceDescriptionRequest_builder{
			Intent: intent, ValidateOnly: proto.Bool(validateOnly),
		}.Build()))
	if err != nil {
		t.Fatalf("ApplyInterfaceDescription(%q, validate_only=%v): %v", description, validateOnly, err)
	}
	return response.Msg.GetMutation()
}

// waitUntilResolved blocks until the lane holds no unresolved mutation, and
// returns the status it saw.
func (d *deployment) waitUntilResolved(t *testing.T) *devicev1.GetDeviceAccessStatusResponse {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last *devicev1.GetDeviceAccessStatusResponse
	for {
		status, err := d.central.devices().GetDeviceAccessStatus(context.Background(),
			connect.NewRequest(devicev1.GetDeviceAccessStatusRequest_builder{Device: deviceRef()}.Build()))
		if err == nil {
			last = status.Msg
			if last.GetUnresolved() == nil && last.GetHighWatermark() > 0 {
				return last
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the mutation never resolved; last status: %v (error %v)", last, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// An operator's interface description reaches the device and comes back as an
// observation, with the lane left free.
//
// This is the assembled run: an operator call into central, a dispatch down
// the stream the agent holds open, the lane submitting it to the device, the
// reports coming back, and central closing the record. Nothing here reaches
// into either host — the whole exchange is observed through central's own API
// and the device's own state, which is what an operator and a switch would
// each see.
func TestAnAppliedDescriptionReachesTheDeviceAndComesBack(t *testing.T) {
	d := assemble(t, 30*time.Second)
	fingerprint := d.waitForFingerprint(t)

	d.apply(t, "0192e6a0-0000-7000-8000-0000000a0001", "uplink to core", fingerprint, false)
	status := d.waitUntilResolved(t)

	description, _, hostKeys, commands, _ := d.device.snapshot()
	if description != "uplink to core" {
		t.Errorf("the device's description is %q, want %q", description, "uplink to core")
	}
	if len(commands) != 1 || commands[0] != fixtureInterface+"=uplink to core" {
		t.Errorf("the device was asked to run %v, want one description write", commands)
	}
	// The mutation went over a shell session opened against the host key the
	// registry pins. A lane that skipped the pin would still have written the
	// description, so this is asserted rather than assumed.
	//
	// Only the mutation's session carries one, and that is central being
	// right rather than this being lax: the pin travels with a shell login
	// and only with one, and this fixture's read credential is SNMP because
	// the identity probe acquires under the same handle. The reads here fall
	// back to the shell holding SNMP material, which is an artifact of a fake
	// that answers only the identity probe over SNMP — a real device answers
	// the read and never reaches that route.
	if !slices.Contains(hostKeys, fixtureHostKey) {
		t.Errorf("no shell session required the pinned host key %q (saw %q); the mutation did not take the route it should have",
			fixtureHostKey, hostKeys)
	}

	rows := status.GetInterfaces()
	if len(rows) != 1 {
		t.Fatalf("central holds %d interface rows, want one", len(rows))
	}
	if got := rows[0].GetInterfaceName(); got != fixtureInterface {
		t.Errorf("the row is for %q, want %q", got, fixtureInterface)
	}
	if got := rows[0].GetDescription(); got != "uplink to core" {
		t.Errorf("the row carries the description %q, want %q", got, "uplink to core")
	}
}
