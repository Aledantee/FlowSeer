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
	"time"

	"buf.build/go/protovalidate"
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
	casRetries             = 8
	orgIndexPrefix         = "org_"
	defaultRollbackTimeout = 5 * time.Second
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
	kv              jetstream.KeyValue
	rollbackTimeout time.Duration
}

// Option configures Store behavior.
type Option func(*Store)

// WithRollbackTimeout overrides the timeout for record writes and rollback deletes.
func WithRollbackTimeout(d time.Duration) Option {
	return func(s *Store) {
		s.rollbackTimeout = d
	}
}

// New constructs a Store over the tenants bucket.
func New(kv jetstream.KeyValue, opts ...Option) *Store {
	s := &Store{
		kv:              kv,
		rollbackTimeout: defaultRollbackTimeout,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Store) rollbackDelete(ctx context.Context, key string, claimRev uint64) error {
	delCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.rollbackTimeout)
	defer cancel()
	if err := s.kv.Delete(delCtx, key, jetstream.LastRevision(claimRev)); err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
		return errs.From(err).Code(ErrCodeStore).Attr("key", key).Msg("rollback delete failed")
	}
	return nil
}

// Create stores a new tenant and its organization secondary index entry.
// Returns ErrCodeAlreadyExists if the tenant ID or organization is already registered.
func (s *Store) Create(ctx context.Context, config *identityv1.TenantConfig) (*identityv1.TenantRecord, error) {
	if err := protovalidate.Validate(config); err != nil {
		return nil, errs.From(err).Code(ErrCodeInvalidConfig).Msg("invalid tenant config")
	}
	tenantID := config.GetRef().GetTenant().GetId()

	orgKey := OrgIndexKey(config.GetIssuer(), config.GetOrganizationClaimValue())
	// Create secondary index first to claim the (issuer, org) pair.
	var claimRev uint64
	rev, err := s.kv.Create(ctx, orgKey, []byte(tenantID))
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			entry, getErr := s.kv.Get(ctx, orgKey)
			if getErr != nil {
				return nil, errs.From(getErr).Code(ErrCodeStore).Msg("read organization index")
			}
			existingTenantID := string(entry.Value())
			existingRec, getRecErr := s.Get(ctx, existingTenantID)
			if getRecErr != nil {
				return nil, getRecErr
			}
			hasValidRecord := existingRec != nil &&
				existingRec.GetConfig().GetIssuer() == config.GetIssuer() &&
				existingRec.GetConfig().GetOrganizationClaimValue() == config.GetOrganizationClaimValue()
			if hasValidRecord {
				return nil, errs.New().Code(ErrCodeAlreadyExists).
					Attr("issuer", config.GetIssuer()).
					Attr("organization", config.GetOrganizationClaimValue()).
					Msg("tenant with organization already exists")
			}
			if time.Since(entry.Created()) <= 2*s.rollbackTimeout {
				return nil, errs.New().Code(ErrCodeConflict).
					Attr("issuer", config.GetIssuer()).
					Attr("organization", config.GetOrganizationClaimValue()).
					Msg("concurrent tenant registration in progress")
			}
			// Orphaned index: the tenant it names has no matching record and the claim has expired. Take it over with CAS.
			var updateErr error
			claimRev, updateErr = s.kv.Update(ctx, orgKey, []byte(tenantID), entry.Revision())
			if updateErr != nil {
				return nil, errs.From(updateErr).Code(ErrCodeStore).Msg("take over organization index")
			}
		} else {
			return nil, errs.From(err).Code(ErrCodeStore).Msg("write organization index")
		}
	} else {
		claimRev = rev
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
		rbErr := s.rollbackDelete(ctx, orgKey, claimRev)
		retErr := errs.From(err).Code(ErrCodeStore).Attr("tenant", tenantID).Msg("encode tenant record")
		return nil, errors.Join(retErr, rbErr)
	}

	recCtx, recCancel := context.WithTimeout(context.WithoutCancel(ctx), s.rollbackTimeout)
	defer recCancel()

	if _, err := s.kv.Create(recCtx, tenantID, data); err != nil {
		rbErr := s.rollbackDelete(ctx, orgKey, claimRev)
		if errors.Is(err, jetstream.ErrKeyExists) {
			retErr := errs.New().Code(ErrCodeAlreadyExists).Attr("tenant", tenantID).Msg("tenant already exists")
			return nil, errors.Join(retErr, rbErr)
		}
		retErr := errs.From(err).Code(ErrCodeStore).Attr("tenant", tenantID).Msg("write tenant record")
		return nil, errors.Join(retErr, rbErr)
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
	rec, err := s.Get(ctx, tenantID)
	if err != nil || rec == nil {
		return rec, err
	}
	cfg := rec.GetConfig()
	if cfg == nil || cfg.GetIssuer() != issuer || cfg.GetOrganizationClaimValue() != orgClaimValue {
		return nil, nil
	}
	return rec, nil
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
			return nil, errs.From(err).Code(ErrCodeStore).Attr("tenant", tenantID).Msg("encode tenant record")
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
			return nil, errs.From(err).Code(ErrCodeStore).Attr("tenant", tenantID).Msg("write tenant record")
		}
		return next, nil
	}
	return nil, errs.New().Code(ErrCodeConflict).Attr("tenant", tenantID).Msg("tenant record write did not settle")
}
