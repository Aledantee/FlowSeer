package protoconformance

import (
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
)

const (
	attributeID  = "0192e6a0-0000-7000-8000-000000000001"
	attributeID2 = "0192e6a0-0000-7000-8000-000000000002"
	ownerID      = "0192e6a0-0000-7000-8000-00000000000a"
)

func attributeRef(id string) *inventoryv1.AttributeGlobalRef {
	return inventoryv1.AttributeGlobalRef_builder{
		Attribute: inventoryv1.AttributeLocalRef_builder{Id: proto.String(id)}.Build(),
	}.Build()
}

func assignmentRef(id string) *inventoryv1.AttributeValueGlobalRef {
	return inventoryv1.AttributeValueGlobalRef_builder{
		AttributeValue: inventoryv1.AttributeValueLocalRef_builder{Id: proto.String(id)}.Build(),
	}.Build()
}

func validAttributeConfig(id string) *inventoryv1.AttributeConfig_builder {
	return &inventoryv1.AttributeConfig_builder{
		Ref:         attributeRef(id),
		Key:         proto.String("rack-position"),
		Name:        proto.String("Rack position"),
		Targets:     []inventoryv1.EntityType{inventoryv1.EntityType_ENTITY_TYPE_DEVICE},
		MultiValued: proto.Bool(false),
		StringType:  &inventoryv1.StringType{},
	}
}

