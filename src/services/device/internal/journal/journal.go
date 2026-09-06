package journal

import (
	"context"
	"errors"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	errsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/errs/v1"
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
	// ErrCodeDecode is a stored record that will not unmarshal.
	ErrCodeDecode = errs.NewCode("journal/decode")
)

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
			return errs.From(err).Code(ErrCodeConflict).Attr("device", deviceID).Msg("write lane record")
		}
		return nil
	}
	return errs.New().Code(ErrCodeConflict).Attr("device", deviceID).Msg("lane record write did not settle")
}

// Admit records a mutation intent at ADMITTED and assigns it the next
// sequence, in one CAS write. A resubmission carrying an idempotency key the
// record already holds returns the recorded state without admitting again.
func (j *Journal) Admit(ctx context.Context, deviceID string, intent *accessv1.MutationIntent, edge *edgev1.EdgeGlobalRef) (*accessv1.MutationState, error) {
	var state *accessv1.MutationState
	err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		ensureDevice(rec, intent.GetDevice())
		if seq, ok := recordedSequence(rec, intent.GetIdempotencyKey()); ok {
			state = recordedState(rec, seq)
			return errSkip
		}
		if rec.HasMutation() {
			return errs.New().Code(ErrCodeState).Attr("device", deviceID).Msg("a mutation already holds this device's lane")
		}
		seq := rec.GetHighWatermark() + 1
		rec.SetHighWatermark(seq)
		m := &accessv1.MutationState{}
		m.SetIntent(intent)
		m.SetSequence(seq)
		m.SetPhase(accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
		m.SetResponsibleEdge(edge)
		rec.SetMutation(m)
		rec.SetAdmittedAt(timestamppb.New(j.clock()))
		rememberKey(rec, intent.GetIdempotencyKey(), seq)
		state = m
		return nil
	})
	if err != nil {
		return nil, err
	}
	return state, nil
}

// OpenRead assigns a read the next sequence and records it, unless the
// interface already has an open read, in which case it returns that read's
// sequence and writes nothing. device names the record's device so a read
// that arrives before the first mutation still writes a valid record.
func (j *Journal) OpenRead(ctx context.Context, deviceID string, device *inventoryv1.DeviceGlobalRef, iface string, read *accessv1.TypedRead, idempotencyKey string, deadline time.Time) (uint64, error) {
	var seq uint64
	err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		ensureDevice(rec, device)
		reads := rec.GetOpenReads()
		if reads == nil {
			reads = map[string]*storev1.OpenRead{}
		}
		if existing, ok := reads[iface]; ok && !existing.HasOutcome() {
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
		case ReportRecovering:
			m.SetBlockReason(accessv1.BlockReason_BLOCK_REASON_INDETERMINATE)
			m.SetBlockedSince(timestamppb.New(j.clock()))
			rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_RECOVERING)
		case ReportReleased, ReportRefused:
			// A released disposition, or a refused terminal ack, frees the
			// lane the same way; the edge's RELEASED report is the barrier.
			clearMutation(rec)
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

// ConfirmCheckpoint records the edge's CheckpointAck.
func (j *Journal) ConfirmCheckpoint(ctx context.Context, deviceID string, sequence uint64) error {
	return j.mutateMutation(ctx, deviceID, sequence, func(rec *storev1.DeviceLaneRecord, _ *accessv1.MutationState) {
		rec.SetCheckpointConfirmed(true)
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
			clearMutation(rec)
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
			clearMutation(rec) // never reached the device: free the lane at once
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return state, nil
}

// ResolveHold records that an abandoned or desynchronized sequence's hold is
// resolved, so the owed-row derivation sends HoldResolved and the edge's ack
// clears it. It clears a held mutation at that sequence so the lane is free
// for the resolution's own intent.
func (j *Journal) ResolveHold(ctx context.Context, deviceID string, sequence uint64) error {
	return j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		if err := addHold(rec, deviceID, sequence); err != nil {
			return err
		}
		if m := rec.GetMutation(); m != nil && m.GetSequence() == sequence {
			clearMutation(rec)
		}
		return nil
	})
}

// SetExpected records the description central expects on one interface, the
// baseline the drift poll compares against.
func (j *Journal) SetExpected(ctx context.Context, deviceID string, device *inventoryv1.DeviceGlobalRef, iface, description string) error {
	err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		ensureDevice(rec, device)
		expected := rec.GetExpectedDescriptions()
		if expected == nil {
			expected = map[string]string{}
		}
		expected[iface] = description
		rec.SetExpectedDescriptions(expected)
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
		ensureDevice(rec, device)
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
	holds := rec.GetHoldResolutionPending()
	for _, s := range holds {
		if s == sequence {
			return nil
		}
	}
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

func ensureDevice(rec *storev1.DeviceLaneRecord, device *inventoryv1.DeviceGlobalRef) {
	if !rec.HasDevice() {
		rec.SetDevice(device)
	}
}

func recordedSequence(rec *storev1.DeviceLaneRecord, key string) (uint64, bool) {
	for _, entry := range rec.GetIdempotency() {
		if entry.GetIdempotencyKey() == key {
			return entry.GetSequence(), true
		}
	}
	return 0, false
}

func recordedState(rec *storev1.DeviceLaneRecord, seq uint64) *accessv1.MutationState {
	if m := rec.GetMutation(); m != nil && m.GetSequence() == seq {
		return m
	}
	// The mutation has closed and the record no longer holds its disposition or
	// intent. A resubmission after the close learns only that the sequence
	// reached a terminal state, reported as RELEASED with its sequence; the
	// terminal disposition is not retained past the close.
	m := &accessv1.MutationState{}
	m.SetSequence(seq)
	m.SetPhase(accessv1.OperationPhase_OPERATION_PHASE_RELEASED)
	return m
}

func rememberKey(rec *storev1.DeviceLaneRecord, key string, seq uint64) {
	entry := &storev1.IdempotencyEntry{}
	entry.SetIdempotencyKey(key)
	entry.SetSequence(seq)
	keys := append(rec.GetIdempotency(), entry)
	if len(keys) > maxIdempotencyKeys {
		keys = keys[len(keys)-maxIdempotencyKeys:]
	}
	rec.SetIdempotency(keys)
}
