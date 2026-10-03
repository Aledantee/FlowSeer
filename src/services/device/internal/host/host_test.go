package host_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
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

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	devicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1/devicev1connect"
	apiedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1/identityv1connect"
	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1/attachv1connect"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/policy/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn/authntest"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/authztest"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/openfga"
	"go.aledante.io/FlowSeer/src/services/device/internal/credential"
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

type testService struct {
	Base        string
	Stop        func()
	WaitStopped func() error
	Issuer      *authntest.Issuer
	Engine      *authztest.Engine
	Token       string
	PrincipalID string
	StateDir    string
	Dir         string
	Hub         *edgebus.Hub
}

var (
	serviceAuthMu sync.RWMutex
	serviceAuth   = make(map[string]authInfo)
)

type authInfo struct {
	token  string
	tenant string
}

func startTestService(
	t *testing.T,
	modifyCfg func(body string) string,
	modifyOpts func(opts *host.Options),
) *testService {
	t.Helper()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	credentialRoot := filepath.Join(dir, "credentials")
	if err := os.MkdirAll(credentialRoot, 0o700); err != nil {
		t.Fatalf("credential dir: %v", err)
	}

	iss := authntest.New(t)
	caPath := iss.WriteCACertFile(t)

	keyPath := filepath.Join(dir, "authz.key")
	if err := os.WriteFile(keyPath, []byte("test-authz-key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	token := iss.Sign(map[string]any{
		"iss": iss.URL(),
		"sub": "operator-1",
		"aud": "flowseer-device-test",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	principalID := authn.ComputePrincipalID(iss.URL(), "operator-1")

	engine := authztest.New()
	if err := engine.Write(context.Background(), []authz.Tuple{
		{Object: "tenant:" + edgebus.DefaultTenant, Relation: "member", User: "user:" + principalID},
	}, nil); err != nil {
		t.Fatal(err)
	}
	engine.Grant("user:"+principalID, "view", "device")
	engine.Grant("user:"+principalID, "operate", "device")
	engine.Grant("user:"+principalID, "view", "edge")
	engine.Grant("user:"+principalID, "manage", "edge")
	engine.Grant("user:"+principalID, "capture", "edge")
	engine.Grant("tenant:"+edgebus.DefaultTenant, "tenant", "device")
	engine.Grant("tenant:"+edgebus.DefaultTenant, "tenant", "edge")

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
authentication {
  issuers {
    issuer: %q
    audience: "flowseer-device-test"
    organization_claim_name: "org_id"
  }
  ca_file: %q
}
authorization {
  endpoint: "https://authz.example.test:8081"
  store_id: "01JK1234567890ABCDEFGHJKMN"
  model_id: "01JK1234567890ABCDEFGHJKMM"
  preshared_key_file: %q
}
`, stateDir, writeRegistry(t, dir), credentialRoot, busPort, busPort, iss.URL(), caPath, keyPath)

	if modifyCfg != nil {
		body = modifyCfg(body)
	}

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
			edgesKV, err := hub.JetStream().KeyValue(context.Background(), edgebus.EdgeBucket)
			if err != nil {
				t.Errorf("open edge bucket: %v", err)
				return
			}
			if err := edgestore.New(edgesKV).IndexEdge(context.Background(), testEdgeID, edgebus.DefaultTenant); err != nil {
				t.Errorf("index test edge: %v", err)
			}
		},
		Engine: engine,
	}

	if modifyOpts != nil {
		modifyOpts(&options)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- host.Run(ctx, cfg, "test", options) }()

	var stopOnce, waitOnce sync.Once
	var runErr error
	var timedOut bool
	stop := func() { stopOnce.Do(cancel) }
	waitStopped := func() error {
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
		waitOnce.Do(func() { runErr = err })
		t.Fatalf("the service stopped before it bound its API listener: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the service did not report a bound API address within thirty seconds")
	}

	base := "https://" + api

	serviceAuthMu.Lock()
	serviceAuth[base] = authInfo{token: token, tenant: edgebus.DefaultTenant}
	serviceAuthMu.Unlock()
	t.Cleanup(func() {
		serviceAuthMu.Lock()
		delete(serviceAuth, base)
		serviceAuthMu.Unlock()
	})

	var h *edgebus.Hub
	select {
	case h = <-hubChan:
	case <-time.After(5 * time.Second):
		t.Fatal("the service did not report its hub within five seconds")
	}

	return &testService{
		Base:        base,
		Stop:        stop,
		WaitStopped: waitStopped,
		Issuer:      iss,
		Engine:      engine,
		Token:       token,
		PrincipalID: principalID,
		StateDir:    stateDir,
		Dir:         dir,
		Hub:         h,
	}
}

// runningService starts a whole device service on loopback and returns the
// base URL its API answers on.
func runningService(t *testing.T) string {
	t.Helper()
	base, _, _ := runningServiceWithControl(t)
	return base
}

// runningServiceWithControl is runningService for a test that drives the
// shutdown itself. stop cancels the service and waitStopped blocks for
// Run's result; both are safe to call more than once, and the cleanup calls
// them too, so a test that stops the service itself does not leave the
// cleanup waiting on a result already taken.
func runningServiceWithControl(t *testing.T) (base string, stop func(), waitStopped func() error) {
	t.Helper()
	svc := startTestService(t, nil, nil)
	return svc.Base, svc.Stop, svc.WaitStopped
}

type authRoundTripper struct {
	base http.RoundTripper
}

func (a *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	serviceAuthMu.RLock()
	info, ok := serviceAuth["https://"+req.URL.Host]
	serviceAuthMu.RUnlock()
	if ok {
		if clone.Header.Get("Authorization") == "" {
			clone.Header.Set("Authorization", "Bearer "+info.token)
		}
		if clone.Header.Get("X-FlowSeer-Tenant") == "" {
			clone.Header.Set("X-FlowSeer-Tenant", info.tenant)
		}
	}
	return a.base.RoundTrip(clone)
}

// insecureClient trusts whatever the service generated and automatically sets
// the test token and tenant headers if not already specified.
func insecureClient() *http.Client {
	return &http.Client{
		Transport: &authRoundTripper{
			base: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		},
		Timeout: 10 * time.Second,
	}
}

// rawInsecureClient trusts whatever the service generated without injecting
// authentication headers.
func rawInsecureClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
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

	iss := authntest.New(t)
	caPath := iss.WriteCACertFile(t)

	keyPath := filepath.Join(dir, "authz.key")
	if err := os.WriteFile(keyPath, []byte("test-authz-key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
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
authentication {
  issuers {
    issuer: %q
    audience: "flowseer-device-test"
    organization_claim_name: "org_id"
  }
  ca_file: %q
}
authorization {
  endpoint: "https://authz.example.test:8081"
  store_id: "01JK1234567890ABCDEFGHJKMN"
  model_id: "01JK1234567890ABCDEFGHJKMM"
  preshared_key_file: %q
}
`, stateDir, writeRegistry(t, dir), credentialRoot, busPort, busPort, iss.URL(), caPath, keyPath)

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
		Engine: authztest.New(),
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

func TestOperatorRPCEnforcementOrder(t *testing.T) {
	svc := startTestService(t, nil, nil)

	const (
		tenantA = "0192e6a0-0000-7000-8000-00000000000a"
		tenantB = "0192e6a0-0000-7000-8000-00000000000b"
	)

	svc.Engine.Reset()
	if err := svc.Engine.Write(context.Background(), []authz.Tuple{
		{Object: "tenant:" + tenantA, Relation: "member", User: "user:" + svc.PrincipalID},
	}, nil); err != nil {
		t.Fatal(err)
	}
	svc.Engine.Grant("user:"+svc.PrincipalID, "view", "edge")

	client := edgev1connect.NewEdgeAdminServiceClient(rawInsecureClient(), svc.Base)

	// 1. GetEdge with no Authorization header and an empty request answers CodeUnauthenticated.
	req1 := connect.NewRequest(&apiedgev1.GetEdgeRequest{})
	req1.Header().Set("X-FlowSeer-Tenant", tenantA)
	_, err1 := client.GetEdge(context.Background(), req1)
	if got := connect.CodeOf(err1); got != connect.CodeUnauthenticated {
		t.Fatalf("no auth: code = %v, want CodeUnauthenticated (%v)", got, err1)
	}

	// 2. With a token and X-FlowSeer-Tenant: B for a principal the engine holds as member of A only,
	// answers CodePermissionDenied.
	req2 := connect.NewRequest(apiedgev1.GetEdgeRequest_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdgeID)}.Build(),
		}.Build(),
	}.Build())
	req2.Header().Set("Authorization", "Bearer "+svc.Token)
	req2.Header().Set("X-FlowSeer-Tenant", tenantB)
	_, err2 := client.GetEdge(context.Background(), req2)
	if got := connect.CodeOf(err2); got != connect.CodePermissionDenied {
		t.Fatalf("tenant B: code = %v, want CodePermissionDenied (%v)", got, err2)
	}

	// 3. With a token, tenant A, and an empty GetEdgeRequest, it answers CodeInvalidArgument
	// with host/invalid-request and the engine records no query.
	svc.Engine.Reset()
	if err := svc.Engine.Write(context.Background(), []authz.Tuple{
		{Object: "tenant:" + tenantA, Relation: "member", User: "user:" + svc.PrincipalID},
	}, nil); err != nil {
		t.Fatal(err)
	}
	svc.Engine.Grant("user:"+svc.PrincipalID, "view", "edge")

	req3 := connect.NewRequest(&apiedgev1.GetEdgeRequest{})
	req3.Header().Set("Authorization", "Bearer "+svc.Token)
	req3.Header().Set("X-FlowSeer-Tenant", tenantA)
	_, err3 := client.GetEdge(context.Background(), req3)
	if got := connect.CodeOf(err3); got != connect.CodeInvalidArgument {
		t.Fatalf("empty request: code = %v, want CodeInvalidArgument (%v)", got, err3)
	}
	if !strings.Contains(err3.Error(), "the request does not satisfy its schema rules") {
		t.Errorf("empty request error = %q, want schema rules error", err3.Error())
	}
	if queries := svc.Engine.Queries(); len(queries) != 0 {
		t.Errorf("recorded %d queries, want none: %v", len(queries), queries)
	}
}

