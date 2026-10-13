package rawwindowapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/ingest/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/ingest/v1/ingestv1connect"
	errsv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/errs/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	rawwindowv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/rawwindow/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/authn"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz"
	"go.aledante.io/FlowSeer/src/services/device/internal/authz/authztest"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
	"go.aledante.io/FlowSeer/src/services/device/internal/rawwindowapi"
)

const (
	testIssuer  = "https://auth.example.test"
	testSubject = "operator-1"
)

// principalInterceptor admits one authenticated principal and leaves the
// tenant header to the client, the way the production authentication
// interceptor feeds the authorization interceptor.
type principalInterceptor struct{ principal authn.Principal }

func (i principalInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return next(authn.NewContext(ctx, i.principal), req)
	}
}

func (i principalInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i principalInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

type fakeRegistry struct {
	edge        string
	integration string
	devices     map[string]bool
}

func (r fakeRegistry) EdgeID() string        { return r.edge }
func (r fakeRegistry) IntegrationID() string { return r.integration }
func (r fakeRegistry) Hosts(_ context.Context, edgeID, deviceID string) (bool, error) {
	return edgeID == r.edge && r.devices[deviceID], nil
}

// operatorHarness serves the operator API over one subscribed edge of tenant A,
// a second edge of the same tenant, and an edge of tenant B.
type operatorHarness struct {
	t         *testing.T
	clock     *testClock
	store     *rawwindowapi.Store
	engine    *authztest.Engine
	principal authn.Principal
	client    ingestv1connect.RawWindowServiceClient

	edge, sameTenantEdge, otherTenantEdge string
	deviceID, integrationID               string

	allowance   atomic.Uint64
	fullPayload atomic.Bool
	notified    atomic.Int64
}

func newOperatorHarness(t *testing.T) *operatorHarness {
	t.Helper()
	h := &operatorHarness{
		t:               t,
		clock:           newClock(),
		engine:          authztest.New(),
		edge:            uuid.NewString(),
		sameTenantEdge:  uuid.NewString(),
		otherTenantEdge: uuid.NewString(),
		deviceID:        uuid.NewString(),
		integrationID:   uuid.NewString(),
	}
	h.store = rawwindowapi.NewStore(testKV, h.clock.Now)
	h.allowance.Store(1000)
	h.fullPayload.Store(true)
	for _, edge := range []string{h.edge, h.sameTenantEdge} {
		if _, err := h.store.Subscribe(t.Context(), tenantA, edge, "", 1, epochOne, sources("syslog")); err != nil {
			t.Fatalf("subscribe %s: %v", edge, err)
		}
	}
	if _, err := h.store.Subscribe(t.Context(), tenantB, h.otherTenantEdge, "", 1, epochOne, sources("syslog")); err != nil {
		t.Fatalf("subscribe the other tenant's edge: %v", err)
	}

	h.principal = authn.Principal{
		ID:      authn.ComputePrincipalID(testIssuer, testSubject),
		Issuer:  testIssuer,
		Subject: testSubject,
		Tenants: []string{tenantA, tenantB},
	}
	h.engine.Grant("user:"+h.principal.ID, "member", "tenant")
	h.engine.Grant("user:"+h.principal.ID, "full_payload", "tenant")
	h.engine.Grant("tenant:"+tenantA, "tenant", "edge")
	h.engine.Grant("tenant:"+tenantB, "tenant", "edge")
	h.allowCapture(h.edge)
	h.allowCapture(h.otherTenantEdge)

	svc := rawwindowapi.NewOperatorService(h.store, rawwindowapi.OperatorServiceConfig{
		EdgeTenant: func(_ context.Context, edgeID string) (string, error) {
			switch edgeID {
			case h.edge, h.sameTenantEdge:
				return tenantA, nil
			case h.otherTenantEdge:
				return tenantB, nil
			default:
				return "", errs.New().Code(edgestore.ErrCodeUnknownEdge).Msg("edge not found")
			}
		},
		FullPayload: func(context.Context, string, *identityv1.OperatorRef) (bool, error) {
			return h.fullPayload.Load(), nil
		},
		Registry: fakeRegistry{edge: h.edge, integration: h.integrationID, devices: map[string]bool{h.deviceID: true}},
		Allowance: func() (uint64, bool) {
			n := h.allowance.Load()
			return n, n > 0
		},
		NotifyChange: func() { h.notified.Add(1) },
	})
	path, handler := ingestv1connect.NewRawWindowServiceHandler(svc,
		connect.WithInterceptors(principalInterceptor{h.principal}, authz.NewInterceptor(h.engine)))
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	h.client = ingestv1connect.NewRawWindowServiceClient(server.Client(), server.URL)
	return h
}

func (h *operatorHarness) allowCapture(edge string) {
	h.t.Helper()
	err := h.engine.Write(h.t.Context(), []authz.Tuple{{Object: "edge:" + edge, Relation: "capture", User: "user:" + h.principal.ID}}, nil)
	if err != nil {
		h.t.Fatalf("grant capture on %s: %v", edge, err)
	}
}

func (h *operatorHarness) openRequest(edge string) *apiingestv1.OpenRawWindowRequest {
	h.t.Helper()
	req := apiingestv1.OpenRawWindowRequest_builder{
		Edge:      edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: new(edge)}.Build()}.Build(),
		Selector:  sourceSelector("syslog"),
		ExpiresAt: timestamppb.New(h.clock.Now().Add(30 * time.Minute)),
		MaxBytes:  new(uint64(100)),
		Authorization: rawwindowv1.RawWindowAuthorization_builder{
			Reason: new("reproduce the parse failure"),
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(req); err != nil {
		h.t.Fatalf("open request violates its schema: %v", err)
	}
	return req
}

func inTenant[T any](tenantID string, msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set("X-FlowSeer-Tenant", tenantID)
	return req
}

func (h *operatorHarness) open(req *apiingestv1.OpenRawWindowRequest) (*rawwindowv1.RawWindowRecord, error) {
	resp, err := h.client.OpenRawWindow(h.t.Context(), inTenant(tenantA, req))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetWindow(), nil
}

func (h *operatorHarness) mustOpen(req *apiingestv1.OpenRawWindowRequest) *rawwindowv1.RawWindowRecord {
	h.t.Helper()
	window, err := h.open(req)
	if err != nil {
		h.t.Fatalf("open: %v", err)
	}
	return window
}

func (h *operatorHarness) get(tenantID, id string) (*rawwindowv1.RawWindowRecord, error) {
	req := apiingestv1.GetRawWindowRequest_builder{RawWindow: rawwindowv1.RawWindowLocalRef_builder{Id: new(id)}.Build()}.Build()
	resp, err := h.client.GetRawWindow(h.t.Context(), inTenant(tenantID, req))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetWindow(), nil
}

func (h *operatorHarness) close(tenantID, id string) (*rawwindowv1.RawWindowRecord, error) {
	req := apiingestv1.CloseRawWindowRequest_builder{RawWindow: rawwindowv1.RawWindowLocalRef_builder{Id: new(id)}.Build()}.Build()
	resp, err := h.client.CloseRawWindow(h.t.Context(), inTenant(tenantID, req))
	if err != nil {
		return nil, err
	}
	return resp.Msg.GetWindow(), nil
}

func (h *operatorHarness) storedWindows(edge string) int {
	h.t.Helper()
	count := 0
	for _, tenantID := range []string{tenantA, tenantB} {
		entry, err := testKV.Get(h.t.Context(), tenantID+"."+edge)
		if err != nil {
			continue
		}
		rec := &storev1.RawWindowEdgeRecord{}
		if err := proto.Unmarshal(entry.Value(), rec); err != nil {
			h.t.Fatalf("decode: %v", err)
		}
		count += len(rec.GetWindows())
	}
	return count
}

func wantConnectCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %v", want)
	}
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("code = %v (%v), want %v", got, err, want)
	}
}

