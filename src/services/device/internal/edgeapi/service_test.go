package edgeapi_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	credentialv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/credential/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgeapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
	"go.aledante.io/FlowSeer/src/services/device/internal/registry"
)

const (
	testDeviceID  = "0192e6a0-0000-7000-8000-0000000000d1"
	testBindingID = "0192e6a0-0000-7000-8000-0000000000b1"
	testPolicyKey = "icx7150-lab"
	testHostKey   = "SHA256:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU"
)

// fakeCredentials answers the credential lookup a policy resolves to.
type fakeCredentials struct {
	material *credentialv1.CredentialMaterial
	err      error
	gotKey   string
	gotVer   uint64
}

func (f *fakeCredentials) Get(key string, version uint64) (*credentialv1.CredentialMaterial, error) {
	f.gotKey, f.gotVer = key, version
	return f.material, f.err
}

func shellMaterial() *credentialv1.CredentialMaterial {
	return credentialv1.CredentialMaterial_builder{
		Shell: credentialv1.ShellCredential_builder{
			Username: proto.String("tegi"),
			Password: proto.String("hunter2hunter2"),
		}.Build(),
	}.Build()
}

func snmpMaterial() *credentialv1.CredentialMaterial {
	return credentialv1.CredentialMaterial_builder{
		SnmpV3: credentialv1.SnmpV3Credential_builder{
			User:           proto.String("tegi"),
			AuthProtocol:   credentialv1.SnmpAuthProtocol_SNMP_AUTH_PROTOCOL_SHA256.Enum(),
			AuthPassphrase: proto.String("authauthauth"),
			PrivProtocol:   credentialv1.SnmpPrivProtocol_SNMP_PRIV_PROTOCOL_AES128.Enum(),
			PrivPassphrase: proto.String("privprivpriv"),
		}.Build(),
	}.Build()
}

func testRegistry(t *testing.T, edgeID string) *registry.Registry {
	t.Helper()
	accessHandle := policyv1.AccessPolicyHandle_builder{Key: proto.String(testPolicyKey), Version: proto.Uint64(3)}.Build()
	reg := storev1.DeviceRegistry_builder{
		Integration: storev1.RegistryIntegration_builder{
			Ref:  inventoryv1.IntegrationGlobalRef_builder{Integration: inventoryv1.IntegrationLocalRef_builder{Id: proto.String("0192e6a0-0000-7000-8000-0000000000c1")}.Build()}.Build(),
			Edge: edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build()}.Build(),
		}.Build(),
		Devices: []*storev1.RegistryDevice{storev1.RegistryDevice_builder{
			Config: inventoryv1.DeviceConfig_builder{
				Ref:            inventoryv1.DeviceGlobalRef_builder{Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(testDeviceID)}.Build()}.Build(),
				Name:           proto.String("icx7150"),
				ManagementMode: inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED.Enum(),
				AccessPolicy:   accessHandle,
			}.Build(),
			Binding:             inventoryv1.BindingGlobalRef_builder{Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(testBindingID)}.Build()}.Build(),
			Ip:                  addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 6}}.Build()}.Build(),
			DelayedApplyHorizon: durationpb.New(30 * time.Second),
		}.Build()},
		Policies: []*storev1.RegistryPolicy{storev1.RegistryPolicy_builder{
			Handle:               accessHandle,
			ReadCredential:       policyv1.CredentialHandle_builder{Key: proto.String("icx7150-lab-read"), Version: proto.Uint64(7)}.Build(),
			SubmissionCredential: policyv1.CredentialHandle_builder{Key: proto.String("icx7150-lab-ssh"), Version: proto.Uint64(1)}.Build(),
			HostTrust:            policyv1.HostTrustHandle_builder{Key: proto.String("icx7150-lab-hostkey"), Version: proto.Uint64(1)}.Build(),
			SshHostKeySha256:     proto.String(testHostKey),
			ReadCredentialTtl:    durationpb.New(2 * time.Minute),
		}.Build()},
	}.Build()

	data, err := prototext.Marshal(reg)
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	path := filepath.Join(t.TempDir(), "registry.textproto")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	loaded, err := registry.Load(path)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	return loaded
}

