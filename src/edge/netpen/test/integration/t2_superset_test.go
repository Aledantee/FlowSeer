//go:build netpen_t2

package integration

import (
	"testing"
)

// TestT2PendingSuperset records the attacks that have no live assertion yet.
func TestT2PendingSuperset(t *testing.T) {
	if testing.Short() {
		t.Skip("live lab tier disabled in short mode; seven superset attacks remain pending")
	}

	supersets := []struct {
		name        string
		expectation string
	}{
		{name: "eigrp", expectation: "live assertion not implemented"},
		{name: "wpad", expectation: "live assertion not implemented"},
		{name: "etherchannel", expectation: "live assertion not implemented"},
		{name: "mld", expectation: "live assertion not implemented"},
		{name: "raflood", expectation: "live assertion not implemented"},
		{name: "lldpspoof", expectation: "live assertion not implemented"},
		{name: "glbp", expectation: "packet-glbp.c 12-byte header and one-byte TLV layout, live peer assertion not implemented"},
	}
	for _, attack := range supersets {
		t.Run(attack.name, func(t *testing.T) {
			t.Logf("t2 %s against %s: pending (%s)", attack.name, t2Config.TargetHost, attack.expectation)
		})
	}
}