// payloadCode is the errs code a Connect error carries as its detail.
func payloadCode(t *testing.T, err error) string {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error %v is not a Connect error", err)
	}
	for _, detail := range connectErr.Details() {
		msg, detailErr := detail.Value()
		if detailErr != nil {
			continue
		}
		if payload, ok := msg.(*errsv1.ErrorPayload); ok {
			return payload.GetCode()
		}
	}
	t.Fatalf("error %v carries no error payload", err)
	return ""
}

func TestOpenRawWindowStampsThePrincipalOverAForgedRequestedBy(t *testing.T) {
	h := newOperatorHarness(t)
	req := h.openRequest(h.edge)
	req.GetAuthorization().SetRequestedBy(identityv1.OperatorRef_builder{
		Issuer: new("https://evil.example.test"), Subject: new("someone-else"),
	}.Build())

	window := h.mustOpen(req)

	got := window.GetConfig().GetAuthorization().GetRequestedBy()
	if got.GetIssuer() != testIssuer || got.GetSubject() != testSubject {
		t.Errorf("requested_by = %v, want the authenticated %s %s", got, testIssuer, testSubject)
	}
	stored, err := h.get(tenantA, windowID(window))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !proto.Equal(stored.GetConfig().GetAuthorization().GetRequestedBy(), got) {
		t.Errorf("stored requested_by = %v, want %v", stored.GetConfig().GetAuthorization().GetRequestedBy(), got)
	}
	if err := protovalidate.Validate(window); err != nil {
		t.Errorf("returned window violates its schema: %v", err)
	}
	if window.GetState().GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING {
		t.Errorf("lifecycle = %v, want pending", window.GetState().GetLifecycle())
	}
}

