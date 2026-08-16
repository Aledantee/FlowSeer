//go:build snmp_integration_t2

package testenv

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.aledante.io/ae"

	"go.aledante.io/FlowSeer/src/common/snmp"
)

// srlinuxDeployTimeout caps containerlab deploy wall time. First-time
// runs pull ghcr.io/nokia/srlinux (~1 GB) and run SR Linux's init,
// which takes 60–90s once the image is local.
const srlinuxDeployTimeout = 8 * time.Minute

// srlinuxReadyTimeout caps the SNMP-level readiness probe after deploy
// returns. SR Linux's SNMP server takes a few seconds to listen after
// the management plane reports up.
const srlinuxReadyTimeout = 2 * time.Minute

// srlinuxProbeBackoff is the inter-attempt delay for the readiness
// loop.
const srlinuxProbeBackoff = 2 * time.Second

// SRLinuxUSMConfig is the USM user baked into the SR Linux startup
// config. SHA-256 / AES-256 is the chosen pair: modern, in the
// library's supported set, accepted by gosnmp v1.43.2, and
// supported by SR Linux. T2's matrix gap vs T1 is documented in the
// test-file headers.
//
// Passphrases are non-secret integration-test values pinned here so
// the startup config and the readiness probe agree.
var SRLinuxUSMConfig = snmp.USMConfig{
	Username:       "flowseer",
	AuthProtocol:   snmp.AuthSHA256,
	AuthPassphrase: "flowseer-auth-passphrase",
	PrivProtocol:   snmp.PrivAES256,
	PrivPassphrase: "flowseer-priv-passphrase",
}

// ExecFn is the shape of the containerlab exec callback returned by
// StartSRLinux. Callers pass a CLI command string; the function runs
// it inside the SR Linux node and returns combined stdout+stderr.
//
// Used by T2's admin-state toggle test, where toggling an interface
// is the trigger for linkUp / linkDown trap emission.
type ExecFn func(cmd string) (output string, err error)

// t2Exec holds the containerlab exec callback the t2 TestMain seeds
// for downstream tests. Mutated only at process-start time (in
// TestMain before m.Run); tests treat it as read-mostly.
var t2Exec ExecFn

// SetT2Exec records the containerlab exec callback returned by
// [StartSRLinux]. Called once by the t2 TestMain before m.Run.
func SetT2Exec(fn ExecFn) { t2Exec = fn }

// T2Exec returns the containerlab exec callback recorded by
// [SetT2Exec], or nil if no t2 TestMain has run. Tests use the
// callback to drive CLI operations inside the SR Linux node
// (admin-state toggle for the link-trap test, etc.).
func T2Exec() ExecFn { return t2Exec }

// StartSRLinux deploys the FlowSeer T2 single-node SR Linux topology
// via containerlab and waits for SNMPv3 readiness against the node's
// management address.
//
// topologyPath is the path to the .clab.yaml file relative to the
// test working directory (typically "testdata/containerlab/srlinux.clab.yaml").
//
// Returns:
//   - target: "<management-ip>:161" for SNMP dial.
//   - execFn: callback for running CLI commands inside the SR Linux
//     node (used by the link-trap test for admin-state toggle).
//   - cleanup: deferred teardown via `containerlab destroy --cleanup`.
//
// Cleanup runs containerlab destroy even when probe failures occur,
// so a botched run does not leave a lab behind.
func StartSRLinux(ctx context.Context, topologyPath string) (target string, execFn ExecFn, cleanup func(), err error) {
	deployCtx, cancel := context.WithTimeout(ctx, srlinuxDeployTimeout)
	defer cancel()

	// Assign cleanup BEFORE invoking `containerlab deploy`. A
	// partial deploy that times out mid-provision still leaves
	// container/CNI state behind, and containerlab has no
	// Ryuk-equivalent — so the cleanup MUST be reachable even on
	// the deploy-error path.
	cleanup = func() {
		c, ccancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer ccancel()
		destroy := exec.CommandContext(c, "containerlab", "destroy", "-t", topologyPath, "--cleanup")
		if out, derr := destroy.CombinedOutput(); derr != nil {
			// Log to stderr so a hung Docker daemon or removed
			// topology file is visible at next-run. A silent
			// swallowed error here means a developer finds stale
			// lab state on their next `make t2` invocation with
			// no breadcrumb.
			fmt.Fprintf(os.Stderr,
				"[snmp_integration_t2] containerlab destroy failed: %v\n  output: %s\n",
				derr, strings.TrimSpace(string(out)))
		}
	}

	cmd := exec.CommandContext(deployCtx, "containerlab", "deploy",
		"-t", topologyPath, "--reconfigure")
	if out, deployErr := cmd.CombinedOutput(); deployErr != nil {
		cleanup()
		return "", nil, nil, ae.Wrapf("containerlab deploy: %s", deployErr, strings.TrimSpace(string(out)))
	}

	inspectCmd := exec.CommandContext(deployCtx, "containerlab", "inspect", "-t", topologyPath, "--format", "json")
	inspectOut, inspectErr := inspectCmd.CombinedOutput()
	if inspectErr != nil {
		cleanup()
		return "", nil, nil, ae.Wrapf("containerlab inspect: %s", inspectErr, strings.TrimSpace(string(inspectOut)))
	}

	nodes, err := parseClabInspect(inspectOut)
	if err != nil {
		cleanup()
		return "", nil, nil, ae.Wrap("parse inspect JSON", err)
	}
	if len(nodes) == 0 {
		cleanup()
		return "", nil, nil, ae.Msg("containerlab inspect returned zero nodes")
	}
	node := nodes[0]
	if node.IPv4 == "" {
		cleanup()
		return "", nil, nil, ae.New().Attr("node", node.Name).Msg("node has no IPv4 address")
	}
	target = net.JoinHostPort(node.IPv4, "161")

	execFn = func(c string) (string, error) {
		ec, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		ex := exec.CommandContext(ec, "containerlab", "exec",
			"-t", topologyPath,
			"--label", "clab-node-name="+node.Name,
			"--cmd", c)
		out, err := ex.CombinedOutput()
		return string(out), err
	}

	if err := waitForSRLinuxReady(ctx, target); err != nil {
		cleanup()
		return "", nil, nil, ae.Wrapf("SR Linux readiness probe at %s", err, target)
	}
	return target, execFn, cleanup, nil
}

