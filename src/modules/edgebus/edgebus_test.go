package edgebus_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/access/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/secret"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

const (
	edgeID  = "0192e6a0-0000-7000-8000-0000000000ed"
	otherID = "0192e6a0-0000-7000-8000-0000000000ee"
)

func TestEdgeCredentialsFormattingRedactsSeed(t *testing.T) {
	const seed = "SUAFLOWSEEREDGESEED"
	creds := edgebus.EdgeCredentials{Seed: secret.NewString(seed)}

	if rendered := fmt.Sprintf("%+v", creds); strings.Contains(rendered, seed) {
		t.Fatalf("formatted edge credentials exposed seed: %s", rendered)
	}
}

func TestLeafDoesNotRetainStartupConfig(t *testing.T) {
	if _, retained := reflect.TypeFor[edgebus.Leaf]().FieldByName("cfg"); retained {
		t.Fatal("Leaf retains LeafConfig, including credentials it no longer needs")
	}
}

func startHub(t *testing.T, dir string, port int) *edgebus.Hub {
	t.Helper()
	hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
		StateDir:    dir,
		FsyncPolicy: service.BusFsyncPeriodic,
		ListenPort:  port,
	})
	if err != nil {
		t.Fatalf("start hub: %v (%v)", err, errs.Attributes(err))
	}
	t.Cleanup(hub.Close)
	return hub
}

func startLeaf(t *testing.T, dir string, hub *edgebus.Hub, id string) *edgebus.Leaf {
	t.Helper()
	if err := hub.AttachEdge(context.Background(), edgebus.DefaultTenant, id); err != nil {
		t.Fatalf("attach edge: %v", err)
	}
	creds, err := hub.MintEdgeUser(context.Background(), id)
	if err != nil {
		t.Fatalf("mint edge user: %v", err)
	}
	return startLeafWith(t, dir, hub.ListenURL(), id, creds)
}

func startLeafWith(t *testing.T, dir, url, id string, creds edgebus.EdgeCredentials) *edgebus.Leaf {
	t.Helper()
	leaf, err := edgebus.StartLeaf(context.Background(), edgebus.LeafConfig{
		StateDir:        dir,
		EdgeID:          id,
		Tenant:          edgebus.DefaultTenant,
		HubURLs:         []string{url},
		CredentialsFile: secret.New(credsFileFor(t, creds)),
		FsyncPolicy:     service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("start leaf: %v", err)
	}
	t.Cleanup(leaf.Close)
	return leaf
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestUndeclaredFsyncPolicyRefusesStart(t *testing.T) {
	_, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{StateDir: t.TempDir()})
	if code, ok := errs.CodeOf(err); !ok || code != edgebus.ErrCodeConfig {
		t.Fatalf("hub without a policy: err=%v code=%q", err, code)
	}
	_, err = edgebus.StartLeaf(context.Background(), edgebus.LeafConfig{StateDir: t.TempDir(), EdgeID: edgeID, Tenant: edgebus.DefaultTenant, HubURLs: []string{"ws://127.0.0.1:1"}})
	if code, ok := errs.CodeOf(err); !ok || code != edgebus.ErrCodeConfig {
		t.Fatalf("leaf without a policy: err=%v code=%q", err, code)
	}
}

func TestLeafNarrowsAnExistingCredentialsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hub.creds")
	if err := os.WriteFile(path, []byte("old credentials"), 0o600); err != nil {
		t.Fatalf("write existing credentials: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("make existing credentials permissive: %v", err)
	}

	_, _ = edgebus.StartLeaf(context.Background(), edgebus.LeafConfig{
		StateDir:        dir,
		EdgeID:          edgeID,
		Tenant:          edgebus.DefaultTenant,
		HubURLs:         []string{"://"},
		CredentialsFile: secret.New([]byte("new credentials")),
		FsyncPolicy:     service.BusFsyncPeriodic,
	})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat credentials: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("existing credentials mode = %o, want 600", got)
	}
}

func TestLeafJoinsWithMintedCredentialAndSourcingFlows(t *testing.T) {
	hub := startHub(t, t.TempDir(), -1)
	leaf := startLeaf(t, t.TempDir(), hub, edgeID)
	waitFor(t, "leaf link", 10*time.Second, func() bool { return hub.LeafCount() == 1 && leaf.HubConnected() })

	if err := hub.AttachEdge(context.Background(), edgebus.DefaultTenant, edgeID); err != nil {
		t.Fatalf("add edge source: %v", err)
	}
	if err := hub.AttachEdge(context.Background(), edgebus.DefaultTenant, edgeID); err != nil {
		t.Fatalf("add edge source twice: %v", err)
	}

	if err := leaf.Publish(context.Background(), leaf.Subject("otel.logs"), []byte("record-1"), ""); err != nil {
		t.Fatalf("publish: %v", err)
	}
	stream, err := hub.EdgeStream(context.Background(), edgeID)
	if err != nil {
		t.Fatalf("edge stream: %v", err)
	}
	waitFor(t, "sourced record", 10*time.Second, func() bool {
		info, err := stream.Info(context.Background())
		return err == nil && info.State.Msgs == 1
	})
	msg, err := stream.GetLastMsgForSubject(context.Background(), leaf.OTelSubject(edgebus.SignalLogs))
	if err != nil {
		t.Fatalf("read sourced record: %v", err)
	}
	if !bytes.Equal(msg.Data, []byte("record-1")) {
		t.Fatalf("sourced record = %q", msg.Data)
	}
}

