package conformance

import (
	"testing"

	"google.golang.org/protobuf/proto"

	authzv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/authz/v1"
)

func TestRuleRules(t *testing.T) {
	// Linking the generated package ensures the custom option descriptor is
	// registered in protoregistry.GlobalFiles for TestEveryDeclaredProtoPackageIsLinked.
	_ = authzv1.E_Rule

	tests := []validationCase{
		{
			name: "valid request rule",
			message: authzv1.Rule_builder{
				Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
				Relation:     proto.String("view"),
				ObjectType:   proto.String("device"),
				ObjectIdPath: proto.String("device.device.id"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "valid tenant rule",
			message: authzv1.Rule_builder{
				Mode:       authzv1.RuleMode_RULE_MODE_TENANT.Enum(),
				Relation:   proto.String("admin"),
				ObjectType: proto.String("tenant"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "valid loaded rule",
			message: authzv1.Rule_builder{
				Mode:       authzv1.RuleMode_RULE_MODE_LOADED.Enum(),
				Relation:   proto.String("manage"),
				ObjectType: proto.String("capture_session"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "valid filtered rule",
			message: authzv1.Rule_builder{
				Mode:       authzv1.RuleMode_RULE_MODE_FILTERED.Enum(),
				Relation:   proto.String("view"),
				ObjectType: proto.String("edge"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "valid platform rule",
			message: authzv1.Rule_builder{
				Mode:       authzv1.RuleMode_RULE_MODE_PLATFORM.Enum(),
				Relation:   proto.String("admin"),
				ObjectType: proto.String("platform"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "path outside request mode",
			message: authzv1.Rule_builder{
				Mode:         authzv1.RuleMode_RULE_MODE_TENANT.Enum(),
				Relation:     proto.String("admin"),
				ObjectType:   proto.String("tenant"),
				ObjectIdPath: proto.String("tenant.id"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "no path inside request mode",
			message: authzv1.Rule_builder{
				Mode:       authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
				Relation:   proto.String("view"),
				ObjectType: proto.String("device"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "tenant type outside tenant mode",
			message: authzv1.Rule_builder{
				Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
				Relation:     proto.String("view"),
				ObjectType:   proto.String("tenant"),
				ObjectIdPath: proto.String("tenant.id"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "another type inside tenant mode",
			message: authzv1.Rule_builder{
				Mode:       authzv1.RuleMode_RULE_MODE_TENANT.Enum(),
				Relation:   proto.String("admin"),
				ObjectType: proto.String("device"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "platform type outside platform mode",
			message: authzv1.Rule_builder{
				Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
				Relation:     proto.String("admin"),
				ObjectType:   proto.String("platform"),
				ObjectIdPath: proto.String("platform.id"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "another type inside platform mode",
			message: authzv1.Rule_builder{
				Mode:       authzv1.RuleMode_RULE_MODE_PLATFORM.Enum(),
				Relation:   proto.String("admin"),
				ObjectType: proto.String("device"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "unspecified mode",
			message: authzv1.Rule_builder{
				Mode:       authzv1.RuleMode_RULE_MODE_UNSPECIFIED.Enum(),
				Relation:   proto.String("view"),
				ObjectType: proto.String("device"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "mode 99",
			message: authzv1.Rule_builder{
				Mode:       authzv1.RuleMode(99).Enum(),
				Relation:   proto.String("view"),
				ObjectType: proto.String("device"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "relation View",
			message: authzv1.Rule_builder{
				Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
				Relation:     proto.String("View"),
				ObjectType:   proto.String("device"),
				ObjectIdPath: proto.String("device.device.id"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "path edges[0].id",
			message: authzv1.Rule_builder{
				Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
				Relation:     proto.String("view"),
				ObjectType:   proto.String("device"),
				ObjectIdPath: proto.String("edges[0].id"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "object type Device",
			message: authzv1.Rule_builder{
				Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
				Relation:     proto.String("view"),
				ObjectType:   proto.String("Device"),
				ObjectIdPath: proto.String("device.device.id"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "filtered rule missing mode",
			message: authzv1.Rule_builder{
				Relation:   proto.String("view"),
				ObjectType: proto.String("edge"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "filtered rule missing relation",
			message: authzv1.Rule_builder{
				Mode:       authzv1.RuleMode_RULE_MODE_FILTERED.Enum(),
				ObjectType: proto.String("edge"),
			}.Build(),
			wantValid: false,
		},
		{
			name: "filtered rule missing object type",
			message: authzv1.Rule_builder{
				Mode:     authzv1.RuleMode_RULE_MODE_FILTERED.Enum(),
				Relation: proto.String("view"),
			}.Build(),
			wantValid: false,
		},
		{
			name:      "empty rule",
			message:   authzv1.Rule_builder{}.Build(),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}
