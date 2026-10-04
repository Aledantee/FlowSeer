package identityapi_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	apiv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/identity/v1/identityv1connect"
	errsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/errs/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/accessstore"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/authztest"
	"go.aledante.io/FlowSeer/src/services/device/internal/identityapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/tenantstore"
)

var (
	_ identityv1connect.TenantServiceHandler      = (*identityapi.TenantService)(nil)
	_ identityv1connect.TenantAdminServiceHandler = (*identityapi.AdminService)(nil)
)

const (
	tenantA   = "0192e6a0-0000-7000-8000-00000000000a"
	tenantB   = "0192e6a0-0000-7000-8000-00000000000b"
	missingID = "0192e6a0-0000-7000-8000-00000000000f"
	issuer    = "https://auth.example.test"
)

var testTime = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

type recordingProjector struct {
	mu            sync.Mutex
	calls         []string
	failTenant    error
	failRequester error
	onTenant      func(context.Context, string) error
}

func (p *recordingProjector) SyncTenant(ctx context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, "tenant:"+id)
	if p.onTenant != nil {
		if err := p.onTenant(ctx, id); err != nil {
			return err
		}
	}
	return p.failTenant
}

func (p *recordingProjector) SyncRequester(_ context.Context, id string, op *identityv1.OperatorRef) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, "requester:"+id+":"+op.GetIssuer()+":"+op.GetSubject())
	return p.failRequester
}

func (p *recordingProjector) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = nil
}

func wantCalls(t *testing.T, p *recordingProjector, want ...string) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if !slices.Equal(p.calls, want) {
		t.Fatalf("projection calls = %v, want %v", p.calls, want)
	}
}

