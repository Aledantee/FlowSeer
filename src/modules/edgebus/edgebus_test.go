package edgebus_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"strings"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

const (
	edgeID  = "0192e6a0-0000-7000-8000-0000000000ed"
	otherID = "0192e6a0-0000-7000-8000-0000000000ee"
)

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
	creds, err := hub.MintEdgeUser(id)
	if err != nil {
		t.Fatalf("mint edge user: %v", err)
	}
	return startLeafWith(t, dir, hub.ListenURL(), id, creds)
}

func startLeafWith(t *testing.T, dir, url, id string, creds edgebus.EdgeCredentials) *edgebus.Leaf {
	t.Helper()
	leaf, err := edgebus.StartLeaf(context.Background(), edgebus.LeafConfig{
		StateDir:    dir,
		EdgeID:      id,
		HubURLs:     []string{url},
		Credentials: creds,
		FsyncPolicy: service.BusFsyncPeriodic,
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

func TestEdgePermissionsConfineTheLeaf(t *testing.T) {
	hub := startHub(t, t.TempDir(), -1)
	leaf := startLeaf(t, t.TempDir(), hub, edgeID)
	waitFor(t, "leaf link", 10*time.Second, func() bool { return hub.LeafCount() == 1 })

	// A publish under another edge's subtree must not cross into the hub.
	foreign := make(chan struct{}, 1)
	own := make(chan struct{}, 1)
	if _, err := hub.EdgeConnection().Subscribe(edgebus.EdgeSubtree(edgebus.DefaultTenant, otherID)+".>", func(*nats.Msg) { foreign <- struct{}{} }); err != nil {
		t.Fatalf("subscribe foreign: %v", err)
	}
	if _, err := hub.EdgeConnection().Subscribe(edgebus.EdgeSubtree(edgebus.DefaultTenant, edgeID)+".probe", func(*nats.Msg) { own <- struct{}{} }); err != nil {
		t.Fatalf("subscribe own: %v", err)
	}
	if err := hub.EdgeConnection().Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	// Interest propagates over the leaf link asynchronously.
	time.Sleep(500 * time.Millisecond)
	if err := leaf.Connection().Publish(edgebus.EdgeSubtree(edgebus.DefaultTenant, otherID)+".probe", []byte("x")); err != nil {
		t.Fatalf("publish foreign: %v", err)
	}
	if err := leaf.Connection().Publish(edgebus.EdgeSubtree(edgebus.DefaultTenant, edgeID)+".probe", []byte("x")); err != nil {
		t.Fatalf("publish own: %v", err)
	}
	if err := leaf.Connection().Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	select {
	case <-own:
	case <-time.After(5 * time.Second):
		t.Fatal("a publish under the edge's own subtree did not reach the hub")
	}
	select {
	case <-foreign:
		t.Fatal("a publish under another edge's subtree reached the hub")
	case <-time.After(500 * time.Millisecond):
	}

	// A subscription on the leaf outside its permitted set imports nothing
	// the hub publishes.
	leaked := make(chan struct{}, 1)
	if _, err := leaf.Connection().Subscribe("flowseer.>", func(*nats.Msg) { leaked <- struct{}{} }); err != nil {
		t.Fatalf("subscribe on leaf: %v", err)
	}
	if err := leaf.Connection().Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := hub.EdgeConnection().Publish("flowseer.default.dispatch.anything", []byte("x")); err != nil {
		t.Fatalf("publish on hub: %v", err)
	}
	if err := hub.EdgeConnection().Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	select {
	case <-leaked:
		t.Fatal("a hub publish outside the edge's permitted set reached the leaf")
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
	creds, err := first.MintEdgeUser(edgeID)
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

// TestEdgeCredentialCannotReachCentralStreams constructs finding 1: anything
// holding an edge credential tries to write into the journal bucket and the
// audit stream, both directly and through the flow-control reflection the
// server obeys with its own permissionless internal client. The account
// split puts those streams where an edge credential cannot address them, so
// central's record is unchanged whatever the edge publishes.
func TestEdgeCredentialCannotReachCentralStreams(t *testing.T) {
	hub := startHub(t, t.TempDir(), -1)
	leaf := startLeaf(t, t.TempDir(), hub, edgeID)
	waitFor(t, "leaf link", 10*time.Second, func() bool { return hub.LeafCount() == 1 })
	if err := hub.AttachEdge(context.Background(), edgeID); err != nil {
		t.Fatalf("attach: %v", err)
	}

	// Central writes a lane record through its own central-account context.
	kv, err := hub.JetStream().KeyValue(context.Background(), edgebus.LaneBucket)
	if err != nil {
		t.Fatalf("bucket: %v", err)
	}
	key := "0192e6a0-0000-7000-8000-0000000000d1"
	if _, err := kv.Put(context.Background(), key, []byte("genuine")); err != nil {
		t.Fatalf("put: %v", err)
	}

	// A direct publish to the KV subject and the audit subject: the edge is
	// in the edge account and cannot even address these.
	for _, subject := range []string{
		"$KV." + edgebus.LaneBucket + "." + key,
		edgebus.AuditSubject(edgebus.DefaultTenant, key),
	} {
		if err := leaf.Connection().Publish(subject, []byte("forged")); err != nil {
			t.Fatalf("publish %s: %v", subject, err)
		}
	}
	// A flow-control control message on the edge's source branch naming the
	// KV subject as its reply: an empty body and the "NATS/1.0 100 " header
	// the server reads as a control frame.
	control := &nats.Msg{
		Subject: edgebus.EdgeSubtree(edgebus.DefaultTenant, edgeID) + ".source.forged",
		Reply:   "$KV." + edgebus.LaneBucket + "." + key,
		Header:  nats.Header{"Status": []string{"100 FlowControl Request"}},
	}
	if err := leaf.Connection().PublishMsg(control); err != nil {
		t.Fatalf("publish control: %v", err)
	}
	if err := leaf.Connection().Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	time.Sleep(2 * time.Second)

	entry, err := kv.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(entry.Value()) != "genuine" {
		t.Fatalf("lane record was overwritten through an edge credential: %q", entry.Value())
	}
	if entry.Revision() != 1 {
		t.Fatalf("lane record revision moved to %d; an edge credential reached the bucket", entry.Revision())
	}
}

// TestAnEdgeCannotStoreARecordAsAnotherEdge constructs the relabel of
// finding 2 with what an edge credential can actually reach. The edge
// cannot learn the source consumer's delivery subject: it is not granted
// publish on its own JetStream API, and the consumer is not enumerable from
// the edge credential (TestEdgePermissionsConfineTheLeaf and the source
// consumer's absence from the edge's own consumer listing are that
// property). So the strongest an edge can do is publish onto its own source
// branch, which it may, carrying a $JS.ACK reply whose @-suffix names
// another edge's subject, the field the sourcing path reads the stored
// subject from. The account split and the per-source SubjectTransform keep
// every stored record under this edge's subtree, and the forwarder refuses
// any that is not (TestForwarderRefusesARecordOutsideItsStreamsEdge), so no
// record is stored or shipped as another edge's.
func TestAnEdgeCannotStoreARecordAsAnotherEdge(t *testing.T) {
	hub := startHub(t, t.TempDir(), -1)
	leaf := startLeaf(t, t.TempDir(), hub, edgeID)
	waitFor(t, "leaf link", 10*time.Second, func() bool { return hub.LeafCount() == 1 })
	if err := hub.AttachEdge(context.Background(), edgeID); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := leaf.Publish(context.Background(), leaf.OTelSubject(edgebus.SignalLogs), []byte("genuine"), ""); err != nil {
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

	other := edgebus.OTelSubject(edgebus.DefaultTenant, otherID, edgebus.SignalLogs)
	for _, branch := range []string{".source.S.forged", ".source.forged"} {
		forged := &nats.Msg{
			Subject: edgebus.EdgeSubtree(edgebus.DefaultTenant, edgeID) + branch,
			Reply:   "$JS.ACK.EDGE_BUFFER.forged.1.9.9.1.0@" + other,
			Data:    []byte("relabeled"),
		}
		for range 3 {
			if err := leaf.Connection().PublishMsg(forged); err != nil {
				t.Fatalf("forge: %v", err)
			}
		}
	}
	if err := leaf.Connection().Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	time.Sleep(2 * time.Second)

	info, err := stream.Info(context.Background(), jetstream.WithSubjectFilter(">"))
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if _, relabeled := info.State.Subjects[other]; relabeled {
		t.Fatalf("a forged reply stored a record under another edge's subject: %v", info.State.Subjects)
	}
	for subject := range info.State.Subjects {
		if !strings.HasPrefix(subject, edgebus.EdgeSubtree(edgebus.DefaultTenant, edgeID)+".") {
			t.Fatalf("a stored record left the edge's subtree: %s", subject)
		}
	}
}
