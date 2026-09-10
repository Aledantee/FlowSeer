package conformance

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
)

const (
	edgeID             = "0192e6a0-0000-7000-8000-0000000000ed"
	cloudIntegrationID = "0192e6a0-0000-7000-8000-0000000000c2"
	setupKeyID         = "abcdefghijklmnopqrstuvwxyz"
	setupKey           = "fse1_" + setupKeyID + "_" + "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrst"
	edgeAudience       = "flowseer-central"
	edgeHeaderTag      = "FlowSeer-Edge "
	// The worked vector's fixed procedure and an empty request body; the
	// vector is illustrative, not a real Heartbeat call.
	edgeProcedure = "/flowseer.api.edge.v1.EdgeService/Heartbeat"
)

var edgeBodySHA256 = func() []byte {
	sum := sha256.Sum256(nil)
	return sum[:]
}()

var edgeIssuedAt = time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)

func edgeRef() *edgev1.EdgeGlobalRef {
	return edgev1.EdgeGlobalRef_builder{
		Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build(),
	}.Build()
}

func edgeSetupKey(issued, expires time.Time) *edgev1.SetupKey {
	return edgev1.SetupKey_builder{
		Id:        proto.String(setupKeyID),
		Status:    edgev1.SetupKeyStatus_SETUP_KEY_STATUS_ISSUED.Enum(),
		IssuedAt:  timestamppb.New(issued),
		ExpiresAt: timestamppb.New(expires),
	}.Build()
}

func TestEdgeSetupKeyRules(t *testing.T) {
	runValidationCases(t, []validationCase{
		{
			name:      "issued key with later expiry is valid",
			message:   edgeSetupKey(edgeIssuedAt, edgeIssuedAt.Add(180*24*time.Hour)),
			wantValid: true,
		},
		{
			name:      "expiry before issue is rejected",
			message:   edgeSetupKey(edgeIssuedAt, edgeIssuedAt.Add(-time.Hour)),
			wantValid: false,
		},
		{
			name: "identifier outside base32 is rejected",
			message: edgev1.SetupKey_builder{
				Id:        proto.String("ABCDEFGHIJKLMNOPQRSTUVWXYZ"),
				Status:    edgev1.SetupKeyStatus_SETUP_KEY_STATUS_ISSUED.Enum(),
				IssuedAt:  timestamppb.New(edgeIssuedAt),
				ExpiresAt: timestamppb.New(edgeIssuedAt.Add(time.Hour)),
			}.Build(),
			wantValid: false,
		},
	})
}

