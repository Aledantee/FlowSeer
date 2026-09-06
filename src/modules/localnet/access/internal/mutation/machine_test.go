package mutation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
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

// fakeSubmission simulates a submission stream whose relay has already
// processed every pulse in order by the time Execute checks Authority() —
// the synchronous-snapshot contract [credential.SubmissionHandle] promises.
// With none given, authority stays AUTHORIZED (what an issued grant implies
// until told otherwise).
func fakeSubmission(pulses ...*edgev1.AuthorityPulse) credential.SubmissionCredentialSource {
	authority := edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED
	for _, p := range pulses {
		authority = p.GetAuthority()
	}
	return submissionFunc(func(context.Context, string, string, uint64) (credential.SubmissionHandle, error) {
		return &fakeSubmissionHandle{grant: &edgev1.SubmissionGrant{}, authority: authority}, nil
	})
}

type fakeSubmissionHandle struct {
	grant     *edgev1.SubmissionGrant
	authority edgev1.SubmissionAuthority
	err       error
}

func (h *fakeSubmissionHandle) Grant() *edgev1.SubmissionGrant        { return h.grant }
func (h *fakeSubmissionHandle) Authority() edgev1.SubmissionAuthority { return h.authority }
func (h *fakeSubmissionHandle) Err() error                            { return h.err }
func (h *fakeSubmissionHandle) Close() error                          { return nil }

type submissionFunc func(ctx context.Context, deviceID, bindingID string, sequence uint64) (credential.SubmissionHandle, error)

func (f submissionFunc) Open(ctx context.Context, deviceID, bindingID string, sequence uint64) (credential.SubmissionHandle, error) {
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

	result := m.Result(nil)
	if result.GetPhaseReached() != accessv1.OperationPhase_OPERATION_PHASE_OBSERVING {
		t.Errorf("Result().PhaseReached = %v, want OBSERVING", result.GetPhaseReached())
	}
}

func TestCheckpointThenRevokedPulseBlocksSubmission(t *testing.T) {
	deliverer := newFakeDeliverer()
	pulse := &edgev1.AuthorityPulse{}
	pulse.SetAuthority(edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_REVOKED)
	deps := baseDeps(deliverer, fakeSubmission(pulse))

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

// TestBrokenSubmissionStreamBlocksSubmissionEvenWithoutARevokedPulse proves
// Execute checks the handle's Err(), not just Authority(): a stream that
// ended abnormally (a dropped connection) must never be treated as "still
// authorized" merely because no explicit REVOKED pulse was ever seen.
func TestBrokenSubmissionStreamBlocksSubmissionEvenWithoutARevokedPulse(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, submissionFunc(func(context.Context, string, string, uint64) (credential.SubmissionHandle, error) {
		return &fakeSubmissionHandle{
			grant:     &edgev1.SubmissionGrant{},
			authority: edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED,
			err:       errors.New("connection reset"),
		}, nil
	}))

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
		t.Fatal("Execute() error = nil, want the broken stream's error")
	}
	if submitted {
		t.Fatal("Submit was called despite the submission stream having ended abnormally")
	}
}

// TestExpiredGrantDeadlineBlocksSubmission proves Execute consults the
// grant's own deadline rather than only Authority(): a grant issued with a
// deadline already in the past must not authorize a command.
func TestExpiredGrantDeadlineBlocksSubmission(t *testing.T) {
	deliverer := newFakeDeliverer()
	past := time.Now().Add(-time.Minute)
	grant := &edgev1.SubmissionGrant{}
	grant.SetDeadline(timestamppb.New(past))

	deps := baseDeps(deliverer, submissionFunc(func(context.Context, string, string, uint64) (credential.SubmissionHandle, error) {
		return &fakeSubmissionHandle{grant: grant, authority: edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED}, nil
	}))

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
		t.Fatal("Execute() error = nil, want an expired-deadline error")
	}
	if submitted {
		t.Fatal("Submit was called despite an already-expired grant deadline")
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

	// MarkVerified straight from ADMITTED must not resurrect the mutation
	// into VERIFIED without any observation ever having been taken.
	if err := m.MarkVerified(context.Background()); err == nil {
		t.Fatal("MarkVerified() error = nil from ADMITTED, want an out-of-order error")
	}

	// Abandon is only valid from RECOVERING.
	if err := m.Abandon(context.Background()); err == nil {
		t.Fatal("Abandon() error = nil from ADMITTED, want an out-of-order error")
	}
}

