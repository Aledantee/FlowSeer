package dispatchapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect"

	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
)

func report(t *testing.T, svc *Service, req *dispatchv1.ReportRequest) {
	t.Helper()
	if _, err := svc.Report(context.Background(), connect.NewRequest(req)); err != nil {
		t.Fatalf("report: %v", err)
	}
}

func resultReport(seq uint64, phase accessv1.OperationPhase, outcome func(*dispatchv1.ExecuteResult)) *dispatchv1.ReportRequest {
	result := &dispatchv1.ExecuteResult{}
	result.SetSequence(seq)
	result.SetPhaseReached(phase)
	outcome(result)
	req := &dispatchv1.ReportRequest{}
	req.SetDeviceId(deviceID)
	req.SetResult(result)
	return req
}

func TestReportResultConfirmsDispatch(t *testing.T) {
	svc, j, _ := newFixture(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000b01"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	report(t, svc, resultReport(1, accessv1.OperationPhase_OPERATION_PHASE_ADMITTED, func(r *dispatchv1.ExecuteResult) {
		r.SetProgress(&dispatchv1.Progress{})
	}))
	rec, _ := j.Record(ctx, deviceID)
	if !rec.GetDispatched() || !rec.GetDispatchConfirmed() ||
		rec.GetMutation().GetPhase() != accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED {
		t.Fatalf("after an ADMITTED result: dispatched=%v confirmed=%v phase=%v",
			rec.GetDispatched(), rec.GetDispatchConfirmed(), rec.GetMutation().GetPhase())
	}
}

func TestReportObservationClosesReadAndLearnsFingerprint(t *testing.T) {
	svc, j, _ := newFixture(t)
	ctx := context.Background()
	seq, err := j.OpenRead(ctx, deviceID, deviceRef(deviceID), "ethernet 1/1/1", typedRead(), "0192e6a0-0000-7000-8000-000000000f04", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("open read: %v", err)
	}
	obs := &accessv1.InterfaceObservation{}
	obs.SetInterfaceName("ethernet 1/1/1")
	prov := &inventoryv1.Provenance{}
	prov.SetFirmwareFingerprint("ICX7150-24P SPS10010h")
	obs.SetProvenance(prov)
	report(t, svc, resultReport(seq, accessv1.OperationPhase_OPERATION_PHASE_OBSERVING, func(r *dispatchv1.ExecuteResult) {
		r.SetObservation(obs)
	}))
	rec, _ := j.Record(ctx, deviceID)
	if !rec.GetOpenReads()["ethernet 1/1/1"].HasObservation() {
		t.Fatal("the read was not closed with its observation")
	}
	if rec.GetFirmwareFingerprint() != "ICX7150-24P SPS10010h" {
		t.Fatalf("fingerprint = %q, want the observation's provenance", rec.GetFirmwareFingerprint())
	}
}

func TestReportOnboardedClearsConfirmationsAndLearnsFingerprint(t *testing.T) {
	svc, j, _ := newFixture(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000b02"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: 1}); err != nil {
		t.Fatalf("admitted: %v", err)
	}
	onboarded := &dispatchv1.Onboarded{}
	onboarded.SetFirmwareFingerprint("ICX7150-24P SPS10010i")
	req := &dispatchv1.ReportRequest{}
	req.SetDeviceId(deviceID)
	req.SetOnboarded(onboarded)
	report(t, svc, req)
	rec, _ := j.Record(ctx, deviceID)
	if rec.GetDispatchConfirmed() {
		t.Fatal("Onboarded did not clear the dispatch confirmation")
	}
	if rec.GetFirmwareFingerprint() != "ICX7150-24P SPS10010i" {
		t.Fatalf("fingerprint = %q, want the onboarded value", rec.GetFirmwareFingerprint())
	}
}

func TestReportRefusedTerminalAckClosesRecord(t *testing.T) {
	svc, j, _ := newFixture(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000b03"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	for _, r := range []journal.Report{
		{Kind: journal.ReportAdmitted, Sequence: 1},
		{Kind: journal.ReportVerified, Sequence: 1},
	} {
		if err := j.ApplyReport(ctx, deviceID, r); err != nil {
			t.Fatalf("apply %v: %v", r.Kind, err)
		}
	}
	report(t, svc, refusedReport(dispatchv1.DispatchKind_DISPATCH_KIND_TERMINAL_ACK, "access/lane-closed"))
	rec, _ := j.Record(ctx, deviceID)
	if rec.HasMutation() {
		t.Fatal("a refused terminal ack did not close the released mutation")
	}
}

