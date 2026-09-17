package drift_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/policy/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/drift"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
	"go.aledante.io/FlowSeer/src/services/device/internal/telemetry"
)

var errAudit = errors.New("stream refused the publish")

const (
	deviceID    = "0192e6a0-0000-7000-8000-0000000000d1"
	edgeID      = "0192e6a0-0000-7000-8000-0000000000e1"
	iface       = "ethernet 1/1/1"
	fingerprint = "fastiron-08.0.95"
	expected    = "uplink to core"
)

type resolver struct {
	entry *storev1.RegistryDevice
}

func (r *resolver) Devices(context.Context, string) ([]string, error) { return []string{deviceID}, nil }

func (r *resolver) Device(id string) (*storev1.RegistryDevice, bool) {
	if id != deviceID {
		return nil, false
	}
	return r.entry, true
}

func (r *resolver) EdgeID() string { return edgeID }

type detection struct {
	iface    string
	expected string
	observed string
}

type recorder struct {
	found []detection
	err   error
}

// signals is the telemetry seam: it records what the poll reported and, like
// the audit recorder, in the order it was called.
type signals struct {
	seen     []detection
	mode     inventoryv1.DeviceManagementMode
	outcomes []telemetry.DriftOutcome
}

func (s *signals) DriftDetected(_ context.Context, _, iface, expected, observed string,
	mode inventoryv1.DeviceManagementMode, outcome telemetry.DriftOutcome,
) {
	s.seen = append(s.seen, detection{iface: iface, expected: expected, observed: observed})
	s.mode = mode
	s.outcomes = append(s.outcomes, outcome)
}

func (r *recorder) DriftDetected(_ context.Context, _ *inventoryv1.DeviceGlobalRef, iface, expected, observed string) error {
	if r.err != nil {
		return r.err
	}
	r.found = append(r.found, detection{iface: iface, expected: expected, observed: observed})
	return nil
}

func registryEntry(mode inventoryv1.DeviceManagementMode) *storev1.RegistryDevice {
	handle := &policyv1.AccessPolicyHandle{}
	handle.SetKey("icx7150-lab")
	handle.SetVersion(3)
	config := &inventoryv1.DeviceConfig{}
	config.SetAccessPolicy(handle)
	config.SetManagementMode(mode)
	entry := &storev1.RegistryDevice{}
	entry.SetConfig(config)
	entry.SetManagedInterfaces([]string{iface})
	return entry
}

type harness struct {
	poller   *drift.Poller
	journal  *journal.Journal
	recorder *recorder
	signals  *signals
}

// newBucket starts a hub and returns the lane bucket its journal writes to.
func newBucket(t *testing.T) jetstream.KeyValue {
	t.Helper()
	hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
		ListenPort:  0,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)
	kv, err := hub.JetStream().KeyValue(context.Background(), edgebus.LaneBucket)
	if err != nil {
		t.Fatalf("bucket: %v", err)
	}
	return kv
}

