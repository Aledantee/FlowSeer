package fsconv

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// SpanError marks span failed with err: Error status with description, and
// error.type from ErrorTypeOf. It does nothing for a nil error, so a caller
// may pass the operation's result unchecked. Pass the error the caller of the
// operation receives, after any mapping to a protocol code.
func SpanError(span trace.Span, err error, description string) {
	if err == nil {
		return
	}
	span.SetAttributes(attribute.String(ErrorTypeKey, ErrorTypeOf(err)))
	span.SetStatus(codes.Error, description)
}
