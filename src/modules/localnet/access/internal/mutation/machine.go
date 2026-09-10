package mutation

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
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
	// ErrCodeAlreadyTerminal answers an acknowledgement for a mutation that
	// has already ended. Its prefix names the module boundary central sees
	// rather than the package that produces it: this code travels back over
	// the dispatch stream as part of the refusal contract, so it belongs to
	// the same namespace as the lane's own codes, and [access] re-exports it
	// under that name.
	ErrCodeAlreadyTerminal = errs.NewCode("access/already-terminal")
)

// Deps bundles this package's dependencies. Read and Submit are
// already bound to whatever route a caller resolved (an explicit pin or
// interfaces.SelectRoute's own fallback) and whatever session that route
// needs; Machine calls them and faithfully propagates their errors without
// retrying or falling back on its own. Construct with keyed fields.
type Deps struct {
	// CurrentFingerprint is the device's firmware fingerprint as last
	// learned by an identity probe. [Admitted] blocks a mutation whose
	// intent names a different one: an intent written against one firmware
	// is not an intent against another.
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

	Read func(ctx context.Context) (*accessv1.InterfaceObservation, error)
	// Submit sends the command over a session the caller opens from the
	// grant's own material. The grant is passed rather than captured
	// because it is acquired inside Execute, immediately before the command
	// and after the authority check, and is one-use.
	Submit func(ctx context.Context, grant *edgev1.SubmissionGrant, intent *accessv1.InterfaceDescriptionChange) error

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

	// done is closed exactly once, when the mutation reaches a terminal
	// phase. Every rest point selects on it, so an acknowledgement applied
	// by central's own goroutine releases a mutation parked anywhere.
	done chan struct{}

	mu              sync.Mutex
	phase           accessv1.OperationPhase
	disposition     accessv1.Disposition
	blockReason     accessv1.BlockReason
	blockedSince    time.Time
	lastObservation *accessv1.InterfaceObservation
	terminated      bool

	// submitted and canceled are the two halves of a one-winner race
	// between "central released this mutation REJECTED" and "the command
	// reached the device". Each is set only while the other is false, both
	// under mu, so exactly one of them can ever be true: a REJECTED
	// acknowledgement either arrives in time to stop the command or is
	// refused. Nothing clears either one.
	submitted bool
	canceled  bool
	// abandoning records that central accepted an abandonment. The lane
	// reads it while completing the submission, so a trailing audit failure
	// cannot release a hold during an abandonment. Verification needs no
	// such field: verifiedLocked derives it from the phase and disposition.
	abandoning bool
	// inRecovery is set while this mutation is being polled by recovery,
	// and is what makes an observation record-free. It is not the same as
	// "phase is RECOVERING": a retry moves the phase to POSSIBLY_APPLIED
	// and the mutation is still in recovery, and that is exactly the window
	// where a phase-based test would start emitting a record per poll
	// again.
	inRecovery bool
	// owed are the audit records this mutation could not deliver, kept as
	// the values that were built so a retry carries the same event id and
	// the stream reads it as a duplicate rather than a second fact.
	owed []*eventv1.DeviceOperationEvent
	// cancelWaits cancels the context [Machine.Execute] runs its waits
	// under, and is nil whenever Execute is not parked in one.
	cancelWaits context.CancelFunc
}

