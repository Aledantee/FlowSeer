package edgebus

import (
	"net/http"
	"testing"
	"time"
)

// TestNewCollectorLeavesTheCallersClientAlone covers the shared-client case
// the timeout default exists for: http.DefaultClient carries a zero timeout,
// and setting it in place would change every other user of that client and
// race the requests already in flight on it.
func TestNewCollectorLeavesTheCallersClientAlone(t *testing.T) {
	shared := &http.Client{Transport: http.DefaultTransport}
	c, err := NewCollector(CollectorConfig{Endpoint: "http://127.0.0.1:1", Client: shared})
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}

	if shared.Timeout != 0 {
		t.Errorf("the caller's client now has a %s timeout it did not ask for", shared.Timeout)
	}
	if c.client == shared {
		t.Fatal("the collector kept the caller's client rather than a copy")
	}
	if c.client.Timeout != defaultCollectorTimeout {
		t.Errorf("collector client timeout = %s, want %s", c.client.Timeout, defaultCollectorTimeout)
	}
	if c.client.Transport != shared.Transport {
		t.Error("the copy dropped the caller's transport")
	}
}

func TestNewCollectorKeepsAClientWithItsOwnTimeout(t *testing.T) {
	own := &http.Client{Timeout: 5 * time.Second}
	c, err := NewCollector(CollectorConfig{Endpoint: "http://127.0.0.1:1", Client: own})
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	if c.client != own {
		t.Error("the collector replaced a client that already sets a timeout")
	}
	if own.Timeout != 5*time.Second {
		t.Errorf("the caller's timeout changed to %s", own.Timeout)
	}
}

func TestNewCollectorBuildsAClientWhenNoneIsGiven(t *testing.T) {
	c, err := NewCollector(CollectorConfig{Endpoint: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	if c.client == nil {
		t.Fatal("the collector has no client")
	}
	if c.client.Timeout != defaultCollectorTimeout {
		t.Errorf("collector client timeout = %s, want %s", c.client.Timeout, defaultCollectorTimeout)
	}
}
