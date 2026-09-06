package conformance

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	errsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/errs/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
)

var executeDeadline = timestamppb.New(edgeIssuedAt.Add(30 * time.Second))

func executeRequest() integrationv1.ExecuteRequest_builder {
	return integrationv1.ExecuteRequest_builder{
		Sequence:       proto.Uint64(42),
		Deadline:       executeDeadline,
		IdempotencyKey: proto.String(idempotencyKey),
		Mutation:       mutationIntent().Build(),
	}
}

func TestExecuteRequestRules(t *testing.T) {
	asRead := executeRequest()
	asRead.Mutation = nil
	asRead.Read = typedRead("ethernet 1/1/1")

	resumed := executeRequest()
	resumed.Resume = true
	resumed.SubmittedAt = timestamppb.New(edgeIssuedAt)

	resumedWithoutTime := executeRequest()
	resumedWithoutTime.Resume = true

	timeWithoutResume := executeRequest()
	timeWithoutResume.SubmittedAt = timestamppb.New(edgeIssuedAt)

	resumedRead := executeRequest()
	resumedRead.Mutation = nil
	resumedRead.Read = typedRead("ethernet 1/1/1")
	resumedRead.Resume = true
	resumedRead.SubmittedAt = timestamppb.New(edgeIssuedAt)

	noOperation := executeRequest()
	noOperation.Mutation = nil

	noSequence := executeRequest()
	noSequence.Sequence = nil

	sequenceZero := executeRequest()
	sequenceZero.Sequence = proto.Uint64(0)

	noDeadline := executeRequest()
	noDeadline.Deadline = nil

	noKey := executeRequest()
	noKey.IdempotencyKey = nil

	tests := []validationCase{
		{name: "mutation dispatch is valid", message: executeRequest().Build(), wantValid: true},
		{name: "read dispatch is valid", message: asRead.Build(), wantValid: true},
		{name: "neither operation set is rejected", message: noOperation.Build()},
		{name: "sequence is required", message: noSequence.Build()},
		{name: "sequence zero is rejected", message: sequenceZero.Build()},
		{name: "deadline is required", message: noDeadline.Build()},
		{name: "idempotency key is required", message: noKey.Build()},
		{name: "resumed mutation carries its submission time", message: resumed.Build(), wantValid: true},
		{name: "resume without a submission time is rejected", message: resumedWithoutTime.Build()},
		{name: "submission time without resume is rejected", message: timeWithoutResume.Build()},
		{name: "a read cannot be resumed", message: resumedRead.Build()},
	}

	runValidationCases(t, tests)
}

func executeResult() integrationv1.ExecuteResult_builder {
	return integrationv1.ExecuteResult_builder{
		Sequence:     proto.Uint64(42),
		PhaseReached: accessv1.OperationPhase_OPERATION_PHASE_OBSERVING.Enum(),
		Observation:  interfaceObservation().Build(),
	}
}

func TestExecuteResultRules(t *testing.T) {
	asError := executeResult()
	asError.Observation = nil
	asError.Error = errsv1.ErrorPayload_builder{Message: proto.String("device unreachable")}.Build()

	asProgress := executeResult()
	asProgress.Observation = nil
	asProgress.Progress = &integrationv1.Progress{}
	asProgress.PhaseReached = accessv1.OperationPhase_OPERATION_PHASE_ADMITTED.Enum()

	submitted := executeResult()
	submitted.Submitted = true

	noOutcome := executeResult()
	noOutcome.Observation = nil

	noPhase := executeResult()
	noPhase.PhaseReached = nil

	sequenceZero := executeResult()
	sequenceZero.Sequence = proto.Uint64(0)

	tests := []validationCase{
		{name: "observation outcome is valid", message: executeResult().Build(), wantValid: true},
		{name: "error outcome is valid", message: asError.Build(), wantValid: true},
		{name: "progress outcome is valid", message: asProgress.Build(), wantValid: true},
		{name: "submitted report is valid", message: submitted.Build(), wantValid: true},
		{name: "neither outcome set is rejected", message: noOutcome.Build()},
		{name: "phase reached is required", message: noPhase.Build()},
		{name: "sequence zero is rejected", message: sequenceZero.Build()},
	}

	runValidationCases(t, tests)
}

