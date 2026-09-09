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

	// dir and provisioning are what a second agent needs to be started over
	// the first one's state, which is how an edge restart is staged.
	dir          string
	provisioning *edgev1.EdgeProvisioning
}

// restartAgent stops this deployment's agent and starts another over the same
// state directory, which is what an edge restarting looks like: the same
// identity, the same enrollment, no memory of what it was doing.
func (d *deployment) restartAgent(t *testing.T) {
	t.Helper()
	d.agent.shutdown()
	d.agent = startAgent(t, d.dir, d.provisioning, d.central.baseURL(), d.device, d.clock)
}

// assemble brings up the whole thing: central, an edge created through the
// admin API, the registry rewritten to list this fixture's device to that
// edge, and an agent enrolled against it.
//
// The restart in the middle is not ceremony. Central draws the edge
// identifier itself and reads its registry once at start, so the registry
// that lists a device to an edge cannot be written until the edge exists.
func assemble(t *testing.T) *deployment {
	t.Helper()
	dir := t.TempDir()
	writeCredentials(t, filepath.Join(dir, "credentials"))

	// A registry naming an edge that does not exist yet. Central serves no
	// device from it, which is all this first start is for.
	registryPath := writeRegistry(t, filepath.Join(dir, "registry.textproto"),
		"0192e6a0-0000-7000-8000-00000000dead", fixtureHorizon)

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
	writeRegistry(t, registryPath, edgeID, fixtureHorizon)
	c.start()

	device := newFakeDevice("as found")
	clock := newTestClock()
	a := startAgent(t, dir, provisioning, c.baseURL(), device, clock)

	return &deployment{
		central: c, agent: a, device: device, clock: clock, edgeID: edgeID,
		dir: dir, provisioning: provisioning,
	}
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
	d := assemble(t)

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
	d := assemble(t)

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

// waitForFingerprint blocks until central has recorded the device's firmware
// epoch, which every mutation intent has to name.
//
// It waits on the status alone. Central learns the epoch from the onboarding
// report the edge sends unprompted at start, so an operator's first change to
// a freshly onboarded device needs nothing to have happened first — which is
// the point, and which this helper is the shortest statement of.
func (d *deployment) waitForFingerprint(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		status, err := d.central.devices().GetDeviceAccessStatus(context.Background(),
			connect.NewRequest(devicev1.GetDeviceAccessStatusRequest_builder{Device: deviceRef()}.Build()))
		if err == nil {
			if fingerprint := status.Msg.GetFirmwareFingerprint(); fingerprint != "" {
				return fingerprint
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
func (d *deployment) apply(t *testing.T, key, description, fingerprint string) *accessv1.MutationState {
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
			Intent: intent,
		}.Build()))
	if err != nil {
		t.Fatalf("ApplyInterfaceDescription(%q): %v", description, err)
	}
	return response.Msg.GetMutation()
}

// waitUntilResolved blocks until the lane holds no unresolved mutation, and
// returns the status it saw.
func (d *deployment) waitUntilResolved(t *testing.T) *devicev1.GetDeviceAccessStatusResponse {
	t.Helper()
	// Generous, and deliberately so: a mutation whose observation did not
	// verify rests in recovery, and the lane's recovery poll runs on a
	// thirty-second interval it does not shorten for anybody. A budget under
	// that would report "never resolved" for a mutation that had simply not
	// been looked at yet, which is a failure this build has already produced
	// once by measuring a thirty-second loop over twenty seconds.
	deadline := time.Now().Add(150 * time.Second)
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
	d := assemble(t)
	fingerprint := d.waitForFingerprint(t)

	d.apply(t, "0192e6a0-0000-7000-8000-0000000a0001", "uplink to core", fingerprint)
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

// abandon writes the terminal state on an open mutation, as an operator does
// for an edge that is not coming back.
func (d *deployment) abandon(t *testing.T, sequence uint64) {
	t.Helper()
	_, err := d.central.devices().AbandonMutation(context.Background(),
		connect.NewRequest(devicev1.AbandonMutationRequest_builder{
			Device:   deviceRef(),
			Sequence: proto.Uint64(sequence),
			Actor: accessv1.Actor_builder{
				Operator: accessv1.OperatorRef_builder{Subject: proto.String("e2e-operator")}.Build(),
			}.Build(),
		}.Build()))
	if err != nil {
		t.Fatalf("AbandonMutation(%d): %v", sequence, err)
	}
}

// An abandoned mutation is resolved by restoring what central expected, and
// the device ends holding it.
//
// The sequence is the one an operator actually meets: a change is applied and
// verified, a second change is started and does not come back, the operator
// abandons it rather than waiting, and then decides between the device's state
// and central's. Restoring is the decision that puts the device back.
//
// The device is made to hide the change rather than to fail or to hang, and
// the difference is the whole scenario. A write that fails leaves the device
// unchanged and central able to dispose the mutation itself; a write that
// hangs never reaches the device at all, since the lane takes its baseline
// read first. A write that lands and is not yet visible to a read is the
// delayed-apply case this system exists for, and the only one where nobody
// can say what the device carries and an operator has a decision to make.
func TestAnAbandonedMutationIsResolvedByRestoringWhatCentralExpected(t *testing.T) {
	d := assemble(t)
	fingerprint := d.waitForFingerprint(t)

	d.apply(t, "0192e6a0-0000-7000-8000-0000000b0001", "uplink to core", fingerprint)
	d.waitUntilResolved(t)

	d.device.pinReads("uplink to core")
	second := d.apply(t, "0192e6a0-0000-7000-8000-0000000b0002", "mistake", fingerprint)
	if second.GetSequence() == 0 {
		t.Fatal("the second mutation was admitted at no sequence")
	}

	// Waited for on two conditions, and the second is the one that matters.
	//
	// Central admits a mutation at its own sequence before the edge has seen
	// it, so "the record's unresolved mutation is mine" is true immediately
	// and says nothing about the edge. Abandoning there takes a different
	// path on purpose: a Dispose of a mutation the edge never reported
	// admitted closes the lane and owes a hold resolution, because there is
	// nothing on the device to be unsure about. That is correct behavior and
	// it is not this scenario — it leaves nothing to resolve, and a test that
	// raced into it would be testing the wrong branch while looking like it
	// tested this one.
	//
	// So: wait until the edge has reported the dispatch admitted, which moves
	// the phase to POSSIBLY_APPLIED, and until the command has actually
	// reached the device. Only then is the mutation one whose effect nobody
	// can establish.
	deadline := time.Now().Add(60 * time.Second)
	for {
		_, _, _, commands, _ := d.device.snapshot()
		status, err := d.central.devices().GetDeviceAccessStatus(context.Background(),
			connect.NewRequest(devicev1.GetDeviceAccessStatusRequest_builder{Device: deviceRef()}.Build()))
		if err == nil &&
			status.Msg.GetUnresolved().GetSequence() == second.GetSequence() &&
			status.Msg.GetUnresolved().GetPhase() == accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED &&
			slices.Contains(commands, fixtureInterface+"=mistake") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the second mutation never reached the device with central holding it open (commands %q, error %v)",
				commands, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	d.abandon(t, second.GetSequence())
	// The change becomes visible. From here a read tells the truth again,
	// which is what lets the restore verify its own work.
	d.device.unpinReads()

	// The edge has to say how the mutation ended before an operator may
	// decide what to do about it: central owes it a terminal acknowledgement
	// for the abandoned sequence and refuses a resolution until that is
	// confirmed. That is right — resolving against a mutation whose fate the
	// edge has not accepted would be deciding without the fact the decision
	// is about.
	//
	// Retried rather than waited on, because there is nothing to wait on.
	// GetDeviceAccessStatus does not carry whether the terminal ack is still
	// owed, so the only signal an operator has that it is safe to resolve is
	// that the resolution stops being refused. That is worth knowing and is
	// noted in the plan; it is not this test's subject.
	restore := connect.NewRequest(devicev1.ResolveDesynchronizationRequest_builder{
		Device:   deviceRef(),
		Sequence: proto.Uint64(second.GetSequence()),
		Actor: accessv1.Actor_builder{
			Operator: accessv1.OperatorRef_builder{Subject: proto.String("e2e-operator")}.Build(),
		}.Build(),
		Restore: &devicev1.RestoreExpectedDecision{},
	}.Build())

	deadline = time.Now().Add(120 * time.Second)
	var err error
	for {
		if _, err = d.central.devices().ResolveDesynchronization(context.Background(), restore); err == nil {
			break
		}
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("ResolveDesynchronization(restore): %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the restore was refused for two minutes; last refusal: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	deadline = time.Now().Add(120 * time.Second)
	for {
		description, _, _, _, _ := d.device.snapshot()
		if description == "uplink to core" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the device holds %q, want the restored %q", description, "uplink to core")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The mistake has to have reached the device, or there was nothing to
	// restore and this test proves nothing. Without this the device would
	// already hold "uplink to core" from the first mutation and every
	// assertion above would pass on a run where the second one never landed.
	_, _, _, commands, _ := d.device.snapshot()
	want := []string{
		fixtureInterface + "=uplink to core",
		fixtureInterface + "=mistake",
		fixtureInterface + "=uplink to core",
	}
	if !slices.Equal(commands, want) {
		t.Errorf("the device was asked to run %q, want %q", commands, want)
	}

	status := d.waitUntilResolved(t)
	if status.GetUnresolved() != nil {
		t.Errorf("the lane is still held by %v after the restore resolved", status.GetUnresolved())
	}
}

// Central killed after the checkpoint and before the result, restarted, and
// the mutation reaches the same outcome.
//
// The point is that the record is the only thing carrying the operation
// across the gap. Central holds the mutation at POSSIBLY_APPLIED in its
// journal, dies, comes back with the same state directory, and has to pick
// the exchange up from what it wrote rather than from anything it remembered:
// the edge is still holding the operation open and still owes a result.
//
// The device hides the change until after the restart so that the result
// cannot have arrived before central went down. Without that the test would
// race, and the run where it lost would be a run where nothing was killed
// between anything.
func TestAMutationSurvivesCentralRestartingUnderIt(t *testing.T) {
	d := assemble(t)
	fingerprint := d.waitForFingerprint(t)

	d.device.pinReads("as found")
	d.apply(t, "0192e6a0-0000-7000-8000-0000000c0001", "uplink to core", fingerprint)

	// The command has reached the device and central has recorded that it
	// may have applied, which is exactly the state the restart has to be
	// survivable from.
	deadline := time.Now().Add(60 * time.Second)
	for {
		_, _, _, commands, _ := d.device.snapshot()
		status, err := d.central.devices().GetDeviceAccessStatus(context.Background(),
			connect.NewRequest(devicev1.GetDeviceAccessStatusRequest_builder{Device: deviceRef()}.Build()))
		if err == nil &&
			status.Msg.GetUnresolved().GetPhase() == accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED &&
			slices.Contains(commands, fixtureInterface+"=uplink to core") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the mutation never reached the device with central holding it at POSSIBLY_APPLIED (commands %q, error %v)",
				commands, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	d.central.shutdown()
	d.central.start()
	d.device.unpinReads()

	status := d.waitUntilResolved(t)

	rows := status.GetInterfaces()
	if len(rows) != 1 || rows[0].GetDescription() != "uplink to core" {
		t.Errorf("after the restart central holds %v, want one row reading %q", rows, "uplink to core")
	}
	description, _, _, commands, _ := d.device.snapshot()
	if description != "uplink to core" {
		t.Errorf("the device holds %q, want %q", description, "uplink to core")
	}
	// One write, not two. A central that came back having forgotten the
	// mutation would dispatch it again, and the device would show the command
	// twice — which is the failure this scenario is really about.
	if got := slices.Contains(commands, fixtureInterface+"=uplink to core"); !got || len(commands) != 1 {
		t.Errorf("the device was asked to run %q, want exactly one description write", commands)
	}
}

// An edge that restarts under an open mutation re-onboards, and central
// resumes the mutation rather than running it again.
//
// This is what the onboarding report is for. The edge sends it unprompted at
// start; central clears the per-dispatch confirmations it had recorded for
// this device and re-sends the mutation that is still open. Because the
// record shows the command may already have reached the device, the dispatch
// carries resume, and the edge admits it straight into recovery instead of
// executing it a second time.
//
// The assertion is the count of writes. A central that forgot the mutation
// would dispatch it fresh and the device would be written twice; a central
// that re-dispatched without resume would do the same. One write is the only
// outcome that distinguishes a resumed mutation from a repeated one, and
// repeating it is the thing this whole design exists to avoid — a switch
// configured twice by a system that lost track of whether it had done it
// once.
func TestAnEdgeRestartingUnderAMutationResumesItRatherThanRepeatingIt(t *testing.T) {
	d := assemble(t)
	fingerprint := d.waitForFingerprint(t)

	// The change stays invisible, so the mutation cannot complete before the
	// restart and the edge is genuinely holding it open when it goes down.
	d.device.pinReads("as found")
	d.apply(t, "0192e6a0-0000-7000-8000-0000000d0001", "uplink to core", fingerprint)

	deadline := time.Now().Add(60 * time.Second)
	for {
		_, _, _, commands, _ := d.device.snapshot()
		status, err := d.central.devices().GetDeviceAccessStatus(context.Background(),
			connect.NewRequest(devicev1.GetDeviceAccessStatusRequest_builder{Device: deviceRef()}.Build()))
		if err == nil &&
			status.Msg.GetUnresolved().GetPhase() == accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED &&
			slices.Contains(commands, fixtureInterface+"=uplink to core") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the mutation never reached the device with central holding it open (commands %q, error %v)",
				commands, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	d.restartAgent(t)
	d.device.unpinReads()

	status := d.waitUntilResolved(t)
	if rows := status.GetInterfaces(); len(rows) != 1 || rows[0].GetDescription() != "uplink to core" {
		t.Errorf("central holds %v, want one row reading %q", rows, "uplink to core")
	}

	_, _, _, commands, _ := d.device.snapshot()
	want := []string{fixtureInterface + "=uplink to core"}
	if !slices.Equal(commands, want) {
		t.Errorf("the device was asked to run %q, want %q — the mutation was repeated rather than resumed",
			commands, want)
	}
}
