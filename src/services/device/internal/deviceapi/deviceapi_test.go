package deviceapi_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	devicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/deviceapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
	"go.aledante.io/FlowSeer/src/services/device/internal/registry"
)

const (
	deviceID   = "0192e6a0-0000-7000-8000-0000000000d1"
	edgeID     = "0192e6a0-0000-7000-8000-0000000000e1"
	iface      = "ethernet 1/1/1"
	policyKey  = "icx7150-lab"
	policyVer  = 3
	fingerling = "fastiron-08.0.95"
)

// resolver stands in for the registry: one device, whose horizon and pinned
// policy the tests move to reach each refusal.
type resolver struct {
	entry   *storev1.RegistryDevice
	missing bool
	// extra names devices the edge also reaches that hold no lane record,
	// for the paging tests.
	extra []string
}

func (r *resolver) Device(id string) (*storev1.RegistryDevice, bool) {
	if r.missing || id != deviceID {
		return nil, false
	}
	return r.entry, true
}

func (r *resolver) Horizon(_ context.Context, id string) (time.Duration, error) {
	entry, ok := r.Device(id)
	if !ok || entry.GetDelayedApplyHorizon() == nil {
		return 0, errs.New().Code(registry.ErrCodeHorizonUnset).Attr("device", id).
			Msg("device has no delayed-apply horizon")
	}
	return entry.GetDelayedApplyHorizon().AsDuration(), nil
}

func (r *resolver) Devices(_ context.Context, id string) ([]string, error) {
	if r.missing || id != edgeID {
		return nil, nil
	}
	return append([]string{deviceID}, r.extra...), nil
}

func (r *resolver) EdgeID() string { return edgeID }

func registryEntry(horizon time.Duration) *storev1.RegistryDevice {
	handle := &policyv1.AccessPolicyHandle{}
	handle.SetKey(policyKey)
	handle.SetVersion(policyVer)
	config := &inventoryv1.DeviceConfig{}
	config.SetAccessPolicy(handle)
	entry := &storev1.RegistryDevice{}
	entry.SetConfig(config)
	if horizon > 0 {
		entry.SetDelayedApplyHorizon(durationpb.New(horizon))
	}
	return entry
}

type harness struct {
	svc      *deviceapi.Service
	journal  *journal.Journal
	resolver *resolver
}

func newHarness(t *testing.T) *harness {
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
	j := journal.New(kv, nil)
	res := &resolver{entry: registryEntry(2 * time.Minute)}
	svc, err := deviceapi.New(deviceapi.Config{
		Journal:  j,
		Resolver: res,
		Watcher:  deviceapi.NewKVWatcher(kv),
		ReadPoll: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &harness{svc: svc, journal: j, resolver: res}
}

func deviceRef() *inventoryv1.DeviceGlobalRef {
	local := &inventoryv1.DeviceLocalRef{}
	local.SetId(deviceID)
	ref := &inventoryv1.DeviceGlobalRef{}
	ref.SetDevice(local)
	return ref
}

func intentFor(description string) *accessv1.MutationIntent {
	operator := &accessv1.OperatorRef{}
	operator.SetSubject("zitadel|1")
	actor := &accessv1.Actor{}
	actor.SetOperator(operator)

	handle := &policyv1.AccessPolicyHandle{}
	handle.SetKey(policyKey)
	handle.SetVersion(policyVer)

	change := &accessv1.InterfaceDescriptionChange{}
	change.SetInterfaceName(iface)
	change.SetDescription(description)

	intent := &accessv1.MutationIntent{}
	intent.SetDevice(deviceRef())
	intent.SetIdempotencyKey(uuid.NewString())
	intent.SetActor(actor)
	intent.SetAccessPolicy(handle)
	intent.SetExpectedFirmwareFingerprint(fingerling)
	intent.SetInterfaceDescription(change)
	return intent
}

func applyRequest(intent *accessv1.MutationIntent, validateOnly bool) *connect.Request[devicev1.ApplyInterfaceDescriptionRequest] {
	msg := &devicev1.ApplyInterfaceDescriptionRequest{}
	msg.SetIntent(intent)
	msg.SetValidateOnly(validateOnly)
	return connect.NewRequest(msg)
}

// observationAt is observation with the reading time named, for the cases that
// turn on whether central saw the interface before or after a mutation.
func observationAt(description string, observedAt time.Time) *accessv1.InterfaceObservation {
	obs := observation(description)
	obs.GetProvenance().SetObservedAt(timestamppb.New(observedAt))
	return obs
}

func observation(description string) *accessv1.InterfaceObservation {
	binding := &inventoryv1.BindingLocalRef{}
	binding.SetId("0192e6a0-0000-7000-8000-0000000000b1")
	bindingRef := &inventoryv1.BindingGlobalRef{}
	bindingRef.SetBinding(binding)
	edgeLocal := &edgev1.EdgeLocalRef{}
	edgeLocal.SetId(edgeID)
	edge := &edgev1.EdgeGlobalRef{}
	edge.SetEdge(edgeLocal)

	provenance := &inventoryv1.Provenance{}
	provenance.SetBinding(bindingRef)
	provenance.SetObservedAt(timestamppb.New(time.Now()))
	provenance.SetProtocol(inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SSH)
	provenance.SetEdge(edge)
	provenance.SetFirmwareFingerprint(fingerling)

	obs := &accessv1.InterfaceObservation{}
	obs.SetInterfaceName(iface)
	obs.SetDescription(description)
	obs.SetAdminStatus(interfacev1.AdminStatus_ADMIN_STATUS_UP)
	obs.SetOperStatus(interfacev1.OperStatus_OPER_STATUS_UP)
	obs.SetProvenance(provenance)
	obs.SetCompleteness(accessv1.Completeness_COMPLETENESS_COMPLETE)
	return obs
}

func wantCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("the call succeeded; want %v", want)
	}
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("code = %v, want %v (%v)", got, want, err)
	}
}

