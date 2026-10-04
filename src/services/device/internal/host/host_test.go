package host_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"google.golang.org/protobuf/types/known/timestamppb"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1/capturev1connect"
	devicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1/devicev1connect"
	apiedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1/identityv1connect"
	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1/attachv1connect"
	auditv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/audit/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/audit/v1/auditv1connect"
	captureedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1"
	captureedgev1connect "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/capture/v1/capturev1connect"
	dispatchv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1/dispatchv1connect"
	errsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/errs/v1"
	capturemodelv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identitymodelv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
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
	"go.aledante.io/FlowSeer/src/services/device/internal/edge"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
	"go.aledante.io/FlowSeer/src/services/device/internal/host"
)

const (
	testEdgeID           = "0192e6a0-0000-7000-8000-0000000000ed"
	testDeviceID         = "0192e6a0-0000-7000-8000-0000000000d1"
	testUnlistedDeviceID = "0192e6a0-0000-7000-8000-0000000000d2"
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

type panickingCheckEngine struct {
	*authztest.Engine
}

func (*panickingCheckEngine) Check(context.Context, authz.Query) (bool, error) {
	panic("authorization checker panicked")
}

var (
	serviceAuthMu sync.RWMutex
	serviceAuth   = make(map[string]authInfo)
)

type authInfo struct {
	token    string
	tenant   string
	certFile string
}

func startTestService(
	t *testing.T,
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
	reconciled := make(chan struct{}, 1)
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
		Reconciled: func() {
			select {
			case reconciled <- struct{}{}:
			default:
			}
		},
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
	serviceAuth[base] = authInfo{
		token:    token,
		tenant:   edgebus.DefaultTenant,
		certFile: filepath.Join(stateDir, "tls.crt"),
	}
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
	select {
	case <-reconciled:
	case <-time.After(30 * time.Second):
		t.Fatal("the projector did not complete its first reconciliation pass")
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
	svc := startTestService(t, nil)
	return svc.Base, svc.Stop, svc.WaitStopped
}

type certTransport struct {
	mu         sync.Mutex
	transports map[string]*http.Transport
}

func (c *certTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	serviceAuthMu.RLock()
	info, ok := serviceAuth["https://"+req.URL.Host]
	serviceAuthMu.RUnlock()
	if !ok || info.certFile == "" {
		return nil, fmt.Errorf("no service certificate for %s", req.URL.Host)
	}

	c.mu.Lock()
	transport := c.transports[info.certFile]
	if transport == nil {
		pemBytes, err := os.ReadFile(info.certFile)
		if err != nil {
			c.mu.Unlock()
			return nil, fmt.Errorf("read service certificate: %w", err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pemBytes) {
			c.mu.Unlock()
			return nil, fmt.Errorf("parse service certificate %s", info.certFile)
		}
		transport = &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    roots,
		}}
		c.transports[info.certFile] = transport
	}
	c.mu.Unlock()
	return transport.RoundTrip(req)
}

var serviceTransport = &certTransport{transports: make(map[string]*http.Transport)}

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

type edgeSigningRoundTripper struct {
	base    http.RoundTripper
	private ed25519.PrivateKey
	t       *testing.T
}

func (s *edgeSigningRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	if err := req.Body.Close(); err != nil {
		return nil, fmt.Errorf("close request body: %w", err)
	}
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	clone.Header.Set("Authorization", signedEdgeHeader(s.t, s.private, req.URL.Path, body, 0x40))
	return s.base.RoundTrip(clone)
}

// serviceClient trusts the certificate generated for the target service and
// automatically sets the test token and tenant headers if not already specified.
func serviceClient() *http.Client {
	return &http.Client{
		Transport: &authRoundTripper{
			base: serviceTransport,
		},
		Timeout: 10 * time.Second,
	}
}

// rawServiceClient trusts the target service without injecting authentication
// headers. It shares the certificate-verifying transport with [serviceClient].
func rawServiceClient() *http.Client {
	return &http.Client{
		Transport: serviceTransport,
		Timeout:   10 * time.Second,
	}
}

func wireErrorCode(err error) string {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return ""
	}
	for _, detail := range connectErr.Details() {
		msg, decodeErr := detail.Value()
		if decodeErr != nil {
			continue
		}
		payload, ok := msg.(*errsv1.ErrorPayload)
		if ok {
			return payload.GetCode()
		}
	}
	return ""
}

