package accessstore_test

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/accessstore"
)

const (
	tenantA = "0192e6a0-0000-7000-8000-00000000000a"
	tenantB = "0192e6a0-0000-7000-8000-00000000000b"
	roleA   = "0192e6a0-0000-7000-8000-000000000001"
	roleB   = "0192e6a0-0000-7000-8000-000000000002"
	aliceID = "7d51eb242d45f961aac22ddcc1b9fb94fb4ec90dd8e6a13165c7f7938c9e91bc"
)

var enrolledAt = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) (*accessstore.Store, jetstream.KeyValue, *edgebus.Hub) {
	t.Helper()
	hub, err := edgebus.StartHub(t.Context(), edgebus.HubConfig{
		StateDir: t.TempDir(), FsyncPolicy: service.BusFsyncPeriodic, ListenPort: 0,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)
	kv, err := hub.JetStream().CreateKeyValue(t.Context(), jetstream.KeyValueConfig{Bucket: "access_test"})
	if err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	return accessstore.New(kv, func() time.Time { return enrolledAt }), kv, hub
}

func operator(subject string) *identityv1.OperatorRef {
	return identityv1.OperatorRef_builder{Issuer: new("https://auth.example.test"), Subject: new(subject)}.Build()
}

func roleRef(id string) *identityv1.RoleGlobalRef {
	return identityv1.RoleGlobalRef_builder{Role: identityv1.RoleLocalRef_builder{Id: new(id)}.Build()}.Build()
}

func tenantRef(id string) *identityv1.TenantGlobalRef {
	return identityv1.TenantGlobalRef_builder{Tenant: identityv1.TenantLocalRef_builder{Id: new(id)}.Build()}.Build()
}

func valid(t *testing.T, record proto.Message) {
	t.Helper()
	if err := protovalidate.Validate(record); err != nil {
		t.Fatalf("invalid fixture: %v", err)
	}
}

func member(t *testing.T, subject string) *identityv1.Member {
	t.Helper()
	rec := identityv1.Member_builder{
		Operator: operator(subject), EnrolledAt: timestamppb.New(enrolledAt), EnrolledBy: operator("admin"),
	}.Build()
	valid(t, rec)
	return rec
}

func role(t *testing.T, id string) *identityv1.Role {
	t.Helper()
	rec := identityv1.Role_builder{
		Ref: roleRef(id), Name: new("viewer"), Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_VIEWER},
	}.Build()
	valid(t, rec)
	return rec
}

func partner(t *testing.T, provider string) *identityv1.Partner {
	t.Helper()
	rec := identityv1.Partner_builder{
		Tenant: tenantRef(provider), ConnectedAt: timestamppb.New(enrolledAt), ConnectedBy: operator("admin"),
		Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_CAPTURER},
	}.Build()
	valid(t, rec)
	return rec
}

func createMember(t *testing.T, s *accessstore.Store, tenantID, subject string) *identityv1.Member {
	t.Helper()
	rec, err := s.CreateMember(t.Context(), tenantID, member(t, subject))
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	return rec
}

func TestRecordKeysAndReads(t *testing.T) {
	s, kv, _ := newStore(t)
	m := createMember(t, s, tenantA, "alice")
	r, err := s.CreateRole(t.Context(), tenantA, role(t, roleA))
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreatePartner(t.Context(), tenantA, partner(t, tenantB))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := kv.Keys(t.Context())
	want := []string{tenantA + ".member." + aliceID, tenantA + ".partner." + tenantB, tenantA + ".role." + roleA}
	if err != nil || !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, %v, want %v", keys, err, want)
	}
	gotM, err := s.Member(t.Context(), tenantA, operator("alice"))
	if err != nil || !proto.Equal(gotM, m) {
		t.Fatalf("member = %v, %v, want %v", gotM, err, m)
	}
	gotR, err := s.Role(t.Context(), tenantA, roleRef(roleA))
	if err != nil || !proto.Equal(gotR, r) {
		t.Fatalf("role = %v, %v, want %v", gotR, err, r)
	}
	gotP, err := s.Partner(t.Context(), tenantA, tenantRef(tenantB))
	if err != nil || !proto.Equal(gotP, p) {
		t.Fatalf("partner = %v, %v, want %v", gotP, err, p)
	}
}

