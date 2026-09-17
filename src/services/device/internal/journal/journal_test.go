package journal_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	errsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/errs/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/policy/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
)

const deviceID = "0192e6a0-0000-7000-8000-0000000000d1"

func newJournal(t *testing.T) *journal.Journal {
	t.Helper()
	j, _ := newJournalKV(t)
	return j
}

// newJournalKV also hands back the bucket, so a test can read the record's
// revision and count the writes an operation took.
func newJournalKV(t *testing.T) (*journal.Journal, jetstream.KeyValue) {
	t.Helper()
	hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
		ListenPort:  0,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)
	kv, err := hub.JetStream().KeyValue(context.Background(), edgebus.LaneBucket)
	if err != nil {
		t.Fatalf("bucket: %v", err)
	}
	return journal.New(kv, nil), kv
}

// holdKey names the intent whose abandonment seeds one pending hold.
func holdKey(n int) string {
	return fmt.Sprintf("0192e6a0-0000-7000-8000-%012d", n)
}

func mutationIntent(key string) *accessv1.MutationIntent {
	device := &inventoryv1.DeviceGlobalRef{}
	device.SetDevice(func() *inventoryv1.DeviceLocalRef {
		r := &inventoryv1.DeviceLocalRef{}
		r.SetId(deviceID)
		return r
	}())
	actor := &accessv1.Actor{}
	op := &accessv1.OperatorRef{}
	op.SetSubject("zitadel|1")
	actor.SetOperator(op)
	policy := &policyv1.AccessPolicyHandle{}
	policy.SetKey("icx7150-lab")
	policy.SetVersion(3)
	change := &accessv1.InterfaceDescriptionChange{}
	change.SetInterfaceName("ethernet 1/1/1")
	change.SetDescription("uplink to core")
	intent := &accessv1.MutationIntent{}
	intent.SetDevice(device)
	intent.SetIdempotencyKey(key)
	intent.SetActor(actor)
	intent.SetAccessPolicy(policy)
	intent.SetExpectedFirmwareFingerprint("ICX7150-24P SPS10010g")
	intent.SetInterfaceDescription(change)
	return intent
}

func edgeRef() *edgev1.EdgeGlobalRef {
	ref := &edgev1.EdgeGlobalRef{}
	local := &edgev1.EdgeLocalRef{}
	local.SetId("0192e6a0-0000-7000-8000-0000000000ed")
	ref.SetEdge(local)
	return ref
}

func deviceRef() *inventoryv1.DeviceGlobalRef {
	device := &inventoryv1.DeviceGlobalRef{}
	local := &inventoryv1.DeviceLocalRef{}
	local.SetId(deviceID)
	device.SetDevice(local)
	return device
}

// holdPending reports whether the record's pending hold-resolution set holds
// the sequence.
func holdPending(rec *storev1.DeviceLaneRecord, sequence uint64) bool {
	for _, s := range rec.GetHoldResolutionPending() {
		if s == sequence {
			return true
		}
	}
	return false
}

// pendingHold puts one hold into the record the way the journal produces one:
// a mutation abandoned before the edge reported it admitted closes the lane
// and leaves the sequence's hold owed. It returns that sequence.
func pendingHold(t *testing.T, j *journal.Journal, key string) uint64 {
	t.Helper()
	ctx := context.Background()
	state, err := j.Admit(ctx, deviceID, mutationIntent(key), edgeRef())
	if err != nil {
		t.Fatalf("admit for hold: %v", err)
	}
	if _, err := j.Dispose(ctx, deviceID, state.GetSequence()); err != nil {
		t.Fatalf("dispose for hold: %v", err)
	}
	return state.GetSequence()
}

