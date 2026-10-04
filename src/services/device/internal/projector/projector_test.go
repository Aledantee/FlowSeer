package projector_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/authztest"
	"go.aledante.io/FlowSeer/src/services/device/internal/projector"
)

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
			return errors.New("simulated session walk failure")
		}
		if err := fn(k.tenantID, rec); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeCaptureSource) Session(_ context.Context, tenantID, sessionID string) (*modelcapturev1.CaptureSessionRecord, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
		session1 = "0192e6a0-0000-7000-8000-0000000000s1"
		session2 = "0192e6a0-0000-7000-8000-0000000000s2"
		tenantID = "0192e6a0-0000-7000-8000-0000000000t1"
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
}

func TestReconcileDriftRestoration(t *testing.T) {
	ctx := context.Background()

	const (
		edgeE   = "0192e6a0-0000-7000-8000-00000000000e"
		edgeX   = "0192e6a0-0000-7000-8000-00000000000x"
		tenantT = "0192e6a0-0000-7000-8000-00000000000t"
		tenant2 = "0192e6a0-0000-7000-8000-0000000000t2"
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
		sessionS = "0192e6a0-0000-7000-8000-00000000000s"
		tenantT  = "0192e6a0-0000-7000-8000-00000000000t"
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
		sessionID = "0192e6a0-0000-7000-8000-00000000000s"
		tenantID  = "0192e6a0-0000-7000-8000-00000000000t"
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
	edges := &fakeEdgeSource{allEdges: map[string]string{edgeID: "tenant-1"}}
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
	edges := &fakeEdgeSource{allEdges: map[string]string{edgeID: "tenant-1"}}
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
	edges := &fakeEdgeSource{allEdges: map[string]string{edgeID: "tenant-1"}}
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
		sessionS = "0192e6a0-0000-7000-8000-00000000000s"
		tenantT  = "0192e6a0-0000-7000-8000-00000000000t"
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
	propertyProbeCount = 540
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
	index         int
	edges         [2]bool
	devices       [2]bool
	sessions      [2]bool
	edgeTuples    [2]propertyTupleState
	deviceTuples  [2]propertyTupleState
	sessionTuples [2][3]propertyTupleState
	grant         bool
	fault         propertyFault
}

type propertyRelations struct {
	*authztest.Engine
	readObject  string
	scanErr     error
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
	w := propertyWorld{
		index: index,
		fault: propertyFault(index % len(propertyFaultNames)),
		grant: index%2 == 0,
	}
	for i := range w.edges {
		w.edges[i] = (index+i)%2 == 0
		w.edgeTuples[i] = propertyTupleState((index + i) % 3)
	}
	for i := range w.devices {
		w.devices[i] = (index+i+1)%2 == 0
		w.deviceTuples[i] = propertyTupleState((index + i + 1) % 3)
	}
	for i := range w.sessions {
		w.sessions[i] = (index+i)%2 == 1
		base := (index/2 + i) % 3
		w.sessionTuples[i] = [3]propertyTupleState{
			propertyTupleState(base),
			propertyTupleState((base + 1) % 3),
			propertyTupleState((base + 1) % 3),
		}
	}

	switch w.fault {
	case propertySessionWalkFault:
		w.sessions[0] = true
		w.sessionTuples[0] = [3]propertyTupleState{propertyTupleAbsent, propertyTuplePresent, propertyTuplePresent}
	case propertyTenantFault:
		w.edges[0] = false
		w.devices[0] = true
	case propertySessionSyncFault:
		w.sessions[0] = true
		w.sessionTuples[0][0] = propertyTupleWrong
	case propertyReadFault, propertyWriteFault:
		w.edgeTuples[0] = propertyTupleWrong
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
		"0192e6a0-0000-7000-8000-0000000000s1",
		"0192e6a0-0000-7000-8000-0000000000s2",
	}
	return edges, devices, sessions
}

func propertyTenants() []string {
	return []string{
		"0192e6a0-0000-7000-8000-0000000000t1",
		"0192e6a0-0000-7000-8000-0000000000t2",
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
			authz.Tuple{Object: "device:" + id, Relation: "tenant", User: "tenant:" + tenantIDs[0]},
			authz.Tuple{Object: "device:" + id, Relation: "tenant", User: "tenant:wrong"})...)
	}
	for i, id := range sessionIDs {
		object := "capture_session:" + id
		principalID := authn.ComputePrincipalID("https://auth.example.com", "subject-"+id)
		desired := []authz.Tuple{
			{Object: object, Relation: "tenant", User: "tenant:" + tenantIDs[i]},
			{Object: object, Relation: "edge", User: "edge:" + edgeIDs[0]},
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
	if w.grant {
		tuples = append(tuples, authz.Tuple{Object: "edge:" + edgeIDs[0], Relation: "capture", User: "user:grant"})
	}
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
		if w.devices[i] && w.edges[0] {
			desired["device:"+id] = []authz.Tuple{{Object: "device:" + id, Relation: "tenant", User: "tenant:" + tenantIDs[0]}}
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
			{Object: object, Relation: "edge", User: "edge:" + edgeIDs[0]},
			{Object: object, Relation: "requester", User: "user:" + principalID},
		}
	}
	for object := range desired {
		slices.SortFunc(desired[object], compareTuples)
	}
	return desired
}

