package conformance

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	authzv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/authz/v1"
)

// knownRelations lists every (object_type, relation) pair permitted by the
// operator authorization direction record and parent plan.
var knownRelations = map[string]map[string]bool{
	"platform": {
		"admin": true,
	},
	"tenant": {
		"member":       true,
		"admin":        true,
		"operator":     true,
		"capturer":     true,
		"viewer":       true,
		"full_payload": true,
	},
	"edge": {
		"tenant":     true,
		"view":       true,
		"operate":    true,
		"capture":    true,
		"administer": true,
	},
	"device": {
		"tenant":  true,
		"view":    true,
		"operate": true,
	},
	"capture_session": {
		"tenant":   true,
		"manage":   true,
		"download": true,
	},
}

// forEachAPIMethod ranges over every RPC method in packages under flowseer.api.
// and returns the count of methods visited.
func forEachAPIMethod(fn func(md protoreflect.MethodDescriptor)) int {
	count := 0
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), "flowseer.api.") {
			return true
		}
		for i := range fd.Services().Len() {
			svc := fd.Services().Get(i)
			for j := range svc.Methods().Len() {
				count++
				fn(svc.Methods().Get(j))
			}
		}
		return true
	})
	return count
}

// methodRule extracts the Rule option attached to a method descriptor, or nil
// if none is present.
func methodRule(method protoreflect.MethodDescriptor) *authzv1.Rule {
	opts, ok := method.Options().(*descriptorpb.MethodOptions)
	if !ok || opts == nil || !proto.HasExtension(opts, authzv1.E_Rule) {
		return nil
	}
	rule, _ := proto.GetExtension(opts, authzv1.E_Rule).(*authzv1.Rule)
	return rule
}

// checkAuthorizationRule verifies that a method carries a valid authorization
// rule option with a specified mode.
func checkAuthorizationRule(method protoreflect.MethodDescriptor) []string {
	rule := methodRule(method)
	if rule == nil {
		return []string{fmt.Sprintf("%s carries no authorization rule", method.FullName())}
	}
	if rule.GetMode() == authzv1.RuleMode_RULE_MODE_UNSPECIFIED {
		return []string{fmt.Sprintf("%s authorization rule declares unspecified mode", method.FullName())}
	}
	if err := protovalidate.Validate(rule); err != nil {
		return []string{fmt.Sprintf("%s authorization rule fails validation: %v", method.FullName(), err)}
	}
	return nil
}

// checkObjectIDPath verifies that a request rule object_id_path resolves
// through singular message fields to a string field of the method input.
func checkObjectIDPath(method protoreflect.MethodDescriptor, rule *authzv1.Rule) []string {
	if rule.GetMode() != authzv1.RuleMode_RULE_MODE_REQUEST {
		return nil
	}
	path := rule.GetObjectIdPath()
	if path == "" {
		return []string{fmt.Sprintf("%s request rule has empty object_id_path", method.FullName())}
	}

	parts := strings.Split(path, ".")
	currentMsg := method.Input()

	for i, part := range parts {
		field := currentMsg.Fields().ByName(protoreflect.Name(part))
		if field == nil {
			return []string{fmt.Sprintf("%s object_id_path %q: message %s has no field %q",
				method.FullName(), path, currentMsg.FullName(), part)}
		}
		if i < len(parts)-1 {
			if field.IsList() || field.IsMap() {
				return []string{fmt.Sprintf("%s object_id_path %q: intermediate field %s is not a singular message",
					method.FullName(), path, field.FullName())}
			}
			if field.Kind() != protoreflect.MessageKind || field.Message() == nil {
				return []string{fmt.Sprintf("%s object_id_path %q: intermediate field %s has kind %v, want message",
					method.FullName(), path, field.FullName(), field.Kind())}
			}
			currentMsg = field.Message()
		} else {
			if field.IsList() || field.IsMap() {
				return []string{fmt.Sprintf("%s object_id_path %q: leaf field %s is not a singular string",
					method.FullName(), path, field.FullName())}
			}
			if field.Kind() != protoreflect.StringKind {
				return []string{fmt.Sprintf("%s object_id_path %q resolves to %v field %s, want string",
					method.FullName(), path, field.Kind(), field.FullName())}
			}
		}
	}
	return nil
}

