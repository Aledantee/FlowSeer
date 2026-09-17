package journal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	errsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/errs/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// Error codes the journal returns.
var (
	// ErrCodeConflict is a CAS write that could not settle within its retry
	// budget; the caller retries the whole operation.
	ErrCodeConflict = errs.NewCode("journal/conflict")
	// ErrCodeStore is a read or transport failure from the bucket: a
	// connection loss, a permission error, a deleted bucket. Unlike a
	// conflict, retrying the same operation will not settle it on its own.
	ErrCodeStore = errs.NewCode("journal/store")
	// ErrCodeState is a write invalid for the record's current state, such
	// as admitting over an open mutation.
	ErrCodeState = errs.NewCode("journal/state")
	// ErrCodeHoldsFull is a hold resolution refused because the device's
	// pending-hold set is at capacity: a wall an operator hits when abandons
	// or resolutions pile up faster than the edge acknowledges them, rather
	// than a record that grows until it fails the bucket's budget.
	ErrCodeHoldsFull = errs.NewCode("journal/holds-full")
	// ErrCodeAckPending is a resolution of a mutation whose terminal
	// acknowledgement the edge still owes. It is separate from ErrCodeState
	// because the operator's next action is different: nothing about the
	// request is wrong and nothing else has to change, it is simply too early.
	ErrCodeAckPending = errs.NewCode("journal/ack-pending")
	// ErrCodeEdgeHolds is a resolution of a mutation the edge was dispatched
	// and has not said how it ended. Waiting does not clear it, which is why
	// it is not ErrCodeAckPending: nothing is owed to the operator until
	// AbandonMutation ends the mutation, and only then is it resolvable.
	ErrCodeEdgeHolds = errs.NewCode("journal/edge-holds")
	// ErrCodeDecode is a stored record that will not unmarshal.
	ErrCodeDecode = errs.NewCode("journal/decode")
	// ErrCodeIdempotencyMismatch is a resubmission carrying a key the record
	// already admitted, with a different intent behind it. One key means one
	// intent, and answering with the new one would describe it as something
	// central recorded when it recorded something else.
	ErrCodeIdempotencyMismatch = errs.NewCode("journal/idempotency-mismatch")
)

// intentDigestLen is how much of the intent's SHA-256 the record keeps, and
// matches the schema's own bound on the field.
const intentDigestLen = 8

const (
	// casRetries bounds one operation's compare-and-set attempts; one writer
	// per device is the norm, so a conflict is a rare replica race.
	casRetries = 8
	// maxIdempotencyKeys is how many recent keys a record remembers for
	// resubmission dedup.
	maxIdempotencyKeys = 64
	// maxPendingHolds bounds the pending hold-resolution set, matching the
	// schema's repeated max_items and the idempotency list's shape, so the
	// record cannot grow without limit against the bucket's budget.
	maxPendingHolds = 64
)

// Journal is central's per-device lane store over the device-lanes bucket.
// Every write is a compare-and-set on one device's record, so one writer
// per device holds across central replicas without a lease. Safe for
// concurrent use.
type Journal struct {
	kv    jetstream.KeyValue
	clock func() time.Time
}

// New constructs a Journal over the device-lanes bucket. A nil clock uses
// the wall clock.
func New(kv jetstream.KeyValue, clock func() time.Time) *Journal {
	if clock == nil {
		clock = time.Now
	}
	return &Journal{kv: kv, clock: clock}
}

// Record reads one device's record, or an empty record when the device has
// none yet.
func (j *Journal) Record(ctx context.Context, deviceID string) (*storev1.DeviceLaneRecord, error) {
	rec, _, err := j.load(ctx, deviceID)
	return rec, err
}

func (j *Journal) load(ctx context.Context, deviceID string) (*storev1.DeviceLaneRecord, uint64, error) {
	entry, err := j.kv.Get(ctx, deviceID)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return &storev1.DeviceLaneRecord{}, 0, nil
	}
	if err != nil {
		return nil, 0, errs.From(err).Code(ErrCodeStore).Attr("device", deviceID).Msg("read lane record")
	}
	rec := &storev1.DeviceLaneRecord{}
	if err := proto.Unmarshal(entry.Value(), rec); err != nil {
		return nil, 0, errs.From(err).Code(ErrCodeDecode).Attr("device", deviceID).Msg("decode lane record")
	}
	return rec, entry.Revision(), nil
}

// errSkip is fn's signal to mutate that no write is needed.
var errSkip = errors.New("journal: no write needed")

