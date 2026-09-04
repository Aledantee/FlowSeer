package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

const (
	telemetryQueueSize       = 2048
	telemetryBatchSize       = 512
	telemetryBatchInterval   = time.Second
	telemetryMetricInterval  = 30 * time.Second
	telemetryShutdownTimeout = 10 * time.Second
	telemetryAttributeLimit  = 32
	telemetryValueLimit      = 256
	telemetryMetricCardLimit = 2000
)

type telemetryShutdown func(context.Context) error

type telemetryFinalExportKey struct{}

type otlpTransport interface {
	uploadLogs(context.Context, *collectorlogspb.ExportLogsServiceRequest) error
	uploadMetrics(context.Context, *collectormetricspb.ExportMetricsServiceRequest) error
	uploadTraces(context.Context, []*tracepb.ResourceSpans) error
}

type telemetryFactorySet struct {
	newResource  func(Identity) (*resource.Resource, error)
	newTransport func(context.Context, normalizedOTLPConnection) (otlpTransport, telemetryShutdown, error)
	newLogs      func(*resource.Resource, otlpTransport, *telemetryDiagnostics, normalizedOTLPConnection) (slog.Handler, telemetryShutdown, error)
	newMetrics   func(*resource.Resource, otlpTransport, *telemetryDiagnostics, normalizedOTLPConnection) (metric.MeterProvider, telemetryShutdown, error)
	newTraces    func(*resource.Resource, otlpTransport, *telemetryDiagnostics, normalizedOTLPConnection) (trace.TracerProvider, telemetryShutdown, error)
}

var defaultTelemetryFactories = telemetryFactorySet{
	newResource:  newTelemetryResource,
	newTransport: newDirectOTLPTransport,
	newLogs:      newManagedLogProvider,
	newMetrics:   newManagedMetricProvider,
	newTraces:    newManagedTraceProvider,
}

type telemetryOwner struct {
	telemetry      telemetry
	localLogger    *slog.Logger
	tracerProvider trace.TracerProvider
	meterProvider  metric.MeterProvider
	logsBacking    signalBacking
	metricsBacking signalBacking
	tracesBacking  signalBacking
	shutdowns      []telemetryShutdown
	once           sync.Once
	err            error
}

func (o *telemetryOwner) shutdown(ctx context.Context) error {
	o.once.Do(func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), telemetryShutdownTimeout)
		defer cancel()
		shutdownCtx = context.WithValue(shutdownCtx, telemetryFinalExportKey{}, true)
		for _, shutdown := range o.shutdowns {
			o.err = errors.Join(o.err, shutdown(shutdownCtx))
		}
	})
	return o.err
}

func newRunTelemetry(
	ctx context.Context,
	identity Identity,
	config normalizedTelemetryConfig,
	factories telemetryFactorySet,
) (*telemetryOwner, error) {
	owner := &telemetryOwner{
		logsBacking:    config.logs.backing,
		metricsBacking: config.metrics.backing,
		tracesBacking:  config.traces.backing,
	}
	if config.injectedShutdown != nil {
		owner.shutdowns = append(owner.shutdowns, config.injectedShutdown)
	}
	unwind := func(cause error) (*telemetryOwner, error) {
		return nil, errors.Join(cause, owner.shutdown(context.WithoutCancel(ctx)))
	}

	localHandler := traceLogHandler{Handler: config.localLogger.Handler()}
	owner.localLogger = slog.New(localHandler)
	logHandler := slog.Handler(localHandler)
	tracerProvider := config.traces.provider
	meterProvider := config.metrics.provider

	managed := config.logs.backing == signalManaged || config.metrics.backing == signalManaged || config.traces.backing == signalManaged
	var transport otlpTransport
	var res *resource.Resource
	if managed {
		var err error
		res, err = factories.newResource(identity)
		if err != nil {
			return nil, fmt.Errorf("create telemetry resource: %w", err)
		}
		var closeTransport telemetryShutdown
		transport, closeTransport, err = factories.newTransport(ctx, config.connection)
		if err != nil {
			return unwind(fmt.Errorf("create telemetry transport: %w", err))
		}
		owner.shutdowns = prependShutdown(owner.shutdowns, closeTransport)
	}

	diagnostics := newTelemetryDiagnostics(os.Stderr)
	var closeLogs, closeMetrics, closeTraces telemetryShutdown
	switch config.logs.backing {
	case signalManaged:
		handler, shutdown, err := factories.newLogs(res, transport, diagnostics, config.connection)
		if err != nil {
			return unwind(fmt.Errorf("create telemetry logs: %w", err))
		}
		closeLogs = shutdown
		owner.shutdowns = prependShutdown(owner.shutdowns, closeLogs)
		logHandler = multiSlogHandler{handlers: []slog.Handler{localHandler, sanitizeSlogHandler{Handler: handler}}}
	case signalInjected:
		logHandler = multiSlogHandler{handlers: []slog.Handler{localHandler, config.logs.handler}}
	}
	if config.metrics.backing == signalManaged {
		provider, shutdown, err := factories.newMetrics(res, transport, diagnostics, config.connection)
		if err != nil {
			return unwind(fmt.Errorf("create telemetry metrics: %w", err))
		}
		meterProvider, closeMetrics = provider, shutdown
		owner.shutdowns = prependShutdown(owner.shutdowns, closeMetrics)
	}
	if config.traces.backing == signalManaged {
		provider, shutdown, err := factories.newTraces(res, transport, diagnostics, config.connection)
		if err != nil {
			return unwind(fmt.Errorf("create telemetry traces: %w", err))
		}
		tracerProvider, closeTraces = provider, shutdown
		owner.shutdowns = prependShutdown(owner.shutdowns, closeTraces)
	}

	capabilities, err := telemetryFromComponents(slog.New(logHandler), tracerProvider, meterProvider, config.propagator)
	if err != nil {
		return unwind(err)
	}
	owner.telemetry = capabilities
	owner.telemetry.owner = owner
	owner.tracerProvider = tracerProvider
	owner.meterProvider = meterProvider
	return owner, nil
}

