package host_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	devicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1/devicev1connect"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1/identityv1connect"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1/attachv1connect"
	eventlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/log/v1"
	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/policy/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/secret"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
	"go.aledante.io/FlowSeer/src/services/device/internal/host"
)

const (
	testEdgeID   = "0192e6a0-0000-7000-8000-0000000000ed"
	testDeviceID = "0192e6a0-0000-7000-8000-0000000000d1"
)

// freePort asks the kernel for a port and gives it straight back, so the
// configuration can name one before the service binds it.
//
// It is a race: between the release and the service's bind, anything on the
// machine may take the port. The API listener no longer needs it — that
// address is configured as port 0 and read back from Options.Bound — but the
// bus still does, because an edge dials the cluster_urls the same file names
// and those have to be written before the service starts.
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

func writeRegistry(t *testing.T, dir string) string {
	t.Helper()
	handle := policyv1.AccessPolicyHandle_builder{Key: proto.String("icx7150-lab"), Version: proto.Uint64(3)}.Build()
	reg := storev1.DeviceRegistry_builder{
		Integration: storev1.RegistryIntegration_builder{
			Ref: inventoryv1.IntegrationGlobalRef_builder{
				Integration: inventoryv1.IntegrationLocalRef_builder{Id: proto.String("0192e6a0-0000-7000-8000-0000000000c1")}.Build(),
			}.Build(),
			Edge: edgev1.EdgeGlobalRef_builder{
				Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdgeID)}.Build(),
			}.Build(),
		}.Build(),
		Devices: []*storev1.RegistryDevice{storev1.RegistryDevice_builder{
			Config: inventoryv1.DeviceConfig_builder{
				Ref:            inventoryv1.DeviceGlobalRef_builder{Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(testDeviceID)}.Build()}.Build(),
				Name:           proto.String("icx7150"),
				ManagementMode: inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED.Enum(),
				AccessPolicy:   handle,
			}.Build(),
			Binding: inventoryv1.BindingGlobalRef_builder{
				Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String("0192e6a0-0000-7000-8000-0000000000b1")}.Build(),
			}.Build(),
			Ip:                  addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 6}}.Build()}.Build(),
			DelayedApplyHorizon: durationpb.New(30 * time.Second),
			ManagedInterfaces:   []string{"ethernet 1/1/1"},
		}.Build()},
		Policies: []*storev1.RegistryPolicy{storev1.RegistryPolicy_builder{
			Handle:               handle,
			ReadCredential:       policyv1.CredentialHandle_builder{Key: proto.String("icx7150-lab-snmp"), Version: proto.Uint64(1)}.Build(),
			SubmissionCredential: policyv1.CredentialHandle_builder{Key: proto.String("icx7150-lab-ssh"), Version: proto.Uint64(1)}.Build(),
			HostTrust:            policyv1.HostTrustHandle_builder{Key: proto.String("icx7150-lab-hostkey"), Version: proto.Uint64(1)}.Build(),
			SshHostKeySha256:     proto.String("SHA256:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU"),
		}.Build()},
	}.Build()

	body, err := prototext.Marshal(reg)
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	path := filepath.Join(dir, "registry.textproto")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	return path
}

// runningService starts a whole device service on loopback and returns the
// base URL its API answers on.
func runningService(t *testing.T) string {
	t.Helper()
	base, _, _ := runningServiceWithControl(t)
	return base
}

func runningServiceWithHub(t *testing.T) (base string, hub *edgebus.Hub) {
	t.Helper()
	base, hub, _, _ = runningServiceWithHubControl(t)
	return base, hub
}

// runningServiceWithControl is runningService for a test that drives the
// shutdown itself. stop cancels the service and waitStopped blocks for
// Run's result; both are safe to call more than once, and the cleanup calls
// them too, so a test that stops the service itself does not leave the
// cleanup waiting on a result already taken.
func runningServiceWithControl(t *testing.T) (base string, stop func(), waitStopped func() error) {
	t.Helper()
	base, _, stop, waitStopped = runningServiceWithHubControl(t)
	return base, stop, waitStopped
}