func TestReportRefusedFirmwareEpochDisposesRejected(t *testing.T) {
	svc, j, _ := newFixture(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000b04"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	report(t, svc, refusedReport(dispatchv1.DispatchKind_DISPATCH_KIND_EXECUTE, "mutation/firmware-epoch"))
	rec, _ := j.Record(ctx, deviceID)
	// The refusal frees the lane without setting dispatched or owing a terminal
	// ack for a command the edge refused before it reached the device.
	if rec.HasMutation() {
		t.Fatalf("firmware-epoch refusal left a mutation open: %+v", rec.GetMutation())
	}
	if rows := journal.OwedRows(rec, time.Now()); len(rows) != 0 {
		t.Fatalf("firmware-epoch refusal left rows owed: %v", rows)
	}
}

// A CheckpointAck is the checkpoint's confirmation, with no phase condition on
// it. The phase condition belongs to the refusal path below, and applying it
// here refused every legitimate ack: the ADMITTED report leaves the last
// reported phase at ADMITTED and nothing advances it before the ack arrives,
// so the checkpoint row re-derived on every relay pass, the edge answered each
// resend with access/no-pending-wait, and the mutation never got past its
// barrier.
func TestReportCheckpointAckConfirmsTheCheckpoint(t *testing.T) {
	svc, j, _ := newFixture(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000b0c"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: 1}); err != nil {
		t.Fatalf("admitted: %v", err)
	}
	rec, _ := j.Record(ctx, deviceID)
	if rows := journal.OwedRows(rec, time.Now()); len(rows) != 1 || rows[0].Kind != journal.OwedCheckpoint {
		t.Fatalf("owed = %+v, want the checkpoint row", rows)
	}

	ack := &dispatchv1.CheckpointAck{}
	ack.SetSequence(1)
	req := &dispatchv1.ReportRequest{}
	req.SetDeviceId(deviceID)
	req.SetCheckpointAck(ack)
	report(t, svc, req)

	rec, _ = j.Record(ctx, deviceID)
	if !rec.GetCheckpointConfirmed() {
		t.Fatal("a CheckpointAck did not confirm the checkpoint")
	}
	if rows := journal.OwedRows(rec, time.Now()); len(rows) != 0 {
		t.Fatalf("owed = %+v, want nothing once the checkpoint is confirmed", rows)
	}
}

func TestReportRefusedCheckpointConfirmsOnlyPastCheckpoint(t *testing.T) {
	svc, j, _ := newFixture(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000b05"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: 1}); err != nil {
		t.Fatalf("admitted: %v", err)
	}
	// At ADMITTED the refusal leaves the checkpoint owed: the checkpoint was
	// not received.
	report(t, svc, refusedReport(dispatchv1.DispatchKind_DISPATCH_KIND_CHECKPOINT, CodeNoPendingWait))
	rec, _ := j.Record(ctx, deviceID)
	if rec.GetCheckpointConfirmed() {
		t.Fatal("a no-pending-wait refusal at ADMITTED wrongly confirmed the checkpoint")
	}
	// Once the edge reports a phase past POSSIBLY_APPLIED, the same refusal is
	// a lost ack: confirm the checkpoint.
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportRecovering, Sequence: 1}); err != nil {
		t.Fatalf("recovering: %v", err)
	}
	report(t, svc, refusedReport(dispatchv1.DispatchKind_DISPATCH_KIND_CHECKPOINT, CodeNoPendingWait))
	rec, _ = j.Record(ctx, deviceID)
	if !rec.GetCheckpointConfirmed() {
		t.Fatal("a no-pending-wait refusal past POSSIBLY_APPLIED did not confirm the checkpoint")
	}
}

func TestRetryableExecuteRefusalLeavesTheRowOwed(t *testing.T) {
	svc, j, _ := newFixture(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000b06"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	// A retryable code is not terminal: the record is unchanged and the execute
	// row stays owed until the edge admits it or an operator ends it.
	report(t, svc, refusedReport(dispatchv1.DispatchKind_DISPATCH_KIND_EXECUTE, "access/lane-closed"))
	rec, _ := j.Record(ctx, deviceID)
	if rec.GetMutation() == nil || rec.GetMutation().HasDisposition() {
		t.Fatalf("a retryable refusal disposed the mutation: %+v", rec.GetMutation())
	}
	if len(pass(t, svc)) != 1 {
		t.Fatal("a retryable refusal stopped the execute row being owed")
	}
}

func TestRefusedTerminalAckOnAbandonmentConfirmsIt(t *testing.T) {
	svc, j, _ := newFixture(t)
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000b07"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: 1}); err != nil {
		t.Fatalf("admitted: %v", err)
	}
	if _, err := j.Dispose(ctx, deviceID, 1); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	// The edge holds no machine for an abandoned sequence and refuses the
	// terminal ack. That must confirm the ack, not be dropped and re-sent
	// forever.
	report(t, svc, refusedReport(dispatchv1.DispatchKind_DISPATCH_KIND_TERMINAL_ACK, "access/lane-closed"))
	rec, _ := j.Record(ctx, deviceID)
	for _, o := range journal.OwedRows(rec, time.Now()) {
		if o.Kind == journal.OwedTerminalAck {
			t.Fatal("a refused terminal ack on an abandonment is still owed")
		}
	}
}

