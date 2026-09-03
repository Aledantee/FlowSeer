package service_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/timestamppb"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
)

var updateMessageV1 = flag.Bool("update-message-v1", false, "rewrite testdata/message_v1.bin after an intentional persistence migration")

func TestMessageV1Compatibility(t *testing.T) {
	path := filepath.Join("testdata", "message_v1.bin")
	want := messageV1Fixture(t)
	if *updateMessageV1 {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating fixture directory: %v", err)
		}
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatalf("writing v1 message fixture: %v", err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading v1 message fixture (run with -update-message-v1 after auditing the schema): %v", err)
	}
	if !bytes.Equal(data, want) {
		t.Fatal("v1 message fixture changed; persisted field or enum numbers require an explicit migration")
	}

	message := &servicev1.Message{}
	if err := proto.Unmarshal(data, message); err != nil {
		t.Fatalf("decoding v1 message fixture: %v", err)
	}
	if err := protovalidate.Validate(message); err != nil {
		t.Fatalf("validating v1 message fixture: %v", err)
	}
	if got, want := message.ProtoReflect().Descriptor().FullName(), protoreflect.FullName("flowseer.service.v1.Message"); got != want {
		t.Fatalf("message full name = %q, want %q", got, want)
	}

	payloadType, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(message.GetTypeName()))
	if err != nil {
		t.Fatalf("resolving payload type %q: %v", message.GetTypeName(), err)
	}
	payload := payloadType.New().Interface()
	if err := proto.Unmarshal(message.GetPayload(), payload); err != nil {
		t.Fatalf("decoding payload %q: %v", message.GetTypeName(), err)
	}
	timestamp, ok := payload.(*timestamppb.Timestamp)
	if !ok {
		t.Fatalf("resolved payload type = %T, want *timestamppb.Timestamp", payload)
	}
	if got, want := timestamp.GetSeconds(), int64(1_788_393_600); got != want {
		t.Errorf("payload seconds = %d, want %d", got, want)
	}
	if len(timestamp.ProtoReflect().GetUnknown()) == 0 {
		t.Error("payload fixture has no unknown fields")
	}

	roundTrip, err := proto.Marshal(message)
	if err != nil {
		t.Fatalf("encoding v1 message fixture: %v", err)
	}
	decoded := &servicev1.Message{}
	if err := proto.Unmarshal(roundTrip, decoded); err != nil {
		t.Fatalf("decoding round-tripped v1 message: %v", err)
	}
	if !bytes.Equal(decoded.ProtoReflect().GetUnknown(), message.ProtoReflect().GetUnknown()) {
		t.Error("envelope unknown fields changed across binary round-trip")
	}

	payloadRoundTrip, err := proto.Marshal(payload)
	if err != nil {
		t.Fatalf("encoding v1 payload: %v", err)
	}
	decodedPayload := payloadType.New().Interface()
	if err := proto.Unmarshal(payloadRoundTrip, decodedPayload); err != nil {
		t.Fatalf("decoding round-tripped v1 payload: %v", err)
	}
	if !bytes.Equal(decodedPayload.ProtoReflect().GetUnknown(), payload.ProtoReflect().GetUnknown()) {
		t.Error("payload unknown fields changed across binary round-trip")
	}
}

func messageV1Fixture(t *testing.T) []byte {
	t.Helper()

	payload := timestamppb.New(time.Date(2026, time.September, 3, 0, 0, 0, 0, time.UTC))
	payload.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x07})
	payloadBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	if err != nil {
		t.Fatalf("encoding fixture payload: %v", err)
	}

	message := servicev1.Message_builder{
		Kind:          servicev1.MessageKind_MESSAGE_KIND_REPLY.Enum(),
		MessageId:     proto.String("aa36b80e-88b5-4e2b-9ff6-6412d106cf80"),
		CorrelationId: proto.String("5a9434af-d74f-4183-ba88-30fda520d2ee"),
		CausationId:   proto.String("3c9c2efd-44d6-4496-aefd-e2b7c17cd8e5"),
		SourcePath:    proto.String("edge/ingest/syslog"),
		TargetPath:    proto.String("edge/uplink"),
		TypeName:      proto.String("google.protobuf.Timestamp"),
		Payload:       payloadBytes,
		Traceparent:   proto.String("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"),
		Tracestate:    proto.String("vendor=value"),
	}.Build()
	message.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x07})

	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		t.Fatalf("encoding v1 message fixture: %v", err)
	}
	return data
}
