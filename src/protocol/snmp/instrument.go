package snmp

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// instrument.go threads injected OpenTelemetry traces and metrics through
// the polling path. Providers arrive via the Dial-time
// [WithTracerProvider] / [WithMeterProvider] options; the
// backend resolves a Tracer/Meter once at Dial and depends only on the
// OTel API, never the SDK. With the default no-op providers every
// hook below is a cheap no-op.
//
// The trap path is not instrumented here: providers are session
// (Dial-time) options and [ListenTraps] takes [TrapOption]s, which
// carry no provider, so trap reception is neither traced nor metered.
// The [TrapStream.Dropped] counter is the only drop signal.

// No OpenTelemetry semantic convention covers SNMP client operations, so
// both the instrument names and the attribute keys live in the
// flowseer.snmp.* namespace.
const (
	attrTarget       = "flowseer.snmp.target"
	attrVersion      = "flowseer.snmp.version"
	attrOperation    = "flowseer.snmp.operation"
	attrRequestID    = "flowseer.snmp.request_id"
	attrRetryCount   = "flowseer.snmp.retry_count"
	attrVarbindCount = "flowseer.snmp.varbind_count"
	attrErrorStatus  = "flowseer.snmp.pdu_error_status"
	attrDirection    = "flowseer.snmp.direction"

	// v3/USM attributes. None carry secret material: the engineID is
	// a non-secret hex identifier, never a key or passphrase.
	attrSecurityLevel = "flowseer.snmp.security_level"
	attrAuthProtocol  = "flowseer.snmp.auth_protocol"
	attrPrivProtocol  = "flowseer.snmp.priv_protocol"
	attrEngineID      = "flowseer.snmp.engine_id"
	attrMsgID         = "flowseer.snmp.msg_id"

	// Log-only attributes: the dialed peer, the address a stray
	// datagram actually came from, the version a mismatched datagram
	// carried, and why a [Watcher] entered fallback mode.
	attrPeer            = "flowseer.snmp.peer"
	attrSource          = "flowseer.snmp.source"
	attrReceivedVersion = "flowseer.snmp.received_version"
	attrFallbackReason  = "flowseer.snmp.fallback_reason"
)

// instruments holds the resolved Tracer and metric instruments for one
// session. It is created at Dial and shared by the session and its
// reactor. The zero value is not usable; build via [newInstruments].
type instruments struct {
	tracer trace.Tracer

	// spanBase is attached to every span: the dialed target plus the
	// version and the USM selectors.
	spanBase []attribute.KeyValue
	// metricBase is spanBase without the target. The dialed address is
	// unbounded across a fleet, so it stays off metric time series.
	metricBase []attribute.KeyValue

	// isNoop is set at Dial when both providers are the OTel no-op
	// providers (the never-nil default). The hot-path hooks then skip
	// attribute-slice construction and span/metric wrapping entirely, since
	// none of it can be observed; the emitted telemetry is unchanged
	// whenever a real provider is configured.
	isNoop bool

	requests metric.Int64Counter
	errors   metric.Int64Counter
	retries  metric.Int64Counter
	latency  metric.Float64Histogram
	pduSize  metric.Int64Histogram
}

