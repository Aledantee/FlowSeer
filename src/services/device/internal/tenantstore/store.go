// Package tenantstore is central's durable record of every tenant in the
// tenants key-value bucket. A tenant's record and its organization index are
// written together by one atomic batch. A record counts as committed only while
// its organization index names it.
package tenantstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"buf.build/go/protovalidate"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/synadia-io/orbit.go/jetstreamext"
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
	// ErrCodeNotFound indicates the tenant does not have a committed record.
	ErrCodeNotFound = errs.NewCode("tenantstore/not-found")
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
	kv      jetstream.KeyValue
	js      jetstream.JetStream
	subject string
	lastMsg func(context.Context, string) (*jetstream.RawStreamMsg, error)
	publish func(context.Context, []*nats.Msg) (*jetstreamext.BatchAck, error)
}

// New constructs a Store over bucket after confirming its stream accepts
// atomic publish batches. The returned store is safe for concurrent use.
func New(ctx context.Context, js jetstream.JetStream, bucket string) (*Store, error) {
	kv, err := js.KeyValue(ctx, bucket)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStore).Attr("bucket", bucket).Msg("open tenant bucket")
	}
	stream, err := js.Stream(ctx, "KV_"+bucket)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStore).Attr("bucket", bucket).Msg("open tenant bucket stream")
	}
	info := stream.CachedInfo()
	if info == nil || !info.Config.AllowAtomicPublish {
		return nil, errs.New().Code(ErrCodeStore).Attr("bucket", bucket).Msg("tenant bucket stream does not allow atomic publish")
	}

	s := &Store{
		kv:      kv,
		js:      js,
		subject: "$KV." + bucket + ".",
		lastMsg: stream.GetLastMsgForSubject,
	}
	s.publish = func(ctx context.Context, messages []*nats.Msg) (*jetstreamext.BatchAck, error) {
		return jetstreamext.PublishMsgBatch(ctx, js, messages, jetstreamext.BatchFlowControl{AckFirst: false})
	}
	return s, nil
}

