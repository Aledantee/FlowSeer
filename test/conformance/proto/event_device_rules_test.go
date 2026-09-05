package conformance

import (
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
)

const eventID = "0192e6a0-0000-7000-8000-00000000e001"

func deviceOperationEvent() eventv1.DeviceOperationEvent_builder {
	return eventv1.DeviceOperationEvent_builder{
		Device:     deviceRef(deviceID),
		EventId:    proto.String(eventID),
		Sequence:   proto.Uint64(42),
		OccurredAt: timestamppb.New(edgeIssuedAt),
		PhaseTransitioned: eventv1.PhaseTransitioned_builder{
			From: accessv1.OperationPhase_OPERATION_PHASE_ADMITTED.Enum(),
			To:   accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED.Enum(),
		}.Build(),
	}
}

func TestDeviceOperationEventRules(t *testing.T) {
	noDevice := deviceOperationEvent()
	noDevice.Device = nil

	noEventID := deviceOperationEvent()
	noEventID.EventId = nil

	noOccurredAt := deviceOperationEvent()
	noOccurredAt.OccurredAt = nil

	noKind := deviceOperationEvent()
	noKind.PhaseTransitioned = nil

	laneFrozen := deviceOperationEvent()
	laneFrozen.Sequence = nil
	laneFrozen.PhaseTransitioned = nil
	laneFrozen.LaneFrozen = eventv1.LaneFrozen_builder{}.Build()

	tooManyCorrelationIDs := deviceOperationEvent()
	correlationIDs := make(map[string]string, 9)
	for i := range 9 {
		correlationIDs[fmt.Sprintf("key-%d", i)] = "value"
	}
	tooManyCorrelationIDs.CorrelationIds = correlationIDs

	tooManyAttributes := deviceOperationEvent()
	attrs := make(map[string]*structpb.Value, 17)
	for i := range 17 {
		attrs[fmt.Sprintf("attr-%d", i)] = structpb.NewBoolValue(true)
	}
	tooManyAttributes.Attributes = attrs

	tests := []validationCase{
		{name: "phase transitioned event is valid", message: deviceOperationEvent().Build(), wantValid: true},
		{name: "lane-level event without a sequence is valid", message: laneFrozen.Build(), wantValid: true},
		{name: "device is required", message: noDevice.Build()},
		{name: "event id is required", message: noEventID.Build()},
		{name: "occurred_at is required", message: noOccurredAt.Build()},
		{name: "no kind set is rejected", message: noKind.Build()},
		{name: "more than 8 correlation ids is rejected", message: tooManyCorrelationIDs.Build()},
		{name: "more than 16 attributes is rejected", message: tooManyAttributes.Build()},
	}

	runValidationCases(t, tests)
}

func TestDeviceOperationEventKindRules(t *testing.T) {
	tests := []validationCase{
		{
			name: "lane blocked with a reason is valid",
			message: eventv1.LaneBlocked_builder{
				Reason: accessv1.BlockReason_BLOCK_REASON_INDETERMINATE.Enum(),
			}.Build(),
			wantValid: true,
		},
		{name: "lane blocked without a reason is rejected", message: eventv1.LaneBlocked_builder{}.Build()},
		{name: "lane released is valid", message: eventv1.LaneReleased_builder{}.Build(), wantValid: true},
		{
			name: "route selected is valid",
			message: eventv1.RouteSelected_builder{
				Protocol: inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP.Enum(),
			}.Build(),
			wantValid: true,
		},
		{name: "route selected without a protocol is rejected", message: eventv1.RouteSelected_builder{}.Build()},
		{
			name:      "discovery completed is valid",
			message:   eventv1.DiscoveryCompleted_builder{FirmwareFingerprint: proto.String(fingerprint)}.Build(),
			wantValid: true,
		},
		{name: "discovery completed without a fingerprint is rejected", message: eventv1.DiscoveryCompleted_builder{}.Build()},
		{
			name: "firmware epoch changed is valid",
			message: eventv1.FirmwareEpochChanged_builder{
				PreviousFingerprint: proto.String(fingerprint),
				NewFingerprint:      proto.String("ICX7150-24P SPS10011a"),
			}.Build(),
			wantValid: true,
		},
		{
			name:    "firmware epoch changed without the new fingerprint is rejected",
			message: eventv1.FirmwareEpochChanged_builder{PreviousFingerprint: proto.String(fingerprint)}.Build(),
		},
		{name: "recovery started is valid", message: eventv1.RecoveryStarted_builder{}.Build(), wantValid: true},
		{
			name:      "drift detected is valid",
			message:   eventv1.DriftDetected_builder{FieldName: proto.String("admin_status")}.Build(),
			wantValid: true,
		},
		{name: "drift detected without a field name is rejected", message: eventv1.DriftDetected_builder{}.Build()},
		{name: "lane frozen is valid", message: eventv1.LaneFrozen_builder{}.Build(), wantValid: true},
	}

	runValidationCases(t, tests)
}
