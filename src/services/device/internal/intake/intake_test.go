package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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
	"google.golang.org/protobuf/types/known/timestamppb"

	eventlogv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/log/v1"
	ingestv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/ingest/v1"
	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	"go.aledante.io/FlowSeer/src/common/secret"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

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
	refusalSubject   = "flowseer." + testTenant + ".edge." + otherEdge + ".ingest.syslog"
	foreignReason    = "foreign_subject"
	malformedReason  = "malformed"
	invalidReason    = "invalid"
	provenanceReason = "foreign_provenance"
)

func TestValidRecordReachesItsTypedStream(t *testing.T) {
	system := newTestSystem(t, Config{})
	envelope, data := validEnvelope(t, testEdge, testDevice, testRecordID, false)

	if err := system.leaf.Publish(context.Background(), system.leaf.Subject("ingest.syslog"), data, ""); err != nil {
		t.Fatalf("publish envelope: %v", err)
	}

	stream, err := system.hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("load typed stream: %v", err)
	}
	waitFor(t, "typed record", func() bool {
		info, err := stream.Info(context.Background())
		return err == nil && info.State.Msgs == 1
	})

	msg, err := stream.GetLastMsgForSubject(context.Background(), edgebus.CentralIngestSubject(testTenant, edgebus.IngestRecordTypeSyslog, testDevice))
	if err != nil {
		t.Fatalf("read typed record: %v", err)
	}
	var got ingestv1.IngestRecord
	if err := proto.Unmarshal(msg.Data, &got); err != nil {
		t.Fatalf("decode typed record: %v", err)
	}
	if !proto.Equal(envelope, &got) {
		t.Fatalf("typed record = %v, want %v", &got, envelope)
	}
}

func TestRedeliveredRecordIsStoredOnce(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	system := newTestSystem(t, Config{MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))})
	_, data := validEnvelope(t, testEdge, testDevice, testRecordID, false)
	msg := &fakeMsg{subject: edgebus.IngestSubject(testTenant, testEdge, "other"), data: data}

	system.intake.handle(context.Background(), testEdge, msg)
	system.intake.handle(context.Background(), testEdge, msg)

	stream, err := system.hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("load typed stream: %v", err)
	}
	waitFor(t, "duplicate publication", func() bool {
		info, err := stream.Info(context.Background())
		return err == nil && info.State.Msgs == 1
	})
	if got := metricValue(t, reader, "flowseer.intake.records.duplicate", "flowseer.intake.record_type", edgebus.IngestRecordTypeSyslog); got != 1 {
		t.Fatalf("duplicate count = %d, want 1", got)
	}
}

func TestRedeliveredRecordWithEvidenceCountsOneDuplicate(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	system := newTestSystem(t, Config{MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))})
	_, data := validEnvelope(t, testEdge, testDevice, testRecordID, true)
	msg := &fakeMsg{subject: edgebus.IngestSubject(testTenant, testEdge, "syslog"), data: data}

	system.intake.handle(context.Background(), testEdge, msg)
	system.intake.handle(context.Background(), testEdge, msg)

	if got := metricValue(t, reader, "flowseer.intake.records.duplicate", "flowseer.intake.record_type", edgebus.IngestRecordTypeSyslog); got != 1 {
		t.Fatalf("duplicate count = %d, want 1", got)
	}
}

func TestValidatorInfrastructureFailureIsRetried(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	var logs syncBuffer
	system := newTestSystem(t, Config{
		Logger:        slog.New(slog.NewJSONHandler(&logs, nil)),
		MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
		RetryDelay:    time.Millisecond,
	})
	_, data := validEnvelope(t, testEdge, testDevice, testRecordID, false)
	validatorErr := errors.New("validator unavailable")
	system.intake.validator = func(proto.Message) error { return validatorErr }
	msg := &fakeMsg{subject: edgebus.IngestSubject(testTenant, testEdge, "syslog"), data: data}

	system.intake.handle(context.Background(), testEdge, msg)

	if msg.termed || msg.acked || msg.nakDelay != time.Millisecond {
		t.Fatalf("validation infrastructure failure: termed=%v acked=%v nak=%s", msg.termed, msg.acked, msg.nakDelay)
	}
	if got := metricValue(t, reader, "flowseer.intake.records.retried", "", ""); got != 1 {
		t.Fatalf("retry count = %d, want 1", got)
	}
	assertRetryLog(t, logs.Bytes(), "validation")
}

