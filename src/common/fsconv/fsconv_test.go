package fsconv_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/fsconv"
	"go.aledante.io/FlowSeer/src/common/tenant"
)

// The wire names are literals so a change to a constant fails here instead of
// silently renaming an attribute every consumer filters on.
func TestWireNames(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"DeviceIDKey", fsconv.DeviceIDKey, "flowseer.device.id"},
		{"DeviceSequenceKey", fsconv.DeviceSequenceKey, "flowseer.device.sequence"},
		{"EdgeIDKey", fsconv.EdgeIDKey, "flowseer.edge.id"},
		{"TenantIDKey", fsconv.TenantIDKey, "flowseer.tenant.id"},
		{"CaptureSessionIDKey", fsconv.CaptureSessionIDKey, "flowseer.capture.session.id"},
		{"CaptureChunkFirstSequenceKey", fsconv.CaptureChunkFirstSequenceKey, "flowseer.capture.chunk.first_sequence"},
		{"ErrorTypeKey", fsconv.ErrorTypeKey, "error.type"},
		{"EventName key", fsconv.EventName("x").Key, "otel.event.name"},
		{"ErrorTypeOther", fsconv.ErrorTypeOther, "_OTHER"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestConstructors(t *testing.T) {
	tests := []struct {
		name     string
		attr     slog.Attr
		wantKey  string
		wantKind slog.Kind
		wantVal  string
	}{
		{"DeviceID", fsconv.DeviceID("dev-1"), "flowseer.device.id", slog.KindString, "dev-1"},
		{"DeviceSequence", fsconv.DeviceSequence(7), "flowseer.device.sequence", slog.KindUint64, "7"},
		{"EdgeID", fsconv.EdgeID("edge-1"), "flowseer.edge.id", slog.KindString, "edge-1"},
		{"TenantID", fsconv.TenantID("t-1"), "flowseer.tenant.id", slog.KindString, "t-1"},
		{"CaptureSessionID", fsconv.CaptureSessionID("s-1"), "flowseer.capture.session.id", slog.KindString, "s-1"},
		{"CaptureChunkFirstSequence", fsconv.CaptureChunkFirstSequence(42), "flowseer.capture.chunk.first_sequence", slog.KindUint64, "42"},
		{"ErrorType", fsconv.ErrorType(errors.New("boom")), "error.type", slog.KindString, "_OTHER"},
		{"ErrorTypeString", fsconv.ErrorTypeString("connect.unavailable"), "error.type", slog.KindString, "connect.unavailable"},
		{"EventName", fsconv.EventName("flowseer.edge.device.onboarded"), "otel.event.name", slog.KindString, "flowseer.edge.device.onboarded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.attr.Key != tt.wantKey {
				t.Errorf("key: got %q, want %q", tt.attr.Key, tt.wantKey)
			}
			if got := tt.attr.Value.Kind(); got != tt.wantKind {
				t.Errorf("kind: got %v, want %v", got, tt.wantKind)
			}
			if got := tt.attr.Value.String(); got != tt.wantVal {
				t.Errorf("value: got %q, want %q", got, tt.wantVal)
			}
		})
	}
}

func TestEventNameEqualsBridgeAttribute(t *testing.T) {
	got := fsconv.EventName("flowseer.edge.device.onboarded")
	want := slog.String("otel.event.name", "flowseer.edge.device.onboarded")
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestErrorTypeOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"errs code", errs.New().Code(tenant.ErrCodeNoTenant).Msg("no tenant"), "tenant/no-tenant"},
		{"wrapped cancel", fmt.Errorf("x: %w", context.Canceled), "context.canceled"},
		{"wrapped deadline", fmt.Errorf("x: %w", context.DeadlineExceeded), "context.deadline_exceeded"},
		{"uncoded", errors.New("boom"), "_OTHER"},
		{"code over deadline", errs.New().Code(tenant.ErrCodeNoTenant).Cause(context.DeadlineExceeded).Msg("late"), "tenant/no-tenant"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fsconv.ErrorTypeOf(tt.err); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSpanError(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)).Tracer("fsconv_test")

	_, failed := tracer.Start(context.Background(), "failed")
	fsconv.SpanError(failed, errors.New("boom"), "rpc failed")
	failed.End()

	_, clean := tracer.Start(context.Background(), "clean")
	fsconv.SpanError(clean, nil, "rpc failed")
	clean.End()

	ended := recorder.Ended()
	if len(ended) != 2 {
		t.Fatalf("got %d ended spans, want 2", len(ended))
	}

	f := ended[0]
	if got := f.Status().Code; got != codes.Error {
		t.Errorf("status code: got %v, want %v", got, codes.Error)
	}
	if got := f.Status().Description; got != "rpc failed" {
		t.Errorf("status description: got %q, want %q", got, "rpc failed")
	}
	attrs := f.Attributes()
	if len(attrs) != 1 {
		t.Fatalf("got %d attributes, want 1: %v", len(attrs), attrs)
	}
	if got := string(attrs[0].Key); got != "error.type" {
		t.Errorf("attribute key: got %q, want %q", got, "error.type")
	}
	if got := attrs[0].Value.AsString(); got != "_OTHER" {
		t.Errorf("error.type: got %q, want %q", got, "_OTHER")
	}

	c := ended[1]
	if got := c.Status().Code; got != codes.Unset {
		t.Errorf("nil error status code: got %v, want %v", got, codes.Unset)
	}
	if got := c.Attributes(); len(got) != 0 {
		t.Errorf("nil error attributes: got %v, want none", got)
	}
}
