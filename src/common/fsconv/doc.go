// Package fsconv holds the telemetry vocabulary more than one FlowSeer tree
// emits: the shared flowseer.* attribute keys and their slog constructors, the
// one error.type classifier, the otel.event.name bridge attribute, and the
// helper that marks a span failed. Two packages that take a key from here
// cannot disagree on its spelling, and two that classify an error here cannot
// disagree on its value.
//
// A key that only one package emits stays a private constant beside its
// instrument. The README states the admission rule.
package fsconv