// Admitted constructs a Machine in phase ADMITTED for req, which already
// carries the central-assigned sequence (execution.proto's field comment:
// "The device's lane sequence this operation was admitted at"). For a
// mutation whose intent's expected firmware fingerprint differs from
// deps.CurrentFingerprint, it returns an error instead of a Machine — the
// mutation never reaches ADMITTED under a stale epoch. A
// read never carries an expected fingerprint and is never blocked here.
// This check alone never emits flowseer.device.firmware.epoch_changed. It
// compares central's expectation against the edge's cached fingerprint, and
// those can differ simply because central is stale. The real epoch signal
// comes from access.Lane's own re-probe, which compares one probe's output
// against an earlier probe's; see the access module README's "Firmware
// epoch" section.
func Admitted(req *integrationv1.ExecuteRequest, deps Deps) (*Machine, error) {
	if mutationIntent := req.GetMutation(); mutationIntent != nil {
		if expected := mutationIntent.GetExpectedFirmwareFingerprint(); expected != deps.CurrentFingerprint {
			// No FirmwareEpochChanged event here: this comparison is
			// against central's own expectation, which can differ from
			// the edge's cached fingerprint simply because central is
			// stale, not because the device's firmware actually changed.
			// A real epoch-change signal needs a fresh probe at observation
			// time compared against the earlier probe's own output. Lane owns
			// that comparison because this state machine has no session
			// factory, so emitting here would durably record
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
		done:  make(chan struct{}),
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

// Sequence is central's lane sequence for this operation, fixed at
// admission. Safe to read without the lock precisely because it never
// changes, which is what lets a caller match an incoming acknowledgement
// against this mutation before taking any of its locks.
func (m *Machine) Sequence() uint64 { return m.req.GetSequence() }

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

// Done is closed when this mutation reaches a terminal phase, whichever
// goroutine takes it there. A step that parks — the checkpoint wait, the
// freeze wait and grant open inside [Machine.Execute], the wait for
// central's acknowledgement — selects on it, so an acknowledgement applied
// on central's own goroutine releases the parked one instead of waiting for
// a deadline to expire.
func (m *Machine) Done() <-chan struct{} { return m.done }

// IsTerminal reports whether this mutation has ended, in RELEASED or
// ABANDONED.
func (m *Machine) IsTerminal() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.terminalLocked()
}

func (m *Machine) terminalLocked() bool {
	return m.phase == accessv1.OperationPhase_OPERATION_PHASE_RELEASED ||
		m.phase == accessv1.OperationPhase_OPERATION_PHASE_ABANDONED
}

// Submitted reports whether the command was handed to the device. It is
// what an [integrationv1.ExecuteResult] carries as submitted, and what
// decides whether an error means "provably nothing was sent" (central may
// dispose it REJECTED) or "the effect is unknown" (it must not).
func (m *Machine) Submitted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.submitted
}

// Verified reports whether this mutation's effect was established: the
// observation showed the change applied, on the ordinary path or on a
// recovery poll. It stays true once the mutation is released, which is what
// lets a caller tell a mutation that ended knowing what happened from one
// that ended not knowing.
func (m *Machine) Verified() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.verifiedLocked()
}

// Abandoning reports whether an accepted acknowledgement chose abandonment,
// including while the terminal transition is still being delivered.
func (m *Machine) Abandoning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.abandoning
}

func (m *Machine) verifiedLocked() bool {
	return m.phase == accessv1.OperationPhase_OPERATION_PHASE_VERIFIED ||
		m.disposition == accessv1.Disposition_DISPOSITION_VERIFIED
}

// Canceled reports whether an accepted REJECTED acknowledgement stopped
// this mutation before its command was sent. It stays true even when the
// acknowledgement's own audit delivery failed part-way and the mutation is
// therefore not yet terminal, which is the state that tells a caller to
// wait for central's re-send rather than to treat the step error as its
// own.
func (m *Machine) Canceled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.canceled
}