func newHarness(t *testing.T, mode inventoryv1.DeviceManagementMode) *harness {
	t.Helper()
	kv := newBucket(t)
	j := journal.New(kv, nil)
	rec := &recorder{}
	sig := &signals{}
	poller, err := drift.New(drift.Config{
		Journal:   j,
		Resolver:  &resolver{entry: registryEntry(mode)},
		Audit:     rec,
		Telemetry: sig,
		Interval:  time.Minute,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &harness{poller: poller, journal: j, recorder: rec, signals: sig}
}

func deviceRef() *inventoryv1.DeviceGlobalRef {
	local := &inventoryv1.DeviceLocalRef{}
	local.SetId(deviceID)
	ref := &inventoryv1.DeviceGlobalRef{}
	ref.SetDevice(local)
	return ref
}

func edgeRef() *edgev1.EdgeGlobalRef {
	local := &edgev1.EdgeLocalRef{}
	local.SetId(edgeID)
	ref := &edgev1.EdgeGlobalRef{}
	ref.SetEdge(local)
	return ref
}

func observation(description string) *accessv1.InterfaceObservation {
	binding := &inventoryv1.BindingLocalRef{}
	binding.SetId("0192e6a0-0000-7000-8000-0000000000b1")
	bindingRef := &inventoryv1.BindingGlobalRef{}
	bindingRef.SetBinding(binding)

	provenance := &inventoryv1.Provenance{}
	provenance.SetBinding(bindingRef)
	provenance.SetObservedAt(timestamppb.New(time.Now()))
	provenance.SetProtocol(inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SSH)
	provenance.SetEdge(edgeRef())
	provenance.SetFirmwareFingerprint(fingerprint)

	obs := &accessv1.InterfaceObservation{}
	obs.SetInterfaceName(iface)
	obs.SetDescription(description)
	obs.SetAdminStatus(interfacev1.AdminStatus_ADMIN_STATUS_UP)
	obs.SetOperStatus(interfacev1.OperStatus_OPER_STATUS_UP)
	obs.SetProvenance(provenance)
	obs.SetCompleteness(accessv1.Completeness_COMPLETENESS_COMPLETE)
	return obs
}

// seeObserved leaves the record in the state a poll pass finds after its read
// answered: an expectation central holds, and an observation of what the
// device actually carries.
func seeObserved(t *testing.T, h *harness, observed string) {
	t.Helper()
	ctx := context.Background()
	if err := h.journal.SetExpected(ctx, deviceID, deviceRef(), iface, expected); err != nil {
		t.Fatalf("set expected: %v", err)
	}
	if err := h.journal.SetFingerprint(ctx, deviceID, deviceRef(), fingerprint); err != nil {
		t.Fatalf("set fingerprint: %v", err)
	}
	read := &accessv1.TypedRead{}
	handle := &policyv1.AccessPolicyHandle{}
	handle.SetKey("icx7150-lab")
	handle.SetVersion(3)
	read.SetAccessPolicy(handle)
	intent := &accessv1.InterfaceReadIntent{}
	intent.SetInterfaceName(iface)
	read.SetInterface(intent)

	seq, err := h.journal.OpenRead(ctx, deviceID, deviceRef(), iface, read, uuid.NewString(), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("open read: %v", err)
	}
	if err := h.journal.CloseRead(ctx, deviceID, iface, seq, observation(observed), nil); err != nil {
		t.Fatalf("close read: %v", err)
	}
}

// A person owns this device's configuration, so central records the
// difference and holds the lane for them rather than putting the value back.
func TestDriftUnderOperatorManagedHoldsAnIntentForTheOperator(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED)
	seeObserved(t, h, "someone else's description")

	h.poller.Pass(ctx)

	if len(h.recorder.found) != 1 {
		t.Fatalf("recorded %d detections, want 1", len(h.recorder.found))
	}
	got := h.recorder.found[0]
	if got.iface != iface || got.expected != expected || got.observed != "someone else's description" {
		t.Errorf("recorded %+v, want the interface and both values", got)
	}

	record, _ := h.journal.Record(ctx, deviceID)
	m := record.GetMutation()
	if m == nil {
		t.Fatal("no intent was recorded for the difference")
	}
	if got := m.GetBlockReason(); got != accessv1.BlockReason_BLOCK_REASON_DESYNCHRONIZED {
		t.Errorf("block reason = %v, want desynchronized", got)
	}
	if got := m.GetIntent().GetActor().GetSystem().GetReason(); got != accessv1.SystemReason_SYSTEM_REASON_RECONCILIATION {
		t.Errorf("actor = %v, want the reconciliation system actor", got)
	}
	// A held intent is not dispatched: the operator decides, not the edge.
	for _, owed := range journal.OwedRows(record, time.Now()) {
		if owed.Kind == journal.OwedExecute {
			t.Error("a held desynchronization intent was owed to the edge")
		}
	}
}

// FlowSeer owns this device's configuration, so the difference becomes an
// ordinary sequenced intent that puts the expected description back.
func TestDriftUnderAuthoritativeAdmitsADispatchableIntent(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_AUTHORITATIVE)
	seeObserved(t, h, "someone else's description")

	h.poller.Pass(ctx)

	record, _ := h.journal.Record(ctx, deviceID)
	m := record.GetMutation()
	if m == nil {
		t.Fatal("no reconciliation intent was admitted")
	}
	if m.HasBlockReason() {
		t.Errorf("the intent is blocked by %v; an authoritative device restores on its own", m.GetBlockReason())
	}
	if got := m.GetIntent().GetInterfaceDescription().GetDescription(); got != expected {
		t.Errorf("intent restores %q, want the expected description", got)
	}
	owed := false
	for _, row := range journal.OwedRows(record, time.Now()) {
		if row.Kind == journal.OwedExecute && row.Sequence == m.GetSequence() {
			owed = true
		}
	}
	if !owed {
		t.Error("the reconciliation intent is not owed to the edge")
	}
}

