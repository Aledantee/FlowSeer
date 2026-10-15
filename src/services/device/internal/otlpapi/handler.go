// Package otlpapi is central's TelemetryService: it posts the OTLP bodies an
// edge delivers to central's collector, unchanged, and answers with how many
// leading bodies it settled. It classifies the collector's answer with the
// same [edgebus.Collector] the forwarder uses for bodies that arrive over the
// edge's stream.
package otlpapi

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	telemetryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/telemetry/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/telemetry/v1/telemetryv1connect"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/fsconv"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

const (
	scopeName = "go.aledante.io/FlowSeer/src/services/device/internal/otlpapi"

	reasonKey = "flowseer.otlpapi.reason"

	reasonInvalid           = "invalid"
	reasonCollectorRejected = "collector_rejected"

	errorTypeCollectorUnavailable = "collector_unavailable"
	errorTypeCollectorRetry       = "collector_retry"
	errorTypeValidation           = "validation"

	logInterval = 10 * time.Second
	refusalKey  = "refusal"
	retryKey    = "retry"
)

// Config wires the handler to central.
type Config struct {
	// Collector receives each body. Nil means central has no collector, and
	// the call settles nothing, so the edge keeps its telemetry.
	Collector *edgebus.Collector
	// EdgeID names the calling edge from the call's verified assertion.
	EdgeID func(context.Context) (string, error)
	// Logger receives refusal events and retry warnings. Nil discards them.
	Logger *slog.Logger
	// MeterProvider backs the handler's counters. Nil records nothing.
	MeterProvider metric.MeterProvider
}

// Handler is central's TelemetryService. It is safe for concurrent use.
type Handler struct {
	collector *edgebus.Collector
	edgeID    func(context.Context) (string, error)
	logger    *slog.Logger
	forwarded metric.Int64Counter
	refused   metric.Int64Counter

	mu         sync.Mutex // guards lastLogged
	lastLogged map[string]time.Time
}

var _ telemetryv1connect.TelemetryServiceHandler = (*Handler)(nil)

// NewHandler builds the handler.
func NewHandler(cfg Config) (*Handler, error) {
	if cfg.EdgeID == nil {
		return nil, errs.New().Code(ErrCodeStart).Msg("the telemetry handler needs an edge resolver")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	provider := cfg.MeterProvider
	if provider == nil {
		provider = metricnoop.NewMeterProvider()
	}
	meter := provider.Meter(scopeName, metric.WithSchemaURL(semconv.SchemaURL))
	forwarded, err := meter.Int64Counter("flowseer.otlpapi.records.forwarded", metric.WithUnit("{record}"),
		metric.WithDescription("Telemetry bodies the collector accepted, by signal"))
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStart).Msg("build forwarded counter")
	}
	refused, err := meter.Int64Counter("flowseer.otlpapi.records.refused", metric.WithUnit("{record}"),
		metric.WithDescription("Telemetry bodies settled without reaching the collector's storage, by reason and signal"))
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStart).Msg("build refused counter")
	}
	return &Handler{
		collector: cfg.Collector, edgeID: cfg.EdgeID, logger: logger,
		forwarded: forwarded, refused: refused,
		lastLogged: map[string]time.Time{},
	}, nil
}

// Deliver posts the batch's bodies in order and answers with the count of
// leading bodies settled. A body is settled when the collector accepts it or
// refuses it for its content, and an invalid export is settled as refused.
// Any other answer ends the count, and so does a missing collector, so the
// edge keeps the rest in its log.
func (h *Handler) Deliver(ctx context.Context, req *connect.Request[telemetryv1.DeliverRequest]) (*connect.Response[telemetryv1.DeliverResponse], error) {
	edgeID, err := h.edgeID(ctx)
	if err != nil {
		return nil, connectErr(errs.From(err).Code(ErrCodeEdge).Msg("identify delivering edge"))
	}
	var settled uint32
	if h.collector != nil {
		for _, export := range req.Msg.GetExports() {
			if !h.deliverOne(ctx, edgeID, export) {
				break
			}
			settled++
		}
	}
	return connect.NewResponse(telemetryv1.DeliverResponse_builder{Settled: &settled}.Build()), nil
}

// deliverOne settles one export and reports whether it did.
func (h *Handler) deliverOne(ctx context.Context, edgeID string, export *telemetryv1.TelemetryExport) bool {
	signal, ok := signalOf(export.GetSignal())
	if err := protovalidate.Validate(export); err != nil {
		var violation *protovalidate.ValidationError
		if !errors.As(err, &violation) {
			h.logRetry(ctx, edgeID, errorTypeValidation)
			return false
		}
		h.refuse(ctx, edgeID, reasonInvalid, signalLabel(signal, ok))
		return true
	}
	label := signalLabel(signal, ok)
	switch h.collector.Post(ctx, signal, export.GetBody()) {
	case edgebus.PostAccepted:
		h.forwarded.Add(ctx, 1, metric.WithAttributes(attribute.String(fsconv.DeviceSignalKey, label)))
		return true
	case edgebus.PostRefused:
		h.refuse(ctx, edgeID, reasonCollectorRejected, label)
		return true
	case edgebus.PostRetryRejected:
		h.logRetry(ctx, edgeID, errorTypeCollectorRetry)
	default:
		h.logRetry(ctx, edgeID, errorTypeCollectorUnavailable)
	}
	return false
}

// signalOf maps the wire enum to the signal the collector path names. The
// second result is false for a value validation refuses.
func signalOf(signal telemetryv1.TelemetrySignal) (edgebus.OTelSignal, bool) {
	switch signal {
	case telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_LOGS:
		return edgebus.SignalLogs, true
	case telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_METRICS:
		return edgebus.SignalMetrics, true
	case telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_TRACES:
		return edgebus.SignalTraces, true
	}
	return "", false
}

// signalLabel is the metric value for a signal, empty for a value no signal
// names, so the label set stays closed.
func signalLabel(signal edgebus.OTelSignal, ok bool) string {
	if !ok {
		return ""
	}
	return string(signal)
}

func (h *Handler) refuse(ctx context.Context, edgeID, reason, signal string) {
	h.refused.Add(ctx, 1, metric.WithAttributes(
		attribute.String(reasonKey, reason),
		attribute.String(fsconv.DeviceSignalKey, signal),
	))
	if !h.allow(edgeID + ":" + refusalKey) {
		return
	}
	h.logger.WarnContext(ctx, "telemetry body refused",
		fsconv.EventName("flowseer.otlpapi.record.refused"),
		fsconv.EdgeID(edgeID),
		slog.String(reasonKey, reason),
		slog.String(fsconv.DeviceSignalKey, signal),
	)
}

func (h *Handler) logRetry(ctx context.Context, edgeID, errorType string) {
	if ctx.Err() != nil || !h.allow(edgeID+":"+retryKey) {
		return
	}
	h.logger.WarnContext(ctx, "telemetry body left for redelivery",
		fsconv.EdgeID(edgeID),
		slog.String(string(semconv.ErrorTypeKey), errorType),
	)
}

// allow reports whether key last logged at least logInterval ago.
func (h *Handler) allow(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if now.Sub(h.lastLogged[key]) < logInterval {
		return false
	}
	h.lastLogged[key] = now
	return true
}