type fixture struct {
	hub       *edgebus.Hub
	kv        jetstream.KeyValue
	tenants   *tenantstore.Store
	access    *accessstore.Store
	tenant    *identityapi.TenantService
	admin     *identityapi.AdminService
	projector *recordingProjector
	now       time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	hub, err := edgebus.StartHub(t.Context(), edgebus.HubConfig{StateDir: t.TempDir(), FsyncPolicy: service.BusFsyncPeriodic, ListenPort: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hub.Close)
	tenants, err := tenantstore.New(t.Context(), hub.JetStream(), edgebus.TenantBucket)
	if err != nil {
		t.Fatal(err)
	}
	kv, err := hub.JetStream().CreateKeyValue(t.Context(), jetstream.KeyValueConfig{Bucket: "identity_access_test"})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{hub: hub, kv: kv, tenants: tenants, projector: &recordingProjector{}, now: testTime}
	f.access = accessstore.New(kv, func() time.Time { return f.now })
	f.tenant = identityapi.NewTenantService(tenants, f.projector)
	f.admin = identityapi.NewAdminService(tenants, f.access, []string{issuer}, func() time.Time { return f.now }, f.projector)
	for _, id := range []string{tenantA, tenantB} {
		config := identityv1.TenantConfig_builder{
			Ref: tenantRef(id), Issuer: new(issuer), OrganizationClaimName: new("groups"), OrganizationClaimValue: new(id),
		}.Build()
		valid(t, config)
		if _, err := tenants.Create(t.Context(), config); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func principal() authn.Principal {
	return authn.Principal{ID: authn.ComputePrincipalID(issuer, "admin"), Issuer: issuer, Subject: "admin", Tenants: []string{tenantA}, Platform: true}
}

func admitted(t *testing.T, id string) context.Context {
	t.Helper()
	p := principal()
	p.Tenants = []string{id}
	engine := authztest.New()
	engine.Grant("user:"+p.ID, "member", "tenant")
	ctx, err := authz.NewInterceptor(engine).Admit(authn.NewContext(t.Context(), p), id)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func operator(subject string) *identityv1.OperatorRef {
	return identityv1.OperatorRef_builder{Issuer: new(issuer), Subject: new(subject)}.Build()
}

func tenantRef(id string) *identityv1.TenantGlobalRef {
	return identityv1.TenantGlobalRef_builder{Tenant: identityv1.TenantLocalRef_builder{Id: new(id)}.Build()}.Build()
}

func roleRef(id string) *identityv1.RoleGlobalRef {
	return identityv1.RoleGlobalRef_builder{Role: identityv1.RoleLocalRef_builder{Id: new(id)}.Build()}.Build()
}

func valid(t *testing.T, msg proto.Message) {
	t.Helper()
	if err := protovalidate.Validate(msg); err != nil {
		t.Fatalf("invalid fixture or response: %v", err)
	}
}

func request[T any, M interface {
	*T
	proto.Message
}](t *testing.T, msg M) *connect.Request[T] {
	t.Helper()
	valid(t, msg)
	return connect.NewRequest((*T)(msg))
}

func wantError(t *testing.T, err error, status connect.Code, code string) {
	t.Helper()
	if got := connect.CodeOf(err); err == nil || got != status {
		t.Fatalf("error = %v, status = %v, want %v", err, got, status)
	}
	if code == "" {
		return
	}
	payload := errorPayload(t, err)
	if got := payload.GetCode(); got != code {
		t.Fatalf("error code = %q, want %q", got, code)
	}
}

func errorPayload(t *testing.T, err error) *errsv1.ErrorPayload {
	t.Helper()
	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error = %T, want Connect error", err)
	}
	for _, detail := range ce.Details() {
		msg, decodeErr := detail.Value()
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if payload, ok := msg.(*errsv1.ErrorPayload); ok {
			return payload
		}
	}
	t.Fatal("error has no payload")
	return nil
}

func createRequest() *apiv1.CreateTenantRequest {
	return apiv1.CreateTenantRequest_builder{Issuer: new(issuer), OrganizationClaimName: new("groups"), OrganizationClaimValue: new("acme"), Name: new("Acme"), Description: new("customer")}.Build()
}

func enroll(t *testing.T, f *fixture, subject string) *identityv1.Member {
	t.Helper()
	resp, err := f.admin.EnrollMember(admitted(t, tenantA), request(t, apiv1.EnrollMemberRequest_builder{Member: operator(subject)}.Build()))
	if err != nil {
		t.Fatal(err)
	}
	valid(t, resp.Msg)
	return resp.Msg.GetMember()
}

func createRole(t *testing.T, f *fixture) *identityv1.Role {
	t.Helper()
	resp, err := f.admin.CreateRole(admitted(t, tenantA), request(t, apiv1.CreateRoleRequest_builder{Name: new("operations"), Description: new("operate devices"), Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_OPERATOR}}.Build()))
	if err != nil {
		t.Fatal(err)
	}
	valid(t, resp.Msg)
	return resp.Msg.GetRole()
}

func storedMember(t *testing.T, f *fixture, subject string) *identityv1.Member {
	t.Helper()
	m, err := f.access.Member(t.Context(), tenantA, operator(subject))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCreateTenantRetriesTheOrganizationAndProjectsCommittedRecords(t *testing.T) {
	f := newFixture(t)
	ctx := admitted(t, tenantA)
	f.projector.onTenant = func(ctx context.Context, id string) error {
		record, err := f.tenants.LookupByOrg(ctx, issuer, "acme")
		if err != nil {
			return err
		}
		if record == nil || record.GetConfig().GetRef().GetTenant().GetId() != id {
			return errors.New("projection ran before the organization claim")
		}
		return nil
	}
	first, err := f.tenant.CreateTenant(ctx, request(t, createRequest()))
	if err != nil {
		t.Fatal(err)
	}
	valid(t, first.Msg)
	id := first.Msg.GetTenant().GetConfig().GetRef().GetTenant().GetId()
	if u, err := uuid.Parse(id); err != nil || u.String() != id || id == tenantA || id == tenantB {
		t.Fatalf("created id = %q, want new canonical UUID", id)
	}
	again, err := f.tenant.CreateTenant(ctx, request(t, createRequest()))
	if err != nil || !proto.Equal(again.Msg, first.Msg) {
		t.Fatalf("retry = %v, %v, want first tenant", again, err)
	}
	wantCalls(t, f.projector, "tenant:"+id, "tenant:"+id)
	for _, change := range []func(*apiv1.CreateTenantRequest){
		func(r *apiv1.CreateTenantRequest) { r.SetName("other") },
		func(r *apiv1.CreateTenantRequest) { r.SetDescription("other") },
		func(r *apiv1.CreateTenantRequest) { r.SetOrganizationClaimName("organizations") },
		func(r *apiv1.CreateTenantRequest) { r.ClearName() },
	} {
		r := createRequest()
		change(r)
		_, err := f.tenant.CreateTenant(ctx, request(t, r))
		wantError(t, err, connect.CodeAlreadyExists, "tenantstore/already-exists")
	}
	got, err := f.tenant.GetTenant(ctx, request(t, apiv1.GetTenantRequest_builder{Tenant: tenantRef(id)}.Build()))
	if err != nil || !proto.Equal(got.Msg.GetTenant(), first.Msg.GetTenant()) {
		t.Fatalf("GetTenant = %v, %v", got, err)
	}
	_, err = f.tenant.GetTenant(ctx, request(t, apiv1.GetTenantRequest_builder{Tenant: tenantRef(missingID)}.Build()))
	wantError(t, err, connect.CodeNotFound, "tenantstore/not-found")
}

func TestCreateTenantRetryRepairsFailedProjection(t *testing.T) {
	f := newFixture(t)
	ctx := admitted(t, tenantA)
	failure := errors.New("engine unavailable with private diagnostic")
	f.projector.failTenant = failure
	_, err := f.tenant.CreateTenant(ctx, request(t, createRequest()))
	wantError(t, err, connect.CodeUnavailable, "")
	if !errs.Retryable(err) || !errors.Is(err, failure) || strings.Contains(err.Error(), "private diagnostic") {
		t.Fatalf("projection error lost retry/cause or leaked diagnostics: %v", err)
	}
	stored, err := f.tenants.LookupByOrg(ctx, issuer, "acme")
	if err != nil || stored == nil {
		t.Fatalf("committed tenant = %v, %v", stored, err)
	}
	f.projector.failTenant = nil
	resp, err := f.tenant.CreateTenant(ctx, request(t, createRequest()))
	if err != nil || !proto.Equal(resp.Msg.GetTenant(), stored) {
		t.Fatalf("retry = %v, %v, want stored tenant", resp, err)
	}
	wantCalls(t, f.projector, "tenant:"+stored.GetConfig().GetRef().GetTenant().GetId(), "tenant:"+stored.GetConfig().GetRef().GetTenant().GetId())
	records, err := f.tenants.List(ctx)
	if err != nil || len(records) != 3 {
		t.Fatalf("tenants = %d, %v, want 3", len(records), err)
	}
}

func TestEnrollmentPreservesProvenanceAndProjectsOnRetry(t *testing.T) {
	f := newFixture(t)
	first := enroll(t, f, "alice")
	if !proto.Equal(first.GetEnrolledBy(), operator("admin")) || !first.GetEnrolledAt().AsTime().Equal(testTime) {
		t.Fatalf("enrollment provenance = %v", first)
	}
	f.now = testTime.Add(time.Hour)
	again := enroll(t, f, "alice")
	if !proto.Equal(first, again) || !proto.Equal(storedMember(t, f, "alice"), first) {
		t.Fatalf("retry changed enrollment: %v", again)
	}
	wantCalls(t, f.projector, "tenant:"+tenantA, "tenant:"+tenantA)
	bad := operator("unknown")
	bad.SetIssuer("https://unknown.example.test")
	_, err := f.admin.EnrollMember(admitted(t, tenantA), request(t, apiv1.EnrollMemberRequest_builder{Member: bad}.Build()))
	wantError(t, err, connect.CodeInvalidArgument, "identityapi/unknown-issuer")
	m, err := f.access.Member(t.Context(), tenantA, bad)
	if err != nil || m != nil {
		t.Fatalf("unknown issuer stored member = %v, %v", m, err)
	}
}

func TestRemovalRetriesAfterTenantOrRequesterProjectionFailure(t *testing.T) {
	for _, stage := range []string{"tenant", "requester"} {
		t.Run(stage, func(t *testing.T) {
			f := newFixture(t)
			enroll(t, f, "alice")
			role := createRole(t, f)
			ctx := admitted(t, tenantA)
			if _, err := f.admin.AssignRole(ctx, request(t, apiv1.AssignRoleRequest_builder{Member: operator("alice"), Role: role.GetRef()}.Build())); err != nil {
				t.Fatal(err)
			}
			if _, err := f.admin.GrantFullPayload(ctx, request(t, apiv1.GrantFullPayloadRequest_builder{Member: operator("alice"), Lifetime: durationpb.New(time.Hour), Reason: new("case 42")}.Build())); err != nil {
				t.Fatal(err)
			}
			before := storedMember(t, f, "alice")
			if len(before.GetRoles()) != 1 || !before.HasFullPayload() {
				t.Fatalf("member before removal = %v, want role and full payload", before)
			}
			f.projector.reset()
			failure := errors.New("projection refused")
			if stage == "tenant" {
				f.projector.failTenant = failure
			} else {
				f.projector.failRequester = failure
			}
			f.projector.onTenant = func(ctx context.Context, id string) error {
				m, err := f.access.Member(ctx, id, operator("alice"))
				if err != nil {
					return err
				}
				if m != nil {
					return errors.New("projection ran before removal")
				}
				return nil
			}
			req := request(t, apiv1.RemoveMemberRequest_builder{Member: operator("alice")}.Build())
			_, err := f.admin.RemoveMember(admitted(t, tenantA), req)
			wantError(t, err, connect.CodeUnavailable, "")
			if !errs.Retryable(err) || !errors.Is(err, failure) || errorPayload(t, err).GetRetry() != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
				t.Fatalf("removal error = %v, want retryable cause", err)
			}
			if m := storedMember(t, f, "alice"); m != nil {
				t.Fatalf("removed member = %v, want absent", m)
			}
			f.projector.failTenant = nil
			f.projector.failRequester = nil
			if _, err := f.admin.RemoveMember(admitted(t, tenantA), req); err != nil {
				t.Fatal(err)
			}
			requester := "requester:" + tenantA + ":" + issuer + ":alice"
			want := []string{"tenant:" + tenantA}
			if stage == "requester" {
				want = append(want, requester)
			}
			want = append(want, "tenant:"+tenantA, requester)
			wantCalls(t, f.projector, want...)
		})
	}
}

func TestRoleAssignmentRequiresRecordsAndDeletionWaitsForUnassignment(t *testing.T) {
	f := newFixture(t)
	ctx := admitted(t, tenantA)
	enroll(t, f, "alice")
	role := createRole(t, f)
	if role.GetName() != "operations" || role.GetDescription() != "operate devices" || !slices.Equal(role.GetRelations(), []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_OPERATOR}) {
		t.Fatalf("role = %v", role)
	}
	req := request(t, apiv1.AssignRoleRequest_builder{Member: operator("alice"), Role: role.GetRef()}.Build())
	f.projector.reset()
	f.projector.onTenant = func(ctx context.Context, id string) error {
		m, err := f.access.Member(ctx, id, operator("alice"))
		if err != nil {
			return err
		}
		if len(m.GetRoles()) != 1 || !proto.Equal(m.GetRoles()[0], role.GetRef()) {
			return errors.New("assignment was not stored before projection")
		}
		return nil
	}
	for range 2 {
		resp, err := f.admin.AssignRole(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		valid(t, resp.Msg)
		if len(resp.Msg.GetMember().GetRoles()) != 1 {
			t.Fatal("repeated assignment must hold one ref")
		}
	}
	wantCalls(t, f.projector, "tenant:"+tenantA, "tenant:"+tenantA)
	_, err := f.admin.DeleteRole(ctx, request(t, apiv1.DeleteRoleRequest_builder{Role: role.GetRef()}.Build()))
	wantError(t, err, connect.CodeFailedPrecondition, "identityapi/role-assigned")
	f.projector.onTenant = func(ctx context.Context, id string) error {
		m, err := f.access.Member(ctx, id, operator("alice"))
		if err == nil && len(m.GetRoles()) != 0 {
			return errors.New("unassignment was not stored before projection")
		}
		return err
	}
	for range 2 {
		if _, err := f.admin.UnassignRole(ctx, request(t, apiv1.UnassignRoleRequest_builder{Member: operator("alice"), Role: role.GetRef()}.Build())); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := f.admin.DeleteRole(ctx, request(t, apiv1.DeleteRoleRequest_builder{Role: role.GetRef()}.Build())); err != nil {
			t.Fatal(err)
		}
	}
	r, err := f.access.Role(ctx, tenantA, role.GetRef())
	if err != nil || r != nil {
		t.Fatalf("deleted role = %v, %v", r, err)
	}
	_, err = f.admin.AssignRole(ctx, req)
	wantError(t, err, connect.CodeNotFound, "identityapi/unknown-role")
	if len(storedMember(t, f, "alice").GetRoles()) != 0 {
		t.Fatal("unknown role was assigned")
	}
	other := createRole(t, f)
	_, err = f.admin.AssignRole(admitted(t, tenantB), request(t, apiv1.AssignRoleRequest_builder{Member: operator("alice"), Role: other.GetRef()}.Build()))
	wantError(t, err, connect.CodeFailedPrecondition, "identityapi/not-a-member")
	if _, err := f.admin.EnrollMember(admitted(t, tenantB), request(t, apiv1.EnrollMemberRequest_builder{Member: operator("alice")}.Build())); err != nil {
		t.Fatal(err)
	}
	_, err = f.admin.AssignRole(admitted(t, tenantB), request(t, apiv1.AssignRoleRequest_builder{Member: operator("alice"), Role: other.GetRef()}.Build()))
	wantError(t, err, connect.CodeNotFound, "identityapi/unknown-role")
}

func TestRoleLimitAllowsAnIdempotentAssignmentAtCapacity(t *testing.T) {
	f := newFixture(t)
	ctx := admitted(t, tenantA)
	enroll(t, f, "alice")
	var refs []*identityv1.RoleGlobalRef
	for i := range 65 {
		r := identityv1.Role_builder{
			Ref: roleRef(fmt.Sprintf("0192e6a0-0000-7000-8000-%012d", i)), Name: new("viewer"),
			Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_VIEWER},
		}.Build()
		valid(t, r)
		if _, err := f.access.CreateRole(ctx, tenantA, r); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, r.GetRef())
	}
	for _, ref := range refs[:64] {
		resp, err := f.admin.AssignRole(ctx, request(t, apiv1.AssignRoleRequest_builder{Member: operator("alice"), Role: ref}.Build()))
		if err != nil {
			t.Fatal(err)
		}
		valid(t, resp.Msg)
	}
	f.projector.reset()
	_, err := f.admin.AssignRole(ctx, request(t, apiv1.AssignRoleRequest_builder{Member: operator("alice"), Role: refs[0]}.Build()))
	if err != nil {
		t.Fatalf("reassignment at capacity: %v", err)
	}
	_, err = f.admin.AssignRole(ctx, request(t, apiv1.AssignRoleRequest_builder{Member: operator("alice"), Role: refs[64]}.Build()))
	wantError(t, err, connect.CodeInvalidArgument, "")
	if got := len(storedMember(t, f, "alice").GetRoles()); got != 64 {
		t.Fatalf("assigned roles = %d, want 64", got)
	}
	wantCalls(t, f.projector, "tenant:"+tenantA)
}

