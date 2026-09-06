package mutation

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/audit"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/credential"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/freeze"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
)

// Error codes this package returns. See each function's doc for when.
var (
	ErrCodeFirmwareEpoch          = errs.NewCode("mutation/firmware-epoch")
	ErrCodeOutOfOrder             = errs.NewCode("mutation/out-of-order")
	ErrCodeRevoked                = errs.NewCode("mutation/revoked")
	ErrCodeConflictingReads       = errs.NewCode("mutation/conflicting-reads")
	ErrCodeDispositionUnspecified = errs.NewCode("mutation/disposition-unspecified")
)

// Deps bundles this package's dependencies. Read, Submit, and Verify are
// already bound to whatever route a caller resolved (an explicit pin or
// interfaces.SelectRoute's own fallback) and whatever session that route
// needs; Machine calls them and faithfully propagates their errors without
// retrying or falling back on its own. Construct with keyed fields.
type Deps struct {
	// CurrentFingerprint is the device's firmware fingerprint as last
	// learned by an identity probe. [Admitted] blocks a mutation whose
	// intent names a different one, per decision 7.
	CurrentFingerprint string
	// DeviceID names the device for audit events built from this Machine
	// and is passed to Submission.Open as OpenDeviceSubmissionRequest's
	// device_id, which is required and UUID-constrained.
	DeviceID string
	// BindingID names the integration binding running this mutation and is
	// passed to Submission.Open as OpenDeviceSubmissionRequest's
	// binding_id, likewise required and UUID-constrained: a real
	// EdgeService rejects an empty value with InvalidArgument.
	BindingID string

	Read   func(ctx context.Context) (*accessv1.InterfaceObservation, error)
	Submit func(ctx context.Context, intent *accessv1.InterfaceDescriptionChange) error
	Verify func(ctx context.Context, intent *accessv1.InterfaceDescriptionChange, since, now time.Time) (*accessv1.InterfaceObservation, interfaces.VerificationDisposition, error)

	Submission credential.SubmissionCredentialSource
	Freeze     *freeze.Gate
	Audit      audit.Deliverer
	Telemetry  *telemetry.View
	Clock      func() time.Time
}

// Machine is one admitted operation's typestate. The zero value is not
// usable; construct with [Admitted]. Safe for concurrent use.
type Machine struct {
	req  *integrationv1.ExecuteRequest
	deps Deps

	mu              sync.Mutex
	phase           accessv1.OperationPhase
	disposition     accessv1.Disposition
	blockReason     accessv1.BlockReason
	blockedSince    time.Time
	lastObservation *accessv1.InterfaceObservation
}

// Admitted constructs a Machine in phase ADMITTED for req, which already
// carries the central-assigned sequence (execution.proto's field comment:
// "The device's lane sequence this operation was admitted at"). For a
// mutation whose intent's expected firmware fingerprint differs from
// deps.CurrentFingerprint, it returns an error instead of a Machine — the
// mutation never reaches ADMITTED under a stale epoch, per decision 7. A
// read never carries an expected fingerprint and is never blocked here.
// This check alone never emits flowseer.device.firmware.epoch_changed —
// nothing in this module does; see the access module README's "Open gap:
// no mid-operation firmware-epoch re-check" section for why and what a
// real fix needs.
func Admitted(req *integrationv1.ExecuteRequest, deps Deps) (*Machine, error) {
	if mutationIntent := req.GetMutation(); mutationIntent != nil {
		if expected := mutationIntent.GetExpectedFirmwareFingerprint(); expected != deps.CurrentFingerprint {
			// No FirmwareEpochChanged event here: this comparison is
			// against central's own expectation, which can differ from
			// the edge's cached fingerprint simply because central is
			// stale, not because the device's firmware actually changed.
			// A real epoch-change signal would need a fresh probe at
			// observation time compared against the earlier probe's own
			// output — this module has no such check (see the README's
			// "Open gap" section) — so emitting here would durably record
			// one event per rejected intent, including a central
			// retrying the same stale intent many times for a single (or
			// no) real change.
			return nil, errs.New().Code(ErrCodeFirmwareEpoch).
				Attr("expected_fingerprint", expected).
				Attr("current_fingerprint", deps.CurrentFingerprint).
				Msg("device firmware fingerprint differs from the intent's expectation")
		}
	}

	return &Machine{
		req:   req,
		deps:  deps,
		phase: accessv1.OperationPhase_OPERATION_PHASE_ADMITTED,
	}, nil
}