func validate(t *testing.T, msg proto.Message) {
	t.Helper()
	if err := protovalidate.Validate(msg); err != nil {
		t.Errorf("message fails its schema rules: %v", err)
	}
}

func TestApplyAdmitsTheIntentAndReturnsItAtAdmission(t *testing.T) {
	h := newHarness(t)

	resp, err := h.svc.ApplyInterfaceDescription(context.Background(), applyRequest(intentFor("uplink to core"), false))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	validate(t, resp.Msg)
	state := resp.Msg.GetMutation()
	if state.GetSequence() != 1 {
		t.Errorf("sequence = %d, want 1", state.GetSequence())
	}
	if got := state.GetPhase(); got != accessv1.OperationPhase_OPERATION_PHASE_ADMITTED {
		t.Errorf("phase = %v, want admitted", got)
	}
	if got := state.GetResponsibleEdge().GetEdge().GetId(); got != edgeID {
		t.Errorf("responsible edge = %q, want the registry's edge", got)
	}
}

func TestApplyRefusesADeviceTheRegistryDoesNotList(t *testing.T) {
	h := newHarness(t)
	h.resolver.missing = true

	_, err := h.svc.ApplyInterfaceDescription(context.Background(), applyRequest(intentFor("x"), false))
	wantCode(t, err, connect.CodeNotFound)
}

// An unmeasured horizon means nothing can bound how long the change takes to
// become visible, so the mutation could never be dispatched. Refusing at the
// door beats admitting one that would sit owed forever.
func TestApplyRefusesADeviceWithNoMeasuredHorizon(t *testing.T) {
	h := newHarness(t)
	h.resolver.entry = registryEntry(0)

	_, err := h.svc.ApplyInterfaceDescription(context.Background(), applyRequest(intentFor("x"), false))
	wantCode(t, err, connect.CodeFailedPrecondition)

	record, _ := h.journal.Record(context.Background(), deviceID)
	if record.GetHighWatermark() != 0 {
		t.Error("a refused apply assigned a sequence")
	}
}

func TestApplyRefusesAnIntentUnderAPolicyVersionTheDeviceDoesNotPin(t *testing.T) {
	h := newHarness(t)
	intent := intentFor("x")
	stale := &policyv1.AccessPolicyHandle{}
	stale.SetKey(policyKey)
	stale.SetVersion(policyVer - 1)
	intent.SetAccessPolicy(stale)

	_, err := h.svc.ApplyInterfaceDescription(context.Background(), applyRequest(intent, false))
	wantCode(t, err, connect.CodeFailedPrecondition)
}