func TestAdmitAssignsSequencesAndDeduplicates(t *testing.T) {
	j := newJournal(t)
	ctx := context.Background()
	const key = "0192e6a0-0000-7000-8000-00000000a001"

	state, err := j.Admit(ctx, deviceID, mutationIntent(key), edgeRef())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if state.GetSequence() != 1 {
		t.Fatalf("first sequence = %d, want 1", state.GetSequence())
	}
	if state.GetPhase() != accessv1.OperationPhase_OPERATION_PHASE_ADMITTED {
		t.Fatalf("phase = %v, want ADMITTED", state.GetPhase())
	}

	// The same key returns the recorded state, admitting nothing new.
	again, err := j.Admit(ctx, deviceID, mutationIntent(key), edgeRef())
	if err != nil {
		t.Fatalf("re-admit: %v", err)
	}
	if again.GetSequence() != 1 {
		t.Fatalf("resubmission sequence = %d, want 1", again.GetSequence())
	}

	// A second, different intent cannot be admitted while the first holds
	// the lane; once it releases, the next admit gets the next sequence.
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-00000000a002"), edgeRef()); err == nil {
		t.Fatal("admitted a second mutation over an open one")
	}
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: 1}); err != nil {
		t.Fatalf("admitted report: %v", err)
	}
	if err := j.ConfirmCheckpoint(ctx, deviceID, 1); err != nil {
		t.Fatalf("confirm checkpoint: %v", err)
	}
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportVerified, Sequence: 1}); err != nil {
		t.Fatalf("verified: %v", err)
	}
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportReleased, Sequence: 1}); err != nil {
		t.Fatalf("released: %v", err)
	}
	next, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-00000000a003"), edgeRef())
	if err != nil {
		t.Fatalf("admit after release: %v", err)
	}
	if next.GetSequence() != 2 {
		t.Fatalf("next sequence = %d, want 2 (sequences only grow)", next.GetSequence())
	}
}

func TestConcurrentAdmitYieldsOneAdmissionAndOneRefusal(t *testing.T) {
	j := newJournal(t)
	ctx := context.Background()

	// Two admits racing on one device: the CAS retry makes one win and one
	// see the other's write, so exactly one is admitted (the lane holds one
	// mutation) and neither loses the record.
	var wg sync.WaitGroup
	results := make([]error, 2)
	keys := []string{"0192e6a0-0000-7000-8000-00000000b001", "0192e6a0-0000-7000-8000-00000000b002"}
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = j.Admit(ctx, deviceID, mutationIntent(keys[i]), edgeRef())
		}()
	}
	wg.Wait()

	admitted, rejected := 0, 0
	for _, err := range results {
		if err == nil {
			admitted++
		} else {
			rejected++
		}
	}
	if admitted != 1 || rejected != 1 {
		t.Fatalf("concurrent admit: %d admitted, %d rejected, want 1 and 1", admitted, rejected)
	}
	rec, err := j.Record(ctx, deviceID)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if rec.GetHighWatermark() != 1 || rec.GetMutation().GetSequence() != 1 {
		t.Fatalf("watermark %d, mutation seq %d, want 1 and 1", rec.GetHighWatermark(), rec.GetMutation().GetSequence())
	}
}

func TestApplyReportWalksThePhasesAndClosesTheRecord(t *testing.T) {
	j := newJournal(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-00000000c001"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}

	// ADMITTED report moves to POSSIBLY_APPLIED and sets the dispatch
	// confirmations in one write.
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: 1}); err != nil {
		t.Fatalf("admitted: %v", err)
	}
	rec, _ := j.Record(ctx, deviceID)
	if rec.GetMutation().GetPhase() != accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED || !rec.GetDispatched() || !rec.GetDispatchConfirmed() {
		t.Fatalf("after ADMITTED: phase %v dispatched %v confirmed %v", rec.GetMutation().GetPhase(), rec.GetDispatched(), rec.GetDispatchConfirmed())
	}

	// A stale duplicate ADMITTED is ignored.
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: 1}); err != nil {
		t.Fatalf("duplicate admitted: %v", err)
	}

	// A report for another sequence is ignored, not misapplied.
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportVerified, Sequence: 99}); err != nil {
		t.Fatalf("wrong-sequence report: %v", err)
	}
	rec, _ = j.Record(ctx, deviceID)
	if rec.GetMutation().HasDisposition() {
		t.Fatal("a report for another sequence set a disposition")
	}

	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportVerified, Sequence: 1}); err != nil {
		t.Fatalf("verified: %v", err)
	}
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportReleased, Sequence: 1}); err != nil {
		t.Fatalf("released: %v", err)
	}
	rec, _ = j.Record(ctx, deviceID)
	if rec.HasMutation() {
		t.Fatal("RELEASED did not close the record")
	}
}

