package projector_test

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"

	// The capture fixture's interface name rule needs its registered extension.
	_ "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/key/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/common/tenant"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/accessstore"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/authztest"
	"go.aledante.io/FlowSeer/src/services/device/internal/captureapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
	"go.aledante.io/FlowSeer/src/services/device/internal/projector"
	"go.aledante.io/FlowSeer/src/services/device/internal/registry"
	"go.aledante.io/FlowSeer/src/services/device/internal/tenantstore"
)

// nats.go v1.54.0 jetstream/kv.go keyValid also rejects dot boundaries and runs.
var storeKeyPattern = regexp.MustCompile(`^[-/_=\.a-zA-Z0-9]+$`)

func validateStoreKey(key string) error {
	if !storeKeyPattern.MatchString(key) || strings.HasPrefix(key, ".") || strings.HasSuffix(key, ".") || strings.Contains(key, "..") {
		return jetstream.ErrInvalidKey
	}
	return nil
}

type fakeEdgeSource struct {
	allEdges  map[string]string // edgeID -> tenantID
	allErr    error
	tenantErr map[string]error
}

func (f *fakeEdgeSource) All(_ context.Context) (map[string]string, error) {
	if f.allErr != nil {
		return nil, f.allErr
	}
	edges := make(map[string]string, len(f.allEdges))
	for k, v := range f.allEdges {
		edges[k] = v
	}
	return edges, nil
}

func (f *fakeEdgeSource) TenantForEdge(_ context.Context, edgeID string) (string, error) {
	if err := validateStoreKey("edge_" + edgeID); err != nil {
		return "", errs.From(err).Code(edgestore.ErrCodeStore).Msg("read edge index")
	}
	if err := f.tenantErr[edgeID]; err != nil {
		return "", err
	}
	return f.allEdges[edgeID], nil
}

type fakeRegistrySource struct {
	edgeID     string
	devices    map[string]*storev1.RegistryDevice
	devicesErr error
}

func (f *fakeRegistrySource) EdgeID() string {
	return f.edgeID
}

func (f *fakeRegistrySource) Device(deviceID string) (*storev1.RegistryDevice, bool) {
	dev, ok := f.devices[deviceID]
	return dev, ok
}

func (f *fakeRegistrySource) Devices(_ context.Context, edgeID string) ([]string, error) {
	if f.devicesErr != nil {
		return nil, f.devicesErr
	}
	if edgeID != f.edgeID {
		return nil, nil
	}
	var ids []string
	for id := range f.devices {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

type sessionKey struct {
	tenantID  string
	sessionID string
}

type fakeCaptureSource struct {
	mu               sync.Mutex
	sessions         map[sessionKey]*modelcapturev1.CaptureSessionRecord
	eachErr          error
	sessionWalkErrID string
	sessionSyncErrID string
}

type fakeTenantSource struct {
	records []*identityv1.TenantRecord
	err     error
}

func (f *fakeTenantSource) List(context.Context) ([]*identityv1.TenantRecord, error) {
	return f.records, f.err
}

type fakeAccessSource struct {
	members  map[string][]*identityv1.Member
	roles    map[string][]*identityv1.Role
	partners map[string][]*identityv1.Partner
	ids      []string
	err      error
}

func (f *fakeAccessSource) TenantIDs(context.Context) ([]string, error) {
	return f.ids, f.err
}

func (f *fakeAccessSource) Members(_ context.Context, id string) ([]*identityv1.Member, error) {
	if err := tenant.Validate(id); err != nil {
		return nil, err
	}
	return f.members[id], f.err
}

func (f *fakeAccessSource) Roles(_ context.Context, id string) ([]*identityv1.Role, error) {
	if err := tenant.Validate(id); err != nil {
		return nil, err
	}
	return f.roles[id], f.err
}

func (f *fakeAccessSource) Partners(_ context.Context, id string) ([]*identityv1.Partner, error) {
	if err := tenant.Validate(id); err != nil {
		return nil, err
	}
	return f.partners[id], f.err
}

func (f *fakeAccessSource) Member(_ context.Context, id string, operator *identityv1.OperatorRef) (*identityv1.Member, error) {
	if err := tenant.Validate(id); err != nil {
		return nil, err
	}
	if f.err != nil {
		return nil, f.err
	}
	for _, member := range f.members[id] {
		if proto.Equal(member.GetOperator(), operator) {
			return member, nil
		}
	}
	return nil, nil
}

func testOperator(subject string) *identityv1.OperatorRef {
	return identityv1.OperatorRef_builder{Issuer: proto.String("https://auth.example.com"), Subject: proto.String(subject)}.Build()
}

func testTenant(t *testing.T) *identityv1.TenantRecord {
	t.Helper()
	const id = "0192e6a0-0000-7000-8000-0000000000c1"
	ref := identityv1.TenantGlobalRef_builder{Tenant: identityv1.TenantLocalRef_builder{Id: proto.String(id)}.Build()}.Build()
	rec := identityv1.TenantRecord_builder{
		Config: identityv1.TenantConfig_builder{
			Ref: ref, Issuer: proto.String("https://auth.example.com"),
			OrganizationClaimName: proto.String("groups"), OrganizationClaimValue: proto.String("acme"),
		}.Build(),
		State: identityv1.TenantState_builder{
			Ref: ref, Lifecycle: identityv1.TenantLifecycle_TENANT_LIFECYCLE_ACTIVE.Enum(),
			CreatedAt: timestamppb.New(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)),
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(rec); err != nil {
		t.Fatalf("tenant fixture: %v", err)
	}
	return rec
}

func testRole(t *testing.T, id string, relations ...identityv1.TenantRelation) *identityv1.Role {
	t.Helper()
	role := identityv1.Role_builder{
		Ref:  identityv1.RoleGlobalRef_builder{Role: identityv1.RoleLocalRef_builder{Id: proto.String(id)}.Build()}.Build(),
		Name: proto.String("Operators"), Relations: relations,
	}.Build()
	if err := protovalidate.Validate(role); err != nil {
		t.Fatalf("role fixture: %v", err)
	}
	return role
}

func testMember(t *testing.T, subject string, roles []*identityv1.RoleGlobalRef, expires time.Time) *identityv1.Member {
	t.Helper()
	operator := testOperator(subject)
	member := identityv1.Member_builder{
		Operator: operator, EnrolledBy: testOperator("admin"),
		EnrolledAt: timestamppb.New(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)), Roles: roles,
	}.Build()
	if !expires.IsZero() {
		member.SetFullPayload(identityv1.FullPayloadGrant_builder{
			ExpiresAt: timestamppb.New(expires), Reason: proto.String("case 42"),
			GrantedBy: testOperator("admin"), GrantedAt: timestamppb.New(expires.Add(-time.Hour)),
		}.Build())
	}
	if err := protovalidate.Validate(member); err != nil {
		t.Fatalf("member fixture: %v", err)
	}
	return member
}

func testPartner(t *testing.T, provider string, relations ...identityv1.TenantRelation) *identityv1.Partner {
	t.Helper()
	partner := identityv1.Partner_builder{
		Tenant:    identityv1.TenantGlobalRef_builder{Tenant: identityv1.TenantLocalRef_builder{Id: proto.String(provider)}.Build()}.Build(),
		Relations: relations, ConnectedAt: timestamppb.New(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)),
		ConnectedBy: testOperator("admin"),
	}.Build()
	if err := protovalidate.Validate(partner); err != nil {
		t.Fatalf("partner fixture: %v", err)
	}
	return partner
}

func (f *fakeCaptureSource) EachSession(_ context.Context, fn func(tenantID string, rec *modelcapturev1.CaptureSessionRecord) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.eachErr != nil {
		return f.eachErr
	}
	keys := make([]sessionKey, 0, len(f.sessions))
	for k := range f.sessions {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b sessionKey) int {
		if a.tenantID != b.tenantID {
			return strings.Compare(a.tenantID, b.tenantID)
		}
		return strings.Compare(a.sessionID, b.sessionID)
	})
	for _, k := range keys {
		rec := f.sessions[k]
		if k.sessionID == f.sessionWalkErrID {
			continue
		}
		if err := fn(k.tenantID, rec); err != nil {
			return err
		}
	}
	if f.sessionWalkErrID != "" {
		for k := range f.sessions {
			if k.sessionID == f.sessionWalkErrID {
				return errors.New("simulated session walk failure")
			}
		}
	}
	return nil
}

func (f *fakeCaptureSource) Session(_ context.Context, tenantID, sessionID string) (*modelcapturev1.CaptureSessionRecord, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := tenant.Validate(tenantID); err != nil {
		return nil, 0, errs.From(err).Code(captureapi.ErrCodeBadSession).Msg("validate tenant")
	}
	if err := validateStoreKey(tenantID + "." + sessionID); err != nil {
		return nil, 0, errs.From(err).Code(captureapi.ErrCodeStore).Msg("read capture session record")
	}
	if sessionID == f.sessionSyncErrID {
		return nil, 0, errors.New("simulated session read failure")
	}
	rec := f.sessions[sessionKey{tenantID: tenantID, sessionID: sessionID}]
	return rec, 1, nil
}

func buildSessionRecord(sessionID, edgeID, issuer, subject string) *modelcapturev1.CaptureSessionRecord {
	var reqBy *identityv1.OperatorRef
	if issuer != "" || subject != "" {
		reqBy = identityv1.OperatorRef_builder{
			Issuer:  proto.String(issuer),
			Subject: proto.String(subject),
		}.Build()
	}
	return modelcapturev1.CaptureSessionRecord_builder{
		Config: modelcapturev1.CaptureSessionConfig_builder{
			Ref: modelcapturev1.CaptureSessionGlobalRef_builder{
				Edge: edgev1.EdgeGlobalRef_builder{
					Edge: edgev1.EdgeLocalRef_builder{
						Id: proto.String(edgeID),
					}.Build(),
				}.Build(),
				CaptureSession: modelcapturev1.CaptureSessionLocalRef_builder{
					Id: proto.String(sessionID),
				}.Build(),
			}.Build(),
			Authorization: modelcapturev1.CaptureAuthorization_builder{
				RequestedBy: reqBy,
			}.Build(),
		}.Build(),
	}.Build()
}

func buildValidSessionRecord(t *testing.T, sessionID, edgeID, issuer, subject string) *modelcapturev1.CaptureSessionRecord {
	t.Helper()
	rec := buildSessionRecord(sessionID, edgeID, issuer, subject)
	config := rec.GetConfig()
	config.SetSource(modelcapturev1.CaptureSource_builder{
		LocalInterface: modelcapturev1.LocalInterfaceSource_builder{InterfaceName: proto.String("eth0")}.Build(),
	}.Build())
	config.SetBudget(modelcapturev1.CaptureBudget_builder{MaxPackets: proto.Uint64(100)}.Build())
	config.GetAuthorization().SetReason("test capture")
	config.GetAuthorization().SetFullPayloadRequested(false)
	rec.SetState(modelcapturev1.CaptureSessionState_builder{
		Ref: config.GetRef(), Lifecycle: modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_PENDING.Enum(),
	}.Build())
	if err := protovalidate.Validate(rec); err != nil {
		t.Fatalf("capture fixture: %v", err)
	}
	return rec
}

