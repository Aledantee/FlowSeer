package identity_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/identity"
)

const testSetupKey = "fse1_abcdefghijklmnopqrstuvwxyz_abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwx"

// centralFake answers Enroll the way the device service does, including the
// part an edge's crash recovery rests on: a repeat with the same public key
// gets the same answer back rather than a refusal.
type centralFake struct {
	calls      int
	registered ed25519.PublicKey
	// onEnroll runs before the answer, so a test can look at the agent's
	// state directory as it was at the moment the call was made.
	onEnroll func()
	err      error
}

func (c *centralFake) Enroll(
	_ context.Context, req *connect.Request[edgev1.EnrollRequest],
) (*connect.Response[edgev1.EnrollResponse], error) {
	c.calls++
	if c.onEnroll != nil {
		c.onEnroll()
	}
	if c.err != nil {
		return nil, c.err
	}

	payload := &edgev1.KeyProofPayload{}
	if err := identity.UnmarshalProofForTest(req.Msg.GetProof(), payload); err != nil {
		return nil, err
	}
	presented := ed25519.PublicKey(payload.GetPublicKey())
	switch {
	case c.registered == nil:
		c.registered = presented
	case !c.registered.Equal(presented):
		return nil, errors.New("setup key was already used to register another key")
	}
	return connect.NewResponse(enrollAnswer()), nil
}

func newStore(t *testing.T) (*identity.Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	store, err := identity.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, dir
}

// TestTheKeyIsPersistedBeforeEnrollIsCalled is the ordering's own test, and
// it looks at the state directory from inside the Enroll call rather than
// after it — the only moment at which the two orders differ.
//
// Reversed, central registers a public key whose private half has not reached
// disk. A crash there leaves an edge that cannot sign, so cannot call Rekey
// to replace the key it does not have, and an operator has to retire it and
// issue a fresh setup key. Asserting only that both files exist afterwards
// would pass for either order.
func TestTheKeyIsPersistedBeforeEnrollIsCalled(t *testing.T) {
	store, dir := newStore(t)

	var keyOnDiskAtCallTime bool
	central := &centralFake{onEnroll: func() {
		_, err := os.Stat(filepath.Join(dir, "edge.key"))
		keyOnDiskAtCallTime = err == nil
	}}

	if _, err := identity.Establish(context.Background(), store, central, testSetupKey); err != nil {
		t.Fatalf("Establish: %v", err)
	}
	if !keyOnDiskAtCallTime {
		t.Fatal("Enroll was called before the key reached disk: a crash there loses the edge")
	}
}

// TestACrashBeforeTheAnswerIsPersistedReEnrollsWithTheSameKey walks the one
// crash point that needs central's cooperation. The key is on disk and the
// answer is not, which is what a restart finds after a crash between the call
// returning and the write landing.
func TestACrashBeforeTheAnswerIsPersistedReEnrollsWithTheSameKey(t *testing.T) {
	store, dir := newStore(t)
	central := &centralFake{}

	first, err := identity.Establish(context.Background(), store, central, testSetupKey)
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}

	// The crash: the answer never reached disk.
	if err := os.Remove(filepath.Join(dir, "enrollment.textproto")); err != nil {
		t.Fatalf("simulate the crash: %v", err)
	}

	second, err := identity.Establish(context.Background(), store, central, testSetupKey)
	if err != nil {
		t.Fatalf("Establish after the crash: %v", err)
	}
	if central.calls != 2 {
		t.Errorf("central saw %d enrollments, want 2: the restart must call again", central.calls)
	}
	if !first.Key.Equal(second.Key) {
		t.Fatal("the restart generated a new key; central holds the old one and would refuse it")
	}
	if first.Enrollment.GetEdge().GetEdge().GetId() != second.Enrollment.GetEdge().GetEdge().GetId() {
		t.Error("the restart came back as a different edge")
	}
}

