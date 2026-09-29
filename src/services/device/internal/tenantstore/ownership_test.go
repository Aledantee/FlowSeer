package tenantstore_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/tenantstore"
)

var errInjectedKV = errors.New("injected key-value failure")

type faultTiming string

const (
	faultBefore faultTiming = "before"
	faultAfter  faultTiming = "after"
)

type mutation string

const (
	mutationCreate mutation = "create"
	mutationUpdate mutation = "update"
	mutationDelete mutation = "delete"
)

type faultSpec struct {
	mutation mutation
	key      string
	timing   faultTiming
}

type faultKV struct {
	jetstream.KeyValue

	mu    sync.Mutex
	fault faultSpec
	fired bool
}

func (kv *faultKV) trigger(op mutation, key string) faultTiming {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if kv.fired || kv.fault.mutation != op || kv.fault.key != key {
		return ""
	}
	kv.fired = true
	return kv.fault.timing
}

func (kv *faultKV) Create(ctx context.Context, key string, value []byte, opts ...jetstream.KVCreateOpt) (uint64, error) {
	timing := kv.trigger(mutationCreate, key)
	if timing == faultBefore {
		return 0, errInjectedKV
	}
	revision, err := kv.KeyValue.Create(ctx, key, value, opts...)
	if err == nil && timing == faultAfter {
		return 0, errInjectedKV
	}
	return revision, err
}

func (kv *faultKV) Update(ctx context.Context, key string, value []byte, revision uint64) (uint64, error) {
	timing := kv.trigger(mutationUpdate, key)
	if timing == faultBefore {
		return 0, errInjectedKV
	}
	newRevision, err := kv.KeyValue.Update(ctx, key, value, revision)
	if err == nil && timing == faultAfter {
		return 0, errInjectedKV
	}
	return newRevision, err
}

func (kv *faultKV) Delete(ctx context.Context, key string, opts ...jetstream.KVDeleteOpt) error {
	timing := kv.trigger(mutationDelete, key)
	if timing == faultBefore {
		return errInjectedKV
	}
	err := kv.KeyValue.Delete(ctx, key, opts...)
	if err == nil && timing == faultAfter {
		return errInjectedKV
	}
	return err
}

