package mutation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/credential"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/freeze"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/mutation"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
)

// fakeDeliverer records every event handed to it and can be told to fail
// once, or to block until released.
type fakeDeliverer struct {
	events    []*eventv1.DeviceOperationEvent
	failNext  bool
	release   chan struct{}
	awaitCall chan struct{}
}

func newFakeDeliverer() *fakeDeliverer {
	return &fakeDeliverer{release: closedChan()}
}

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func (d *fakeDeliverer) Emit(ctx context.Context, event *eventv1.DeviceOperationEvent) error {
	if d.awaitCall != nil {
		close(d.awaitCall)
	}
	select {
	case <-d.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	d.events = append(d.events, event)
	if d.failNext {
		d.failNext = false
		return errors.New("delivery failed")
	}
	return nil
}

func fakeSubmission(pulses ...credential.SubmissionUpdate) credential.SubmissionCredentialSource {
	return submissionFunc(func(_ context.Context, _, _ string, _ uint64) (*edgev1.SubmissionGrant, <-chan credential.SubmissionUpdate, error) {
		ch := make(chan credential.SubmissionUpdate, len(pulses))
		for _, p := range pulses {
			ch <- p
		}
		close(ch)
		grant := &edgev1.SubmissionGrant{}
		return grant, ch, nil
	})
}

type submissionFunc func(ctx context.Context, deviceID, bindingID string, sequence uint64) (*edgev1.SubmissionGrant, <-chan credential.SubmissionUpdate, error)

func (f submissionFunc) Open(ctx context.Context, deviceID, bindingID string, sequence uint64) (*edgev1.SubmissionGrant, <-chan credential.SubmissionUpdate, error) {
	return f(ctx, deviceID, bindingID, sequence)
}

// mutationRequest builds an ExecuteRequest whose intent always expects
// fingerprint "fw-A" — the fixed value every baseDeps sets as
// CurrentFingerprint, so a caller wanting a fingerprint mismatch builds its
// own MutationIntent instead of adding an unused parameter here.
func mutationRequest(sequence uint64, description string) *integrationv1.ExecuteRequest {
	change := &accessv1.InterfaceDescriptionChange{}
	change.SetInterfaceName("ethernet 1/1/1")
	change.SetDescription(description)

	intent := &accessv1.MutationIntent{}
	intent.SetExpectedFirmwareFingerprint("fw-A")
	intent.SetInterfaceDescription(change)

	req := &integrationv1.ExecuteRequest{}
	req.SetSequence(sequence)
	req.SetMutation(intent)
	return req
}

func readRequest(sequence uint64) *integrationv1.ExecuteRequest {
	readIntent := &accessv1.InterfaceReadIntent{}
	readIntent.SetInterfaceName("ethernet 1/1/1")
	typedRead := &accessv1.TypedRead{}
	typedRead.SetInterface(readIntent)

	req := &integrationv1.ExecuteRequest{}
	req.SetSequence(sequence)
	req.SetRead(typedRead)
	return req
}

func completeObservation(description string) *accessv1.InterfaceObservation {
	obs := &accessv1.InterfaceObservation{}
	obs.SetInterfaceName("ethernet 1/1/1")
	obs.SetDescription(description)
	obs.SetAdminStatus(interfacev1.AdminStatus_ADMIN_STATUS_UP)
	obs.SetOperStatus(interfacev1.OperStatus_OPER_STATUS_UP)
	obs.SetCompleteness(accessv1.Completeness_COMPLETENESS_COMPLETE)
	return obs
}

func baseDeps(deliverer *fakeDeliverer, submission credential.SubmissionCredentialSource) mutation.Deps {
	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		panic(err)
	}
	return mutation.Deps{
		CurrentFingerprint: "fw-A",
		DeviceID:           "0192e6a0-0000-7000-8000-0000000000ed",
		Submission:         submission,
		Freeze:             freeze.New(view),
		Audit:              deliverer,
		Telemetry:          view,
		Clock:              time.Now,
	}
}