func TestApplyReportErrorBeforeSubmissionDisposesRejected(t *testing.T) {
	j := newJournal(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-00000000d001"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	// An error before the command was submitted — a firmware-epoch block, or
	// an Execute failure before the device latched — proves the edge holds the
	// sequence but nothing reached the device. Central disposes REJECTED and
	// owes the terminal ack the edge waits for; it does not close the record
	// and leave the edge waiting for an ack that never comes.
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportError, Sequence: 1, Submitted: false}); err != nil {
		t.Fatalf("error report: %v", err)
	}
	rec, _ := j.Record(ctx, deviceID)
	m := rec.GetMutation()
	if m == nil {
		t.Fatal("a pre-submission error closed the record instead of disposing REJECTED")
	}
	if m.GetDisposition() != accessv1.Disposition_DISPOSITION_REJECTED || !rec.GetDispatched() {
		t.Fatalf("disposition %v, dispatched %v; want REJECTED and dispatched so the ack is owed", m.GetDisposition(), rec.GetDispatched())
	}
	// The edge's RELEASED report then closes it through the ordinary path.
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportReleased, Sequence: 1}); err != nil {
		t.Fatalf("released: %v", err)
	}
	rec, _ = j.Record(ctx, deviceID)
	if rec.HasMutation() {
		t.Fatal("RELEASED did not close the rejected mutation")
	}
}

func TestDisposeAndResolveDesynchronization(t *testing.T) {
	j := newJournal(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-00000000e001"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: 1}); err != nil {
		t.Fatalf("admitted: %v", err)
	}
	if err := j.ConfirmCheckpoint(ctx, deviceID, 1); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportRecovering, Sequence: 1}); err != nil {
		t.Fatalf("recovering: %v", err)
	}
	state, err := j.Dispose(ctx, deviceID, 1)
	if err != nil {
		t.Fatalf("dispose: %v", err)
	}
	if state.GetDisposition() != accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED ||
		state.GetBlockReason() != accessv1.BlockReason_BLOCK_REASON_RECOVERY_HOLD {
		t.Fatalf("disposed state = %v / %v", state.GetDisposition(), state.GetBlockReason())
	}
	// The mutation stays held until ResolveDesynchronization clears it.
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAbandoned, Sequence: 1}); err != nil {
		t.Fatalf("abandoned: %v", err)
	}
	rec, _ := j.Record(ctx, deviceID)
	if !rec.HasMutation() {
		t.Fatal("abandonment closed the record; it must stay held for resolution")
	}
	if _, _, err := j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{Sequence: 1}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rec, _ = j.Record(ctx, deviceID)
	if rec.HasMutation() {
		t.Fatal("ResolveDesynchronization did not clear the held mutation")
	}
	if !holdPending(rec, 1) {
		t.Fatalf("hold resolution pending = %v, want 1 present", rec.GetHoldResolutionPending())
	}
	if err := j.ConfirmHoldResolved(ctx, deviceID, 1); err != nil {
		t.Fatalf("confirm hold: %v", err)
	}
	rec, _ = j.Record(ctx, deviceID)
	if holdPending(rec, 1) {
		t.Fatal("ConfirmHoldResolved did not clear the pending row")
	}
}

