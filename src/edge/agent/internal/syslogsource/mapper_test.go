package syslogsource_test

import (
	"bytes"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	validate "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	eventlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/log/v1"
	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	netlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/log/v1"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/syslogsource"
	"go.aledante.io/FlowSeer/src/protocol/syslog"
)

const (
	testDeviceID  = "0192e6a0-0000-7000-8000-000000000001"
	testBindingID = "0192e6a0-0000-7000-8000-000000000002"
	testEdgeID    = "0192e6a0-0000-7000-8000-000000000003"
)

func testDeviceEntry() lanehost.DeviceEntry {
	devRef := inventoryv1.DeviceGlobalRef_builder{
		Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(testDeviceID)}.Build(),
	}.Build()
	bindRef := inventoryv1.BindingGlobalRef_builder{
		Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(testBindingID)}.Build(),
	}.Build()
	return lanehost.DeviceEntry{
		DeviceID: testDeviceID,
		Device:   devRef,
		Binding:  bindRef,
	}
}

func testEdgeRef() *edgev1.EdgeGlobalRef {
	return edgev1.EdgeGlobalRef_builder{
		Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdgeID)}.Build(),
	}.Build()
}

func TestMapper_PayloadCases(t *testing.T) {
	t.Parallel()

	parser := newSourceParser(t)

	receiveTime := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	obs := syslog.Observation{
		ReceivedAt: receiveTime,
		Peer:       netip.MustParseAddrPort("192.0.2.1:514"),
		Transport:  syslog.UDP,
	}

	cases := []struct {
		name             string
		payload          []byte
		checkSyslog      func(t *testing.T, rec *ingestv1.IngestRecord, isFailure bool)
		wantParseFailure bool
	}{
		{
			name:    "RFC 5424 complete datagram",
			payload: []byte("<34>1 2026-10-03T10:00:00Z sw1 app - - - link down"),
			checkSyslog: func(t *testing.T, env *ingestv1.IngestRecord, isFailure bool) {
				if isFailure {
					t.Errorf("isParseFailure = true, want false")
				}
				s := env.GetSyslog()
				if !s.HasSeverity() || s.GetSeverity() != netlogv1.SyslogSeverity_SYSLOG_SEVERITY_CRITICAL {
					t.Errorf("severity = %v, want CRITICAL", s.GetSeverity())
				}
				if !s.HasFacility() || s.GetFacility() != netlogv1.SyslogFacility_SYSLOG_FACILITY_AUTH {
					t.Errorf("facility = %v, want AUTH", s.GetFacility())
				}
				if s.GetHostname() != "sw1" {
					t.Errorf("hostname = %q, want sw1", s.GetHostname())
				}
				if s.GetAppName() != "app" {
					t.Errorf("app_name = %q, want app", s.GetAppName())
				}
				if string(s.GetMessage()) != "link down" {
					t.Errorf("message = %q, want 'link down'", string(s.GetMessage()))
				}
				if !s.HasSentAt() {
					t.Fatal("sent_at is unset, want set for RFC 5424 timestamp")
				}
				wantSentAt := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
				if !s.GetSentAt().AsTime().Equal(wantSentAt) {
					t.Errorf("sent_at = %v, want %v", s.GetSentAt().AsTime(), wantSentAt)
				}
				if env.GetRaw() != nil {
					t.Errorf("raw evidence = %v, want nil for complete message", env.GetRaw())
				}
			},
			wantParseFailure: false,
		},
		{
			name:    "RFC 5424 timestamp before year 0001",
			payload: []byte("<34>1 0000-01-01T00:00:00Z sw1 app - - - timestamp out of range"),
			checkSyslog: func(t *testing.T, env *ingestv1.IngestRecord, isFailure bool) {
				if !isFailure {
					t.Errorf("isParseFailure = false, want true")
				}
				if env.GetSyslog().HasSentAt() {
					t.Errorf("sent_at = %v, want unset on out of range timestamp", env.GetSyslog().GetSentAt())
				}
				if env.GetRaw() == nil {
					t.Fatal("raw evidence = nil, want present on parse failure")
				}
			},
			wantParseFailure: true,
		},
		{
			name:    "legacy line without PRI",
			payload: []byte("Oct  3 10:00:00 UTC sw1 app: link down"),
			checkSyslog: func(t *testing.T, env *ingestv1.IngestRecord, isFailure bool) {
				if !isFailure {
					t.Errorf("isParseFailure = false, want true")
				}
				s := env.GetSyslog()
				if s.HasSeverity() {
					t.Errorf("severity = %v, want unset", s.GetSeverity())
				}
				if s.HasFacility() {
					t.Errorf("facility = %v, want unset", s.GetFacility())
				}
				raw := env.GetRaw()
				if raw == nil {
					t.Fatal("raw evidence = nil, want present on parse failure")
				}
				if raw.GetReason() != ingestv1.RawReason_RAW_REASON_PARSE_FAILURE {
					t.Errorf("raw reason = %v, want RAW_REASON_PARSE_FAILURE", raw.GetReason())
				}
			},
			wantParseFailure: true,
		},
		{
			name:    "RFC 5424 with 256-character hostname",
			payload: []byte("<34>1 2026-10-03T10:00:00Z " + strings.Repeat("h", 256) + " app - - - link down"),
			checkSyslog: func(t *testing.T, env *ingestv1.IngestRecord, isFailure bool) {
				if !isFailure {
					t.Errorf("isParseFailure = false, want true")
				}
				s := env.GetSyslog()
				if s.HasHostname() {
					t.Errorf("hostname = %q, want unset when > 255 chars", s.GetHostname())
				}
				raw := env.GetRaw()
				if raw == nil {
					t.Fatal("raw evidence = nil, want present on parse failure")
				}
				if raw.GetReason() != ingestv1.RawReason_RAW_REASON_PARSE_FAILURE {
					t.Errorf("raw reason = %v, want RAW_REASON_PARSE_FAILURE", raw.GetReason())
				}
			},
			wantParseFailure: true,
		},
		{
			name:    "structured data value that is not UTF-8",
			payload: []byte("<34>1 2026-10-03T10:00:00Z sw1 app - - [ex@32473 k=\"\xff\"] link down"),
			checkSyslog: func(t *testing.T, env *ingestv1.IngestRecord, isFailure bool) {
				if !isFailure {
					t.Errorf("isParseFailure = false, want true")
				}
				if n := len(env.GetSyslog().GetStructuredData()); n != 0 {
					t.Errorf("structured data elements = %d, want 0 for a value the schema cannot hold", n)
				}
				if env.GetRaw() == nil {
					t.Error("raw evidence = nil, want present on parse failure")
				}
			},
			wantParseFailure: true,
		},
		{
			name:    "empty datagram",
			payload: []byte{},
			checkSyslog: func(t *testing.T, env *ingestv1.IngestRecord, isFailure bool) {
				if !isFailure {
					t.Errorf("isParseFailure = false, want true")
				}
				raw := env.GetRaw()
				if raw == nil {
					t.Fatal("raw evidence = nil, want present on empty datagram")
				}
				if len(raw.GetData()) != 0 {
					t.Errorf("raw data len = %d, want 0", len(raw.GetData()))
				}
				if raw.GetReason() != ingestv1.RawReason_RAW_REASON_PARSE_FAILURE {
					t.Errorf("raw reason = %v, want RAW_REASON_PARSE_FAILURE", raw.GetReason())
				}
			},
			wantParseFailure: true,
		},
		{
			name:    "duplicate structured data ID",
			payload: []byte("<34>1 2026-10-03T10:00:00Z sw1 app - - [exampleSDID@32473 i=\"1\"][exampleSDID@32473 j=\"2\"] duplicate sd"),
			checkSyslog: func(t *testing.T, env *ingestv1.IngestRecord, isFailure bool) {
				if !isFailure {
					t.Errorf("isParseFailure = false, want true")
				}
				if n := len(env.GetSyslog().GetStructuredData()); n != 1 {
					t.Errorf("structured data elements = %d, want 1", n)
				}
				if env.GetRaw() == nil {
					t.Fatal("raw evidence = nil, want present on parse failure")
				}
			},
			wantParseFailure: true,
		},
		{
			name:    "structured data parameter value over 1024 bytes",
			payload: []byte("<34>1 2026-10-03T10:00:00Z sw1 app - - [exampleSDID@32473 key=\"" + strings.Repeat("v", 1025) + "\"] long value"),
			checkSyslog: func(t *testing.T, env *ingestv1.IngestRecord, isFailure bool) {
				if !isFailure {
					t.Errorf("isParseFailure = false, want true")
				}
				if n := len(env.GetSyslog().GetStructuredData()); n != 0 {
					t.Errorf("structured data elements = %d, want 0", n)
				}
				if env.GetRaw() == nil {
					t.Fatal("raw evidence = nil, want present on parse failure")
				}
			},
			wantParseFailure: true,
		},
		{
			name:    "non-UTF-8 application header value",
			payload: []byte("<34>1 2026-10-03T10:00:00Z sw1 \xff\xfe - - - bad utf8"),
			checkSyslog: func(t *testing.T, env *ingestv1.IngestRecord, isFailure bool) {
				if !isFailure {
					t.Errorf("isParseFailure = false, want true")
				}
				if env.GetSyslog().HasAppName() {
					t.Errorf("app_name = %q, want unset", env.GetSyslog().GetAppName())
				}
				if env.GetRaw() == nil {
					t.Fatal("raw evidence = nil, want present on parse failure")
				}
			},
			wantParseFailure: true,
		},
		{
			name:    "65535-byte payload with no envelope",
			payload: bytes.Repeat([]byte("x"), 65535),
			checkSyslog: func(t *testing.T, env *ingestv1.IngestRecord, isFailure bool) {
				if !isFailure {
					t.Errorf("isParseFailure = false, want true")
				}
				s := env.GetSyslog()
				if got := len(s.GetMessage()); got != 65527 {
					t.Errorf("message length = %d, want 65527", got)
				}
				if !s.GetMessageTruncated() {
					t.Error("message_truncated = false, want true")
				}
				if got := len(env.GetRaw().GetData()); got != 65535 {
					t.Errorf("raw data length = %d, want 65535", got)
				}
			},
			wantParseFailure: true,
		},
		{
			name:    "legacy tag application of 50 bytes",
			payload: []byte("<34>Oct  3 10:00:00 " + strings.Repeat("a", 50) + ": legacy long tag"),
			checkSyslog: func(t *testing.T, env *ingestv1.IngestRecord, isFailure bool) {
				if !isFailure {
					t.Errorf("isParseFailure = false, want true")
				}
				if env.GetSyslog().HasAppName() {
					t.Errorf("app_name = %q, want unset", env.GetSyslog().GetAppName())
				}
				if env.GetRaw() == nil {
					t.Fatal("raw evidence = nil, want present on parse failure")
				}
			},
			wantParseFailure: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := parser.Parse(tc.payload, obs)
			if err != nil {
				t.Fatalf("parser.Parse: %v", err)
			}

			envelope, isFailure := buildValidEnvelope(t, rec)
			if isFailure != tc.wantParseFailure {
				t.Errorf("isParseFailure = %v, want %v", isFailure, tc.wantParseFailure)
			}

			p := envelope.GetProvenance()
			if p.GetBinding().GetBinding().GetId() != testBindingID {
				t.Errorf("provenance binding = %q, want %q", p.GetBinding().GetBinding().GetId(), testBindingID)
			}
			if p.GetEdge().GetEdge().GetId() != testEdgeID {
				t.Errorf("provenance edge = %q, want %q", p.GetEdge().GetEdge().GetId(), testEdgeID)
			}
			if p.GetLog() != inventoryv1.LogProtocol_LOG_PROTOCOL_SYSLOG {
				t.Errorf("provenance log protocol = %v, want SYSLOG", p.GetLog())
			}
			if p.HasFirmwareFingerprint() {
				t.Errorf("provenance firmware_fingerprint = %q, want unset", p.GetFirmwareFingerprint())
			}
			if !p.GetObservedAt().AsTime().Equal(receiveTime) {
				t.Errorf("observed_at = %v, want %v", p.GetObservedAt().AsTime(), receiveTime)
			}
			if !envelope.GetSyslog().GetReceivedAt().AsTime().Equal(receiveTime) {
				t.Errorf("syslog received_at = %v, want %v", envelope.GetSyslog().GetReceivedAt().AsTime(), receiveTime)
			}

			tc.checkSyslog(t, envelope, isFailure)
		})
	}
}

