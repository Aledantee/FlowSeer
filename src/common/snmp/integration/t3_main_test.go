//go:build snmp_integration_t3

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/snmp/integration/testenv"
)

// snmprecManifestPath is the YAML manifest the t3 tier loads,
// resolved relative to the test working directory.
const snmprecManifestPath = "testdata/snmprec/manifest.yaml"

// snmprecDataDir is the host directory snmpsim bind-mounts as its
// replay corpus. testenv.StartSnmpsim resolves this to an absolute
// path before passing it to Docker.
const snmprecDataDir = "testdata/snmprec"

// snmpsimContextDir is the Docker build context for the snmpsim
// image, mirroring the testdata/snmpd/ pattern.
const snmpsimContextDir = "testdata/snmpsim"

// snmpsimStartupBudget caps the wall-clock window for "from cold to
// snmpsim-ready". First-run pulls the lextudio/snmpsim image; budget
// is sized to cover the pull.
const snmpsimStartupBudget = 5 * time.Minute

// t3Manifest is the loaded manifest, populated by TestMain and read
// by t3_replay_test.go's table-driven test.
var t3Manifest Manifest

// TestMain owns the T3 snmpsim container lifecycle: load the
// manifest (load-time validation happens here so a broken manifest
// fails the tier before container start), launch snmpsim with the
// data dir mounted, run tests, terminate.
//
// The tier skips cleanly when Docker is absent. When the manifest
// is broken or container start fails, the tier exits 1 with a
// diagnostic; cleanup runs in either case.
func TestMain(m *testing.M) {
	if !testenv.HasDocker() {
		fmt.Fprintln(os.Stderr, "[snmp_integration_t3] docker not on PATH; skipping tier")
		os.Exit(0)
	}

	manifest, err := LoadManifest(snmprecManifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[snmp_integration_t3] LoadManifest: %v\n", err)
		os.Exit(1)
	}
	if len(manifest.Entries) == 0 {
		fmt.Fprintln(os.Stderr, "[snmp_integration_t3] manifest contains no entries; nothing to replay")
		os.Exit(1)
	}
	t3Manifest = manifest
	probeCommunity := manifest.Entries[0].SnmpsimContext

	ctx, cancel := context.WithTimeout(context.Background(), snmpsimStartupBudget)
	defer cancel()

	target, cleanup, err := testenv.StartSnmpsim(ctx, snmpsimContextDir, snmprecDataDir, probeCommunity)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[snmp_integration_t3] StartSnmpsim: %v\n", err)
		if cleanup != nil {
			cleanup()
		}
		os.Exit(1)
	}
	testenv.SetTarget(target)
	fmt.Fprintf(os.Stderr, "[snmp_integration_t3] snmpsim up at %s (%d manifest entries)\n", target, len(manifest.Entries))

	code := m.Run()
	cleanup()
	os.Exit(code)
}
