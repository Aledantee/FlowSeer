package tenantstore_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/tenantstore"
)

func newStore(t *testing.T) *tenantstore.Store {
	t.Helper()
	s, _ := newStoreWithKV(t)
	return s
}

func newStoreWithKV(t *testing.T) (*tenantstore.Store, jetstream.KeyValue) {
	t.Helper()
	hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
		StateDir:    t.TempDir(),
		FsyncPolicy: service.BusFsyncPeriodic,
		ListenPort:  0,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)
	kv, err := hub.JetStream().KeyValue(context.Background(), edgebus.TenantBucket)
	if err != nil {
		t.Fatalf("bucket: %v", err)
	}
	return tenantstore.New(kv), kv
}

func tenantRef(id string) *identityv1.TenantGlobalRef {
	return identityv1.TenantGlobalRef_builder{
		Tenant: identityv1.TenantLocalRef_builder{
			Id: proto.String(id),
		}.Build(),
	}.Build()
}

const defaultIssuer = "https://idp.example.test"

func sampleTenantConfig(id, org string) *identityv1.TenantConfig {
	return sampleTenantConfigWithIssuer(id, defaultIssuer, org)
}

func sampleTenantConfigWithIssuer(id, issuer, org string) *identityv1.TenantConfig {
	return identityv1.TenantConfig_builder{
		Ref:                    tenantRef(id),
		Issuer:                 proto.String(issuer),
		OrganizationClaimName:  proto.String("org_id"),
		OrganizationClaimValue: proto.String(org),
		Name:                   proto.String("Test Tenant"),
		Description:            proto.String("Tenant for unit test"),
	}.Build()
}

func TestCreateAndGetTenant(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const id = "0192e6a0-0000-7000-8000-000000000001"
	cfg := sampleTenantConfig(id, "org-alpha")

	rec, err := s.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec == nil {
		t.Fatal("Create returned nil record")
	}
	if got := rec.GetConfig().GetRef().GetTenant().GetId(); got != id {
		t.Fatalf("record tenant id = %q, want %q", got, id)
	}
	if rec.GetState().GetLifecycle() != identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE {
		t.Fatalf("lifecycle = %v, want ACTIVE", rec.GetState().GetLifecycle())
	}
	if rec.GetState().GetCreatedAt() == nil {
		t.Fatal("created_at is nil")
	}

	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil record")
	}
	if got.GetConfig().GetRef().GetTenant().GetId() != id {
		t.Fatalf("got id %q, want %q", got.GetConfig().GetRef().GetTenant().GetId(), id)
	}

	absent, err := s.Get(ctx, "0192e6a0-0000-7000-8000-000000000099")
	if err != nil {
		t.Fatalf("Get absent: %v", err)
	}
	if absent != nil {
		t.Fatalf("expected nil for absent tenant, got %v", absent)
	}
}

func TestLookupByOrg(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const (
		id1     = "0192e6a0-0000-7000-8000-000000000002"
		issuer1 = "https://idp.example.test"
		org1    = "org-beta"
		id2     = "0192e6a0-0000-7000-8000-000000000003"
		issuer2 = "https://other.example.test"
		org2    = "org-gamma"
	)
	cfg1 := sampleTenantConfigWithIssuer(id1, issuer1, org1)
	if _, err := s.Create(ctx, cfg1); err != nil {
		t.Fatalf("Create cfg1: %v", err)
	}
	cfg2 := sampleTenantConfigWithIssuer(id2, issuer2, org2)
	if _, err := s.Create(ctx, cfg2); err != nil {
		t.Fatalf("Create cfg2: %v", err)
	}

	found1, err := s.LookupByOrg(ctx, issuer1, org1)
	if err != nil {
		t.Fatalf("LookupByOrg 1: %v", err)
	}
	if found1 == nil || found1.GetConfig().GetRef().GetTenant().GetId() != id1 {
		t.Fatalf("got tenant ID %v, want %q", found1, id1)
	}

	found2, err := s.LookupByOrg(ctx, issuer2, org2)
	if err != nil {
		t.Fatalf("LookupByOrg 2: %v", err)
	}
	if found2 == nil || found2.GetConfig().GetRef().GetTenant().GetId() != id2 {
		t.Fatalf("got tenant ID %v, want %q", found2, id2)
	}

	// Unknown org returns nil without error
	unknown, err := s.LookupByOrg(ctx, issuer1, "unknown-org")
	if err != nil {
		t.Fatalf("LookupByOrg unknown org: %v", err)
	}
	if unknown != nil {
		t.Fatalf("expected nil for unknown org, got %v", unknown)
	}

	// Unknown issuer returns nil without error
	unknownIssuer, err := s.LookupByOrg(ctx, "https://unknown.test", org1)
	if err != nil {
		t.Fatalf("LookupByOrg unknown issuer: %v", err)
	}
	if unknownIssuer != nil {
		t.Fatalf("expected nil for unknown issuer, got %v", unknownIssuer)
	}
}

