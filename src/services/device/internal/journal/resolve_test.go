package journal_test

import (
	"context"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"github.com/nats-io/nats.go/jetstream"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
)

const resolveIface = "ethernet 1/1/2"

// heldIntent leaves the record in the state the drift poll leaves it in: an
// intent admitted and held, never dispatched, waiting for the operator to say
// what to do about the difference it found.
func heldIntent(t *testing.T, j *journal.Journal, key string) uint64 {
	t.Helper()
	state, err := j.Admit(context.Background(), deviceID, mutationIntent(key), edgeRef())
	if err != nil {
		t.Fatalf("admit held intent: %v", err)
	}
	return state.GetSequence()
}

func revisionOf(t *testing.T, kv jetstream.KeyValue) uint64 {
	t.Helper()
	entry, err := kv.Get(context.Background(), deviceID)
	if err != nil {
		t.Fatalf("read revision: %v", err)
	}
	return entry.Revision()
}

// The restore arm resolves and admits in one write. Two writes would leave a
// state between them with the lane free, the hold resolved, and no intent
// recorded — an operator told their expected state is being restored while the
// record says nobody is restoring it.
func TestResolveDesynchronizationRestoresInOneWrite(t *testing.T) {
	ctx := context.Background()
	j, kv := newJournalKV(t)
	held := heldIntent(t, j, "0192e6a0-0000-7000-8000-000000000a01")
	before := revisionOf(t, kv)

	resolved, admitted, err := j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{
		Sequence: held,
		Intent:   mutationIntent("0192e6a0-0000-7000-8000-000000000a02"),
		Edge:     edgeRef(),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if got := revisionOf(t, kv) - before; got != 1 {
		t.Errorf("the resolution took %d writes, want 1", got)
	}
	if resolved.GetSequence() != held {
		t.Errorf("resolved sequence = %d, want %d", resolved.GetSequence(), held)
	}
	if admitted.GetSequence() != held+1 {
		t.Errorf("admitted sequence = %d, want %d", admitted.GetSequence(), held+1)
	}
	rec, _ := j.Record(ctx, deviceID)
	if rec.GetMutation().GetSequence() != admitted.GetSequence() {
		t.Errorf("the lane holds %d, want the admitted %d", rec.GetMutation().GetSequence(), admitted.GetSequence())
	}
	if !holdPending(rec, held) {
		t.Errorf("pending holds = %v, want the resolved %d", rec.GetHoldResolutionPending(), held)
	}
}

// An intent held at ADMITTED never reached the device, so it is disposed
// REJECTED and released — never abandoned, which would claim the device might
// carry it. The phase moves with the disposition in the one write, so the
// state the record holds and the state the caller reads both satisfy the
// schema rule that ties the two together.
func TestResolveDesynchronizationDisposesAHeldIntentRejected(t *testing.T) {
	ctx := context.Background()
	j := newJournal(t)
	held := heldIntent(t, j, "0192e6a0-0000-7000-8000-000000000a03")

	resolved, admitted, err := j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{
		Sequence:  held,
		Interface: resolveIface,
		Expected:  "what the device actually carries",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if admitted != nil {
		t.Errorf("the accept arm admitted %d", admitted.GetSequence())
	}
	if got := resolved.GetDisposition(); got != accessv1.Disposition_DISPOSITION_REJECTED {
		t.Errorf("disposition = %v, want rejected", got)
	}
	if got := resolved.GetPhase(); got != accessv1.OperationPhase_OPERATION_PHASE_RELEASED {
		t.Errorf("phase = %v, want released", got)
	}
	if err := protovalidate.Validate(resolved); err != nil {
		t.Errorf("the disposed state fails its schema rules: %v", err)
	}
	rec, _ := j.Record(ctx, deviceID)
	if rec.HasMutation() {
		t.Error("the resolution left the lane held")
	}
	if got := rec.GetExpectedDescriptions()[resolveIface]; got != "what the device actually carries" {
		t.Errorf("expected description = %q, want the adopted one", got)
	}
}

// An abandoned sequence is already terminal: the resolution ends its hold and
// leaves its disposition alone, because that disposition is the record of what
// central could not establish about the device.
func TestResolveDesynchronizationKeepsAnAbandonedDisposition(t *testing.T) {
	ctx := context.Background()
	j := newJournal(t)
	state, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000a04"), edgeRef())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	seq := state.GetSequence()
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: seq}); err != nil {
		t.Fatalf("admitted: %v", err)
	}
	if _, err := j.Dispose(ctx, deviceID, seq); err != nil {
		t.Fatalf("dispose: %v", err)
	}

	resolved, _, err := j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{Sequence: seq})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := resolved.GetDisposition(); got != accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED {
		t.Errorf("disposition = %v, want the abandonment's own", got)
	}
}