func TestOpenRawWindowRefusesAnEdgeOfAnotherTenantOrAnUnknownOne(t *testing.T) {
	h := newOperatorHarness(t)
	h.allowCapture(h.sameTenantEdge)
	for name, edge := range map[string]string{"another tenant's edge": h.otherTenantEdge, "an unknown edge": uuid.NewString()} {
		t.Run(name, func(t *testing.T) {
			h.allowCapture(edge)

			_, err := h.open(h.openRequest(edge))

			wantConnectCode(t, err, connect.CodeNotFound)
		})
	}
	if got := h.storedWindows(h.otherTenantEdge); got != 0 {
		t.Errorf("the other tenant's edge holds %d windows, want 0", got)
	}
}

func TestOpenRawWindowRequiresCaptureAccessOnTheEdge(t *testing.T) {
	h := newOperatorHarness(t)
	h.allowCapture(h.sameTenantEdge)
	if err := h.engine.Write(t.Context(), nil, []authz.Tuple{{Object: "edge:" + h.sameTenantEdge, Relation: "capture", User: "user:" + h.principal.ID}}); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	_, err := h.open(h.openRequest(h.sameTenantEdge))

	wantConnectCode(t, err, connect.CodePermissionDenied)
	if got := h.storedWindows(h.sameTenantEdge); got != 0 {
		t.Errorf("a refused open stored %d windows, want 0", got)
	}
}

func TestOpenRawWindowFullPayloadRequirements(t *testing.T) {
	t.Run("no tuple", func(t *testing.T) {
		h := newOperatorHarness(t)
		h.engine.Reset()
		h.engine.Grant("user:"+h.principal.ID, "member", "tenant")
		h.engine.Grant("tenant:"+tenantA, "tenant", "edge")
		h.allowCapture(h.edge)

		_, err := h.open(h.openRequest(h.edge))

		wantConnectCode(t, err, connect.CodePermissionDenied)
		if got := h.storedWindows(h.edge); got != 0 {
			t.Errorf("a refused open stored %d windows, want 0", got)
		}
	})

	t.Run("a grant that expired while its tuple remains", func(t *testing.T) {
		h := newOperatorHarness(t)
		h.fullPayload.Store(false)

		_, err := h.open(h.openRequest(h.edge))

		wantConnectCode(t, err, connect.CodePermissionDenied)
		if got := payloadCode(t, err); got != "rawwindowapi/full-payload-expired" {
			t.Errorf("error code = %s, want rawwindowapi/full-payload-expired", got)
		}
		if got := h.storedWindows(h.edge); got != 0 {
			t.Errorf("a refused open stored %d windows, want 0", got)
		}
	})

	t.Run("an active grant", func(t *testing.T) {
		h := newOperatorHarness(t)

		h.mustOpen(h.openRequest(h.edge))
	})
}