func TestMapRecord_PeerAddressNormalization(t *testing.T) {
	t.Parallel()

	parser, err := syslog.NewParser(syslog.ParseOptions{})
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}

	payload := []byte("<34>1 2026-10-03T10:00:00Z sw1 app - - - link down")
	devEntry := testDeviceEntry()
	edgeRef := testEdgeRef()

	tests := []struct {
		name       string
		peer       netip.AddrPort
		wantV4     bool
		wantV6     bool
		wantOctets []byte
	}{
		{
			name:       "IPv4 peer",
			peer:       netip.MustParseAddrPort("192.0.2.1:514"),
			wantV4:     true,
			wantOctets: []byte{192, 0, 2, 1},
		},
		{
			name:       "IPv4-mapped IPv6 peer",
			peer:       netip.MustParseAddrPort("[::ffff:192.0.2.1]:514"),
			wantV4:     true,
			wantOctets: []byte{192, 0, 2, 1},
		},
		{
			name:       "IPv6 peer",
			peer:       netip.MustParseAddrPort("[2001:db8::1]:514"),
			wantV6:     true,
			wantOctets: netip.MustParseAddr("2001:db8::1").AsSlice(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			obs := syslog.Observation{
				ReceivedAt: time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC),
				Peer:       tc.peer,
				Transport:  syslog.UDP,
			}
			rec, err := parser.Parse(payload, obs)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			syslogRec, _, _ := syslogsource.MapRecord(rec, devEntry, edgeRef)
			srcAddr := syslogRec.GetSourceAddress()
			if tc.wantV4 {
				if !srcAddr.HasV4() {
					t.Fatalf("SourceAddress = %v, want V4", srcAddr)
				}
				if !bytes.Equal(srcAddr.GetV4().GetOctets(), tc.wantOctets) {
					t.Errorf("V4 octets = %v, want %v", srcAddr.GetV4().GetOctets(), tc.wantOctets)
				}
			}
			if tc.wantV6 {
				if !srcAddr.HasV6() {
					t.Fatalf("SourceAddress = %v, want V6", srcAddr)
				}
				if !bytes.Equal(srcAddr.GetV6().GetOctets(), tc.wantOctets) {
					t.Errorf("V6 octets = %v, want %v", srcAddr.GetV6().GetOctets(), tc.wantOctets)
				}
			}
		})
	}
}

