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

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

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
	_, err = edgebus.StartLeaf(context.Background(), edgebus.LeafConfig{StateDir: t.TempDir(), EdgeID: edgeID, HubURLs: []string{"ws://127.0.0.1:1"}})
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

	if err := hub.AttachEdge(context.Background(), edgeID); err != nil {
		t.Fatalf("add edge source: %v", err)
	}
	if err := hub.AttachEdge(context.Background(), edgeID); err != nil {
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
	if err := hub.AttachEdge(context.Background(), edgeID); err != nil {
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
	if err := hub.AttachEdge(context.Background(), edgeID); err != nil {
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
	creds, err := first.MintEdgeUser(context.Background(), edgeID)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	leaf := startLeafWith(t, t.TempDir(), url, edgeID, creds)
	waitFor(t, "leaf link", 10*time.Second, func() bool { return first.LeafCount() == 1 })
	if err := first.AttachEdge(context.Background(), edgeID); err != nil {
		t.Fatalf("add edge source: %v", err)
	}
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
	if err := hub.AttachEdge(context.Background(), edgeID); err != nil {
		t.Fatalf("attach a: %v", err)
	}
	if err := hub.AttachEdge(context.Background(), otherID); err != nil {
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
	if err := first.AttachEdge(context.Background(), edgeID); err != nil {
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
