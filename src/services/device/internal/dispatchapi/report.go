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

// The wire codes a Refused carries. They are the edge access module's
// contract (src/modules/localnet/access): access/no-pending-wait and
// access/unknown-device are exported there and mutation/firmware-epoch lives
// in that module's internal/mutation, so — central not otherwise depending on
// that module, and the internal one being unimportable — they are named here
// rather than imported across the module boundary. Kept together so the
// classification below reads as one contract.
const (
	// codeNoPendingWait answers a CheckpointRequest the edge holds no wait
	// for; once the edge has reported a phase at or past POSSIBLY_APPLIED it
	// is a lost ack, not a lost checkpoint, so central confirms the checkpoint.
	codeNoPendingWait = "access/no-pending-wait"
	// codeFirmwareEpoch refuses an Execute because the firmware epoch changed;
	// terminal, disposes the mutation REJECTED.
	codeFirmwareEpoch = "mutation/firmware-epoch"
	// codeUnknownDevice refuses because the edge does not know the device;
	// terminal only when the registry no longer lists it, else retryable.
	codeUnknownDevice = "access/unknown-device"
)

// Report applies one edge report to the device's record and answers once the
// write is durable. Requirement 5's state transitions are the journal's, which
// U3 proves; this handler is the translation from the wire report to the
// journal call, classifying a result as the open mutation's or an open read's.
func (s *Service) Report(ctx context.Context, req *connect.Request[integrationv1.ReportRequest]) (*connect.Response[integrationv1.ReportResponse], error) {
	deviceID := req.Msg.GetDeviceId()
	if err := s.authorizeDevice(ctx, deviceID); err != nil {
		return nil, connectErr(err)
	}
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
		return nil, connectErr(err)
	}
	return connect.NewResponse(&integrationv1.ReportResponse{}), nil
}

// applyResult applies an ExecuteResult, which answers either the open mutation
// or an open read at the same sequence. It learns the firmware fingerprint
// from any observation's provenance first, then classifies against the record.
func (s *Service) applyResult(ctx context.Context, deviceID string, result *integrationv1.ExecuteResult) error {
	seq := result.GetSequence()
	rec, err := s.cfg.Journal.Record(ctx, deviceID)
	if err != nil {
		return err
	}
	// Classify before writing anything. A result that matches neither the open
	// mutation nor an open read is stale — writing its fingerprint would let a
	// stale re-send overwrite a newer one, and, for a device with no record,
	// would conjure a lane record keyed by whatever id the report carried.
	if m := rec.GetMutation(); m != nil && m.GetSequence() == seq {
		if err := s.learnFingerprint(ctx, deviceID, result); err != nil {
			return err
		}
		rep, ok := mutationReport(result)
		if !ok {
			s.log.WarnContext(ctx, "ignoring a mutation result at an unhandled phase",
				"device", deviceID, "sequence", seq, "phase", result.GetPhaseReached())
			return nil
		}
		return s.cfg.Journal.ApplyReport(ctx, deviceID, rep)
	}
	if _, iface, ok := findReadIface(rec, seq); ok {
		if err := s.learnFingerprint(ctx, deviceID, result); err != nil {
			return err
		}
		return s.closeReadResult(ctx, deviceID, iface, seq, result)
	}
	return nil // neither the open mutation nor an open read: a stale report
}

// learnFingerprint records the firmware fingerprint an observation's
// provenance carries, if any. Called only once the result is classified, so a
// stale report writes nothing.
func (s *Service) learnFingerprint(ctx context.Context, deviceID string, result *integrationv1.ExecuteResult) error {
	obs := result.GetObservation()
	if obs == nil {
		return nil
	}
	fp := obs.GetProvenance().GetFirmwareFingerprint()
	if fp == "" {
		return nil
	}
	return s.cfg.Journal.SetFingerprint(ctx, deviceID, deviceRef(deviceID), fp)
}