// mutate runs fn against the device's record under compare-and-set,
// retrying on a revision conflict. fn returning errSkip means "no write is
// needed" and mutate returns nil without writing.
func (j *Journal) mutate(ctx context.Context, deviceID string, fn func(*storev1.DeviceLaneRecord) error) error {
	for attempt := 0; attempt < casRetries; attempt++ {
		rec, revision, err := j.load(ctx, deviceID)
		if err != nil {
			return err
		}
		if err := fn(rec); err != nil {
			if errors.Is(err, errSkip) {
				return nil
			}
			return err
		}
		data, err := proto.Marshal(rec)
		if err != nil {
			return errs.From(err).Code(ErrCodeDecode).Attr("device", deviceID).Msg("encode lane record")
		}
		if revision == 0 {
			_, err = j.kv.Create(ctx, deviceID, data)
			if errors.Is(err, jetstream.ErrKeyExists) {
				continue // another writer created it; reload and retry
			}
		} else {
			_, err = j.kv.Update(ctx, deviceID, data, revision)
			if errors.Is(err, jetstream.ErrKeyRevisionMismatch) {
				continue // another writer advanced it; reload and retry
			}
		}
		if err != nil {
			return errs.From(err).Code(ErrCodeStore).Attr("device", deviceID).Msg("write lane record")
		}
		return nil
	}
	return errs.New().Code(ErrCodeConflict).Attr("device", deviceID).Msg("lane record write did not settle")
}

// Admit records a mutation intent at ADMITTED and assigns it the next
// sequence, in one CAS write. A resubmission carrying an idempotency key the
// record already holds returns the recorded state without admitting again.
func (j *Journal) Admit(ctx context.Context, deviceID string, intent *accessv1.MutationIntent, edge *edgev1.EdgeGlobalRef) (*accessv1.MutationState, error) {
	return j.admit(ctx, deviceID, intent, edge, accessv1.BlockReason_BLOCK_REASON_UNSPECIFIED)
}

// AdmitBlocked admits an intent that is recorded but must not be dispatched,
// under the reason that holds it. It is how a device whose configuration a
// person owns records a difference central found: the intent takes the lane,
// which stops anything else being admitted behind it, and waits for the
// operator to accept, restore, or replace it. The owed-row derivation refuses
// to dispatch a blocked mutation, so the hold needs no second mechanism.
func (j *Journal) AdmitBlocked(ctx context.Context, deviceID string, intent *accessv1.MutationIntent, edge *edgev1.EdgeGlobalRef, reason accessv1.BlockReason) (*accessv1.MutationState, error) {
	return j.admit(ctx, deviceID, intent, edge, reason)
}

func (j *Journal) admit(ctx context.Context, deviceID string, intent *accessv1.MutationIntent, edge *edgev1.EdgeGlobalRef, reason accessv1.BlockReason) (*accessv1.MutationState, error) {
	var state *accessv1.MutationState
	err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		seq, recorded, err := recordedSequence(rec, deviceID, intent)
		if err != nil {
			return err
		}
		if recorded {
			state = recordedState(rec, seq, intent, edge)
			return errSkip
		}
		if rec.HasMutation() {
			return errs.New().Code(ErrCodeState).Attr("device", deviceID).Msg("a mutation already holds this device's lane")
		}
		state = admitIntent(rec, deviceID, intent, edge, j.clock())
		if reason != accessv1.BlockReason_BLOCK_REASON_UNSPECIFIED {
			state.SetBlockReason(reason)
			state.SetBlockedSince(timestamppb.New(j.clock()))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return state, nil
}

// OpenRead assigns a read the next sequence and records it, unless the
// interface already has a live open read, in which case it returns that read's
// sequence and writes nothing. device names the record's device so a read
// that arrives before the first mutation still writes a valid record; an
// absent ref is built from deviceID rather than left unset.
//
// A read whose deadline has passed is not joined. It owes no row and is due to
// be closed with a deadline error, so joining it would answer this caller with
// the previous read's failure instead of reading the device — the sweep and
// this call would otherwise race to decide which. Replacing it is what the
// sweep was going to do to it anyway.
func (j *Journal) OpenRead(ctx context.Context, deviceID string, device *inventoryv1.DeviceGlobalRef, iface string, read *accessv1.TypedRead, idempotencyKey string, deadline time.Time) (uint64, error) {
	var seq uint64
	err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		ensureDevice(rec, device, deviceID)
		reads := rec.GetOpenReads()
		if reads == nil {
			reads = map[string]*storev1.OpenRead{}
		}
		if existing, ok := reads[iface]; ok && !existing.HasOutcome() && !readExpired(existing, j.clock()) {
			seq = existing.GetSequence()
			return errSkip
		}
		seq = rec.GetHighWatermark() + 1
		rec.SetHighWatermark(seq)
		entry := &storev1.OpenRead{}
		entry.SetSequence(seq)
		entry.SetRead(read)
		entry.SetIdempotencyKey(idempotencyKey)
		entry.SetDeadline(timestamppb.New(deadline))
		reads[iface] = entry
		rec.SetOpenReads(reads)
		return nil
	})
	return seq, err
}

// readExpired reports whether an open read's deadline has passed at now. An
// entry with no deadline never expires; nothing writes one today, and treating
// a missing bound as an elapsed one would drop reads that are still running.
func readExpired(read *storev1.OpenRead, now time.Time) bool {
	deadline := read.GetDeadline()
	return deadline != nil && !deadline.AsTime().After(now)
}

