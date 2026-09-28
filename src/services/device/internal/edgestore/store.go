// Package edgestore is central's durable record of every edge, in the edges
// key-value bucket, partitioned by tenant as <tenantID>.<edgeID>, alongside
// an index from each outstanding setup key's identifier to the edge it was
// issued to, and an index from enrolled edge ID to its tenant.
// It holds the edge's public record and the digest of the setup key it was last
// issued, and it is the source the assertion verifier's key lookup reads: the
// enrolled edge's Ed25519 public key and lifecycle. Every write is a
// compare-and-set on one edge's record, so enrollment and re-keying hold across
// central replicas without a lease.
package edgestore

import (
	"context"
	"crypto/ed25519"
	"errors"
	"slices"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// Error codes the store returns.
var (
	// ErrCodeStore is a read or transport failure from the bucket.
	ErrCodeStore = errs.NewCode("edgestore/store")
	// ErrCodeConflict is a CAS write that did not settle within its retry budget.
	ErrCodeConflict = errs.NewCode("edgestore/conflict")
	// ErrCodeDecode is a stored record that will not unmarshal.
	ErrCodeDecode = errs.NewCode("edgestore/decode")
	// ErrCodeState is a write invalid for the record's current state.
	ErrCodeState = errs.NewCode("edgestore/state")
)

const (
	casRetries          = 8
	setupKeyIndexPrefix = "setupkey_"
	edgeIndexPrefix     = "edge_"
)

func edgeRecordKey(tenantID, edgeID string) string {
	return tenantID + "." + edgeID
}

// Store is central's per-edge record store over the edges bucket. Safe for
// concurrent use.
type Store struct {
	kv jetstream.KeyValue
}

// New constructs a Store over the edges bucket.
func New(kv jetstream.KeyValue) *Store { return &Store{kv: kv} }

// Get returns one edge's stored record and the bucket revision it was read at,
// or a nil record and revision 0 when the edge has none yet.
func (s *Store) Get(ctx context.Context, tenantID, edgeID string) (*storev1.StoredEdge, uint64, error) {
	if tenantID == "" || edgeID == "" {
		return nil, 0, nil
	}
	key := edgeRecordKey(tenantID, edgeID)
	entry, err := s.kv.Get(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, errs.From(err).Code(ErrCodeStore).Attr("tenant", tenantID).Attr("edge", edgeID).Msg("read edge record")
	}
	rec := &storev1.StoredEdge{}
	if err := proto.Unmarshal(entry.Value(), rec); err != nil {
		return nil, 0, errs.From(err).Code(ErrCodeDecode).Attr("tenant", tenantID).Attr("edge", edgeID).Msg("decode edge record")
	}
	return rec, entry.Revision(), nil
}

// Lookup is the assertion verifier's key lookup: the enrolled Ed25519 public
// key and lifecycle for an edge. It resolves the edge's tenant via the edge
// index, then loads the partitioned record. An edge with no record returns a
// nil key and the unspecified lifecycle with no error, so the verifier fails
// it as a bad signature without the store having to tell an unknown edge from a
// stored one to an unauthenticated caller; a real store failure returns the
// error, which the verifier reports as a retryable lookup failure.
func (s *Store) Lookup(ctx context.Context, edgeID string) (ed25519.PublicKey, edgev1.EdgeLifecycle, error) {
	tenantID, err := s.TenantForEdge(ctx, edgeID)
	if err != nil {
		return nil, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_UNSPECIFIED, err
	}
	if tenantID == "" {
		return nil, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_UNSPECIFIED, nil
	}
	rec, _, err := s.Get(ctx, tenantID, edgeID)
	if err != nil {
		return nil, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_UNSPECIFIED, err
	}
	if rec == nil {
		return nil, edgev1.EdgeLifecycle_EDGE_LIFECYCLE_UNSPECIFIED, nil
	}
	state := rec.GetRecord().GetState()
	return ed25519.PublicKey(state.GetPublicKey()), state.GetLifecycle(), nil
}

// EdgeForSetupKey returns the tenant and edge a setup key identifier was issued to,
// or empty strings when no entry names it.
func (s *Store) EdgeForSetupKey(ctx context.Context, keyID string) (tenantID string, edgeID string, err error) {
	entry, err := s.kv.Get(ctx, setupKeyIndexPrefix+keyID)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return "", "", nil
	}
	if err != nil {
		return "", "", errs.From(err).Code(ErrCodeStore).Msg("read setup key index")
	}
	val := string(entry.Value())
	parts := strings.SplitN(val, ".", 2)
	if len(parts) != 2 {
		return "", "", errs.New().Code(ErrCodeDecode).Attr("value", val).Msg("malformed setup key index entry")
	}
	return parts[0], parts[1], nil
}