func TestGrantsRefuseAnUnenrolledOperatorWithoutWriting(t *testing.T) {
	f := newFixture(t)
	ctx := admitted(t, tenantA)
	role := createRole(t, f)
	f.projector.reset()
	_, err := f.admin.AssignRole(ctx, request(t, apiv1.AssignRoleRequest_builder{Member: operator("absent"), Role: role.GetRef()}.Build()))
	wantError(t, err, connect.CodeFailedPrecondition, "identityapi/not-a-member")
	_, err = f.admin.GrantFullPayload(ctx, request(t, apiv1.GrantFullPayloadRequest_builder{Member: operator("absent"), Lifetime: durationpb.New(time.Hour), Reason: new("case 42")}.Build()))
	wantError(t, err, connect.CodeFailedPrecondition, "identityapi/not-a-member")
	if m := storedMember(t, f, "absent"); m != nil {
		t.Fatalf("unrequested enrollment = %v", m)
	}
	wantCalls(t, f.projector)
}

func TestCreateRoleReturnsStoredRoleAndLogsProjectionFailure(t *testing.T) {
	f := newFixture(t)
	f.projector.failTenant = errors.New("private engine diagnostic")
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	first := createRole(t, f)
	second := createRole(t, f)
	if proto.Equal(first.GetRef(), second.GetRef()) {
		t.Fatal("two creates reused a role id")
	}
	stored, err := f.access.Role(t.Context(), tenantA, first.GetRef())
	if err != nil || !proto.Equal(stored, first) {
		t.Fatalf("stored role = %v, %v", stored, err)
	}
	wantCalls(t, f.projector, "tenant:"+tenantA, "tenant:"+tenantA)
	if strings.Count(logs.String(), "role projection failed") != 2 || strings.Contains(logs.String(), "private engine diagnostic") || !strings.Contains(logs.String(), `"error.type":"unknown"`) {
		t.Fatalf("projection diagnostics = %s", logs.String())
	}
}