// CloseRead sets an open read's outcome; a later write removes the entry
// once its waiter has read it. Exactly one of obs or errPayload is set. The
// sequence scopes the write: an interface's entry can be replaced by a later
// read (OpenRead reuses the key once the prior read closed), so a late report
// for an earlier sequence must not land on the read that succeeded it, and a
// report for an entry that already closed is dropped.
func (j *Journal) CloseRead(ctx context.Context, deviceID, iface string, sequence uint64, obs *accessv1.InterfaceObservation, errPayload *errsv1.ErrorPayload) error {
	err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		reads := rec.GetOpenReads()
		entry, ok := reads[iface]
		if !ok || entry.GetSequence() != sequence || entry.HasOutcome() {
			return errSkip
		}
		if obs != nil {
			entry.SetObservation(obs)
			keepLastObservation(rec, iface, obs)
		} else {
			entry.SetError(errPayload)
		}
		rec.SetOpenReads(reads)
		return nil
	})
	return err
}

// SweepExpiredReads closes every open read whose deadline has passed with no
// outcome, recording errPayload, and returns the interface names it closed.
// It observes expiry and records the failure in one CAS write, so a report
// that lands between observing an expiry and recording it wins: an entry that
// acquired an outcome first is left untouched. [ExpiredReads] is its pure
// counterpart for tests and callers that only need to know what is due.
func (j *Journal) SweepExpiredReads(ctx context.Context, deviceID string, now time.Time, errPayload *errsv1.ErrorPayload) ([]string, error) {
	var swept []string
	err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		reads := rec.GetOpenReads()
		swept = nil
		for name, entry := range reads {
			if entry.HasOutcome() {
				continue
			}
			if deadline := entry.GetDeadline(); deadline != nil && !deadline.AsTime().After(now) {
				entry.SetError(errPayload)
				swept = append(swept, name)
			}
		}
		if len(swept) == 0 {
			return errSkip
		}
		rec.SetOpenReads(reads)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return swept, nil
}

// MarkOnboarded records that the edge restarted and re-subscribed: the
// per-dispatch confirmations are cleared so the open mutation and any pending
// hold are re-sent, while dispatched — the fact that the edge once held the
// mutation — survives.
func (j *Journal) MarkOnboarded(ctx context.Context, deviceID string) error {
	err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		if !rec.GetDispatchConfirmed() && !rec.GetCheckpointConfirmed() {
			return errSkip
		}
		rec.SetDispatchConfirmed(false)
		rec.SetCheckpointConfirmed(false)
		return nil
	})
	return err
}

// ReportKind is which edge report ApplyReport applies.
type ReportKind int

const (
	// ReportAdmitted confirms the edge admitted the mutation; its CAS write
	// is the one that reaches POSSIBLY_APPLIED.
	ReportAdmitted ReportKind = iota + 1
	// ReportVerified records a verified disposition.
	ReportVerified
	// ReportRecovering records that the effect is indeterminate.
	ReportRecovering
	// ReportReleased closes the record after a released disposition.
	ReportReleased
	// ReportAbandoned confirms an abandonment; the mutation stays held for
	// the operator, but the terminal ack no longer needs re-sending.
	ReportAbandoned
	// ReportRefused is the edge refusing a terminal ack it cannot apply; it
	// closes the record the way ReportReleased does and never re-disposes.
	ReportRefused
	// ReportError is a failure the edge reports; Submitted says whether the
	// command was handed to the device first.
	ReportError
)

// Report is one edge report, addressed to a sequence.
type Report struct {
	Kind      ReportKind
	Sequence  uint64
	Submitted bool
	// Observation is the read-back the edge verified the mutation with, when
	// it sent one. On ReportVerified it becomes the interface's last
	// observation, which is what proves the change took.
	Observation *accessv1.InterfaceObservation
}

// ApplyReport applies an edge report scoped to the open mutation's sequence.
// A report for another sequence, a duplicate that finds the state already
// advanced, or a report that would regress the mutation is ignored. Once the
// mutation carries a disposition only the terminal-clearing reports proceed,
// so an in-flight report cannot overwrite an operator's abandonment; and an
// edge report cannot advance the mutation before its own admission is
// recorded (dispatched).
func (j *Journal) ApplyReport(ctx context.Context, deviceID string, rep Report) error {
	err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		m := rec.GetMutation()
		if m == nil || m.GetSequence() != rep.Sequence {
			return errSkip // stale, or for a sequence no longer open
		}
		if !reportApplies(rep.Kind, m, rec) {
			return errSkip
		}
		switch rep.Kind {
		case ReportAdmitted:
			rec.SetDispatched(true)
			rec.SetDispatchConfirmed(true)
			// A first admission advances the phase; a re-admission after
			// Onboarded only re-confirms the dispatch, and must not regress
			// a mutation already at POSSIBLY_APPLIED or in recovery.
			if m.GetPhase() == accessv1.OperationPhase_OPERATION_PHASE_ADMITTED {
				m.SetPhase(accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED)
				rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
			}
		case ReportVerified:
			m.SetDisposition(accessv1.Disposition_DISPOSITION_VERIFIED)
			m.SetPhase(accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED)
			rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_VERIFIED)
			// A verified change is what central expects from now on, and the
			// read-back that proved it is what the interface last showed.
			// Both land in this write: an expectation with no observation
			// behind it would have the drift poll comparing against a value
			// nobody confirmed, and an observation with no expectation would
			// not be kept at all.
			if change := m.GetIntent().GetInterfaceDescription(); change != nil {
				setExpected(rec, change.GetInterfaceName(), change.GetDescription())
				if obs := rep.Observation; obs != nil {
					keepLastObservation(rec, change.GetInterfaceName(), obs)
				}
			}
		case ReportRecovering:
			m.SetBlockReason(accessv1.BlockReason_BLOCK_REASON_INDETERMINATE)
			m.SetBlockedSince(timestamppb.New(j.clock()))
			rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_RECOVERING)
		case ReportReleased, ReportRefused:
			// A released disposition, or a refused terminal ack, frees the
			// lane the same way; the edge's RELEASED report is the barrier.
			closeMutation(rec, m)
		case ReportAbandoned:
			rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_ABANDONED)
		case ReportError:
			// An error report proves the edge holds the sequence, whether or
			// not the command reached the device.
			rec.SetDispatched(true)
			if rep.Submitted {
				// The command was sent; the effect is unknown, so recovery,
				// never a rejection.
				m.SetBlockReason(accessv1.BlockReason_BLOCK_REASON_INDETERMINATE)
				m.SetBlockedSince(timestamppb.New(j.clock()))
				rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_RECOVERING)
				// The phase moves with the report. OwedRows derives resume
				// from it, so a command the edge has said it submitted must
				// not be re-dispatched as fresh work: left at ADMITTED, the
				// row goes back out with resume false and the edge admits it
				// again rather than resuming it into recovery.
				if m.GetPhase() == accessv1.OperationPhase_OPERATION_PHASE_ADMITTED {
					m.SetPhase(accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED)
				}
				return nil
			}
			// The command never reached the device: dispose REJECTED so the
			// terminal ack is owed. The edge's RELEASED report closes it
			// through the ordinary path.
			m.SetDisposition(accessv1.Disposition_DISPOSITION_REJECTED)
			m.SetPhase(accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED)
		}
		return nil
	})
	return err
}

