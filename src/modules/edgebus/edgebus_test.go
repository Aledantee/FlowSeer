package edgebus_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/access/v1"
	operatorv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/operator/v1"
	modeledgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

const edgeID = "0192e6a0-0000-7000-8000-0000000000ed"

func startHub(t *testing.T, dir string) *edgebus.Hub {
	t.Helper()
	hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
		StateDir:    dir,
		FsyncPolicy: service.BusFsyncPeriodic,
	})
	if err != nil {
		t.Fatalf("start hub: %v (%v)", err, errs.Attributes(err))
	}
	t.Cleanup(hub.Close)
	return hub
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
}

func TestAuditStreamStoresADuplicateEventOnce(t *testing.T) {
	hub := startHub(t, t.TempDir())
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

func TestCentralIngestStreamsExistWithTheirLimits(t *testing.T) {
	hub := startHub(t, t.TempDir())
	ctx := context.Background()

	for _, test := range []struct {
		name     string
		stream   string
		subject  string
		maxBytes int64
		maxAge   time.Duration
		discard  jetstream.DiscardPolicy
	}{
		{
			name:     "typed",
			stream:   "FLOWSEER_INGEST_SYSLOG",
			subject:  "flowseer.*.ingest.syslog.*",
			maxBytes: 256 << 20,
			discard:  jetstream.DiscardNew,
		},
		{
			name:     "evidence",
			stream:   "FLOWSEER_INGEST_EVIDENCE",
			subject:  "flowseer.*.evidence.>",
			maxBytes: 64 << 20,
			maxAge:   24 * time.Hour,
			discard:  jetstream.DiscardOld,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stream, err := hub.JetStream().Stream(ctx, test.stream)
			if err != nil {
				t.Fatalf("stream: %v", err)
			}
			info, err := stream.Info(ctx)
			if err != nil {
				t.Fatalf("stream info: %v", err)
			}
			if !reflect.DeepEqual(info.Config.Subjects, []string{test.subject}) {
				t.Errorf("subjects = %v, want [%s]", info.Config.Subjects, test.subject)
			}
			if info.Config.MaxAge != test.maxAge {
				t.Errorf("max age = %s, want %s", info.Config.MaxAge, test.maxAge)
			}
			if info.Config.MaxBytes != test.maxBytes {
				t.Errorf("max bytes = %d, want %d", info.Config.MaxBytes, test.maxBytes)
			}
			if info.Config.Discard != test.discard {
				t.Errorf("discard = %q, want %q", info.Config.Discard, test.discard)
			}
			if info.Config.Duplicates != 10*time.Minute {
				t.Errorf("duplicate window = %s, want 10m", info.Config.Duplicates)
			}
		})
	}
}

func TestCentralIngestSubjectsDoNotOverlap(t *testing.T) {
	hub := startHub(t, t.TempDir())
	const (
		tenantID = "0198a3c0-0000-7000-8000-0000000000aa"
		deviceID = "0198a3c0-0000-7000-8000-000000000001"
		edgeID   = "0198a3c0-0000-7000-8000-0000000000ed"
	)

	subjects := []string{
		"flowseer." + tenantID + ".audit.device." + deviceID,
		"flowseer." + tenantID + ".ingest.syslog." + deviceID,
		"flowseer." + tenantID + ".evidence.syslog." + deviceID,
		"flowseer." + tenantID + ".edge." + edgeID + ".ingest.syslog",
	}
	wantStreams := []string{
		"FLOWSEER_DEVICE_AUDIT",
		"FLOWSEER_INGEST_SYSLOG",
		"FLOWSEER_INGEST_EVIDENCE",
	}
	for i, subject := range subjects[:3] {
		ack, err := hub.JetStream().Publish(context.Background(), subject, []byte(fmt.Sprintf("message-%d", i)))
		if err != nil {
			t.Fatalf("publish %s: %v", subject, err)
		}
		if ack.Stream != wantStreams[i] {
			t.Errorf("publish %s stream = %q, want %q", subject, ack.Stream, wantStreams[i])
		}
	}
	if _, err := hub.JetStream().Publish(context.Background(), subjects[3], []byte("edge-shaped")); !errors.Is(err, jetstream.ErrNoStreamResponse) {
		t.Fatalf("edge-shaped publish error = %v, want %v", err, jetstream.ErrNoStreamResponse)
	}
}