type harness struct {
	admin    *edgeapi.AdminService
	edge     *edgeapi.Service
	store    *edgestore.Store
	creds    *fakeCredentials
	record   *edgev1.EdgeRecord
	setupKey string
	edgeID   string
	now      time.Time
}

// newHarness creates one pending edge and stands the edge service up against a
// registry that names it, so the device the registry lists is a device the
// created edge actually hosts.
func newHarness(t *testing.T) *harness {
	t.Helper()
	hub := newHub(t)
	store := newStoreOver(t, hub)
	h := &harness{
		store: store,
		creds: &fakeCredentials{material: snmpMaterial()},
		now:   testClock,
	}
	h.admin = newAdminOver(t, store, func() time.Time { return h.now })
	h.record, h.setupKey = createEdge(t, h.admin)
	h.edgeID = refOf(h.record).GetEdge().GetId()

	anchor := make([]byte, 32)
	svc, err := edgeapi.NewService(store, testRegistry(t, h.edgeID), h.creds, hub, edgeapi.ServiceConfig{
		Audience:     "flowseer-central",
		TrustAnchors: [][]byte{anchor},
		ClusterURLs:  []string{"wss://central.example.test:4223"},
	}, func() time.Time { return h.now }, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	h.edge = svc
	return h
}

func enrollCtx(edgeID string, nonce []byte) context.Context {
	return edgeapi.ContextWithVerifiedAssertion(context.Background(), edgev1.EdgeAssertion_builder{
		Edge:  edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build()}.Build(),
		Nonce: nonce,
	}.Build())
}

func keyProof(t *testing.T, private ed25519.PrivateKey, public ed25519.PublicKey, bind func(*edgev1.KeyProofPayload)) *edgev1.KeyProof {
	t.Helper()
	payload := edgev1.KeyProofPayload_builder{PublicKey: public}.Build()
	bind(payload)
	wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal key proof payload: %v", err)
	}
	return edgev1.KeyProof_builder{Payload: wire, Signature: ed25519.Sign(private, wire)}.Build()
}

func enrollProof(t *testing.T, private ed25519.PrivateKey, public ed25519.PublicKey, setupKey string) *edgev1.KeyProof {
	t.Helper()
	return keyProof(t, private, public, func(p *edgev1.KeyProofPayload) { p.SetSetupKeyId(keyIDOf(setupKey)) })
}

func TestEnrollConsumesTheKeyAndRegistersTheEdgesOwnKey(t *testing.T) {
	h := newHarness(t)
	record, setupKey := h.record, h.setupKey
	edgeID := refOf(record).GetEdge().GetId()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	resp, err := h.edge.Enroll(context.Background(), connect.NewRequest(edgev1.EnrollRequest_builder{
		SetupKey: proto.String(setupKey),
		Proof:    enrollProof(t, private, public, setupKey),
	}.Build()))
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if err := protovalidate.Validate(resp.Msg); err != nil {
		t.Fatalf("Enroll response fails its schema rules: %v", err)
	}
	if got := resp.Msg.GetEdge().GetEdge().GetId(); got != edgeID {
		t.Errorf("enrolled edge = %q, want %q", got, edgeID)
	}

	key, lifecycle, err := h.store.Lookup(context.Background(), edgeID)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if lifecycle != edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED {
		t.Errorf("lifecycle = %v, want enrolled", lifecycle)
	}
	if !key.Equal(public) {
		t.Error("the verifier's key lookup does not return the key the edge registered")
	}
	stored := storedEdge(t, h.store, refOf(record))
	if got := stored.GetRecord().GetState().GetSetupKey().GetStatus(); got != edgev1.SetupKeyStatus_SETUP_KEY_STATUS_CONSUMED {
		t.Errorf("setup key status = %v, want consumed", got)
	}
}

