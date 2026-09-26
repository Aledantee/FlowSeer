package conformance

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/durationpb"

	aaav1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/aaa/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
)

// testNet1 returns 192.0.2.host, an address in the RFC 5737 documentation
// block.
func testNet1(host byte) *addrv1.IpAddress {
	return addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{192, 0, 2, host}}.Build()}.Build()
}

// TestAaaServerRules holds a server to an address and exactly one protocol,
// and its ports, retransmit count, and timeout to their ranges.
func TestAaaServerRules(t *testing.T) {
	radius := func(r *aaav1.RadiusServer_builder) *aaav1.AaaServer_builder {
		return &aaav1.AaaServer_builder{Address: testNet1(10), Radius: r.Build()}
	}
	withoutAddress := radius(&aaav1.RadiusServer_builder{AuthPort: proto.Uint32(1812)})
	withoutAddress.Address = nil
	negativeTimeout := radius(&aaav1.RadiusServer_builder{AuthPort: proto.Uint32(1812)})
	negativeTimeout.Timeout = durationpb.New(-1e9)

	runFieldCases(t, []fieldCase{
		{name: "RADIUS server", message: radius(&aaav1.RadiusServer_builder{AuthPort: proto.Uint32(1812), AcctPort: proto.Uint32(1813)}).Build()},
		{name: "TACACS+ server", message: aaav1.AaaServer_builder{Address: testNet1(11), Tacacs: aaav1.TacacsServer_builder{Port: proto.Uint32(49)}.Build()}.Build()},
		{name: "address absent", message: withoutAddress.Build(), wantField: "address", wantText: "value is required"},
		{name: "no protocol", message: aaav1.AaaServer_builder{Address: testNet1(10)}.Build(), wantField: "protocol", wantText: "exactly one field is required"},
		{name: "TACACS+ port above 65535", message: aaav1.AaaServer_builder{Address: testNet1(11), Tacacs: aaav1.TacacsServer_builder{Port: proto.Uint32(65536)}.Build()}.Build(), wantField: "tacacs.port", wantText: "less than or equal to 65535"},
		{name: "retransmits above 255", message: radius(&aaav1.RadiusServer_builder{RetransmitAttempts: proto.Uint32(256)}).Build(), wantField: "radius.retransmit_attempts", wantText: "less than or equal to 255"},
		{name: "negative timeout", message: negativeTimeout.Build(), wantField: "timeout", wantText: "greater than or equal to"},
	})
}

// secretBearingFields returns the fields of the files' messages whose names
// suggest credential material.
func secretBearingFields(files ...protoreflect.FileDescriptor) []string {
	var found []string
	var walk func(protoreflect.MessageDescriptors)
	walk = func(messages protoreflect.MessageDescriptors) {
		for i := range messages.Len() {
			message := messages.Get(i)
			fields := message.Fields()
			for j := range fields.Len() {
				name := string(fields.Get(j).Name())
				for _, word := range []string{"secret", "key", "password", "passphrase"} {
					if strings.Contains(name, word) {
						found = append(found, string(fields.Get(j).FullName()))
						break
					}
				}
			}
			walk(message.Messages())
		}
	}
	for _, file := range files {
		walk(file.Messages())
	}
	return found
}

// TestAaaCarriesNoSecret holds net/aaa to server identity: a shared secret is
// credential material and has no field in the package.
func TestAaaCarriesNoSecret(t *testing.T) {
	var files []protoreflect.FileDescriptor
	protoregistry.GlobalFiles.RangeFilesByPackage(aaav1.File_flowseer_net_aaa_v1_aaa_server_proto.Package(), func(file protoreflect.FileDescriptor) bool {
		files = append(files, file)
		return true
	})
	if len(files) == 0 {
		t.Fatal("no net/aaa files are registered")
	}
	if found := secretBearingFields(files...); len(found) > 0 {
		t.Errorf("net/aaa fields carry credential material: %v", found)
	}

	t.Run("synthetic", func(t *testing.T) {
		file := buildSyntheticFile(t, "aaa_secret_breaks.proto", nil, &descriptorpb.DescriptorProto{
			Name: proto.String("Server"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:   proto.String("secret_key"),
				Number: proto.Int32(1),
				Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			}},
		})
		want := "flowseer.conformance.synthetic.v1.Server.secret_key"
		if found := secretBearingFields(file); len(found) != 1 || found[0] != want {
			t.Errorf("got %v, want [%s]", found, want)
		}
	})
}