func TestIntakeCloseCancelsInFlightPublish(t *testing.T) {
	publisher := &blockingPublisher{started: make(chan struct{})}
	system := newTestSystem(t, Config{Central: publisher})
	_, data := validEnvelope(t, testEdge, testDevice, testRecordID, false)
	if err := system.leaf.Publish(context.Background(), system.leaf.Subject("ingest.syslog"), data, ""); err != nil {
		t.Fatalf("publish envelope: %v", err)
	}
	waitFor(t, "publish to start", func() bool {
		select {
		case <-publisher.started:
			return true
		default:
			return false
		}
	})
	closed := make(chan struct{})
	go func() {
		system.intake.Close()
		close(closed)
	}()
	// This deadline covers cancellation of Publish and the consumer drain.
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("intake close did not finish while publish was in flight")
	}
	stream, err := system.hub.EdgeStream(context.Background(), testEdge)
	if err != nil {
		t.Fatalf("load edge stream: %v", err)
	}
	consumer, err := stream.Consumer(context.Background(), consumerName)
	if err != nil {
		t.Fatalf("load intake consumer: %v", err)
	}
	info, err := consumer.Info(context.Background())
	if err != nil {
		t.Fatalf("read intake consumer: %v", err)
	}
	if info.NumAckPending == 0 {
		t.Fatal("in-flight source message was acknowledged")
	}
}

func TestConsumeErrorLogUsesBoundedErrorType(t *testing.T) {
	var logs syncBuffer
	i := &Intake{
		logger:     slog.New(slog.NewJSONHandler(&logs, nil)),
		lastLogged: map[string]time.Time{},
	}
	i.logConsumeError(context.Background(), testEdge, errors.New("unbounded server detail"))

	var record map[string]any
	if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	if got := record["error.type"]; got != "consume_error" {
		t.Fatalf("error.type = %v, want consume_error", got)
	}
	if got := record["flowseer.edge.id"]; got != testEdge {
		t.Fatalf("edge id = %v, want %s", got, testEdge)
	}
	if strings.Contains(logs.String(), "unbounded server detail") {
		t.Fatal("consume error log included the raw error")
	}
}

func TestConsumeErrorLogDistinguishesNonTerminalError(t *testing.T) {
	var logs syncBuffer
	i := &Intake{
		logger:     slog.New(slog.NewJSONHandler(&logs, nil)),
		lastLogged: map[string]time.Time{},
	}
	i.logConsumeError(context.Background(), testEdge, jetstream.ErrNoHeartbeat)

	var record map[string]any
	if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	if got := record["msg"]; got != "intake consumer error" {
		t.Fatalf("log message = %v, want intake consumer error", got)
	}
	if got := record["error.type"]; got != "no_heartbeat" {
		t.Fatalf("error.type = %v, want no_heartbeat", got)
	}
}