func TestEdgePermissionsConfineTheLeafWhileSourcingFlows(t *testing.T) {
	hub := startHub(t, t.TempDir(), -1)
	leaf := startLeaf(t, t.TempDir(), hub, edgeID)
	waitFor(t, "leaf link", 10*time.Second, func() bool { return hub.LeafCount() == 1 })
	if err := hub.AttachEdge(context.Background(), edgebus.DefaultTenant, edgeID); err != nil {
		t.Fatalf("attach: %v", err)
	}

	// The permitted set is enough to source: a record the edge publishes on
	// its own subtree reaches the hub's per-edge stream. Proven in the same
	// test as the confinement below, so a future narrowing that breaks
	// sourcing or a widening that restores it fails one test.
	if err := leaf.Publish(context.Background(), leaf.OTelSubject(edgebus.SignalLogs), []byte("sourced"), ""); err != nil {
		t.Fatalf("publish: %v", err)
	}
	stream, err := hub.EdgeStream(context.Background(), edgeID)
	if err != nil {
		t.Fatalf("edge stream: %v", err)
	}
	waitFor(t, "sourced record", 15*time.Second, func() bool {
		info, err := stream.Info(context.Background())
		return err == nil && info.State.Msgs == 1
	})

	// Confinement: a subscription on the whole namespace imports nothing the
	// hub publishes, so nothing central reaches an edge by this path.
	leaked := make(chan struct{}, 1)
	const marker = "central-only-marker"
	if _, err := leaf.Connection().Subscribe("flowseer.>", func(m *nats.Msg) {
		// The edge's own sourcing deliveries also match flowseer.>; only a
		// message central published, carrying the marker, is a leak.
		if string(m.Data) == marker {
			select {
			case leaked <- struct{}{}:
			default:
			}
		}
	}); err != nil {
		t.Fatalf("subscribe on leaf: %v", err)
	}
	if err := leaf.Connection().Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := hub.Connection().Publish("flowseer.default.dispatch.anything", []byte(marker)); err != nil {
		t.Fatalf("publish on hub: %v", err)
	}
	if err := hub.Connection().Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	select {
	case <-leaked:
		t.Fatal("a hub central publish reached the leaf")
	case <-time.After(500 * time.Millisecond):
	}
}

// collector records every OTLP body it receives.
type collector struct {
	mu     sync.Mutex
	bodies map[string][][]byte
}