// newSourceParser returns a parser configured as the source's receiver
// configures its own.
func newSourceParser(tb testing.TB) *syslog.Parser {
	tb.Helper()
	opts := syslogsource.ReceiverOptions()
	opts.Parse.Limits = opts.Limits
	parser, err := syslog.NewParser(opts.Parse)
	if err != nil {
		tb.Fatalf("NewParser: %v", err)
	}
	return parser
}

// buildValidEnvelope maps rec the way Source.Run does and fails the test
// unless the envelope passes protovalidate and marshals, since either failure
// ends Run. It reports the mapper's parse failure flag.
func buildValidEnvelope(tb testing.TB, rec syslog.Record) (*ingestv1.IngestRecord, bool) {
	tb.Helper()
	syslogRec, prov, isFailure := syslogsource.MapRecord(rec, testDeviceEntry(), testEdgeRef())
	var rawData []byte
	if rec.Raw != nil {
		rawData = *rec.Raw
	}
	envelope := syslogsource.BuildEnvelope("0192e6a0-0000-7000-8000-000000000042", prov, syslogRec, rawData, isFailure, 0)
	if err := protovalidate.Validate(envelope); err != nil {
		tb.Fatalf("protovalidate: %v", err)
	}
	if _, err := proto.Marshal(envelope); err != nil {
		tb.Fatalf("proto.Marshal: %v", err)
	}
	return envelope, isFailure
}

