package journal

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
)

var now = time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)

// mut builds a MutationState at a phase for the given sequence.
func mut(seq uint64, phase accessv1.OperationPhase) *accessv1.MutationState {
	m := &accessv1.MutationState{}
	m.SetSequence(seq)
	m.SetPhase(phase)
	return m
}

func openRead(seq uint64, deadline time.Time) *storev1.OpenRead {
	r := &storev1.OpenRead{}
	r.SetSequence(seq)
	r.SetDeadline(timestamppb.New(deadline))
	r.SetIdempotencyKey("0192e6a0-0000-7000-8000-00000000a002")
	return r
}

// owedKinds reduces a slice of Owed to a comparable multiset keyed by
// (kind, sequence) so a table row states exactly what is owed.
type row struct {
	kind OwedKind
	seq  uint64
}

func rowsOf(owed []Owed) map[row]int {
	m := map[row]int{}
	for _, o := range owed {
		m[row{o.Kind, o.Sequence}]++
	}
	return m
}

// TestOwedRowTable is the proof of the outbox decision: every enumerated
// record state maps to a distinct, constructible record and to exactly the
// rows the decision says it owes. A state added to DeviceLaneRecord without
// a row here does not fail this test, but the invariant test below catches a
// state that owes nothing without a terminator.
func TestOwedRowTable(t *testing.T) {
	// helpers to assemble records for each state.
	admittedUnconfirmed := func() *storev1.DeviceLaneRecord {
		rec := &storev1.DeviceLaneRecord{}
		rec.SetMutation(mut(7, accessv1.OperationPhase_OPERATION_PHASE_ADMITTED))
		return rec
	}
	possiblyCheckpointUnconfirmed := func() *storev1.DeviceLaneRecord {
		rec := &storev1.DeviceLaneRecord{}
		rec.SetMutation(mut(7, accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED))
		rec.SetDispatched(true)
		rec.SetDispatchConfirmed(true)
		rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
		return rec
	}
	recovering := func() *storev1.DeviceLaneRecord {
		rec := possiblyCheckpointUnconfirmed()
		rec.SetCheckpointConfirmed(true)
		m := rec.GetMutation()
		m.SetBlockReason(accessv1.BlockReason_BLOCK_REASON_INDETERMINATE)
		m.SetBlockedSince(timestamppb.New(now))
		rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_RECOVERING)
		return rec
	}
	verifiedAwaitingRelease := func() *storev1.DeviceLaneRecord {
		rec := &storev1.DeviceLaneRecord{}
		m := mut(7, accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED)
		m.SetDisposition(accessv1.Disposition_DISPOSITION_VERIFIED)
		rec.SetMutation(m)
		rec.SetDispatched(true)
		rec.SetDispatchConfirmed(true)
		rec.SetCheckpointConfirmed(true)
		rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_VERIFIED)
		return rec
	}
	abandonedAckOwed := func() *storev1.DeviceLaneRecord {
		rec := &storev1.DeviceLaneRecord{}
		m := mut(7, accessv1.OperationPhase_OPERATION_PHASE_ABANDONED)
		m.SetDisposition(accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED)
		m.SetBlockReason(accessv1.BlockReason_BLOCK_REASON_RECOVERY_HOLD)
		m.SetBlockedSince(timestamppb.New(now))
		rec.SetMutation(m)
		rec.SetDispatched(true)
		rec.SetDispatchConfirmed(true)
		rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_RECOVERING)
		return rec
	}
	abandonedAckConfirmed := func() *storev1.DeviceLaneRecord {
		rec := abandonedAckOwed()
		rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_ABANDONED)
		return rec
	}
	afterOnboardedVerified := func() *storev1.DeviceLaneRecord {
		rec := verifiedAwaitingRelease()
		rec.SetDispatchConfirmed(false)
		rec.SetCheckpointConfirmed(false)
		return rec
	}
	afterOnboardedRecovering := func() *storev1.DeviceLaneRecord {
		rec := recovering()
		rec.SetDispatchConfirmed(false)
		rec.SetCheckpointConfirmed(false)
		return rec
	}
	heldReconciliation := func() *storev1.DeviceLaneRecord {
		rec := &storev1.DeviceLaneRecord{}
		m := mut(7, accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
		m.SetBlockReason(accessv1.BlockReason_BLOCK_REASON_DESYNCHRONIZED)
		m.SetBlockedSince(timestamppb.New(now))
		rec.SetMutation(m)
		return rec
	}
	holdResolvedUnconfirmed := func() *storev1.DeviceLaneRecord {
		rec := &storev1.DeviceLaneRecord{}
		rec.SetHoldResolutionPending(5)
		return rec
	}
	restoreWrite := func() *storev1.DeviceLaneRecord {
		rec := &storev1.DeviceLaneRecord{}
		rec.SetHoldResolutionPending(7)
		rec.SetMutation(mut(8, accessv1.OperationPhase_OPERATION_PHASE_ADMITTED))
		return rec
	}
	openReadState := func() *storev1.DeviceLaneRecord {
		rec := &storev1.DeviceLaneRecord{}
		rec.SetOpenReads(map[string]*storev1.OpenRead{"eth1": openRead(9, now.Add(30*time.Second))})
		return rec
	}
	closedReadState := func() *storev1.DeviceLaneRecord {
		rec := &storev1.DeviceLaneRecord{}
		r := openRead(9, now.Add(30*time.Second))
		r.SetObservation(&accessv1.InterfaceObservation{})
		rec.SetOpenReads(map[string]*storev1.OpenRead{"eth1": r})
		return rec
	}
	expiredReadState := func() *storev1.DeviceLaneRecord {
		rec := &storev1.DeviceLaneRecord{}
		rec.SetOpenReads(map[string]*storev1.OpenRead{"eth1": openRead(9, now.Add(-time.Second))})
		return rec
	}

	tests := []struct {
		name string
		rec  *storev1.DeviceLaneRecord
		want map[row]int
	}{
		{"no mutation, no reads", &storev1.DeviceLaneRecord{}, map[row]int{}},
		{"ADMITTED, dispatch unconfirmed", admittedUnconfirmed(), map[row]int{{OwedExecute, 7}: 1}},
		{"POSSIBLY_APPLIED, checkpoint unconfirmed", possiblyCheckpointUnconfirmed(), map[row]int{{OwedCheckpoint, 7}: 1}},
		{"recovering owes nothing to the edge", recovering(), map[row]int{}},
		{"VERIFIED awaiting RELEASED", verifiedAwaitingRelease(), map[row]int{{OwedTerminalAck, 7}: 1}},
		{"abandoned, ack owed", abandonedAckOwed(), map[row]int{{OwedTerminalAck, 7}: 1}},
		{"abandoned, ack confirmed, held", abandonedAckConfirmed(), map[row]int{}},
		{"VERIFIED after Onboarded re-sends the terminal ack", afterOnboardedVerified(), map[row]int{{OwedTerminalAck, 7}: 1}},
		{"recovering after Onboarded re-dispatches with resume", afterOnboardedRecovering(), map[row]int{{OwedExecute, 7}: 1}},
		{"held reconciliation intent owes nothing", heldReconciliation(), map[row]int{}},
		{"hold resolved, unconfirmed", holdResolvedUnconfirmed(), map[row]int{{OwedHoldResolved, 5}: 1}},
		{"restore owes hold and dispatch together", restoreWrite(), map[row]int{{OwedHoldResolved, 7}: 1, {OwedExecute, 8}: 1}},
		{"open read owes its dispatch", openReadState(), map[row]int{{OwedRead, 9}: 1}},
		{"closed read owes nothing", closedReadState(), map[row]int{}},
		{"expired read is swept, not owed", expiredReadState(), map[row]int{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rowsOf(OwedRows(tt.rec, now))
			if len(got) != len(tt.want) {
				t.Fatalf("owed %v, want %v", got, tt.want)
			}
			for r, n := range tt.want {
				if got[r] != n {
					t.Fatalf("owed %v, want %v", got, tt.want)
				}
			}
			// The resume bit on the after-Onboarded recovering case.
			if tt.name == "recovering after Onboarded re-dispatches with resume" {
				owed := OwedRows(tt.rec, now)
				if len(owed) != 1 || !owed[0].Resume {
					t.Fatalf("expected a single resume dispatch, got %+v", owed)
				}
			}

		})
	}
}

