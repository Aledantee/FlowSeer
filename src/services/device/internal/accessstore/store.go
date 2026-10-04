// Package accessstore persists tenant membership, roles, and partner links in
// one key-value bucket. Member grants share the member's key so removal deletes
// the enrollment and its grants together.
package accessstore

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
)

var (
	// ErrCodeStore is a retryable bucket read or transport failure.
	ErrCodeStore = errs.NewCode("accessstore/store")
	// ErrCodeConflict is a conditional write that exhausted its retry budget.
	ErrCodeConflict = errs.NewCode("accessstore/conflict")
	// ErrCodeDecode is a record that cannot be encoded or decoded.
	ErrCodeDecode = errs.NewCode("accessstore/decode")
	// ErrCodeNotFound is a mutation of an absent record.
	ErrCodeNotFound = errs.NewCode("accessstore/not-found")
	// ErrCodeRoleAssigned is a role deletion refused because a member names it.
	ErrCodeRoleAssigned = errs.NewCode("accessstore/role-assigned")
)

const casRetries = 8

// Store holds access records under <tenant>.<kind>.<id> keys. It is safe for
// concurrent use when its bucket and clock are. The zero value is not usable.
// Callers validate record contents before writing them.
// Bucket failures return ErrCodeStore and are retryable. Encoding failures
// return ErrCodeDecode, and exhausted CAS retries return ErrCodeConflict.
// An ended caller context returns its unwrapped error.
type Store struct {
	kv  jetstream.KeyValue
	now func() time.Time
}

// New uses kv for every record kind and now for full-payload expiry checks.
// Both arguments must be non-nil and safe for concurrent use.
func New(kv jetstream.KeyValue, now func() time.Time) *Store {
	return &Store{kv: kv, now: now}
}

func recordKey(tenantID, kind, id string) (string, error) {
	if err := tenant.Validate(tenantID); err != nil {
		return "", errs.From(err).Code(ErrCodeStore).Attr("tenant", tenantID).Msg("validate tenant")
	}
	return tenantID + "." + kind + "." + id, nil
}

func principalID(operator *identityv1.OperatorRef) string {
	return authn.ComputePrincipalID(operator.GetIssuer(), operator.GetSubject())
}

// Member returns the enrollment and grants of operator, or nil when absent.
func (s *Store) Member(ctx context.Context, tenantID string, operator *identityv1.OperatorRef) (*identityv1.Member, error) {
	key, err := recordKey(tenantID, "member", principalID(operator))
	if err != nil {
		return nil, err
	}
	rec, _, err := readRecord(ctx, s, key, func() *identityv1.Member { return &identityv1.Member{} })
	return rec, err
}

// Role returns the tenant's role, or nil when absent.
func (s *Store) Role(ctx context.Context, tenantID string, ref *identityv1.RoleGlobalRef) (*identityv1.Role, error) {
	key, err := recordKey(tenantID, "role", ref.GetRole().GetId())
	if err != nil {
		return nil, err
	}
	rec, _, err := readRecord(ctx, s, key, func() *identityv1.Role { return &identityv1.Role{} })
	return rec, err
}

// Partner returns the customer's provider link, or nil when absent.
func (s *Store) Partner(ctx context.Context, tenantID string, provider *identityv1.TenantGlobalRef) (*identityv1.Partner, error) {
	key, err := recordKey(tenantID, "partner", provider.GetTenant().GetId())
	if err != nil {
		return nil, err
	}
	rec, _, err := readRecord(ctx, s, key, func() *identityv1.Partner { return &identityv1.Partner{} })
	return rec, err
}

// CreateMember enrolls a member only when absent. A retry returns the stored
// record, preserving its enrollment time and grants.
func (s *Store) CreateMember(ctx context.Context, tenantID string, member *identityv1.Member) (*identityv1.Member, error) {
	key, err := recordKey(tenantID, "member", principalID(member.GetOperator()))
	if err != nil {
		return nil, err
	}
	return createRecord(ctx, s, key, member, func() *identityv1.Member { return &identityv1.Member{} })
}

// CreateRole stores a role only when its id is absent. An existing id returns
// its stored role without replacing its name or relations.
func (s *Store) CreateRole(ctx context.Context, tenantID string, role *identityv1.Role) (*identityv1.Role, error) {
	key, err := recordKey(tenantID, "role", role.GetRef().GetRole().GetId())
	if err != nil {
		return nil, err
	}
	return createRecord(ctx, s, key, role, func() *identityv1.Role { return &identityv1.Role{} })
}

// CreatePartner connects a provider only when absent. A retry returns the
// stored link without replacing its connection time or relations.
func (s *Store) CreatePartner(ctx context.Context, tenantID string, partner *identityv1.Partner) (*identityv1.Partner, error) {
	key, err := recordKey(tenantID, "partner", partner.GetTenant().GetTenant().GetId())
	if err != nil {
		return nil, err
	}
	return createRecord(ctx, s, key, partner, func() *identityv1.Partner { return &identityv1.Partner{} })
}

