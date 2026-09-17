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
	"errors"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/semconv/v1.43.0/rpcconv"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// scopeName is the device service's instrumentation scope.
const scopeName = "go.aledante.io/FlowSeer/src/services/device"

// ErrCodeInstrument is a failure constructing one of this service's metric
// instruments. The code names the device service's scope rather than this
// package, because the access module's telemetry package already owns
// "telemetry/instrument" and a code is unique across the repository — two
// packages of the same name are exactly where that collides.
var ErrCodeInstrument = errs.NewCode("device-telemetry/instrument")

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
	attrKeyDriftOutcome   = attribute.Key("flowseer.device.drift.outcome")
)

// DriftOutcome is what central did about a difference it found. It is a closed
// set so it can be a metric label, and it is on the counter because without it
// the counter measures two different things at once.
//
// Every arm that admits an intent takes the device's lane, and the poll skips
// a device whose lane is held — so an admitted detection increments once and
// then stops until an operator resolves it. The arm that admits nothing does
// not self-limit: the lane stays free and the same unresolved condition
// increments on every pass, forever on a device no read ever reaches. Without
// this label a drift dashboard reads high for a reason that is not drift, and
// the rate it shows is the poll interval.
type DriftOutcome string

const (
	// DriftOutcomeDispatched is a reconciliation intent admitted and left
	// dispatchable: central is putting the expected description back.
	DriftOutcomeDispatched DriftOutcome = "dispatched"
	// DriftOutcomeHeld is an intent admitted and blocked for the operator to
	// accept, restore, or replace.
	DriftOutcomeHeld DriftOutcome = "held"
	// DriftOutcomeNoEpoch is a detection central cannot act on because it has
	// not learned the device's firmware epoch, which an intent must name.
	// It repeats every pass until a read reports one, so it is a condition to
	// read as a level rather than a rate.
	DriftOutcomeNoEpoch DriftOutcome = "no_epoch"
)

// View is the instrumentation the host builds once and the service's internal
// packages emit through. A nil *View is safe to call and emits nothing, which
// is what a test that does not care about signals passes.
type View struct {
	logger          *slog.Logger
	driftDetections metric.Int64Counter
	rpcDuration     rpcconv.ServerCallDuration
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

	rpcDuration, err := rpcconv.NewServerCallDuration(meter)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeInstrument).Msg("build rpc server duration histogram")
	}

	return &View{logger: logger, driftDetections: driftDetections, rpcDuration: rpcDuration}, nil
}

// RecordRPC records one served call's duration.
//
// This is the aggregate view of the API, and it is what makes it safe to log a
// refused call at DEBUG. A refusal is an answer the caller acted on, so a line
// per refusal buries the failures nobody chose; but "how often is this RPC
// refused, and with what" is a real operational question, and it is a metric's
// question rather than a log's. Someone reading the interceptor's DEBUG level
// as an oversight should read this first.
//
// errType is the classified error for a failed call and empty for one that
// succeeded, so the histogram's count is the denominator every failure rate is
// taken over. Both attributes are closed sets: the procedures this service
// serves, and the codes it declares.
func (v *View) RecordRPC(ctx context.Context, procedure string, seconds float64, errType string) {
	if v == nil {
		return
	}

	attrs := []attribute.KeyValue{semconv.RPCMethodKey.String(RPCMethod(procedure))}
	if errType != "" {
		attrs = append(attrs, semconv.ErrorTypeKey.String(errType))
	}
	v.rpcDuration.Record(ctx, seconds, rpcconv.SystemNameConnectrpc, attrs...)
}

// DriftDetected reports one managed interface found carrying something other
// than what central expects, and what central did about it.
//
// The event carries the device, the interface, and both descriptions, because
// an operator reading it has to see what changed without going to fetch the
// record. The counter carries the management mode and the outcome, four
// combinations in all: the interface name is per device and the descriptions
// are unbounded operator-written text, so neither can be a series.
func (v *View) DriftDetected(
	ctx context.Context,
	deviceID, iface, expected, observed string,
	mode inventoryv1.DeviceManagementMode,
	outcome DriftOutcome,
) {
	if v == nil {
		return
	}

	v.logger.LogAttrs(ctx, slog.LevelInfo, "drift detected",
		slog.String("otel.event.name", "flowseer.device.drift.detected"),
		slog.String(attrKeyDevice, deviceID),
		slog.String(attrKeyInterface, iface),
		slog.String(attrKeyExpected, expected),
		slog.String(attrKeyObserved, observed),
		slog.String(string(attrKeyDriftOutcome), string(outcome)),
	)
	v.driftDetections.Add(ctx, 1, metric.WithAttributes(
		attrKeyManagementMode.String(mode.String()),
		attrKeyDriftOutcome.String(string(outcome)),
	))
}

// RPCMethod is the value semconv's rpc.method takes for a Connect procedure.
//
// In semconv v1.43.0 — the version this repository pins — rpc.method is "the
// fully-qualified logical name of the method from the RPC interface
// perspective", and its examples are "com.example.ExampleService/exampleMethod"
// and "EchoService/Echo". There is no rpc.service attribute in that version at
// all. So the service-qualified name belongs here, whole, and the only thing
// that needed correcting was the leading slash: Connect's Procedure is
// "/flowseer.api.device.v1.DeviceService/ApplyInterfaceDescription" and the
// convention's vocabulary has no leading slash.
//
// Recorded because a review read this as the older convention, where
// rpc.method is the bare method name and rpc.service carries the service, and
// splitting the procedure to match that would have written a value the pinned
// version does not define and dropped half of one it does. Check the version
// in go.mod before changing this.
func RPCMethod(procedure string) string {
	return strings.TrimPrefix(procedure, "/")
}

// ErrorType classifies a failure for semconv's error.type: the error's own
// code where it has one, and the two context causes by name where it does
// not. Bounded on purpose, because it becomes a metric dimension downstream,
// and never the error's own message, which carries whatever the transport put
// in it.
func ErrorType(err error) string {
	if code, ok := errs.CodeOf(err); ok {
		return string(code)
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "context.deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "context.canceled"
	default:
		return "unknown"
	}
}