// closeDoneLocked marks the mutation terminal and releases everything
// parked on Done. The caller must hold mu. Idempotent: the terminal phase
// is written by more than one path, and a second close would panic.
func (m *Machine) closeDoneLocked() {
	if m.terminated {
		return
	}
	m.terminated = true
	close(m.done)
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
// value. The record is delivered before the state it describes is
// published, at every transition and not only at release, so a phase this
// process reports is one the durable account already carries.
// commitPhaseLocked writes the phase only if it is still the one the caller
// read before it released mu. The caller must hold mu.
//
// Every phase move here is check-then-act across an unlocked audit delivery:
// the phase is read, mu is released for the Emit, and mu is retaken to write.
// A door call on central's goroutine — an acknowledgement, an abandonment —
// can land in that window and move the phase to a terminal one. Writing
// unconditionally then puts an open phase back over a terminal one, which
// produces a state MutationState.disposition_matches_phase forbids and lets
// a poll go on driving a mutation central has already ended.
//
// A false return means the move is stale and the caller must not proceed as
// though it happened. The audit record has already been delivered by then;
// that is correct, because it records an attempt that really was made.
func (m *Machine) commitPhaseLocked(from, to accessv1.OperationPhase) bool {
	if m.phase != from {
		return false
	}
	m.phase = to
	return true
}

// staleTransition reports a phase move that was overtaken while its record
// was being delivered.
func (m *Machine) staleTransition() error {
	return errs.New().Code(ErrCodeAlreadyTerminal).
		Attr("phase", m.Phase().String()).
		Msg("mutation moved on while this transition was being recorded")
}

func (m *Machine) transition(ctx context.Context, to accessv1.OperationPhase) error {
	m.mu.Lock()
	from := m.phase
	m.mu.Unlock()

	event := audit.BuildPhaseTransitioned(m.deps.Clock, m.common(ctx), from, to)
	if err := m.deps.Audit.Emit(ctx, event); err != nil {
		return errs.Wrap(err, "deliver phase transitioned event")
	}

	m.mu.Lock()
	committed := m.commitPhaseLocked(from, to)
	m.mu.Unlock()
	if !committed {
		return m.staleTransition()
	}

	return nil
}

// transitionWithDisposition is [Machine.transition] for a move that also
// settles the mutation's outcome. Phase and disposition are written in one
// critical section because MutationState.disposition_matches_phase is an
// invariant at every instant a reader can observe, not merely at rest: a
// concurrent Disposition() call must never find ACKNOWLEDGED paired with
// DISPOSITION_UNSPECIFIED, which is exactly what two separate writes under
// two separate locks would expose.
func (m *Machine) transitionWithDisposition(ctx context.Context, to accessv1.OperationPhase, disposition accessv1.Disposition) error {
	m.mu.Lock()
	from := m.phase
	m.mu.Unlock()

	event := audit.BuildPhaseTransitioned(m.deps.Clock, m.common(ctx), from, to)
	if err := m.deps.Audit.Emit(ctx, event); err != nil {
		return errs.Wrap(err, "deliver phase transitioned event")
	}

	m.mu.Lock()
	committed := m.commitPhaseLocked(from, to)
	if committed {
		m.disposition = disposition
	}
	m.mu.Unlock()
	if !committed {
		return m.staleTransition()
	}

	return nil
}

// block records reason and when it began, matching
// MutationState.blocked_since_matches_reason, then delivers a LaneBlocked
// audit event and telemetry. It does not itself change Phase(). The state
// write happens before the audit attempt, not after: a caller (recovery's
// Runner, deciding whether to engage a Hold) must see the block took
// effect even when the accompanying audit record's delivery fails, rather
// than an undelivered notification silently leaving the machine unblocked.
func (m *Machine) block(ctx context.Context, reason accessv1.BlockReason) error {
	m.mu.Lock()
	m.blockReason = reason
	m.blockedSince = m.deps.Clock()
	m.mu.Unlock()

	event := audit.BuildLaneBlocked(m.deps.Clock, m.common(ctx), reason)
	if err := m.deps.Audit.Emit(ctx, event); err != nil {
		m.retainOwed(event)
		return errs.Wrap(err, "deliver lane blocked event")
	}

	m.deps.Telemetry.LaneBlocked(ctx, reason)
	return nil
}

// Checkpoint acknowledges central's CheckpointRequest for this mutation's
// sequence and moves the phase to POSSIBLY_APPLIED: central durably
// records that the intent may reach the device before any command is
// submitted. It is invalid for a read, which never checkpoints.
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
// SUBMISSION_AUTHORITY_AUTHORIZED immediately before doing so — not merely
// the absence of a seen revocation, which a
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

	// Every wait below runs under waitCtx rather than the caller's own
	// context, so an accepted REJECTED acknowledgement can end them the
	// moment it is applied instead of leaving this mutation parked until
	// the submitter's deadline. Publishing the cancel under mu, and
	// clearing it on the way out, is what lets Acknowledge reach a wait
	// that is running right now without reaching one that is not.
	waitCtx, cancelWaits := context.WithCancel(ctx)
	defer cancelWaits()

	m.mu.Lock()
	if m.canceled {
		m.mu.Unlock()
		return errs.New().Code(ErrCodeOutOfOrder).
			Msg("mutation was released REJECTED before execution began; the command is not sent")
	}
	m.cancelWaits = cancelWaits
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.cancelWaits = nil
		m.mu.Unlock()
	}()

	// Held for the rest of this call, not just the check: Freeze must not
	// return while this device write is still in flight, and a plain
	// AwaitSideEffect only proves no freeze was active at the instant it
	// returned. Gate.Enter's own barrier RLock is not cancellable, so a
	// cancel delivered while this call is parked in it takes effect when it
	// unparks — which is early enough, because the latch below is the only
	// thing standing between here and the command going out.
	leave, err := m.deps.Freeze.Enter(waitCtx)
	if err != nil {
		return errs.Wrap(err, "await control-plane freeze")
	}
	defer leave()

	handle, err := m.deps.Submission.Open(waitCtx, m.deps.DeviceID, m.deps.BindingID, m.req.GetSequence())
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
	// honored: the command is never sent. Once Submit is called, the
	// ambiguity rule takes over — a lost connection during or after
	// submission does not fail the mutation; Observe and recovery handle
	// that ambiguity instead.
	if err := waitCtx.Err(); err != nil {
		return errs.Wrap(err, "context ended before submission")
	}

	// The latch. Taking canceled and setting submitted in one critical
	// section is what makes "released REJECTED" and "command sent" mutually
	// exclusive rather than merely unlikely: Acknowledge sets canceled only
	// while submitted is false under this same lock, so whichever of the two
	// reaches mu first wins outright and the other is refused. Checking
	// waitCtx above is not a substitute — the cancel it observes is
	// delivered asynchronously, and a mutation could pass that check and
	// still be canceled before reaching here.
	m.mu.Lock()
	if m.canceled {
		m.mu.Unlock()
		return errs.New().Code(ErrCodeOutOfOrder).
			Msg("mutation was released REJECTED before the command was sent")
	}
	m.submitted = true
	m.mu.Unlock()

	if err := m.deps.Submit(waitCtx, handle.Grant(), mutationIntent.GetInterfaceDescription()); err != nil {
		// The latch is released only for a failure the adapter can prove
		// changed nothing. Everything else keeps it, which is the
		// conservative default and the one a transport that may have
		// delivered deserves: an adapter that grows a refusal path and does
		// not mark it lands on "the effect is unknown" rather than on a
		// device reported untouched that may not be.
		//
		// This does not make the latch two-sided. Setting it before the call
		// is what makes "released REJECTED" and "command sent" mutually
		// exclusive, and clearing it here can lose a race with an Acknowledge
		// that already read it as true and declined to cancel. That is
		// harmless — the mutation ends rejected either way — but a reader of
		// the latch's own comment should not expect a symmetry that is not
		// there.
		if code, ok := errs.CodeOf(err); ok && code == interfaces.ErrCodeNotSubmitted {
			m.mu.Lock()
			m.submitted = false
			m.mu.Unlock()
		}
		return errs.Wrap(err, "submit mutation")
	}

	return nil
}