func TestEdgeStateRules(t *testing.T) {
	key := bytes.Repeat([]byte{1}, ed25519.PublicKeySize)
	runValidationCases(t, []validationCase{
		{
			name: "pending edge needs neither key nor contact",
			message: edgev1.EdgeState_builder{
				Ref:       edgeRef(),
				Lifecycle: edgev1.EdgeLifecycle_EDGE_LIFECYCLE_PENDING.Enum(),
				SetupKey:  edgeSetupKey(edgeIssuedAt, edgeIssuedAt.Add(time.Hour)),
			}.Build(),
			wantValid: true,
		},
		{
			name: "enrolled edge carries key and contact",
			message: edgev1.EdgeState_builder{
				Ref:       edgeRef(),
				Lifecycle: edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED.Enum(),
				Contact:   edgev1.EdgeContact_EDGE_CONTACT_ACTIVE.Enum(),
				PublicKey: key,
			}.Build(),
			wantValid: true,
		},
		{
			name: "enrolled edge without key is rejected",
			message: edgev1.EdgeState_builder{
				Ref:       edgeRef(),
				Lifecycle: edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED.Enum(),
				Contact:   edgev1.EdgeContact_EDGE_CONTACT_ACTIVE.Enum(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "enrolled edge without contact is rejected",
			message: edgev1.EdgeState_builder{
				Ref:       edgeRef(),
				Lifecycle: edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED.Enum(),
				PublicKey: key,
			}.Build(),
			wantValid: false,
		},
		{
			name: "public key of the wrong length is rejected",
			message: edgev1.EdgeState_builder{
				Ref:       edgeRef(),
				Lifecycle: edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED.Enum(),
				Contact:   edgev1.EdgeContact_EDGE_CONTACT_ACTIVE.Enum(),
				PublicKey: key[:31],
			}.Build(),
			wantValid: false,
		},
		{
			name: "lifecycle zero is rejected",
			message: edgev1.EdgeState_builder{
				Ref:       edgeRef(),
				Lifecycle: edgev1.EdgeLifecycle_EDGE_LIFECYCLE_UNSPECIFIED.Enum(),
			}.Build(),
			wantValid: false,
		},
	})
}

func TestEdgeEventRules(t *testing.T) {
	runValidationCases(t, []validationCase{
		{
			name: "creation has no from",
			message: edgev1.EdgeEvent_builder{
				Ref: edgeRef(),
				To:  edgev1.EdgeLifecycle_EDGE_LIFECYCLE_PENDING.Enum(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "enrollment moves pending to enrolled",
			message: edgev1.EdgeEvent_builder{
				Ref:  edgeRef(),
				From: edgev1.EdgeLifecycle_EDGE_LIFECYCLE_PENDING.Enum(),
				To:   edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED.Enum(),
			}.Build(),
			wantValid: true,
		},
		{
			name: "unchanged lifecycle is rejected",
			message: edgev1.EdgeEvent_builder{
				Ref:  edgeRef(),
				From: edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED.Enum(),
				To:   edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED.Enum(),
			}.Build(),
			wantValid: false,
		},
	})
}

func edgeAssertion(validity time.Duration) *edgev1.EdgeAssertion {
	nonce := make([]byte, 16)
	for i := range nonce {
		nonce[i] = byte(i)
	}
	return edgev1.EdgeAssertion_builder{
		Edge:       edgeRef(),
		Audience:   proto.String(edgeAudience),
		IssuedAt:   timestamppb.New(edgeIssuedAt),
		ExpiresAt:  timestamppb.New(edgeIssuedAt.Add(validity)),
		Nonce:      nonce,
		Procedure:  proto.String(edgeProcedure),
		BodySha256: edgeBodySHA256,
	}.Build()
}

func TestEdgeAssertionRules(t *testing.T) {
	shortNonce := edgeAssertion(30 * time.Second)
	shortNonce.SetNonce(shortNonce.GetNonce()[:15])
	emptyProcedure := edgeAssertion(30 * time.Second)
	emptyProcedure.SetProcedure("")
	malformedProcedure := edgeAssertion(30 * time.Second)
	malformedProcedure.SetProcedure("Heartbeat")
	shortBodyHash := edgeAssertion(30 * time.Second)
	shortBodyHash.SetBodySha256(shortBodyHash.GetBodySha256()[:31])
	runValidationCases(t, []validationCase{
		{
			name:      "thirty second validity is valid",
			message:   edgeAssertion(30 * time.Second),
			wantValid: true,
		},
		{
			name:      "sixty second validity is the limit",
			message:   edgeAssertion(60 * time.Second),
			wantValid: true,
		},
		{
			name:      "two minute validity is rejected",
			message:   edgeAssertion(120 * time.Second),
			wantValid: false,
		},
		{
			name:      "expiry before issue is rejected",
			message:   edgeAssertion(-time.Second),
			wantValid: false,
		},
		{
			name:      "nonce of the wrong length is rejected",
			message:   shortNonce,
			wantValid: false,
		},
		{
			name:      "empty procedure is rejected",
			message:   emptyProcedure,
			wantValid: false,
		},
		{
			name:      "procedure without a leading slash is rejected",
			message:   malformedProcedure,
			wantValid: false,
		},
		{
			name:      "body hash of the wrong length is rejected",
			message:   shortBodyHash,
			wantValid: false,
		},
		{
			name: "signature of the wrong length is rejected",
			message: edgev1.SignedEdgeAssertion_builder{
				Payload:   []byte{1},
				Signature: make([]byte, ed25519.SignatureSize-1),
			}.Build(),
			wantValid: false,
		},
	})
}

func TestEdgeKeyProofRules(t *testing.T) {
	key := bytes.Repeat([]byte{2}, ed25519.PublicKeySize)
	runValidationCases(t, []validationCase{
		{
			name: "enrollment proof binds to a setup key",
			message: edgev1.KeyProofPayload_builder{
				PublicKey:  key,
				SetupKeyId: proto.String(setupKeyID),
			}.Build(),
			wantValid: true,
		},
		{
			name: "rekey proof binds to an assertion nonce",
			message: edgev1.KeyProofPayload_builder{
				PublicKey:      key,
				AssertionNonce: make([]byte, 16),
			}.Build(),
			wantValid: true,
		},
		{
			name: "proof without a binding is rejected",
			message: edgev1.KeyProofPayload_builder{
				PublicKey: key,
			}.Build(),
			wantValid: false,
		},
		{
			name: "proof with a short key is rejected",
			message: edgev1.KeyProofPayload_builder{
				PublicKey:  key[:16],
				SetupKeyId: proto.String(setupKeyID),
			}.Build(),
			wantValid: false,
		},
	})
}

func TestEdgeProvisioningAndRequestRules(t *testing.T) {
	anchor := bytes.Repeat([]byte{3}, 32)
	runValidationCases(t, []validationCase{
		{
			name: "provisioning with one anchor is valid",
			message: edgev1.EdgeProvisioning_builder{
				CentralUrl:   proto.String("https://central.example.net"),
				SetupKey:     proto.String(setupKey),
				TrustAnchors: [][]byte{anchor},
			}.Build(),
			wantValid: true,
		},
		{
			name: "provisioning without anchors is rejected",
			message: edgev1.EdgeProvisioning_builder{
				CentralUrl: proto.String("https://central.example.net"),
				SetupKey:   proto.String(setupKey),
			}.Build(),
			wantValid: false,
		},
		{
			name: "malformed setup key is rejected",
			message: edgev1.EdgeProvisioning_builder{
				CentralUrl:   proto.String("https://central.example.net"),
				SetupKey:     proto.String("fse1_abc"),
				TrustAnchors: [][]byte{anchor},
			}.Build(),
			wantValid: false,
		},
		{
			name: "enroll request needs a well-formed setup key",
			message: edgev1.EnrollRequest_builder{
				SetupKey: proto.String("fse1_abc"),
				Proof: edgev1.KeyProof_builder{
					Payload:   []byte{1},
					Signature: make([]byte, ed25519.SignatureSize),
				}.Build(),
			}.Build(),
			wantValid: false,
		},
		{
			name: "list page size above the cap is rejected",
			message: edgev1.ListEdgesRequest_builder{
				PageSize: proto.Uint32(501),
			}.Build(),
			wantValid: false,
		},
		{
			name:      "list request with defaults is valid",
			message:   edgev1.ListEdgesRequest_builder{}.Build(),
			wantValid: true,
		},
	})
}

// The README shows one worked Authorization header. This test computes the
// same header from the fixed vector and fails when the README does not carry
// it, so the two cannot drift apart.
func TestEdgeAssertionHeaderVector(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	private := ed25519.NewKeyFromSeed(seed)

	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(edgeAssertion(30 * time.Second))
	if err != nil {
		t.Fatalf("marshal assertion: %v", err)
	}
	signed := edgev1.SignedEdgeAssertion_builder{
		Payload:   payload,
		Signature: ed25519.Sign(private, payload),
	}.Build()
	wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(signed)
	if err != nil {
		t.Fatalf("marshal signed assertion: %v", err)
	}
	header := edgeHeaderTag + base64.RawStdEncoding.EncodeToString(wire)
	t.Logf("header vector: %s", header)

	if !ed25519.Verify(private.Public().(ed25519.PublicKey), signed.GetPayload(), signed.GetSignature()) {
		t.Fatal("signature does not verify over the payload bytes")
	}

	readme, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", "proto", "flowseer", "api", "edge", "v1", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if !strings.Contains(string(readme), header) {
		t.Errorf("README does not carry the header vector:\n%s", header)
	}
}

// TestEdgeAssertionStreamOpenVector pins a second worked header, for a
// server-stream open with a non-empty body: the body hashed is the
// enveloped request message exactly as sent, so a middleware that hashes
// the HTTP body bytes reproduces it.
func TestEdgeAssertionStreamOpenVector(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	private := ed25519.NewKeyFromSeed(seed)

	request := edgev1.OpenDeviceSubmissionRequest_builder{
		DeviceId:  proto.String(deviceID),
		BindingId: proto.String(bindingID),
		Sequence:  proto.Uint64(42),
	}.Build()
	message, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	body := make([]byte, 5, 5+len(message))
	body[1] = byte(len(message) >> 24)
	body[2] = byte(len(message) >> 16)
	body[3] = byte(len(message) >> 8)
	body[4] = byte(len(message))
	body = append(body, message...)
	bodySum := sha256.Sum256(body)

	assertion := edgeAssertion(30 * time.Second)
	assertion.SetProcedure("/flowseer.api.edge.v1.EdgeService/OpenDeviceSubmission")
	assertion.SetBodySha256(bodySum[:])

	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(assertion)
	if err != nil {
		t.Fatalf("marshal assertion: %v", err)
	}
	signed := edgev1.SignedEdgeAssertion_builder{
		Payload:   payload,
		Signature: ed25519.Sign(private, payload),
	}.Build()
	wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(signed)
	if err != nil {
		t.Fatalf("marshal signed assertion: %v", err)
	}
	header := edgeHeaderTag + base64.RawStdEncoding.EncodeToString(wire)
	t.Logf("stream open header vector: %s", header)

	readme, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", "proto", "flowseer", "api", "edge", "v1", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if !strings.Contains(string(readme), header) {
		t.Errorf("README does not carry the stream open header vector:\n%s", header)
	}
}

func TestIntegrationConfigHostRules(t *testing.T) {
	local := func(edge *edgev1.EdgeGlobalRef) *inventoryv1.IntegrationConfig {
		return inventoryv1.IntegrationConfig_builder{
			Ref:           integrationRef(integrationID),
			CredentialRef: proto.String("secret/local"),
			Edge:          edge,
			LocalNetwork:  inventoryv1.LocalNetworkConfig_builder{}.Build(),
		}.Build()
	}
	runValidationCases(t, []validationCase{
		{
			name:      "local-network integration names its edge",
			message:   local(edgeRef()),
			wantValid: true,
		},
		{
			name:      "local-network integration without an edge is rejected",
			message:   local(nil),
			wantValid: false,
		},
		{
			name: "third-party integration may run centrally",
			message: inventoryv1.IntegrationConfig_builder{
				Ref:           integrationRef(cloudIntegrationID),
				CredentialRef: proto.String("secret/cloud"),
				ThirdParty: inventoryv1.ThirdPartyConfig_builder{
					KindName:           proto.String("meraki"),
					TypeName:           proto.String("vendor.meraki.v1.Config"),
					Value:              []byte{},
					DescriptorRevision: proto.String("r1"),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
	})
}

func TestEdgeRequestRules(t *testing.T) {
	proof := edgev1.KeyProof_builder{
		Payload:   []byte{1},
		Signature: make([]byte, ed25519.SignatureSize),
	}.Build()
	runValidationCases(t, []validationCase{
		{
			name: "well-formed enroll request is valid",
			message: edgev1.EnrollRequest_builder{
				SetupKey: proto.String(setupKey),
				Proof:    proof,
			}.Build(),
			wantValid: true,
		},
		{
			name: "signed assertion with a full signature is valid",
			message: edgev1.SignedEdgeAssertion_builder{
				Payload:   []byte{1},
				Signature: make([]byte, ed25519.SignatureSize),
			}.Build(),
			wantValid: true,
		},
		{
			name: "heartbeat with a version is valid",
			message: edgev1.HeartbeatRequest_builder{
				AgentVersion: proto.String("0.1.0"),
			}.Build(),
			wantValid: true,
		},
		{
			name: "create edge with a future setup key expiry is valid",
			message: edgev1.CreateEdgeRequest_builder{
				Name:              proto.String("Berlin DC-1"),
				SetupKeyExpiresAt: timestamppb.New(time.Now().Add(time.Hour)),
			}.Build(),
			wantValid: true,
		},
		{
			name: "create edge with a past setup key expiry is rejected",
			message: edgev1.CreateEdgeRequest_builder{
				SetupKeyExpiresAt: timestamppb.New(time.Now().Add(-time.Hour)),
			}.Build(),
			wantValid: false,
		},
		{
			name: "issue setup key with a past expiry is rejected",
			message: edgev1.IssueSetupKeyRequest_builder{
				Edge:      edgeRef(),
				ExpiresAt: timestamppb.New(time.Now().Add(-time.Hour)),
			}.Build(),
			wantValid: false,
		},
	})
}