// checkKnownRelation verifies that a rule names a known (object_type, relation)
// pair from the operator authorization direction.
func checkKnownRelation(method protoreflect.MethodDescriptor, rule *authzv1.Rule) []string {
	objType := rule.GetObjectType()
	rel := rule.GetRelation()
	rels, ok := knownRelations[objType]
	if !ok {
		return []string{fmt.Sprintf("%s authorization rule names unknown object type %q", method.FullName(), objType)}
	}
	if !rels[rel] {
		return []string{fmt.Sprintf("%s authorization rule names unknown relation %q for object type %q",
			method.FullName(), rel, objType)}
	}
	return nil
}

func buildSyntheticService(t *testing.T, name string, methods ...*descriptorpb.MethodDescriptorProto) protoreflect.ServiceDescriptor {
	t.Helper()

	fdp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("flowseer/conformance/synthetic/v1/" + name + ".proto"),
		Package: proto.String("flowseer.conformance.synthetic.v1"),
		Syntax:  proto.String("proto3"),
		Dependency: []string{
			"flowseer/authz/v1/rule.proto",
			"flowseer/api/edge/v1/edge_admin_service.proto",
			"flowseer/api/device/v1/device_service.proto",
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name:   proto.String(name),
				Method: methods,
			},
		},
	}

	file, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatalf("build synthetic service %s: %v", name, err)
	}
	return file.Services().Get(0)
}

func syntheticMethod(name, inputType, outputType string, rule *authzv1.Rule) *descriptorpb.MethodDescriptorProto {
	m := &descriptorpb.MethodDescriptorProto{
		Name:       proto.String(name),
		InputType:  proto.String(inputType),
		OutputType: proto.String(outputType),
	}
	if rule != nil {
		opts := &descriptorpb.MethodOptions{}
		proto.SetExtension(opts, authzv1.E_Rule, rule)
		m.Options = opts
	}
	return m
}

// TestEveryOperatorRPCHasAuthorizationRule ensures every operator RPC in
// flowseer.api. carries a valid authorization rule option.
func TestEveryOperatorRPCHasAuthorizationRule(t *testing.T) {
	methodCount := forEachAPIMethod(func(method protoreflect.MethodDescriptor) {
		for _, violation := range checkAuthorizationRule(method) {
			t.Errorf("%s", violation)
		}
	})
	if methodCount == 0 {
		t.Fatalf("no operator RPC methods found under flowseer.api.")
	}

	t.Run("synthetic", func(t *testing.T) {
		compliantRule := authzv1.Rule_builder{
			Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
			ObjectType:   proto.String("edge"),
			Relation:     proto.String("view"),
			ObjectIdPath: proto.String("edge.edge.id"),
		}.Build()

		svc := buildSyntheticService(t, "RulePresenceService",
			syntheticMethod("CompliantMethod",
				".flowseer.api.edge.v1.GetEdgeRequest",
				".flowseer.api.edge.v1.GetEdgeResponse",
				compliantRule),
			syntheticMethod("MissingRuleMethod",
				".flowseer.api.edge.v1.GetEdgeRequest",
				".flowseer.api.edge.v1.GetEdgeResponse",
				nil),
			syntheticMethod("InvalidRuleMethod",
				".flowseer.api.edge.v1.GetEdgeRequest",
				".flowseer.api.edge.v1.GetEdgeResponse",
				authzv1.Rule_builder{
					Mode:       authzv1.RuleMode_RULE_MODE_TENANT.Enum(),
					ObjectType: proto.String("tenant"),
					Relation:   proto.String("INVALID_RELATION"),
				}.Build()),
			syntheticMethod("UnspecifiedModeMethod",
				".flowseer.api.edge.v1.GetEdgeRequest",
				".flowseer.api.edge.v1.GetEdgeResponse",
				authzv1.Rule_builder{
					Mode:       authzv1.RuleMode_RULE_MODE_UNSPECIFIED.Enum(),
					ObjectType: proto.String("tenant"),
					Relation:   proto.String("admin"),
				}.Build()),
		)

		tests := []struct {
			name        string
			methodIndex int
			wantReason  string
		}{
			{
				name:        "compliant method reports no violations",
				methodIndex: 0,
				wantReason:  "",
			},
			{
				name:        "missing rule is reported",
				methodIndex: 1,
				wantReason:  "carries no authorization rule",
			},
			{
				name:        "rule failing validation is reported",
				methodIndex: 2,
				wantReason:  "authorization rule fails validation",
			},
			{
				name:        "unspecified mode is reported",
				methodIndex: 3,
				wantReason:  "unspecified mode",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				method := svc.Methods().Get(tt.methodIndex)
				violations := checkAuthorizationRule(method)
				if tt.wantReason == "" {
					if len(violations) != 0 {
						t.Errorf("got violations: %v, want none", violations)
					}
					return
				}
				if len(violations) == 0 {
					t.Fatalf("got no violations, want one containing %q", tt.wantReason)
				}
				if !slices.ContainsFunc(violations, func(v string) bool { return strings.Contains(v, tt.wantReason) }) {
					t.Errorf("got violations %v, want one containing %q", violations, tt.wantReason)
				}
			})
		}
	})
}

