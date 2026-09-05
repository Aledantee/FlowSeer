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
	asRead.Read = accessv1.TypedRead_builder{Interface: interfaceReadIntent("ethernet 1/1/1")}.Build()

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

	noOutcome := executeResult()
	noOutcome.Observation = nil

	noPhase := executeResult()
	noPhase.PhaseReached = nil

	sequenceZero := executeResult()
	sequenceZero.Sequence = proto.Uint64(0)

	tests := []validationCase{
		{name: "observation outcome is valid", message: executeResult().Build(), wantValid: true},
		{name: "error outcome is valid", message: asError.Build(), wantValid: true},
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
