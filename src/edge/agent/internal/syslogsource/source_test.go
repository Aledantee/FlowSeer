package syslogsource_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"

	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	netlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/log/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/secret"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/lanehost"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/syslogsource"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/protocol/syslog"
)

func startHub(t *testing.T, dir string) *edgebus.Hub {
	t.Helper()
	hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
		StateDir:    dir,
		FsyncPolicy: service.BusFsyncPeriodic,
		ListenPort:  -1,
	})
	if err != nil {
		t.Fatalf("start hub: %v (%v)", err, errs.Attributes(err))
	}
	t.Cleanup(hub.Close)
	return hub
}

func startLeaf(t *testing.T, dir string, hub *edgebus.Hub) *edgebus.Leaf {
	t.Helper()
	id := testEdgeID
	if err := hub.AttachEdge(context.Background(), edgebus.DefaultTenant, id); err != nil {
		t.Fatalf("attach edge: %v", err)
	}
	creds, err := hub.MintEdgeUser(context.Background(), id)
	if err != nil {
		t.Fatalf("mint edge user: %v", err)
	}
	credsBytes, err := creds.CredsFile()
	if err != nil {
		t.Fatalf("render credentials: %v", err)
	}
	leaf, err := edgebus.StartLeaf(context.Background(), edgebus.LeafConfig{
		StateDir:        dir,
		EdgeID:          id,
		Tenant:          edgebus.DefaultTenant,
		HubURLs:         []string{hub.ListenURL()},
		CredentialsFile: secret.New(credsBytes),
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
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func readMetricSum(reader *sdkmetric.ManualReader, name string) (int64, bool) {
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		return 0, false
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
					var total int64
					for _, dp := range sum.DataPoints {
						total += dp.Value
					}
					return total, true
				}
			}
		}
	}
	return 0, false
}

// readMetricAttr returns the value of one attribute on the first data point of
// a counter, and whether the counter has such a point.
func readMetricAttr(reader *sdkmetric.ManualReader, name, key string) (string, bool) {
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		return "", false
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if m.Name != name || !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				if v, found := dp.Attributes.Value(attribute.Key(key)); found {
					return v.AsString(), true
				}
			}
		}
	}
	return "", false
}

func TestSource_UDPHostedAndUnknownAddress(t *testing.T) {
	t.Parallel()

	hub := startHub(t, t.TempDir())
	leaf := startLeaf(t, t.TempDir(), hub)
	waitFor(t, "leaf link", 10*time.Second, func() bool { return hub.LeafCount() == 1 && leaf.HubConnected() })

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	meter := mp.Meter("test")

	index := lanehost.NewDeviceIndex()
	bindRef := inventoryv1.BindingGlobalRef_builder{
		Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(testBindingID)}.Build(),
	}.Build()
	index.Add("192.0.2.1", testDeviceID, bindRef)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src, err := syslogsource.Listen(ctx, syslogsource.Config{
		Listeners: []syslog.ListenConfig{
			{Transport: syslog.UDP, Address: "127.0.0.1:0"},
		},
		Index:     index,
		Publisher: leaf,
		EdgeRef:   testEdgeRef(),
		Meter:     meter,
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = src.Close() }()

	spawn.Go(ctx, "syslog-source-runner", func() {
		_ = src.Run(ctx)
	})

	udpAddr := src.Receiver().Addresses()[0].Address
	conn, err := net.Dial("udp", udpAddr)
	if err != nil {
		t.Fatalf("Dial UDP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	payload := []byte("<34>1 2026-10-03T10:00:00Z sw1 app - - - unknown source drop")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write unknown UDP: %v", err)
	}

	waitFor(t, "dropped metric", 5*time.Second, func() bool {
		val, ok := readMetricSum(reader, "flowseer.edge.syslog.dropped")
		return ok && val >= 1
	})

	if reason, ok := readMetricAttr(reader, "flowseer.edge.syslog.dropped", "flowseer.edge.syslog.reason"); !ok || reason != "unknown_source" {
		t.Errorf("dropped reason = %q (found=%t), want unknown_source", reason, ok)
	}

	index.Add("127.0.0.1", testDeviceID, bindRef)

	hostedPayload := []byte("<34>1 2026-10-03T10:00:00Z sw1 app - - - hosted link down")
	if _, err := conn.Write(hostedPayload); err != nil {
		t.Fatalf("write hosted UDP: %v", err)
	}

	stream, err := hub.EdgeStream(context.Background(), testEdgeID)
	if err != nil {
		t.Fatalf("hub edge stream: %v", err)
	}

	subject := leaf.Subject("ingest.syslog")
	waitFor(t, "stored message in hub stream", 10*time.Second, func() bool {
		msg, err := stream.GetLastMsgForSubject(context.Background(), subject)
		return err == nil && msg != nil
	})

	msg, err := stream.GetLastMsgForSubject(context.Background(), subject)
	if err != nil {
		t.Fatalf("read message from stream: %v", err)
	}

	var env ingestv1.IngestRecord
	if err := proto.Unmarshal(msg.Data, &env); err != nil {
		t.Fatalf("unmarshal IngestRecord: %v", err)
	}

	if err := protovalidate.Validate(&env); err != nil {
		t.Fatalf("protovalidate: %v", err)
	}

	recordID := env.GetRecordId()
	if msgID := msg.Header.Get("Nats-Msg-Id"); msgID != recordID {
		t.Errorf("Nats-Msg-Id header = %q, want record_id %q", msgID, recordID)
	}

	if srcStream := msg.Header.Get("Nats-Stream-Source"); srcStream == "" {
		t.Error("Nats-Stream-Source header is empty, want stream name")
	}

	if env.GetSyslog().GetHostname() != "sw1" {
		t.Errorf("hostname = %q, want sw1", env.GetSyslog().GetHostname())
	}
	if string(env.GetSyslog().GetMessage()) != "hosted link down" {
		t.Errorf("message = %q, want 'hosted link down'", string(env.GetSyslog().GetMessage()))
	}
}

