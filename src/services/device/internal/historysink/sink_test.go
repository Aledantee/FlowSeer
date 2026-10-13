package historysink

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"

	identityv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/identity/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/service"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

// fakeInserter records every batch and answers from fn, which may be nil.
type fakeInserter struct {
	mu     sync.Mutex
	calls  [][]Row
	tokens []string
	fn     func(rows []Row, call int) error
}

func (f *fakeInserter) Insert(_ context.Context, rows []Row, token string) error {
	f.mu.Lock()
	f.calls = append(f.calls, append([]Row(nil), rows...))
	f.tokens = append(f.tokens, token)
	call := len(f.calls)
	fn := f.fn
	f.mu.Unlock()
	if fn != nil {
		return fn(rows, call)
	}
	return nil
}

func (f *fakeInserter) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// rowCount counts the rows of every call, redeliveries included.
func (f *fakeInserter) rowCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += len(c)
	}
	return n
}

var errUnavailable = errors.New("server unavailable")

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

// publish stores one syslog record in the central ingest stream. The message
// id differs per call, so a repeated record id is stored as a second message.
func publish(t *testing.T, hub *edgebus.Hub, recordID, tenantID string) {
	t.Helper()
	b := baseSyslog()
	record := syslogEnvelope(b.Build())
	record.SetRecordId(recordID)
	data, err := proto.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	subject := edgebus.CentralIngestSubject(tenantID, edgebus.IngestRecordTypeSyslog, testDevice)
	if _, err := hub.JetStream().Publish(context.Background(), subject, data, jetstream.WithMsgID(fmt.Sprintf("%s-%d", recordID, time.Now().UnixNano()))); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func recordID(n int) string { return fmt.Sprintf("018f0000-0000-7000-8000-%012d", n) }

type running struct {
	hub    *edgebus.Hub
	sink   *Sink
	reader *sdkmetric.ManualReader
	cancel context.CancelFunc
	done   chan struct{}
}

func startSink(t *testing.T, hub *edgebus.Hub, cfg Config) *running {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	cfg.JetStream = hub.JetStream()
	cfg.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	if cfg.Retention == nil {
		cfg.Retention = func(context.Context, string) (identityv1.RetentionClass, error) {
			return identityv1.RetentionClass_RETENTION_CLASS_STANDARD, nil
		}
	}
	if cfg.FlushInterval == 0 {
		cfg.FlushInterval = 50 * time.Millisecond
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = 50 * time.Millisecond
	}
	sink, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("start sink: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{hub: hub, sink: sink, reader: reader, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		_ = sink.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-r.done
	})
	return r
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// consumerInfo reads the consumer through a handle of its own, since a pull
// consumer handle is not safe for concurrent Info calls and the sink's Run
// loop uses its own.
func consumerInfo(t *testing.T, r *running) *jetstream.ConsumerInfo {
	t.Helper()
	handle, err := r.hub.JetStream().Consumer(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog), consumerName)
	if err != nil {
		t.Fatalf("consumer handle: %v", err)
	}
	info, err := handle.Info(context.Background())
	if err != nil {
		t.Fatalf("consumer info: %v", err)
	}
	return info
}

func counter(t *testing.T, r *running, name, key, value string) int64 {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := r.reader.Collect(context.Background(), &data); err != nil {
		t.Fatalf("collect: %v", err)
	}
	var total int64
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != name {
				continue
			}
			switch d := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, p := range d.DataPoints {
					if v, ok := p.Attributes.Value(attribute.Key(key)); key == "" || (ok && v.AsString() == value) {
						total += p.Value
					}
				}
			case metricdata.Gauge[int64]:
				for _, p := range d.DataPoints {
					total += p.Value
				}
			}
		}
	}
	return total
}

func TestNothingIsAcknowledgedBeforeTheInsertReturns(t *testing.T) {
	hub := startHub(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	var once sync.Once
	inserter := &fakeInserter{fn: func([]Row, int) error {
		once.Do(func() { close(entered) })
		<-release
		return nil
	}}
	for n := 1; n <= 3; n++ {
		publish(t, hub, recordID(n), testTenant)
	}
	r := startSink(t, hub, Config{Inserter: inserter})
	t.Cleanup(releaseOnce)

	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("the insert never started")
	}
	info := consumerInfo(t, r)
	if info.NumAckPending != 3 || info.AckFloor.Stream != 0 {
		t.Fatalf("during the insert: %d pending and ack floor %d, want 3 pending and floor 0", info.NumAckPending, info.AckFloor.Stream)
	}

	releaseOnce()
	waitFor(t, "acknowledgement after the insert", func() bool {
		i := consumerInfo(t, r)
		return i.NumAckPending == 0 && i.AckFloor.Stream == 3
	})
	if got := counter(t, r, "flowseer.history.records.inserted", "", ""); got != 3 {
		t.Errorf("inserted = %d, want 3", got)
	}
}