func TestDuplicatePrevention(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const (
		id1 = "0192e6a0-0000-7000-8000-000000000010"
		id2 = "0192e6a0-0000-7000-8000-000000000011"
		org = "org-unique"
	)

	// Create first tenant
	cfg1 := sampleTenantConfig(id1, org)
	if _, err := s.Create(ctx, cfg1); err != nil {
		t.Fatalf("Create first tenant: %v", err)
	}

	// Attempt to create second tenant with different ID but same (issuer, org)
	cfgDupOrg := sampleTenantConfig(id2, org)
	_, err := s.Create(ctx, cfgDupOrg)
	if err == nil {
		t.Fatal("Create with duplicate organization succeeded, want error")
	}
	if code, _ := errs.CodeOf(err); code != tenantstore.ErrCodeAlreadyExists {
		t.Fatalf("got error code %v, want %v", code, tenantstore.ErrCodeAlreadyExists)
	}

	// Attempt to create tenant with duplicate ID
	cfgDupID := sampleTenantConfig(id1, "org-different")
	_, err = s.Create(ctx, cfgDupID)
	if err == nil {
		t.Fatal("Create with duplicate ID succeeded, want error")
	}
	if code, _ := errs.CodeOf(err); code != tenantstore.ErrCodeAlreadyExists {
		t.Fatalf("got error code %v, want %v", code, tenantstore.ErrCodeAlreadyExists)
	}

	got, err := s.LookupByOrg(ctx, defaultIssuer, "org-different")
	if err != nil {
		t.Fatalf("LookupByOrg: %v", err)
	}
	if got != nil {
		t.Fatalf("LookupByOrg returned %v, want nil", got)
	}

	const freshID = "0192e6a0-0000-7000-8000-000000000003"
	cfgFresh := sampleTenantConfig(freshID, "org-different")
	if _, err := s.Create(ctx, cfgFresh); err != nil {
		t.Fatalf("Create with fresh ID after rolled back org index: %v", err)
	}
}

func TestListFiltersOrgKeys(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const (
		id1 = "0192e6a0-0000-7000-8000-000000000021"
		id2 = "0192e6a0-0000-7000-8000-000000000022"
	)

	if _, err := s.Create(ctx, sampleTenantConfig(id1, "org-1")); err != nil {
		t.Fatalf("Create id1: %v", err)
	}
	if _, err := s.Create(ctx, sampleTenantConfig(id2, "org-2")); err != nil {
		t.Fatalf("Create id2: %v", err)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List returned %d items, want 2", len(list))
	}
	if list[0].GetConfig().GetRef().GetTenant().GetId() != id1 {
		t.Errorf("list[0] ID = %q, want %q", list[0].GetConfig().GetRef().GetTenant().GetId(), id1)
	}
	if list[1].GetConfig().GetRef().GetTenant().GetId() != id2 {
		t.Errorf("list[1] ID = %q, want %q", list[1].GetConfig().GetRef().GetTenant().GetId(), id2)
	}
}

