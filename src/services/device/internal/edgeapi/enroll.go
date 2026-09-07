package edgeapi

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
)

// Enroll consumes a setup key and registers the edge's Ed25519 public key,
// which is the key the assertion verifier reads from then on. It is the only
// call that carries no assertion, so it authenticates on the setup key alone.
//
// Repeating it with the same key and the same public key returns the same
// identity and writes nothing, so an edge that lost the response recovers by
// calling again. Repeating it with the same key and a different public key is
// refused and logged: that is what a setup key read out of a shipped box looks
// like, and the operator's answer is to retire the edge.
func (s *Service) Enroll(ctx context.Context, req *connect.Request[edgev1.EnrollRequest]) (*connect.Response[edgev1.EnrollResponse], error) {
	key := req.Msg.GetSetupKey()
	keyID, ok := setupKeyID(key)
	if !ok {
		return nil, connectErr(errs.New().Code(ErrCodeRequest).Msg("setup key is not a well-formed key string"))
	}

	edgeID, err := s.store.EdgeForSetupKey(ctx, keyID)
	if err != nil {
		return nil, connectErr(err)
	}
	stored, _, err := s.storedFor(ctx, edgeID)
	if err != nil {
		return nil, connectErr(err)
	}
	// The presented key is compared against the stored digest in constant time,
	// and against a fixed zero digest when the edge holds none, so an
	// identifier that names no live key costs the same as one that does. An
	// early-return compare here would accept and reject correctly in every test
	// and leak the secret by timing.
	if !setupKeyMatches(key, stored) {
		return nil, connectErr(errs.New().Code(ErrCodeSetupKeyRefused).Attr("setup_key_id", keyID).
			Msg("setup key does not enroll"))
	}

	// Past this line the caller has proven possession of the key, so a refusal
	// may say which condition failed.
	state := stored.GetRecord().GetState()
	if state.GetLifecycle() == edgev1.EdgeLifecycle_EDGE_LIFECYCLE_RETIRED {
		return nil, connectErr(errs.New().Code(ErrCodeLifecycle).Attr("edge", edgeID).Msg("edge is retired"))
	}
	setupKey := state.GetSetupKey()
	switch setupKey.GetStatus() {
	case edgev1.SetupKeyStatus_SETUP_KEY_STATUS_ISSUED, edgev1.SetupKeyStatus_SETUP_KEY_STATUS_CONSUMED:
	default:
		return nil, connectErr(errs.New().Code(ErrCodeSetupKey).Attr("edge", edgeID).
			Attr("status", setupKey.GetStatus().String()).Msg("setup key is no longer usable"))
	}
	now := s.clock()
	if !now.Before(setupKey.GetExpiresAt().AsTime()) {
		return nil, connectErr(errs.New().Code(ErrCodeSetupKey).Attr("edge", edgeID).Msg("setup key has expired"))
	}

	public, err := keyProofPublicKey(req.Msg.GetProof(), func(payload *edgev1.KeyProofPayload) error {
		if payload.GetSetupKeyId() != keyID {
			return errs.New().Code(ErrCodeKeyProof).Attr("edge", edgeID).
				Msg("key proof is not bound to the setup key it was presented with")
		}
		return nil
	})
	if err != nil {
		return nil, connectErr(err)
	}

	if setupKey.GetStatus() == edgev1.SetupKeyStatus_SETUP_KEY_STATUS_CONSUMED {
		if !ed25519.PublicKey(state.GetPublicKey()).Equal(public) {
			s.log.WarnContext(ctx, "setup key presented with another key",
				slog.String("otel.event.name", "flowseer.edge.setup_key.reused"),
				slog.String("flowseer.edge.id", edgeID),
				slog.String("flowseer.edge.setup_key.id", keyID),
			)
			return nil, connectErr(errs.New().Code(ErrCodeSetupKeyRefused).Attr("edge", edgeID).
				Attr("setup_key_id", keyID).Msg("setup key was already used to register another key"))
		}
		return s.enrollResponse(edgeID, now), nil
	}

	if _, err := s.store.Mutate(ctx, edgeID, func(current *storev1.StoredEdge) (*storev1.StoredEdge, error) {
		return consumeSetupKey(current, key, edgeID, public, now)
	}); err != nil {
		return nil, connectErr(err)
	}

	return s.enrollResponse(edgeID, now), nil
}