func prependShutdown(existing []telemetryShutdown, shutdowns ...telemetryShutdown) []telemetryShutdown {
	result := make([]telemetryShutdown, 0, len(shutdowns)+len(existing))
	for _, shutdown := range shutdowns {
		if shutdown != nil {
			result = append(result, shutdown)
		}
	}
	return append(result, existing...)
}

func newTelemetryResource(identity Identity) (*resource.Resource, error) {
	sdkVersion := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dependency := range info.Deps {
			if dependency.Path == "go.opentelemetry.io/otel/sdk" {
				sdkVersion = dependency.Version
				break
			}
		}
	}
	return resource.NewSchemaless(
		semconv.ServiceName(identity.Name),
		semconv.ServiceNamespace(identity.Namespace),
		semconv.ServiceVersion(identity.Version),
		semconv.TelemetrySDKLanguageGo,
		semconv.TelemetrySDKName("opentelemetry"),
		semconv.TelemetrySDKVersion(sdkVersion),
	), nil
}

func newManagedLogProvider(
	res *resource.Resource,
	transport otlpTransport,
	diagnostics *telemetryDiagnostics,
	_ normalizedOTLPConnection,
) (slog.Handler, telemetryShutdown, error) {
	exporter := &managedLogExporter{transport: transport, diagnostics: diagnostics}
	processor := log.NewBatchProcessor(
		exporter,
		log.WithMaxQueueSize(telemetryQueueSize),
		log.WithExportMaxBatchSize(telemetryBatchSize),
		log.WithExportInterval(telemetryBatchInterval),
		log.WithExportTimeout(telemetryRetryLimit),
	)
	provider := log.NewLoggerProvider(
		log.WithResource(res),
		log.WithProcessor(processor),
		log.WithAttributeCountLimit(telemetryAttributeLimit),
		log.WithAttributeValueLengthLimit(telemetryValueLimit),
	)
	handler := otelslog.NewHandler(
		instrumentationScope,
		otelslog.WithLoggerProvider(provider),
		otelslog.WithVersion(instrumentationVersion),
	)
	return handler, provider.Shutdown, nil
}

func newManagedMetricProvider(
	res *resource.Resource,
	transport otlpTransport,
	diagnostics *telemetryDiagnostics,
	_ normalizedOTLPConnection,
) (metric.MeterProvider, telemetryShutdown, error) {
	exporter := &managedMetricExporter{transport: transport, diagnostics: diagnostics}
	reader := sdkmetric.NewPeriodicReader(
		exporter,
		sdkmetric.WithInterval(telemetryMetricInterval),
		sdkmetric.WithTimeout(telemetryRetryLimit),
	)
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
		sdkmetric.WithCardinalityLimit(telemetryMetricCardLimit),
	)
	return provider, provider.Shutdown, nil
}