func TestPartnerReconnectionReplacesRelationsAndPreservesProvenance(t *testing.T) {
	f := newFixture(t)
	ctx := admitted(t, tenantA)
	req := request(t, apiv1.ConnectPartnerRequest_builder{Partner: tenantRef(tenantB), Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_CAPTURER}}.Build())
	first, err := f.admin.ConnectPartner(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	valid(t, first.Msg)
	if !proto.Equal(first.Msg.GetPartner().GetConnectedBy(), operator("admin")) || !first.Msg.GetPartner().GetConnectedAt().AsTime().Equal(testTime) {
		t.Fatalf("connection provenance = %v", first.Msg)
	}
	f.now = testTime.Add(time.Hour)
	req.Msg.SetRelations([]identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_VIEWER})
	valid(t, req.Msg)
	f.projector.onTenant = func(ctx context.Context, id string) error {
		p, err := f.access.Partner(ctx, id, tenantRef(tenantB))
		if err == nil && !slices.Equal(p.GetRelations(), []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_VIEWER}) {
			return errors.New("replacement was not stored before projection")
		}
		return err
	}
	again, err := f.admin.ConnectPartner(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(again.Msg.GetPartner().GetConnectedAt(), first.Msg.GetPartner().GetConnectedAt()) || !proto.Equal(again.Msg.GetPartner().GetConnectedBy(), first.Msg.GetPartner().GetConnectedBy()) {
		t.Fatalf("reconnection changed provenance: %v", again.Msg)
	}
	wantCalls(t, f.projector, "tenant:"+tenantA, "tenant:"+tenantA)
	f.projector.onTenant = nil
	for range 2 {
		if _, err := f.admin.DisconnectPartner(ctx, request(t, apiv1.DisconnectPartnerRequest_builder{Partner: tenantRef(tenantB)}.Build())); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := f.access.Partner(ctx, tenantA, tenantRef(tenantB))
	if err != nil || stored != nil {
		t.Fatalf("disconnected partner = %v, %v", stored, err)
	}
	wantCalls(t, f.projector, "tenant:"+tenantA, "tenant:"+tenantA, "tenant:"+tenantA, "tenant:"+tenantA)
}

func TestPartnerRefusesSelfMissingTenantAndAdminRelation(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		name, id string
		relation identityv1.TenantRelation
		status   connect.Code
	}{
		{"self", tenantA, identityv1.TenantRelation_TENANT_RELATION_CAPTURER, connect.CodeInvalidArgument},
		{"missing", missingID, identityv1.TenantRelation_TENANT_RELATION_CAPTURER, connect.CodeNotFound},
		{"admin", tenantB, identityv1.TenantRelation_TENANT_RELATION_ADMIN, connect.CodeInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.admin.ConnectPartner(admitted(t, tenantA), connect.NewRequest(apiv1.ConnectPartnerRequest_builder{Partner: tenantRef(tc.id), Relations: []identityv1.TenantRelation{tc.relation}}.Build()))
			wantError(t, err, tc.status, "")
		})
	}
	partners, err := f.access.ListPartners(t.Context(), tenantA)
	if err != nil || len(partners) != 0 {
		t.Fatalf("refused partners = %v, %v", partners, err)
	}
	wantCalls(t, f.projector)
}