func TestTenantOwnershipProperties(t *testing.T) {
	ctx := context.Background()
	_, kv := newStoreWithKV(t)
	nextID := 100

	newIdentity := func() (string, string) {
		nextID++
		return fmt.Sprintf("0192e6a0-0000-7000-8000-%012d", nextID), fmt.Sprintf("org-property-%d", nextID)
	}

	t.Run("fault enumeration", func(t *testing.T) {
		for _, state := range []struct {
			name     string
			mutation mutation
			key      func(string, string) string
			setup    func(*testing.T, jetstream.KeyValue, string, string) []string
		}{
			{
				name:     "primary write",
				mutation: mutationCreate,
				key:      func(id, _ string) string { return id },
			},
			{
				name:     "index create",
				mutation: mutationCreate,
				key: func(_, org string) string {
					return tenantstore.OrgIndexKey(defaultIssuer, org)
				},
			},
			{
				name:     "orphan takeover",
				mutation: mutationUpdate,
				key: func(_, org string) string {
					return tenantstore.OrgIndexKey(defaultIssuer, org)
				},
				setup: func(t *testing.T, kv jetstream.KeyValue, _ string, org string) []string {
					t.Helper()
					ghostID := "0192e6a0-0000-7000-8000-000000000900"
					if _, err := kv.Put(ctx, tenantstore.OrgIndexKey(defaultIssuer, org), []byte(ghostID)); err != nil {
						t.Fatalf("seed orphaned index: %v", err)
					}
					return []string{ghostID}
				},
			},
			{
				name:     "rollback delete",
				mutation: mutationDelete,
				key:      func(id, _ string) string { return id },
				setup: func(t *testing.T, kv jetstream.KeyValue, id, org string) []string {
					t.Helper()
					ownerID := id[:24] + "1" + id[25:]
					if _, err := tenantstore.New(kv).Create(ctx, sampleTenantConfig(ownerID, org)); err != nil {
						t.Fatalf("seed organization owner: %v", err)
					}
					return []string{ownerID}
				},
			},
		} {
			for _, timing := range []faultTiming{faultBefore, faultAfter} {
				state := state
				timing := timing
				t.Run(state.name+"/"+string(timing), func(t *testing.T) {
					id, org := newIdentity()
					ids := []string{id}
					if state.setup != nil {
						ids = append(ids, state.setup(t, kv, id, org)...)
					}
					wrapped := &faultKV{
						KeyValue: kv,
						fault: faultSpec{
							mutation: state.mutation,
							key:      state.key(id, org),
							timing:   timing,
						},
					}
					s := tenantstore.New(wrapped)
					_, err := s.Create(ctx, sampleTenantConfig(id, org))
					assertCreateOutcome(t, s, id, org, err)
					assertOwnershipViews(t, s, kv, org, ids)
				})
			}
		}
	})

	t.Run("retry with own orphaned index", func(t *testing.T) {
		id, org := newIdentity()
		orgKey := tenantstore.OrgIndexKey(defaultIssuer, org)
		if _, err := kv.Create(ctx, orgKey, []byte(id)); err != nil {
			t.Fatalf("seed own index: %v", err)
		}
		s := tenantstore.New(kv)
		if _, err := s.Create(ctx, sampleTenantConfig(id, org)); err != nil {
			t.Fatalf("retry Create: %v", err)
		}
		assertOwnershipViews(t, s, kv, org, []string{id})
	})

	for _, initial := range []string{"empty", "orphaned index"} {
		initial := initial
		t.Run("concurrent creates from "+initial, func(t *testing.T) {
			const concurrency = 8
			_, org := newIdentity()
			ids := make([]string, concurrency)
			for i := range ids {
				id, _ := newIdentity()
				ids[i] = id
			}

			var store *tenantstore.Store
			if initial == "orphaned index" {
				ghostID := "0192e6a0-0000-7000-8000-000000000902"
				orgKey := tenantstore.OrgIndexKey(defaultIssuer, org)
				if _, err := kv.Create(ctx, orgKey, []byte(ghostID)); err != nil {
					t.Fatalf("seed orphaned index: %v", err)
				}
				store = tenantstore.New(newOrgReadBarrierKV(kv, orgKey, ghostID, concurrency))
			} else {
				store = tenantstore.New(kv)
			}

			start := make(chan struct{})
			results := make(chan createResult, concurrency)
			var wg sync.WaitGroup
			for _, id := range ids {
				wg.Add(1)
				go func(id string) {
					defer wg.Done()
					<-start
					_, err := store.Create(ctx, sampleTenantConfig(id, org))
					results <- createResult{id: id, err: err}
				}(id)
			}
			close(start)
			wg.Wait()
			close(results)

			successes := 0
			for result := range results {
				if result.err == nil {
					successes++
				} else if code, _ := errs.CodeOf(result.err); code != tenantstore.ErrCodeAlreadyExists {
					t.Errorf("Create(%s) error code = %q, want %q: %v", result.id, code, tenantstore.ErrCodeAlreadyExists, result.err)
				}
				assertCreateOutcome(t, store, result.id, org, result.err)
			}
			if successes != 1 {
				t.Fatalf("successful Creates = %d, want 1", successes)
			}
			assertOwnershipViews(t, store, kv, org, ids)
		})
	}

	t.Run("rollback preserves newer primary revision", func(t *testing.T) {
		id, org := newIdentity()
		ownerID, _ := newIdentity()
		if _, err := tenantstore.New(kv).Create(ctx, sampleTenantConfig(ownerID, org)); err != nil {
			t.Fatalf("seed organization owner: %v", err)
		}
		replacement := sampleTenantRecord(sampleTenantConfig(id, org))
		data, err := proto.Marshal(replacement)
		if err != nil {
			t.Fatalf("marshal replacement: %v", err)
		}
		wrapped := &replaceBeforeDeleteKV{KeyValue: kv, key: id, value: data}
		s := tenantstore.New(wrapped)
		if _, err := s.Create(ctx, sampleTenantConfig(id, org)); err == nil {
			t.Fatal("Create with owned organization succeeded, want error")
		}
		entry, err := kv.Get(ctx, id)
		if err != nil {
			t.Fatalf("newer primary record was deleted: %v", err)
		}
		if !proto.Equal(replacement, decodeTenantRecord(t, entry.Value())) {
			t.Fatal("newer primary record changed during rollback")
		}
		assertOwnershipViews(t, s, kv, org, []string{id, ownerID})
	})
}

type createResult struct {
	id  string
	err error
}

type orgReadBarrierKV struct {
	jetstream.KeyValue
	orgKey       string
	ghostID      string
	participants int
	release      chan struct{}

	mu       sync.Mutex
	arrivals int
}

func newOrgReadBarrierKV(kv jetstream.KeyValue, orgKey, ghostID string, participants int) *orgReadBarrierKV {
	return &orgReadBarrierKV{
		KeyValue:     kv,
		orgKey:       orgKey,
		ghostID:      ghostID,
		participants: participants,
		release:      make(chan struct{}),
	}
}

