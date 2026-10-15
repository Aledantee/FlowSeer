package otlpapi_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	connect "connectrpc.com/connect"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	telemetryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/telemetry/v1"
	"go.aledante.io/FlowSeer/src/common/fsconv"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/otlpapi"
)

// collectorDouble records every post and answers with the next status in its
// script, repeating the last one.
type collectorDouble struct {
	server *httptest.Server

	mu       sync.Mutex
	statuses []int
	posts    []post
}

type post struct {
	path        string
	contentType string
	authority   string
	body        []byte
}

func newCollectorDouble(t *testing.T, statuses ...int) *collectorDouble {
	t.Helper()
	d := &collectorDouble{statuses: statuses}
	d.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.posts = append(d.posts, post{
			path: r.URL.Path, contentType: r.Header.Get("Content-Type"),
			authority: r.Header.Get("Authorization"), body: body,
		})
		status := d.statuses[min(len(d.posts), len(d.statuses))-1]
		d.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(d.server.Close)
	return d
}

func (d *collectorDouble) received() []post {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]post(nil), d.posts...)
}

type system struct {
	handler *otlpapi.Handler
	reader  *sdkmetric.ManualReader
}

func newSystem(t *testing.T, collector *edgebus.Collector, edgeID func(context.Context) (string, error)) system {
	t.Helper()
	if edgeID == nil {
		edgeID = func(context.Context) (string, error) { return "edge-a", nil }
	}
	reader := sdkmetric.NewManualReader()
	handler, err := otlpapi.NewHandler(otlpapi.Config{
		Collector:     collector,
		EdgeID:        edgeID,
		MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return system{handler: handler, reader: reader}
}

func collectorFor(t *testing.T, d *collectorDouble, headers map[string]string) *edgebus.Collector {
	t.Helper()
	collector, err := edgebus.NewCollector(edgebus.CollectorConfig{Endpoint: d.server.URL, Headers: headers})
	if err != nil {
		t.Fatalf("new collector: %v", err)
	}
	return collector
}

func export(signal telemetryv1.TelemetrySignal, body string) *telemetryv1.TelemetryExport {
	return telemetryv1.TelemetryExport_builder{Signal: signal.Enum(), Body: []byte(body)}.Build()
}

func (s system) deliver(exports ...*telemetryv1.TelemetryExport) (uint32, error) {
	resp, err := s.handler.Deliver(context.Background(), connect.NewRequest(telemetryv1.DeliverRequest_builder{Exports: exports}.Build()))
	if err != nil {
		return 0, err
	}
	return resp.Msg.GetSettled(), nil
}

func mustDeliver(t *testing.T, s system, exports ...*telemetryv1.TelemetryExport) uint32 {
	t.Helper()
	settled, err := s.deliver(exports...)
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	return settled
}

// counted reads a counter's value at the point whose attributes include every
// given pair.
func counted(t *testing.T, reader *sdkmetric.ManualReader, name string, want ...attribute.KeyValue) int64 {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if m.Name != name || !ok {
				continue
			}
			for _, point := range sum.DataPoints {
				matches := true
				for _, kv := range want {
					if got, ok := point.Attributes.Value(kv.Key); !ok || got != kv.Value {
						matches = false
					}
				}
				if matches {
					return point.Value
				}
			}
		}
	}
	return 0
}

func TestBodyReachesTheCollectorUnchanged(t *testing.T) {
	d := newCollectorDouble(t, http.StatusOK)
	s := newSystem(t, collectorFor(t, d, map[string]string{"Authorization": "Bearer token"}), nil)
	body := "\x00\x01 not text \xff"

	if got := mustDeliver(t, s, export(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_LOGS, body)); got != 1 {
		t.Fatalf("settled = %d, want 1", got)
	}

	posts := d.received()
	if len(posts) != 1 {
		t.Fatalf("collector saw %d posts, want 1", len(posts))
	}
	if got := string(posts[0].body); got != body {
		t.Errorf("collector body = %q, want %q", got, body)
	}
	if posts[0].path != "/v1/logs" {
		t.Errorf("path = %q, want /v1/logs", posts[0].path)
	}
	if posts[0].contentType != "application/x-protobuf" {
		t.Errorf("content type = %q, want application/x-protobuf", posts[0].contentType)
	}
	if posts[0].authority != "Bearer token" {
		t.Errorf("authorization = %q, want the configured header", posts[0].authority)
	}
	if got := counted(t, s.reader, "flowseer.otlpapi.records.forwarded", attribute.String(fsconv.DeviceSignalKey, "logs")); got != 1 {
		t.Errorf("forwarded logs = %d, want 1", got)
	}
}