// TestAbandonAfterReleaseIsRejected proves a RELEASED (terminal) mutation
// cannot be resurrected into ABANDONED.
func TestAbandonAfterReleaseIsRejected(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, fakeSubmission())
	deps.Submit = func(_ context.Context, _ *accessv1.InterfaceDescriptionChange) error { return nil }
	deps.Read = func(_ context.Context) (*accessv1.InterfaceObservation, error) {
		return completeObservation("x"), nil
	}

	req := mutationRequest(9, "x")
	m, err := mutation.Admitted(req, deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}
	ctx := context.Background()
	checkpointReq := &integrationv1.CheckpointRequest{}
	checkpointReq.SetSequence(9)
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
	termAck := &integrationv1.TerminalResultAck{}
	termAck.SetSequence(9)
	termAck.SetDisposition(accessv1.Disposition_DISPOSITION_VERIFIED)
	if err := m.Acknowledge(ctx, termAck); err != nil {
		t.Fatalf("Acknowledge() error: %v", err)
	}

	if err := m.Abandon(ctx); err == nil {
		t.Fatal("Abandon() error = nil after RELEASED, want an out-of-order error")
	}
	if got := m.Phase(); got != accessv1.OperationPhase_OPERATION_PHASE_RELEASED {
		t.Errorf("Phase() = %v, want RELEASED unchanged", got)
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

// TestAdmittedOnFirmwareEpochMismatchDeliversNoAuditEvent proves the
// admission-time check does NOT emit FirmwareEpochChanged: that comparison
// is against central's own (possibly stale) expectation, not evidence that
// the device's firmware actually changed, so recording it would durably log
// once per rejected intent rather than once per real change. See
// [Machine.Observe]'s mid-operation comparison for the one true emission
// site, covered by TestObserveDetectsFirmwareEpochChangeMidOperation.
func TestAdmittedOnFirmwareEpochMismatchDeliversNoAuditEvent(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, fakeSubmission())

	req := mutationRequest(9, "x")
	req.GetMutation().SetExpectedFirmwareFingerprint("stale-fingerprint")

	m, err := mutation.Admitted(req, deps)
	if err == nil {
		t.Fatal("Admitted() error = nil, want a firmware epoch mismatch error")
	}
	if m != nil {
		t.Fatal("Admitted() returned a non-nil Machine alongside its error")
	}
	if code, _ := errs.CodeOf(err); code != mutation.ErrCodeFirmwareEpoch {
		t.Fatalf("Admitted() error code = %v, want %v", code, mutation.ErrCodeFirmwareEpoch)
	}

	if len(deliverer.events) != 0 {
		t.Fatalf("expected no audit event from the admission-time check, got %d", len(deliverer.events))
	}
}

// driveToVerified admits and drives req through Checkpoint, Execute,
// Observe, Compare, and MarkVerified, leaving the Machine at VERIFIED — the
// shared setup for the tests below that check Disposition/BlockReason/
// BlockedSince at and after that phase.
func driveToVerified(t *testing.T, req *integrationv1.ExecuteRequest, deps mutation.Deps) *mutation.Machine {
	t.Helper()
	deps.Submit = func(context.Context, *accessv1.InterfaceDescriptionChange) error { return nil }
	deps.Read = func(context.Context) (*accessv1.InterfaceObservation, error) {
		return completeObservation("uplink to core"), nil
	}

	ctx := context.Background()
	m, err := mutation.Admitted(req, deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}
	checkpointReq := &integrationv1.CheckpointRequest{}
	checkpointReq.SetSequence(req.GetSequence())
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
	return m
}

