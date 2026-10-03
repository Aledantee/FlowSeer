package syslogsource_test

import (
	"bytes"
	"net/netip"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

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

	parser, err := syslog.NewParser(syslog.ParseOptions{
		CaptureRaw: true,
		Limits: syslog.Limits{
			MaxPayload: 65535,
		},
	})
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}

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

	devEntry := testDeviceEntry()
	edgeRef := testEdgeRef()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := parser.Parse(tc.payload, obs)
			if err != nil {
				t.Fatalf("parser.Parse: %v", err)
			}

			syslogRec, prov, isFailure := syslogsource.MapRecord(rec, devEntry, edgeRef)
			if isFailure != tc.wantParseFailure {
				t.Errorf("isParseFailure = %v, want %v", isFailure, tc.wantParseFailure)
			}

			var rawData []byte
			if rec.Raw != nil {
				rawData = *rec.Raw
			}
			envelope := syslogsource.BuildEnvelope(
				"0192e6a0-0000-7000-8000-000000000042",
				prov,
				syslogRec,
				rawData,
				isFailure,
				0,
			)

			if err := protovalidate.Validate(envelope); err != nil {
				t.Fatalf("protovalidate: %v", err)
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

func TestMapRecord_PeerAddressNormalisation(t *testing.T) {
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
