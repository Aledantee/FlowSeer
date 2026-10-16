package intake

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	rawwindowv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/rawwindow/v1"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

func publishPrepared(t *testing.T, hub *edgebus.Hub, tenantID, edgeID string, record *ingestv1.IngestRecord, data []byte) {
	t.Helper()
	publications, reason, retryType := prepareRecord(tenantID, edgeID, record, data, func(msg proto.Message) error { return validateEnvelope(msg.(*ingestv1.IngestRecord)) })
	if reason != "" || retryType != "" {
		t.Fatalf("prepare record: refusal %q, retry %q", reason, retryType)
	}
	for _, item := range publications {
		if _, err := hub.JetStream().Publish(context.Background(), item.subject, item.data, jetstream.WithMsgID(item.messageID)); err != nil {
			t.Fatalf("publish prepared record: %v", err)
		}
	}
}

func TestRawEvidenceIsMovedToTheEvidenceStream(t *testing.T) {
	hub := startHub(t)
	envelope, data := validEnvelope(t, testEdge, testDevice, testRecordID, true)
	publishPrepared(t, hub, testTenant, testEdge, envelope, data)

	typed, err := hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("load typed stream: %v", err)
	}
	evidence, err := hub.JetStream().Stream(context.Background(), edgebus.EvidenceStream)
	if err != nil {
		t.Fatalf("load evidence stream: %v", err)
	}

	evidenceMsg, err := evidence.GetLastMsgForSubject(context.Background(), edgebus.EvidenceSubject(testTenant, edgebus.IngestRecordTypeSyslog, testDevice))
	if err != nil {
		t.Fatalf("read evidence: %v", err)
	}
	if !bytes.Equal(evidenceMsg.Data, data) {
		t.Fatalf("evidence bytes differ from published bytes")
	}
	typedMsg, err := typed.GetLastMsgForSubject(context.Background(), edgebus.CentralIngestSubject(testTenant, edgebus.IngestRecordTypeSyslog, testDevice))
	if err != nil {
		t.Fatalf("read typed record: %v", err)
	}
	var got ingestv1.IngestRecord
	if err := proto.Unmarshal(typedMsg.Data, &got); err != nil {
		t.Fatalf("decode typed record: %v", err)
	}
	want := proto.Clone(envelope).(*ingestv1.IngestRecord)
	want.ClearRaw()
	if !proto.Equal(&got, want) {
		t.Fatalf("typed record = %v, want %v", &got, want)
	}
	if got.GetRaw() != nil {
		t.Fatal("typed record retained raw evidence")
	}
}

func TestWindowEvidenceKeepsItsRefsAndTheTypedRecordKeepsNone(t *testing.T) {
	hub := startHub(t)
	envelope, _ := validEnvelope(t, testEdge, testDevice, testRecordID, false)
	windows := []string{"0198a3c0-0000-7000-8000-000000000a01", "0198a3c0-0000-7000-8000-000000000a02"}
	refs := make([]*rawwindowv1.RawWindowLocalRef, len(windows))
	for i, id := range windows {
		refs[i] = rawwindowv1.RawWindowLocalRef_builder{Id: proto.String(id)}.Build()
	}
	envelope.SetRaw(ingestv1.RawEvidence_builder{
		Data:    []byte("window bytes"),
		Reason:  ingestv1.RawReason_RAW_REASON_WINDOW.Enum(),
		Windows: refs,
	}.Build())
	if err := validateEnvelope(envelope); err != nil {
		t.Fatalf("validate window envelope: %v", err)
	}
	data, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal window envelope: %v", err)
	}
	publishPrepared(t, hub, testTenant, testEdge, envelope, data)

	typed, evidence := centralStreams(t, hub)

	evidenceMsg, err := evidence.GetLastMsgForSubject(context.Background(), edgebus.EvidenceSubject(testTenant, edgebus.IngestRecordTypeSyslog, testDevice))
	if err != nil {
		t.Fatalf("read evidence: %v", err)
	}
	var kept ingestv1.IngestRecord
	if err := proto.Unmarshal(evidenceMsg.Data, &kept); err != nil {
		t.Fatalf("decode evidence: %v", err)
	}
	var got []string
	for _, ref := range kept.GetRaw().GetWindows() {
		got = append(got, ref.GetId())
	}
	if !slices.Equal(got, windows) {
		t.Errorf("evidence window refs = %v, want %v", got, windows)
	}

	typedMsg, err := typed.GetLastMsgForSubject(context.Background(), edgebus.CentralIngestSubject(testTenant, edgebus.IngestRecordTypeSyslog, testDevice))
	if err != nil {
		t.Fatalf("read typed record: %v", err)
	}
	var published ingestv1.IngestRecord
	if err := proto.Unmarshal(typedMsg.Data, &published); err != nil {
		t.Fatalf("decode typed record: %v", err)
	}
	if published.HasRaw() {
		t.Errorf("typed record kept raw evidence with refs %v, want none", published.GetRaw().GetWindows())
	}
}

func TestValidatorInfrastructureFailureIsRetried(t *testing.T) {
	s := newHandlerSystem(t, handlerOptions{})
	var logs bytes.Buffer
	s.handler.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	s.handler.validator = func(proto.Message) error { return errors.New("validator unavailable") }
	if got := mustDeliver(t, s, record(t, testEdge, testRecordID, false)); got != 0 {
		t.Fatalf("settled = %d, want 0", got)
	}
	if got := s.typedMessages(t); got != 0 {
		t.Errorf("typed messages = %d, want 0", got)
	}
	if got := metricValue(t, s.reader, "flowseer.intake.records.retried", "flowseer.intake.reason", "validation"); got != 1 {
		t.Errorf("validation retries = %d, want 1", got)
	}
	if !strings.Contains(logs.String(), `"error.type":"validation"`) {
		t.Errorf("retry log = %s, want validation error.type", logs.String())
	}
}

func TestSameRecordIDInTwoTenantsStoresBoth(t *testing.T) {
	hub := startHub(t)
	first, firstData := validEnvelope(t, testEdge, testDevice, testRecordID, false)
	second, secondData := validEnvelope(t, otherEdge, otherDevice, testRecordID, false)
	publishPrepared(t, hub, testTenant, testEdge, first, firstData)
	publishPrepared(t, hub, otherTenant, otherEdge, second, secondData)
	typed, _ := centralStreams(t, hub)
	info, err := typed.Info(context.Background())
	if err != nil {
		t.Fatalf("typed stream info: %v", err)
	}
	if got := info.State.Msgs; got != 2 {
		t.Fatalf("typed messages = %d, want 2", got)
	}
	for _, subject := range []string{
		edgebus.CentralIngestSubject(testTenant, edgebus.IngestRecordTypeSyslog, testDevice),
		edgebus.CentralIngestSubject(otherTenant, edgebus.IngestRecordTypeSyslog, otherDevice),
	} {
		if _, err := typed.GetLastMsgForSubject(context.Background(), subject); err != nil {
			t.Fatalf("read tenant subject %q: %v", subject, err)
		}
	}
}