func TestFullHappyPathPhaseByPhase(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, fakeSubmission())

	var submitted bool
	deps.Submit = func(_ context.Context, _ *accessv1.InterfaceDescriptionChange) error {
		submitted = true
		return nil
	}
	deps.Read = func(_ context.Context) (*accessv1.InterfaceObservation, error) {
		return completeObservation("uplink to core"), nil
	}

	req := mutationRequest(42, "uplink to core")
	m, err := mutation.Admitted(req, deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}
	if got := m.Phase(); got != accessv1.OperationPhase_OPERATION_PHASE_ADMITTED {
		t.Fatalf("Phase() = %v, want ADMITTED", got)
	}

	ctx := context.Background()

	checkpointReq := &integrationv1.CheckpointRequest{}
	checkpointReq.SetSequence(42)
	ack, err := m.Checkpoint(ctx, checkpointReq)
	if err != nil {
		t.Fatalf("Checkpoint() error: %v", err)
	}
	if ack.GetSequence() != 42 {
		t.Errorf("CheckpointAck sequence = %d, want 42", ack.GetSequence())
	}
	if got := m.Phase(); got != accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED {
		t.Fatalf("Phase() = %v, want POSSIBLY_APPLIED", got)
	}

	if err := m.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if !submitted {
		t.Error("Submit was never called")
	}

	obs, err := m.Observe(ctx)
	if err != nil {
		t.Fatalf("Observe() error: %v", err)
	}
	if obs.GetDescription() != "uplink to core" {
		t.Errorf("observation description = %q, want %q", obs.GetDescription(), "uplink to core")
	}
	if got := m.Phase(); got != accessv1.OperationPhase_OPERATION_PHASE_OBSERVING {
		t.Fatalf("Phase() = %v, want OBSERVING", got)
	}

	disposition, err := m.Compare(ctx, nil)
	if err != nil {
		t.Fatalf("Compare() error: %v", err)
	}
	if disposition != accessv1.Disposition_DISPOSITION_VERIFIED {
		t.Fatalf("Compare() = %v, want VERIFIED", disposition)
	}

	if err := m.MarkVerified(ctx); err != nil {
		t.Fatalf("MarkVerified() error: %v", err)
	}
	if got := m.Phase(); got != accessv1.OperationPhase_OPERATION_PHASE_VERIFIED {
		t.Fatalf("Phase() = %v, want VERIFIED", got)
	}

	termAck := &integrationv1.TerminalResultAck{}
	termAck.SetSequence(42)
	termAck.SetDisposition(accessv1.Disposition_DISPOSITION_VERIFIED)
	if err := m.Acknowledge(ctx, termAck); err != nil {
		t.Fatalf("Acknowledge() error: %v", err)
	}
	if got := m.Phase(); got != accessv1.OperationPhase_OPERATION_PHASE_RELEASED {
		t.Fatalf("Phase() = %v, want RELEASED", got)
	}

	wantPhases := []accessv1.OperationPhase{
		accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED,
		accessv1.OperationPhase_OPERATION_PHASE_OBSERVING,
		accessv1.OperationPhase_OPERATION_PHASE_VERIFIED,
		accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED,
		accessv1.OperationPhase_OPERATION_PHASE_RELEASED,
	}
	var gotPhases []accessv1.OperationPhase
	for _, e := range deliverer.events {
		if pt := e.GetPhaseTransitioned(); pt != nil {
			gotPhases = append(gotPhases, pt.GetTo())
		}
	}
	if len(gotPhases) != len(wantPhases) {
		t.Fatalf("phase transitions = %v, want %v", gotPhases, wantPhases)
	}
	for i := range wantPhases {
		if gotPhases[i] != wantPhases[i] {
			t.Errorf("phase transition %d = %v, want %v", i, gotPhases[i], wantPhases[i])
		}
	}
}

func TestReadArmStopsAtObserving(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, fakeSubmission())
	deps.CurrentFingerprint = "" // reads never carry an expected fingerprint
	deps.Read = func(_ context.Context) (*accessv1.InterfaceObservation, error) {
		return completeObservation("uplink to core"), nil
	}

	req := readRequest(7)
	m, err := mutation.Admitted(req, deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}

	ctx := context.Background()
	if err := m.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if _, err := m.Observe(ctx); err != nil {
		t.Fatalf("Observe() error: %v", err)
	}
	if got := m.Phase(); got != accessv1.OperationPhase_OPERATION_PHASE_OBSERVING {
		t.Fatalf("Phase() = %v, want OBSERVING", got)
	}

	result := m.Result()
	if result.GetPhaseReached() != accessv1.OperationPhase_OPERATION_PHASE_OBSERVING {
		t.Errorf("Result().PhaseReached = %v, want OBSERVING", result.GetPhaseReached())
	}
}

