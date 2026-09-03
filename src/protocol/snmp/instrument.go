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
// carry no provider. Trap telemetry awaits provider trap-options and is a
// follow-up; the [TrapStream.Dropped] counter already gives operators
// drop visibility.

// Attribute keys use the repo snake_case convention. No OTel
// semantic convention covers SNMP client operations, so metric names live
// in the flowseer.snmp.* namespace.
const (
	attrTarget       = "snmp_target"
	attrVersion      = "snmp_version"
	attrOperation    = "snmp_operation"
	attrRequestID    = "snmp_request_id"
	attrRetryCount   = "snmp_retry_count"
	attrVarbindCount = "snmp_varbind_count"
	attrErrorStatus  = "snmp_pdu_error_status"
	attrErrorKind    = "snmp_error_kind"
	attrDirection    = "snmp_direction"

	// v3/USM attributes. None carry secret material: the engineID is
	// a non-secret hex identifier, never a key or passphrase.
	attrSecurityLevel = "snmp_security_level"
	attrAuthProtocol  = "snmp_auth_protocol"
	attrPrivProtocol  = "snmp_priv_protocol"
	attrEngineID      = "snmp_engine_id"
	attrMsgID         = "snmp_msg_id"
)

// instruments holds the resolved Tracer and metric instruments for one
// session. It is created at Dial and shared by the session and its
// reactor. The zero value is not usable; build via [newInstruments].
type instruments struct {
	tracer trace.Tracer
	base   []attribute.KeyValue // target + version, attached to every signal

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
	tracer := cfg.TracerProvider.Tracer(scopeName, trace.WithInstrumentationVersion(version))
	meter := cfg.MeterProvider.Meter(scopeName, metric.WithInstrumentationVersion(version))

	// Both providers being the OTel no-op type means every signal is
	// discarded; detect it once here so the per-operation hooks can avoid
	// building attribute slices that nothing will ever read.
	_, tracerNoop := cfg.TracerProvider.(tracenoop.TracerProvider)
	_, meterNoop := cfg.MeterProvider.(metricnoop.MeterProvider)

	base := []attribute.KeyValue{
		attribute.String(attrTarget, target),
		attribute.String(attrVersion, cfg.Version.String()),
	}
	// v3 sessions carry the non-secret USM protocol selectors on every signal.
	// Passphrases and keys never appear.
	if cfg.USM != nil {
		base = append(base,
			attribute.String(attrSecurityLevel, cfg.USM.Level().String()),
			attribute.String(attrAuthProtocol, cfg.USM.AuthProtocol.String()),
			attribute.String(attrPrivProtocol, cfg.USM.PrivProtocol.String()),
		)
	}
	in := &instruments{
		tracer: tracer,
		isNoop: tracerNoop && meterNoop,
		base:   base,
	}

	var regErrs []error
	var err error
	if in.requests, err = meter.Int64Counter("flowseer.snmp.requests",
		metric.WithDescription("SNMP request PDUs issued")); err != nil {
		regErrs = append(regErrs, err)
	}
	if in.errors, err = meter.Int64Counter("flowseer.snmp.errors",
		metric.WithDescription("SNMP operations that failed, by classified error kind")); err != nil {
		regErrs = append(regErrs, err)
	}
	if in.retries, err = meter.Int64Counter("flowseer.snmp.retries",
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
		metric.WithDescription("in-flight SNMP requests on this session"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(inFlight(), metric.WithAttributes(in.base...))
			return nil
		})); err != nil {
		regErrs = append(regErrs, err)
	}
	if _, err = meter.Int64ObservableCounter("flowseer.snmp.dropped",
		metric.WithDescription("unmatched/late/malformed datagrams dropped by the reactor"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(dropped(), metric.WithAttributes(in.base...))
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
	attrs := make([]attribute.KeyValue, 0, len(in.base)+1)
	attrs = append(attrs, attribute.String(attrOperation, op))
	attrs = append(attrs, in.base...)
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
		span.RecordError(err)
		span.SetStatus(codes.Error, classifyError(err))
		in.recordError(ctx, op, err)
	}
}

func (in *instruments) opAttrs(op string) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(in.base)+1)
	attrs = append(attrs, attribute.String(attrOperation, op))
	return append(attrs, in.base...)
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
	attrs = append(attrs, attribute.String(attrErrorKind, classifyError(err)))
	in.errors.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// recordRetry counts one retransmit.
func (in *instruments) recordRetry(ctx context.Context) {
	if in.isNoop {
		return
	}
	in.retries.Add(ctx, 1, metric.WithAttributes(in.base...))
}

// recordPDUSize records a datagram size, attributed by direction
// ("tx"/"rx"). It is on the hot rx path (one call per received datagram),
// so it skips all attribute work when instrumentation is no-op.
func (in *instruments) recordPDUSize(ctx context.Context, direction string, n int) {
	if in.isNoop {
		return
	}
	attrs := make([]attribute.KeyValue, 0, len(in.base)+1)
	attrs = append(attrs, attribute.String(attrDirection, direction))
	attrs = append(attrs, in.base...)
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
