package identity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// Enroller is the one EdgeService call an edge makes before it has an
// identity to sign with. Satisfied by the generated client; an interface so
// the ordering below can be tested without a server.
type Enroller interface {
	Enroll(context.Context, *connect.Request[edgev1.EnrollRequest]) (*connect.Response[edgev1.EnrollResponse], error)
}

// Identity is an enrolled edge: the key central registered and the answer
// that named it.
//
// The key is unexported because it is a private key, and an exported field
// holding one renders in full through fmt, slog and JSON — ed25519.PrivateKey
// is a []byte and nothing on the path to it redacts. Reaching it is this
// package's business; what a caller needs is Signer.
type Identity struct {
	key        ed25519.PrivateKey
	Enrollment *edgev1.EnrollResponse
	// Set only when Enroll returned in this process. The time inside a loaded
	// enrollment is historical and cannot define a current clock offset.
	serverTime time.Time
}

// Signer builds this identity's assertion headers. A fresh enrollment seeds
// its clock from central; a loaded enrollment starts from the local clock and
// lets the signed transport recover a skewed clock from a fresh refusal.
//
// The schema has central return its own time for one reason: an edge whose
// real-time clock is dead signs with a date from the factory, central refuses
// the assertion for skew, and bus attachment — a fatal startup failure —
// never succeeds. Adopting central's clock is what makes that edge work, and
// doing it from the transport rather than only at enrollment is what carries
// it past the run that enrolled.
func (i *Identity) Signer(ctx context.Context, now func() time.Time, log *slog.Logger) *Signer {
	signer := NewSigner(i.key, i.Enrollment, now)
	if log != nil {
		signer.log = log
	}
	if !i.serverTime.IsZero() {
		signer.AdoptServerTime(ctx, i.serverTime)
	}
	return signer
}

// TrustAnchors are the SPKI digests this edge pins central by, replacing
// whatever it was provisioned with.
func (i *Identity) TrustAnchors() [][]byte { return i.Enrollment.GetTrustAnchors() }

// Establish returns this edge's identity, enrolling if it has not already.
//
// The order is the point, and it is asymmetric. The key is written first,
// then Enroll is called, then the answer is written:
//
//   - Crash before the key lands: nothing happened. The next start generates
//     a key and enrolls; the setup key is still unconsumed.
//   - Crash after the key, before the call: the next start enrolls with that
//     key; the setup key is still unconsumed.
//   - Crash after the call returns, before the answer lands: the next start
//     enrolls again with the same key, and central answers a repeat of the
//     same key with the same response rather than refusing it.
//   - Crash after the answer lands: nothing left to do.
//
// Reversed — enroll first, then persist — the third case has no way back.
// Central would hold a public key whose private half never reached disk, so
// this edge could not sign, so it could not call Rekey to replace the key it
// does not have. An operator would have to retire the edge and issue a fresh
// setup key, because IssueSetupKey refuses an enrolled one. One order costs a
// round trip; the other costs the edge.
//
// setupKey is only read when this edge has no enrollment yet. An edge that is
// already enrolled never presents one again, and never needs one to survive a
// restart.
//
// now must be the same clock the returned identity's Signer is given, since
// what is kept is the difference between the two. Nil means time.Now, which
// is what the agent passes on both sides.
func Establish(ctx context.Context, store *Store, client Enroller, setupKey string) (*Identity, error) {
	key, err := store.LoadKey()
	if err != nil {
		return nil, err
	}
	if key == nil {
		_, generated, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeState).Msg("generate the agent key")
		}
		// Before the call, always. See this function's doc for the crash
		// point this order exists to survive.
		if err := store.SaveKey(generated); err != nil {
			return nil, err
		}
		key = generated
	}

	enrollment, err := store.LoadEnrollment()
	if err != nil {
		return nil, err
	}
	if enrollment != nil {
		return &Identity{key: key, Enrollment: enrollment}, nil
	}

	if setupKey == "" {
		return nil, errs.New().Code(ErrCodeEnroll).
			Msg("this edge is not enrolled and was given no setup key")
	}

	proof, err := keyProof(key, setupKey)
	if err != nil {
		return nil, err
	}
	request := &edgev1.EnrollRequest{}
	request.SetSetupKey(setupKey)
	request.SetProof(proof)

	response, err := client.Enroll(ctx, connect.NewRequest(request))
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeEnroll).Msg("enroll with central")
	}
	if err := store.SaveEnrollment(response.Msg); err != nil {
		return nil, err
	}
	// Central's own time, kept as the answer gave it. The round trip is
	// inside any difference it implies, which is why the schema caps an
	// assertion's life well above the trip rather than subtracting it.
	identity := &Identity{key: key, Enrollment: response.Msg}
	if serverTime := response.Msg.GetServerTime(); serverTime != nil {
		identity.serverTime = serverTime.AsTime()
	}
	return identity, nil
}

// keyProof proves possession of the key being registered, bound to the setup
// key's identifier so the proof cannot be lifted onto another enrollment.
func keyProof(key ed25519.PrivateKey, setupKey string) (*edgev1.KeyProof, error) {
	id, ok := setupKeyID(setupKey)
	if !ok {
		return nil, errs.New().Code(ErrCodeEnroll).Msg("setup key is not a well-formed key string")
	}

	payload := &edgev1.KeyProofPayload{}
	payload.SetPublicKey(key.Public().(ed25519.PublicKey))
	payload.SetSetupKeyId(id)

	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeEnroll).Msg("serialize the key proof")
	}

	proof := &edgev1.KeyProof{}
	proof.SetPayload(body)
	proof.SetSignature(ed25519.Sign(key, body))
	return proof, nil
}

// setupKeyID is the identifier segment of a setup key string,
// "fse1_<id>_<secret>". Only the identifier is bound into the proof: it is
// the half the schema documents as safe to carry in the clear.
func setupKeyID(key string) (string, bool) {
	const prefix = "fse1_"
	if len(key) < len(prefix) || key[:len(prefix)] != prefix {
		return "", false
	}
	rest := key[len(prefix):]
	for i := range rest {
		if rest[i] == '_' {
			return rest[:i], i > 0
		}
	}
	return "", false
}
