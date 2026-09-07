package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

// operationSpanName and routeSpanName are the two low-cardinality span names
// this module emits. Never put an object or request ID in either.
const (
	operationSpanName = "flowseer.device.operation"
	routeSpanName     = "flowseer.device.route"
)

// EndFunc closes the span it was returned alongside. errPtr, if non-nil and
// pointing at a non-nil error when EndFunc runs, marks the span as failed
// with a bounded error.type; classify passes the raw error through the
// caller's own classifier so no raw error text reaches the span.
type EndFunc func(errPtr *error, classify func(error) string)

func end(span trace.Span) EndFunc {
	return func(errPtr *error, classify func(error) string) {
		defer span.End()
		if errPtr == nil || *errPtr == nil {
			return
		}
		errType := "unknown"
		if classify != nil {
			errType = classify(*errPtr)
		}
		span.SetAttributes(semconv.ErrorTypeKey.String(errType))
		span.SetStatus(codes.Error, "operation failed")
	}
}

// StartOperation starts the flowseer.device.operation span: an INTERNAL span
// covering one admitted lane item from admission to its terminal phase. It
// is not itself a remote call, so it carries INTERNAL kind; the
// flowseer.device.route span nested under it is the actual outbound call.
// operationClass is a low-cardinality name such as "interface_description"
// or "interface_read", never an object ID.
func (v *View) StartOperation(ctx context.Context, operationClass string) (context.Context, EndFunc) {
	if v == nil {
		return ctx, func(*error, func(error) string) {}
	}
	ctx, span := v.tracer.Start(ctx, operationSpanName,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attrOperationClass(operationClass)),
	)
	return ctx, end(span)
}

// NoteBaselineUnavailable marks the current operation span as having run
// without a pre-mutation baseline, with a bounded reason.
//
// It is an attribute rather than an event because it is not a thing that
// happened to the device, it is a property of this operation: recovery for
// it cannot corroborate, so the mutation can only verify or abandon. That
// degradation is otherwise invisible — a mutation with no baseline behaves
// exactly like one whose device was simply never going to agree — and an
// invisible degradation is one that can be permanent without anyone
// noticing, which is how the baseline came to be missing for every mutation
// in the first place.
func (v *View) NoteBaselineUnavailable(ctx context.Context, reason string) {
	if v == nil {
		return
	}
	trace.SpanFromContext(ctx).SetAttributes(attrKeyBaselineUnavailable.String(reason))
}

// StartRoute starts the flowseer.device.route span for one route attempt: a
// CLIENT span, since this is the actual outbound SNMP or SSH call to the
// device. On the ordinary path it is a child of the current context's span
// (the operation span). On a recovery retry, pass linkTo (the original
// operation span's context) so the retry's route span links back to it
// instead of parenting under a span that may have already ended or that a
// later, independent attempt should not stretch — per
// docs/solutions/architecture-patterns/trace-context-relays-through-trace-disabled-modules.md's
// rule that work which can run later than the caller returns is a link, not
// a child.
func (v *View) StartRoute(ctx context.Context, route string, linkTo trace.SpanContext) (context.Context, EndFunc) {
	if v == nil {
		return ctx, func(*error, func(error) string) {}
	}
	opts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrRouteKind(route)),
	}
	if linkTo.IsValid() {
		opts = append(opts, trace.WithLinks(trace.Link{SpanContext: linkTo}))
	}
	ctx, span := v.tracer.Start(ctx, routeSpanName, opts...)
	return ctx, end(span)
}