// MutateMember applies fn under compare-and-set and returns the stored member.
// It returns ErrCodeNotFound without calling fn for an absent enrollment. fn
// may run again on a conflict and must preserve the operator's identity.
func (s *Store) MutateMember(ctx context.Context, tenantID string, operator *identityv1.OperatorRef, fn func(*identityv1.Member) error) (*identityv1.Member, error) {
	key, err := recordKey(tenantID, "member", principalID(operator))
	if err != nil {
		return nil, err
	}
	return mutateRecord(ctx, s, key, func() *identityv1.Member { return &identityv1.Member{} }, fn)
}

// UpdatePartner replaces the provider's relations while preserving the stored
// connection metadata. An absent link returns ErrCodeNotFound.
func (s *Store) UpdatePartner(ctx context.Context, tenantID string, provider *identityv1.TenantGlobalRef, relations []identityv1.TenantRelation) (*identityv1.Partner, error) {
	key, err := recordKey(tenantID, "partner", provider.GetTenant().GetId())
	if err != nil {
		return nil, err
	}
	return mutateRecord(ctx, s, key, func() *identityv1.Partner { return &identityv1.Partner{} }, func(current *identityv1.Partner) error {
		current.SetRelations(relations)
		return nil
	})
}

// DeleteMember removes the enrollment and every grant it holds under
// compare-and-set. It reports false without error when the member is absent.
func (s *Store) DeleteMember(ctx context.Context, tenantID string, operator *identityv1.OperatorRef) (bool, error) {
	key, err := recordKey(tenantID, "member", principalID(operator))
	if err != nil {
		return false, err
	}
	return s.deleteRecord(ctx, key)
}

// DeleteRole removes an unassigned role under compare-and-set and reports
// whether it existed. A member naming the role causes ErrCodeRoleAssigned.
// Assignments racing the scan may retain a ref to a deleted role, which grants
// nothing because projection requires a stored role.
func (s *Store) DeleteRole(ctx context.Context, tenantID string, ref *identityv1.RoleGlobalRef) (bool, error) {
	key, err := recordKey(tenantID, "role", ref.GetRole().GetId())
	if err != nil {
		return false, err
	}
	role, err := s.Role(ctx, tenantID, ref)
	if err != nil || role == nil {
		return false, err
	}
	members, err := s.ListMembers(ctx, tenantID)
	if err != nil {
		return false, err
	}
	for _, member := range members {
		for _, assigned := range member.GetRoles() {
			if assigned.GetRole().GetId() == ref.GetRole().GetId() {
				return false, errs.New().Code(ErrCodeRoleAssigned).Attr("key", key).Msg("role is assigned to a member")
			}
		}
	}
	return s.deleteRecord(ctx, key)
}

// DeletePartner removes a provider link under compare-and-set. It reports
// false without error when the link is absent.
func (s *Store) DeletePartner(ctx context.Context, tenantID string, provider *identityv1.TenantGlobalRef) (bool, error) {
	key, err := recordKey(tenantID, "partner", provider.GetTenant().GetId())
	if err != nil {
		return false, err
	}
	return s.deleteRecord(ctx, key)
}

// ListMembers returns only this tenant's members, ordered by principal id.
// An empty tenant returns nil. Records removed during the scan are skipped.
func (s *Store) ListMembers(ctx context.Context, tenantID string) ([]*identityv1.Member, error) {
	prefix, err := recordKey(tenantID, "member", "")
	if err != nil {
		return nil, err
	}
	return listRecords(ctx, s, prefix, func() *identityv1.Member { return &identityv1.Member{} })
}

// ListRoles returns only this tenant's roles, ordered by role id.
// An empty tenant returns nil. Records removed during the scan are skipped.
func (s *Store) ListRoles(ctx context.Context, tenantID string) ([]*identityv1.Role, error) {
	prefix, err := recordKey(tenantID, "role", "")
	if err != nil {
		return nil, err
	}
	return listRecords(ctx, s, prefix, func() *identityv1.Role { return &identityv1.Role{} })
}

// ListPartners returns only this tenant's links, ordered by provider tenant id.
// An empty tenant returns nil. Records removed during the scan are skipped.
func (s *Store) ListPartners(ctx context.Context, tenantID string) ([]*identityv1.Partner, error) {
	prefix, err := recordKey(tenantID, "partner", "")
	if err != nil {
		return nil, err
	}
	return listRecords(ctx, s, prefix, func() *identityv1.Partner { return &identityv1.Partner{} })
}