// newInstruments resolves the Tracer/Meter from the session config's
// providers and registers the metric instruments, including the
// observable in-flight gauge and dropped-reply counter sourced from the
// reactor. inFlight and dropped are sampled by the OTel collection
// callback.
func newInstruments(cfg *SessionConfig, target string, inFlight, dropped func() int64) (*instruments, error) {
	tracer := cfg.TracerProvider.Tracer(scopeName,
		trace.WithInstrumentationVersion(version), trace.WithSchemaURL(semconv.SchemaURL))
	meter := cfg.MeterProvider.Meter(scopeName,
		metric.WithInstrumentationVersion(version), metric.WithSchemaURL(semconv.SchemaURL))

	// Both providers being the OTel no-op type means every signal is
	// discarded; detect it once here so the per-operation hooks can avoid
	// building attribute slices that nothing will ever read.
	_, tracerNoop := cfg.TracerProvider.(tracenoop.TracerProvider)
	_, meterNoop := cfg.MeterProvider.(metricnoop.MeterProvider)

	metricBase := []attribute.KeyValue{
		attribute.String(attrVersion, cfg.Version.String()),
	}
	// v3 sessions carry the non-secret USM protocol selectors on every signal.
	// Passphrases and keys never appear.
	if cfg.USM != nil {
		metricBase = append(metricBase,
			attribute.String(attrSecurityLevel, cfg.USM.Level().String()),
			attribute.String(attrAuthProtocol, cfg.USM.AuthProtocol.String()),
			attribute.String(attrPrivProtocol, cfg.USM.PrivProtocol.String()),
		)
	}
	spanBase := make([]attribute.KeyValue, 0, len(metricBase)+1)
	spanBase = append(spanBase, attribute.String(attrTarget, target))
	spanBase = append(spanBase, metricBase...)
	in := &instruments{
		tracer:     tracer,
		isNoop:     tracerNoop && meterNoop,
		spanBase:   spanBase,
		metricBase: metricBase,
	}

	var regErrs []error
	var err error
	if in.requests, err = meter.Int64Counter("flowseer.snmp.requests",
		metric.WithUnit("{request}"),
		metric.WithDescription("SNMP request PDUs issued")); err != nil {
		regErrs = append(regErrs, err)
	}
	if in.errors, err = meter.Int64Counter("flowseer.snmp.errors",
		metric.WithUnit("{error}"),
		metric.WithDescription("SNMP operations that failed, by classified error kind")); err != nil {
		regErrs = append(regErrs, err)
	}
	if in.retries, err = meter.Int64Counter("flowseer.snmp.retries",
		metric.WithUnit("{retransmit}"),
		metric.WithDescription("SNMP request retransmits")); err != nil {
		regErrs = append(regErrs, err)
	}
	if in.latency, err = meter.Float64Histogram("flowseer.snmp.request.duration",
		metric.WithDescription("SNMP per-PDU round-trip latency"),
		metric.WithUnit("s")); err != nil {
		regErrs = append(regErrs, err)
	}
	if in.pduSize, err = meter.Int64Histogram("flowseer.snmp.pdu.size",
		metric.WithDescription("SNMP datagram size on the wire"),
		metric.WithUnit("By")); err != nil {
		regErrs = append(regErrs, err)
	}

	if _, err = meter.Int64ObservableGauge("flowseer.snmp.in_flight",
		metric.WithUnit("{request}"),
		metric.WithDescription("in-flight SNMP requests on this session"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(inFlight(), metric.WithAttributes(in.metricBase...))
			return nil
		})); err != nil {
		regErrs = append(regErrs, err)
	}
	if _, err = meter.Int64ObservableCounter("flowseer.snmp.dropped",
		metric.WithUnit("{datagram}"),
		metric.WithDescription("unmatched/late/malformed datagrams dropped by the reactor"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(dropped(), metric.WithAttributes(in.metricBase...))
			return nil
		})); err != nil {
		regErrs = append(regErrs, err)
	}

	if joined := errors.Join(regErrs...); joined != nil {
		return nil, errs.Wrap(joined, "snmp: register instruments")
	}
	return in, nil
}

// startOp opens a span for one SNMP operation, returning the span-bearing
// context the reactor enriches with request-id / retry-count.
func (in *instruments) startOp(ctx context.Context, op string) (context.Context, trace.Span) {
	if in.isNoop {
		// The no-op tracer ignores attributes; skip building them.
		return in.tracer.Start(ctx, "snmp."+op)
	}
	attrs := make([]attribute.KeyValue, 0, len(in.spanBase)+1)
	attrs = append(attrs, attribute.String(attrOperation, op))
	attrs = append(attrs, in.spanBase...)
	return in.tracer.Start(ctx, "snmp."+op, trace.WithAttributes(attrs...))
}