func buildRegistryDevice(deviceID string) *storev1.RegistryDevice {
	return storev1.RegistryDevice_builder{
		Config: inventoryv1.DeviceConfig_builder{
			Ref: inventoryv1.DeviceGlobalRef_builder{
				Device: inventoryv1.DeviceLocalRef_builder{
					Id: proto.String(deviceID),
				}.Build(),
			}.Build(),
		}.Build(),
	}.Build()
}

func readAllTuples(t *testing.T, engine authz.Relations) []authz.Tuple {
	t.Helper()
	var all []authz.Tuple
	err := engine.Scan(context.Background(), func(t authz.Tuple) error {
		all = append(all, t)
		return nil
	})
	if err != nil {
		t.Fatalf("readAllTuples: %v", err)
	}
	slices.SortFunc(all, func(a, b authz.Tuple) int {
		if c := slices.Compare([]byte(a.Object), []byte(b.Object)); c != 0 {
			return c
		}
		if c := slices.Compare([]byte(a.Relation), []byte(b.Relation)); c != 0 {
			return c
		}
		return slices.Compare([]byte(a.User), []byte(b.User))
	})
	return all
}

func TestProjectorOwnedRelations(t *testing.T) {
	ctx := context.Background()
	engine := authztest.New()

	const (
		edgeID   = "0192e6a0-0000-7000-8000-0000000000e1"
		deviceID = "0192e6a0-0000-7000-8000-0000000000d1"
		session1 = "0192e6a0-0000-7000-8000-0000000000c2"
		session2 = "0192e6a0-0000-7000-8000-0000000000c3"
		tenantID = "0192e6a0-0000-7000-8000-0000000000b1"
		issuer   = "https://auth.example.com"
		subject  = "user-123"
	)

	edges := &fakeEdgeSource{
		allEdges: map[string]string{edgeID: tenantID},
	}
	reg := &fakeRegistrySource{
		edgeID: edgeID,
		devices: map[string]*storev1.RegistryDevice{
			deviceID: buildRegistryDevice(deviceID),
		},
	}
	captures := &fakeCaptureSource{
		sessions: map[sessionKey]*modelcapturev1.CaptureSessionRecord{
			{tenantID: tenantID, sessionID: session1}: buildSessionRecord(session1, edgeID, issuer, subject),
			{tenantID: tenantID, sessionID: session2}: buildSessionRecord(session2, edgeID, "", ""),
		},
	}

	p := projector.New(engine, edges, reg, captures, 0, nil, nil)

	// Sync edge
	if err := p.Sync(ctx, projector.Object{Type: "edge", ID: edgeID}); err != nil {
		t.Fatalf("Sync edge: %v", err)
	}
	edgeTuples, err := engine.Read(ctx, "edge:"+edgeID)
	if err != nil {
		t.Fatalf("Read edge tuples: %v", err)
	}
	wantEdgeTuple := authz.Tuple{Object: "edge:" + edgeID, Relation: "tenant", User: "tenant:" + tenantID}
	if len(edgeTuples) != 1 || edgeTuples[0] != wantEdgeTuple {
		t.Fatalf("edge tuples = %v, want [%v]", edgeTuples, wantEdgeTuple)
	}

	// Sync device
	if err := p.Sync(ctx, projector.Object{Type: "device", ID: deviceID}); err != nil {
		t.Fatalf("Sync device: %v", err)
	}
	deviceTuples, err := engine.Read(ctx, "device:"+deviceID)
	if err != nil {
		t.Fatalf("Read device tuples: %v", err)
	}
	wantDeviceTuple := authz.Tuple{Object: "device:" + deviceID, Relation: "tenant", User: "tenant:" + tenantID}
	if len(deviceTuples) != 1 || deviceTuples[0] != wantDeviceTuple {
		t.Fatalf("device tuples = %v, want [%v]", deviceTuples, wantDeviceTuple)
	}

	// Sync session1 (with issuer) -> 3 tuples
	if err := p.Sync(ctx, projector.Object{Type: "capture_session", ID: session1, Tenant: tenantID}); err != nil {
		t.Fatalf("Sync session1: %v", err)
	}
	s1Tuples, err := engine.Read(ctx, "capture_session:"+session1)
	if err != nil {
		t.Fatalf("Read session1 tuples: %v", err)
	}
	principalID := authn.ComputePrincipalID(issuer, subject)
	wantS1 := []authz.Tuple{
		{Object: "capture_session:" + session1, Relation: "tenant", User: "tenant:" + tenantID},
		{Object: "capture_session:" + session1, Relation: "edge", User: "edge:" + edgeID},
		{Object: "capture_session:" + session1, Relation: "requester", User: "user:" + principalID},
	}
	if len(s1Tuples) != 3 {
		t.Fatalf("session1 got %d tuples, want 3: %v", len(s1Tuples), s1Tuples)
	}
	for _, w := range wantS1 {
		if !slices.Contains(s1Tuples, w) {
			t.Errorf("session1 missing tuple %v", w)
		}
	}

	// Sync session2 (without issuer) -> 2 tuples
	if err := p.Sync(ctx, projector.Object{Type: "capture_session", ID: session2, Tenant: tenantID}); err != nil {
		t.Fatalf("Sync session2: %v", err)
	}
	s2Tuples, err := engine.Read(ctx, "capture_session:"+session2)
	if err != nil {
		t.Fatalf("Read session2 tuples: %v", err)
	}
	wantS2 := []authz.Tuple{
		{Object: "capture_session:" + session2, Relation: "tenant", User: "tenant:" + tenantID},
		{Object: "capture_session:" + session2, Relation: "edge", User: "edge:" + edgeID},
	}
	if len(s2Tuples) != 2 {
		t.Fatalf("session2 got %d tuples, want 2: %v", len(s2Tuples), s2Tuples)
	}
	for _, w := range wantS2 {
		if !slices.Contains(s2Tuples, w) {
			t.Errorf("session2 missing tuple %v", w)
		}
	}

	t.Run("access records", func(t *testing.T) {
		const roleID = "0192e6a0-0000-7000-8000-0000000000a1"
		const providerID = "0192e6a0-0000-7000-8000-0000000000b1"
		const tenantID = "0192e6a0-0000-7000-8000-0000000000c1"
		const session1 = "0192e6a0-0000-7000-8000-0000000000c2"
		const session2 = "0192e6a0-0000-7000-8000-0000000000c3"
		edges := &fakeEdgeSource{allEdges: map[string]string{edgeID: tenantID}}
		captures := &fakeCaptureSource{sessions: map[sessionKey]*modelcapturev1.CaptureSessionRecord{
			{tenantID: tenantID, sessionID: session1}: buildValidSessionRecord(t, session1, edgeID, issuer, subject),
			{tenantID: tenantID, sessionID: session2}: buildValidSessionRecord(t, session2, edgeID, "", ""),
		}}
		now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
		role := testRole(t, roleID, identityv1.TenantRelation_TENANT_RELATION_ADMIN, identityv1.TenantRelation_TENANT_RELATION_OPERATOR)
		member := testMember(t, subject, []*identityv1.RoleGlobalRef{role.GetRef()}, now.Add(time.Hour))
		access := &fakeAccessSource{
			ids:     []string{tenantID},
			members: map[string][]*identityv1.Member{tenantID: {member}},
			roles:   map[string][]*identityv1.Role{tenantID: {role}},
			partners: map[string][]*identityv1.Partner{
				tenantID: {testPartner(t, providerID, identityv1.TenantRelation_TENANT_RELATION_CAPTURER, identityv1.TenantRelation_TENANT_RELATION_VIEWER)},
			},
		}
		engine := authztest.New()
		stale := []authz.Tuple{
			{Object: "tenant:" + tenantID, Relation: "admin", User: "user:x"},
			{Object: "edge:" + edgeID, Relation: "capture", User: "user:x"},
			{Object: "device:" + deviceID, Relation: "view", User: "user:x"},
		}
		untouched := authz.Tuple{Object: "tenant:" + tenantID, Relation: "member", User: "user:x"}
		if err := engine.Write(ctx, append(stale, untouched), nil); err != nil {
			t.Fatalf("seed tuples: %v", err)
		}
		p := projector.New(engine, edges, reg, captures, 0, nil, nil,
			projector.WithTenantSource(&fakeTenantSource{records: []*identityv1.TenantRecord{testTenant(t)}}),
			projector.WithAccessSource(access),
			projector.WithPlatformPrincipals([]string{"platform-admin"}),
			projector.WithClock(func() time.Time { return now }),
		)
		counts, err := p.Reconcile(ctx)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if counts != (projector.RepairedCounts{Edges: 1, Devices: 1, CaptureSessions: 2, Tenants: 1, Roles: 1, Platforms: 1}) {
			t.Errorf("counts = %+v, want one repair per access object and original resource repairs", counts)
		}
		got := readAllTuples(t, engine)
		principal := "user:" + authn.ComputePrincipalID(issuer, subject)
		rows := []struct {
			name  string
			tuple authz.Tuple
		}{
			{"platform enrollment", authz.Tuple{Object: "platform:flowseer", Relation: "enrolled", User: "user:platform-admin"}},
			{"tenant platform", authz.Tuple{Object: "tenant:" + tenantID, Relation: "platform", User: "platform:flowseer"}},
			{"tenant enrollment", authz.Tuple{Object: "tenant:" + tenantID, Relation: "enrolled", User: principal}},
			{"tenant partner", authz.Tuple{Object: "tenant:" + tenantID, Relation: "partner", User: "tenant:" + providerID}},
			{"tenant role admin", authz.Tuple{Object: "tenant:" + tenantID, Relation: "admin", User: "role:" + roleID + "#assignee"}},
			{"tenant role operator", authz.Tuple{Object: "tenant:" + tenantID, Relation: "operator", User: "role:" + roleID + "#assignee"}},
			{"tenant partner capturer", authz.Tuple{Object: "tenant:" + tenantID, Relation: "capturer", User: "tenant:" + providerID + "#active_admin"}},
			{"tenant partner viewer", authz.Tuple{Object: "tenant:" + tenantID, Relation: "viewer", User: "tenant:" + providerID + "#active_admin"}},
			{"tenant full payload", authz.Tuple{Object: "tenant:" + tenantID, Relation: "full_payload", User: principal}},
			{"role assignee", authz.Tuple{Object: "role:" + roleID, Relation: "assignee", User: principal}},
			{"session requester", authz.Tuple{Object: "capture_session:" + session1, Relation: "requester", User: principal}},
			{"edge tenant", authz.Tuple{Object: "edge:" + edgeID, Relation: "tenant", User: "tenant:" + tenantID}},
			{"device tenant", authz.Tuple{Object: "device:" + deviceID, Relation: "tenant", User: "tenant:" + tenantID}},
		}
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				if !slices.Contains(got, row.tuple) {
					t.Errorf("tuple missing: %v", row.tuple)
				}
			})
		}
		for _, row := range []struct {
			name  string
			tuple authz.Tuple
		}{
			{"tenant admin drift", stale[0]},
			{"edge grant cleanup", stale[1]},
			{"device grant cleanup", stale[2]},
		} {
			t.Run(row.name, func(t *testing.T) {
				if slices.Contains(got, row.tuple) {
					t.Errorf("stale tuple remains: %v", row.tuple)
				}
			})
		}
		if !slices.Contains(got, untouched) {
			t.Errorf("unowned member tuple missing: %v", untouched)
		}
	})
}