// The caller decided against a firmware epoch. If the device has since
// reported another, the decision was made about a device that no longer
// exists in that form, and nothing is written.
func TestApplyRefusesAStaleFirmwareFingerprint(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	if err := h.journal.SetFingerprint(ctx, deviceID, deviceRef(), "fastiron-09.0.10"); err != nil {
		t.Fatalf("set fingerprint: %v", err)
	}

	_, err := h.svc.ApplyInterfaceDescription(ctx, applyRequest(intentFor("x"), false))
	wantCode(t, err, connect.CodeFailedPrecondition)

	record, _ := h.journal.Record(ctx, deviceID)
	if record.HasMutation() {
		t.Error("a refused apply admitted the intent anyway")
	}
}

// A device nobody has probed yet has no fingerprint to compare against, and an
// unknown epoch is not a stale one: refusing there would make the first write
// on a fresh device impossible.
func TestApplyAcceptsWhenNoFingerprintHasBeenReported(t *testing.T) {
	h := newHarness(t)

	if _, err := h.svc.ApplyInterfaceDescription(context.Background(), applyRequest(intentFor("x"), false)); err != nil {
		t.Fatalf("apply against an unprobed device: %v", err)
	}
}

func TestApplyRefusesASecondIntentWhileTheLaneIsHeld(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	if _, err := h.svc.ApplyInterfaceDescription(ctx, applyRequest(intentFor("first"), false)); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	_, err := h.svc.ApplyInterfaceDescription(ctx, applyRequest(intentFor("second"), false))
	wantCode(t, err, connect.CodeFailedPrecondition)
}

func TestApplyValidateOnlyRecordsNothing(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	resp, err := h.svc.ApplyInterfaceDescription(ctx, applyRequest(intentFor("x"), true))
	if err != nil {
		t.Fatalf("validate_only apply: %v", err)
	}
	if resp.Msg.HasMutation() {
		t.Error("validate_only returned a mutation")
	}
	record, _ := h.journal.Record(ctx, deviceID)
	if record.GetHighWatermark() != 0 || record.HasMutation() {
		t.Error("validate_only wrote to the record")
	}
}

func TestApplyValidateOnlyRefusesWhatAnApplyWouldRefuse(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	if _, err := h.svc.ApplyInterfaceDescription(ctx, applyRequest(intentFor("first"), false)); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	_, err := h.svc.ApplyInterfaceDescription(ctx, applyRequest(intentFor("second"), true))
	wantCode(t, err, connect.CodeFailedPrecondition)
}

// A retried submission is the same intent, so it reads back the mutation it
// already has rather than being refused by the lane its own first call holds.
func TestApplyResubmissionReturnsTheRecordedMutation(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	intent := intentFor("uplink to core")

	first, err := h.svc.ApplyInterfaceDescription(ctx, applyRequest(intent, false))
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	again, err := h.svc.ApplyInterfaceDescription(ctx, applyRequest(intent, false))
	if err != nil {
		t.Fatalf("resubmission: %v", err)
	}

	if again.Msg.GetMutation().GetSequence() != first.Msg.GetMutation().GetSequence() {
		t.Errorf("the resubmission was admitted again, at %d", again.Msg.GetMutation().GetSequence())
	}
	record, _ := h.journal.Record(ctx, deviceID)
	if record.GetHighWatermark() != 1 {
		t.Errorf("high watermark = %d, want 1", record.GetHighWatermark())
	}
}

