package journal_test

import (
	"context"
	"testing"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
)

const readIface = "ethernet 1/1/1"

// owesRow reports whether the record owes a row of the given kind at the given
// sequence, at time now.
func owesRow(t *testing.T, j *journal.Journal, kind journal.OwedKind, seq uint64) bool {
	t.Helper()
	rec, err := j.Record(context.Background(), deviceID)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	for _, o := range journal.OwedRows(rec, time.Now()) {
		if o.Kind == kind && o.Sequence == seq {
			return true
		}
	}
	return false
}

// TestNamedTerminatorIsInvocableForEveryOwedRow is a cross-seam invariant: for
// every kind of row the owed-row derivation produces, the terminator the
// design names for that row can actually act on a record in the state that
// owes it, and acting stops the row being owed. The owed-row table proves a
// state owes a row or names a terminator; this proves the named terminator's
// own guard admits that state — the gap between those two claims is where a
// strand wearing a terminator's name would live.
//
// It runs each case across the backgrounds a record can carry: no pending
// hold, and a hold pending for an unrelated sequence. The second is the one
// the first form of this test missed: it is the plan's restore arm, where an
// abandoned sequence's hold is still owed while a fresh intent owes its own
// row, and it is where AbandonMutation and ResolveDesynchronization refused
// outright until hold resolution became a set.
func TestNamedTerminatorIsInvocableForEveryOwedRow(t *testing.T) {
	ctx := context.Background()

	admit := func(t *testing.T, j *journal.Journal, key string) uint64 {
		t.Helper()
		state, err := j.Admit(ctx, deviceID, mutationIntent(key), edgeRef())
		if err != nil {
			t.Fatalf("admit: %v", err)
		}
		return state.GetSequence()
	}

	cases := []struct {
		name      string
		owe       func(t *testing.T, j *journal.Journal) (journal.OwedKind, uint64)
		terminate func(t *testing.T, j *journal.Journal, seq uint64) error
	}{
		{
			// Terminator: the edge's HoldResolvedAck, ConfirmHoldResolved.
			name: "hold resolved / ConfirmHoldResolved",
			owe: func(t *testing.T, j *journal.Journal) (journal.OwedKind, uint64) {
				if err := j.ResolveHold(ctx, deviceID, 5); err != nil {
					t.Fatalf("resolve hold: %v", err)
				}
				return journal.OwedHoldResolved, 5
			},
			terminate: func(t *testing.T, j *journal.Journal, seq uint64) error {
				return j.ConfirmHoldResolved(ctx, deviceID, seq)
			},
		},
		{
			// A fresh execute row: phase ADMITTED, no disposition, dispatched
			// false. Dispose admits a mutation at the sequence with no
			// disposition, and — the edge never reported admitted — closes the
			// lane and adds a hold, without refusing over an unrelated one.
			name: "execute never dispatched / AbandonMutation",
			owe: func(t *testing.T, j *journal.Journal) (journal.OwedKind, uint64) {
				seq := admit(t, j, "0192e6a0-0000-7000-8000-000000000e01")
				return journal.OwedExecute, seq
			},
			terminate: func(t *testing.T, j *journal.Journal, seq uint64) error {
				_, err := j.Dispose(ctx, deviceID, seq)
				return err
			},
		},
		{
			// A resumed execute row after Onboarded: phase POSSIBLY_APPLIED, no
			// disposition, dispatched true, dispatch_confirmed false. Dispose
			// admits it and writes a held abandonment.
			name: "execute resume after onboarded / AbandonMutation",
			owe: func(t *testing.T, j *journal.Journal) (journal.OwedKind, uint64) {
				seq := admit(t, j, "0192e6a0-0000-7000-8000-000000000e02")
				if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: seq}); err != nil {
					t.Fatalf("admitted report: %v", err)
				}
				if err := j.MarkOnboarded(ctx, deviceID); err != nil {
					t.Fatalf("onboarded: %v", err)
				}
				return journal.OwedExecute, seq
			},
			terminate: func(t *testing.T, j *journal.Journal, seq uint64) error {
				_, err := j.Dispose(ctx, deviceID, seq)
				return err
			},
		},
		{
			// Terminator: the edge's CheckpointAck, ConfirmCheckpoint.
			name: "checkpoint / ConfirmCheckpoint",
			owe: func(t *testing.T, j *journal.Journal) (journal.OwedKind, uint64) {
				seq := admit(t, j, "0192e6a0-0000-7000-8000-000000000e03")
				if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: seq}); err != nil {
					t.Fatalf("admitted report: %v", err)
				}
				return journal.OwedCheckpoint, seq
			},
			terminate: func(t *testing.T, j *journal.Journal, seq uint64) error {
				return j.ConfirmCheckpoint(ctx, deviceID, seq)
			},
		},
		{
			// Terminator: the read's result report, CloseRead.
			name: "read / CloseRead",
			owe: func(t *testing.T, j *journal.Journal) (journal.OwedKind, uint64) {
				seq, err := j.OpenRead(ctx, deviceID, deviceRef(), readIface, typedRead(), "0192e6a0-0000-7000-8000-000000000e04", time.Now().Add(time.Minute))
				if err != nil {
					t.Fatalf("open read: %v", err)
				}
				return journal.OwedRead, seq
			},
			terminate: func(t *testing.T, j *journal.Journal, seq uint64) error {
				return j.CloseRead(ctx, deviceID, readIface, seq, &accessv1.InterfaceObservation{}, nil)
			},
		},
		{
			// Terminator: the edge's RELEASED report closes a released
			// disposition's terminal ack.
			name: "terminal ack released / ReportReleased",
			owe: func(t *testing.T, j *journal.Journal) (journal.OwedKind, uint64) {
				seq := admit(t, j, "0192e6a0-0000-7000-8000-000000000e05")
				for _, r := range []journal.Report{
					{Kind: journal.ReportAdmitted, Sequence: seq},
					{Kind: journal.ReportVerified, Sequence: seq},
				} {
					if err := j.ApplyReport(ctx, deviceID, r); err != nil {
						t.Fatalf("apply %v: %v", r.Kind, err)
					}
				}
				return journal.OwedTerminalAck, seq
			},
			terminate: func(t *testing.T, j *journal.Journal, seq uint64) error {
				return j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportReleased, Sequence: seq})
			},
		},
		{
			// Terminator: the edge's ABANDONED report confirms an abandonment's
			// terminal ack; the mutation then stays held for
			// ResolveDesynchronization, but the ack row is no longer owed.
			name: "terminal ack abandoned / ReportAbandoned",
			owe: func(t *testing.T, j *journal.Journal) (journal.OwedKind, uint64) {
				seq := admit(t, j, "0192e6a0-0000-7000-8000-000000000e06")
				if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: seq}); err != nil {
					t.Fatalf("admitted report: %v", err)
				}
				if _, err := j.Dispose(ctx, deviceID, seq); err != nil {
					t.Fatalf("dispose: %v", err)
				}
				return journal.OwedTerminalAck, seq
			},
			terminate: func(t *testing.T, j *journal.Journal, seq uint64) error {
				return j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAbandoned, Sequence: seq})
			},
		},
	}

	backgrounds := []struct {
		name string
		seed func(t *testing.T, j *journal.Journal)
	}{
		{"no hold", func(*testing.T, *journal.Journal) {}},
		{"hold pending for another sequence", func(t *testing.T, j *journal.Journal) {
			if err := j.ResolveHold(ctx, deviceID, 99); err != nil {
				t.Fatalf("seed hold: %v", err)
			}
		}},
	}

	for _, bg := range backgrounds {
		for _, c := range cases {
			t.Run(bg.name+"/"+c.name, func(t *testing.T) {
				j := newJournal(t)
				bg.seed(t, j)
				kind, seq := c.owe(t, j)
				if !owesRow(t, j, kind, seq) {
					t.Fatalf("setup did not owe kind=%d seq=%d", kind, seq)
				}
				if err := c.terminate(t, j, seq); err != nil {
					t.Fatalf("named terminator could not act on the owing record: %v", err)
				}
				if owesRow(t, j, kind, seq) {
					t.Fatalf("the named terminator did not stop kind=%d seq=%d being owed", kind, seq)
				}
				// The terminator must not clobber an unrelated pending hold.
				rec, _ := j.Record(ctx, deviceID)
				if bg.name != "no hold" && !holdPending(rec, 99) {
					t.Fatal("the terminator dropped an unrelated pending hold")
				}
			})
		}
	}
}