func TestOpenRawWindowReadsTheGrantFreshOnEveryCall(t *testing.T) {
	h := newOperatorHarness(t)
	h.mustOpen(h.openRequest(h.edge))

	h.fullPayload.Store(false)
	_, err := h.open(h.openRequest(h.edge))

	wantConnectCode(t, err, connect.CodePermissionDenied)
	if got := h.storedWindows(h.edge); got != 1 {
		t.Errorf("stored windows = %d, want only the first", got)
	}
}

func TestOpenRawWindowRefusesAnUnsupportedSource(t *testing.T) {
	h := newOperatorHarness(t)
	req := h.openRequest(h.edge)
	req.SetSelector(sourceSelector("netflow"))

	_, err := h.open(req)

	wantConnectCode(t, err, connect.CodeInvalidArgument)
	if got := payloadCode(t, err); got != "rawwindowapi/unsupported-source" {
		t.Errorf("error code = %s, want rawwindowapi/unsupported-source", got)
	}
}

func TestOpenRawWindowPlacesDeviceAndIntegrationSelectorsThroughTheRegistry(t *testing.T) {
	h := newOperatorHarness(t)
	device := func(id string) *rawwindowv1.RawWindowSelector {
		return rawwindowv1.RawWindowSelector_builder{Device: inventoryv1.DeviceGlobalRef_builder{
			Device: inventoryv1.DeviceLocalRef_builder{Id: new(id)}.Build(),
		}.Build()}.Build()
	}
	integration := func(id string) *rawwindowv1.RawWindowSelector {
		return rawwindowv1.RawWindowSelector_builder{Integration: inventoryv1.IntegrationGlobalRef_builder{
			Integration: inventoryv1.IntegrationLocalRef_builder{Id: new(id)}.Build(),
		}.Build()}.Build()
	}
	cases := []struct {
		name     string
		edge     string
		selector *rawwindowv1.RawWindowSelector
		want     connect.Code
	}{
		{name: "a hosted device", edge: h.edge, selector: device(h.deviceID)},
		{name: "a device the edge does not host", edge: h.edge, selector: device(uuid.NewString()), want: connect.CodeNotFound},
		{name: "a device on an edge the registry does not describe", edge: h.sameTenantEdge, selector: device(h.deviceID), want: connect.CodeNotFound},
		{name: "the registry's integration", edge: h.edge, selector: integration(h.integrationID)},
		{name: "another integration", edge: h.edge, selector: integration(uuid.NewString()), want: connect.CodeNotFound},
		{name: "the integration on an edge the registry does not describe", edge: h.sameTenantEdge, selector: integration(h.integrationID), want: connect.CodeNotFound},
	}
	h.allowCapture(h.sameTenantEdge)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := h.openRequest(tc.edge)
			req.SetSelector(tc.selector)

			_, err := h.open(req)

			if tc.want == 0 {
				if err != nil {
					t.Fatalf("open: %v", err)
				}
				return
			}
			wantConnectCode(t, err, tc.want)
		})
	}
}

func TestOpenRawWindowExpiryBoundaryThroughTheAPI(t *testing.T) {
	h := newOperatorHarness(t)
	h.allowance.Store(1_000_000)
	now := h.clock.Now()

	for _, tc := range []struct {
		name    string
		expires time.Time
		want    connect.Code
	}{
		{name: "exactly one hour", expires: now.Add(time.Hour)},
		{name: "one tick beyond one hour", expires: now.Add(time.Hour + time.Nanosecond), want: connect.CodeInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := h.openRequest(h.edge)
			req.SetExpiresAt(timestamppb.New(tc.expires))

			_, err := h.open(req)

			if tc.want == 0 {
				if err != nil {
					t.Fatalf("open: %v", err)
				}
				return
			}
			wantConnectCode(t, err, tc.want)
			if got := payloadCode(t, err); got != "rawwindowapi/bad-expiry" {
				t.Errorf("error code = %s, want rawwindowapi/bad-expiry", got)
			}
		})
	}
}