func TestStatusReflectsTheRecord(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	if _, err := h.svc.ApplyInterfaceDescription(ctx, applyRequest(intentFor("uplink"), false)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := h.journal.SetFingerprint(ctx, deviceID, deviceRef(), fingerling); err != nil {
		t.Fatalf("set fingerprint: %v", err)
	}

	msg := &devicev1.GetDeviceAccessStatusRequest{}
	msg.SetDevice(deviceRef())
	resp, err := h.svc.GetDeviceAccessStatus(ctx, connect.NewRequest(msg))
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	validate(t, resp.Msg)
	if resp.Msg.GetHighWatermark() != 1 {
		t.Errorf("high watermark = %d, want 1", resp.Msg.GetHighWatermark())
	}
	if resp.Msg.GetUnresolved().GetSequence() != 1 {
		t.Error("the admitted mutation is not reported unresolved")
	}
	if resp.Msg.GetFirmwareFingerprint() != fingerling {
		t.Errorf("fingerprint = %q, want the reported one", resp.Msg.GetFirmwareFingerprint())
	}
}

func TestAbandonEndsAnOpenMutationAndRefusesATerminalOne(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	applied, err := h.svc.ApplyInterfaceDescription(ctx, applyRequest(intentFor("uplink"), false))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	seq := applied.Msg.GetMutation().GetSequence()

	msg := &devicev1.AbandonMutationRequest{}
	msg.SetDevice(deviceRef())
	msg.SetSequence(seq)
	msg.SetActor(intentFor("x").GetActor())

	resp, err := h.svc.AbandonMutation(ctx, connect.NewRequest(msg))
	if err != nil {
		t.Fatalf("abandon: %v", err)
	}
	validate(t, resp.Msg)
	if got := resp.Msg.GetMutation().GetDisposition(); got != accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED {
		t.Errorf("disposition = %v, want indeterminate-abandoned", got)
	}

	// The same call again finds nothing open at that sequence.
	_, err = h.svc.AbandonMutation(ctx, connect.NewRequest(msg))
	wantCode(t, err, connect.CodeFailedPrecondition)
}

// heldDescription is what the held mutation was trying to apply.
const heldDescription = "uplink to core"

// held leaves a mutation abandoned into a recovery hold: admitted, reported
// admitted by the edge, then abandoned — the state ResolveDesynchronization
// exists for.
func held(t *testing.T, h *harness) uint64 {
	t.Helper()
	ctx := context.Background()
	applied, err := h.svc.ApplyInterfaceDescription(ctx, applyRequest(intentFor(heldDescription), false))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	seq := applied.Msg.GetMutation().GetSequence()
	if err := h.journal.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: seq}); err != nil {
		t.Fatalf("admitted report: %v", err)
	}
	if _, err := h.journal.Dispose(ctx, deviceID, seq); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	// The edge confirms the abandonment. A hold is resolvable only once it
	// has: without this report the mutation still owes its terminal
	// acknowledgement and every resolution below would be refused, which is
	// what TestResolveRefusesWhileTheEdgeStillOwesItsAcknowledgement covers.
	if err := h.journal.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAbandoned, Sequence: seq}); err != nil {
		t.Fatalf("abandoned report: %v", err)
	}
	return seq
}

// An operator who abandons a mutation and immediately resolves it is the
// natural sequence of actions and the one that used to break the edge: the
// resolution closed the record, the terminal acknowledgement stopped being
// owed, and the edge sat waiting for it while central dispatched the
// replacement into a lane the old sequence still held.
func TestResolveRefusesWhileTheEdgeStillOwesItsAcknowledgement(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	applied, err := h.svc.ApplyInterfaceDescription(ctx, applyRequest(intentFor(heldDescription), false))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	seq := applied.Msg.GetMutation().GetSequence()
	if err := h.journal.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: seq}); err != nil {
		t.Fatalf("admitted report: %v", err)
	}
	if _, err := h.journal.Dispose(ctx, deviceID, seq); err != nil {
		t.Fatalf("dispose: %v", err)
	}

	msg := resolveRequest(seq)
	msg.SetAccept(&devicev1.AcceptObservedDecision{})
	_, err = h.svc.ResolveDesynchronization(ctx, connect.NewRequest(msg))
	wantCode(t, err, connect.CodeFailedPrecondition)

	record, err := h.journal.Record(ctx, deviceID)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if !record.HasMutation() {
		t.Fatal("the refused resolution closed the mutation anyway")
	}
}

func resolveRequest(sequence uint64) *devicev1.ResolveDesynchronizationRequest {
	msg := &devicev1.ResolveDesynchronizationRequest{}
	msg.SetDevice(deviceRef())
	msg.SetSequence(sequence)
	msg.SetActor(intentFor("x").GetActor())
	return msg
}

