package conformance

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	keyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/key/v1"

	// Linked for the walks below and for nothing else. Every other FlowSeer
	// package reaches protoregistry.GlobalFiles because an example-based test
	// in this directory builds one of its messages; this one has no such test
	// yet, and TestEveryDeclaredProtoPackageIsLinked fails without the
	// import rather than letting the package go unwalked in silence.
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lacp/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/agent/v1"
)

// TestTheClassWalkReadsEveryCarrierShape drives the key-rule walk over
// descriptors built here, one compliant and one non-compliant carrier in each
// shape it has to descend into.
//
// It is the case the walk itself cannot make: every real declaration is
// compliant, so a walk that read nothing at all would pass the suite. A
// repeated item or a map key and value is read from the field's own option,
// not from the carrier descriptor, and the non-compliant ones have to be
// reported.
func TestTheClassWalkReadsEveryCarrierShape(t *testing.T) {
	file := buildSyntheticCarriers(t, "carriers.proto",
		singularCarrier("CompliantSingular", protoreflect.StringKind, keyRules(keyv1.E_InterfaceName)),
		singularCarrier("SingularWithoutRule", protoreflect.StringKind, nil),
		singularCarrier("BytesCarrier", protoreflect.BytesKind, nil),
		listCarrier("CompliantList", &validate.FieldRules{
			Type: &validate.FieldRules_Repeated{Repeated: &validate.RepeatedRules{Items: restatedItems(keyNameMaxLen, shellSafeInterfaceNamePattern)}},
		}),
		listCarrier("ListWithoutItemRules", nil),
		mapCarrier("CompliantMap", &validate.FieldRules{
			Type: &validate.FieldRules_Map{Map: &validate.MapRules{
				Keys:   restatedItems(keyNameMaxLen, ""),
				Values: restatedItems(keyNameMaxLen, ""),
			}},
		}),
		mapCarrier("MapWithoutEntryRules", nil),
	)

	walk := &keyWalk{}
	walk.messages(file.Messages())

	// Keyed by message, because a site name is message-qualified and each
	// case here is its own message.
	const none = "carries none of interface_name or shell_safe_interface_name on its "
	want := map[string][]string{
		"CompliantSingular":    nil,
		"CompliantList":        nil,
		"CompliantMap":         nil,
		"SingularWithoutRule":  {"SingularWithoutRule.interface_name spells an interface name and " + none + "field"},
		"ListWithoutItemRules": {"ListWithoutItemRules.interface_name spells an interface name and " + none + "repeated item"},
		"MapWithoutEntryRules": {
			"MapWithoutEntryRules.interface_name spells an interface name and " + none + "map key",
			"MapWithoutEntryRules.interface_name spells an interface name and " + none + "map value",
		},
		"BytesCarrier": {"BytesCarrier.interface_name spells an interface name but its field is bytes"},
	}

	for message, fragments := range want {
		t.Run(message, func(t *testing.T) {
			reported := violationsFor(walk.violations, message+".")
			if len(reported) != len(fragments) {
				t.Errorf("got %d violations, want %d:\n  %s",
					len(reported), len(fragments), strings.Join(reported, "\n  "))
				return
			}
			for _, fragment := range fragments {
				if !slices.ContainsFunc(reported, func(v string) bool { return strings.Contains(v, fragment) }) {
					t.Errorf("got violations:\n  %s\nwant one containing %q",
						strings.Join(reported, "\n  "), fragment)
				}
			}
		})
	}
}

func violationsFor(violations []string, prefix string) []string {
	var out []string
	for _, v := range violations {
		if strings.Contains(v, prefix) {
			out = append(out, v)
		}
	}
	return out
}

// buildSyntheticCarriers builds a file of the given messages against
// GlobalFiles, which resolves the net/key rules the carriers name.
func buildSyntheticCarriers(t *testing.T, name string, messages ...*descriptorpb.DescriptorProto) protoreflect.FileDescriptor {
	t.Helper()

	fdp := &descriptorpb.FileDescriptorProto{
		Name:        proto.String("flowseer/conformance/synthetic/v1/" + name),
		Package:     proto.String("flowseer.conformance.synthetic.v1"),
		Syntax:      proto.String("proto3"),
		Dependency:  []string{"buf/validate/validate.proto", "flowseer/net/key/v1/key.proto"},
		MessageType: messages,
	}

	file, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatalf("build the synthetic carriers in %s: %v", name, err)
	}
	return file
}