func TestEnrollIsIdempotentForTheSameKeyAndPublicKey(t *testing.T) {
	h := newHarness(t)
	record, setupKey := h.record, h.setupKey
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	req := func() *connect.Request[edgev1.EnrollRequest] {
		return connect.NewRequest(edgev1.EnrollRequest_builder{
			SetupKey: proto.String(setupKey),
			Proof:    enrollProof(t, private, public, setupKey),
		}.Build())
	}

	first, err := h.edge.Enroll(context.Background(), req())
	if err != nil {
		t.Fatalf("first Enroll: %v", err)
	}
	second, err := h.edge.Enroll(context.Background(), req())
	if err != nil {
		t.Fatalf("second Enroll: %v", err)
	}
	if first.Msg.GetEdge().GetEdge().GetId() != second.Msg.GetEdge().GetEdge().GetId() {
		t.Fatal("a repeated enrollment returned a different identity")
	}

	stored := storedEdge(t, h.store, refOf(record))
	if got := stored.GetRecord().GetState().GetEnrolledAt().AsTime(); !got.Equal(testClock) {
		t.Errorf("enrolled_at = %v, want the first enrollment's %v", got, testClock)
	}
}

func TestEnrollRefusesTheSameKeyWithAnotherPublicKey(t *testing.T) {
	h := newHarness(t)
	record, setupKey := h.record, h.setupKey
	public, private, _ := ed25519.GenerateKey(nil)
	if _, err := h.edge.Enroll(context.Background(), connect.NewRequest(edgev1.EnrollRequest_builder{
		SetupKey: proto.String(setupKey),
		Proof:    enrollProof(t, private, public, setupKey),
	}.Build())); err != nil {
		t.Fatalf("Enroll: %v", err)
	}

	thiefPublic, thiefPrivate, _ := ed25519.GenerateKey(nil)
	_, err := h.edge.Enroll(context.Background(), connect.NewRequest(edgev1.EnrollRequest_builder{
		SetupKey: proto.String(setupKey),
		Proof:    enrollProof(t, thiefPrivate, thiefPublic, setupKey),
	}.Build()))
	wantConnectCode(t, err, connect.CodePermissionDenied)

	key, _, err := h.store.Lookup(context.Background(), refOf(record).GetEdge().GetId())
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !key.Equal(public) {
		t.Fatal("a refused enrollment replaced the registered key")
	}
}

func TestEnrollRefusesAnUnknownOrWrongKeyIndistinguishably(t *testing.T) {
	h := newHarness(t)
	live := h.setupKey
	public, private, _ := ed25519.GenerateKey(nil)

	// Same identifier as a live key, different secret.
	wrongSecret := live[:len("fse1_")+26+1] + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	unknown := "fse1_bbbbbbbbbbbbbbbbbbbbbbbbbb_cccccccccccccccccccccccccccccccccccccccccccccccccccc"

	for _, key := range []string{wrongSecret, unknown} {
		_, err := h.edge.Enroll(context.Background(), connect.NewRequest(edgev1.EnrollRequest_builder{
			SetupKey: proto.String(key),
			Proof:    enrollProof(t, private, public, key),
		}.Build()))
		wantConnectCode(t, err, connect.CodePermissionDenied)
	}
}

func TestEnrollRefusesAProofBoundToAnotherSetupKey(t *testing.T) {
	h := newHarness(t)
	setupKey := h.setupKey
	public, private, _ := ed25519.GenerateKey(nil)

	proof := keyProof(t, private, public, func(p *edgev1.KeyProofPayload) {
		p.SetSetupKeyId("bbbbbbbbbbbbbbbbbbbbbbbbbb")
	})
	_, err := h.edge.Enroll(context.Background(), connect.NewRequest(edgev1.EnrollRequest_builder{
		SetupKey: proto.String(setupKey),
		Proof:    proof,
	}.Build()))
	wantConnectCode(t, err, connect.CodeInvalidArgument)
}