func TestOpenAndCloseReadShareTheCounter(t *testing.T) {
	j := newJournal(t)
	ctx := context.Background()
	read := &accessv1.TypedRead{}
	read.SetAccessPolicy(func() *policyv1.AccessPolicyHandle {
		p := &policyv1.AccessPolicyHandle{}
		p.SetKey("icx7150-lab")
		p.SetVersion(3)
		return p
	}())
	intent := &accessv1.InterfaceReadIntent{}
	intent.SetInterfaceName("ethernet 1/1/1")
	read.SetInterface(intent)

	seq, err := j.OpenRead(ctx, deviceID, deviceRef(), "ethernet 1/1/1", read, "0192e6a0-0000-7000-8000-00000000f001", time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("open read: %v", err)
	}
	if seq != 1 {
		t.Fatalf("read sequence = %d, want 1", seq)
	}
	// A second poll of the same interface while the first is open skips.
	again, err := j.OpenRead(ctx, deviceID, deviceRef(), "ethernet 1/1/1", read, "0192e6a0-0000-7000-8000-00000000f002", time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	if again != 1 {
		t.Fatalf("colliding poll got sequence %d, want the existing 1", again)
	}
	obs := &accessv1.InterfaceObservation{}
	obs.SetInterfaceName("ethernet 1/1/1")
	if err := j.CloseRead(ctx, deviceID, "ethernet 1/1/1", seq, obs, nil); err != nil {
		t.Fatalf("close read: %v", err)
	}
	rec, _ := j.Record(ctx, deviceID)
	if !rec.GetOpenReads()["ethernet 1/1/1"].HasObservation() {
		t.Fatal("CloseRead did not record the observation")
	}
}

func typedRead() *accessv1.TypedRead {
	read := &accessv1.TypedRead{}
	policy := &policyv1.AccessPolicyHandle{}
	policy.SetKey("icx7150-lab")
	policy.SetVersion(3)
	read.SetAccessPolicy(policy)
	intent := &accessv1.InterfaceReadIntent{}
	intent.SetInterfaceName("ethernet 1/1/1")
	read.SetInterface(intent)
	return read
}

func TestReadmissionAfterOnboardedConfirmsWithoutRedispatchLoop(t *testing.T) {
	j := newJournal(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000010001"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: 1}); err != nil {
		t.Fatalf("admitted: %v", err)
	}
	// The edge restarts: Onboarded clears the per-dispatch confirmation, so the
	// mutation is re-dispatched with resume, but dispatched survives.
	if err := j.MarkOnboarded(ctx, deviceID); err != nil {
		t.Fatalf("onboarded: %v", err)
	}
	rec, _ := j.Record(ctx, deviceID)
	if rec.GetDispatchConfirmed() || !rec.GetDispatched() {
		t.Fatalf("after Onboarded: dispatched %v confirmed %v, want true and false", rec.GetDispatched(), rec.GetDispatchConfirmed())
	}
	// The resume dispatch is re-admitted. This must re-confirm the dispatch and
	// not regress POSSIBLY_APPLIED back to ADMITTED, so the execute row stops
	// being owed rather than being re-sent on every relay pass forever.
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: 1}); err != nil {
		t.Fatalf("re-admitted: %v", err)
	}
	rec, _ = j.Record(ctx, deviceID)
	if !rec.GetDispatchConfirmed() {
		t.Fatal("re-admission after Onboarded did not re-confirm the dispatch: the execute row is owed forever")
	}
	if rec.GetMutation().GetPhase() != accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED {
		t.Fatalf("re-admission regressed the phase to %v", rec.GetMutation().GetPhase())
	}
}

func TestReportCannotOverwriteOperatorAbandonment(t *testing.T) {
	j := newJournal(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000010101"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: 1}); err != nil {
		t.Fatalf("admitted: %v", err)
	}
	if _, err := j.Dispose(ctx, deviceID, 1); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	// An in-flight verified report for the same sequence lands after the
	// operator abandoned it. It must not overwrite the abandonment with a
	// verified disposition central would then owe a verified ack for.
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportVerified, Sequence: 1}); err != nil {
		t.Fatalf("late verified: %v", err)
	}
	rec, _ := j.Record(ctx, deviceID)
	if rec.GetMutation().GetDisposition() != accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED {
		t.Fatalf("late report overwrote the abandonment: disposition %v", rec.GetMutation().GetDisposition())
	}
}

func TestDisposeBeforeDispatchClosesAndOwesHoldResolved(t *testing.T) {
	j := newJournal(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000010201"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	// The operator abandons before the edge ever reported the mutation
	// admitted. Nothing reached the device, so the lane closes and a
	// hold-resolved row releases an edge that may already hold the dispatch.
	state, err := j.Dispose(ctx, deviceID, 1)
	if err != nil {
		t.Fatalf("dispose: %v", err)
	}
	if state.GetDisposition() != accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED {
		t.Fatalf("returned disposition %v", state.GetDisposition())
	}
	rec, _ := j.Record(ctx, deviceID)
	if rec.HasMutation() {
		t.Fatal("abandon-before-dispatch left the mutation open: the lane holds forever")
	}
	if !holdPending(rec, 1) {
		t.Fatalf("hold resolution pending = %v, want 1 present", rec.GetHoldResolutionPending())
	}
	// The lane is free for the next admission.
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000010202"), edgeRef()); err != nil {
		t.Fatalf("admit after abandon-before-dispatch: %v", err)
	}
}

