package projector_test

import (
	"context"
	"slices"
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
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/openfga"
	"go.aledante.io/FlowSeer/src/services/device/internal/projector"
)

type fakeEdgeSource struct {
	allEdges map[string]string // edgeID -> tenantID
}

func (f *fakeEdgeSource) All(_ context.Context) (map[string]string, error) {
	edges := make(map[string]string, len(f.allEdges))
	for k, v := range f.allEdges {
		edges[k] = v
	}
	return edges, nil
}

func (f *fakeEdgeSource) TenantForEdge(_ context.Context, edgeID string) (string, error) {
	return f.allEdges[edgeID], nil
}

type fakeRegistrySource struct {
	edgeID  string
	devices map[string]*storev1.RegistryDevice
}

func (f *fakeRegistrySource) EdgeID() string {
	return f.edgeID
}

func (f *fakeRegistrySource) Device(deviceID string) (*storev1.RegistryDevice, bool) {
	dev, ok := f.devices[deviceID]
	return dev, ok
}

func (f *fakeRegistrySource) Devices(_ context.Context, edgeID string) ([]string, error) {
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
	mu       sync.Mutex
	sessions map[sessionKey]*modelcapturev1.CaptureSessionRecord
}

func (f *fakeCaptureSource) EachSession(_ context.Context, fn func(tenantID string, rec *modelcapturev1.CaptureSessionRecord) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, rec := range f.sessions {
		if err := fn(k.tenantID, rec); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeCaptureSource) Session(_ context.Context, tenantID, sessionID string) (*modelcapturev1.CaptureSessionRecord, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func TestDecisionsTable(t *testing.T) {
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

func TestRequirement9DriftCases(t *testing.T) {
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
		if err := engine.Write(ctx, []authz.Tuple{trueTuple, staleTuple}, nil); err != nil {
			t.Fatalf("Write: %v", err)
		}

		edges := &fakeEdgeSource{allEdges: map[string]string{edgeE: tenantT}}
		p := projector.New(engine, edges, nil, &fakeCaptureSource{}, 0, nil, nil)

		// Assert before: holds both
		before := readAllTuples(t, engine)
		if len(before) != 2 {
			t.Fatalf("engine before pass = %v, want 2 tuples", before)
		}

		counts, err := p.Reconcile(ctx)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if counts.Edges != 1 {
			t.Fatalf("counts.Edges = %d, want 1", counts.Edges)
		}

		// Assert after: staleTuple deleted, trueTuple preserved
		after := readAllTuples(t, engine)
		want := []authz.Tuple{trueTuple}
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

	// Edge was added by snapshot reconcile
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
	failCode   errs.Code
}

func (r *retryEngine) Write(ctx context.Context, writes, deletes []authz.Tuple) error {
	r.mu.Lock()
	r.writeCount++
	count := r.writeCount
	code := r.failCode
	r.mu.Unlock()

	if count == 1 && code != "" {
		return errs.New().Code(code).Msg("simulated write failure")
	}
	return r.Engine.Write(ctx, writes, deletes)
}

func TestWriteConflictRetried(t *testing.T) {
	ctx := context.Background()
	baseEngine := authztest.New()
	retEngine := &retryEngine{
		Engine:   baseEngine,
		failCode: openfga.ErrCodeConflict,
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

	tuples, err := baseEngine.Read(ctx, "edge:"+edgeID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(tuples) != 1 {
		t.Fatalf("got %d tuples after retried write, want 1", len(tuples))
	}
}

func TestWriteUnreachableReturnsError(t *testing.T) {
	ctx := context.Background()
	baseEngine := authztest.New()
	retEngine := &retryEngine{
		Engine:   baseEngine,
		failCode: openfga.ErrCodeUnreachable,
	}

	const edgeID = "0192e6a0-0000-7000-8000-00000000000e"
	edges := &fakeEdgeSource{allEdges: map[string]string{edgeID: "tenant-1"}}
	p := projector.New(retEngine, edges, nil, &fakeCaptureSource{}, 0, nil, nil)

	err := p.Sync(ctx, projector.Object{Type: "edge", ID: edgeID})
	if err == nil {
		t.Fatal("Sync succeeded, want ErrCodeUnreachable")
	}
	code, ok := errs.CodeOf(err)
	if !ok || code != openfga.ErrCodeUnreachable {
		t.Fatalf("err code = %v, want %v", code, openfga.ErrCodeUnreachable)
	}
}

func TestRunLifecycle(t *testing.T) {
	engine := authztest.New()
	// Initially fail with unreachable
	engine.Fail(errs.New().Code(openfga.ErrCodeUnreachable).Msg("engine unavailable"))

	edges := &fakeEdgeSource{allEdges: map[string]string{}}
	reconciledCh := make(chan struct{}, 5)
	p := projector.New(engine, edges, nil, &fakeCaptureSource{}, 20*time.Millisecond, nil, func() {
		select {
		case reconciledCh <- struct{}{}:
		default:
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	spawn.Go(ctx, "test projector Run", func() {
		runErrCh <- p.Run(ctx)
	})

	// Wait briefly while failing - reconciled should not be called
	time.Sleep(50 * time.Millisecond)
	select {
	case <-reconciledCh:
		t.Fatal("reconciled called while engine failing")
	default:
	}

	// Now clear failure; projector should keep going, succeed, and call reconciled
	engine.Fail(nil)

	select {
	case <-reconciledCh:
		// Succeeded!
	case <-time.After(2 * time.Second):
		t.Fatal("reconciled was not called after engine recovered")
	}

	cancel()

	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("Run returned error on cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit on context cancellation")
	}
}