// Observe reads the affected state over a fresh session: a
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

	m.mu.Lock()
	recovering := m.inRecovery
	m.mu.Unlock()

	if !recovering {
		if err := m.transition(ctx, accessv1.OperationPhase_OPERATION_PHASE_OBSERVING); err != nil {
			return nil, err
		}
	}

	obs, readErr := m.deps.Read(ctx)

	if recovering {
		// A recovery observation emits no audit record and leaves the
		// mutation at RECOVERING, which is where it rests between polls.
		//
		// No record, because recovery polls every RecoveryPollInterval for
		// up to the whole horizon: recording the OBSERVING and RECOVERING
		// transitions would put two records per poll into a durable stream
		// whose readers are asking what happened to the device, and bury
		// the RecoveryStarted and the terminal record that answer them
		// under hundreds that do not. The phase is set directly here for
		// the same reason — there is no record for it to follow.
		//
		// Set rather than left alone, because a retry moves the phase to
		// POSSIBLY_APPLIED and the next poll's Observe must still leave the
		// mutation somewhere a later Observe and Compare can be called
		// from. Leaving it stuck at OBSERVING on a read error would wedge
		// the mutation, since OBSERVING is not a valid source phase for
		// either.
		// Only from a phase this poll is still entitled to move. An
		// acknowledgement that ended the mutation while the read was in
		// flight has already written a terminal phase and a disposition;
		// putting RECOVERING back over it would pair the two in a way the
		// schema forbids and let this poll go on driving a mutation central
		// has ended.
		m.mu.Lock()
		if !m.terminated && !m.terminalLocked() {
			m.phase = accessv1.OperationPhase_OPERATION_PHASE_RECOVERING
		}
		m.mu.Unlock()
	}

	if readErr != nil {
		return nil, errs.Wrap(readErr, "observe")
	}

	// No firmware-epoch re-check here, deliberately, and not because there
	// should not be one: access.Lane runs it, around this call. The check
	// cannot live here, because the only fingerprints this type holds are
	// deps.CurrentFingerprint (a probe digest) and the observation's own
	// provenance fingerprint (whatever the host supplied to
	// interfaces.Read). Those come from unrelated sources with no defined
	// relationship, and comparing them blocked every mutation on the
	// documented production path. A real check needs two probes, and only
	// the Lane has a session factory to take the second one with.

	m.mu.Lock()
	m.lastObservation = obs
	m.mu.Unlock()

	return obs, nil
}

