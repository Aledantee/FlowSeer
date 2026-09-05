package edge

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

const (
	testEdgeID    = "0192e6a0-0000-7000-8000-0000000000ed"
	testAudience  = "flowseer-central"
	testProcedure = "/flowseer.api.edge.v1.EdgeService/Heartbeat"
)

var testIssuedAt = time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)

// testAssertion builds an otherwise-valid EdgeAssertion for testEdgeID,
// matching the README's own worked vector, and applies mutate if it is not
// nil so a test case can invalidate exactly one field.
func testAssertion(mutate func(*edgev1.EdgeAssertion)) *edgev1.EdgeAssertion {
	nonce := make([]byte, 16)
	for i := range nonce {
		nonce[i] = byte(i)
	}
	bodyHash := sha256.Sum256(nil)
	a := edgev1.EdgeAssertion_builder{
		Edge: edgev1.EdgeGlobalRef_builder{
			Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(testEdgeID)}.Build(),
		}.Build(),
		Audience:   proto.String(testAudience),
		IssuedAt:   timestamppb.New(testIssuedAt),
		ExpiresAt:  timestamppb.New(testIssuedAt.Add(30 * time.Second)),
		Nonce:      nonce,
		Procedure:  proto.String(testProcedure),
		BodySha256: bodyHash[:],
	}.Build()
	if mutate != nil {
		mutate(a)
	}
	return a
}

// signHeader signs a with private and renders the Authorization header
// value the verifier expects.
func signHeader(t *testing.T, private ed25519.PrivateKey, a *edgev1.EdgeAssertion) string {
	t.Helper()
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(a)
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
	return HeaderScheme + " " + base64.RawStdEncoding.EncodeToString(wire)
}

func lookupReturning(publicKey ed25519.PublicKey, lifecycle edgev1.EdgeLifecycle) KeyLookup {
	return func(_ context.Context, _ string) (ed25519.PublicKey, edgev1.EdgeLifecycle, error) {
		return publicKey, lifecycle, nil
	}
}

func testVerifier(at time.Time, skew time.Duration, lookup KeyLookup) *Verifier {
	v := NewVerifier(testAudience, skew, lookup)
	v.now = func() time.Time { return at }
	return v
}