func TestCentralIngestDuplicateWindowDoesNotExceedMaxAge(t *testing.T) {
	hub, err := edgebus.StartHub(context.Background(), edgebus.HubConfig{
		StateDir:             t.TempDir(),
		FsyncPolicy:          service.BusFsyncPeriodic,
		AuditDuplicateWindow: 30 * time.Minute,
		EvidenceMaxAge:       2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(hub.Close)

	for _, test := range []struct {
		name string
		want time.Duration
		get  func(context.Context) (jetstream.Stream, error)
	}{
		{name: "evidence", want: 2 * time.Minute, get: func(ctx context.Context) (jetstream.Stream, error) {
			return hub.JetStream().Stream(ctx, edgebus.EvidenceStream)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			stream, err := test.get(context.Background())
			if err != nil {
				t.Fatalf("stream: %v", err)
			}
			info, err := stream.Info(context.Background())
			if err != nil {
				t.Fatalf("stream info: %v", err)
			}
			if info.Config.Duplicates != test.want {
				t.Fatalf("duplicate window = %s, want %s", info.Config.Duplicates, test.want)
			}
		})
	}
}

func TestLaneBucketSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	first := startHub(t, dir)
	kv, err := first.JetStream().KeyValue(context.Background(), edgebus.LaneBucket)
	if err != nil {
		t.Fatalf("bucket: %v", err)
	}
	if _, err := kv.Put(context.Background(), "device-1", []byte("record")); err != nil {
		t.Fatalf("put: %v", err)
	}
	first.Close()

	second := startHub(t, dir)
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

func TestAuditStreamWildcardStoresEventsFromMultipleTenants(t *testing.T) {
	ctx := context.Background()
	hub := startHub(t, t.TempDir())

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

func TestTenantBucketAllowsAtomicPublishAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	first := startHub(t, dir)
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

func fetchSubject(t *testing.T, stream jetstream.Stream, subject string) []string {
	t.Helper()
	ctx := context.Background()
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		FilterSubject: subject,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		t.Fatalf("create consumer for %s: %v", subject, err)
	}
	batch, err := consumer.Fetch(10, jetstream.FetchMaxWait(2*time.Second))
	if err != nil {
		t.Fatalf("fetch %s: %v", subject, err)
	}
	var bodies []string
	for msg := range batch.Messages() {
		bodies = append(bodies, string(msg.Data()))
		_ = msg.Ack()
	}
	return bodies
}

func TestOperatorActionStreamMaxPerSubject(t *testing.T) {
	ctx := context.Background()
	hub, err := edgebus.StartHub(ctx, edgebus.HubConfig{
		StateDir:                    t.TempDir(),
		FsyncPolicy:                 service.BusFsyncPeriodic,
		OperatorActionMaxPerSubject: 2,
		OperatorReadMaxPerSubject:   2,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	defer hub.Close()

	const tenantID = "tenant-op-test"
	changeSubject := edgebus.OperatorActionSubject(tenantID, "edge_create")
	viewSubject := edgebus.OperatorReadSubject(tenantID, "edge_get")

	for i, body := range []string{"rec-1", "rec-2", "rec-3"} {
		if _, err := hub.JetStream().Publish(ctx, changeSubject, []byte(body), jetstream.WithMsgID(fmt.Sprintf("msg-change-%d", i))); err != nil {
			t.Fatalf("publish on the change subject: %v", err)
		}
	}
	for i, body := range []string{"rec-4", "rec-5", "rec-6"} {
		if _, err := hub.JetStream().Publish(ctx, viewSubject, []byte(body), jetstream.WithMsgID(fmt.Sprintf("msg-view-%d", i))); err != nil {
			t.Fatalf("publish on the view subject: %v", err)
		}
	}

	actions, err := hub.JetStream().Stream(ctx, edgebus.OperatorActionStream)
	if err != nil {
		t.Fatalf("get operator action stream: %v", err)
	}
	actionInfo, err := actions.Info(ctx)
	if err != nil {
		t.Fatalf("action stream info: %v", err)
	}
	if actionInfo.State.Msgs != 2 {
		t.Fatalf("action stream messages = %d, want 2 (a view is not stored there)", actionInfo.State.Msgs)
	}
	if got := fetchSubject(t, actions, changeSubject); len(got) != 2 || got[0] != "rec-2" || got[1] != "rec-3" {
		t.Fatalf("change subject messages = %v, want [rec-2 rec-3]", got)
	}

	reads, err := hub.JetStream().Stream(ctx, edgebus.OperatorReadStream)
	if err != nil {
		t.Fatalf("get operator read stream: %v", err)
	}
	readInfo, err := reads.Info(ctx)
	if err != nil {
		t.Fatalf("read stream info: %v", err)
	}
	if readInfo.State.Msgs != 2 {
		t.Fatalf("read stream messages = %d, want 2", readInfo.State.Msgs)
	}
	if got := fetchSubject(t, reads, viewSubject); len(got) != 2 || got[0] != "rec-5" || got[1] != "rec-6" {
		t.Fatalf("view subject messages = %v, want [rec-5 rec-6]", got)
	}
}

// The trail's sizing rests on what one record costs the stream. A stored
// record is 34 bytes plus its subject, header, and payload
// (nats-server v2.15.0 server/filestore.go, fileStoreMsgSizeRaw), so an
// IssueSetupKey attempt and completion with a 60-character issuer and a
// 36-character subject stay under 512 bytes each. At that size a subject at
// its 10,000-record cap holds under 5 MiB.
func TestOperatorActionRecordFitsTheSizingArithmetic(t *testing.T) {
	const (
		maxRecordBytes = 512
		// "NATS/1.0\r\nNats-Msg-Id: " + a 36-character id + "\r\n\r\n".
		msgIDHeaderBytes = 10 + 13 + 36 + 2 + 2
		recordOverhead   = 34
	)
	ctx := context.Background()
	hub := startHub(t, t.TempDir())
	stream, err := hub.JetStream().Stream(ctx, edgebus.OperatorActionStream)
	if err != nil {
		t.Fatalf("get operator action stream: %v", err)
	}

	const (
		tenantID = "0192e6a0-0000-7000-8000-0000000000aa"
		callID   = "0192e6a0-0000-7000-8000-0000000000c1"
	)
	operator := &identityv1.OperatorRef{}
	operator.SetIssuer("https://" + strings.Repeat("a", 52))
	operator.SetSubject(strings.Repeat("s", 36))
	local := &modeledgev1.EdgeLocalRef{}
	local.SetId(edgeID)
	edge := &modeledgev1.EdgeGlobalRef{}
	edge.SetEdge(local)

	attempt := &operatorv1.OperatorActionEvent{}
	attempt.SetEventId("0192e6a0-0000-7000-8000-0000000000e1")
	attempt.SetCallId(callID)
	attempt.SetOccurredAt(timestamppb.New(time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)))
	attempt.SetOperator(operator)
	attempt.SetAction(operatorv1.OperatorAction_OPERATOR_ACTION_SETUP_KEY_ISSUE)
	attempt.SetEdge(edge)
	attempt.SetAttempted(&operatorv1.OperatorActionAttempted{})

	completion := proto.Clone(attempt).(*operatorv1.OperatorActionEvent)
	completion.SetEventId("0192e6a0-0000-7000-8000-0000000000e2")
	completed := &operatorv1.OperatorActionCompleted{}
	completed.SetOutcome(operatorv1.OperatorActionOutcome_OPERATOR_ACTION_OUTCOME_SUCCEEDED)
	completion.SetCompleted(completed)

	subject := edgebus.OperatorActionSubject(tenantID, "setup_key_issue")
	var stored uint64
	for _, event := range []*operatorv1.OperatorActionEvent{attempt, completion} {
		payload, err := proto.Marshal(event)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		if _, err := hub.JetStream().Publish(ctx, subject, payload, jetstream.WithMsgID(event.GetEventId())); err != nil {
			t.Fatalf("publish %s: %v", event.WhichDetail(), err)
		}
		info, err := stream.Info(ctx)
		if err != nil {
			t.Fatalf("stream info: %v", err)
		}
		size := info.State.Bytes - stored
		stored = info.State.Bytes

		want := uint64(recordOverhead + len(subject) + msgIDHeaderBytes + len(payload))
		if size != want {
			t.Errorf("%s record occupies %d bytes, want %d (%d overhead + %d subject + %d header + %d payload)",
				event.WhichDetail(), size, want, recordOverhead, len(subject), msgIDHeaderBytes, len(payload))
		}
		if size >= maxRecordBytes {
			t.Errorf("%s record occupies %d bytes, want under %d", event.WhichDetail(), size, maxRecordBytes)
		}
	}
}

func TestOperatorActionStreamExistsUnderDefaultBudgetWithKVWriteAccepted(t *testing.T) {
	ctx := context.Background()
	hub := startHub(t, t.TempDir())

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

func TestOperatorStreamsAndSixBucketsExistUnderTheDefaultCentralBudget(t *testing.T) {
	ctx := context.Background()
	hub := startHub(t, t.TempDir())

	for _, bucket := range []string{edgebus.LaneBucket, edgebus.EdgeBucket, edgebus.CapturesBucket, edgebus.RawWindowsBucket, edgebus.TenantBucket, edgebus.AccessBucket} {
		if _, err := hub.JetStream().KeyValue(ctx, bucket); err != nil {
			t.Errorf("key-value bucket %q missing: %v", bucket, err)
		}
	}
	kv, err := hub.JetStream().KeyValue(ctx, edgebus.AccessBucket)
	if err != nil {
		t.Fatalf("get access bucket: %v", err)
	}
	if _, err := kv.Put(ctx, "tenant-a.member.principal", []byte("member")); err != nil {
		t.Fatalf("write into the access bucket: %v", err)
	}

	for _, want := range []struct {
		name       string
		subjects   []string
		maxBytes   int64
		maxPerSubj int64
		sample     string
	}{
		{edgebus.OperatorActionStream, []string{"flowseer.*.operator.action.*"}, 64 << 20, 10000, edgebus.OperatorActionSubject("tenant-a", "member_enroll")},
		{edgebus.OperatorReadStream, []string{"flowseer.*.operator.read.*"}, 16 << 20, 1000, edgebus.OperatorReadSubject("tenant-a", "edge_get")},
	} {
		stream, err := hub.JetStream().Stream(ctx, want.name)
		if err != nil {
			t.Errorf("stream %q missing: %v", want.name, err)
			continue
		}
		cfg := stream.CachedInfo().Config
		if !reflect.DeepEqual(cfg.Subjects, want.subjects) {
			t.Errorf("stream %q subjects = %v, want %v", want.name, cfg.Subjects, want.subjects)
		}
		if cfg.MaxBytes != want.maxBytes {
			t.Errorf("stream %q MaxBytes = %d, want %d", want.name, cfg.MaxBytes, want.maxBytes)
		}
		if cfg.MaxMsgsPerSubject != want.maxPerSubj {
			t.Errorf("stream %q MaxMsgsPerSubject = %d, want %d", want.name, cfg.MaxMsgsPerSubject, want.maxPerSubj)
		}
		if cfg.Discard != jetstream.DiscardOld {
			t.Errorf("stream %q Discard = %v, want DiscardOld", want.name, cfg.Discard)
		}
		if _, err := hub.JetStream().Publish(ctx, want.sample, []byte("rec")); err != nil {
			t.Errorf("publish on %q for stream %q: %v", want.sample, want.name, err)
			continue
		}
		info, err := stream.Info(ctx)
		if err != nil {
			t.Fatalf("stream %q info: %v", want.name, err)
		}
		if info.State.Msgs != 1 {
			t.Errorf("stream %q holds %d records after one publish on %q, want 1", want.name, info.State.Msgs, want.sample)
		}
	}
}

func TestOperatorReadStreamLimitsComeFromTheConfiguration(t *testing.T) {
	ctx := context.Background()
	hub, err := edgebus.StartHub(ctx, edgebus.HubConfig{
		StateDir:                  t.TempDir(),
		FsyncPolicy:               service.BusFsyncPeriodic,
		OperatorReadMaxBytes:      2 << 20,
		OperatorReadMaxPerSubject: 7,
	})
	if err != nil {
		t.Fatalf("start hub: %v", err)
	}
	defer hub.Close()

	stream, err := hub.JetStream().Stream(ctx, edgebus.OperatorReadStream)
	if err != nil {
		t.Fatalf("get operator read stream: %v", err)
	}
	cfg := stream.CachedInfo().Config
	if cfg.MaxBytes != 2<<20 || cfg.MaxMsgsPerSubject != 7 {
		t.Fatalf("read stream limits = %d bytes and %d per subject, want %d and 7", cfg.MaxBytes, cfg.MaxMsgsPerSubject, 2<<20)
	}
}

func TestHubStartFailsWhenCentralBudgetBelowStreamSum(t *testing.T) {
	ctx := context.Background()
	// Defaults: audit stream 256 MiB, operator action stream 64 MiB, operator
	// read stream 16 MiB, typed ingest stream 256 MiB, evidence stream 64 MiB.
	// Sum: 656 MiB. A central budget below the sum of reservations must fail
	// StartHub, and both budgets tried are below it.
	config := edgebus.HubConfig{
		FsyncPolicy: service.BusFsyncPeriodic,
	}
	for _, budget := range []int64{600 << 20, 650 << 20} {
		config.StateDir = t.TempDir()
		config.CentralBudgetBytes = budget
		_, err := edgebus.StartHub(ctx, config)
		if err == nil {
			t.Fatalf("StartHub with central budget %d MiB below stream sum succeeded, want error", budget>>20)
		}
		if code, ok := errs.CodeOf(err); !ok || code != edgebus.ErrCodeHub {
			t.Fatalf("StartHub err=%v, code=%q, want code %q", err, code, edgebus.ErrCodeHub)
		}
	}

	config.StateDir = t.TempDir()
	config.CentralBudgetBytes = 768 << 20
	hub, err := edgebus.StartHub(ctx, config)
	if err != nil {
		t.Fatalf("StartHub with sufficient central budget: %v", err)
	}
	hub.Close()
}