func TestResolveAcceptAdoptsWhatTheDeviceCarriesAndAdmitsNothing(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seq := held(t, h)
	// The interface is managed — central holds an expectation for it — and a
	// read has since seen what the device really carries.
	if err := h.journal.SetExpected(ctx, deviceID, deviceRef(), iface, "uplink to core"); err != nil {
		t.Fatalf("set expected: %v", err)
	}
	closeARead(t, h, observation("whatever the device says"))

	msg := resolveRequest(seq)
	msg.SetAccept(&devicev1.AcceptObservedDecision{})
	resp, err := h.svc.ResolveDesynchronization(ctx, connect.NewRequest(msg))
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	if resp.Msg.HasMutation() {
		t.Error("accept admitted a mutation")
	}
	record, _ := h.journal.Record(ctx, deviceID)
	if got := record.GetExpectedDescriptions()[iface]; got != "whatever the device says" {
		t.Errorf("expected description = %q, want the observed one", got)
	}
	if record.HasMutation() {
		t.Error("accept left the lane held")
	}
}

func TestResolveRestoreAdmitsCentralsOwnIntentToPutTheExpectationBack(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seq := held(t, h)
	if err := h.journal.SetExpected(ctx, deviceID, deviceRef(), iface, "uplink to core"); err != nil {
		t.Fatalf("set expected: %v", err)
	}
	if err := h.journal.SetFingerprint(ctx, deviceID, deviceRef(), fingerling); err != nil {
		t.Fatalf("set fingerprint: %v", err)
	}

	msg := resolveRequest(seq)
	msg.SetRestore(&devicev1.RestoreExpectedDecision{})
	resp, err := h.svc.ResolveDesynchronization(ctx, connect.NewRequest(msg))
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	validate(t, resp.Msg)
	admitted := resp.Msg.GetMutation()
	if admitted.GetSequence() != seq+1 {
		t.Errorf("restored at sequence %d, want %d", admitted.GetSequence(), seq+1)
	}
	intent := admitted.GetIntent()
	if got := intent.GetActor().GetSystem().GetReason(); got != accessv1.SystemReason_SYSTEM_REASON_RECONCILIATION {
		t.Errorf("actor = %v, want the reconciliation system actor", got)
	}
	if got := intent.GetInterfaceDescription().GetDescription(); got != "uplink to core" {
		t.Errorf("restored description = %q, want the expectation", got)
	}
}

func TestResolveReplaceAdmitsTheCarriedIntent(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seq := held(t, h)

	msg := resolveRequest(seq)
	msg.SetReplace(intentFor("uplink to core b"))
	resp, err := h.svc.ResolveDesynchronization(ctx, connect.NewRequest(msg))
	if err != nil {
		t.Fatalf("replace: %v", err)
	}

	validate(t, resp.Msg)
	if got := resp.Msg.GetMutation().GetIntent().GetInterfaceDescription().GetDescription(); got != "uplink to core b" {
		t.Errorf("admitted description = %q, want the carried one", got)
	}
}

// A replacement is a fresh decision about the device and is checked like one.
func TestResolveReplaceRefusesAnIntentUnderTheWrongPolicyVersion(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seq := held(t, h)

	replacement := intentFor("uplink to core b")
	stale := &policyv1.AccessPolicyHandle{}
	stale.SetKey(policyKey)
	stale.SetVersion(policyVer - 1)
	replacement.SetAccessPolicy(stale)

	msg := resolveRequest(seq)
	msg.SetReplace(replacement)
	_, err := h.svc.ResolveDesynchronization(ctx, connect.NewRequest(msg))
	wantCode(t, err, connect.CodeFailedPrecondition)

	record, _ := h.journal.Record(ctx, deviceID)
	if !record.HasMutation() {
		t.Error("a refused replace resolved the hold anyway")
	}
}

// Accepting means adopting what the device carries, so it needs an
// observation. Without one there is nothing to adopt and the expectation would
// stand for nothing.
func TestResolveAcceptRefusesWithNoObservationToAdopt(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seq := held(t, h)

	msg := resolveRequest(seq)
	msg.SetAccept(&devicev1.AcceptObservedDecision{})
	_, err := h.svc.ResolveDesynchronization(ctx, connect.NewRequest(msg))
	wantCode(t, err, connect.CodeFailedPrecondition)
}