// consumeSetupKey is the enrollment write, run by the store against whatever the
// record holds at write time.
//
// It re-runs the digest comparison rather than trusting the one that
// authenticated the caller, because that one ran against a record read two round
// trips earlier and this runs against the record being written. They have to be
// one decision. An operator whose setup key leaked reaches for IssueSetupKey,
// which leaves a fresh ISSUED key on the record; a caller holding the old key
// that landed in the window between the two reads would otherwise consume the
// replacement it never presented and register itself as the edge, with the
// remedy for the leak being what opened the hole. Anything added here that
// authorizes a write must check what it is authorizing against, not inherit a
// decision made before the record was re-read.
func consumeSetupKey(current *storev1.StoredEdge, key, edgeID string, public ed25519.PublicKey, now time.Time) (*storev1.StoredEdge, error) {
	if current == nil {
		return nil, notFound(edgeID)
	}
	if !setupKeyMatches(key, current) {
		return nil, errs.New().Code(ErrCodeSetupKeyRefused).Attr("edge", edgeID).Msg("setup key does not enroll")
	}

	state := current.GetRecord().GetState()
	stored := state.GetSetupKey()
	if stored.GetStatus() != edgev1.SetupKeyStatus_SETUP_KEY_STATUS_ISSUED {
		// Another replica enrolled between the read above and this write. Its
		// answer is the same identity, so accept it rather than registering a
		// second key over the first.
		if !ed25519.PublicKey(state.GetPublicKey()).Equal(public) {
			return nil, errs.New().Code(ErrCodeSetupKeyRefused).Attr("edge", edgeID).
				Msg("setup key was already used to register another key")
		}
		return nil, edgestore.ErrSkip
	}

	stored.SetStatus(edgev1.SetupKeyStatus_SETUP_KEY_STATUS_CONSUMED)
	state.SetLifecycle(edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED)
	state.SetContact(edgev1.EdgeContact_EDGE_CONTACT_ACTIVE)
	state.SetPublicKey(public)
	state.SetEnrolledAt(timestamppb.New(now))
	state.SetLastSeenAt(timestamppb.New(now))
	return current, nil
}

// Rekey replaces the calling edge's registered key with the one its proof
// names. The old key is refused from the next call on, because the verifier
// reads the registered key and there is only ever one. A call whose proof names
// the key already registered succeeds and writes nothing, so an edge that lost
// the response repeats the call with either key.
//
// An OpenDeviceSubmission stream the edge opened under the old key keeps
// running: a stream is authorized when it opens, and its grant's own deadline
// bounds it. Ending the edge's standing outright is RetireEdge.
func (s *Service) Rekey(ctx context.Context, req *connect.Request[edgev1.RekeyRequest]) (*connect.Response[edgev1.RekeyResponse], error) {
	edgeID, err := EdgeIDFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	nonce, err := AssertionNonceFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	public, err := keyProofPublicKey(req.Msg.GetProof(), func(payload *edgev1.KeyProofPayload) error {
		if subtle.ConstantTimeCompare(payload.GetAssertionNonce(), nonce) != 1 {
			return errs.New().Code(ErrCodeKeyProof).Attr("edge", edgeID).
				Msg("key proof is not bound to this call's assertion")
		}
		return nil
	})
	if err != nil {
		return nil, connectErr(err)
	}

	now := s.clock()
	if _, err := s.store.Mutate(ctx, edgeID, func(current *storev1.StoredEdge) (*storev1.StoredEdge, error) {
		if current == nil {
			return nil, notFound(edgeID)
		}
		state := current.GetRecord().GetState()
		if state.GetLifecycle() != edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED {
			return nil, errs.New().Code(ErrCodeLifecycle).Attr("edge", edgeID).Msg("edge is not enrolled")
		}
		if ed25519.PublicKey(state.GetPublicKey()).Equal(public) {
			return nil, edgestore.ErrSkip
		}
		state.SetPublicKey(public)
		state.SetEnrolledAt(timestamppb.New(now))
		state.SetLastSeenAt(timestamppb.New(now))
		return current, nil
	}); err != nil {
		return nil, connectErr(err)
	}

	return connect.NewResponse(edgev1.RekeyResponse_builder{ServerTime: timestamppb.New(now)}.Build()), nil
}