// reportApplies is ApplyReport's phase-order guard: it says whether a report
// may act on the mutation as it stands. A terminal mutation accepts only the
// reports that clear or confirm the terminal state; a lifecycle report that
// implies the edge holds the sequence is refused until the edge's admission
// is recorded.
func reportApplies(kind ReportKind, m *accessv1.MutationState, rec *storev1.DeviceLaneRecord) bool {
	if m.HasDisposition() {
		switch kind {
		case ReportReleased, ReportRefused:
			// An abandonment stays held until ResolveDesynchronization; only a
			// released disposition is cleared by the edge's RELEASED report.
			return m.GetDisposition() != accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED
		case ReportAbandoned:
			return m.GetDisposition() == accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED
		default:
			return false // the mutation is terminal; nothing else may regress it
		}
	}
	switch kind {
	case ReportAdmitted:
		return !rec.GetDispatchConfirmed() // a re-confirmation past confirmation is a no-op
	case ReportVerified, ReportRecovering:
		return rec.GetDispatched() // the edge cannot verify or recover what it never admitted
	case ReportError:
		return true // an error is valid before or after admission
	default:
		return false // Released, Refused, Abandoned need a disposition to act on
	}
}

// ConfirmCheckpoint records the edge's CheckpointAck, and only while the
// dispatch it belongs to is still confirmed. An acknowledgement for any other
// sequence, or one arriving after MarkOnboarded cleared the confirmations,
// writes nothing.
func (j *Journal) ConfirmCheckpoint(ctx context.Context, deviceID string, sequence uint64) error {
	return j.mutateMutation(ctx, deviceID, sequence, func(rec *storev1.DeviceLaneRecord, _ *accessv1.MutationState) {
		// Only while the dispatch this checkpoint belongs to is confirmed.
		// The flag is per dispatch and MarkOnboarded clears it, so an
		// acknowledgement draining after a restart would otherwise mark the
		// checkpoint of a dispatch that no longer exists — and OwedRows
		// would then never send the CheckpointRequest the new dispatch
		// needs, leaving the edge parked until its horizon.
		if rec.GetDispatchConfirmed() {
			rec.SetCheckpointConfirmed(true)
		}
	})
}

// ConfirmHoldResolved clears a hold-resolution row the edge acknowledged.
func (j *Journal) ConfirmHoldResolved(ctx context.Context, deviceID string, sequence uint64) error {
	return j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		if !removeHold(rec, sequence) {
			return errSkip
		}
		return nil
	})
}

// Dispose is AbandonMutation: it ends a non-terminal mutation. When the edge
// has reported the mutation admitted, it writes ABANDONED with a recovery
// hold that ResolveDesynchronization later resolves. When the edge never
// reported admitted there is nothing on the device to recover, so it closes
// the lane and owes a HoldResolved row instead, which releases an edge that
// may already hold the dispatched ExecuteRequest. Either way it returns the
// abandoned state for the caller and the audit.
func (j *Journal) Dispose(ctx context.Context, deviceID string, sequence uint64) (*accessv1.MutationState, error) {
	var state *accessv1.MutationState
	err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		m := rec.GetMutation()
		if m == nil || m.GetSequence() != sequence {
			return errs.New().Code(ErrCodeState).Attr("device", deviceID).Msg("no open mutation at that sequence to abandon")
		}
		if m.HasDisposition() {
			return errs.New().Code(ErrCodeState).Attr("device", deviceID).Msg("mutation is already terminal")
		}
		m.SetPhase(accessv1.OperationPhase_OPERATION_PHASE_ABANDONED)
		m.SetDisposition(accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED)
		if !rec.GetDispatched() {
			if err := addHold(rec, deviceID, sequence); err != nil {
				return err // refuse before clearing, so the abandon is atomic
			}
			state = m // detached: the record's mutation is cleared below
			closeMutation(rec, m)
			return nil
		}
		m.SetBlockReason(accessv1.BlockReason_BLOCK_REASON_RECOVERY_HOLD)
		m.SetBlockedSince(timestamppb.New(j.clock()))
		state = m
		return nil
	})
	if err != nil {
		return nil, err
	}
	return state, nil
}