func TestConsumeErrorLogSkipsCompletedContext(t *testing.T) {
	var logs syncBuffer
	i := &Intake{
		logger:     slog.New(slog.NewJSONHandler(&logs, nil)),
		lastLogged: map[string]time.Time{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	i.logConsumeError(ctx, testEdge, jetstream.ErrConsumerDeleted)

	if logs.Len() != 0 {
		t.Fatalf("shutdown log = %s, want empty", logs.String())
	}
}

func TestConsumeErrorLogFromDeletedConsumer(t *testing.T) {
	var logs syncBuffer
	system := newTestSystem(t, Config{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	stream, err := system.hub.EdgeStream(context.Background(), testEdge)
	if err != nil {
		t.Fatalf("load edge stream: %v", err)
	}
	if err := stream.DeleteConsumer(context.Background(), consumerName); err != nil {
		t.Fatalf("delete intake consumer: %v", err)
	}
	waitFor(t, "consumer deletion log", func() bool {
		return strings.Contains(logs.String(), "\"msg\":\"intake consumer stopped\"") &&
			strings.Contains(logs.String(), "\"error.type\":\"consumer_deleted\"")
	})
}

func TestConsumeErrorLogAlwaysLogsTerminalError(t *testing.T) {
	var logs syncBuffer
	i := &Intake{
		logger:     slog.New(slog.NewJSONHandler(&logs, nil)),
		lastLogged: map[string]time.Time{},
	}

	i.logConsumeError(context.Background(), testEdge, jetstream.ErrNoHeartbeat)
	i.logConsumeError(context.Background(), testEdge, jetstream.ErrConsumerDeleted)

	data := logs.String()
	if strings.Count(data, "\"msg\":\"intake consumer error\"") != 1 {
		t.Fatalf("non-terminal log count = %d, want 1", strings.Count(data, "\"msg\":\"intake consumer error\""))
	}
	if strings.Count(data, "\"msg\":\"intake consumer stopped\"") != 1 {
		t.Fatalf("terminal log count = %d, want 1", strings.Count(data, "\"msg\":\"intake consumer stopped\""))
	}
}

func TestRefusalAndRetryLogsUseSeparateLimits(t *testing.T) {
	var logs syncBuffer
	i := &Intake{
		logger:     slog.New(slog.NewJSONHandler(&logs, nil)),
		lastLogged: map[string]time.Time{},
	}

	i.logRefusal(context.Background(), testEdge, refusalSubject, foreignReason)
	i.logRetry(context.Background(), testEdge, refusalSubject, "validation")

	data := logs.String()
	if !strings.Contains(data, "\"msg\":\"record refused\"") {
		t.Fatal("refusal log missing")
	}
	if !strings.Contains(data, "\"msg\":\"record retry scheduled\"") {
		t.Fatal("retry log missing")
	}
}

func TestRetryLogIsLimitedPerEdge(t *testing.T) {
	var logs syncBuffer
	i := &Intake{
		logger:     slog.New(slog.NewJSONHandler(&logs, nil)),
		lastLogged: map[string]time.Time{},
	}

	for _, edgeID := range []string{testEdge, otherEdge} {
		subject := edgebus.IngestSubject(testTenant, edgeID, "syslog")
		i.logRetry(context.Background(), edgeID, subject, "validation")
		i.logRetry(context.Background(), edgeID, subject, "marshal")
	}

	assertLogEdges(t, logs.Bytes(), "record retry scheduled", testEdge, otherEdge)
}

func TestRefusalLogIsLimitedPerEdge(t *testing.T) {
	var logs syncBuffer
	i := &Intake{
		logger:     slog.New(slog.NewJSONHandler(&logs, nil)),
		lastLogged: map[string]time.Time{},
	}

	for _, edgeID := range []string{testEdge, otherEdge} {
		i.logRefusal(context.Background(), edgeID, refusalSubject, foreignReason)
		i.logRefusal(context.Background(), edgeID, refusalSubject, malformedReason)
	}

	assertLogEdges(t, logs.Bytes(), "record refused", testEdge, otherEdge)
}

func TestConsumeErrorLogIsLimitedPerEdge(t *testing.T) {
	var logs syncBuffer
	i := &Intake{
		logger:     slog.New(slog.NewJSONHandler(&logs, nil)),
		lastLogged: map[string]time.Time{},
	}

	for _, edgeID := range []string{testEdge, otherEdge} {
		i.logConsumeError(context.Background(), edgeID, jetstream.ErrNoHeartbeat)
		i.logConsumeError(context.Background(), edgeID, jetstream.ErrNoHeartbeat)
	}

	assertLogEdges(t, logs.Bytes(), "intake consumer error", testEdge, otherEdge)
}

func assertLogEdges(t *testing.T, data []byte, message string, want ...string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != len(want) {
		t.Fatalf("log line count = %d, want %d", len(lines), len(want))
	}
	wantCounts := make(map[string]int, len(want))
	for _, edgeID := range want {
		wantCounts[edgeID]++
	}
	gotCounts := make(map[string]int, len(lines))
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log: %v", err)
		}
		if got := record["msg"]; got != message {
			t.Errorf("log message = %v, want %s", got, message)
		}
		edgeID, ok := record["flowseer.edge.id"].(string)
		if !ok {
			t.Errorf("log edge id = %v, want one of %v", record["flowseer.edge.id"], want)
			continue
		}
		gotCounts[edgeID]++
	}
	for edgeID, wantCount := range wantCounts {
		if got := gotCounts[edgeID]; got != wantCount {
			t.Errorf("log count for edge %s = %d, want %d", edgeID, got, wantCount)
		}
	}
}

func assertRetryLog(t *testing.T, data []byte, errorType string) {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode retry log: %v", err)
	}
	if got := record["msg"]; got != "record retry scheduled" {
		t.Fatalf("retry log message = %v, want record retry scheduled", got)
	}
	if got := record["error.type"]; got != errorType {
		t.Fatalf("retry error.type = %v, want %s", got, errorType)
	}
}

func TestRecordFromAnEdgeWithNoTenantIsRetried(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	system := newTestSystem(t, Config{MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))})
	_, data := validEnvelope(t, otherEdge, testDevice, testRecordID, false)
	msg := &fakeMsg{subject: edgebus.IngestSubject(testTenant, otherEdge, "syslog"), data: data}

	system.intake.handle(context.Background(), otherEdge, msg)

	if msg.termed || msg.acked || msg.nakDelay != defaultRetryDelay {
		t.Fatalf("message from an edge with no tenant: termed=%v acked=%v nak=%s", msg.termed, msg.acked, msg.nakDelay)
	}
	if got := metricValue(t, reader, "flowseer.intake.records.retried", "", ""); got != 1 {
		t.Fatalf("retry count = %d, want 1", got)
	}
}