// schemaMaxLen reads a string field's max_len from the schema, so a bound
// the schema changes moves the rows below with it.
func schemaMaxLen(tb testing.TB, msg proto.Message, field protoreflect.Name) int {
	tb.Helper()
	fd := msg.ProtoReflect().Descriptor().Fields().ByName(field)
	if fd == nil {
		tb.Fatalf("%s has no field %q", msg.ProtoReflect().Descriptor().FullName(), field)
	}
	rules, _ := proto.GetExtension(fd.Options(), validate.E_Field).(*validate.FieldRules)
	if !rules.GetString().HasMaxLen() {
		tb.Fatalf("%s.%s has no string max_len", msg.ProtoReflect().Descriptor().FullName(), field)
	}
	return int(rules.GetString().GetMaxLen())
}

// payloadForm is one grammar position that can carry a bounded field.
type payloadForm struct {
	name string
	// build returns a payload carrying value in the field.
	build func(value string) string
	// reach is the longest value the parser hands on from this position, or
	// zero when it hands on any length.
	reach int
	// headerASCII marks a position whose parser rejects non-ASCII bytes with a
	// diagnostic, so a multi-byte value there is also a parse failure.
	headerASCII bool
	// multibyte and rawBytes mark positions where the parser passes
	// multi-byte characters and non-UTF-8 bytes through to the mapper.
	multibyte, rawBytes bool
	// canBeEmpty marks positions where the grammar yields a present, empty
	// value for the field.
	canBeEmpty bool
	// completeAtBound marks positions where a value within the bound parses as
	// Complete, so the mapper must not report a failure.
	completeAtBound bool
}