func TestFullPayloadReplacementUsesCentralClockAndExpiresExactly(t *testing.T) {
	f := newFixture(t)
	ctx := admitted(t, tenantA)
	enroll(t, f, "alice")
	f.projector.reset()
	grant := func(lifetime time.Duration, reason string) *identityv1.Member {
		resp, err := f.admin.GrantFullPayload(ctx, request(t, apiv1.GrantFullPayloadRequest_builder{Member: operator("alice"), Lifetime: durationpb.New(lifetime), Reason: new(reason)}.Build()))
		if err != nil {
			t.Fatal(err)
		}
		valid(t, resp.Msg)
		return resp.Msg.GetMember()
	}
	first := grant(time.Hour, "case 42")
	if got := first.GetFullPayload().GetExpiresAt().AsTime(); !got.Equal(time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("expires_at = %v, want 13:00 UTC", got)
	}
	f.now = time.Date(2026, 10, 4, 12, 30, 0, 0, time.UTC)
	second := grant(2*time.Hour, "case 43")
	g := storedMember(t, f, "alice").GetFullPayload()
	if !proto.Equal(g, second.GetFullPayload()) || g.GetReason() != "case 43" || !proto.Equal(g.GetGrantedBy(), operator("admin")) || !g.GetGrantedAt().AsTime().Equal(f.now) || !g.GetExpiresAt().AsTime().Equal(time.Date(2026, 10, 4, 14, 30, 0, 0, time.UTC)) {
		t.Fatalf("replacement = %v", g)
	}
	f.now = time.Date(2026, 10, 4, 14, 29, 59, 0, time.UTC)
	active, err := f.access.FullPayloadActive(ctx, tenantA, operator("alice"))
	if err != nil || !active {
		t.Fatalf("grant before expiry = %v, %v, want active", active, err)
	}
	f.now = time.Date(2026, 10, 4, 14, 30, 0, 0, time.UTC)
	active, err = f.access.FullPayloadActive(ctx, tenantA, operator("alice"))
	if err != nil || active {
		t.Fatalf("grant at expiry = %v, %v, want inactive", active, err)
	}
	for _, lifetime := range []time.Duration{0, -time.Second, 25 * time.Hour} {
		_, err := f.admin.GrantFullPayload(ctx, connect.NewRequest(apiv1.GrantFullPayloadRequest_builder{Member: operator("alice"), Lifetime: durationpb.New(lifetime), Reason: new("case 44")}.Build()))
		wantError(t, err, connect.CodeInvalidArgument, "")
	}
	for range 2 {
		if _, err := f.admin.RevokeFullPayload(ctx, request(t, apiv1.RevokeFullPayloadRequest_builder{Member: operator("alice")}.Build())); err != nil {
			t.Fatal(err)
		}
	}
	if storedMember(t, f, "alice").HasFullPayload() {
		t.Fatal("revocation kept the grant")
	}
	wantCalls(t, f.projector, "tenant:"+tenantA, "tenant:"+tenantA, "tenant:"+tenantA, "tenant:"+tenantA)
}

