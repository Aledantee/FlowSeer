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

// knownRelations lists every permitted (object_type, relation) pair for
// operator authorization rules.
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
func forEachAPIMethod(files *protoregistry.Files, fn func(md protoreflect.MethodDescriptor)) int {
	count := 0
	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
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
// rule option.
func checkAuthorizationRule(method protoreflect.MethodDescriptor) []string {
	rule := methodRule(method)
	if rule == nil {
		return []string{fmt.Sprintf("%s carries no authorization rule", method.FullName())}
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
			if field.Kind() != protoreflect.MessageKind {
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

// checkKnownRelation verifies that a rule names a permitted (object_type, relation)
// pair.
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

func newSyntheticFileDescriptor(t *testing.T, fdp *descriptorpb.FileDescriptorProto) protoreflect.FileDescriptor {
	t.Helper()

	file, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatalf("build synthetic file %s: %v", fdp.GetName(), err)
	}
	return file
}

func buildSyntheticService(t *testing.T, name string, messages []*descriptorpb.DescriptorProto, methods ...*descriptorpb.MethodDescriptorProto) protoreflect.ServiceDescriptor {
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
		MessageType: messages,
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name:   proto.String(name),
				Method: methods,
			},
		},
	}
	return newSyntheticFileDescriptor(t, fdp).Services().Get(0)
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

func assertViolations(t *testing.T, violations []string, wantReason string) {
	t.Helper()
	if wantReason == "" {
		if len(violations) != 0 {
			t.Errorf("got violations: %v, want none", violations)
		}
		return
	}
	if len(violations) == 0 {
		t.Fatalf("got no violations, want one containing %q", wantReason)
	}
	if !slices.ContainsFunc(violations, func(v string) bool { return strings.Contains(v, wantReason) }) {
		t.Errorf("got violations %v, want one containing %q", violations, wantReason)
	}
}

// TestEveryOperatorRPCHasAuthorizationRule ensures every operator RPC in
// flowseer.api. carries a valid authorization rule option.
func TestEveryOperatorRPCHasAuthorizationRule(t *testing.T) {
	methodCount := forEachAPIMethod(protoregistry.GlobalFiles, func(method protoreflect.MethodDescriptor) {
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

		apiFile1 := newSyntheticFileDescriptor(t, &descriptorpb.FileDescriptorProto{
			Name:    proto.String("flowseer/api/test/v1/test_service.proto"),
			Package: proto.String("flowseer.api.test.v1"),
			Syntax:  proto.String("proto3"),
			Dependency: []string{
				"flowseer/authz/v1/rule.proto",
				"flowseer/api/edge/v1/edge_admin_service.proto",
			},
			Service: []*descriptorpb.ServiceDescriptorProto{
				{
					Name: proto.String("PrecedingService"),
					Method: []*descriptorpb.MethodDescriptorProto{
						syntheticMethod("PrecedingMethod",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							compliantRule),
					},
				},
				{
					Name: proto.String("MiddleService"),
					Method: []*descriptorpb.MethodDescriptorProto{
						syntheticMethod("FirstMethod",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							compliantRule),
						syntheticMethod("SecondMethod",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							compliantRule),
					},
				},
				{
					Name: proto.String("TestService"),
					Method: []*descriptorpb.MethodDescriptorProto{
						syntheticMethod("RuledMethod",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							compliantRule),
						syntheticMethod("SecondRuledMethod",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							compliantRule),
						syntheticMethod("Ping",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							nil),
					},
				},
			},
		})

		apiFile2 := newSyntheticFileDescriptor(t, &descriptorpb.FileDescriptorProto{
			Name:    proto.String("flowseer/api/sample/v1/sample_service.proto"),
			Package: proto.String("flowseer.api.sample.v1"),
			Syntax:  proto.String("proto3"),
			Dependency: []string{
				"flowseer/authz/v1/rule.proto",
				"flowseer/api/edge/v1/edge_admin_service.proto",
			},
			Service: []*descriptorpb.ServiceDescriptorProto{
				{
					Name: proto.String("AlphaService"),
					Method: []*descriptorpb.MethodDescriptorProto{
						syntheticMethod("AlphaOne",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							compliantRule),
						syntheticMethod("AlphaTwo",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							compliantRule),
						syntheticMethod("AlphaThree",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							compliantRule),
						syntheticMethod("AlphaFour",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							compliantRule),
					},
				},
				{
					Name: proto.String("BetaService"),
					Method: []*descriptorpb.MethodDescriptorProto{
						syntheticMethod("BetaOne",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							compliantRule),
						syntheticMethod("BetaTwo",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							compliantRule),
						syntheticMethod("BetaThree",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							compliantRule),
						syntheticMethod("BetaFour",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							compliantRule),
						syntheticMethod("BetaFive",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							compliantRule),
					},
				},
			},
		})

		siblingFile := newSyntheticFileDescriptor(t, &descriptorpb.FileDescriptorProto{
			Name:    proto.String("flowseer/apix/v1/sibling.proto"),
			Package: proto.String("flowseer.apix.v1"),
			Syntax:  proto.String("proto3"),
			Dependency: []string{
				"flowseer/api/edge/v1/edge_admin_service.proto",
			},
			Service: []*descriptorpb.ServiceDescriptorProto{
				{
					Name: proto.String("SiblingService"),
					Method: []*descriptorpb.MethodDescriptorProto{
						syntheticMethod("SiblingMethod",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							nil),
					},
				},
			},
		})

		otherFile := newSyntheticFileDescriptor(t, &descriptorpb.FileDescriptorProto{
			Name:    proto.String("flowseer/other/synthetic/v1/other.proto"),
			Package: proto.String("flowseer.other.synthetic.v1"),
			Syntax:  proto.String("proto3"),
			Dependency: []string{
				"flowseer/api/edge/v1/edge_admin_service.proto",
			},
			Service: []*descriptorpb.ServiceDescriptorProto{
				{
					Name: proto.String("OtherService"),
					Method: []*descriptorpb.MethodDescriptorProto{
						syntheticMethod("OtherMethod",
							".flowseer.api.edge.v1.GetEdgeRequest",
							".flowseer.api.edge.v1.GetEdgeResponse",
							nil),
					},
				},
			},
		})

		files := new(protoregistry.Files)
		if err := files.RegisterFile(apiFile1); err != nil {
			t.Fatalf("register apiFile1: %v", err)
		}
		if err := files.RegisterFile(apiFile2); err != nil {
			t.Fatalf("register apiFile2: %v", err)
		}
		if err := files.RegisterFile(siblingFile); err != nil {
			t.Fatalf("register siblingFile: %v", err)
		}
		if err := files.RegisterFile(otherFile); err != nil {
			t.Fatalf("register otherFile: %v", err)
		}

		wantVisited := []string{
			"flowseer.api.sample.v1.AlphaService.AlphaFour",
			"flowseer.api.sample.v1.AlphaService.AlphaOne",
			"flowseer.api.sample.v1.AlphaService.AlphaThree",
			"flowseer.api.sample.v1.AlphaService.AlphaTwo",
			"flowseer.api.sample.v1.BetaService.BetaFive",
			"flowseer.api.sample.v1.BetaService.BetaFour",
			"flowseer.api.sample.v1.BetaService.BetaOne",
			"flowseer.api.sample.v1.BetaService.BetaThree",
			"flowseer.api.sample.v1.BetaService.BetaTwo",
			"flowseer.api.test.v1.MiddleService.FirstMethod",
			"flowseer.api.test.v1.MiddleService.SecondMethod",
			"flowseer.api.test.v1.PrecedingService.PrecedingMethod",
			"flowseer.api.test.v1.TestService.Ping",
			"flowseer.api.test.v1.TestService.RuledMethod",
			"flowseer.api.test.v1.TestService.SecondRuledMethod",
		}
		slices.Sort(wantVisited)

		var visited []string
		var violations []string
		count := forEachAPIMethod(files, func(method protoreflect.MethodDescriptor) {
			visited = append(visited, string(method.FullName()))
			violations = append(violations, checkAuthorizationRule(method)...)
		})
		slices.Sort(visited)
		if !slices.Equal(visited, wantVisited) {
			t.Errorf("visited methods %v, want %v", visited, wantVisited)
		}
		if count != len(wantVisited) {
			t.Errorf("forEachAPIMethod count = %d, want %d", count, len(wantVisited))
		}

		wantViolations := []string{"flowseer.api.test.v1.TestService.Ping carries no authorization rule"}
		if !slices.Equal(violations, wantViolations) {
			t.Errorf("got violations %v, want %v", violations, wantViolations)
		}

		t.Run("rule failing validation is reported", func(t *testing.T) {
			invalidRule := authzv1.Rule_builder{
				Mode:       authzv1.RuleMode_RULE_MODE_TENANT.Enum(),
				ObjectType: proto.String("tenant"),
				Relation:   proto.String("INVALID_RELATION"),
			}.Build()
			svc := buildSyntheticService(t, "InvalidRuleService", nil,
				syntheticMethod("InvalidRuleMethod",
					".flowseer.api.edge.v1.GetEdgeRequest",
					".flowseer.api.edge.v1.GetEdgeResponse",
					invalidRule),
			)
			violations := checkAuthorizationRule(svc.Methods().Get(0))
			assertViolations(t, violations, "authorization rule fails validation")
		})
	})
}

// TestAuthorizationRuleObjectPathResolves ensures every request rule object_id_path
// resolves through singular message fields to a string field of the method input.
func TestAuthorizationRuleObjectPathResolves(t *testing.T) {
	requestRuleCount := 0
	forEachAPIMethod(protoregistry.GlobalFiles, func(method protoreflect.MethodDescriptor) {
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

		repeatedIntermediateRule := authzv1.Rule_builder{
			Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
			ObjectType:   proto.String("edge"),
			Relation:     proto.String("administer"),
			ObjectIdPath: proto.String("orphaned.device_id"),
		}.Build()

		nonMessageIntermediateRule := authzv1.Rule_builder{
			Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
			ObjectType:   proto.String("edge"),
			Relation:     proto.String("view"),
			ObjectIdPath: proto.String("edge.edge.id.subfield"),
		}.Build()

		mapIntermediateRule := authzv1.Rule_builder{
			Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
			ObjectType:   proto.String("edge"),
			Relation:     proto.String("view"),
			ObjectIdPath: proto.String("labels.value"),
		}.Build()

		repeatedLeafRule := authzv1.Rule_builder{
			Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
			ObjectType:   proto.String("edge"),
			Relation:     proto.String("view"),
			ObjectIdPath: proto.String("tags"),
		}.Build()

		mapLeafRule := authzv1.Rule_builder{
			Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
			ObjectType:   proto.String("edge"),
			Relation:     proto.String("view"),
			ObjectIdPath: proto.String("labels"),
		}.Build()

		syntheticCarrierMsg := &descriptorpb.DescriptorProto{
			Name: proto.String("SyntheticCarrier"),
			Field: []*descriptorpb.FieldDescriptorProto{
				{
					Name:     proto.String("labels"),
					Number:   proto.Int32(1),
					Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
					Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
					TypeName: proto.String(".flowseer.conformance.synthetic.v1.SyntheticCarrier.LabelsEntry"),
				},
				{
					Name:   proto.String("tags"),
					Number: proto.Int32(2),
					Label:  descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
					Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				},
			},
			NestedType: []*descriptorpb.DescriptorProto{
				{
					Name: proto.String("LabelsEntry"),
					Options: &descriptorpb.MessageOptions{
						MapEntry: proto.Bool(true),
					},
					Field: []*descriptorpb.FieldDescriptorProto{
						{
							Name:   proto.String("key"),
							Number: proto.Int32(1),
							Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
							Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						},
						{
							Name:   proto.String("value"),
							Number: proto.Int32(2),
							Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
							Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						},
					},
				},
			},
		}

		svc := buildSyntheticService(t, "PathResolutionService",
			[]*descriptorpb.DescriptorProto{syntheticCarrierMsg},
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
			syntheticMethod("RepeatedIntermediateMethod",
				".flowseer.api.edge.v1.RetireEdgeResponse",
				".flowseer.api.edge.v1.RetireEdgeResponse",
				repeatedIntermediateRule),
			syntheticMethod("NonMessageIntermediateMethod",
				".flowseer.api.edge.v1.GetEdgeRequest",
				".flowseer.api.edge.v1.GetEdgeResponse",
				nonMessageIntermediateRule),
			syntheticMethod("MapIntermediateMethod",
				".flowseer.conformance.synthetic.v1.SyntheticCarrier",
				".flowseer.conformance.synthetic.v1.SyntheticCarrier",
				mapIntermediateRule),
			syntheticMethod("RepeatedLeafMethod",
				".flowseer.conformance.synthetic.v1.SyntheticCarrier",
				".flowseer.conformance.synthetic.v1.SyntheticCarrier",
				repeatedLeafRule),
			syntheticMethod("MapLeafMethod",
				".flowseer.conformance.synthetic.v1.SyntheticCarrier",
				".flowseer.conformance.synthetic.v1.SyntheticCarrier",
				mapLeafRule),
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
			{
				name:        "repeated intermediate field is reported",
				methodIndex: 4,
				rule:        repeatedIntermediateRule,
				wantReason:  "is not a singular message",
			},
			{
				name:        "non-message intermediate field is reported",
				methodIndex: 5,
				rule:        nonMessageIntermediateRule,
				wantReason:  "has kind string, want message",
			},
			{
				name:        "map intermediate field is reported",
				methodIndex: 6,
				rule:        mapIntermediateRule,
				wantReason:  "is not a singular message",
			},
			{
				name:        "repeated leaf field is reported",
				methodIndex: 7,
				rule:        repeatedLeafRule,
				wantReason:  "is not a singular string",
			},
			{
				name:        "map leaf field is reported",
				methodIndex: 8,
				rule:        mapLeafRule,
				wantReason:  "is not a singular string",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				method := svc.Methods().Get(tt.methodIndex)
				violations := checkObjectIDPath(method, tt.rule)
				assertViolations(t, violations, tt.wantReason)
			})
		}
	})
}