func TestATransientErrorNaksTheBatchAndItIsInsertedOnceTheStoreReturns(t *testing.T) {
	hub := startHub(t)
	inserter := &fakeInserter{fn: func(_ []Row, call int) error {
		if call == 1 {
			return errs.From(errUnavailable).Retryable().Msg("send insert")
		}
		return nil
	}}
	for n := 1; n <= 4; n++ {
		publish(t, hub, recordID(n), testTenant)
	}
	r := startSink(t, hub, Config{Inserter: inserter})

	waitFor(t, "every record acknowledged", func() bool { return consumerInfo(t, r).AckFloor.Stream == 4 })

	if got := counter(t, r, "flowseer.history.records.retried", "", ""); got < 4 {
		t.Errorf("retried = %d, want at least 4 after the failed batch", got)
	}
	if got := counter(t, r, "flowseer.history.records.inserted", "", ""); got != 4 {
		t.Errorf("inserted = %d, want 4", got)
	}
	if got := counter(t, r, "flowseer.history.records.refused", "", ""); got != 0 {
		t.Errorf("refused = %d, want 0: a transient error must not terminate a record", got)
	}
}

func TestAPermanentlyRejectedRecordIsTerminatedAndItsNeighboursAreStored(t *testing.T) {
	hub := startHub(t)
	bad := recordID(3)
	inserter := &fakeInserter{fn: func(rows []Row, _ int) error {
		for _, r := range rows {
			if r.RecordID.String() == bad {
				return errs.New().Code(ErrCodeRejected).Msg("send insert")
			}
		}
		return nil
	}}
	for n := 1; n <= 6; n++ {
		publish(t, hub, recordID(n), testTenant)
	}
	r := startSink(t, hub, Config{Inserter: inserter, FlushInterval: 300 * time.Millisecond})

	waitFor(t, "every record resolved", func() bool {
		i := consumerInfo(t, r)
		return i.NumAckPending == 0 && i.AckFloor.Stream == 6
	})

	if got := counter(t, r, "flowseer.history.records.inserted", "", ""); got != 5 {
		t.Errorf("inserted = %d, want 5", got)
	}
	if got := counter(t, r, "flowseer.history.records.refused", "flowseer.history.reason", "rejected"); got != 1 {
		t.Errorf("refused as rejected = %d, want 1", got)
	}
	if got := counter(t, r, "flowseer.history.records.retried", "", ""); got != 0 {
		t.Errorf("retried = %d, want 0: a rejection must not retry", got)
	}
	stored := map[string]bool{}
	inserter.mu.Lock()
	for _, call := range inserter.calls {
		for _, row := range call {
			stored[row.RecordID.String()] = true
		}
	}
	inserter.mu.Unlock()
	if len(stored) != 6 {
		t.Errorf("the sink tried %d distinct records, want all 6 including the bad one", len(stored))
	}
}

func TestARedeliveredRecordIsInsertedAndAcknowledgedAgain(t *testing.T) {
	hub := startHub(t)
	inserter := &fakeInserter{}
	publish(t, hub, recordID(1), testTenant)
	publish(t, hub, recordID(1), testTenant)
	r := startSink(t, hub, Config{Inserter: inserter})

	waitFor(t, "both deliveries acknowledged", func() bool { return consumerInfo(t, r).AckFloor.Stream == 2 })

	if got := inserter.rowCount(); got != 2 {
		t.Errorf("rows inserted = %d, want 2: deduplication belongs to the store, not the sink", got)
	}
}

