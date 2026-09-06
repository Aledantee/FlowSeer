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
	// ErrCodeState is a write invalid for the record's current state, such
	// as admitting over an open mutation.
	ErrCodeState = errs.NewCode("journal/state")
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
		return nil, 0, errs.From(err).Code(ErrCodeConflict).Attr("device", deviceID).Msg("read lane record")
	}
	rec := &storev1.DeviceLaneRecord{}
	if err := proto.Unmarshal(entry.Value(), rec); err != nil {
		return nil, 0, errs.From(err).Code(ErrCodeDecode).Attr("device", deviceID).Msg("decode lane record")
	}
	return rec, entry.Revision(), nil
}

// skip is fn's signal to mutate that no write is needed.
var skip = errors.New("journal: no write needed")

// mutate runs fn against the device's record under compare-and-set,
// retrying on a revision conflict. fn returning skip means "no write is
// needed" and returns the record unchanged.
func (j *Journal) mutate(ctx context.Context, deviceID string, fn func(*storev1.DeviceLaneRecord) error) (*storev1.DeviceLaneRecord, error) {
	for attempt := 0; attempt < casRetries; attempt++ {
		rec, revision, err := j.load(ctx, deviceID)
		if err != nil {
			return nil, err
		}
		if err := fn(rec); err != nil {
			if errors.Is(err, skip) {
				return rec, nil
			}
			return nil, err
		}
		data, err := proto.Marshal(rec)
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeDecode).Attr("device", deviceID).Msg("encode lane record")
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
			return nil, errs.From(err).Code(ErrCodeConflict).Attr("device", deviceID).Msg("write lane record")
		}
		return rec, nil
	}
	return nil, errs.New().Code(ErrCodeConflict).Attr("device", deviceID).Msg("lane record write did not settle")
}

// Admit records a mutation intent at ADMITTED and assigns it the next
// sequence, in one CAS write. A resubmission carrying an idempotency key the
// record already holds returns the recorded state without admitting again.
func (j *Journal) Admit(ctx context.Context, deviceID string, intent *accessv1.MutationIntent, edge *edgev1.EdgeGlobalRef) (*accessv1.MutationState, error) {
	var state *accessv1.MutationState
	_, err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		ensureDevice(rec, intent.GetDevice())
		if seq, ok := recordedSequence(rec, intent.GetIdempotencyKey()); ok {
			state = recordedState(rec, seq)
			return skip
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
// sequence and writes nothing.
func (j *Journal) OpenRead(ctx context.Context, deviceID, iface string, read *accessv1.TypedRead, idempotencyKey string, deadline time.Time) (uint64, error) {
	var seq uint64
	_, err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		reads := rec.GetOpenReads()
		if reads == nil {
			reads = map[string]*storev1.OpenRead{}
		}
		if existing, ok := reads[iface]; ok && !existing.HasOutcome() {
			seq = existing.GetSequence()
			return skip
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
// once its waiter has read it. Exactly one of obs or errPayload is set.
func (j *Journal) CloseRead(ctx context.Context, deviceID, iface string, obs *accessv1.InterfaceObservation, errPayload *errsv1.ErrorPayload) error {
	_, err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		reads := rec.GetOpenReads()
		entry, ok := reads[iface]
		if !ok {
			return skip
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
	// ReportAbandoned confirms an abandonment; the mutation stays held.
	ReportAbandoned
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

// ApplyReport applies an edge report in phase order, scoped to the open
// mutation's sequence. A report for another sequence, or a duplicate that
// finds the state already advanced, is ignored.
func (j *Journal) ApplyReport(ctx context.Context, deviceID string, rep Report) error {
	_, err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		m := rec.GetMutation()
		if m == nil || m.GetSequence() != rep.Sequence {
			return skip // stale, or for a sequence no longer open
		}
		switch rep.Kind {
		case ReportAdmitted:
			if rec.GetDispatched() {
				return skip
			}
			rec.SetDispatched(true)
			rec.SetDispatchConfirmed(true)
			m.SetPhase(accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED)
			rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
		case ReportVerified:
			m.SetDisposition(accessv1.Disposition_DISPOSITION_VERIFIED)
			m.SetPhase(accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED)
			rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_VERIFIED)
		case ReportRecovering:
			m.SetBlockReason(accessv1.BlockReason_BLOCK_REASON_INDETERMINATE)
			m.SetBlockedSince(timestamppb.New(j.clock()))
			rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_RECOVERING)
		case ReportReleased:
			clearMutation(rec)
		case ReportAbandoned:
			rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_ABANDONED)
		case ReportError:
			if rep.Submitted {
				// The command was sent; the effect is unknown, so recovery,
				// never a rejection.
				m.SetBlockReason(accessv1.BlockReason_BLOCK_REASON_INDETERMINATE)
				m.SetBlockedSince(timestamppb.New(j.clock()))
				rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_RECOVERING)
				return nil
			}
			if !rec.GetDispatched() {
				clearMutation(rec) // never reached the device; close it
				return nil
			}
			m.SetDisposition(accessv1.Disposition_DISPOSITION_REJECTED)
			m.SetPhase(accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED)
		}
		return nil
	})
	return err
}