func TestEnrollRefusesAProofSignedByAnotherKey(t *testing.T) {
	h := newHarness(t)
	setupKey := h.setupKey
	public, _, _ := ed25519.GenerateKey(nil)
	_, otherPrivate, _ := ed25519.GenerateKey(nil)

	_, err := h.edge.Enroll(context.Background(), connect.NewRequest(edgev1.EnrollRequest_builder{
		SetupKey: proto.String(setupKey),
		Proof:    enrollProof(t, otherPrivate, public, setupKey),
	}.Build()))
	wantConnectCode(t, err, connect.CodeInvalidArgument)
}

func TestEnrollRefusesAWithdrawnOrRetiredKey(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		withdraw func(t *testing.T, h *harness, ref *edgev1.EdgeGlobalRef)
	}{
		{"revoked", func(t *testing.T, h *harness, ref *edgev1.EdgeGlobalRef) {
			if _, err := h.admin.RevokeSetupKey(ctx, connect.NewRequest(edgev1.RevokeSetupKeyRequest_builder{Edge: ref}.Build())); err != nil {
				t.Fatalf("RevokeSetupKey: %v", err)
			}
		}},
		{"retired", func(t *testing.T, h *harness, ref *edgev1.EdgeGlobalRef) {
			if _, err := h.admin.RetireEdge(ctx, connect.NewRequest(edgev1.RetireEdgeRequest_builder{Edge: ref}.Build())); err != nil {
				t.Fatalf("RetireEdge: %v", err)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			record, setupKey := h.record, h.setupKey
			tc.withdraw(t, h, refOf(record))
			public, private, _ := ed25519.GenerateKey(nil)

			_, err := h.edge.Enroll(ctx, connect.NewRequest(edgev1.EnrollRequest_builder{
				SetupKey: proto.String(setupKey),
				Proof:    enrollProof(t, private, public, setupKey),
			}.Build()))
			wantConnectCode(t, err, connect.CodePermissionDenied)
		})
	}
}

func TestEnrollRefusesAnExpiredKey(t *testing.T) {
	h := newHarness(t)
	setupKey := h.setupKey
	h.now = testClock.Add(181 * 24 * time.Hour)
	public, private, _ := ed25519.GenerateKey(nil)

	_, err := h.edge.Enroll(context.Background(), connect.NewRequest(edgev1.EnrollRequest_builder{
		SetupKey: proto.String(setupKey),
		Proof:    enrollProof(t, private, public, setupKey),
	}.Build()))
	wantConnectCode(t, err, connect.CodeFailedPrecondition)
}

func enrolledHarness(t *testing.T) (*harness, string, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	h := newHarness(t)
	setupKey := h.setupKey
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if _, err := h.edge.Enroll(context.Background(), connect.NewRequest(edgev1.EnrollRequest_builder{
		SetupKey: proto.String(setupKey),
		Proof:    enrollProof(t, private, public, setupKey),
	}.Build())); err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	return h, h.edgeID, public, private
}

func TestRekeyReplacesTheKeyAndLeavesTheOldOneRefusing(t *testing.T) {
	h, edgeID, old, _ := enrolledHarness(t)
	nonce := []byte("0123456789abcdef")
	fresh, freshPrivate, _ := ed25519.GenerateKey(nil)

	resp, err := h.edge.Rekey(enrollCtx(edgeID, nonce), connect.NewRequest(edgev1.RekeyRequest_builder{
		Proof: keyProof(t, freshPrivate, fresh, func(p *edgev1.KeyProofPayload) { p.SetAssertionNonce(nonce) }),
	}.Build()))
	if err != nil {
		t.Fatalf("Rekey: %v", err)
	}
	if err := protovalidate.Validate(resp.Msg); err != nil {
		t.Fatalf("Rekey response fails its schema rules: %v", err)
	}

	key, _, err := h.store.Lookup(context.Background(), edgeID)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !key.Equal(fresh) {
		t.Fatal("the registered key is not the one Rekey named")
	}
	if key.Equal(old) {
		t.Fatal("the old key still verifies")
	}
}

