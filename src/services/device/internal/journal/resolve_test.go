package journal_test

import (
	"context"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/types/known/timestamppb"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
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
	// The edge confirms the abandonment. Without this the terminal ack is
	// still owed and the resolution is refused, which the test below covers.
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAbandoned, Sequence: seq}); err != nil {
		t.Fatalf("abandoned: %v", err)
	}

	resolved, _, err := j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{Sequence: seq})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := resolved.GetDisposition(); got != accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED {
		t.Errorf("disposition = %v, want the abandonment's own", got)
	}
}

// Resolving an abandonment the edge has not acknowledged is refused. The
// abandonment owes the edge a TerminalResultAck, and closing the record here
// would withdraw that row: the edge would stay parked in its acknowledgement
// wait until its own operation context expired, while central admitted the
// replacement into a lane the old sequence still occupied. Reversing the guard
// makes this test fail and every other resolve test pass, which is how the bug
// survived — the shared fixture built this state and treated it as legal.
func TestResolveDesynchronizationRefusesAnAbandonmentTheEdgeHasNotAcknowledged(t *testing.T) {
	ctx := context.Background()
	j := newJournal(t)
	state, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000a14"), edgeRef())
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

	_, _, err = j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{Sequence: seq})
	if code, _ := errs.CodeOf(err); code != journal.ErrCodeAckPending {
		t.Fatalf("resolve error = %v, want code %v", err, journal.ErrCodeAckPending)
	}
	rec, err := j.Record(ctx, deviceID)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if !rec.HasMutation() {
		t.Fatal("the refused resolution closed the mutation anyway")
	}
	owed := journal.OwedRows(rec, time.Now())
	if len(owed) != 1 || owed[0].Kind != journal.OwedTerminalAck || owed[0].Sequence != seq {
		t.Fatalf("owed = %+v, want the terminal ack for %d", owed, seq)
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

// A key the record already holds from an earlier admission is not this
// resolution's own retry. Echoing that older sequence's state would answer the
// operator with a success while the hold they asked about still stands, so the
// echo is limited to a key admitted past the sequence being resolved — which
// is where a replacement always lands.
func TestAResolutionKeyAdmittedForAnEarlierSequenceIsRefused(t *testing.T) {
	ctx := context.Background()
	j := newJournal(t)
	key := "0192e6a0-0000-7000-8000-000000000a15"

	earlier, err := j.Admit(ctx, deviceID, mutationIntent(key), edgeRef())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := j.ApplyReport(ctx, deviceID, journal.Report{
		Kind:     journal.ReportAdmitted,
		Sequence: earlier.GetSequence(),
	}); err != nil {
		t.Fatalf("admitted: %v", err)
	}
	if _, err := j.Dispose(ctx, deviceID, earlier.GetSequence()); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	if err := j.ApplyReport(ctx, deviceID, journal.Report{
		Kind:     journal.ReportAbandoned,
		Sequence: earlier.GetSequence(),
	}); err != nil {
		t.Fatalf("abandoned: %v", err)
	}

	_, _, err = j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{
		Sequence: earlier.GetSequence(),
		Intent:   mutationIntent(key),
		Edge:     edgeRef(),
	})
	if code, _ := errs.CodeOf(err); code != journal.ErrCodeIdempotencyMismatch {
		t.Fatalf("resolve error = %v, want code %v", err, journal.ErrCodeIdempotencyMismatch)
	}
	rec, err := j.Record(ctx, deviceID)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if !rec.HasMutation() {
		t.Fatal("the refused resolution closed the mutation anyway")
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

// A resubmission after the lane closed has to read how the sequence ended.
// The record keeps only the disposition, on the idempotency entry, and the
// state built from it has to satisfy the schema rule that ties a disposition
// to a terminal phase — a rule nothing enforced here before, because the
// invalid value never reached storage and only ever existed in a response.
func TestResubmissionAfterTheCloseReadsHowTheSequenceEnded(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name  string
		close func(t *testing.T, j *journal.Journal, seq uint64)
		want  accessv1.Disposition
	}{
		{
			name: "verified then released",
			close: func(t *testing.T, j *journal.Journal, seq uint64) {
				for _, kind := range []journal.ReportKind{journal.ReportAdmitted, journal.ReportVerified, journal.ReportReleased} {
					if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: kind, Sequence: seq}); err != nil {
						t.Fatalf("report %v: %v", kind, err)
					}
				}
			},
			want: accessv1.Disposition_DISPOSITION_VERIFIED,
		},
		{
			name: "verified then a refused terminal ack",
			close: func(t *testing.T, j *journal.Journal, seq uint64) {
				for _, kind := range []journal.ReportKind{journal.ReportAdmitted, journal.ReportVerified, journal.ReportRefused} {
					if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: kind, Sequence: seq}); err != nil {
						t.Fatalf("report %v: %v", kind, err)
					}
				}
			},
			want: accessv1.Disposition_DISPOSITION_VERIFIED,
		},
		{
			name: "abandoned before the edge reported it admitted",
			close: func(t *testing.T, j *journal.Journal, seq uint64) {
				if _, err := j.Dispose(ctx, deviceID, seq); err != nil {
					t.Fatalf("dispose: %v", err)
				}
			},
			want: accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED,
		},
		{
			name: "refused by the edge before dispatch",
			close: func(t *testing.T, j *journal.Journal, seq uint64) {
				if _, err := j.RejectDispatch(ctx, deviceID, seq); err != nil {
					t.Fatalf("reject: %v", err)
				}
			},
			want: accessv1.Disposition_DISPOSITION_REJECTED,
		},
		{
			name: "resolved while held",
			close: func(t *testing.T, j *journal.Journal, seq uint64) {
				if _, _, err := j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{Sequence: seq}); err != nil {
					t.Fatalf("resolve: %v", err)
				}
			},
			want: accessv1.Disposition_DISPOSITION_REJECTED,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := newJournal(t)
			intent := mutationIntent("0192e6a0-0000-7000-8000-000000000b01")
			state, err := j.Admit(ctx, deviceID, intent, edgeRef())
			if err != nil {
				t.Fatalf("admit: %v", err)
			}
			c.close(t, j, state.GetSequence())

			again, err := j.Admit(ctx, deviceID, intent, edgeRef())
			if err != nil {
				t.Fatalf("resubmit: %v", err)
			}
			if again.GetSequence() != state.GetSequence() {
				t.Errorf("the resubmission was admitted again, at %d", again.GetSequence())
			}
			if got := again.GetDisposition(); got != c.want {
				t.Errorf("disposition = %v, want %v", got, c.want)
			}
			if err := protovalidate.Validate(again); err != nil {
				t.Errorf("the resubmission's state fails its schema rules: %v", err)
			}
		})
	}
}

