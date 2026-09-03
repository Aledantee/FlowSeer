//go:build snmp_integration_t2

package integration

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/snmp/test/integration/testenv"
)

// srlinuxTopologyPath is the .clab.yaml the t2 tier deploys, resolved
// relative to the test working directory (src/common/snmp/test/integration/).
const srlinuxTopologyPath = "testdata/containerlab/srlinux.clab.yaml"

// srlinuxStartupBudget caps the wall-clock window for "from cold to
// SNMPv3 ready". First-run pulls ghcr.io/nokia/srlinux (~1 GB) and
// runs SR Linux's init sequence; the budget includes both.
const srlinuxStartupBudget = 10 * time.Minute

// TestMain owns the T2 SR Linux lab lifecycle: containerlab deploy,
// SNMPv3 readiness probe, run tests, containerlab destroy. The tier
// skips cleanly (exit 0) when either Docker or containerlab is
// missing; container errors exit 1 with a diagnostic.
//
// Short mode runs offline tests before any Docker or containerlab setup.
func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	if !testenv.HasDocker() {
		fmt.Fprintln(os.Stderr, "[snmp_integration_t2] docker not on PATH; skipping tier")
		os.Exit(0)
	}
	if !testenv.HasContainerlab() {
		fmt.Fprintln(os.Stderr, "[snmp_integration_t2] containerlab not on PATH; install from https://containerlab.dev/ to run this tier")
		os.Exit(0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), srlinuxStartupBudget)

	target, execFn, cleanup, err := testenv.StartSRLinux(ctx, srlinuxTopologyPath)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[snmp_integration_t2] StartSRLinux: %v\n", err)
		os.Exit(1)
	}
	testenv.SetTarget(target)
	testenv.SetT2Exec(execFn)
	fmt.Fprintf(os.Stderr, "[snmp_integration_t2] SR Linux up at %s\n", target)

	code := m.Run()
	if err := cleanup(); err != nil {
		fmt.Fprintf(os.Stderr, "[snmp_integration_t2] cleanup: %v\n", err)
		code = 1
	}
	os.Exit(code)
}