func TestRekeyNamingTheRegisteredKeySucceedsWithoutChange(t *testing.T) {
	h, edgeID, current, private := enrolledHarness(t)
	before := storedEdge(t, h.store, edgeRefFor(edgeID)).GetRecord().GetState().GetEnrolledAt().AsTime()
	h.now = testClock.Add(time.Hour)

	// The edge lost the first response and repeats the call, still holding the
	// key already registered.
	nonce := []byte("0123456789abcdef")
	if _, err := h.edge.Rekey(enrollCtx(edgeID, nonce), connect.NewRequest(edgev1.RekeyRequest_builder{
		Proof: keyProof(t, private, current, func(p *edgev1.KeyProofPayload) { p.SetAssertionNonce(nonce) }),
	}.Build())); err != nil {
		t.Fatalf("repeated Rekey: %v", err)
	}

	stored := storedEdge(t, h.store, edgeRefFor(edgeID))
	if key := ed25519.PublicKey(stored.GetRecord().GetState().GetPublicKey()); !key.Equal(current) {
		t.Fatal("the registered key changed")
	}
	if after := stored.GetRecord().GetState().GetEnrolledAt().AsTime(); !after.Equal(before) {
		t.Errorf("enrolled_at moved to %v, want the unchanged %v", after, before)
	}
}

func edgeRefFor(edgeID string) *edgev1.EdgeGlobalRef {
	return edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build()}.Build()
}

func TestRekeyRefusesAProofBoundToAnotherNonce(t *testing.T) {
	h, edgeID, _, _ := enrolledHarness(t)
	fresh, freshPrivate, _ := ed25519.GenerateKey(nil)

	_, err := h.edge.Rekey(enrollCtx(edgeID, []byte("0123456789abcdef")), connect.NewRequest(edgev1.RekeyRequest_builder{
		Proof: keyProof(t, freshPrivate, fresh, func(p *edgev1.KeyProofPayload) {
			p.SetAssertionNonce([]byte("fedcba9876543210"))
		}),
	}.Build()))
	wantConnectCode(t, err, connect.CodeInvalidArgument)
}

func TestHeartbeatRecordsLivenessAndReturnsCentralsClock(t *testing.T) {
	h, edgeID, _, _ := enrolledHarness(t)
	h.now = testClock.Add(time.Minute)

	resp, err := h.edge.Heartbeat(enrollCtx(edgeID, nil), connect.NewRequest(edgev1.HeartbeatRequest_builder{
		AgentVersion: proto.String("v0.1.0"),
	}.Build()))
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if err := protovalidate.Validate(resp.Msg); err != nil {
		t.Fatalf("Heartbeat response fails its schema rules: %v", err)
	}
	if got := resp.Msg.GetServerTime().AsTime(); !got.Equal(h.now) {
		t.Errorf("server_time = %v, want %v", got, h.now)
	}
	stored := storedEdge(t, h.store, edgeRefFor(edgeID))
	if got := stored.GetRecord().GetState().GetLastSeenAt().AsTime(); !got.Equal(h.now) {
		t.Errorf("last_seen_at = %v, want %v", got, h.now)
	}
}

func TestAttachBusHandsOnTheHubsCredentialUnchanged(t *testing.T) {
	h, edgeID, _, _ := enrolledHarness(t)

	resp, err := h.edge.AttachBus(enrollCtx(edgeID, nil), connect.NewRequest(&edgev1.AttachBusRequest{}))
	if err != nil {
		t.Fatalf("AttachBus: %v", err)
	}
	if err := protovalidate.Validate(resp.Msg); err != nil {
		t.Fatalf("AttachBus response fails its schema rules: %v", err)
	}
	if len(resp.Msg.GetAccountJwt()) == 0 {
		t.Error("no account JWT was returned")
	}
	if !bytes.Contains(resp.Msg.GetUserCredential(), []byte("-----BEGIN NATS USER JWT-----")) {
		t.Error("the user credential is not a .creds file a leaf node can read")
	}
	want := edgebus.EdgePublishSubjects(edgebus.DefaultTenant, edgeID)
	for name, subject := range want {
		if got := resp.Msg.GetSubjects()[name]; got != subject {
			t.Errorf("subject %q = %q, want %q", name, got, subject)
		}
	}
	if len(resp.Msg.GetSubjects()) != len(want) {
		t.Errorf("subject map has %d entries, want %d", len(resp.Msg.GetSubjects()), len(want))
	}
}