func runningServiceWithHubControl(t *testing.T) (base string, hub *edgebus.Hub, stop func(), waitStopped func() error) {
	t.Helper()
	dir := t.TempDir()
	// Deliberately not created. A packaged deployment names a state
	// directory that does not exist yet on its first start, and that is the
	// one path nothing covered while every test made it first — so the
	// smoke tests take the real path and would fail if the service stopped
	// creating it.
	stateDir := filepath.Join(dir, "state")
	credentialRoot := filepath.Join(dir, "credentials")
	if err := os.MkdirAll(credentialRoot, 0o700); err != nil {
		t.Fatalf("credential dir: %v", err)
	}

	busPort := freePort(t)
	// The API asks the kernel for a port, which is what the schema says port
	// 0 is for, and the address comes back through Options.Bound below.
	// central_url carries no port for the same reason: it is what an edge is
	// told at issue time, nothing here dials what is issued, and a port
	// written before the bind would be a guess.
	body := fmt.Sprintf(`
state_dir: %q
registry_path: %q
credential_root: %q
listeners {
  api: "127.0.0.1:0"
  bus: "127.0.0.1:%d"
}
edges {
  central_url: "https://127.0.0.1"
  assertion_audience: "flowseer-device-test"
  cluster_urls: "ws://127.0.0.1:%d"
}
`, stateDir, writeRegistry(t, dir), credentialRoot, busPort, busPort)

	cfg, err := host.LoadConfig(writeConfig(t, body))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Buffered and sent to without blocking: a module restart binds again and
	// reports again, and the service must not stall on a test that has
	// already taken the first address.
	apiBound := make(chan string, 1)
	hubChan := make(chan *edgebus.Hub, 1)
	options := host.Options{
		Bound: func(api string) {
			select {
			case apiBound <- api:
			default:
			}
		},
		Hub: func(hub *edgebus.Hub) {
			select {
			case hubChan <- hub:
			default:
			}
			edgesKV, err := hub.JetStream().KeyValue(context.Background(), edgebus.EdgeBucket)
			if err != nil {
				t.Errorf("open edge bucket: %v", err)
				return
			}
			if err := edgestore.New(edgesKV).IndexEdge(context.Background(), testEdgeID, edgebus.DefaultTenant); err != nil {
				t.Errorf("index test edge: %v", err)
			}
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- host.Run(ctx, cfg, "test", options) }()

	var stopOnce, waitOnce sync.Once
	var runErr error
	var timedOut bool
	stop = func() { stopOnce.Do(cancel) }
	waitStopped = func() error {
		waitOnce.Do(func() {
			select {
			case runErr = <-done:
			case <-time.After(30 * time.Second):
				timedOut = true
			}
		})
		if timedOut {
			t.Error("the service did not stop within thirty seconds of cancellation")
		}
		return runErr
	}

	t.Cleanup(func() {
		stop()
		if err := waitStopped(); err != nil {
			t.Errorf("the service stopped with %v, want a clean shutdown", err)
		}
	})

	var api string
	select {
	case api = <-apiBound:
	case err := <-done:
		// Recorded through the same once, so the cleanup's waitStopped
		// returns it instead of waiting thirty seconds for a result this
		// select already took.
		waitOnce.Do(func() { runErr = err })
		t.Fatalf("the service stopped before it bound its API listener: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the service did not report a bound API address within thirty seconds")
	}

	select {
	case hub = <-hubChan:
	case <-time.After(5 * time.Second):
		t.Fatal("the service did not report its hub within five seconds")
	}

	return "https://" + api, hub, stop, waitStopped
}

func TestIntakeRepublishesAnEdgeRecord(t *testing.T) {
	_, hub := runningServiceWithHub(t)
	if err := hub.AttachEdge(context.Background(), edgebus.DefaultTenant, testEdgeID); err != nil {
		t.Fatalf("attach edge: %v", err)
	}
	credentials, err := hub.MintEdgeUser(context.Background(), testEdgeID)
	if err != nil {
		t.Fatalf("mint edge credentials: %v", err)
	}
	credentialsFile, err := credentials.CredsFile()
	if err != nil {
		t.Fatalf("format edge credentials: %v", err)
	}
	leaf, err := edgebus.StartLeaf(context.Background(), edgebus.LeafConfig{
		StateDir:        t.TempDir(),
		EdgeID:          testEdgeID,
		Tenant:          edgebus.DefaultTenant,
		HubURLs:         []string{hub.ListenURL()},
		CredentialsFile: secret.New(credentialsFile),
		TLS:             &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // the host generates a self-signed certificate for this loopback test
		FsyncPolicy:     service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("start edge leaf: %v", err)
	}
	t.Cleanup(leaf.Close)
	waitForHost(t, "edge leaf connection", func() bool {
		return leaf.HubConnected() && hub.LeafCount() > 0
	})

	envelope := validIngestRecord(t)
	data, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal ingest record: %v", err)
	}
	if err := leaf.Publish(context.Background(), leaf.Subject("ingest.syslog"), data, ""); err != nil {
		t.Fatalf("publish ingest record: %v", err)
	}

	stream, err := hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("load central ingest stream: %v", err)
	}
	waitForHost(t, "central ingest record", func() bool {
		info, infoErr := stream.Info(context.Background())
		return infoErr == nil && info.State.Msgs == 1
	})
	msg, err := stream.GetLastMsgForSubject(context.Background(), edgebus.CentralIngestSubject(edgebus.DefaultTenant, edgebus.IngestRecordTypeSyslog, testDeviceID))
	if err != nil {
		t.Fatalf("read central ingest record: %v", err)
	}
	var got ingestv1.IngestRecord
	if err := proto.Unmarshal(msg.Data, &got); err != nil {
		t.Fatalf("decode central ingest record: %v", err)
	}
	if !proto.Equal(envelope, &got) {
		t.Fatalf("central ingest record = %v, want %v", &got, envelope)
	}
}