func TestReadReturnsTheObservationTheEdgeReports(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	go func() {
		seq := awaitOpenRead(t, h)
		if err := h.journal.CloseRead(context.Background(), deviceID, iface, seq, observation("uplink to core"), nil); err != nil {
			t.Errorf("close read: %v", err)
		}
	}()

	msg := &devicev1.ReadInterfaceRequest{}
	msg.SetDevice(deviceRef())
	msg.SetInterfaceName(iface)
	resp, err := h.svc.ReadInterface(ctx, connect.NewRequest(msg))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	validate(t, resp.Msg)
	if got := resp.Msg.GetInterface().GetDescription(); got != "uplink to core" {
		t.Errorf("description = %q, want the observed one", got)
	}
}

// A read the caller gave up on stays open: its dispatch is still owed, and its
// answer still lands in the record for the status call to show.
func TestReadThatOutlastsItsCallerLeavesTheReadOpen(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	msg := &devicev1.ReadInterfaceRequest{}
	msg.SetDevice(deviceRef())
	msg.SetInterfaceName(iface)
	_, err := h.svc.ReadInterface(ctx, connect.NewRequest(msg))
	wantCode(t, err, connect.CodeDeadlineExceeded)

	record, _ := h.journal.Record(context.Background(), deviceID)
	entry, ok := record.GetOpenReads()[iface]
	if !ok || entry.HasOutcome() {
		t.Error("the abandoned call closed the read it opened")
	}
}

