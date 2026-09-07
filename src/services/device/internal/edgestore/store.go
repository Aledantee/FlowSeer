// Package edgestore is central's durable record of every edge, in the edges
// key-value bucket, one key per edge id. It holds the edge's public record and
// the digest of the setup key it was last issued, and it is the source the
// assertion verifier's key lookup reads: the enrolled edge's Ed25519 public
// key and lifecycle. Every write is a compare-and-set on one edge's record, so
// enrollment and re-keying hold across central replicas without a lease.
package edgestore

import (
	"context"
	"crypto/ed25519"
	"errors"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// Error codes the store returns.
var (
	// ErrCodeStore is a read or transport failure from the bucket.
	ErrCodeStore = errs.NewCode("edgestore/store")
	// ErrCodeConflict is a CAS write that did not settle within its retry
	// budget.
	ErrCodeConflict = errs.NewCode("edgestore/conflict")
	// ErrCodeDecode is a stored record that will not unmarshal.
	ErrCodeDecode = errs.NewCode("edgestore/decode")
	// ErrCodeState is a write invalid for the record's current state.
	ErrCodeState = errs.NewCode("edgestore/state")
)

const casRetries = 8

// Store is central's per-edge record store over the edges bucket. Safe for
// concurrent use.
type Store struct {
	kv jetstream.KeyValue
}

// New constructs a Store over the edges bucket.
func New(kv jetstream.KeyValue) *Store { return &Store{kv: kv} }

// Get returns one edge's stored record and the bucket revision it was read at,
// or a nil record and revision 0 when the edge has none yet.
func (s *Store) Get(ctx context.Context, edgeID string) (*storev1.StoredEdge, uint64, error) {
	entry, err := s.kv.Get(ctx, edgeID)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, errs.From(err).Code(ErrCodeStore).Attr("edge", edgeID).Msg("read edge record")
	}
	rec := &storev1.StoredEdge{}
	if err := proto.Unmarshal(entry.Value(), rec); err != nil {
		return nil, 0, errs.From(err).Code(ErrCodeDecode).Attr("edge", edgeID).Msg("decode edge record")
	}
	return rec, entry.Revision(), nil
}

// Lookup is the assertion verifier's key lookup: the enrolled Ed25519 public
// key and lifecycle for an edge. An edge with no record returns a nil key and
// the unspecified lifecycle with no error, so the verifier fails it as a bad
// signature without the store having to tell an unknown edge from a stored one
// to an unauthenticated caller; a real store failure returns the error, which
// the verifier reports as a retryable lookup failure.
func (s *Store) Lookup(ctx context.Context, edgeID string) (ed25519.PublicKey, edgev1.EdgeLifecycle, error) {
	rec, _, err := s.Get(ctx, edgeID)
	if err != nil {
		return nil, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_UNSPECIFIED, err
	}
	if rec == nil {
		return nil, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_UNSPECIFIED, nil
	}
	state := rec.GetRecord().GetState()
	return ed25519.PublicKey(state.GetPublicKey()), state.GetLifecycle(), nil
}

// Keys returns every stored edge id in ascending order. An empty bucket
// returns no keys and no error, so a listing over a deployment with no edges
// is an empty page rather than a failure.
func (s *Store) Keys(ctx context.Context) ([]string, error) {
	keys, err := s.kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStore).Msg("list edge records")
	}
	return keys, nil
}

// Mutate runs fn against the edge's record under compare-and-set, retrying on
// a revision conflict. fn receives the current record, or nil when the edge
// has none, and returns the record to store. Returning ErrSkip stores nothing.
func (s *Store) Mutate(ctx context.Context, edgeID string, fn func(current *storev1.StoredEdge) (*storev1.StoredEdge, error)) (*storev1.StoredEdge, error) {
	for attempt := 0; attempt < casRetries; attempt++ {
		current, revision, err := s.Get(ctx, edgeID)
		if err != nil {
			return nil, err
		}
		next, err := fn(current)
		if err != nil {
			if errors.Is(err, ErrSkip) {
				return current, nil
			}
			return nil, err
		}
		data, err := proto.Marshal(next)
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeDecode).Attr("edge", edgeID).Msg("encode edge record")
		}
		if revision == 0 {
			_, err = s.kv.Create(ctx, edgeID, data)
			if errors.Is(err, jetstream.ErrKeyExists) {
				continue // another writer created it; reload and retry
			}
		} else {
			_, err = s.kv.Update(ctx, edgeID, data, revision)
			if errors.Is(err, jetstream.ErrKeyRevisionMismatch) {
				continue // another writer advanced it; reload and retry
			}
		}
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeConflict).Attr("edge", edgeID).Msg("write edge record")
		}
		return next, nil
	}
	return nil, errs.New().Code(ErrCodeConflict).Attr("edge", edgeID).Msg("edge record write did not settle")
}

// ErrSkip is a Mutate fn's signal that no write is needed.
var ErrSkip = errors.New("edgestore: no write needed")