func validIngestRecord(t *testing.T) *ingestv1.IngestRecord {
	t.Helper()
	edge := edgev1.EdgeGlobalRef_builder{
		Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdgeID)}.Build(),
	}.Build()
	device := inventoryv1.DeviceGlobalRef_builder{
		Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(testDeviceID)}.Build(),
	}.Build()
	binding := inventoryv1.BindingGlobalRef_builder{
		Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String("0192e6a0-0000-7000-8000-0000000000b1")}.Build(),
	}.Build()
	record := ingestv1.IngestRecord_builder{
		RecordId: proto.String("0192e6a0-0000-7000-8000-000000000101"),
		Provenance: inventoryv1.Provenance_builder{
			Binding:    binding,
			ObservedAt: timestamppb.New(time.Now().UTC()),
			Edge:       edge,
			Log:        inventoryv1.LogProtocol_LOG_PROTOCOL_SYSLOG.Enum(),
		}.Build(),
		Syslog: eventlogv1.SyslogRecord_builder{
			Device:     device,
			ReceivedAt: timestamppb.New(time.Now().UTC()),
			Message:    []byte("message"),
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(record); err != nil {
		t.Fatalf("validate ingest record fixture: %v", err)
	}
	return record
}

func waitForHost(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// insecureClient trusts whatever the service generated. An edge pins the
// digest instead; this test is not the edge.
func insecureClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // a loopback service that generated its own certificate this second
		},
		Timeout: 10 * time.Second,
	}
}

// The whole service starts from a file and answers an operator: the hub comes
// up, the four modules that need it find it, the API listener serves the
// certificate the host obtained, and a call reaches the handler behind both
// interceptors and comes back with the registry's own answer.
func TestTheServiceStartsFromAFileAndAnswers(t *testing.T) {
	base := runningService(t)
	client := devicev1connect.NewDeviceServiceClient(insecureClient(), base)

	msg := &devicev1.GetDeviceAccessStatusRequest{}
	local := &inventoryv1.DeviceLocalRef{}
	local.SetId("0192e6a0-0000-7000-8000-0000000000ff")
	device := &inventoryv1.DeviceGlobalRef{}
	device.SetDevice(local)
	msg.SetDevice(device)

	_, err := callWhenServing(t, client, msg)

	if got := connect.CodeOf(err); got != connect.CodeNotFound {
		t.Fatalf("code = %v, want not_found for a device the registry does not list (%v)", got, err)
	}
	if err != nil && !strings.Contains(err.Error(), "no such device") {
		t.Errorf("message = %q, want the operator's sentence", err.Error())
	}
}

