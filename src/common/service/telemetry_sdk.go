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
	"unicode/utf8"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
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
	telemetryLogLevel        = slog.LevelInfo
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

// telemetryOwner exclusively owns managed pipelines and borrowed-shutdown
// callbacks for one run. Module views may borrow capabilities but cannot close
// them.
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

// shutdown closes each owned dependency once under one cancellation-independent
// deadline and returns the same combined result to every caller.
func (o *telemetryOwner) shutdown(ctx context.Context) error {
	o.once.Do(func() {
		o.err = runTelemetryShutdowns(ctx, o.shutdowns, telemetryShutdownTimeout)
	})
	return o.err
}

// runTelemetryShutdowns ignores prior cancellation and divides one deadline
// among the remaining callbacks so an early shutdown cannot consume every
// later callback's opportunity to run.
func runTelemetryShutdowns(ctx context.Context, shutdowns []telemetryShutdown, timeout time.Duration) error {
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	shutdownCtx = context.WithValue(shutdownCtx, telemetryFinalExportKey{}, true)

	var err error
	for i, shutdown := range shutdowns {
		deadline, _ := shutdownCtx.Deadline()
		remaining := time.Until(deadline)
		entryTimeout := remaining / time.Duration(len(shutdowns)-i)
		entryCtx, entryCancel := context.WithTimeout(shutdownCtx, max(entryTimeout, time.Nanosecond))
		err = errors.Join(err, shutdown(entryCtx))
		entryCancel()
	}
	return err
}

// newRunTelemetry constructs one run's local and exported signal graph. Any
// partial construction failure unwinds already-created dependencies in reverse
// ownership order.
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

	localHandler := traceLogHandler{Handler: config.localLogger.Handler().WithAttrs(serviceIdentityLogAttrs(identity))}
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
			return unwind(fmt.Errorf("create telemetry resource: %w", err))
		}
		var closeTransport telemetryShutdown
		transport, closeTransport, err = factories.newTransport(ctx, config.connection)
		if err != nil {
			return unwind(fmt.Errorf("create telemetry transport: %w", err))
		}
		owner.shutdowns = prependShutdown(owner.shutdowns, closeTransport)
	}

	diagnostics := newServiceTelemetryDiagnostics(os.Stderr, identity)
	var closeLogs, closeMetrics, closeTraces telemetryShutdown
	switch config.logs.backing {
	case signalManaged:
		handler, shutdown, err := factories.newLogs(res, transport, diagnostics, config.connection)
		if err != nil {
			return unwind(fmt.Errorf("create telemetry logs: %w", err))
		}
		closeLogs = shutdown
		owner.shutdowns = prependShutdown(owner.shutdowns, closeLogs)
		exportHandler := minimumSlogLevelHandler{Handler: handler, minimum: telemetryLogLevel}
		logHandler = multiSlogHandler{handlers: []slog.Handler{localHandler, sanitizeSlogHandler{Handler: exportHandler}}}
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

// prependShutdown keeps dependents ahead of their dependencies in shutdown
// order.
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
	return resource.NewWithAttributes(
		semconv.SchemaURL,
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
	exporter := &managedLogExporter{
		exportGuard: exportGuard{signal: "logs"},
		transport:   transport,
		diagnostics: diagnostics,
	}
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
		otelslog.WithSchemaURL(semconv.SchemaURL),
	)
	shutdown := func(ctx context.Context) error {
		exporter.beginShutdown(ctx)
		return errors.Join(provider.Shutdown(ctx), exporter.finalError())
	}
	return handler, shutdown, nil
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
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: "*"},
			sdkmetric.Stream{AttributeFilter: managedMetricAttributeFilter},
		)),
	)
	return provider, provider.Shutdown, nil
}

func newManagedTraceProvider(
	res *resource.Resource,
	transport otlpTransport,
	diagnostics *telemetryDiagnostics,
	config normalizedOTLPConnection,
) (trace.TracerProvider, telemetryShutdown, error) {
	client := &managedTraceClient{transport: transport}
	exporter, err := otlptrace.New(context.Background(), client)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize trace exporter: %w", err)
	}
	guarded := &managedTraceExporter{
		SpanExporter: exporter,
		exportGuard:  exportGuard{signal: "traces"},
		diagnostics:  diagnostics,
	}
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
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(config.traceSampleRatio))),
		sdktrace.WithRawSpanLimits(limits),
		sdktrace.WithSpanProcessor(processor),
	)
	shutdown := func(ctx context.Context) error {
		guarded.beginShutdown(ctx)
		return errors.Join(provider.Shutdown(ctx), guarded.finalError())
	}
	return provider, shutdown, nil
}

