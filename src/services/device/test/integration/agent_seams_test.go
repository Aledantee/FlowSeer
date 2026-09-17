package integration_test

import (
	"context"
	"testing"
	"time"

	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	agenthost "go.aledante.io/FlowSeer/src/edge/agent/host"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
)

// The agent's substitution seams can be filled from outside its application,
// which is the property its own tests cannot express: they live under
// src/edge/agent and may name every type in its internal packages, and this
// one may not.
//
// It is a compile-time property first. The dialer factories are declared in
// internal/lanehost, so without the aliases in the host package there is no
// way to write a function of the right type out here and the file does not
// build — which is the failure, and the same one the access module's
// assembly test exists for.
//
// What it asserts beyond compiling is that the closures reach the agent as
// values rather than being dropped: the fields hold what was put in them.
// Whether the lane then dials through them is the end-to-end test's, because
// it takes a live central to get an agent as far as onboarding a device.
func TestTheAgentsDeviceSeamsCanBeFilledFromOutside(t *testing.T) {
	t.Parallel()

	var snmpEndpoint, shellEndpoint agenthost.Endpoint
	fixed := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	opts := agenthost.Options{
		OpenSNMP: func(endpoint agenthost.Endpoint) func(context.Context, *attachv1.DeviceCredential) (access.SNMPSession, error) {
			snmpEndpoint = endpoint
			return func(context.Context, *attachv1.DeviceCredential) (access.SNMPSession, error) {
				return access.SNMPSession{}, nil
			}
		},
		OpenShell: func(endpoint agenthost.Endpoint) func(context.Context, *attachv1.DeviceCredential, string) (access.ShellSession, error) {
			shellEndpoint = endpoint
			return func(context.Context, *attachv1.DeviceCredential, string) (access.ShellSession, error) {
				return access.ShellSession{}, nil
			}
		},
		Clock: func() time.Time { return fixed },
	}

	if opts.OpenSNMP == nil || opts.OpenShell == nil || opts.Clock == nil {
		t.Fatal("a substitution was dropped on the way into Options")
	}

	// Called here rather than trusted, because a factory field that type-checks
	// still has to be the one the agent hands an endpoint to. This is the
	// shape of that call, with the endpoint the agent would compute.
	want := agenthost.Endpoint{Address: "198.51.100.7", SNMPPort: 1161, SSHPort: 2222}
	opts.OpenSNMP(want)
	opts.OpenShell(want)

	if snmpEndpoint != want {
		t.Errorf("the SNMP factory was built with %+v, want %+v", snmpEndpoint, want)
	}
	if shellEndpoint != want {
		t.Errorf("the shell factory was built with %+v, want %+v", shellEndpoint, want)
	}
	if got := opts.Clock(); !got.Equal(fixed) {
		t.Errorf("the clock read %v, want %v", got, fixed)
	}
}
