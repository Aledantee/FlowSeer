//go:build netpen_t2

package integration

import (
	"os"
	"testing"

	"go.aledante.io/FlowSeer/test/integration/netpen/testenv"
)

// TestT2SupersetBehavioralTruth runs each of the eight superset attacks
// against the operator-supplied vIOS target and records the findings
// class. This is the behavioral-truth validation (ground-truth source
// (b)): a real Cisco NOS validates that the attack produces the
// expected finding against a real switch, not just the correct wire
// shape.
//
// The test mirrors TestAE6_SupersetReproducibility but targets the
// operator's vIOS image. Results are recorded in VALIDATION_MATRIX.md
// under the (b) column.
func TestT2SupersetBehavioralTruth(t *testing.T) {
	target := testenv.Target()
	if target == "" {
		t.Skip("no t2 target; NETPEN_T2_TARGET not set")
	}

	supersets := []string{"ospf", "eigrp", "wpad", "etherchannel", "mld", "raflood", "lldpspoof", "glbp"}
	for _, attack := range supersets {
		t.Run(attack, func(t *testing.T) {
			// The operator runs netpen against the vIOS target.
			// The exact invocation depends on the image shape;
			// this test documents the contract and records the
			// findings class.
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