func TestMutationRetriesProjectStoredAndAbsentRecords(t *testing.T) {
	f := newFixture(t)
	ctx := admitted(t, tenantA)
	enroll(t, f, "alice")
	role := createRole(t, f)
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"enroll", func() error {
			_, err := f.admin.EnrollMember(ctx, request(t, apiv1.EnrollMemberRequest_builder{Member: operator("alice")}.Build()))
			return err
		}},
		{"assign", func() error {
			_, err := f.admin.AssignRole(ctx, request(t, apiv1.AssignRoleRequest_builder{Member: operator("alice"), Role: role.GetRef()}.Build()))
			return err
		}},
		{"grant", func() error {
			_, err := f.admin.GrantFullPayload(ctx, request(t, apiv1.GrantFullPayloadRequest_builder{Member: operator("alice"), Lifetime: durationpb.New(time.Hour), Reason: new("case 42")}.Build()))
			return err
		}},
		{"connect", func() error {
			_, err := f.admin.ConnectPartner(ctx, request(t, apiv1.ConnectPartnerRequest_builder{Partner: tenantRef(tenantB), Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_CAPTURER}}.Build()))
			return err
		}},
		{"unassign", func() error {
			_, err := f.admin.UnassignRole(ctx, request(t, apiv1.UnassignRoleRequest_builder{Member: operator("alice"), Role: role.GetRef()}.Build()))
			return err
		}},
		{"delete role", func() error {
			_, err := f.admin.DeleteRole(ctx, request(t, apiv1.DeleteRoleRequest_builder{Role: role.GetRef()}.Build()))
			return err
		}},
		{"disconnect", func() error {
			_, err := f.admin.DisconnectPartner(ctx, request(t, apiv1.DisconnectPartnerRequest_builder{Partner: tenantRef(tenantB)}.Build()))
			return err
		}},
		{"revoke", func() error {
			_, err := f.admin.RevokeFullPayload(ctx, request(t, apiv1.RevokeFullPayloadRequest_builder{Member: operator("alice")}.Build()))
			return err
		}},
		{"unassign absent member", func() error {
			_, err := f.admin.UnassignRole(ctx, request(t, apiv1.UnassignRoleRequest_builder{Member: operator("absent"), Role: role.GetRef()}.Build()))
			return err
		}},
		{"revoke absent member", func() error {
			_, err := f.admin.RevokeFullPayload(ctx, request(t, apiv1.RevokeFullPayloadRequest_builder{Member: operator("absent")}.Build()))
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.projector.reset()
			failure := errors.New("projection failed")
			f.projector.failTenant = failure
			err := tc.call()
			wantError(t, err, connect.CodeUnavailable, "")
			if !errs.Retryable(err) || !errors.Is(err, failure) {
				t.Fatalf("retry error = %v", err)
			}
			f.projector.failTenant = nil
			if err := tc.call(); err != nil {
				t.Fatal(err)
			}
			wantCalls(t, f.projector, "tenant:"+tenantA, "tenant:"+tenantA)
		})
	}
}