func (s *Store) getRawWithRevision(ctx context.Context, tenantID string) (*identityv1.TenantRecord, uint64, error) {
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

// Create stores a new tenant and its organization index in one atomic batch. A
// retry with an equal committed configuration returns the stored record. Any
// other stored configuration or claimed organization returns
// ErrCodeAlreadyExists. ErrCodeStore means the outcome is unknown until the
// same call is retried.
func (s *Store) Create(ctx context.Context, config *identityv1.TenantConfig) (*identityv1.TenantRecord, error) {
	if err := protovalidate.Validate(config); err != nil {
		return nil, errs.From(err).Code(ErrCodeInvalidConfig).Msg("invalid tenant config")
	}
	tenantID := config.GetRef().GetTenant().GetId()

	active := identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE
	state := identityv1.TenantState_builder{
		Ref:       config.GetRef(),
		Lifecycle: &active,
		CreatedAt: timestamppb.Now(),
	}.Build()

	newRecord := identityv1.TenantRecord_builder{
		Config: config,
		State:  state,
	}.Build()

	data, err := proto.Marshal(newRecord)
	if err != nil {
		return nil, errs.From(err).Code(ErrCodeStore).Attr("tenant", tenantID).Msg("encode tenant record")
	}

	orgKey := OrgIndexKey(config.GetIssuer(), config.GetOrganizationClaimValue())
	for attempt := 0; attempt < casRetries; attempt++ {
		var record *identityv1.TenantRecord
		recordData := data

		recordMsg, recordSeq, err := s.readLast(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		if recordMsg != nil && !isMarker(recordMsg) {
			record = &identityv1.TenantRecord{}
			if err := proto.Unmarshal(recordMsg.Data, record); err != nil {
				return nil, errs.From(err).Code(ErrCodeDecode).Attr("tenant", tenantID).Msg("decode tenant record")
			}
			if !proto.Equal(record.GetConfig(), config) {
				return nil, errs.New().Code(ErrCodeAlreadyExists).Attr("tenant", tenantID).Msg("tenant already exists")
			}
			recordData = recordMsg.Data
			recordSeq = recordMsg.Sequence
		}

		indexMsg, indexSeq, err := s.readLast(ctx, orgKey)
		if err != nil {
			return nil, err
		}
		if indexMsg != nil && !isMarker(indexMsg) {
			if string(indexMsg.Data) == tenantID {
				if record == nil {
					recordMsg, _, err = s.readLast(ctx, tenantID)
					if err != nil {
						return nil, err
					}
					if recordMsg != nil && !isMarker(recordMsg) {
						record = &identityv1.TenantRecord{}
						if err := proto.Unmarshal(recordMsg.Data, record); err != nil {
							return nil, errs.From(err).Code(ErrCodeDecode).Attr("tenant", tenantID).Msg("decode tenant record")
						}
					}
				}
				if record != nil && proto.Equal(record.GetConfig(), config) {
					return record, nil
				}
			}
			return nil, errs.New().Code(ErrCodeAlreadyExists).
				Attr("issuer", config.GetIssuer()).
				Attr("organization", config.GetOrganizationClaimValue()).
				Msg("tenant with organization already exists")
		}

		if record == nil {
			record = newRecord
		}

		messages := []*nats.Msg{
			batchMessage(s.subject+tenantID, recordData, recordSeq),
			batchMessage(s.subject+orgKey, []byte(tenantID), indexSeq),
		}
		if _, err := s.publish(ctx, messages); err != nil {
			if isCASConflict(err) {
				continue
			}
			return nil, errs.From(err).Code(ErrCodeStore).Attr("tenant", tenantID).Msg("publish tenant claim batch")
		}
		return record, nil
	}
	return nil, errs.New().Code(ErrCodeConflict).Attr("tenant", tenantID).Msg("tenant claim did not settle")
}

func (s *Store) readLast(ctx context.Context, key string) (*jetstream.RawStreamMsg, uint64, error) {
	msg, err := s.lastMsg(ctx, s.subject+key)
	if errors.Is(err, jetstream.ErrMsgNotFound) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, errs.From(err).Code(ErrCodeStore).Attr("key", key).Msg("read tenant store subject")
	}
	return msg, msg.Sequence, nil
}

func batchMessage(subject string, data []byte, expected uint64) *nats.Msg {
	msg := nats.NewMsg(subject)
	msg.Data = data
	msg.Header.Set(jetstream.ExpectedLastSubjSeqHeader, strconv.FormatUint(expected, 10))
	return msg
}

func isMarker(msg *jetstream.RawStreamMsg) bool {
	operation := msg.Header.Get("KV-Operation")
	if operation == "DEL" || operation == "PURGE" {
		return true
	}
	switch msg.Header.Get(jetstream.MarkerReasonHeader) {
	case "MaxAge", "Purge", "Remove":
		return true
	default:
		return false
	}
}

func isCASConflict(err error) bool {
	var apiErr *jetstream.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence ||
		apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequenceConstant
}

// Get returns the committed tenant record for tenantID, or nil if no record is
// named by its organization index.
func (s *Store) Get(ctx context.Context, tenantID string) (*identityv1.TenantRecord, error) {
	rec, _, err := s.GetWithRevision(ctx, tenantID)
	return rec, err
}

// GetWithRevision returns the committed tenant record for tenantID and its KV
// revision, or nil and revision 0 if the record is absent or uncommitted.
func (s *Store) GetWithRevision(ctx context.Context, tenantID string) (*identityv1.TenantRecord, uint64, error) {
	rec, revision, err := s.getRawWithRevision(ctx, tenantID)
	if err != nil {
		return nil, 0, err
	}
	if rec == nil {
		return nil, 0, nil
	}
	cfg := rec.GetConfig()
	if cfg == nil || cfg.GetRef().GetTenant().GetId() != tenantID {
		return nil, 0, nil
	}
	entry, err := s.kv.Get(ctx, OrgIndexKey(cfg.GetIssuer(), cfg.GetOrganizationClaimValue()))
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, errs.From(err).Code(ErrCodeStore).Attr("tenant", tenantID).Msg("read organization index")
	}
	if string(entry.Value()) != tenantID {
		return nil, 0, nil
	}
	return rec, revision, nil
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

// List returns committed tenant records in ascending order by tenant ID.
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

// Mutate runs fn against a committed tenant record under compare-and-set,
// retrying on a revision conflict. It returns ErrCodeNotFound without calling fn
// when tenantID has no committed record. fn may change tenant state and mutable
// configuration, but not the tenant ID, issuer, or organization claim value.
// Returning ErrSkip stores nothing.
func (s *Store) Mutate(ctx context.Context, tenantID string, fn func(current *identityv1.TenantRecord) (*identityv1.TenantRecord, error)) (*identityv1.TenantRecord, error) {
	for attempt := 0; attempt < casRetries; attempt++ {
		current, revision, err := s.GetWithRevision(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		if current == nil {
			return nil, errs.New().Code(ErrCodeNotFound).Attr("tenant", tenantID).Msg("tenant not found")
		}
		next, err := fn(current)
		if err != nil {
			if errors.Is(err, ErrSkip) {
				return current, nil
			}
			return nil, err
		}
		currentCfg := current.GetConfig()
		nextCfg := next.GetConfig()
		if nextCfg.GetRef().GetTenant().GetId() != tenantID ||
			currentCfg.GetIssuer() != nextCfg.GetIssuer() ||
			currentCfg.GetOrganizationClaimValue() != nextCfg.GetOrganizationClaimValue() {
			return nil, errs.New().Code(ErrCodeInvalidConfig).Attr("tenant", tenantID).Msg("tenant organization binding cannot change")
		}
		data, err := proto.Marshal(next)
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeStore).Attr("tenant", tenantID).Msg("encode tenant record")
		}
		_, err = s.kv.Update(ctx, tenantID, data, revision)
		if errors.Is(err, jetstream.ErrKeyRevisionMismatch) {
			continue
		}
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeStore).Attr("tenant", tenantID).Msg("write tenant record")
		}
		return next, nil
	}
	return nil, errs.New().Code(ErrCodeConflict).Attr("tenant", tenantID).Msg("tenant record write did not settle")
}
