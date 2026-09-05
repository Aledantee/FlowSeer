package mutation

import (
	"context"
	"sync"
	"time"

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
	ErrCodeFirmwareEpoch    = errs.NewCode("mutation/firmware-epoch")
	ErrCodeOutOfOrder       = errs.NewCode("mutation/out-of-order")
	ErrCodeRevoked          = errs.NewCode("mutation/revoked")
	ErrCodeConflictingReads = errs.NewCode("mutation/conflicting-reads")
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
	// DeviceID names the device for audit events built from this Machine.
	DeviceID string

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
	lastObservation *accessv1.InterfaceObservation
}

// Admitted constructs a Machine in phase ADMITTED for req, which already
// carries the central-assigned sequence (execution.proto's field comment:
// "The device's lane sequence this operation was admitted at"). For a
// mutation whose intent's expected firmware fingerprint differs from
// deps.CurrentFingerprint, it returns an error instead of a Machine — the
// mutation never reaches ADMITTED under a stale epoch, per decision 7. A
// read never carries an expected fingerprint and is never blocked here.
func Admitted(req *integrationv1.ExecuteRequest, deps Deps) (*Machine, error) {
	if mutationIntent := req.GetMutation(); mutationIntent != nil {
		if mutationIntent.GetExpectedFirmwareFingerprint() != deps.CurrentFingerprint {
			return nil, errs.New().Code(ErrCodeFirmwareEpoch).
				Attr("expected_fingerprint", mutationIntent.GetExpectedFirmwareFingerprint()).
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

func (m *Machine) common() audit.Common {
	return audit.Common{
		Device:   audit.Device{DeviceID: m.deps.DeviceID},
		Sequence: m.req.GetSequence(),
	}
}

func (m *Machine) requirePhase(want accessv1.OperationPhase, method string) error {
	if got := m.Phase(); got != want {
		return errs.New().Code(ErrCodeOutOfOrder).
			Attr("method", method).
			Attr("phase", got.String()).
			Attr("required_phase", want.String()).
			Msgf("%s is only valid from %s", method, want)
	}
	return nil
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

	event := audit.BuildPhaseTransitioned(m.deps.Clock, m.common(), from, to)
	if err := m.deps.Audit.Emit(ctx, event); err != nil {
		return errs.Wrap(err, "deliver phase transitioned event")
	}

	m.mu.Lock()
	m.phase = to
	m.mu.Unlock()

	return nil
}

// block delivers a LaneBlocked audit event and records the block reason.
// It does not itself change Phase().
func (m *Machine) block(ctx context.Context, reason accessv1.BlockReason) error {
	event := audit.BuildLaneBlocked(m.deps.Clock, m.common(), reason)
	if err := m.deps.Audit.Emit(ctx, event); err != nil {
		return errs.Wrap(err, "deliver lane blocked event")
	}

	m.mu.Lock()
	m.blockReason = reason
	m.mu.Unlock()

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

// latestPulseRevoked drains every pulse already queued on updates without
// blocking and reports whether the most recent one revoked submission
// authority. No pulse queued yet means authority is still whatever the
// grant conferred, so it reports false.
func latestPulseRevoked(updates <-chan credential.SubmissionUpdate) bool {
	revoked := false
	for {
		select {
		case u, ok := <-updates:
			if !ok {
				return revoked
			}
			if u.Pulse != nil {
				revoked = u.Pulse.GetAuthority() == edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_REVOKED
			}
		default:
			return revoked
		}
	}
}

// Execute submits the mutation's command, checking submission authority
// immediately before doing so, per decision 9. For a read it is a no-op:
// integration/device/v1's README states a read's phase_reached is
// OBSERVING, so a read's device contact happens in [Machine.Observe], not
// here.
func (m *Machine) Execute(ctx context.Context) error {
	mutationIntent := m.req.GetMutation()
	if mutationIntent == nil {
		return nil
	}
	if err := m.requirePhase(accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED, "Execute"); err != nil {
		return err
	}

	if err := m.deps.Freeze.AwaitSideEffect(ctx); err != nil {
		return errs.Wrap(err, "await control-plane freeze")
	}

	_, updates, err := m.deps.Submission.Open(ctx, "", "", m.req.GetSequence())
	if err != nil {
		return errs.Wrap(err, "open device submission")
	}

	if latestPulseRevoked(updates) {
		return errs.New().Code(ErrCodeRevoked).
			Msg("submission authority was revoked before the command was sent")
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

	obs, err := m.deps.Read(ctx)
	if err != nil {
		return nil, errs.Wrap(err, "observe")
	}

	m.mu.Lock()
	m.lastObservation = obs
	m.mu.Unlock()

	return obs, nil
}

// Compare reports the mutation's disposition against the last call to
// [Machine.Observe]. cached, if non-nil, is an earlier complete observation
// of the same target this mutation must not silently override: if it
// conflicts with the fresh observation on any compared field, no
// observation carries authority (decision-record requirement) and Compare
// blocks with BLOCK_REASON_CONFLICTING_READS instead of reporting a
// disposition. A read has no intent to compare against and always reports
// DISPOSITION_UNSPECIFIED with a nil error: its own observation is the
// result.
func (m *Machine) Compare(ctx context.Context, cached *accessv1.InterfaceObservation) (accessv1.Disposition, error) {
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

// MarkVerified transitions to VERIFIED and records the VERIFIED disposition
// and BLOCK_REASON_UNACKNOWLEDGED, per the device/access/v1 README's
// walkthrough: "phase VERIFIED, with block_reason: UNACKNOWLEDGED until
// central holds the result."
func (m *Machine) MarkVerified(ctx context.Context) error {
	if err := m.transition(ctx, accessv1.OperationPhase_OPERATION_PHASE_VERIFIED); err != nil {
		return err
	}

	m.mu.Lock()
	m.disposition = accessv1.Disposition_DISPOSITION_VERIFIED
	m.mu.Unlock()

	return m.block(ctx, accessv1.BlockReason_BLOCK_REASON_UNACKNOWLEDGED)
}

// EnterRecovering transitions to RECOVERING and blocks the lane with
// BLOCK_REASON_INDETERMINATE: decision 5's ambiguity-stays-indeterminate
// rule for a mutation whose effect could not yet be established.
func (m *Machine) EnterRecovering(ctx context.Context) error {
	if err := m.transition(ctx, accessv1.OperationPhase_OPERATION_PHASE_RECOVERING); err != nil {
		return err
	}
	if err := m.block(ctx, accessv1.BlockReason_BLOCK_REASON_INDETERMINATE); err != nil {
		return err
	}

	m.deps.Telemetry.RecoveryStarted(ctx)
	event := audit.BuildRecoveryStarted(m.deps.Clock, m.common())
	return m.deps.Audit.Emit(ctx, event)
}

// Abandon ends recovery in ABANDONED with disposition
// INDETERMINATE_ABANDONED and BLOCK_REASON_RECOVERY_HOLD, per decision 5:
// an authorized cancellation or a qualified timeout abandons a mutation
// whose effect recovery could not establish, and the hold that follows is
// resolved only by an explicit call outside this package (an operator
// decision or a reconciliation intent), never by a retry.
func (m *Machine) Abandon(ctx context.Context) error {
	m.mu.Lock()
	from := m.phase
	m.mu.Unlock()

	event := audit.BuildPhaseTransitioned(m.deps.Clock, m.common(), from, accessv1.OperationPhase_OPERATION_PHASE_ABANDONED)
	if err := m.deps.Audit.Emit(ctx, event); err != nil {
		return errs.Wrap(err, "deliver phase transitioned event")
	}

	m.mu.Lock()
	m.phase = accessv1.OperationPhase_OPERATION_PHASE_ABANDONED
	m.disposition = accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED
	m.blockReason = accessv1.BlockReason_BLOCK_REASON_RECOVERY_HOLD
	m.mu.Unlock()

	return nil
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
// Phase() at ACKNOWLEDGED — requirement 15 and 17's ordering.
func (m *Machine) release(ctx context.Context) error {
	m.mu.Lock()
	from := m.phase
	m.mu.Unlock()

	phaseEvent := audit.BuildPhaseTransitioned(m.deps.Clock, m.common(), from, accessv1.OperationPhase_OPERATION_PHASE_RELEASED)
	if err := m.deps.Audit.Emit(ctx, phaseEvent); err != nil {
		return errs.Wrap(err, "deliver phase transitioned event")
	}

	releasedEvent := audit.BuildLaneReleased(m.deps.Clock, m.common())
	if err := m.deps.Audit.Emit(ctx, releasedEvent); err != nil {
		return errs.Wrap(err, "deliver lane released event")
	}

	m.mu.Lock()
	m.phase = accessv1.OperationPhase_OPERATION_PHASE_RELEASED
	m.mu.Unlock()

	m.deps.Telemetry.LaneReleased(ctx)
	return nil
}

// Result builds this mutation's current ExecuteResult: the sequence, the
// phase reached, and either the last observation or, for a caller that
// tracks a failure separately, none. It may be called at any phase; a
// caller building the wire result after Observe/Compare has already run
// gets a populated Observation.
func (m *Machine) Result() *integrationv1.ExecuteResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := &integrationv1.ExecuteResult{}
	result.SetSequence(m.req.GetSequence())
	result.SetPhaseReached(m.phase)
	if m.lastObservation != nil {
		result.SetObservation(m.lastObservation)
	}
	return result
}