// RejectDispatch disposes a mutation REJECTED because the edge refused its
// dispatch, and how it does so turns on whether the edge had reported the
// mutation admitted. When it had not (dispatched unset), the refusal is proof
// the command never reached the device: the lane is freed at once and no
// terminal ack is owed. When it had (dispatched set — a resumed dispatch after
// Onboarded, refused because the firmware epoch changed), the command may
// already have reached the device, so the mutation is kept and disposed
// REJECTED with dispatched untouched; the terminal ack is then owed and the
// edge's RELEASED report frees the lane through the ordinary path. Either way
// it returns the disposed state for the caller to audit. A stale sequence or
// an already-terminal mutation is ignored.
func (j *Journal) RejectDispatch(ctx context.Context, deviceID string, sequence uint64) (*accessv1.MutationState, error) {
	var state *accessv1.MutationState
	err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		m := rec.GetMutation()
		if m == nil || m.GetSequence() != sequence || m.HasDisposition() {
			return errSkip
		}
		m.SetPhase(accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED)
		m.SetDisposition(accessv1.Disposition_DISPOSITION_REJECTED)
		state = m
		if !rec.GetDispatched() {
			closeMutation(rec, m) // never reached the device: free the lane at once
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return state, nil
}

// Resolution is one arm of the operator's ResolveDesynchronization, in the
// terms the record keeps: which held sequence ends, what central expects on
// the interface afterwards, and what takes the resolved mutation's place.
type Resolution struct {
	// Sequence is the held sequence being resolved: a mutation abandoned into
	// a recovery hold, or an intent the drift poll is holding.
	Sequence uint64
	// Interface and Expected adopt what the device actually carries as the new
	// expectation, which is the accept arm. An empty Interface leaves the
	// expectation alone; an empty Expected is a description a device really
	// can carry, so the two cannot be one field.
	Interface string
	Expected  string
	// Intent is admitted at the next sequence, in the same write: the restore
	// arm's reconciliation intent, or the replace arm's carried one. Nil
	// admits nothing, which is the accept arm.
	Intent *accessv1.MutationIntent
	// Edge is the intent's responsible edge, required with Intent.
	Edge *edgev1.EdgeGlobalRef
}

// ResolveDesynchronization ends a held sequence and admits its replacement in
// one compare-and-set write, and returns the resolved state and the admitted
// one — either may be nil, since the accept arm admits nothing and a sequence
// whose mutation the record already closed has no state left to return.
//
// One write because the two halves cannot be separated safely. Resolving frees
// the lane and stops the drift poll seeing a held mutation; admitting is what
// records the operator's decision. A central that stopped between them would
// leave the hold resolved, the edge released, drift free to re-evaluate, and
// the operator believing the expected state was being put back while nothing
// recorded it. [Journal.Admit] refuses over a held mutation, so the two-call
// shape is not merely riskier, it is the only other shape available.
//
// The arms differ only in what they carry. An intent the drift poll held at
// ADMITTED never reached the device, so it is disposed REJECTED and released
// here, never ABANDONED. A sequence already abandoned is terminal and keeps
// its disposition; only its hold is resolved. Either way the sequence's hold
// is recorded, so the edge is told to clear its own before the next dispatch.
//
// An abandonment the edge has not acknowledged yet is refused with
// [ErrCodeAckPending], which is [TerminatorNamed]'s rule enforced: while the
// terminal acknowledgement is owed, closing the record would withdraw it and
// leave the edge waiting on an answer nothing would send. A mutation the edge
// was dispatched and has not ended is refused with [ErrCodeEdgeHolds]:
// AbandonMutation ends it, and no amount of waiting will.
//
// A resubmission carrying an idempotency key the record already holds returns
// the recorded state and writes nothing, so a lost response is retried rather
// than resolved twice.
func (j *Journal) ResolveDesynchronization(ctx context.Context, deviceID string, res Resolution) (*accessv1.MutationState, *accessv1.MutationState, error) {
	var resolved, admitted *accessv1.MutationState
	err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		if res.Intent != nil {
			seq, recorded, err := recordedSequence(rec, deviceID, res.Intent)
			if err != nil {
				return err
			}
			if recorded {
				if seq <= res.Sequence {
					// The key was admitted for an earlier sequence, not by this
					// resolution: a replacement is always admitted past the
					// sequence it replaces. Echoing that older state would
					// answer the operator with a success while the hold they
					// asked to resolve still stands.
					return errs.New().Code(ErrCodeIdempotencyMismatch).Attr("device", deviceID).
						Attr("sequence", seq).Attr("resolving", res.Sequence).
						Msg("this idempotency key was already admitted for an earlier sequence")
				}
				admitted = recordedState(rec, seq, res.Intent, res.Edge)
				return errSkip
			}
		}

		m := rec.GetMutation()
		switch {
		case m != nil && m.GetSequence() == res.Sequence:
			if terminalAckOwed(rec) {
				// The edge still holds this sequence and is waiting to be told
				// how it ended. Closing the record here withdraws the owed
				// TerminalResultAck, so the edge would sit in its acknowledgement
				// wait until its own operation context expired while central
				// dispatched the replacement into a lane it still occupies.
				// [TerminatorNamed] already says this arm is the terminator only
				// once the edge has confirmed; this is that rule enforced.
				return errs.New().Code(ErrCodeAckPending).Attr("device", deviceID).
					Attr("sequence", res.Sequence).
					Msg("the edge has not acknowledged how this mutation ended")
			}
			if !m.HasDisposition() && rec.GetDispatched() {
				// The edge holds this sequence and has not said how it ended.
				// Writing REJECTED here would record that the command never
				// reached the device for one that may already have applied,
				// drop the edge's own later report as stale, and dispatch the
				// replacement into a lane the edge still occupies.
				// AbandonMutation is the terminator for a mutation in this
				// state.
				return errs.New().Code(ErrCodeEdgeHolds).Attr("device", deviceID).
					Attr("sequence", res.Sequence).
					Msg("the edge still holds this mutation; abandon it before resolving")
			}
			if !m.HasDisposition() {
				// Held at ADMITTED and never dispatched: the disposition is
				// REJECTED, and the phase moves to RELEASED in this one write
				// so the record never holds a disposition its phase forbids.
				m.SetPhase(accessv1.OperationPhase_OPERATION_PHASE_RELEASED)
				m.SetDisposition(accessv1.Disposition_DISPOSITION_REJECTED)
			}
			resolved = m // detached: the record's mutation is cleared below
			if err := addHold(rec, deviceID, res.Sequence); err != nil {
				return err // refuse before clearing, so the resolution is atomic
			}
			closeMutation(rec, m)
		case m != nil:
			return errs.New().Code(ErrCodeState).Attr("device", deviceID).
				Msg("another mutation holds this device's lane")
		case holdPending(rec, res.Sequence):
			// The abandonment already closed the lane and recorded the hold;
			// the arm's own intent is all that is left to admit.
		default:
			return errs.New().Code(ErrCodeState).Attr("device", deviceID).
				Msg("no held sequence to resolve")
		}

		if res.Interface != "" {
			setExpected(rec, res.Interface, res.Expected)
		}

		if res.Intent != nil {
			admitted = admitIntent(rec, deviceID, res.Intent, res.Edge, j.clock())
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return resolved, admitted, nil
}

// DropHolds forgets every hold resolution this device still owes an edge. It
// is what retiring an edge does to the lanes it hosted: a hold exists to tell
// an edge to clear its own, so with no edge left to tell, the obligation is
// moot rather than pending, and owing it forever is what fills the pending set
// and walls off the abandons an operator working around a dead edge needs.
//
// It ends no mutation. Abandoning live work is a decision only an operator
// makes, and AbandonMutation is where they make it.
func (j *Journal) DropHolds(ctx context.Context, deviceID string) error {
	return j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		if len(rec.GetHoldResolutionPending()) == 0 {
			return errSkip
		}
		rec.SetHoldResolutionPending(nil)
		return nil
	})
}

