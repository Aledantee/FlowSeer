//go:build netpen_t2

package integration

import (
	"fmt"
	"os"
	"testing"

	"go.aledante.io/FlowSeer/test/integration/netpen/testenv"
)

// TestMain owns the T2 lifecycle. T2 is the opt-in operator-supplied
// virtual-Cisco tier: the operator provides a vIOS-class image (or any
// Cisco IOS/IOS-XE image that runs under their container runtime) and
// sets NETPEN_T2_IMAGE to its name. The tier is never automated and never
// a gate — it skips cleanly when the image is not provided.
//
// This mirrors the snmp t4 pattern (operator-supplied live device).
func TestMain(m *testing.M) {
	if !testenv.HasDocker() {
		fmt.Fprintln(os.Stderr, "[netpen_t2] docker not on PATH; skipping tier")
		os.Exit(0)
	}
	if !testenv.DockerDaemonRunning() {
		fmt.Fprintln(os.Stderr, "[netpen_t2] docker daemon not running; skipping tier")
		os.Exit(0)
	}
	if os.Getenv("NETPEN_T2_IMAGE") == "" {
		fmt.Fprintln(os.Stderr, "[netpen_t2] NETPEN_T2_IMAGE not set; skipping tier (operator-supplied vIOS-class image required)")
		os.Exit(0)
	}

	// The operator owns the image lifecycle. TestMain sets the target
	// from NETPEN_T2_TARGET (the management IP of the vIOS instance).
	target := os.Getenv("NETPEN_T2_TARGET")
	if target == "" {
		fmt.Fprintln(os.Stderr, "[netpen_t2] NETPEN_T2_TARGET not set; skipping tier (operator must set the vIOS management IP)")
		os.Exit(0)
	}
	testenv.SetTarget(target)
	fmt.Fprintf(os.Stderr, "[netpen_t2] operator-supplied target at %s\n", target)

	code := m.Run()
	os.Exit(code)
}
