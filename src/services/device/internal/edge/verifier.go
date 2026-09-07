package edge

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"sync"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// HeaderScheme is the Authorization scheme every authenticated EdgeService
// call carries: "Authorization: FlowSeer-Edge <base64 SignedEdgeAssertion>".
// The edge agent has its own copy, since this package is internal to the
// device service. The api/edge README's worked header vector is what keeps
// them from drifting: the agent's signer is tested against that string.
const HeaderScheme = "FlowSeer-Edge"

// Error codes, one per rejected verifier step. A caller distinguishes a
// stale clock from a replay from the code alone.
var (
	ErrCodeBadHeader          = errs.NewCode("edge/bad-header")
	ErrCodeBadSignature       = errs.NewCode("edge/bad-signature")
	ErrCodeKeyLookupFailed    = errs.NewCode("edge/key-lookup-failed")
	ErrCodeMalformedAssertion = errs.NewCode("edge/malformed-assertion")
	ErrCodeWrongProcedure     = errs.NewCode("edge/wrong-procedure")
	ErrCodeWrongBodyHash      = errs.NewCode("edge/wrong-body-hash")
	ErrCodeRetiredEdge        = errs.NewCode("edge/retired-edge")
	ErrCodeWrongAudience      = errs.NewCode("edge/wrong-audience")
	ErrCodeClockSkew          = errs.NewCode("edge/clock-skew")
	ErrCodeExpired            = errs.NewCode("edge/expired")
	ErrCodeReplayedNonce      = errs.NewCode("edge/replayed-nonce")
)

// KeyLookup resolves the public key and lifecycle central holds for one
// edge id. An error, an empty key, or a key of the wrong size are all
// treated as a signature failure, because the verifier cannot tell a
// storage error from an unknown edge without leaking which one it is to
// an unauthenticated caller.
type KeyLookup func(ctx context.Context, edgeID string) (ed25519.PublicKey, edgev1.EdgeLifecycle, error)

// Verifier checks a SignedEdgeAssertion header against one Connect call.
// A Verifier is safe for concurrent use.
type Verifier struct {
	audience string
	skew     time.Duration
	lookup   KeyLookup
	now      func() time.Time

	mu   sync.Mutex
	seen map[string]time.Time
}

// NewVerifier returns a Verifier for a central deployment configured with
// the given audience value and clock-skew tolerance.
func NewVerifier(audience string, clockSkew time.Duration, lookup KeyLookup) *Verifier {
	return &Verifier{
		audience: audience,
		skew:     clockSkew,
		lookup:   lookup,
		now:      time.Now,
		seen:     make(map[string]time.Time),
	}
}