// TestARefusedHoldResolvedConfirmsTheRow holds this handler to the contract
// the envelope's README states: a refusal answering a HoldResolved counts as
// its confirmation, because the edge holds nothing for that sequence.
//
// Classifying the refusal instead leaves the member pending against a peer
// that refuses it identically every time — an edge whose onboarding failed
// answers access/unknown-device for a device central still lists, which
// central reads as retryable. The members then accumulate until the pending
// set is full and every further abandon on that device is refused.
func TestARefusedHoldResolvedConfirmsTheRow(t *testing.T) {
	svc, j, _ := newFixture(t)
	ctx := context.Background()
	// A hold gets into the record by abandoning a mutation the edge never
	// reported admitted: the lane closes and the sequence's hold is owed.
	state, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000d05"), edgeRef())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	seq := state.GetSequence()
	if _, err := j.Dispose(ctx, deviceID, seq); err != nil {
		t.Fatalf("dispose: %v", err)
	}
	refused := &dispatchv1.Refused{}
	refused.SetSequence(seq)
	refused.SetKind(dispatchv1.DispatchKind_DISPATCH_KIND_HOLD_RESOLVED)
	refused.SetCode("access/lane-closed")
	req := &dispatchv1.ReportRequest{}
	req.SetDeviceId(deviceID)
	req.SetRefused(refused)
	report(t, svc, req)
	rec, _ := j.Record(ctx, deviceID)
	pending := false
	for _, s := range rec.GetHoldResolutionPending() {
		if s == seq {
			pending = true
		}
	}
	if pending {
		t.Fatal("a hold-resolved refusal left the row owed against an edge that holds nothing for it")
	}
}

func TestReportRefusesADeviceTheEdgeDoesNotHost(t *testing.T) {
	j, kv := newJournalKV(t)
	svc := New(Config{
		Journal:  j,
		Resolver: fakeResolver{lists: true, notHost: true},
		Watch:    kv,
		EdgeID:   func(context.Context) (string, error) { return edgeID, nil },
	})
	req := &dispatchv1.ReportRequest{}
	req.SetDeviceId(deviceID)
	ack := &dispatchv1.CheckpointAck{}
	ack.SetSequence(1)
	req.SetCheckpointAck(ack)
	_, err := svc.Report(context.Background(), connect.NewRequest(req))
	if err == nil {
		t.Fatal("Report accepted a report about a device the edge does not host")
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want permission denied", connect.CodeOf(err))
	}
}

func refusedReport(kind dispatchv1.DispatchKind, code string) *dispatchv1.ReportRequest {
	refused := &dispatchv1.Refused{}
	refused.SetSequence(1)
	refused.SetKind(kind)
	refused.SetCode(code)
	req := &dispatchv1.ReportRequest{}
	req.SetDeviceId(deviceID)
	req.SetRefused(refused)
	return req
}

func TestListsFailureLeavesTheExecuteRowOwed(t *testing.T) {
	j, kv := newJournalKV(t)
	svc := New(Config{
		Journal:  j,
		Resolver: fakeResolver{lists: true, listsErr: errors.New("registry unavailable")},
		Watch:    kv,
		EdgeID:   func(context.Context) (string, error) { return edgeID, nil },
		Resend:   50 * time.Millisecond,
	})
	ctx := context.Background()
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000c01"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}
	// An unknown-device refusal is terminal only if the registry says the
	// device is gone. When the registry cannot answer, the row stays owed
	// rather than disposing on a transient failure.
	report(t, svc, refusedReport(dispatchv1.DispatchKind_DISPATCH_KIND_EXECUTE, "access/unknown-device"))
	rec, _ := j.Record(ctx, deviceID)
	if rec.GetMutation() == nil || rec.GetMutation().HasDisposition() {
		t.Fatalf("a refusal under a Lists failure disposed the mutation: %+v", rec.GetMutation())
	}
	if len(pass(t, svc)) != 1 {
		t.Fatal("a refusal under a Lists failure stopped the execute row being owed")
	}
}

