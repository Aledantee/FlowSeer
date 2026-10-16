package edgebus

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"go.aledante.io/FlowSeer/src/common/service"
)

func TestHubHasNoEdgeFacingListener(t *testing.T) {
	h := cleanupHub(t, t.TempDir())
	if !h.opts.DontListen || h.opts.Websocket.Port != 0 || h.opts.LeafNode.Port != 0 || len(h.opts.LeafNode.Remotes) != 0 {
		t.Fatalf("server listener options = client disabled %v, WebSocket port %d, leaf port %d, remotes %d", h.opts.DontListen, h.opts.Websocket.Port, h.opts.LeafNode.Port, len(h.opts.LeafNode.Remotes))
	}
}

func TestCentralAccountCarriesNoImportsOrExports(t *testing.T) {
	h := cleanupHub(t, t.TempDir())
	encoded, err := h.keys.accountJWT(h.keys.central, "CENTRAL", 1)
	if err != nil {
		t.Fatalf("central jwt: %v", err)
	}
	claims, err := jwt.DecodeAccountClaims(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(claims.Imports) != 0 || len(claims.Exports) != 0 {
		t.Fatalf("central account has %d imports and %d exports, want none", len(claims.Imports), len(claims.Exports))
	}
}

func TestHubLoadsOnlySystemAndCentralAccounts(t *testing.T) {
	h := cleanupHub(t, t.TempDir())
	assertHubAccounts(t, h)
}

func assertHubAccounts(t *testing.T, h *Hub) {
	t.Helper()
	accounts, err := h.server.Accountz(nil)
	if err != nil {
		t.Fatalf("accountz: %v", err)
	}
	system, err := h.keys.system.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	central, err := h.keys.central.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"$G", system, central}
	slices.Sort(want)
	slices.Sort(accounts.Accounts)
	if !slices.Equal(accounts.Accounts, want) {
		t.Errorf("loaded accounts = %v, want %v", accounts.Accounts, want)
	}
}

func TestHubServesCentralWithFormerEdgeState(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadOrCreateKeys(dir); err != nil {
		t.Fatal(err)
	}
	former, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	seed, err := former.Seed()
	if err != nil {
		t.Fatal(err)
	}
	account, err := former.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	strays := map[string][]byte{
		filepath.Join(dir, "keys", "edge-x.nk"):     seed,
		filepath.Join(dir, "keys", "edge-x.tenant"): []byte("tenant-a\n"),
	}
	for path, body := range strays {
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store := filepath.Join(dir, "jetstream", "jetstream", account)
	if err := os.MkdirAll(filepath.Join(store, "streams", "FLOWSEER_EDGE_x"), 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "server.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := logFile.Close(); err != nil {
			t.Error(err)
		}
	})
	h, err := StartHub(t.Context(), HubConfig{
		StateDir: dir, FsyncPolicy: service.BusFsyncPeriodic,
		Logger: slog.New(slog.NewJSONHandler(logFile, nil)),
	})
	if err != nil {
		t.Fatalf("start with former edge state: %v", err)
	}
	t.Cleanup(h.Close)
	assertHubAccounts(t, h)
	subject := AuditSubject("tenant-a", "device-a")
	ack, err := h.JetStream().Publish(context.Background(), subject, []byte("central record"))
	if err != nil {
		t.Fatalf("publish CENTRAL: %v", err)
	}
	stream, err := h.JetStream().Stream(t.Context(), AuditStream)
	if err != nil {
		t.Fatal(err)
	}
	record, err := stream.GetMsg(t.Context(), ack.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	if record.Subject != subject || string(record.Data) != "central record" {
		t.Errorf("CENTRAL record = %s %q, want %s %q", record.Subject, record.Data, subject, "central record")
	}
	logs, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(logs), "Account fetch failed"); got != 1 {
		t.Errorf("account fetch warnings = %d, want 1: %s", got, logs)
	}
	if !strings.Contains(string(logs), `"level":"WARN"`) {
		t.Errorf("warning not routed to host logger: %s", logs)
	}
	for path, want := range strays {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(want) {
			t.Errorf("stray file %s changed: err=%v", filepath.Base(path), err)
		}
	}
	if _, err := os.Stat(store); err != nil {
		t.Errorf("former account directory removed: %v", err)
	}
}