func propertyOwned(tuples []authz.Tuple) map[string][]authz.Tuple {
	owned := make(map[string][]authz.Tuple)
	for _, tuple := range tuples {
		objType, _, ok := strings.Cut(tuple.Object, ":")
		if ok && ((objType == "edge" || objType == "device") && tuple.Relation == "tenant" || objType == "capture_session" && (tuple.Relation == "tenant" || tuple.Relation == "edge" || tuple.Relation == "requester")) {
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
	registry := &fakeRegistrySource{edgeID: edgeIDs[0], devices: make(map[string]*storev1.RegistryDevice)}
	for i, id := range deviceIDs {
		if w.devices[i] {
			registry.devices[id] = buildRegistryDevice(id)
		}
	}
	captures := &fakeCaptureSource{sessions: make(map[sessionKey]*modelcapturev1.CaptureSessionRecord)}
	for i, id := range sessionIDs {
		if w.sessions[i] {
			captures.sessions[sessionKey{tenantID: tenantIDs[i], sessionID: id}] = buildSessionRecord(id, edgeIDs[0], "https://auth.example.com", "subject-"+id)
		}
	}
	switch w.fault {
	case propertyAllFault:
		edges.allErr = errors.New("simulated edge listing failure")
	case propertyTenantFault:
		edges.tenantErr = map[string]error{edgeIDs[0]: errors.New("simulated edge tenant read failure")}
	case propertyDevicesFault:
		registry.devicesErr = errors.New("simulated device listing failure")
	case propertyEachSessionFault:
		captures.eachErr = errors.New("simulated session listing failure")
	case propertySessionWalkFault:
		captures.sessionWalkErrID = sessionIDs[0]
	case propertySessionSyncFault:
		captures.sessionSyncErrID = sessionIDs[0]
	case propertyReadFault:
		// The relationship wrapper carries this fault.
	case propertyScanFault:
		// The relationship wrapper carries this fault.
	case propertyWriteFault:
		// The relationship wrapper carries this fault.
	}
	return edges, registry, captures
}

func propertyRun(t *testing.T, w propertyWorld) propertyRunResult {
	t.Helper()
	base := authztest.New()
	if err := base.Write(context.Background(), propertyInitialTuples(w), nil); err != nil {
		t.Fatalf("seed property world: %v", err)
	}
	relations := &propertyRelations{Engine: base}
	edges, registry, captures := propertySources(w)
	edgeIDs, _, _ := propertyIDs()
	switch w.fault {
	case propertyReadFault:
		relations.readObject = "edge:" + edgeIDs[0]
	case propertyScanFault:
		relations.scanErr = errors.New("simulated relationship scan failure")
	case propertyWriteFault:
		relations.writeObject = "edge:" + edgeIDs[0]
	}
	p := projector.New(relations, edges, registry, captures, 0, nil, nil)
	counts, err := p.Reconcile(context.Background())
	return propertyRunResult{
		errorPresent: err != nil,
		counts:       counts,
		tuples:       readAllTuples(t, base),
		writes:       slices.Clone(relations.writes),
		deletes:      slices.Clone(relations.deletes),
	}
}

type propertyRunResult struct {
	errorPresent bool
	counts       projector.RepairedCounts
	tuples       []authz.Tuple
	writes       []authz.Tuple
	deletes      []authz.Tuple
}

func propertySameResult(a, b propertyRunResult) bool {
	return a.errorPresent == b.errorPresent && a.counts == b.counts && slices.Equal(a.tuples, b.tuples) && slices.Equal(a.writes, b.writes) && slices.Equal(a.deletes, b.deletes)
}

func propertyRecordKnown(w propertyWorld, object string) bool {
	objType, objID, _ := strings.Cut(object, ":")
	_, deviceIDs, sessionIDs := propertyIDs()
	switch w.fault {
	case propertyAllFault:
		return objType != "edge"
	case propertyTenantFault:
		if objType != "device" {
			return true
		}
		for i, id := range deviceIDs {
			if id == objID {
				return !w.devices[i]
			}
		}
		return false
	case propertyDevicesFault:
		return objType != "device"
	case propertyEachSessionFault, propertySessionWalkFault:
		return objType != "capture_session"
	case propertySessionSyncFault:
		return objType != "capture_session" || objID != sessionIDs[0]
	default:
		return true
	}
}

func propertyAffected(w propertyWorld, object string) bool {
	objType, objID, _ := strings.Cut(object, ":")
	edgeIDs, _, sessionIDs := propertyIDs()
	switch w.fault {
	case propertyAllFault:
		return objType == "edge" || objType == "device"
	case propertyTenantFault:
		return (objType == "edge" && objID == edgeIDs[0]) || objType == "device"
	case propertyDevicesFault:
		return objType == "device"
	case propertyEachSessionFault, propertySessionWalkFault:
		return objType == "capture_session"
	case propertySessionSyncFault:
		return objType == "capture_session" && objID == sessionIDs[0]
	case propertyReadFault, propertyWriteFault:
		return objType == "edge" && objID == edgeIDs[0]
	case propertyScanFault:
		return true
	default:
		return false
	}
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
				t.Errorf("world %d fault %s deleted %v after an unknown record read", w.index, propertyFaultNames[w.fault], tuple)
			}
			if slices.Contains(desired[object], tuple) {
				t.Errorf("world %d fault %s deleted desired tuple %v", w.index, propertyFaultNames[w.fault], tuple)
			}
		}
	}

	if w.grant {
		edgeIDs, _, _ := propertyIDs()
		grant := authz.Tuple{Object: "edge:" + edgeIDs[0], Relation: "capture", User: "user:grant"}
		if !slices.Contains(result.tuples, grant) {
			t.Errorf("world %d fault %s changed unowned grant", w.index, propertyFaultNames[w.fault])
		}
	}

	if w.fault == propertyNoFault {
		want := propertyInitialUnowned(w)
		for _, tuples := range desired {
			want = append(want, tuples...)
		}
		slices.SortFunc(want, compareTuples)
		if !slices.Equal(result.tuples, want) {
			t.Errorf("world %d no-failure pass tuples = %v, want %v", w.index, result.tuples, want)
		}
		return
	}
	if !result.errorPresent {
		t.Fatalf("world %d fault %s returned nil error", w.index, propertyFaultNames[w.fault])
	}
	for _, object := range propertyExpectedObjects() {
		if propertyAffected(w, object) {
			continue
		}
		if !slices.Equal(after[object], desired[object]) {
			t.Errorf("world %d fault %s left unaffected %s = %v, want %v", w.index, propertyFaultNames[w.fault], object, after[object], desired[object])
		}
	}
}

