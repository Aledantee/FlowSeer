package integration_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1/devicev1connect"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	credentialv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/credential/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/policy/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	centralhost "go.aledante.io/FlowSeer/src/services/device/internal/host"
)

// The identifiers the fixture is built around. The device is shaped like the
// lab ICX7150 the first live write targets, so the registry this test writes
// and the one deploy/lab ships differ in their placeholders rather than in
// their shape.
const (
	fixtureIntegrationID = "0192e6a0-0000-7000-8000-0000000000c1"
	fixtureBindingID     = "0192e6a0-0000-7000-8000-0000000000b1"
	fixtureDeviceID      = "0192e6a0-0000-7000-8000-0000000000d1"
	fixtureInterface     = "ethernet 1/1/1"
	fixturePolicyKey     = "icx7150-lab"
	fixtureReadKey       = "icx7150-lab-snmp"
	fixtureSubmissionKey = "icx7150-lab-ssh"
	// The digest the fake shell reports as its host key. The lane pins what
	// the registry names against what the session reports, so the two have
	// to agree or onboarding fails on a check that is doing its job.
	fixtureHostKey = "SHA256:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU"
	// fixtureHorizon is the device's measured delayed-apply horizon. A
	// mutation on a device without one is refused outright, so this is not
	// decoration: it is what makes the fixture's device mutable at all.
	//
	// It also decides how much recovery this fixture gets, and that is why it
	// is ten minutes rather than the thirty seconds a lab switch might
	// actually measure. Recovery's budget is the horizon plus one poll
	// interval, and a mutation whose effect that budget does not establish
	// rests held for an operator and never resolves on its own. The restart
	// scenario needs the budget to outlast a central restart on a loaded
	// machine; a thirty-second horizon did not, and the failure looked like
	// the system hanging rather than like a fixture out of range.
	//
	// The interval does not grow with it. The lane caps it, so ten minutes
	// buys many more looks rather than longer gaps between them, which is
	// what makes this both patient and prompt.
	fixtureHorizon = 10 * time.Minute
	// fixtureResolveDeadline bounds a wait for a mutation to resolve.
	//
	// Derived from the poll cap rather than from the horizon. What a waiting
	// test is actually waiting for is the next recovery look, which the lane
	// caps at thirty seconds, plus whatever the exchange around it costs —
	// a restart, a report's own retry cadence, a loaded machine. It is not
	// waiting for the budget, which is ten minutes here and is spent only
	// when something is wrong.
	fixtureResolveDeadline = 4 * time.Minute
)

// freePort asks the kernel for a port and gives it straight back.
//
// It is a race, and it is here because both addresses this fixture needs have
// to be written into files before anything binds. The bus is named by
// cluster_urls, and the API is named by the provisioning the agent reads at
// start — and the agent has to keep dialing one address across a central
// restart, which is a scenario this test exercises. Central can report the
// port it bound, and does, but nothing can tell the agent about a new one
// after it has read its file.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release the port: %v", err)
	}
	return port
}

func writeFile(t *testing.T, path string, body []byte) string {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func writePrototext(t *testing.T, path string, msg proto.Message) string {
	t.Helper()
	body, err := prototext.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	return writeFile(t, path, body)
}

// writeCredentials lays out the credential mount central reads: one prototext
// file per key, mode 0600, each with the sidecar naming its version. Not a
// symlink in sight, because the provider opens every file O_NOFOLLOW.
func writeCredentials(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("credential root: %v", err)
	}
	snmp := credentialv1.CredentialMaterial_builder{
		SnmpV3: credentialv1.SnmpV3Credential_builder{
			User:           proto.String("flowseer"),
			AuthProtocol:   credentialv1.SnmpAuthProtocol_SNMP_AUTH_PROTOCOL_SHA256.Enum(),
			AuthPassphrase: proto.String("fixture-auth-passphrase"),
			PrivProtocol:   credentialv1.SnmpPrivProtocol_SNMP_PRIV_PROTOCOL_AES128.Enum(),
			PrivPassphrase: proto.String("fixture-priv-passphrase"),
		}.Build(),
	}.Build()
	shell := credentialv1.CredentialMaterial_builder{
		Shell: credentialv1.ShellCredential_builder{
			Username: proto.String("flowseer"),
			Password: proto.String("fixture-shell-password"),
		}.Build(),
	}.Build()

	for key, material := range map[string]*credentialv1.CredentialMaterial{
		fixtureReadKey:       snmp,
		fixtureSubmissionKey: shell,
	} {
		writePrototext(t, filepath.Join(root, key), material)
		meta, err := json.Marshal(struct {
			Version uint64 `json:"version"`
		}{Version: 1})
		if err != nil {
			t.Fatalf("marshal credential metadata: %v", err)
		}
		writeFile(t, filepath.Join(root, key+".meta.json"), meta)
	}
}

