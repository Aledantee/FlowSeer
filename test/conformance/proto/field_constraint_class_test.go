package conformance

import (
	"fmt"
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

	// Linked for the walks below and for nothing else. Every other FlowSeer
	// package reaches protoregistry.GlobalFiles because an example-based test
	// in this directory builds one of its messages; this one has no such test
	// yet, and TestEveryDeclaredProtoPackageIsLinked fails without the
	// import rather than letting the package go unwalked in silence.
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/edge/v1"
)

// interfaceNamePattern is the character class every interface name in this
// schema means, wherever it is declared.
//
// Declared here rather than derived from any one declaration site: a test
// that read its expectation from the schema would move with the drift it
// exists to catch. This constant is the statement that every message
// spelling an interface name means the same thing by it.
//
// It is here because an interface name reaches a device's shell command line
// at every site, and the sibling description field in the same message
// already carried a character-class rule for exactly that reason while the
// name carried only a length.
const interfaceNamePattern = `^[A-Za-z0-9][A-Za-z0-9 ./:_-]*$`

// interfaceNameReaches says where an interface name ends up, and is quoted
// back at whoever declares one without the class.
const interfaceNameReaches = "it is interpolated into the command line an adapter sends over the device's shell"

// namesAnInterface reports whether a field name spells a device interface
// name.
//
// Shape rather than an exact string, because the exact string is what let
// flowseer.net.protocol.lldp.v1.Neighbor.local_interface_name sit unbounded
// through a commit that bounded its four siblings: the rule was keyed on
// "interface_name" and that field is not spelled that way.
func namesAnInterface(name string) bool {
	// The plural is the same value in a list, and the suffix rule cannot see
	// it. It is also the schema's only repeated carrier of an interface
	// name, which makes it the live case for the descent below.
	return name == "interface_name" || strings.HasSuffix(name, "_interface_name") || name == "managed_interfaces"
}

// exempt names a declaration site that spells an interface name and
// deliberately does not carry the class, with the reason.
//
// The distinction is direction. A name a caller supplies becomes a command
// this system sends, and the pattern is what stops it carrying a second one.
// A name a device reports is data this system read back: constraining it to
// what our own commands may contain would reject a row for spelling its own
// interface in a way we did not anticipate, which loses the reading and
// tells an operator nothing about the device.
//
// The bound still applies to both — an unbounded string from a device is a
// different problem — and every row here carries max_len for that reason.
// TestSharedFieldNamesCarryTheSameConstraints checks the bound before it
// consults this map, so an exemption buys the pattern and nothing else.
var exempt = map[string]string{
	"flowseer.net.switching.v1.FdbEntry.interface_name":                 "device-reported egress interface",
	"flowseer.net.protocol.lldp.v1.PortSettings.interface_name":         "device-reported agent interface",
	"flowseer.net.protocol.lldp.v1.Neighbor.local_interface_name":       "device-reported receiving interface",
	"flowseer.net.ip.v1.InterfaceAddress.interface_name":                "device-reported interface",
	"flowseer.net.ip.v1.NeighborEntry.interface_name":                   "device-reported scoping interface",
	"flowseer.net.protocol.stp.v1.PortState.interface_name":             "device-reported spanning tree port",
	"flowseer.net.protocol.stp.v1.BridgeState.root_port_interface_name": "device-reported root port",
	"flowseer.api.inventory.v1.ComponentState.interface_name":           "device-reported port-to-interface join",
	"flowseer.api.inventory.v1.LinkEnd.interface_name":                  "device-reported or announced interface",
}

