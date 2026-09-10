package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	"go.aledante.io/FlowSeer/src/services/device/internal/telemetry"
)

func newView(t *testing.T, logs *bytes.Buffer) (*telemetry.View, *metric.ManualReader) {
	t.Helper()
	reader := metric.NewManualReader()
	view, err := telemetry.NewView(telemetry.ViewConfig{
		MeterProvider: metric.NewMeterProvider(metric.WithReader(reader)),
		Logger:        slogTo(logs),
	})
	if err != nil {
		t.Fatalf("NewView: %v", err)
	}
	return view, reader
}

// The event carries what an operator needs to see without fetching the record:
// which device, which interface, and both descriptions.
func TestTheEventCarriesTheDeviceTheInterfaceAndBothDescriptions(t *testing.T) {
	var logs bytes.Buffer
	view, _ := newView(t, &logs)

	view.DriftDetected(context.Background(), "0192e6a0-0000-7000-8000-0000000000d1", "ethernet 1/1/1",
		"uplink to core", "uplink to core b",
		inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_AUTHORITATIVE,
		telemetry.DriftOutcomeDispatched)

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record); err != nil {
		t.Fatalf("parse the record: %v (%s)", err, logs.String())
	}
	want := map[string]string{
		"otel.event.name":                      "flowseer.device.drift.detected",
		"flowseer.device.id":                   "0192e6a0-0000-7000-8000-0000000000d1",
		"flowseer.device.interface":            "ethernet 1/1/1",
		"flowseer.device.description.expected": "uplink to core",
		"flowseer.device.description.observed": "uplink to core b",
	}
	for key, value := range want {
		if got, _ := record[key].(string); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if got, _ := record["msg"].(string); got != "drift detected" {
		t.Errorf("msg = %q, want the stable phrase", got)
	}
}

// The descriptions are operator-written text about a customer's topology and
// the interface name is per device. None of them can be a series, so the
// counter carries the management mode and nothing else — two values, which is
// what makes it aggregatable at all.
func TestTheCounterCarriesTheManagementModeAndNothingElse(t *testing.T) {
	view, reader := newView(t, &bytes.Buffer{})

	view.DriftDetected(context.Background(), "0192e6a0-0000-7000-8000-0000000000d1", "ethernet 1/1/1",
		"uplink to core", "uplink to core b",
		inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED, telemetry.DriftOutcomeHeld)
	view.DriftDetected(context.Background(), "0192e6a0-0000-7000-8000-0000000000d2", "ethernet 1/1/9",
		"spare", "patched to lab",
		inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED, telemetry.DriftOutcomeHeld)

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect: %v", err)
	}

	sum := findCounter(t, &collected, "flowseer.device.drift.detections")
	if len(sum.DataPoints) != 1 {
		t.Fatalf("two detections on one mode made %d series, want one", len(sum.DataPoints))
	}
	point := sum.DataPoints[0]
	if point.Value != 2 {
		t.Errorf("count = %d, want 2", point.Value)
	}
	if got := point.Attributes.Len(); got != 2 {
		t.Fatalf("the counter carries %d attributes, want the mode and the outcome: %v", got, point.Attributes.ToSlice())
	}
	mode, ok := point.Attributes.Value("flowseer.device.management_mode")
	if !ok || mode.AsString() != "DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED" {
		t.Errorf("management mode = %v, present = %v", mode, ok)
	}
}

// The names, units and description are an operational API: a dashboard and an
// alert are written against them, and changing one is a schema migration
// rather than a rename.
func TestTheCounterIsNamedAndUnitedAsDocumented(t *testing.T) {
	view, reader := newView(t, &bytes.Buffer{})
	view.DriftDetected(context.Background(), "d", "i", "a", "b",
		inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_AUTHORITATIVE, telemetry.DriftOutcomeDispatched)

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, scope := range collected.ScopeMetrics {
		if scope.Scope.Name != "go.aledante.io/FlowSeer/src/services/device" {
			continue
		}
		if !strings.HasPrefix(scope.Scope.SchemaURL, "https://opentelemetry.io/schemas/") {
			t.Errorf("scope schema url = %q", scope.Scope.SchemaURL)
		}
		for _, m := range scope.Metrics {
			if m.Name != "flowseer.device.drift.detections" {
				continue
			}
			if m.Unit != "{detection}" {
				t.Errorf("unit = %q, want {detection}", m.Unit)
			}
			if m.Description == "" {
				t.Error("the instrument carries no description")
			}
			return
		}
	}
	t.Fatal("the counter was not collected under the device service's own scope")
}

// A nil View is what a caller that wants no signals passes, and calling it
// must not be a crash in production because a test left it unset.
func TestANilViewEmitsNothing(_ *testing.T) {
	var view *telemetry.View
	view.DriftDetected(context.Background(), "d", "i", "a", "b",
		inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_AUTHORITATIVE, telemetry.DriftOutcomeDispatched)
}

func findCounter(t *testing.T, collected *metricdata.ResourceMetrics, name string) metricdata.Sum[int64] {
	t.Helper()
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s is %T, want an int64 sum", name, m.Data)
			}
			return sum
		}
	}
	t.Fatalf("%s was not collected", name)
	return metricdata.Sum[int64]{}
}

// TestRPCMethodMatchesTheConventionsVocabulary pins the value rpc.method
// takes. semconv v1.43.0 defines it as the fully-qualified logical method
// name — "com.example.ExampleService/exampleMethod" — and defines no
// rpc.service at all, so the service-qualified name belongs here whole,
// without Connect's leading slash.
func TestRPCMethodMatchesTheConventionsVocabulary(t *testing.T) {
	const procedure = "/flowseer.api.device.v1.DeviceService/ApplyInterfaceDescription"
	want := "flowseer.api.device.v1.DeviceService/ApplyInterfaceDescription"
	if got := telemetry.RPCMethod(procedure); got != want {
		t.Errorf("RPCMethod(%q) = %q, want %q", procedure, got, want)
	}
	// Idempotent: a procedure already without the slash is unchanged, so a
	// caller that has trimmed it does not lose its first segment.
	if got := telemetry.RPCMethod(want); got != want {
		t.Errorf("RPCMethod(%q) = %q, want it unchanged", want, got)
	}
}