// Verify checks header against the invoked procedure's full Connect method
// name (for example "/flowseer.api.edge.v1.EdgeService/Heartbeat") and the
// uncompressed HTTP request body bytes exactly as received — for a
// server-stream open, the Connect-enveloped request message, the same bytes
// the body-verifying middleware hashes off the wire — and returns the parsed
// assertion once every step passes.
func (v *Verifier) Verify(ctx context.Context, header, procedure string, body []byte) (*edgev1.EdgeAssertion, error) {
	raw, ok := strings.CutPrefix(header, HeaderScheme+" ")
	if !ok {
		return nil, errs.New().Code(ErrCodeBadHeader).Msg("authorization header does not carry the FlowSeer-Edge scheme")
	}
	wire, err := base64.RawStdEncoding.DecodeString(raw)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeBadHeader).Msg("decode assertion header")
	}

	signed := &edgev1.SignedEdgeAssertion{}
	if err := proto.Unmarshal(wire, signed); err != nil {
		return nil, errs.From(err).Code(ErrCodeBadHeader).Msg("unmarshal signed assertion")
	}
	if err := protovalidate.Validate(signed); err != nil {
		return nil, errs.From(err).Code(ErrCodeBadHeader).Msg("validate signed assertion envelope")
	}

	// Step 2: read the edge ref from the payload. Every other claim in it
	// is untrusted input until the signature verifies below.
	assertion := &edgev1.EdgeAssertion{}
	if err := proto.Unmarshal(signed.GetPayload(), assertion); err != nil {
		return nil, errs.From(err).Code(ErrCodeBadHeader).Msg("unmarshal assertion payload")
	}
	edgeID := assertion.GetEdge().GetEdge().GetId()
	if edgeID == "" {
		return nil, errs.New().Code(ErrCodeBadHeader).Msg("assertion payload names no edge")
	}

	// Step 3: verify the signature over the raw payload bytes.
	publicKey, lifecycle, lookupErr := v.lookup(ctx, edgeID)
	switch {
	case lookupErr != nil:
		return nil, errs.From(lookupErr).Code(ErrCodeKeyLookupFailed).Retryable().Attr("edge_id", edgeID).Msg("look up edge key")
	case len(publicKey) != ed25519.PublicKeySize:
		// This length check is load-bearing, not belt-and-braces: ed25519.Verify
		// panics on a key that is not exactly PublicKeySize, and an unknown edge
		// looks up to a nil key. Refusing it here is what turns the unknown-edge
		// path — the first one an attacker reaches — into a bad-signature error
		// rather than a crash. It must precede the Verify call below; do not
		// remove it on the assumption Verify validates its own input.
		return nil, errs.New().Code(ErrCodeBadSignature).Attr("edge_id", edgeID).Msg("no key registered for edge")
	case !ed25519.Verify(publicKey, signed.GetPayload(), signed.GetSignature()):
		return nil, errs.New().Code(ErrCodeBadSignature).Attr("edge_id", edgeID).Msg("assertion signature does not verify")
	}

	// Step 4: the parsed assertion passes its own validation.
	if err := protovalidate.Validate(assertion); err != nil {
		return nil, errs.From(err).Code(ErrCodeMalformedAssertion).Attr("edge_id", edgeID).Msg("assertion payload fails validation")
	}

	// Step 5: the procedure binds to the call in progress.
	if assertion.GetProcedure() != procedure {
		return nil, errs.New().Code(ErrCodeWrongProcedure).Attr("edge_id", edgeID).Msg("assertion procedure does not match the invoked RPC")
	}

	// Step 6: the body hash binds to the request in progress.
	bodyHash := sha256.Sum256(body)
	if !bytes.Equal(assertion.GetBodySha256(), bodyHash[:]) {
		return nil, errs.New().Code(ErrCodeWrongBodyHash).Attr("edge_id", edgeID).Msg("assertion body hash does not match the request")
	}

	// Step 7: the edge's lifecycle is enrolled.
	if lifecycle != edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED {
		return nil, errs.New().Code(ErrCodeRetiredEdge).Attr("edge_id", edgeID).Msg("edge is not enrolled")
	}

	// Step 8: the audience matches this deployment.
	if assertion.GetAudience() != v.audience {
		return nil, errs.New().Code(ErrCodeWrongAudience).Attr("edge_id", edgeID).Msg("assertion audience does not match this deployment")
	}

	// Steps 9 and 10: the assertion sits inside its clock window. A skewed
	// issued_at and a genuinely expired assertion get different codes,
	// because the remedy differs: the edge adopts server_time for the
	// first and mints a fresh assertion for the second.
	now := v.now()
	issuedAt := assertion.GetIssuedAt().AsTime()
	if skew := now.Sub(issuedAt); skew > v.skew || skew < -v.skew {
		return nil, errs.New().Code(ErrCodeClockSkew).Attr("edge_id", edgeID).Msg("assertion issued_at is outside the clock skew window")
	}
	if !now.Before(assertion.GetExpiresAt().AsTime()) {
		return nil, errs.New().Code(ErrCodeExpired).Attr("edge_id", edgeID).Msg("assertion has expired")
	}

	// Step 11: the nonce has not been seen for this edge within the
	// validity window.
	if !v.recordNonce(edgeID, assertion.GetNonce(), assertion.GetExpiresAt().AsTime()) {
		return nil, errs.New().Code(ErrCodeReplayedNonce).Attr("edge_id", edgeID).Msg("assertion nonce was already used")
	}

	return assertion, nil
}

// recordNonce reports whether nonce is fresh for edgeID and records it
// until expiresAt. It also purges every entry this Verifier is already
// past the expiry of, so the cache never grows past the edges and
// assertions currently within their validity window.
func (v *Verifier) recordNonce(edgeID string, nonce []byte, expiresAt time.Time) bool {
	key := edgeID + "\x00" + string(nonce)
	now := v.now()

	v.mu.Lock()
	defer v.mu.Unlock()

	for k, exp := range v.seen {
		if !exp.After(now) {
			delete(v.seen, k)
		}
	}

	if exp, ok := v.seen[key]; ok && exp.After(now) {
		return false
	}
	v.seen[key] = expiresAt
	return true
}