// TestAuthorizationRuleObjectPathResolves ensures every request rule object_id_path
// resolves through singular message fields to a string field of the method input.
func TestAuthorizationRuleObjectPathResolves(t *testing.T) {
	requestRuleCount := 0
	forEachAPIMethod(func(method protoreflect.MethodDescriptor) {
		rule := methodRule(method)
		if rule == nil || rule.GetMode() != authzv1.RuleMode_RULE_MODE_REQUEST {
			return
		}
		requestRuleCount++
		for _, violation := range checkObjectIDPath(method, rule) {
			t.Errorf("%s", violation)
		}
	})
	if requestRuleCount == 0 {
		t.Fatalf("no request-mode operator RPC methods found under flowseer.api.")
	}

	t.Run("synthetic", func(t *testing.T) {
		compliantRule := authzv1.Rule_builder{
			Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
			ObjectType:   proto.String("edge"),
			Relation:     proto.String("view"),
			ObjectIdPath: proto.String("edge.edge.id"),
		}.Build()

		noSuchFieldRule := authzv1.Rule_builder{
			Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
			ObjectType:   proto.String("edge"),
			Relation:     proto.String("view"),
			ObjectIdPath: proto.String("edge.edge.name"),
		}.Build()

		messageLeafRule := authzv1.Rule_builder{
			Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
			ObjectType:   proto.String("edge"),
			Relation:     proto.String("view"),
			ObjectIdPath: proto.String("edge.edge"),
		}.Build()

		uint64LeafRule := authzv1.Rule_builder{
			Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
			ObjectType:   proto.String("device"),
			Relation:     proto.String("operate"),
			ObjectIdPath: proto.String("sequence"),
		}.Build()

		svc := buildSyntheticService(t, "PathResolutionService",
			syntheticMethod("CompliantPathMethod",
				".flowseer.api.edge.v1.GetEdgeRequest",
				".flowseer.api.edge.v1.GetEdgeResponse",
				compliantRule),
			syntheticMethod("NoSuchFieldMethod",
				".flowseer.api.edge.v1.GetEdgeRequest",
				".flowseer.api.edge.v1.GetEdgeResponse",
				noSuchFieldRule),
			syntheticMethod("MessageLeafMethod",
				".flowseer.api.edge.v1.GetEdgeRequest",
				".flowseer.api.edge.v1.GetEdgeResponse",
				messageLeafRule),
			syntheticMethod("Uint64LeafMethod",
				".flowseer.api.device.v1.AbandonMutationRequest",
				".flowseer.api.device.v1.AbandonMutationResponse",
				uint64LeafRule),
		)

		tests := []struct {
			name        string
			methodIndex int
			rule        *authzv1.Rule
			wantReason  string
		}{
			{
				name:        "compliant path resolves",
				methodIndex: 0,
				rule:        compliantRule,
				wantReason:  "",
			},
			{
				name:        "nonexistent field is reported",
				methodIndex: 1,
				rule:        noSuchFieldRule,
				wantReason:  "has no field \"name\"",
			},
			{
				name:        "message leaf is reported",
				methodIndex: 2,
				rule:        messageLeafRule,
				wantReason:  "want string",
			},
			{
				name:        "uint64 field is reported",
				methodIndex: 3,
				rule:        uint64LeafRule,
				wantReason:  "uint64",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				method := svc.Methods().Get(tt.methodIndex)
				violations := checkObjectIDPath(method, tt.rule)
				if tt.wantReason == "" {
					if len(violations) != 0 {
						t.Errorf("got violations: %v, want none", violations)
					}
					return
				}
				if len(violations) == 0 {
					t.Fatalf("got no violations, want one containing %q", tt.wantReason)
				}
				if !slices.ContainsFunc(violations, func(v string) bool { return strings.Contains(v, tt.wantReason) }) {
					t.Errorf("got violations %v, want one containing %q", violations, tt.wantReason)
				}
			})
		}
	})
}