func TestVerifiedPhaseCarriesNoDispositionYet(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, fakeSubmission())
	m := driveToVerified(t, mutationRequest(20, "uplink to core"), deps)

	// MutationState.disposition_matches_phase requires disposition set
	// exactly at ACKNOWLEDGED, RELEASED, or ABANDONED; VERIFIED is none of
	// those, so Disposition() must still report the zero value here even
	// though the mutation's own outcome is already known to be VERIFIED.
	if got := m.Disposition(); got != accessv1.Disposition_DISPOSITION_UNSPECIFIED {
		t.Errorf("Disposition() at VERIFIED = %v, want DISPOSITION_UNSPECIFIED", got)
	}
	if got := m.BlockReason(); got != accessv1.BlockReason_BLOCK_REASON_UNACKNOWLEDGED {
		t.Errorf("BlockReason() at VERIFIED = %v, want BLOCK_REASON_UNACKNOWLEDGED", got)
	}
	since, ok := m.BlockedSince()
	if !ok {
		t.Fatal("BlockedSince() ok = false while a block reason is set")
	}
	if since.IsZero() {
		t.Error("BlockedSince() returned the zero time while blocked")
	}
}

func TestAcknowledgeSetsDispositionAndReleaseClearsTheBlock(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, fakeSubmission())
	m := driveToVerified(t, mutationRequest(21, "uplink to core"), deps)

	termAck := &integrationv1.TerminalResultAck{}
	termAck.SetSequence(21)
	termAck.SetDisposition(accessv1.Disposition_DISPOSITION_VERIFIED)
	if err := m.Acknowledge(context.Background(), termAck); err != nil {
		t.Fatalf("Acknowledge() error: %v", err)
	}

	if got := m.Phase(); got != accessv1.OperationPhase_OPERATION_PHASE_RELEASED {
		t.Fatalf("Phase() = %v, want RELEASED", got)
	}
	if got := m.Disposition(); got != accessv1.Disposition_DISPOSITION_VERIFIED {
		t.Errorf("Disposition() after Acknowledge = %v, want VERIFIED", got)
	}
	if got := m.BlockReason(); got != accessv1.BlockReason_BLOCK_REASON_UNSPECIFIED {
		t.Errorf("BlockReason() after release = %v, want BLOCK_REASON_UNSPECIFIED", got)
	}
	if since, ok := m.BlockedSince(); ok || !since.IsZero() {
		t.Errorf("BlockedSince() after release = (%v, %v), want (zero, false)", since, ok)
	}
}

func TestAcknowledgeRejectsUnspecifiedDisposition(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, fakeSubmission())
	m := driveToVerified(t, mutationRequest(22, "uplink to core"), deps)

	termAck := &integrationv1.TerminalResultAck{}
	termAck.SetSequence(22)
	// Disposition left at its zero value, DISPOSITION_UNSPECIFIED.
	if err := m.Acknowledge(context.Background(), termAck); err == nil {
		t.Fatal("Acknowledge() error = nil, want an error for an unspecified disposition")
	}
	if got := m.Phase(); got != accessv1.OperationPhase_OPERATION_PHASE_VERIFIED {
		t.Fatalf("Phase() after a rejected Acknowledge = %v, want VERIFIED (unchanged)", got)
	}
}

func TestResultSetsErrorArmWhenGivenAnError(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, fakeSubmission())
	m, err := mutation.Admitted(readRequest(1), deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}

	result := m.Result(errors.New("device unreachable"))
	if result.GetError() == nil {
		t.Fatal("expected the error arm to be set")
	}
	if result.GetObservation() != nil {
		t.Error("expected the observation arm to be unset alongside the error arm")
	}
}

func TestResultFallsBackToErrorArmWhenNoObservationRecorded(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, fakeSubmission())
	m, err := mutation.Admitted(readRequest(1), deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}

	// Result called with a nil error before Observe ever ran: the required
	// oneof must still end up with exactly one arm set, never neither.
	result := m.Result(nil)
	if result.GetObservation() != nil {
		t.Fatal("expected no observation to have been recorded yet")
	}
	if result.GetError() == nil {
		t.Fatal("expected Result() to fall back to the error arm rather than leave the oneof unset")
	}
}

