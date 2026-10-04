package conformance

import (
	"bytes"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/log/v1"
	netlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/log/v1"
)

func TestSyslogRecordPresence(t *testing.T) {
	validRecord := eventlogv1.SyslogRecord_builder{
		Device:     deviceRef(deviceID),
		ReceivedAt: timestamppb.Now(),
		Severity:   netlogv1.SyslogSeverity_SYSLOG_SEVERITY_EMERGENCY.Enum(),
		Facility:   netlogv1.SyslogFacility_SYSLOG_FACILITY_KERN.Enum(),
	}.Build()

	if !validRecord.HasSeverity() {
		t.Errorf("expected HasSeverity() to be true for EMERGENCY (0)")
	}
	if !validRecord.HasFacility() {
		t.Errorf("expected HasFacility() to be true for KERN (0)")
	}

	baseRecord := func() eventlogv1.SyslogRecord_builder {
		return eventlogv1.SyslogRecord_builder{
			Device:     deviceRef(deviceID),
			ReceivedAt: timestamppb.Now(),
			Severity:   netlogv1.SyslogSeverity_SYSLOG_SEVERITY_EMERGENCY.Enum(),
			Facility:   netlogv1.SyslogFacility_SYSLOG_FACILITY_KERN.Enum(),
		}
	}

	runFieldCases(t, []fieldCase{
		{
			name:    "severity 0 EMERGENCY and facility 0 KERN pass",
			message: validRecord,
		},
		{
			name: "severity absent passes",
			message: func() *eventlogv1.SyslogRecord {
				b := baseRecord()
				b.Severity = nil
				return b.Build()
			}(),
		},
		{
			name: "facility absent passes",
			message: func() *eventlogv1.SyslogRecord {
				b := baseRecord()
				b.Facility = nil
				return b.Build()
			}(),
		},
		{
			name: "severity 8 fails defined_only",
			message: func() *eventlogv1.SyslogRecord {
				b := baseRecord()
				s := netlogv1.SyslogSeverity(8)
				b.Severity = &s
				return b.Build()
			}(),
			wantField: "severity",
			wantText:  "must be one of the defined enum values",
		},
		{
			name: "facility 24 fails defined_only",
			message: func() *eventlogv1.SyslogRecord {
				b := baseRecord()
				f := netlogv1.SyslogFacility(24)
				b.Facility = &f
				return b.Build()
			}(),
			wantField: "facility",
			wantText:  "must be one of the defined enum values",
		},
	})
}

func TestSyslogRecordRules(t *testing.T) {
	baseRecord := func() eventlogv1.SyslogRecord_builder {
		return eventlogv1.SyslogRecord_builder{
			Device:     deviceRef(deviceID),
			ReceivedAt: timestamppb.Now(),
			Severity:   netlogv1.SyslogSeverity_SYSLOG_SEVERITY_INFORMATIONAL.Enum(),
			Facility:   netlogv1.SyslogFacility_SYSLOG_FACILITY_LOCAL0.Enum(),
		}
	}

	recordWith := func(modify func(b *eventlogv1.SyslogRecord_builder)) *eventlogv1.SyslogRecord {
		b := baseRecord()
		modify(&b)
		return b.Build()
	}

	sd1 := eventlogv1.SyslogStructuredDataElement_builder{
		Id: proto.String("timeQuality"),
		Params: []*eventlogv1.SyslogStructuredDataParam{
			eventlogv1.SyslogStructuredDataParam_builder{
				Name:  proto.String("tzKnown"),
				Value: proto.String("1"),
			}.Build(),
		},
	}.Build()

	sd2 := eventlogv1.SyslogStructuredDataElement_builder{
		Id: proto.String("origin"),
		Params: []*eventlogv1.SyslogStructuredDataParam{
			eventlogv1.SyslogStructuredDataParam_builder{
				Name:  proto.String("software"),
				Value: proto.String("test"),
			}.Build(),
		},
	}.Build()

	emptyParamValueSD := eventlogv1.SyslogStructuredDataElement_builder{
		Id: proto.String("exampleSD"),
		Params: []*eventlogv1.SyslogStructuredDataParam{
			eventlogv1.SyslogStructuredDataParam_builder{
				Name:  proto.String("emptyParam"),
				Value: proto.String(""),
			}.Build(),
		},
	}.Build()

	msg65527 := bytes.Repeat([]byte("a"), 65527)
	msg65528 := bytes.Repeat([]byte("a"), 65528)

	runFieldCases(t, []fieldCase{
		{
			name: "valid record with structured data passes",
			message: recordWith(func(b *eventlogv1.SyslogRecord_builder) {
				b.StructuredData = []*eventlogv1.SyslogStructuredDataElement{sd1, sd2}
				b.Hostname = proto.String("switch-1")
				b.AppName = proto.String("sshd")
				b.ProcId = proto.String("1234")
				b.MsgId = proto.String("ID47")
				b.Message = []byte("Accepted publickey")
			}),
		},
		{
			name: "missing device fails",
			message: recordWith(func(b *eventlogv1.SyslogRecord_builder) {
				b.Device = nil
			}),
			wantField: "device",
			wantText:  "value is required",
		},
		{
			name: "missing received_at fails",
			message: recordWith(func(b *eventlogv1.SyslogRecord_builder) {
				b.ReceivedAt = nil
			}),
			wantField: "received_at",
			wantText:  "value is required",
		},
		{
			name: "app_name of 49 chars fails max_len 48",
			message: recordWith(func(b *eventlogv1.SyslogRecord_builder) {
				b.AppName = proto.String(strings.Repeat("a", 49))
			}),
			wantField: "app_name",
			wantText:  "must be at most 48 characters",
		},
		{
			name: "duplicate structured data IDs fail sd_ids_unique rule",
			message: recordWith(func(b *eventlogv1.SyslogRecord_builder) {
				b.StructuredData = []*eventlogv1.SyslogStructuredDataElement{sd1, sd1}
			}),
			wantRule: "syslog_record.sd_ids_unique",
		},
		{
			name: "SD-ID of 33 chars fails max_len 32",
			message: recordWith(func(b *eventlogv1.SyslogRecord_builder) {
				b.StructuredData = []*eventlogv1.SyslogStructuredDataElement{
					eventlogv1.SyslogStructuredDataElement_builder{
						Id: proto.String(strings.Repeat("s", 33)),
					}.Build(),
				}
			}),
			wantField: "structured_data[0].id",
			wantText:  "must be at most 32 characters",
		},
		{
			name: "empty parameter value passes",
			message: recordWith(func(b *eventlogv1.SyslogRecord_builder) {
				b.StructuredData = []*eventlogv1.SyslogStructuredDataElement{emptyParamValueSD}
			}),
		},
		{
			name: "message of 65527 bytes passes with message_truncated true",
			message: recordWith(func(b *eventlogv1.SyslogRecord_builder) {
				b.Message = msg65527
				b.MessageTruncated = proto.Bool(true)
			}),
		},
		{
			name: "message of 65528 bytes fails max_len",
			message: recordWith(func(b *eventlogv1.SyslogRecord_builder) {
				b.Message = msg65528
			}),
			wantField: "message",
			wantText:  "must be at most 65527 bytes",
		},
	})
}