// awaitOpenRead waits for the handler to have opened its read, so the test
// closes the read the call is waiting on rather than racing it.
func awaitOpenRead(t *testing.T, h *harness) uint64 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		record, err := h.journal.Record(context.Background(), deviceID)
		if err == nil {
			if entry, ok := record.GetOpenReads()[iface]; ok {
				return entry.GetSequence()
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("no read was opened")
	return 0
}

// closeARead opens and closes one read, which is how an observation reaches
// the record: nothing else writes what central last saw on an interface.
func closeARead(t *testing.T, h *harness, obs *accessv1.InterfaceObservation) {
	t.Helper()
	ctx := context.Background()
	read := &accessv1.TypedRead{}
	handle := &policyv1.AccessPolicyHandle{}
	handle.SetKey(policyKey)
	handle.SetVersion(policyVer)
	read.SetAccessPolicy(handle)
	intent := &accessv1.InterfaceReadIntent{}
	intent.SetInterfaceName(iface)
	read.SetInterface(intent)

	seq, err := h.journal.OpenRead(ctx, deviceID, deviceRef(), iface, read, uuid.NewString(), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("open read: %v", err)
	}
	if err := h.journal.CloseRead(ctx, deviceID, iface, seq, obs, nil); err != nil {
		t.Fatalf("close read: %v", err)
	}
}

// Accepting adopts what the device carries, and an observation from before the
// mutation was admitted describes what it carried before the write nobody
// could establish. Adopting it would record an expectation the device may not
// match, and the drift poll would then dispatch central's own stale value back
// over whatever the operator's abandoned write actually left there.
func TestResolveAcceptRefusesAnObservationOlderThanTheMutation(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seq := held(t, h)
	if err := h.journal.SetExpected(ctx, deviceID, deviceRef(), iface, "uplink to core"); err != nil {
		t.Fatalf("set expected: %v", err)
	}
	record, err := h.journal.Record(ctx, deviceID)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	closeARead(t, h, observationAt("what it carried before", record.GetAdmittedAt().AsTime().Add(-time.Minute)))

	msg := resolveRequest(seq)
	msg.SetAccept(&devicev1.AcceptObservedDecision{})
	_, err = h.svc.ResolveDesynchronization(ctx, connect.NewRequest(msg))
	wantCode(t, err, connect.CodeFailedPrecondition)

	record, err = h.journal.Record(ctx, deviceID)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if got := record.GetExpectedDescriptions()[iface]; got != "uplink to core" {
		t.Fatalf("expected description = %q, want the refusal to have adopted nothing", got)
	}
}

// Restore admits an intent central decides on its own, so nobody else can
// supply the firmware epoch it is decided against — and an intent that names
// none is not a valid intent. Until a read reports one, restore refuses rather
// than writing a record that fails its own schema.
func TestResolveRestoreRefusesBeforeCentralKnowsTheFirmwareEpoch(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seq := held(t, h)
	if err := h.journal.SetExpected(ctx, deviceID, deviceRef(), iface, "uplink to core"); err != nil {
		t.Fatalf("set expected: %v", err)
	}

	msg := resolveRequest(seq)
	msg.SetRestore(&devicev1.RestoreExpectedDecision{})
	_, err := h.svc.ResolveDesynchronization(ctx, connect.NewRequest(msg))
	wantCode(t, err, connect.CodeFailedPrecondition)

	record, _ := h.journal.Record(ctx, deviceID)
	if record.GetHighWatermark() != seq {
		t.Error("a refused restore assigned a sequence")
	}
}

// Retiring an edge orphans its lanes rather than ending them, so the operator
// has to be told which ones — with the sequence they will abandon and the
// phase it stopped at, not just how many.
func TestListEdgeOpenMutationsNamesWhatRetiringAnEdgeWouldOrphan(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	applied, err := h.svc.ApplyInterfaceDescription(ctx, applyRequest(intentFor("uplink"), false))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	msg := &devicev1.ListEdgeOpenMutationsRequest{}
	msg.SetEdgeId(edgeID)
	resp, err := h.svc.ListEdgeOpenMutations(ctx, connect.NewRequest(msg))
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	validate(t, resp.Msg)
	rows := resp.Msg.GetOpen()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if got := rows[0].GetDevice().GetDevice().GetId(); got != deviceID {
		t.Errorf("device = %q, want the held one", got)
	}
	if got := rows[0].GetMutation().GetSequence(); got != applied.Msg.GetMutation().GetSequence() {
		t.Errorf("sequence = %d, want the held mutation's", got)
	}
	if got := rows[0].GetMutation().GetPhase(); got != accessv1.OperationPhase_OPERATION_PHASE_ADMITTED {
		t.Errorf("phase = %v, want the phase it stopped at", got)
	}
}

func TestListEdgeOpenMutationsPagesInDeviceOrder(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	if _, err := h.svc.ApplyInterfaceDescription(ctx, applyRequest(intentFor("uplink"), false)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	msg := &devicev1.ListEdgeOpenMutationsRequest{}
	msg.SetEdgeId(edgeID)
	msg.SetPageSize(1)
	first, err := h.svc.ListEdgeOpenMutations(ctx, connect.NewRequest(msg))
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	validate(t, first.Msg)
	if len(first.Msg.GetOpen()) != 1 {
		t.Fatalf("first page rows = %d, want 1", len(first.Msg.GetOpen()))
	}
	// The one held lane is also the edge's last device, so the page is full
	// and the listing is still known to be complete: no token.
	if first.Msg.HasNextPageToken() {
		t.Errorf("token %q on the last page; want none", first.Msg.GetNextPageToken())
	}

	// A second device sorting after the held one leaves the page full with
	// devices unexamined, so a token is handed out; the page after it holds
	// no lane and ends the listing.
	later := "ffffffff-0000-7000-8000-0000000000d2"
	h.resolver.extra = []string{later}
	first, err = h.svc.ListEdgeOpenMutations(ctx, connect.NewRequest(msg))
	if err != nil {
		t.Fatalf("first page with a later device: %v", err)
	}
	validate(t, first.Msg)
	if got := first.Msg.GetNextPageToken(); got != deviceID {
		t.Fatalf("token = %q, want the last device served %q", got, deviceID)
	}
	msg.SetPageToken(first.Msg.GetNextPageToken())
	second, err := h.svc.ListEdgeOpenMutations(ctx, connect.NewRequest(msg))
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	validate(t, second.Msg)
	if len(second.Msg.GetOpen()) != 0 || second.Msg.HasNextPageToken() {
		t.Errorf("page after %s = %d rows, token %q; want empty and no token", deviceID, len(second.Msg.GetOpen()), second.Msg.GetNextPageToken())
	}

	// A token naming a device that has since left the edge resumes at the
	// next id rather than failing.
	msg.SetPageToken("0192e6a0-0000-7000-8000-0000000000a0")
	resumed, err := h.svc.ListEdgeOpenMutations(ctx, connect.NewRequest(msg))
	if err != nil {
		t.Fatalf("resume after a departed device: %v", err)
	}
	if len(resumed.Msg.GetOpen()) != 1 {
		t.Errorf("rows after a departed device = %d, want the held one", len(resumed.Msg.GetOpen()))
	}
}

func TestListEdgeOpenMutationsReportsNothingWhenNoLaneIsHeld(t *testing.T) {
	h := newHarness(t)

	msg := &devicev1.ListEdgeOpenMutationsRequest{}
	msg.SetEdgeId(edgeID)
	resp, err := h.svc.ListEdgeOpenMutations(context.Background(), connect.NewRequest(msg))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(resp.Msg.GetOpen()) != 0 {
		t.Errorf("rows = %d, want none", len(resp.Msg.GetOpen()))
	}
}

// A mutation the edge was dispatched and has not ended is refused, and the
// code says which call ends it. Every other resolve test above abandons the
// mutation first, so HasDisposition() short-circuits this guard and it would
// pass the suite if it were deleted.
func TestResolveRefusesAMutationTheEdgeStillHolds(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	applied, err := h.svc.ApplyInterfaceDescription(ctx, applyRequest(intentFor(heldDescription), false))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	seq := applied.Msg.GetMutation().GetSequence()
	if err := h.journal.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: seq}); err != nil {
		t.Fatalf("admitted report: %v", err)
	}

	msg := resolveRequest(seq)
	msg.SetAccept(&devicev1.AcceptObservedDecision{})
	_, err = h.svc.ResolveDesynchronization(ctx, connect.NewRequest(msg))
	wantCode(t, err, connect.CodeFailedPrecondition)
	// FailedPrecondition is also what a second intent over a held lane
	// answers; what tells the two apart for the operator is the remedy.
	if !strings.Contains(err.Error(), "AbandonMutation") {
		t.Errorf("message = %q, want the call that ends the mutation named", err)
	}

	record, err := h.journal.Record(ctx, deviceID)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if record.GetMutation().HasDisposition() {
		t.Fatal("the refused resolution disposed the mutation anyway")
	}
}

