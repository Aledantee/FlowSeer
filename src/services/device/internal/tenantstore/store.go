// Package tenantstore is central's durable record of every tenant in the
// tenants key-value bucket. Primary records are keyed by tenant ID (UUID),
// alongside secondary index keys mapping (issuer, organization claim) to
// tenant ID.
package tenantstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// Error codes the tenant store returns.
var (
	// ErrCodeStore is a read or transport failure from the bucket.
	ErrCodeStore = errs.NewCode("tenantstore/store")
	// ErrCodeConflict is a CAS write that did not settle within its retry budget.
	ErrCodeConflict = errs.NewCode("tenantstore/conflict")
	// ErrCodeDecode is a stored record that will not unmarshal.
	ErrCodeDecode = errs.NewCode("tenantstore/decode")
	// ErrCodeAlreadyExists indicates a tenant or organization index already exists.
	ErrCodeAlreadyExists = errs.NewCode("tenantstore/already-exists")
	// ErrCodeInvalidConfig indicates the supplied tenant configuration is invalid.
	ErrCodeInvalidConfig = errs.NewCode("tenantstore/invalid-config")
)

const (
	casRetries     = 8
	orgIndexPrefix = "org_"
)

// ErrSkip is a Mutate fn's signal that no write is needed.
var ErrSkip = errs.Msg("tenantstore: no write needed")

// OrgIndexKey formats the secondary index key for a given issuer and organization claim value.
func OrgIndexKey(issuer, orgClaimValue string) string {
	h := sha256.Sum256([]byte(issuer + "\x00" + orgClaimValue))
	return fmt.Sprintf("%s%x", orgIndexPrefix, h)
}

// Store is central's per-tenant record store over the tenants KV bucket.
// Safe for concurrent use.
type Store struct {
	kv jetstream.KeyValue
}

// New constructs a Store over the tenants bucket.
func New(kv jetstream.KeyValue) *Store {
	return &Store{kv: kv}
}

// Create stores a new tenant and its organization secondary index entry.
// Returns ErrCodeAlreadyExists if the tenant ID or organization is already registered.
func (s *Store) Create(ctx context.Context, config *identityv1.TenantConfig) (*identityv1.TenantRecord, error) {
	if config == nil || config.GetRef() == nil || config.GetRef().GetTenant() == nil {
		return nil, errs.New().Code(ErrCodeInvalidConfig).Msg("tenant config missing ref")
	}
	tenantID := config.GetRef().GetTenant().GetId()
	if tenantID == "" {
		return nil, errs.New().Code(ErrCodeInvalidConfig).Msg("tenant id is empty")
	}
	if config.GetIssuer() == "" || config.GetOrganizationClaimValue() == "" {
		return nil, errs.New().Code(ErrCodeInvalidConfig).Msg("tenant issuer or organization claim value is empty")
	}

	orgKey := OrgIndexKey(config.GetIssuer(), config.GetOrganizationClaimValue())
	// Create secondary index first to claim the (issuer, org) pair.
	if _, err := s.kv.Create(ctx, orgKey, []byte(tenantID)); err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			return nil, errs.New().Code(ErrCodeAlreadyExists).
				Attr("issuer", config.GetIssuer()).
				Attr("organization", config.GetOrganizationClaimValue()).
				Msg("tenant with organization already exists")
		}
		return nil, errs.From(err).Code(ErrCodeStore).Msg("write organization index")
	}

	active := identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE
	state := identityv1.TenantState_builder{
		Ref:       config.GetRef(),
		Lifecycle: &active,
		CreatedAt: timestamppb.Now(),
	}.Build()

	record := identityv1.TenantRecord_builder{
		Config: config,
		State:  state,
	}.Build()

	data, err := proto.Marshal(record)
	if err != nil {
		_ = s.kv.Delete(ctx, orgKey)
		return nil, errs.From(err).Code(ErrCodeDecode).Attr("tenant", tenantID).Msg("encode tenant record")
	}

	if _, err := s.kv.Create(ctx, tenantID, data); err != nil {
		_ = s.kv.Delete(ctx, orgKey)
		if errors.Is(err, jetstream.ErrKeyExists) {
			return nil, errs.New().Code(ErrCodeAlreadyExists).Attr("tenant", tenantID).Msg("tenant already exists")
		}
		return nil, errs.From(err).Code(ErrCodeStore).Attr("tenant", tenantID).Msg("write tenant record")
	}

	return record, nil
}