func TestSource_DualStackListenerResolvesAnIPv4Device(t *testing.T) {
	t.Parallel()

	hub := startHub(t, t.TempDir())
	leaf := startLeaf(t, t.TempDir(), hub)
	waitFor(t, "leaf link", 10*time.Second, func() bool { return hub.LeafCount() == 1 && leaf.HubConnected() })

	index := lanehost.NewDeviceIndex()
	index.Add("127.0.0.1", testDeviceID, inventoryv1.BindingGlobalRef_builder{
		Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(testBindingID)}.Build(),
	}.Build())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src, err := syslogsource.Listen(ctx, syslogsource.Config{
		Listeners: []syslog.ListenConfig{{Transport: syslog.UDP, Address: "[::]:0"}},
		Index:     index,
		Publisher: leaf,
		EdgeRef:   testEdgeRef(),
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = src.Close() }()
	spawn.Go(ctx, "syslog-source-runner", func() { _ = src.Run(ctx) })

	_, port, err := net.SplitHostPort(src.Receiver().Addresses()[0].Address)
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}
	conn, err := net.Dial("udp4", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Fatalf("Dial UDP: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("<34>1 2026-10-03T10:00:00Z sw1 app - - - link down")); err != nil {
		t.Fatalf("write UDP: %v", err)
	}

	stream, err := hub.EdgeStream(context.Background(), testEdgeID)
	if err != nil {
		t.Fatalf("hub edge stream: %v", err)
	}
	subject := leaf.Subject("ingest.syslog")
	var rawMsg *jetstream.RawStreamMsg
	waitFor(t, "stored message in hub stream", 10*time.Second, func() bool {
		msg, err := stream.GetLastMsgForSubject(context.Background(), subject)
		if err == nil && msg != nil {
			rawMsg = msg
			return true
		}
		return false
	})
	if rawMsg == nil {
		t.Fatal("no message stored in hub stream")
	}

	var env ingestv1.IngestRecord
	if err := proto.Unmarshal(rawMsg.Data, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	syslogRec := env.GetSyslog()
	if syslogRec.GetHostname() != "sw1" {
		t.Errorf("hostname = %q, want sw1", syslogRec.GetHostname())
	}
	if string(syslogRec.GetMessage()) != "link down" {
		t.Errorf("message = %q, want 'link down'", string(syslogRec.GetMessage()))
	}
	srcAddr := syslogRec.GetSourceAddress()
	if !srcAddr.HasV4() {
		t.Fatalf("SourceAddress = %v, want V4 arm", srcAddr)
	}
	wantOctets := []byte{127, 0, 0, 1}
	if !bytes.Equal(srcAddr.GetV4().GetOctets(), wantOctets) {
		t.Errorf("SourceAddress.V4 = %v, want %v", srcAddr.GetV4().GetOctets(), wantOctets)
	}
}

func TestSource_TCPFiveFramingsAndAutoCases(t *testing.T) {
	t.Parallel()

	hub := startHub(t, t.TempDir())
	leaf := startLeaf(t, t.TempDir(), hub)
	waitFor(t, "leaf link", 10*time.Second, func() bool { return hub.LeafCount() == 1 && leaf.HubConnected() })

	index := lanehost.NewDeviceIndex()
	bindRef := inventoryv1.BindingGlobalRef_builder{
		Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(testBindingID)}.Build(),
	}.Build()
	index.Add("127.0.0.1", testDeviceID, bindRef)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	listeners := []syslog.ListenConfig{
		{Transport: syslog.TCP, Address: "127.0.0.1:0", Framing: syslog.Auto},
		{Transport: syslog.TCP, Address: "127.0.0.1:0", Framing: syslog.OctetCounting},
		{Transport: syslog.TCP, Address: "127.0.0.1:0", Framing: syslog.LF},
		{Transport: syslog.TCP, Address: "127.0.0.1:0", Framing: syslog.CRLF},
		{Transport: syslog.TCP, Address: "127.0.0.1:0", Framing: syslog.NUL},
	}

	src, err := syslogsource.Listen(ctx, syslogsource.Config{
		Listeners: listeners,
		Index:     index,
		Publisher: leaf,
		EdgeRef:   testEdgeRef(),
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = src.Close() }()

	spawn.Go(ctx, "syslog-source-runner", func() {
		_ = src.Run(ctx)
	})

	stream, err := hub.EdgeStream(context.Background(), testEdgeID)
	if err != nil {
		t.Fatalf("hub edge stream: %v", err)
	}
	subject := leaf.Subject("ingest.syslog")

	endpoints := src.Receiver().Addresses()
	if len(endpoints) != 5 {
		t.Fatalf("len(endpoints) = %d, want 5", len(endpoints))
	}

	autoAddr := endpoints[0].Address
	payload1 := []byte("<34>1 2026-10-03T10:00:00Z sw1 app - - - auto-form1")
	frame1 := append([]byte(fmt.Sprintf("%d ", len(payload1))), payload1...)
	sendTCP(t, autoAddr, frame1)
	msg1 := waitForMessage(t, stream, subject, "auto-form1")
	if msg1 == nil {
		t.Fatal("auto-form1 message not found")
	}

	sendTCP(t, autoAddr, []byte("<34>1 2026-10-03T10:00:00Z sw1 app - - - auto-form2\n"))
	msg2 := waitForMessage(t, stream, subject, "auto-form2")
	if msg2 == nil {
		t.Fatal("auto-form2 message not found")
	}

	octetAddr := endpoints[1].Address
	payloadOctet := []byte("<34>1 2026-10-03T10:00:00Z sw1 app - - - octet-msg")
	frameOctet := append([]byte(fmt.Sprintf("%d ", len(payloadOctet))), payloadOctet...)
	sendTCP(t, octetAddr, frameOctet)
	msg3 := waitForMessage(t, stream, subject, "octet-msg")
	if msg3 == nil {
		t.Fatal("octet-msg message not found")
	}

	lfAddr := endpoints[2].Address
	sendTCP(t, lfAddr, []byte("Oct  3 10:00:00 sw1 app: up\n"))
	msg4 := waitForMessage(t, stream, subject, "up")
	if msg4 == nil {
		t.Fatal("LF line message not found")
	}
	var envLF ingestv1.IngestRecord
	if err := proto.Unmarshal(msg4.Data, &envLF); err != nil {
		t.Fatalf("unmarshal LF record: %v", err)
	}
	if envLF.GetSyslog().HasSeverity() {
		t.Errorf("LF record severity = %v, want unset", envLF.GetSyslog().GetSeverity())
	}

	crlfAddr := endpoints[3].Address
	sendTCP(t, crlfAddr, []byte("Oct  3 10:00:00 sw1 app: crlf-up\r\n"))
	msg5 := waitForMessage(t, stream, subject, "crlf-up")
	if msg5 == nil {
		t.Fatal("CRLF message not found")
	}

	nulAddr := endpoints[4].Address
	sendTCP(t, nulAddr, []byte("Oct  3 10:00:00 sw1 app: nul-up\x00"))
	msg6 := waitForMessage(t, stream, subject, "nul-up")
	if msg6 == nil {
		t.Fatal("NUL message not found")
	}

	before := src.Receiver().Stats()

	conn, err := net.Dial("tcp", autoAddr)
	if err != nil {
		t.Fatalf("Dial auto: %v", err)
	}
	_, _ = conn.Write([]byte("Oct  3 10:00:00 sw1 app: up\n"))

	buf := make([]byte, 16)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Read(buf)
	if !errors.Is(err, io.EOF) {
		t.Errorf("read error = %v, want io.EOF", err)
	}
	_ = conn.Close()

	waitFor(t, "framing_errors increment", 2*time.Second, func() bool {
		return src.Receiver().Stats().FramingErrors == before.FramingErrors+1
	})
	// The receiver counts a frame as received before it queues it, so an
	// unchanged count shows the LF line never became a record.
	if got := src.Receiver().Stats().Received; got != before.Received {
		t.Errorf("received = %d, want %d: the LF line under auto framing was accepted", got, before.Received)
	}
}

func TestSource_OctetCounted65535ByteFrameIsCutAndKeepsRaw(t *testing.T) {
	t.Parallel()

	hub := startHub(t, t.TempDir())
	leaf := startLeaf(t, t.TempDir(), hub)
	waitFor(t, "leaf link", 10*time.Second, func() bool { return hub.LeafCount() == 1 && leaf.HubConnected() })

	index := lanehost.NewDeviceIndex()
	bindRef := inventoryv1.BindingGlobalRef_builder{
		Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(testBindingID)}.Build(),
	}.Build()
	index.Add("127.0.0.1", testDeviceID, bindRef)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src, err := syslogsource.Listen(ctx, syslogsource.Config{
		Listeners: []syslog.ListenConfig{
			{Transport: syslog.TCP, Address: "127.0.0.1:0", Framing: syslog.OctetCounting},
		},
		Index:     index,
		Publisher: leaf,
		EdgeRef:   testEdgeRef(),
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = src.Close() }()

	spawn.Go(ctx, "syslog-source-runner", func() {
		_ = src.Run(ctx)
	})

	stream, err := hub.EdgeStream(context.Background(), testEdgeID)
	if err != nil {
		t.Fatalf("hub edge stream: %v", err)
	}
	subject := leaf.Subject("ingest.syslog")

	largePayload := bytes.Repeat([]byte("z"), 65535)
	frameHeader := fmt.Sprintf("%d ", len(largePayload))
	frame := append([]byte(frameHeader), largePayload...)

	tcpAddr := src.Receiver().Addresses()[0].Address
	sendTCP(t, tcpAddr, frame)

	waitFor(t, "large payload in stream", 10*time.Second, func() bool {
		msg, err := stream.GetLastMsgForSubject(context.Background(), subject)
		return err == nil && msg != nil
	})

	msg, err := stream.GetLastMsgForSubject(context.Background(), subject)
	if err != nil {
		t.Fatalf("read message: %v", err)
	}

	var env ingestv1.IngestRecord
	if err := proto.Unmarshal(msg.Data, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if err := protovalidate.Validate(&env); err != nil {
		t.Fatalf("protovalidate: %v", err)
	}

	s := env.GetSyslog()
	if len(s.GetMessage()) != 65527 {
		t.Errorf("message len = %d, want 65527", len(s.GetMessage()))
	}
	if !s.GetMessageTruncated() {
		t.Errorf("message_truncated = false, want true")
	}

	raw := env.GetRaw()
	if raw == nil {
		t.Fatal("raw evidence = nil, want present")
	}
	if len(raw.GetData()) != 65535 {
		t.Errorf("raw data len = %d, want 65535", len(raw.GetData()))
	}
	if raw.GetReason() != ingestv1.RawReason_RAW_REASON_PARSE_FAILURE {
		t.Errorf("raw reason = %v, want RAW_REASON_PARSE_FAILURE", raw.GetReason())
	}
}

type retryPublisherStub struct {
	mu       sync.Mutex
	attempts int
	msgIDs   []string
	done     chan struct{}
}

func (s *retryPublisherStub) Subject(string) string {
	return "flowseer.test.ingest.syslog"
}

func (s *retryPublisherStub) Publish(_ context.Context, _ string, _ []byte, msgID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	s.msgIDs = append(s.msgIDs, msgID)
	if s.attempts == 1 {
		return errors.New("buffer temporarily refused")
	}
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return nil
}

func TestSource_PublisherRetryWithBackoff(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	meter := mp.Meter("test")

	index := lanehost.NewDeviceIndex()
	bindRef := inventoryv1.BindingGlobalRef_builder{
		Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(testBindingID)}.Build(),
	}.Build()
	index.Add("127.0.0.1", testDeviceID, bindRef)

	stub := &retryPublisherStub{done: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src, err := syslogsource.Listen(ctx, syslogsource.Config{
		Listeners: []syslog.ListenConfig{
			{Transport: syslog.UDP, Address: "127.0.0.1:0"},
		},
		Index:          index,
		Publisher:      stub,
		EdgeRef:        testEdgeRef(),
		Meter:          meter,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = src.Close() }()

	spawn.Go(ctx, "syslog-source-runner", func() {
		_ = src.Run(ctx)
	})

	udpAddr := src.Receiver().Addresses()[0].Address
	conn, err := net.Dial("udp", udpAddr)
	if err != nil {
		t.Fatalf("Dial UDP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	payload := []byte("<34>1 2026-10-03T10:00:00Z sw1 app - - - retry test")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write UDP: %v", err)
	}

	select {
	case <-stub.done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for publisher retry")
	}

	waitFor(t, "published metric", 5*time.Second, func() bool {
		val, ok := readMetricSum(reader, "flowseer.edge.syslog.published")
		return ok && val == 1
	})

	stub.mu.Lock()
	defer stub.mu.Unlock()

	if stub.attempts != 2 {
		t.Errorf("stub.attempts = %d, want 2", stub.attempts)
	}
	if len(stub.msgIDs) != 2 || stub.msgIDs[0] != stub.msgIDs[1] {
		t.Errorf("stub.msgIDs = %v, want same msgID twice", stub.msgIDs)
	}

	retries, ok := readMetricSum(reader, "flowseer.edge.syslog.publish.retries")
	if !ok || retries != 1 {
		t.Errorf("publish.retries metric = %d (ok=%t), want 1", retries, ok)
	}

	published, ok := readMetricSum(reader, "flowseer.edge.syslog.published")
	if !ok || published != 1 {
		t.Errorf("published metric = %d (ok=%t), want 1", published, ok)
	}
}

func sendTCP(t *testing.T, addr string, data []byte) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial TCP %s: %v", addr, err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write TCP %s: %v", addr, err)
	}
}