// The record keeps no intent past a close, so the state a resubmission reads
// is built from the intent the caller just sent. That is only true while one
// key means one intent, which is the caller's promise and not something
// central can take on trust: a key reused for a different request would
// otherwise be answered with a description of the new intent attached to a
// sequence that did something else.
func TestAKeyReusedForADifferentIntentIsRefused(t *testing.T) {
	ctx := context.Background()
	j := newJournal(t)
	const key = "0192e6a0-0000-7000-8000-000000000c01"

	first, err := j.Admit(ctx, deviceID, mutationIntent(key), edgeRef())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}

	different := mutationIntent(key)
	different.SetExpectedFirmwareFingerprint("fastiron-09.0.10")
	_, err = j.Admit(ctx, deviceID, different, edgeRef())
	if code, _ := errs.CodeOf(err); code != journal.ErrCodeIdempotencyMismatch {
		t.Fatalf("error code = %v, want an idempotency mismatch", code)
	}

	rec, _ := j.Record(ctx, deviceID)
	if rec.GetHighWatermark() != first.GetSequence() {
		t.Errorf("the refused resubmission assigned a sequence: watermark %d", rec.GetHighWatermark())
	}
	if got := rec.GetMutation().GetIntent().GetExpectedFirmwareFingerprint(); got == "fastiron-09.0.10" {
		t.Error("the refused resubmission overwrote the recorded intent")
	}
}

// The same intent under the same key still reads back, which is the point of
// the key.
func TestAKeyResubmittedWithTheSameIntentStillEchoes(t *testing.T) {
	ctx := context.Background()
	j := newJournal(t)
	intent := mutationIntent("0192e6a0-0000-7000-8000-000000000c02")

	first, err := j.Admit(ctx, deviceID, intent, edgeRef())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	again, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000c02"), edgeRef())
	if err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	if again.GetSequence() != first.GetSequence() {
		t.Errorf("resubmission = %d, want the recorded %d", again.GetSequence(), first.GetSequence())
	}
}