// TestAnEnrolledEdgeNeverEnrollsAgain covers the ordinary restart. An edge
// with both files does not present a setup key — it has none to present, the
// one it used is consumed, and calling would be asking central to re-register
// a key it already holds.
func TestAnEnrolledEdgeNeverEnrollsAgain(t *testing.T) {
	store, _ := newStore(t)
	central := &centralFake{}

	if _, err := identity.Establish(context.Background(), store, central, testSetupKey); err != nil {
		t.Fatalf("Establish: %v", err)
	}
	// No setup key this time, which is what a restarted agent has.
	again, err := identity.Establish(context.Background(), store, central, "")
	if err != nil {
		t.Fatalf("Establish on a restart: %v", err)
	}
	if central.calls != 1 {
		t.Errorf("central saw %d enrollments, want 1", central.calls)
	}
	if again.Enrollment.GetAudience() == "" {
		t.Error("the restart loaded an enrollment with no audience; it could not sign")
	}
	if len(again.TrustAnchors()) == 0 {
		t.Error("the restart loaded no trust anchors; it could not pin central")
	}
}

func TestOnlyAFreshEnrollmentSeedsTheAssertionClock(t *testing.T) {
	store, _ := newStore(t)
	central := &centralFake{}
	freshLocalNow := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	serverNow := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	restartLocalNow := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	fresh, err := identity.Establish(context.Background(), store, central, testSetupKey)
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	var logs bytes.Buffer
	freshSigner := fresh.Signer(
		context.Background(),
		func() time.Time { return freshLocalNow },
		slog.New(slog.NewJSONHandler(&logs, nil)),
	)
	identity.SetNonceForTest(freshSigner, make([]byte, 16))
	freshHeader, err := freshSigner.Header(vectorProcedure, nil)
	if err != nil {
		t.Fatalf("fresh Header: %v", err)
	}
	if got := identity.DecodeForTest(t, freshHeader).GetIssuedAt().AsTime(); !got.Equal(serverNow) {
		t.Errorf("fresh issued_at = %v, want enrollment server_time %v", got, serverNow)
	}
	if got := logs.String(); !strings.Contains(got, `"msg":"assertion clock corrected"`) || !strings.Contains(got, `"flowseer.edge.clock_offset_ms"`) {
		t.Errorf("clock correction log = %q, want the correction and its offset", got)
	}

	loaded, err := identity.Establish(context.Background(), store, central, "")
	if err != nil {
		t.Fatalf("Establish on restart: %v", err)
	}
	loadedSigner := loaded.Signer(context.Background(), func() time.Time { return restartLocalNow }, nil)
	identity.SetNonceForTest(loadedSigner, make([]byte, 16))
	loadedHeader, err := loadedSigner.Header(vectorProcedure, nil)
	if err != nil {
		t.Fatalf("loaded Header: %v", err)
	}
	if got := identity.DecodeForTest(t, loadedHeader).GetIssuedAt().AsTime(); !got.Equal(restartLocalNow) {
		t.Errorf("loaded issued_at = %v, want current local time %v; persisted server_time is stale", got, restartLocalNow)
	}
}

// TestAnUnenrolledEdgeWithNoSetupKeyFails is the case an operator meets when
// they start an agent that was never provisioned. It fails at start with a
// sentence naming the reason rather than at the first call with a signature
// error.
func TestAnUnenrolledEdgeWithNoSetupKeyFails(t *testing.T) {
	store, _ := newStore(t)
	if _, err := identity.Establish(context.Background(), store, &centralFake{}, ""); err == nil {
		t.Fatal("Establish() error = nil, want an unenrolled edge with no setup key refused")
	}
}

// TestTheKeyFileIsPrivate: it is the whole of this edge's identity, and
// central holds only the public half, so an edge that leaks it is
// impersonable and one that loses it is unrecoverable.
func TestTheKeyFileIsPrivate(t *testing.T) {
	store, dir := newStore(t)
	if _, err := identity.Establish(context.Background(), store, &centralFake{}, testSetupKey); err != nil {
		t.Fatalf("Establish: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "edge.key"))
	if err != nil {
		t.Fatalf("stat the key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key mode = %o, want 600", perm)
	}
	if dirInfo, err := os.Stat(dir); err != nil {
		t.Fatalf("stat the state dir: %v", err)
	} else if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("state directory mode = %o, want 700", perm)
	}
}

// enrollAnswer is what central returns: the identity, its clock, the audience
// every assertion names, and the anchors that replace whatever the edge was
// provisioned with.
func enrollAnswer() *edgev1.EnrollResponse {
	response := vectorEnrollment()
	response.SetServerTime(timestamppb.New(time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)))
	response.SetTrustAnchors([][]byte{make([]byte, 32)})
	return response
}