func TestMutateRetrySettlement(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const id = "0192e6a0-0000-7000-8000-000000000030"

	rec, err := s.Create(ctx, sampleTenantConfig(id, "org-mutate"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// 1. Basic mutation
	suspended := identityv1.TenantLifecycle_TENANT_LIFECYCLE_SUSPENDED
	updated, err := s.Mutate(ctx, id, func(current *identityv1.TenantRecord) (*identityv1.TenantRecord, error) {
		if current == nil {
			return nil, errors.New("expected existing record")
		}
		return identityv1.TenantRecord_builder{
			Config: current.GetConfig(),
			State: identityv1.TenantState_builder{
				Ref:       current.GetConfig().GetRef(),
				Lifecycle: &suspended,
				CreatedAt: current.GetState().GetCreatedAt(),
			}.Build(),
		}.Build(), nil
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if updated.GetState().GetLifecycle() != suspended {
		t.Fatalf("lifecycle = %v, want SUSPENDED", updated.GetState().GetLifecycle())
	}

	// 2. Test ErrSkip leaves record unchanged
	skipped, err := s.Mutate(ctx, id, func(_ *identityv1.TenantRecord) (*identityv1.TenantRecord, error) {
		return nil, tenantstore.ErrSkip
	})
	if err != nil {
		t.Fatalf("Mutate ErrSkip: %v", err)
	}
	if skipped.GetState().GetLifecycle() != suspended {
		t.Fatalf("skipped record lifecycle = %v, want SUSPENDED", skipped.GetState().GetLifecycle())
	}

	// 3. Test CAS conflict retry settlement
	var attempts int32
	active := identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE
	_, err = s.Mutate(ctx, id, func(current *identityv1.TenantRecord) (*identityv1.TenantRecord, error) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			// Sneak in a concurrent update on the first attempt
			_, sneakErr := s.Mutate(ctx, id, func(c *identityv1.TenantRecord) (*identityv1.TenantRecord, error) {
				return identityv1.TenantRecord_builder{
					Config: c.GetConfig(),
					State: identityv1.TenantState_builder{
						Ref:       c.GetConfig().GetRef(),
						Lifecycle: &suspended,
						CreatedAt: c.GetState().GetCreatedAt(),
					}.Build(),
				}.Build(), nil
			})
			if sneakErr != nil {
				return nil, sneakErr
			}
		}
		return identityv1.TenantRecord_builder{
			Config: current.GetConfig(),
			State: identityv1.TenantState_builder{
				Ref:       current.GetConfig().GetRef(),
				Lifecycle: &active,
				CreatedAt: current.GetState().GetCreatedAt(),
			}.Build(),
		}.Build(), nil
	})
	if err != nil {
		t.Fatalf("Mutate with concurrent collision: %v", err)
	}
	if attempts < 2 {
		t.Fatalf("expected at least 2 attempts due to CAS retry, got %d", attempts)
	}
	finalRec, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get final: %v", err)
	}
	if finalRec.GetState().GetLifecycle() != active {
		t.Fatalf("final lifecycle = %v, want ACTIVE", finalRec.GetState().GetLifecycle())
	}
	_ = rec
}

func TestLookupByOrgMismatch(t *testing.T) {
	s, kv := newStoreWithKV(t)
	ctx := context.Background()
	const id = "0192e6a0-0000-7000-8000-000000000040"
	if _, err := s.Create(ctx, sampleTenantConfig(id, "org-actual")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Simulate corrupted or orphaned index pointing to this tenant
	spoofedKey := tenantstore.OrgIndexKey(defaultIssuer, "org-spoofed")
	if _, err := kv.Create(ctx, spoofedKey, []byte(id)); err != nil {
		t.Fatalf("create spoofed index: %v", err)
	}
	rec, err := s.LookupByOrg(ctx, defaultIssuer, "org-spoofed")
	if err != nil {
		t.Fatalf("LookupByOrg: %v", err)
	}
	if rec != nil {
		t.Fatalf("LookupByOrg returned %v, want nil for mismatched org index", rec)
	}
}

func TestOrphanedOrgIndexTakeover(t *testing.T) {
	s, kv := newStoreWithKV(t)
	ctx := context.Background()
	const ghostID = "0192e6a0-0000-7000-8000-000000000050"
	const newID = "0192e6a0-0000-7000-8000-000000000051"
	const org = "org-orphaned"

	// Simulate an orphaned index: ghostID was written to org index but no tenant record was written
	orgKey := tenantstore.OrgIndexKey(defaultIssuer, org)
	if _, err := kv.Create(ctx, orgKey, []byte(ghostID)); err != nil {
		t.Fatalf("create orphaned index: %v", err)
	}

	// Create immediately with newID without waiting: should detect ghostID has no record, take over org index clock-free, and succeed
	rec, err := s.Create(ctx, sampleTenantConfig(newID, org))
	if err != nil {
		t.Fatalf("Create with orphaned index: %v", err)
	}
	if rec.GetConfig().GetRef().GetTenant().GetId() != newID {
		t.Fatalf("created tenant ID = %q, want %q", rec.GetConfig().GetRef().GetTenant().GetId(), newID)
	}

	lookedUp, err := s.LookupByOrg(ctx, defaultIssuer, org)
	if err != nil {
		t.Fatalf("LookupByOrg: %v", err)
	}
	if lookedUp == nil || lookedUp.GetConfig().GetRef().GetTenant().GetId() != newID {
		t.Fatalf("LookupByOrg = %v, want tenant %s", lookedUp, newID)
	}
}

func TestRollbackDeletesPrimaryRecordWhenOrgOwned(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const (
		id1 = "0192e6a0-0000-7000-8000-000000000055"
		id2 = "0192e6a0-0000-7000-8000-000000000056"
		org = "org-rollback-active"
	)

	// First tenant succeeds and claims the org.
	_, err := s.Create(ctx, sampleTenantConfig(id1, org))
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}

	// Second tenant with same org fails with ErrCodeAlreadyExists.
	_, err = s.Create(ctx, sampleTenantConfig(id2, org))
	if err == nil {
		t.Fatal("second Create with same org succeeded, want error")
	}
	if code, _ := errs.CodeOf(err); code != tenantstore.ErrCodeAlreadyExists {
		t.Fatalf("got code %v, want %v", code, tenantstore.ErrCodeAlreadyExists)
	}

	// Verify rollback: second tenant's primary record was deleted from the store.
	rec, err := s.Get(ctx, id2)
	if err != nil {
		t.Fatalf("Get id2: %v", err)
	}
	if rec != nil {
		t.Fatalf("expected rolled-back tenant %s to be deleted, got %v", id2, rec)
	}
}

func TestConcurrentCreateOrgConflictSettlement(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const org = "org-concurrent-compete"
	const concurrency = 8

	ids := make([]string, concurrency)
	for i := range ids {
		ids[i] = fmt.Sprintf("0192e6a0-0000-7000-8000-%012d", 70+i)
	}

	var wg sync.WaitGroup
	errsCh := make(chan error, concurrency)
	succCh := make(chan string, concurrency)

	for _, id := range ids {
		wg.Add(1)
		go func(tenantID string) {
			defer wg.Done()
			_, err := s.Create(ctx, sampleTenantConfig(tenantID, org))
			if err != nil {
				errsCh <- err
			} else {
				succCh <- tenantID
			}
		}(id)
	}
	wg.Wait()
	close(errsCh)
	close(succCh)

	var winner string
	winners := 0
	for id := range succCh {
		winner = id
		winners++
	}
	if winners != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", winners)
	}

	for err := range errsCh {
		if code, _ := errs.CodeOf(err); code != tenantstore.ErrCodeAlreadyExists {
			t.Errorf("expected ErrCodeAlreadyExists for loser, got %v", err)
		}
	}

	// Verify winner's record exists and losers have no orphaned primary records
	for _, id := range ids {
		rec, err := s.Get(ctx, id)
		if err != nil {
			t.Errorf("Get(%s): %v", id, err)
			continue
		}
		if id == winner {
			if rec == nil {
				t.Errorf("winner %s has nil record", id)
			}
		} else {
			if rec != nil {
				t.Errorf("loser %s has orphaned primary record: %v", id, rec)
			}
		}
	}
}

