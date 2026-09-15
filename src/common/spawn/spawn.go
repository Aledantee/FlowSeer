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
// attached to the reported error. The span event does not carry it: a panic
// value is unbounded, and a span attribute is not the place for it.
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
// caller that needs to wait for fn keeps its own sync.WaitGroup — Go does not
// restart, back off, or cancel siblings either, since that policy belongs to
// whatever owns the unit of work.
//
// A recovered panic is always reported through a log record at error level,
// and through a span event when ctx carries a recording span. When opts
// includes [ReportTo], the same error also reaches sink.
//
// # What fn owes its caller on the panic path
//
// A panic skips everything fn had left to do, and Go's recover runs after
// every deferred call fn registered. Two consequences bind every call site,
// and both have produced real defects here:
//
//   - A completion deferred inside fn — wg.Done, close(ch), [pump.Pump.Done]
//     — runs during the unwind, before the sink. Whatever the caller learns
//     from that completion, it learns before the error is recorded, so a
//     consumer that drains to a closed channel and then reads an error sees
//     none. Put the completion on fn's normal path and in the sink, so
//     exactly one of them reaches it, or join by counted receive rather than
//     by a WaitGroup whose Done fn defers.
//   - A lock fn holds is released only by a deferred Unlock. An explicit
//     Unlock on fn's normal path is skipped by the panic, and the mutex stays
//     held for the life of the process — a hang where the unrecovered panic
//     was a crash, which is worse.
//
// sink is called with the goroutine already unwound. It must not block: no
// further report follows it, so a sink parked on a channel strands the
// goroutine silently. A panic inside sink is recovered and reported on its
// own, because a helper whose purpose is that a panic costs one unit of work
// cannot let a reporting path take the process down — but the error the sink
// was handed does not reach wherever sink was taking it.
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
				reportSinkPanic(ctx, label, o.sink, err)
			}
		}()

		fn()
	}()
}

// reportSinkPanic calls sink and recovers a panic raised inside it. Several
// sinks re-enter the component that has just panicked, so this is a reachable
// path, not defense in depth: without it the recovery itself would take the
// process down, which is the outcome Go exists to prevent.
func reportSinkPanic(ctx context.Context, label string, sink func(error), err error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}

		report(ctx, label+" sink", panicError(label+" sink", recovered))
	}()

	sink(err)
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