func TestAnInterfaceMatchingItsExpectationIsNotDrift(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED)
	seeObserved(t, h, expected)

	h.poller.Pass(ctx)

	if len(h.recorder.found) != 0 {
		t.Errorf("recorded %v for an interface that matches", h.recorder.found)
	}
	record, _ := h.journal.Record(ctx, deviceID)
	if record.HasMutation() {
		t.Error("an interface that matches admitted an intent")
	}
}

// Central's own change is the obvious explanation for an interface not
// matching what central expected before it. Reporting that as drift would be
// central detecting itself.
func TestDriftDoesNotEvaluateWhileAMutationHoldsTheLane(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED)
	seeObserved(t, h, "someone else's description")
	if _, err := h.journal.Admit(ctx, deviceID, operatorIntent(), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}

	h.poller.Pass(ctx)

	if len(h.recorder.found) != 0 {
		t.Errorf("recorded %v while a mutation held the lane", h.recorder.found)
	}
}

// An abandoned mutation means nobody knows what the device carries, so a
// difference is not news — and the lane is held for the operator either way.
func TestDriftDoesNotEvaluateWhileAnAbandonmentIsUnresolved(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED)
	seeObserved(t, h, "someone else's description")
	state, err := h.journal.Admit(ctx, deviceID, operatorIntent(), edgeRef())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := h.journal.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: state.GetSequence()}); err != nil {
		t.Fatalf("admitted: %v", err)
	}
	if _, err := h.journal.Dispose(ctx, deviceID, state.GetSequence()); err != nil {
		t.Fatalf("dispose: %v", err)
	}

	h.poller.Pass(ctx)

	if len(h.recorder.found) != 0 {
		t.Errorf("recorded %v while an abandonment was unresolved", h.recorder.found)
	}
}

// Central has applied nothing to this interface, so it has nothing to differ
// from. Adopting whatever the first read found would be central deciding an
// expectation nobody asked for.
func TestAnInterfaceWithNoExpectationIsNotJudged(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED)

	h.poller.Pass(ctx)

	if len(h.recorder.found) != 0 {
		t.Errorf("recorded %v for an interface central expects nothing of", h.recorder.found)
	}
	record, _ := h.journal.Record(ctx, deviceID)
	if _, ok := record.GetOpenReads()[iface]; !ok {
		t.Error("the pass asked for no read; an unjudged interface is still polled")
	}
}

