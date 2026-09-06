package snmp

import (
	"context"
	"net"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"go.aledante.io/FlowSeer/src/common/secret"
)

// These are the repo's first OTel assertion tests. They use the OTel SDK
// (test-only — production code imports only the API) to capture spans
// and metrics emitted by a native session and verify their shape.

func dialInstrumented(t *testing.T, agent *mockAgent, opts ...Option) (Session, *tracetest.SpanRecorder, *sdkmetric.ManualReader) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	base := []Option{
		WithCommunity(secret.NewString("public")),
		WithMinSecurity(MinSecurityNoAuth),
		WithTimeout(time.Second),
		WithTracerProvider(tp),
		WithMeterProvider(mp),
	}
	sess, err := NewSession(context.Background(), agent.addr.String(), V2c, append(base, opts...)...)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess, sr, reader
}

func findSpan(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

func spanAttr(s sdktrace.ReadOnlySpan, key string) (string, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.String(), true
		}
	}
	return "", false
}

func metricNames(t *testing.T, reader *sdkmetric.ManualReader) map[string]bool {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	names := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names[m.Name] = true
		}
	}
	return names
}

func TestInstrument_GetEmitsSpanAndMetrics(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	agent := startMIBAgent(t, []mibEntry{{oid, octet(oid, "Router X")}}, mibBehavior{})
	sess, sr, reader := dialInstrumented(t, agent)

	if _, err := sess.Get(context.Background(), []OID{oid}); err != nil {
		t.Fatal(err)
	}

	span := findSpan(sr.Ended(), "snmp.Get")
	if span == nil {
		t.Fatalf("no snmp.Get span; got %d spans", len(sr.Ended()))
	}
	for _, key := range []string{attrOperation, attrTarget, attrVersion, attrVarbindCount, attrRequestID} {
		if _, ok := spanAttr(span, key); !ok {
			t.Errorf("span missing attribute %s", key)
		}
	}
	if v, _ := spanAttr(span, attrOperation); v != "Get" {
		t.Errorf("operation attr = %q, want Get", v)
	}

	names := metricNames(t, reader)
	for _, want := range []string{
		"flowseer.snmp.requests",
		"flowseer.snmp.request.duration",
		"flowseer.snmp.in_flight",
		"flowseer.snmp.dropped",
	} {
		if !names[want] {
			t.Errorf("missing metric %q (have %v)", want, names)
		}
	}
}

func TestInstrument_WalkEmitsSpan(t *testing.T) {
	root, entries := ifTable(3)
	agent := startMIBAgent(t, entries, mibBehavior{})
	sess, sr, _ := dialInstrumented(t, agent)

	w := sess.Walk(context.Background(), root)
	for range w.Iter() {
	}
	if err := w.Err(); err != nil {
		t.Fatal(err)
	}
	if findSpan(sr.Ended(), "snmp.Walk") == nil {
		t.Fatalf("no snmp.Walk span; got %d spans", len(sr.Ended()))
	}
}

func TestInstrument_PDUErrorRecordsStatus(t *testing.T) {
	// An agent that answers every Get with a genErr PDU error.
	agent := startMockAgent(t, func(req *message, _ *net.UDPAddr, send func(*message)) {
		send(&message{
			version:   req.version,
			community: req.community,
			pdu: pdu{
				typ:         pduGetResponse,
				requestID:   req.pdu.requestID,
				errorStatus: GenErr,
				errorIndex:  1,
				varbinds:    req.pdu.varbinds,
			},
		})
	})
	sess, sr, reader := dialInstrumented(t, agent)

	oid := MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	if _, err := sess.Get(context.Background(), []OID{oid}); err == nil {
		t.Fatal("expected a PDU error")
	}
	span := findSpan(sr.Ended(), "snmp.Get")
	if span == nil {
		t.Fatal("no snmp.Get span")
	}
	if v, ok := spanAttr(span, attrErrorStatus); !ok || v != GenErr.String() {
		t.Errorf("error-status attr = %q (ok=%v), want %q", v, ok, GenErr.String())
	}
	if !metricNames(t, reader)["flowseer.snmp.errors"] {
		t.Error("error counter not recorded")
	}
}