func TestPendingHoldsAccumulateAsASet(t *testing.T) {
	j := newJournal(t)
	ctx := context.Background()
	// Two different holds can be pending at once — the restore arm resolves an
	// abandoned sequence while a new intent's own hold is still owed — so a
	// second resolution adds rather than overwrites, and neither hold's row
	// vanishes before the edge acknowledges it.
	first := pendingHold(t, j, "0192e6a0-0000-7000-8000-000000000501")
	second := pendingHold(t, j, "0192e6a0-0000-7000-8000-000000000502")
	// Re-resolving a pending sequence is idempotent: the set does not grow.
	if _, _, err := j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{Sequence: first}); err != nil {
		t.Fatalf("idempotent resolve: %v", err)
	}
	rec, _ := j.Record(ctx, deviceID)
	if !holdPending(rec, first) || !holdPending(rec, second) || len(rec.GetHoldResolutionPending()) != 2 {
		t.Fatalf("pending holds = %v, want {%d, %d}", rec.GetHoldResolutionPending(), first, second)
	}
	// Confirming one leaves the other owed.
	if err := j.ConfirmHoldResolved(ctx, deviceID, first); err != nil {
		t.Fatalf("confirm %d: %v", first, err)
	}
	rec, _ = j.Record(ctx, deviceID)
	if holdPending(rec, first) || !holdPending(rec, second) {
		t.Fatalf("pending holds = %v, want only %d", rec.GetHoldResolutionPending(), second)
	}
}

func TestCloseReadIgnoresAStaleSequence(t *testing.T) {
	j := newJournal(t)
	ctx := context.Background()
	const iface = "ethernet 1/1/1"

	seq1, err := j.OpenRead(ctx, deviceID, deviceRef(), iface, typedRead(), "0192e6a0-0000-7000-8000-000000010301", time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("open read 1: %v", err)
	}
	// Close it, then open a second read on the same interface, which reuses the
	// entry and takes the next sequence.
	if err := j.CloseRead(ctx, deviceID, iface, seq1, &accessv1.InterfaceObservation{}, nil); err != nil {
		t.Fatalf("close read 1: %v", err)
	}
	seq2, err := j.OpenRead(ctx, deviceID, deviceRef(), iface, typedRead(), "0192e6a0-0000-7000-8000-000000010302", time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("open read 2: %v", err)
	}
	if seq2 == seq1 {
		t.Fatalf("second read reused sequence %d", seq2)
	}
	// A late result for the first read must not land on the second.
	stale := &accessv1.InterfaceObservation{}
	stale.SetInterfaceName("stale")
	if err := j.CloseRead(ctx, deviceID, iface, seq1, stale, nil); err != nil {
		t.Fatalf("stale close: %v", err)
	}
	rec, _ := j.Record(ctx, deviceID)
	if rec.GetOpenReads()[iface].HasOutcome() {
		t.Fatal("a stale result for an earlier read closed the read that succeeded it")
	}
}