// The poll's read is an ordinary row on the lane, and one already in flight is
// not asked for twice.
func TestAPassOpensOneReadAndLeavesAnUnansweredOneAlone(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED)

	h.poller.Pass(ctx)
	first, _ := h.journal.Record(ctx, deviceID)
	opened := first.GetOpenReads()[iface].GetSequence()

	h.poller.Pass(ctx)
	second, _ := h.journal.Record(ctx, deviceID)

	if got := second.GetOpenReads()[iface].GetSequence(); got != opened {
		t.Errorf("the second pass opened read %d over the unanswered %d", got, opened)
	}
	if second.GetHighWatermark() != first.GetHighWatermark() {
		t.Errorf("the second pass assigned a sequence: watermark %d, was %d",
			second.GetHighWatermark(), first.GetHighWatermark())
	}
}

// A detection that cannot be recorded must not become an intent nobody can
// explain: the operator would find a held lane with nothing saying why.
func TestADetectionThatCannotBeRecordedAdmitsNothing(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED)
	seeObserved(t, h, "someone else's description")
	h.recorder.err = errAudit

	h.poller.Pass(ctx)

	record, _ := h.journal.Record(ctx, deviceID)
	if record.HasMutation() {
		t.Error("an unrecorded detection still held the lane")
	}
}

// Central admits a reconciliation intent on its own behalf, so nobody else can
// supply the firmware epoch it is decided against — and an intent must name
// one. Without the guard the poll admits an intent that fails MutationIntent's
// own rules and, under AUTHORITATIVE, dispatches it.
func TestDriftWithNoLearnedFirmwareEpochRecordsTheDetectionAndAdmitsNothing(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_AUTHORITATIVE)
	if err := h.journal.SetExpected(ctx, deviceID, deviceRef(), iface, expected); err != nil {
		t.Fatalf("set expected: %v", err)
	}
	read := &accessv1.TypedRead{}
	handle := &policyv1.AccessPolicyHandle{}
	handle.SetKey("icx7150-lab")
	handle.SetVersion(3)
	read.SetAccessPolicy(handle)
	intent := &accessv1.InterfaceReadIntent{}
	intent.SetInterfaceName(iface)
	read.SetInterface(intent)
	seq, err := h.journal.OpenRead(ctx, deviceID, deviceRef(), iface, read, uuid.NewString(), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("open read: %v", err)
	}
	if err := h.journal.CloseRead(ctx, deviceID, iface, seq, observation("someone else's description"), nil); err != nil {
		t.Fatalf("close read: %v", err)
	}

	h.poller.Pass(ctx)

	if len(h.recorder.found) != 1 {
		t.Fatalf("detections = %d, want the difference recorded", len(h.recorder.found))
	}
	record, err := h.journal.Record(ctx, deviceID)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if record.HasMutation() {
		t.Fatalf("the poll admitted %v with no epoch to decide it against", record.GetMutation())
	}
	// This arm admits nothing, so the lane stays free and the same condition
	// is detected again on every pass. The label is what keeps the counter
	// from reading that repetition as a drift rate.
	if got := h.signals.outcomes; len(got) != 1 || got[0] != telemetry.DriftOutcomeNoEpoch {
		t.Fatalf("outcomes = %v, want one no_epoch", got)
	}
}

// The first pass over a device central has never written to creates its lane
// record, and that record must name its device: the field is required, and a
// stored record that fails its own rules is found by whatever reads it next,
// not here.
func TestTheFirstPassOverAnUnwrittenDeviceStoresARecordNamingIt(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_AUTHORITATIVE)

	h.poller.Pass(ctx)

	record, err := h.journal.Record(ctx, deviceID)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if got := record.GetDevice().GetDevice().GetId(); got != deviceID {
		t.Fatalf("record device = %q, want %q", got, deviceID)
	}
	if err := protovalidate.Validate(record); err != nil {
		t.Fatalf("the stored record fails its schema rules: %v", err)
	}
}

