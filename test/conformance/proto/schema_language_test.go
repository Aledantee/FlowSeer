package conformance

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// floatingPointAllowlist names the fields the schema language rules a float
// or double out of use for: quantities with no native fixed-point resolution
// (geographic coordinates, an operator-written decimal attribute value).
// Every other quantity is an integer in a canonical unit.
var floatingPointAllowlist = []string{
	"flowseer.model.inventory.v1.Location.latitude",
	"flowseer.model.inventory.v1.Location.longitude",
	"flowseer.model.inventory.v1.Number.decimal",
}

// TestNoFloatingPointFields walks every FlowSeer package and fails on a float
// or double field outside the allowlist. A float anywhere else hides a unit
// decision nobody made: precision and range are chosen per quantity, in an
// integer unit, at schema time.
func TestNoFloatingPointFields(t *testing.T) {
	eachFlowseerFile(func(fd protoreflect.FileDescriptor) {
		for _, violation := range floatingPointViolations(fd.Messages()) {
			t.Error(violation)
		}
	})

	// A descriptor that breaks only this rule must be flagged, or the walk
	// above cannot tell a clean tree from one it read nothing from.
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("flowseer/conformance/synthetic/v1/floaty.proto"),
		Package: proto.String("flowseer.conformance.synthetic.v1"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Floaty"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:   proto.String("loss_percent"),
				Number: proto.Int32(1),
				Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:   descriptorpb.FieldDescriptorProto_TYPE_FLOAT.Enum(),
			}},
		}},
	}
	file, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatalf("build the synthetic descriptor: %v", err)
	}
	if got := floatingPointViolations(file.Messages()); len(got) != 1 {
		t.Errorf("synthetic float field produced %d violations, want 1: %v", len(got), got)
	}
}

// floatingPointViolations returns one line per float or double field in
// messages, descending into nested types and map entries, outside the
// allowlist.
func floatingPointViolations(messages protoreflect.MessageDescriptors) []string {
	var violations []string
	eachField(messages, func(field protoreflect.FieldDescriptor) {
		if field.Kind() != protoreflect.FloatKind && field.Kind() != protoreflect.DoubleKind {
			return
		}
		site := string(field.ContainingMessage().FullName()) + "." + string(field.Name())
		if !slices.Contains(floatingPointAllowlist, site) {
			violations = append(violations, fmt.Sprintf("%s is %s and no allowlist row permits it; "+
				"a float outside the three permitted sites hides a canonical-unit decision", site, field.Kind()))
		}
	})
	return violations
}

// eachFlowseerFile calls fn for every FlowSeer-owned file descriptor linked
// into this binary. TestEveryDeclaredProtoPackageIsLinked is what keeps
// "every" true.
func eachFlowseerFile(fn func(protoreflect.FileDescriptor)) {
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if strings.HasPrefix(string(fd.Package()), "flowseer.") {
			fn(fd)
		}
		return true
	})
}

// eachField calls fn for every field in messages, recursing into nested
// message types. Map entries are included: their key and value kinds are
// exactly where a map-carried float would hide.
func eachField(messages protoreflect.MessageDescriptors, fn func(protoreflect.FieldDescriptor)) {
	for i := range messages.Len() {
		message := messages.Get(i)
		for j := range message.Fields().Len() {
			fn(message.Fields().Get(j))
		}
		eachField(message.Messages(), fn)
	}
}
