//go:build yang_integration_t1

package testenv

import (
	"context"
	"net"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/gnmi"
	"go.aledante.io/FlowSeer/src/common/netconf"
	"go.aledante.io/FlowSeer/src/common/restconf"
)

// Netopeer2Credentials is the reference image's built-in account.
const (
	Netopeer2User     = "netconf"
	Netopeer2Password = "netconf"
)

// readyTimeout caps each protocol-level readiness probe.
const readyTimeout = 60 * time.Second

// probeBackoff is the inter-attempt delay for readiness loops.
const probeBackoff = 500 * time.Millisecond

// StartNetopeer2 builds the fixture-loaded netopeer2 image from
// contextDir and starts it with port 830 mapped. Readiness is a
// successful netconf.Dial plus capability exchange through the public
// constructor. The returned cleanup terminates the container.
func StartNetopeer2(ctx context.Context, contextDir string) (target string, cleanup func(), err error) {
	target, cleanup, err = startFromContext(ctx, contextDir, "830/tcp",
		wait.ForListeningPort("830/tcp").WithStartupTimeout(120*time.Second))
	if err != nil {
		return "", nil, err
	}
	deadline := time.Now().Add(readyTimeout)
	for {
		s, derr := netconf.Dial(ctx, target, netconf.Options{
			Username:              Netopeer2User,
			Password:              Netopeer2Password,
			InsecureIgnoreHostKey: true,
			DialTimeout:           5 * time.Second,
		})
		if derr == nil {
			_ = s.Close(ctx)
			return target, cleanup, nil
		}
		if time.Now().After(deadline) {
			cleanup()
			return "", nil, errs.Wrap(derr, "netopeer2 never became NETCONF-ready")
		}
		time.Sleep(probeBackoff)
	}
}

// StartClixon builds the RESTCONF-enabled clixon image from
// contextDir and starts it with port 80 mapped. Readiness is a
// successful restconf.Dial (root discovery via host-meta) through the
// public constructor.
func StartClixon(ctx context.Context, contextDir string) (baseURL string, cleanup func(), err error) {
	target, cleanup, err := startFromContext(ctx, contextDir, "80/tcp",
		wait.ForLog("clixon restconf started").WithStartupTimeout(120*time.Second))
	if err != nil {
		return "", nil, err
	}
	baseURL = "http://" + target
	deadline := time.Now().Add(readyTimeout)
	for {
		s, derr := restconf.Dial(ctx, baseURL, restconf.Options{Timeout: 5 * time.Second})
		if derr == nil {
			_ = s.Close()
			return baseURL, cleanup, nil
		}
		if time.Now().After(deadline) {
			cleanup()
			return "", nil, errs.Wrap(derr, "clixon never became RESTCONF-ready")
		}
		time.Sleep(probeBackoff)
	}
}

// StartGNMITarget builds FlowSeer's reference gNMI target from
// contextDir and starts it with port 9339 mapped. Readiness is a
// successful gnmi.Dial (Capabilities exchange) through the public
// constructor.
func StartGNMITarget(ctx context.Context, contextDir string) (target string, cleanup func(), err error) {
	target, cleanup, err = startFromContext(ctx, contextDir, "9339/tcp",
		wait.ForLog("gnmitarget listening").WithStartupTimeout(120*time.Second))
	if err != nil {
		return "", nil, err
	}
	deadline := time.Now().Add(readyTimeout)
	for {
		s, derr := gnmi.Dial(ctx, target, gnmi.Options{Plaintext: true, DialTimeout: 5 * time.Second})
		if derr == nil {
			_ = s.Close()
			return target, cleanup, nil
		}
		if time.Now().After(deadline) {
			cleanup()
			return "", nil, errs.Wrap(derr, "gnmitarget never became gNMI-ready")
		}
		time.Sleep(probeBackoff)
	}
}

// startFromContext builds and starts one container from a Docker
// build context, returning host:port for the exposed port.
func startFromContext(ctx context.Context, contextDir, port string, waitFor wait.Strategy) (string, func(), error) {
	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:    contextDir,
				Dockerfile: "Dockerfile",
				KeepImage:  true,
			},
			ExposedPorts: []string{port},
			WaitingFor:   waitFor,
		},
		Started: true,
	}
	ctr, err := testcontainers.GenericContainer(ctx, req)
	if err != nil {
		return "", nil, errs.Wrapf(err, "start container from %s", contextDir)
	}
	cleanup := func() { _ = ctr.Terminate(context.Background()) }

	host, err := ctr.Host(ctx)
	if err != nil {
		cleanup()
		return "", nil, errs.Wrap(err, "container host")
	}
	mapped, err := ctr.MappedPort(ctx, port)
	if err != nil {
		cleanup()
		return "", nil, errs.Wrap(err, "container mapped port")
	}
	return net.JoinHostPort(host, mapped.Port()), cleanup, nil
}