// SetExpected records the description central expects on one interface, the
// baseline the drift poll compares against.
func (j *Journal) SetExpected(ctx context.Context, deviceID string, device *inventoryv1.DeviceGlobalRef, iface, description string) error {
	err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		ensureDevice(rec, device, deviceID)
		setExpected(rec, iface, description)
		return nil
	})
	return err
}

// SetFingerprint records the firmware fingerprint the edge reported.
func (j *Journal) SetFingerprint(ctx context.Context, deviceID string, device *inventoryv1.DeviceGlobalRef, fingerprint string) error {
	err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		if rec.GetFirmwareFingerprint() == fingerprint {
			return errSkip
		}
		ensureDevice(rec, device, deviceID)
		rec.SetFirmwareFingerprint(fingerprint)
		return nil
	})
	return err
}

// mutateMutation applies fn only when the record's open mutation matches
// sequence.
func (j *Journal) mutateMutation(ctx context.Context, deviceID string, sequence uint64, fn func(*storev1.DeviceLaneRecord, *accessv1.MutationState)) error {
	err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		m := rec.GetMutation()
		if m == nil || m.GetSequence() != sequence {
			return errSkip
		}
		fn(rec, m)
		return nil
	})
	return err
}

// clearMutation closes an open mutation and its per-mutation confirmations.
// admitIntent records intent at the next sequence, at ADMITTED, and takes the
// lane. The caller has already established that the lane is free.
func admitIntent(rec *storev1.DeviceLaneRecord, deviceID string, intent *accessv1.MutationIntent, edge *edgev1.EdgeGlobalRef, now time.Time) *accessv1.MutationState {
	ensureDevice(rec, intent.GetDevice(), deviceID)
	seq := rec.GetHighWatermark() + 1
	rec.SetHighWatermark(seq)
	m := &accessv1.MutationState{}
	m.SetIntent(intent)
	m.SetSequence(seq)
	m.SetPhase(accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
	m.SetResponsibleEdge(edge)
	rec.SetMutation(m)
	rec.SetAdmittedAt(timestamppb.New(now))
	rememberKey(rec, intent.GetIdempotencyKey(), seq, intentDigest(intent))
	return m
}

// holdPending reports whether the sequence's hold resolution is still owed.
func holdPending(rec *storev1.DeviceLaneRecord, sequence uint64) bool {
	for _, s := range rec.GetHoldResolutionPending() {
		if s == sequence {
			return true
		}
	}
	return false
}

// setExpected records what central expects on one interface, which is also
// what makes the interface managed as far as the record is concerned.
func setExpected(rec *storev1.DeviceLaneRecord, iface, description string) {
	expected := rec.GetExpectedDescriptions()
	if expected == nil {
		expected = map[string]string{}
	}
	expected[iface] = description
	rec.SetExpectedDescriptions(expected)
}

// keepLastObservation records a complete observation as what central last saw
// on a managed interface, which is what the drift comparison and the status
// call read. An interface central holds no expected description for is not
// managed, so its read leaves nothing behind but its own closed entry: a
// one-off read of any of a switch's ports must not start growing the record a
// row at a time. A partial observation is not kept either, since a comparison
// against one would be a comparison against fields nobody read.
func keepLastObservation(rec *storev1.DeviceLaneRecord, iface string, obs *accessv1.InterfaceObservation) {
	if obs.GetCompleteness() != accessv1.Completeness_COMPLETENESS_COMPLETE {
		return
	}
	if _, managed := rec.GetExpectedDescriptions()[iface]; !managed {
		return
	}
	observations := rec.GetLastObservations()
	if observations == nil {
		observations = map[string]*accessv1.InterfaceObservation{}
	}
	observations[iface] = obs
	rec.SetLastObservations(observations)
}

// closeMutation frees the lane and keeps, on the sequence's idempotency entry,
// the disposition it ended at. That is all the record retains of a closed
// mutation, and a resubmission after the close has nothing else to read: told
// only that the sequence reached its end, an operator cannot tell a rejection
// from a success. Every path that closes a mutation has already set its
// disposition, so the entry is never left saying nothing.
func closeMutation(rec *storev1.DeviceLaneRecord, m *accessv1.MutationState) {
	for _, entry := range rec.GetIdempotency() {
		if entry.GetSequence() == m.GetSequence() {
			entry.SetDisposition(m.GetDisposition())
			break
		}
	}
	clearMutation(rec)
}

func clearMutation(rec *storev1.DeviceLaneRecord) {
	rec.ClearMutation()
	rec.ClearAdmittedAt()
	rec.SetDispatched(false)
	rec.SetDispatchConfirmed(false)
	rec.SetCheckpointConfirmed(false)
	rec.ClearLastReportedPhase()
}

// addHold adds a sequence to the pending hold-resolution set, keeping it a set
// (a re-resolution of a sequence already pending is a no-op). It refuses with
// ErrCodeHoldsFull when the set is at capacity and the sequence is new, so an
// operator piling up resolutions the edge has not acknowledged hits a wall
// that names the reason rather than growing the record silently.
func addHold(rec *storev1.DeviceLaneRecord, deviceID string, sequence uint64) error {
	if holdPending(rec, sequence) {
		return nil
	}
	holds := rec.GetHoldResolutionPending()
	if len(holds) >= maxPendingHolds {
		return errs.New().Code(ErrCodeHoldsFull).Attr("device", deviceID).
			Msg("pending hold resolutions are at capacity; the edge must acknowledge some before more can be recorded")
	}
	rec.SetHoldResolutionPending(append(holds, sequence))
	return nil
}

// removeHold drops a sequence from the pending hold-resolution set and reports
// whether it was there.
func removeHold(rec *storev1.DeviceLaneRecord, sequence uint64) bool {
	holds := rec.GetHoldResolutionPending()
	for i, s := range holds {
		if s == sequence {
			rec.SetHoldResolutionPending(append(holds[:i:i], holds[i+1:]...))
			return true
		}
	}
	return false
}

func ensureDevice(rec *storev1.DeviceLaneRecord, device *inventoryv1.DeviceGlobalRef, deviceID string) {
	if rec.HasDevice() {
		return
	}
	if device.GetDevice().GetId() == "" {
		// The field is required on a stored record, and a caller with no ref
		// to hand is the ordinary first-write case rather than a mistake: the
		// drift poll opens a read on a device that has never been written to.
		// Building the ref from the key the write is addressed by keeps the
		// record valid; passing the caller's nil through would persist a
		// record failing its own schema rules, which nothing dereferences
		// today and something eventually will.
		device = deviceRef(deviceID)
	}
	rec.SetDevice(device)
}

// deviceRef builds a device ref from the id a record is keyed by.
func deviceRef(id string) *inventoryv1.DeviceGlobalRef {
	local := &inventoryv1.DeviceLocalRef{}
	local.SetId(id)
	ref := &inventoryv1.DeviceGlobalRef{}
	ref.SetDevice(local)
	return ref
}

// recordedSequence reports the sequence an idempotency key was admitted at,
// and refuses a key whose intent is not the one it was admitted with. The
// refusal is what makes the recorded state safe to answer with: the record
// keeps no intent of its own past the close, so a match here is the only
// thing that makes describing the caller's intent as the recorded one true.
func recordedSequence(rec *storev1.DeviceLaneRecord, deviceID string, intent *accessv1.MutationIntent) (uint64, bool, error) {
	key := intent.GetIdempotencyKey()
	if key == "" {
		return 0, false, nil
	}
	for _, entry := range rec.GetIdempotency() {
		if entry.GetIdempotencyKey() != key {
			continue
		}
		if !bytes.Equal(entry.GetIntentDigest(), intentDigest(intent)) {
			return 0, false, errs.New().Code(ErrCodeIdempotencyMismatch).Attr("device", deviceID).
				Attr("sequence", entry.GetSequence()).
				Msg("this idempotency key was admitted with a different intent")
		}
		return entry.GetSequence(), true, nil
	}
	return 0, false, nil
}

func recordedState(rec *storev1.DeviceLaneRecord, seq uint64, intent *accessv1.MutationIntent, edge *edgev1.EdgeGlobalRef) *accessv1.MutationState {
	if m := rec.GetMutation(); m != nil && m.GetSequence() == seq {
		return m
	}
	// The mutation closed and the record kept only how it ended. The intent
	// and the edge come from this call: the same idempotency key is the same
	// intent, which is what makes the key idempotent, so echoing the caller's
	// own is not a claim about anything the record forgot.
	m := &accessv1.MutationState{}
	m.SetIntent(intent)
	m.SetSequence(seq)
	m.SetPhase(accessv1.OperationPhase_OPERATION_PHASE_RELEASED)
	m.SetResponsibleEdge(edge)
	for _, entry := range rec.GetIdempotency() {
		if entry.GetSequence() == seq && entry.HasDisposition() {
			m.SetDisposition(entry.GetDisposition())
			break
		}
	}
	return m
}

// intentDigest identifies an intent well enough to catch a caller reusing one
// idempotency key for two different requests. Eight bytes of SHA-256 is a
// collision every few billion intents on one device, against a mistake, not an
// adversary.
//
// The digest is taken over the projection below rather than the marshaled
// message. Deterministic marshaling is stable only within one binary —
// protobuf-go says so, and names fingerprinting as a use that must define its
// own canonicalization — so digesting the wire bytes would let a routine
// dependency bump change every live digest. The operator's own unchanged
// retry would then come back as "this key was used for a different request",
// with no way forward but a fresh key, which is the second admission the key
// exists to prevent.
//
// Each part is written length-prefixed so no two field values can run
// together into the same bytes. A field added to MutationIntent must be added
// here too, which [TestIntentDigestCoversEveryIntentField] is there to force:
// a field the projection omits is a difference two intents can carry while
// digesting alike.
func intentDigest(intent *accessv1.MutationIntent) []byte {
	sum := sha256.New()
	writePart := func(part string) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = sum.Write(size[:])
		_, _ = sum.Write([]byte(part))
	}

	writePart(intent.GetDevice().GetDevice().GetId())
	writePart(intent.GetIdempotencyKey())
	writePart(actorPart(intent.GetActor()))
	writePart(intent.GetAccessPolicy().GetKey())
	var policyVersion [8]byte
	binary.BigEndian.PutUint64(policyVersion[:], intent.GetAccessPolicy().GetVersion())
	_, _ = sum.Write(policyVersion[:])
	writePart(intent.GetExpectedFirmwareFingerprint())
	writePart(changePart(intent))

	return sum.Sum(nil)[:intentDigestLen]
}

