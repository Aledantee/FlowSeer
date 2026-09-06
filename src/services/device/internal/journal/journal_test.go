package journal_test

import (
	"context"
	"sync"
	"testing"
	"time"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
)

const deviceID = "0192e6a0-0000-7000-8000-0000000000d1"

func newJournal(t *testing.T) *journal.Journal {
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
	return journal.New(kv, nil)
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

func TestConcurrentAdmitYieldsDistinctSequences(t *testing.T) {
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

func TestApplyReportErrorBeforeSubmissionClosesRatherThanRecovers(t *testing.T) {
	j := newJournal(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-00000000d001"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	// An error at ADMITTED, before the command was submitted (dispatched
	// still false), closes the record rather than entering recovery.
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportError, Sequence: 1, Submitted: false}); err != nil {
		t.Fatalf("error report: %v", err)
	}
	rec, _ := j.Record(ctx, deviceID)
	if rec.HasMutation() {
		t.Fatal("a pre-submission error left a mutation open")
	}
}

func TestDisposeAndResolveHold(t *testing.T) {
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
	// The mutation stays held until ResolveHold clears it.
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAbandoned, Sequence: 1}); err != nil {
		t.Fatalf("abandoned: %v", err)
	}
	rec, _ := j.Record(ctx, deviceID)
	if !rec.HasMutation() {
		t.Fatal("abandonment closed the record; it must stay held for resolution")
	}
	if err := j.ResolveHold(ctx, deviceID, 1); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rec, _ = j.Record(ctx, deviceID)
	if rec.HasMutation() {
		t.Fatal("ResolveHold did not clear the held mutation")
	}
	if !rec.HasHoldResolutionPending() || rec.GetHoldResolutionPending() != 1 {
		t.Fatalf("hold resolution pending = %v", rec.GetHoldResolutionPending())
	}
	if err := j.ConfirmHoldResolved(ctx, deviceID, 1); err != nil {
		t.Fatalf("confirm hold: %v", err)
	}
	rec, _ = j.Record(ctx, deviceID)
	if rec.HasHoldResolutionPending() {
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

	seq, err := j.OpenRead(ctx, deviceID, "ethernet 1/1/1", read, "0192e6a0-0000-7000-8000-00000000f001", time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("open read: %v", err)
	}
	if seq != 1 {
		t.Fatalf("read sequence = %d, want 1", seq)
	}
	// A second poll of the same interface while the first is open skips.
	again, err := j.OpenRead(ctx, deviceID, "ethernet 1/1/1", read, "0192e6a0-0000-7000-8000-00000000f002", time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	if again != 1 {
		t.Fatalf("colliding poll got sequence %d, want the existing 1", again)
	}
	obs := &accessv1.InterfaceObservation{}
	obs.SetInterfaceName("ethernet 1/1/1")
	if err := j.CloseRead(ctx, deviceID, "ethernet 1/1/1", obs, nil); err != nil {
		t.Fatalf("close read: %v", err)
	}
	rec, _ := j.Record(ctx, deviceID)
	if !rec.GetOpenReads()["ethernet 1/1/1"].HasObservation() {
		t.Fatal("CloseRead did not record the observation")
	}
}
