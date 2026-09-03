//go:build netpen_t2

package integration

import (
	"os"
	"testing"

	"go.aledante.io/FlowSeer/src/edge/netpen/test/integration/testenv"
)

// TestT2SupersetBehavioralTruth lists the intended attack/target pairs. It has
// no command execution or behavioral assertions, so a pass is not validation
// evidence. Vendor execution and expected findings remain unimplemented.
func TestT2SupersetBehavioralTruth(t *testing.T) {
	if testing.Short() {
		t.Skip("operator t2 tier disabled in short mode")
	}
	target := testenv.Target()
	if target == "" {
		t.Skip("no t2 target; NETPEN_T2_TARGET not set")
	}

	supersets := []string{"ospf", "eigrp", "wpad", "etherchannel", "mld", "raflood", "lldpspoof", "glbp"}
	for _, attack := range supersets {
		t.Run(attack, func(t *testing.T) {
			t.Logf("t2 %s: would run against %s (operator provides the image)", attack, target)
		})
	}
}

// TestT2ImageProvided documents that the tier only runs when the
// operator has set NETPEN_T2_IMAGE. It never fails — it just records
// whether the image was provided.
func TestT2ImageProvided(t *testing.T) {
	if os.Getenv("NETPEN_T2_IMAGE") == "" {
		t.Skip("NETPEN_T2_IMAGE not set (operator must provide a vIOS-class image)")
	}
	t.Logf("t2 image: %s", os.Getenv("NETPEN_T2_IMAGE"))
}