// actorPart names who asked, with the arm it came from, so an operator subject
// can never project to the same string as a system reason.
func actorPart(actor *accessv1.Actor) string {
	switch {
	case actor.HasOperator():
		return "operator:" + actor.GetOperator().GetSubject()
	case actor.HasSystem():
		return "system:" + actor.GetSystem().GetReason().String()
	default:
		return "none:"
	}
}

// changePart names the typed change, with its arm, so a change arm added later
// cannot project to the empty string the way an unset one does.
func changePart(intent *accessv1.MutationIntent) string {
	switch intent.WhichChange() {
	case accessv1.MutationIntent_InterfaceDescription_case:
		change := intent.GetInterfaceDescription()
		return "interface_description:" + change.GetInterfaceName() + "=" + change.GetDescription()
	default:
		return "none:"
	}
}

func rememberKey(rec *storev1.DeviceLaneRecord, key string, seq uint64, digest []byte) {
	entry := &storev1.IdempotencyEntry{}
	entry.SetIdempotencyKey(key)
	entry.SetSequence(seq)
	entry.SetIntentDigest(digest)
	keys := append(rec.GetIdempotency(), entry)
	if len(keys) > maxIdempotencyKeys {
		keys = keys[len(keys)-maxIdempotencyKeys:]
	}
	rec.SetIdempotency(keys)
}
