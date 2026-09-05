package telemetry

import (
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// scopeName is this module's own instrumentation scope: a module that owns
// a scope names it itself rather than reusing the host service runtime's,
// per src/common/service's Tracer/Meter doc comments and
// docs/conventions/observability.md's Instrumentation Scope rule.
const scopeName = "go.aledante.io/FlowSeer/src/modules/localnet/access"

// View bundles the instrumentation a caller supplies once and every other
// internal package reads from thereafter. The zero value is not usable;
// construct with [NewView].
type View struct {
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
	logger     *slog.Logger

	operationDuration metric.Float64Histogram
	routeSelections   metric.Int64Counter
}

// ViewConfig supplies the providers a [View] builds its own scoped
// instruments from. A nil TracerProvider or MeterProvider yields a no-op
// scope; a nil Propagator yields one that extracts and injects nothing; a
// nil Logger discards every record. Construct with keyed fields.
type ViewConfig struct {
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	Propagator     propagation.TextMapPropagator
	Logger         *slog.Logger
}

// ErrCodeInstrument identifies a failure constructing one of this module's
// metric instruments.
var ErrCodeInstrument = errs.NewCode("telemetry/instrument")

// NewView constructs a [View] with its own scoped tracer and meter, per the
// observability convention's requirement that a scope-owning module name
// itself and set semconv.SchemaURL rather than reusing the host runtime's
// scope.
func NewView(cfg ViewConfig) (*View, error) {
	tracerProvider := cfg.TracerProvider
	if tracerProvider == nil {
		tracerProvider = tracenoop.NewTracerProvider()
	}
	meterProvider := cfg.MeterProvider
	if meterProvider == nil {
		meterProvider = metricnoop.NewMeterProvider()
	}
	propagator := cfg.Propagator
	if propagator == nil {
		propagator = propagation.NewCompositeTextMapPropagator()
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	tracer := tracerProvider.Tracer(scopeName, trace.WithSchemaURL(semconv.SchemaURL))
	meter := meterProvider.Meter(scopeName, metric.WithSchemaURL(semconv.SchemaURL))

	operationDuration, err := meter.Float64Histogram(
		"flowseer.device.operation.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Elapsed time from lane admission to a device operation's terminal phase"),
	)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeInstrument).Msg("build operation duration histogram")
	}

	routeSelections, err := meter.Int64Counter(
		"flowseer.device.route.selections",
		metric.WithUnit("{selection}"),
		metric.WithDescription("Routes selected to answer or fall through one device operation"),
	)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeInstrument).Msg("build route selections counter")
	}

	return &View{
		tracer:            tracer,
		propagator:        propagator,
		logger:            logger,
		operationDuration: operationDuration,
		routeSelections:   routeSelections,
	}, nil
}

// Propagator returns the configured text-map propagator, for a caller that
// extracts a traceparent from an inbound envelope or injects one into an
// outbound one.
func (v *View) Propagator() propagation.TextMapPropagator {
	if v == nil {
		return propagation.NewCompositeTextMapPropagator()
	}
	return v.propagator
}