func TestCheckpointAndTerminalAckRules(t *testing.T) {
	tests := []validationCase{
		{name: "checkpoint request is valid", message: integrationv1.CheckpointRequest_builder{Sequence: proto.Uint64(42)}.Build(), wantValid: true},
		{name: "checkpoint request sequence zero is rejected", message: integrationv1.CheckpointRequest_builder{Sequence: proto.Uint64(0)}.Build()},
		{name: "checkpoint ack is valid", message: integrationv1.CheckpointAck_builder{Sequence: proto.Uint64(42)}.Build(), wantValid: true},
		{name: "checkpoint ack sequence zero is rejected", message: integrationv1.CheckpointAck_builder{Sequence: proto.Uint64(0)}.Build()},
		{
			name: "terminal ack is valid",
			message: integrationv1.TerminalResultAck_builder{
				Sequence:    proto.Uint64(42),
				Disposition: accessv1.Disposition_DISPOSITION_VERIFIED.Enum(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "terminal ack without disposition is rejected",
			message: integrationv1.TerminalResultAck_builder{
				Sequence: proto.Uint64(42),
			}.Build(),
		},
	}

	runValidationCases(t, tests)
}

func TestHoldResolvedRules(t *testing.T) {
	runValidationCases(t, []validationCase{
		{name: "hold resolved is valid", message: integrationv1.HoldResolved_builder{Sequence: proto.Uint64(42)}.Build(), wantValid: true},
		{name: "hold resolved sequence zero is rejected", message: integrationv1.HoldResolved_builder{Sequence: proto.Uint64(0)}.Build()},
		{name: "hold resolved ack is valid", message: integrationv1.HoldResolvedAck_builder{Sequence: proto.Uint64(42)}.Build(), wantValid: true},
		{name: "hold resolved ack without a sequence is rejected", message: integrationv1.HoldResolvedAck_builder{}.Build()},
	})
}

func refused(code string) integrationv1.Refused_builder {
	return integrationv1.Refused_builder{
		Sequence: proto.Uint64(42),
		Kind:     integrationv1.DispatchKind_DISPATCH_KIND_CHECKPOINT.Enum(),
		Code:     proto.String(code),
	}
}

func TestRefusedAndOnboardedRules(t *testing.T) {
	noKind := refused("access/no-pending-wait")
	noKind.Kind = nil

	unspecifiedKind := refused("access/no-pending-wait")
	unspecifiedKind.Kind = integrationv1.DispatchKind_DISPATCH_KIND_UNSPECIFIED.Enum()

	runValidationCases(t, []validationCase{
		{name: "refusal with a lane code is valid", message: refused("access/no-pending-wait").Build(), wantValid: true},
		{name: "refusal code must be package slash name", message: refused("NoPendingWait").Build()},
		{name: "refusal without a kind is rejected", message: noKind.Build()},
		{name: "refusal with the zero kind is rejected", message: unspecifiedKind.Build()},
		{name: "onboarded with a fingerprint is valid", message: integrationv1.Onboarded_builder{FirmwareFingerprint: proto.String(fingerprint)}.Build(), wantValid: true},
		{name: "onboarded without a fingerprint is rejected", message: integrationv1.Onboarded_builder{}.Build()},
	})
}

func TestDispatchStreamRules(t *testing.T) {
	runValidationCases(t, []validationCase{
		{
			name: "execute dispatch names its device",
			message: integrationv1.SubscribeResponse_builder{
				DeviceId: proto.String(deviceID),
				Execute:  executeRequest().Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "hold resolved dispatch is valid",
			message: integrationv1.SubscribeResponse_builder{
				DeviceId:     proto.String(deviceID),
				HoldResolved: integrationv1.HoldResolved_builder{Sequence: proto.Uint64(42)}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "dispatch without a device is rejected",
			message: integrationv1.SubscribeResponse_builder{
				Execute: executeRequest().Build(),
			}.Build(),
		},
		{
			name:    "dispatch without a message is rejected",
			message: integrationv1.SubscribeResponse_builder{DeviceId: proto.String(deviceID)}.Build(),
		},
		{
			name: "result report is valid",
			message: integrationv1.ReportRequest_builder{
				DeviceId: proto.String(deviceID),
				Result:   executeResult().Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "onboarded report is valid",
			message: integrationv1.ReportRequest_builder{
				DeviceId:  proto.String(deviceID),
				Onboarded: integrationv1.Onboarded_builder{FirmwareFingerprint: proto.String(fingerprint)}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "report with a device that is not a uuid is rejected",
			message: integrationv1.ReportRequest_builder{
				DeviceId: proto.String("switch-1"),
				Result:   executeResult().Build(),
			}.Build(),
		},
		{
			name:    "report without an arm is rejected",
			message: integrationv1.ReportRequest_builder{DeviceId: proto.String(deviceID)}.Build(),
		},
	})
}
