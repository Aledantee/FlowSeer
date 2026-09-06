package dispatchapi

import (
	"context"

	connect "connectrpc.com/connect"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/journal"
)

// ErrCodeReport is a report central could not classify or apply.
var ErrCodeReport = errs.NewCode("dispatchapi/report")

// no-pending-wait is the lane's code for a checkpoint the edge holds no wait
// for. Answering a CheckpointRequest with it, once the edge has reported a
// phase at or past POSSIBLY_APPLIED, is a lost ack rather than a lost
// checkpoint, so central marks the checkpoint confirmed.
const codeNoPendingWait = "access/no-pending-wait"

// Report applies one edge report to the device's record and answers once the
// write is durable. Requirement 5's state transitions are the journal's, which
// U3 proves; this handler is the translation from the wire report to the
// journal call, classifying a result as the open mutation's or an open read's.
func (s *Service) Report(ctx context.Context, req *connect.Request[integrationv1.ReportRequest]) (*connect.Response[integrationv1.ReportResponse], error) {
	deviceID := req.Msg.GetDeviceId()
	var err error
	switch req.Msg.WhichReport() {
	case integrationv1.ReportRequest_Result_case:
		err = s.applyResult(ctx, deviceID, req.Msg.GetResult())
	case integrationv1.ReportRequest_CheckpointAck_case:
		err = s.cfg.Journal.ConfirmCheckpoint(ctx, deviceID, req.Msg.GetCheckpointAck().GetSequence())
	case integrationv1.ReportRequest_HoldResolvedAck_case:
		err = s.cfg.Journal.ConfirmHoldResolved(ctx, deviceID, req.Msg.GetHoldResolvedAck().GetSequence())
	case integrationv1.ReportRequest_Refused_case:
		err = s.applyRefused(ctx, deviceID, req.Msg.GetRefused())
	case integrationv1.ReportRequest_Onboarded_case:
		err = s.applyOnboarded(ctx, deviceID, req.Msg.GetOnboarded())
	default:
		err = errs.New().Code(ErrCodeReport).Attr("device", deviceID).Msg("report carries no known arm")
	}
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&integrationv1.ReportResponse{}), nil
}

// applyResult applies an ExecuteResult, which answers either the open mutation
// or an open read at the same sequence. It learns the firmware fingerprint
// from any observation's provenance first, then classifies against the record.
func (s *Service) applyResult(ctx context.Context, deviceID string, result *integrationv1.ExecuteResult) error {
	seq := result.GetSequence()
	if obs := result.GetObservation(); obs != nil {
		if fp := obs.GetProvenance().GetFirmwareFingerprint(); fp != "" {
			if err := s.cfg.Journal.SetFingerprint(ctx, deviceID, deviceRef(deviceID), fp); err != nil {
				return err
			}
		}
	}
	rec, err := s.cfg.Journal.Record(ctx, deviceID)
	if err != nil {
		return err
	}
	if m := rec.GetMutation(); m != nil && m.GetSequence() == seq {
		return s.cfg.Journal.ApplyReport(ctx, deviceID, mutationReport(result))
	}
	if _, iface, ok := findReadIface(rec, seq); ok {
		return s.closeReadResult(ctx, deviceID, iface, seq, result)
	}
	return nil // neither the open mutation nor an open read: a stale report
}

// mutationReport maps an ExecuteResult to the journal report its phase names.
// An error outcome is a failure report, carrying whether the command was
// submitted; otherwise the phase reached names the transition.
func mutationReport(result *integrationv1.ExecuteResult) journal.Report {
	seq := result.GetSequence()
	if result.WhichOutcome() == integrationv1.ExecuteResult_Error_case {
		return journal.Report{Kind: journal.ReportError, Sequence: seq, Submitted: result.GetSubmitted()}
	}
	var kind journal.ReportKind
	switch result.GetPhaseReached() {
	case accessv1.OperationPhase_OPERATION_PHASE_ADMITTED,
		accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED:
		kind = journal.ReportAdmitted
	case accessv1.OperationPhase_OPERATION_PHASE_OBSERVING,
		accessv1.OperationPhase_OPERATION_PHASE_RECOVERING:
		kind = journal.ReportRecovering
	case accessv1.OperationPhase_OPERATION_PHASE_VERIFIED,
		accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED:
		kind = journal.ReportVerified
	case accessv1.OperationPhase_OPERATION_PHASE_RELEASED:
		kind = journal.ReportReleased
	case accessv1.OperationPhase_OPERATION_PHASE_ABANDONED:
		kind = journal.ReportAbandoned
	default:
		kind = journal.ReportAdmitted
	}
	return journal.Report{Kind: kind, Sequence: seq}
}