// The whole service starts from a file and refuses an operator's request for a
// device absent from the registry without revealing whether it exists.
func TestTheServiceStartsFromAFileAndAnswers(t *testing.T) {
	svc := startTestService(t, nil)
	client := devicev1connect.NewDeviceServiceClient(serviceClient(), svc.Base)

	msg := &devicev1.GetDeviceAccessStatusRequest{}
	local := &inventoryv1.DeviceLocalRef{}
	local.SetId(testUnlistedDeviceID)
	device := &inventoryv1.DeviceGlobalRef{}
	device.SetDevice(local)
	msg.SetDevice(device)

	_, err := callWhenServing(t, client, msg)
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Fatalf("status for an unlisted device = %v, want CodePermissionDenied (%v)", got, err)
	}
	if got := wireErrorCode(err); got != authz.ErrCodeDenied.String() {
		t.Fatalf("wire error code for an unlisted device = %q, want %q (%v)", got, authz.ErrCodeDenied, err)
	}
	foundTenantQuery := false
	for _, q := range svc.Engine.Queries() {
		if q.Object == "device:"+testUnlistedDeviceID && q.Relation == "tenant" && q.User == "tenant:"+edgebus.DefaultTenant {
			foundTenantQuery = true
			break
		}
	}
	if !foundTenantQuery {
		t.Fatalf("authorization engine recorded no tenant query for unlisted device %q: %v", testUnlistedDeviceID, svc.Engine.Queries())
	}
}

// The device the registry does list is answered from the journal, which means
// the lane bucket the hub created is reachable through the handle the modules
// waited on.
func TestAListedDeviceIsAnsweredFromTheJournal(t *testing.T) {
	base := runningService(t)
	client := devicev1connect.NewDeviceServiceClient(serviceClient(), base)

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

	// Dialed rather than inferred. The reported address is only worth something
	// if it is the one accepting connections, even when authorization refuses the
	// requested device.
	client := devicev1connect.NewDeviceServiceClient(serviceClient(), base)
	msg := &devicev1.GetDeviceAccessStatusRequest{}
	local := &inventoryv1.DeviceLocalRef{}
	local.SetId(testUnlistedDeviceID)
	device := &inventoryv1.DeviceGlobalRef{}
	device.SetDevice(local)
	msg.SetDevice(device)

	_, err := callWhenServing(t, client, msg)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a call to the reported address answered %v, want CodePermissionDenied for an unlisted device", err)
	}
	if got := wireErrorCode(err); got != authz.ErrCodeDenied.String() {
		t.Errorf("wire error code for an unlisted device = %q, want %q (%v)", got, authz.ErrCodeDenied, err)
	}
}

