package historysink

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/log/v1"
	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	logv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/log/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

const (
	testTenant   = "018f0000-0000-7000-8000-0000000000a1"
	testDevice   = "018f0000-0000-7000-8000-0000000000d1"
	testRecordID = "018f0000-0000-7000-8000-000000000101"
	testLocation = "018f0000-0000-7000-8000-0000000000e1"
	testBinding  = "018f0000-0000-7000-8000-0000000000b1"
)

// syslogEnvelope builds an ingest record around the syslog payload.
func syslogEnvelope(syslog *eventlogv1.SyslogRecord) *ingestv1.IngestRecord {
	return ingestv1.IngestRecord_builder{
		RecordId: proto.String(testRecordID),
		Provenance: inventoryv1.Provenance_builder{
			Binding: inventoryv1.BindingGlobalRef_builder{
				Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(testBinding)}.Build(),
			}.Build(),
			ObservedAt: timestamppb.New(time.Date(2026, 10, 2, 10, 0, 1, 0, time.UTC)),
			Log:        inventoryv1.LogProtocol_LOG_PROTOCOL_SYSLOG.Enum(),
		}.Build(),
		Syslog: syslog,
	}.Build()
}

func baseSyslog() eventlogv1.SyslogRecord_builder {
	return eventlogv1.SyslogRecord_builder{
		Device: inventoryv1.DeviceGlobalRef_builder{
			Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(testDevice)}.Build(),
		}.Build(),
		ReceivedAt: timestamppb.New(time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)),
		Message:    []byte("link down"),
	}
}

func TestSeverityEmergencyIsZeroWithItsPresenceBitSet(t *testing.T) {
	b := baseSyslog()
	b.Severity = logv1.SyslogSeverity_SYSLOG_SEVERITY_EMERGENCY.Enum()
	b.Facility = logv1.SyslogFacility_SYSLOG_FACILITY_LOCAL7.Enum()

	row, err := mapRecord(testTenant, syslogEnvelope(b.Build()), "", 2)
	if err != nil {
		t.Fatalf("map: %v", err)
	}

	if row.Severity != 0 || row.Present&PresentSeverity == 0 {
		t.Errorf("severity %d with present %04b, want 0 with the severity bit set", row.Severity, row.Present)
	}
	if row.Facility != 23 || row.Present&PresentFacility == 0 {
		t.Errorf("facility %d with present %04b, want 23 with the facility bit set", row.Facility, row.Present)
	}
}

func TestAMessageWithoutAPriorityClearsTheSeverityAndFacilityBits(t *testing.T) {
	row, err := mapRecord(testTenant, syslogEnvelope(baseSyslog().Build()), "", 2)
	if err != nil {
		t.Fatalf("map: %v", err)
	}

	if row.Present&(PresentSeverity|PresentFacility) != 0 {
		t.Errorf("present = %04b, want the severity and facility bits cleared", row.Present)
	}
	if row.Severity != 0 || row.Facility != 0 {
		t.Errorf("severity %d and facility %d, want 0 and 0 when absent", row.Severity, row.Facility)
	}
}

func TestAMessageThatIsNotUTF8KeepsItsBytes(t *testing.T) {
	want := []byte{0x66, 0xff, 0xfe, 0x00, 0xc3, 0x28, 0x6f}
	b := baseSyslog()
	b.Message = want

	row, err := mapRecord(testTenant, syslogEnvelope(b.Build()), "", 2)
	if err != nil {
		t.Fatalf("map: %v", err)
	}

	if got := []byte(row.Message); string(got) != string(want) {
		t.Errorf("message = % x, want % x", got, want)
	}
}

