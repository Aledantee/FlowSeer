//go:build snmp_integration_t1

package testenv

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/secret"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// snmpdReadyTimeout caps the SNMP-level readiness probe wall time. The
// testcontainers wait strategy already blocks until snmpd logs its
// startup banner; this probe is the second-stage check that v2c GET
// works end-to-end.
const snmpdReadyTimeout = 30 * time.Second

// snmpdProbeBackoff is the inter-attempt delay for the readiness loop.
const snmpdProbeBackoff = 500 * time.Millisecond

var startSnmpdContainer = testcontainers.GenericContainer

// StartSnmpd builds the FlowSeer T1 snmpd image from contextDir and
// launches a container with UDP/161 mapped to a random host port.
// contextDir is the Docker build context — typically "testdata/snmpd"
// relative to the test working directory.
//
// Readiness is established in two stages:
//
//  1. testcontainers waits for snmpd's startup log line ("NET-SNMP
//     version") to appear on stdout, guaranteeing the agent has
//     finished initialization.
//  2. A polling loop dials via [snmp.NewSession] with
//     SNMPv2c and runs a Get sysUpTime.0 until it succeeds or the
//     [snmpdReadyTimeout] elapses. Probing through [snmp.NewSession] rather
//     than a direct Backend call means the readiness check survives a
//     Backend swap unchanged.
//
// Failed startup terminates any partial container and joins cleanup errors with
// the startup cause. On success, callers must invoke cleanup before exiting.
// Cleanup uses a fresh 30-second context and returns any termination error.
// Call it once. The startup context can be canceled
// once this function returns; cleanup does not depend on it.
func StartSnmpd(ctx context.Context, contextDir string) (target string, cleanup func() error, err error) {
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
	ctr, err := startSnmpdContainer(ctx, req)
	if err != nil {
		return "", nil, errors.Join(errs.Wrap(err, "start snmpd container"), terminateContainer(ctr))
	}
	cleanup = func() error { return terminateContainer(ctr) }

	host, err := ctr.Host(ctx)
	if err != nil {
		return "", nil, errors.Join(errs.Wrap(err, "snmpd container host"), terminateContainer(ctr))
	}
	port, err := ctr.MappedPort(ctx, "161/udp")
	if err != nil {
		return "", nil, errors.Join(errs.Wrap(err, "snmpd container mapped port"), terminateContainer(ctr))
	}
	target = net.JoinHostPort(host, port.Port())

	if err := waitForSnmpdReady(ctx, target); err != nil {
		return "", nil, errors.Join(errs.Wrapf(err, "snmpd readiness probe at %s", target), terminateContainer(ctr))
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
	sysUpTime := snmp.MustOID(1, 3, 6, 1, 2, 1, 1, 3, 0)
	return waitReady(ctx, snmpdReadyTimeout, snmpdProbeBackoff, func(ctx context.Context) error {
		sess, err := snmp.NewSession(ctx, target, snmp.V2c,
			snmp.WithCommunity(secret.NewString("public")),
			snmp.WithMinSecurity(snmp.MinSecurityNoAuth),
			snmp.WithTimeout(2*time.Second),
			snmp.WithRetries(1),
		)
		if err != nil {
			return err
		}
		_, err = sess.Get(ctx, []snmp.OID{sysUpTime})
		_ = sess.Close()
		return err
	})
}