func TestAccessGrantExpiryAndMissingRole(t *testing.T) {
	ctx := context.Background()
	const tenantID = "0192e6a0-0000-7000-8000-0000000000c1"
	const roleID = "0192e6a0-0000-7000-8000-0000000000a1"
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	missingRef := identityv1.RoleGlobalRef_builder{Role: identityv1.RoleLocalRef_builder{Id: proto.String(roleID)}.Build()}.Build()
	member := testMember(t, "alice", []*identityv1.RoleGlobalRef{missingRef}, now.Add(time.Hour))
	access := &fakeAccessSource{ids: []string{tenantID}, members: map[string][]*identityv1.Member{tenantID: {member}}}
	engine := authztest.New()
	principal := "user:" + authn.ComputePrincipalID(member.GetOperator().GetIssuer(), member.GetOperator().GetSubject())
	staleRole := authz.Tuple{Object: "role:" + roleID, Relation: "assignee", User: principal}
	if err := engine.Write(ctx, []authz.Tuple{staleRole}, nil); err != nil {
		t.Fatalf("seed role tuple: %v", err)
	}
	p := projector.New(engine, &fakeEdgeSource{}, nil, &fakeCaptureSource{}, 0, nil, nil,
		projector.WithTenantSource(&fakeTenantSource{records: []*identityv1.TenantRecord{testTenant(t)}}),
		projector.WithAccessSource(access), projector.WithClock(func() time.Time { return now }),
	)
	if _, err := p.Reconcile(ctx); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	grant := authz.Tuple{Object: "tenant:" + tenantID, Relation: "full_payload", User: principal}
	got := readAllTuples(t, engine)
	if !slices.Contains(got, grant) || slices.Contains(got, staleRole) {
		t.Fatalf("first pass tuples = %v, want active grant and no missing-role assignment", got)
	}
	now = now.Add(time.Hour)
	if _, err := p.Reconcile(ctx); err != nil {
		t.Fatalf("expiry Reconcile: %v", err)
	}
	if got := readAllTuples(t, engine); slices.Contains(got, grant) {
		t.Errorf("expired grant remains: %v", got)
	}
}

func TestSyncRequesterRequiresMembership(t *testing.T) {
	ctx := context.Background()
	const tenantID = "0192e6a0-0000-7000-8000-0000000000c1"
	const sessionID = "0192e6a0-0000-7000-8000-0000000000c2"
	const edgeID = "0192e6a0-0000-7000-8000-0000000000e1"
	operator := testOperator("alice")
	principal := "user:" + authn.ComputePrincipalID(operator.GetIssuer(), operator.GetSubject())
	requester := authz.Tuple{Object: "capture_session:" + sessionID, Relation: "requester", User: principal}
	captures := &fakeCaptureSource{sessions: map[sessionKey]*modelcapturev1.CaptureSessionRecord{
		{tenantID: tenantID, sessionID: sessionID}: buildValidSessionRecord(t, sessionID, edgeID, operator.GetIssuer(), operator.GetSubject()),
	}}
	access := &fakeAccessSource{ids: []string{tenantID}}
	engine := authztest.New()
	if err := engine.Write(ctx, []authz.Tuple{requester}, nil); err != nil {
		t.Fatalf("seed requester: %v", err)
	}
	p := projector.New(engine, &fakeEdgeSource{}, nil, captures, 0, nil, nil, projector.WithAccessSource(access))
	if _, err := p.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile without member: %v", err)
	}
	if got := readAllTuples(t, engine); slices.Contains(got, requester) {
		t.Fatalf("requester without member remains: %v", got)
	}
	access.members = map[string][]*identityv1.Member{tenantID: {testMember(t, "alice", nil, time.Time{})}}
	if _, err := p.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile after enrollment: %v", err)
	}
	if got := readAllTuples(t, engine); !slices.Contains(got, requester) {
		t.Errorf("requester after enrollment missing: %v", got)
	}
	access.members[tenantID] = nil
	if err := p.SyncRequester(ctx, tenantID, operator); err != nil {
		t.Fatalf("SyncRequester after removal: %v", err)
	}
	if got := readAllTuples(t, engine); slices.Contains(got, requester) {
		t.Errorf("requester after removal remains: %v", got)
	}
}

func TestSyncTenantCleansDeletedRole(t *testing.T) {
	ctx := context.Background()
	const tenantID = "0192e6a0-0000-7000-8000-0000000000c1"
	const roleID = "0192e6a0-0000-7000-8000-0000000000a1"
	role := testRole(t, roleID, identityv1.TenantRelation_TENANT_RELATION_OPERATOR)
	member := testMember(t, "alice", []*identityv1.RoleGlobalRef{role.GetRef()}, time.Time{})
	access := &fakeAccessSource{
		ids: []string{tenantID}, members: map[string][]*identityv1.Member{tenantID: {member}},
		roles: map[string][]*identityv1.Role{tenantID: {role}},
	}
	engine := authztest.New()
	p := projector.New(engine, &fakeEdgeSource{}, nil, &fakeCaptureSource{}, 0, nil, nil,
		projector.WithTenantSource(&fakeTenantSource{records: []*identityv1.TenantRecord{testTenant(t)}}), projector.WithAccessSource(access))
	if err := p.SyncTenant(ctx, tenantID); err != nil {
		t.Fatalf("initial SyncTenant: %v", err)
	}
	tenantGrant := authz.Tuple{Object: "tenant:" + tenantID, Relation: "operator", User: "role:" + roleID + "#assignee"}
	roleGrant := authz.Tuple{Object: "role:" + roleID, Relation: "assignee", User: "user:" + authn.ComputePrincipalID(member.GetOperator().GetIssuer(), member.GetOperator().GetSubject())}
	if got := readAllTuples(t, engine); !slices.Contains(got, tenantGrant) || !slices.Contains(got, roleGrant) {
		t.Fatalf("initial grants missing: %v", got)
	}
	access.roles[tenantID] = nil
	if err := p.SyncTenant(ctx, tenantID); err != nil {
		t.Fatalf("SyncTenant after deletion: %v", err)
	}
	if got := readAllTuples(t, engine); slices.Contains(got, tenantGrant) || slices.Contains(got, roleGrant) {
		t.Errorf("deleted role grants remain: %v", got)
	}
	const otherTenant = "0192e6a0-0000-7000-8000-0000000000b1"
	const otherRoleID = "0192e6a0-0000-7000-8000-0000000000a2"
	otherRole := testRole(t, otherRoleID, identityv1.TenantRelation_TENANT_RELATION_VIEWER)
	otherMember := testMember(t, "bob", []*identityv1.RoleGlobalRef{otherRole.GetRef()}, time.Time{})
	access.ids = []string{tenantID, otherTenant}
	access.roles[otherTenant] = []*identityv1.Role{otherRole}
	access.members[otherTenant] = []*identityv1.Member{otherMember}
	foreignTenantGrant := authz.Tuple{Object: "tenant:" + tenantID, Relation: "viewer", User: "role:" + otherRoleID + "#assignee"}
	foreignRoleGrant := authz.Tuple{Object: "role:" + otherRoleID, Relation: "assignee", User: "user:" + authn.ComputePrincipalID(otherMember.GetOperator().GetIssuer(), otherMember.GetOperator().GetSubject())}
	if err := engine.Write(ctx, []authz.Tuple{foreignTenantGrant, foreignRoleGrant}, nil); err != nil {
		t.Fatalf("seed foreign role: %v", err)
	}
	if err := p.SyncTenant(ctx, tenantID); err != nil {
		t.Fatalf("SyncTenant with foreign role drift: %v", err)
	}
	got := readAllTuples(t, engine)
	if slices.Contains(got, foreignTenantGrant) || !slices.Contains(got, foreignRoleGrant) {
		t.Errorf("foreign role after tenant sync = %v, want role assignment but no tenant grant", got)
	}
}

func TestAccessReconcileSourceFailurePreservesTuples(t *testing.T) {
	ctx := context.Background()
	const tenantID = "0192e6a0-0000-7000-8000-0000000000c1"
	const roleID = "0192e6a0-0000-7000-8000-0000000000a1"
	readErr := errors.New("access records unavailable")
	for _, tc := range []struct {
		name      string
		tenantErr error
		accessErr error
	}{
		{"tenant list", readErr, nil},
		{"access list", nil, readErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := authztest.New()
			stored := []authz.Tuple{
				{Object: "tenant:" + tenantID, Relation: "admin", User: "user:x"},
				{Object: "role:" + roleID, Relation: "assignee", User: "user:x"},
			}
			if err := engine.Write(ctx, stored, nil); err != nil {
				t.Fatalf("seed tuples: %v", err)
			}
			p := projector.New(engine, &fakeEdgeSource{}, nil, &fakeCaptureSource{}, 0, nil, nil,
				projector.WithTenantSource(&fakeTenantSource{err: tc.tenantErr}),
				projector.WithAccessSource(&fakeAccessSource{err: tc.accessErr}))
			if _, err := p.Reconcile(ctx); !errors.Is(err, readErr) {
				t.Fatalf("Reconcile error = %v, want %v", err, readErr)
			}
			got := readAllTuples(t, engine)
			if len(got) != len(stored) || !slices.Contains(got, stored[0]) || !slices.Contains(got, stored[1]) {
				t.Errorf("tuples after failed source = %v, want %v", got, stored)
			}
		})
	}
}

func TestAccessEnrollmentDuringScanKeepsTuple(t *testing.T) {
	ctx := context.Background()
	const tenantID = "0192e6a0-0000-7000-8000-0000000000c1"
	member := testMember(t, "alice", nil, time.Time{})
	tuple := authz.Tuple{Object: "tenant:" + tenantID, Relation: "enrolled", User: "user:" + authn.ComputePrincipalID(member.GetOperator().GetIssuer(), member.GetOperator().GetSubject())}
	engine := authztest.New()
	if err := engine.Write(ctx, []authz.Tuple{{Object: "tenant:" + tenantID, Relation: "platform", User: "platform:flowseer"}}, nil); err != nil {
		t.Fatalf("seed platform tuple: %v", err)
	}
	access := &fakeAccessSource{ids: []string{tenantID}}
	eng := &raceEngine{Engine: engine, beforeScan: func() {
		access.members = map[string][]*identityv1.Member{tenantID: {member}}
		if err := engine.Write(ctx, []authz.Tuple{tuple}, nil); err != nil {
			t.Fatalf("enrollment write: %v", err)
		}
	}}
	p := projector.New(eng, &fakeEdgeSource{}, nil, &fakeCaptureSource{}, 0, nil, nil,
		projector.WithTenantSource(&fakeTenantSource{records: []*identityv1.TenantRecord{testTenant(t)}}), projector.WithAccessSource(access))
	if _, err := p.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got := readAllTuples(t, engine); !slices.Contains(got, tuple) {
		t.Errorf("enrollment written during scan missing: %v", got)
	}
}