func TestCreatePreservesStoredRecords(t *testing.T) {
	s, _, _ := newStore(t)
	first := createMember(t, s, tenantA, "alice")
	retry := member(t, "alice")
	retry.SetEnrolledAt(timestamppb.New(enrolledAt.Add(time.Hour)))
	valid(t, retry)
	got, err := s.CreateMember(t.Context(), tenantA, retry)
	if err != nil || !proto.Equal(got, first) {
		t.Fatalf("retry member = %v, %v, want first record %v", got, err, first)
	}
	p := partner(t, tenantB)
	if _, err := s.CreatePartner(t.Context(), tenantA, p); err != nil {
		t.Fatal(err)
	}
	changed := proto.Clone(p).(*identityv1.Partner)
	changed.SetConnectedAt(timestamppb.New(enrolledAt.Add(time.Hour)))
	changed.SetRelations([]identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_VIEWER})
	valid(t, changed)
	gotP, err := s.CreatePartner(t.Context(), tenantA, changed)
	if err != nil || !proto.Equal(gotP, p) {
		t.Fatalf("retry partner = %v, %v, want first record %v", gotP, err, p)
	}
}

type concurrentCreateKV struct {
	jetstream.KeyValue
	ready   chan struct{}
	release chan struct{}
}

func (kv *concurrentCreateKV) Create(ctx context.Context, key string, value []byte, opts ...jetstream.KVCreateOpt) (uint64, error) {
	kv.ready <- struct{}{}
	select {
	case <-kv.release:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	return kv.KeyValue.Create(ctx, key, value, opts...)
}

func TestConcurrentMemberCreatesReturnWinner(t *testing.T) {
	_, kv, _ := newStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	racing := &concurrentCreateKV{KeyValue: kv, ready: make(chan struct{}, 2), release: make(chan struct{})}
	s := accessstore.New(racing, func() time.Time { return enrolledAt })
	first := member(t, "alice")
	second := member(t, "alice")
	second.SetEnrolledAt(timestamppb.New(enrolledAt.Add(time.Hour)))
	valid(t, second)
	type result struct {
		record *identityv1.Member
		err    error
	}
	results := make(chan result, 2)
	for _, candidate := range []*identityv1.Member{first, second} {
		spawn.Go(ctx, "create member", func() {
			record, err := s.CreateMember(ctx, tenantA, candidate)
			results <- result{record, err}
		}, spawn.ReportTo(func(err error) { results <- result{err: err} }))
	}
	for range 2 {
		select {
		case <-racing.ready:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(racing.release)
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || !proto.Equal(a.record, b.record) {
		t.Fatalf("create results = %v, %v and %v, %v, want one stored record", a.record, a.err, b.record, b.err)
	}
	stored, err := s.Member(t.Context(), tenantA, operator("alice"))
	if err != nil || !proto.Equal(stored, a.record) {
		t.Fatalf("stored member = %v, %v, want %v", stored, err, a.record)
	}
}

func TestListsAreOrderedAndTenantScoped(t *testing.T) {
	s, _, _ := newStore(t)
	for _, tenantID := range []string{tenantA, tenantB} {
		createMember(t, s, tenantID, "bob")
		createMember(t, s, tenantID, "alice")
		for _, id := range []string{roleB, roleA} {
			if _, err := s.CreateRole(t.Context(), tenantID, role(t, id)); err != nil {
				t.Fatal(err)
			}
		}
		for _, provider := range []string{tenantB, tenantA} {
			if _, err := s.CreatePartner(t.Context(), tenantID, partner(t, provider)); err != nil {
				t.Fatal(err)
			}
		}
	}
	members, err := s.ListMembers(t.Context(), tenantA)
	if err != nil || len(members) != 2 || members[0].GetOperator().GetSubject() != "alice" || members[1].GetOperator().GetSubject() != "bob" {
		t.Fatalf("members = %v, %v, want [alice bob]", members, err)
	}
	roles, err := s.ListRoles(t.Context(), tenantA)
	if err != nil || len(roles) != 2 || roles[0].GetRef().GetRole().GetId() != roleA || roles[1].GetRef().GetRole().GetId() != roleB {
		t.Fatalf("roles = %v, %v, want [%s %s]", roles, err, roleA, roleB)
	}
	partners, err := s.ListPartners(t.Context(), tenantA)
	if err != nil || len(partners) != 2 || partners[0].GetTenant().GetTenant().GetId() != tenantA || partners[1].GetTenant().GetTenant().GetId() != tenantB {
		t.Fatalf("partners = %v, %v, want [%s %s]", partners, err, tenantA, tenantB)
	}
	if _, err := s.DeleteMember(t.Context(), tenantA, operator("alice")); err != nil {
		t.Fatal(err)
	}
	other, err := s.Member(t.Context(), tenantB, operator("alice"))
	if err != nil || other == nil {
		t.Fatalf("other tenant member = %v, %v, want present", other, err)
	}
}

func TestTenantIDsIncludeEveryRecordKind(t *testing.T) {
	s, _, _ := newStore(t)
	ids, err := s.TenantIDs(t.Context())
	if err != nil || len(ids) != 0 {
		t.Fatalf("empty tenant IDs = %v, %v", ids, err)
	}
	createMember(t, s, tenantA, "alice")
	if _, err := s.CreateRole(t.Context(), tenantB, role(t, roleA)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePartner(t.Context(), "default", partner(t, tenantA)); err != nil {
		t.Fatal(err)
	}
	ids, err = s.TenantIDs(t.Context())
	want := []string{tenantA, tenantB, "default"}
	if err != nil || !slices.Equal(ids, want) {
		t.Fatalf("tenant IDs = %v, %v, want %v", ids, err, want)
	}
	if _, err := s.DeleteMember(t.Context(), tenantA, operator("alice")); err != nil {
		t.Fatal(err)
	}
	ids, err = s.TenantIDs(t.Context())
	if err != nil || !slices.Equal(ids, []string{tenantB, "default"}) {
		t.Fatalf("tenant IDs after deletion = %v, %v", ids, err)
	}
}

func TestConcurrentMemberMutationsBothLand(t *testing.T) {
	s, _, _ := newStore(t)
	createMember(t, s, tenantA, "alice")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make(chan error, 2)
	for _, id := range []string{roleA, roleB} {
		var calls atomic.Int32
		spawn.Go(ctx, "mutate member", func() {
			_, err := s.MutateMember(ctx, tenantA, operator("alice"), func(current *identityv1.Member) error {
				if calls.Add(1) == 1 {
					ready <- struct{}{}
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				current.SetRoles(append(current.GetRoles(), roleRef(id)))
				return nil
			})
			results <- err
		}, spawn.ReportTo(func(err error) { results <- err }))
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Member(t.Context(), tenantA, operator("alice"))
	if err != nil || len(got.GetRoles()) != 2 {
		t.Fatalf("roles = %v, %v, want both assignments", got.GetRoles(), err)
	}
	ids := []string{got.GetRoles()[0].GetRole().GetId(), got.GetRoles()[1].GetRole().GetId()}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{roleA, roleB}) {
		t.Fatalf("roles = %v, want [%s %s]", ids, roleA, roleB)
	}
	valid(t, got)
}

func TestMemberMutationErrorsAndRetryBudget(t *testing.T) {
	s, kv, _ := newStore(t)
	called := false
	_, err := s.MutateMember(t.Context(), tenantA, operator("alice"), func(*identityv1.Member) error { called = true; return nil })
	if code, _ := errs.CodeOf(err); code != accessstore.ErrCodeNotFound || called {
		t.Fatalf("missing member mutation = %v, called %v, want not-found without callback", err, called)
	}
	stored := createMember(t, s, tenantA, "alice")
	callbackErr := errors.New("refuse mutation")
	_, err = s.MutateMember(t.Context(), tenantA, operator("alice"), func(*identityv1.Member) error { return callbackErr })
	if !errors.Is(err, callbackErr) {
		t.Fatalf("callback error = %v, want %v", err, callbackErr)
	}
	data, err := proto.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, err = s.MutateMember(t.Context(), tenantA, operator("alice"), func(current *identityv1.Member) error {
		calls++
		current.SetRoles([]*identityv1.RoleGlobalRef{roleRef(roleA)})
		_, err := kv.Put(t.Context(), tenantA+".member."+aliceID, data)
		return err
	})
	if code, _ := errs.CodeOf(err); code != accessstore.ErrCodeConflict || calls != 8 {
		t.Fatalf("contended mutation = %v after %d calls, want conflict after 8", err, calls)
	}
	got, err := s.Member(t.Context(), tenantA, operator("alice"))
	if err != nil || !proto.Equal(got, stored) {
		t.Fatalf("record after refused writes = %v, %v, want %v", got, err, stored)
	}
}

func TestDeletionPresenceAndRecreation(t *testing.T) {
	s, _, _ := newStore(t)
	tests := []struct {
		name   string
		create func() error
		remove func() (bool, error)
	}{
		{"member", func() error { _, err := s.CreateMember(t.Context(), tenantA, member(t, "alice")); return err }, func() (bool, error) { return s.DeleteMember(t.Context(), tenantA, operator("alice")) }},
		{"role", func() error { _, err := s.CreateRole(t.Context(), tenantA, role(t, roleA)); return err }, func() (bool, error) { return s.DeleteRole(t.Context(), tenantA, roleRef(roleA)) }},
		{"partner", func() error { _, err := s.CreatePartner(t.Context(), tenantA, partner(t, tenantB)); return err }, func() (bool, error) { return s.DeletePartner(t.Context(), tenantA, tenantRef(tenantB)) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.create(); err != nil {
				t.Fatal(err)
			}
			if present, err := tt.remove(); err != nil || !present {
				t.Fatalf("delete = %v, %v, want true, nil", present, err)
			}
			if present, err := tt.remove(); err != nil || present {
				t.Fatalf("second delete = %v, %v, want false, nil", present, err)
			}
			if err := tt.create(); err != nil {
				t.Fatalf("recreate: %v", err)
			}
			if present, err := tt.remove(); err != nil || !present {
				t.Fatalf("delete recreated record = %v, %v, want true, nil", present, err)
			}
		})
	}
}

type racingDeleteKV struct {
	jetstream.KeyValue
	deletes int
	value   []byte
}

func (kv *racingDeleteKV) Delete(ctx context.Context, key string, opts ...jetstream.KVDeleteOpt) error {
	kv.deletes++
	if kv.deletes == 1 {
		if _, err := kv.Put(ctx, key, kv.value); err != nil {
			return err
		}
	}
	return kv.KeyValue.Delete(ctx, key, opts...)
}

func TestDeleteRetriesAConcurrentMemberWrite(t *testing.T) {
	s, kv, _ := newStore(t)
	m := createMember(t, s, tenantA, "alice")
	m.SetRoles([]*identityv1.RoleGlobalRef{roleRef(roleA)})
	valid(t, m)
	data, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	racing := &racingDeleteKV{KeyValue: kv, value: data}
	s = accessstore.New(racing, func() time.Time { return enrolledAt })
	present, err := s.DeleteMember(t.Context(), tenantA, operator("alice"))
	if err != nil || !present || racing.deletes != 2 {
		t.Fatalf("delete = %v, %v, attempts %d, want true, nil, 2", present, err, racing.deletes)
	}
	got, err := s.Member(t.Context(), tenantA, operator("alice"))
	if err != nil || got != nil {
		t.Fatalf("deleted member = %v, %v, want nil, nil", got, err)
	}
}

func TestDeleteRoleRefusesAssignmentsInItsTenant(t *testing.T) {
	s, _, _ := newStore(t)
	if _, err := s.CreateRole(t.Context(), tenantA, role(t, roleA)); err != nil {
		t.Fatal(err)
	}
	createMember(t, s, tenantA, "alice")
	if _, err := s.MutateMember(t.Context(), tenantA, operator("alice"), func(current *identityv1.Member) error {
		current.SetRoles([]*identityv1.RoleGlobalRef{roleRef(roleA)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Member(t.Context(), tenantA, operator("alice"))
	if err != nil || len(got.GetRoles()) != 1 {
		t.Fatalf("assignment = %v, %v", got, err)
	}
	present, err := s.DeleteRole(t.Context(), tenantA, roleRef(roleA))
	if code, _ := errs.CodeOf(err); code != accessstore.ErrCodeRoleAssigned || present {
		t.Fatalf("assigned role delete = %v, %v, want false, role-assigned", present, err)
	}
	r, err := s.Role(t.Context(), tenantA, roleRef(roleA))
	if err != nil || r == nil {
		t.Fatalf("assigned role = %v, %v, want present", r, err)
	}
	if _, err := s.DeleteMember(t.Context(), tenantA, operator("alice")); err != nil {
		t.Fatal(err)
	}
	createMember(t, s, tenantB, "alice")
	if _, err := s.MutateMember(t.Context(), tenantB, operator("alice"), func(current *identityv1.Member) error {
		current.SetRoles([]*identityv1.RoleGlobalRef{roleRef(roleA)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	present, err = s.DeleteRole(t.Context(), tenantA, roleRef(roleA))
	if err != nil || !present {
		t.Fatalf("role with foreign assignment delete = %v, %v, want true, nil", present, err)
	}
}

func TestFullPayloadExpiryIsExact(t *testing.T) {
	s, kv, _ := newStore(t)
	createMember(t, s, tenantA, "alice")
	if active, err := s.FullPayloadActive(t.Context(), tenantA, operator("alice")); err != nil || active {
		t.Fatalf("no grant = %v, %v, want false, nil", active, err)
	}
	grant := identityv1.FullPayloadGrant_builder{
		ExpiresAt: timestamppb.New(enrolledAt.Add(time.Hour)), Reason: new("case 42"), GrantedBy: operator("admin"), GrantedAt: timestamppb.New(enrolledAt),
	}.Build()
	valid(t, grant)
	if _, err := s.MutateMember(t.Context(), tenantA, operator("alice"), func(current *identityv1.Member) error { current.SetFullPayload(grant); return nil }); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		now  time.Time
		want bool
	}{
		{"before", enrolledAt.Add(time.Hour - time.Nanosecond), true},
		{"exact", enrolledAt.Add(time.Hour), false},
		{"after", enrolledAt.Add(time.Hour + time.Nanosecond), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clock := accessstore.New(kv, func() time.Time { return tt.now })
			active, err := clock.FullPayloadActive(t.Context(), tenantA, operator("alice"))
			if err != nil || active != tt.want {
				t.Fatalf("active = %v, %v, want %v", active, err, tt.want)
			}
		})
	}
	if active, err := s.FullPayloadActive(t.Context(), tenantB, operator("alice")); err != nil || active {
		t.Fatalf("absent member grant = %v, %v, want false, nil", active, err)
	}
	if present, err := s.DeleteMember(t.Context(), tenantA, operator("alice")); err != nil || !present {
		t.Fatalf("delete granted member = %v, %v, want true, nil", present, err)
	}
	if active, err := s.FullPayloadActive(t.Context(), tenantA, operator("alice")); err != nil || active {
		t.Fatalf("removed member grant = %v, %v, want false, nil", active, err)
	}
	createMember(t, s, tenantA, "alice")
	if active, err := s.FullPayloadActive(t.Context(), tenantA, operator("alice")); err != nil || active {
		t.Fatalf("re-enrolled member grant = %v, %v, want false, nil", active, err)
	}
}

func TestPartnerRelationsReplacementPreservesConnection(t *testing.T) {
	s, _, _ := newStore(t)
	p, err := s.CreatePartner(t.Context(), tenantA, partner(t, tenantB))
	if err != nil {
		t.Fatal(err)
	}
	relations := []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_VIEWER}
	got, err := s.UpdatePartner(t.Context(), tenantA, tenantRef(tenantB), relations)
	if err != nil || !slices.Equal(got.GetRelations(), relations) || !proto.Equal(got.GetConnectedAt(), p.GetConnectedAt()) || !proto.Equal(got.GetConnectedBy(), p.GetConnectedBy()) {
		t.Fatalf("updated partner = %v, %v, want viewer with original connection", got, err)
	}
	valid(t, got)
	stored, err := s.Partner(t.Context(), tenantA, tenantRef(tenantB))
	if err != nil || !proto.Equal(stored, got) {
		t.Fatalf("stored partner = %v, %v, want %v", stored, err, got)
	}
}

func TestMalformedRecordsReportDecode(t *testing.T) {
	s, kv, _ := newStore(t)
	tests := []struct {
		name, key string
		read      func() error
	}{
		{"member", tenantA + ".member." + aliceID, func() error { _, err := s.Member(t.Context(), tenantA, operator("alice")); return err }},
		{"role", tenantA + ".role." + roleA, func() error { _, err := s.Role(t.Context(), tenantA, roleRef(roleA)); return err }},
		{"partner", tenantA + ".partner." + tenantB, func() error { _, err := s.Partner(t.Context(), tenantA, tenantRef(tenantB)); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := kv.Put(t.Context(), tt.key, []byte{0xff}); err != nil {
				t.Fatal(err)
			}
			err := tt.read()
			if code, _ := errs.CodeOf(err); code != accessstore.ErrCodeDecode {
				t.Fatalf("decode error = %v, want accessstore/decode", err)
			}
		})
	}
}

func TestClosedConnectionReportsRetryableStoreError(t *testing.T) {
	s, _, hub := newStore(t)
	createMember(t, s, tenantA, "alice")
	hub.Connection().Close()
	tests := []struct {
		name string
		call func() error
	}{
		{"read", func() error { _, err := s.Member(t.Context(), tenantA, operator("alice")); return err }},
		{"create", func() error { _, err := s.CreateRole(t.Context(), tenantA, role(t, roleA)); return err }},
		{"mutate", func() error {
			_, err := s.MutateMember(t.Context(), tenantA, operator("alice"), func(*identityv1.Member) error { return nil })
			return err
		}},
		{"delete", func() error { _, err := s.DeleteMember(t.Context(), tenantA, operator("alice")); return err }},
		{"list", func() error { _, err := s.ListMembers(t.Context(), tenantA); return err }},
		{"tenant IDs", func() error { _, err := s.TenantIDs(t.Context()); return err }},
		{"full payload", func() error { _, err := s.FullPayloadActive(t.Context(), tenantA, operator("alice")); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if code, _ := errs.CodeOf(err); code != accessstore.ErrCodeStore || !errs.Retryable(err) {
				t.Fatalf("error = %v, retryable %v, want retryable accessstore/store", err, errs.Retryable(err))
			}
		})
	}
}

func TestTenantValidation(t *testing.T) {
	s, kv, _ := newStore(t)
	for _, bad := range []string{"", "acme.prod", "../escape", "platform"} {
		t.Run(bad, func(t *testing.T) {
			calls := []func() error{
				func() error { _, err := s.CreateMember(t.Context(), bad, member(t, "alice")); return err },
				func() error { _, err := s.CreateRole(t.Context(), bad, role(t, roleA)); return err },
				func() error { _, err := s.CreatePartner(t.Context(), bad, partner(t, tenantB)); return err },
				func() error { _, err := s.Member(t.Context(), bad, operator("alice")); return err },
				func() error { _, err := s.Role(t.Context(), bad, roleRef(roleA)); return err },
				func() error { _, err := s.Partner(t.Context(), bad, tenantRef(tenantB)); return err },
				func() error {
					_, err := s.MutateMember(t.Context(), bad, operator("alice"), func(*identityv1.Member) error { t.Error("invalid tenant reached mutation"); return nil })
					return err
				},
				func() error { _, err := s.UpdatePartner(t.Context(), bad, tenantRef(tenantB), nil); return err },
				func() error { _, err := s.DeleteMember(t.Context(), bad, operator("alice")); return err },
				func() error { _, err := s.DeleteRole(t.Context(), bad, roleRef(roleA)); return err },
				func() error { _, err := s.DeletePartner(t.Context(), bad, tenantRef(tenantB)); return err },
				func() error { _, err := s.ListMembers(t.Context(), bad); return err },
				func() error { _, err := s.ListRoles(t.Context(), bad); return err },
				func() error { _, err := s.ListPartners(t.Context(), bad); return err },
				func() error { _, err := s.FullPayloadActive(t.Context(), bad, operator("alice")); return err },
			}
			for i, call := range calls {
				err := call()
				if code, _ := errs.CodeOf(err); code != accessstore.ErrCodeStore {
					t.Errorf("operation %d error = %v, want accessstore/store", i, err)
				}
			}
		})
	}
	if keys, err := kv.Keys(t.Context()); !errors.Is(err, jetstream.ErrNoKeysFound) || len(keys) != 0 {
		t.Fatalf("invalid tenant wrote keys %v, %v", keys, err)
	}
}