func TestAcquireReadCredentialDeliversThePinnedVersionWithItsExpiry(t *testing.T) {
	h, edgeID, _, _ := enrolledHarness(t)

	resp, err := h.edge.AcquireReadCredential(enrollCtx(edgeID, nil), connect.NewRequest(edgev1.AcquireReadCredentialRequest_builder{
		DeviceId:     proto.String(testDeviceID),
		BindingId:    proto.String(testBindingID),
		AccessPolicy: policyv1.AccessPolicyHandle_builder{Key: proto.String(testPolicyKey), Version: proto.Uint64(3)}.Build(),
	}.Build()))
	if err != nil {
		t.Fatalf("AcquireReadCredential: %v", err)
	}
	if err := protovalidate.Validate(resp.Msg); err != nil {
		t.Fatalf("response fails its schema rules: %v", err)
	}
	if h.creds.gotKey != "icx7150-lab-read" || h.creds.gotVer != 7 {
		t.Errorf("resolved %q v%d, want the policy's read credential icx7150-lab-read v7", h.creds.gotKey, h.creds.gotVer)
	}
	if got := resp.Msg.GetExpiresAt().AsTime(); !got.Equal(h.now.Add(2 * time.Minute)) {
		t.Errorf("expires_at = %v, want the policy's ttl from now", got)
	}
	if resp.Msg.GetSshHostKeySha256() != "" {
		t.Error("an SNMP credential carried an SSH host key pin")
	}
}

func TestAcquireReadCredentialCarriesThePinOnlyForAShellLogin(t *testing.T) {
	h, edgeID, _, _ := enrolledHarness(t)
	h.creds.material = shellMaterial()

	resp, err := h.edge.AcquireReadCredential(enrollCtx(edgeID, nil), connect.NewRequest(edgev1.AcquireReadCredentialRequest_builder{
		DeviceId:     proto.String(testDeviceID),
		BindingId:    proto.String(testBindingID),
		AccessPolicy: policyv1.AccessPolicyHandle_builder{Key: proto.String(testPolicyKey), Version: proto.Uint64(3)}.Build(),
	}.Build()))
	if err != nil {
		t.Fatalf("AcquireReadCredential: %v", err)
	}
	if err := protovalidate.Validate(resp.Msg); err != nil {
		t.Fatalf("response fails its schema rules: %v", err)
	}
	if got := resp.Msg.GetSshHostKeySha256(); got != testHostKey {
		t.Errorf("ssh_host_key_sha256 = %q, want the policy's pin", got)
	}
}

func TestAcquireReadCredentialRefusesADeviceTheEdgeDoesNotHost(t *testing.T) {
	h, edgeID, _, _ := enrolledHarness(t)

	_, err := h.edge.AcquireReadCredential(enrollCtx(edgeID, nil), connect.NewRequest(edgev1.AcquireReadCredentialRequest_builder{
		DeviceId:     proto.String("0192e6a0-0000-7000-8000-0000000000d9"),
		BindingId:    proto.String(testBindingID),
		AccessPolicy: policyv1.AccessPolicyHandle_builder{Key: proto.String(testPolicyKey), Version: proto.Uint64(3)}.Build(),
	}.Build()))
	wantConnectCode(t, err, connect.CodePermissionDenied)
}

func TestAcquireReadCredentialRefusesAPolicyVersionTheDeviceDoesNotPin(t *testing.T) {
	h, edgeID, _, _ := enrolledHarness(t)

	_, err := h.edge.AcquireReadCredential(enrollCtx(edgeID, nil), connect.NewRequest(edgev1.AcquireReadCredentialRequest_builder{
		DeviceId:     proto.String(testDeviceID),
		BindingId:    proto.String(testBindingID),
		AccessPolicy: policyv1.AccessPolicyHandle_builder{Key: proto.String(testPolicyKey), Version: proto.Uint64(2)}.Build(),
	}.Build()))
	wantConnectCode(t, err, connect.CodeFailedPrecondition)
}