func (kv *orgReadBarrierKV) Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error) {
	entry, err := kv.KeyValue.Get(ctx, key)
	if err != nil || key != kv.orgKey || string(entry.Value()) != kv.ghostID {
		return entry, err
	}

	kv.mu.Lock()
	kv.arrivals++
	if kv.arrivals == kv.participants {
		close(kv.release)
	}
	kv.mu.Unlock()

	select {
	case <-kv.release:
		return entry, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type replaceBeforeDeleteKV struct {
	jetstream.KeyValue
	key   string
	value []byte
	once  sync.Once
	err   error
}

func (kv *replaceBeforeDeleteKV) Delete(ctx context.Context, key string, opts ...jetstream.KVDeleteOpt) error {
	if key == kv.key {
		kv.once.Do(func() {
			_, kv.err = kv.Put(ctx, key, kv.value)
		})
		if kv.err != nil {
			return kv.err
		}
	}
	return kv.KeyValue.Delete(ctx, key, opts...)
}

func sampleTenantRecord(config *identityv1.TenantConfig) *identityv1.TenantRecord {
	active := identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE
	return identityv1.TenantRecord_builder{
		Config: config,
		State: identityv1.TenantState_builder{
			Ref:       config.GetRef(),
			Lifecycle: &active,
			CreatedAt: timestamppb.New(time.Unix(1, 0)),
		}.Build(),
	}.Build()
}

func assertCreateOutcome(t *testing.T, s *tenantstore.Store, id, org string, createErr error) {
	t.Helper()
	rec, revision, err := s.GetWithRevision(context.Background(), id)
	if err != nil {
		t.Fatalf("GetWithRevision(%s): %v", id, err)
	}
	lookup, err := s.LookupByOrg(context.Background(), defaultIssuer, org)
	if err != nil {
		t.Fatalf("LookupByOrg(%s): %v", id, err)
	}
	if createErr == nil {
		if tenantID(rec) != id || revision == 0 {
			t.Fatalf("successful Create(%s) visibility = (%q, %d), want record and revision", id, tenantID(rec), revision)
		}
		if tenantID(lookup) != id {
			t.Fatalf("successful Create(%s) lookup = %q, want %q", id, tenantID(lookup), id)
		}
		return
	}
	if rec != nil || revision != 0 {
		t.Fatalf("failed Create(%s) visibility = (%v, %d), want (nil, 0): %v", id, rec, revision, createErr)
	}
}

func assertOwnershipViews(t *testing.T, s *tenantstore.Store, kv jetstream.KeyValue, org string, ids []string) {
	t.Helper()
	ctx := context.Background()
	orgEntry, err := kv.Get(ctx, tenantstore.OrgIndexKey(defaultIssuer, org))
	owner := ""
	if err == nil {
		owner = string(orgEntry.Value())
	} else if !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("read organization index: %v", err)
	}

	visible := make([]string, 0, 1)
	for _, id := range ids {
		rec, err := s.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		revisionRec, revision, err := s.GetWithRevision(ctx, id)
		if err != nil {
			t.Fatalf("GetWithRevision(%s): %v", id, err)
		}
		if !proto.Equal(rec, revisionRec) {
			t.Fatalf("Get and GetWithRevision disagree for %s", id)
		}
		if rec == nil {
			if revision != 0 {
				t.Fatalf("hidden tenant %s revision = %d, want 0", id, revision)
			}
			continue
		}
		if revision == 0 {
			t.Fatalf("visible tenant %s revision = 0", id)
		}
		visible = append(visible, id)
	}
	if len(visible) > 1 {
		t.Fatalf("visible tenants for (%s, %s) = %v, want at most one", defaultIssuer, org, visible)
	}
	if len(visible) == 1 && visible[0] != owner {
		t.Fatalf("visible tenant = %s, organization index owner = %s", visible[0], owner)
	}

	listed, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	listedIDs := make([]string, 0, len(listed))
	for _, rec := range listed {
		id := tenantID(rec)
		if slices.Contains(ids, id) {
			listedIDs = append(listedIDs, id)
		}
	}
	slices.Sort(visible)
	slices.Sort(listedIDs)
	if !slices.Equal(listedIDs, visible) {
		t.Fatalf("List tenants = %v, visible tenants = %v", listedIDs, visible)
	}

	lookup, err := s.LookupByOrg(ctx, defaultIssuer, org)
	if err != nil {
		t.Fatalf("LookupByOrg: %v", err)
	}
	lookupID := tenantID(lookup)
	if len(visible) == 0 && lookupID != "" {
		t.Fatalf("LookupByOrg tenant = %s, want none", lookupID)
	}
	if len(visible) == 1 && lookupID != visible[0] {
		t.Fatalf("LookupByOrg tenant = %s, want %s", lookupID, visible[0])
	}
}

func tenantID(rec *identityv1.TenantRecord) string {
	return rec.GetConfig().GetRef().GetTenant().GetId()
}

func decodeTenantRecord(t *testing.T, data []byte) *identityv1.TenantRecord {
	t.Helper()
	rec := &identityv1.TenantRecord{}
	if err := proto.Unmarshal(data, rec); err != nil {
		t.Fatalf("unmarshal tenant record: %v", err)
	}
	return rec
}