type boundedField struct {
	name  string
	limit int
	forms []payloadForm
	// get returns the field's value in the mapped record and whether it is set.
	get func(*eventlogv1.SyslogRecord) (string, bool)
	// emptyValid is set for a field whose schema allows an empty value.
	emptyValid bool
}

func headerGetter[T any](has func(T) bool, get func(T) string) func(T) (string, bool) {
	return func(r T) (string, bool) { return get(r), has(r) }
}

func boundedFields(tb testing.TB) []boundedField {
	tb.Helper()
	rec := &eventlogv1.SyslogRecord{}
	elem := &eventlogv1.SyslogStructuredDataElement{}
	param := &eventlogv1.SyslogStructuredDataParam{}

	const (
		rfc5424 = "<34>1 2026-10-03T10:00:00Z "
		legacy  = "<34>Oct  3 10:00:00 "
	)
	sd := func(format string) func(string) string {
		return func(v string) string { return rfc5424 + "sw1 app - - " + fmt.Sprintf(format, v) + " x" }
	}
	firstParam := func(r *eventlogv1.SyslogRecord) *eventlogv1.SyslogStructuredDataParam {
		if len(r.GetStructuredData()) == 0 || len(r.GetStructuredData()[0].GetParams()) == 0 {
			return nil
		}
		return r.GetStructuredData()[0].GetParams()[0]
	}

	return []boundedField{
		{
			name:  "hostname",
			limit: schemaMaxLen(tb, rec, "hostname"),
			get:   headerGetter((*eventlogv1.SyslogRecord).HasHostname, (*eventlogv1.SyslogRecord).GetHostname),
			forms: []payloadForm{
				{
					name:        "rfc5424",
					build:       func(v string) string { return rfc5424 + v + " app - - - x" },
					headerASCII: true, multibyte: true, rawBytes: true, canBeEmpty: true, completeAtBound: true,
				},
				{
					name:      "legacy",
					build:     func(v string) string { return legacy + v + " app: x" },
					multibyte: true, rawBytes: true,
				},
			},
		},
		{
			name:  "app_name",
			limit: schemaMaxLen(tb, rec, "app_name"),
			get:   headerGetter((*eventlogv1.SyslogRecord).HasAppName, (*eventlogv1.SyslogRecord).GetAppName),
			forms: []payloadForm{
				{
					name:        "rfc5424",
					build:       func(v string) string { return rfc5424 + "sw1 " + v + " - - - x" },
					headerASCII: true, multibyte: true, rawBytes: true, canBeEmpty: true, completeAtBound: true,
				},
				{
					name:  "legacy tag",
					build: func(v string) string { return legacy + "sw1 " + v + ": x" },
					reach: 128,
				},
			},
		},
		{
			name:  "proc_id",
			limit: schemaMaxLen(tb, rec, "proc_id"),
			get:   headerGetter((*eventlogv1.SyslogRecord).HasProcId, (*eventlogv1.SyslogRecord).GetProcId),
			forms: []payloadForm{
				{
					name:        "rfc5424",
					build:       func(v string) string { return rfc5424 + "sw1 app " + v + " - - x" },
					headerASCII: true, multibyte: true, rawBytes: true, canBeEmpty: true, completeAtBound: true,
				},
				{
					name:  "legacy tag",
					build: func(v string) string { return legacy + "sw1 app[" + v + "]: x" },
					reach: 123, canBeEmpty: true,
				},
			},
		},
		{
			name:  "msg_id",
			limit: schemaMaxLen(tb, rec, "msg_id"),
			get:   headerGetter((*eventlogv1.SyslogRecord).HasMsgId, (*eventlogv1.SyslogRecord).GetMsgId),
			forms: []payloadForm{{
				name:        "rfc5424",
				build:       func(v string) string { return rfc5424 + "sw1 app - " + v + " - x" },
				headerASCII: true, multibyte: true, rawBytes: true, canBeEmpty: true, completeAtBound: true,
			}},
		},
		{
			name:  "sd id",
			limit: schemaMaxLen(tb, elem, "id"),
			get: func(r *eventlogv1.SyslogRecord) (string, bool) {
				if len(r.GetStructuredData()) == 0 {
					return "", false
				}
				return r.GetStructuredData()[0].GetId(), true
			},
			forms: []payloadForm{{name: "rfc5424", build: sd(`[%s k="v"]`), completeAtBound: true}},
		},
		{
			name:  "param name",
			limit: schemaMaxLen(tb, param, "name"),
			get: func(r *eventlogv1.SyslogRecord) (string, bool) {
				p := firstParam(r)
				return p.GetName(), p != nil
			},
			forms: []payloadForm{{name: "rfc5424", build: sd(`[ex@1 %s="v"]`), completeAtBound: true}},
		},
		{
			name:  "param value",
			limit: schemaMaxLen(tb, param, "value"),
			get: func(r *eventlogv1.SyslogRecord) (string, bool) {
				p := firstParam(r)
				return p.GetValue(), p != nil
			},
			emptyValid: true,
			forms: []payloadForm{{
				name:  "rfc5424",
				build: sd(`[ex@1 k="%s"]`), multibyte: true, rawBytes: true, canBeEmpty: true, completeAtBound: true,
			}},
		},
	}
}

