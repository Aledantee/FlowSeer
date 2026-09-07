package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// HeaderScheme is the Authorization scheme every assertion-bearing call
// uses. The verifier has its own copy (the device service's internal/edge
// package, which nothing outside that service may import), so this is a
// mirrored constant and the api/edge README's worked header vector is what
// keeps the two honest: TestTheHeaderMatchesTheSpecifiedVector below builds a
// header from the README's fixed inputs and compares it to the README's own
// string, scheme included. Change either copy alone and that fails.
const HeaderScheme = "FlowSeer-Edge"

// assertionLifetime is how long an assertion is valid. The schema caps it at
// 60 seconds; this is well inside that, because the window is what a captured
// assertion buys and the only thing it costs to shorten is clock tolerance.
const assertionLifetime = 30 * time.Second

// Signer builds the Authorization header every call except Enroll carries.
//
// One assertion authorizes one call: it names the procedure and the SHA-256
// of the request body, so a captured header cannot be replayed against a
// different RPC or a mutated body even inside its own window. Safe for
// concurrent use — it holds no state between calls beyond the key.
type Signer struct {
	key      ed25519.PrivateKey
	edge     *edgev1.EdgeGlobalRef
	audience string
	now      func() time.Time
	// nonce is the source of the 16 random bytes central refuses a repeat
	// of. A test substitutes it; production leaves it nil for crypto/rand.
	nonce func() ([]byte, error)
}

// NewSigner builds the signer for an enrolled edge.
func NewSigner(key ed25519.PrivateKey, enrollment *edgev1.EnrollResponse, now func() time.Time) *Signer {
	if now == nil {
		now = time.Now
	}
	return &Signer{key: key, edge: enrollment.GetEdge(), audience: enrollment.GetAudience(), now: now}
}

// Header returns the Authorization value for one call: the procedure as
// Connect names it ("/flowseer.api.edge.v1.EdgeService/Heartbeat") and the
// exact request body bytes that will go on the wire.
//
// The body must be the uncompressed bytes central will hash, which for a
// server-stream open is the one Connect-enveloped request message. Central
// refuses a compressed request before verifying, so the bytes signed here are
// the bytes read there.
func (s *Signer) Header(procedure string, body []byte) (string, error) {
	nonce, err := s.newNonce()
	if err != nil {
		return "", err
	}

	issued := s.now().UTC()
	digest := sha256.Sum256(body)

	assertion := &edgev1.EdgeAssertion{}
	assertion.SetEdge(s.edge)
	assertion.SetAudience(s.audience)
	assertion.SetIssuedAt(timestamppb.New(issued))
	assertion.SetExpiresAt(timestamppb.New(issued.Add(assertionLifetime)))
	assertion.SetNonce(nonce)
	assertion.SetProcedure(procedure)
	assertion.SetBodySha256(digest[:])

	// Deterministic, because the bytes signed are the bytes central verifies
	// against and both sides serialize independently.
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(assertion)
	if err != nil {
		return "", errs.From(err).Code(ErrCodeState).Msg("serialize the edge assertion")
	}

	signed := &edgev1.SignedEdgeAssertion{}
	signed.SetPayload(payload)
	signed.SetSignature(ed25519.Sign(s.key, payload))

	wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(signed)
	if err != nil {
		return "", errs.From(err).Code(ErrCodeState).Msg("serialize the signed assertion")
	}
	return HeaderScheme + " " + base64.RawStdEncoding.EncodeToString(wire), nil
}

func (s *Signer) newNonce() ([]byte, error) {
	if s.nonce != nil {
		return s.nonce()
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, errs.From(err).Code(ErrCodeState).Msg("draw an assertion nonce")
	}
	return nonce, nil
}
