package conformance

import (
	"fmt"
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

// keyNameMaxLen is the upper bound every device-local key rule enforces: the
// SNMPv2-TC DisplayString SIZE (0..255), which IF-MIB ifName uses. A repeated
// carrier restates it on its items, and this constant is what the restatement
// is checked against.
//
// Declared here rather than read from net/key: a test that took its
// expectation from the schema would move with the drift it exists to catch.
const keyNameMaxLen = 255

// shellSafeInterfaceNamePattern is the character class
// shell_safe_interface_name enforces, restated by a repeated carrier of a
// name FlowSeer sends to a device.
const shellSafeInterfaceNamePattern = `^[A-Za-z0-9][A-Za-z0-9 ./:_-]*$`

// keyRule is one predefined rule from net/key, with the character class a
// repeated carrier restates for it ("" when the rule has none).
type keyRule struct {
	ext     protoreflect.ExtensionType
	pattern string
}

func (r keyRule) name() string {
	return string(r.ext.TypeDescriptor().Name())
}

// keyClass is one kind of device-local key: which field names spell it and
// which net/key rules may validate it. A carrier takes exactly one of them.
type keyClass struct {
	noun  string
	names func(string) bool
	rules []keyRule
}

// keyClasses are the device-local keys whose fields the walk holds to the
// net/key rules. An interface name has two rules because it has two uses: a
// name the device reported, and a name FlowSeer sends back into a command
// line on the device.
var keyClasses = []keyClass{
	{
		noun:  "an interface name",
		names: namesAnInterface,
		rules: []keyRule{
			{ext: keyv1.E_InterfaceName},
			{ext: keyv1.E_ShellSafeInterfaceName, pattern: shellSafeInterfaceNamePattern},
		},
	},
	{
		noun:  "a network instance name",
		names: namesANetworkInstance,
		rules: []keyRule{{ext: keyv1.E_NetworkInstanceName}},
	},
}

// namesAnInterface reports whether a field name spells a device interface
// name.
//
// Shape rather than an exact string, because the exact string is what let
// flowseer.net.protocol.lldp.v1.Neighbor.local_interface_name sit unbounded
// through a commit that bounded its four siblings: the rule was keyed on
// "interface_name" and that field is not spelled that way. The shape is
// complete only because the schema names every interface-name field this
// way; a field holding one under another name is misnamed.
func namesAnInterface(name string) bool {
	// The plural is the same value in a list, and the suffix rule cannot see
	// it. It is also the schema's only repeated carrier of an interface
	// name, which makes it the live case for the item descent below.
	return name == "interface_name" || strings.HasSuffix(name, "_interface_name") || name == "managed_interfaces"
}

// namesANetworkInstance reports whether a field name spells the network
// instance a device-local row belongs to.
func namesANetworkInstance(name string) bool {
	return name == "network_instance" || strings.HasSuffix(name, "_network_instance")
}

// TestKeyFieldsUseKeyRules walks every FlowSeer package and holds each field
// spelling a device-local key to the net/key rules: a singular carrier takes
// exactly one of its class's rules and no min_len, max_len, or pattern of its
// own, so the bound and character class are stated once in net/key. A
// repeated or map carrier cannot name the extension from a protovalidate
// aggregate, so its items restate one rule's bounds exactly.
//
// A violation names the full message and field, so a stale claim about a
// site is visible in the output rather than inferred.
func TestKeyFieldsUseKeyRules(t *testing.T) {
	walk := &keyWalk{}
	eachFlowseerFile(func(fd protoreflect.FileDescriptor) {
		walk.messages(fd.Messages())
	})
	for _, violation := range walk.violations {
		t.Error(violation)
	}

	// One synthetic message per way a carrier breaks the rule. Each must be
	// flagged, or the walk above cannot tell a clean tree from one it read
	// nothing from.
	withBound := keyRules(keyv1.E_InterfaceName)
	withBound.GetString().MaxLen = proto.Uint64(64)
	networkInstance := singularCarrier("NetworkInstanceWithoutRule", protoreflect.StringKind, nil)
	networkInstance.Field[0].Name = proto.String("network_instance")

	file := buildSyntheticCarriers(t, "key_breaks.proto",
		singularCarrier("NoRule", protoreflect.StringKind, &validate.FieldRules{Required: proto.Bool(true)}),
		singularCarrier("BothRules", protoreflect.StringKind, keyRules(keyv1.E_InterfaceName, keyv1.E_ShellSafeInterfaceName)),
		singularCarrier("OwnBoundBesideRule", protoreflect.StringKind, withBound),
		listCarrier("ItemsWithWrongBounds", &validate.FieldRules{
			Type: &validate.FieldRules_Repeated{Repeated: &validate.RepeatedRules{Items: restatedItems(64, shellSafeInterfaceNamePattern)}},
		}),
		networkInstance,
	)
	synthetic := &keyWalk{}
	synthetic.messages(file.Messages())

	want := map[string]string{
		"NoRule":                     "NoRule.interface_name spells an interface name and carries none of interface_name or shell_safe_interface_name on its field",
		"BothRules":                  "BothRules.interface_name carries interface_name and shell_safe_interface_name together on its field",
		"OwnBoundBesideRule":         "OwnBoundBesideRule.interface_name declares its own max_len beside interface_name on its field",
		"ItemsWithWrongBounds":       "ItemsWithWrongBounds.interface_name restates min_len 1, max_len 64, pattern",
		"NetworkInstanceWithoutRule": "NetworkInstanceWithoutRule.network_instance spells a network instance name and carries none of network_instance_name on its field",
	}
	for message, fragment := range want {
		reported := violationsFor(synthetic.violations, message+".")
		if len(reported) != 1 || !strings.Contains(reported[0], fragment) {
			t.Errorf("%s produced %d violations, want one containing %q:\n  %s",
				message, len(reported), fragment, strings.Join(reported, "\n  "))
		}
	}
}

// keyWalk collects key-rule violations in one pass.
type keyWalk struct {
	violations []string
}

func (w *keyWalk) reportf(format string, args ...any) {
	w.violations = append(w.violations, fmt.Sprintf(format, args...))
}

func (w *keyWalk) messages(messages protoreflect.MessageDescriptors) {
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

// field applies the key rules to whichever carrier holds the rules for this
// field's shape.
//
// The descent is the whole point. protovalidate hangs a repeated field's item
// rules under repeated.items and a map's under map.keys and map.values, all
// of them on the option of the field itself; the synthetic key and value
// descriptors of a map entry carry no option at all. Reading string rules
// straight off the field is therefore right for exactly one of the three
// shapes, and silently reads nothing for the other two, which reports a
// compliant list and an unconstrained one identically.
func (w *keyWalk) field(field protoreflect.FieldDescriptor) {
	name := string(field.Name())
	for _, class := range keyClasses {
		if !class.names(name) {
			continue
		}
		site := string(field.ContainingMessage().FullName()) + "." + name
		rules := fieldRules(field)
		switch {
		case field.IsMap():
			w.carrier(class, site, "map key", field.MapKey().Kind(), rules.GetMap().GetKeys(), true)
			w.carrier(class, site, "map value", field.MapValue().Kind(), rules.GetMap().GetValues(), true)
		case field.IsList():
			w.carrier(class, site, "repeated item", field.Kind(), rules.GetRepeated().GetItems(), true)
		default:
			w.carrier(class, site, "field", field.Kind(), rules, false)
		}
	}
}

// carrier checks one carrier position of one field. An aggregate carrier (a
// repeated item, a map key or value) may restate one rule's bounds in place
// of naming the rule.
func (w *keyWalk) carrier(class keyClass, site, position string, kind protoreflect.Kind, rules *validate.FieldRules, aggregate bool) {
	// A non-string carrier is not out of scope, it is unexaminable: no
	// string rule can be written against it.
	if kind != protoreflect.StringKind {
		w.reportf("%s spells %s but its %s is %s, so no string rule can constrain it", site, class.noun, position, kind)
		return
	}

	s := rules.GetString()
	var carried []string
	for _, rule := range class.rules {
		if s != nil && proto.HasExtension(s, rule.ext) && proto.GetExtension(s, rule.ext).(bool) {
			carried = append(carried, rule.name())
		}
	}
	own := ownStringBounds(s)

	switch {
	case len(carried) > 1:
		w.reportf("%s carries %s together on its %s; exactly one applies", site, strings.Join(carried, " and "), position)
	case len(carried) == 1 && len(own) > 0:
		w.reportf("%s declares its own %s beside %s on its %s; the rule is the bound",
			site, strings.Join(own, ", "), carried[0], position)
	case len(carried) == 1:
	case len(own) == 0 || !aggregate:
		w.reportf("%s spells %s and carries none of %s on its %s", site, class.noun, class.ruleNames(), position)
	case !restatesOneRule(class, s):
		w.reportf("%s restates %s on its %s, want min_len 1, max_len %d, and the pattern of exactly one of %s",
			site, describeBounds(s), position, keyNameMaxLen, class.ruleNames())
	}
}

func (c keyClass) ruleNames() string {
	names := make([]string, 0, len(c.rules))
	for _, rule := range c.rules {
		names = append(names, rule.name())
	}
	return strings.Join(names, " or ")
}

// ownStringBounds names the length and pattern rules a carrier declares
// itself, in the order a violation lists them.
func ownStringBounds(s *validate.StringRules) []string {
	if s == nil {
		return nil
	}
	var own []string
	if s.MinLen != nil {
		own = append(own, "min_len")
	}
	if s.MaxLen != nil {
		own = append(own, "max_len")
	}
	if s.Pattern != nil {
		own = append(own, "pattern")
	}
	return own
}

// restatesOneRule reports whether an aggregate carrier's own bounds are
// exactly one of the class's rules restated: min_len 1, the shared maximum,
// and that rule's character class or none.
func restatesOneRule(class keyClass, s *validate.StringRules) bool {
	if s.GetMinLen() != 1 || s.GetMaxLen() != keyNameMaxLen {
		return false
	}
	return slices.ContainsFunc(class.rules, func(rule keyRule) bool { return s.GetPattern() == rule.pattern })
}

func describeBounds(s *validate.StringRules) string {
	var parts []string
	if s.MinLen != nil {
		parts = append(parts, fmt.Sprintf("min_len %d", s.GetMinLen()))
	}
	if s.MaxLen != nil {
		parts = append(parts, fmt.Sprintf("max_len %d", s.GetMaxLen()))
	}
	if s.Pattern != nil {
		parts = append(parts, fmt.Sprintf("pattern %q", s.GetPattern()))
	}
	return strings.Join(parts, ", ")
}

// keyRules returns field rules naming each of exts, as a singular carrier of
// a key declares them.
func keyRules(exts ...protoreflect.ExtensionType) *validate.FieldRules {
	s := &validate.StringRules{}
	for _, ext := range exts {
		proto.SetExtension(s, ext, true)
	}
	return &validate.FieldRules{Type: &validate.FieldRules_String_{String_: s}}
}

// restatedItems returns the item rules a repeated carrier restates: min_len
// 1, maxLen, and pattern when it is not empty.
func restatedItems(maxLen uint64, pattern string) *validate.FieldRules {
	s := &validate.StringRules{MinLen: proto.Uint64(1), MaxLen: proto.Uint64(maxLen)}
	if pattern != "" {
		s.Pattern = proto.String(pattern)
	}
	return &validate.FieldRules{Type: &validate.FieldRules_String_{String_: s}}
}