// TestAuthorizationRuleNamesKnownRelation ensures every rule names a permitted
// (object_type, relation) pair.
func TestAuthorizationRuleNamesKnownRelation(t *testing.T) {
	ruleCount := 0
	forEachAPIMethod(protoregistry.GlobalFiles, func(method protoreflect.MethodDescriptor) {
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

		unknownObjectTypeRule := authzv1.Rule_builder{
			Mode:         authzv1.RuleMode_RULE_MODE_REQUEST.Enum(),
			ObjectType:   proto.String("unknown_type"),
			Relation:     proto.String("view"),
			ObjectIdPath: proto.String("edge.edge.id"),
		}.Build()

		svc := buildSyntheticService(t, "KnownRelationService", nil,
			syntheticMethod("CompliantRelationMethod",
				".flowseer.api.edge.v1.GetEdgeRequest",
				".flowseer.api.edge.v1.GetEdgeResponse",
				compliantRule),
			syntheticMethod("UnknownRelationMethod",
				".flowseer.api.edge.v1.GetEdgeRequest",
				".flowseer.api.edge.v1.GetEdgeResponse",
				unknownRelationRule),
			syntheticMethod("UnknownObjectTypeMethod",
				".flowseer.api.edge.v1.GetEdgeRequest",
				".flowseer.api.edge.v1.GetEdgeResponse",
				unknownObjectTypeRule),
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
			{
				name:        "unknown object type is reported",
				methodIndex: 2,
				rule:        unknownObjectTypeRule,
				wantReason:  "unknown object type \"unknown_type\"",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				method := svc.Methods().Get(tt.methodIndex)
				violations := checkKnownRelation(method, tt.rule)
				assertViolations(t, violations, tt.wantReason)
			})
		}
	})
}
