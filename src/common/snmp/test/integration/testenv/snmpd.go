//go:build snmp_integration_t1

package testenv

import (
	"context"
	"net"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"go.aledante.io/FlowSeer/src/common/snmp"
)

// snmpdReadyTimeout caps the SNMP-level readiness probe wall time. The
// testcontainers wait strategy already blocks until snmpd logs its
// startup banner; this probe is the second-stage check that v2c GET
// works end-to-end.
const snmpdReadyTimeout = 30 * time.Second

// snmpdProbeBackoff is the inter-attempt delay for the readiness loop.
const snmpdProbeBackoff = 500 * time.Millisecond

// StartSnmpd builds the FlowSeer T1 snmpd image from contextDir and
// launches a container with UDP/161 mapped to a random host port.
// contextDir is the Docker build context — typically "testdata/snmpd"
// relative to the test working directory.
//
// Readiness is established in two stages:
//
//  1. testcontainers waits for snmpd's startup log line ("NET-SNMP
//     version") to appear on stdout, guaranteeing the agent has
//     finished initialisation.
//  2. A polling loop dials via [snmp.NewSession] with
//     SNMPv2c and runs a Get sysUpTime.0 until it succeeds or the
//     [snmpdReadyTimeout] elapses. Probing through [snmp.NewSession] rather
//     than a direct Backend call means the readiness check survives a
//     Backend swap unchanged.
//
// The returned cleanup function terminates the container; callers
// (typically a tier's TestMain) MUST invoke it before the process
// exits to avoid leaking Docker resources. testcontainers' reaper
// also reaps the container if the process dies before cleanup runs,
// so a panicking test still tears down.
func StartSnmpd(ctx context.Context, contextDir string) (target string, cleanup func(), err error) {
	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:    contextDir,
				Dockerfile: "Dockerfile",
				KeepImage:  true,
			},
			ExposedPorts: []string{"161/udp"},
			WaitingFor:   wait.ForLog("NET-SNMP version").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	}
	ctr, err := testcontainers.GenericContainer(ctx, req)
	if err != nil {
		return "", nil, errs.Wrap(err, "start snmpd container")
	}
	cleanup = func() {
		_ = ctr.Terminate(context.Background())
	}

	host, err := ctr.Host(ctx)
	if err != nil {
		cleanup()
		return "", nil, errs.Wrap(err, "snmpd container host")
	}
	port, err := ctr.MappedPort(ctx, "161/udp")
	if err != nil {
		cleanup()
		return "", nil, errs.Wrap(err, "snmpd container mapped port")
	}
	target = net.JoinHostPort(host, port.Port())

	if err := waitForSnmpdReady(ctx, target); err != nil {
		cleanup()
		return "", nil, errs.Wrapf(err, "snmpd readiness probe at %s", target)
	}
	return target, cleanup, nil
}

// waitForSnmpdReady probes the running snmpd via [snmp.NewSession]
// until a Get sysUpTime.0 succeeds or [snmpdReadyTimeout] elapses.
//
// The probe uses SNMPv2c with [snmp.MinSecurityNoAuth] because the
// default [snmp.MinSecurityAuthNoPriv] floor rejects v2c sessions at
// Dial — the readiness check needs to reach the wire below that floor.
func waitForSnmpdReady(ctx context.Context, target string) error {
	probeCtx, cancel := context.WithTimeout(ctx, snmpdReadyTimeout)
	defer cancel()

	sysUpTime := snmp.MustOID(1, 3, 6, 1, 2, 1, 1, 3, 0)
	var lastErr error
	for {
		sess, err := snmp.NewSession(probeCtx, target, snmp.V2c,
			snmp.WithCommunity("public"),
			snmp.WithMinSecurity(snmp.MinSecurityNoAuth),
			snmp.WithTimeout(2*time.Second),
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

		// Sleep with context-cancellation observation. Sleeping
		// unconditionally costs up to one full backoff interval of
		// dead time after the outer deadline fires.
		select {
		case <-time.After(snmpdProbeBackoff):
		case <-probeCtx.Done():
			if lastErr == nil {
				return probeCtx.Err()
			}
			return errs.Wrap(lastErr, "probe exhausted")
		}
	}
}