// Enroll is the one edge procedure an unauthenticated caller can reach: it
// runs in front of the assertion middleware, because an edge has no identity
// to sign with yet, so the middleware's body limit does not cover it. The
// bound is the handler's own, and it is the only thing standing between the
// listener and a body sized to exhaust the process.
func TestEnrollRefusesABodyPastTheBound(t *testing.T) {
	base := runningService(t)
	client := serviceClient()
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

func TestTenantServiceIsMountedAndRequiresAuthentication(t *testing.T) {
	base := runningService(t)
	client := rawServiceClient()
	waitUntilServing(t, client, base)

	req, err := http.NewRequest(http.MethodPost, base+identityv1connect.TenantServiceCreateTenantProcedure, bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/proto")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post to CreateTenant: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	tenantClient := identityv1connect.NewTenantServiceClient(client, base)
	_, err = tenantClient.CreateTenant(context.Background(), connect.NewRequest(&identityv1.CreateTenantRequest{}))
	if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want %v", got, connect.CodeUnauthenticated)
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
	svc := startTestService(t, nil)

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

	client := edgev1connect.NewEdgeAdminServiceClient(rawServiceClient(), svc.Base)

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
	if got := wireErrorCode(err3); got != host.ErrCodeInvalidRequest.String() {
		t.Errorf("empty request error code = %q, want %v (%v)", got, host.ErrCodeInvalidRequest, err3)
	}
	if queries := svc.Engine.Queries(); len(queries) != 0 {
		t.Errorf("recorded %d queries, want none: %v", len(queries), queries)
	}
}

func TestHostMountsServicesOnTheCorrectInterceptorChains(t *testing.T) {
	svc := startTestService(t, nil)
	client := rawServiceClient()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	installEnrolledTestEdge(t, svc, public)
	captureClient := capturev1connect.NewCaptureServiceClient(serviceClient(), svc.Base)
	if _, err := captureClient.CreateCaptureSession(context.Background(), connect.NewRequest(capturev1.CreateCaptureSessionRequest_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdgeID)}.Build(),
		}.Build(),
		Source: capturemodelv1.CaptureSource_builder{
			LocalInterface: capturemodelv1.LocalInterfaceSource_builder{
				InterfaceName: proto.String("eth0"),
			}.Build(),
		}.Build(),
		Budget: capturemodelv1.CaptureBudget_builder{
			MaxPackets: proto.Uint64(10),
		}.Build(),
		Authorization: capturemodelv1.CaptureAuthorization_builder{
			RequestedBy: identitymodelv1.OperatorRef_builder{
				Issuer:  proto.String(svc.Issuer.URL()),
				Subject: proto.String("operator-1"),
			}.Build(),
			Reason:               proto.String("mount test"),
			FullPayloadRequested: proto.Bool(false),
		}.Build(),
	}.Build())); err != nil {
		t.Fatalf("create capture assignment: %v", err)
	}

	cases := []struct {
		name      string
		procedure string
		message   proto.Message
	}{
		{
			name:      "edge service",
			procedure: attachv1connect.EdgeServiceHeartbeatProcedure,
			message:   attachv1.HeartbeatRequest_builder{AgentVersion: proto.String("test")}.Build(),
		},
		{
			name:      "dispatch service",
			procedure: dispatchv1connect.DispatchServiceReportProcedure,
			message:   &dispatchv1.ReportRequest{},
		},
		{
			name:      "audit service",
			procedure: auditv1connect.AuditServiceDeliverProcedure,
			message:   &auditv1.DeliverRequest{},
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := proto.Marshal(tc.message)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			req, err := http.NewRequest(http.MethodPost, svc.Base+tc.procedure, bytes.NewReader(body))
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header.Set("Content-Type", "application/proto")
			req.Header.Set("Authorization", signedEdgeHeader(t, private, tc.procedure, body, byte(i)))
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("call %s: %v", tc.procedure, err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode == http.StatusUnauthorized {
				t.Fatalf("call %s answered HTTP 401, which is CodeUnauthenticated", tc.procedure)
			}
		})
	}

	t.Run("capture edge service", func(t *testing.T) {
		client := &http.Client{
			Transport: &edgeSigningRoundTripper{
				base:    serviceTransport,
				private: private,
				t:       t,
			},
			Timeout: 10 * time.Second,
		}
		captureEdgeClient := captureedgev1connect.NewCaptureEdgeServiceClient(client, svc.Base)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stream, err := captureEdgeClient.SubscribeCaptureAssignments(
			ctx, connect.NewRequest(&captureedgev1.SubscribeCaptureAssignmentsRequest{}),
		)
		if err != nil {
			if got := connect.CodeOf(err); got == connect.CodeUnauthenticated {
				t.Fatalf("stream open answered %v, want an edge interceptor result", err)
			}
			t.Fatalf("open stream: %v", err)
		}
		t.Cleanup(func() { _ = stream.Close() })
		if stream.Receive() {
			cancel()
			for stream.Receive() {
			}
		}
		if got := connect.CodeOf(stream.Err()); got == connect.CodeUnauthenticated {
			t.Fatalf("stream answered %v, want an edge interceptor result", stream.Err())
		}
	})

	for _, procedure := range []string{
		attachv1connect.EdgeServiceHeartbeatProcedure,
		dispatchv1connect.DispatchServiceReportProcedure,
		auditv1connect.AuditServiceDeliverProcedure,
	} {
		t.Run("edge/refusal-code/"+procedure[strings.LastIndexByte(procedure, '/')+1:], func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, svc.Base+procedure, strings.NewReader("{}"))
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header.Set("Content-Type", "application/proto")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("call %s: %v", procedure, err)
			}
			defer func() { _ = resp.Body.Close() }()
			if got := resp.Header.Get("FlowSeer-Refusal-Code"); got == "" {
				t.Fatalf("response to %s lacks the edge refusal code, status %d", procedure, resp.StatusCode)
			}
		})
	}

	operatorChecks := []struct {
		name string
		call func() error
	}{
		{
			name: "tenant",
			call: func() error {
				_, err := identityv1connect.NewTenantServiceClient(client, svc.Base).CreateTenant(
					context.Background(), connect.NewRequest(&identityv1.CreateTenantRequest{}),
				)
				return err
			},
		},
		{
			name: "tenant admin",
			call: func() error {
				_, err := identityv1connect.NewTenantAdminServiceClient(client, svc.Base).ListMembers(
					context.Background(), connect.NewRequest(&identityv1.ListMembersRequest{}),
				)
				return err
			},
		},
		{
			name: "edge admin",
			call: func() error {
				_, err := edgev1connect.NewEdgeAdminServiceClient(client, svc.Base).GetEdge(
					context.Background(), connect.NewRequest(&apiedgev1.GetEdgeRequest{}),
				)
				return err
			},
		},
		{
			name: "device",
			call: func() error {
				_, err := devicev1connect.NewDeviceServiceClient(client, svc.Base).GetDeviceAccessStatus(
					context.Background(), connect.NewRequest(&devicev1.GetDeviceAccessStatusRequest{}),
				)
				return err
			},
		},
		{
			name: "capture",
			call: func() error {
				_, err := capturev1connect.NewCaptureServiceClient(client, svc.Base).GetCaptureSession(
					context.Background(), connect.NewRequest(&capturev1.GetCaptureSessionRequest{}),
				)
				return err
			},
		},
	}
	for _, tc := range operatorChecks {
		t.Run("operator/"+tc.name, func(t *testing.T) {
			err := tc.call()
			if connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("error = %v, want Unauthenticated", err)
			}
			if got := wireErrorCode(err); got != authn.ErrCodeTokenInvalid.String() {
				t.Fatalf("wire error code = %q, want %q (%v)", got, authn.ErrCodeTokenInvalid, err)
			}
		})
	}
}