func TestListsPageInKeyOrderAndKeepTenantsSeparate(t *testing.T) {
	f := newFixture(t)
	ctx := admitted(t, tenantA)
	want := make(map[string]bool)
	for i := range 5 {
		m := enroll(t, f, fmt.Sprintf("member-%d", i))
		want[m.GetOperator().GetSubject()] = true
	}
	if _, err := f.admin.EnrollMember(admitted(t, tenantB), request(t, apiv1.EnrollMemberRequest_builder{Member: operator("other-tenant")}.Build())); err != nil {
		t.Fatal(err)
	}
	f.projector.reset()
	seen := make(map[string]bool)
	var order []string
	token := ""
	for page := range 3 {
		r := apiv1.ListMembersRequest_builder{PageSize: new(uint32(2))}.Build()
		if token != "" {
			r.SetPageToken(token)
		}
		resp, err := f.admin.ListMembers(ctx, request(t, r))
		if err != nil {
			t.Fatal(err)
		}
		valid(t, resp.Msg)
		if got, want := len(resp.Msg.GetMembers()), []int{2, 2, 1}[page]; got != want {
			t.Fatalf("page %d size = %d, want %d", page, got, want)
		}
		for _, m := range resp.Msg.GetMembers() {
			subject := m.GetOperator().GetSubject()
			if !want[subject] || seen[subject] {
				t.Fatalf("unexpected or repeated member %q", subject)
			}
			seen[subject] = true
			order = append(order, authn.ComputePrincipalID(m.GetOperator().GetIssuer(), subject))
		}
		token = resp.Msg.GetNextPageToken()
		if (token == "") != (page == 2) {
			t.Fatalf("page %d token = %q", page, token)
		}
	}
	if len(seen) != 5 || !slices.IsSorted(order) {
		t.Fatalf("members = %v, order = %v", seen, order)
	}
	wantCalls(t, f.projector)
	_, err := f.admin.ListMembers(ctx, request(t, apiv1.ListMembersRequest_builder{PageToken: new("%%%")}.Build()))
	wantError(t, err, connect.CodeInvalidArgument, "")
	for _, size := range []uint32{0, 501} {
		_, err := f.admin.ListMembers(ctx, connect.NewRequest(apiv1.ListMembersRequest_builder{PageSize: new(size)}.Build()))
		wantError(t, err, connect.CodeInvalidArgument, "")
	}
	for range 3 {
		createRole(t, f)
	}
	roles, err := f.admin.ListRoles(ctx, request(t, apiv1.ListRolesRequest_builder{PageSize: new(uint32(2))}.Build()))
	if err != nil {
		t.Fatal(err)
	}
	valid(t, roles.Msg)
	more, err := f.admin.ListRoles(ctx, request(t, apiv1.ListRolesRequest_builder{PageSize: new(uint32(2)), PageToken: new(roles.Msg.GetNextPageToken())}.Build()))
	if err != nil || len(roles.Msg.GetRoles()) != 2 || len(more.Msg.GetRoles()) != 1 || more.Msg.HasNextPageToken() {
		t.Fatalf("role pages = %v, %v, %v", roles, more, err)
	}
	for _, id := range []string{tenantA, tenantB} {
		_, err := f.admin.ConnectPartner(admitted(t, id), request(t, apiv1.ConnectPartnerRequest_builder{Partner: tenantRef(map[string]string{tenantA: tenantB, tenantB: tenantA}[id]), Relations: []identityv1.TenantRelation{identityv1.TenantRelation_TENANT_RELATION_VIEWER}}.Build()))
		if err != nil {
			t.Fatal(err)
		}
	}
	partners, err := f.admin.ListPartners(ctx, request(t, &apiv1.ListPartnersRequest{}))
	if err != nil || len(partners.Msg.GetPartners()) != 1 || partners.Msg.GetPartners()[0].GetTenant().GetTenant().GetId() != tenantB || partners.Msg.HasNextPageToken() {
		t.Fatalf("partners = %v, %v", partners, err)
	}
	tenantPage, err := f.tenant.ListTenants(ctx, request(t, apiv1.ListTenantsRequest_builder{PageSize: new(uint32(1))}.Build()))
	if err != nil {
		t.Fatal(err)
	}
	valid(t, tenantPage.Msg)
	nextTenant, err := f.tenant.ListTenants(ctx, request(t, apiv1.ListTenantsRequest_builder{PageSize: new(uint32(1)), PageToken: new(tenantPage.Msg.GetNextPageToken())}.Build()))
	if err != nil || len(nextTenant.Msg.GetTenants()) != 1 || nextTenant.Msg.GetTenants()[0].GetConfig().GetRef().GetTenant().GetId() != tenantB || nextTenant.Msg.HasNextPageToken() {
		t.Fatalf("tenant second page = %v, %v", nextTenant, err)
	}
}

