//go:build snmp_integration_t1

package integration

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/protocol/snmp/test/integration/testenv"
)

// snmpdContextDir is the Docker build context for the T1 snmpd image,
// resolved relative to the test working directory (which Go sets to
// the test package's source directory: src/protocol/snmp/test/integration/).
const snmpdContextDir = "testdata/snmpd"

// snmpdStartupBudget caps the wall-clock budget for snmpd container
// build, start, and SNMP-level readiness. Generous because the first
// run includes a `docker build` step that pulls the Debian base layer.
const snmpdStartupBudget = 5 * time.Minute

// TestMain owns the T1 snmpd container lifecycle: build, start, probe,
// run tests, terminate. When Docker is unavailable the entire tier is
// skipped (exit 0); when container start fails the tier fails with a
// diagnostic (exit 1). Short mode runs offline tests before any Docker setup.
func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	if !testenv.HasDocker() {
		fmt.Fprintln(os.Stderr, "[snmp_integration_t1] docker not on PATH; skipping tier")
		os.Exit(0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), snmpdStartupBudget)

	target, cleanup, err := testenv.StartSnmpd(ctx, snmpdContextDir)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[snmp_integration_t1] StartSnmpd: %v\n", err)
		os.Exit(1)
	}
	testenv.SetTarget(target)
	fmt.Fprintf(os.Stderr, "[snmp_integration_t1] snmpd up at %s\n", target)

	code := m.Run()
	if err := cleanup(); err != nil {
		fmt.Fprintf(os.Stderr, "[snmp_integration_t1] cleanup: %v\n", err)
		code = 1
	}
	os.Exit(code)
}