func TestForeignSubjectIsRefused(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	system := newTestSystem(t, Config{MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))})
	_, data := validEnvelope(t, testEdge, testDevice, testRecordID, false)
	msg := &fakeMsg{subject: refusalSubject, data: data}

	system.intake.handle(context.Background(), testEdge, msg)

	if !msg.termed || msg.acked || msg.nakDelay != 0 {
		t.Fatalf("foreign subject message: termed=%v acked=%v nak=%s", msg.termed, msg.acked, msg.nakDelay)
	}
	if got := metricValue(t, reader, "flowseer.intake.records.refused", "flowseer.intake.reason", foreignReason); got != 1 {
		t.Fatalf("foreign subject refusal count = %d, want 1", got)
	}
}

func TestRefusedRecordsAreTerminated(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	system := newTestSystem(t, Config{MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))})
	stream, err := system.hub.EdgeStream(context.Background(), testEdge)
	if err != nil {
		t.Fatalf("load edge stream: %v", err)
	}
	consumer, err := stream.Consumer(context.Background(), consumerName)
	if err != nil {
		t.Fatalf("load intake consumer: %v", err)
	}
	config := consumer.CachedInfo().Config
	config.AckWait = 50 * time.Millisecond
	if _, err := stream.UpdateConsumer(context.Background(), config); err != nil {
		t.Fatalf("shorten acknowledgement wait: %v", err)
	}

	_, validData := validEnvelope(t, testEdge, testDevice, testRecordID, false)
	var invalidEnvelope ingestv1.IngestRecord
	if err := proto.Unmarshal(validData, &invalidEnvelope); err != nil {
		t.Fatalf("decode valid fixture: %v", err)
	}
	invalidEnvelope.ClearRecordId()
	invalidData, err := proto.Marshal(&invalidEnvelope)
	if err != nil {
		t.Fatalf("marshal invalid fixture: %v", err)
	}
	for _, data := range [][]byte{{0xff, 0xff, 0xff}, invalidData} {
		if err := system.leaf.Publish(context.Background(), system.leaf.Subject("ingest.syslog"), data, ""); err != nil {
			t.Fatalf("publish refused fixture: %v", err)
		}
	}

	waitFor(t, "malformed and invalid refusals", func() bool {
		return metricValue(t, reader, "flowseer.intake.records.refused", "flowseer.intake.reason", malformedReason) == 1 &&
			metricValue(t, reader, "flowseer.intake.records.refused", "flowseer.intake.reason", invalidReason) == 1
	})
	deadline := time.After(200 * time.Millisecond)
	<-deadline
	if got := metricValue(t, reader, "flowseer.intake.records.refused", "flowseer.intake.reason", malformedReason); got != 1 {
		t.Fatalf("malformed refusal count after two acknowledgement waits = %d, want 1", got)
	}
	if got := metricValue(t, reader, "flowseer.intake.records.refused", "flowseer.intake.reason", invalidReason); got != 1 {
		t.Fatalf("invalid refusal count after two acknowledgement waits = %d, want 1", got)
	}
	info, err := consumer.Info(context.Background())
	if err != nil {
		t.Fatalf("read consumer info: %v", err)
	}
	if info.NumAckPending != 0 {
		t.Fatalf("ack pending = %d, want 0", info.NumAckPending)
	}
}