func TestSweepExpiredReadsClosesExpiredAndSkipsAnswered(t *testing.T) {
	j := newJournal(t)
	ctx := context.Background()

	expired := &accessv1.TypedRead{}
	expired.SetAccessPolicy(typedRead().GetAccessPolicy())
	ei := &accessv1.InterfaceReadIntent{}
	ei.SetInterfaceName("eth-expired")
	expired.SetInterface(ei)
	if _, err := j.OpenRead(ctx, deviceID, deviceRef(), "eth-expired", expired, "0192e6a0-0000-7000-8000-000000010401", time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("open expired: %v", err)
	}
	answered := &accessv1.TypedRead{}
	answered.SetAccessPolicy(typedRead().GetAccessPolicy())
	ai := &accessv1.InterfaceReadIntent{}
	ai.SetInterfaceName("eth-answered")
	answered.SetInterface(ai)
	answeredSeq, err := j.OpenRead(ctx, deviceID, deviceRef(), "eth-answered", answered, "0192e6a0-0000-7000-8000-000000010402", time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("open answered: %v", err)
	}
	// The answered read got its observation just before the sweep, though its
	// deadline has also passed. The sweep must leave it alone.
	if err := j.CloseRead(ctx, deviceID, "eth-answered", answeredSeq, &accessv1.InterfaceObservation{}, nil); err != nil {
		t.Fatalf("answer: %v", err)
	}
	errPayload := &errsv1.ErrorPayload{}
	swept, err := j.SweepExpiredReads(ctx, deviceID, time.Now(), errPayload)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(swept) != 1 || swept[0] != "eth-expired" {
		t.Fatalf("swept %v, want [eth-expired]", swept)
	}
	rec, _ := j.Record(ctx, deviceID)
	if !rec.GetOpenReads()["eth-expired"].HasError() {
		t.Fatal("the sweep did not record the deadline error")
	}
	if !rec.GetOpenReads()["eth-answered"].HasObservation() {
		t.Fatal("the sweep overwrote an answered read")
	}
}

// TestTerminatorsAreInvocable proves the invariant's terminator arms are not
// labels: for each nothing-owed state that names a terminator, the terminator's
// journal method actually acts on the record and moves it forward.
func TestTerminatorsAreInvocable(t *testing.T) {
	t.Run("AbandonMutation ends a recovering mutation", func(t *testing.T) {
		j := newJournal(t)
		ctx := context.Background()
		if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000010501"), edgeRef()); err != nil {
			t.Fatalf("admit: %v", err)
		}
		if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: 1}); err != nil {
			t.Fatalf("admitted: %v", err)
		}
		if err := j.ConfirmCheckpoint(ctx, deviceID, 1); err != nil {
			t.Fatalf("checkpoint: %v", err)
		}
		if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportRecovering, Sequence: 1}); err != nil {
			t.Fatalf("recovering: %v", err)
		}
		// Recovering owes nothing; AbandonMutation is its terminator.
		if _, err := j.Dispose(ctx, deviceID, 1); err != nil {
			t.Fatalf("dispose: %v", err)
		}
		rec, _ := j.Record(ctx, deviceID)
		if !rec.GetMutation().HasDisposition() {
			t.Fatal("AbandonMutation did not terminate the recovering mutation")
		}
	})
	t.Run("ResolveDesynchronization frees a held abandonment", func(t *testing.T) {
		j := newJournal(t)
		ctx := context.Background()
		if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000010601"), edgeRef()); err != nil {
			t.Fatalf("admit: %v", err)
		}
		if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: 1}); err != nil {
			t.Fatalf("admitted: %v", err)
		}
		if _, err := j.Dispose(ctx, deviceID, 1); err != nil {
			t.Fatalf("dispose: %v", err)
		}
		if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAbandoned, Sequence: 1}); err != nil {
			t.Fatalf("abandoned: %v", err)
		}
		// The held abandonment owes nothing; ResolveDesynchronization is its
		// terminator, and it moves the record to a hold-resolved row.
		if _, _, err := j.ResolveDesynchronization(ctx, deviceID, journal.Resolution{Sequence: 1}); err != nil {
			t.Fatalf("resolve: %v", err)
		}
		rec, _ := j.Record(ctx, deviceID)
		if rec.HasMutation() || !holdPending(rec, 1) {
			t.Fatal("ResolveDesynchronization did not free the held abandonment")
		}
	})
}