func TestEdgeServicesNeedNoTokenWhileClosedPortEngineRefusesOperator(t *testing.T) {
	closedPort := freePort(t)
	iss := authntest.New(t)
	caPath := iss.WriteCACertFile(t)
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "authz.key")
	if err := os.WriteFile(keyPath, []byte("test-key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

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
authentication {
  issuers {
    issuer: %q
    audience: "flowseer-device-test"
    organization_claim_name: "org_id"
  }
  ca_file: %q
}
authorization {
  endpoint: "https://127.0.0.1:%d"
  store_id: "01JK1234567890ABCDEFGHJKMN"
  model_id: "01JK1234567890ABCDEFGHJKMM"
  preshared_key_file: %q
}
`, stateDir, writeRegistry(t, dir), credentialRoot, busPort, busPort, iss.URL(), caPath, closedPort, keyPath)

	cfg, err := host.LoadConfig(writeConfig(t, body))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	apiBound := make(chan string, 1)
	opts := host.Options{
		Bound: func(api string) {
			select {
			case apiBound <- api:
			default:
			}
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- host.Run(ctx, cfg, "test", opts) }()

	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})

	var api string
	select {
	case api = <-apiBound:
	case err := <-done:
		t.Fatalf("service stopped before binding API: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("service did not bind API within 30s")
	}

	base := "https://" + api

	// 1. EdgeService.Enroll with an unknown setup key gets the answer it gets with the engine reachable.
	edgeClient := attachv1connect.NewEdgeServiceClient(rawInsecureClient(), base)
	unknownKey := "fse1_aaaaaaaaaaaaaaaaaaaaaaaaaa_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	keyProofPayload := edgev1.KeyProofPayload_builder{
		PublicKey:  pub,
		SetupKeyId: proto.String("aaaaaaaaaaaaaaaaaaaaaaaaaa"),
	}.Build()
	payloadWire, err := proto.Marshal(keyProofPayload)
	if err != nil {
		t.Fatalf("Marshal KeyProofPayload: %v", err)
	}
	proof := edgev1.KeyProof_builder{
		Payload:   payloadWire,
		Signature: ed25519.Sign(priv, payloadWire),
	}.Build()

	enrollReq := connect.NewRequest(attachv1.EnrollRequest_builder{
		SetupKey: proto.String(unknownKey),
		Proof:    proof,
	}.Build())
	_, enrollErr := edgeClient.Enroll(context.Background(), enrollReq)
	if enrollErr == nil {
		t.Fatal("Enroll succeeded with unknown setup key")
	}
	if got := connect.CodeOf(enrollErr); got != connect.CodePermissionDenied {
		t.Fatalf("Enroll code = %v, want CodePermissionDenied (%v)", got, enrollErr)
	}

	// 2. While GetEdge answers CodeUnavailable with authz/unavailable.
	token := iss.Sign(map[string]any{
		"iss": iss.URL(),
		"sub": "operator-1",
		"aud": "flowseer-device-test",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	adminClient := edgev1connect.NewEdgeAdminServiceClient(rawInsecureClient(), base)
	getEdgeReq := connect.NewRequest(apiedgev1.GetEdgeRequest_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdgeID)}.Build(),
		}.Build(),
	}.Build())
	getEdgeReq.Header().Set("Authorization", "Bearer "+token)
	getEdgeReq.Header().Set("X-FlowSeer-Tenant", edgebus.DefaultTenant)

	_, getEdgeErr := adminClient.GetEdge(context.Background(), getEdgeReq)
	if got := connect.CodeOf(getEdgeErr); got != connect.CodeUnavailable {
		t.Fatalf("GetEdge code = %v, want CodeUnavailable (%v)", got, getEdgeErr)
	}
	if !strings.Contains(getEdgeErr.Error(), "authorization is unavailable") {
		t.Errorf("GetEdge error = %q, want authorization is unavailable", getEdgeErr.Error())
	}
}

func TestAbsentPresharedKeyFileRefusesStartBeforeListenerBinds(t *testing.T) {
	dir := t.TempDir()
	iss := authntest.New(t)
	caPath := iss.WriteCACertFile(t)
	absentKey := filepath.Join(dir, "absent.key")

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
authentication {
  issuers {
    issuer: %q
    audience: "flowseer-device-test"
    organization_claim_name: "org_id"
  }
  ca_file: %q
}
authorization {
  endpoint: "https://authz.example.test:8081"
  store_id: "01JK1234567890ABCDEFGHJKMN"
  model_id: "01JK1234567890ABCDEFGHJKMM"
  preshared_key_file: %q
}
`, stateDir, writeRegistry(t, dir), credentialRoot, busPort, busPort, iss.URL(), caPath, absentKey)

	cfg, err := host.LoadConfig(writeConfig(t, body))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	listenerBound := false
	opts := host.Options{
		Bound: func(string) {
			listenerBound = true
		},
	}

	ctx := context.Background()
	err = host.Run(ctx, cfg, "test", opts)
	if err == nil {
		t.Fatal("host.Run succeeded with absent preshared key file")
	}
	if listenerBound {
		t.Fatal("listener bound before preshared key file was validated")
	}
	code, ok := errs.CodeOf(err)
	if !ok || code != credential.ErrCodeNotFound {
		t.Fatalf("err code = %v, want credential/not-found (%v)", code, err)
	}
}