func TestForeignProvenanceIsRefused(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	system := newTestSystem(t, Config{MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))})
	envelope, data := validEnvelope(t, otherEdge, testDevice, testRecordID, false)
	if err := system.leaf.Publish(context.Background(), system.leaf.Subject("ingest.syslog"), data, ""); err != nil {
		t.Fatalf("publish foreign provenance: %v", err)
	}

	waitFor(t, "foreign provenance refusal", func() bool {
		return metricValue(t, reader, "flowseer.intake.records.refused", "flowseer.intake.reason", provenanceReason) == 1
	})
	if envelope.GetProvenance().GetEdge().GetEdge().GetId() != otherEdge {
		t.Fatalf("fixture provenance edge = %q, want %q", envelope.GetProvenance().GetEdge().GetEdge().GetId(), otherEdge)
	}
	stream, err := system.hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("load typed stream: %v", err)
	}
	info, err := stream.Info(context.Background())
	if err != nil {
		t.Fatalf("read typed stream: %v", err)
	}
	if info.State.Msgs != 0 {
		t.Fatalf("typed stream holds %d messages, want 0", info.State.Msgs)
	}
}

func TestRawEvidenceIsMovedToTheEvidenceStream(t *testing.T) {
	system := newTestSystem(t, Config{})
	envelope, data := validEnvelope(t, testEdge, testDevice, testRecordID, true)
	if err := system.leaf.Publish(context.Background(), system.leaf.Subject("ingest.syslog"), data, ""); err != nil {
		t.Fatalf("publish raw envelope: %v", err)
	}

	typed, err := system.hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("load typed stream: %v", err)
	}
	evidence, err := system.hub.JetStream().Stream(context.Background(), edgebus.EvidenceStream)
	if err != nil {
		t.Fatalf("load evidence stream: %v", err)
	}
	waitFor(t, "typed and evidence records", func() bool {
		typedInfo, typedErr := typed.Info(context.Background())
		evidenceInfo, evidenceErr := evidence.Info(context.Background())
		return typedErr == nil && evidenceErr == nil && typedInfo.State.Msgs == 1 && evidenceInfo.State.Msgs == 1
	})

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