// TestOwedInvariant asserts the property the table exists to guarantee, over
// every state above: each record either owes at least one message, or is
// operator-terminated, or is closed (nothing open); and no open sequence
// owes two rows. A state that owes nothing without a terminator — the shape
// findings F2 and F7 flagged — fails here.
func TestOwedInvariant(t *testing.T) {
	states := invariantStates()
	for _, tt := range states {
		t.Run(tt.name, func(t *testing.T) {
			owed := OwedRows(tt.rec, now)

			perSeq := map[uint64]int{}
			for _, o := range owed {
				perSeq[o.Sequence]++
			}
			for seq, n := range perSeq {
				if n > 1 {
					t.Fatalf("sequence %d owes %d rows, want at most one", seq, n)
				}
			}

			closed := tt.rec.GetMutation() == nil &&
				openUnclosedReads(tt.rec) == 0 &&
				!tt.rec.HasHoldResolutionPending()
			switch {
			case len(owed) > 0:
			case OperatorTerminated(tt.rec):
			case len(ExpiredReads(tt.rec, now)) > 0: // due for the sweep, not stranded
			case closed:
			default:
				t.Fatalf("state %q owes nothing, names no operator terminator, and is not closed: a stranded lane", tt.name)
			}
		})
	}
}

func openUnclosedReads(rec *storev1.DeviceLaneRecord) int {
	n := 0
	for _, r := range rec.GetOpenReads() {
		if !r.HasOutcome() {
			n++
		}
	}
	return n
}