func TestNoAccessSourcePreservesTenantDrift(t *testing.T) {
	ctx := context.Background()
	engine := authztest.New()
	tuple := authz.Tuple{Object: "tenant:orphan", Relation: "admin", User: "user:x"}
	if err := engine.Write(ctx, []authz.Tuple{tuple}, nil); err != nil {
		t.Fatalf("seed tuple: %v", err)
	}
	p := projector.New(engine, &fakeEdgeSource{}, nil, &fakeCaptureSource{}, 0, nil, nil)
	if counts, err := p.Reconcile(ctx); err != nil || counts.Total() != 0 {
		t.Fatalf("Reconcile = %+v, %v, want no repairs", counts, err)
	}
	if got := readAllTuples(t, engine); !slices.Equal(got, []authz.Tuple{tuple}) {
		t.Errorf("tenant tuple without access source = %v, want [%v]", got, tuple)
	}
}

func TestReconcileDeletesOwnedTuplesUnderInvalidObjectID(t *testing.T) {
	ctx := context.Background()
	engine := authztest.New()

	strayTuples := []authz.Tuple{
		{Object: "tenant:acme", Relation: "admin", User: "user:x"},
		{Object: "tenant:acme", Relation: "enrolled", User: "user:x"},
		{Object: "role:not-a-uuid", Relation: "assignee", User: "user:x"},
		{Object: "capture_session:not-a-uuid", Relation: "tenant", User: "tenant:acme"},
		{Object: "capture_session:a%b", Relation: "tenant", User: "tenant:0192e6a0-0000-7000-8000-0000000000c1"},
		{Object: "edge:a%b", Relation: "view", User: "user:x"},
		{Object: "platform:other", Relation: "enrolled", User: "user:x"},
	}
	platformAdmin := authz.Tuple{Object: "platform:flowseer", Relation: "enrolled", User: "user:platform-admin"}
	if err := engine.Write(ctx, append(strayTuples, platformAdmin), nil); err != nil {
		t.Fatalf("seed tuples: %v", err)
	}

	access := &fakeAccessSource{
		ids: []string{"0192e6a0-0000-7000-8000-0000000000c1"},
	}
	p := projector.New(engine, &fakeEdgeSource{}, nil, &fakeCaptureSource{}, 0, nil, nil,
		projector.WithAccessSource(access),
		projector.WithTenantSource(&fakeTenantSource{}),
		projector.WithPlatformPrincipals([]string{"platform-admin"}),
	)

	counts, err := p.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile error = %v, want nil", err)
	}
	if counts != (projector.RepairedCounts{Edges: 1, Tenants: 1, Roles: 1, CaptureSessions: 2, Platforms: 1}) {
		t.Errorf("counts = %+v, want repairs for six invalid objects", counts)
	}
	got := readAllTuples(t, engine)
	for _, stray := range strayTuples {
		if slices.Contains(got, stray) {
			t.Errorf("tuples after reconcile = %v, want %v deleted", got, stray)
		}
	}
	if !slices.Contains(got, platformAdmin) {
		t.Errorf("platform admin tuple was unexpectedly removed: %v", got)
	}
}

type storedAccessSource struct {
	*accessstore.Store
}

func (s storedAccessSource) Members(ctx context.Context, tenantID string) ([]*identityv1.Member, error) {
	return s.ListMembers(ctx, tenantID)
}

func (s storedAccessSource) Roles(ctx context.Context, tenantID string) ([]*identityv1.Role, error) {
	return s.ListRoles(ctx, tenantID)
}

func (s storedAccessSource) Partners(ctx context.Context, tenantID string) ([]*identityv1.Partner, error) {
	return s.ListPartners(ctx, tenantID)
}

