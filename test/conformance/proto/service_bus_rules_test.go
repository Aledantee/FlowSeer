package conformance

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
)

func TestServiceBusControlRecordWireContracts(t *testing.T) {
	tests := []struct {
		message protoreflect.MessageDescriptor
		name    protoreflect.FullName
		fields  map[protoreflect.Name]protoreflect.FieldNumber
	}{
		{
			message: (&servicev1.RuntimeManifest{}).ProtoReflect().Descriptor(),
			name:    "flowseer.service.v1.RuntimeManifest",
			fields: map[protoreflect.Name]protoreflect.FieldNumber{
				"service_namespace": 1,
				"service_name":      2,
				"domain":            3,
				"envelope_type":     4,
				"envelope_version":  5,
				"subject_version":   6,
				"nats_version":      7,
				"modules":           8,
			},
		},
		{
			message: (&servicev1.ReconciliationRecord{}).ProtoReflect().Descriptor(),
			name:    "flowseer.service.v1.ReconciliationRecord",
			fields: map[protoreflect.Name]protoreflect.FieldNumber{
				"previous":         1,
				"desired":          2,
				"desired_checksum": 3,
				"phase":            4,
			},
		},
		{
			message: (&servicev1.Settlement{}).ProtoReflect().Descriptor(),
			name:    "flowseer.service.v1.Settlement",
			fields: map[protoreflect.Name]protoreflect.FieldNumber{
				"target_path":    1,
				"message_id":     2,
				"retry_count":    3,
				"disposition_id": 4,
				"state":          5,
			},
		},
		{
			message: (&servicev1.StoreProvenance{}).ProtoReflect().Descriptor(),
			name:    "flowseer.service.v1.StoreProvenance",
			fields: map[protoreflect.Name]protoreflect.FieldNumber{
				"format_version":    1,
				"nats_version":      2,
				"service_namespace": 3,
				"service_name":      4,
				"domain":            5,
			},
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.name), func(t *testing.T) {
			if got := tt.message.FullName(); got != tt.name {
				t.Errorf("message full name = %q, want %q", got, tt.name)
			}
			for name, want := range tt.fields {
				field := tt.message.Fields().ByName(name)
				if field == nil {
					t.Errorf("field %q is missing", name)
					continue
				}
				if got := field.Number(); got != want {
					t.Errorf("field %q number = %d, want %d", name, got, want)
				}
			}
		})
	}
}
