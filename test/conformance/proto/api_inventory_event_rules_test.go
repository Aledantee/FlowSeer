package conformance

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
)

const (
	bindingID     = "0192e6a0-0000-7000-8000-0000000000b1"
	deviceID      = "0192e6a0-0000-7000-8000-0000000000d1"
	deviceID2     = "0192e6a0-0000-7000-8000-0000000000d2"
	integrationID = "0192e6a0-0000-7000-8000-0000000000c1"
	placementID   = "0192e6a0-0000-7000-8000-0000000000e1"
)

func bindingRef() *inventoryv1.BindingGlobalRef {
	return inventoryv1.BindingGlobalRef_builder{
		Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(bindingID)}.Build(),
	}.Build()
}

func deviceRef(id string) *inventoryv1.DeviceGlobalRef {
	return inventoryv1.DeviceGlobalRef_builder{
		Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(id)}.Build(),
	}.Build()
}

func integrationRef(id string) *inventoryv1.IntegrationGlobalRef {
	return inventoryv1.IntegrationGlobalRef_builder{
		Integration: inventoryv1.IntegrationLocalRef_builder{Id: proto.String(id)}.Build(),
	}.Build()
}

func scopeRef(platformID string) *inventoryv1.IntegrationScopeGlobalRef {
	return inventoryv1.IntegrationScopeGlobalRef_builder{
		Integration: integrationRef(integrationID),
		Scope: inventoryv1.IntegrationScopeLocalRef_builder{
			PlatformId: proto.String(platformID),
		}.Build(),
	}.Build()
}

func placementRef() *inventoryv1.PlacementGlobalRef {
	return inventoryv1.PlacementGlobalRef_builder{
		Placement: inventoryv1.PlacementLocalRef_builder{Id: proto.String(placementID)}.Build(),
	}.Build()
}

func validBindingState() *inventoryv1.BindingState {
	return inventoryv1.BindingState_builder{
		Ref:         bindingRef(),
		Device:      deviceRef(deviceID),
		Integration: integrationRef(integrationID),
		Status:      inventoryv1.BindingStatus_BINDING_STATUS_VERIFIED.Enum(),
		PlatformId:  proto.String("sw-1"),
	}.Build()
}

func TestBindingStateHealthRules(t *testing.T) {
	since := timestamppb.New(time.Unix(1700000000, 0))
	tests := []validationCase{
		{
			name:      "verified with no failure metadata is valid",
			message:   validBindingState(),
			wantValid: true,
		},
		{
			name: "verified must not carry unreachable_since",
			message: func() proto.Message {
				b := validBindingState()
				b.SetUnreachableSince(since)
				return b
			}(),
			wantValid: false,
		},
		{
			name: "verified must not carry a failure kind",
			message: func() proto.Message {
				b := validBindingState()
				b.SetFailureKind(inventoryv1.BindingFailureKind_BINDING_FAILURE_KIND_TIMEOUT)
				return b
			}(),
			wantValid: false,
		},
		{
			name: "degraded must name its failure kind",
			message: func() proto.Message {
				b := validBindingState()
				b.SetStatus(inventoryv1.BindingStatus_BINDING_STATUS_DEGRADED)
				return b
			}(),
			wantValid: false,
		},
		{
			name: "degraded with a failure kind is valid",
			message: func() proto.Message {
				b := validBindingState()
				b.SetStatus(inventoryv1.BindingStatus_BINDING_STATUS_DEGRADED)
				b.SetFailureKind(inventoryv1.BindingFailureKind_BINDING_FAILURE_KIND_AUTH_FAILED)
				return b
			}(),
			wantValid: true,
		},
		{
			name: "unreachable needs its start and failure kind",
			message: func() proto.Message {
				b := validBindingState()
				b.SetStatus(inventoryv1.BindingStatus_BINDING_STATUS_UNREACHABLE)
				b.SetFailureKind(inventoryv1.BindingFailureKind_BINDING_FAILURE_KIND_TIMEOUT)
				return b
			}(),
			wantValid: false,
		},
		{
			name: "unreachable with start and failure kind is valid",
			message: func() proto.Message {
				b := validBindingState()
				b.SetStatus(inventoryv1.BindingStatus_BINDING_STATUS_UNREACHABLE)
				b.SetUnreachableSince(since)
				b.SetFailureKind(inventoryv1.BindingFailureKind_BINDING_FAILURE_KIND_TIMEOUT)
				return b
			}(),
			wantValid: true,
		},
	}
	runValidationCases(t, tests)
}

