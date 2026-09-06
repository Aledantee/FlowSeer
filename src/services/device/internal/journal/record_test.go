package journal

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
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

// orow is a comparable projection of an Owed row that keeps every field the
// row exists to carry — kind, sequence, and the Resume and Disposition
// payloads — so a table row states exactly what is owed and a regression in
// either payload fails rather than passing on a bare (kind, sequence) match.
type orow struct {
	kind   OwedKind
	seq    uint64
	resume bool
	disp   accessv1.Disposition
}

func rowsOf(owed []Owed) map[orow]int {
	m := map[orow]int{}
	for _, o := range owed {
		m[orow{o.Kind, o.Sequence, o.Resume, o.Disposition}]++
	}
	return m
}

func wantRows(os ...Owed) map[orow]int { return rowsOf(os) }

// laneState is one enumerated lane record: a distinct, constructible state and
// the exact rows it owes. It is the single table both TestOwedRowTable and
// TestOwedNoLaneStranded run over, so the rows and the invariant cannot drift
// apart or be asserted over disjoint state sets.
type laneState struct {
	name string
	rec  *storev1.DeviceLaneRecord
	want map[orow]int
}

func laneStates() []laneState {
	admittedUnconfirmed := func() *storev1.DeviceLaneRecord {
		rec := withAdmission(&storev1.DeviceLaneRecord{})
		rec.SetMutation(mut(7, accessv1.OperationPhase_OPERATION_PHASE_ADMITTED))
		return rec
	}
	possiblyCheckpointUnconfirmed := func() *storev1.DeviceLaneRecord {
		rec := withAdmission(&storev1.DeviceLaneRecord{})
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
	terminalAwaitingRelease := func(disp accessv1.Disposition) *storev1.DeviceLaneRecord {
		rec := withAdmission(&storev1.DeviceLaneRecord{})
		m := mut(7, accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED)
		m.SetDisposition(disp)
		rec.SetMutation(m)
		rec.SetDispatched(true)
		rec.SetDispatchConfirmed(true)
		rec.SetCheckpointConfirmed(true)
		rec.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_VERIFIED)
		return rec
	}
	abandonedAckOwed := func() *storev1.DeviceLaneRecord {
		rec := withAdmission(&storev1.DeviceLaneRecord{})
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
	afterOnboarded := func(rec *storev1.DeviceLaneRecord) *storev1.DeviceLaneRecord {
		rec.SetDispatchConfirmed(false)
		rec.SetCheckpointConfirmed(false)
		return rec
	}
	heldReconciliation := func() *storev1.DeviceLaneRecord {
		rec := withAdmission(&storev1.DeviceLaneRecord{})
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
	abandonBeforeDispatch := func() *storev1.DeviceLaneRecord {
		// The state Dispose leaves when it abandons a mutation the edge never
		// reported admitted: the lane is closed and a hold-resolved row
		// releases the edge, which may already hold the ExecuteRequest.
		rec := &storev1.DeviceLaneRecord{}
		rec.SetHoldResolutionPending(7)
		return rec
	}
	restoreWrite := func() *storev1.DeviceLaneRecord {
		rec := withAdmission(&storev1.DeviceLaneRecord{})
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

	ack := func(disp accessv1.Disposition) Owed {
		return Owed{Kind: OwedTerminalAck, Sequence: 7, Disposition: disp}
	}
	return []laneState{
		{"no mutation, no reads", &storev1.DeviceLaneRecord{}, wantRows()},
		{"ADMITTED, dispatch unconfirmed", admittedUnconfirmed(), wantRows(Owed{Kind: OwedExecute, Sequence: 7})},
		{"POSSIBLY_APPLIED, checkpoint unconfirmed", possiblyCheckpointUnconfirmed(), wantRows(Owed{Kind: OwedCheckpoint, Sequence: 7})},
		{"recovering owes nothing to the edge", recovering(), wantRows()},
		{"VERIFIED awaiting RELEASED", terminalAwaitingRelease(accessv1.Disposition_DISPOSITION_VERIFIED), wantRows(ack(accessv1.Disposition_DISPOSITION_VERIFIED))},
		{"REJECTED awaiting RELEASED", terminalAwaitingRelease(accessv1.Disposition_DISPOSITION_REJECTED), wantRows(ack(accessv1.Disposition_DISPOSITION_REJECTED))},
		{"abandoned, ack owed", abandonedAckOwed(), wantRows(ack(accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED))},
		{"abandoned, ack confirmed, held", abandonedAckConfirmed(), wantRows()},
		{"VERIFIED after Onboarded re-sends the terminal ack", afterOnboarded(terminalAwaitingRelease(accessv1.Disposition_DISPOSITION_VERIFIED)), wantRows(ack(accessv1.Disposition_DISPOSITION_VERIFIED))},
		{"REJECTED after Onboarded re-sends the terminal ack", afterOnboarded(terminalAwaitingRelease(accessv1.Disposition_DISPOSITION_REJECTED)), wantRows(ack(accessv1.Disposition_DISPOSITION_REJECTED))},
		{"recovering after Onboarded re-dispatches with resume", afterOnboarded(recovering()), wantRows(Owed{Kind: OwedExecute, Sequence: 7, Resume: true})},
		{"held reconciliation intent owes nothing", heldReconciliation(), wantRows()},
		{"hold resolved, unconfirmed", holdResolvedUnconfirmed(), wantRows(Owed{Kind: OwedHoldResolved, Sequence: 5})},
		{"abandon before dispatch: lane closed, hold resolved owed", abandonBeforeDispatch(), wantRows(Owed{Kind: OwedHoldResolved, Sequence: 7})},
		{"restore owes hold and dispatch together", restoreWrite(), wantRows(Owed{Kind: OwedHoldResolved, Sequence: 7}, Owed{Kind: OwedExecute, Sequence: 8})},
		{"open read owes its dispatch", openReadState(), wantRows(Owed{Kind: OwedRead, Sequence: 9})},
		{"closed read owes nothing", closedReadState(), wantRows()},
		{"expired read is swept, not owed", expiredReadState(), wantRows()},
	}
}

// withAdmission sets the admission time an open mutation's record must carry
// (device_lane_record.mutation_has_admission_time), so the states the table
// builds satisfy the schema they will be stored under.
func withAdmission(rec *storev1.DeviceLaneRecord) *storev1.DeviceLaneRecord {
	rec.SetAdmittedAt(timestamppb.New(now))
	return rec
}

// TestOwedRowTable is the proof of the outbox decision: every enumerated
// record state maps to exactly the rows it owes, Resume and Disposition
// included.
func TestOwedRowTable(t *testing.T) {
	for _, tt := range laneStates() {
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
		})
	}
}

// TestOwedNoLaneStranded asserts, per open sequence, that no sequence is
// stranded: the mutation's sequence appears in the owed rows or names a
// terminator; the hold's sequence appears in the owed rows; each unclosed
// read's sequence appears in the owed rows or is past its deadline. It runs
// over both the readable table and the generated cross product, so a stranded
// state cannot hide by being left out of a hand-written list, and no owed row
// on one sequence can mask a stranded state on another.
func TestOwedNoLaneStranded(t *testing.T) {
	for _, tt := range laneStates() {
		t.Run(tt.name, func(t *testing.T) { assertNoStrandedSequence(t, tt.rec) })
	}
	seen := 0
	forEachReachableMutation(t, func(rec *storev1.DeviceLaneRecord, desc string) {
		for _, bg := range backgrounds() {
			combined := bg.apply(proto.Clone(rec).(*storev1.DeviceLaneRecord))
			t.Run(desc+bg.name, func(t *testing.T) { assertNoStrandedSequence(t, combined) })
			seen++
		}
	})
	if seen == 0 {
		t.Fatal("cross product produced no reachable states; the generator is broken")
	}
}

func assertNoStrandedSequence(t *testing.T, rec *storev1.DeviceLaneRecord) {
	t.Helper()
	owed := OwedRows(rec, now)
	inOwed := map[uint64]bool{}
	perSeq := map[uint64]int{}
	for _, o := range owed {
		inOwed[o.Sequence] = true
		perSeq[o.Sequence]++
	}
	for seq, n := range perSeq {
		if n > 1 {
			t.Fatalf("sequence %d owes %d rows, want at most one", seq, n)
		}
	}
	if m := rec.GetMutation(); m != nil {
		if seq := m.GetSequence(); !inOwed[seq] && !TerminatorNamed(rec) {
			t.Fatalf("mutation sequence %d owes nothing and names no terminator: a stranded lane", seq)
		}
	}
	if rec.HasHoldResolutionPending() {
		if seq := rec.GetHoldResolutionPending(); !inOwed[seq] {
			t.Fatalf("hold sequence %d owes nothing: a stranded hold", seq)
		}
	}
	for iface, r := range rec.GetOpenReads() {
		if r.HasOutcome() {
			continue
		}
		seq := r.GetSequence()
		expired := r.GetDeadline() != nil && !r.GetDeadline().AsTime().After(now)
		if !inOwed[seq] && !expired {
			t.Fatalf("read %q sequence %d owes nothing and is not expired: a stranded read", iface, seq)
		}
	}
}

// TestTableExhibitsEveryReachableMutationBehavior is the exhaustiveness check
// the outbox proof requires: it derives every mutation the journal can write
// from the enum descriptors, and fails if the readable table exhibits no row
// with the same owed-and-terminator behavior. A phase, disposition, or block
// reason added to the schema enters the cross product on its own, so a new
// value that produces an unhandled behavior fails here until a table row
// demonstrates it. It does not catch a wholly new record field; only field
// reflection would.
func TestTableExhibitsEveryReachableMutationBehavior(t *testing.T) {
	exhibited := map[string]string{}
	for _, s := range laneStates() {
		exhibited[mutationSignature(mutationOnly(s.rec))] = s.name
	}
	missing := map[string]string{}
	forEachReachableMutation(t, func(rec *storev1.DeviceLaneRecord, desc string) {
		sig := mutationSignature(rec)
		if _, ok := exhibited[sig]; !ok {
			missing[sig] = desc
		}
	})
	if len(missing) > 0 {
		var lines []string
		for sig, desc := range missing {
			lines = append(lines, fmt.Sprintf("  %s  e.g. %s", sig, desc))
		}
		sort.Strings(lines)
		t.Fatalf("reachable mutation behaviors no table row exhibits:\n%s", strings.Join(lines, "\n"))
	}
}

// TestOwedComposesAdditively shows the hold, the mutation, and the reads
// contribute their rows independently: OwedRows over a record carrying all
// three equals the union of the rows each contributes alone. This is what lets
// the invariant and the exhaustiveness check reason about the mutation
// dimension on its own.
func TestOwedComposesAdditively(t *testing.T) {
	rec := withAdmission(&storev1.DeviceLaneRecord{})
	rec.SetMutation(mut(7, accessv1.OperationPhase_OPERATION_PHASE_ADMITTED))
	rec.SetHoldResolutionPending(5)
	rec.SetOpenReads(map[string]*storev1.OpenRead{"eth1": openRead(9, now.Add(time.Minute))})

	combined := rowsOf(OwedRows(rec, now))
	want := wantRows(
		Owed{Kind: OwedExecute, Sequence: 7},
		Owed{Kind: OwedHoldResolved, Sequence: 5},
		Owed{Kind: OwedRead, Sequence: 9},
	)
	if len(combined) != len(want) {
		t.Fatalf("combined owed %v, want %v", combined, want)
	}
	for r, n := range want {
		if combined[r] != n {
			t.Fatalf("combined owed %v, want %v", combined, want)
		}
	}
}

// mutationOnly clones the record keeping only the mutation and its
// record-level facts, so a table state's mutation behavior can be compared
// against the generated mutation-only states.
func mutationOnly(rec *storev1.DeviceLaneRecord) *storev1.DeviceLaneRecord {
	c := proto.Clone(rec).(*storev1.DeviceLaneRecord)
	c.SetOpenReads(nil)
	c.ClearHoldResolutionPending()
	return c
}

// mutationSignature is a record's owed-and-terminator behavior as a canonical
// string, with sequence numbers normalized out so states differing only in
// their sequence share a signature.
func mutationSignature(rec *storev1.DeviceLaneRecord) string {
	var rows []string
	for _, o := range OwedRows(rec, now) {
		rows = append(rows, fmt.Sprintf("%d/resume=%t/disp=%d", o.Kind, o.Resume, o.Disposition))
	}
	sort.Strings(rows)
	return fmt.Sprintf("owed=[%s] terminator=%t", strings.Join(rows, ","), TerminatorNamed(rec))
}

// background is an independent contribution — a hold or a read on its own
// sequence — layered onto a mutation record so the invariant sweep sees a
// stranded mutation even when another sequence owes a row.
type background struct {
	name  string
	apply func(*storev1.DeviceLaneRecord) *storev1.DeviceLaneRecord
}

func backgrounds() []background {
	return []background{
		{"", func(r *storev1.DeviceLaneRecord) *storev1.DeviceLaneRecord { return r }},
		{"+future-read", func(r *storev1.DeviceLaneRecord) *storev1.DeviceLaneRecord {
			r.SetOpenReads(map[string]*storev1.OpenRead{"eth1": openRead(201, now.Add(time.Minute))})
			return r
		}},
		{"+expired-read", func(r *storev1.DeviceLaneRecord) *storev1.DeviceLaneRecord {
			r.SetOpenReads(map[string]*storev1.OpenRead{"eth1": openRead(202, now.Add(-time.Second))})
			return r
		}},
		{"+hold", func(r *storev1.DeviceLaneRecord) *storev1.DeviceLaneRecord {
			r.SetHoldResolutionPending(100)
			return r
		}},
	}
}

// enumValues returns an enum's non-zero values from its generated name map, so
// a value added to the schema is swept without editing the test.
func enumValues[E ~int32](names map[int32]string) []E {
	var vs []E
	for v := range names {
		if v != 0 {
			vs = append(vs, E(v))
		}
	}
	sort.Slice(vs, func(i, j int) bool { return vs[i] < vs[j] })
	return vs
}

// forEachReachableMutation walks the cross product of the mutation-carrying
// dimensions — phase, disposition, block reason, and the dispatch facts — over
// the enum values the schema defines, and calls fn for each state the journal
// can actually write. mutationReachable is the explicit filter: it excludes a
// combination only for a stated schema rule or a fact about which journal
// method writes it, so a combination that becomes reachable later stops being
// excluded when its reason stops holding.
func forEachReachableMutation(t *testing.T, fn func(rec *storev1.DeviceLaneRecord, desc string)) {
	t.Helper()
	phases := enumValues[accessv1.OperationPhase](accessv1.OperationPhase_name)
	dispositions := append([]accessv1.Disposition{0}, enumValues[accessv1.Disposition](accessv1.Disposition_name)...)
	blocks := append([]accessv1.BlockReason{0}, enumValues[accessv1.BlockReason](accessv1.BlockReason_name)...)
	lastReported := append([]accessv1.OperationPhase{0}, phases...)
	bools := []bool{false, true}

	for _, phase := range phases {
		for _, disp := range dispositions {
			for _, block := range blocks {
				for _, dispatched := range bools {
					for _, dc := range bools {
						for _, cc := range bools {
							for _, lr := range lastReported {
								if ok, _ := mutationReachable(phase, disp, block, dispatched, dc); !ok {
									continue
								}
								rec := buildMutation(phase, disp, block, dispatched, dc, cc, lr)
								desc := fmt.Sprintf("phase=%d disp=%d block=%d d=%t dc=%t cc=%t lr=%d",
									phase, disp, block, dispatched, dc, cc, lr)
								fn(rec, desc)
							}
						}
					}
				}
			}
		}
	}
}

func isTerminalPhase(phase accessv1.OperationPhase) bool {
	switch phase {
	case accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED,
		accessv1.OperationPhase_OPERATION_PHASE_RELEASED,
		accessv1.OperationPhase_OPERATION_PHASE_ABANDONED:
		return true
	default:
		return false
	}
}

// mutationReachable reports whether the journal can write a mutation with these
// facts, and if not, why. Each exclusion is a schema rule or a fact about the
// journal's writes; nothing is skipped silently.
func mutationReachable(phase accessv1.OperationPhase, disp accessv1.Disposition, block accessv1.BlockReason, dispatched, dc bool) (bool, string) {
	if phase == accessv1.OperationPhase_OPERATION_PHASE_INTENT_RECORDED {
		return false, "Admit records at ADMITTED with a sequence; INTENT_RECORDED carries no sequence and is never stored"
	}
	hasDisp := disp != 0
	if hasDisp != isTerminalPhase(phase) {
		return false, "schema mutation_state.disposition_matches_phase: a disposition is set exactly at a terminal phase"
	}
	if dc && !dispatched {
		return false, "dispatch_confirmed is set only by ReportAdmitted, which sets dispatched in the same write"
	}
	if hasDisp && !dispatched {
		return false, "a disposition is written only when the edge holds the sequence; Dispose of an un-dispatched mutation closes the lane instead"
	}
	if block == accessv1.BlockReason_BLOCK_REASON_RECOVERY_HOLD && !hasDisp {
		return false, "RECOVERY_HOLD is written only by Dispose, which sets the abandoned disposition in the same write"
	}
	return true, ""
}

func buildMutation(phase accessv1.OperationPhase, disp accessv1.Disposition, block accessv1.BlockReason, dispatched, dc, cc bool, lr accessv1.OperationPhase) *storev1.DeviceLaneRecord {
	rec := withAdmission(&storev1.DeviceLaneRecord{})
	rec.SetHighWatermark(300)
	m := mut(7, phase)
	if disp != 0 {
		m.SetDisposition(disp)
	}
	if block != 0 {
		m.SetBlockReason(block)
		m.SetBlockedSince(timestamppb.New(now))
	}
	rec.SetMutation(m)
	rec.SetDispatched(dispatched)
	rec.SetDispatchConfirmed(dc)
	rec.SetCheckpointConfirmed(cc)
	if lr != 0 {
		rec.SetLastReportedPhase(lr)
	}
	return rec
}