// The digest is taken over a named projection of the intent's fields, so every
// field an operator can vary must reach it. A field the projection misses is a
// difference two intents can carry while digesting alike, and the key that was
// meant to catch the reuse answers it instead.
func TestEveryProjectedIntentFieldChangesTheDigest(t *testing.T) {
	const key = "0192e6a0-0000-7000-8000-0000000000c1"
	cases := map[string]func(*accessv1.MutationIntent){
		"device": func(i *accessv1.MutationIntent) {
			local := &inventoryv1.DeviceLocalRef{}
			local.SetId("0192e6a0-0000-7000-8000-0000000000d9")
			ref := &inventoryv1.DeviceGlobalRef{}
			ref.SetDevice(local)
			i.SetDevice(ref)
		},
		"operator subject": func(i *accessv1.MutationIntent) {
			op := &accessv1.OperatorRef{}
			op.SetSubject("zitadel|2")
			i.GetActor().SetOperator(op)
		},
		"actor arm": func(i *accessv1.MutationIntent) {
			system := &accessv1.SystemActor{}
			system.SetReason(accessv1.SystemReason_SYSTEM_REASON_RECONCILIATION)
			i.GetActor().SetSystem(system)
		},
		"policy key":     func(i *accessv1.MutationIntent) { i.GetAccessPolicy().SetKey("icx7150-prod") },
		"policy version": func(i *accessv1.MutationIntent) { i.GetAccessPolicy().SetVersion(4) },
		"firmware epoch": func(i *accessv1.MutationIntent) { i.SetExpectedFirmwareFingerprint("ICX7150-24P SPS10011a") },
		"interface name": func(i *accessv1.MutationIntent) { i.GetInterfaceDescription().SetInterfaceName("ethernet 1/1/2") },
		"description":    func(i *accessv1.MutationIntent) { i.GetInterfaceDescription().SetDescription("uplink to spare") },
	}

	for name, vary := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			j := newJournal(t)
			if _, err := j.Admit(ctx, deviceID, mutationIntent(key), edgeRef()); err != nil {
				t.Fatalf("admit: %v", err)
			}
			changed := mutationIntent(key)
			vary(changed)

			_, err := j.Admit(ctx, deviceID, changed, edgeRef())
			if code, _ := errs.CodeOf(err); code != journal.ErrCodeIdempotencyMismatch {
				t.Fatalf("re-admit error = %v, want code %v", err, journal.ErrCodeIdempotencyMismatch)
			}
		})
	}
}

// A forcing function, not a proof about today: it fails when MutationIntent
// gains a field, so whoever adds one decides whether the digest's projection
// covers it rather than finding out from a key that stopped catching reuse.
func TestIntentDigestProjectionCoversEveryIntentField(t *testing.T) {
	want := map[int32]string{
		1:  "device",
		2:  "idempotency_key",
		3:  "actor",
		4:  "access_policy",
		5:  "expected_firmware_fingerprint",
		10: "interface_description",
	}

	fields := (&accessv1.MutationIntent{}).ProtoReflect().Descriptor().Fields()
	got := map[int32]string{}
	for i := range fields.Len() {
		field := fields.Get(i)
		got[int32(field.Number())] = string(field.Name())
	}
	if len(got) != len(want) {
		t.Fatalf("MutationIntent fields = %v, want %v; add the new one to intentDigest's projection", got, want)
	}
	for number, name := range want {
		if got[number] != name {
			t.Errorf("field %d = %q, want %q", number, got[number], name)
		}
	}
}

// A read past its deadline owes no row and is waiting to be closed with a
// deadline error. Joining it would answer a fresh caller with the previous
// read's failure instead of reading the device, so the entry is replaced.
func TestAReadPastItsDeadlineIsNotJoined(t *testing.T) {
	ctx := context.Background()
	j := newJournal(t)

	stale, err := j.OpenRead(ctx, deviceID, deviceRef(), readIface, typedRead(), "0192e6a0-0000-7000-8000-0000000000c8", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("open expired read: %v", err)
	}
	fresh, err := j.OpenRead(ctx, deviceID, deviceRef(), readIface, typedRead(), "0192e6a0-0000-7000-8000-0000000000c9", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("open fresh read: %v", err)
	}

	if fresh == stale {
		t.Fatalf("the fresh read joined the expired one at sequence %d", stale)
	}
	rec, err := j.Record(ctx, deviceID)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if got := rec.GetOpenReads()[readIface].GetSequence(); got != fresh {
		t.Fatalf("open read = %d, want the fresh one at %d", got, fresh)
	}
	owed := journal.OwedRows(rec, time.Now())
	if len(owed) != 1 || owed[0].Sequence != fresh {
		t.Fatalf("owed = %+v, want the fresh read at %d", owed, fresh)
	}
}
