package edgebus

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
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
	f := &Forwarder{
		cfg: ForwarderConfig{Endpoint: collector.URL, Client: collector.Client(), RetryDelay: time.Millisecond},
		hub: &Hub{tenant: DefaultTenant},
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
}