func TestReconcileDeletesEveryOwnedRelationWithoutAReadableRecord(t *testing.T) {
	ctx := t.Context()
	hub, err := edgebus.StartHub(ctx, edgebus.HubConfig{
		StateDir: t.TempDir(), FsyncPolicy: service.BusFsyncPeriodic, ListenPort: 0,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)
	bucket := func(name string) jetstream.KeyValue {
		t.Helper()
		kv, err := hub.JetStream().KeyValue(ctx, name)
		if err != nil {
			t.Fatalf("open %s bucket: %v", name, err)
		}
		return kv
	}
	edges := edgestore.New(bucket(edgebus.EdgeBucket))
	captures, err := captureapi.NewStore(bucket(edgebus.CapturesBucket), t.TempDir(), time.Now)
	if err != nil {
		t.Fatalf("open capture store: %v", err)
	}
	access := storedAccessSource{accessstore.New(bucket(edgebus.AccessBucket), time.Now)}
	tenants, err := tenantstore.New(ctx, hub.JetStream(), edgebus.TenantBucket)
	if err != nil {
		t.Fatalf("open tenant store: %v", err)
	}

	const validID = "0192e6a0-0000-7000-8000-0000000000c1"
	cases := []struct {
		name     string
		id       string
		tenantID string
	}{
		{"not_uuid", "not-a-uuid", "acme"},
		{"invalid_key", "a%b", validID},
		{"invalid_tenant", validID, "acme"},
	}
	owned := projector.OwnedRelations()
	objectTypes := make([]string, 0, len(owned))
	for objectType := range owned {
		objectTypes = append(objectTypes, objectType)
	}
	slices.Sort(objectTypes)
	ran := 0
	for _, objectType := range objectTypes {
		slices.Sort(owned[objectType])
		for _, relation := range owned[objectType] {
			for _, tc := range cases {
				t.Run(objectType+"/"+relation+"/"+tc.name, func(t *testing.T) {
					ran++
					id := tc.id
					if objectType == "platform" && tc.name == "invalid_tenant" {
						id = "flowseer"
					}
					object := objectType + ":" + id
					stray := authz.Tuple{Object: object, Relation: relation, User: "user:x"}
					if relation == "tenant" {
						stray.User = "tenant:" + tc.tenantID
					}
					seed := []authz.Tuple{stray}
					if objectType == "capture_session" && relation != "tenant" {
						seed = append(seed, authz.Tuple{Object: object, Relation: "tenant", User: "tenant:" + tc.tenantID})
					}
					engine := authztest.New()
					if err := engine.Write(ctx, seed, nil); err != nil {
						t.Fatalf("seed tuples: %v", err)
					}
					before := readAllTuples(t, engine)
					if len(before) != len(seed) || !slices.Contains(before, stray) {
						t.Fatalf("tuples before pass = %v, want %v", before, seed)
					}
					p := projector.New(engine, edges, &registry.Registry{}, captures, 0, nil, nil,
						projector.WithAccessSource(access), projector.WithTenantSource(tenants),
					)
					counts, err := p.Reconcile(ctx)
					if err != nil {
						t.Fatalf("Reconcile error = %v, want nil", err)
					}
					if counts.Total() != 1 {
						t.Errorf("repaired objects = %d, want 1", counts.Total())
					}
					if got := readAllTuples(t, engine); len(got) != 0 {
						t.Errorf("tuples after one pass = %v, want empty", got)
					}
				})
			}
		}
	}
	if ran != 63 {
		t.Errorf("ran %d cases, want 63", ran)
	}
}

func TestSyncTenantCleansInvalidTenantID(t *testing.T) {
	ctx := context.Background()
	engine := authztest.New()
	stray := authz.Tuple{Object: "tenant:acme", Relation: "admin", User: "user:x"}
	if err := engine.Write(ctx, []authz.Tuple{stray}, nil); err != nil {
		t.Fatalf("seed stray tuple: %v", err)
	}
	access := &fakeAccessSource{
		ids: []string{"0192e6a0-0000-7000-8000-0000000000c1"},
	}
	p := projector.New(engine, &fakeEdgeSource{}, nil, &fakeCaptureSource{}, 0, nil, nil,
		projector.WithAccessSource(access),
	)
	if err := p.SyncTenant(ctx, "acme"); err != nil {
		t.Fatalf("SyncTenant(acme) error = %v, want nil", err)
	}
	if got := readAllTuples(t, engine); slices.Contains(got, stray) {
		t.Errorf("stray tuple after SyncTenant = %v, want removed", got)
	}
}

func TestReconcileDriftRestoration(t *testing.T) {
	ctx := context.Background()

	const (
		edgeE   = "0192e6a0-0000-7000-8000-00000000000e"
		edgeX   = "0192e6a0-0000-7000-8000-0000000000f1"
		tenantT = "0192e6a0-0000-7000-8000-0000000000b4"
		tenant2 = "0192e6a0-0000-7000-8000-0000000000b2"
	)

	t.Run("edge:E#tenant missing restored", func(t *testing.T) {
		engine := authztest.New()
		edges := &fakeEdgeSource{allEdges: map[string]string{edgeE: tenantT}}
		p := projector.New(engine, edges, nil, &fakeCaptureSource{}, 0, nil, nil)

		// Assert before: empty
		before := readAllTuples(t, engine)
		if len(before) != 0 {
			t.Fatalf("engine before pass = %v, want empty", before)
		}

		counts, err := p.Reconcile(ctx)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if counts.Edges != 1 {
			t.Fatalf("counts.Edges = %d, want 1", counts.Edges)
		}

		// Assert after: restored
		after := readAllTuples(t, engine)
		want := []authz.Tuple{
			{Object: "edge:" + edgeE, Relation: "tenant", User: "tenant:" + tenantT},
		}
		if !slices.Equal(after, want) {
			t.Fatalf("engine after pass = %v, want %v", after, want)
		}
	})

	t.Run("edge:X#tenant with no record deleted", func(t *testing.T) {
		engine := authztest.New()
		orphanTuple := authz.Tuple{Object: "edge:" + edgeX, Relation: "tenant", User: "tenant:" + tenantT}
		if err := engine.Write(ctx, []authz.Tuple{orphanTuple}, nil); err != nil {
			t.Fatalf("Write: %v", err)
		}

		edges := &fakeEdgeSource{allEdges: map[string]string{}}
		p := projector.New(engine, edges, nil, &fakeCaptureSource{}, 0, nil, nil)

		// Assert before: holds orphanTuple
		before := readAllTuples(t, engine)
		if len(before) != 1 || before[0] != orphanTuple {
			t.Fatalf("engine before pass = %v, want [%v]", before, orphanTuple)
		}

		counts, err := p.Reconcile(ctx)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if counts.Edges != 1 {
			t.Fatalf("counts.Edges = %d, want 1", counts.Edges)
		}

		// Assert after: deleted (empty)
		after := readAllTuples(t, engine)
		if len(after) != 0 {
			t.Fatalf("engine after pass = %v, want empty", after)
		}
	})

	t.Run("edge:E#tenant beside true tenant deleted", func(t *testing.T) {
		engine := authztest.New()
		trueTuple := authz.Tuple{Object: "edge:" + edgeE, Relation: "tenant", User: "tenant:" + tenantT}
		staleTuple := authz.Tuple{Object: "edge:" + edgeE, Relation: "tenant", User: "tenant:" + tenant2}
		grantTuple := authz.Tuple{Object: "edge:" + edgeE, Relation: "capture", User: "user:u"}
		if err := engine.Write(ctx, []authz.Tuple{trueTuple, staleTuple, grantTuple}, nil); err != nil {
			t.Fatalf("Write: %v", err)
		}

		edges := &fakeEdgeSource{allEdges: map[string]string{edgeE: tenantT}}
		p := projector.New(engine, edges, nil, &fakeCaptureSource{}, 0, nil, nil)

		// Assert before: holds all 3
		before := readAllTuples(t, engine)
		if len(before) != 3 {
			t.Fatalf("engine before pass = %v, want 3 tuples", before)
		}

		counts, err := p.Reconcile(ctx)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if counts.Edges != 1 {
			t.Fatalf("counts.Edges = %d, want 1", counts.Edges)
		}

		// Assert after: staleTuple deleted, trueTuple and grantTuple preserved
		after := readAllTuples(t, engine)
		want := []authz.Tuple{grantTuple, trueTuple}
		if !slices.Equal(after, want) {
			t.Fatalf("engine after pass = %v, want %v", after, want)
		}
	})

	t.Run("edge:E#capture preserved untouched", func(t *testing.T) {
		engine := authztest.New()
		trueTuple := authz.Tuple{Object: "edge:" + edgeE, Relation: "tenant", User: "tenant:" + tenantT}
		grantTuple := authz.Tuple{Object: "edge:" + edgeE, Relation: "capture", User: "user:u"}
		if err := engine.Write(ctx, []authz.Tuple{trueTuple, grantTuple}, nil); err != nil {
			t.Fatalf("Write: %v", err)
		}

		edges := &fakeEdgeSource{allEdges: map[string]string{edgeE: tenantT}}
		p := projector.New(engine, edges, nil, &fakeCaptureSource{}, 0, nil, nil)

		// Assert before
		before := readAllTuples(t, engine)
		if len(before) != 2 {
			t.Fatalf("engine before pass = %v, want 2 tuples", before)
		}

		counts, err := p.Reconcile(ctx)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if counts.Total() != 0 {
			t.Fatalf("counts.Total() = %d, want 0", counts.Total())
		}

		// Assert after: grantTuple preserved untouched
		after := readAllTuples(t, engine)
		if !slices.Contains(after, grantTuple) {
			t.Fatalf("grantTuple missing after pass: %v", after)
		}
		if !slices.Contains(after, trueTuple) {
			t.Fatalf("trueTuple missing after pass: %v", after)
		}
	})
}

type raceEngine struct {
	*authztest.Engine
	beforeScan func()
}

func (r *raceEngine) Scan(ctx context.Context, fn func(authz.Tuple) error) error {
	if r.beforeScan != nil {
		r.beforeScan()
		r.beforeScan = nil
	}
	return r.Engine.Scan(ctx, fn)
}

func TestReconcileRaceCondition(t *testing.T) {
	ctx := context.Background()
	baseEngine := authztest.New()

	const (
		edgeID   = "0192e6a0-0000-7000-8000-00000000000e"
		sessionS = "0192e6a0-0000-7000-8000-0000000000c4"
		tenantT  = "0192e6a0-0000-7000-8000-0000000000b4"
		issuer   = "https://auth.example.com"
		subject  = "u1"
	)

	edges := &fakeEdgeSource{allEdges: map[string]string{edgeID: tenantT}}
	captures := &fakeCaptureSource{sessions: make(map[sessionKey]*modelcapturev1.CaptureSessionRecord)}

	eng := &raceEngine{
		Engine: baseEngine,
		beforeScan: func() {
			// Race: adds session to source and writes its tuples to engine before yielding
			captures.mu.Lock()
			captures.sessions[sessionKey{tenantID: tenantT, sessionID: sessionS}] = buildSessionRecord(sessionS, edgeID, issuer, subject)
			captures.mu.Unlock()

			principalID := authn.ComputePrincipalID(issuer, subject)
			tuples := []authz.Tuple{
				{Object: "capture_session:" + sessionS, Relation: "tenant", User: "tenant:" + tenantT},
				{Object: "capture_session:" + sessionS, Relation: "edge", User: "edge:" + edgeID},
				{Object: "capture_session:" + sessionS, Relation: "requester", User: "user:" + principalID},
			}
			if err := baseEngine.Write(ctx, tuples, nil); err != nil {
				t.Fatalf("raceEngine write: %v", err)
			}
		},
	}

	p := projector.New(eng, edges, nil, captures, 0, nil, nil)

	counts, err := p.Reconcile(ctx)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// Session created during scan was not modified by reconcile
	if counts.CaptureSessions != 0 {
		t.Fatalf("CaptureSessions repaired = %d, want 0", counts.CaptureSessions)
	}

	// Verify all three session tuples were left untouched
	sTuples, err := baseEngine.Read(ctx, "capture_session:"+sessionS)
	if err != nil {
		t.Fatalf("Read session tuples: %v", err)
	}
	if len(sTuples) != 3 {
		t.Fatalf("got %d session tuples, want 3: %v", len(sTuples), sTuples)
	}
}

func TestDeletedSessionSync(t *testing.T) {
	ctx := context.Background()
	engine := authztest.New()

	const (
		sessionID = "0192e6a0-0000-7000-8000-0000000000c4"
		tenantID  = "0192e6a0-0000-7000-8000-0000000000b4"
	)

	// Engine holds three session tuples and one grant
	tuples := []authz.Tuple{
		{Object: "capture_session:" + sessionID, Relation: "tenant", User: "tenant:" + tenantID},
		{Object: "capture_session:" + sessionID, Relation: "edge", User: "edge:e1"},
		{Object: "capture_session:" + sessionID, Relation: "requester", User: "user:u1"},
		{Object: "capture_session:" + sessionID, Relation: "operator", User: "user:admin"},
	}
	if err := engine.Write(ctx, tuples, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Capture source has NO session (deleted)
	captures := &fakeCaptureSource{sessions: make(map[sessionKey]*modelcapturev1.CaptureSessionRecord)}
	p := projector.New(engine, &fakeEdgeSource{}, nil, captures, 0, nil, nil)

	// Sync deleted session
	if err := p.Sync(ctx, projector.Object{Type: "capture_session", ID: sessionID, Tenant: tenantID}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	after, err := engine.Read(ctx, "capture_session:"+sessionID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := []authz.Tuple{
		{Object: "capture_session:" + sessionID, Relation: "operator", User: "user:admin"},
	}
	if !slices.Equal(after, want) {
		t.Fatalf("after Sync deleted session = %v, want only grant %v", after, want)
	}
}

type retryEngine struct {
	*authztest.Engine
	mu         sync.Mutex
	writeCount int
	readCount  int
	failCode   errs.Code
	alwaysFail bool
}

func (r *retryEngine) Read(ctx context.Context, object string) ([]authz.Tuple, error) {
	r.mu.Lock()
	r.readCount++
	r.mu.Unlock()
	return r.Engine.Read(ctx, object)
}

func (r *retryEngine) Write(ctx context.Context, writes, deletes []authz.Tuple) error {
	r.mu.Lock()
	r.writeCount++
	count := r.writeCount
	code := r.failCode
	always := r.alwaysFail
	r.mu.Unlock()

	if (always || count == 1) && code != "" {
		return errs.New().Code(code).Msg("simulated write failure")
	}
	return r.Engine.Write(ctx, writes, deletes)
}

func TestWriteConflictRetried(t *testing.T) {
	ctx := context.Background()
	baseEngine := authztest.New()
	retEngine := &retryEngine{
		Engine:   baseEngine,
		failCode: authz.ErrCodeConflict,
	}

	const edgeID = "0192e6a0-0000-7000-8000-00000000000e"
	edges := &fakeEdgeSource{allEdges: map[string]string{edgeID: "0192e6a0-0000-7000-8000-0000000000b1"}}
	p := projector.New(retEngine, edges, nil, &fakeCaptureSource{}, 0, nil, nil)

	if err := p.Sync(ctx, projector.Object{Type: "edge", ID: edgeID}); err != nil {
		t.Fatalf("Sync failed despite retry: %v", err)
	}

	if retEngine.writeCount != 2 {
		t.Fatalf("writeCount = %d, want 2 (1 retry)", retEngine.writeCount)
	}
	if retEngine.readCount != 2 {
		t.Fatalf("readCount = %d, want 2 (re-read before retry)", retEngine.readCount)
	}

	tuples, err := baseEngine.Read(ctx, "edge:"+edgeID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(tuples) != 1 {
		t.Fatalf("got %d tuples after retried write, want 1", len(tuples))
	}
}

func TestWriteConflictThreeTimesInAll(t *testing.T) {
	ctx := context.Background()
	baseEngine := authztest.New()
	retEngine := &retryEngine{
		Engine:     baseEngine,
		failCode:   authz.ErrCodeConflict,
		alwaysFail: true,
	}

	const edgeID = "0192e6a0-0000-7000-8000-00000000000e"
	edges := &fakeEdgeSource{allEdges: map[string]string{edgeID: "0192e6a0-0000-7000-8000-0000000000b1"}}
	p := projector.New(retEngine, edges, nil, &fakeCaptureSource{}, 0, nil, nil)

	err := p.Sync(ctx, projector.Object{Type: "edge", ID: edgeID})
	if err == nil {
		t.Fatal("Sync succeeded, want ErrCodeConflict")
	}
	code, ok := errs.CodeOf(err)
	if !ok || code != authz.ErrCodeConflict {
		t.Fatalf("err code = %v, want %v", code, authz.ErrCodeConflict)
	}
	if retEngine.writeCount != 3 {
		t.Fatalf("writeCount = %d, want 3 (initial attempt plus 2 retries)", retEngine.writeCount)
	}
	if retEngine.readCount != 3 {
		t.Fatalf("readCount = %d, want 3", retEngine.readCount)
	}
}

func TestWriteUnavailableReturnsError(t *testing.T) {
	ctx := context.Background()
	baseEngine := authztest.New()
	retEngine := &retryEngine{
		Engine:   baseEngine,
		failCode: authz.ErrCodeUnavailable,
	}

	const edgeID = "0192e6a0-0000-7000-8000-00000000000e"
	edges := &fakeEdgeSource{allEdges: map[string]string{edgeID: "0192e6a0-0000-7000-8000-0000000000b1"}}
	p := projector.New(retEngine, edges, nil, &fakeCaptureSource{}, 0, nil, nil)

	err := p.Sync(ctx, projector.Object{Type: "edge", ID: edgeID})
	if err == nil {
		t.Fatal("Sync succeeded, want ErrCodeUnavailable")
	}
	code, ok := errs.CodeOf(err)
	if !ok || code != authz.ErrCodeUnavailable {
		t.Fatalf("err code = %v, want %v", code, authz.ErrCodeUnavailable)
	}
}

func TestRunLifecycle(t *testing.T) {
	engine := authztest.New()
	engine.Fail(errs.New().Code(authz.ErrCodeUnavailable).Msg("engine unavailable"))

	edges := &fakeEdgeSource{allEdges: map[string]string{}}
	reconciledCh := make(chan struct{}, 5)
	var waitMu sync.Mutex
	var waits []time.Duration
	var reconciledAtWaits []int
	p := projector.New(engine, edges, nil, &fakeCaptureSource{}, 15*time.Second, nil, func() {
		waitMu.Lock()
		reconciledAtWaits = append(reconciledAtWaits, len(waits))
		waitMu.Unlock()
		select {
		case reconciledCh <- struct{}{}:
		default:
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p.SetWaitHook(func(waitCtx context.Context, d time.Duration) error {
		waitMu.Lock()
		waits = append(waits, d)
		count := len(waits)
		waitMu.Unlock()

		if count == 3 {
			engine.Fail(nil)
		}
		if count > 3 {
			cancel()
			return waitCtx.Err()
		}
		return nil
	})

	runErrCh := make(chan error, 1)
	spawn.Go(ctx, "test projector Run", func() {
		runErrCh <- p.Run(ctx)
	})

	select {
	case <-reconciledCh:
	case <-time.After(2 * time.Second):
		t.Fatal("reconciled was not called after engine recovered")
	}

	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("Run returned error on cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit on context cancellation")
	}

	waitMu.Lock()
	gotWaits := slices.Clone(waits)
	gotReconciledAtWaits := slices.Clone(reconciledAtWaits)
	waitMu.Unlock()

	wantWaits := []time.Duration{5 * time.Second, 10 * time.Second, 15 * time.Second, 15 * time.Second}
	if !slices.Equal(gotWaits, wantWaits) {
		t.Fatalf("waits = %v, want %v", gotWaits, wantWaits)
	}
	if want := []int{3}; !slices.Equal(gotReconciledAtWaits, want) {
		t.Fatalf("reconciled callbacks occurred after waits %v, want %v", gotReconciledAtWaits, want)
	}
}

type objectFailingEngine struct {
	*authztest.Engine
	failPrefix string
}

func (o *objectFailingEngine) Write(ctx context.Context, writes, deletes []authz.Tuple) error {
	for _, w := range writes {
		if strings.HasPrefix(w.Object, o.failPrefix) {
			return errors.New("simulated object write failure")
		}
	}
	return o.Engine.Write(ctx, writes, deletes)
}

func TestReconcileContinuesPastFailingObjectAndCollectsErrors(t *testing.T) {
	ctx := context.Background()

	const (
		edgeE    = "0192e6a0-0000-7000-8000-00000000000e"
		sessionS = "0192e6a0-0000-7000-8000-0000000000c4"
		tenantT  = "0192e6a0-0000-7000-8000-0000000000b4"
	)

	baseEngine := authztest.New()
	failEngine := &objectFailingEngine{
		Engine:     baseEngine,
		failPrefix: "capture_session:",
	}

	edges := &fakeEdgeSource{allEdges: map[string]string{edgeE: tenantT}}
	captures := &fakeCaptureSource{
		sessions: map[sessionKey]*modelcapturev1.CaptureSessionRecord{
			{tenantID: tenantT, sessionID: sessionS}: buildSessionRecord(sessionS, edgeE, "", ""),
		},
	}

	p := projector.New(failEngine, edges, nil, captures, 0, nil, nil)

	counts, err := p.Reconcile(ctx)
	if err == nil {
		t.Fatal("Reconcile returned nil error, want joined error from failing session")
	}

	// Session fails, but Edge must still be repaired despite sorting after capture_session
	if counts.Edges != 1 {
		t.Fatalf("counts.Edges = %d, want 1", counts.Edges)
	}

	edgeTuples, err := baseEngine.Read(ctx, "edge:"+edgeE)
	if err != nil {
		t.Fatalf("Read edge tuples: %v", err)
	}
	if len(edgeTuples) != 1 {
		t.Fatalf("got %d edge tuples, want 1", len(edgeTuples))
	}
}

type propertyTupleState uint8

const (
	propertyTupleAbsent propertyTupleState = iota
	propertyTuplePresent
	propertyTupleWrong
)

type propertyFault uint8

const (
	propertyNoFault propertyFault = iota
	propertySessionWalkFault
	propertyAllFault
	propertyTenantFault
	propertyDevicesFault
	propertyEachSessionFault
	propertySessionSyncFault
	propertyReadFault
	propertyScanFault
	propertyWriteFault
)

const (
	propertyWorldCount = 512
)

var propertyFaultNames = [...]string{
	"none",
	"session walk",
	"all",
	"tenant for edge",
	"devices",
	"each session",
	"session",
	"read",
	"scan",
	"write",
}

type propertyWorld struct {
	edges         [2]bool
	devices       [2]bool
	sessions      [2]bool
	edgeTuples    [2]propertyTupleState
	deviceTuples  [2]propertyTupleState
	sessionTuples [2][3]propertyTupleState
	grants        [3]bool
	registryEdge  int
	sessionEdges  [2]int
	fault         propertyFault
	faultTarget   int
}

type propertyRNG struct {
	state uint64
}

func (r *propertyRNG) next() uint64 {
	r.state = r.state*6364136223846793005 + 1442695040888963407
	return r.state
}

func (r *propertyRNG) intn(n int) int {
	return int((r.next() >> 32) % uint64(n))
}

func (r *propertyRNG) tupleState() propertyTupleState {
	return propertyTupleState(r.intn(3))
}

type propertyRelations struct {
	*authztest.Engine
	readObject  string
	scanErr     error
	scanAfter   int
	writeObject string
	writes      []authz.Tuple
	deletes     []authz.Tuple
}

func (r *propertyRelations) Read(ctx context.Context, object string) ([]authz.Tuple, error) {
	if object == r.readObject {
		return nil, errors.New("simulated relationship read failure")
	}
	return r.Engine.Read(ctx, object)
}

func (r *propertyRelations) Scan(ctx context.Context, fn func(authz.Tuple) error) error {
	if r.scanErr != nil {
		seen := 0
		err := r.Engine.Scan(ctx, func(tuple authz.Tuple) error {
			if seen == r.scanAfter {
				return r.scanErr
			}
			seen++
			return fn(tuple)
		})
		if err != nil {
			return err
		}
		return r.scanErr
	}
	return r.Engine.Scan(ctx, fn)
}

func (r *propertyRelations) Write(ctx context.Context, writes, deletes []authz.Tuple) error {
	for _, tuple := range append(slices.Clone(writes), deletes...) {
		if strings.HasPrefix(tuple.Object, r.writeObject) && r.writeObject != "" {
			return errors.New("simulated relationship write failure")
		}
	}
	r.writes = append(r.writes, writes...)
	r.deletes = append(r.deletes, deletes...)
	return r.Engine.Write(ctx, writes, deletes)
}

func propertyWorldAt(index int) propertyWorld {
	// Enumerate the boolean dimensions so every record layout and grant
	// combination appears. The fixed-seed stream draws the remaining dimensions.
	const seed = uint64(0x4d595df4d0f33173)
	rng := propertyRNG{state: seed + uint64(index+1)*0x9e3779b97f4a7c15}
	bit := func(offset int) bool { return index&(1<<offset) != 0 }
	var w propertyWorld
	for i := range w.edges {
		w.edges[i] = bit(i)
		w.edgeTuples[i] = rng.tupleState()
	}
	for i := range w.devices {
		w.devices[i] = bit(len(w.edges) + i)
		w.deviceTuples[i] = rng.tupleState()
	}
	for i := range w.sessions {
		w.sessions[i] = bit(len(w.edges) + len(w.devices) + i)
		for relation := range w.sessionTuples[i] {
			w.sessionTuples[i][relation] = rng.tupleState()
		}
	}
	for i := range w.grants {
		w.grants[i] = bit(len(w.edges) + len(w.devices) + len(w.sessions) + i)
	}
	if w.edges[0] || !w.edges[1] {
		w.registryEdge = 0
	} else {
		w.registryEdge = 1
	}
	for i := range w.sessionEdges {
		w.sessionEdges[i] = rng.intn(2)
	}
	// Give every record-sensitive fault one full layout block. The remaining
	// worlds cover the other fault kinds in fixed-size blocks.
	const layoutCount = 1 << 6
	switch {
	case index < layoutCount:
		w.fault = propertySessionWalkFault
	case index < 2*layoutCount:
		w.fault = propertyTenantFault
	case index < 3*layoutCount:
		w.fault = propertySessionSyncFault
	case index < 4*layoutCount:
		w.fault = propertyScanFault
	default:
		remaining := (index - 4*layoutCount) / 42
		switch remaining {
		case 0:
			w.fault = propertyNoFault
		case 1:
			w.fault = propertyAllFault
		case 2:
			w.fault = propertyDevicesFault
		case 3:
			w.fault = propertyEachSessionFault
		case 4:
			w.fault = propertyReadFault
		default:
			w.fault = propertyWriteFault
		}
	}
	if w.fault == propertyTenantFault {
		if w.sessions[0] {
			w.faultTarget = 1 - w.registryEdge
		} else {
			w.faultTarget = w.registryEdge
		}
	} else {
		w.faultTarget = index % 6
	}
	return w
}

func propertyIDs() (edges, devices, sessions []string) {
	edges = []string{
		"0192e6a0-0000-7000-8000-0000000000e1",
		"0192e6a0-0000-7000-8000-0000000000e2",
	}
	devices = []string{
		"0192e6a0-0000-7000-8000-0000000000d1",
		"0192e6a0-0000-7000-8000-0000000000d2",
	}
	sessions = []string{
		"0192e6a0-0000-7000-8000-0000000000c2",
		"0192e6a0-0000-7000-8000-0000000000c3",
	}
	return edges, devices, sessions
}

func propertyTenants() []string {
	return []string{
		"0192e6a0-0000-7000-8000-0000000000b1",
		"0192e6a0-0000-7000-8000-0000000000b2",
	}
}

func propertyOwnedTuple(state propertyTupleState, desired, wrong authz.Tuple) []authz.Tuple {
	switch state {
	case propertyTuplePresent:
		return []authz.Tuple{desired}
	case propertyTupleWrong:
		return []authz.Tuple{wrong}
	default:
		return nil
	}
}

func propertyInitialTuples(w propertyWorld) []authz.Tuple {
	edgeIDs, deviceIDs, sessionIDs := propertyIDs()
	tenantIDs := propertyTenants()
	var tuples []authz.Tuple
	for i, id := range edgeIDs {
		tuples = append(tuples, propertyOwnedTuple(w.edgeTuples[i],
			authz.Tuple{Object: "edge:" + id, Relation: "tenant", User: "tenant:" + tenantIDs[i]},
			authz.Tuple{Object: "edge:" + id, Relation: "tenant", User: "tenant:wrong"})...)
	}
	for i, id := range deviceIDs {
		tuples = append(tuples, propertyOwnedTuple(w.deviceTuples[i],
			authz.Tuple{Object: "device:" + id, Relation: "tenant", User: "tenant:" + tenantIDs[w.registryEdge]},
			authz.Tuple{Object: "device:" + id, Relation: "tenant", User: "tenant:wrong"})...)
	}
	for i, id := range sessionIDs {
		object := "capture_session:" + id
		principalID := authn.ComputePrincipalID("https://auth.example.com", "subject-"+id)
		desired := []authz.Tuple{
			{Object: object, Relation: "tenant", User: "tenant:" + tenantIDs[i]},
			{Object: object, Relation: "edge", User: "edge:" + edgeIDs[w.sessionEdges[i]]},
			{Object: object, Relation: "requester", User: "user:" + principalID},
		}
		wrong := []authz.Tuple{
			{Object: object, Relation: "tenant", User: "tenant:wrong"},
			{Object: object, Relation: "edge", User: "edge:wrong"},
			{Object: object, Relation: "requester", User: "user:wrong"},
		}
		for relation := range desired {
			tuples = append(tuples, propertyOwnedTuple(w.sessionTuples[i][relation], desired[relation], wrong[relation])...)
		}
	}
	tuples = append(tuples, propertyInitialUnowned(w)...)
	return tuples
}

func propertyDesiredTuples(w propertyWorld) map[string][]authz.Tuple {
	edgeIDs, deviceIDs, sessionIDs := propertyIDs()
	tenantIDs := propertyTenants()
	desired := make(map[string][]authz.Tuple)
	for i, id := range edgeIDs {
		if w.edges[i] {
			desired["edge:"+id] = []authz.Tuple{{Object: "edge:" + id, Relation: "tenant", User: "tenant:" + tenantIDs[i]}}
		}
	}
	for i, id := range deviceIDs {
		if w.devices[i] && w.edges[w.registryEdge] {
			desired["device:"+id] = []authz.Tuple{{Object: "device:" + id, Relation: "tenant", User: "tenant:" + tenantIDs[w.registryEdge]}}
		}
	}
	for i, id := range sessionIDs {
		if !w.sessions[i] {
			continue
		}
		object := "capture_session:" + id
		principalID := authn.ComputePrincipalID("https://auth.example.com", "subject-"+id)
		desired[object] = []authz.Tuple{
			{Object: object, Relation: "tenant", User: "tenant:" + tenantIDs[i]},
			{Object: object, Relation: "edge", User: "edge:" + edgeIDs[w.sessionEdges[i]]},
			{Object: object, Relation: "requester", User: "user:" + principalID},
		}
	}
	for object := range desired {
		slices.SortFunc(desired[object], compareTuples)
	}
	return desired
}

func propertyIsOwnedTuple(tuple authz.Tuple) bool {
	objType, _, ok := strings.Cut(tuple.Object, ":")
	return ok && (((objType == "edge" || objType == "device") && tuple.Relation == "tenant") ||
		(objType == "capture_session" && (tuple.Relation == "tenant" || tuple.Relation == "edge" || tuple.Relation == "requester")))
}

func propertyOwned(tuples []authz.Tuple) map[string][]authz.Tuple {
	owned := make(map[string][]authz.Tuple)
	for _, tuple := range tuples {
		if propertyIsOwnedTuple(tuple) {
			owned[tuple.Object] = append(owned[tuple.Object], tuple)
		}
	}
	for object := range owned {
		slices.SortFunc(owned[object], func(a, b authz.Tuple) int {
			if a.Relation != b.Relation {
				return strings.Compare(a.Relation, b.Relation)
			}
			return strings.Compare(a.User, b.User)
		})
	}
	return owned
}

func propertyExpectedObjects() []string {
	edgeIDs, deviceIDs, sessionIDs := propertyIDs()
	objects := make([]string, 0, len(edgeIDs)+len(deviceIDs)+len(sessionIDs))
	for _, id := range edgeIDs {
		objects = append(objects, "edge:"+id)
	}
	for _, id := range deviceIDs {
		objects = append(objects, "device:"+id)
	}
	for _, id := range sessionIDs {
		objects = append(objects, "capture_session:"+id)
	}
	return objects
}

func propertySources(w propertyWorld) (*fakeEdgeSource, *fakeRegistrySource, *fakeCaptureSource) {
	edgeIDs, deviceIDs, sessionIDs := propertyIDs()
	tenantIDs := propertyTenants()
	edges := &fakeEdgeSource{allEdges: make(map[string]string)}
	for i, id := range edgeIDs {
		if w.edges[i] {
			edges.allEdges[id] = tenantIDs[i]
		}
	}
	registry := &fakeRegistrySource{edgeID: edgeIDs[w.registryEdge], devices: make(map[string]*storev1.RegistryDevice)}
	for i, id := range deviceIDs {
		if w.devices[i] {
			registry.devices[id] = buildRegistryDevice(id)
		}
	}
	captures := &fakeCaptureSource{sessions: make(map[sessionKey]*modelcapturev1.CaptureSessionRecord)}
	for i, id := range sessionIDs {
		if w.sessions[i] {
			captures.sessions[sessionKey{tenantID: tenantIDs[i], sessionID: id}] = buildSessionRecord(id, edgeIDs[w.sessionEdges[i]], "https://auth.example.com", "subject-"+id)
		}
	}
	switch w.fault {
	case propertyAllFault:
		edges.allErr = errors.New("simulated edge listing failure")
	case propertyTenantFault:
		edges.tenantErr = map[string]error{edgeIDs[w.faultTarget%len(edgeIDs)]: errors.New("simulated edge tenant read failure")}
	case propertyDevicesFault:
		registry.devicesErr = errors.New("simulated device listing failure")
	case propertyEachSessionFault:
		captures.eachErr = errors.New("simulated session listing failure")
	case propertySessionWalkFault:
		if w.sessions[w.faultTarget%len(sessionIDs)] {
			captures.sessionWalkErrID = sessionIDs[w.faultTarget%len(sessionIDs)]
		}
	case propertySessionSyncFault:
		if w.sessions[w.faultTarget%len(sessionIDs)] {
			captures.sessionSyncErrID = sessionIDs[w.faultTarget%len(sessionIDs)]
		}
	case propertyReadFault:
		// The relationship wrapper carries this fault.
	case propertyScanFault:
		// The relationship wrapper carries this fault.
	case propertyWriteFault:
		// The relationship wrapper carries this fault.
	}
	return edges, registry, captures
}

func propertySetup(t *testing.T, w propertyWorld, reconciled func()) (*projector.Projector, *propertyRelations, *authztest.Engine) {
	t.Helper()
	base := authztest.New()
	if err := base.Write(context.Background(), propertyInitialTuples(w), nil); err != nil {
		t.Fatalf("seed property world: %v", err)
	}
	relations := &propertyRelations{Engine: base}
	edges, registry, captures := propertySources(w)
	switch w.fault {
	case propertyReadFault:
		relations.readObject = propertyExpectedObjects()[w.faultTarget]
	case propertyScanFault:
		relations.scanErr = errors.New("simulated relationship scan failure")
		relations.scanAfter = 1 + w.faultTarget%3
	case propertyWriteFault:
		relations.writeObject = propertyExpectedObjects()[w.faultTarget]
	}
	return projector.New(relations, edges, registry, captures, 0, nil, reconciled), relations, base
}

func propertyRun(t *testing.T, w propertyWorld) propertyRunResult {
	t.Helper()
	reconciledCalls := 0
	p, relations, base := propertySetup(t, w, func() {
		reconciledCalls++
	})
	counts, err := p.Reconcile(context.Background())
	return propertyRunResult{
		errorPresent:    err != nil,
		counts:          counts,
		tuples:          readAllTuples(t, base),
		writes:          slices.Clone(relations.writes),
		deletes:         slices.Clone(relations.deletes),
		reconciledCalls: reconciledCalls,
	}
}

type propertyRunResult struct {
	errorPresent    bool
	counts          projector.RepairedCounts
	tuples          []authz.Tuple
	writes          []authz.Tuple
	deletes         []authz.Tuple
	reconciledCalls int
}

func propertySameResult(a, b propertyRunResult) bool {
	return a.errorPresent == b.errorPresent && a.counts == b.counts && slices.Equal(a.tuples, b.tuples) && slices.Equal(a.writes, b.writes) && slices.Equal(a.deletes, b.deletes) && a.reconciledCalls == b.reconciledCalls
}

func propertyRecordKnown(w propertyWorld, object string) bool {
	objType, objID, _ := strings.Cut(object, ":")
	edgeIDs, deviceIDs, sessionIDs := propertyIDs()
	switch w.fault {
	case propertyAllFault:
		return objType != "edge"
	case propertyTenantFault:
		if objType == "edge" && objID == edgeIDs[w.faultTarget%2] {
			return false
		}
		if objType == "device" && w.registryEdge == w.faultTarget%2 {
			for i, id := range deviceIDs {
				if id == objID {
					return !w.devices[i]
				}
			}
		}
		return true
	case propertyDevicesFault:
		return objType != "device"
	case propertyEachSessionFault:
		return objType != "capture_session"
	case propertySessionWalkFault:
		if objType != "capture_session" || !w.sessions[w.faultTarget%len(sessionIDs)] {
			return true
		}
		for i, id := range sessionIDs {
			if id == objID {
				return i != w.faultTarget%len(sessionIDs) && w.sessions[i]
			}
		}
		return true
	case propertySessionSyncFault:
		return objType != "capture_session" || !w.sessions[w.faultTarget%2] || objID != sessionIDs[w.faultTarget%2]
	default:
		return true
	}
}

func propertyAffected(w propertyWorld, object string) bool {
	objType, objID, _ := strings.Cut(object, ":")
	edgeIDs, deviceIDs, sessionIDs := propertyIDs()
	switch w.fault {
	case propertyAllFault:
		return objType == "edge"
	case propertyTenantFault:
		if objType == "edge" {
			return objID == edgeIDs[w.faultTarget%2]
		}
		if objType != "device" || w.registryEdge != w.faultTarget%2 {
			return false
		}
		for i, id := range deviceIDs {
			if id == objID {
				return w.devices[i]
			}
		}
		return false
	case propertyDevicesFault:
		return objType == "device"
	case propertyEachSessionFault:
		return objType == "capture_session"
	case propertySessionWalkFault:
		if objType != "capture_session" || !w.sessions[w.faultTarget%len(sessionIDs)] {
			return false
		}
		for i, id := range sessionIDs {
			if id == objID {
				return i == w.faultTarget%len(sessionIDs) || !w.sessions[i]
			}
		}
		return false
	case propertySessionSyncFault:
		return objType == "capture_session" && w.sessions[w.faultTarget%2] && objID == sessionIDs[w.faultTarget%2]
	case propertyReadFault, propertyWriteFault:
		return object == propertyExpectedObjects()[w.faultTarget]
	case propertyScanFault:
		switch objType {
		case "edge":
			for i, id := range edgeIDs {
				if id == objID {
					return !w.edges[i]
				}
			}
		case "device":
			for i, id := range deviceIDs {
				if id == objID {
					return !w.devices[i] || !w.edges[w.registryEdge]
				}
			}
		case "capture_session":
			for i, id := range sessionIDs {
				if id == objID {
					return !w.sessions[i]
				}
			}
		}
		return false
	default:
		return false
	}
}

type propertyPredicateSignature struct {
	affected [6]bool
	known    [6]bool
}

func propertyPredicateSignatureAt(w propertyWorld, objects []string) propertyPredicateSignature {
	var signature propertyPredicateSignature
	for i, object := range objects {
		signature.affected[i] = propertyAffected(w, object)
		signature.known[i] = propertyRecordKnown(w, object)
	}
	return signature
}

func propertyAssert(t *testing.T, w propertyWorld, result propertyRunResult) {
	t.Helper()
	desired := propertyDesiredTuples(w)
	before := propertyOwned(propertyInitialTuples(w))
	after := propertyOwned(result.tuples)
	for object, tuples := range before {
		for _, tuple := range tuples {
			if slices.Contains(after[object], tuple) {
				continue
			}
			if !propertyRecordKnown(w, object) {
				t.Errorf("fault %s deleted %v after an unknown record read", propertyFaultNames[w.fault], tuple)
			}
			if slices.Contains(desired[object], tuple) {
				t.Errorf("fault %s deleted desired tuple %v", propertyFaultNames[w.fault], tuple)
			}
		}
	}

	gotUnowned := propertyUnowned(result.tuples)
	wantUnowned := propertyInitialUnowned(w)
	if !slices.Equal(gotUnowned, wantUnowned) {
		t.Errorf("fault %s changed unowned tuples: got %v, want %v", propertyFaultNames[w.fault], gotUnowned, wantUnowned)
	}

	if !result.errorPresent {
		want := propertyInitialUnowned(w)
		for _, tuples := range desired {
			want = append(want, tuples...)
		}
		slices.SortFunc(want, compareTuples)
		if !slices.Equal(result.tuples, want) {
			t.Errorf("no-failure pass tuples = %v, want %v", result.tuples, want)
		}
		if result.reconciledCalls != 0 {
			t.Errorf("successful direct pass called Reconciled %d times, want 0", result.reconciledCalls)
		}
		return
	}
	if result.reconciledCalls != 0 {
		t.Errorf("fault %s called Reconciled %d times, want 0", propertyFaultNames[w.fault], result.reconciledCalls)
	}
	for _, object := range propertyExpectedObjects() {
		if propertyAffected(w, object) {
			continue
		}
		if !slices.Equal(after[object], desired[object]) {
			t.Errorf("fault %s left unaffected %s = %v, want %v", propertyFaultNames[w.fault], object, after[object], desired[object])
		}
	}
}

func propertyUnowned(tuples []authz.Tuple) []authz.Tuple {
	var unowned []authz.Tuple
	for _, tuple := range tuples {
		if !propertyIsOwnedTuple(tuple) {
			unowned = append(unowned, tuple)
		}
	}
	slices.SortFunc(unowned, compareTuples)
	return unowned
}

func propertyInitialUnowned(w propertyWorld) []authz.Tuple {
	edgeIDs, deviceIDs, sessionIDs := propertyIDs()
	objects := []authz.Tuple{
		{Object: "edge:" + edgeIDs[0], Relation: "capture", User: "user:edge-grant"},
		{Object: "device:" + deviceIDs[0], Relation: "capture", User: "user:device-grant"},
		{Object: "capture_session:" + sessionIDs[0], Relation: "capture", User: "user:session-grant"},
	}
	var grants []authz.Tuple
	for i, tuple := range objects {
		if w.grants[i] {
			grants = append(grants, tuple)
		}
	}
	slices.SortFunc(grants, compareTuples)
	return grants
}

func compareTuples(a, b authz.Tuple) int {
	if a.Object != b.Object {
		return strings.Compare(a.Object, b.Object)
	}
	if a.Relation != b.Relation {
		return strings.Compare(a.Relation, b.Relation)
	}
	return strings.Compare(a.User, b.User)
}

func propertyFaultProbe(fault propertyFault) propertyWorld {
	w := propertyWorld{
		edges:        [2]bool{false, true},
		devices:      [2]bool{true, true},
		sessions:     [2]bool{true, true},
		edgeTuples:   [2]propertyTupleState{propertyTupleWrong, propertyTupleWrong},
		deviceTuples: [2]propertyTupleState{propertyTupleWrong, propertyTupleWrong},
		grants:       [3]bool{true, true, true},
		registryEdge: 0,
		sessionEdges: [2]int{0, 1},
		fault:        fault,
	}
	for i := range w.sessionTuples {
		w.sessionTuples[i] = [3]propertyTupleState{propertyTupleWrong, propertyTupleWrong, propertyTupleWrong}
	}
	if fault == propertySessionWalkFault {
		w.faultTarget = 1
		w.sessionTuples[1] = [3]propertyTupleState{propertyTupleAbsent, propertyTuplePresent, propertyTuplePresent}
	}
	return w
}

func TestReconcileGeneratedWorldsPreserveTuplesOnReadFailure(t *testing.T) {
	runCount := 0
	run := func(w propertyWorld) propertyRunResult {
		runCount++
		result := propertyRun(t, w)
		propertyAssert(t, w, result)
		return result
	}

	seenFaults := make(map[propertyFault]bool)
	seenWorlds := make(map[propertyWorld]struct{}, propertyWorldCount)
	seenRecordLayouts := make(map[uint8]bool, 1<<6)
	predicateSignatures := [len(propertyFaultNames)]map[propertyPredicateSignature]struct{}{}
	for fault := range predicateSignatures {
		predicateSignatures[fault] = make(map[propertyPredicateSignature]struct{})
	}
	objects := propertyExpectedObjects()
	walkFaultWorldSeen := false
	faultCounts := [len(propertyFaultNames)]int{}
	faultTargets := [len(propertyFaultNames)][6]bool{}
	for index := 0; index < propertyWorldCount; index++ {
		world := propertyWorldAt(index)
		seenWorlds[world] = struct{}{}
		var recordLayout uint8
		for i, present := range world.edges {
			if present {
				recordLayout |= 1 << i
			}
		}
		for i, present := range world.devices {
			if present {
				recordLayout |= 1 << (len(world.edges) + i)
			}
		}
		for i, present := range world.sessions {
			if present {
				recordLayout |= 1 << (len(world.edges) + len(world.devices) + i)
			}
		}
		seenRecordLayouts[recordLayout] = true
		predicateSignatures[world.fault][propertyPredicateSignatureAt(world, objects)] = struct{}{}
		if world.fault == propertySessionWalkFault {
			target := world.faultTarget % len(world.sessions)
			other := (target + 1) % len(world.sessions)
			if world.sessions[target] && !world.sessions[other] {
				for _, state := range world.sessionTuples[other] {
					if state == propertyTuplePresent || state == propertyTupleWrong {
						walkFaultWorldSeen = true
						break
					}
				}
			}
		}
		faultCounts[world.fault]++
		faultTargets[world.fault][world.faultTarget] = true
		seenFaults[world.fault] = true
		run(world)
	}
	if len(seenWorlds) != propertyWorldCount {
		t.Fatalf("generated %d distinct worlds, want %d", len(seenWorlds), propertyWorldCount)
	}
	if len(seenRecordLayouts) != 64 {
		t.Fatalf("generated %d record layouts, want 64", len(seenRecordLayouts))
	}
	if !walkFaultWorldSeen {
		t.Fatal("generated no session walk fault with a present target, absent other session, and stale other-session tuple")
	}
	wantFaultCounts := [len(propertyFaultNames)]int{42, 64, 42, 64, 42, 42, 64, 42, 64, 46}
	if faultCounts != wantFaultCounts {
		t.Fatalf("fault counts = %v, want %v", faultCounts, wantFaultCounts)
	}
	wantPredicateSignatureCounts := [len(propertyFaultNames)]int{1, 4, 1, 8, 1, 1, 3, 6, 52, 6}
	for fault, want := range wantPredicateSignatureCounts {
		if got := len(predicateSignatures[fault]); got != want {
			t.Fatalf("fault %s predicate signatures = %d, want %d", propertyFaultNames[fault], got, want)
		}
	}
	for _, fault := range []propertyFault{propertyReadFault, propertyWriteFault} {
		for target, seen := range faultTargets[fault] {
			if !seen {
				t.Errorf("fault %s did not target object %d", propertyFaultNames[fault], target)
			}
		}
	}
	for fault := propertySessionWalkFault; fault <= propertyWriteFault; fault++ {
		if !seenFaults[fault] {
			t.Errorf("generated worlds did not exercise fault %s", propertyFaultNames[fault])
		}
	}

	for _, fault := range []propertyFault{
		propertySessionWalkFault,
		propertyAllFault,
		propertyTenantFault,
		propertyDevicesFault,
		propertyEachSessionFault,
		propertySessionSyncFault,
		propertyReadFault,
		propertyScanFault,
		propertyWriteFault,
	} {
		base := propertyFaultProbe(propertyNoFault)
		variant := propertyFaultProbe(fault)
		baseResult := run(base)
		variantResult := run(variant)
		if propertySameResult(baseResult, variantResult) {
			t.Errorf("fault dimension %s did not change behavior", propertyFaultNames[fault])
		}
	}

	statePairs := []struct {
		name    string
		base    propertyWorld
		variant propertyWorld
	}{
		{
			name:    "edge record",
			base:    propertyFaultProbe(propertyNoFault),
			variant: propertyFaultProbe(propertyNoFault),
		},
		{
			name:    "device record",
			base:    propertyFaultProbe(propertyNoFault),
			variant: propertyFaultProbe(propertyNoFault),
		},
		{
			name:    "session record",
			base:    propertyFaultProbe(propertyNoFault),
			variant: propertyFaultProbe(propertyNoFault),
		},
		{
			name:    "owned tuple",
			base:    propertyFaultProbe(propertyNoFault),
			variant: propertyFaultProbe(propertyNoFault),
		},
		{
			name:    "unowned grant",
			base:    propertyFaultProbe(propertyNoFault),
			variant: propertyFaultProbe(propertyNoFault),
		},
	}
	statePairs[0].base.edges[0] = false
	statePairs[0].variant.edges[0] = true
	statePairs[1].base.edges[0] = true
	statePairs[1].variant.edges[0] = true
	statePairs[1].base.devices[0] = false
	statePairs[1].variant.devices[0] = true
	statePairs[2].base.sessions[0] = false
	statePairs[2].variant.sessions[0] = true
	statePairs[3].base.edges[0] = true
	statePairs[3].variant.edges[0] = true
	statePairs[3].base.edgeTuples[0] = propertyTupleAbsent
	statePairs[3].variant.edgeTuples[0] = propertyTupleWrong
	statePairs[4].base.grants[0] = false
	statePairs[4].variant.grants[0] = true
	for _, pair := range statePairs {
		baseResult := run(pair.base)
		variantResult := run(pair.variant)
		sameWrites := slices.Equal(baseResult.writes, variantResult.writes)
		sameDeletes := slices.Equal(baseResult.deletes, variantResult.deletes)
		sameCounts := baseResult.counts == variantResult.counts
		sameErrors := baseResult.errorPresent == variantResult.errorPresent
		sameTuples := slices.Equal(baseResult.tuples, variantResult.tuples)
		if pair.name == "unowned grant" {
			if !sameWrites || !sameDeletes || !sameCounts || !sameErrors || sameTuples {
				t.Errorf("state dimension %s changed more than the seeded grant: writes equal %v, deletes equal %v, counts equal %v, errors equal %v, tuples equal %v", pair.name, sameWrites, sameDeletes, sameCounts, sameErrors, sameTuples)
			}
			continue
		}
		if pair.name == "owned tuple" {
			if !sameWrites || sameDeletes || !sameCounts || !sameErrors || !sameTuples {
				t.Errorf("state dimension %s produced writes equal %v, deletes equal %v, counts equal %v, errors equal %v, tuples equal %v", pair.name, sameWrites, sameDeletes, sameCounts, sameErrors, sameTuples)
			}
			continue
		}
		if propertySameResult(baseResult, variantResult) {
			t.Errorf("state dimension %s did not change writes, deletes, counts, error, or final tuples", pair.name)
		}
	}

	if runCount != 540 {
		t.Fatalf("ran %d worlds, want 540", runCount)
	}
}

func TestReconciledStaysSilentForFailedPropertyPass(t *testing.T) {
	reconciledCalls := 0
	p, _, _ := propertySetup(t, propertyFaultProbe(propertyScanFault), func() {
		reconciledCalls++
	})

	ctx, cancel := context.WithCancel(context.Background())
	p.SetWaitHook(func(waitCtx context.Context, _ time.Duration) error {
		cancel()
		return waitCtx.Err()
	})
	if err := p.Run(ctx); err != nil {
		t.Fatalf("Run returned error after cancellation: %v", err)
	}
	if reconciledCalls != 0 {
		t.Fatalf("Reconciled called %d times after a failed pass, want 0", reconciledCalls)
	}
}