type callerInterceptor struct {
	principal  authn.Principal
	authorizer *authz.Interceptor
}

func (i callerInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return i.authorizer.WrapUnary(next)(authn.NewContext(ctx, i.principal), req)
	}
}

func (i callerInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i callerInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

type platformChecker struct{ *authztest.Engine }

func (e platformChecker) Check(ctx context.Context, q authz.Query) (bool, error) {
	if q.Object == "platform:flowseer" {
		if !slices.Contains(q.ContextualTuples, authz.Tuple{Object: "platform:flowseer", Relation: "claimed", User: q.User}) {
			return false, nil
		}
	}
	return e.Engine.Check(ctx, q)
}

func TestInterceptorsEnforcePlatformAndTenantAdminRules(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		platform, tenantAdmin bool
	}{
		{"platform admin", true, true}, {"no platform claim", false, true}, {"provider admin", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			p := principal()
			p.Platform = tc.platform
			engine := authztest.New()
			engine.Grant("user:"+p.ID, "admin", "platform")
			engine.Grant("user:"+p.ID, "member", "tenant")
			if tc.tenantAdmin {
				engine.Grant("user:"+p.ID, "admin", "tenant")
			}
			i := callerInterceptor{principal: p, authorizer: authz.NewInterceptor(platformChecker{engine})}
			mux := http.NewServeMux()
			path, handler := identityv1connect.NewTenantServiceHandler(f.tenant, connect.WithInterceptors(i))
			mux.Handle(path, handler)
			path, handler = identityv1connect.NewTenantAdminServiceHandler(f.admin, connect.WithInterceptors(i))
			mux.Handle(path, handler)
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)
			tenantClient := identityv1connect.NewTenantServiceClient(server.Client(), server.URL)
			_, err := tenantClient.CreateTenant(t.Context(), request(t, createRequest()))
			if tc.platform {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				wantError(t, err, connect.CodePermissionDenied, "authz/denied")
			}
			adminClient := identityv1connect.NewTenantAdminServiceClient(server.Client(), server.URL)
			req := request(t, apiv1.EnrollMemberRequest_builder{Member: operator("alice")}.Build())
			req.Header().Set("X-FlowSeer-Tenant", tenantA)
			_, err = adminClient.EnrollMember(t.Context(), req)
			if tc.tenantAdmin {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				wantError(t, err, connect.CodePermissionDenied, "authz/denied")
				if storedMember(t, f, "alice") != nil {
					t.Fatal("unauthorized enrollment was stored")
				}
			}
		})
	}
}

func TestHandlersRefuseMissingIdentityOrAdmittedTenant(t *testing.T) {
	f := newFixture(t)
	_, err := f.tenant.ListTenants(t.Context(), request(t, &apiv1.ListTenantsRequest{}))
	wantError(t, err, connect.CodeUnauthenticated, "")
	_, err = f.admin.ListMembers(t.Context(), request(t, &apiv1.ListMembersRequest{}))
	wantError(t, err, connect.CodeUnauthenticated, "")
	_, err = f.admin.ListMembers(authn.NewContext(t.Context(), principal()), request(t, &apiv1.ListMembersRequest{}))
	wantError(t, err, connect.CodeUnauthenticated, "tenant/no-tenant")
	wantCalls(t, f.projector)
}

func TestStoreFailuresAreSanitizedAndRetryable(t *testing.T) {
	f := newFixture(t)
	ctx := admitted(t, tenantA)
	if _, err := f.kv.Put(ctx, tenantA+".member."+authn.ComputePrincipalID(issuer, "corrupt"), []byte{0xff}); err != nil {
		t.Fatal(err)
	}
	_, err := f.admin.ListMembers(ctx, request(t, &apiv1.ListMembersRequest{}))
	wantError(t, err, connect.CodeInternal, "accessstore/decode")
	f.hub.Connection().Close()
	_, err = f.admin.EnrollMember(ctx, request(t, apiv1.EnrollMemberRequest_builder{Member: operator("alice")}.Build()))
	wantError(t, err, connect.CodeUnavailable, "accessstore/store")
	if !errs.Retryable(err) || errorPayload(t, err).GetRetry() != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
		t.Fatalf("access failure = %v, want retryable", err)
	}
	_, err = f.tenant.CreateTenant(ctx, request(t, createRequest()))
	wantError(t, err, connect.CodeUnavailable, "tenantstore/store")
	if !errs.Retryable(err) || errorPayload(t, err).GetRetry() != errsv1.RetryDisposition_RETRY_DISPOSITION_RETRYABLE {
		t.Fatalf("tenant failure = %v, want retryable", err)
	}
}
