package dispatchapi

import (
	"context"
	"testing"
	"time"

	connect "connectrpc.com/connect"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
)

func report(t *testing.T, svc *Service, req *integrationv1.ReportRequest) {
	t.Helper()
	if _, err := svc.Report(context.Background(), connect.NewRequest(req)); err != nil {
		t.Fatalf("report: %v", err)
	}
}

func resultReport(seq uint64, phase accessv1.OperationPhase, outcome func(*integrationv1.ExecuteResult)) *integrationv1.ReportRequest {
	result := &integrationv1.ExecuteResult{}
	result.SetSequence(seq)
	result.SetPhaseReached(phase)
	outcome(result)
	req := &integrationv1.ReportRequest{}
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
	report(t, svc, resultReport(1, accessv1.OperationPhase_OPERATION_PHASE_ADMITTED, func(r *integrationv1.ExecuteResult) {
		r.SetProgress(&integrationv1.Progress{})
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
	report(t, svc, resultReport(seq, accessv1.OperationPhase_OPERATION_PHASE_OBSERVING, func(r *integrationv1.ExecuteResult) {
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
	onboarded := &integrationv1.Onboarded{}
	onboarded.SetFirmwareFingerprint("ICX7150-24P SPS10010i")
	req := &integrationv1.ReportRequest{}
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
	report(t, svc, refusedReport(integrationv1.DispatchKind_DISPATCH_KIND_TERMINAL_ACK, "access/lane-closed"))
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
	report(t, svc, refusedReport(integrationv1.DispatchKind_DISPATCH_KIND_EXECUTE, "mutation/firmware-epoch"))
	rec, _ := j.Record(ctx, deviceID)
	if rec.GetMutation().GetDisposition() != accessv1.Disposition_DISPOSITION_REJECTED {
		t.Fatalf("firmware-epoch refusal disposed %v, want REJECTED", rec.GetMutation().GetDisposition())
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
	report(t, svc, refusedReport(integrationv1.DispatchKind_DISPATCH_KIND_CHECKPOINT, codeNoPendingWait))
	rec, _ := j.Record(ctx, deviceID)
	if rec.GetCheckpointConfirmed() {
		t.Fatal("a no-pending-wait refusal at ADMITTED wrongly confirmed the checkpoint")
	}
	// Once the edge reports a phase past POSSIBLY_APPLIED, the same refusal is
	// a lost ack: confirm the checkpoint.
	if err := j.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportRecovering, Sequence: 1}); err != nil {
		t.Fatalf("recovering: %v", err)
	}
	report(t, svc, refusedReport(integrationv1.DispatchKind_DISPATCH_KIND_CHECKPOINT, codeNoPendingWait))
	rec, _ = j.Record(ctx, deviceID)
	if !rec.GetCheckpointConfirmed() {
		t.Fatal("a no-pending-wait refusal past POSSIBLY_APPLIED did not confirm the checkpoint")
	}
}

func refusedReport(kind integrationv1.DispatchKind, code string) *integrationv1.ReportRequest {
	refused := &integrationv1.Refused{}
	refused.SetSequence(1)
	refused.SetKind(kind)
	refused.SetCode(code)
	req := &integrationv1.ReportRequest{}
	req.SetDeviceId(deviceID)
	req.SetRefused(refused)
	return req
}
