package telemetry_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
)

// namedEventCase enumerates every flowseer.device.* named event the task
// requires, and the call that must produce it.
func namedEventCases(v *telemetry.View) []struct {
	name string
	call func(ctx context.Context)
} {
	return []struct {
		name string
		call func(ctx context.Context)
	}{
		{"flowseer.device.route.selected", func(ctx context.Context) {
			v.RouteSelected(ctx, inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP, false)
		}},
		{"flowseer.device.route.fallback", func(ctx context.Context) {
			v.RouteSelected(ctx, inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SSH, true)
		}},
		{"flowseer.device.discovery.completed", func(ctx context.Context) {
			v.DiscoveryCompleted(ctx, "fingerprint")
		}},
		{"flowseer.device.firmware.epoch_changed", func(ctx context.Context) { v.FirmwareEpochChanged(ctx) }},
		{"flowseer.device.recovery.started", func(ctx context.Context) { v.RecoveryStarted(ctx) }},
		{"flowseer.device.drift.detected", func(ctx context.Context) { v.DriftDetected(ctx) }},
		{"flowseer.device.lane.frozen", func(ctx context.Context) { v.LaneFrozen(ctx) }},
		{"flowseer.device.lane.blocked", func(ctx context.Context) {
			v.LaneBlocked(ctx, accessv1.BlockReason_BLOCK_REASON_CONFLICTING_READS)
		}},
		{"flowseer.device.lane.released", func(ctx context.Context) { v.LaneReleased(ctx) }},
	}
}

// recordingHandler collects records for inspection. Tests call it from one
// goroutine, so it needs no synchronization.
type recordingHandler struct {
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *recordingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *recordingHandler) eventNames() []string {
	names := make([]string, 0, len(h.records))
	for _, r := range h.records {
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "otel.event.name" {
				names = append(names, a.Value.String())
			}
			return true
		})
	}
	return names
}

func TestNamedEventsMatchTheTaskList(t *testing.T) {
	for _, tc := range namedEventCases(nil) {
		t.Run(tc.name, func(t *testing.T) {
			handler := &recordingHandler{}
			view, err := telemetry.NewView(telemetry.ViewConfig{Logger: slog.New(handler)})
			if err != nil {
				t.Fatalf("NewView() error: %v", err)
			}

			for _, c := range namedEventCases(view) {
				if c.name == tc.name {
					c.call(context.Background())
				}
			}

			names := handler.eventNames()
			found := false
			for _, n := range names {
				if n == tc.name {
					found = true
				}
			}
			if !found {
				t.Errorf("event %s was not emitted; got %v", tc.name, names)
			}
		})
	}
}

func TestRouteSelectedWithoutFallthroughEmitsOnlyOneEvent(t *testing.T) {
	handler := &recordingHandler{}
	view, err := telemetry.NewView(telemetry.ViewConfig{Logger: slog.New(handler)})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}

	view.RouteSelected(context.Background(), inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP, false)

	names := handler.eventNames()
	if len(names) != 1 || names[0] != "flowseer.device.route.selected" {
		t.Errorf("event names = %v, want exactly [flowseer.device.route.selected]", names)
	}
}

func TestMetricAttributesStayWithinTheAllowlistAndCap(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	view, err := telemetry.NewView(telemetry.ViewConfig{
		MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
	})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}

	ctx := context.Background()
	view.RecordOperationDuration(ctx, "interface_description", 0.05, "")
	view.RecordOperationDuration(ctx, "interface_description", 0.10, "timeout")
	view.RecordRouteSelection(ctx, "snmp", telemetry.OutcomeSuccess)

	allowlist := map[string]bool{
		"flowseer.device.operation": true,
		"flowseer.device.route":     true,
		"flowseer.device.outcome":   true,
		"flowseer.device.reason":    true,
		"error.type":                true,
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("Collect() error: %v", err)
	}

	seen := 0
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			seen++
			for _, attrs := range dataPointAttributes(t, m) {
				if attrs.Len() > 2 {
					t.Errorf("metric %s: %d attributes, want at most 2 (%v)", m.Name, attrs.Len(), attrs.ToSlice())
				}
				for _, kv := range attrs.ToSlice() {
					key := string(kv.Key)
					if !allowlist[key] {
						t.Errorf("metric %s: attribute %q is not in the allowlist", m.Name, key)
					}
					if key != "error.type" && !strings.HasPrefix(key, "flowseer.") {
						t.Errorf("metric %s: non-standard attribute %q is not flowseer.*-namespaced", m.Name, key)
					}
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("no metrics were collected")
	}
}

func dataPointAttributes(t *testing.T, m metricdata.Metrics) []attribute.Set {
	t.Helper()
	switch data := m.Data.(type) {
	case metricdata.Histogram[float64]:
		out := make([]attribute.Set, 0, len(data.DataPoints))
		for _, dp := range data.DataPoints {
			out = append(out, dp.Attributes)
		}
		return out
	case metricdata.Sum[int64]:
		out := make([]attribute.Set, 0, len(data.DataPoints))
		for _, dp := range data.DataPoints {
			out = append(out, dp.Attributes)
		}
		return out
	default:
		t.Fatalf("unexpected metric data type %T for %s", data, m.Name)
		return nil
	}
}

// alwaysFailingSpanExporter proves an exporter failure never blocks or
// fails the caller (requirement 17): ExportSpans always errors.
type alwaysFailingSpanExporter struct{}

func (alwaysFailingSpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return errors.New("export always fails")
}
func (alwaysFailingSpanExporter) Shutdown(context.Context) error { return nil }

func TestExporterFailureDoesNotBlockOrFailTheCaller(t *testing.T) {
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(alwaysFailingSpanExporter{}))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	view, err := telemetry.NewView(telemetry.ViewConfig{TracerProvider: provider})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}

	ctx, endOp := view.StartOperation(context.Background(), "interface_description")
	_, endRoute := view.StartRoute(ctx, "snmp", trace.SpanContext{})
	endRoute(nil, nil)
	endOp(nil, nil)
	// Reaching here without a hang or a panic is the assertion: the failing
	// exporter's error never surfaces to this caller.
}

func TestRecoveryRouteSpanLinksInsteadOfParenting(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	view, err := telemetry.NewView(telemetry.ViewConfig{TracerProvider: provider})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}

	opCtx, endOp := view.StartOperation(context.Background(), "interface_description")
	opSpanContext := trace.SpanContextFromContext(opCtx)
	endOp(nil, nil)

	// A later, independent recovery attempt does not have the operation
	// span on its context (it may run after the operation span ended), so
	// it links to the saved span context explicitly.
	_, endRoute := view.StartRoute(context.Background(), "ssh", opSpanContext)
	endRoute(nil, nil)

	ended := recorder.Ended()
	var routeSpan sdktrace.ReadOnlySpan
	for _, s := range ended {
		if s.Name() == "flowseer.device.route" {
			routeSpan = s
		}
	}
	if routeSpan == nil {
		t.Fatal("route span was not recorded")
	}
	links := routeSpan.Links()
	if len(links) != 1 || links[0].SpanContext.TraceID() != opSpanContext.TraceID() {
		t.Errorf("route span links = %v, want one link to the operation span's trace %s", links, opSpanContext.TraceID())
	}
	if routeSpan.Parent().IsValid() {
		t.Error("recovery route span should not be parented under the operation span")
	}
}
