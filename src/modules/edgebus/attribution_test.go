package edgebus

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestEdgeOfHubStream(t *testing.T) {
	if edge, ok := edgeOfHubStream(HubEdgeStream("edge-1")); !ok || edge != "edge-1" {
		t.Fatalf("edgeOfHubStream = %q, %v", edge, ok)
	}
	for _, name := range []string{AuditStream, HubEdgeStreamPrefix, "OTHER"} {
		if _, ok := edgeOfHubStream(name); ok {
			t.Fatalf("%q read as an edge stream", name)
		}
	}
}

// fakeMsg is the slice of jetstream.Msg the forwarder touches.
type fakeMsg struct {
	jetstream.Msg
	subject string
	data    []byte
	acked   bool
	termed  bool
	naked   bool
}

func (m *fakeMsg) Subject() string                  { return m.subject }
func (m *fakeMsg) Data() []byte                     { return m.data }
func (m *fakeMsg) Ack() error                       { m.acked = true; return nil }
func (m *fakeMsg) Term() error                      { m.termed = true; return nil }
func (m *fakeMsg) NakWithDelay(time.Duration) error { m.naked = true; return nil }

func TestForwarderRefusesARecordOutsideItsStreamsEdge(t *testing.T) {
	posted := 0
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		posted++
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	var logs bytes.Buffer
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	refused, err := provider.Meter(scopeName).Int64Counter("flowseer.edgebus.records.refused")
	if err != nil {
		t.Fatal(err)
	}
	f := &Forwarder{
		cfg:        ForwarderConfig{Endpoint: collector.URL, Client: collector.Client(), RetryDelay: time.Millisecond},
		hub:        &Hub{},
		logger:     slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
		refused:    refused,
		lastLogged: map[string]time.Time{},
	}
	f.cfg.Client.Timeout = 5 * time.Second

	own := &fakeMsg{subject: OTelSubject(DefaultTenant, "edge-a", SignalLogs), data: []byte("x")}
	f.forward("edge-a", own)
	if !own.acked || posted != 1 {
		t.Fatalf("own record: acked=%v posted=%d", own.acked, posted)
	}

	foreign := &fakeMsg{subject: OTelSubject(DefaultTenant, "edge-b", SignalLogs), data: []byte("x")}
	f.forward("edge-a", foreign)
	if !foreign.termed || foreign.acked || posted != 1 {
		t.Fatalf("foreign record: termed=%v acked=%v posted=%d", foreign.termed, foreign.acked, posted)
	}
	if f.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", f.Dropped())
	}

	// The refusal is visible: a WARN event naming both edges, and a counter
	// labeled by reason and signal only.
	record := logs.String()
	for _, want := range []string{`"level":"WARN"`, `"otel.event.name":"flowseer.edgebus.record.refused"`, `"flowseer.edge.id":"edge-a"`, `"flowseer.edgebus.claimed_edge_id":"edge-b"`, `"flowseer.edgebus.reason":"foreign_subject"`} {
		if !strings.Contains(record, want) {
			t.Fatalf("refusal record lacks %s: %s", want, record)
		}
	}
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "flowseer.edgebus.records.refused" {
				continue
			}
			sum := m.Data.(metricdata.Sum[int64])
			for _, point := range sum.DataPoints {
				reason, _ := point.Attributes.Value("flowseer.edgebus.reason")
				if reason.AsString() == reasonForeignSubject && point.Value == 1 {
					found = true
				}
				if _, hasEdge := point.Attributes.Value("flowseer.edge.id"); hasEdge {
					t.Fatal("the counter carries an edge id")
				}
			}
		}
	}
	if !found {
		t.Fatal("refused counter did not record the foreign subject")
	}

	// A second refusal inside the interval is counted but not logged again.
	logs.Reset()
	f.forward("edge-a", &fakeMsg{subject: OTelSubject(DefaultTenant, "edge-b", SignalLogs)})
	if f.Dropped() != 2 || logs.Len() != 0 {
		t.Fatalf("second refusal: dropped=%d logged=%q", f.Dropped(), logs.String())
	}
}