func TestCheckpointThenRevokedPulseBlocksSubmission(t *testing.T) {
	deliverer := newFakeDeliverer()
	pulse := &edgev1.AuthorityPulse{}
	pulse.SetAuthority(edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_REVOKED)
	deps := baseDeps(deliverer, fakeSubmission(credential.SubmissionUpdate{Pulse: pulse}))

	var submitted bool
	deps.Submit = func(_ context.Context, _ *accessv1.InterfaceDescriptionChange) error {
		submitted = true
		return nil
	}

	req := mutationRequest(1, "x")
	m, err := mutation.Admitted(req, deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}

	ctx := context.Background()
	checkpointReq := &integrationv1.CheckpointRequest{}
	checkpointReq.SetSequence(1)
	if _, err := m.Checkpoint(ctx, checkpointReq); err != nil {
		t.Fatalf("Checkpoint() error: %v", err)
	}

	if err := m.Execute(ctx); err == nil {
		t.Fatal("Execute() error = nil, want a revoked-authority error")
	}
	if submitted {
		t.Fatal("Submit was called despite a revoked authority pulse")
	}
}

func TestCancellationBeforeSubmissionBlocksItCancellationAfterDoesNot(t *testing.T) {
	deliverer := newFakeDeliverer()

	t.Run("before submission", func(t *testing.T) {
		deps := baseDeps(deliverer, fakeSubmission())
		var submitted bool
		deps.Submit = func(_ context.Context, _ *accessv1.InterfaceDescriptionChange) error {
			submitted = true
			return nil
		}
		req := mutationRequest(2, "x")
		m, err := mutation.Admitted(req, deps)
		if err != nil {
			t.Fatalf("Admitted() error: %v", err)
		}
		checkpointReq := &integrationv1.CheckpointRequest{}
		checkpointReq.SetSequence(2)
		if _, err := m.Checkpoint(context.Background(), checkpointReq); err != nil {
			t.Fatalf("Checkpoint() error: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := m.Execute(ctx); err == nil {
			t.Fatal("Execute() error = nil, want a context-canceled error")
		}
		if submitted {
			t.Fatal("Submit was called after the context was canceled before submission")
		}
	})

	t.Run("after submission", func(t *testing.T) {
		deps := baseDeps(deliverer, fakeSubmission())
		var observed bool
		deps.Submit = func(_ context.Context, _ *accessv1.InterfaceDescriptionChange) error {
			return nil
		}
		deps.Read = func(_ context.Context) (*accessv1.InterfaceObservation, error) {
			observed = true
			return completeObservation("x"), nil
		}
		req := mutationRequest(3, "x")
		m, err := mutation.Admitted(req, deps)
		if err != nil {
			t.Fatalf("Admitted() error: %v", err)
		}
		checkpointReq := &integrationv1.CheckpointRequest{}
		checkpointReq.SetSequence(3)
		if _, err := m.Checkpoint(context.Background(), checkpointReq); err != nil {
			t.Fatalf("Checkpoint() error: %v", err)
		}
		if err := m.Execute(context.Background()); err != nil {
			t.Fatalf("Execute() error: %v", err)
		}

		// A canceled context after submission still proceeds to observe,
		// per decision 5: a lost connection after submission does not fail
		// the mutation.
		if _, err := m.Observe(context.Background()); err != nil {
			t.Fatalf("Observe() error: %v", err)
		}
		if !observed {
			t.Fatal("Observe's Read was never called after submission")
		}
	})
}

func TestConflictingReadsBlockInsteadOfVerifying(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, fakeSubmission())
	deps.Submit = func(_ context.Context, _ *accessv1.InterfaceDescriptionChange) error { return nil }
	deps.Read = func(_ context.Context) (*accessv1.InterfaceObservation, error) {
		return completeObservation("fresh value"), nil
	}

	req := mutationRequest(9, "x")
	m, err := mutation.Admitted(req, deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}
	checkpointReq := &integrationv1.CheckpointRequest{}
	checkpointReq.SetSequence(9)
	ctx := context.Background()
	if _, err := m.Checkpoint(ctx, checkpointReq); err != nil {
		t.Fatalf("Checkpoint() error: %v", err)
	}
	if err := m.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if _, err := m.Observe(ctx); err != nil {
		t.Fatalf("Observe() error: %v", err)
	}

	cached := completeObservation("cached value")
	if _, err := m.Compare(ctx, cached); err == nil {
		t.Fatal("Compare() error = nil, want a conflicting-reads error")
	}
}