// finishOp records the operation's varbind count and PDU error-status on
// the span and sets the span status from err, then records the classified
// error metric when err is non-nil.
func (in *instruments) finishOp(ctx context.Context, span trace.Span, op string, varbinds int, pe *PDUError, err error) {
	if in.isNoop {
		// No span/metric backing; nothing to record.
		return
	}
	span.SetAttributes(attribute.Int(attrVarbindCount, varbinds))
	if pe != nil {
		span.SetAttributes(attribute.String(attrErrorStatus, pe.Status.String()))
	}
	if err != nil {
		kind := classifyError(err)
		span.RecordError(err)
		span.SetAttributes(semconv.ErrorTypeKey.String(kind))
		span.SetStatus(codes.Error, kind)
		in.recordError(ctx, op, err)
	}
}

func (in *instruments) opAttrs(op string) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(in.metricBase)+1)
	attrs = append(attrs, attribute.String(attrOperation, op))
	return append(attrs, in.metricBase...)
}

// recordRequest counts one issued request PDU and its round-trip latency.
func (in *instruments) recordRequest(ctx context.Context, op string, d time.Duration) {
	if in.isNoop {
		return
	}
	attrs := metric.WithAttributes(in.opAttrs(op)...)
	in.requests.Add(ctx, 1, attrs)
	in.latency.Record(ctx, d.Seconds(), attrs)
}

// recordError counts one failed operation, attributed by classified kind.
func (in *instruments) recordError(ctx context.Context, op string, err error) {
	if in.isNoop {
		return
	}
	attrs := in.opAttrs(op)
	attrs = append(attrs, semconv.ErrorTypeKey.String(classifyError(err)))
	in.errors.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// recordRetry counts one retransmit.
func (in *instruments) recordRetry(ctx context.Context) {
	if in.isNoop {
		return
	}
	in.retries.Add(ctx, 1, metric.WithAttributes(in.metricBase...))
}

// recordPDUSize records a datagram size, attributed by direction
// ("tx"/"rx"). It is on the hot rx path (one call per received datagram),
// so it skips all attribute work when instrumentation is no-op.
func (in *instruments) recordPDUSize(ctx context.Context, direction string, n int) {
	if in.isNoop {
		return
	}
	attrs := make([]attribute.KeyValue, 0, len(in.metricBase)+1)
	attrs = append(attrs, attribute.String(attrDirection, direction))
	attrs = append(attrs, in.metricBase...)
	in.pduSize.Record(ctx, int64(n), metric.WithAttributes(attrs...))
}

// hexString renders raw octets (e.g. an engineID) as a non-secret hex
// identifier for a span attribute.
func hexString(b []byte) string { return hex.EncodeToString(b) }

// classifyError maps an error to a low-cardinality kind for the error
// counter and span status ("error count by classified type").
func classifyError(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "context_deadline"
	case errors.Is(err, errTimeout):
		return "timeout"
	case errors.Is(err, ErrSessionClosed):
		return "session_closed"
	case errors.Is(err, errAtCapacity):
		return "at_capacity"
	case errors.Is(err, ErrCommunityMismatch):
		return "community_mismatch"
	case errors.Is(err, ErrVersionMismatch):
		return "version_mismatch"
	case errors.Is(err, warnCounter64InV1):
		return "counter64_in_v1"

	// v3/USM kinds. Low-cardinality, secret-free.
	case errors.Is(err, ErrAuthFailed):
		return "auth_failure"
	case errors.Is(err, ErrPrivDecrypt):
		return "decryption_error"
	case errors.Is(err, ErrUSMDowngrade):
		return "unsupported_sec_level"
	case errors.Is(err, ErrUSMNoKeys):
		return "usm_no_keys"
	case errors.Is(err, ErrResyncExhausted):
		return "not_in_time_window"
	case errors.Is(err, ErrDiscoveryFailed):
		return "report_unknown_engine"
	case errors.Is(err, ErrReportWrongDigest):
		return "wrong_digest"
	case errors.Is(err, ErrReportDecryptionError):
		return "decryption_error"
	case errors.Is(err, ErrReportUnsupportedSecLevel):
		return "unsupported_sec_level"
	case errors.Is(err, ErrReportUnknownUserName):
		return "unknown_user_name"
	case errors.Is(err, ErrReportUnexpected):
		return "report_unexpected"
	}
	var pe *PDUError
	if errors.As(err, &pe) {
		return "pdu_error"
	}
	return "other"
}