// writeRegistry writes the registry naming one device, listed to edgeID.
//
// The edge identifier is central's to draw, so the first registry this
// fixture writes names one that does not exist yet and the real one is
// written over it once the edge has been created. Central reads this file
// once at start, which is why that costs a restart.
func writeRegistry(t *testing.T, path, edgeID string, horizon time.Duration) string {
	t.Helper()
	handle := policyv1.AccessPolicyHandle_builder{Key: proto.String(fixturePolicyKey), Version: proto.Uint64(1)}.Build()
	device := storev1.RegistryDevice_builder{
		Config: inventoryv1.DeviceConfig_builder{
			Ref:            inventoryv1.DeviceGlobalRef_builder{Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(fixtureDeviceID)}.Build()}.Build(),
			Name:           proto.String("icx7150"),
			ManagementMode: inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED.Enum(),
			AccessPolicy:   handle,
		}.Build(),
		Binding:           inventoryv1.BindingGlobalRef_builder{Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(fixtureBindingID)}.Build()}.Build(),
		Ip:                addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 6}}.Build()}.Build(),
		ManagedInterfaces: []string{fixtureInterface},
	}.Build()
	if horizon > 0 {
		device.SetDelayedApplyHorizon(durationpb.New(horizon))
	}

	registry := storev1.DeviceRegistry_builder{
		Integration: storev1.RegistryIntegration_builder{
			Ref:  inventoryv1.IntegrationGlobalRef_builder{Integration: inventoryv1.IntegrationLocalRef_builder{Id: proto.String(fixtureIntegrationID)}.Build()}.Build(),
			Edge: edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build()}.Build(),
		}.Build(),
		Devices: []*storev1.RegistryDevice{device},
		Policies: []*storev1.RegistryPolicy{storev1.RegistryPolicy_builder{
			Handle:               handle,
			ReadCredential:       policyv1.CredentialHandle_builder{Key: proto.String(fixtureReadKey), Version: proto.Uint64(1)}.Build(),
			SubmissionCredential: policyv1.CredentialHandle_builder{Key: proto.String(fixtureSubmissionKey), Version: proto.Uint64(1)}.Build(),
			HostTrust:            policyv1.HostTrustHandle_builder{Key: proto.String("icx7150-lab-hostkey"), Version: proto.Uint64(1)}.Build(),
			SshHostKeySha256:     proto.String(fixtureHostKey),
		}.Build()},
	}.Build()
	return writePrototext(t, path, registry)
}

// insecureClient trusts whatever central generated. An edge pins the digest
// its provisioning carries; this client is the operator, not the edge.
func insecureClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // a loopback service that generated its own certificate this second
		},
		Timeout: 30 * time.Second,
	}
}

// central is one run of the device service, startable and stoppable more than
// once over the same state directory.
type central struct {
	t         *testing.T
	dir       string
	configDir string
	registry  string
	apiPort   int
	busPort   int

	// client is built once and reused. A fresh http.Transport per call keeps
	// its own idle pool with no IdleConnTimeout, and the status polls run at
	// 50ms for minutes, so a per-call transport leaks thousands of
	// connections and their goroutines on both sides of this process.
	client *http.Client

	mu      sync.Mutex
	stop    func()
	stopped chan error
	hub     *edgebus.Hub
}