func propertyInitialUnowned(w propertyWorld) []authz.Tuple {
	if !w.grant {
		return nil
	}
	edgeIDs, _, _ := propertyIDs()
	return []authz.Tuple{{Object: "edge:" + edgeIDs[0], Relation: "capture", User: "user:grant"}}
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
	w := propertyWorldAt(0)
	w.fault = fault
	w.edges[0] = true
	w.devices[0] = true
	w.sessions[0] = true
	w.edgeTuples[0] = propertyTupleWrong
	w.deviceTuples[0] = propertyTupleWrong
	w.sessionTuples[0] = [3]propertyTupleState{propertyTupleWrong, propertyTupleWrong, propertyTupleWrong}
	if fault == propertyTenantFault {
		w.edges[0] = false
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
	for index := 0; index < propertyWorldCount; index++ {
		world := propertyWorldAt(index)
		seenFaults[world.fault] = true
		run(world)
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
	statePairs[1].base.devices[0] = false
	statePairs[1].variant.devices[0] = true
	statePairs[2].base.sessions[0] = false
	statePairs[2].variant.sessions[0] = true
	statePairs[3].base.edgeTuples[0] = propertyTupleAbsent
	statePairs[3].variant.edgeTuples[0] = propertyTupleWrong
	statePairs[4].base.grant = false
	statePairs[4].variant.grant = true
	for _, pair := range statePairs {
		baseResult := run(pair.base)
		variantResult := run(pair.variant)
		if propertySameResult(baseResult, variantResult) {
			t.Errorf("state dimension %s did not change behavior", pair.name)
		}
	}

	if runCount != propertyProbeCount {
		t.Fatalf("ran %d worlds, want %d", runCount, propertyProbeCount)
	}
}
