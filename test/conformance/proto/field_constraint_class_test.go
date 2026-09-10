package conformance

import (
	"strings"
	"testing"

	"buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// requiredRules is the constraint set a field name must carry wherever it is
// declared, keyed by field name.
//
// Declared here rather than derived from any one declaration site: a test
// that read its expectation from the schema would move with the drift it
// exists to catch. A row is the statement that every message spelling this
// field means the same thing by it.
//
// interface_name is here because it reaches a device's shell command line at
// every site, and the sibling description field in the same message already
// carried a character-class rule for exactly that reason while the name
// carried only a length.
var requiredRules = map[string]struct {
	pattern string
	why     string
}{
	"interface_name": {
		pattern: `^[A-Za-z0-9][A-Za-z0-9 ./:_-]*$`,
		why:     "it is interpolated into the command line an adapter sends over the device's shell",
	},
}

// exempt names a declaration site that carries a field in requiredRules and
// deliberately does not carry its pattern, with the reason.
//
// The distinction is direction. A name a caller supplies becomes a command
// this system sends, and the pattern is what stops it carrying a second one.
// A name a device reports is data this system read back: constraining it to
// what our own commands may contain would reject a row for spelling its own
// interface in a way we did not anticipate, which loses the reading and
// tells an operator nothing about the device.
//
// The bound still applies to both — an unbounded string from a device is a
// different problem — and these four carry max_len for that reason.
var exempt = map[string]string{
	"flowseer.net.switching.v1.FdbEntry.interface_name":         "device-reported egress interface",
	"flowseer.net.protocol.lldp.v1.PortSettings.interface_name": "device-reported agent interface",
	"flowseer.net.ip.v1.InterfaceAddress.interface_name":        "device-reported interface",
	"flowseer.net.ip.v1.NeighborEntry.interface_name":           "device-reported scoping interface",
}

// TestSharedFieldNamesCarryTheSameConstraints walks every message in the
// FlowSeer schema and checks that a field named in requiredRules carries its
// pattern, wherever it is declared — singular fields, repeated items, and map
// keys alike.
//
// The example-based tests beside this one can only prove a rule that exists:
// a hand-built message exercises a constraint, and a constraint that is
// absent produces no behavior to write a case against. This is the shape that
// catches an absence.
func TestSharedFieldNamesCarryTheSameConstraints(t *testing.T) {
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), "flowseer.") {
			return true
		}
		checkMessages(t, fd.Messages())
		return true
	})
}

func checkMessages(t *testing.T, messages protoreflect.MessageDescriptors) {
	t.Helper()
	for i := range messages.Len() {
		message := messages.Get(i)
		if message.IsMapEntry() {
			continue
		}
		for j := range message.Fields().Len() {
			checkField(t, message.Fields().Get(j))
		}
		checkMessages(t, message.Messages())
	}
}

func checkField(t *testing.T, field protoreflect.FieldDescriptor) {
	t.Helper()

	if field.IsMap() {
		checkNamed(t, field, string(field.Name()), field.MapKey())
		checkNamed(t, field, string(field.Name()), field.MapValue())
		return
	}
	checkNamed(t, field, string(field.Name()), field)
}

// checkNamed applies the row for name, if there is one, to the descriptor
// that actually carries the constraint.
func checkNamed(t *testing.T, owner protoreflect.FieldDescriptor, name string, carrier protoreflect.FieldDescriptor) {
	t.Helper()

	rule, ok := requiredRules[name]
	if !ok || carrier.Kind() != protoreflect.StringKind {
		return
	}
	site := string(owner.ContainingMessage().FullName()) + "." + name
	if _, deviated := exempt[site]; deviated {
		// Still bounded, even where the pattern does not apply.
		if !hasMaxLen(carrier) {
			t.Errorf("%s is exempt from the %s pattern but declares no max_len", site, name)
		}
		return
	}
	if got := stringPattern(carrier); got != rule.pattern {
		t.Errorf("%s.%s carries pattern %q, want %q\n  %s reaches a place where that matters: %s",
			owner.ContainingMessage().FullName(), name, got, rule.pattern, name, rule.why)
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

// stringPattern is the string.pattern rule a field carries, or "" when it
// carries none.
func stringPattern(field protoreflect.FieldDescriptor) string {
	rules := fieldRules(field)
	if rules == nil || rules.GetString() == nil {
		return ""
	}
	return rules.GetString().GetPattern()
}

// hasMaxLen reports whether a string field declares an upper bound.
func hasMaxLen(field protoreflect.FieldDescriptor) bool {
	rules := fieldRules(field)
	return rules != nil && rules.GetString() != nil && rules.GetString().MaxLen != nil
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