func TestVerifierAcceptsTheReadmeVector(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	private := ed25519.NewKeyFromSeed(seed)
	public := private.Public().(ed25519.PublicKey)

	header := signHeader(t, private, testAssertion(nil))
	v := testVerifier(testIssuedAt.Add(3*time.Second), 5*time.Second, lookupReturning(public, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED))

	got, err := v.Verify(context.Background(), header, testProcedure, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.GetEdge().GetEdge().GetId() != testEdgeID {
		t.Errorf("edge id = %q, want %q", got.GetEdge().GetEdge().GetId(), testEdgeID)
	}
}

func TestVerifierRejectsEachStep(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	private := ed25519.NewKeyFromSeed(seed)
	public := private.Public().(ed25519.PublicKey)
	otherPrivate := ed25519.NewKeyFromSeed(bytes32(1))

	tests := []struct {
		name      string
		header    func() string
		procedure string
		body      []byte
		lookup    KeyLookup
		at        time.Time
		skew      time.Duration
		wantCode  errs.Code
	}{
		{
			name:      "missing scheme",
			header:    func() string { return "Bearer abc" },
			procedure: testProcedure,
			lookup:    lookupReturning(public, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED),
			at:        testIssuedAt.Add(3 * time.Second),
			skew:      5 * time.Second,
			wantCode:  ErrCodeBadHeader,
		},
		{
			name:      "invalid base64",
			header:    func() string { return HeaderScheme + " not-valid-base64!!" },
			procedure: testProcedure,
			lookup:    lookupReturning(public, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED),
			at:        testIssuedAt.Add(3 * time.Second),
			skew:      5 * time.Second,
			wantCode:  ErrCodeBadHeader,
		},
		{
			name:      "signature from a different key",
			header:    func() string { return signHeader(t, otherPrivate, testAssertion(nil)) },
			procedure: testProcedure,
			lookup:    lookupReturning(public, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED),
			at:        testIssuedAt.Add(3 * time.Second),
			skew:      5 * time.Second,
			wantCode:  ErrCodeBadSignature,
		},
		{
			name: "empty procedure fails the assertion's own validation",
			header: func() string {
				return signHeader(t, private, testAssertion(func(a *edgev1.EdgeAssertion) { a.SetProcedure("") }))
			},
			procedure: testProcedure,
			lookup:    lookupReturning(public, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED),
			at:        testIssuedAt.Add(3 * time.Second),
			skew:      5 * time.Second,
			wantCode:  ErrCodeMalformedAssertion,
		},
		{
			name:      "procedure does not match the invoked RPC",
			header:    func() string { return signHeader(t, private, testAssertion(nil)) },
			procedure: "/flowseer.api.edge.v1.EdgeService/Rekey",
			lookup:    lookupReturning(public, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED),
			at:        testIssuedAt.Add(3 * time.Second),
			skew:      5 * time.Second,
			wantCode:  ErrCodeWrongProcedure,
		},
		{
			name:      "body hash does not match the request",
			header:    func() string { return signHeader(t, private, testAssertion(nil)) },
			procedure: testProcedure,
			body:      []byte("tampered"),
			lookup:    lookupReturning(public, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED),
			at:        testIssuedAt.Add(3 * time.Second),
			skew:      5 * time.Second,
			wantCode:  ErrCodeWrongBodyHash,
		},
		{
			name:      "edge is retired",
			header:    func() string { return signHeader(t, private, testAssertion(nil)) },
			procedure: testProcedure,
			lookup:    lookupReturning(public, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_RETIRED),
			at:        testIssuedAt.Add(3 * time.Second),
			skew:      5 * time.Second,
			wantCode:  ErrCodeRetiredEdge,
		},
		{
			name: "audience does not match this deployment",
			header: func() string {
				return signHeader(t, private, testAssertion(func(a *edgev1.EdgeAssertion) { a.SetAudience("some-other-deployment") }))
			},
			procedure: testProcedure,
			lookup:    lookupReturning(public, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED),
			at:        testIssuedAt.Add(3 * time.Second),
			skew:      5 * time.Second,
			wantCode:  ErrCodeWrongAudience,
		},
		{
			name:      "issued_at is outside the clock skew window",
			header:    func() string { return signHeader(t, private, testAssertion(nil)) },
			procedure: testProcedure,
			lookup:    lookupReturning(public, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED),
			at:        testIssuedAt.Add(-time.Hour),
			skew:      5 * time.Second,
			wantCode:  ErrCodeExpired,
		},
		{
			name:      "assertion has expired",
			header:    func() string { return signHeader(t, private, testAssertion(nil)) },
			procedure: testProcedure,
			lookup:    lookupReturning(public, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED),
			at:        testIssuedAt.Add(31 * time.Second),
			skew:      time.Minute,
			wantCode:  ErrCodeExpired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := testVerifier(tt.at, tt.skew, tt.lookup)
			_, err := v.Verify(context.Background(), tt.header(), tt.procedure, tt.body)
			if err == nil {
				t.Fatal("Verify succeeded, want an error")
			}
			code, ok := errs.CodeOf(err)
			if !ok || code != tt.wantCode {
				t.Errorf("code = %v (ok=%v), want %v: %v", code, ok, tt.wantCode, err)
			}
		})
	}
}

func TestVerifierRejectsReplayedNonce(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	private := ed25519.NewKeyFromSeed(seed)
	public := private.Public().(ed25519.PublicKey)

	header := signHeader(t, private, testAssertion(nil))
	v := testVerifier(testIssuedAt.Add(3*time.Second), 5*time.Second, lookupReturning(public, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED))

	if _, err := v.Verify(context.Background(), header, testProcedure, nil); err != nil {
		t.Fatalf("first Verify: %v", err)
	}

	_, err := v.Verify(context.Background(), header, testProcedure, nil)
	if err == nil {
		t.Fatal("second Verify succeeded, want a replay error")
	}
	if code, ok := errs.CodeOf(err); !ok || code != ErrCodeReplayedNonce {
		t.Errorf("code = %v (ok=%v), want %v: %v", code, ok, ErrCodeReplayedNonce, err)
	}
}

func bytes32(fill byte) []byte {
	b := make([]byte, ed25519.SeedSize)
	for i := range b {
		b[i] = fill
	}
	return b
}