// The device the registry does list is answered from the journal, which means
// the lane bucket the hub created is reachable through the handle the modules
// waited on.
func TestAListedDeviceIsAnsweredFromTheJournal(t *testing.T) {
	base := runningService(t)
	client := devicev1connect.NewDeviceServiceClient(insecureClient(), base)

	msg := &devicev1.GetDeviceAccessStatusRequest{}
	local := &inventoryv1.DeviceLocalRef{}
	local.SetId(testDeviceID)
	device := &inventoryv1.DeviceGlobalRef{}
	device.SetDevice(local)
	msg.SetDevice(device)

	resp, err := callWhenServing(t, client, msg)
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	if got := resp.Msg.GetHighWatermark(); got != 0 {
		t.Errorf("high watermark = %d, want the empty record's zero", got)
	}
	if resp.Msg.HasUnresolved() {
		t.Errorf("a device nothing has been applied to reports unresolved work: %v", resp.Msg.GetUnresolved())
	}
}

// callWhenServing retries until the listener is up, then returns whatever the
// service answered. A dial failure and a refusal are both Unavailable over
// Connect, so the two are told apart by the message rather than the code:
// nothing else in this service produces a connection error.
func callWhenServing(
	t *testing.T, client devicev1connect.DeviceServiceClient, msg *devicev1.GetDeviceAccessStatusRequest,
) (*connect.Response[devicev1.GetDeviceAccessStatusResponse], error) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := client.GetDeviceAccessStatus(context.Background(), connect.NewRequest(msg))
		if err == nil || !dialFailure(err) {
			return resp, err
		}
		if time.Now().After(deadline) {
			t.Fatalf("the api never came up: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func dialFailure(err error) bool {
	return strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "connect: ") ||
		strings.Contains(err.Error(), "EOF")
}

// The service reports the port the kernel gave it, and serves on that one.
//
// The schema tells an operator that a port of 0 asks the kernel for one, and
// until the listener was bound here rather than inside ListenAndServeTLS
// there was no way to find out the answer: the address never reached the
// process, and the only line carrying it printed the ":0" from the file. The
// two halves are asserted together on purpose. A reported address that
// nothing serves on, and a service that serves somewhere it did not report,
// are both the failure this exists to rule out, and either one alone would
// pass an assertion on the other.
func TestTheServiceReportsThePortItWasGiven(t *testing.T) {
	base := runningService(t)

	port := base[strings.LastIndex(base, ":")+1:]
	if port == "" || port == "0" {
		t.Fatalf("the service reported %q, want an address carrying the port it bound", base)
	}

	// Dialed rather than inferred. The reported address is only worth
	// something if it is the one accepting connections.
	client := devicev1connect.NewDeviceServiceClient(insecureClient(), base)
	msg := &devicev1.GetDeviceAccessStatusRequest{}
	local := &inventoryv1.DeviceLocalRef{}
	local.SetId("0192e6a0-0000-7000-8000-0000000000ff")
	device := &inventoryv1.DeviceGlobalRef{}
	device.SetDevice(local)
	msg.SetDevice(device)

	if _, err := callWhenServing(t, client, msg); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("a call to the reported address answered %v, want the handler's not-found", err)
	}
}

// Enroll is the one edge procedure an unauthenticated caller can reach: it
// runs in front of the assertion middleware, because an edge has no identity
// to sign with yet, so the middleware's body limit does not cover it. The
// bound is the handler's own, and it is the only thing standing between the
// listener and a body sized to exhaust the process.
func TestEnrollRefusesABodyPastTheBound(t *testing.T) {
	base := runningService(t)
	client := insecureClient()
	waitUntilServing(t, client, base)

	oversize := postEnroll(t, client, base, make([]byte, 2<<20))
	if oversize == http.StatusOK {
		t.Fatalf("a 2 MiB Enroll body was accepted (status %d)", oversize)
	}

	// A short body reaches the handler and is refused on its content, which
	// is what makes the answer above the bound rather than Enroll refusing
	// everything.
	short := postEnroll(t, client, base, []byte{0x00})
	if short == oversize {
		t.Errorf("a one-byte body and a 2 MiB body are answered alike (status %d)", short)
	}
}