func newManagedTraceProvider(
	res *resource.Resource,
	transport otlpTransport,
	diagnostics *telemetryDiagnostics,
	_ normalizedOTLPConnection,
) (trace.TracerProvider, telemetryShutdown, error) {
	client := &managedTraceClient{transport: transport}
	exporter, err := otlptrace.New(context.Background(), client)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize trace exporter: %w", err)
	}
	guarded := &managedTraceExporter{SpanExporter: exporter, diagnostics: diagnostics}
	processor := sdktrace.NewBatchSpanProcessor(
		guarded,
		sdktrace.WithMaxQueueSize(telemetryQueueSize),
		sdktrace.WithMaxExportBatchSize(telemetryBatchSize),
		sdktrace.WithBatchTimeout(telemetryBatchInterval),
		sdktrace.WithExportTimeout(telemetryRetryLimit),
	)
	limits := sdktrace.SpanLimits{
		AttributeValueLengthLimit:   telemetryValueLimit,
		AttributeCountLimit:         telemetryAttributeLimit,
		EventCountLimit:             telemetryAttributeLimit,
		LinkCountLimit:              telemetryAttributeLimit,
		AttributePerEventCountLimit: telemetryAttributeLimit,
		AttributePerLinkCountLimit:  telemetryAttributeLimit,
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithRawSpanLimits(limits),
		sdktrace.WithSpanProcessor(processor),
	)
	shutdown := func(ctx context.Context) error {
		// BatchSpanProcessor.Shutdown reports a queued export failure through the
		// global SDK handler instead of returning it. ForceFlush is required here
		// to keep the final export error on the service-owned shutdown path.
		return errors.Join(provider.ForceFlush(ctx), provider.Shutdown(ctx))
	}
	return provider, shutdown, nil
}

type multiSlogHandler struct {
	handlers []slog.Handler
}

func (h multiSlogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h multiSlogHandler) Handle(ctx context.Context, record slog.Record) error {
	var err error
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, record.Level) {
			err = errors.Join(err, handler.Handle(ctx, record.Clone()))
		}
	}
	return err
}

func (h multiSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = handler.WithAttrs(attrs)
	}
	return multiSlogHandler{handlers: handlers}
}

func (h multiSlogHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		handlers[i] = handler.WithGroup(name)
	}
	return multiSlogHandler{handlers: handlers}
}

type telemetryDiagnostics struct {
	logger *slog.Logger
	now    func() time.Time
	mu     sync.Mutex
	state  map[string]telemetryDiagnosticState
}

type telemetryDiagnosticState struct {
	last       time.Time
	count      uint64
	suppressed uint64
}

type managedLogExporter struct {
	transport   otlpTransport
	diagnostics *telemetryDiagnostics
}

func (e *managedLogExporter) Export(ctx context.Context, records []log.Record) error {
	request := transformLogRecords(records)
	if err := e.transport.uploadLogs(ctx, request); err != nil {
		if isFinalTelemetryExport(ctx) {
			return errors.New("logs telemetry export failed")
		}
		e.diagnostics.report("logs", "unavailable")
	}
	return nil
}

func (e *managedLogExporter) ForceFlush(context.Context) error {
	return nil
}

func (e *managedLogExporter) Shutdown(context.Context) error {
	return nil
}

type managedMetricExporter struct {
	transport   otlpTransport
	diagnostics *telemetryDiagnostics
}

func (e *managedMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(kind)
}