// The resolved sequence owes exactly its hold-resolved row and nothing else:
// the record never carries a state between the disposal and the admission, so
// the owed-row derivation has nothing to say about one.
func TestResolveDesynchronizationOwesOnlyTheHoldAndTheNewIntent(t *testing.T) {
	ctx := context.Background()
	j := newJournal(t)
	held := heldIntent(t, j, "0192e6a0-0000-7000-8000-000000000a05")

	_, admitted, err := j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{
		Sequence: held,
		Intent:   mutationIntent("0192e6a0-0000-7000-8000-000000000a06"),
		Edge:     edgeRef(),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	rec, _ := j.Record(ctx, deviceID)
	want := map[journal.OwedKind]uint64{
		journal.OwedHoldResolved: held,
		journal.OwedExecute:      admitted.GetSequence(),
	}
	got := map[journal.OwedKind]uint64{}
	for _, owed := range journal.OwedRows(rec, time.Now()) {
		if prior, dup := got[owed.Kind]; dup {
			t.Fatalf("kind %d owed twice, at %d and %d", owed.Kind, prior, owed.Sequence)
		}
		got[owed.Kind] = owed.Sequence
	}
	if len(got) != len(want) {
		t.Fatalf("owed rows = %v, want %v", got, want)
	}
	for kind, seq := range want {
		if got[kind] != seq {
			t.Errorf("kind %d owed at %d, want %d", kind, got[kind], seq)
		}
	}
}

// A lost response is retried, not resolved twice: the retry finds its
// idempotency key recorded and writes nothing.
func TestResolveDesynchronizationIsIdempotent(t *testing.T) {
	ctx := context.Background()
	j, kv := newJournalKV(t)
	held := heldIntent(t, j, "0192e6a0-0000-7000-8000-000000000a07")
	intent := mutationIntent("0192e6a0-0000-7000-8000-000000000a08")

	_, first, err := j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{
		Sequence: held, Intent: intent, Edge: edgeRef(),
	})
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	after := revisionOf(t, kv)

	_, again, err := j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{
		Sequence: held, Intent: intent, Edge: edgeRef(),
	})
	if err != nil {
		t.Fatalf("repeated resolve: %v", err)
	}
	if again.GetSequence() != first.GetSequence() {
		t.Errorf("the retry admitted %d, want the recorded %d", again.GetSequence(), first.GetSequence())
	}
	if got := revisionOf(t, kv); got != after {
		t.Errorf("the retry wrote (revision %d, was %d)", got, after)
	}
}

// A refusal leaves nothing half-done. The full hold set is the refusal that
// can arrive after the disposal is decided, so it is the one that proves the
// write is all or nothing.
func TestResolveDesynchronizationRefusedLeavesTheRecordUntouched(t *testing.T) {
	ctx := context.Background()
	j := newJournal(t)
	for s := range 64 {
		pendingHold(t, j, holdKey(s))
	}
	held := heldIntent(t, j, "0192e6a0-0000-7000-8000-000000000a09")
	before, _ := j.Record(ctx, deviceID)

	_, admitted, err := j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{
		Sequence: held,
		Intent:   mutationIntent("0192e6a0-0000-7000-8000-000000000a10"),
		Edge:     edgeRef(),
	})
	if code, _ := errs.CodeOf(err); code != journal.ErrCodeHoldsFull {
		t.Fatalf("error code = %v, want holds-full", code)
	}
	if admitted != nil {
		t.Error("a refused resolution admitted the intent anyway")
	}
	rec, _ := j.Record(ctx, deviceID)
	if rec.GetHighWatermark() != before.GetHighWatermark() {
		t.Errorf("high watermark moved to %d, was %d", rec.GetHighWatermark(), before.GetHighWatermark())
	}
	m := rec.GetMutation()
	if m == nil || m.GetSequence() != held || m.HasDisposition() {
		t.Errorf("the held intent was disposed anyway: %v", m)
	}
}

func TestResolveDesynchronizationRefusesASequenceNothingHolds(t *testing.T) {
	ctx := context.Background()
	j := newJournal(t)
	held := heldIntent(t, j, "0192e6a0-0000-7000-8000-000000000a11")

	// A sequence the record neither holds nor owes a hold for: resolving it
	// would be a way to admit an intent past the lane's one-mutation rule.
	_, _, err := j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{
		Sequence: held + 7,
		Intent:   mutationIntent("0192e6a0-0000-7000-8000-000000000a12"),
		Edge:     edgeRef(),
	})
	if code, _ := errs.CodeOf(err); code != journal.ErrCodeState {
		t.Fatalf("error code = %v, want state", code)
	}
	rec, _ := j.Record(ctx, deviceID)
	if rec.GetMutation().GetSequence() != held {
		t.Error("the refusal disturbed the held mutation")
	}
}