// TestSharedFieldNamesCarryTheSameConstraints walks every message in the
// FlowSeer schema and checks that a field spelling an interface name carries
// the bound everywhere and the pattern everywhere it is not exempt —
// singular fields, repeated items, and map keys and values alike.
//
// The example-based tests beside this one can only prove a rule that exists:
// a hand-built message exercises a constraint, and a constraint that is
// absent produces no behavior to write a case against. This is the shape that
// catches an absence.
//
// It sees only the packages linked into this binary, which is every package
// some test in this directory imports.
// TestEveryDeclaredProtoPackageIsLinked is what keeps that "every".
func TestSharedFieldNamesCarryTheSameConstraints(t *testing.T) {
	walk := &classWalk{matched: map[string]bool{}}
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), "flowseer.") {
			return true
		}
		walk.messages(fd.Messages())
		return true
	})

	for _, violation := range walk.violations {
		t.Error(violation)
	}

	// An exemption nobody matched is a row that authorizes a site that no
	// longer exists — and it keeps authorizing, because the map is keyed on
	// a full message name that a future message is free to reuse.
	for site, reason := range exempt {
		if !walk.matched[site] {
			t.Errorf("exempt names %s (%s) and the walk found no such field; "+
				"a stale row pre-authorizes any message that later takes that name", site, reason)
		}
	}
}

// classWalk collects the class violations in one pass and records which
// exemptions were live.
type classWalk struct {
	violations []string
	matched    map[string]bool
}

func (w *classWalk) reportf(format string, args ...any) {
	w.violations = append(w.violations, fmt.Sprintf(format, args...))
}

func (w *classWalk) messages(messages protoreflect.MessageDescriptors) {
	for i := range messages.Len() {
		message := messages.Get(i)
		if message.IsMapEntry() {
			continue
		}
		for j := range message.Fields().Len() {
			w.field(message.Fields().Get(j))
		}
		w.messages(message.Messages())
	}
}

// field applies the class to whichever carrier actually holds the rules for
// this field's shape.
//
// The descent is the whole point. protovalidate hangs a repeated field's item
// rules under repeated.items and a map's under map.keys and map.values, all
// of them on the option of the field itself; the synthetic key and value
// descriptors of a map entry carry no option at all. Reading string rules
// straight off the field is therefore right for exactly one of the three
// shapes, and silently reads "" for the other two — which reports a compliant
// list and an unconstrained one identically.
func (w *classWalk) field(field protoreflect.FieldDescriptor) {
	name := string(field.Name())
	if !namesAnInterface(name) {
		return
	}
	rules := fieldRules(field)

	switch {
	case field.IsMap():
		w.carrier(field, "map key", field.MapKey().Kind(), rules.GetMap().GetKeys())
		w.carrier(field, "map value", field.MapValue().Kind(), rules.GetMap().GetValues())
	case field.IsList():
		w.carrier(field, "repeated item", field.Kind(), rules.GetRepeated().GetItems())
	default:
		w.carrier(field, "field", field.Kind(), rules)
	}
}

// carrier applies the class to one carrier position of one field.
func (w *classWalk) carrier(owner protoreflect.FieldDescriptor, position string, kind protoreflect.Kind, rules *validate.FieldRules) {
	site := string(owner.ContainingMessage().FullName()) + "." + string(owner.Name())

	// A non-string carrier is not out of scope, it is unexaminable: bytes
	// named interface_name reaches the same command line and no string rule
	// can be written against it.
	if kind != protoreflect.StringKind {
		w.reportf("%s spells an interface name but its %s is %s, so no string rule can constrain it; %s",
			site, position, kind, interfaceNameReaches)
		return
	}

	// Before the exemption, not inside it. An exemption is from the pattern;
	// the bound is what stops an arbitrarily long name reaching the shell,
	// and that argument does not care who supplied the name.
	if !declaresMaxLen(rules) {
		w.reportf("%s declares no max_len on its %s; %s", site, position, interfaceNameReaches)
	}

	if _, deviated := exempt[site]; deviated {
		w.matched[site] = true
		return
	}
	if got := rules.GetString().GetPattern(); got != interfaceNamePattern {
		w.reportf("%s carries pattern %q on its %s, want %q\n  an interface name reaches a place where that matters: %s",
			site, got, position, interfaceNamePattern, interfaceNameReaches)
	}
}

// declaresMaxLen reports whether a carrier's rules bound the string's length.
//
// Nil-safe at both levels, which is the whole reason it exists: a carrier
// with no rules at all is the case the walk has to report rather than crash
// on.
func declaresMaxLen(rules *validate.FieldRules) bool {
	s := rules.GetString()
	return s != nil && s.MaxLen != nil
}