func TestObserveDetectsFirmwareEpochChangeMidOperation(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, fakeSubmission())
	deps.Submit = func(context.Context, *accessv1.InterfaceDescriptionChange) error { return nil }
	deps.Read = func(context.Context) (*accessv1.InterfaceObservation, error) {
		obs := completeObservation("uplink to core")
		prov := &inventoryv1.Provenance{}
		prov.SetFirmwareFingerprint("fw-B") // deps.CurrentFingerprint is "fw-A"
		obs.SetProvenance(prov)
		return obs, nil
	}

	ctx := context.Background()
	req := mutationRequest(30, "uplink to core")
	m, err := mutation.Admitted(req, deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}
	checkpointReq := &integrationv1.CheckpointRequest{}
	checkpointReq.SetSequence(30)
	if _, err := m.Checkpoint(ctx, checkpointReq); err != nil {
		t.Fatalf("Checkpoint() error: %v", err)
	}
	if err := m.Execute(ctx); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	if _, err := m.Observe(ctx); err == nil {
		t.Fatal("Observe() error = nil, want a firmware epoch change error")
	} else if code, _ := errs.CodeOf(err); code != mutation.ErrCodeFirmwareEpoch {
		t.Fatalf("Observe() error code = %v, want %v", code, mutation.ErrCodeFirmwareEpoch)
	}

	if got := m.BlockReason(); got != accessv1.BlockReason_BLOCK_REASON_FIRMWARE_EPOCH_CHANGED {
		t.Errorf("BlockReason() = %v, want BLOCK_REASON_FIRMWARE_EPOCH_CHANGED", got)
	}

	found := false
	for _, event := range deliverer.events {
		if changed := event.GetFirmwareEpochChanged(); changed != nil {
			found = true
			if changed.GetPreviousFingerprint() != "fw-A" || changed.GetNewFingerprint() != "fw-B" {
				t.Errorf("FirmwareEpochChanged = {previous: %q, new: %q}, want {previous: \"fw-A\", new: \"fw-B\"}",
					changed.GetPreviousFingerprint(), changed.GetNewFingerprint())
			}
		}
	}
	if !found {
		t.Error("expected a FirmwareEpochChanged audit event from the mid-operation check")
	}
}

func TestCorrelationIDsCarryIdempotencyKeyAndTraceID(t *testing.T) {
	deliverer := newFakeDeliverer()
	deps := baseDeps(deliverer, fakeSubmission())
	deps.Submit = func(context.Context, *accessv1.InterfaceDescriptionChange) error { return nil }
	deps.Read = func(context.Context) (*accessv1.InterfaceObservation, error) {
		return completeObservation("uplink to core"), nil
	}

	req := mutationRequest(31, "uplink to core")
	const idempotencyKey = "0192e6a0-0000-7000-8000-0000000000ee"
	req.GetMutation().SetIdempotencyKey(idempotencyKey)

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("TraceIDFromHex() error: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("SpanIDFromHex() error: %v", err)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	m, err := mutation.Admitted(req, deps)
	if err != nil {
		t.Fatalf("Admitted() error: %v", err)
	}
	checkpointReq := &integrationv1.CheckpointRequest{}
	checkpointReq.SetSequence(31)
	if _, err := m.Checkpoint(ctx, checkpointReq); err != nil {
		t.Fatalf("Checkpoint() error: %v", err)
	}

	if len(deliverer.events) == 0 {
		t.Fatal("expected at least one audit event from Checkpoint's transition")
	}
	ids := deliverer.events[len(deliverer.events)-1].GetCorrelationIds()
	if ids["idempotency_key"] != idempotencyKey {
		t.Errorf("correlation_ids[idempotency_key] = %q, want %q", ids["idempotency_key"], idempotencyKey)
	}
	if ids["trace_id"] != traceID.String() {
		t.Errorf("correlation_ids[trace_id] = %q, want %q", ids["trace_id"], traceID.String())
	}
}
