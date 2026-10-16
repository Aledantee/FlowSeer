package intake

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgeingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/ingest/v1"
	eventlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/log/v1"
	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/edgestore"
)

type handlerSystem struct {
	hub     *edgebus.Hub
	handler *Handler
	reader  *sdkmetric.ManualReader
}

// handlerOptions overrides what newHandlerSystem wires. A nil field takes the
// default: the hub's own publisher, the test edge, and the test tenant.
type handlerOptions struct {
	central    func(hub *edgebus.Hub) Publisher
	edgeID     func(context.Context) (string, error)
	edgeTenant func(context.Context, string) (string, error)
}

func newHandlerSystem(t *testing.T, opts handlerOptions) handlerSystem {
	t.Helper()
	hub := startHub(t)
	var central Publisher = hub.JetStream()
	if opts.central != nil {
		central = opts.central(hub)
	}
	if opts.edgeID == nil {
		opts.edgeID = func(context.Context) (string, error) { return testEdge, nil }
	}
	if opts.edgeTenant == nil {
		opts.edgeTenant = func(context.Context, string) (string, error) { return testTenant, nil }
	}
	reader := sdkmetric.NewManualReader()
	handler, err := NewHandler(HandlerConfig{
		Central:       central,
		EdgeID:        opts.edgeID,
		EdgeTenant:    opts.edgeTenant,
		MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return handlerSystem{hub: hub, handler: handler, reader: reader}
}

func (s handlerSystem) deliver(records ...*ingestv1.IngestRecord) (uint32, error) {
	resp, err := s.handler.Deliver(context.Background(), connect.NewRequest(edgeingestv1.DeliverRequest_builder{Records: records}.Build()))
	if err != nil {
		return 0, err
	}
	return resp.Msg.GetSettled(), nil
}

func (s handlerSystem) typedMessages(t *testing.T) uint64 {
	t.Helper()
	typed, _ := centralStreams(t, s.hub)
	info, err := typed.Info(context.Background())
	if err != nil {
		t.Fatalf("typed stream info: %v", err)
	}
	return info.State.Msgs
}

func record(t *testing.T, edgeID, recordID string, raw bool) *ingestv1.IngestRecord {
	t.Helper()
	envelope, _ := validEnvelope(t, edgeID, testDevice, recordID, raw)
	return envelope
}

func mustDeliver(t *testing.T, s handlerSystem, records ...*ingestv1.IngestRecord) uint32 {
	t.Helper()
	settled, err := s.deliver(records...)
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	return settled
}

func TestDeliveredRecordReachesTypedAndEvidenceStreams(t *testing.T) {
	s := newHandlerSystem(t, handlerOptions{})
	delivered := record(t, testEdge, testRecordID, true)

	if got := mustDeliver(t, s, delivered); got != 1 {
		t.Fatalf("settled = %d, want 1", got)
	}

	typed, evidence := centralStreams(t, s.hub)
	wantID := testTenant + "." + testRecordID
	typedMsg, err := typed.GetLastMsgForSubject(context.Background(), edgebus.CentralIngestSubject(testTenant, edgebus.IngestRecordTypeSyslog, testDevice))
	if err != nil {
		t.Fatalf("read typed record: %v", err)
	}
	if got := typedMsg.Header.Get(nats.MsgIdHdr); got != wantID {
		t.Errorf("typed Nats-Msg-Id = %q, want %q", got, wantID)
	}
	var gotTyped ingestv1.IngestRecord
	if err := proto.Unmarshal(typedMsg.Data, &gotTyped); err != nil {
		t.Fatalf("decode typed record: %v", err)
	}
	if gotTyped.HasRaw() {
		t.Error("the typed record still carries raw evidence")
	}
	evidenceMsg, err := evidence.GetLastMsgForSubject(context.Background(), edgebus.EvidenceSubject(testTenant, edgebus.IngestRecordTypeSyslog, testDevice))
	if err != nil {
		t.Fatalf("read evidence record: %v", err)
	}
	if got := evidenceMsg.Header.Get(nats.MsgIdHdr); got != wantID {
		t.Errorf("evidence Nats-Msg-Id = %q, want %q", got, wantID)
	}
	var gotEvidence ingestv1.IngestRecord
	if err := proto.Unmarshal(evidenceMsg.Data, &gotEvidence); err != nil {
		t.Fatalf("decode evidence record: %v", err)
	}
	if !proto.Equal(&gotEvidence, delivered) {
		t.Errorf("evidence record = %v, want %v", &gotEvidence, delivered)
	}
}

func TestInvalidRecordIsSettledAndRefused(t *testing.T) {
	s := newHandlerSystem(t, handlerOptions{})
	invalid := record(t, testEdge, otherRecordID, false)
	invalid.ClearProvenance()

	got := mustDeliver(t, s, record(t, testEdge, testRecordID, false), invalid, record(t, testEdge, "0198a3c0-0000-7000-8000-000000000103", false))

	if got != 3 {
		t.Fatalf("settled = %d, want 3", got)
	}
	if got := s.typedMessages(t); got != 2 {
		t.Errorf("typed messages = %d, want 2", got)
	}
	if got := metricValue(t, s.reader, "flowseer.intake.records.refused", "flowseer.intake.reason", invalidReason); got != 1 {
		t.Errorf("invalid refusals = %d, want 1", got)
	}
}

func TestForeignProvenanceIsSettledAndRefused(t *testing.T) {
	s := newHandlerSystem(t, handlerOptions{})

	got := mustDeliver(t, s, record(t, otherEdge, testRecordID, false))

	if got != 1 {
		t.Fatalf("settled = %d, want 1", got)
	}
	if got := s.typedMessages(t); got != 0 {
		t.Errorf("typed messages = %d, want 0", got)
	}
	if got := metricValue(t, s.reader, "flowseer.intake.records.refused", "flowseer.intake.reason", provenanceReason); got != 1 {
		t.Errorf("foreign provenance refusals = %d, want 1", got)
	}
}

// failingPublisher refuses its failAt-th call and forwards the rest.
type failingPublisher struct {
	mu      sync.Mutex
	inner   Publisher
	failAt  int
	failure error
	calls   int
}

func (p *failingPublisher) Publish(ctx context.Context, subject string, data []byte, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	p.mu.Lock()
	p.calls++
	fail := p.calls == p.failAt
	p.mu.Unlock()
	if fail {
		return nil, p.failure
	}
	return p.inner.Publish(ctx, subject, data, opts...)
}

func TestPublishFailureStopsTheCount(t *testing.T) {
	s := newHandlerSystem(t, handlerOptions{central: func(hub *edgebus.Hub) Publisher {
		return &failingPublisher{inner: hub.JetStream(), failAt: 2, failure: errors.New("injected publish failure")}
	}})

	got := mustDeliver(t, s,
		record(t, testEdge, testRecordID, false),
		record(t, testEdge, otherRecordID, false),
		record(t, testEdge, "0198a3c0-0000-7000-8000-000000000103", false))

	if got != 1 {
		t.Fatalf("settled = %d, want 1", got)
	}
	if got := s.typedMessages(t); got != 1 {
		t.Errorf("typed messages = %d, want 1", got)
	}
	if got := metricValue(t, s.reader, "flowseer.intake.records.retried", "flowseer.intake.reason", reasonPublishFailed); got != 1 {
		t.Errorf("publish_failed retries = %d, want 1", got)
	}
}

func TestFullTypedStreamStopsTheCount(t *testing.T) {
	full := fmt.Errorf("nats: %w", &jetstream.APIError{Code: 400, ErrorCode: 10077, Description: "maximum bytes exceeded"})
	s := newHandlerSystem(t, handlerOptions{central: func(hub *edgebus.Hub) Publisher {
		return &failingPublisher{inner: hub.JetStream(), failAt: 1, failure: full}
	}})

	got := mustDeliver(t, s, record(t, testEdge, testRecordID, false))

	if got != 0 {
		t.Fatalf("settled = %d, want 0", got)
	}
	if got := metricValue(t, s.reader, "flowseer.intake.records.retried", "flowseer.intake.reason", reasonStreamFull); got != 1 {
		t.Errorf("stream_full retries = %d, want 1", got)
	}
}

func TestTenantComesFromTheEdgeStore(t *testing.T) {
	var asked string
	s := newHandlerSystem(t, handlerOptions{edgeTenant: func(_ context.Context, edgeID string) (string, error) {
		asked = edgeID
		return otherTenant, nil
	}})

	if got := mustDeliver(t, s, record(t, testEdge, testRecordID, false)); got != 1 {
		t.Fatalf("settled = %d, want 1", got)
	}

	if asked != testEdge {
		t.Errorf("tenant asked for edge %q, want %q", asked, testEdge)
	}
	typed, _ := centralStreams(t, s.hub)
	if _, err := typed.GetLastMsgForSubject(context.Background(), edgebus.CentralIngestSubject(otherTenant, edgebus.IngestRecordTypeSyslog, testDevice)); err != nil {
		t.Fatalf("read the record under the store's tenant: %v", err)
	}
}

func TestTenantLookupErrorIsUnavailable(t *testing.T) {
	s := newHandlerSystem(t, handlerOptions{edgeTenant: func(context.Context, string) (string, error) {
		return "", errs.New().Code(edgestore.ErrCodeUnknownEdge).Msg("edge has no indexed tenant")
	}})

	_, err := s.deliver(record(t, testEdge, testRecordID, false))

	if got := connect.CodeOf(err); got != connect.CodeUnavailable {
		t.Fatalf("code = %v, want unavailable (err %v)", got, err)
	}
	if got := s.typedMessages(t); got != 0 {
		t.Errorf("typed messages = %d, want 0", got)
	}
}

func TestBatchDeliveredTwiceIsStoredOnce(t *testing.T) {
	s := newHandlerSystem(t, handlerOptions{})
	delivered := record(t, testEdge, testRecordID, false)

	for attempt := 1; attempt <= 2; attempt++ {
		if got := mustDeliver(t, s, delivered); got != 1 {
			t.Fatalf("attempt %d: settled = %d, want 1", attempt, got)
		}
	}

	if got := s.typedMessages(t); got != 1 {
		t.Errorf("typed messages = %d, want 1", got)
	}
	if got := metricValue(t, s.reader, "flowseer.intake.records.duplicate", "flowseer.intake.record_type", edgebus.IngestRecordTypeSyslog); got != 1 {
		t.Errorf("duplicates = %d, want 1", got)
	}
}

func TestMissingEdgeIsUnauthenticated(t *testing.T) {
	s := newHandlerSystem(t, handlerOptions{edgeID: func(context.Context) (string, error) {
		return "", errors.New("no assertion")
	}})

	_, err := s.deliver(record(t, testEdge, testRecordID, false))

	if got := connect.CodeOf(err); got != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want unauthenticated (err %v)", got, err)
	}
}

const (
	testTenant       = "0198a3c0-0000-7000-8000-0000000000aa"
	otherTenant      = "0198a3c0-0000-7000-8000-0000000000ab"
	testEdge         = "0198a3c0-0000-7000-8000-0000000000ed"
	otherEdge        = "0198a3c0-0000-7000-8000-0000000000ee"
	testDevice       = "0198a3c0-0000-7000-8000-000000000001"
	otherDevice      = "0198a3c0-0000-7000-8000-000000000002"
	testRecordID     = "0198a3c0-0000-7000-8000-000000000101"
	otherRecordID    = "0198a3c0-0000-7000-8000-000000000102"
	testBindingID    = "0198a3c0-0000-7000-8000-000000000201"
	invalidReason    = "invalid"
	provenanceReason = "foreign_provenance"
)

func startHub(t *testing.T) *edgebus.Hub {
	t.Helper()
	hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
		StateDir:           t.TempDir(),
		FsyncPolicy:        service.BusFsyncPeriodic,
		MaxStoreBytes:      768 << 20,
		CentralBudgetBytes: 512 << 20,
		IngestMaxBytes:     112 << 20,
		EvidenceMaxBytes:   64 << 20,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)
	return hub
}

