//go:build netpen_t1

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/netpen/integration/testenv"
)

// frrStartupBudget caps the wall-clock window for "from cold to OSPF
// adjacency ready". First-run pulls quay.io/frrouting/frr (~200 MB) and
// runs FRR's init sequence; the budget includes both.
const frrStartupBudget = 5 * time.Minute

// frrTopologyDir is the docker-compose context for the FRR lab,
// resolved relative to the test working directory (which Go sets to
// the test package's source directory: integration/).
const frrTopologyDir = "t1/testdata/frr"

// TestMain owns the T1 lab lifecycle: docker compose up, OSPF readiness
// probe, run tests, docker compose down. When the Docker daemon is not
// running the entire tier is skipped (exit 0) with a printed reason; when
// container start fails the tier fails with a diagnostic (exit 1).
// Cleanup is registered before m.Run so a panicking test still tears the
// lab down.
//
// containerlab is preferred when installed; otherwise plain docker compose
// with FRR nodes on a custom bridge. This mirrors the HOST REALITY decision
// from the U13 plan.
func TestMain(m *testing.M) {
	if !testenv.HasDocker() {
		fmt.Fprintln(os.Stderr, "[netpen_t1] docker not on PATH; skipping tier")
		os.Exit(0)
	}
	if !testenv.DockerDaemonRunning() {
		fmt.Fprintln(os.Stderr, "[netpen_t1] docker daemon not running; skipping tier (artifacts shipped, live tier needs a running daemon)")
		os.Exit(0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), frrStartupBudget)
	defer cancel()

	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[netpen_t1] getwd: %v\n", err)
		os.Exit(1)
	}
	composeFile := filepath.Join(wd, frrTopologyDir, "docker-compose.yml")

	target, cleanup, err := startFRRLab(ctx, composeFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[netpen_t1] startFRRLab: %v\n", err)
		if cleanup != nil {
			cleanup()
		}
		os.Exit(1)
	}
	testenv.SetTarget(target)
	fmt.Fprintf(os.Stderr, "[netpen_t1] FRR lab up; OSPF target at %s\n", target)

	code := m.Run()
	cleanup()
	os.Exit(code)
}
