package edgeapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"regexp"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiedgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1/edgev1connect"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgeapi"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
	"go.aledante.io/FlowSeer/src/services/device/internal/registry"
)

var _ edgev1connect.EdgeAdminServiceHandler = (*edgeapi.AdminService)(nil)

// setupKeyPattern is the schema's own rule for the key string, repeated here
// so a generator that drifts from it fails in this package too.
var setupKeyPattern = regexp.MustCompile(`^fse1_[a-z2-7]{26}_[a-z2-7]{52}$`)

var testClock = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)

// newHub starts the embedded hub the tests run against: it holds the edges
// bucket and mints the bus credentials AttachBus returns, so neither is faked.
func newHub(t *testing.T) *edgebus.Hub {
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
	return hub
}

func newStoreOver(t *testing.T, hub *edgebus.Hub) *edgestore.Store {
	t.Helper()
	kv, err := hub.JetStream().KeyValue(context.Background(), edgebus.EdgeBucket)
	if err != nil {
		t.Fatalf("bucket: %v", err)
	}
	return edgestore.New(kv)
}

func newAdminOver(t *testing.T, store *edgestore.Store, clock func() time.Time) *edgeapi.AdminService {
	t.Helper()
	anchor := make([]byte, 32)
	admin, err := edgeapi.NewAdminService(store, nil, edgeapi.Provisioning{
		CentralURL:   "https://central.example.test",
		TrustAnchors: [][]byte{anchor},
	}, edgeapi.NewContact(0, 0), clock)
	if err != nil {
		t.Fatalf("NewAdminService: %v", err)
	}
	return admin
}

func newAdmin(t *testing.T) (*edgeapi.AdminService, *edgestore.Store) {
	t.Helper()
	store := newStoreOver(t, newHub(t))
	return newAdminOver(t, store, func() time.Time { return testClock }), store
}

func createEdge(t *testing.T, admin *edgeapi.AdminService) (*edgev1.EdgeRecord, string) {
	t.Helper()
	resp, err := admin.CreateEdge(context.Background(), connect.NewRequest(apiedgev1.CreateEdgeRequest_builder{
		Name: proto.String("site-a"),
	}.Build()))
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	if err := protovalidate.Validate(resp.Msg); err != nil {
		t.Fatalf("CreateEdge response fails its schema rules: %v", err)
	}
	return resp.Msg.GetEdge(), resp.Msg.GetProvisioning().GetSetupKey()
}

func refOf(record *edgev1.EdgeRecord) *edgev1.EdgeGlobalRef {
	return record.GetConfig().GetRef()
}

func storedEdge(t *testing.T, store *edgestore.Store, ref *edgev1.EdgeGlobalRef) *storev1.StoredEdge {
	t.Helper()
	stored, _, err := store.Get(context.Background(), ref.GetEdge().GetId())
	if err != nil {
		t.Fatalf("read stored edge: %v", err)
	}
	if stored == nil {
		t.Fatal("edge has no stored record")
	}
	return stored
}

func wantConnectCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("call succeeded; want %v", want)
	}
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("code = %v, want %v (%v)", got, want, err)
	}
}

func TestCreateEdgeShowsTheSetupKeyOnceAndStoresOnlyItsDigest(t *testing.T) {
	admin, store := newAdmin(t)
	record, key := createEdge(t, admin)

	if !setupKeyPattern.MatchString(key) {
		t.Fatalf("setup key %q does not match the key string rule", key)
	}
	if got := record.GetState().GetLifecycle(); got != edgev1.EdgeLifecycle_EDGE_LIFECYCLE_PENDING {
		t.Errorf("lifecycle = %v, want pending", got)
	}
	if got := record.GetState().GetSetupKey().GetStatus(); got != edgev1.SetupKeyStatus_SETUP_KEY_STATUS_ISSUED {
		t.Errorf("status = %v, want issued", got)
	}
	if got, want := record.GetState().GetSetupKey().GetId(), key[len("fse1_"):len("fse1_")+26]; got != want {
		t.Errorf("setup key id = %q, want the key's middle segment %q", got, want)
	}
	if got, want := record.GetState().GetSetupKey().GetExpiresAt().AsTime(), testClock.Add(180*24*time.Hour); !got.Equal(want) {
		t.Errorf("expires_at = %v, want %v", got, want)
	}

	stored := storedEdge(t, store, refOf(record))
	digest := sha256.Sum256([]byte(key))
	if got := stored.GetSetupKeyHash(); string(got) != string(digest[:]) {
		t.Error("stored digest is not the sha-256 of the key string")
	}
	if wire, err := proto.Marshal(stored); err != nil {
		t.Fatalf("marshal stored edge: %v", err)
	} else if bytes.Contains(wire, []byte(key)) {
		t.Fatal("the stored record carries the setup key string")
	}

	got, err := admin.GetEdge(context.Background(), connect.NewRequest(apiedgev1.GetEdgeRequest_builder{Edge: refOf(record)}.Build()))
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if wire, err := proto.Marshal(got.Msg); err != nil {
		t.Fatalf("marshal GetEdge response: %v", err)
	} else if bytes.Contains(wire, []byte(key)) {
		t.Fatal("GetEdge returned the setup key string")
	}
}