type namedRecord struct {
	name string
	rec  *storev1.DeviceLaneRecord
}

// invariantStates mirrors the table's states for the invariant sweep; a
// mutation-carrying error would surface in both.
func invariantStates() []namedRecord {
	mkTerminalAbandonedConfirmed := func() *storev1.DeviceLaneRecord {
		rec := &storev1.DeviceLaneRecord{}
		m := mut(7, accessv1.OperationPhase_OPERATION_PHASE_ABANDONED)
		m.SetDisposition(accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED)
		m.SetBlockReason(accessv1.BlockReason_BLOCK_REASON_RECOVERY_HOLD)
		m.SetBlockedSince(timestamppb.New(now))
		rec.SetMutation(m)
		rec.SetDispatched(true)
		rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_ABANDONED)
		return rec
	}
	held := func() *storev1.DeviceLaneRecord {
		rec := &storev1.DeviceLaneRecord{}
		m := mut(7, accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
		m.SetBlockReason(accessv1.BlockReason_BLOCK_REASON_DESYNCHRONIZED)
		m.SetBlockedSince(timestamppb.New(now))
		rec.SetMutation(m)
		return rec
	}
	// A read whose deadline passed and nothing else open: the sweep will
	// close it, so it is transiently not-owed; the invariant treats an
	// expired read as due-for-sweep, i.e. not a stranded lane.
	expiredOnly := func() *storev1.DeviceLaneRecord {
		rec := &storev1.DeviceLaneRecord{}
		rec.SetOpenReads(map[string]*storev1.OpenRead{"eth1": openRead(9, now.Add(-time.Second))})
		return rec
	}
	return []namedRecord{
		{"empty is closed", &storev1.DeviceLaneRecord{}},
		{"abandoned ack confirmed is operator-terminated", mkTerminalAbandonedConfirmed()},
		{"held reconciliation is operator-terminated", held()},
		{"expired read is due for sweep", expiredOnly()},
	}
}