func TestCreateValidation(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	// Missing organization_claim_name
	invalidCfg := identityv1.TenantConfig_builder{
		Ref:                    tenantRef("0192e6a0-0000-7000-8000-000000000060"),
		Issuer:                 proto.String(defaultIssuer),
		OrganizationClaimValue: proto.String("org-test"),
	}.Build()
	_, err := s.Create(ctx, invalidCfg)
	if err == nil {
		t.Fatal("Create with invalid config succeeded, want error")
	}
	if code, _ := errs.CodeOf(err); code != tenantstore.ErrCodeInvalidConfig {
		t.Fatalf("got code %v, want %v", code, tenantstore.ErrCodeInvalidConfig)
	}

	// Invalid UUID in ref
	invalidUUIDCfg := sampleTenantConfig("not-a-uuid", "org-test-uuid")
	_, err = s.Create(ctx, invalidUUIDCfg)
	if err == nil {
		t.Fatal("Create with non-UUID succeeded, want error")
	}
	if code, _ := errs.CodeOf(err); code != tenantstore.ErrCodeInvalidConfig {
		t.Fatalf("got code %v, want %v", code, tenantstore.ErrCodeInvalidConfig)
	}
}

func TestMutateRequiresCommittedRecord(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	const id = "0192e6a0-0000-7000-8000-000000000070"

	called := false
	_, err := s.Mutate(ctx, id, func(_ *identityv1.TenantRecord) (*identityv1.TenantRecord, error) {
		called = true
		return sampleTenantRecord(sampleTenantConfig(id, "org-rev0")), nil
	})
	if err == nil {
		t.Fatal("Mutate missing tenant succeeded, want error")
	}
	if called {
		t.Fatal("Mutate called fn for a missing tenant")
	}

	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("Get = %v, want nil", got)
	}
}

