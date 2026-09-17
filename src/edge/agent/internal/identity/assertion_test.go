package identity_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	attachv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/attach/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/identity"
)

// The worked vector from spec/proto/flowseer/model/edge/v1/README.md, which a
// conformance test keeps in that file. Private key from a seed of 32 zero
// bytes, edge id 0192e6a0-0000-7000-8000-0000000000ed, audience
// flowseer-central, issued 2026-09-05T12:00:00Z expiring 30 seconds later,
// nonce bytes 00 through 0f, procedure
// /flowseer.edge.attach.v1.EdgeService/Heartbeat, body_sha256 of an empty body.
const (
	vectorEdgeID    = "0192e6a0-0000-7000-8000-0000000000ed"
	vectorAudience  = "flowseer-central"
	vectorProcedure = "/flowseer.edge.attach.v1.EdgeService/Heartbeat"
	vectorHeader    = "FlowSeer-Edge CrABCigKJgokMDE5MmU2YTAtMDAwMC03MDAwLTgwMDAtMDAwMDAwMDAwMGVkEhBmbG93c2Vlci1jZW50cmFsGgYIwIjw1AYiBgjeiPDUBioQAAECAwQFBgcICQoLDA0ODzIuL2Zsb3dzZWVyLmVkZ2UuYXR0YWNoLnYxLkVkZ2VTZXJ2aWNlL0hlYXJ0YmVhdDog47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFUSQFTvraPhC4Oqv5YZ2M5g/C7gPGXFrUoZOO2fGBux7F5ymuCZkyRbfx1gZOOwhBZ3hT+vgHxR8yYexZ2LLktvZwM"
)

func vectorEnrollment() *attachv1.EnrollResponse {
	local := &edgev1.EdgeLocalRef{}
	local.SetId(vectorEdgeID)
	ref := &edgev1.EdgeGlobalRef{}
	ref.SetEdge(local)

	response := &attachv1.EnrollResponse{}
	response.SetEdge(ref)
	response.SetAudience(vectorAudience)
	return response
}

// TestTheHeaderMatchesTheSpecifiedVector is the signer's only real evidence.
//
// A signer checked against its own output passes for any self-consistent
// signer, including one that signs the wrong bytes, orders the payload
// differently, or uses the wrong base64 alphabet — every one of which central
// refuses and none of which such a test would notice. This compares against a
// string computed independently and published in the model/edge README,
// which a conformance test keeps in that file.
//
// It is also what keeps HeaderScheme honest. The verifier's copy is in a
// package this one may not import, so the scheme is mirrored; the vector
// includes it, and changing either copy alone fails here.
func TestTheHeaderMatchesTheSpecifiedVector(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	issued := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	nonce := make([]byte, 16)
	for i := range nonce {
		nonce[i] = byte(i)
	}

	signer := identity.NewSigner(key, vectorEnrollment(), func() time.Time { return issued })
	identity.SetNonceForTest(signer, nonce)

	got, err := signer.Header(vectorProcedure, nil)
	if err != nil {
		t.Fatalf("Header() error: %v", err)
	}
	if got != vectorHeader {
		t.Errorf("Header() =\n%s\nwant\n%s", got, vectorHeader)
	}
}

// TestTheHeaderBindsTheBodyAndTheProcedure proves the two bindings that make
// a captured assertion useless anywhere but its own call. Same key, same
// clock, same nonce: only the procedure or the body differs, so a header that
// came out identical would mean the binding is not there.
func TestTheHeaderBindsTheBodyAndTheProcedure(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	issued := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	nonce := make([]byte, 16)

	newSigner := func() *identity.Signer {
		signer := identity.NewSigner(key, vectorEnrollment(), func() time.Time { return issued })
		identity.SetNonceForTest(signer, nonce)
		return signer
	}

	base, err := newSigner().Header(vectorProcedure, []byte("one"))
	if err != nil {
		t.Fatalf("Header() error: %v", err)
	}
	otherBody, err := newSigner().Header(vectorProcedure, []byte("two"))
	if err != nil {
		t.Fatalf("Header() error: %v", err)
	}
	otherProcedure, err := newSigner().Header("/flowseer.edge.attach.v1.EdgeService/AttachBus", []byte("one"))
	if err != nil {
		t.Fatalf("Header() error: %v", err)
	}

	if base == otherBody {
		t.Error("the same header authorizes two different bodies")
	}
	if base == otherProcedure {
		t.Error("the same header authorizes two different procedures")
	}
	if !strings.HasPrefix(base, identity.HeaderScheme+" ") {
		t.Errorf("header = %q, want the %s scheme", base, identity.HeaderScheme)
	}
}

// TestTheBodyDigestIsOfTheBytesOnTheWire pins which bytes are hashed: the
// request body exactly as sent, so central hashing what it received gets the
// same value. An empty body is the case a unary call with no fields produces.
func TestTheBodyDigestIsOfTheBytesOnTheWire(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	signer := identity.NewSigner(key, vectorEnrollment(), func() time.Time {
		return time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	})
	identity.SetNonceForTest(signer, make([]byte, 16))

	body := []byte("a request body")
	header, err := signer.Header(vectorProcedure, body)
	if err != nil {
		t.Fatalf("Header() error: %v", err)
	}
	assertion := identity.DecodeForTest(t, header)
	want := sha256.Sum256(body)
	if string(assertion.GetBodySha256()) != string(want[:]) {
		t.Error("body_sha256 is not the digest of the body that was passed")
	}
	if assertion.GetProcedure() != vectorProcedure {
		t.Errorf("procedure = %q, want %q", assertion.GetProcedure(), vectorProcedure)
	}
	if assertion.GetExpiresAt().AsTime().Sub(assertion.GetIssuedAt().AsTime()) > 60*time.Second {
		t.Error("the assertion outlives the 60 seconds its schema allows")
	}
}
