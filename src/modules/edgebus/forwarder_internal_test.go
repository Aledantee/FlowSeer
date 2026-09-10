package edgebus

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestForwarderLeavesTheCallersClientAlone covers the shared-client case the
// timeout default was written for: http.DefaultClient carries a zero timeout,
// and setting it in place would change every other user of that client and
// race the requests already in flight on it.
func TestForwarderLeavesTheCallersClientAlone(t *testing.T) {
	shared := &http.Client{Transport: http.DefaultTransport}
	f, err := StartForwarder(context.Background(), &Hub{}, ForwarderConfig{
		Endpoint: "http://127.0.0.1:1",
		Client:   shared,
	})
	if err != nil {
		t.Fatalf("StartForwarder: %v", err)
	}
	t.Cleanup(f.Close)

	if shared.Timeout != 0 {
		t.Errorf("the caller's client now has a %s timeout it did not ask for", shared.Timeout)
	}
	if f.cfg.Client == shared {
		t.Fatal("the forwarder kept the caller's client rather than a copy")
	}
	if f.cfg.Client.Timeout != 30*time.Second {
		t.Errorf("forwarder client timeout = %s, want 30s", f.cfg.Client.Timeout)
	}
	if f.cfg.Client.Transport != shared.Transport {
		t.Error("the copy dropped the caller's transport")
	}
}

// TestForwarderAnswerClasses fixes what each collector answer does to a
// record, and which reason a record dropped at the delivery backstop is
// counted under. A collector refusing this deployment's authorization must
// stay legible as collector_rejected however many attempts it took to give
// up: that is the one counter saying the collector is the problem.
func TestForwarderAnswerClasses(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		delivered uint64
		wantAck   bool
		wantNak   bool
		wantTerm  bool
		wantCount string
	}{
		{name: "accepted", status: http.StatusOK, wantAck: true},
		{name: "this body is unacceptable", status: http.StatusBadRequest, wantTerm: true, wantCount: reasonCollectorReject},
		{name: "stale token, attempts left", status: http.StatusUnauthorized, wantNak: true},
		{name: "endpoint moved, attempts left", status: http.StatusNotFound, wantNak: true},
		{
			name: "stale token, attempts exhausted", status: http.StatusForbidden,
			delivered: forwarderMaxDeliver, wantTerm: true, wantCount: reasonCollectorReject,
		},
		{name: "collector failing, attempts left", status: http.StatusBadGateway, wantNak: true},
		{
			name: "collector failing, attempts exhausted", status: http.StatusServiceUnavailable,
			delivered: forwarderMaxDeliver, wantTerm: true, wantCount: reasonDeliveryExceeded,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer collector.Close()

			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			refused, err := provider.Meter(scopeName).Int64Counter("flowseer.edgebus.records.refused")
			if err != nil {
				t.Fatal(err)
			}
			f := &Forwarder{
				cfg:        ForwarderConfig{Endpoint: collector.URL, Client: collector.Client(), RetryDelay: time.Millisecond},
				hub:        &Hub{},
				logger:     slog.New(slog.DiscardHandler),
				refused:    refused,
				lastLogged: map[string]time.Time{},
			}
			f.cfg.Client.Timeout = 5 * time.Second

			msg := &fakeMsg{
				subject:   OTelSubject(DefaultTenant, "edge-a", SignalMetrics),
				data:      []byte("body"),
				delivered: tc.delivered,
			}
			f.forward("edge-a", msg)

			if msg.acked != tc.wantAck || msg.naked != tc.wantNak || msg.termed != tc.wantTerm {
				t.Fatalf("status %d: acked=%v naked=%v termed=%v, want %v/%v/%v",
					tc.status, msg.acked, msg.naked, msg.termed, tc.wantAck, tc.wantNak, tc.wantTerm)
			}
			counted := refusedByReason(t, reader)
			if tc.wantCount == "" {
				if len(counted) != 0 {
					t.Fatalf("status %d counted a refusal: %v", tc.status, counted)
				}
				return
			}
			if counted[tc.wantCount] != 1 {
				t.Fatalf("status %d counted %v, want one %s", tc.status, counted, tc.wantCount)
			}
			if signal := countedSignal(t, reader); signal != string(SignalMetrics) {
				t.Errorf("refusal counted with signal %q, want %q", signal, SignalMetrics)
			}
		})
	}
}

// refusedByReason reads the refused-records counter back as reason to count.
func refusedByReason(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	counted := map[string]int64{}
	for _, point := range refusedPoints(t, reader) {
		reason, _ := point.Attributes.Value("flowseer.edgebus.reason")
		counted[reason.AsString()] += point.Value
	}
	return counted
}

func countedSignal(t *testing.T, reader *sdkmetric.ManualReader) string {
	t.Helper()
	for _, point := range refusedPoints(t, reader) {
		signal, _ := point.Attributes.Value("flowseer.device.signal")
		return signal.AsString()
	}
	return ""
}

func refusedPoints(t *testing.T, reader *sdkmetric.ManualReader) []metricdata.DataPoint[int64] {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	var points []metricdata.DataPoint[int64]
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "flowseer.edgebus.records.refused" {
				continue
			}
			points = append(points, m.Data.(metricdata.Sum[int64]).DataPoints...)
		}
	}
	return points
}
