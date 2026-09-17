package integration_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
)

// bootstrapEnvironment lays out a deployment on disk and returns the exports
// the runbook's own blocks consume.
//
// The values are this deployment's; the names and every command that uses
// them are the runbook's. That is the one seam where the document and the
// test are not identical characters, and it cannot be closed: an operator's
// paths are theirs.
//
// The registry the bootstrap writes is the one shipped in deploy/lab, through
// the script the runbook names. So the lab's own registry file is what this
// test exercises, rather than a copy of it that could drift.
func bootstrapEnvironment(t *testing.T, dir string) string {
	t.Helper()

	repo, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatalf("resolve the repository root: %v", err)
	}

	run := filepath.Join(dir, "run")
	state := filepath.Join(dir, "central-state")
	agentState := filepath.Join(dir, "agent-state")
	credentials := filepath.Join(dir, "credentials")
	registry := filepath.Join(dir, "registry.textproto")
	provisioning := filepath.Join(dir, "provisioning.textproto")

	for _, d := range []string{run, agentState} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("create %s: %v", d, err)
		}
	}
	writeCredentials(t, credentials)

	apiPort, busPort := freePort(t), freePort(t)
	central := fmt.Sprintf("https://127.0.0.1:%d", apiPort)

	writeFile(t, filepath.Join(dir, "device.textproto"), []byte(fmt.Sprintf(`
state_dir: %q
registry_path: %q
credential_root: %q
listeners {
  api: "127.0.0.1:%d"
  bus: "127.0.0.1:%d"
}
edges {
  central_url: %q
  assertion_audience: "flowseer-lab"
  cluster_urls: "ws://127.0.0.1:%d"
}
intervals {
  drift { seconds: 3600 }
}
`, state, registry, credentials, apiPort, busPort, central, busPort)))

	writeFile(t, filepath.Join(dir, "agent.textproto"), []byte(fmt.Sprintf(`
state_dir: %q
provisioning_path: %q
`, agentState, provisioning)))

	writeFirstRegistry(t, registry)

	// The bootstrap renders the shipped registry template over the first one.
	// That template names a documentation-range address, which is
	// unreachable by design and therefore slow to fail: the SNMP probe waits
	// out its whole retransmit horizon and the runbook's last block waits for
	// onboarding. A closed loopback port is refused immediately, so the
	// sequence reaches the same place — the agent enrolled, attached and
	// running its lane, with the device unonboarded — deterministically and
	// without leaving this machine.
	template := filepath.Join(dir, "registry-template.textproto")
	writeLoopbackRegistryTemplate(t, template)

	return fmt.Sprintf(`set -eu
export FLOWSEER_REGISTRY_TEMPLATE=%q
export FLOWSEER_REPO=%q
export RUN=%q
export CENTRAL_CONFIG=%q
export AGENT_CONFIG=%q
export REGISTRY=%q
export PROVISIONING=%q
export CENTRAL=%q
export CACERT=%q
export DEVICE_ID=%q
export INTERFACE=%q
`, template, repo, run,
		filepath.Join(dir, "device.textproto"),
		filepath.Join(dir, "agent.textproto"),
		registry, provisioning, central,
		filepath.Join(state, "tls.crt"),
		fixtureDeviceID, fixtureInterface)
}

// writeLoopbackRegistryTemplate renders the shipped registry template with
// its device address replaced by a closed loopback port.
//
// Same shape, same placeholder, so the bootstrap exercises the real
// rendering path; only the address differs. Refused immediately rather than
// timing out, and it reaches nothing outside this machine.
func writeLoopbackRegistryTemplate(t *testing.T, path string) {
	t.Helper()
	shipped, err := os.ReadFile(filepath.Join(repoRoot, "deploy", "lab", "registry.textproto"))
	if err != nil {
		t.Fatalf("read the shipped registry template: %v", err)
	}
	body := strings.Replace(string(shipped),
		`ip { v4 { octets: "\300\000\002\006" } }`,
		`ip { v4 { octets: "\177\000\000\001" } }`, 1)
	if body == string(shipped) {
		t.Fatal("the shipped registry template no longer carries the address this test replaces")
	}
	writeFile(t, path, []byte(body))
}

// writeFirstRegistry writes the registry central starts on before any edge
// exists: the integration, and no devices.
//
// No devices is the honest first state and it is why the runbook says to do
// it this way. An integration serving none claims nothing about the edge it
// names, and the edge does not exist yet — where a registry listing a real
// device to an identifier nobody minted is a statement that is simply untrue
// for as long as it stands. Every rule on DeviceRegistry is vacuous over an
// empty device list, so this is as valid as the full one.
//
// This deliberately differs from what assemble does, which lists the device
// against a made-up edge because that was convenient inside a fixture. The
// document is the thing being tested, so the fixture follows it.
func writeFirstRegistry(t *testing.T, path string) {
	t.Helper()
	registry := storev1.DeviceRegistry_builder{
		Integration: storev1.RegistryIntegration_builder{
			Ref: inventoryv1.IntegrationGlobalRef_builder{
				Integration: inventoryv1.IntegrationLocalRef_builder{Id: proto.String(fixtureIntegrationID)}.Build(),
			}.Build(),
			Edge: edgev1.EdgeGlobalRef_builder{
				Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(placeholderEdgeID)}.Build(),
			}.Build(),
		}.Build(),
	}.Build()
	writePrototext(t, path, registry)
}

// placeholderEdgeID stands in the first registry's integration until central
// has minted a real one. It names nothing, which is the point: the registry
// it appears in lists no devices, so it makes no claim about any device being
// served by an edge that does not exist.
const placeholderEdgeID = "00000000-0000-7000-8000-000000000000"