// The resolution arms admit through the same door and need the same guard.
func TestAResolutionKeyReusedForADifferentIntentIsRefused(t *testing.T) {
	ctx := context.Background()
	j := newJournal(t)
	const key = "0192e6a0-0000-7000-8000-000000000c03"
	held := heldIntent(t, j, "0192e6a0-0000-7000-8000-000000000c04")

	if _, _, err := j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{
		Sequence: held, Intent: mutationIntent(key), Edge: edgeRef(),
	}); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	different := mutationIntent(key)
	different.SetExpectedFirmwareFingerprint("fastiron-09.0.10")
	_, _, err := j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{
		Sequence: held, Intent: different, Edge: edgeRef(),
	})
	if code, _ := errs.CodeOf(err); code != journal.ErrCodeIdempotencyMismatch {
		t.Fatalf("error code = %v, want an idempotency mismatch", code)
	}
}

// A verified change is what central expects from now on, and the read-back
// that proved it is what the interface last showed. Both land in the write
// that records the verification: without the expectation nothing is managed,
// and the drift poll has no baseline to compare a later read against.
func TestAVerifiedMutationLeavesTheExpectationAndTheObservationBehind(t *testing.T) {
	ctx := context.Background()
	j := newJournal(t)
	state, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000d01"), edgeRef())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	seq := state.GetSequence()
	applied := state.GetIntent().GetInterfaceDescription()
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: seq}); err != nil {
		t.Fatalf("admitted: %v", err)
	}

	proof := completeObservation(applied.GetInterfaceName(), applied.GetDescription())
	if err := j.ApplyReport(ctx, deviceID, journal.Report{
		Kind: journal.ReportVerified, Sequence: seq, Observation: proof,
	}); err != nil {
		t.Fatalf("verified: %v", err)
	}

	rec, _ := j.Record(ctx, deviceID)
	if got := rec.GetExpectedDescriptions()[applied.GetInterfaceName()]; got != applied.GetDescription() {
		t.Errorf("expected description = %q, want what the mutation applied", got)
	}
	if got := rec.GetLastObservations()[applied.GetInterfaceName()]; got == nil {
		t.Error("the read-back that proved the change was not kept")
	} else if got.GetDescription() != applied.GetDescription() {
		t.Errorf("last observation = %q, want the proof", got.GetDescription())
	}
}

// completeObservation is a read-back an edge would report: complete, with the
// provenance a complete observation must carry.
func completeObservation(iface, description string) *accessv1.InterfaceObservation {
	binding := &inventoryv1.BindingLocalRef{}
	binding.SetId("0192e6a0-0000-7000-8000-0000000000b1")
	bindingRef := &inventoryv1.BindingGlobalRef{}
	bindingRef.SetBinding(binding)

	provenance := &inventoryv1.Provenance{}
	provenance.SetBinding(bindingRef)
	provenance.SetObservedAt(timestamppb.New(time.Now()))
	provenance.SetProtocol(inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SSH)
	provenance.SetEdge(edgeRef())
	provenance.SetFirmwareFingerprint("fastiron-08.0.95")

	obs := &accessv1.InterfaceObservation{}
	obs.SetInterfaceName(iface)
	obs.SetDescription(description)
	obs.SetAdminStatus(interfacev1.AdminStatus_ADMIN_STATUS_UP)
	obs.SetOperStatus(interfacev1.OperStatus_OPER_STATUS_UP)
	obs.SetProvenance(provenance)
	obs.SetCompleteness(accessv1.Completeness_COMPLETENESS_COMPLETE)
	return obs
}

// Resolving a mutation the edge holds and has not ended is refused with
// [journal.ErrCodeEdgeHolds], not ack-pending: no acknowledgement is on its
// way, so waiting never clears it. Writing REJECTED here would record that the
// command never reached the device for one that may already have applied, drop
// the edge's own later report as stale, and dispatch the replacement into a
// lane the edge still occupies.
func TestResolveDesynchronizationRefusesAMutationTheEdgeStillHolds(t *testing.T) {
	ctx := context.Background()
	j := newJournal(t)
	state, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000a1a"), edgeRef())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	seq := state.GetSequence()
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: seq}); err != nil {
		t.Fatalf("admitted: %v", err)
	}

	_, _, err = j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{Sequence: seq})
	if code, _ := errs.CodeOf(err); code != journal.ErrCodeEdgeHolds {
		t.Fatalf("resolve error = %v, want code %v", err, journal.ErrCodeEdgeHolds)
	}
	rec, err := j.Record(ctx, deviceID)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if rec.GetMutation().HasDisposition() {
		t.Fatal("the refused resolution disposed the mutation anyway")
	}
}