func installEnrolledTestEdge(t *testing.T, svc *testService, public ed25519.PublicKey) {
	t.Helper()
	edgesKV, err := svc.Hub.JetStream().KeyValue(context.Background(), edgebus.EdgeBucket)
	if err != nil {
		t.Fatalf("open edge bucket: %v", err)
	}
	store := edgestore.New(edgesKV)
	ref := edgev1.EdgeGlobalRef_builder{
		Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdgeID)}.Build(),
	}.Build()
	lifecycle := edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED
	if _, err := store.Mutate(context.Background(), edgebus.DefaultTenant, testEdgeID, func(*storev1.StoredEdge) (*storev1.StoredEdge, error) {
		return storev1.StoredEdge_builder{
			Record: edgev1.EdgeRecord_builder{
				Config: edgev1.EdgeConfig_builder{Ref: ref}.Build(),
				State: edgev1.EdgeState_builder{
					Ref:       ref,
					Lifecycle: &lifecycle,
					PublicKey: public,
				}.Build(),
			}.Build(),
		}.Build(), nil
	}); err != nil {
		t.Fatalf("install enrolled edge: %v", err)
	}
}

func signedEdgeHeader(t *testing.T, private ed25519.PrivateKey, procedure string, body []byte, nonceByte byte) string {
	t.Helper()
	nonce := make([]byte, 16)
	for i := range nonce {
		nonce[i] = nonceByte + byte(i)
	}
	bodyHash := sha256.Sum256(body)
	now := time.Now()
	assertion := edgev1.EdgeAssertion_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdgeID)}.Build(),
		}.Build(),
		Audience:   proto.String("flowseer-device-test"),
		IssuedAt:   timestamppb.New(now),
		ExpiresAt:  timestamppb.New(now.Add(time.Minute)),
		Nonce:      nonce,
		Procedure:  proto.String(procedure),
		BodySha256: bodyHash[:],
	}.Build()
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(assertion)
	if err != nil {
		t.Fatalf("marshal assertion: %v", err)
	}
	signed := edgev1.SignedEdgeAssertion_builder{
		Payload:   payload,
		Signature: ed25519.Sign(private, payload),
	}.Build()
	wire, err := proto.Marshal(signed)
	if err != nil {
		t.Fatalf("marshal signed assertion: %v", err)
	}
	return edge.HeaderScheme + " " + base64.RawStdEncoding.EncodeToString(wire)
}

