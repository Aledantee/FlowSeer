package conformance

import (
	"strings"
	"testing"

	"buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	keyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/key/v1"
)

// TestInterfaceNameRule exercises the rule a device-reported interface name
// carries: the 1-to-255-character bound and nothing else, because a device
// may spell its own interface in ways our own command lines cannot carry.
func TestInterfaceNameRule(t *testing.T) {
	candidate := stringRuleCarrier(t, keyv1.E_InterfaceName)

	runValidationCases(t, []validationCase{
		{name: "an empty name is rejected", message: candidate(""), wantValid: false},
		{name: "a name of 256 characters is rejected", message: candidate(strings.Repeat("a", 256)), wantValid: false},
		{name: "a name of 255 characters is accepted", message: candidate(strings.Repeat("a", 255)), wantValid: true},
		{name: "parentheses and spaces are accepted", message: candidate("Gi1/0/1 (uplink)"), wantValid: true},
	})
}

// TestShellSafeInterfaceNameRule exercises the rule for a name FlowSeer
// interpolates into a shell command line the adapter sends to a device: the
// same bound, plus the character class.
func TestShellSafeInterfaceNameRule(t *testing.T) {
	candidate := stringRuleCarrier(t, keyv1.E_ShellSafeInterfaceName)

	runValidationCases(t, []validationCase{
		{name: "a shell metacharacter is rejected", message: candidate("eth0;reboot"), wantValid: false},
		{name: "a leading non-alphanumeric is rejected", message: candidate(" href/index.html"), wantValid: false},
		{name: "a device-style interface name is accepted", message: candidate("GigabitEthernet1/0/1"), wantValid: true},
		{name: "the full character class is accepted", message: candidate("Gi1/0/1 eth-trunk.100_2:3-4 x5"), wantValid: true},
	})
}

// stringRuleCarrier returns a constructor for values of a synthetic message
// carrying one string field whose only rule is the predefined rule ext. The
// key rules live on no message of their own, so a carrier is what gives
// protovalidate something to evaluate.
func stringRuleCarrier(t *testing.T, ext protoreflect.ExtensionType) func(string) proto.Message {
	t.Helper()

	rules := &validate.StringRules{}
	proto.SetExtension(rules, ext, true)

	opts := &descriptorpb.FieldOptions{}
	proto.SetExtension(opts, validate.E_Field, &validate.FieldRules{
		Type: &validate.FieldRules_String_{String_: rules},
	})

	// proto2 syntax so the carrier field has explicit presence without the
	// synthetic oneof proto3 optional fields require.
	fdp := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("flowseer/conformance/synthetic/v1/key_carrier.proto"),
		Package:    proto.String("flowseer.conformance.synthetic.v1"),
		Syntax:     proto.String("proto2"),
		Dependency: []string{"buf/validate/validate.proto", "flowseer/net/key/v1/key.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("KeyCarrier"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:    proto.String("candidate"),
				Number:  proto.Int32(1),
				Label:   descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:    descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				Options: opts,
			}},
		}},
	}

	file, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatalf("build the string-rule carrier: %v", err)
	}

	message := file.Messages().ByName("KeyCarrier")
	field := message.Fields().ByName("candidate")

	return func(value string) proto.Message {
		msg := dynamicpb.NewMessage(message)
		msg.Set(field, protoreflect.ValueOfString(value))
		return msg
	}
}