// mutationReport maps an ExecuteResult to the journal report its phase names,
// and reports whether the phase is one the handler applies. An error outcome
// is a failure report carrying whether the command was submitted; otherwise
// the phase reached names the transition. An unrecognized phase — a value a
// later schema edit adds, or one the Reporter never sends for a mutation — is
// not forced onto a transition; it returns ok false so the caller ignores it,
// rather than being silently treated as ADMITTED.
func mutationReport(result *integrationv1.ExecuteResult) (journal.Report, bool) {
	seq := result.GetSequence()
	if result.WhichOutcome() == integrationv1.ExecuteResult_Error_case {
		return journal.Report{Kind: journal.ReportError, Sequence: seq, Submitted: result.GetSubmitted()}, true
	}
	var kind journal.ReportKind
	switch result.GetPhaseReached() {
	case accessv1.OperationPhase_OPERATION_PHASE_ADMITTED,
		accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED:
		kind = journal.ReportAdmitted
	case accessv1.OperationPhase_OPERATION_PHASE_RECOVERING:
		kind = journal.ReportRecovering
	case accessv1.OperationPhase_OPERATION_PHASE_VERIFIED,
		accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED:
		kind = journal.ReportVerified
	case accessv1.OperationPhase_OPERATION_PHASE_RELEASED:
		kind = journal.ReportReleased
	case accessv1.OperationPhase_OPERATION_PHASE_ABANDONED:
		kind = journal.ReportAbandoned
	default:
		return journal.Report{}, false
	}
	return journal.Report{Kind: kind, Sequence: seq, Observation: result.GetObservation()}, true
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
		// A refused terminal ack never re-disposes, whatever the code. For a
		// released disposition the edge's refusal closes the record the way
		// RELEASED does; for an abandonment it confirms the ack instead, since
		// the mutation stays held for ResolveDesynchronization and a
		// ReportRefused would be dropped, re-deriving the ack forever.
		rec, err := s.cfg.Journal.Record(ctx, deviceID)
		if err != nil {
			return err
		}
		if m := rec.GetMutation(); m != nil && m.GetDisposition() == accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED {
			return s.cfg.Journal.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportAbandoned, Sequence: seq})
		}
		return s.cfg.Journal.ApplyReport(ctx, deviceID, journal.Report{Kind: journal.ReportRefused, Sequence: seq})
	case integrationv1.DispatchKind_DISPATCH_KIND_EXECUTE:
		terminal, err := s.terminalRefusal(ctx, deviceID, refused.GetCode())
		if err != nil {
			// The registry could not say whether it still lists the device;
			// leave the row owed rather than disposing on a transient failure.
			s.log.WarnContext(ctx, "could not classify an execute refusal; leaving the row owed",
				"device", deviceID, "code", refused.GetCode(), "error", err)
			return nil
		}
		if terminal {
			// A terminal refusal disposes the mutation REJECTED; RejectDispatch
			// frees the lane if the edge never admitted it, or keeps it and owes
			// the terminal ack if it had. The disposal is central's own
			// decision, so central writes the audit record for it, through the
			// emitter rather than from here: the reason the edge refused lives
			// nowhere else once the lane closes.
			return s.rejectDispatch(ctx, deviceID, seq, refused.GetCode())
		}
		return nil // a retryable code leaves the row owed until the operator ends it
	case integrationv1.DispatchKind_DISPATCH_KIND_HOLD_RESOLVED:
		terminal, err := s.terminalRefusal(ctx, deviceID, refused.GetCode())
		if err != nil {
			s.log.WarnContext(ctx, "could not classify a hold-resolved refusal; leaving the row owed",
				"device", deviceID, "code", refused.GetCode(), "error", err)
			return nil
		}
		if terminal {
			// The edge durably cannot act on the hold — it does not know the
			// device, or the device is re-homed — so its lane hold does not
			// exist and the obligation is moot, not pending. Remove the member
			// rather than owe it forever against a peer that cannot discharge it.
			return s.cfg.Journal.ConfirmHoldResolved(ctx, deviceID, seq)
		}
		return nil // a retryable hold-resolved refusal leaves the row owed for re-send
	default:
		return nil
	}
}

// authorizeDevice binds a report to the edge the assertion names: the calling
// edge must host the device the report concerns, or an edge could drive
// another edge's devices. A resolve failure is surfaced, not treated as
// permission granted.
func (s *Service) authorizeDevice(ctx context.Context, deviceID string) error {
	edgeID, err := s.cfg.EdgeID(ctx)
	if err != nil {
		return errs.From(err).Code(ErrCodeEdge).Msg("identify reporting edge")
	}
	hosts, err := s.cfg.Resolver.Hosts(ctx, edgeID, deviceID)
	if err != nil {
		return errs.From(err).Code(ErrCodeResolve).Attr("edge", edgeID).Attr("device", deviceID).Msg("resolve edge-device binding")
	}
	if !hosts {
		return errs.New().Code(ErrCodeForbidden).Attr("edge", edgeID).Attr("device", deviceID).
			Msg("edge does not host this device")
	}
	return nil
}

// terminalRefusal reports whether an Execute refusal code disposes the
// mutation rather than leaving the row owed: a firmware-epoch mismatch always,
// and an unknown device only when the registry no longer lists it. Every other
// code, listed retryable or unlisted, leaves the row owed. A failure to tell
// whether the registry lists the device returns the error, and the caller
// leaves the row owed rather than disposing on a transient failure.
func (s *Service) terminalRefusal(ctx context.Context, deviceID, code string) (bool, error) {
	switch code {
	case codeFirmwareEpoch:
		return true, nil
	case codeUnknownDevice:
		listed, err := s.cfg.Resolver.Lists(ctx, deviceID)
		if err != nil {
			return false, err
		}
		return !listed, nil
	default:
		return false, nil
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

// rejectDispatch disposes the mutation and records why. The record is written
// after the disposal, not before: the disposal is what the operator's next
// call reads, and a failed publish must not leave a mutation the edge refused
// still holding the lane. A lost record is reported, never swallowed.
func (s *Service) rejectDispatch(ctx context.Context, deviceID string, sequence uint64, code string) error {
	rec, err := s.cfg.Journal.Record(ctx, deviceID)
	if err != nil {
		return err
	}
	from := rec.GetMutation().GetPhase()
	device := rec.GetDevice()

	state, err := s.cfg.Journal.RejectDispatch(ctx, deviceID, sequence)
	if err != nil {
		return err
	}
	if state == nil {
		return nil // already terminal, or a stale sequence: nothing was disposed
	}
	if s.cfg.Audit == nil {
		s.log.WarnContext(ctx, "central rejected a dispatch with no audit emitter wired; the reason is not recorded",
			"device", deviceID, "sequence", sequence, "code", code)
		return nil
	}
	return s.cfg.Audit.DispatchRejected(ctx, device, state, from, code)
}
