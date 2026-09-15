// Package spawn runs first-party goroutines under one supervised entry
// point. It exists so that a panic inside a spawned goroutine — which Go
// never propagates to the spawning frame — stops only the unit of work that
// panicked instead of the process, and is reported rather than silent.
package spawn

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// panicAttrKey is the stable key under which the recovered panic value is
// attached to the reported error and to the span event's attributes.
const panicAttrKey = "panic"

// labelAttrKey is the log attribute carrying the caller-supplied label.
const labelAttrKey = "flowseer.spawn.label"

// spanEventName names the span event added for a recovered panic.
const spanEventName = "flowseer.spawn.panic"

// Option configures [Go].
type Option func(*options)

type options struct {
	sink func(error)
}

// ReportTo also hands the recovered error to sink.
func ReportTo(sink func(error)) Option {
	return func(o *options) {
		o.sink = sink
	}
}

// Go runs fn in a new goroutine, recovering a panic and reporting it as a
// structured error labeled by the unit of work fn performs.
//
// Go returns as soon as the goroutine is started; it does not join fn. A
// caller that needs to wait for fn keeps its own sync.WaitGroup, with
// wg.Add(1) before this call and wg.Done() deferred inside fn — Go does not
// restart, back off, or cancel siblings either, since that policy belongs to
// whatever owns the unit of work.
//
// A recovered panic is always reported through a log record at error level,
// and through a span event when ctx carries a recording span. When opts
// includes [ReportTo], the same error also reaches sink.
func Go(ctx context.Context, label string, fn func(), opts ...Option) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	go func() {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}

			err := panicError(label, recovered)
			report(ctx, label, err)

			if o.sink != nil {
				o.sink(err)
			}
		}()

		fn()
	}()
}

// panicError builds the structured error for a recovered panic value. A
// value that is already an error is wrapped, so errors.Is and errors.As
// reach it; either way the recovered value is attached under panicAttrKey
// and the message names label.
func panicError(label string, recovered any) error {
	b := errs.New()
	if cause, ok := recovered.(error); ok {
		b = errs.From(cause)
	}

	return b.Attr(panicAttrKey, recovered).Msgf("%s panicked", label)
}

// report emits the observability floor for a recovered panic: one log
// record at error level, and a span event when ctx carries a recording
// span. See docs/conventions/observability.md.
func report(ctx context.Context, label string, err error) {
	slog.ErrorContext(ctx, "goroutine panicked",
		slog.String(labelAttrKey, label),
		slog.Any("error", err),
	)

	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		span.AddEvent(spanEventName, trace.WithAttributes(
			attribute.String(labelAttrKey, label),
		))
	}
}