// TestInstrument_NoopDefault confirms operations run with no provider
// configured (the never-nil, zero-overhead noop default).
func TestInstrument_NoopDefault(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	agent := startMIBAgent(t, []mibEntry{{oid, octet(oid, "x")}}, mibBehavior{})
	// dialNative passes no provider options → noop defaults.
	sess := dialNative(t, agent, V2c)
	if _, err := sess.Get(context.Background(), []OID{oid}); err != nil {
		t.Fatalf("noop-instrumented Get failed: %v", err)
	}
}

// TestInstrument_NilProviderIsNoOp confirms WithTracerProvider(nil) keeps
// the default rather than panicking.
func TestInstrument_NilProviderIsNoOp(t *testing.T) {
	oid := MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	agent := startMIBAgent(t, []mibEntry{{oid, octet(oid, "x")}}, mibBehavior{})
	sess, err := NewSession(context.Background(), agent.addr.String(), V2c, WithCommunity(secret.NewString("public")),
		WithMinSecurity(MinSecurityNoAuth), WithTimeout(time.Second),
		WithTracerProvider(nil), WithMeterProvider(nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if _, err := sess.Get(context.Background(), []OID{oid}); err != nil {
		t.Fatalf("Get with nil providers failed: %v", err)
	}
}

// TestInstrument_V3SpanAttributes confirms a v3 op span carries the USM
// attributes (security level, auth/priv protocol, engineID, msgID) and that
// no passphrase ever appears in any emitted span attribute.
func TestInstrument_V3SpanAttributes(t *testing.T) {
	agent := startV3Agent(t, baseCfg(), v3TestEngine, 3, 1000, nil)
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	sess, err := NewSession(context.Background(), agent.addr.String(), V3,
		WithUSM(baseCfg()),
		WithTimeout(time.Second),
		WithTracerProvider(tp), WithMeterProvider(mp))
	if err != nil {
		t.Fatalf("Dial v3: %v", err)
	}
	defer func() { _ = sess.Close() }()

	if _, err := sess.Get(context.Background(), []OID{MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	span := findSpan(sr.Ended(), "snmp.Get")
	if span == nil {
		t.Fatalf("no snmp.Get span")
	}
	for _, key := range []string{attrSecurityLevel, attrAuthProtocol, attrPrivProtocol, attrEngineID, attrMsgID} {
		if _, ok := spanAttr(span, key); !ok {
			t.Errorf("v3 span missing attribute %s", key)
		}
	}
	if v, _ := spanAttr(span, attrSecurityLevel); v != "authPriv" {
		t.Errorf("security_level = %q, want authPriv", v)
	}
	// Redaction: no passphrase in any span attribute value.
	cfg := baseCfg()
	for _, s := range sr.Ended() {
		for _, kv := range s.Attributes() {
			val := kv.Value.String()
			if cfg.AuthPassphrase.EqualString(val) || cfg.PrivPassphrase.EqualString(val) {
				t.Fatalf("span attr %s leaked a passphrase", kv.Key)
			}
		}
	}
}

// TestInstrument_V3ErrorKinds pins the low-cardinality v3 error-kind mapping.
func TestInstrument_V3ErrorKinds(t *testing.T) {
	cases := map[error]string{
		ErrAuthFailed:                "auth_failure",
		ErrPrivDecrypt:               "decryption_error",
		ErrUSMDowngrade:              "unsupported_sec_level",
		ErrUSMNoKeys:                 "usm_no_keys",
		ErrResyncExhausted:           "not_in_time_window",
		ErrDiscoveryFailed:           "report_unknown_engine",
		ErrReportWrongDigest:         "wrong_digest",
		ErrReportUnknownUserName:     "unknown_user_name",
		ErrReportUnsupportedSecLevel: "unsupported_sec_level",
		ErrReportUnexpected:          "report_unexpected",
	}
	for err, want := range cases {
		if got := classifyError(err); got != want {
			t.Errorf("classifyError(%v) = %q, want %q", err, got, want)
		}
	}
}