func (s *Service) enrollResponse(edgeID string, now time.Time) *connect.Response[edgev1.EnrollResponse] {
	return connect.NewResponse(edgev1.EnrollResponse_builder{
		Edge:         edgeRef(edgeID),
		ServerTime:   timestamppb.New(now),
		Audience:     proto.String(s.cfg.Audience),
		TrustAnchors: s.cfg.TrustAnchors,
	}.Build())
}

// storedFor reads an edge's record, tolerating an empty id so a setup key that
// resolved to nothing still reaches the constant-time comparison below.
func (s *Service) storedFor(ctx context.Context, edgeID string) (*storev1.StoredEdge, uint64, error) {
	if edgeID == "" {
		return nil, 0, nil
	}
	return s.store.Get(ctx, edgeID)
}

// setupKeyID is the identifier segment of a well-formed setup key string.
func setupKeyID(key string) (string, bool) {
	if !setupKeyStringPattern.MatchString(key) {
		return "", false
	}
	return key[len(setupKeyPrefix) : len(setupKeyPrefix)+setupKeyIDLen], true
}

// setupKeyMatches compares a presented key against an edge's stored digest in
// constant time. An edge with no record, or one holding no digest, compares
// against a fixed zero digest of the same length, so the comparison runs to
// completion either way and its duration says nothing about which case it was.
func setupKeyMatches(key string, stored *storev1.StoredEdge) bool {
	digest := hashSetupKey(key)
	want := stored.GetSetupKeyHash()
	if len(want) != sha256.Size {
		want = make([]byte, sha256.Size)
	}
	return subtle.ConstantTimeCompare(digest, want) == 1
}

// keyProofPublicKey checks that a key proof shows possession of the private
// half of the key it registers, and that it is bound to this registration by
// bind. The signature is verified over the payload bytes exactly as received,
// so a proof cannot be replayed under a re-encoding of the same claims.
func keyProofPublicKey(proof *edgev1.KeyProof, bind func(*edgev1.KeyProofPayload) error) (ed25519.PublicKey, error) {
	if err := protovalidate.Validate(proof); err != nil {
		return nil, errs.From(err).Code(ErrCodeKeyProof).Msg("key proof fails its schema rules")
	}
	payload := &edgev1.KeyProofPayload{}
	if err := proto.Unmarshal(proof.GetPayload(), payload); err != nil {
		return nil, errs.From(err).Code(ErrCodeKeyProof).Msg("unmarshal key proof payload")
	}
	if err := protovalidate.Validate(payload); err != nil {
		return nil, errs.From(err).Code(ErrCodeKeyProof).Msg("key proof payload fails its schema rules")
	}
	if err := bind(payload); err != nil {
		return nil, err
	}

	public := ed25519.PublicKey(payload.GetPublicKey())
	if len(public) != ed25519.PublicKeySize {
		return nil, errs.New().Code(ErrCodeKeyProof).Msg("key proof names no Ed25519 public key")
	}
	if !ed25519.Verify(public, proof.GetPayload(), proof.GetSignature()) {
		return nil, errs.New().Code(ErrCodeKeyProof).Msg("key proof signature does not verify")
	}
	return public, nil
}
