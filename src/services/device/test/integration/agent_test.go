package integration_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	agentv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/agent/v1"
	agenthost "go.aledante.io/FlowSeer/src/edge/agent/host"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
)

// agent is one run of the edge agent against a fake device.
type agent struct {
	t       *testing.T
	dir     string
	device  *fakeDevice
	clock   *testClock
	stop    func()
	stopped chan error
	once    sync.Once
}

// testClock is the agent's view of time. It starts at a fixed instant and
// only moves when a test moves it, so an operation waiting out a
// delayed-apply horizon waits for an advance rather than for a wall second.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// startAgent writes the agent's two files and runs it against device.
//
// The provisioning is central's own CreateEdge answer with its central_url
// replaced by the address this fixture actually bound, since the deployment
// under test is not the one that message was written for.
func startAgent(t *testing.T, dir string, provisioning *edgev1.EdgeProvisioning, centralURL string, device *fakeDevice, clock *testClock) *agent {
	t.Helper()

	stateDir := filepath.Join(dir, "agent-state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("agent state dir: %v", err)
	}

	shipped := proto.Clone(provisioning).(*edgev1.EdgeProvisioning) //nolint:errcheck // Clone of a concrete message
	shipped.SetCentralUrl(centralURL)
	provisioningPath := writePrototext(t, filepath.Join(dir, "provisioning.textproto"), shipped)

	cfg := agentv1.AgentConfig_builder{
		StateDir:         proto.String(stateDir),
		ProvisioningPath: proto.String(provisioningPath),
		LogLevel:         agentv1.AgentLogLevel_AGENT_LOG_LEVEL_DEBUG.Enum(),
	}.Build()
	configPath := writePrototext(t, filepath.Join(dir, "agent.textproto"), cfg)

	loaded, err := agenthost.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("agent LoadConfig: %v", err)
	}

	a := &agent{t: t, dir: dir, device: device, clock: clock, stopped: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	a.stop = cancel
	go func() {
		a.stopped <- agenthost.Run(ctx, loaded, "e2e", agenthost.Options{
			OpenSNMP: func(endpoint agenthost.Endpoint) func(context.Context, *attachv1.DeviceCredential) (access.SNMPSession, error) {
				return device.snmpFactory(renderEndpoint(endpoint))
			},
			OpenShell: func(endpoint agenthost.Endpoint) func(context.Context, *attachv1.DeviceCredential, string) (access.ShellSession, error) {
				return device.shellFactory(renderEndpoint(endpoint))
			},
			Clock: clock.Now,
		})
	}()
	t.Cleanup(a.shutdown)
	return a
}

// renderEndpoint is the endpoint as the real dialers would target it, so a
// test asserting on it is asserting on the address a connection would have
// been made to rather than on the struct's field values.
func renderEndpoint(e agenthost.Endpoint) string {
	snmpPort, sshPort := e.SNMPPort, e.SSHPort
	if snmpPort == 0 {
		snmpPort = 161
	}
	if sshPort == 0 {
		sshPort = 22
	}
	return fmt.Sprintf("%s snmp=%s ssh=%s",
		e.Address,
		net.JoinHostPort(e.Address, strconv.Itoa(snmpPort)),
		net.JoinHostPort(e.Address, strconv.Itoa(sshPort)))
}

func (a *agent) shutdown() {
	a.once.Do(func() {
		a.stop()
		select {
		case err := <-a.stopped:
			if err != nil {
				a.t.Errorf("the agent stopped with %v, want a clean shutdown", err)
			}
		case <-time.After(60 * time.Second):
			a.t.Error("the agent did not stop within a minute of cancellation")
		}
	})
}