func TestTypedPublishFailureAfterEvidenceRedelivers(t *testing.T) {
	publisher := &controlledPublisher{failTyped: 1}
	system := newTestSystem(t, Config{Central: publisher})
	_, data := validEnvelope(t, testEdge, testDevice, testRecordID, true)
	if err := system.leaf.Publish(context.Background(), system.leaf.Subject("ingest.syslog"), data, ""); err != nil {
		t.Fatalf("publish envelope: %v", err)
	}

	typed, evidence := centralStreams(t, system.hub)
	waitFor(t, "redelivered typed record", func() bool {
		typedInfo, typedErr := typed.Info(context.Background())
		evidenceInfo, evidenceErr := evidence.Info(context.Background())
		return typedErr == nil && evidenceErr == nil && typedInfo.State.Msgs == 1 && evidenceInfo.State.Msgs == 1
	})
	msg, err := typed.GetLastMsgForSubject(context.Background(), edgebus.CentralIngestSubject(testTenant, edgebus.IngestRecordTypeSyslog, testDevice))
	if err != nil {
		t.Fatalf("read typed record: %v", err)
	}
	var got ingestv1.IngestRecord
	if err := proto.Unmarshal(msg.Data, &got); err != nil {
		t.Fatalf("decode typed record: %v", err)
	}
	if got.GetRaw() != nil {
		t.Fatal("typed record retained raw evidence after redelivery")
	}
	if publisher.callCount() != 4 {
		t.Fatalf("publish calls = %d, want evidence, failed typed, evidence retry, typed retry", publisher.callCount())
	}
}

func TestFailedPublishIsRetried(t *testing.T) {
	publisher := &controlledPublisher{failAll: 2}
	system := newTestSystem(t, Config{Central: publisher, RetryDelay: time.Millisecond})
	_, data := validEnvelope(t, testEdge, testDevice, testRecordID, false)
	if err := system.leaf.Publish(context.Background(), system.leaf.Subject("ingest.syslog"), data, ""); err != nil {
		t.Fatalf("publish envelope: %v", err)
	}

	typed, _ := centralStreams(t, system.hub)
	waitFor(t, "retried typed record", func() bool {
		info, err := typed.Info(context.Background())
		return err == nil && info.State.Msgs == 1
	})
	if publisher.callCount() != 3 {
		t.Fatalf("publish calls = %d, want 3", publisher.callCount())
	}
}

func TestStoredPublishWithLostAckIsNotStoredTwice(t *testing.T) {
	publisher := &controlledPublisher{loseFirstAck: true}
	system := newTestSystem(t, Config{Central: publisher, RetryDelay: time.Millisecond})
	_, data := validEnvelope(t, testEdge, testDevice, testRecordID, false)
	if err := system.leaf.Publish(context.Background(), system.leaf.Subject("ingest.syslog"), data, ""); err != nil {
		t.Fatalf("publish envelope: %v", err)
	}

	typed, _ := centralStreams(t, system.hub)
	waitFor(t, "stored typed record after lost ack", func() bool {
		info, err := typed.Info(context.Background())
		return err == nil && info.State.Msgs == 1 && publisher.callCount() == 2
	})
}

func TestRecordTypeComesFromThePayload(t *testing.T) {
	system := newTestSystem(t, Config{})
	_, data := validEnvelope(t, testEdge, testDevice, testRecordID, false)
	if err := system.leaf.Publish(context.Background(), edgebus.IngestSubject(testTenant, testEdge, "other"), data, ""); err != nil {
		t.Fatalf("publish envelope on other source: %v", err)
	}
	typed, _ := centralStreams(t, system.hub)
	waitFor(t, "payload-typed record", func() bool {
		info, err := typed.Info(context.Background())
		return err == nil && info.State.Msgs == 1
	})
	if _, err := typed.GetLastMsgForSubject(context.Background(), edgebus.CentralIngestSubject(testTenant, edgebus.IngestRecordTypeSyslog, testDevice)); err != nil {
		t.Fatalf("payload type subject missing: %v", err)
	}
}