// TestAuthorizationRuleNamesKnownRelation ensures every rule names an (object_type, relation)
// pair declared in the operator authorization direction relation table.
func TestAuthorizationRuleNamesKnownRelation(t *testing.T) {
	ruleCount := 0
	forEachAPIMethod(func(method protoreflect.MethodDescriptor) {
		rule := methodRule(method)
		if rule == nil {
			return
		}
		ruleCount++
		for _, violation := range checkKnownRelation(method, rule) {
			t.Errorf("%s", violation)
		}
	})
	if ruleCount == 0 {
		t.Fatalf("no authorization rules found under flowseer.api.")
	}

	t.Run("synthetic", func(t *testing.T) {
		compliantRule := authzv1.Rule_builder{
			Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
			ObjectType:   proto.String("edge"),
			Relation:     proto.String("view"),
			ObjectIdPath: proto.String("edge.edge.id"),
		}.Build()

		unknownRelationRule := authzv1.Rule_builder{
			Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
			ObjectType:   proto.String("edge"),
			Relation:     proto.String("delete"),
			ObjectIdPath: proto.String("edge.edge.id"),
		}.Build()

		svc := buildSyntheticService(t, "KnownRelationService",
			syntheticMethod("CompliantRelationMethod",
				".flowseer.api.edge.v1.GetEdgeRequest",
				".flowseer.api.edge.v1.GetEdgeResponse",
				compliantRule),
			syntheticMethod("UnknownRelationMethod",
				".flowseer.api.edge.v1.GetEdgeRequest",
				".flowseer.api.edge.v1.GetEdgeResponse",
				unknownRelationRule),
		)

		tests := []struct {
			name        string
			methodIndex int
			rule        *authzv1.Rule
			wantReason  string
		}{
			{
				name:        "compliant relation reports no violations",
				methodIndex: 0,
				rule:        compliantRule,
				wantReason:  "",
			},
			{
				name:        "unknown relation is reported",
				methodIndex: 1,
				rule:        unknownRelationRule,
				wantReason:  "unknown relation \"delete\" for object type \"edge\"",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				method := svc.Methods().Get(tt.methodIndex)
				violations := checkKnownRelation(method, tt.rule)
				if tt.wantReason == "" {
					if len(violations) != 0 {
						t.Errorf("got violations: %v, want none", violations)
					}
					return
				}
				if len(violations) == 0 {
					t.Fatalf("got no violations, want one containing %q", tt.wantReason)
				}
				if !slices.ContainsFunc(violations, func(v string) bool { return strings.Contains(v, tt.wantReason) }) {
					t.Errorf("got violations %v, want one containing %q", violations, tt.wantReason)
				}
			})
		}
	})
}