func TestEveryIssuedSetupKeyIsDistinct(t *testing.T) {
	admin, _ := newAdmin(t)
	seen := make(map[string]struct{}, 64)
	ids := make(map[string]struct{}, 64)
	for i := 0; i < 64; i++ {
		record, key := createEdge(t, admin)
		if _, dup := seen[key]; dup {
			t.Fatal("two edges were issued the same setup key")
		}
		seen[key] = struct{}{}
		id := record.GetState().GetSetupKey().GetId()
		if _, dup := ids[id]; dup {
			t.Fatal("two setup keys share an identifier")
		}
		ids[id] = struct{}{}
	}
}

func TestCreateEdgeRefusesAnExpiryThatIsNotInTheFuture(t *testing.T) {
	admin, _ := newAdmin(t)
	_, err := admin.CreateEdge(context.Background(), connect.NewRequest(apiedgev1.CreateEdgeRequest_builder{
		SetupKeyExpiresAt: timestamppb.New(testClock.Add(-time.Second)),
	}.Build()))
	wantConnectCode(t, err, connect.CodeInvalidArgument)
}

func TestIssueSetupKeyReplacesTheUnusedKeyAndTheOldDigestIsGone(t *testing.T) {
	admin, store := newAdmin(t)
	record, first := createEdge(t, admin)
	firstDigest := sha256.Sum256([]byte(first))

	resp, err := admin.IssueSetupKey(context.Background(), connect.NewRequest(apiedgev1.IssueSetupKeyRequest_builder{
		Edge: refOf(record),
	}.Build()))
	if err != nil {
		t.Fatalf("IssueSetupKey: %v", err)
	}
	if err := protovalidate.Validate(resp.Msg); err != nil {
		t.Fatalf("IssueSetupKey response fails its schema rules: %v", err)
	}
	second := resp.Msg.GetProvisioning().GetSetupKey()
	if second == first {
		t.Fatal("a re-issue returned the same key")
	}

	stored := storedEdge(t, store, refOf(record))
	if string(stored.GetSetupKeyHash()) == string(firstDigest[:]) {
		t.Fatal("the replaced key still enrolls: its digest is still stored")
	}
	secondDigest := sha256.Sum256([]byte(second))
	if string(stored.GetSetupKeyHash()) != string(secondDigest[:]) {
		t.Fatal("the issued key's digest was not stored")
	}
	if got := stored.GetRecord().GetState().GetSetupKey().GetId(); got != resp.Msg.GetEdge().GetState().GetSetupKey().GetId() {
		t.Errorf("stored key id = %q, want the returned one", got)
	}
}

func TestIssueSetupKeyReturnsARetiredEdgeToPending(t *testing.T) {
	admin, _ := newAdmin(t)
	record, _ := createEdge(t, admin)
	if _, err := admin.RetireEdge(context.Background(), connect.NewRequest(apiedgev1.RetireEdgeRequest_builder{Edge: refOf(record)}.Build())); err != nil {
		t.Fatalf("RetireEdge: %v", err)
	}

	resp, err := admin.IssueSetupKey(context.Background(), connect.NewRequest(apiedgev1.IssueSetupKeyRequest_builder{Edge: refOf(record)}.Build()))
	if err != nil {
		t.Fatalf("IssueSetupKey on a retired edge: %v", err)
	}
	if got := resp.Msg.GetEdge().GetState().GetLifecycle(); got != edgev1.EdgeLifecycle_EDGE_LIFECYCLE_PENDING {
		t.Errorf("lifecycle = %v, want pending", got)
	}
}