func TestSameRecordIDInTwoTenantsStoresBoth(t *testing.T) {
	system := newTestSystem(t, Config{DiscoveryInterval: 10 * time.Millisecond})
	secondLeaf := startLeaf(t, system.hub, otherTenant, otherEdge)
	_, firstData := validEnvelope(t, testEdge, testDevice, testRecordID, false)
	_, secondData := validEnvelope(t, otherEdge, otherDevice, testRecordID, false)
	if err := system.leaf.Publish(context.Background(), system.leaf.Subject("ingest.syslog"), firstData, ""); err != nil {
		t.Fatalf("publish first tenant record: %v", err)
	}
	if err := secondLeaf.Publish(context.Background(), secondLeaf.Subject("ingest.syslog"), secondData, ""); err != nil {
		t.Fatalf("publish second tenant record: %v", err)
	}
	typed, _ := centralStreams(t, system.hub)
	waitFor(t, "both tenant records", func() bool {
		info, err := typed.Info(context.Background())
		return err == nil && info.State.Msgs == 2
	})
	for _, subject := range []string{
		edgebus.CentralIngestSubject(testTenant, edgebus.IngestRecordTypeSyslog, testDevice),
		edgebus.CentralIngestSubject(otherTenant, edgebus.IngestRecordTypeSyslog, otherDevice),
	} {
		if _, err := typed.GetLastMsgForSubject(context.Background(), subject); err != nil {
			t.Fatalf("read tenant subject %q: %v", subject, err)
		}
	}
}

func TestInstrumentsReportEachOutcome(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	publisher := &controlledPublisher{failAll: 1}
	system := newTestSystem(t, Config{Central: publisher, MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)), RetryDelay: time.Millisecond})
	_, firstData := validEnvelope(t, testEdge, testDevice, testRecordID, false)
	system.intake.handle(context.Background(), testEdge, &fakeMsg{subject: edgebus.IngestSubject(testTenant, testEdge, "syslog"), data: firstData})
	system.intake.handle(context.Background(), testEdge, &fakeMsg{subject: edgebus.IngestSubject(testTenant, testEdge, "syslog"), data: firstData})
	system.intake.handle(context.Background(), testEdge, &fakeMsg{subject: refusalSubject, data: firstData})
	_, retryData := validEnvelope(t, testEdge, testDevice, otherRecordID, false)
	system.intake.handle(context.Background(), testEdge, &fakeMsg{subject: edgebus.IngestSubject(testTenant, testEdge, "syslog"), data: retryData})
	system.intake.handle(context.Background(), testEdge, &fakeMsg{subject: edgebus.IngestSubject(testTenant, testEdge, "syslog"), data: retryData})

	if got := metricValue(t, reader, "flowseer.intake.records.republished", "flowseer.intake.record_type", edgebus.IngestRecordTypeSyslog); got != 3 {
		t.Fatalf("republished count = %d, want 3", got)
	}
	if got := metricValue(t, reader, "flowseer.intake.records.duplicate", "flowseer.intake.record_type", edgebus.IngestRecordTypeSyslog); got != 1 {
		t.Fatalf("duplicate count = %d, want 1", got)
	}
	if got := metricValue(t, reader, "flowseer.intake.records.refused", "flowseer.intake.reason", foreignReason); got != 1 {
		t.Fatalf("refused count = %d, want 1", got)
	}
	if got := metricValue(t, reader, "flowseer.intake.records.retried", "", ""); got != 1 {
		t.Fatalf("retried count = %d, want 1", got)
	}
}

type testSystem struct {
	hub    *edgebus.Hub
	leaf   *edgebus.Leaf
	intake *Intake
}

func newTestSystem(t *testing.T, cfg Config) testSystem {
	t.Helper()
	hub := startHub(t)
	leaf := startLeaf(t, hub, testTenant, testEdge)
	if cfg.Central == nil {
		cfg.Central = hub.JetStream()
	}
	if publisher, ok := cfg.Central.(*controlledPublisher); ok {
		publisher.setPublisher(hub.JetStream())
	}
	intake, err := Start(context.Background(), Config{
		Hub:               hub,
		Central:           cfg.Central,
		RetryDelay:        cfg.RetryDelay,
		DiscoveryInterval: cfg.DiscoveryInterval,
		Logger:            cfg.Logger,
		MeterProvider:     cfg.MeterProvider,
	})
	if err != nil {
		t.Fatalf("start intake: %v", err)
	}
	t.Cleanup(intake.Close)
	return testSystem{hub: hub, leaf: leaf, intake: intake}
}

