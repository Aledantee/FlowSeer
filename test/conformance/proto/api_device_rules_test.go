package conformance

import (
	"testing"

	"google.golang.org/protobuf/proto"

	devicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
)

func TestDeviceReadInterfaceRules(t *testing.T) {
	partialInterface := interfaceObservation()
	partialInterface.Completeness = accessv1.Completeness_COMPLETENESS_PARTIAL.Enum()

	tests := []validationCase{
		{
			name: "device and interface name are valid",
			message: devicev1.ReadInterfaceRequest_builder{
				Device:        deviceRef(deviceID),
				InterfaceName: proto.String("ethernet 1/1/1"),
			}.Build(),
			wantValid: true,
		},
		{
			name:    "device is required",
			message: devicev1.ReadInterfaceRequest_builder{InterfaceName: proto.String("ethernet 1/1/1")}.Build(),
		},
		{
			name:    "interface name is required",
			message: devicev1.ReadInterfaceRequest_builder{Device: deviceRef(deviceID)}.Build(),
		},
		{
			name: "response requires the observation",
			message: devicev1.ReadInterfaceResponse_builder{
				Interface: interfaceObservation().Build(),
			}.Build(),
			wantValid: true,
		},
		{name: "empty response is rejected", message: devicev1.ReadInterfaceResponse_builder{}.Build()},
		{
			name: "a partial observation is never the answer",
			message: devicev1.ReadInterfaceResponse_builder{
				Interface: partialInterface.Build(),
			}.Build(),
		},
	}

	runValidationCases(t, tests)
}

func TestDeviceApplyInterfaceDescriptionRules(t *testing.T) {
	tests := []validationCase{
		{
			name:      "intent with an interface description is valid",
			message:   devicev1.ApplyInterfaceDescriptionRequest_builder{Intent: mutationIntent().Build()}.Build(),
			wantValid: true,
		},
		{
			name: "validate_only with an intent is valid",
			message: devicev1.ApplyInterfaceDescriptionRequest_builder{
				Intent:       mutationIntent().Build(),
				ValidateOnly: proto.Bool(true),
			}.Build(),
			wantValid: true,
		},
		{name: "intent is required", message: devicev1.ApplyInterfaceDescriptionRequest_builder{}.Build()},
		{
			name:      "response without a mutation is valid for validate_only",
			message:   devicev1.ApplyInterfaceDescriptionResponse_builder{}.Build(),
			wantValid: true,
		},
		{
			name: "response with an admitted mutation is valid",
			message: devicev1.ApplyInterfaceDescriptionResponse_builder{
				Mutation: mutationState(accessv1.OperationPhase_OPERATION_PHASE_ADMITTED).Build(),
			}.Build(),
			wantValid: true,
		},
	}

	runValidationCases(t, tests)
}

func TestDeviceAccessStatusRules(t *testing.T) {
	released := mutationState(accessv1.OperationPhase_OPERATION_PHASE_RELEASED)
	released.Disposition = accessv1.Disposition_DISPOSITION_VERIFIED.Enum()

	acknowledged := mutationState(accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED)
	acknowledged.Disposition = accessv1.Disposition_DISPOSITION_VERIFIED.Enum()

	tests := []validationCase{
		{
			name:      "fresh device has watermark zero and nothing else",
			message:   devicev1.GetDeviceAccessStatusResponse_builder{HighWatermark: proto.Uint64(0)}.Build(),
			wantValid: true,
		},
		{
			name: "acknowledged mutation may be unresolved",
			message: devicev1.GetDeviceAccessStatusResponse_builder{
				HighWatermark:       proto.Uint64(42),
				Unresolved:          acknowledged.Build(),
				FirmwareFingerprint: proto.String(fingerprint),
				Interfaces:          []*accessv1.InterfaceObservation{interfaceObservation().Build()},
			}.Build(),
			wantValid: true,
		},
		{
			name: "released mutation cannot be unresolved",
			message: devicev1.GetDeviceAccessStatusResponse_builder{
				HighWatermark: proto.Uint64(42),
				Unresolved:    released.Build(),
			}.Build(),
		},
		{
			name: "duplicate interface names are rejected",
			message: devicev1.GetDeviceAccessStatusResponse_builder{
				HighWatermark: proto.Uint64(42),
				Interfaces: []*accessv1.InterfaceObservation{
					interfaceObservation().Build(),
					interfaceObservation().Build(),
				},
			}.Build(),
		},
		{
			name:    "high watermark is required",
			message: devicev1.GetDeviceAccessStatusResponse_builder{}.Build(),
		},
		{
			name: "empty firmware fingerprint is rejected",
			message: devicev1.GetDeviceAccessStatusResponse_builder{
				HighWatermark:       proto.Uint64(0),
				FirmwareFingerprint: proto.String(""),
			}.Build(),
		},
		{
			name:      "request requires the device",
			message:   devicev1.GetDeviceAccessStatusRequest_builder{}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}

func TestDeviceAbandonAndResolveRules(t *testing.T) {
	tests := []validationCase{
		{
			name: "abandon with device, sequence, and actor is valid",
			message: devicev1.AbandonMutationRequest_builder{
				Device:   deviceRef(deviceID),
				Sequence: proto.Uint64(42),
				Actor:    operatorActor(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "abandon without a sequence is rejected",
			message: devicev1.AbandonMutationRequest_builder{
				Device: deviceRef(deviceID),
				Actor:  operatorActor(),
			}.Build(),
		},
		{
			name: "abandon with sequence zero is rejected",
			message: devicev1.AbandonMutationRequest_builder{
				Device:   deviceRef(deviceID),
				Sequence: proto.Uint64(0),
				Actor:    operatorActor(),
			}.Build(),
		},
		{
			name: "abandon without an actor is rejected",
			message: devicev1.AbandonMutationRequest_builder{
				Device:   deviceRef(deviceID),
				Sequence: proto.Uint64(42),
			}.Build(),
		},
		{
			name:    "abandon response requires the mutation",
			message: devicev1.AbandonMutationResponse_builder{}.Build(),
		},
		{
			name: "resolve by accepting is valid",
			message: devicev1.ResolveDesynchronizationRequest_builder{
				Device:   deviceRef(deviceID),
				Sequence: proto.Uint64(42),
				Actor:    operatorActor(),
				Accept:   devicev1.AcceptObservedDecision_builder{}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "resolve by replacing carries a valid intent",
			message: devicev1.ResolveDesynchronizationRequest_builder{
				Device:   deviceRef(deviceID),
				Sequence: proto.Uint64(42),
				Actor:    operatorActor(),
				Replace:  mutationIntent().Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "resolve without a decision is rejected",
			message: devicev1.ResolveDesynchronizationRequest_builder{
				Device:   deviceRef(deviceID),
				Sequence: proto.Uint64(42),
				Actor:    operatorActor(),
			}.Build(),
		},
		{
			name: "resolve by replacing with an invalid intent is rejected",
			message: devicev1.ResolveDesynchronizationRequest_builder{
				Device:   deviceRef(deviceID),
				Sequence: proto.Uint64(42),
				Actor:    operatorActor(),
				Replace:  accessv1.MutationIntent_builder{}.Build(),
			}.Build(),
		},
		{
			name:      "resolve response may carry no mutation",
			message:   devicev1.ResolveDesynchronizationResponse_builder{}.Build(),
			wantValid: true,
		},
	}

	runValidationCases(t, tests)
}