type boundRow struct {
	name    string
	payload string
	// wantValue is the field's expected value when wantSet.
	wantValue string
	wantSet   bool
	// wantFailure is checked only when checkFailure is set.
	wantFailure  bool
	checkFailure bool
}

// boundRows derives the rows for one field and position from the schema
// bound: at the bound, one over, empty, and the multi-byte and non-UTF-8
// values the position passes through. Every row is a payload the parser
// accepts, so each one reaches the mapper.
func boundRows(f boundedField, form payloadForm) []boundRow {
	const (
		ascii     = "a"
		multibyte = "あ"
	)
	atBound := min(f.limit, cmpOr(form.reach, f.limit))
	var rows []boundRow
	add := func(name, value string, set bool, failure, check bool) {
		rows = append(rows, boundRow{
			name: f.name + "/" + form.name + "/" + name, payload: form.build(value),
			wantValue: value, wantSet: set, wantFailure: failure, checkFailure: check,
		})
	}

	add("at bound", strings.Repeat(ascii, atBound), true, false, form.completeAtBound)
	if form.reach == 0 || form.reach > f.limit {
		add("one over bound", strings.Repeat(ascii, f.limit+1), false, true, true)
	}
	if form.canBeEmpty {
		if f.emptyValid {
			add("empty", "", true, false, form.completeAtBound)
		} else {
			add("empty", "", false, true, true)
		}
	}
	if form.multibyte {
		add("multi-byte characters at bound", strings.Repeat(multibyte, f.limit), true, form.headerASCII, form.completeAtBound || form.headerASCII)
		add("multi-byte characters one over bound", strings.Repeat(multibyte, f.limit+1), false, true, true)
	}
	if form.rawBytes {
		add("non-UTF-8 byte", strings.Repeat(ascii, f.limit-1)+"\xff", false, true, true)
	}
	return rows
}