func TestBindingEventImmutableRelations(t *testing.T) {
	tests := []validationCase{
		{
			name: "a transition keeping device and integration is valid",
			message: inventoryv1.BindingEvent_builder{
				Ref:    bindingRef(),
				Before: validBindingState(),
				After: func() *inventoryv1.BindingState {
					b := validBindingState()
					b.SetStatus(inventoryv1.BindingStatus_BINDING_STATUS_RETIRED)
					return b
				}(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "a transition may not retarget the device",
			message: inventoryv1.BindingEvent_builder{
				Ref:    bindingRef(),
				Before: validBindingState(),
				After: func() *inventoryv1.BindingState {
					b := validBindingState()
					b.SetDevice(deviceRef(deviceID2))
					return b
				}(),
			}.Build(),
			wantValid: false,
		},
	}
	runValidationCases(t, tests)
}

func validPlacement(from, until *timestamppb.Timestamp) *inventoryv1.Placement {
	return inventoryv1.Placement_builder{
		Ref:            placementRef(),
		Device:         deviceRef(deviceID),
		Scope:          scopeRef("net-1"),
		Source:         inventoryv1.PlacementSource_PLACEMENT_SOURCE_OPERATOR.Enum(),
		EffectiveFrom:  from,
		EffectiveUntil: until,
	}.Build()
}

func TestPlacementEventImmutability(t *testing.T) {
	from := timestamppb.New(time.Unix(1700000000, 0))
	until := timestamppb.New(time.Unix(1700003600, 0))
	tests := []validationCase{
		{
			name: "closing a placement is valid",
			message: inventoryv1.PlacementEvent_builder{
				Ref:    placementRef(),
				Before: validPlacement(from, nil),
				After:  validPlacement(from, until),
			}.Build(),
			wantValid: true,
		},
		{
			name: "a transition may not move the placement's scope",
			message: inventoryv1.PlacementEvent_builder{
				Ref:    placementRef(),
				Before: validPlacement(from, nil),
				After: func() *inventoryv1.Placement {
					p := inventoryv1.Placement_builder{
						Ref:           placementRef(),
						Device:        deviceRef(deviceID),
						Scope:         scopeRef("net-2"),
						Source:        inventoryv1.PlacementSource_PLACEMENT_SOURCE_OPERATOR.Enum(),
						EffectiveFrom: from,
					}.Build()
					return p
				}(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "a closed placement may not reopen",
			message: inventoryv1.PlacementEvent_builder{
				Ref:    placementRef(),
				Before: validPlacement(from, until),
				After:  validPlacement(from, nil),
			}.Build(),
			wantValid: false,
		},
	}
	runValidationCases(t, tests)
}

func TestAssignmentEventImmutableCore(t *testing.T) {
	owner := func(id string) *inventoryv1.EntityRef {
		return inventoryv1.EntityRef_builder{
			Type: inventoryv1.EntityType_ENTITY_TYPE_DEVICE.Enum(),
			Id:   proto.String(id),
		}.Build()
	}
	assignment := func(ownerID, text string) *inventoryv1.AttributeValue {
		return inventoryv1.AttributeValue_builder{
			Ref:       assignmentRef(attributeID2),
			Owner:     owner(ownerID),
			Attribute: attributeRef(attributeID),
			Values: []*inventoryv1.AttributeValuePayload{
				inventoryv1.AttributeValuePayload_builder{Text: proto.String(text)}.Build(),
			},
		}.Build()
	}
	tests := []validationCase{
		{
			name: "a transition changing only the values is valid",
			message: inventoryv1.AttributeValueEvent_builder{
				Ref:    assignmentRef(attributeID2),
				Before: assignment(deviceID, "u17"),
				After:  assignment(deviceID, "u18"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "a transition may not move the assignment's owner",
			message: inventoryv1.AttributeValueEvent_builder{
				Ref:    assignmentRef(attributeID2),
				Before: assignment(deviceID, "u17"),
				After:  assignment(deviceID2, "u17"),
			}.Build(),
			wantValid: false,
		},
	}
	runValidationCases(t, tests)
}

func TestIntegrationStateVerifiedIdentity(t *testing.T) {
	state := func(lc inventoryv1.IntegrationLifecycle, platformID *string) *inventoryv1.IntegrationState {
		return inventoryv1.IntegrationState_builder{
			Ref:        integrationRef(integrationID),
			Lifecycle:  lc.Enum(),
			PlatformId: platformID,
		}.Build()
	}
	tests := []validationCase{
		{
			name:      "a candidate needs no platform id",
			message:   state(inventoryv1.IntegrationLifecycle_INTEGRATION_LIFECYCLE_CANDIDATE, nil),
			wantValid: true,
		},
		{
			name:      "a verified integration names its platform id",
			message:   state(inventoryv1.IntegrationLifecycle_INTEGRATION_LIFECYCLE_VERIFIED, proto.String("org-7")),
			wantValid: true,
		},
		{
			name:      "verified without a platform id is rejected",
			message:   state(inventoryv1.IntegrationLifecycle_INTEGRATION_LIFECYCLE_VERIFIED, nil),
			wantValid: false,
		},
	}
	runValidationCases(t, tests)
}