// Peek reads the device without touching the mutation's phase, emitting a
// record, or recording the observation as the one Compare will judge.
//
// It is the pre-mutation baseline: what the device held before the command
// went out, which recovery corroborates against — two later observations
// matching it say the command did not land. Deliberately not Observe. An
// observation is a phase the mutation moves through and a record in the
// audit stream; this is a read taken while the mutation is still at
// ADMITTED, and routing it through Observe both refuses (ADMITTED is not a
// valid source phase for a mutation) and, if it did not, would move the
// phase out from under Checkpoint.
func (m *Machine) Peek(ctx context.Context) (*accessv1.InterfaceObservation, error) {
	return m.deps.Read(ctx)
}

// Compare reports the mutation's disposition against the last call to
// [Machine.Observe]. Valid from OBSERVING (the ordinary path) or RECOVERING
// (Observe's own re-observation transitions back to RECOVERING, per its doc
// comment, so that is where a recovery retry's Compare call finds the
// mutation — OperationPhase's own doc allows VERIFIED from either).
// cached, if non-nil, is an earlier complete observation of the same target
// this mutation must not silently override: if it conflicts with the fresh
// observation on any compared field, no observation carries authority and
// Compare blocks with BLOCK_REASON_CONFLICTING_READS instead of reporting a
// disposition. Lane currently passes nil at every production call site: its
// pre-mutation read is a recovery baseline, not a reading of the intended new
// state that the post-mutation observation must agree with. A read has no
// intent to compare against and always reports DISPOSITION_UNSPECIFIED with a
// nil error: its own observation is the result.
func (m *Machine) Compare(ctx context.Context, cached *accessv1.InterfaceObservation) (accessv1.Disposition, error) {
	if err := m.requireAnyPhase("Compare",
		accessv1.OperationPhase_OPERATION_PHASE_OBSERVING,
		accessv1.OperationPhase_OPERATION_PHASE_RECOVERING,
	); err != nil {
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

	m.mu.Lock()
	m.inRecovery = false
	m.mu.Unlock()

	// disposition is not set here: MutationState.disposition_matches_phase
	// requires it set only while the phase is ACKNOWLEDGED, RELEASED, or
	// ABANDONED, and VERIFIED is none of those. [Machine.Acknowledge] sets
	// it from central's own ack once the phase reaches ACKNOWLEDGED.
	return m.block(ctx, accessv1.BlockReason_BLOCK_REASON_UNACKNOWLEDGED)
}

// EnterRecovering transitions to RECOVERING and blocks the lane with
// BLOCK_REASON_INDETERMINATE: an ambiguous outcome stays indeterminate
// rather than being resolved by guess for a mutation whose effect could not yet be established.
func (m *Machine) EnterRecovering(ctx context.Context) error {
	// Already in recovery: nothing to record. A second call is a caller
	// retrying, not a second entry, and emitting the transition, the block
	// and the RecoveryStarted again would put three fresh event ids in the
	// stream as new facts, and move blocked_since to a moment the block did
	// not begin.
	m.mu.Lock()
	already := m.inRecovery
	m.mu.Unlock()
	if already {
		return nil
	}

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
	// The phase moves only if its own record was delivered, which is
	// the audit-before-state rule, and is why a failure here leaves nothing written:
	// the mutation is not in recovery and a caller must not treat it as if
	// it were.
	if err := m.transition(ctx, accessv1.OperationPhase_OPERATION_PHASE_RECOVERING); err != nil {
		return err
	}

	// Past here the phase is RECOVERING, so the mutation is in recovery
	// whatever the audit stream has heard. Set before the deliveries below
	// rather than after them: inRecovery describes the machine, and a
	// machine at RECOVERING whose flag says otherwise would have Observe
	// trying to transition into OBSERVING from a phase it is already past.
	m.mu.Lock()
	m.inRecovery = true
	m.mu.Unlock()

	// Both remaining records are attempted, and a failure retains rather
	// than abandons. block writes its state before delivering, so the block
	// is real either way; returning here instead would leave a mutation that
	// is blocked, in recovery, and has nobody scheduled to look at it — see
	// the caller.
	var owed error
	if err := m.block(ctx, accessv1.BlockReason_BLOCK_REASON_INDETERMINATE); err != nil {
		owed = err
	}

	m.deps.Telemetry.RecoveryStarted(ctx)
	event := audit.BuildRecoveryStarted(m.deps.Clock, m.common(ctx))
	if err := m.deps.Audit.Emit(ctx, event); err != nil {
		m.retainOwed(event)
		owed = errors.Join(owed, errs.Wrap(err, "deliver recovery started event"))
	}
	return owed
}

// InRecovery reports whether this mutation reached RECOVERING, which is the
// question a caller asks after EnterRecovering fails: the phase moves only
// once its own record is delivered, so a failure before that leaves nothing
// written, and a failure after it leaves a mutation that is in recovery and
// needs a poll.
func (m *Machine) InRecovery() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inRecovery
}