func singularCarrier(message string, kind protoreflect.Kind, rules *validate.FieldRules) *descriptorpb.DescriptorProto {
	fieldType := descriptorpb.FieldDescriptorProto_TYPE_STRING
	if kind == protoreflect.BytesKind {
		fieldType = descriptorpb.FieldDescriptorProto_TYPE_BYTES
	}
	return &descriptorpb.DescriptorProto{
		Name: proto.String(message),
		Field: []*descriptorpb.FieldDescriptorProto{{
			Name:    proto.String("interface_name"),
			Number:  proto.Int32(1),
			Label:   descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:    fieldType.Enum(),
			Options: validateOption(rules),
		}},
	}
}

func listCarrier(message string, rules *validate.FieldRules) *descriptorpb.DescriptorProto {
	return &descriptorpb.DescriptorProto{
		Name: proto.String(message),
		Field: []*descriptorpb.FieldDescriptorProto{{
			Name:    proto.String("interface_name"),
			Number:  proto.Int32(1),
			Label:   descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
			Type:    descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			Options: validateOption(rules),
		}},
	}
}

func mapCarrier(message string, rules *validate.FieldRules) *descriptorpb.DescriptorProto {
	entry := message + ".InterfaceNameEntry"
	return &descriptorpb.DescriptorProto{
		Name: proto.String(message),
		Field: []*descriptorpb.FieldDescriptorProto{{
			Name:     proto.String("interface_name"),
			Number:   proto.Int32(1),
			Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
			Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
			TypeName: proto.String(".flowseer.conformance.synthetic.v1." + entry),
			Options:  validateOption(rules),
		}},
		NestedType: []*descriptorpb.DescriptorProto{{
			Name:    proto.String("InterfaceNameEntry"),
			Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
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
		}},
	}
}

func validateOption(rules *validate.FieldRules) *descriptorpb.FieldOptions {
	if rules == nil {
		return nil
	}
	opts := &descriptorpb.FieldOptions{}
	proto.SetExtension(opts, validate.E_Field, rules)
	return opts
}

// TestEveryDeclaredProtoPackageIsLinked closes the walk's blind spot: both
// class tests read protoregistry.GlobalFiles, which holds the packages this
// binary imports and nothing else, so a schema file no test in this directory
// touches is skipped rather than reported.
//
// A file added without an example-based test beside it fails here, which is
// the honest place for it to fail — the fix is to import the package, and
// then the class walk covers it.
func TestEveryDeclaredProtoPackageIsLinked(t *testing.T) {
	linked := map[string]bool{}
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		linked[fd.Path()] = true
		return true
	})

	protoRoot := filepath.Join(repoRoot(t), "spec", "proto")
	err := filepath.WalkDir(protoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".proto" {
			return err
		}
		rel, err := filepath.Rel(protoRoot, path)
		if err != nil {
			return err
		}
		// The class rules cover FlowSeer schemas. Vendored third-party schemas
		// are reference material.
		if !strings.HasPrefix(filepath.ToSlash(rel), "flowseer/") {
			return nil
		}
		if !linked[filepath.ToSlash(rel)] {
			t.Errorf("%s is not linked into the conformance binary, so the class walks never see it; "+
				"import its generated package from a test in this directory", filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", protoRoot, err)
	}
}

// TestEveryMapDeclaresAnUpperBound is the second class rule: a map with no
// max_pairs is an unbounded field on a message a peer produces.
//
// The upper bound must be declared before per-map tests can exercise it. Tests
// of existing bounds are structurally silent on an absent max_pairs rule.
func TestEveryMapDeclaresAnUpperBound(t *testing.T) {
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), "flowseer.") {
			return true
		}
		checkMapBounds(t, fd.Messages())
		return true
	})
}

func checkMapBounds(t *testing.T, messages protoreflect.MessageDescriptors) {
	t.Helper()
	for i := range messages.Len() {
		message := messages.Get(i)
		if message.IsMapEntry() {
			continue
		}
		for j := range message.Fields().Len() {
			field := message.Fields().Get(j)
			if field.IsMap() && !hasMaxPairs(field) {
				t.Errorf("%s.%s is a map with no max_pairs; a peer decides how large it is",
					message.FullName(), field.Name())
			}
		}
		checkMapBounds(t, message.Messages())
	}
}

// hasMaxPairs reports whether a map field declares an upper bound.
func hasMaxPairs(field protoreflect.FieldDescriptor) bool {
	rules := fieldRules(field)
	return rules != nil && rules.GetMap() != nil && rules.GetMap().MaxPairs != nil
}

func fieldRules(field protoreflect.FieldDescriptor) *validate.FieldRules {
	opts, ok := field.Options().(*descriptorpb.FieldOptions)
	if !ok || opts == nil {
		return nil
	}
	ext := proto.GetExtension(opts, validate.E_Field)
	rules, _ := ext.(*validate.FieldRules)
	return rules
}