func TestHostsFailureIsSurfaced(t *testing.T) {
	j, kv := newJournalKV(t)
	svc := New(Config{
		Journal:  j,
		Resolver: fakeResolver{lists: true, hostsErr: errors.New("registry unavailable")},
		Watch:    kv,
		EdgeID:   func(context.Context) (string, error) { return edgeID, nil },
	})
	ack := &dispatchv1.CheckpointAck{}
	ack.SetSequence(1)
	req := &dispatchv1.ReportRequest{}
	req.SetDeviceId(deviceID)
	req.SetCheckpointAck(ack)
	// A binding lookup that fails is surfaced, not read as permission granted.
	if _, err := svc.Report(context.Background(), connect.NewRequest(req)); err == nil {
		t.Fatal("a Hosts failure was swallowed; the report was accepted")
	}
}

// The reporting edge is authenticated but not trusted with central's own
// transports: a registry lookup that fails names its bus and its subject, and
// that text must not ride back on the refusal.
func TestReportSendsNoResolverDetailToTheReportingEdge(t *testing.T) {
	const detail = "nats: no responders available for registry lookup"
	j, kv := newJournalKV(t)
	svc := New(Config{
		Journal:  j,
		Resolver: fakeResolver{lists: true, hostsErr: errors.New(detail)},
		Watch:    kv,
		EdgeID:   func(context.Context) (string, error) { return edgeID, nil },
	})
	ack := &dispatchv1.CheckpointAck{}
	ack.SetSequence(1)
	req := &dispatchv1.ReportRequest{}
	req.SetDeviceId(deviceID)
	req.SetCheckpointAck(ack)

	_, err := svc.Report(context.Background(), connect.NewRequest(req))
	if err == nil {
		t.Fatal("a Hosts failure was swallowed; the report was accepted")
	}
	if got := connect.CodeOf(err); got != connect.CodeUnavailable {
		t.Errorf("code = %v, want unavailable", got)
	}
	if strings.Contains(err.Error(), detail) {
		t.Errorf("the edge was sent central's transport detail: %q", err.Error())
	}
}

// recordingAudit captures what central asked to have recorded about its own
// decision.
type recordingAudit struct {
	calls int
	state *accessv1.MutationState
	from  accessv1.OperationPhase
	code  string
	err   error
}

func (r *recordingAudit) DispatchRejected(_ context.Context, _ *inventoryv1.DeviceGlobalRef, state *accessv1.MutationState, from accessv1.OperationPhase, code string) error {
	r.calls++
	r.state, r.from, r.code = state, from, code
	return r.err
}

// A firmware-epoch refusal is terminal, and the reason lives nowhere else once
// the lane closes: the record keeps the disposition, not why it was reached.
func TestATerminalRefusalRecordsWhyCentralRejectedIt(t *testing.T) {
	ctx := context.Background()
	j, kv := newJournalKV(t)
	audit := &recordingAudit{}
	svc := New(Config{
		Journal:  j,
		Resolver: fakeResolver{lists: true},
		Watch:    kv,
		EdgeID:   func(context.Context) (string, error) { return edgeID, nil },
		Audit:    audit,
	})
	state, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000c07"), edgeRef())
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	seq := state.GetSequence()
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAdmitted, Sequence: seq}); err != nil {
		t.Fatalf("admitted: %v", err)
	}

	report(t, svc, refusedReport(dispatchv1.DispatchKind_DISPATCH_KIND_EXECUTE, CodeFirmwareEpoch))

	if audit.calls != 1 {
		t.Fatalf("central recorded %d rejections, want 1", audit.calls)
	}
	if got := audit.state.GetDisposition(); got != accessv1.Disposition_DISPOSITION_REJECTED {
		t.Errorf("recorded disposition = %v, want rejected", got)
	}
	if audit.code != CodeFirmwareEpoch {
		t.Errorf("recorded code = %q, want the refusing code", audit.code)
	}
	if audit.from != accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED {
		t.Errorf("recorded from-phase = %v, want the phase the mutation was in", audit.from)
	}
}

// A retryable refusal disposes nothing, so there is nothing to record: an
// audit record for a mutation still in flight would say it ended when it did
// not.
func TestARetryableRefusalRecordsNothing(t *testing.T) {
	ctx := context.Background()
	j, kv := newJournalKV(t)
	audit := &recordingAudit{}
	svc := New(Config{
		Journal:  j,
		Resolver: fakeResolver{lists: true},
		Watch:    kv,
		EdgeID:   func(context.Context) (string, error) { return edgeID, nil },
		Audit:    audit,
	})
	if _, err := j.Admit(ctx, deviceID, mutationIntent("0192e6a0-0000-7000-8000-000000000c08"), edgeRef()); err != nil {
		t.Fatalf("admit: %v", err)
	}

	report(t, svc, refusedReport(dispatchv1.DispatchKind_DISPATCH_KIND_EXECUTE, "access/unreachable"))

	if audit.calls != 0 {
		t.Errorf("central recorded %d rejections for a retryable refusal, want 0", audit.calls)
	}
}