// postEnroll sends body to Enroll over the Connect protocol and reports the
// HTTP status. The body is deliberately not a valid request: what is under
// test is how far it gets, not what it says.
func postEnroll(t *testing.T, client *http.Client, base string, body []byte) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+attachv1connect.EdgeServiceEnrollProcedure, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	req.Header.Set("Content-Type", "application/proto")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post to Enroll: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// waitUntilServing blocks until the API listener answers, so a test that
// speaks raw HTTP does not race the service's startup.
func waitUntilServing(t *testing.T, client *http.Client, base string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := client.Get(base + "/")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the api never came up: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TenantService is not mounted on the device service mux, so requests to its
// procedure paths are refused with HTTP 404 and Connect Unimplemented.
func TestTenantServiceIsNotMounted(t *testing.T) {
	base := runningService(t)
	client := insecureClient()
	waitUntilServing(t, client, base)

	req, err := http.NewRequest(http.MethodPost, base+identityv1connect.TenantServiceCreateTenantProcedure, bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/proto")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post to CreateTenant: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d (HTTP 404)", resp.StatusCode, http.StatusNotFound)
	}

	tenantClient := identityv1connect.NewTenantServiceClient(client, base)
	_, err = tenantClient.CreateTenant(context.Background(), connect.NewRequest(&identityv1.CreateTenantRequest{}))
	if got := connect.CodeOf(err); got != connect.CodeUnimplemented {
		t.Errorf("code = %v, want %v (Connect Unimplemented)", got, connect.CodeUnimplemented)
	}
}

// Startup does not automatically bind or index an unindexed edge from the
// device registry into the edges key-value bucket.
func TestStartupDoesNotIndexUnindexedRegistryEdge(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	credentialRoot := filepath.Join(dir, "credentials")
	if err := os.MkdirAll(credentialRoot, 0o700); err != nil {
		t.Fatalf("credential dir: %v", err)
	}

	busPort := freePort(t)
	body := fmt.Sprintf(`
state_dir: %q
registry_path: %q
credential_root: %q
listeners {
  api: "127.0.0.1:0"
  bus: "127.0.0.1:%d"
}
edges {
  central_url: "https://127.0.0.1"
  assertion_audience: "flowseer-device-test"
  cluster_urls: "ws://127.0.0.1:%d"
}
`, stateDir, writeRegistry(t, dir), credentialRoot, busPort, busPort)

	cfg, err := host.LoadConfig(writeConfig(t, body))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	apiBound := make(chan string, 1)
	hubChan := make(chan *edgebus.Hub, 1)
	options := host.Options{
		Bound: func(api string) {
			select {
			case apiBound <- api:
			default:
			}
		},
		Hub: func(hub *edgebus.Hub) {
			select {
			case hubChan <- hub:
			default:
			}
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var runErr error
	var waitOnce sync.Once
	var timedOut bool
	var earlyExit bool
	waitStopped := func() error {
		waitOnce.Do(func() {
			select {
			case runErr = <-done:
			case <-time.After(30 * time.Second):
				timedOut = true
			}
		})
		return runErr
	}
	t.Cleanup(func() {
		cancel()
		err := waitStopped()
		if earlyExit {
			return
		}
		if timedOut {
			t.Error("the service did not stop within thirty seconds of cancellation")
		}
		if err != nil {
			t.Errorf("the service stopped with %v, want a clean shutdown", runErr)
		}
	})
	go func() {
		done <- host.Run(ctx, cfg, "test", options)
	}()

	select {
	case <-apiBound:
	case err := <-done:
		earlyExit = true
		waitOnce.Do(func() { runErr = err })
		t.Fatalf("the service stopped before it bound its API listener: %v", err)
	case <-time.After(30 * time.Second):
		earlyExit = true
		t.Fatal("the service did not report a bound API address within thirty seconds")
	}

	var h *edgebus.Hub
	select {
	case h = <-hubChan:
	case <-time.After(5 * time.Second):
		t.Fatal("the service did not report its hub within five seconds")
	}

	edgesKV, err := h.JetStream().KeyValue(context.Background(), edgebus.EdgeBucket)
	if err != nil {
		t.Fatalf("open edge bucket: %v", err)
	}

	store := edgestore.New(edgesKV)
	tenantID, err := store.TenantForEdge(context.Background(), testEdgeID)
	if err != nil {
		t.Fatalf("TenantForEdge: %v", err)
	}
	if tenantID != "" {
		t.Fatalf("registry edge %q has tenant index %q, want none", testEdgeID, tenantID)
	}
}