func TestTheRetentionClassIsStampedCachedAndDefaultsToStandard(t *testing.T) {
	hub := startHub(t)
	var mu sync.Mutex
	lookups := 0
	retention := func(context.Context, string) (identityv1.RetentionClass, error) {
		mu.Lock()
		defer mu.Unlock()
		lookups++
		return identityv1.RetentionClass_RETENTION_CLASS_UNSPECIFIED, nil
	}
	inserter := &fakeInserter{}
	publish(t, hub, recordID(1), testTenant)
	r := startSink(t, hub, Config{Inserter: inserter, Retention: retention})
	waitFor(t, "first record", func() bool { return consumerInfo(t, r).AckFloor.Stream == 1 })
	publish(t, hub, recordID(2), testTenant)
	waitFor(t, "second record", func() bool { return consumerInfo(t, r).AckFloor.Stream == 2 })

	inserter.mu.Lock()
	defer inserter.mu.Unlock()
	for _, call := range inserter.calls {
		for _, row := range call {
			if row.RetentionClass != 2 {
				t.Errorf("retention_class = %d, want 2 for a tenant with no class", row.RetentionClass)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if lookups != 1 {
		t.Errorf("retention lookups = %d, want 1 for two records of one tenant", lookups)
	}
}

func TestAFailedRetentionLookupRetriesTheRecordWithoutTerminatingIt(t *testing.T) {
	hub := startHub(t)
	var mu sync.Mutex
	calls := 0
	retention := func(context.Context, string) (identityv1.RetentionClass, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return 0, errUnavailable
		}
		return identityv1.RetentionClass_RETENTION_CLASS_SHORT, nil
	}
	inserter := &fakeInserter{}
	publish(t, hub, recordID(1), testTenant)
	r := startSink(t, hub, Config{Inserter: inserter, Retention: retention})

	waitFor(t, "the record acknowledged", func() bool { return consumerInfo(t, r).AckFloor.Stream == 1 })

	if got := counter(t, r, "flowseer.history.records.refused", "", ""); got != 0 {
		t.Errorf("refused = %d, want 0", got)
	}
	inserter.mu.Lock()
	defer inserter.mu.Unlock()
	if len(inserter.calls) != 1 || inserter.calls[0][0].RetentionClass != 1 {
		t.Errorf("inserted %v, want one row with retention class 1", inserter.calls)
	}
}

func TestARecordThatCannotBecomeARowIsTerminatedWithoutAnInsert(t *testing.T) {
	hub := startHub(t)
	inserter := &fakeInserter{}
	publish(t, hub, recordID(1), "not-a-tenant")
	r := startSink(t, hub, Config{Inserter: inserter})

	waitFor(t, "the record terminated", func() bool {
		i := consumerInfo(t, r)
		return i.NumAckPending == 0 && i.AckFloor.Stream == 1
	})

	if got := counter(t, r, "flowseer.history.records.refused", "flowseer.history.reason", "record"); got != 1 {
		t.Errorf("refused as record = %d, want 1", got)
	}
	if got := inserter.callCount(); got != 0 {
		t.Errorf("inserts = %d, want 0 for a record that maps to no row", got)
	}
}

func TestTheDeviceMirrorRunsAgainUntilItSucceeds(t *testing.T) {
	hub := startHub(t)
	var mu sync.Mutex
	attempts := 0
	mirror := func(context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		attempts++
		if attempts < 3 {
			return errUnavailable
		}
		return nil
	}
	startSink(t, hub, Config{Inserter: &fakeInserter{}, Mirror: mirror})

	waitFor(t, "three mirror attempts", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return attempts >= 3
	})
	time.Sleep(200 * time.Millisecond) // a mirror that kept running would show as a fourth attempt
	mu.Lock()
	defer mu.Unlock()
	if attempts != 3 {
		t.Errorf("mirror attempts = %d, want 3 and then no more", attempts)
	}
}

func TestTheDiscardedGaugeCountsRecordsTheStreamRemovedBeforeTheyWereAcknowledged(t *testing.T) {
	hub := startHub(t)
	for n := 1; n <= 5; n++ {
		publish(t, hub, recordID(n), testTenant)
	}
	stream, err := hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	sink, err := Start(context.Background(), Config{
		JetStream: hub.JetStream(),
		Inserter:  &fakeInserter{},
		Retention: func(context.Context, string) (identityv1.RetentionClass, error) { return 2, nil },
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// A limit removes the oldest records the way an age or byte bound does,
	// without moving the consumer's acknowledged floor as a purge would.
	config := stream.CachedInfo().Config
	config.MaxMsgs = 2
	if _, err := hub.JetStream().UpdateStream(context.Background(), config); err != nil {
		t.Fatalf("limit the stream: %v", err)
	}

	sink.pollDiscarded(context.Background())

	if got := sink.metrics.discarded.Load(); got != 3 {
		t.Errorf("discarded = %d, want 3: sequences 1 to 3 were removed before any acknowledgement", got)
	}
}

func TestStartRefusesAConfigurationWithoutWhatItNeeds(t *testing.T) {
	hub := startHub(t)
	retention := func(context.Context, string) (identityv1.RetentionClass, error) { return 2, nil }
	tests := map[string]Config{
		"no JetStream": {Inserter: &fakeInserter{}, Retention: retention},
		"no inserter":  {JetStream: hub.JetStream(), Retention: retention},
		"no retention": {JetStream: hub.JetStream(), Inserter: &fakeInserter{}},
	}
	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Start(context.Background(), cfg)

			if code, _ := errs.CodeOf(err); code != ErrCodeStart {
				t.Errorf("code = %v (err %v), want %v", code, err, ErrCodeStart)
			}
		})
	}
}

func TestSubjectTenantTakesTokenOne(t *testing.T) {
	tests := []struct {
		subject string
		want    string
		ok      bool
	}{
		{"flowseer." + testTenant + ".ingest.syslog." + testDevice, testTenant, true},
		{"flowseer.default.ingest.syslog.d1", "default", true},
		{"other." + testTenant + ".ingest.syslog.d1", "", false},
		{"flowseer", "", false},
		{"flowseer..ingest.syslog.d1", "", false},
	}
	for _, tt := range tests {
		got, ok := subjectTenant(tt.subject)
		if got != tt.want || ok != tt.ok {
			t.Errorf("subjectTenant(%q) = %q, %v; want %q, %v", tt.subject, got, ok, tt.want, tt.ok)
		}
	}
}
