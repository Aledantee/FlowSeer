package fsconv

import (
	"log/slog"

	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// The shared flowseer.* attribute keys. They are untyped so a span or metric
// caller can pass one to attribute.String without a second constructor.
const (
	// DeviceIDKey names the device an occurrence is about.
	DeviceIDKey = "flowseer.device.id"
	// DeviceSequenceKey names a position in a device's operation sequence.
	DeviceSequenceKey = "flowseer.device.sequence"
	// EdgeIDKey names the edge an occurrence is about or came from.
	EdgeIDKey = "flowseer.edge.id"
	// TenantIDKey names the ambient tenant.
	TenantIDKey = "flowseer.tenant.id"
	// CaptureSessionIDKey names a capture session.
	CaptureSessionIDKey = "flowseer.capture.session.id"
	// CaptureChunkFirstSequenceKey names the first sequence a capture chunk
	// carries.
	CaptureChunkFirstSequenceKey = "flowseer.capture.chunk.first_sequence"
)

// eventNameKey is the bridge attribute the shared log pipeline maps to a
// LogRecord's EventName.
const eventNameKey = string(semconv.OTelEventNameKey)

// DeviceID returns the flowseer.device.id attribute.
func DeviceID(id string) slog.Attr { return slog.String(DeviceIDKey, id) }

// DeviceSequence returns the flowseer.device.sequence attribute.
func DeviceSequence(sequence uint64) slog.Attr { return slog.Uint64(DeviceSequenceKey, sequence) }

// EdgeID returns the flowseer.edge.id attribute.
func EdgeID(id string) slog.Attr { return slog.String(EdgeIDKey, id) }

// TenantID returns the flowseer.tenant.id attribute.
func TenantID(id string) slog.Attr { return slog.String(TenantIDKey, id) }

// CaptureSessionID returns the flowseer.capture.session.id attribute.
func CaptureSessionID(id string) slog.Attr { return slog.String(CaptureSessionIDKey, id) }

// CaptureChunkFirstSequence returns the flowseer.capture.chunk.first_sequence
// attribute.
func CaptureChunkFirstSequence(sequence uint64) slog.Attr {
	return slog.Uint64(CaptureChunkFirstSequenceKey, sequence)
}

// EventName returns the otel.event.name attribute that turns a log record
// into a named event once the shared pipeline maps it. The name is a fixed,
// documented event name, never a dynamic value.
func EventName(name string) slog.Attr { return slog.String(eventNameKey, name) }