func waitForMessage(t *testing.T, stream jetstream.Stream, subject, substring string) *jetstream.RawStreamMsg {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		msg, err := stream.GetLastMsgForSubject(context.Background(), subject)
		if err == nil && msg != nil && bytes.Contains(msg.Data, []byte(substring)) {
			return msg
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

func TestSource_ListenRefusesAnEdgeRefWithoutAnID(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	noID := edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{}.Build()}.Build()
	for name, ref := range map[string]*edgev1.EdgeGlobalRef{"nil": nil, "no id": noID} {
		t.Run(name, func(t *testing.T) {
			_, err := syslogsource.Listen(ctx, syslogsource.Config{
				Listeners: []syslog.ListenConfig{
					{Transport: syslog.UDP, Address: "127.0.0.1:0"},
				},
				Index:     lanehost.NewDeviceIndex(),
				Publisher: &retryPublisherStub{},
				EdgeRef:   ref,
			})
			if err == nil {
				t.Fatal("Listen succeeded, want a refusal")
			}
			const want = "syslog source requires an edge reference with an id"
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Listen error = %q, want it to contain %q", err, want)
			}
		})
	}
}

type retryFailingPublisherStub struct {
	called chan struct{}
	once   sync.Once
}

func (p *retryFailingPublisherStub) Subject(string) string {
	return "flowseer.test.ingest.syslog"
}

