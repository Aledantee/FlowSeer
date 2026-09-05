//go:build snmp_integration_t3 || snmp_integration_t5

package testenv

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// snmpsimReadyTimeout caps the SNMP-level readiness probe wall time.
const snmpsimReadyTimeout = 30 * time.Second

// snmpsimProbeBackoff is the inter-attempt delay for the readiness
// loop.
const snmpsimProbeBackoff = 500 * time.Millisecond

// snmpsimContainerPort is the UDP port snmpsim listens on inside the
// container. The standard SNMP port 161 requires elevated privileges;
// snmpsim's typical workaround is the unprivileged 1024 endpoint.
// The host-side port testcontainers assigns is reported as the
// target.
const snmpsimContainerPort = "1024/udp"

var startSnmpsimContainer = testcontainers.GenericContainer

// StartSnmpsim builds the FlowSeer T3 snmpsim image and launches it
// with dataDir bind-mounted as the replay corpus. The container
// exposes UDP/1024 randomly mapped to a host port; readiness is
// established by an SNMPv2c Get sysUpTime.0 probe through the
// [snmp.NewSession] using probeCommunity (matching the first manifest
// entry's snmpsim_context — snmpsim routes by v2c community →
// context).
//
// contextDir is the Docker build context (typically
// "testdata/snmpsim"); the Dockerfile installs the snmpsim-lextudio
// Python package on a python:3.12-slim base.
//
// dataDir is the host path to the directory containing .snmprec
// files; it's bind-mounted at /usr/local/snmpsim/data inside the
// container.
//
// Failed startup terminates any partial container and joins cleanup errors with
// the startup cause. On success, callers must invoke cleanup before exiting.
// Cleanup uses a fresh 30-second context and returns any termination error.
// Call it once. The startup context can be canceled
// once this function returns; cleanup does not depend on it.
func StartSnmpsim(ctx context.Context, contextDir, dataDir, probeCommunity string) (target string, cleanup func() error, err error) {
	absData, err := filepath.Abs(dataDir)
	if err != nil {
		return "", nil, errs.Wrapf(err, "resolve data dir %s", dataDir)
	}

	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:    contextDir,
				Dockerfile: "Dockerfile",
				KeepImage:  true,
			},
			ExposedPorts: []string{snmpsimContainerPort},
			HostConfigModifier: func(hc *container.HostConfig) {
				hc.Mounts = append(hc.Mounts, mount.Mount{
					Type:   mount.TypeBind,
					Source: absData,
					Target: "/usr/local/snmpsim/data",
				})
			},
			WaitingFor: wait.ForLog("Listening at UDP/IPv4 endpoint").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	}
	ctr, err := startSnmpsimContainer(ctx, req)
	if err != nil {
		return "", nil, errors.Join(errs.Wrap(err, "start snmpsim container"), terminateContainer(ctr))
	}
	cleanup = func() error { return terminateContainer(ctr) }

	host, err := ctr.Host(ctx)
	if err != nil {
		return "", nil, errors.Join(errs.Wrap(err, "snmpsim container host"), terminateContainer(ctr))
	}
	port, err := ctr.MappedPort(ctx, snmpsimContainerPort)
	if err != nil {
		return "", nil, errors.Join(errs.Wrap(err, "snmpsim container mapped port"), terminateContainer(ctr))
	}
	target = net.JoinHostPort(host, port.Port())

	if err := waitForSnmpsimReady(ctx, target, probeCommunity); err != nil {
		return "", nil, errors.Join(errs.Wrapf(err, "snmpsim readiness probe at %s", target), terminateContainer(ctr))
	}
	return target, cleanup, nil
}

// waitForSnmpsimReady polls v2c Get sysUpTime.0 with the manifest's
// first-entry community as the context selector. snmpsim's community
// → context mapping routes to the matching .snmprec file; if the
// probe succeeds the engine is live and capable of serving the
// replay.
func waitForSnmpsimReady(ctx context.Context, target, community string) error {
	sysUpTime := snmp.MustOID(1, 3, 6, 1, 2, 1, 1, 3, 0)
	return waitReady(ctx, snmpsimReadyTimeout, snmpsimProbeBackoff, func(ctx context.Context) error {
		sess, err := snmp.NewSession(ctx, target, snmp.V2c,
			snmp.WithCommunity(community),
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
