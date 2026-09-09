package integration_test

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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
	response, err := d.central.devices().ApplyInterfaceDescription(context.Background(),
		connect.NewRequest(devicev1.ApplyInterfaceDescriptionRequest_builder{
			Intent: d.intent(key, description, fingerprint),
		}.Build()))
	if err != nil {
		t.Fatalf("ApplyInterfaceDescription(%q): %v", description, err)
	}
	return response.Msg.GetMutation()
}

// intent is the one an operator sends, dry run or not.
func (d *deployment) intent(key, description, fingerprint string) *accessv1.MutationIntent {
	return accessv1.MutationIntent_builder{
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
}

// validate sends the same intent apply does with validate_only set, and
// returns the response. Central checks the intent and records nothing.
func (d *deployment) validate(t *testing.T, key, description, fingerprint string) *devicev1.ApplyInterfaceDescriptionResponse {
	t.Helper()
	response, err := d.central.devices().ApplyInterfaceDescription(context.Background(),
		connect.NewRequest(devicev1.ApplyInterfaceDescriptionRequest_builder{
			Intent:       d.intent(key, description, fingerprint),
			ValidateOnly: proto.Bool(true),
		}.Build()))
	if err != nil {
		t.Fatalf("ApplyInterfaceDescription(%q, validate_only): %v", description, err)
	}
	return response.Msg
}

// waitUntilResolved blocks until the lane holds no unresolved mutation, and
// returns the status it saw.
func (d *deployment) waitUntilResolved(t *testing.T) *devicev1.GetDeviceAccessStatusResponse {
	t.Helper()
	started := time.Now()
	deadline := started.Add(fixtureResolveDeadline)
	var last *devicev1.GetDeviceAccessStatusResponse
	// Sampled while waiting and logged only on the way out.
	//
	// A mutation that does not resolve is the hardest failure in this package
	// to diagnose after the fact, because the two explanations — recovery
	// looked and saw nothing, or recovery never looked — differ only in
	// whether the device was asked, and nothing in the final status says
	// which. The session count is what separated them both times it came up.
	// A failure that carries it costs one line more than one that does not,
	// and this failure has twice been seen on a machine that was not the one
	// that could reproduce it.
	//
	// What the trail cannot show is why a poll that ran came back with
	// nothing: that reason is inside central and reaches only its own log,
	// graded at DEBUG because an operator turns it up when they need it. The
	// U8f diagnosis needed exactly that and nothing else would have served.
	// So if a trail ever says the polls ran and nothing resolved, the next
	// step is one line — set log_level to LOG_LEVEL_DEBUG in the fixture's
	// central configuration and run it again. It is not on by default because
	// the host logs to stderr rather than through the test, so every run
	// would carry it whether or not anything failed.
	var trail []string
	var lastErr error
	nextSample := 15 * time.Second
	for {
		if elapsed := time.Since(started); elapsed > nextSample {
			nextSample += 15 * time.Second
			_, _, _, cmds, sessions := d.device.snapshot()
			trail = append(trail, fmt.Sprintf("t+%.0fs sessions=%d writes=%d phase=%v status_err=%v",
				elapsed.Seconds(), sessions, len(cmds), last.GetUnresolved().GetPhase(), lastErr))
		}
		status, err := d.central.devices().GetDeviceAccessStatus(context.Background(),
			connect.NewRequest(devicev1.GetDeviceAccessStatusRequest_builder{Device: deviceRef()}.Build()))
		lastErr = err
		if err == nil {
			last = status.Msg
			if last.GetUnresolved() == nil && last.GetHighWatermark() > 0 {
				return last
			}
		}
		if time.Now().After(deadline) {
			t.Logf("the device over the wait, sampled every 15s: %v", trail)
			// The audit stream is the only place a freeze is visible from
			// out here, and whether the lane froze is the difference between
			// two explanations of this failure that look identical in the
			// trail: an edge that never got the dispatch because its lane was
			// frozen, and one that got it and could not finish. Two missed
			// heartbeats freeze, each bounded by the heartbeat interval, so a
			// central restart long enough to miss two crosses a threshold —
			// which is the shape a failure with two durations and nothing
			// between them has.
			var kinds []string
			for _, r := range d.central.auditRecords(t) {
				kinds = append(kinds, fmt.Sprintf("%v(seq=%d)", r.WhichDetail(), r.GetSequence()))
			}
			t.Logf("the audit stream at that point: %v", kinds)
			// And where the edge actually is. The trail says whether the
			// device was asked; only the stacks say what is stopping it from
			// being asked. The recovery poll takes the device's drain lock
			// with a blocking Lock, so a poll that never runs is a poll
			// behind something that holds it, and the holder is the answer.
			t.Logf("goroutines in the access and agent packages:\n%s", accessGoroutines())
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

	// Unpinned before central comes back, not after. The recovery poll
	// acquires its read credential from central, so it cannot observe while
	// central is down — but making the change visible first means the first
	// poll after central returns sees the truth, however long the restart
	// took. Unpinning afterwards makes the test a race between the restart
	// and the next poll, which is a race it loses on a loaded machine.
	d.central.shutdown()
	d.device.unpinReads()
	d.central.start()

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

// The audit stream accounts for what was done to the device, in an order that
// makes the account usable.
//
// Three things are asserted and each answers a different way the stream could
// be useless. That the records are there at all, because a stream missing a
// record cannot be caught by an ordering check — an ordering claim is
// satisfied by a stream holding none of what it names. That the device was
// identified before anything was done to it, because a change recorded
// against a device whose epoch was never established is a change nobody can
// attribute. And that nothing is released before its own evidence, which is
// the property the whole account rests on: a stream where a release can
// precede the record justifying it cannot answer what happened, only assert
// that something did.
//
// The last one is asserted over every operation the stream holds rather than
// over this test's own, because it is a property of the stream and iterating
// what is there costs nothing.
//
// It is not a transcript. A transcript asserts whatever the system happened
// to do on the day it was written, which makes it pass by construction and
// then break on every unrelated change until its readers learn to update it
// without reading it. What is asserted here is a subsequence, so records may
// appear between the ones named.
//
// The ordering is sound because these are all the lane's own records and the
// edge delivers them synchronously: the deliverer blocks until central
// answers, and central answers only once the stream holds the record, so the
// lane cannot move past a record that is not yet durable. Central writes
// records of its own at its own moments, and no order is claimed between
// those and these.
func TestTheAuditStreamAccountsForTheChangeInOrder(t *testing.T) {
	d := assemble(t)
	fingerprint := d.waitForFingerprint(t)
	mutation := d.apply(t, "0192e6a0-0000-7000-8000-0000000f0001", "uplink to core", fingerprint)
	d.waitUntilResolved(t)

	records := d.central.auditRecords(t)
	if len(records) == 0 {
		t.Fatal("the audit stream holds nothing after a device was onboarded and changed")
	}

	sequence := mutation.GetSequence()
	discovered, verified, released := -1, -1, -1
	for i, r := range records {
		switch {
		case r.HasDiscoveryCompleted() && discovered < 0:
			discovered = i
		case r.HasPhaseTransitioned() && r.GetSequence() == sequence &&
			r.GetPhaseTransitioned().GetTo() == accessv1.OperationPhase_OPERATION_PHASE_VERIFIED:
			verified = i
		case r.HasLaneReleased() && r.GetSequence() == sequence:
			released = i
		}
	}

	// Present, each named separately so a missing one says which.
	if discovered < 0 {
		t.Error("no discovery record: the device's epoch was never accounted for")
	}
	if verified < 0 {
		t.Errorf("no record of mutation %d being verified: the change has no evidence in the account", sequence)
	}
	if released < 0 {
		t.Errorf("no record of mutation %d releasing the lane: the account does not say the operation ended", sequence)
	}
	if discovered < 0 || verified < 0 || released < 0 {
		return
	}

	// In order.
	if discovered >= verified || verified >= released {
		t.Errorf("the account reads discovery at %d, verification at %d, release at %d; want them in that order",
			discovered, verified, released)
	}

	// And the invariant, over every operation the stream holds rather than
	// this one.
	//
	// Two halves, because one of them alone is nearly vacuous. A release with
	// no earlier record for its own sequence is the account claiming an
	// operation ended without ever saying what it did — but on its own that
	// is satisfied by any earlier record at all, and a first draft of this
	// check passed while the release was moved ahead of the very transition
	// that justifies it. The half that bites is the other one: a release
	// closes the account for its operation, so nothing about that operation
	// may follow it. A record after the release is the account still being
	// written after it said the matter was settled, and it is what a reader
	// asking "what happened to this device" would have to reconcile.
	evidenced := map[uint64]bool{}
	releasedAt := map[uint64]int{}
	for i, r := range records {
		seq := r.GetSequence()
		if at, closed := releasedAt[seq]; closed {
			t.Errorf("record %d is about operation %d, which the account already closed at record %d",
				i, seq, at)
		}
		if r.HasPhaseTransitioned() {
			evidenced[seq] = true
		}
		if r.HasLaneReleased() {
			if !evidenced[seq] {
				t.Errorf("record %d releases the lane for operation %d with nothing earlier in the stream about it",
					i, seq)
			}
			releasedAt[seq] = i
		}
	}
}

// The dry run checks the intent and touches nothing.
//
// This is the last thing an operator does before the irreversible step, and
// until now no assembled test had walked it. Every unit test on both sides
// passes whether or not a validate_only intent is dispatched, because neither
// side can see the other: central's handler returning early and the edge
// never being asked look identical from inside either one. If it did
// dispatch, the dry run would be the write — an operator would take the step
// they took specifically to avoid taking.
//
// What it asserts is the absence of an effect, which needs care, because
// absence is also what you get from a test that did nothing. So the dry run
// is followed by a real apply of the same description, and the device's
// command log is asserted to hold exactly one write. One entry means the dry
// run added none and the machinery was working; zero would mean the apply
// never happened either and the whole test proved nothing.
func TestADryRunChecksTheIntentAndTouchesNothing(t *testing.T) {
	d := assemble(t)
	fingerprint := d.waitForFingerprint(t)

	before, err := d.central.devices().GetDeviceAccessStatus(context.Background(),
		connect.NewRequest(devicev1.GetDeviceAccessStatusRequest_builder{Device: deviceRef()}.Build()))
	if err != nil {
		t.Fatalf("GetDeviceAccessStatus before the dry run: %v", err)
	}

	dryResponse := d.validate(t, "0192e6a0-0000-7000-8000-0000000aa001", "uplink to core", fingerprint)

	// The response says nothing was admitted, which is the schema's rule:
	// the mutation is unset exactly when the request set validate_only.
	if dryResponse.GetMutation() != nil {
		t.Errorf("the dry run returned mutation %v, want none — it was not supposed to admit anything", dryResponse.GetMutation())
	}

	after, err := d.central.devices().GetDeviceAccessStatus(context.Background(),
		connect.NewRequest(devicev1.GetDeviceAccessStatusRequest_builder{Device: deviceRef()}.Build()))
	if err != nil {
		t.Fatalf("GetDeviceAccessStatus after the dry run: %v", err)
	}
	if got, want := after.Msg.GetHighWatermark(), before.Msg.GetHighWatermark(); got != want {
		t.Errorf("the dry run moved the high watermark from %d to %d; it consumed a sequence for an intent it did not record", want, got)
	}
	if after.Msg.GetUnresolved() != nil {
		t.Errorf("the dry run left %v holding the lane", after.Msg.GetUnresolved())
	}

	// Then the real one, and the device's whole command log.
	d.apply(t, "0192e6a0-0000-7000-8000-0000000aa002", "uplink to core", fingerprint)
	d.waitUntilResolved(t)

	_, _, _, commands, _ := d.device.snapshot()
	want := []string{fixtureInterface + "=uplink to core"}
	if !slices.Equal(commands, want) {
		t.Errorf("the device was asked to run %q, want %q — a dry run that reached the device is a dry run that was the write",
			commands, want)
	}
}

// accessGoroutines is every goroutine whose stack mentions the access module
// or the agent, which is where a stuck edge is.
//
// Filtered rather than dumped whole: a run of this package has hundreds of
// goroutines and the interesting ones are a handful, and a failure message
// nobody reads to the end is a failure message that did not report anything.
func accessGoroutines() string {
	// Grown until it fits rather than sized by guess. runtime.Stack
	// truncates silently at the buffer's length, and a truncated dump that
	// happens to omit the goroutine you are looking for reads exactly like
	// that goroutine not existing — which is the wrong answer to the only
	// question this is asked.
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}

	var kept []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "localnet/access") || strings.Contains(g, "edge/agent") {
			kept = append(kept, g)
		}
	}
	return strings.Join(kept, "\n\n")
}