// IndexSetupKey points a setup key identifier at an edge under tenantID.
func (s *Store) IndexSetupKey(ctx context.Context, keyID, tenantID, edgeID string) error {
	val := tenantID + "." + edgeID
	if _, err := s.kv.Put(ctx, setupKeyIndexPrefix+keyID, []byte(val)); err != nil {
		return errs.From(err).Code(ErrCodeStore).Attr("tenant", tenantID).Attr("edge", edgeID).Msg("write setup key index")
	}
	return nil
}

// UnindexSetupKey drops a setup key identifier's entry. An entry that outlives
// the digest it was written beside is harmless, so a caller that cannot delete
// one has not left a key usable.
func (s *Store) UnindexSetupKey(ctx context.Context, keyID string) error {
	if err := s.kv.Delete(ctx, setupKeyIndexPrefix+keyID); err != nil {
		return errs.From(err).Code(ErrCodeStore).Msg("delete setup key index")
	}
	return nil
}

// IndexEdge records the tenant an enrolled edge belongs to.
func (s *Store) IndexEdge(ctx context.Context, edgeID, tenantID string) error {
	if _, err := s.kv.Put(ctx, edgeIndexPrefix+edgeID, []byte(tenantID)); err != nil {
		return errs.From(err).Code(ErrCodeStore).Attr("tenant", tenantID).Attr("edge", edgeID).Msg("write edge index")
	}
	return nil
}

// TenantForEdge returns the tenant ID for an enrolled edge, or empty string if not found.
func (s *Store) TenantForEdge(ctx context.Context, edgeID string) (string, error) {
	entry, err := s.kv.Get(ctx, edgeIndexPrefix+edgeID)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return "", nil
	}
	if err != nil {
		return "", errs.From(err).Code(ErrCodeStore).Attr("edge", edgeID).Msg("read edge index")
	}
	return string(entry.Value()), nil
}

// Keys returns every stored edge id belonging to tenantID in ascending order.
// An empty bucket or tenant with no edges returns nil keys and no error.
func (s *Store) Keys(ctx context.Context, tenantID string) ([]string, error) {
	keys, err := s.kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStore).Attr("tenant", tenantID).Msg("list edge records")
	}
	prefix := tenantID + "."
	var edges []string
	for _, key := range keys {
		if strings.HasPrefix(key, prefix) {
			edges = append(edges, strings.TrimPrefix(key, prefix))
		}
	}
	slices.Sort(edges)
	return edges, nil
}

// Mutate runs fn against the edge's record under compare-and-set, retrying on
// a revision conflict. fn receives the current record, or nil when the edge
// has none, and returns the record to store. Returning ErrSkip stores nothing.
func (s *Store) Mutate(ctx context.Context, tenantID, edgeID string, fn func(current *storev1.StoredEdge) (*storev1.StoredEdge, error)) (*storev1.StoredEdge, error) {
	key := edgeRecordKey(tenantID, edgeID)
	for attempt := 0; attempt < casRetries; attempt++ {
		current, revision, err := s.Get(ctx, tenantID, edgeID)
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
			return nil, errs.From(err).Code(ErrCodeDecode).Attr("tenant", tenantID).Attr("edge", edgeID).Msg("encode edge record")
		}
		if revision == 0 {
			_, err = s.kv.Create(ctx, key, data)
			if errors.Is(err, jetstream.ErrKeyExists) {
				continue // another writer created it; reload and retry
			}
		} else {
			_, err = s.kv.Update(ctx, key, data, revision)
			if errors.Is(err, jetstream.ErrKeyRevisionMismatch) {
				continue // another writer advanced it; reload and retry
			}
		}
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeConflict).Attr("tenant", tenantID).Attr("edge", edgeID).Msg("write edge record")
		}
		return next, nil
	}
	return nil, errs.New().Code(ErrCodeConflict).Attr("tenant", tenantID).Attr("edge", edgeID).Msg("edge record write did not settle")
}

// ErrSkip is a Mutate fn's signal that no write is needed.
var ErrSkip = errs.Msg("edgestore: no write needed")