// A mutation the edge never received is central's alone, so resolving it is
// not the operator claiming anything about the device: the journal disposes it
// REJECTED, which is the true statement that the command never left. Refusing
// here would leave only AbandonMutation, which records the opposite.
func TestResolveDisposesAMutationTheEdgeNeverReceived(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	applied, err := h.svc.ApplyInterfaceDescription(ctx, applyRequest(intentFor(heldDescription), false))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	seq := applied.Msg.GetMutation().GetSequence()
	// The interface is managed, so the read central closes is kept as the last
	// observation and the accept arm has something to adopt.
	if err := h.journal.SetExpected(ctx, deviceID, deviceRef(), iface, heldDescription); err != nil {
		t.Fatalf("set expected: %v", err)
	}
	closeARead(t, h, observation("whatever the device says"))

	msg := resolveRequest(seq)
	msg.SetAccept(&devicev1.AcceptObservedDecision{})
	if _, err := h.svc.ResolveDesynchronization(ctx, connect.NewRequest(msg)); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	record, err := h.journal.Record(ctx, deviceID)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if record.HasMutation() {
		t.Fatalf("the resolution left the mutation open: %+v", record.GetMutation())
	}
	if got := record.GetExpectedDescriptions()[iface]; got != "whatever the device says" {
		t.Errorf("expected description = %q, want what the read observed", got)
	}
}

// Nothing in the schema ties the device the request names to the one the
// replacement intent names. Admitted into this device's lane, an intent naming
// another one would put that name on the audit record, the idempotency digest,
// and the ExecuteRequest the edge receives.
func TestResolveReplaceRefusesAnIntentNamingAnotherDevice(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seq := held(t, h)

	other := intentFor("uplink to core b")
	local := &inventoryv1.DeviceLocalRef{}
	local.SetId("0192e6a0-0000-7000-8000-0000000000ff")
	ref := &inventoryv1.DeviceGlobalRef{}
	ref.SetDevice(local)
	other.SetDevice(ref)

	msg := resolveRequest(seq)
	msg.SetReplace(other)
	_, err := h.svc.ResolveDesynchronization(ctx, connect.NewRequest(msg))
	wantCode(t, err, connect.CodeInvalidArgument)
}