func TestIssueSetupKeyRefusesAnEnrolledEdge(t *testing.T) {
	admin, store := newAdmin(t)
	record, _ := createEdge(t, admin)
	enroll(t, store, refOf(record))

	_, err := admin.IssueSetupKey(context.Background(), connect.NewRequest(apiedgev1.IssueSetupKeyRequest_builder{Edge: refOf(record)}.Build()))
	wantConnectCode(t, err, connect.CodeFailedPrecondition)
}

// enroll moves an edge to enrolled the way the enrollment handler will, so the
// admin RPCs can be tested against a live edge before that handler exists.
func enroll(t *testing.T, store *edgestore.Store, ref *edgev1.EdgeGlobalRef) {
	t.Helper()
	if _, err := store.Mutate(context.Background(), ref.GetEdge().GetId(), func(current *storev1.StoredEdge) (*storev1.StoredEdge, error) {
		state := current.GetRecord().GetState()
		state.SetLifecycle(edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED)
		state.SetContact(edgev1.EdgeContact_EDGE_CONTACT_ACTIVE)
		state.SetPublicKey(make([]byte, 32))
		state.GetSetupKey().SetStatus(edgev1.SetupKeyStatus_SETUP_KEY_STATUS_CONSUMED)
		current.ClearSetupKeyHash()
		return current, nil
	}); err != nil {
		t.Fatalf("enroll: %v", err)
	}
}

func TestRevokeSetupKeyClearsTheDigestAndRecordsTheStatus(t *testing.T) {
	admin, store := newAdmin(t)
	record, _ := createEdge(t, admin)

	resp, err := admin.RevokeSetupKey(context.Background(), connect.NewRequest(apiedgev1.RevokeSetupKeyRequest_builder{Edge: refOf(record)}.Build()))
	if err != nil {
		t.Fatalf("RevokeSetupKey: %v", err)
	}
	if err := protovalidate.Validate(resp.Msg); err != nil {
		t.Fatalf("RevokeSetupKey response fails its schema rules: %v", err)
	}
	if got := resp.Msg.GetEdge().GetState().GetSetupKey().GetStatus(); got != edgev1.SetupKeyStatus_SETUP_KEY_STATUS_REVOKED {
		t.Errorf("status = %v, want revoked", got)
	}
	if storedEdge(t, store, refOf(record)).HasSetupKeyHash() {
		t.Fatal("a revoked key still enrolls: its digest is still stored")
	}

	_, err = admin.RevokeSetupKey(context.Background(), connect.NewRequest(apiedgev1.RevokeSetupKeyRequest_builder{Edge: refOf(record)}.Build()))
	wantConnectCode(t, err, connect.CodeFailedPrecondition)
}

func TestRevokeSetupKeyRefusesAConsumedKey(t *testing.T) {
	admin, store := newAdmin(t)
	record, _ := createEdge(t, admin)
	enroll(t, store, refOf(record))

	_, err := admin.RevokeSetupKey(context.Background(), connect.NewRequest(apiedgev1.RevokeSetupKeyRequest_builder{Edge: refOf(record)}.Build()))
	wantConnectCode(t, err, connect.CodeFailedPrecondition)
	if got := storedEdge(t, store, refOf(record)).GetRecord().GetState().GetLifecycle(); got != edgev1.EdgeLifecycle_EDGE_LIFECYCLE_ENROLLED {
		t.Errorf("lifecycle = %v, want the enrollment left alone", got)
	}
}