func TestHostRecoversAuthorizationPanicsOnUnaryAndStreamingCalls(t *testing.T) {
	svc := startTestService(t, func(opts *host.Options) {
		opts.Engine = &panickingCheckEngine{Engine: authztest.New()}
	})

	assertPanicError := func(t *testing.T, err error) {
		t.Helper()
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("error code = %v, want %v (%v)", connect.CodeOf(err), connect.CodeInternal, err)
		}
		if got := wireErrorCode(err); got != host.ErrCodePanic.String() {
			t.Fatalf("wire error code = %q, want %q (%v)", got, host.ErrCodePanic, err)
		}
	}

	deviceClient := devicev1connect.NewDeviceServiceClient(serviceClient(), svc.Base)
	deviceReq := connect.NewRequest(devicev1.GetDeviceAccessStatusRequest_builder{
		Device: inventoryv1.DeviceGlobalRef_builder{
			Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(testDeviceID)}.Build(),
		}.Build(),
	}.Build())
	_, err := deviceClient.GetDeviceAccessStatus(context.Background(), deviceReq)
	assertPanicError(t, err)

	captureClient := capturev1connect.NewCaptureServiceClient(serviceClient(), svc.Base)
	captureReq := connect.NewRequest(capturev1.DownloadCaptureSessionRequest_builder{
		Session: capturemodelv1.CaptureSessionGlobalRef_builder{
			Edge: edgev1.EdgeGlobalRef_builder{
				Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdgeID)}.Build(),
			}.Build(),
			CaptureSession: capturemodelv1.CaptureSessionLocalRef_builder{
				Id: proto.String("0192e6a0-0000-7000-8000-0000000000c1"),
			}.Build(),
		}.Build(),
	}.Build())
	stream, err := captureClient.DownloadCaptureSession(context.Background(), captureReq)
	if err == nil {
		if stream.Receive() {
			t.Fatal("stream delivered a response after the authorization checker panicked")
		}
		err = stream.Err()
	}
	assertPanicError(t, err)
}