func startHub(t *testing.T) *edgebus.Hub {
	t.Helper()
	hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
		StateDir:           t.TempDir(),
		FsyncPolicy:        service.BusFsyncPeriodic,
		ListenPort:         -1,
		MaxStoreBytes:      768 << 20,
		CentralBudgetBytes: 512 << 20,
		EdgeBudgetBytes:    128 << 20,
		IngestMaxBytes:     112 << 20,
		EvidenceMaxBytes:   64 << 20,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)
	return hub
}

func startLeaf(t *testing.T, hub *edgebus.Hub, tenantID, edgeID string) *edgebus.Leaf {
	t.Helper()
	if err := hub.AttachEdge(context.Background(), tenantID, edgeID); err != nil {
		t.Fatalf("attach edge: %v", err)
	}
	creds, err := hub.MintEdgeUser(context.Background(), edgeID)
	if err != nil {
		t.Fatalf("mint edge user: %v", err)
	}
	body, err := creds.CredsFile()
	if err != nil {
		t.Fatalf("format credentials: %v", err)
	}
	leaf, err := edgebus.StartLeaf(context.Background(), edgebus.LeafConfig{
		StateDir:        t.TempDir(),
		EdgeID:          edgeID,
		Tenant:          tenantID,
		HubURLs:         []string{hub.ListenURL()},
		CredentialsFile: secret.New(body),
		FsyncPolicy:     service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("start leaf: %v", err)
	}
	t.Cleanup(leaf.Close)
	waitFor(t, "leaf link", func() bool { return leaf.HubConnected() && hub.LeafCount() > 0 })
	return leaf
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

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type fakeMsg struct {
	jetstream.Msg
	subject   string
	data      []byte
	delivered uint64
	acked     bool
	termed    bool
	nakDelay  time.Duration
}

func (m *fakeMsg) Subject() string                        { return m.subject }
func (m *fakeMsg) Data() []byte                           { return m.data }
func (m *fakeMsg) Ack() error                             { m.acked = true; return nil }
func (m *fakeMsg) Term() error                            { m.termed = true; return nil }
func (m *fakeMsg) NakWithDelay(delay time.Duration) error { m.nakDelay = delay; return nil }
func (m *fakeMsg) Metadata() (*jetstream.MsgMetadata, error) {
	return &jetstream.MsgMetadata{NumDelivered: max(m.delivered, 1), Timestamp: time.Now().Add(-time.Second)}, nil
}

type controlledPublisher struct {
	mu           sync.Mutex
	publisher    Publisher
	failAll      int
	failTyped    int
	loseFirstAck bool
	calls        int
}

func (p *controlledPublisher) Publish(ctx context.Context, subject string, payload []byte, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	if p.failAll > 0 {
		p.failAll--
		p.mu.Unlock()
		return nil, errors.New("injected publish failure")
	}
	if p.failTyped > 0 && !strings.Contains(subject, ".evidence.") {
		p.failTyped--
		p.mu.Unlock()
		return nil, errors.New("injected typed publish failure")
	}
	p.mu.Unlock()
	ack, err := p.publisher.Publish(ctx, subject, payload, opts...)
	if err != nil {
		return nil, err
	}
	if p.loseFirstAck && call == 1 {
		return nil, errors.New("injected lost acknowledgement")
	}
	return ack, nil
}

func (p *controlledPublisher) setPublisher(publisher Publisher) {
	p.mu.Lock()
	p.publisher = publisher
	p.mu.Unlock()
}

type blockingPublisher struct {
	started chan struct{}
	once    sync.Once
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (p *blockingPublisher) Publish(ctx context.Context, _ string, _ []byte, _ ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	p.once.Do(func() { close(p.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *controlledPublisher) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
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