func (p *retryFailingPublisherStub) Publish(context.Context, string, []byte, string) error {
	p.once.Do(func() {
		close(p.called)
	})
	return errors.New("temporary publish failure")
}

func TestSource_RunReturnsNilOnCancellationDuringRetry(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	index := lanehost.NewDeviceIndex()
	bindRef := inventoryv1.BindingGlobalRef_builder{
		Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(testBindingID)}.Build(),
	}.Build()
	index.Add("127.0.0.1", testDeviceID, bindRef)

	stub := &retryFailingPublisherStub{called: make(chan struct{})}

	src, err := syslogsource.Listen(ctx, syslogsource.Config{
		Listeners: []syslog.ListenConfig{
			{Transport: syslog.UDP, Address: "127.0.0.1:0"},
		},
		Index:          index,
		Publisher:      stub,
		EdgeRef:        testEdgeRef(),
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = src.Close() }()

	errCh := make(chan error, 1)
	spawn.Go(ctx, "syslog-source-runner", func() {
		errCh <- src.Run(ctx)
	})

	udpAddr := src.Receiver().Addresses()[0].Address
	conn, err := net.Dial("udp", udpAddr)
	if err != nil {
		t.Fatalf("Dial UDP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	payload := []byte("<34>1 2026-10-03T10:00:00Z sw1 app - - - retry test")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write UDP: %v", err)
	}

	select {
	case <-stub.called:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Publish to be called")
	}

	cancel()

	select {
	case runErr := <-errCh:
		if runErr != nil {
			t.Errorf("Run returned error %v, want nil on context cancellation", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to return")
	}
}

// blockingPublisherStub holds Publish until its context ends, as a buffer that
// is mid-write when the agent shuts down does, and returns the context error.
type blockingPublisherStub struct {
	called chan struct{}
	once   sync.Once
}

func (p *blockingPublisherStub) Subject(string) string {
	return "flowseer.test.ingest.syslog"
}

func (p *blockingPublisherStub) Publish(ctx context.Context, _ string, _ []byte, _ string) error {
	p.once.Do(func() { close(p.called) })
	<-ctx.Done()
	return ctx.Err()
}

func TestSource_RunReturnsNilWhenCanceledInsidePublish(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	index := lanehost.NewDeviceIndex()
	bindRef := inventoryv1.BindingGlobalRef_builder{
		Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(testBindingID)}.Build(),
	}.Build()
	index.Add("127.0.0.1", testDeviceID, bindRef)

	stub := &blockingPublisherStub{called: make(chan struct{})}
	src, err := syslogsource.Listen(ctx, syslogsource.Config{
		Listeners: []syslog.ListenConfig{
			{Transport: syslog.UDP, Address: "127.0.0.1:0"},
		},
		Index:     index,
		Publisher: stub,
		EdgeRef:   testEdgeRef(),
		Meter:     mp.Meter("test"),
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = src.Close() }()

	errCh := make(chan error, 1)
	spawn.Go(ctx, "syslog-source-runner", func() {
		errCh <- src.Run(ctx)
	})

	conn, err := net.Dial("udp", src.Receiver().Addresses()[0].Address)
	if err != nil {
		t.Fatalf("Dial UDP: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("<34>1 2026-10-03T10:00:00Z sw1 app - - - publish blocks")); err != nil {
		t.Fatalf("write UDP: %v", err)
	}

	select {
	case <-stub.called:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Publish to be called")
	}
	cancel()

	select {
	case runErr := <-errCh:
		if runErr != nil {
			t.Errorf("Run returned error %v, want nil on context cancellation", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to return")
	}
	if retries, ok := readMetricSum(reader, "flowseer.edge.syslog.publish.retries"); ok && retries != 0 {
		t.Errorf("publish.retries = %d, want 0: a canceled publish is not a refusal", retries)
	}
}

type capturingPublisherStub struct {
	mu      sync.Mutex
	records []*ingestv1.IngestRecord
}

func (s *capturingPublisherStub) Subject(string) string {
	return "flowseer.test.ingest.syslog"
}

func (s *capturingPublisherStub) Publish(_ context.Context, _ string, data []byte, _ string) error {
	var env ingestv1.IngestRecord
	if err := proto.Unmarshal(data, &env); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, &env)
	return nil
}

func (s *capturingPublisherStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

func (s *capturingPublisherStub) recordAt(i int) *ingestv1.IngestRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records[i]
}

func TestSource_RawPolicySuppressionAndMetrics(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	meter := mp.Meter("test")

	index := lanehost.NewDeviceIndex()
	bindRef := inventoryv1.BindingGlobalRef_builder{
		Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(testBindingID)}.Build(),
	}.Build()
	index.Add("127.0.0.1", testDeviceID, bindRef)

	stub := &capturingPublisherStub{}
	policy := syslogsource.NewRawPolicy(1, 3, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src, err := syslogsource.Listen(ctx, syslogsource.Config{
		Listeners: []syslog.ListenConfig{
			{Transport: syslog.UDP, Address: "127.0.0.1:0"},
		},
		Index:     index,
		Publisher: stub,
		EdgeRef:   testEdgeRef(),
		Meter:     meter,
		Policy:    policy,
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = src.Close() }()

	spawn.Go(ctx, "syslog-source-runner", func() {
		_ = src.Run(ctx)
	})

	udpAddr := src.Receiver().Addresses()[0].Address
	conn, err := net.Dial("udp", udpAddr)
	if err != nil {
		t.Fatalf("Dial UDP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	completePayload := []byte("<34>1 2026-10-03T10:00:00Z sw1 app 123 ID42 [exampleSDID@32473 iut=\"3\"] complete msg")
	if _, err := conn.Write(completePayload); err != nil {
		t.Fatalf("write complete UDP: %v", err)
	}

	waitFor(t, "record 1 (complete)", 5*time.Second, func() bool {
		return stub.count() == 1
	})

	rec0 := stub.recordAt(0)
	if rec0.GetRaw() != nil {
		t.Errorf("complete record carries raw evidence %v, want nil", rec0.GetRaw())
	}
	s := rec0.GetSyslog()
	if s == nil {
		t.Fatal("syslog record is nil")
	}
	if s.GetFacility() != netlogv1.SyslogFacility_SYSLOG_FACILITY_AUTH {
		t.Errorf("facility = %v, want SYSLOG_FACILITY_AUTH (4)", s.GetFacility())
	}
	if s.GetSeverity() != netlogv1.SyslogSeverity_SYSLOG_SEVERITY_CRITICAL {
		t.Errorf("severity = %v, want SYSLOG_SEVERITY_CRITICAL (2)", s.GetSeverity())
	}

	if _, err := conn.Write([]byte("parse failure 1")); err != nil {
		t.Fatalf("write failure 1: %v", err)
	}
	waitFor(t, "record 2 (failure 1)", 5*time.Second, func() bool {
		return stub.count() == 2
	})
	rec1 := stub.recordAt(1)
	if rec1.GetRaw() == nil {
		t.Fatal("failure 1 raw evidence is nil, want kept")
	}
	if rec1.GetRaw().GetSuppressedSinceLast() != 0 {
		t.Errorf("failure 1 suppressed = %d, want 0", rec1.GetRaw().GetSuppressedSinceLast())
	}

	if _, err := conn.Write([]byte("parse failure 2")); err != nil {
		t.Fatalf("write failure 2: %v", err)
	}
	waitFor(t, "record 3 (failure 2)", 5*time.Second, func() bool {
		return stub.count() == 3
	})
	rec2 := stub.recordAt(2)
	if rec2.GetRaw() != nil {
		t.Errorf("failure 2 raw evidence = %v, want nil", rec2.GetRaw())
	}

	if _, err := conn.Write([]byte("parse failure 3")); err != nil {
		t.Fatalf("write failure 3: %v", err)
	}
	waitFor(t, "record 4 (failure 3)", 5*time.Second, func() bool {
		return stub.count() == 4
	})
	rec3 := stub.recordAt(3)
	if rec3.GetRaw() != nil {
		t.Errorf("failure 3 raw evidence = %v, want nil", rec3.GetRaw())
	}

	if _, err := conn.Write([]byte("parse failure 4")); err != nil {
		t.Fatalf("write failure 4: %v", err)
	}
	waitFor(t, "record 5 (failure 4)", 5*time.Second, func() bool {
		return stub.count() == 5
	})
	rec4 := stub.recordAt(4)
	if rec4.GetRaw() == nil {
		t.Fatal("failure 4 raw evidence is nil, want kept")
	}
	if rec4.GetRaw().GetSuppressedSinceLast() != 2 {
		t.Errorf("failure 4 suppressed = %d, want 2", rec4.GetRaw().GetSuppressedSinceLast())
	}

	waitFor(t, "raw metrics", 5*time.Second, func() bool {
		kept, ok1 := readMetricSum(reader, "flowseer.edge.syslog.raw.kept")
		suppressed, ok2 := readMetricSum(reader, "flowseer.edge.syslog.raw.suppressed")
		return ok1 && ok2 && kept == 2 && suppressed == 2
	})

	kept, ok := readMetricSum(reader, "flowseer.edge.syslog.raw.kept")
	if !ok || kept != 2 {
		t.Errorf("raw.kept metric = %d (ok=%t), want 2", kept, ok)
	}
	suppressed, ok := readMetricSum(reader, "flowseer.edge.syslog.raw.suppressed")
	if !ok || suppressed != 2 {
		t.Errorf("raw.suppressed metric = %d (ok=%t), want 2", suppressed, ok)
	}
}

func TestSource_DatagramFromSharedAddressYieldsNoEnvelopeAndAmbiguousDroppedCount(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	meter := mp.Meter("test")

	index := lanehost.NewDeviceIndex()
	bindRef := inventoryv1.BindingGlobalRef_builder{
		Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(testBindingID)}.Build(),
	}.Build()
	if err := protovalidate.Validate(bindRef); err != nil {
		t.Fatalf("bindRef: %v", err)
	}
	index.Add("127.0.0.1", testDeviceID, bindRef)
	index.Add("127.0.0.1", "0192e6a0-0000-7000-8000-0000000000d2", bindRef)

	stub := &capturingPublisherStub{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src, err := syslogsource.Listen(ctx, syslogsource.Config{
		Listeners: []syslog.ListenConfig{
			{Transport: syslog.UDP, Address: "127.0.0.1:0"},
		},
		Index:     index,
		Publisher: stub,
		EdgeRef:   testEdgeRef(),
		Meter:     meter,
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = src.Close() }()

	spawn.Go(ctx, "syslog-source-runner", func() {
		_ = src.Run(ctx)
	})

	udpAddr := src.Receiver().Addresses()[0].Address
	conn, err := net.Dial("udp", udpAddr)
	if err != nil {
		t.Fatalf("Dial UDP: %v", err)
	}
	defer func() { _ = conn.Close() }()

	payload := []byte("<34>1 2026-10-03T10:00:00Z sw1 app - - - shared address drop")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write UDP 1: %v", err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write UDP 2: %v", err)
	}

	waitFor(t, "dropped metric", 5*time.Second, func() bool {
		val, ok := readMetricSum(reader, "flowseer.edge.syslog.dropped")
		return ok && val == 2
	})

	if reason, ok := readMetricAttr(reader, "flowseer.edge.syslog.dropped", "flowseer.edge.syslog.reason"); !ok || reason != "ambiguous_source" {
		t.Errorf("dropped reason = %q (found=%t), want ambiguous_source", reason, ok)
	}
	if _, found := readMetricAttr(reader, "flowseer.edge.syslog.dropped", "flowseer.device.id"); found {
		t.Error("flowseer.device.id attribute found on dropped metric, want none")
	}

	if count := stub.count(); count != 0 {
		t.Fatalf("published %d messages, want 0", count)
	}
}