type minimumSlogLevelHandler struct {
	slog.Handler
	minimum slog.Level
}

func (h minimumSlogLevelHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.minimum && h.Handler.Enabled(ctx, level)
}

func (h minimumSlogLevelHandler) Handle(ctx context.Context, record slog.Record) error {
	return h.Handler.Handle(ctx, record)
}

func (h minimumSlogLevelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return minimumSlogLevelHandler{Handler: h.Handler.WithAttrs(attrs), minimum: h.minimum}
}

func (h minimumSlogLevelHandler) WithGroup(name string) slog.Handler {
	return minimumSlogLevelHandler{Handler: h.Handler.WithGroup(name), minimum: h.minimum}
}

// multiSlogHandler fans one record out to independently enabled handlers and
// clones it so one branch cannot mutate another branch's view.
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

// telemetryDiagnostics rate-limits export warnings independently per signal.
// mu guards state and calls to now.
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

// exportGuard tracks the in-flight exports of one managed exporter so shutdown
// can cancel them, and remembers whether the final drain failed. signal names
// the OTLP signal in diagnostics and in the shutdown error; the remaining zero
// values are ready to use. shutdownCtx is stored because the SDK calls Export
// on its own schedule, with no context of the shutdown that must adopt it.
type exportGuard struct {
	signal      string
	mu          sync.Mutex
	shutdownCtx context.Context
	active      map[uint64]context.CancelFunc
	nextExport  uint64
	finalFailed bool
}

// startExport derives the context for one export and returns the function that
// ends it. Once shutdown has begun the export runs under the shutdown context
// instead of the caller's, so a drain outlives the caller's cancellation.
func (g *exportGuard) startExport(ctx context.Context) (context.Context, context.CancelFunc) {
	g.mu.Lock()
	if g.shutdownCtx != nil {
		ctx = g.shutdownCtx
	}
	if g.active == nil {
		g.active = make(map[uint64]context.CancelFunc)
	}
	exportCtx, cancel := context.WithCancel(ctx)
	id := g.nextExport
	g.nextExport++
	g.active[id] = cancel
	g.mu.Unlock()
	return exportCtx, func() {
		cancel()
		g.mu.Lock()
		delete(g.active, id)
		g.mu.Unlock()
	}
}

// beginShutdown adopts ctx for later exports and cancels the in-flight ones.
func (g *exportGuard) beginShutdown(ctx context.Context) {
	g.mu.Lock()
	g.shutdownCtx = ctx
	for _, cancel := range g.active {
		cancel()
	}
	g.mu.Unlock()
}

// recordFinalFailureIfShuttingDown classifies one export failure: final reports
// whether it belongs to a drain, and suppress whether the caller should swallow
// it because finalError already carries it.
func (g *exportGuard) recordFinalFailureIfShuttingDown(ctx context.Context) (final, suppress bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.shutdownCtx != nil {
		g.finalFailed = true
		return true, true
	}
	return isFinalTelemetryExport(ctx), false
}

// finalError reports the drain failure recorded during shutdown, or nil.
func (g *exportGuard) finalError() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.finalFailed {
		return g.exportFailure()
	}
	return nil
}

// exportFailure is the stable, signal-scoped error returned for a failed drain.
func (g *exportGuard) exportFailure() error {
	return fmt.Errorf("%s telemetry export failed", g.signal)
}

// managedLogExporter reports and absorbs routine export failures so telemetry
// cannot fail service work. A final drain returns a stable shutdown error.
type managedLogExporter struct {
	exportGuard
	transport   otlpTransport
	diagnostics *telemetryDiagnostics
}