// ConfirmCheckpoint records the edge's CheckpointAck.
func (j *Journal) ConfirmCheckpoint(ctx context.Context, deviceID string, sequence uint64) error {
	return j.mutateMutation(ctx, deviceID, sequence, func(rec *storev1.DeviceLaneRecord, _ *accessv1.MutationState) {
		rec.SetCheckpointConfirmed(true)
	})
}

// ConfirmHoldResolved clears a hold-resolution row the edge acknowledged.
func (j *Journal) ConfirmHoldResolved(ctx context.Context, deviceID string, sequence uint64) error {
	_, err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		if !rec.HasHoldResolutionPending() || rec.GetHoldResolutionPending() != sequence {
			return skip
		}
		rec.ClearHoldResolutionPending()
		return nil
	})
	return err
}

// Dispose is AbandonMutation: it writes the mutation ABANDONED with a
// recovery hold at once, the response the API requires. The edge's own
// abandonment follows the terminal ack.
func (j *Journal) Dispose(ctx context.Context, deviceID string, sequence uint64) (*accessv1.MutationState, error) {
	var state *accessv1.MutationState
	_, err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		m := rec.GetMutation()
		if m == nil || m.GetSequence() != sequence {
			return errs.New().Code(ErrCodeState).Attr("device", deviceID).Msg("no open mutation at that sequence to abandon")
		}
		if m.HasDisposition() {
			return errs.New().Code(ErrCodeState).Attr("device", deviceID).Msg("mutation is already terminal")
		}
		m.SetPhase(accessv1.OperationPhase_OPERATION_PHASE_ABANDONED)
		m.SetDisposition(accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED)
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

// ResolveHold records that an abandoned or desynchronized sequence's hold is
// resolved, so the owed-row derivation sends HoldResolved and the edge's ack
// clears it. It clears a held mutation at that sequence so the lane is free
// for the resolution's own intent.
func (j *Journal) ResolveHold(ctx context.Context, deviceID string, sequence uint64) error {
	_, err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		rec.SetHoldResolutionPending(sequence)
		if m := rec.GetMutation(); m != nil && m.GetSequence() == sequence {
			clearMutation(rec)
		}
		return nil
	})
	return err
}

// SetExpected records the description central expects on one interface, the
// baseline the drift poll compares against.
func (j *Journal) SetExpected(ctx context.Context, deviceID, iface, description string) error {
	_, err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
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
func (j *Journal) SetFingerprint(ctx context.Context, deviceID, fingerprint string) error {
	_, err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		if rec.GetFirmwareFingerprint() == fingerprint {
			return skip
		}
		rec.SetFirmwareFingerprint(fingerprint)
		return nil
	})
	return err
}

// mutateMutation applies fn only when the record's open mutation matches
// sequence.
func (j *Journal) mutateMutation(ctx context.Context, deviceID string, sequence uint64, fn func(*storev1.DeviceLaneRecord, *accessv1.MutationState)) error {
	_, err := j.mutate(ctx, deviceID, func(rec *storev1.DeviceLaneRecord) error {
		m := rec.GetMutation()
		if m == nil || m.GetSequence() != sequence {
			return skip
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
	// The mutation has closed; report the sequence it was admitted at.
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