func TestRetireEdgeWithdrawsTheOutstandingKeyAndIsIdempotent(t *testing.T) {
	admin, store := newAdmin(t)
	record, _ := createEdge(t, admin)

	resp, err := admin.RetireEdge(context.Background(), connect.NewRequest(apiedgev1.RetireEdgeRequest_builder{Edge: refOf(record)}.Build()))
	if err != nil {
		t.Fatalf("RetireEdge: %v", err)
	}
	if err := protovalidate.Validate(resp.Msg); err != nil {
		t.Fatalf("RetireEdge response fails its schema rules: %v", err)
	}
	if got := resp.Msg.GetEdge().GetState().GetLifecycle(); got != edgev1.EdgeLifecycle_EDGE_LIFECYCLE_RETIRED {
		t.Errorf("lifecycle = %v, want retired", got)
	}
	if got := resp.Msg.GetEdge().GetState().GetSetupKey().GetStatus(); got != edgev1.SetupKeyStatus_SETUP_KEY_STATUS_REVOKED {
		t.Errorf("status = %v, want revoked", got)
	}
	if storedEdge(t, store, refOf(record)).HasSetupKeyHash() {
		t.Fatal("a retired edge's setup key still enrolls: its digest is still stored")
	}

	again, err := admin.RetireEdge(context.Background(), connect.NewRequest(apiedgev1.RetireEdgeRequest_builder{Edge: refOf(record)}.Build()))
	if err != nil {
		t.Fatalf("a repeated RetireEdge failed: %v", err)
	}
	if got := again.Msg.GetEdge().GetState().GetLifecycle(); got != edgev1.EdgeLifecycle_EDGE_LIFECYCLE_RETIRED {
		t.Errorf("lifecycle = %v, want retired", got)
	}
}

func TestUnknownEdgeIsNotFoundOnEveryOperation(t *testing.T) {
	admin, _ := newAdmin(t)
	ref := edgev1.EdgeGlobalRef_builder{
		Edge: edgev1.EdgeLocalRef_builder{Id: proto.String("0192e6a0-0000-7000-8000-00000000dead")}.Build(),
	}.Build()
	ctx := context.Background()

	_, err := admin.GetEdge(ctx, connect.NewRequest(apiedgev1.GetEdgeRequest_builder{Edge: ref}.Build()))
	wantConnectCode(t, err, connect.CodeNotFound)
	_, err = admin.IssueSetupKey(ctx, connect.NewRequest(apiedgev1.IssueSetupKeyRequest_builder{Edge: ref}.Build()))
	wantConnectCode(t, err, connect.CodeNotFound)
	_, err = admin.RevokeSetupKey(ctx, connect.NewRequest(apiedgev1.RevokeSetupKeyRequest_builder{Edge: ref}.Build()))
	wantConnectCode(t, err, connect.CodeNotFound)
	_, err = admin.RetireEdge(ctx, connect.NewRequest(apiedgev1.RetireEdgeRequest_builder{Edge: ref}.Build()))
	wantConnectCode(t, err, connect.CodeNotFound)
}

func TestARequestNamingNoEdgeIsRefused(t *testing.T) {
	admin, _ := newAdmin(t)
	_, err := admin.GetEdge(context.Background(), connect.NewRequest(&apiedgev1.GetEdgeRequest{}))
	wantConnectCode(t, err, connect.CodeInvalidArgument)
}