func TestOpenRawWindowWithoutAnAllowanceIsRefused(t *testing.T) {
	h := newOperatorHarness(t)
	h.allowance.Store(0)

	_, err := h.open(h.openRequest(h.edge))

	wantConnectCode(t, err, connect.CodeFailedPrecondition)
	if got := payloadCode(t, err); got != "rawwindowapi/no-allowance" {
		t.Errorf("error code = %s, want rawwindowapi/no-allowance", got)
	}
	if got := h.storedWindows(h.edge); got != 0 {
		t.Errorf("a refused open stored %d windows, want 0", got)
	}
}

func TestOpenRawWindowBoundsTheBudgetByTheAllowance(t *testing.T) {
	h := newOperatorHarness(t)
	h.allowance.Store(10)
	budget := func(n uint64) *apiingestv1.OpenRawWindowRequest {
		req := h.openRequest(h.edge)
		req.SetMaxBytes(n)
		return req
	}
	h.mustOpen(budget(6))
	h.mustOpen(budget(4))

	_, err := h.open(budget(1))

	wantConnectCode(t, err, connect.CodeResourceExhausted)
}

// A refused open does not reach the relay, and a successful open and a
// successful close each do. It does not cover a close that changes nothing:
// the handler notifies on every close that loads the window.
func TestARefusedOpenDoesNotNotifyTheRelayButAnOpenAndACloseDo(t *testing.T) {
	h := newOperatorHarness(t)
	h.fullPayload.Store(false)
	if _, err := h.open(h.openRequest(h.edge)); err == nil {
		t.Fatal("open with an expired grant succeeded")
	}
	if got := h.notified.Load(); got != 0 {
		t.Fatalf("a refused open notified the relay %d times, want 0", got)
	}

	h.fullPayload.Store(true)
	window := h.mustOpen(h.openRequest(h.edge))
	if got := h.notified.Load(); got != 1 {
		t.Fatalf("notifications after an open = %d, want 1", got)
	}

	if _, err := h.close(tenantA, windowID(window)); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := h.notified.Load(); got != 2 {
		t.Errorf("notifications after a close = %d, want 2", got)
	}
}