// closeReadResult closes an open read with its observation or error; a
// progress report on a read records nothing.
func (s *Service) closeReadResult(ctx context.Context, deviceID, iface string, seq uint64, result *integrationv1.ExecuteResult) error {
	switch result.WhichOutcome() {
	case integrationv1.ExecuteResult_Observation_case:
		return s.cfg.Journal.CloseRead(ctx, deviceID, iface, seq, result.GetObservation(), nil)
	case integrationv1.ExecuteResult_Error_case:
		return s.cfg.Journal.CloseRead(ctx, deviceID, iface, seq, nil, result.GetError())
	default:
		return nil
	}
}

// applyRefused applies a row-level negative confirmation. The dispatch kind
// says which row the refusal answers, and the code decides whether central
// keeps owing the row or disposes the mutation.
func (s *Service) applyRefused(ctx context.Context, deviceID string, refused *integrationv1.Refused) error {
	seq := refused.GetSequence()
	switch refused.GetKind() {
	case integrationv1.DispatchKind_DISPATCH_KIND_CHECKPOINT:
		if refused.GetCode() != codeNoPendingWait {
			return nil // an unlisted checkpoint refusal leaves the row owed
		}
		rec, err := s.cfg.Journal.Record(ctx, deviceID)
		if err != nil {
			return err
		}
		if pastCheckpoint(rec.GetLastReportedPhase()) {
			return s.cfg.Journal.ConfirmCheckpoint(ctx, deviceID, seq)
		}
		return nil // at ADMITTED the row stays owed; the checkpoint was not received
	case integrationv1.DispatchKind_DISPATCH_KIND_TERMINAL_ACK:
		// A refused terminal ack closes the record the way RELEASED does and
		// never re-disposes the mutation, whatever the code.
		return s.cfg.Journal.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportRefused, Sequence: seq})
	case integrationv1.DispatchKind_DISPATCH_KIND_EXECUTE:
		if s.terminalRefusal(ctx, deviceID, refused.GetCode()) {
			return s.cfg.Journal.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportError, Sequence: seq})
		}
		return nil // a retryable code leaves the row owed until the operator ends it
	default:
		return nil // a hold-resolved refusal leaves the row owed for re-send
	}
}

// terminalRefusal reports whether an Execute refusal code disposes the
// mutation rather than leaving the row owed: a firmware-epoch mismatch always,
// and an unknown device only when the registry no longer lists it. Every other
// code, listed retryable or unlisted, leaves the row owed.
func (s *Service) terminalRefusal(ctx context.Context, deviceID, code string) bool {
	switch code {
	case "mutation/firmware-epoch":
		return true
	case "access/unknown-device":
		return !s.cfg.Resolver.Lists(ctx, deviceID)
	default:
		return false
	}
}

// applyOnboarded clears the per-dispatch confirmations and records the
// fingerprint the identity probe learned.
func (s *Service) applyOnboarded(ctx context.Context, deviceID string, onboarded *integrationv1.Onboarded) error {
	if err := s.cfg.Journal.MarkOnboarded(ctx, deviceID); err != nil {
		return err
	}
	return s.cfg.Journal.SetFingerprint(ctx, deviceID, deviceRef(deviceID), onboarded.GetFirmwareFingerprint())
}

// pastCheckpoint reports whether a reported phase is at or past
// POSSIBLY_APPLIED. Reports set the last phase to ADMITTED, RECOVERING,
// VERIFIED, or ABANDONED; every one but ADMITTED is past the checkpoint.
func pastCheckpoint(phase accessv1.OperationPhase) bool {
	return phase >= accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED
}

// deviceRef builds the device ref a record write needs from the device id the
// report carries.
func deviceRef(id string) *inventoryv1.DeviceGlobalRef {
	local := &inventoryv1.DeviceLocalRef{}
	local.SetId(id)
	ref := &inventoryv1.DeviceGlobalRef{}
	ref.SetDevice(local)
	return ref
}
