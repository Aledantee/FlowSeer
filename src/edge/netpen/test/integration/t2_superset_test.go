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

	supersets := []string{"eigrp", "wpad", "etherchannel", "mld", "raflood", "lldpspoof", "glbp"}
	for _, attack := range supersets {
		t.Run(attack, func(t *testing.T) {
			t.Logf("t2 %s against %s: pending (live assertion not implemented)", attack, t2Config.TargetHost)
		})
	}
}