// Get returns the stored tenant record for tenantID, or nil if no record exists.
func (s *Store) Get(ctx context.Context, tenantID string) (*identityv1.TenantRecord, error) {
	rec, _, err := s.GetWithRevision(ctx, tenantID)
	return rec, err
}

// GetWithRevision returns the stored tenant record for tenantID and its KV revision,
// or nil and revision 0 if no record exists.
func (s *Store) GetWithRevision(ctx context.Context, tenantID string) (*identityv1.TenantRecord, uint64, error) {
	entry, err := s.kv.Get(ctx, tenantID)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, errs.From(err).Code(ErrCodeStore).Attr("tenant", tenantID).Msg("read tenant record")
	}
	rec := &identityv1.TenantRecord{}
	if err := proto.Unmarshal(entry.Value(), rec); err != nil {
		return nil, 0, errs.From(err).Code(ErrCodeDecode).Attr("tenant", tenantID).Msg("decode tenant record")
	}
	return rec, entry.Revision(), nil
}

// LookupByOrg resolves a tenant record by its identity provider issuer and organization claim value.
// Returns nil if no matching tenant exists.
func (s *Store) LookupByOrg(ctx context.Context, issuer, orgClaimValue string) (*identityv1.TenantRecord, error) {
	orgKey := OrgIndexKey(issuer, orgClaimValue)
	entry, err := s.kv.Get(ctx, orgKey)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStore).Msg("read organization index")
	}
	tenantID := string(entry.Value())
	return s.Get(ctx, tenantID)
}

// List returns all stored tenant records in ascending order by tenant ID.
// Secondary index keys (prefixed with "org_") are filtered out.
func (s *Store) List(ctx context.Context) ([]*identityv1.TenantRecord, error) {
	keys, err := s.kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStore).Msg("list tenant records")
	}

	var tenantIDs []string
	for _, key := range keys {
		if !strings.HasPrefix(key, orgIndexPrefix) {
			tenantIDs = append(tenantIDs, key)
		}
	}
	slices.Sort(tenantIDs)

	records := make([]*identityv1.TenantRecord, 0, len(tenantIDs))
	for _, id := range tenantIDs {
		rec, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			records = append(records, rec)
		}
	}
	return records, nil
}

// Mutate runs fn against the tenant's record under compare-and-set, retrying on
// a revision conflict. fn receives the current record, or nil when the tenant
// has none, and returns the record to store. Returning ErrSkip stores nothing.
func (s *Store) Mutate(ctx context.Context, tenantID string, fn func(current *identityv1.TenantRecord) (*identityv1.TenantRecord, error)) (*identityv1.TenantRecord, error) {
	for attempt := 0; attempt < casRetries; attempt++ {
		current, revision, err := s.GetWithRevision(ctx, tenantID)
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
			return nil, errs.From(err).Code(ErrCodeDecode).Attr("tenant", tenantID).Msg("encode tenant record")
		}
		if revision == 0 {
			_, err = s.kv.Create(ctx, tenantID, data)
			if errors.Is(err, jetstream.ErrKeyExists) {
				continue
			}
		} else {
			_, err = s.kv.Update(ctx, tenantID, data, revision)
			if errors.Is(err, jetstream.ErrKeyRevisionMismatch) {
				continue
			}
		}
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeConflict).Attr("tenant", tenantID).Msg("write tenant record")
		}
		return next, nil
	}
	return nil, errs.New().Code(ErrCodeConflict).Attr("tenant", tenantID).Msg("tenant record write did not settle")
}
