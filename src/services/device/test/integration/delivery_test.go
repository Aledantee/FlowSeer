package integration_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestASyslogDatagramReachesItsStreamWithNoLeafLink(t *testing.T) {
	d := assembleRawWindow(t)
	d.waitDeviceResolved(t)

	const text = "delivery-with-no-leaf-link"
	d.send(t, text)
	d.typedRecord(t, text)

	keysDir := filepath.Join(d.central.dir, "central-state", "keys")
	keys, err := os.ReadDir(keysDir)
	if err != nil {
		t.Fatalf("read central keys: %v", err)
	}
	if len(keys) != 3 {
		t.Errorf("central key files = %d, want 3", len(keys))
	}
	for _, key := range keys {
		if strings.HasPrefix(key.Name(), "edge-") {
			t.Errorf("central created per-edge key file %q", key.Name())
		}
	}
	path := filepath.Join(d.agent.dir, "agent-state", "jetstream")
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("agent JetStream directory stat = %v, want not exist", err)
	}
}
