package service

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestStaticRegistryRejectsSameNameWithDifferentSchema(t *testing.T) {
	first := dynamicMessageType(t, descriptorpb.FieldDescriptorProto_TYPE_STRING)
	second := dynamicMessageType(t, descriptorpb.FieldDescriptorProto_TYPE_BYTES)
	_, err := validateDeclaration(Config{
		Identity: testIdentity(),
		Modules: []Module{
			{Name: "first", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: messageKindEvent, Message: first.New().Interface()}}}},
			{Name: "second", Leaf: &Leaf{Setup: testSetup(), Subscriptions: []Subscription{{Kind: messageKindEvent, Message: second.New().Interface()}}}},
		},
	})
	if err == nil {
		t.Fatal("different schemas sharing a protobuf full name were accepted")
	}
}

func TestAttemptHandlerRejectsSameNameWithDifferentSchema(t *testing.T) {
	declared := dynamicMessageType(t, descriptorpb.FieldDescriptorProto_TYPE_STRING)
	handler := dynamicMessageType(t, descriptorpb.FieldDescriptorProto_TYPE_BYTES)
	declaration, err := validateDeclaration(Config{
		Identity: testIdentity(),
		Modules: []Module{{Name: "worker", Leaf: &Leaf{
			Setup:         testSetup(),
			Subscriptions: []Subscription{{Kind: messageKindCommand, Message: declared.New().Interface()}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = validateAttemptHandlers(declaration.modules[0].path, declaration.modules[0].leaf.subscriptions, []Handler{{
		Kind:    messageKindCommand,
		Message: handler.New().Interface(),
		Handle:  func(context.Context, proto.Message) error { return nil },
	}})
	if err == nil {
		t.Fatal("handler with a different schema sharing the declared full name was accepted")
	}
}

func dynamicMessageType(t *testing.T, fieldType descriptorpb.FieldDescriptorProto_Type) protoreflect.MessageType {
	t.Helper()
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    proto.String("schema.proto"),
		Package: proto.String("flowseer.test"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Payload"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:   proto.String("value"),
				Number: proto.Int32(1),
				Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:   fieldType.Enum(),
			}},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return dynamicpb.NewMessageType(file.Messages().ByName("Payload"))
}