func TestEntityRefRules(t *testing.T) {
	tests := []validationCase{
		{
			name: "a typed uuid ref is valid",
			message: inventoryv1.EntityRef_builder{
				Type: inventoryv1.EntityType_ENTITY_TYPE_DEVICE.Enum(),
				Id:   proto.String(ownerID),
			}.Build(),
			wantValid: true,
		},
		{
			name: "the kind must be present",
			message: inventoryv1.EntityRef_builder{
				Id: proto.String(ownerID),
			}.Build(),
			wantValid: false,
		},
		{
			name: "the unspecified kind is rejected",
			message: inventoryv1.EntityRef_builder{
				Type: inventoryv1.EntityType_ENTITY_TYPE_UNSPECIFIED.Enum(),
				Id:   proto.String(ownerID),
			}.Build(),
			wantValid: false,
		},
		{
			name: "a non-uuid id is rejected",
			message: inventoryv1.EntityRef_builder{
				Type: inventoryv1.EntityType_ENTITY_TYPE_DEVICE.Enum(),
				Id:   proto.String("switch-7"),
			}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}

func TestAttributeConfigRules(t *testing.T) {
	missingCardinality := validAttributeConfig(attributeID)
	missingCardinality.MultiValued = nil

	emptyTargets := validAttributeConfig(attributeID)
	emptyTargets.Targets = nil

	duplicateTargets := validAttributeConfig(attributeID)
	duplicateTargets.Targets = []inventoryv1.EntityType{
		inventoryv1.EntityType_ENTITY_TYPE_DEVICE,
		inventoryv1.EntityType_ENTITY_TYPE_DEVICE,
	}

	noTypeArm := validAttributeConfig(attributeID)
	noTypeArm.StringType = nil

	uppercaseKey := validAttributeConfig(attributeID)
	uppercaseKey.Key = proto.String("Rack-Position")

	tests := []validationCase{
		{
			name:      "a full definition is valid",
			message:   validAttributeConfig(attributeID).Build(),
			wantValid: true,
		},
		{
			name:      "cardinality must be declared",
			message:   missingCardinality.Build(),
			wantValid: false,
		},
		{
			name:      "at least one target kind is required",
			message:   emptyTargets.Build(),
			wantValid: false,
		},
		{
			name:      "duplicate target kinds are rejected",
			message:   duplicateTargets.Build(),
			wantValid: false,
		},
		{
			name:      "a definition without a type arm is rejected",
			message:   noTypeArm.Build(),
			wantValid: false,
		},
		{
			name:      "keys are lowercase kebab",
			message:   uppercaseKey.Build(),
			wantValid: false,
		},
		{
			name: "duplicate enum keys are rejected",
			message: inventoryv1.EnumType_builder{
				Values: []string{"staging", "staging"},
			}.Build(),
			wantValid: false,
		},
		{
			name:      "an empty enum vocabulary is rejected",
			message:   inventoryv1.EnumType_builder{}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}

// The enum-key shape is written out twice — on EnumType.values and on the
// enum_key payload arm — because protovalidate has no shared constants. These
// cases feed both rules the same boundary inputs so the two literals cannot
// drift apart without a test failing.
func TestEnumKeyShapeStaysInSync(t *testing.T) {
	longKey := strings.Repeat("k", 65)
	edgeKey := strings.Repeat("k", 64)

	tests := []validationCase{
		{
			name: "a 64-char key satisfies the definition side",
			message: inventoryv1.EnumType_builder{
				Values: []string{edgeKey},
			}.Build(),
			wantValid: true,
		},
		{
			name: "a 64-char key satisfies the value side",
			message: inventoryv1.AttributeValuePayload_builder{
				EnumKey: proto.String(edgeKey),
			}.Build(),
			wantValid: true,
		},
		{
			name: "a 65-char key fails the definition side",
			message: inventoryv1.EnumType_builder{
				Values: []string{longKey},
			}.Build(),
			wantValid: false,
		},
		{
			name: "a 65-char key fails the value side",
			message: inventoryv1.AttributeValuePayload_builder{
				EnumKey: proto.String(longKey),
			}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}

func TestAttributeValuePayloadRules(t *testing.T) {
	tests := []validationCase{
		{
			name: "text at the cap is valid",
			message: inventoryv1.AttributeValuePayload_builder{
				Text: proto.String(strings.Repeat("x", 2048)),
			}.Build(),
			wantValid: true,
		},
		{
			name: "text over the cap is rejected",
			message: inventoryv1.AttributeValuePayload_builder{
				Text: proto.String(strings.Repeat("x", 2049)),
			}.Build(),
			wantValid: false,
		},
		{
			name: "empty text is rejected",
			message: inventoryv1.AttributeValuePayload_builder{
				Text: proto.String(""),
			}.Build(),
			wantValid: false,
		},
		{
			name:      "a payload without an arm is rejected",
			message:   inventoryv1.AttributeValuePayload_builder{}.Build(),
			wantValid: false,
		},
		{
			name: "an integer number is valid",
			message: inventoryv1.AttributeValuePayload_builder{
				Number: inventoryv1.Number_builder{Integer: proto.Int64(42)}.Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "NaN is rejected",
			message: inventoryv1.Number_builder{
				Decimal: proto.Float64(math.NaN()),
			}.Build(),
			wantValid: false,
		},
		{
			name: "infinity is rejected",
			message: inventoryv1.Number_builder{
				Decimal: proto.Float64(math.Inf(1)),
			}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}

func TestAttributeEventRules(t *testing.T) {
	tests := []validationCase{
		{
			name: "a create event with matching refs is valid",
			message: inventoryv1.AttributeEvent_builder{
				Ref:   attributeRef(attributeID),
				After: validAttributeConfig(attributeID).Build(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "an event without sides is rejected",
			message: inventoryv1.AttributeEvent_builder{
				Ref: attributeRef(attributeID),
			}.Build(),
			wantValid: false,
		},
		{
			name: "a side describing a different definition is rejected",
			message: inventoryv1.AttributeEvent_builder{
				Ref:   attributeRef(attributeID),
				After: validAttributeConfig(attributeID2).Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "a side without its own ref is rejected by recursion",
			message: inventoryv1.AttributeEvent_builder{
				Ref: attributeRef(attributeID),
				After: (&inventoryv1.AttributeConfig_builder{
					Key:         proto.String("rack-position"),
					Name:        proto.String("Rack position"),
					Targets:     []inventoryv1.EntityType{inventoryv1.EntityType_ENTITY_TYPE_DEVICE},
					MultiValued: proto.Bool(false),
					StringType:  &inventoryv1.StringType{},
				}).Build(),
			}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}

func TestAttributeValueRules(t *testing.T) {
	owner := inventoryv1.EntityRef_builder{
		Type: inventoryv1.EntityType_ENTITY_TYPE_DEVICE.Enum(),
		Id:   proto.String(ownerID),
	}.Build()
	payload := inventoryv1.AttributeValuePayload_builder{
		Text: proto.String("u42"),
	}.Build()
	assignment := func(id string) *inventoryv1.AttributeValueConfig {
		return inventoryv1.AttributeValueConfig_builder{
			Ref:       assignmentRef(id),
			Owner:     owner,
			Attribute: attributeRef(attributeID),
			Values:    []*inventoryv1.AttributeValuePayload{payload},
		}.Build()
	}

	tests := []validationCase{
		{
			name:      "a full assignment is valid",
			message:   assignment(ownerID),
			wantValid: true,
		},
		{
			name: "an assignment without values is rejected",
			message: inventoryv1.AttributeValueConfig_builder{
				Ref:       assignmentRef(ownerID),
				Owner:     owner,
				Attribute: attributeRef(attributeID),
			}.Build(),
			wantValid: false,
		},
		{
			name: "an assignment without an owner is rejected",
			message: inventoryv1.AttributeValueConfig_builder{
				Ref:       assignmentRef(ownerID),
				Attribute: attributeRef(attributeID),
				Values:    []*inventoryv1.AttributeValuePayload{payload},
			}.Build(),
			wantValid: false,
		},
		{
			name: "a value event with mismatched refs is rejected",
			message: inventoryv1.AttributeValueEvent_builder{
				Ref:   assignmentRef(attributeID),
				After: assignment(attributeID2),
			}.Build(),
			wantValid: false,
		},
		{
			name: "a value event with matching refs is valid",
			message: inventoryv1.AttributeValueEvent_builder{
				Ref:   assignmentRef(attributeID),
				After: assignment(attributeID),
			}.Build(),
			wantValid: true,
		},
	}

	runValidationCases(t, tests)
}

func TestTagEventRefMatchRules(t *testing.T) {
	tagRef := func(id string) *inventoryv1.TagGlobalRef {
		return inventoryv1.TagGlobalRef_builder{
			Tag: inventoryv1.TagLocalRef_builder{Id: proto.String(id)}.Build(),
		}.Build()
	}
	tagConfig := func(id string) *inventoryv1.TagConfig {
		return inventoryv1.TagConfig_builder{
			Ref:  tagRef(id),
			Name: proto.String("EMEA"),
		}.Build()
	}

	tests := []validationCase{
		{
			name: "a tag event with matching refs is valid",
			message: inventoryv1.TagEvent_builder{
				Ref:   tagRef(attributeID),
				After: tagConfig(attributeID),
			}.Build(),
			wantValid: true,
		},
		{
			name: "a tag event describing a different tag is rejected",
			message: inventoryv1.TagEvent_builder{
				Ref:   tagRef(attributeID),
				After: tagConfig(attributeID2),
			}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}