type failWriteEngine struct {
	*authztest.Engine
	mu        sync.Mutex
	failWrite error
}

func (f *failWriteEngine) Write(ctx context.Context, writes, deletes []authz.Tuple) error {
	f.mu.Lock()
	err := f.failWrite
	f.mu.Unlock()
	if err != nil {
		return err
	}
	return f.Engine.Write(ctx, writes, deletes)
}

func (f *failWriteEngine) SetFailWrite(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failWrite = err
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

func TestCreateEdgeSucceedsAndLogsWhenEngineWriteFails(t *testing.T) {
	var logBuf syncBuffer
	engine := &failWriteEngine{
		Engine: authztest.New(),
	}

	svc := startTestService(t, nil, func(opts *host.Options) {
		opts.Engine = engine
		opts.LogWriter = &logBuf
	})

	if err := engine.Write(context.Background(), []authz.Tuple{
		{Object: "tenant:" + edgebus.DefaultTenant, Relation: "member", User: "user:" + svc.PrincipalID},
		{Object: "tenant:" + edgebus.DefaultTenant, Relation: "admin", User: "user:" + svc.PrincipalID},
	}, nil); err != nil {
		t.Fatal(err)
	}
	engine.Grant("user:"+svc.PrincipalID, "manage", "edge")

	engine.SetFailWrite(errs.New().Code(openfga.ErrCodeUnreachable).Msg("engine write unreachable"))
	logBuf.Reset()

	client := edgev1connect.NewEdgeAdminServiceClient(insecureClient(), svc.Base)
	req := connect.NewRequest(apiedgev1.CreateEdgeRequest_builder{
		Name: proto.String("failing-write-edge"),
	}.Build())
	resp, err := client.CreateEdge(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}

	if resp.Msg.GetEdge() == nil || resp.Msg.GetEdge().GetConfig().GetRef().GetEdge().GetId() == "" {
		t.Fatal("CreateEdge response did not contain created edge")
	}
	createdEdgeID := resp.Msg.GetEdge().GetConfig().GetRef().GetEdge().GetId()
	if resp.Msg.GetProvisioning().GetSetupKey() == "" {
		t.Fatal("CreateEdge response did not contain setup key")
	}

	edgesKV, err := svc.Hub.JetStream().KeyValue(context.Background(), edgebus.EdgeBucket)
	if err != nil {
		t.Fatalf("open edge bucket: %v", err)
	}
	store := edgestore.New(edgesKV)
	stored, _, err := store.Get(context.Background(), edgebus.DefaultTenant, createdEdgeID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if stored == nil {
		t.Fatal("edge was not stored in edge store")
	}

	output := logBuf.String()
	type logRecord struct {
		Msg       string `json:"msg"`
		ErrorType string `json:"error.type"`
	}
	var found bool
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec logRecord
		if err := json.Unmarshal([]byte(line), &rec); err == nil {
			if rec.Msg == "failed to project object relationship" && rec.ErrorType == "authz/engine-unreachable" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("expected log record with msg %q and error.type %q, got log:\n%s", "failed to project object relationship", "authz/engine-unreachable", output)
	}
}