func TestCollectorAnswersMapAsTheForwarderMapsThem(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		closed      bool
		wantSettled uint32
		wantRefused int64
	}{
		{name: "accepted", status: http.StatusOK, wantSettled: 1},
		{name: "this body is unacceptable", status: http.StatusBadRequest, wantSettled: 1, wantRefused: 1},
		{name: "unauthorized", status: http.StatusUnauthorized},
		{name: "forbidden", status: http.StatusForbidden},
		{name: "not found", status: http.StatusNotFound},
		{name: "request timeout", status: http.StatusRequestTimeout},
		{name: "too many requests", status: http.StatusTooManyRequests},
		{name: "unavailable", status: http.StatusServiceUnavailable},
		{name: "closed server", closed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newCollectorDouble(t, tc.status)
			s := newSystem(t, collectorFor(t, d, nil), nil)
			if tc.closed {
				d.server.Close()
			}

			got := mustDeliver(t, s, export(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_METRICS, "body"))

			if got != tc.wantSettled {
				t.Errorf("settled = %d, want %d", got, tc.wantSettled)
			}
			refused := counted(t, s.reader, "flowseer.otlpapi.records.refused",
				attribute.String("flowseer.otlpapi.reason", "collector_rejected"),
				attribute.String(fsconv.DeviceSignalKey, "metrics"))
			if refused != tc.wantRefused {
				t.Errorf("collector_rejected refusals = %d, want %d", refused, tc.wantRefused)
			}
		})
	}
}

func TestRetryAnswerStopsTheCount(t *testing.T) {
	d := newCollectorDouble(t, http.StatusOK, http.StatusServiceUnavailable)
	s := newSystem(t, collectorFor(t, d, nil), nil)
	logs := telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_LOGS

	got := mustDeliver(t, s, export(logs, "one"), export(logs, "two"), export(logs, "three"))

	if got != 1 {
		t.Fatalf("settled = %d, want 1", got)
	}
	if posts := d.received(); len(posts) != 2 {
		t.Errorf("collector saw %d posts, want 2", len(posts))
	}
}

func TestNewCollectorRefusesAMalformedEndpoint(t *testing.T) {
	for _, endpoint := range []string{"", "collector", "localhost:4318", "ftp://collector", "http://", "://x", "/v1/logs"} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := edgebus.NewCollector(edgebus.CollectorConfig{Endpoint: endpoint}); err == nil {
				t.Fatalf("NewCollector(%q) accepted the endpoint", endpoint)
			}
		})
	}
	for _, endpoint := range []string{"http://collector:4318", "https://collector/otlp"} {
		if _, err := edgebus.NewCollector(edgebus.CollectorConfig{Endpoint: endpoint}); err != nil {
			t.Errorf("NewCollector(%q): %v", endpoint, err)
		}
	}
}

func TestNoCollectorSettlesNothing(t *testing.T) {
	s := newSystem(t, nil, nil)

	got := mustDeliver(t, s, export(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_LOGS, "body"))

	if got != 0 {
		t.Fatalf("settled = %d, want 0", got)
	}
}

func TestInvalidExportIsSettledAndRefused(t *testing.T) {
	d := newCollectorDouble(t, http.StatusOK)
	s := newSystem(t, collectorFor(t, d, nil), nil)
	logs := telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_LOGS

	got := mustDeliver(t, s,
		export(logs, "one"),
		export(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_UNSPECIFIED, "two"),
		export(logs, "three"))

	if got != 3 {
		t.Fatalf("settled = %d, want 3", got)
	}
	if posts := d.received(); len(posts) != 2 {
		t.Errorf("collector saw %d posts, want 2", len(posts))
	}
	if got := counted(t, s.reader, "flowseer.otlpapi.records.refused", attribute.String("flowseer.otlpapi.reason", "invalid")); got != 1 {
		t.Errorf("invalid refusals = %d, want 1", got)
	}
}

func TestMissingEdgeIsUnauthenticated(t *testing.T) {
	d := newCollectorDouble(t, http.StatusOK)
	s := newSystem(t, collectorFor(t, d, nil), func(context.Context) (string, error) {
		return "", errors.New("no assertion")
	})

	_, err := s.deliver(export(telemetryv1.TelemetrySignal_TELEMETRY_SIGNAL_LOGS, "body"))

	if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want unauthenticated (err %v)", got, err)
	}
	if posts := d.received(); len(posts) != 0 {
		t.Errorf("collector saw %d posts, want 0", len(posts))
	}
}