func operatorIntent() *accessv1.MutationIntent {
	operator := &accessv1.OperatorRef{}
	operator.SetSubject("zitadel|1")
	actor := &accessv1.Actor{}
	actor.SetOperator(operator)

	handle := &policyv1.AccessPolicyHandle{}
	handle.SetKey("icx7150-lab")
	handle.SetVersion(3)

	change := &accessv1.InterfaceDescriptionChange{}
	change.SetInterfaceName(iface)
	change.SetDescription("something an operator asked for")

	intent := &accessv1.MutationIntent{}
	intent.SetDevice(deviceRef())
	intent.SetIdempotencyKey(uuid.NewString())
	intent.SetActor(actor)
	intent.SetAccessPolicy(handle)
	intent.SetExpectedFirmwareFingerprint(fingerprint)
	intent.SetInterfaceDescription(change)
	return intent
}

// A detection produces exactly one event, carrying what the record carries.
func TestADetectionReportsOneSignalCarryingWhatItFound(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED)
	seeObserved(t, h, "someone else's description")

	h.poller.Pass(ctx)

	if len(h.signals.seen) != 1 {
		t.Fatalf("signals = %+v, want exactly one", h.signals.seen)
	}
	got := h.signals.seen[0]
	if got.iface != iface || got.expected != expected || got.observed != "someone else's description" {
		t.Errorf("signal = %+v, want the difference the record holds", got)
	}
	if h.signals.mode != inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED {
		t.Errorf("mode = %v, want the device's own", h.signals.mode)
	}
	if got := h.signals.outcomes; len(got) != 1 || got[0] != telemetry.DriftOutcomeHeld {
		t.Fatalf("outcomes = %v, want one held", got)
	}
}

// An AUTHORITATIVE detection says central is putting the description back,
// which is a different thing from a detection nobody can act on. Both are
// drift; only one of them stops repeating.
func TestAnAuthoritativeDetectionReportsThatItWasDispatched(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_AUTHORITATIVE)
	seeObserved(t, h, "someone else's description")

	h.poller.Pass(ctx)

	if got := h.signals.outcomes; len(got) != 1 || got[0] != telemetry.DriftOutcomeDispatched {
		t.Fatalf("outcomes = %v, want one dispatched", got)
	}
}

// The audit record is written before the signal is reported, and this is the
// test that holds the order.
//
// Both writes can fail and the two losses are not equal. A record with no
// signal is a detection an operator finds in the stream, which is where they
// look for what happened to a device. A signal with no record is an alarm
// about a detection that left no durable trace — someone is woken to
// investigate something that cannot be reconstructed. Reverse the two lines in
// judge and this test fails, which is the only thing holding them in that
// order: on the happy path the ordering is invisible.
func TestASignalIsNotReportedForADetectionTheStreamRefused(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_AUTHORITATIVE)
	seeObserved(t, h, "someone else's description")
	h.recorder.err = errAudit

	h.poller.Pass(ctx)

	if len(h.signals.seen) != 0 {
		t.Fatalf("a signal was reported for a detection nothing recorded: %+v", h.signals.seen)
	}
	record, err := h.journal.Record(ctx, deviceID)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if record.HasMutation() {
		t.Fatal("the poll admitted an intent behind a detection it could not record")
	}
}

// A poll with no telemetry wired still detects, records and acts. The signal
// is what a dashboard counts; losing it must not stop the work.
func TestAPollWithNoTelemetryStillRecordsAndActs(t *testing.T) {
	ctx := context.Background()
	kv := newBucket(t)
	j := journal.New(kv, nil)
	rec := &recorder{}
	poller, err := drift.New(drift.Config{
		Journal:  j,
		Resolver: &resolver{entry: registryEntry(inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_AUTHORITATIVE)},
		Audit:    rec,
		Interval: time.Minute,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := &harness{poller: poller, journal: j, recorder: rec}
	seeObserved(t, h, "someone else's description")

	poller.Pass(ctx)

	if len(rec.found) != 1 {
		t.Fatalf("detections = %d, want the difference recorded", len(rec.found))
	}
	record, err := j.Record(ctx, deviceID)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if !record.HasMutation() {
		t.Fatal("the poll recorded a detection and admitted nothing")
	}
}
