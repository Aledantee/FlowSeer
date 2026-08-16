//go:build snmp_integration_t2

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/snmp/integration/testenv"
)

// srlinuxTopologyPath is the .clab.yaml the t2 tier deploys, resolved
// relative to the test working directory (common/snmp/integration/).
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
// Cleanup is registered before m.Run so a panicking test still tears
// the lab down via `containerlab destroy --cleanup`.
func TestMain(m *testing.M) {
	if !testenv.HasDocker() {
		fmt.Fprintln(os.Stderr, "[snmp_integration_t2] docker not on PATH; skipping tier")
		os.Exit(0)
	}
	if !testenv.HasContainerlab() {
		fmt.Fprintln(os.Stderr, "[snmp_integration_t2] containerlab not on PATH; install from https://containerlab.dev/ to run this tier")
		os.Exit(0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), srlinuxStartupBudget)
	defer cancel()

	target, execFn, cleanup, err := testenv.StartSRLinux(ctx, srlinuxTopologyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[snmp_integration_t2] StartSRLinux: %v\n", err)
		if cleanup != nil {
			cleanup()
		}
		os.Exit(1)
	}
	testenv.SetTarget(target)
	testenv.SetT2Exec(execFn)
	fmt.Fprintf(os.Stderr, "[snmp_integration_t2] SR Linux up at %s\n", target)

	code := m.Run()
	cleanup()
	os.Exit(code)
}