func TestMutateRejectsOrganizationBindingChange(t *testing.T) {
	const (
		id       = "0192e6a0-0000-7000-8000-000000000071"
		issuer   = "https://idp.example.test"
		org      = "org-mutate-binding"
		newOrg   = "org-mutate-binding-new"
		newIssue = "https://other.example.test"
	)

	for _, tc := range []struct {
		name   string
		issuer string
		org    string
	}{
		{name: "issuer", issuer: newIssue, org: org},
		{name: "organization", issuer: issuer, org: newOrg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			ctx := context.Background()
			if _, err := s.Create(ctx, sampleTenantConfigWithIssuer(id, issuer, org)); err != nil {
				t.Fatalf("Create: %v", err)
			}

			_, err := s.Mutate(ctx, id, func(current *identityv1.TenantRecord) (*identityv1.TenantRecord, error) {
				return identityv1.TenantRecord_builder{
					Config: sampleTenantConfigWithIssuer(id, tc.issuer, tc.org),
					State:  current.GetState(),
				}.Build(), nil
			})
			if err == nil {
				t.Fatal("Mutate organization binding succeeded, want error")
			}

			got, err := s.LookupByOrg(ctx, issuer, org)
			if err != nil {
				t.Fatalf("LookupByOrg original binding: %v", err)
			}
			if tenantID(got) != id {
				t.Fatalf("LookupByOrg original binding tenant = %q, want %q", tenantID(got), id)
			}
			changed, err := s.LookupByOrg(ctx, tc.issuer, tc.org)
			if err != nil {
				t.Fatalf("LookupByOrg changed binding: %v", err)
			}
			if tc.issuer != issuer || tc.org != org {
				if changed != nil {
					t.Fatalf("LookupByOrg changed binding = %v, want nil", changed)
				}
			}
		})
	}
}