// retainOwed keeps a record whose delivery failed, so it can be re-sent
// rather than lost.
//
// The event value is kept, not its inputs. Every constructor mints a fresh
// event_id, and the audit stream deduplicates on exactly that id — so a
// rebuilt record is a second record rather than a retry, and an account with
// duplicates is as wrong as one with holes while looking healthier.
func (m *Machine) retainOwed(event *eventv1.DeviceOperationEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.owed = append(m.owed, event)
}

// OwedRecordIDs are the event ids this mutation is still holding, for a
// report that has to name what the account is missing.
func (m *Machine) OwedRecordIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.owed))
	for _, e := range m.owed {
		ids = append(ids, e.GetEventId())
	}
	return ids
}

// DeliverOwed re-sends every retained record, keeping the ones that still do
// not land. Safe to call repeatedly; the recovery poll does, once per attempt.
//
// Re-sending the retained value is what makes this a retry rather than a
// second record: the stream deduplicates on the event id the value already
// carries.
func (m *Machine) DeliverOwed(ctx context.Context) error {
	m.mu.Lock()
	pending := m.owed
	m.owed = nil
	m.mu.Unlock()

	var failed []*eventv1.DeviceOperationEvent
	var err error
	for _, event := range pending {
		if emitErr := m.deps.Audit.Emit(ctx, event); emitErr != nil {
			failed = append(failed, event)
			err = errors.Join(err, emitErr)
		}
	}

	if len(failed) > 0 {
		m.mu.Lock()
		// Prepended: the records that have waited longest are the ones an
		// account reads first, and a later retry must not reorder them.
		m.owed = append(failed, m.owed...)
		m.mu.Unlock()
	}
	return err
}

// BlockFirmwareEpoch blocks the lane with FIRMWARE_EPOCH_CHANGED for a
// mutation whose device changed firmware before the command was sent. It
// does not move the phase: nothing was submitted, so the mutation is still
// exactly where it was, waiting for central to dispose it.
func (m *Machine) BlockFirmwareEpoch(ctx context.Context) error {
	return m.block(ctx, accessv1.BlockReason_BLOCK_REASON_FIRMWARE_EPOCH_CHANGED)
}

// Resume admits a mutation central is re-dispatching after an edge restart,
// past a checkpoint central already holds confirmed. It moves ADMITTED to
// POSSIBLY_APPLIED and latches submitted.
//
// Latching submitted is the point, not a side effect. Central only re-sends
// with resume once it has recorded the checkpoint, which means the command
// may already have reached the device on the run that died — nobody can
// say. From here a REJECTED acknowledgement must be refused, because
// disposing this mutation "nothing happened" would record that about a
// device that may be holding the change.
//
// The caller supplies the horizon start from the request's carried
// admission time, never from its own clock: an edge that crash-loops would
// otherwise restart the horizon on every run and the mutation would never
// abandon.
func (m *Machine) Resume(ctx context.Context) error {
	if m.IsRead() {
		return errs.New().Code(ErrCodeOutOfOrder).Msg("resume is not valid for a read operation")
	}
	if err := m.requirePhase(accessv1.OperationPhase_OPERATION_PHASE_ADMITTED, "Resume"); err != nil {
		return err
	}
	// Latched before the transition, not after. The transition publishes
	// POSSIBLY_APPLIED and releases mu; a REJECTED acknowledgement landing
	// in the gap would find submitted false at a phase that accepts it and
	// record "nothing happened" about a device that may hold the change
	// from the run that died. A latch set too early only refuses a REJECTED
	// acknowledgement slightly sooner, which is the safe direction for a
	// resume.
	m.mu.Lock()
	m.submitted = true
	m.mu.Unlock()
	return m.transition(ctx, accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED)
}

