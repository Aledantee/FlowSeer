//go:build netpen_t1

package integration

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/test/integration/testenv"
)

// frrStartupBudget includes image pulls, container startup, and OSPF readiness.
const frrStartupBudget = 5 * time.Minute

// frrTopologyDir is the docker-compose context for the FRR lab,
// resolved relative to the test working directory (which Go sets to
// the test package's source directory: src/edge/netpen/test/integration/).
const frrTopologyDir = "t1/testdata/frr"

// TestMain owns the T1 lab lifecycle: docker compose up, OSPF readiness
// probe, run tests, docker compose down. When the Docker daemon is not
// running the entire tier is skipped (exit 0) with a printed reason; when
// container start fails the tier fails with a diagnostic (exit 1).
// Short mode runs only the offline harness tests and never checks Docker.
func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(runT1(m))
}

func runT1(m *testing.M) int {
	if testing.Short() {
		return m.Run()
	}
	if !testenv.HasDocker() {
		fmt.Fprintln(os.Stderr, "[netpen_t1] docker not on PATH; skipping tier")
		return 0
	}
	if !testenv.DockerDaemonRunning() {
		fmt.Fprintln(os.Stderr, "[netpen_t1] docker daemon not running; skipping tier (artifacts shipped, live tier needs a running daemon)")
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), frrStartupBudget)
	defer cancel()

	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[netpen_t1] getwd: %v\n", err)
		return 1
	}
	composeFile := filepath.Join(wd, frrTopologyDir, "docker-compose.yml")

	target, cleanup, err := startFRRLab(ctx, composeFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[netpen_t1] startFRRLab: %v\n", err)
		if err := cleanup(); err != nil {
			fmt.Fprintf(os.Stderr, "[netpen_t1] cleanup: %v\n", err)
		}
		return 1
	}
	testenv.SetTarget(target)
	fmt.Fprintf(os.Stderr, "[netpen_t1] FRR lab up; OSPF target at %s\n", target)

	code := m.Run()
	if err := cleanup(); err != nil {
		fmt.Fprintf(os.Stderr, "[netpen_t1] cleanup: %v\n", err)
		return 1
	}
	return code
}
