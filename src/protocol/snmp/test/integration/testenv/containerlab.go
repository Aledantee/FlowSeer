//go:build snmp_integration_t2

package testenv

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os/exec"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
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
// the startup config and the readiness probe agree. Treat this value as read-only
// once startup begins; concurrent writes are not supported.
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
// Callbacks returned by [StartSRLinux] use an independent 30-second deadline and
// preserve context.DeadlineExceeded on timeout. They report process errors; the
// caller must also inspect CLI output for rejected commands that exit zero.
type ExecFn func(cmd string) (output string, err error)

// t2Exec holds the containerlab exec callback the t2 TestMain seeds
// for downstream tests. Mutated only at process-start time (in
// TestMain before m.Run); tests treat it as read-mostly.
var t2Exec ExecFn

// SetT2Exec records the containerlab exec callback returned by
// [StartSRLinux]. Call it before m.Run; it must not overlap calls to [T2Exec].
func SetT2Exec(fn ExecFn) { t2Exec = fn }

// T2Exec returns the containerlab exec callback recorded by
// [SetT2Exec], or nil if no t2 TestMain has run. Tests use the
// callback to drive CLI operations inside the SR Linux node. Concurrent reads
// are safe after SetT2Exec completes; replacing the callback requires exclusive
// access.
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
// Failed startup attempts cleanup and joins its errors with the startup cause.
// On success the caller must invoke cleanup before exiting. Cleanup has a fresh
// two-minute context and returns any destroy error. Call it once.
// The startup context can be canceled after return without
// disabling the exec callback or cleanup.
func StartSRLinux(ctx context.Context, topologyPath string) (target string, execFn ExecFn, cleanup func() error, err error) {
	deployCtx, cancel := context.WithTimeout(ctx, srlinuxDeployTimeout)
	defer cancel()

	// A partial deployment can leave container/CNI state behind; destroy
	// remains available when deployment fails because containerlab has no reaper.
	destroyLab := func() error {
		c, ccancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer ccancel()
		destroy := exec.CommandContext(c, "containerlab", "destroy", "-t", topologyPath, "--cleanup")
		if out, derr := destroy.CombinedOutput(); derr != nil {
			return errors.Join(c.Err(), errs.Wrapf(derr, "containerlab destroy: %s", strings.TrimSpace(string(out))))
		}
		return nil
	}
	cleanup = destroyLab

	cmd := exec.CommandContext(deployCtx, "containerlab", "deploy",
		"-t", topologyPath, "--reconfigure")
	if out, deployErr := cmd.CombinedOutput(); deployErr != nil {
		return "", nil, nil, errors.Join(deployCtx.Err(), errs.Wrapf(deployErr, "containerlab deploy: %s", strings.TrimSpace(string(out))), destroyLab())
	}

	inspectCmd := exec.CommandContext(deployCtx, "containerlab", "inspect", "-t", topologyPath, "--format", "json")
	inspectOut, inspectErr := inspectCmd.CombinedOutput()
	if inspectErr != nil {
		return "", nil, nil, errors.Join(deployCtx.Err(), errs.Wrapf(inspectErr, "containerlab inspect: %s", strings.TrimSpace(string(inspectOut))), destroyLab())
	}

	nodes, err := parseClabInspect(inspectOut)
	if err != nil {
		return "", nil, nil, errors.Join(errs.Wrap(err, "parse inspect JSON"), destroyLab())
	}
	if len(nodes) == 0 {
		return "", nil, nil, errors.Join(errs.Msg("containerlab inspect returned zero nodes"), destroyLab())
	}
	node := nodes[0]
	if node.IPv4 == "" {
		return "", nil, nil, errors.Join(errs.New().Attr("node", node.Name).Msg("node has no IPv4 address"), destroyLab())
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
		return string(out), errors.Join(ec.Err(), err)
	}

	if err := waitForSRLinuxReady(ctx, target); err != nil {
		return "", nil, nil, errors.Join(errs.Wrapf(err, "SR Linux readiness probe at %s", target), destroyLab())
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
// the older bare-array shape, returning a normalized node list with
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
		if err := probeCtx.Err(); err != nil {
			return err
		}
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
			return errors.Join(probeCtx.Err(), errs.Wrap(lastErr, "probe exhausted"))
		}
	}
}
