package integration_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestASyslogDatagramReachesItsStreamWithNoLeafLink(t *testing.T) {
	d := assembleRawWindow(t)
	d.waitDeviceResolved(t)

	const text = "delivery-with-no-leaf-link"
	d.send(t, text)
	d.typedRecord(t, text)

	d.central.mu.Lock()
	hub := d.central.hub
	d.central.mu.Unlock()
	if got := hub.LeafCount(); got != 0 {
		t.Errorf("hub leaf count = %d, want 0", got)
	}
	path := filepath.Join(d.agent.dir, "agent-state", "jetstream")
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("agent JetStream directory stat = %v, want not exist", err)
	}
}
