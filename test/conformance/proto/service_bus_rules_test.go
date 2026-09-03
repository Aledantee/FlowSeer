package conformance

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
)

func TestServiceBusControlRecordValidation(t *testing.T) {
	manifest := validRuntimeManifest()
	cases := []validationCase{
		{name: "complete runtime manifest", message: manifest, wantValid: true},
		{name: "empty runtime manifest", message: &servicev1.RuntimeManifest{}},
		{
			name: "complete reconciliation record",
			message: servicev1.ReconciliationRecord_builder{
				Desired:         manifest,
				DesiredChecksum: make([]byte, 32),
				Phase:           servicev1.ReconciliationPhase_RECONCILIATION_PHASE_PREPARED.Enum(),
			}.Build(),
			wantValid: true,
		},
		{name: "empty reconciliation record", message: &servicev1.ReconciliationRecord{}},
		{
			name: "wrong reconciliation checksum size",
			message: servicev1.ReconciliationRecord_builder{
				Desired:         manifest,
				DesiredChecksum: []byte("short"),
				Phase:           servicev1.ReconciliationPhase_RECONCILIATION_PHASE_PREPARED.Enum(),
			}.Build(),
		},
		{
			name: "complete store provenance",
			message: servicev1.StoreProvenance_builder{
				FormatVersion:    proto.Uint32(1),
				NatsVersion:      proto.String("2.14.6"),
				ServiceNamespace: proto.String("flowseer"),
				ServiceName:      proto.String("edge"),
				Domain:           proto.String("v1_abc234"),
			}.Build(),
			wantValid: true,
		},
		{name: "empty store provenance", message: &servicev1.StoreProvenance{}},
		{
			name: "complete settlement",
			message: servicev1.Settlement_builder{
				TargetPath:    proto.String("edge/worker"),
				MessageId:     proto.String("aa36b80e-88b5-4e2b-9ff6-6412d106cf80"),
				RetryCount:    proto.Uint32(1),
				DispositionId: proto.String("5a9434af-d74f-4183-ba88-30fda520d2ee"),
				State:         servicev1.SettlementState_SETTLEMENT_STATE_RETRY.Enum(),
			}.Build(),
			wantValid: true,
		},
		{name: "empty settlement", message: &servicev1.Settlement{}},
	}

	runValidationCases(t, cases)
}

func validRuntimeManifest() *servicev1.RuntimeManifest {
	return servicev1.RuntimeManifest_builder{
		ServiceNamespace: proto.String("flowseer"),
		ServiceName:      proto.String("edge"),
		Domain:           proto.String("v1_abc234"),
		EnvelopeType:     proto.String("flowseer.service.v1.Message"),
		EnvelopeVersion:  proto.Uint32(1),
		SubjectVersion:   proto.Uint32(1),
		NatsVersion:      proto.String("2.14.6"),
		Modules: []*servicev1.ModuleContract{
			servicev1.ModuleContract_builder{
				Path:                proto.String("edge/worker"),
				PathToken:           proto.String("ZWRnZS93b3JrZXI"),
				DurableName:         proto.String("v1_abc234"),
				DeliveryConcurrency: proto.Uint32(1),
			}.Build(),
		},
	}.Build()
}

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