// Phase reports the mutation's current durable phase.
func (m *Machine) Phase() accessv1.OperationPhase {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.phase
}

// IsRead reports whether this Machine drives a TypedRead rather than a
// MutationIntent.
func (m *Machine) IsRead() bool { return m.req.GetMutation() == nil }

// Disposition reports the mutation's terminal outcome. Meaningful only once
// [Machine.Phase] is ACKNOWLEDGED, RELEASED, or ABANDONED — the zero value,
// DISPOSITION_UNSPECIFIED, at any other phase, matching
// MutationState.disposition_matches_phase.
func (m *Machine) Disposition() accessv1.Disposition {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.disposition
}

// BlockReason reports why the device's lane is blocked, or
// BLOCK_REASON_UNSPECIFIED when it is not.
func (m *Machine) BlockReason() accessv1.BlockReason {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.blockReason
}

// BlockedSince reports when the current block began and whether one is in
// effect. ok is false, and since the zero value, exactly when
// [Machine.BlockReason] is BLOCK_REASON_UNSPECIFIED — matching
// MutationState.blocked_since_matches_reason.
func (m *Machine) BlockedSince() (since time.Time, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.blockedSince, m.blockReason != accessv1.BlockReason_BLOCK_REASON_UNSPECIFIED
}

// correlationIDs builds the idempotency key and trace id an audit event
// carries, bounded and copied so a caller's own map is never aliased or
// mutated by [audit.newEvent]'s further bounding. A read carries no
// idempotency key, since only a MutationIntent has one.
func (m *Machine) correlationIDs(ctx context.Context) map[string]string {
	ids := make(map[string]string, 2)
	if key := m.req.GetMutation().GetIdempotencyKey(); key != "" {
		ids["idempotency_key"] = key
	}
	if sc := trace.SpanContextFromContext(ctx); sc.HasTraceID() {
		ids["trace_id"] = sc.TraceID().String()
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func (m *Machine) common(ctx context.Context) audit.Common {
	return audit.Common{
		Device:         audit.Device{DeviceID: m.deps.DeviceID},
		Sequence:       m.req.GetSequence(),
		CorrelationIDs: m.correlationIDs(ctx),
	}
}

func (m *Machine) requirePhase(want accessv1.OperationPhase, method string) error {
	return m.requireAnyPhase(method, want)
}

// requireAnyPhase rejects a call unless the mutation's current phase is one
// of want.
func (m *Machine) requireAnyPhase(method string, want ...accessv1.OperationPhase) error {
	got := m.Phase()
	for _, w := range want {
		if got == w {
			return nil
		}
	}
	return errs.New().Code(ErrCodeOutOfOrder).
		Attr("method", method).
		Attr("phase", got.String()).
		Msgf("%s is not valid from %s", method, got)
}

// transition delivers a PhaseTransitioned audit event for the move to to
// and, only once that delivery succeeds, updates the durable phase. A
// Deliverer failure leaves Phase() reporting the mutation's last durable
// value — decision 13's audit-before-release rule applied to every
// transition, not only release.
func (m *Machine) transition(ctx context.Context, to accessv1.OperationPhase) error {
	m.mu.Lock()
	from := m.phase
	m.mu.Unlock()

	event := audit.BuildPhaseTransitioned(m.deps.Clock, m.common(ctx), from, to)
	if err := m.deps.Audit.Emit(ctx, event); err != nil {
		return errs.Wrap(err, "deliver phase transitioned event")
	}

	m.mu.Lock()
	m.phase = to
	m.mu.Unlock()

	return nil
}

// block records reason and when it began, matching
// MutationState.blocked_since_matches_reason, then delivers a LaneBlocked
// audit event and telemetry. It does not itself change Phase(). The state
// write happens before the audit attempt, not after: a caller (recovery's
// Runner, deciding whether to engage a Hold) must see the block took
// effect even when the accompanying audit record's delivery fails —
// mirroring EvaluateDrift's own engage-before-audit ordering — rather than
// an undelivered notification silently leaving the machine unblocked.
func (m *Machine) block(ctx context.Context, reason accessv1.BlockReason) error {
	m.mu.Lock()
	m.blockReason = reason
	m.blockedSince = m.deps.Clock()
	m.mu.Unlock()

	event := audit.BuildLaneBlocked(m.deps.Clock, m.common(ctx), reason)
	if err := m.deps.Audit.Emit(ctx, event); err != nil {
		return errs.Wrap(err, "deliver lane blocked event")
	}

	m.deps.Telemetry.LaneBlocked(ctx, reason)
	return nil
}

// Checkpoint acknowledges central's CheckpointRequest for this mutation's
// sequence and moves the phase to POSSIBLY_APPLIED: decision 4's rule that
// central durably records the intent may reach the device before any
// command is submitted. It is invalid for a read, which never checkpoints.
func (m *Machine) Checkpoint(ctx context.Context, req *integrationv1.CheckpointRequest) (*integrationv1.CheckpointAck, error) {
	if m.IsRead() {
		return nil, errs.New().Code(ErrCodeOutOfOrder).Msg("checkpoint is not valid for a read operation")
	}
	if req.GetSequence() != m.req.GetSequence() {
		return nil, errs.New().Code(ErrCodeOutOfOrder).
			Attr("checkpoint_sequence", req.GetSequence()).
			Attr("admitted_sequence", m.req.GetSequence()).
			Msg("checkpoint sequence does not match the admitted request")
	}
	if err := m.requirePhase(accessv1.OperationPhase_OPERATION_PHASE_ADMITTED, "Checkpoint"); err != nil {
		return nil, err
	}
	if err := m.transition(ctx, accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED); err != nil {
		return nil, err
	}

	ack := &integrationv1.CheckpointAck{}
	ack.SetSequence(req.GetSequence())
	return ack, nil
}

// Execute submits the mutation's command, requiring a positive
// SUBMISSION_AUTHORITY_AUTHORIZED immediately before doing so, per decision
// 9 — not merely the absence of a seen revocation, which a
// not-yet-delivered pulse could satisfy while a revocation is already in
// flight. For a read it is a no-op: integration/device/v1's README states a
// read's phase_reached is OBSERVING, so a read's device contact happens in
// [Machine.Observe], not here.
func (m *Machine) Execute(ctx context.Context) error {
	mutationIntent := m.req.GetMutation()
	if mutationIntent == nil {
		return nil
	}
	if err := m.requirePhase(accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED, "Execute"); err != nil {
		return err
	}

	// Held for the rest of this call, not just the check: Freeze must not
	// return while this device write is still in flight, and a plain
	// AwaitSideEffect only proves no freeze was active at the instant it
	// returned.
	leave, err := m.deps.Freeze.Enter(ctx)
	if err != nil {
		return errs.Wrap(err, "await control-plane freeze")
	}
	defer leave()

	handle, err := m.deps.Submission.Open(ctx, m.deps.DeviceID, m.deps.BindingID, m.req.GetSequence())
	if err != nil {
		return errs.Wrap(err, "open device submission")
	}
	defer func() { _ = handle.Close() }()

	if handle.Authority() != edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED {
		return errs.New().Code(ErrCodeRevoked).
			Msg("submission authority is not AUTHORIZED; the command is not sent")
	}
	if streamErr := handle.Err(); streamErr != nil {
		return errs.Wrap(streamErr, "submission stream ended before the command was sent")
	}
	if deadline := handle.Grant().GetDeadline(); deadline != nil && deadline.AsTime().Before(m.deps.Clock()) {
		return errs.New().Code(ErrCodeRevoked).
			Msg("submission grant's deadline has already passed; the command is not sent")
	}

	// A cancellation delivered between CheckpointAck and here must be
	// honored: the command is never sent. Once Submit is called, decision
	// 5 takes over — a lost connection during or after submission does not
	// fail the mutation; Observe/recovery handle that ambiguity instead.
	if err := ctx.Err(); err != nil {
		return errs.Wrap(err, "context ended before submission")
	}

	if err := m.deps.Submit(ctx, mutationIntent.GetInterfaceDescription()); err != nil {
		return errs.Wrap(err, "submit mutation")
	}

	return nil
}

// Observe reads the affected state over a fresh session, per decision 2: a
// mutation is verified only by an observation, never by its own command
// succeeding. It is valid from POSSIBLY_APPLIED (the ordinary path), from
// RECOVERING (a recovery re-observation), or from ADMITTED for a read
// (which never checkpoints or executes).
func (m *Machine) Observe(ctx context.Context) (*accessv1.InterfaceObservation, error) {
	phase := m.Phase()
	valid := phase == accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED ||
		phase == accessv1.OperationPhase_OPERATION_PHASE_RECOVERING ||
		(m.IsRead() && phase == accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
	if !valid {
		return nil, errs.New().Code(ErrCodeOutOfOrder).
			Attr("phase", phase.String()).
			Msg("observe is only valid from POSSIBLY_APPLIED, RECOVERING, or ADMITTED for a read")
	}

	if err := m.transition(ctx, accessv1.OperationPhase_OPERATION_PHASE_OBSERVING); err != nil {
		return nil, err
	}

	obs, readErr := m.deps.Read(ctx)

	// A recovery re-observation returns to RECOVERING rather than staying
	// at OBSERVING, since OBSERVING is the transient sub-state of one
	// observation attempt and RECOVERING is where a mutation rests between
	// them — and it does so whether or not the read succeeded. A device
	// that is still unreachable (the exact case recovery exists for) must
	// leave the phase somewhere Observe can be called from again; leaving
	// it stuck at OBSERVING on a read error would permanently wedge the
	// mutation there, since OBSERVING is not itself a valid source phase
	// for a later Observe call, and the horizon check that would abandon
	// it never runs.
	if phase == accessv1.OperationPhase_OPERATION_PHASE_RECOVERING {
		if err := m.transition(ctx, accessv1.OperationPhase_OPERATION_PHASE_RECOVERING); err != nil {
			return nil, err
		}
	}

	if readErr != nil {
		return nil, errs.Wrap(readErr, "observe")
	}

	// There is deliberately no mid-operation firmware-epoch re-check here.
	// obs.GetProvenance().GetFirmwareFingerprint() and deps.CurrentFingerprint
	// come from unrelated sources this module never reconciles:
	// CurrentFingerprint is epoch.Probe's own hex SHA-256 digest (or a
	// host's FingerprintOverride), while the observation's provenance
	// fingerprint is whatever the host's own ProvenanceInputs supplied to
	// interfaces.Read — copied through verbatim, never written from
	// ds.fingerprint, since AddDevice does not return the probed digest to
	// its caller. Comparing them is comparing values with no defined
	// relationship, not detecting a real epoch change; on the documented
	// production path (a host sets FirmwareFingerprint as the schema
	// requires, no FingerprintOverride) the values differ by construction
	// and this check blocked every mutation. A real mid-operation check
	// needs a fresh identity probe at observation time compared against
	// the probe's own earlier output — probe output to probe output, never
	// probe output to a host-supplied provenance field — and that needs a
	// live transport this module's synchronous Submit path does not have.
	// See the README's "Open gap: no mid-operation firmware-epoch
	// re-check" section for where that re-probe belongs and what else is
	// unwired alongside it.

	m.mu.Lock()
	m.lastObservation = obs
	m.mu.Unlock()

	return obs, nil
}

// Compare reports the mutation's disposition against the last call to
// [Machine.Observe]. Valid from OBSERVING (the ordinary path) or RECOVERING
// (Observe's own re-observation transitions back to RECOVERING, per its doc
// comment, so that is where a recovery retry's Compare call finds the
// mutation — OperationPhase's own doc allows VERIFIED from either).
// cached, if non-nil, is an earlier complete observation of the same target
// this mutation must not silently override: if it conflicts with the fresh
// observation on any compared field, no observation carries authority
// (decision-record requirement) and Compare blocks with
// BLOCK_REASON_CONFLICTING_READS instead of reporting a disposition. A read
// has no intent to compare against and always reports DISPOSITION_UNSPECIFIED
// with a nil error: its own observation is the result.
func (m *Machine) Compare(ctx context.Context, cached *accessv1.InterfaceObservation) (accessv1.Disposition, error) {
	if err := m.requireAnyPhase("Compare", accessv1.OperationPhase_OPERATION_PHASE_OBSERVING, accessv1.OperationPhase_OPERATION_PHASE_RECOVERING); err != nil {
		return accessv1.Disposition_DISPOSITION_UNSPECIFIED, err
	}

	m.mu.Lock()
	obs := m.lastObservation
	m.mu.Unlock()
	if obs == nil {
		return accessv1.Disposition_DISPOSITION_UNSPECIFIED, errs.New().Code(ErrCodeOutOfOrder).
			Msg("compare called before observe")
	}

	if cached != nil {
		if conflict, field := interfaces.ConflictingReads(cached, obs); conflict {
			if err := m.block(ctx, accessv1.BlockReason_BLOCK_REASON_CONFLICTING_READS); err != nil {
				return accessv1.Disposition_DISPOSITION_UNSPECIFIED, err
			}
			return accessv1.Disposition_DISPOSITION_UNSPECIFIED, errs.New().Code(ErrCodeConflictingReads).
				Attr("field", field).
				Msg("two complete reads disagree; no observation carries authority")
		}
	}

	mutationIntent := m.req.GetMutation()
	if mutationIntent == nil {
		return accessv1.Disposition_DISPOSITION_UNSPECIFIED, nil
	}

	if obs.GetCompleteness() == accessv1.Completeness_COMPLETENESS_COMPLETE &&
		interfaces.DescriptionApplied(obs, mutationIntent.GetInterfaceDescription()) {
		return accessv1.Disposition_DISPOSITION_VERIFIED, nil
	}

	return accessv1.Disposition_DISPOSITION_UNSPECIFIED, nil
}

// MarkVerified transitions to VERIFIED and records
// BLOCK_REASON_UNACKNOWLEDGED, per the device/access/v1 README's
// walkthrough: "phase VERIFIED, with block_reason: UNACKNOWLEDGED until
// central holds the result." Valid from OBSERVING or RECOVERING, matching
// [Machine.Compare]'s doc for why a recovery retry's verification is found
// at RECOVERING rather than OBSERVING.
func (m *Machine) MarkVerified(ctx context.Context) error {
	if err := m.requireAnyPhase("MarkVerified", accessv1.OperationPhase_OPERATION_PHASE_OBSERVING, accessv1.OperationPhase_OPERATION_PHASE_RECOVERING); err != nil {
		return err
	}
	if err := m.transition(ctx, accessv1.OperationPhase_OPERATION_PHASE_VERIFIED); err != nil {
		return err
	}

	// disposition is not set here: MutationState.disposition_matches_phase
	// requires it set only while the phase is ACKNOWLEDGED, RELEASED, or
	// ABANDONED, and VERIFIED is none of those. [Machine.Acknowledge] sets
	// it from central's own ack once the phase reaches ACKNOWLEDGED.
	return m.block(ctx, accessv1.BlockReason_BLOCK_REASON_UNACKNOWLEDGED)
}

// EnterRecovering transitions to RECOVERING and blocks the lane with
// BLOCK_REASON_INDETERMINATE: decision 5's ambiguity-stays-indeterminate
// rule for a mutation whose effect could not yet be established.
func (m *Machine) EnterRecovering(ctx context.Context) error {
	// Valid from POSSIBLY_APPLIED (a submit or checkpoint step failed
	// ambiguously before any observation), OBSERVING (the observation
	// itself failed — the unreachable-device case recovery exists for),
	// or RECOVERING (a caller may call this again; it is idempotent).
	if err := m.requireAnyPhase("EnterRecovering",
		accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED,
		accessv1.OperationPhase_OPERATION_PHASE_OBSERVING,
		accessv1.OperationPhase_OPERATION_PHASE_RECOVERING,
	); err != nil {
		return err
	}
	if err := m.transition(ctx, accessv1.OperationPhase_OPERATION_PHASE_RECOVERING); err != nil {
		return err
	}
	if err := m.block(ctx, accessv1.BlockReason_BLOCK_REASON_INDETERMINATE); err != nil {
		return err
	}

	m.deps.Telemetry.RecoveryStarted(ctx)
	event := audit.BuildRecoveryStarted(m.deps.Clock, m.common(ctx))
	return m.deps.Audit.Emit(ctx, event)
}

// Abandon ends recovery in ABANDONED with disposition
// INDETERMINATE_ABANDONED and BLOCK_REASON_RECOVERY_HOLD, per decision 5:
// an authorized cancellation or a qualified timeout abandons a mutation
// whose effect recovery could not establish, and the hold that follows is
// resolved only by an explicit call outside this package (an operator
// decision or a reconciliation intent), never by a retry. Valid from
// RECOVERING (the ordinary case) or OBSERVING: a recovery re-observation
// that fails to durably return to RECOVERING (its own second audit
// delivery failed) must still be abandonable once the horizon elapses, or
// that state has no exit at all.
func (m *Machine) Abandon(ctx context.Context) error {
	if err := m.requireAnyPhase("Abandon",
		accessv1.OperationPhase_OPERATION_PHASE_RECOVERING,
		accessv1.OperationPhase_OPERATION_PHASE_OBSERVING,
	); err != nil {
		return err
	}

	m.mu.Lock()
	from := m.phase
	m.mu.Unlock()

	event := audit.BuildPhaseTransitioned(m.deps.Clock, m.common(ctx), from, accessv1.OperationPhase_OPERATION_PHASE_ABANDONED)
	if err := m.deps.Audit.Emit(ctx, event); err != nil {
		return errs.Wrap(err, "deliver phase transitioned event")
	}

	m.mu.Lock()
	m.phase = accessv1.OperationPhase_OPERATION_PHASE_ABANDONED
	m.disposition = accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED
	m.mu.Unlock()

	// Routed through block, not set directly: the recovery hold is the one
	// block reason that requires an operator decision, and needs the same
	// audit record and telemetry event every other block reason gets.
	return m.block(ctx, accessv1.BlockReason_BLOCK_REASON_RECOVERY_HOLD)
}

// Acknowledge records central's TerminalResultAck, moves the phase to
// ACKNOWLEDGED, then to RELEASED — decision 4's barrier: only a durable
// terminal disposition central acknowledges back frees the device's lane
// for the next sequence.
func (m *Machine) Acknowledge(ctx context.Context, ack *integrationv1.TerminalResultAck) error {
	if ack.GetSequence() != m.req.GetSequence() {
		return errs.New().Code(ErrCodeOutOfOrder).
			Attr("ack_sequence", ack.GetSequence()).
			Attr("admitted_sequence", m.req.GetSequence()).
			Msg("acknowledgement sequence does not match the admitted request")
	}
	if ack.GetDisposition() == accessv1.Disposition_DISPOSITION_UNSPECIFIED {
		return errs.New().Code(ErrCodeDispositionUnspecified).
			Msg("acknowledgement disposition is unspecified; MutationState rejects the zero value at a terminal phase")
	}
	if err := m.requirePhase(accessv1.OperationPhase_OPERATION_PHASE_VERIFIED, "Acknowledge"); err != nil {
		return err
	}

	if err := m.transition(ctx, accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED); err != nil {
		return err
	}

	m.mu.Lock()
	m.disposition = ack.GetDisposition()
	m.mu.Unlock()

	return m.release(ctx)
}

// release delivers both the PhaseTransitioned and LaneReleased audit events
// before reporting RELEASED, so a Deliverer failure on either leaves
// Phase() at ACKNOWLEDGED, and clears the block: RELEASED means the
// device's lane is free for the next mutation, so
// MutationState.blocked_since_matches_reason requires both block_reason
// and blocked_since unset from here on.
func (m *Machine) release(ctx context.Context) error {
	m.mu.Lock()
	from := m.phase
	m.mu.Unlock()

	phaseEvent := audit.BuildPhaseTransitioned(m.deps.Clock, m.common(ctx), from, accessv1.OperationPhase_OPERATION_PHASE_RELEASED)
	if err := m.deps.Audit.Emit(ctx, phaseEvent); err != nil {
		return errs.Wrap(err, "deliver phase transitioned event")
	}

	releasedEvent := audit.BuildLaneReleased(m.deps.Clock, m.common(ctx))
	if err := m.deps.Audit.Emit(ctx, releasedEvent); err != nil {
		return errs.Wrap(err, "deliver lane released event")
	}

	m.mu.Lock()
	m.phase = accessv1.OperationPhase_OPERATION_PHASE_RELEASED
	m.blockReason = accessv1.BlockReason_BLOCK_REASON_UNSPECIFIED
	m.blockedSince = time.Time{}
	m.mu.Unlock()

	m.deps.Telemetry.LaneReleased(ctx)
	return nil
}

// Result builds this mutation's current ExecuteResult: the sequence, the
// phase reached, and exactly one outcome arm, matching the wire message's
// required oneof. Pass the error a caller's own operation failed with, or
// nil for a call made after a successful Observe; err always wins the
// oneof when non-nil. A nil err with no observation yet recorded (Result
// called before Observe ever ran) still cannot leave the oneof unset, so it
// falls back to reporting that absence as the error arm rather than
// emitting an invalid message.
func (m *Machine) Result(err error) *integrationv1.ExecuteResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := &integrationv1.ExecuteResult{}
	result.SetSequence(m.req.GetSequence())
	result.SetPhaseReached(m.phase)

	switch {
	case err != nil:
		result.SetError(errs.EncodeForClient(err))
	case m.lastObservation != nil:
		result.SetObservation(m.lastObservation)
	default:
		result.SetError(errs.EncodeForClient(errs.New().Msg("no observation was recorded for this operation")))
	}
	return result
}