// auditRecords reads the whole audit stream in the order the stream holds it.
//
// That order is the stream's own sequence, which for the lane's records is
// also the order they were emitted: the edge's audit deliverer blocks until
// central answers, and central answers only once the stream has acked, so the
// lane cannot move past a record that is not yet held. Across producers there
// is a sequence but no such guarantee — central writes its own records at its
// own moments — so an ordering claim is only sound within one producer's
// records, which is what the assertions here confine themselves to.
func (c *central) auditRecords(t *testing.T) []*eventv1.DeviceOperationEvent {
	t.Helper()
	c.mu.Lock()
	hub := c.hub
	c.mu.Unlock()
	if hub == nil {
		t.Fatal("central reported no hub; the audit stream cannot be read")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stream, err := hub.JetStream().Stream(ctx, edgebus.AuditStream)
	if err != nil {
		t.Fatalf("open the audit stream: %v", err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("audit stream info: %v", err)
	}

	var records []*eventv1.DeviceOperationEvent
	for seq := info.State.FirstSeq; seq <= info.State.LastSeq; seq++ {
		msg, err := stream.GetMsg(ctx, seq)
		if err != nil {
			// A sequence the stream no longer holds is not a failure: the
			// stream ages records out, and this walk is over what is there.
			continue
		}
		event := &eventv1.DeviceOperationEvent{}
		if err := proto.Unmarshal(msg.Data, event); err != nil {
			t.Fatalf("audit record at sequence %d does not decode: %v", seq, err)
		}
		records = append(records, event)
	}
	return records
}

func newCentral(t *testing.T, dir, registryPath string) *central {
	t.Helper()
	c := &central{t: t, dir: dir, configDir: dir, registry: registryPath, apiPort: freePort(t), busPort: freePort(t), client: insecureClient()}
	t.Cleanup(c.client.CloseIdleConnections)
	return c
}

func (c *central) baseURL() string { return fmt.Sprintf("https://127.0.0.1:%d", c.apiPort) }

// start brings central up and returns once its API listener is bound, which
// Options.Bound reports. Waiting on that rather than retrying a call is what
// keeps the rest of this file free of readiness loops.
func (c *central) start() {
	c.t.Helper()
	body := fmt.Sprintf(`
state_dir: %q
registry_path: %q
credential_root: %q
listeners {
  api: "127.0.0.1:%d"
  bus: "127.0.0.1:%d"
}
edges {
  central_url: %q
  assertion_audience: "flowseer-e2e"
  cluster_urls: "ws://127.0.0.1:%d"
}
intervals {
  dispatch_resend { seconds: 1 }
  drift { seconds: 3600 }
}
`, filepath.Join(c.dir, "central-state"), c.registry, filepath.Join(c.dir, "credentials"),
		c.apiPort, c.busPort, c.baseURL(), c.busPort)

	cfg, err := centralhost.LoadConfig(writeFile(c.t, filepath.Join(c.configDir, "central.textproto"), []byte(body)))
	if err != nil {
		c.t.Fatalf("central LoadConfig: %v", err)
	}

	bound := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- centralhost.Run(ctx, cfg, "e2e", centralhost.Options{
			Bound: func(api string) {
				select {
				case bound <- api:
				default:
				}
			},
			// Replaced rather than kept, because the handle dies with the
			// attempt that produced it: a hub rebuilt under RestForOne
			// invalidates the last one, and reading the stream through a
			// closed server is the failure its doc warns about.
			Hub: func(hub *edgebus.Hub) {
				c.mu.Lock()
				c.hub = hub
				c.mu.Unlock()
			},
		})
	}()

	select {
	case api := <-bound:
		if want := fmt.Sprintf("127.0.0.1:%d", c.apiPort); api != want {
			c.t.Fatalf("central bound %q, want %q", api, want)
		}
	case err := <-done:
		c.t.Fatalf("central stopped before it bound its listener: %v", err)
	case <-time.After(60 * time.Second):
		cancel()
		c.t.Fatal("central did not bind its listener within a minute")
	}

	c.mu.Lock()
	c.stop, c.stopped = cancel, done
	c.mu.Unlock()
	c.t.Cleanup(c.shutdown)
}

// shutdown stops central and waits for Run to return. Safe to call more than
// once, and the cleanup calls it too.
func (c *central) shutdown() {
	c.mu.Lock()
	stop, stopped := c.stop, c.stopped
	c.stop, c.stopped = nil, nil
	c.mu.Unlock()
	if stop == nil {
		return
	}
	stop()
	// The client outlives this run and the next start reuses the same port, so
	// a pooled keep-alive connection to the process being stopped could be
	// handed to the first request after the restart.
	c.client.CloseIdleConnections()
	select {
	case err := <-stopped:
		if err != nil {
			c.t.Errorf("central stopped with %v, want a clean shutdown", err)
		}
	case <-time.After(60 * time.Second):
		c.t.Error("central did not stop within a minute of cancellation")
	}
}

func (c *central) admin() edgev1connect.EdgeAdminServiceClient {
	return edgev1connect.NewEdgeAdminServiceClient(c.client, c.baseURL())
}

func (c *central) devices() devicev1connect.DeviceServiceClient {
	return devicev1connect.NewDeviceServiceClient(c.client, c.baseURL())
}

func deviceRef() *inventoryv1.DeviceGlobalRef {
	return inventoryv1.DeviceGlobalRef_builder{
		Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(fixtureDeviceID)}.Build(),
	}.Build()
}