// clabInspectNode mirrors the JSON shape `containerlab inspect
// --format json` emits per node. The schema has shifted across
// containerlab releases; this struct captures the fields the harness
// actually uses. Unknown fields are ignored.
type clabInspectNode struct {
	Name string `json:"name"`
	IPv4 string `json:"ipv4_address"`
}

type clabInspectV050 struct {
	Containers []clabInspectNode `json:"containers"`
}

// parseClabInspect parses the JSON output of `containerlab inspect
// --format json`. It tolerates the v0.50+ "containers" envelope and
// the older bare-array shape, returning a normalised node list with
// CIDR suffixes stripped from IPv4 addresses.
func parseClabInspect(data []byte) ([]clabInspectNode, error) {
	var nodes []clabInspectNode
	var v050 clabInspectV050
	if err := json.Unmarshal(data, &v050); err == nil && len(v050.Containers) > 0 {
		nodes = v050.Containers
	} else {
		var bare []clabInspectNode
		if berr := json.Unmarshal(data, &bare); berr != nil {
			return nil, berr
		}
		nodes = bare
	}
	for i := range nodes {
		if idx := strings.IndexByte(nodes[i].IPv4, '/'); idx > 0 {
			nodes[i].IPv4 = nodes[i].IPv4[:idx]
		}
	}
	return nodes, nil
}

// waitForSRLinuxReady polls (via [snmp.NewSession]) SNMPv3 Get sysUpTime.0
// until success or timeout. The probe uses [SRLinuxUSMConfig] —
// matching the user the startup-config provisions — and runs through
// [snmp.NewSession].
func waitForSRLinuxReady(ctx context.Context, target string) error {
	probeCtx, cancel := context.WithTimeout(ctx, srlinuxReadyTimeout)
	defer cancel()

	sysUpTime := snmp.MustOID(1, 3, 6, 1, 2, 1, 1, 3, 0)
	var lastErr error
	for {
		sess, err := snmp.NewSession(probeCtx, target, snmp.V3,
			snmp.WithUSM(SRLinuxUSMConfig),
			snmp.WithMinSecurity(snmp.MinSecurityNoAuth),
			snmp.WithTimeout(3*time.Second),
			snmp.WithRetries(1),
		)
		if err == nil {
			_, err = sess.Get(probeCtx, []snmp.OID{sysUpTime})
			_ = sess.Close()
			if err == nil {
				return nil
			}
		}
		lastErr = err

		// Sleep with context-cancellation observation. The earlier
		// shape slept unconditionally, then checked ctx.Done() at
		// the top of the next iteration — costing up to one full
		// backoff interval (2s) of dead time after the outer
		// deadline fired.
		select {
		case <-time.After(srlinuxProbeBackoff):
		case <-probeCtx.Done():
			if lastErr == nil {
				return probeCtx.Err()
			}
			return ae.Wrap("probe exhausted", lastErr)
		}
	}
}
