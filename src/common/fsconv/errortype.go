package fsconv

import (
	"context"
	"errors"
	"log/slog"

	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrorTypeKey is the standard error.type attribute key.
const ErrorTypeKey = string(semconv.ErrorTypeKey)

// ErrorTypeOther is the error.type value for an error the classifier cannot
// name. It is a variable because semconv defines it as an attribute value,
// which is not a constant expression.
var ErrorTypeOther = semconv.ErrorTypeOther.Value.AsString()

// ErrorTypeOf classifies err for error.type. It returns the empty string for a
// nil error, the errs code when err carries one, context.deadline_exceeded or
// context.canceled for the two context causes, and ErrorTypeOther otherwise.
// The value is bounded because it becomes a metric dimension downstream, so it
// never carries the error's message or Go type.
func ErrorTypeOf(err error) string {
	if err == nil {
		return ""
	}
	if code, ok := errs.CodeOf(err); ok {
		return code.String()
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "context.deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "context.canceled"
	default:
		return ErrorTypeOther
	}
}

// ErrorType returns the error.type attribute for err, classified by
// ErrorTypeOf.
func ErrorType(err error) slog.Attr { return slog.String(ErrorTypeKey, ErrorTypeOf(err)) }

// ErrorTypeString returns the error.type attribute for a value a caller
// already classified, such as a local wrapper's result or a refusal code.
func ErrorTypeString(value string) slog.Attr { return slog.String(ErrorTypeKey, value) }