func TestPinnedRouteFailurePropagatesWithoutFallback(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, fakeSubmission())

	fallbackCalled := false
	deps.Read = func(_ context.Context) (*accessv1.InterfaceObservation, error) {
		// Simulates a caller-built Read closure honoring an explicit pin:
		// the pin's own route failed, so the closure returns that failure
		// directly. fallbackCalled would only ever be set by a fallback
		// route Machine itself triggered, which must never happen — Machine
		// has no route-selection logic of its own to test here.
		return nil, errors.New("pinned ssh route failed")
	}

	req := readRequest(11)
	deps.CurrentFingerprint = ""
	m, err := mutation.Admitted(req, deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}

	ctx := context.Background()
	if err := m.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if _, err := m.Observe(ctx); err == nil {
		t.Fatal("Observe() error = nil, want the pinned route's failure")
	}
	if fallbackCalled {
		t.Fatal("a pinned route's failure must never fall back")
	}
}

func TestOutOfOrderTransitionIsRejected(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, fakeSubmission())
	req := mutationRequest(5, "x")
	m, err := mutation.Admitted(req, deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}

	// Execute before Checkpoint: still ADMITTED, not POSSIBLY_APPLIED.
	if err := m.Execute(context.Background()); err == nil {
		t.Fatal("Execute() error = nil, want an out-of-order error")
	}
}

func TestExecuteBlocksWhileFrozenAndProceedsOnceUnfrozen(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, fakeSubmission())
	var submitted bool
	deps.Submit = func(_ context.Context, _ *accessv1.InterfaceDescriptionChange) error {
		submitted = true
		return nil
	}

	req := mutationRequest(6, "x")
	m, err := mutation.Admitted(req, deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}
	ctx := context.Background()
	checkpointReq := &integrationv1.CheckpointRequest{}
	checkpointReq.SetSequence(6)
	if _, err := m.Checkpoint(ctx, checkpointReq); err != nil {
		t.Fatalf("Checkpoint() error: %v", err)
	}

	deps.Freeze.Freeze(ctx)

	done := make(chan error, 1)
	go func() { done <- m.Execute(context.Background()) }()

	select {
	case <-done:
		t.Fatal("Execute returned before Unfreeze")
	case <-time.After(50 * time.Millisecond):
	}
	if submitted {
		t.Fatal("Submit was called while frozen")
	}
	if m.Phase() != accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED {
		t.Fatalf("Phase() = %v while frozen, want POSSIBLY_APPLIED (no phase transition while paused)", m.Phase())
	}

	deps.Freeze.Unfreeze(ctx)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Execute() error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Execute did not return after Unfreeze")
	}
	if !submitted {
		t.Fatal("Submit was never called after Unfreeze")
	}
}

func TestDelivererErrorAtReleaseLeavesPhaseAtLastDurableValue(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, fakeSubmission())
	deps.Submit = func(_ context.Context, _ *accessv1.InterfaceDescriptionChange) error { return nil }
	deps.Read = func(_ context.Context) (*accessv1.InterfaceObservation, error) {
		return completeObservation("x"), nil
	}

	req := mutationRequest(8, "x")
	m, err := mutation.Admitted(req, deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}
	ctx := context.Background()
	checkpointReq := &integrationv1.CheckpointRequest{}
	checkpointReq.SetSequence(8)
	if _, err := m.Checkpoint(ctx, checkpointReq); err != nil {
		t.Fatalf("Checkpoint() error: %v", err)
	}
	if err := m.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if _, err := m.Observe(ctx); err != nil {
		t.Fatalf("Observe() error: %v", err)
	}
	if _, err := m.Compare(ctx, nil); err != nil {
		t.Fatalf("Compare() error: %v", err)
	}
	if err := m.MarkVerified(ctx); err != nil {
		t.Fatalf("MarkVerified() error: %v", err)
	}

	deliverer.failNext = true
	termAck := &integrationv1.TerminalResultAck{}
	termAck.SetSequence(8)
	termAck.SetDisposition(accessv1.Disposition_DISPOSITION_VERIFIED)
	if err := m.Acknowledge(ctx, termAck); err == nil {
		t.Fatal("Acknowledge() error = nil, want the deliverer's failure")
	}

	if got := m.Phase(); got != accessv1.OperationPhase_OPERATION_PHASE_VERIFIED {
		t.Fatalf("Phase() = %v, want VERIFIED (last durable value before the failed release)", got)
	}
}
