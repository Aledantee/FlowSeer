//go:build yang_integration_t1

package testenv

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/protocol/gnmi"
	"go.aledante.io/FlowSeer/src/protocol/netconf"
	"go.aledante.io/FlowSeer/src/protocol/restconf"
)

const (
	// Netopeer2User is the reference image's built-in login name.
	Netopeer2User = "netconf"
	// Netopeer2Password authenticates the reference image's built-in account.
	Netopeer2Password = "netconf"
)

// StartNetopeer2 builds the fixture-loaded netopeer2 image from
// contextDir and starts it with port 830 mapped. Readiness is a
// successful netconf.Dial plus capability exchange through the public
// constructor. Call cleanup once after use; it returns any termination error.
func StartNetopeer2(ctx context.Context, contextDir string) (target string, cleanup func() error, err error) {
	target, cleanup, err = startFromContext(ctx, contextDir, "830/tcp",
		wait.ForListeningPort("830/tcp").WithStartupTimeout(120*time.Second))
	if err != nil {
		return "", nil, err
	}
	err = waitReady(ctx, func(probeCtx context.Context) error {
		s, err := netconf.Dial(probeCtx, target, netconf.Options{
			Username:              Netopeer2User,
			Password:              Netopeer2Password,
			InsecureIgnoreHostKey: true,
			DialTimeout:           5 * time.Second,
		})
		if err != nil {
			return err
		}
		return s.Close(probeCtx)
	})
	if err != nil {
		cleanupErr := cleanup()
		if ctx.Err() != nil {
			if cleanupErr != nil {
				return "", nil, errors.Join(ctx.Err(), cleanupErr)
			}
			return "", nil, ctx.Err()
		}
		return "", nil, errs.Wrap(errors.Join(err, cleanupErr), "netopeer2 never became NETCONF-ready")
	}
	return target, cleanup, nil
}

// StartClixon builds the RESTCONF-enabled clixon image from
// contextDir and starts it with port 80 mapped. Readiness is a
// successful restconf.Dial (root discovery via host-meta) through the
// public constructor. Call cleanup once after use; it returns any termination error.
func StartClixon(ctx context.Context, contextDir string) (baseURL string, cleanup func() error, err error) {
	target, cleanup, err := startFromContext(ctx, contextDir, "80/tcp",
		wait.ForLog("clixon restconf started").WithStartupTimeout(120*time.Second))
	if err != nil {
		return "", nil, err
	}
	baseURL = "http://" + target
	err = waitReady(ctx, func(probeCtx context.Context) error {
		s, err := restconf.Dial(probeCtx, baseURL, restconf.Options{Timeout: 5 * time.Second})
		if err != nil {
			return err
		}
		return s.Close()
	})
	if err != nil {
		cleanupErr := cleanup()
		if ctx.Err() != nil {
			if cleanupErr != nil {
				return "", nil, errors.Join(ctx.Err(), cleanupErr)
			}
			return "", nil, ctx.Err()
		}
		return "", nil, errs.Wrap(errors.Join(err, cleanupErr), "clixon never became RESTCONF-ready")
	}
	return baseURL, cleanup, nil
}

// StartGNMITarget builds FlowSeer's reference gNMI target from
// contextDir and starts it with port 9339 mapped. Readiness is a
// successful gnmi.Dial (Capabilities exchange) through the public
// constructor. Call cleanup once after use; it returns any termination error.
func StartGNMITarget(ctx context.Context, contextDir string) (target string, cleanup func() error, err error) {
	target, cleanup, err = startFromContext(ctx, contextDir, "9339/tcp",
		wait.ForLog("gnmitarget listening").WithStartupTimeout(120*time.Second))
	if err != nil {
		return "", nil, err
	}
	err = waitReady(ctx, func(probeCtx context.Context) error {
		s, err := gnmi.Dial(probeCtx, target, gnmi.Options{Plaintext: true, DialTimeout: 5 * time.Second})
		if err != nil {
			return err
		}
		return s.Close()
	})
	if err != nil {
		cleanupErr := cleanup()
		if ctx.Err() != nil {
			if cleanupErr != nil {
				return "", nil, errors.Join(ctx.Err(), cleanupErr)
			}
			return "", nil, ctx.Err()
		}
		return "", nil, errs.Wrap(errors.Join(err, cleanupErr), "gnmitarget never became gNMI-ready")
	}
	return target, cleanup, nil
}

// startFromContext builds and starts one container from a Docker
// build context, returning host:port for the exposed port.
func startFromContext(ctx context.Context, contextDir, port string, waitFor wait.Strategy) (string, func() error, error) {
	return startContainer(ctx, contextDir, port, waitFor, testcontainers.GenericContainer)
}

func startContainer(ctx context.Context, contextDir, port string, waitFor wait.Strategy,
	create func(context.Context, testcontainers.GenericContainerRequest) (testcontainers.Container, error),
) (string, func() error, error) {
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
	ctr, err := create(ctx, req)
	if err != nil {
		if ctr != nil {
			cleanCtx, cancel := context.WithTimeout(context.Background(), readyTimeout)
			defer cancel()
			err = errors.Join(err, ctr.Terminate(cleanCtx))
		}
		return "", nil, errs.Wrapf(err, "start container from %s", contextDir)
	}
	cleanup := func() error {
		cleanCtx, cancel := context.WithTimeout(context.Background(), readyTimeout)
		defer cancel()
		return ctr.Terminate(cleanCtx)
	}

	host, err := ctr.Host(ctx)
	if err != nil {
		return "", nil, errs.Wrap(errors.Join(err, cleanup()), "container host")
	}
	mapped, err := ctr.MappedPort(ctx, port)
	if err != nil {
		return "", nil, errs.Wrap(errors.Join(err, cleanup()), "container mapped port")
	}
	return net.JoinHostPort(host, mapped.Port()), cleanup, nil
}