func cmpOr(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

// TestMapRecord_BoundedFieldsFollowTheSchema checks every bounded schema
// field at its bound, one over, and empty, for each grammar position that
// reaches it. A mapper guard that is missing makes a device message fail
// validation, which ends Source.Run, so each guard has a row that fails
// without it.
func TestMapRecord_BoundedFieldsFollowTheSchema(t *testing.T) {
	t.Parallel()

	parser := newSourceParser(t)
	obs := syslog.Observation{
		ReceivedAt: time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC),
		Peer:       netip.MustParseAddrPort("192.0.2.1:514"),
		Transport:  syslog.UDP,
	}

	for _, f := range boundedFields(t) {
		for _, form := range f.forms {
			for _, row := range boundRows(f, form) {
				t.Run(row.name, func(t *testing.T) {
					t.Parallel()

					rec, err := parser.Parse([]byte(row.payload), obs)
					if err != nil {
						t.Fatalf("Parse: %v", err)
					}
					env, isFailure := buildValidEnvelope(t, rec)

					got, set := f.get(env.GetSyslog())
					if set != row.wantSet {
						t.Fatalf("field set = %t (%q), want %t", set, got, row.wantSet)
					}
					if row.wantSet && got != row.wantValue {
						t.Errorf("value differs from the payload's: got %d characters, want %d", len([]rune(got)), len([]rune(row.wantValue)))
					}
					if row.checkFailure && isFailure != row.wantFailure {
						t.Errorf("isParseFailure = %t, want %t", isFailure, row.wantFailure)
					}
				})
			}
		}
	}
}

// FuzzMapRecordValidates holds the property that makes the mapper's guards
// unnecessary to list: for every payload the parser accepts under the
// source's options, the envelope built from MapRecord validates and marshals.
// The seed corpus runs under plain go test.
func FuzzMapRecordValidates(f *testing.F) {
	seeds := []string{
		"",
		"<34>1 2026-10-03T10:00:00Z sw1 app 42 ID1 [ex@1 k=\"v\"] link down",
		"<34>1 - - - - - -",
		"<34>1 0000-01-01T00:00:00Z sw1 app - - - year zero",
		"<34>1 2026-10-03T10:00:00Z  app - - - empty hostname",
		"<34>Oct  3 10:00:00 sw1 app[42]: legacy with PRI",
		"<34>Oct  3 10:00:00 sw1 app[]: legacy empty process",
		"Oct  3 10:00:00 sw1 app: legacy without PRI",
		"<34>Oct  3 10:00:00 sw1\xff app\xfe[1\xfd]: legacy non-UTF-8",
		"<34>1 2026-10-03T10:00:00Z h\xff a\xff p\xff m\xff - non-UTF-8 header fields",
		"<34>1 2026-10-03T10:00:00Z sw1 app - - [i\xffd k=\"v\"] non-UTF-8 sd id",
		"<34>1 2026-10-03T10:00:00Z sw1 app - - [id k\xff=\"v\"] non-UTF-8 param name",
		"<34>1 2026-10-03T10:00:00Z sw1 app - - [id k=\"\xff\"] non-UTF-8 param value",
		"<34>1 2026-10-03T10:00:00Z sw1 app - - [id k=\"\"][id j=\"2\"] duplicate id, empty value",
		"<34>1 2026-10-03T10:00:00Z sw1 app - - [id k=\"v\" k=\"w\"] duplicate param",
		"<192>1 2026-10-03T10:00:00Z sw1 app - - - priority out of range",
		strings.Repeat("x", 65535),
	}
	for _, f2 := range boundedFields(f) {
		for _, form := range f2.forms {
			for _, row := range boundRows(f2, form) {
				seeds = append(seeds, row.payload)
			}
		}
	}
	for _, seed := range seeds {
		f.Add([]byte(seed))
	}

	parser := newSourceParser(f)
	obs := syslog.Observation{
		ReceivedAt: time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC),
		Peer:       netip.MustParseAddrPort("192.0.2.1:514"),
		Transport:  syslog.UDP,
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		rec, err := parser.Parse(payload, obs)
		if err != nil {
			t.Skip("payload over the parser's limit")
		}
		buildValidEnvelope(t, rec)
	})
}