// The writer panics from the real edge Enroll handler after it logs a wrong
// public key for a consumed setup key. Host recovery must keep that panic on
// the ordinary RPC error path so telemetry records the handler-panic error.
func TestHostRecoversAnEdgeHandlerPanicBeforeTelemetry(t *testing.T) {
	var logWriter panicOnceLogWriter
	svc := startTestService(t, func(opts *host.Options) {
		opts.LogWriter = &logWriter
	})

	if err := svc.Engine.Write(context.Background(), []authz.Tuple{
		{Object: "tenant:" + edgebus.DefaultTenant, Relation: "member", User: "user:" + svc.PrincipalID},
	}, nil); err != nil {
		t.Fatal(err)
	}
	svc.Engine.Grant("user:"+svc.PrincipalID, "admin", "tenant")
	svc.Engine.Grant("user:"+svc.PrincipalID, "manage", "edge")

	adminClient := edgev1connect.NewEdgeAdminServiceClient(serviceClient(), svc.Base)
	created, err := adminClient.CreateEdge(context.Background(), connect.NewRequest(
		apiedgev1.CreateEdgeRequest_builder{Name: proto.String("panic-recovery-edge")}.Build(),
	))
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	setupKey := created.Msg.GetProvisioning().GetSetupKey()
	if setupKey == "" {
		t.Fatal("CreateEdge returned no setup key")
	}
	keyID := setupKey[len("fse1_") : len("fse1_")+26]

	enrollRequest := func(t *testing.T, setupKey string, keyID string, public ed25519.PublicKey, private ed25519.PrivateKey) *connect.Request[attachv1.EnrollRequest] {
		t.Helper()
		payload := edgev1.KeyProofPayload_builder{
			PublicKey:  public,
			SetupKeyId: proto.String(keyID),
		}.Build()
		payloadWire, err := proto.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal key proof payload: %v", err)
		}
		return connect.NewRequest(attachv1.EnrollRequest_builder{
			SetupKey: proto.String(setupKey),
			Proof: edgev1.KeyProof_builder{
				Payload:   payloadWire,
				Signature: ed25519.Sign(private, payloadWire),
			}.Build(),
		}.Build())
	}

	firstPublic, firstPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate first edge key: %v", err)
	}
	edgeClient := attachv1connect.NewEdgeServiceClient(serviceClient(), svc.Base)
	if _, err := edgeClient.Enroll(context.Background(), enrollRequest(t, setupKey, keyID, firstPublic, firstPrivate)); err != nil {
		t.Fatalf("first Enroll: %v", err)
	}

	secondPublic, secondPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate second edge key: %v", err)
	}
	_, err = edgeClient.Enroll(context.Background(), enrollRequest(t, setupKey, keyID, secondPublic, secondPrivate))
	if got := connect.CodeOf(err); got != connect.CodeInternal {
		t.Fatalf("second Enroll code = %v, want CodeInternal (%v)", got, err)
	}
	if got := wireErrorCode(err); got != host.ErrCodePanic.String() {
		t.Fatalf("second Enroll wire error code = %q, want %q (%v)", got, host.ErrCodePanic, err)
	}
	if !strings.Contains(logWriter.String(), `"error.type":"host/handler-panic"`) {
		t.Fatalf("telemetry did not record the recovered edge panic: %s", logWriter.String())
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
	serviceAuthMu.Lock()
	serviceAuth[base] = authInfo{certFile: filepath.Join(stateDir, "tls.crt")}
	serviceAuthMu.Unlock()
	t.Cleanup(func() {
		serviceAuthMu.Lock()
		delete(serviceAuth, base)
		serviceAuthMu.Unlock()
	})

	// 1. EdgeService.Enroll with an unknown setup key gets the answer it gets with the engine reachable.
	edgeClient := attachv1connect.NewEdgeServiceClient(rawServiceClient(), base)
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
	reachable := startTestService(t, nil)
	_, reachableErr := attachv1connect.NewEdgeServiceClient(rawServiceClient(), reachable.Base).Enroll(
		context.Background(), connect.NewRequest(enrollReq.Msg),
	)
	_, enrollErr := edgeClient.Enroll(context.Background(), enrollReq)
	if enrollErr == nil {
		t.Fatal("Enroll succeeded with unknown setup key")
	}
	if got := connect.CodeOf(enrollErr); got != connect.CodePermissionDenied {
		t.Fatalf("Enroll code = %v, want CodePermissionDenied (%v)", got, enrollErr)
	}
	if got, want := connect.CodeOf(enrollErr), connect.CodeOf(reachableErr); got != want {
		t.Fatalf("closed-engine Enroll code = %v, reachable-engine code = %v", got, want)
	}

	// 2. While GetEdge answers CodeUnavailable with authz/unavailable.
	token := iss.Sign(map[string]any{
		"iss": iss.URL(),
		"sub": "operator-1",
		"aud": "flowseer-device-test",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	adminClient := edgev1connect.NewEdgeAdminServiceClient(rawServiceClient(), base)
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
	if got := wireErrorCode(getEdgeErr); got != authz.ErrCodeUnavailable.String() {
		t.Errorf("GetEdge error code = %q, want %v (%v)", got, authz.ErrCodeUnavailable, getEdgeErr)
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

func startupConfig(t *testing.T, endpoint, storeID, authnCA, authzCA string) string {
	t.Helper()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	credentialRoot := filepath.Join(dir, "credentials")
	if err := os.MkdirAll(credentialRoot, 0o700); err != nil {
		t.Fatalf("credential dir: %v", err)
	}
	issuer := authntest.New(t)
	keyPath := filepath.Join(dir, "authz.key")
	if err := os.WriteFile(keyPath, []byte("test-authz-key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	busPort := freePort(t)
	authnCAField := ""
	if authnCA != "" {
		authnCAField = fmt.Sprintf("  ca_file: %q\n", authnCA)
	}
	authzCAField := ""
	if authzCA != "" {
		authzCAField = fmt.Sprintf("  ca_file: %q\n", authzCA)
	}

	return fmt.Sprintf(`
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
%s
}
authorization {
  endpoint: %q
  store_id: %q
  model_id: "01JK1234567890ABCDEFGHJKMM"
  preshared_key_file: %q
%s
}
`, stateDir, writeRegistry(t, dir), credentialRoot, busPort, busPort, issuer.URL(), authnCAField,
		endpoint, storeID, keyPath, authzCAField)
}

func TestLocalStartupFaultsReturnBeforeAPIBinds(t *testing.T) {
	missingAuthnCA := filepath.Join(t.TempDir(), "missing-authn-ca.pem")
	missingAuthzCA := filepath.Join(t.TempDir(), "missing-authz-ca.pem")
	badAuthnCA := filepath.Join(t.TempDir(), "bad-authn-ca.pem")
	if err := os.WriteFile(badAuthnCA, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write bad authentication CA: %v", err)
	}

	tests := []struct {
		name      string
		endpoint  string
		storeID   string
		authnCA   string
		authzCA   string
		startCode errs.Code
	}{
		{
			name:      "malformed store id",
			endpoint:  "https://authz.example.test:8081",
			storeID:   "bad",
			startCode: openfga.ErrCodeConfig,
		},
		{
			name:      "unreadable authorization CA",
			endpoint:  "https://authz.example.test:8081",
			storeID:   "01JK1234567890ABCDEFGHJKMN",
			authzCA:   missingAuthzCA,
			startCode: openfga.ErrCodeConfig,
		},
		{
			name:      "unreadable authentication CA",
			endpoint:  "https://authz.example.test:8081",
			storeID:   "01JK1234567890ABCDEFGHJKMN",
			authnCA:   missingAuthnCA,
			startCode: host.ErrCodeStart,
		},
		{
			name:      "unparsable authentication CA",
			endpoint:  "https://authz.example.test:8081",
			storeID:   "01JK1234567890ABCDEFGHJKMN",
			authnCA:   badAuthnCA,
			startCode: host.ErrCodeStart,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := startupConfig(t, tc.endpoint, tc.storeID, tc.authnCA, tc.authzCA)
			cfg, err := host.LoadConfig(writeConfig(t, body))
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}

			bound := make(chan string, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- host.Run(ctx, cfg, "test", host.Options{
					Bound: func(api string) { bound <- api },
				})
			}()

			select {
			case api := <-bound:
				t.Fatalf("API bound at %s before startup fault", api)
			case err := <-done:
				if got, ok := errs.CodeOf(err); !ok || got != tc.startCode {
					t.Fatalf("host.Run code = %v, want %v (%v)", got, tc.startCode, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("host.Run did not return before the API listener bound")
			}
		})
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

type panicOnceLogWriter struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	panicked bool
}

func (w *panicOnceLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.panicked && bytes.Contains(p, []byte(`"msg":"setup key presented with another key"`)) {
		w.panicked = true
		panic("edge handler log writer panicked")
	}
	return w.buf.Write(p)
}

func (w *panicOnceLogWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
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

	svc := startTestService(t, func(opts *host.Options) {
		opts.Engine = engine
		opts.LogWriter = &logBuf
	})

	if err := engine.Write(context.Background(), []authz.Tuple{
		{Object: "tenant:" + edgebus.DefaultTenant, Relation: "member", User: "user:" + svc.PrincipalID},
	}, nil); err != nil {
		t.Fatal(err)
	}
	engine.Grant("user:"+svc.PrincipalID, "admin", "tenant")
	engine.Grant("user:"+svc.PrincipalID, "manage", "edge")

	engine.SetFailWrite(errs.New().Code(openfga.ErrCodeUnreachable).Msg("engine write unreachable"))
	logBuf.Reset()

	client := edgev1connect.NewEdgeAdminServiceClient(serviceClient(), svc.Base)
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
	var matches int
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec logRecord
		if err := json.Unmarshal([]byte(line), &rec); err == nil {
			if rec.Msg == "failed to project object relationship" && rec.ErrorType == "authz/engine-unreachable" {
				matches++
			}
		}
	}
	if matches != 1 {
		t.Fatalf("found %d log records with msg %q and error.type %q, want one; got log:\n%s", matches, "failed to project object relationship", "authz/engine-unreachable", output)
	}
}