// Retry returns a mutation in recovery to POSSIBLY_APPLIED so its command
// can be sent again, and is the one step of a recovery poll that is
// recorded: exactly one PhaseTransitioned, because resending a command to a
// device is a real event and the observations around it are not.
//
// The submit latch is deliberately not reset. submitted stays true because
// the command did go out, once, and a REJECTED acknowledgement offered
// during a retry must still be refused — the device may already hold the
// change whether or not this attempt succeeds.
func (m *Machine) Retry(ctx context.Context) error {
	if err := m.requirePhase(accessv1.OperationPhase_OPERATION_PHASE_RECOVERING, "Retry"); err != nil {
		return err
	}
	if err := m.transition(ctx, accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED); err != nil {
		return err
	}
	m.mu.Lock()
	m.inRecovery = true
	m.mu.Unlock()
	return nil
}

// Abandon ends recovery in ABANDONED with disposition
// INDETERMINATE_ABANDONED and BLOCK_REASON_RECOVERY_HOLD: an authorized
// cancellation or a qualified timeout abandons a mutation
// whose effect recovery could not establish, and the hold that follows is
// resolved only by an explicit call outside this package (an operator
// decision or a reconciliation intent), never by a retry.
//
// Valid from any open phase, not only RECOVERING. Two callers need that:
// a recovery re-observation that failed to durably return to RECOVERING
// (its own second audit delivery failed) must still be abandonable once
// the horizon elapses, or that state has no exit at all; and central's own
// INDETERMINATE_ABANDONED acknowledgement is accepted wherever the
// mutation rests, since an edge that never comes back can be parked
// anywhere. A mutation that has already ended is refused — the decision
// belongs to whoever ended it.
func (m *Machine) Abandon(ctx context.Context) error {
	m.mu.Lock()
	if m.terminalLocked() {
		m.mu.Unlock()
		return errs.New().Code(ErrCodeAlreadyTerminal).
			Msg("mutation has already ended; it cannot be abandoned")
	}
	from := m.phase
	m.mu.Unlock()

	event := audit.BuildPhaseTransitioned(m.deps.Clock, m.common(ctx), from, accessv1.OperationPhase_OPERATION_PHASE_ABANDONED)
	if err := m.deps.Audit.Emit(ctx, event); err != nil {
		return errs.Wrap(err, "deliver phase transitioned event")
	}

	m.mu.Lock()
	if m.terminalLocked() && m.disposition != accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED {
		// Something else ended it while this record was in flight, and that
		// decision stands: whoever ended it owns the disposition.
		m.mu.Unlock()
		return m.staleTransition()
	}
	m.phase = accessv1.OperationPhase_OPERATION_PHASE_ABANDONED
	m.disposition = accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED
	m.inRecovery = false
	m.closeDoneLocked()
	m.mu.Unlock()

	// Routed through block, not set directly: the recovery hold is the one
	// block reason that requires an operator decision, and needs the same
	// audit record and telemetry event every other block reason gets.
	return m.block(ctx, accessv1.BlockReason_BLOCK_REASON_RECOVERY_HOLD)
}