// TestAbandonedHoldResolvableUnderAnOlderHold is Finding 1's symmetric case: an
// abandoned mutation whose ack the edge confirmed owes nothing and names
// ResolveDesynchronization as its terminator; that terminator must act even
// when an older, unrelated hold is still pending — which the single-valued
// field made impossible.
func TestAbandonedHoldResolvableUnderAnOlderHold(t *testing.T) {
	ctx := context.Background()
	j := newJournal(t)
	if err := j.ResolveHold(ctx, deviceID, 99); err != nil { // an older, unacknowledged hold
		t.Fatalf("seed hold: %v", err)
	}
	state, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000e08"), edgeRef())
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
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAbandoned, Sequence: seq}); err != nil {
		t.Fatalf("abandoned: %v", err)
	}
	// The mutation owes nothing now; ResolveDesynchronization must free it.
	if err := j.ResolveHold(ctx, deviceID, seq); err != nil {
		t.Fatalf("ResolveDesynchronization refused under an older hold: %v", err)
	}
	rec, _ := j.Record(ctx, deviceID)
	if rec.HasMutation() {
		t.Fatal("ResolveDesynchronization did not free the abandoned mutation")
	}
	if !holdPending(rec, 99) || !holdPending(rec, seq) {
		t.Fatalf("both holds should be pending, got %v", rec.GetHoldResolutionPending())
	}
}

// TestDisposeAdvancesEvenAMutationThatOwesNothing checks the permissive side of
// Dispose's guard: it admits a mutation at the sequence with no disposition
// even when that mutation owes nothing (a recovering record between reports),
// and it advances the record rather than silently no-oping.
func TestDisposeAdvancesEvenAMutationThatOwesNothing(t *testing.T) {
	ctx := context.Background()
	j := newJournal(t)
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000e07"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	for _, r := range []journal.Report{
		{Kind: journal.ReportAdmitted, Sequence: 1},
		{Kind: journal.ReportRecovering, Sequence: 1},
	} {
		if err := j.ApplyReport(ctx, deviceID, r); err != nil {
			t.Fatalf("apply %v: %v", r.Kind, err)
		}
	}
	if err := j.ConfirmCheckpoint(ctx, deviceID, 1); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if rec, _ := j.Record(ctx, deviceID); len(journal.OwedRows(rec, time.Now())) != 0 {
		t.Fatal("setup expected a mutation that owes nothing")
	}
	if _, err := j.Dispose(ctx, deviceID, 1); err != nil {
		t.Fatalf("Dispose could not act on a quiet mutation: %v", err)
	}
	rec, _ := j.Record(ctx, deviceID)
	if !rec.GetMutation().HasDisposition() {
		t.Fatal("Dispose silently no-oped on a mutation that owed nothing")
	}
}
