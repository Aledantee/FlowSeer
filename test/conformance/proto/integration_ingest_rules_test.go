package conformance

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/log/v1"
	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
)

func testProvenance() *inventoryv1.Provenance {
	return inventoryv1.Provenance_builder{
		Binding:    bindingRef(),
		ObservedAt: timestamppb.Now(),
		Log:        inventoryv1.LogProtocol_LOG_PROTOCOL_SYSLOG.Enum(),
	}.Build()
}

func testSyslogRecord() *eventlogv1.SyslogRecord {
	return eventlogv1.SyslogRecord_builder{
		Device:     deviceRef(deviceID),
		ReceivedAt: timestamppb.Now(),
	}.Build()
}

func baseIngestRecord() ingestv1.IngestRecord_builder {
	return ingestv1.IngestRecord_builder{
		RecordId:   proto.String("0192e6a0-0000-7000-8000-000000000001"),
		Provenance: testProvenance(),
		Syslog:     testSyslogRecord(),
	}
}

func TestIngestRecordRules(t *testing.T) {
	data65535 := bytes.Repeat([]byte("a"), 65535)
	data65536 := bytes.Repeat([]byte("a"), 65536)
	unspecifiedReason := ingestv1.RawReason_RAW_REASON_UNSPECIFIED
	undefinedReason := ingestv1.RawReason(99)

	runFieldCases(t, []fieldCase{
		{
			name:    "valid envelope passes",
			message: baseIngestRecord().Build(),
		},
		{
			name: "missing record_id fails",
			message: func() *ingestv1.IngestRecord {
				b := baseIngestRecord()
				b.RecordId = nil
				return b.Build()
			}(),
			wantField: "record_id",
			wantText:  "value is required",
		},
		{
			name: "non-uuid record_id fails",
			message: func() *ingestv1.IngestRecord {
				b := baseIngestRecord()
				b.RecordId = proto.String("not-a-uuid")
				return b.Build()
			}(),
			wantField: "record_id",
			wantText:  "must be a valid UUID",
		},
		{
			name: "missing provenance fails",
			message: func() *ingestv1.IngestRecord {
				b := baseIngestRecord()
				b.Provenance = nil
				return b.Build()
			}(),
			wantField: "provenance",
			wantText:  "value is required",
		},
		{
			name: "missing payload oneof fails",
			message: func() *ingestv1.IngestRecord {
				b := baseIngestRecord()
				b.Syslog = nil
				return b.Build()
			}(),
			wantField: "payload",
			wantText:  "exactly one field is required",
		},
		{
			name: "raw evidence data of exactly 65535 octets passes",
			message: func() *ingestv1.IngestRecord {
				b := baseIngestRecord()
				b.Raw = ingestv1.RawEvidence_builder{
					Data:   data65535,
					Reason: ingestv1.RawReason_RAW_REASON_PARSE_FAILURE.Enum(),
				}.Build()
				return b.Build()
			}(),
		},
		{
			name: "raw evidence data of 65536 octets fails",
			message: func() *ingestv1.IngestRecord {
				b := baseIngestRecord()
				b.Raw = ingestv1.RawEvidence_builder{
					Data:   data65536,
					Reason: ingestv1.RawReason_RAW_REASON_PARSE_FAILURE.Enum(),
				}.Build()
				return b.Build()
			}(),
			wantField: "raw.data",
			wantText:  "65535",
		},
		{
			name: "raw evidence with no reason fails",
			message: func() *ingestv1.IngestRecord {
				b := baseIngestRecord()
				b.Raw = ingestv1.RawEvidence_builder{
					Data: []byte("test"),
				}.Build()
				return b.Build()
			}(),
			wantField: "raw.reason",
			wantText:  "value is required",
		},
		{
			name: "raw evidence with empty datagram zero bytes passes",
			message: func() *ingestv1.IngestRecord {
				b := baseIngestRecord()
				b.Raw = ingestv1.RawEvidence_builder{
					Data:                []byte{},
					Reason:              ingestv1.RawReason_RAW_REASON_PARSE_FAILURE.Enum(),
					SuppressedSinceLast: proto.Uint64(5),
				}.Build()
				return b.Build()
			}(),
		},
		{
			name: "raw evidence with unspecified reason fails",
			message: func() *ingestv1.IngestRecord {
				b := baseIngestRecord()
				b.Raw = ingestv1.RawEvidence_builder{
					Reason: &unspecifiedReason,
				}.Build()
				return b.Build()
			}(),
			wantField: "raw.reason",
			wantText:  "must not be in list",
		},
		{
			name: "raw evidence with undefined reason fails",
			message: func() *ingestv1.IngestRecord {
				b := baseIngestRecord()
				b.Raw = ingestv1.RawEvidence_builder{
					Reason: &undefinedReason,
				}.Build()
				return b.Build()
			}(),
			wantField: "raw.reason",
			wantText:  "must be one of the defined enum values",
		},
		{
			name: "raw evidence with RAW_REASON_WINDOW passes",
			message: func() *ingestv1.IngestRecord {
				b := baseIngestRecord()
				b.Raw = ingestv1.RawEvidence_builder{
					Data:   []byte("test"),
					Reason: ingestv1.RawReason_RAW_REASON_WINDOW.Enum(),
				}.Build()
				return b.Build()
			}(),
		},
		{
			name:    "syslog record holding no id with only device and received_at passes",
			message: testSyslogRecord(),
		},
		{
			name: "envelope wrapping minimal syslog record fails only on missing record_id",
			message: func() *ingestv1.IngestRecord {
				b := baseIngestRecord()
				b.RecordId = nil
				b.Syslog = testSyslogRecord()
				return b.Build()
			}(),
			wantField: "record_id",
			wantText:  "value is required",
		},
	})
}