func TestAcquireReadCredentialRefusesAnotherDevicesBinding(t *testing.T) {
	h, edgeID, _, _ := enrolledHarness(t)

	_, err := h.edge.AcquireReadCredential(enrollCtx(edgeID, nil), connect.NewRequest(edgev1.AcquireReadCredentialRequest_builder{
		DeviceId:     proto.String(testDeviceID),
		BindingId:    proto.String("0192e6a0-0000-7000-8000-0000000000b9"),
		AccessPolicy: policyv1.AccessPolicyHandle_builder{Key: proto.String(testPolicyKey), Version: proto.Uint64(3)}.Build(),
	}.Build()))
	wantConnectCode(t, err, connect.CodePermissionDenied)
}

func TestEveryAuthenticatedCallRefusesAContextWithNoVerifiedEdge(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	_, err := h.edge.Heartbeat(ctx, connect.NewRequest(edgev1.HeartbeatRequest_builder{AgentVersion: proto.String("v0")}.Build()))
	wantConnectCode(t, err, connect.CodeUnauthenticated)
	_, err = h.edge.AttachBus(ctx, connect.NewRequest(&edgev1.AttachBusRequest{}))
	wantConnectCode(t, err, connect.CodeUnauthenticated)
	_, err = h.edge.Rekey(ctx, connect.NewRequest(&edgev1.RekeyRequest{}))
	wantConnectCode(t, err, connect.CodeUnauthenticated)
	_, err = h.edge.AcquireReadCredential(ctx, connect.NewRequest(&edgev1.AcquireReadCredentialRequest{}))
	wantConnectCode(t, err, connect.CodeUnauthenticated)
}

func TestContactAgesOutOfTheHeartbeatRatherThanTheRecord(t *testing.T) {
	h, edgeID, _, _ := enrolledHarness(t)
	ref := edgeRefFor(edgeID)

	for _, tc := range []struct {
		name    string
		silence time.Duration
		want    edgev1.EdgeContact
	}{
		{"fresh", time.Minute, edgev1.EdgeContact_EDGE_CONTACT_ACTIVE},
		{"stale", 10 * time.Minute, edgev1.EdgeContact_EDGE_CONTACT_STALE},
		{"dormant", 48 * time.Hour, edgev1.EdgeContact_EDGE_CONTACT_DORMANT},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.now = testClock.Add(tc.silence)
			resp, err := h.admin.GetEdge(context.Background(), connect.NewRequest(edgev1.GetEdgeRequest_builder{Edge: ref}.Build()))
			if err != nil {
				t.Fatalf("GetEdge: %v", err)
			}
			if err := protovalidate.Validate(resp.Msg); err != nil {
				t.Fatalf("GetEdge response fails its schema rules: %v", err)
			}
			if got := resp.Msg.GetEdge().GetState().GetContact(); got != tc.want {
				t.Errorf("contact after %v of silence = %v, want %v", tc.silence, got, tc.want)
			}
		})
	}

	// A heartbeat returns it to active, which is what makes dormant a
	// statement about silence rather than a state an edge gets stuck in.
	h.now = testClock.Add(49 * time.Hour)
	if _, err := h.edge.Heartbeat(enrollCtx(edgeID, nil), connect.NewRequest(edgev1.HeartbeatRequest_builder{
		AgentVersion: proto.String("v0.1.0"),
	}.Build())); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	resp, err := h.admin.GetEdge(context.Background(), connect.NewRequest(edgev1.GetEdgeRequest_builder{Edge: ref}.Build()))
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if got := resp.Msg.GetEdge().GetState().GetContact(); got != edgev1.EdgeContact_EDGE_CONTACT_ACTIVE {
		t.Errorf("contact after a heartbeat = %v, want active", got)
	}
}
