package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// Attribute keys this package's metrics and spans draw from. Every
// non-standard key is namespaced flowseer.device.*; error.type is the one
// standard key, defined by semconv and never prefixed. No metric recorded by
// this package carries more than two of these at once, per
// docs/conventions/observability.md's cardinality rule and the task's
// explicit two-attribute cap.
const (
	attrKeyOperation = attribute.Key("flowseer.device.operation")
	attrKeyRoute     = attribute.Key("flowseer.device.route")
	attrKeyOutcome   = attribute.Key("flowseer.device.outcome")
)

func attrOperationClass(operationClass string) attribute.KeyValue {
	return attrKeyOperation.String(operationClass)
}

func attrRouteKind(route string) attribute.KeyValue {
	return attrKeyRoute.String(route)
}

// Outcome is a low-cardinality final result this package's metrics record.
// It is deliberately a closed set, not a raw error, so the metric's
// cardinality stays bounded.
type Outcome string

const (
	// OutcomeSuccess marks a route selection or operation as complete.
	OutcomeSuccess Outcome = "success"
	// OutcomeFailure marks a route selection or operation as failed.
	OutcomeFailure Outcome = "failure"
)

// RecordOperationDuration records one flowseer.device.operation.duration
// observation. errType is the classified error.type for a failed operation
// and must be empty for a successful one, keeping the attribute set to at
// most two members: operation class always, error.type only on failure.
func (v *View) RecordOperationDuration(ctx context.Context, operationClass string, seconds float64, errType string) {
	if v == nil {
		return
	}
	attrs := []attribute.KeyValue{attrOperationClass(operationClass)}
	if errType != "" {
		attrs = append(attrs, semconv.ErrorTypeKey.String(errType))
	}
	v.operationDuration.Record(ctx, seconds, metric.WithAttributes(attrs...))
}

// RecordRouteSelection increments flowseer.device.route.selections for one
// route attempt's outcome: route kind and outcome, never more.
func (v *View) RecordRouteSelection(ctx context.Context, route string, outcome Outcome) {
	if v == nil {
		return
	}
	v.routeSelections.Add(ctx, 1, metric.WithAttributes(
		attrRouteKind(route),
		attrKeyOutcome.String(string(outcome)),
	))
}