func TestMapRecordCarriesTheStampedAndOptionalFields(t *testing.T) {
	b := baseSyslog()
	b.SentAt = timestamppb.New(time.Date(2026, 10, 2, 9, 59, 59, 0, time.UTC))
	b.Hostname = proto.String("sw1")
	b.AppName = proto.String("link")
	b.ProcId = proto.String("42")
	b.MsgId = proto.String("ID47")
	b.MessageTruncated = proto.Bool(true)
	b.SourceAddress = addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{10, 1, 2, 3}}.Build()}.Build()
	b.StructuredData = []*eventlogv1.SyslogStructuredDataElement{
		eventlogv1.SyslogStructuredDataElement_builder{Id: proto.String("origin"), Params: []*eventlogv1.SyslogStructuredDataParam{
			eventlogv1.SyslogStructuredDataParam_builder{Name: proto.String("ip"), Value: proto.String("10.0.0.1")}.Build(),
			eventlogv1.SyslogStructuredDataParam_builder{Name: proto.String("sw"), Value: proto.String("")}.Build(),
		}}.Build(),
		eventlogv1.SyslogStructuredDataElement_builder{Id: proto.String("empty")}.Build(),
	}

	row, err := mapRecord(testTenant, syslogEnvelope(b.Build()), testLocation, 3)
	if err != nil {
		t.Fatalf("map: %v", err)
	}

	if row.TenantID != testTenant || row.DeviceID.String() != testDevice || row.RecordID.String() != testRecordID {
		t.Errorf("identity = %s %s %s", row.TenantID, row.DeviceID, row.RecordID)
	}
	if row.LocationID.String() != testLocation || row.RetentionClass != 3 {
		t.Errorf("stamps: location %s, class %d, want %s and 3", row.LocationID, row.RetentionClass, testLocation)
	}
	if want := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC); !row.ReceivedAt.Equal(want) {
		t.Errorf("received = %v, want %v", row.ReceivedAt, want)
	}
	if want := time.Date(2026, 10, 2, 10, 0, 1, 0, time.UTC); !row.ObservedAt.Equal(want) {
		t.Errorf("observed = %v, want %v", row.ObservedAt, want)
	}
	if want := time.Date(2026, 10, 2, 9, 59, 59, 0, time.UTC); !row.SentAt.Equal(want) || row.Present&PresentSentAt == 0 {
		t.Errorf("sent_at = %v with present %04b, want %v with its bit set", row.SentAt, row.Present, want)
	}
	if got := netip.AddrFrom16(row.SourceIP); got != netip.MustParseAddr("::ffff:10.1.2.3") || row.Present&PresentSourceIP == 0 {
		t.Errorf("source ip = %v with present %04b, want the IPv4-mapped 10.1.2.3", got, row.Present)
	}
	if row.Hostname != "sw1" || row.AppName != "link" || row.ProcID != "42" || row.MsgID != "ID47" || !row.MessageTruncated {
		t.Errorf("header fields = %+v", row)
	}
	if got := row.SDIDs; len(got) != 2 || got[0] != "origin" || got[1] != "empty" {
		t.Errorf("sd_ids = %q, want [origin empty]", got)
	}
	if got := row.SDParamSDID; len(got) != 2 || got[0] != "origin" || got[1] != "origin" {
		t.Errorf("sd_param_sd_id = %q, want [origin origin]", got)
	}
	if got := row.SDParamName; len(got) != 2 || got[0] != "ip" || got[1] != "sw" {
		t.Errorf("sd_param_name = %q, want [ip sw]", got)
	}
	if got := row.SDParamValue; len(got) != 2 || got[0] != "10.0.0.1" || got[1] != "" {
		t.Errorf("sd_param_value = %q, want [10.0.0.1 and an empty value]", got)
	}
}

func TestAnAbsentSentAtLeavesTheEpochAndItsBitCleared(t *testing.T) {
	row, err := mapRecord(testTenant, syslogEnvelope(baseSyslog().Build()), "", 2)
	if err != nil {
		t.Fatalf("map: %v", err)
	}

	if row.Present&(PresentSentAt|PresentSourceIP) != 0 {
		t.Errorf("present = %04b, want the sent_at and source_ip bits cleared", row.Present)
	}
	if !row.SentAt.Equal(time.Unix(0, 0)) {
		t.Errorf("sent_at = %v, want the epoch, which a DateTime64 column can hold", row.SentAt)
	}
}

func TestMapRecordRefusesWhatCannotBecomeARow(t *testing.T) {
	tests := []struct {
		name   string
		tenant string
		record func() *ingestv1.IngestRecord
		want   string
	}{
		{"no syslog payload", testTenant, func() *ingestv1.IngestRecord {
			return ingestv1.IngestRecord_builder{RecordId: proto.String(testRecordID)}.Build()
		}, "no syslog payload"},
		{"tenant that is not a UUID", "a1", func() *ingestv1.IngestRecord { return syslogEnvelope(baseSyslog().Build()) }, "invalid tenant"},
		{"device that is not a UUID", testTenant, func() *ingestv1.IngestRecord {
			b := baseSyslog()
			b.Device = inventoryv1.DeviceGlobalRef_builder{Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String("d1")}.Build()}.Build()
			return syslogEnvelope(b.Build())
		}, "invalid device id"},
		{"record id that is not a UUID", testTenant, func() *ingestv1.IngestRecord {
			r := syslogEnvelope(baseSyslog().Build())
			r.SetRecordId("r1")
			return r
		}, "invalid record id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := mapRecord(tt.tenant, tt.record(), "", 2)

			if code, _ := errs.CodeOf(err); code != ErrCodeRecord {
				t.Errorf("code = %v (err %v), want %v", code, err, ErrCodeRecord)
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %v does not name %q", err, tt.want)
			}
		})
	}
}

func TestBatchTokensDifferForSameEndsAndOtherMembersAndIgnoreOrder(t *testing.T) {
	first := batchToken([]uint64{10, 11, 14})
	other := batchToken([]uint64{10, 12, 14})
	reordered := batchToken([]uint64{14, 10, 11})

	if first == other {
		t.Errorf("two batches with the ends 10 and 14 and different members share the token %s", first)
	}
	if first != reordered {
		t.Errorf("the same members in another order gave %s, want %s", reordered, first)
	}
}