// TestTheClassWalkReadsEveryCarrierShape drives the walk over descriptors
// built here, because the schema has no non-compliant field to point it at.
//
// It is the case the walk itself cannot make: every real declaration is
// compliant, so a walk that read nothing at all would pass the suite. These
// carriers are built compliant and non-compliant in each of the three shapes,
// and the non-compliant ones have to be reported.
func TestTheClassWalkReadsEveryCarrierShape(t *testing.T) {
	file := syntheticCarriers(t)

	walk := &classWalk{matched: map[string]bool{}}
	walk.messages(file.Messages())

	// Keyed by message, because a site name is message-qualified and each
	// case here is its own message.
	want := map[string][]string{
		"CompliantSingular": nil,
		"CompliantList":     nil,
		"CompliantMap":      nil,
		"SingularWithoutMaxLen": {
			"SingularWithoutMaxLen.interface_name declares no max_len on its field",
		},
		"ListWithoutItemRules": {
			"ListWithoutItemRules.interface_name declares no max_len on its repeated item",
			`ListWithoutItemRules.interface_name carries pattern "" on its repeated item`,
		},
		"MapWithoutEntryRules": {
			"MapWithoutEntryRules.interface_name declares no max_len on its map key",
			`MapWithoutEntryRules.interface_name carries pattern "" on its map key`,
			"MapWithoutEntryRules.interface_name declares no max_len on its map value",
			`MapWithoutEntryRules.interface_name carries pattern "" on its map value`,
		},
		"BytesCarrier": {
			"BytesCarrier.interface_name spells an interface name but its field is bytes",
		},
	}

	for message, fragments := range want {
		reported := violationsFor(walk.violations, message+".")
		if len(reported) != len(fragments) {
			t.Errorf("%s produced %d violations, want %d:\n  %s",
				message, len(reported), len(fragments), strings.Join(reported, "\n  "))
			continue
		}
		for _, fragment := range fragments {
			if !slices.ContainsFunc(reported, func(v string) bool { return strings.Contains(v, fragment) }) {
				t.Errorf("%s reported no violation containing %q; got:\n  %s",
					message, fragment, strings.Join(reported, "\n  "))
			}
		}
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

// syntheticCarriers builds a file whose messages spell an interface name in
// every carrier shape the walk has to descend into.
func syntheticCarriers(t *testing.T) protoreflect.FileDescriptor {
	t.Helper()

	compliantString := &validate.StringRules{
		MaxLen:  proto.Uint64(64),
		Pattern: proto.String(interfaceNamePattern),
	}
	compliant := func() *validate.FieldRules {
		return &validate.FieldRules{Type: &validate.FieldRules_String_{String_: proto.Clone(compliantString).(*validate.StringRules)}}
	}

	fdp := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("flowseer/conformance/synthetic/v1/carriers.proto"),
		Package:    proto.String("flowseer.conformance.synthetic.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"buf/validate/validate.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			singularCarrier("CompliantSingular", protoreflect.StringKind, compliant()),
			singularCarrier("SingularWithoutMaxLen", protoreflect.StringKind, &validate.FieldRules{
				Required: proto.Bool(true),
				Type: &validate.FieldRules_String_{String_: &validate.StringRules{
					Pattern: proto.String(interfaceNamePattern),
				}},
			}),
			singularCarrier("BytesCarrier", protoreflect.BytesKind, nil),
			listCarrier("CompliantList", &validate.FieldRules{
				Type: &validate.FieldRules_Repeated{Repeated: &validate.RepeatedRules{Items: compliant()}},
			}),
			listCarrier("ListWithoutItemRules", nil),
			mapCarrier("CompliantMap", &validate.FieldRules{
				Type: &validate.FieldRules_Map{Map: &validate.MapRules{Keys: compliant(), Values: compliant()}},
			}),
			mapCarrier("MapWithoutEntryRules", nil),
		},
	}

	file, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatalf("build the synthetic carriers: %v", err)
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
		// Vendored third-party schemas are here as reference material, not as
		// this system's contracts, and the class rules do not speak about
		// them.
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
// Every map added to this schema is bounded except one, and the suite tests
// the upper bound of each map that has one — so it tests declared bounds and
// is structurally silent on absent ones.
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