func TestListEdgesPagesEveryEdgeExactlyOnce(t *testing.T) {
	admin, _ := newAdmin(t)
	want := make(map[string]struct{}, 7)
	for i := 0; i < 7; i++ {
		record, _ := createEdge(t, admin)
		want[record.GetConfig().GetRef().GetEdge().GetId()] = struct{}{}
	}

	got := make(map[string]struct{}, len(want))
	var order []string
	token := ""
	for pages := 0; ; pages++ {
		if pages > len(want) {
			t.Fatal("listing did not terminate")
		}
		resp, err := admin.ListEdges(context.Background(), connect.NewRequest(apiedgev1.ListEdgesRequest_builder{
			PageSize:  proto.Uint32(3),
			PageToken: optional(token),
		}.Build()))
		if err != nil {
			t.Fatalf("ListEdges: %v", err)
		}
		if err := protovalidate.Validate(resp.Msg); err != nil {
			t.Fatalf("ListEdges response fails its schema rules: %v", err)
		}
		for _, edge := range resp.Msg.GetEdges() {
			id := edge.GetConfig().GetRef().GetEdge().GetId()
			if _, dup := got[id]; dup {
				t.Fatalf("edge %s was listed twice", id)
			}
			got[id] = struct{}{}
			order = append(order, id)
		}
		token = resp.Msg.GetNextPageToken()
		if token == "" {
			break
		}
	}

	if len(got) != len(want) {
		t.Fatalf("listed %d edges, want %d", len(got), len(want))
	}
	for id := range want {
		if _, ok := got[id]; !ok {
			t.Errorf("edge %s was never listed", id)
		}
	}
	for i := 1; i < len(order); i++ {
		if order[i-1] >= order[i] {
			t.Fatalf("listing is not in ascending order: %q then %q", order[i-1], order[i])
		}
	}
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func TestListEdgesRefusesATokenItDidNotIssue(t *testing.T) {
	admin, _ := newAdmin(t)
	_, err := admin.ListEdges(context.Background(), connect.NewRequest(apiedgev1.ListEdgesRequest_builder{
		PageToken: proto.String("not base64!"),
	}.Build()))
	wantConnectCode(t, err, connect.CodeInvalidArgument)
}

func TestNewAdminServiceRefusesProvisioningAnEdgeCannotPin(t *testing.T) {
	store := edgestore.New(nil)
	anchor := make([]byte, 32)

	if _, err := edgeapi.NewAdminService(store, nil, edgeapi.Provisioning{TrustAnchors: [][]byte{anchor}}, edgeapi.NewContact(0, 0), nil); err == nil {
		t.Error("a provisioning with no central url was accepted")
	}
	if _, err := edgeapi.NewAdminService(store, nil, edgeapi.Provisioning{CentralURL: "https://central.example.test"}, edgeapi.NewContact(0, 0), nil); err == nil {
		t.Error("a provisioning with no trust anchor was accepted")
	}
	if _, err := edgeapi.NewAdminService(store, nil, edgeapi.Provisioning{
		CentralURL:   "https://central.example.test",
		TrustAnchors: [][]byte{make([]byte, 31)},
	}, edgeapi.NewContact(0, 0), nil); err == nil {
		t.Error("a trust anchor that is not a sha-256 digest was accepted")
	}

	tooMany := make([][]byte, 9)
	for i := range tooMany {
		tooMany[i] = make([]byte, 32)
	}
	if _, err := edgeapi.NewAdminService(store, nil, edgeapi.Provisioning{
		CentralURL:   "https://central.example.test",
		TrustAnchors: tooMany,
	}, edgeapi.NewContact(0, 0), nil); err == nil {
		t.Error("more trust anchors than the schema allows were accepted")
	}
}

func TestAnIssuedSetupKeyResolvesToItsEdge(t *testing.T) {
	admin, store := newAdmin(t)
	record, key := createEdge(t, admin)

	got, err := store.EdgeForSetupKey(context.Background(), keyIDOf(key))
	if err != nil {
		t.Fatalf("EdgeForSetupKey: %v", err)
	}
	if want := refOf(record).GetEdge().GetId(); got != want {
		t.Fatalf("edge for setup key = %q, want %q", got, want)
	}
}

func TestAnUnknownSetupKeyResolvesToNoEdge(t *testing.T) {
	_, store := newAdmin(t)
	got, err := store.EdgeForSetupKey(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("EdgeForSetupKey: %v", err)
	}
	if got != "" {
		t.Fatalf("an unissued identifier resolved to %q", got)
	}
}

func TestWithdrawingASetupKeyDropsItsIndexEntry(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		withdraw func(t *testing.T, admin *edgeapi.AdminService, ref *edgev1.EdgeGlobalRef)
	}{
		{"revoke", func(t *testing.T, admin *edgeapi.AdminService, ref *edgev1.EdgeGlobalRef) {
			if _, err := admin.RevokeSetupKey(ctx, connect.NewRequest(apiedgev1.RevokeSetupKeyRequest_builder{Edge: ref}.Build())); err != nil {
				t.Fatalf("RevokeSetupKey: %v", err)
			}
		}},
		{"retire", func(t *testing.T, admin *edgeapi.AdminService, ref *edgev1.EdgeGlobalRef) {
			if _, err := admin.RetireEdge(ctx, connect.NewRequest(apiedgev1.RetireEdgeRequest_builder{Edge: ref}.Build())); err != nil {
				t.Fatalf("RetireEdge: %v", err)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			admin, store := newAdmin(t)
			record, key := createEdge(t, admin)
			tc.withdraw(t, admin, refOf(record))

			got, err := store.EdgeForSetupKey(ctx, keyIDOf(key))
			if err != nil {
				t.Fatalf("EdgeForSetupKey: %v", err)
			}
			if got != "" {
				t.Fatalf("a withdrawn key still resolves to %q", got)
			}
		})
	}
}

func TestReissuingMovesTheIndexToTheNewKey(t *testing.T) {
	ctx := context.Background()
	admin, store := newAdmin(t)
	record, first := createEdge(t, admin)

	resp, err := admin.IssueSetupKey(ctx, connect.NewRequest(apiedgev1.IssueSetupKeyRequest_builder{Edge: refOf(record)}.Build()))
	if err != nil {
		t.Fatalf("IssueSetupKey: %v", err)
	}
	second := resp.Msg.GetProvisioning().GetSetupKey()

	got, err := store.EdgeForSetupKey(ctx, keyIDOf(second))
	if err != nil {
		t.Fatalf("EdgeForSetupKey: %v", err)
	}
	if want := refOf(record).GetEdge().GetId(); got != want {
		t.Fatalf("the issued key resolves to %q, want %q", got, want)
	}
	stale, err := store.EdgeForSetupKey(ctx, keyIDOf(first))
	if err != nil {
		t.Fatalf("EdgeForSetupKey: %v", err)
	}
	if stale != "" {
		t.Fatalf("the replaced key still resolves to %q", stale)
	}
}

func TestRetiringAnEnrolledEdgeSucceedsWithNoOutstandingKey(t *testing.T) {
	admin, store := newAdmin(t)
	record, _ := createEdge(t, admin)
	enroll(t, store, refOf(record))

	resp, err := admin.RetireEdge(context.Background(), connect.NewRequest(apiedgev1.RetireEdgeRequest_builder{Edge: refOf(record)}.Build()))
	if err != nil {
		t.Fatalf("RetireEdge on an enrolled edge: %v", err)
	}
	if got := resp.Msg.GetEdge().GetState().GetLifecycle(); got != edgev1.EdgeLifecycle_EDGE_LIFECYCLE_RETIRED {
		t.Errorf("lifecycle = %v, want retired", got)
	}
}

func TestListEdgesSkipsTheSetupKeyIndexEntries(t *testing.T) {
	admin, _ := newAdmin(t)
	for i := 0; i < 3; i++ {
		createEdge(t, admin)
	}

	resp, err := admin.ListEdges(context.Background(), connect.NewRequest(&apiedgev1.ListEdgesRequest{}))
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	if got := len(resp.Msg.GetEdges()); got != 3 {
		t.Fatalf("listed %d edges, want 3; index entries must not be listed as edges", got)
	}
}

// keyIDOf is the middle segment of a setup key string, the part the index is
// keyed by.
func keyIDOf(key string) string {
	return key[len("fse1_") : len("fse1_")+26]
}

// laneHolds records what retirement asked of the lane records.
type laneHolds struct {
	devices    []string
	dropped    []string
	err        error
	devicesErr error
	open       map[string]uint64
	openErr    error
}

func (l *laneHolds) Devices(context.Context, string) ([]string, error) {
	if l.devicesErr != nil {
		return nil, l.devicesErr
	}
	return l.devices, nil
}

func (l *laneHolds) DropHolds(_ context.Context, deviceID string) error {
	if l.err != nil {
		return l.err
	}
	l.dropped = append(l.dropped, deviceID)
	return nil
}

func (l *laneHolds) OpenMutation(_ context.Context, deviceID string) (uint64, bool, error) {
	if l.openErr != nil {
		return 0, false, l.openErr
	}
	sequence, open := l.open[deviceID]
	return sequence, open, nil
}

// A hold tells an edge to clear its own. A retired edge will never take it, so
// keeping it pending owes something to a peer that cannot discharge it — and
// enough of them wall off the abandons the operator now has to make.
func TestRetireEdgeForgetsWhatItsDevicesOwedIt(t *testing.T) {
	store := newStoreOver(t, newHub(t))
	holds := &laneHolds{devices: []string{"device-a", "device-b"}}
	admin, err := edgeapi.NewAdminService(store, holds, edgeapi.Provisioning{
		CentralURL:   "https://central.example.test",
		TrustAnchors: [][]byte{make([]byte, 32)},
	}, edgeapi.NewContact(0, 0), func() time.Time { return testClock })
	if err != nil {
		t.Fatalf("NewAdminService: %v", err)
	}
	record, _ := createEdge(t, admin)

	if _, err := admin.RetireEdge(context.Background(), connect.NewRequest(apiedgev1.RetireEdgeRequest_builder{
		Edge: refOf(record),
	}.Build())); err != nil {
		t.Fatalf("RetireEdge: %v", err)
	}

	if len(holds.dropped) != 2 {
		t.Fatalf("dropped holds on %v, want both devices", holds.dropped)
	}
}

// Retirement ends no mutation: abandoning live work destroys something the
// operator did not ask to lose, and AbandonMutation is where they say so.
func TestRetireEdgeEndsNoMutation(t *testing.T) {
	store := newStoreOver(t, newHub(t))
	holds := &laneHolds{devices: []string{"device-a"}}
	admin, err := edgeapi.NewAdminService(store, holds, edgeapi.Provisioning{
		CentralURL:   "https://central.example.test",
		TrustAnchors: [][]byte{make([]byte, 32)},
	}, edgeapi.NewContact(0, 0), func() time.Time { return testClock })
	if err != nil {
		t.Fatalf("NewAdminService: %v", err)
	}
	record, _ := createEdge(t, admin)

	if _, err := admin.RetireEdge(context.Background(), connect.NewRequest(apiedgev1.RetireEdgeRequest_builder{
		Edge: refOf(record),
	}.Build())); err != nil {
		t.Fatalf("RetireEdge: %v", err)
	}

	// The only lane-record call retirement makes is the hold drop. Anything
	// that ended a mutation would have to be another method on this seam.
	if got := holds.dropped; len(got) != 1 || got[0] != "device-a" {
		t.Errorf("retirement touched the lane records as %v", got)
	}
}

// A drop that fails is reported, and retirement is idempotent, so the
// operator's retry finishes what the first call started rather than leaving
// the edge retired with its holds still owed and nothing saying so.
func TestRetireEdgeReportsAFailedDropAndFinishesOnRetry(t *testing.T) {
	store := newStoreOver(t, newHub(t))
	holds := &laneHolds{devices: []string{"device-a"}, err: errors.New("bucket unreachable")}
	admin, err := edgeapi.NewAdminService(store, holds, edgeapi.Provisioning{
		CentralURL:   "https://central.example.test",
		TrustAnchors: [][]byte{make([]byte, 32)},
	}, edgeapi.NewContact(0, 0), func() time.Time { return testClock })
	if err != nil {
		t.Fatalf("NewAdminService: %v", err)
	}
	record, _ := createEdge(t, admin)
	req := apiedgev1.RetireEdgeRequest_builder{Edge: refOf(record)}.Build()

	if _, err := admin.RetireEdge(context.Background(), connect.NewRequest(req)); err == nil {
		t.Fatal("a failed hold drop was swallowed")
	}

	holds.err = nil
	if _, err := admin.RetireEdge(context.Background(), connect.NewRequest(req)); err != nil {
		t.Fatalf("retry after a failed drop: %v", err)
	}
	if len(holds.dropped) != 1 {
		t.Errorf("the retry dropped %v, want the device the first call could not", holds.dropped)
	}
}

// An edge can be created and retired without a registry ever describing it, so
// a registry that answers "this is not my edge" leaves retirement nothing to
// drop rather than something to fail on. Every other lookup failure still
// stops the call, which the test above covers.
func TestRetireEdgeSucceedsWhenTheRegistryDescribesAnotherEdge(t *testing.T) {
	store := newStoreOver(t, newHub(t))
	holds := &laneHolds{devicesErr: errs.New().Code(registry.ErrCodeUnknownEdge).Msg("this registry describes a different edge")}
	admin, err := edgeapi.NewAdminService(store, holds, edgeapi.Provisioning{
		CentralURL:   "https://central.example.test",
		TrustAnchors: [][]byte{make([]byte, 32)},
	}, edgeapi.NewContact(0, 0), func() time.Time { return testClock })
	if err != nil {
		t.Fatalf("NewAdminService: %v", err)
	}
	record, _ := createEdge(t, admin)

	resp, err := admin.RetireEdge(context.Background(), connect.NewRequest(apiedgev1.RetireEdgeRequest_builder{
		Edge: refOf(record),
	}.Build()))
	if err != nil {
		t.Fatalf("RetireEdge: %v", err)
	}
	if got := resp.Msg.GetEdge().GetState().GetLifecycle(); got != edgev1.EdgeLifecycle_EDGE_LIFECYCLE_RETIRED {
		t.Fatalf("lifecycle = %v, want retired", got)
	}
	if len(holds.dropped) != 0 {
		t.Fatalf("dropped %v, want nothing", holds.dropped)
	}
}

// Retirement ends no mutation, so every lane the edge still held is now work
// only an operator can end. Handing it back with the retirement is the
// difference between ending those lanes today and finding one stuck weeks
// later, and the rows carry what AbandonMutation takes.
func TestRetireEdgeNamesTheLanesItOrphaned(t *testing.T) {
	store := newStoreOver(t, newHub(t))
	holds := &laneHolds{
		devices: []string{"device-b", "device-a", "device-c"},
		open:    map[string]uint64{"device-a": 7, "device-c": 12},
	}
	admin, err := edgeapi.NewAdminService(store, holds, edgeapi.Provisioning{
		CentralURL:   "https://central.example.test",
		TrustAnchors: [][]byte{make([]byte, 32)},
	}, edgeapi.NewContact(0, 0), func() time.Time { return testClock })
	if err != nil {
		t.Fatalf("NewAdminService: %v", err)
	}
	record, _ := createEdge(t, admin)

	resp, err := admin.RetireEdge(context.Background(), connect.NewRequest(apiedgev1.RetireEdgeRequest_builder{
		Edge: refOf(record),
	}.Build()))
	if err != nil {
		t.Fatalf("RetireEdge: %v", err)
	}

	orphaned := resp.Msg.GetOrphaned()
	if len(orphaned) != 2 {
		t.Fatalf("orphaned = %v, want the two devices holding an open mutation", orphaned)
	}
	if got := orphaned[0].GetDeviceId(); got != "device-a" {
		t.Errorf("first orphan = %q, want device-a; the list is in device order", got)
	}
	if got := orphaned[0].GetSequence(); got != 7 {
		t.Errorf("device-a sequence = %d, want the one AbandonMutation takes", got)
	}
	if got := orphaned[1].GetDeviceId(); got != "device-c" {
		t.Errorf("second orphan = %q, want device-c", got)
	}
	if len(holds.dropped) != 3 {
		t.Errorf("dropped holds on %v, want all three devices", holds.dropped)
	}
}

// A record that cannot be read must not shorten the list. An operator acting
// on a short one takes it for the whole answer and leaves a lane held by an
// edge that is never coming back.
func TestRetireEdgeRefusesRatherThanReportingAShortOrphanList(t *testing.T) {
	store := newStoreOver(t, newHub(t))
	holds := &laneHolds{devices: []string{"device-a"}, openErr: errors.New("bucket unreachable")}
	admin, err := edgeapi.NewAdminService(store, holds, edgeapi.Provisioning{
		CentralURL:   "https://central.example.test",
		TrustAnchors: [][]byte{make([]byte, 32)},
	}, edgeapi.NewContact(0, 0), func() time.Time { return testClock })
	if err != nil {
		t.Fatalf("NewAdminService: %v", err)
	}
	record, _ := createEdge(t, admin)

	if _, err := admin.RetireEdge(context.Background(), connect.NewRequest(apiedgev1.RetireEdgeRequest_builder{
		Edge: refOf(record),
	}.Build())); err == nil {
		t.Fatal("an unreadable lane record was reported as no open mutation")
	}
}
