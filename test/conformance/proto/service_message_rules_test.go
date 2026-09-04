package conformance

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
)

func TestServiceMessageValidation(t *testing.T) {
	valid := func() servicev1.Message_builder {
		return servicev1.Message_builder{
			Kind:          servicev1.MessageKind_MESSAGE_KIND_COMMAND.Enum(),
			MessageId:     proto.String("aa36b80e-88b5-4e2b-9ff6-6412d106cf80"),
			CorrelationId: proto.String("5a9434af-d74f-4183-ba88-30fda520d2ee"),
			CausationId:   proto.String("3c9c2efd-44d6-4496-aefd-e2b7c17cd8e5"),
			SourcePath:    proto.String("edge/ingest/syslog"),
			TargetPath:    proto.String("edge/uplink"),
			TypeName:      proto.String("google.protobuf.Timestamp"),
			Payload:       []byte{0x08, 0x01},
			Traceparent:   proto.String("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"),
			Tracestate:    proto.String("vendor=value"),
		}
	}

	cases := []validationCase{
		{
			name:      "a complete durable message is valid",
			message:   valid().Build(),
			wantValid: true,
		},
		{
			name: "optional flow and trace identifiers may be absent",
			message: func() *servicev1.Message {
				message := valid()
				message.CorrelationId = nil
				message.CausationId = nil
				message.Traceparent = nil
				message.Tracestate = nil
				return message.Build()
			}(),
			wantValid: true,
		},
		{
			name: "an empty encoded protobuf payload is valid",
			message: func() *servicev1.Message {
				message := valid()
				message.Payload = []byte{}
				return message.Build()
			}(),
			wantValid: true,
		},
		{
			name: "a missing message id is rejected",
			message: func() *servicev1.Message {
				message := valid()
				message.MessageId = nil
				return message.Build()
			}(),
		},
		{
			name: "a missing target path is rejected",
			message: func() *servicev1.Message {
				message := valid()
				message.TargetPath = nil
				return message.Build()
			}(),
		},
		{
			name: "a missing source path is rejected",
			message: func() *servicev1.Message {
				message := valid()
				message.SourcePath = nil
				return message.Build()
			}(),
		},
		{
			name: "a missing payload type is rejected",
			message: func() *servicev1.Message {
				message := valid()
				message.TypeName = nil
				return message.Build()
			}(),
		},
		{
			name: "missing payload bytes are rejected",
			message: func() *servicev1.Message {
				message := valid()
				message.Payload = nil
				return message.Build()
			}(),
		},
		{
			name: "an unknown envelope kind is rejected",
			message: func() *servicev1.Message {
				message := valid()
				message.Kind = servicev1.MessageKind(99).Enum()
				return message.Build()
			}(),
		},
		{
			name: "a malformed message id is rejected",
			message: func() *servicev1.Message {
				message := valid()
				message.MessageId = proto.String("not-a-uuid")
				return message.Build()
			}(),
		},
		{
			name: "a malformed correlation id is rejected",
			message: func() *servicev1.Message {
				message := valid()
				message.CorrelationId = proto.String("not-a-uuid")
				return message.Build()
			}(),
		},
		{
			name: "a malformed causation id is rejected",
			message: func() *servicev1.Message {
				message := valid()
				message.CausationId = proto.String("not-a-uuid")
				return message.Build()
			}(),
		},
		{
			name: "a malformed source path is rejected",
			message: func() *servicev1.Message {
				message := valid()
				message.SourcePath = proto.String("edge/BadModule")
				return message.Build()
			}(),
		},
		{
			name: "a malformed target path is rejected",
			message: func() *servicev1.Message {
				message := valid()
				message.TargetPath = proto.String("edge//uplink")
				return message.Build()
			}(),
		},
		{
			name: "a malformed payload type name is rejected",
			message: func() *servicev1.Message {
				message := valid()
				message.TypeName = proto.String("Timestamp")
				return message.Build()
			}(),
		},
		{
			name: "a malformed traceparent is rejected",
			message: func() *servicev1.Message {
				message := valid()
				message.Traceparent = proto.String("4bf92f3577b34da6a3ce929d0e0e4736")
				return message.Build()
			}(),
		},
	}

	runValidationCases(t, cases)
}

func TestServiceMessageWireContract(t *testing.T) {
	descriptor := (&servicev1.Message{}).ProtoReflect().Descriptor()
	fields := map[string]protoreflect.FieldNumber{
		"kind":           1,
		"message_id":     2,
		"correlation_id": 3,
		"causation_id":   4,
		"source_path":    5,
		"target_path":    6,
		"type_name":      7,
		"payload":        8,
		"traceparent":    9,
		"tracestate":     10,
	}

	if got, want := descriptor.FullName(), protoreflect.FullName("flowseer.service.v1.Message"); got != want {
		t.Errorf("message full name = %q, want %q", got, want)
	}
	for name, want := range fields {
		field := descriptor.Fields().ByName(protoreflect.Name(name))
		if field == nil {
			t.Errorf("field %q is missing", name)
			continue
		}
		if got := field.Number(); got != want {
			t.Errorf("field %q number = %d, want %d", name, got, want)
		}
	}

	enum := servicev1.MessageKind_MESSAGE_KIND_UNSPECIFIED.Descriptor()
	values := map[string]protoreflect.EnumNumber{
		"MESSAGE_KIND_UNSPECIFIED": 0,
		"MESSAGE_KIND_COMMAND":     1,
		"MESSAGE_KIND_EVENT":       2,
		"MESSAGE_KIND_REPLY":       3,
	}
	for name, want := range values {
		value := enum.Values().ByName(protoreflect.Name(name))
		if value == nil {
			t.Errorf("message kind %q is missing", name)
			continue
		}
		if got := value.Number(); got != want {
			t.Errorf("message kind %q number = %d, want %d", name, got, want)
		}
	}
}