func TestGetRawWindowRequiresCaptureAccessOnTheStoredEdge(t *testing.T) {
	h := newOperatorHarness(t)
	h.allowCapture(h.sameTenantEdge)
	own := h.mustOpen(h.openRequest(h.edge))
	// The same tenant's second edge, opened while access was granted, and
	// revoked afterwards.
	other := h.mustOpen(h.openRequest(h.sameTenantEdge))
	if err := h.engine.Write(t.Context(), nil, []authz.Tuple{{Object: "edge:" + h.sameTenantEdge, Relation: "capture", User: "user:" + h.principal.ID}}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	queriesBefore := len(h.engine.Queries())

	got, err := h.get(tenantA, windowID(own))
	if err != nil {
		t.Fatalf("get of the permitted edge's window: %v", err)
	}
	if windowID(got) != windowID(own) {
		t.Errorf("get returned window %s, want %s", windowID(got), windowID(own))
	}
	_, err = h.get(tenantA, windowID(other))
	wantConnectCode(t, err, connect.CodePermissionDenied)

	checked := map[string]bool{}
	for _, q := range h.engine.Queries()[queriesBefore:] {
		if q.Relation == "capture" {
			checked[q.Object] = true
		}
	}
	for _, edge := range []string{h.edge, h.sameTenantEdge} {
		if !checked["edge:"+edge] {
			t.Errorf("no capture check on the stored edge %s; checked %v", edge, checked)
		}
	}
}

func TestGetAndCloseOfAnotherTenantsOrAnUnknownWindowAreNotFound(t *testing.T) {
	h := newOperatorHarness(t)
	theirs, err := h.store.Open(t.Context(), tenantB, rawwindowapi.OpenRequest{
		EdgeID: h.otherTenantEdge, Selector: sourceSelector("syslog"), ExpiresAt: h.clock.Now().Add(time.Minute),
		MaxBytes: 1, Authorization: authorization("theirs"), AllowanceBytes: 10,
	})
	if err != nil {
		t.Fatalf("open the other tenant's window: %v", err)
	}

	for name, id := range map[string]string{"another tenant's window": windowID(theirs), "an unknown window": uuid.NewString()} {
		t.Run(name, func(t *testing.T) {
			_, err := h.get(tenantA, id)
			wantConnectCode(t, err, connect.CodeNotFound)

			_, err = h.close(tenantA, id)
			wantConnectCode(t, err, connect.CodeNotFound)
		})
	}
	if state, err := h.store.Window(t.Context(), tenantB, windowID(theirs)); err != nil || state.GetState().GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING {
		t.Errorf("the other tenant's window = %v, %v, want it untouched and pending", state, err)
	}

	// The same holds in the other direction: tenant B's call cannot reach a
	// window of tenant A.
	mine := h.mustOpen(h.openRequest(h.edge))
	_, err = h.get(tenantB, windowID(mine))
	wantConnectCode(t, err, connect.CodeNotFound)
	_, err = h.close(tenantB, windowID(mine))
	wantConnectCode(t, err, connect.CodeNotFound)
	if got, err := h.get(tenantA, windowID(mine)); err != nil || got.GetState().GetLifecycle() != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING {
		t.Errorf("tenant A's own window = %v, %v, want it untouched", got, err)
	}
}

func TestCloseRawWindowIsIdempotent(t *testing.T) {
	h := newOperatorHarness(t)
	window := h.mustOpen(h.openRequest(h.edge))

	first, err := h.close(tenantA, windowID(window))
	if err != nil {
		t.Fatalf("first close: %v", err)
	}
	revisionAfterFirst := currentRevision(t, h, window)
	second, err := h.close(tenantA, windowID(window))
	if err != nil {
		t.Fatalf("second close: %v", err)
	}

	if !proto.Equal(first, second) {
		t.Errorf("closing twice returned %v then %v, want the same window", first, second)
	}
	if got := currentRevision(t, h, window); got != revisionAfterFirst {
		t.Errorf("the second close moved the window revision from %d to %d", revisionAfterFirst, got)
	}
	if got := first.GetState().GetLifecycle(); got != rawwindowv1.RawWindowLifecycle_RAW_WINDOW_LIFECYCLE_PENDING {
		t.Errorf("a closed window the edge has not reported is %v, want pending", got)
	}
}

func TestCloseRawWindowOfAnEndedWindowSucceedsAndChangesNothing(t *testing.T) {
	h := newOperatorHarness(t)
	window := h.mustOpen(h.openRequest(h.edge))
	h.clock.Advance(31 * time.Minute)

	got, err := h.close(tenantA, windowID(window))
	if err != nil {
		t.Fatalf("close of an expired window: %v", err)
	}
	if got.GetState().GetStopReason() != rawwindowv1.RawWindowStopReason_RAW_WINDOW_STOP_REASON_EXPIRED {
		t.Errorf("stop reason = %v, want EXPIRED unchanged", got.GetState().GetStopReason())
	}
}

func currentRevision(t *testing.T, h *operatorHarness, window *rawwindowv1.RawWindowRecord) uint64 {
	t.Helper()
	owed, err := h.store.Owed(t.Context(), tenantA, h.edge, 1, epochOne)
	if err != nil {
		t.Fatalf("owed: %v", err)
	}
	for _, control := range owed {
		if control.GetRef().GetRawWindow().GetId() == windowID(window) {
			return control.GetRevision()
		}
	}
	t.Fatalf("window %s is owed nothing", windowID(window))
	return 0
}