func (e *managedMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (e *managedMetricExporter) Export(ctx context.Context, data *metricdata.ResourceMetrics) error {
	request, err := transformResourceMetrics(data)
	if err == nil {
		err = e.transport.uploadMetrics(ctx, request)
	}
	if err != nil {
		if isFinalTelemetryExport(ctx) {
			return errors.New("metrics telemetry export failed")
		}
		e.diagnostics.report("metrics", "unavailable")
	}
	return nil
}

func (e *managedMetricExporter) ForceFlush(context.Context) error {
	return nil
}

func (e *managedMetricExporter) Shutdown(context.Context) error {
	return nil
}

type managedTraceClient struct {
	transport otlpTransport
}

func (c *managedTraceClient) Start(context.Context) error {
	return nil
}

func (c *managedTraceClient) Stop(context.Context) error {
	return nil
}

func (c *managedTraceClient) UploadTraces(ctx context.Context, spans []*tracepb.ResourceSpans) error {
	sanitizeResourceSpans(spans)
	return c.transport.uploadTraces(ctx, spans)
}

type managedTraceExporter struct {
	sdktrace.SpanExporter
	diagnostics *telemetryDiagnostics
}

func (e *managedTraceExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if err := e.SpanExporter.ExportSpans(ctx, spans); err != nil {
		if isFinalTelemetryExport(ctx) {
			return errors.New("traces telemetry export failed")
		}
		e.diagnostics.report("traces", "unavailable")
	}
	return nil
}

func (e *managedTraceExporter) Shutdown(ctx context.Context) error {
	return e.SpanExporter.Shutdown(ctx)
}

func isFinalTelemetryExport(ctx context.Context) bool {
	final, _ := ctx.Value(telemetryFinalExportKey{}).(bool)
	return final
}

func newTelemetryDiagnostics(writer io.Writer) *telemetryDiagnostics {
	return &telemetryDiagnostics{
		logger: slog.New(slog.NewJSONHandler(writer, nil)),
		now:    time.Now,
		state:  make(map[string]telemetryDiagnosticState),
	}
}

func (d *telemetryDiagnostics) report(signal, category string) {
	switch signal {
	case "logs", "metrics", "traces":
	default:
		signal = "unknown"
	}
	switch category {
	case "unavailable", "timeout", "rejected", "encoding":
	default:
		category = "unknown"
	}
	d.mu.Lock()
	now := d.now()
	state := d.state[signal]
	state.count++
	if !state.last.IsZero() && now.Sub(state.last) < time.Minute {
		state.suppressed++
		d.state[signal] = state
		d.mu.Unlock()
		return
	}
	suppressed := state.suppressed
	state.last, state.suppressed = now, 0
	d.state[signal] = state
	d.mu.Unlock()
	d.logger.Warn("telemetry export failed",
		"signal", signal,
		"category", category,
		"count", state.count,
		"suppressed", suppressed,
		"time", now.UTC().Format(time.RFC3339),
	)
}

type sanitizeSlogHandler struct {
	slog.Handler
}

func (h sanitizeSlogHandler) Handle(ctx context.Context, record slog.Record) error {
	record = record.Clone()
	record.Message = sanitizeTelemetryString(record.Message)
	attrs := make([]slog.Attr, 0, min(record.NumAttrs(), telemetryAttributeLimit))
	record.Attrs(func(attr slog.Attr) bool {
		if len(attrs) < telemetryAttributeLimit {
			if sanitized, ok := sanitizeSlogAttr(attr); ok {
				attrs = append(attrs, sanitized)
			}
		}
		return true
	})
	record = slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.AddAttrs(attrs...)
	return h.Handler.Handle(ctx, record)
}

func (h sanitizeSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	sanitized := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if value, ok := sanitizeSlogAttr(attr); ok {
			sanitized = append(sanitized, value)
		}
	}
	return sanitizeSlogHandler{Handler: h.Handler.WithAttrs(sanitized)}
}

func (h sanitizeSlogHandler) WithGroup(name string) slog.Handler {
	return sanitizeSlogHandler{Handler: h.Handler.WithGroup(sanitizeTelemetryString(name))}
}

func sanitizeSlogAttr(attr slog.Attr) (slog.Attr, bool) {
	attr.Value = attr.Value.Resolve()
	if prohibitedTelemetryKey(attr.Key) {
		return slog.Attr{}, false
	}
	attr.Key = truncateTelemetryString(attr.Key)
	switch attr.Value.Kind() {
	case slog.KindGroup:
		children := make([]slog.Attr, 0, min(len(attr.Value.Group()), telemetryAttributeLimit))
		for _, child := range attr.Value.Group() {
			if len(children) >= telemetryAttributeLimit {
				break
			}
			if value, ok := sanitizeSlogAttr(child); ok {
				children = append(children, value)
			}
		}
		attr.Value = slog.GroupValue(children...)
	case slog.KindString:
		attr.Value = slog.StringValue(sanitizeTelemetryString(attr.Value.String()))
	case slog.KindAny:
		attr.Value = slog.StringValue(sanitizeTelemetryString(fmt.Sprint(attr.Value.Any())))
	}
	return attr, true
}

func sanitizeTelemetryString(value string) string {
	if containsTelemetrySecretMarker(value) {
		return "[redacted]"
	}
	return truncateTelemetryString(value)
}

func truncateTelemetryString(value string) string {
	if len(value) <= telemetryValueLimit {
		return value
	}
	return value[:telemetryValueLimit]
}