// TenantIDs returns the distinct tenant ids holding any live access record,
// in ascending order. An empty bucket returns nil.
func (s *Store) TenantIDs(ctx context.Context) ([]string, error) {
	keys, err := s.keys(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, key := range keys {
		parts := strings.SplitN(key, ".", 3)
		if len(parts) != 3 || parts[2] == "" || tenant.Validate(parts[0]) != nil {
			continue
		}
		switch parts[1] {
		case "member", "role", "partner":
			ids = append(ids, parts[0])
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

// FullPayloadActive reports whether operator has a grant expiring strictly
// after now. An absent enrollment or grant returns false without error.
func (s *Store) FullPayloadActive(ctx context.Context, tenantID string, operator *identityv1.OperatorRef) (bool, error) {
	member, err := s.Member(ctx, tenantID, operator)
	if err != nil {
		return false, err
	}
	expires := member.GetFullPayload().GetExpiresAt()
	return expires != nil && expires.AsTime().After(s.now()), nil
}

func readRecord[M proto.Message](ctx context.Context, s *Store, key string, empty func() M) (M, uint64, error) {
	var zero M
	entry, err := s.kv.Get(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return zero, 0, nil
	}
	if err != nil {
		return zero, 0, storeError(ctx, err, key, "read access record")
	}
	rec := empty()
	if err := proto.Unmarshal(entry.Value(), rec); err != nil {
		return zero, 0, errs.From(err).Code(ErrCodeDecode).Attr("key", key).Msg("decode access record")
	}
	return rec, entry.Revision(), nil
}

func createRecord[M proto.Message](ctx context.Context, s *Store, key string, proposed M, empty func() M) (M, error) {
	var zero M
	for range casRetries {
		current, revision, err := readRecord(ctx, s, key, empty)
		if err != nil {
			return zero, err
		}
		if revision != 0 {
			return current, nil
		}
		data, err := encodeRecord(key, proposed)
		if err != nil {
			return zero, err
		}
		_, err = s.kv.Create(ctx, key, data)
		if errors.Is(err, jetstream.ErrKeyExists) || errors.Is(err, jetstream.ErrKeyRevisionMismatch) {
			continue
		}
		if err != nil {
			return zero, storeError(ctx, err, key, "create access record")
		}
		return proposed, nil
	}
	return zero, conflictError(key)
}

func mutateRecord[M proto.Message](ctx context.Context, s *Store, key string, empty func() M, fn func(M) error) (M, error) {
	var zero M
	for range casRetries {
		current, revision, err := readRecord(ctx, s, key, empty)
		if err != nil {
			return zero, err
		}
		if revision == 0 {
			return zero, errs.New().Code(ErrCodeNotFound).Attr("key", key).Msg("access record is absent")
		}
		if err := fn(current); err != nil {
			return zero, err
		}
		data, err := encodeRecord(key, current)
		if err != nil {
			return zero, err
		}
		_, err = s.kv.Update(ctx, key, data, revision)
		if errors.Is(err, jetstream.ErrKeyRevisionMismatch) {
			continue
		}
		if err != nil {
			return zero, storeError(ctx, err, key, "update access record")
		}
		return current, nil
	}
	return zero, conflictError(key)
}

func (s *Store) deleteRecord(ctx context.Context, key string) (bool, error) {
	for range casRetries {
		entry, err := s.kv.Get(ctx, key)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return false, nil
		}
		if err != nil {
			return false, storeError(ctx, err, key, "read access record for deletion")
		}
		err = s.kv.Delete(ctx, key, jetstream.LastRevision(entry.Revision()))
		if errors.Is(err, jetstream.ErrKeyRevisionMismatch) {
			continue
		}
		if err != nil {
			return false, storeError(ctx, err, key, "delete access record")
		}
		return true, nil
	}
	return false, conflictError(key)
}

func (s *Store) keys(ctx context.Context) ([]string, error) {
	keys, err := s.kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return nil, nil
	}
	if err != nil {
		return nil, storeError(ctx, err, "", "list access records")
	}
	slices.Sort(keys)
	return keys, nil
}

func listRecords[M proto.Message](ctx context.Context, s *Store, prefix string, empty func() M) ([]M, error) {
	keys, err := s.keys(ctx)
	if err != nil {
		return nil, err
	}
	var records []M
	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rec, revision, err := readRecord(ctx, s, key, empty)
		if err != nil {
			return nil, err
		}
		if revision != 0 {
			records = append(records, rec)
		}
	}
	return records, nil
}

func encodeRecord(key string, record proto.Message) ([]byte, error) {
	data, err := proto.Marshal(record)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeDecode).Attr("key", key).Msg("encode access record")
	}
	return data, nil
}

func storeError(ctx context.Context, err error, key, message string) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return errs.From(err).Code(ErrCodeStore).Retryable().Attr("key", key).Msg(message)
}

func conflictError(key string) error {
	return errs.New().Code(ErrCodeConflict).Attr("key", key).Msg("access record write did not settle")
}