func validEnvelope(t *testing.T, edgeID, deviceID, recordID string, raw bool) (*ingestv1.IngestRecord, []byte) {
	t.Helper()
	ref := edgev1.EdgeGlobalRef_builder{Edge: edgev1.EdgeLocalRef_builder{Id: proto.String(edgeID)}.Build()}.Build()
	device := inventoryv1.DeviceGlobalRef_builder{Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(deviceID)}.Build()}.Build()
	binding := inventoryv1.BindingGlobalRef_builder{Binding: inventoryv1.BindingLocalRef_builder{Id: proto.String(testBindingID)}.Build()}.Build()
	provenance := inventoryv1.Provenance_builder{
		Binding:    binding,
		ObservedAt: timestamppb.New(time.Now().UTC()),
		Edge:       ref,
		Log:        inventoryv1.LogProtocol_LOG_PROTOCOL_SYSLOG.Enum(),
	}.Build()
	envelope := ingestv1.IngestRecord_builder{
		RecordId:   proto.String(recordID),
		Provenance: provenance,
		Syslog: eventlogv1.SyslogRecord_builder{
			Device:     device,
			ReceivedAt: timestamppb.New(time.Now().UTC()),
			Message:    []byte("message"),
		}.Build(),
	}.Build()
	if raw {
		envelope.SetRaw(ingestv1.RawEvidence_builder{
			Data:   []byte("raw bytes"),
			Reason: ingestv1.RawReason_RAW_REASON_PARSE_FAILURE.Enum(),
		}.Build())
	}
	if err := validateEnvelope(envelope); err != nil {
		t.Fatalf("validate envelope fixture: %v", err)
	}
	data, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope fixture: %v", err)
	}
	return envelope, data
}

func validateEnvelope(envelope *ingestv1.IngestRecord) error {
	return protovalidate.Validate(envelope)
}

func centralStreams(t *testing.T, hub *edgebus.Hub) (jetstream.Stream, jetstream.Stream) {
	t.Helper()
	typed, err := hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("load typed stream: %v", err)
	}
	evidence, err := hub.JetStream().Stream(context.Background(), edgebus.EvidenceStream)
	if err != nil {
		t.Fatalf("load evidence stream: %v", err)
	}
	return typed, evidence
}

func metricValue(t *testing.T, reader *sdkmetric.ManualReader, name, key, value string) int64 {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	for _, scope := range data.ScopeMetrics {
		for _, metricData := range scope.Metrics {
			if metricData.Name != name {
				continue
			}
			sum, ok := metricData.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("metric %q has data %T, want int64 sum", name, metricData.Data)
			}
			for _, point := range sum.DataPoints {
				if key == "" {
					return point.Value
				}
				if got, ok := point.Attributes.Value(attribute.Key(key)); ok && got.AsString() == value {
					return point.Value
				}
			}
		}
	}
	return 0
}
