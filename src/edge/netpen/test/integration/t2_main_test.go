//go:build netpen_t2

package integration

import (
	"flag"
	"fmt"
	"os"
	"testing"

	"go.aledante.io/FlowSeer/src/edge/netpen/test/integration/lab"
)

var t2Config lab.Config

// TestMain owns the live-lab configuration gate for the opt-in T2 tier.
func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	cfg, err := lab.ConfigFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[netpen_t2] %v; skipping live lab tier\n", err)
		os.Exit(0)
	}
	t2Config = cfg
	fmt.Fprintf(os.Stderr, "[netpen_t2] live %s target at %s via injector %s\n", cfg.TargetPlatform, cfg.TargetHost, cfg.InjectorHost)

	os.Exit(m.Run())
}
