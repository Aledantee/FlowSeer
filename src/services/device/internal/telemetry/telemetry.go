// Package telemetry is the device service's own instrumentation scope.
//
// Central detects drift, so the durable record and the signal are both
// central's and neither is the edge's any more. The scope is named here rather
// than borrowed from the service runtime, and the instruments are built once
// by the host, because an emitter that constructed its own provider is how the
// access module ended up with nine named events, three with no production
// producer and some at the wrong level with unbounded labels.
package telemetry

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// scopeName is the device service's instrumentation scope.
const scopeName = "go.aledante.io/FlowSeer/src/services/device"

// ErrCodeInstrument is a failure constructing one of this service's metric
// instruments.
var ErrCodeInstrument = errs.NewCode("telemetry/instrument")

// Attribute keys. The management mode is the only one a metric carries: it has
// two values. The device and the interface identify one occurrence and belong
// on the event; the descriptions are operator-written text about a customer's
// topology and belong on the event and nowhere else.
const (
	attrKeyDevice         = "flowseer.device.id"
	attrKeyInterface      = "flowseer.device.interface"
	attrKeyExpected       = "flowseer.device.description.expected"
	attrKeyObserved       = "flowseer.device.description.observed"
	attrKeyManagementMode = attribute.Key("flowseer.device.management_mode")
)

// View is the instrumentation the host builds once and the service's internal
// packages emit through. A nil *View is safe to call and emits nothing, which
// is what a test that does not care about signals passes.
type View struct {
	logger          *slog.Logger
	driftDetections metric.Int64Counter
}

// ViewConfig supplies what a [View] is built from. A nil MeterProvider records
// no measurements; a nil Logger discards every record. Construct with keyed
// fields.
type ViewConfig struct {
	MeterProvider metric.MeterProvider
	Logger        *slog.Logger
}

// NewView builds the service's scope and its instruments.
func NewView(cfg ViewConfig) (*View, error) {
	meterProvider := cfg.MeterProvider
	if meterProvider == nil {
		meterProvider = metricnoop.NewMeterProvider()
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	meter := meterProvider.Meter(scopeName, metric.WithSchemaURL(semconv.SchemaURL))
	driftDetections, err := meter.Int64Counter(
		"flowseer.device.drift.detections",
		metric.WithUnit("{detection}"),
		metric.WithDescription("Managed interfaces found carrying a description other than the one central expects, counted when the poll judges them"),
	)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeInstrument).Msg("build drift detection counter")
	}

	return &View{logger: logger, driftDetections: driftDetections}, nil
}

// DriftDetected reports one managed interface found carrying something other
// than what central expects.
//
// The event carries the device, the interface, and both descriptions, because
// an operator reading it has to see what changed without going to fetch the
// record. The counter carries only the management mode, which decides what
// central does about a difference and has two values: the interface name is
// per device and the descriptions are unbounded operator-written text, so
// neither can be a series.
func (v *View) DriftDetected(ctx context.Context, deviceID, iface, expected, observed string, mode inventoryv1.DeviceManagementMode) {
	if v == nil {
		return
	}

	v.logger.LogAttrs(ctx, slog.LevelInfo, "drift detected",
		slog.String("otel.event.name", "flowseer.device.drift.detected"),
		slog.String(attrKeyDevice, deviceID),
		slog.String(attrKeyInterface, iface),
		slog.String(attrKeyExpected, expected),
		slog.String(attrKeyObserved, observed),
	)
	v.driftDetections.Add(ctx, 1, metric.WithAttributes(attrKeyManagementMode.String(mode.String())))
}