func newCollector(t *testing.T) (*collector, *httptest.Server) {
	t.Helper()
	c := &collector{bodies: map[string][][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		c.bodies[r.URL.Path] = append(c.bodies[r.URL.Path], body)
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return c, srv
}

func (c *collector) received(path string) [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bodies[path]
}

func postOTLP(t *testing.T, endpoint string, signal edgebus.OTelSignal, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, endpoint+"/v1/"+string(signal), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestReceiverToForwarderCarriesBodiesUnchanged(t *testing.T) {
	hub := startHub(t, t.TempDir(), -1)
	leaf := startLeaf(t, t.TempDir(), hub, edgeID)
	waitFor(t, "leaf link", 10*time.Second, func() bool { return hub.LeafCount() == 1 })
	if err := hub.AttachEdge(context.Background(), edgebus.DefaultTenant, edgeID); err != nil {
		t.Fatalf("add edge source: %v", err)
	}
	receiver, err := edgebus.StartReceiver(leaf)
	if err != nil {
		t.Fatalf("start receiver: %v", err)
	}
	t.Cleanup(func() { _ = receiver.Close(context.Background()) })
	c, collectorSrv := newCollector(t)
	forwarder, err := edgebus.StartForwarder(context.Background(), hub, edgebus.ForwarderConfig{Endpoint: collectorSrv.URL, RetryDelay: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("start forwarder: %v", err)
	}
	t.Cleanup(forwarder.Close)

	body := []byte{0x0a, 0x03, 0x01, 0x02, 0x03}
	if resp := postOTLP(t, receiver.Endpoint(), edgebus.SignalLogs, body); resp.StatusCode != http.StatusOK {
		t.Fatalf("receiver status = %d", resp.StatusCode)
	}
	waitFor(t, "forwarded body", 15*time.Second, func() bool { return len(c.received("/v1/logs")) == 1 })
	if got := c.received("/v1/logs")[0]; !bytes.Equal(got, body) {
		t.Fatalf("forwarded body = %x, want %x", got, body)
	}

	compressed, err := http.NewRequest(http.MethodPost, receiver.Endpoint()+"/v1/logs", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	compressed.Header.Set("Content-Type", "application/x-protobuf")
	compressed.Header.Set("Content-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(compressed)
	if err != nil {
		t.Fatalf("post compressed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("compressed export status = %d, want 415", resp.StatusCode)
	}
}

func TestRecordsPublishedWhileTheHubIsDownArriveAfterReconnect(t *testing.T) {
	hubDir := t.TempDir()
	first := startHub(t, hubDir, -1)
	url := first.ListenURL()
	port := first.ListenPort()
	if err := first.AttachEdge(context.Background(), edgebus.DefaultTenant, edgeID); err != nil {
		t.Fatalf("add edge source: %v", err)
	}
	creds, err := first.MintEdgeUser(context.Background(), edgeID)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	leaf := startLeafWith(t, t.TempDir(), url, edgeID, creds)
	waitFor(t, "leaf link", 10*time.Second, func() bool { return first.LeafCount() == 1 })
	first.Close()
	waitFor(t, "link down", 10*time.Second, func() bool { return !leaf.HubConnected() })

	if err := leaf.Publish(context.Background(), leaf.OTelSubject(edgebus.SignalTraces), []byte("buffered"), ""); err != nil {
		t.Fatalf("publish while down: %v", err)
	}

	second := startHub(t, hubDir, port)
	waitFor(t, "link up again", 20*time.Second, func() bool { return second.LeafCount() == 1 })
	c, collectorSrv := newCollector(t)
	forwarder, err := edgebus.StartForwarder(context.Background(), second, edgebus.ForwarderConfig{Endpoint: collectorSrv.URL, RetryDelay: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("start forwarder: %v", err)
	}
	t.Cleanup(forwarder.Close)
	// Sourcing re-establishes on the server's own retry cadence after the
	// hub comes back, about forty seconds; the buffer holds the record
	// meanwhile.
	waitFor(t, "buffered record forwarded", 2*time.Minute, func() bool { return len(c.received("/v1/traces")) == 1 })
	if got := c.received("/v1/traces")[0]; !bytes.Equal(got, []byte("buffered")) {
		t.Fatalf("forwarded = %q", got)
	}
}

func TestAuditStreamStoresADuplicateEventOnce(t *testing.T) {
	hub := startHub(t, t.TempDir(), 0)
	subject := edgebus.AuditSubject(edgebus.DefaultTenant, "0192e6a0-0000-7000-8000-0000000000d1")
	for range 2 {
		if _, err := hub.JetStream().Publish(context.Background(), subject, []byte("event"), jetstream.WithMsgID("event-1")); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	stream, err := hub.JetStream().Stream(context.Background(), edgebus.AuditStream)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	info, err := stream.Info(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.State.Msgs != 1 {
		t.Fatalf("audit stream holds %d messages, want 1", info.State.Msgs)
	}
}

func TestLaneBucketSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	first := startHub(t, dir, 0)
	kv, err := first.JetStream().KeyValue(context.Background(), edgebus.LaneBucket)
	if err != nil {
		t.Fatalf("bucket: %v", err)
	}
	if _, err := kv.Put(context.Background(), "device-1", []byte("record")); err != nil {
		t.Fatalf("put: %v", err)
	}
	first.Close()

	second := startHub(t, dir, 0)
	kv, err = second.JetStream().KeyValue(context.Background(), edgebus.LaneBucket)
	if err != nil {
		t.Fatalf("bucket after restart: %v", err)
	}
	entry, err := kv.Get(context.Background(), "device-1")
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if string(entry.Value()) != "record" {
		t.Fatalf("value after restart = %q", entry.Value())
	}
}

func TestPinVerifierRejectsAnUnanchoredCertificate(t *testing.T) {
	anchored := selfSigned(t)
	other := selfSigned(t)
	verify := edgebus.PinVerifier([][]byte{edgebus.SPKIDigest(anchored)})
	if err := verify([][]byte{anchored.Raw}, nil); err != nil {
		t.Fatalf("anchored certificate rejected: %v", err)
	}
	err := verify([][]byte{other.Raw}, nil)
	if code, ok := errs.CodeOf(err); !ok || code != edgebus.ErrCodePin {
		t.Fatalf("unanchored certificate: err=%v code=%q", err, code)
	}
	if err := verify(nil, nil); err == nil {
		t.Fatal("empty chain accepted")
	}
}

func selfSigned(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "flowseer-central"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return cert
}

func TestPublishOutsideTheBufferedBranchesFailsLoudly(t *testing.T) {
	hub := startHub(t, t.TempDir(), -1)
	leaf := startLeaf(t, t.TempDir(), hub, edgeID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Inside the edge's subtree but on neither buffered branch: no stream
	// holds it, so the publish must fail rather than be acknowledged by
	// nothing.
	err := leaf.Publish(ctx, leaf.Subject("misc.record"), []byte("lost?"), "")
	if err == nil {
		t.Fatal("a publish outside the buffered branches was acknowledged")
	}
	if code, ok := errs.CodeOf(err); !ok || code != edgebus.ErrCodeLeaf {
		t.Fatalf("publish error: %v (code %q)", err, code)
	}
}

// TestOneEdgeCannotAddressAnotherEdgesJetStreamAPI constructs the isolation boundary.
// Before per-edge accounts, every edge's leaf carried its own
// $JS.edge-<id>.API interest in one shared account, so a reflection an edge
// provoked could name another edge's STREAM.DELETE and destroy its buffer.
// Each edge now has its own account, so edge A's leaf cannot address edge
// B's JetStream API at all, reflected or direct, and B's buffer survives.
func TestOneEdgeCannotAddressAnotherEdgesJetStreamAPI(t *testing.T) {
	hub := startHub(t, t.TempDir(), -1)
	a := startLeaf(t, t.TempDir(), hub, edgeID)
	b := startLeaf(t, t.TempDir(), hub, otherID)
	waitFor(t, "both leaves", 10*time.Second, func() bool { return hub.LeafCount() == 2 })
	if err := hub.AttachEdge(context.Background(), edgebus.DefaultTenant, edgeID); err != nil {
		t.Fatalf("attach a: %v", err)
	}
	if err := hub.AttachEdge(context.Background(), edgebus.DefaultTenant, otherID); err != nil {
		t.Fatalf("attach b: %v", err)
	}

	// B has a record in its own buffer.
	if err := b.Publish(context.Background(), b.OTelSubject(edgebus.SignalLogs), []byte("b-record"), ""); err != nil {
		t.Fatalf("b publish: %v", err)
	}
	bBuffer, err := jetstream.NewWithDomain(b.Connection(), edgebus.EdgeDomain(otherID))
	if err != nil {
		t.Fatalf("b buffer ctx: %v", err)
	}
	waitFor(t, "b buffered", 10*time.Second, func() bool {
		st, err := bBuffer.Stream(context.Background(), edgebus.EdgeBufferStream)
		if err != nil {
			return false
		}
		info, err := st.Info(context.Background())
		return err == nil && info.State.Msgs == 1
	})

	// A tries to delete B's buffer through B's JetStream API, both a plain
	// request and a header-only one shaped like the reflected control frame.
	apiDelete := "$JS." + edgebus.EdgeDomain(otherID) + ".API.STREAM.DELETE." + edgebus.EdgeBufferStream
	if err := a.Connection().Publish(apiDelete, nil); err != nil {
		t.Logf("a delete publish rejected locally: %v", err) // a permissions error here is the point
	}
	_ = a.Connection().Flush()
	time.Sleep(2 * time.Second)

	st, err := bBuffer.Stream(context.Background(), edgebus.EdgeBufferStream)
	if err != nil {
		t.Fatalf("b buffer after: %v", err)
	}
	info, err := st.Info(context.Background())
	if err != nil {
		t.Fatalf("b buffer info: %v", err)
	}
	if info.State.Msgs != 1 {
		t.Fatalf("edge A reached edge B's buffer: %d records remain", info.State.Msgs)
	}
}

// credsFileFor renders minted credentials the way AttachBus does before
// they cross the wire, so a test builds a leaf from the same bytes an edge
// receives.
func credsFileFor(t *testing.T, creds edgebus.EdgeCredentials) []byte {
	t.Helper()
	body, err := creds.CredsFile()
	if err != nil {
		t.Fatalf("render credentials: %v", err)
	}
	return body
}

// TestARestartSkipsAPersistedEdgeItCannotRestore covers the two ways a key
// file in the state directory outlives the hub's ability to use it: an id
// that is no longer usable as a stream name, and a seed a crash left short.
// Neither may stop the hub, since refusing to start leaves every other edge
// unserved for one of them — and the skip runs with no Logger configured,
// which is what every caller but the device host passes.
func TestARestartSkipsAPersistedEdgeItCannotRestore(t *testing.T) {
	dir := t.TempDir()
	first := startHub(t, dir, 0)
	if err := first.AttachEdge(context.Background(), edgebus.DefaultTenant, edgeID); err != nil {
		t.Fatalf("attach: %v", err)
	}
	first.Close()

	keys := filepath.Join(dir, "keys")
	for name, body := range map[string][]byte{
		"edge-not.a.stream.name.nk": []byte("SAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"),
		"edge-truncated.nk":         []byte("SA"),
	} {
		if err := os.WriteFile(filepath.Join(keys, name), body, 0o600); err != nil {
			t.Fatalf("plant %s: %v", name, err)
		}
	}

	second, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
		StateDir:    dir,
		FsyncPolicy: service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	t.Cleanup(second.Close)

	attached := second.AttachedEdges()
	if len(attached) != 1 || attached[0] != edgeID {
		t.Fatalf("attached = %v, want only the edge whose key is usable", attached)
	}
}

func TestAuditStreamWildcardStoresEventsFromMultipleTenants(t *testing.T) {
	ctx := context.Background()
	hub := startHub(t, t.TempDir(), 0)

	const (
		tenantA = "tenant-a"
		tenantB = "tenant-b"
		device1 = "0192e6a0-0000-7000-8000-0000000000d1"
		device2 = "0192e6a0-0000-7000-8000-0000000000d2"
	)

	evA := &accessv1.DeviceOperationEvent{}
	evA.SetEventId("ev-a")
	evA.SetLaneReleased(&accessv1.LaneReleased{})
	bodyA, err := proto.Marshal(evA)
	if err != nil {
		t.Fatalf("marshal event a: %v", err)
	}

	evB := &accessv1.DeviceOperationEvent{}
	evB.SetEventId("ev-b")
	evB.SetLaneReleased(&accessv1.LaneReleased{})
	bodyB, err := proto.Marshal(evB)
	if err != nil {
		t.Fatalf("marshal event b: %v", err)
	}

	subA := edgebus.AuditSubject(tenantA, device1)
	subB := edgebus.AuditSubject(tenantB, device2)

	if err := hub.Connection().Publish(subA, bodyA); err != nil {
		t.Fatalf("publish tenant a event: %v", err)
	}
	if err := hub.Connection().Publish(subB, bodyB); err != nil {
		t.Fatalf("publish tenant b event: %v", err)
	}
	if err := hub.Connection().Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	stream, err := hub.JetStream().Stream(ctx, edgebus.AuditStream)
	if err != nil {
		t.Fatalf("audit stream: %v", err)
	}

	waitFor(t, "two audit records stored", 10*time.Second, func() bool {
		info, err := stream.Info(ctx)
		return err == nil && info.State.Msgs == 2
	})

	msg1, err := stream.GetMsg(ctx, 1)
	if err != nil {
		t.Fatalf("get msg 1: %v", err)
	}
	if msg1.Subject != subA {
		t.Errorf("msg1 subject = %s, want %s", msg1.Subject, subA)
	}

	msg2, err := stream.GetMsg(ctx, 2)
	if err != nil {
		t.Fatalf("get msg 2: %v", err)
	}
	if msg2.Subject != subB {
		t.Errorf("msg2 subject = %s, want %s", msg2.Subject, subB)
	}
}

func TestMintEdgeUserPerTenant(t *testing.T) {
	ctx := context.Background()
	hub := startHub(t, t.TempDir(), 0)

	const customTenant = "11111111-2222-3333-4444-555555555555"
	if err := hub.AttachEdge(ctx, customTenant, edgeID); err != nil {
		t.Fatalf("attach edge: %v", err)
	}

	creds, err := hub.MintEdgeUser(ctx, edgeID)
	if err != nil {
		t.Fatalf("mint user: %v", err)
	}

	claims, err := jwt.DecodeUserClaims(creds.UserJWT.RevealString())
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}

	expectedSubtree := "flowseer." + customTenant + ".edge." + edgeID + ".>"
	found := false
	for _, sub := range claims.Pub.Allow {
		if sub == expectedSubtree {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("pub permissions %v do not contain %s", claims.Pub.Allow, expectedSubtree)
	}
}

func TestRestartedHubReattachesEdgeUnderPersistedTenant(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	const customTenant = "11111111-2222-3333-4444-555555555555"
	first := startHub(t, dir, 0)
	if err := first.AttachEdge(ctx, customTenant, edgeID); err != nil {
		t.Fatalf("attach edge: %v", err)
	}
	if got, ok := first.EdgeTenant(edgeID); !ok || got != customTenant {
		t.Fatalf("first hub edge tenant = %s (ok=%v), want %s", got, ok, customTenant)
	}
	first.Close()

	sidecarPath := filepath.Join(dir, "keys", "edge-"+edgeID+".tenant")
	content, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read tenant sidecar: %v", err)
	}
	if strings.TrimSpace(string(content)) != customTenant {
		t.Fatalf("sidecar content = %q, want %q", string(content), customTenant)
	}

	second, err := edgebus.StartHub(ctx, edgebus.HubConfig{
		StateDir:    dir,
		FsyncPolicy: service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("restart hub: %v", err)
	}
	t.Cleanup(second.Close)

	if got, ok := second.EdgeTenant(edgeID); !ok || got != customTenant {
		t.Fatalf("second hub edge tenant = %s (ok=%v), want %s", got, ok, customTenant)
	}

	creds, err := second.MintEdgeUser(ctx, edgeID)
	if err != nil {
		t.Fatalf("mint user on restarted hub: %v", err)
	}
	claims, err := jwt.DecodeUserClaims(creds.UserJWT.RevealString())
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	expectedSubtree := "flowseer." + customTenant + ".edge." + edgeID + ".>"
	found := false
	for _, sub := range claims.Pub.Allow {
		if sub == expectedSubtree {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("restarted hub pub permissions %v do not contain %s", claims.Pub.Allow, expectedSubtree)
	}
}

func TestTenantBucketAllowsAtomicPublishAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	first := startHub(t, dir, 0)
	tenantStream, err := first.JetStream().Stream(ctx, "KV_"+edgebus.TenantBucket)
	if err != nil {
		t.Fatalf("load tenant stream: %v", err)
	}
	if !tenantStream.CachedInfo().Config.AllowAtomicPublish {
		t.Fatal("first hub tenant stream AllowAtomicPublish = false, want true")
	}
	first.Close()

	second, err := edgebus.StartHub(ctx, edgebus.HubConfig{
		StateDir:    dir,
		FsyncPolicy: service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("restart hub: %v", err)
	}
	t.Cleanup(second.Close)

	tenantStream, err = second.JetStream().Stream(ctx, "KV_"+edgebus.TenantBucket)
	if err != nil {
		t.Fatalf("load tenant stream after restart: %v", err)
	}
	if !tenantStream.CachedInfo().Config.AllowAtomicPublish {
		t.Fatal("restarted hub tenant stream AllowAtomicPublish = false, want true")
	}

	laneStream, err := second.JetStream().Stream(ctx, "KV_"+edgebus.LaneBucket)
	if err != nil {
		t.Fatalf("load lane stream after restart: %v", err)
	}
	if laneStream.CachedInfo().Config.AllowAtomicPublish {
		t.Fatal("lane stream AllowAtomicPublish = true, want false")
	}
}

func TestAttachEdgeRejectsInvalidTenant(t *testing.T) {
	ctx := context.Background()
	hub := startHub(t, t.TempDir(), 0)

	invalidTenants := []string{
		"not-a-uuid",
		"acme.prod",
		"../escape",
		"tenant-prod-42",
		"",
	}
	for _, bad := range invalidTenants {
		if err := hub.AttachEdge(ctx, bad, edgeID); err == nil {
			t.Errorf("AttachEdge with invalid tenant %q succeeded, want error", bad)
		}
	}
}

func TestMintEdgeUserRefusesUnknownEdge(t *testing.T) {
	ctx := context.Background()
	hub := startHub(t, t.TempDir(), 0)

	if _, err := hub.MintEdgeUser(ctx, "unknown-edge"); err == nil {
		t.Fatal("MintEdgeUser on unknown edge succeeded, want error")
	}
}

func TestAttachEdgeRejectsConflictingTenant(t *testing.T) {
	ctx := context.Background()
	hub := startHub(t, t.TempDir(), 0)

	const (
		tenant1 = "11111111-1111-1111-1111-111111111111"
		tenant2 = "22222222-2222-2222-2222-222222222222"
	)
	if err := hub.AttachEdge(ctx, tenant1, edgeID); err != nil {
		t.Fatalf("initial AttachEdge: %v", err)
	}
	if err := hub.AttachEdge(ctx, tenant2, edgeID); err == nil {
		t.Fatal("AttachEdge with conflicting tenant succeeded, want error")
	}
}

func TestRestartedHubFailsOnUnreadableSidecar(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	const customTenant = "11111111-2222-3333-4444-555555555555"
	const healthyEdge = "edge-healthy"
	first := startHub(t, dir, 0)
	if err := first.AttachEdge(ctx, customTenant, edgeID); err != nil {
		t.Fatalf("attach edge: %v", err)
	}
	if err := first.AttachEdge(ctx, customTenant, healthyEdge); err != nil {
		t.Fatalf("attach healthy edge: %v", err)
	}
	first.Close()

	sidecarPath := filepath.Join(dir, "keys", "edge-"+edgeID+".tenant")
	if err := os.Chmod(sidecarPath, 0o000); err != nil {
		t.Fatalf("chmod 0000 sidecar: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(sidecarPath, 0o600)
	})

	second, err := edgebus.StartHub(ctx, edgebus.HubConfig{
		StateDir:    dir,
		FsyncPolicy: service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("StartHub failed on unreadable sidecar: %v", err)
	}
	defer second.Close()

	got, ok := second.EdgeTenant(healthyEdge)
	if !ok || got != customTenant {
		t.Fatalf("healthy edge tenant = (%q, %v), want (%q, true)", got, ok, customTenant)
	}
	if _, ok := second.EdgeTenant(edgeID); ok {
		t.Fatalf("broken edge tenant unexpectedly present")
	}
}

func TestZeroHubEdgeTenantReturnsUnknown(t *testing.T) {
	var h edgebus.Hub
	if tenant, ok := h.EdgeTenant("edge-1"); ok || tenant != "" {
		t.Fatalf("zero Hub EdgeTenant = (%q, %v), want (\"\", false)", tenant, ok)
	}
}

func TestLeafPublishesUnderAssignedTenant(t *testing.T) {
	ctx := context.Background()
	const (
		customTenant = "0192e6a0-aaaa-7000-8000-0000000000a1"
		edge1        = "0192e6a0-eeee-7000-8000-0000000000e1"
	)

	hub := startHub(t, t.TempDir(), -1)
	if err := hub.AttachEdge(ctx, customTenant, edge1); err != nil {
		t.Fatalf("attach edge: %v", err)
	}
	creds, err := hub.MintEdgeUser(ctx, edge1)
	if err != nil {
		t.Fatalf("mint edge user: %v", err)
	}

	leaf, err := edgebus.StartLeaf(ctx, edgebus.LeafConfig{
		StateDir:        t.TempDir(),
		EdgeID:          edge1,
		Tenant:          customTenant,
		HubURLs:         []string{hub.ListenURL()},
		CredentialsFile: secret.New(credsFileFor(t, creds)),
		FsyncPolicy:     service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("start leaf: %v", err)
	}
	t.Cleanup(leaf.Close)

	waitFor(t, "leaf link", 10*time.Second, func() bool { return hub.LeafCount() == 1 })

	c, collectorSrv := newCollector(t)
	forwarder, err := edgebus.StartForwarder(ctx, hub, edgebus.ForwarderConfig{
		Endpoint:   collectorSrv.URL,
		RetryDelay: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start forwarder: %v", err)
	}
	t.Cleanup(forwarder.Close)

	body := []byte{0x0a, 0x03, 0x01, 0x02, 0x03}
	metricsSubject := leaf.OTelSubject(edgebus.SignalMetrics)
	wantSubject := edgebus.OTelSubject(customTenant, edge1, edgebus.SignalMetrics)
	if metricsSubject != wantSubject {
		t.Fatalf("leaf OTelSubject = %q, want %q", metricsSubject, wantSubject)
	}

	if err := leaf.Publish(ctx, metricsSubject, body, ""); err != nil {
		t.Fatalf("leaf publish: %v", err)
	}

	waitFor(t, "forwarded metrics", 15*time.Second, func() bool {
		return len(c.received("/v1/metrics")) == 1
	})
	if got := c.received("/v1/metrics")[0]; !bytes.Equal(got, body) {
		t.Fatalf("forwarded body = %x, want %x", got, body)
	}
}

func TestTenantFromSubjects(t *testing.T) {
	const (
		validEdgeID = "0192e6a0-0000-7000-8000-0000000000ed"
		validTenant = "0192e6a0-0000-7000-8000-0000000000c1"
	)

	tests := []struct {
		name       string
		edgeID     string
		subjects   map[string]string
		wantTenant string
		wantErr    bool
	}{
		{
			name:     "empty map",
			edgeID:   validEdgeID,
			subjects: map[string]string{},
			wantErr:  true,
		},
		{
			name:   "malformed short subject",
			edgeID: validEdgeID,
			subjects: map[string]string{
				"otel.logs": "flowseer.default.edge",
			},
			wantErr: true,
		},
		{
			name:   "malformed wrong prefix",
			edgeID: validEdgeID,
			subjects: map[string]string{
				"otel.logs": "other.default.edge." + validEdgeID + ".otel.logs",
			},
			wantErr: true,
		},
		{
			name:   "malformed wrong middle segment",
			edgeID: validEdgeID,
			subjects: map[string]string{
				"otel.logs": "flowseer.default.device." + validEdgeID + ".otel.logs",
			},
			wantErr: true,
		},
		{
			name:   "foreign edge id",
			edgeID: validEdgeID,
			subjects: map[string]string{
				"otel.logs": "flowseer." + validTenant + ".edge.0192e6a0-0000-7000-8000-0000000000ee.otel.logs",
			},
			wantErr: true,
		},
		{
			name:   "upper case UUID tenant",
			edgeID: validEdgeID,
			subjects: map[string]string{
				"otel.logs": "flowseer.0192E6A0-0000-7000-8000-0000000000C1.edge." + validEdgeID + ".otel.logs",
			},
			wantErr: true,
		},
		{
			name:   "mismatched tenants",
			edgeID: validEdgeID,
			subjects: map[string]string{
				"otel.logs":    "flowseer." + validTenant + ".edge." + validEdgeID + ".otel.logs",
				"otel.metrics": "flowseer.default.edge." + validEdgeID + ".otel.metrics",
			},
			wantErr: true,
		},
		{
			name:       "valid default tenant",
			edgeID:     validEdgeID,
			subjects:   edgebus.EdgePublishSubjects(edgebus.DefaultTenant, validEdgeID),
			wantTenant: edgebus.DefaultTenant,
		},
		{
			name:       "valid UUID tenant",
			edgeID:     validEdgeID,
			subjects:   edgebus.EdgePublishSubjects(validTenant, validEdgeID),
			wantTenant: validTenant,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := edgebus.TenantFromSubjects(tc.edgeID, tc.subjects)
			if (err != nil) != tc.wantErr {
				t.Fatalf("TenantFromSubjects() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.wantTenant {
				t.Errorf("TenantFromSubjects() = %q, want %q", got, tc.wantTenant)
			}
		})
	}
}

func TestStartLeafRefusesEmptyAndInvalidTenant(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	cfg := edgebus.LeafConfig{
		StateDir:        stateDir,
		EdgeID:          edgeID,
		HubURLs:         []string{"ws://127.0.0.1:1"},
		CredentialsFile: secret.New([]byte("creds")),
		FsyncPolicy:     service.BusFsyncPeriodic,
	}

	cfg.Tenant = ""
	_, err := edgebus.StartLeaf(context.Background(), cfg)
	if code, ok := errs.CodeOf(err); !ok || code != edgebus.ErrCodeConfig {
		t.Fatalf("StartLeaf with empty tenant: err=%v, code=%q, want ErrCodeConfig", err, code)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("empty tenant created state directory: stat error = %v", err)
	}

	cfg.Tenant = "invalid-tenant"
	_, err = edgebus.StartLeaf(context.Background(), cfg)
	if code, ok := errs.CodeOf(err); !ok || code != edgebus.ErrCodeConfig {
		t.Fatalf("StartLeaf with invalid tenant: err=%v, code=%q, want ErrCodeConfig", err, code)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("invalid tenant created state directory: stat error = %v", err)
	}
}

func TestOperatorActionStreamMaxPerSubject(t *testing.T) {
	ctx := context.Background()
	hub, err := edgebus.StartHub(ctx, edgebus.HubConfig{
		StateDir:                    t.TempDir(),
		FsyncPolicy:                 service.BusFsyncPeriodic,
		OperatorActionMaxPerSubject: 2,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	defer hub.Close()

	const tenantID = "tenant-op-test"
	subj1 := edgebus.OperatorActionSubject(tenantID, "edge_create")
	subj2 := edgebus.OperatorActionSubject(tenantID, "edge_get")

	// Publish three records on subj1 with distinct msgIDs.
	for i, body := range []string{"rec-1", "rec-2", "rec-3"} {
		if _, err := hub.JetStream().Publish(ctx, subj1, []byte(body), jetstream.WithMsgID(fmt.Sprintf("msg-s1-%d", i))); err != nil {
			t.Fatalf("publish on subj1: %v", err)
		}
	}
	// Publish one record on subj2.
	if _, err := hub.JetStream().Publish(ctx, subj2, []byte("rec-4"), jetstream.WithMsgID("msg-s2-0")); err != nil {
		t.Fatalf("publish on subj2: %v", err)
	}

	stream, err := hub.JetStream().Stream(ctx, edgebus.OperatorActionStream)
	if err != nil {
		t.Fatalf("get operator action stream: %v", err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	if info.State.Msgs != 3 {
		t.Fatalf("stream messages = %d, want 3", info.State.Msgs)
	}

	// Consume messages from subj1: should receive only the newest two ("rec-2" and "rec-3").
	cons1, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		FilterSubject: subj1,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		t.Fatalf("create consumer for subj1: %v", err)
	}
	batch1, err := cons1.Fetch(10, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatalf("fetch subj1: %v", err)
	}
	var got1 []string
	for msg := range batch1.Messages() {
		got1 = append(got1, string(msg.Data()))
		_ = msg.Ack()
	}
	if len(got1) != 2 || got1[0] != "rec-2" || got1[1] != "rec-3" {
		t.Fatalf("subj1 messages = %v, want [rec-2 rec-3]", got1)
	}

	// Consume messages from subj2: should receive the one ("rec-4").
	cons2, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		FilterSubject: subj2,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		t.Fatalf("create consumer for subj2: %v", err)
	}
	batch2, err := cons2.Fetch(10, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatalf("fetch subj2: %v", err)
	}
	var got2 []string
	for msg := range batch2.Messages() {
		got2 = append(got2, string(msg.Data()))
		_ = msg.Ack()
	}
	if len(got2) != 1 || got2[0] != "rec-4" {
		t.Fatalf("subj2 messages = %v, want [rec-4]", got2)
	}
}

func TestOperatorActionStreamExistsUnderDefaultBudgetWithKVWriteAccepted(t *testing.T) {
	ctx := context.Background()
	hub := startHub(t, t.TempDir(), 0)

	if _, err := hub.JetStream().Stream(ctx, edgebus.AuditStream); err != nil {
		t.Fatalf("audit stream missing: %v", err)
	}
	if _, err := hub.JetStream().Stream(ctx, edgebus.OperatorActionStream); err != nil {
		t.Fatalf("operator action stream missing: %v", err)
	}

	kv, err := hub.JetStream().KeyValue(ctx, edgebus.LaneBucket)
	if err != nil {
		t.Fatalf("get lane bucket: %v", err)
	}
	if _, err := kv.Put(ctx, "dev-test-1", []byte("lane-data")); err != nil {
		t.Fatalf("kv write into lane bucket failed: %v", err)
	}
}

func TestHubStartFailsWhenCentralBudgetBelowStreamSum(t *testing.T) {
	ctx := context.Background()
	// Audit stream default: 256 MiB. Operator action stream default: 64 MiB. Sum: 320 MiB.
	// A central budget of 300 MiB is below the sum of reservations and must fail StartHub.
	_, err := edgebus.StartHub(ctx, edgebus.HubConfig{
		StateDir:           t.TempDir(),
		FsyncPolicy:        service.BusFsyncPeriodic,
		CentralBudgetBytes: 300 << 20,
	})
	if err == nil {
		t.Fatal("StartHub with central budget below stream sum succeeded, want error")
	}
}