// Acknowledge applies central's TerminalResultAck to this mutation at the
// door: it decides the acknowledgement against the phase and the submit
// latch under mu, marks the outcome, and only then walks the mutation to
// its terminal phase. It is called on central's own goroutine, not the
// submitter's, so it reaches a mutation resting anywhere.
//
// Nothing is stored for later. An acknowledgement held to be re-validated
// when the phase moves has to be re-armed on every path that leaves a rest
// point, and every such path is a chance to drop it; deciding here, under
// the lock that also guards the phase, leaves nothing to re-validate.
//
// The three refusals differ in what the caller should do. out-of-order is a
// disposition this phase does not allow and nothing changed, so central
// must re-derive what it owes. already-terminal means this mutation has
// ended and its own terminal report is on the way. A disposition of
// UNSPECIFIED is refused outright: MutationState rejects the zero value at
// a terminal phase, so accepting it would write a record the schema
// forbids.
//
// A failed audit delivery part-way through returns the error, and central
// re-sends. The identical acknowledgement is then accepted again and
// resumes the walk where it stopped, which is what the two re-entry arms
// below are for: once the ACKNOWLEDGED record has landed the phase has
// moved, so the door's ordinary checks — which read that phase — would
// refuse the re-send and the mutation would never reach a terminal state at
// all. Re-entry is keyed on the phase and disposition already written,
// which is the durable record of which walk was chosen.
func (m *Machine) Acknowledge(ctx context.Context, ack *integrationv1.TerminalResultAck) error {
	if ack.GetSequence() != m.req.GetSequence() {
		return errs.New().Code(ErrCodeOutOfOrder).
			Attr("ack_sequence", ack.GetSequence()).
			Attr("admitted_sequence", m.req.GetSequence()).
			Msg("acknowledgement sequence does not match the admitted request")
	}
	disposition := ack.GetDisposition()
	if disposition == accessv1.Disposition_DISPOSITION_UNSPECIFIED {
		return errs.New().Code(ErrCodeDispositionUnspecified).
			Msg("acknowledgement disposition is unspecified; MutationState rejects the zero value at a terminal phase")
	}

	m.mu.Lock()
	// Before the already-terminal refusal: Abandon writes ABANDONED and then
	// delivers the hold record, so a failure of that last delivery leaves a
	// terminal phase with no hold. Refusing the re-send here would leave the
	// device's lane unheld with nothing able to engage it.
	if m.phase == accessv1.OperationPhase_OPERATION_PHASE_ABANDONED &&
		disposition == accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED &&
		m.blockReason != accessv1.BlockReason_BLOCK_REASON_RECOVERY_HOLD {
		m.mu.Unlock()
		return m.block(ctx, accessv1.BlockReason_BLOCK_REASON_RECOVERY_HOLD)
	}
	if m.terminalLocked() {
		m.mu.Unlock()
		return errs.New().Code(ErrCodeAlreadyTerminal).
			Attr("phase", m.Phase().String()).
			Msg("mutation has already ended; its terminal report is on the way")
	}

	// The walk already started and stopped part-way. Its phase and
	// disposition say which one it was, so an identical re-send resumes it
	// rather than being refused for resting where the walk left it.
	if m.phase == accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED &&
		m.disposition == disposition {
		m.mu.Unlock()
		return m.release(ctx)
	}
	var cancel context.CancelFunc
	switch disposition {
	case accessv1.Disposition_DISPOSITION_VERIFIED:
		if m.phase != accessv1.OperationPhase_OPERATION_PHASE_VERIFIED {
			m.mu.Unlock()
			return m.outOfOrder(disposition)
		}
	case accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED:
		// Accepted at every open phase. An edge that never comes back is
		// the case this exists for, and it can be resting anywhere.
		m.abandoning = true
	case accessv1.Disposition_DISPOSITION_REJECTED:
		// Only while the command provably has not gone out: at ADMITTED,
		// or at POSSIBLY_APPLIED with the submit latch still open. After
		// the latch the device may already hold the change, and disposing
		// it REJECTED would record that nothing happened when something
		// might have.
		if m.submitted || (m.phase != accessv1.OperationPhase_OPERATION_PHASE_ADMITTED &&
			m.phase != accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED) {
			m.mu.Unlock()
			return m.outOfOrder(disposition)
		}
		m.canceled = true
		cancel = m.cancelWaits
	default:
		m.mu.Unlock()
		return m.outOfOrder(disposition)
	}
	m.mu.Unlock()

	// Outside mu: the mutation this wakes may take mu on its way out, and
	// it can only find canceled already set, never unset.
	if cancel != nil {
		cancel()
	}

	if disposition == accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED {
		return m.Abandon(ctx)
	}
	if err := m.transitionWithDisposition(ctx, accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED, disposition); err != nil {
		return err
	}
	return m.release(ctx)
}

// outOfOrder reports a disposition this mutation's phase does not allow,
// with nothing changed.
func (m *Machine) outOfOrder(disposition accessv1.Disposition) error {
	return errs.New().Code(ErrCodeOutOfOrder).
		Attr("phase", m.Phase().String()).
		Attr("disposition", disposition.String()).
		Msgf("%s is not a valid acknowledgement from %s", disposition, m.Phase())
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
	if m.phase != from && m.phase != accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED {
		m.mu.Unlock()
		return m.staleTransition()
	}
	m.phase = accessv1.OperationPhase_OPERATION_PHASE_RELEASED
	m.blockReason = accessv1.BlockReason_BLOCK_REASON_UNSPECIFIED
	m.blockedSince = time.Time{}
	m.closeDoneLocked()
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
	result.SetSubmitted(m.submitted)

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

// Progress builds a non-terminal report: where the mutation has reached and
// whether its command went out, with the empty progress arm
// integration/device/v1 defines for a report that is neither an observation
// nor an error. Admission and entry into recovery are reported this way —
// central needs to know a mutation moved without being told it finished.
func (m *Machine) Progress() *integrationv1.ExecuteResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := &integrationv1.ExecuteResult{}
	result.SetSequence(m.req.GetSequence())
	result.SetPhaseReached(m.phase)
	result.SetSubmitted(m.submitted)
	result.SetProgress(&integrationv1.Progress{})
	return result
}