func (e *managedLogExporter) Export(ctx context.Context, records []log.Record) error {
	exportCtx, finish := e.startExport(ctx)
	defer finish()
	request := transformLogRecords(records)
	if err := e.transport.uploadLogs(exportCtx, request); err != nil {
		final, suppress := e.recordFinalFailureIfShuttingDown(ctx)
		if final {
			if suppress {
				return nil
			}
			return e.exportFailure()
		}
		e.diagnostics.report(ctx, e.signal, telemetryExportFailureCategory(err))
	}
	return nil
}

func (e *managedLogExporter) ForceFlush(context.Context) error {
	return nil
}

func (e *managedLogExporter) Shutdown(context.Context) error {
	return nil
}

// managedMetricExporter reports and absorbs routine export failures so
// telemetry cannot fail service work. A final drain returns a stable shutdown
// error.
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
	if !hasMetricData(data) {
		return nil
	}
	request, err := transformResourceMetrics(data)
	if err == nil {
		err = e.transport.uploadMetrics(ctx, request)
	}
	if err != nil {
		if isFinalTelemetryExport(ctx) {
			return errors.New("metrics telemetry export failed")
		}
		e.diagnostics.report(ctx, "metrics", telemetryExportFailureCategory(err))
	}
	return nil
}

func hasMetricData(data *metricdata.ResourceMetrics) bool {
	if data == nil {
		return false
	}
	for _, scope := range data.ScopeMetrics {
		if len(scope.Metrics) != 0 {
			return true
		}
	}
	return false
}

func (e *managedMetricExporter) ForceFlush(context.Context) error {
	return nil
}

func (e *managedMetricExporter) Shutdown(context.Context) error {
	return nil
}

func managedMetricAttributeFilter(value attribute.KeyValue) bool {
	if prohibitedTelemetryKey(string(value.Key)) {
		return false
	}
	return !telemetryAttributeValueContainsSecret(value.Value)
}

func telemetryAttributeValueContainsSecret(value attribute.Value) bool {
	switch value.Type() {
	case attribute.STRING:
		return containsTelemetrySecretMarker(value.AsString())
	case attribute.STRINGSLICE:
		for _, member := range value.AsStringSlice() {
			if containsTelemetrySecretMarker(member) {
				return true
			}
		}
	case attribute.SLICE:
		for _, member := range value.AsSlice() {
			if telemetryAttributeValueContainsSecret(member) {
				return true
			}
		}
	case attribute.MAP:
		for _, member := range value.AsMap() {
			if prohibitedTelemetryKey(string(member.Key)) || telemetryAttributeValueContainsSecret(member.Value) {
				return true
			}
		}
	}
	return false
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

// managedTraceExporter reports and absorbs routine export failures so
// telemetry cannot fail service work. Shutdown cancels active exports and
// routes any final flush failure through the owner's shutdown result.
type managedTraceExporter struct {
	sdktrace.SpanExporter
	exportGuard
	diagnostics *telemetryDiagnostics
}

func (e *managedTraceExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	exportCtx, finish := e.startExport(ctx)
	defer finish()
	if err := e.SpanExporter.ExportSpans(exportCtx, spans); err != nil {
		final, suppress := e.recordFinalFailureIfShuttingDown(ctx)
		if final {
			if suppress {
				return nil
			}
			return e.exportFailure()
		}
		e.diagnostics.report(ctx, e.signal, telemetryExportFailureCategory(err))
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

func newServiceTelemetryDiagnostics(writer io.Writer, identity Identity) *telemetryDiagnostics {
	diagnostics := newTelemetryDiagnostics(writer)
	diagnostics.logger = slog.New(diagnostics.logger.Handler().WithAttrs(serviceIdentityLogAttrs(identity)))
	return diagnostics
}

// report emits at most one warning per signal per minute and includes the
// number suppressed since the preceding warning. ctx is the export context, so
// the warning carries the export's trace correlation.
func (d *telemetryDiagnostics) report(ctx context.Context, signal, category string) {
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
	d.logger.WarnContext(ctx, "telemetry export failed",
		"flowseer.telemetry.signal", signal,
		"flowseer.telemetry.category", category,
		"flowseer.telemetry.count", state.count,
		"flowseer.telemetry.suppressed", suppressed,
	)
}

// sanitizeSlogHandler bounds and redacts the managed-export branch without
// changing trusted local records.
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
		return slog.Attr{}, false
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
	limit := telemetryValueLimit
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}
