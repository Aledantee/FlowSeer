package historysink

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
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
	stored []Row // the rows of every call that returned nil
	fn     func(rows []Row, call int) error
}

func (f *fakeInserter) Insert(_ context.Context, rows []Row, token string) error {
	f.mu.Lock()
	f.calls = append(f.calls, append([]Row(nil), rows...))
	f.tokens = append(f.tokens, token)
	call := len(f.calls)
	fn := f.fn
	f.mu.Unlock()
	var err error
	if fn != nil {
		err = fn(rows, call)
	}
	if err == nil {
		f.mu.Lock()
		f.stored = append(f.stored, rows...)
		f.mu.Unlock()
	}
	return err
}

// storedIDs returns the record ids of the rows the inserter accepted, sorted.
func (f *fakeInserter) storedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]string, len(f.stored))
	for i, row := range f.stored {
		ids[i] = row.RecordID.String()
	}
	slices.Sort(ids)
	return ids
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

// publish stores one syslog record in the central ingest stream. The message
// id differs per call, so a repeated record id is stored as a second message.
func publish(t *testing.T, hub *edgebus.Hub, recordID, tenantID string) {
	t.Helper()
	if err := tryPublish(t, hub, recordID, tenantID); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

// tryPublish is publish that returns the stream's answer, so a test can
// assert that a full stream refuses the record.
func tryPublish(t *testing.T, hub *edgebus.Hub, recordID, tenantID string) error {
	t.Helper()
	b := baseSyslog()
	record := syslogEnvelope(b.Build())
	record.SetRecordId(recordID)
	data, err := proto.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	subject := edgebus.CentralIngestSubject(tenantID, edgebus.IngestRecordTypeSyslog, testDevice)
	_, err = hub.JetStream().Publish(context.Background(), subject, data, jetstream.WithMsgID(fmt.Sprintf("%s-%d", recordID, time.Now().UnixNano())))
	return err
}

func recordID(n int) string { return fmt.Sprintf("018f0000-0000-7000-8000-%012d", n) }

type running struct {
	hub    *edgebus.Hub
	sink   *Sink
	reader *sdkmetric.ManualReader
	cancel context.CancelFunc
	done   chan struct{}
}

// bareSink starts a sink without its Run loop, so a test drives fetch,
// process, and clean by hand.
func bareSink(t *testing.T, hub *edgebus.Hub, cfg Config) *running {
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
	return &running{hub: hub, sink: sink, reader: reader}
}

func startSink(t *testing.T, hub *edgebus.Hub, cfg Config) *running {
	t.Helper()
	r := bareSink(t, hub, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel, r.done = cancel, make(chan struct{})
	sink := r.sink
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

// fetchOffered fetches until the stream has offered n records, which may
// take several calls while a negative acknowledgement's delay runs out.
func fetchOffered(t *testing.T, sink *Sink, n int) []jetstream.Msg {
	t.Helper()
	var offered []jetstream.Msg
	waitFor(t, fmt.Sprintf("%d records offered", n), func() bool {
		msgs, err := sink.fetch()
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		offered = append(offered, msgs...)
		return len(offered) >= n
	})
	return offered
}

// bySequence orders messages by stream sequence. Redeliveries after two
// negative acknowledgements arrive in the order their delays run out, which
// is not the order of the records.
func bySequence(t *testing.T, msgs []jetstream.Msg) []jetstream.Msg {
	t.Helper()
	seq := func(m jetstream.Msg) uint64 {
		meta, err := m.Metadata()
		if err != nil {
			t.Fatalf("message metadata: %v", err)
		}
		return meta.Sequence.Stream
	}
	slices.SortFunc(msgs, func(a, b jetstream.Msg) int { return cmp.Compare(seq(a), seq(b)) })
	return msgs
}

// syncBuffer is a log sink safe for the sink's goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func failOnce(call int) error {
	if call == 1 {
		return errs.From(errUnavailable).Retryable().Msg("send insert")
	}
	return nil
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
	if got := counter(t, r, "flowseer.history.records.redelivered", "", ""); got != 4 {
		t.Errorf("redelivered = %d, want 4: each record came back once", got)
	}
	inserter.mu.Lock()
	defer inserter.mu.Unlock()
	if len(inserter.tokens) < 2 || inserter.tokens[1] != inserter.tokens[0] {
		t.Errorf("tokens = %v, want the retry to carry the first attempt's token", inserter.tokens)
	}
	if len(inserter.calls) > 1 && len(inserter.calls[1]) != 4 {
		t.Errorf("retry carried %d rows, want the 4 of the first attempt", len(inserter.calls[1]))
	}
}

// Batch {1,2,3} fails after the store may have applied it, then records 4 and
// 5 arrive. The five come back in one fetch, so a sink that did not keep the
// batch would insert them as one block with a new token.
func TestAnAmbiguousFailureRetriesItsBatchWithItsTokenAndNewRecordsFormTheirOwn(t *testing.T) {
	hub := startHub(t)
	inserter := &fakeInserter{fn: func(_ []Row, call int) error { return failOnce(call) }}
	for n := 1; n <= 3; n++ {
		publish(t, hub, recordID(n), testTenant)
	}
	r := bareSink(t, hub, Config{Inserter: inserter, RetryDelay: 200 * time.Millisecond})
	ctx := context.Background()
	r.sink.process(ctx, fetchOffered(t, r.sink, 3))
	publish(t, hub, recordID(4), testTenant)
	publish(t, hub, recordID(5), testTenant)

	r.sink.process(ctx, fetchOffered(t, r.sink, 5))

	inserter.mu.Lock()
	defer inserter.mu.Unlock()
	if len(inserter.calls) != 3 {
		t.Fatalf("inserts = %d, want 3: the failed batch, the new pair, and the retained batch", len(inserter.calls))
	}
	byToken := map[string][]string{}
	for i, call := range inserter.calls[1:] {
		for _, row := range call {
			byToken[inserter.tokens[i+1]] = append(byToken[inserter.tokens[i+1]], row.RecordID.String())
		}
	}
	for _, ids := range byToken {
		slices.Sort(ids)
	}
	if got := byToken[inserter.tokens[0]]; !slices.Equal(got, []string{recordID(1), recordID(2), recordID(3)}) {
		t.Errorf("rows under the first attempt's token = %v, want records 1 to 3", got)
	}
	delete(byToken, inserter.tokens[0])
	for token, ids := range byToken {
		if !slices.Equal(ids, []string{recordID(4), recordID(5)}) {
			t.Errorf("rows under token %q = %v, want records 4 and 5", token, ids)
		}
	}
	if len(byToken) != 1 {
		t.Errorf("tokens besides the first attempt's = %d, want 1 for the new pair", len(byToken))
	}
}

// A peer sink takes record 2, so it never returns to this sink. The batch
// holding record 1 waits for it until the deadline and then lets record 1 go.
func TestARetainedBatchWhoseMemberNeverReturnsIsReleasedAtItsDeadline(t *testing.T) {
	hub := startHub(t)
	inserter := &fakeInserter{fn: func(_ []Row, call int) error { return failOnce(call) }}
	publish(t, hub, recordID(1), testTenant)
	publish(t, hub, recordID(2), testTenant)
	r := bareSink(t, hub, Config{Inserter: inserter})
	ctx := context.Background()
	r.sink.process(ctx, fetchOffered(t, r.sink, 2))
	returned := bySequence(t, fetchOffered(t, r.sink, 2))
	if err := returned[1].Ack(); err != nil {
		t.Fatalf("peer acknowledges record 2: %v", err)
	}
	r.sink.process(ctx, returned[:1])
	if got := inserter.callCount(); got != 1 {
		t.Fatalf("inserts while the batch waits = %d, want only the failed one", got)
	}

	r.sink.batches[0].deadline = time.Now().Add(-time.Second)
	r.sink.settleRetained(ctx, time.Now())
	r.sink.process(ctx, fetchOffered(t, r.sink, 1))

	if got := inserter.storedIDs(); !slices.Equal(got, []string{recordID(1)}) {
		t.Errorf("stored records = %v, want record 1 alone once its batch was released", got)
	}
	waitFor(t, "both records resolved", func() bool { return consumerInfo(t, r).AckFloor.Stream == 2 })
}

// The deadline a transient failure sets is twice the retry delay plus the
// grace, counted from the moment the batch is retained.
func TestAFailedInsertRetainsItsBatchForTwiceTheRetryDelayPlusTheGrace(t *testing.T) {
	hub := startHub(t)
	inserter := &fakeInserter{fn: func(_ []Row, call int) error { return failOnce(call) }}
	publish(t, hub, recordID(1), testTenant)
	r := bareSink(t, hub, Config{Inserter: inserter, RetryDelay: 200 * time.Millisecond})

	before := time.Now()
	r.sink.process(context.Background(), fetchOffered(t, r.sink, 1))
	after := time.Now()

	if len(r.sink.batches) != 1 {
		t.Fatalf("held batches = %d, want 1", len(r.sink.batches))
	}
	wait := 2*r.sink.cfg.RetryDelay + retainGrace
	if got := r.sink.batches[0].deadline; got.Before(before.Add(wait)) || got.After(after.Add(wait)) {
		t.Errorf("deadline = %v, want between %v and %v", got, before.Add(wait), after.Add(wait))
	}
}

// Record 1 comes back twice before record 2 returns. The batch inserts once,
// when both are present, and acknowledges record 1 through its later delivery.
func TestARetainedMemberDeliveredTwiceIsInsertedOnceAndAcknowledgedThroughTheLaterDelivery(t *testing.T) {
	hub := startHub(t)
	inserter := &fakeInserter{fn: func(_ []Row, call int) error { return failOnce(call) }}
	publish(t, hub, recordID(1), testTenant)
	publish(t, hub, recordID(2), testTenant)
	r := bareSink(t, hub, Config{Inserter: inserter})
	ctx := context.Background()
	r.sink.process(ctx, fetchOffered(t, r.sink, 2))
	second := fetchOffered(t, r.sink, 2)
	r.sink.process(ctx, second[:1])
	if err := second[0].NakWithDelay(r.sink.cfg.RetryDelay); err != nil {
		t.Fatalf("nak the first delivery of record 1: %v", err)
	}
	third := fetchOffered(t, r.sink, 1)
	r.sink.process(ctx, third)
	if got := inserter.callCount(); got != 1 {
		t.Fatalf("inserts before record 2 returned = %d, want only the failed one", got)
	}

	r.sink.process(ctx, second[1:])

	if got := inserter.callCount(); got != 2 {
		t.Errorf("inserts = %d, want 2: the failed attempt and one retry", got)
	}
	inserter.mu.Lock()
	rows, sameToken := len(inserter.calls[len(inserter.calls)-1]), inserter.tokens[len(inserter.tokens)-1] == inserter.tokens[0]
	inserter.mu.Unlock()
	if rows != 2 || !sameToken {
		t.Errorf("retry carried %d rows and the first token = %v, want 2 rows and the first token", rows, sameToken)
	}
	waitFor(t, "both records acknowledged", func() bool {
		i := consumerInfo(t, r)
		return i.NumAckPending == 0 && i.AckFloor.Stream == 2
	})
}

// A consumer deleted while the sink is down is created again with the
// member's config, starts at the first stored record, and is reported.
func TestAMissingMemberIsCreatedAgainWithAWarningAndOffersEveryStoredRecord(t *testing.T) {
	hub := startHub(t)
	publish(t, hub, recordID(1), testTenant)
	publish(t, hub, recordID(2), testTenant)
	stream, err := hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if err := stream.DeleteConsumer(context.Background(), consumerName); err != nil {
		t.Fatalf("delete the member: %v", err)
	}
	var logs syncBuffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	r := bareSink(t, hub, Config{Inserter: &fakeInserter{}, Logger: logger})

	if got := len(fetchOffered(t, r.sink, 2)); got != 2 {
		t.Errorf("records offered by the recreated member = %d, want both stored records", got)
	}
	cfg := consumerInfo(t, r).Config
	if cfg.AckPolicy != jetstream.AckExplicitPolicy || cfg.MaxDeliver != -1 || cfg.AckWait != 2*time.Minute || cfg.MaxAckPending != 10000 || cfg.FilterSubject != "" {
		t.Errorf("recreated member config = %+v, want the member's explicit-ack, unlimited-delivery, unfiltered config", cfg)
	}
	if out := logs.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "flowseer.history.reason=member") {
		t.Errorf("log = %q, want a warning with reason member", out)
	}
}

// A member changed by hand is not repaired, since cleanup trusts its config.
// The sink keeps consuming, and every pass reports its failure.
func TestADifferingMemberIsLeftAsItIsAndEveryCleanupPassFailsWithItsErrorType(t *testing.T) {
	hub := startHub(t)
	stream, err := hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	member, err := stream.Consumer(context.Background(), consumerName)
	if err != nil {
		t.Fatalf("member: %v", err)
	}
	cfg := member.CachedInfo().Config
	cfg.FilterSubject = "flowseer." + testTenant + ".ingest.syslog.>"
	if _, err := stream.UpdateConsumer(context.Background(), cfg); err != nil {
		t.Fatalf("filter the member: %v", err)
	}
	r := bareSink(t, hub, Config{Inserter: &fakeInserter{}})

	r.sink.clean(context.Background())
	r.sink.clean(context.Background())

	if got := consumerInfo(t, r).Config.FilterSubject; got != cfg.FilterSubject {
		t.Errorf("member filter = %q, want %q: the sink must leave the member as it found it", got, cfg.FilterSubject)
	}
	if got := cleanupErrorTypes(t, r); got["cleanup"] != 2 || len(got) != 1 {
		t.Errorf("cleanup passes by error type = %v, want two failures of type cleanup", got)
	}
}

// Shutdown hands a held batch back without counting a retry. The member that
// had returned is negatively acknowledged so the stream offers it again.
func TestShutdownReleasesAHeldBatchWithoutCountingARetry(t *testing.T) {
	hub := startHub(t)
	inserter := &fakeInserter{fn: func(_ []Row, call int) error { return failOnce(call) }}
	publish(t, hub, recordID(1), testTenant)
	publish(t, hub, recordID(2), testTenant)
	r := bareSink(t, hub, Config{Inserter: inserter})
	ctx := context.Background()
	r.sink.process(ctx, fetchOffered(t, r.sink, 2))
	r.sink.process(ctx, fetchOffered(t, r.sink, 2)[:1])
	retried := counter(t, r, "flowseer.history.records.retried", "", "")

	r.sink.releaseRetained()

	if len(r.sink.batches) != 0 || len(r.sink.retained) != 0 {
		t.Errorf("held batches = %d and members = %d, want none", len(r.sink.batches), len(r.sink.retained))
	}
	if got := counter(t, r, "flowseer.history.records.retried", "", ""); got != retried {
		t.Errorf("retried = %d after shutdown, want the %d counted before", got, retried)
	}
	if got := len(fetchOffered(t, r.sink, 1)); got != 1 {
		t.Errorf("records offered after shutdown = %d, want the returned member", got)
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

func TestATransientFailureOfTheFirstHalfNaksTheSecondHalfWithTheRetryDelay(t *testing.T) {
	hub := startHub(t)
	inserter := &fakeInserter{fn: func(_ []Row, call int) error {
		switch call {
		case 1:
			return errs.New().Code(ErrCodeRejected).Msg("send insert")
		case 2:
			return errs.From(errUnavailable).Retryable().Msg("send insert")
		}
		return nil
	}}
	for n := 1; n <= 4; n++ {
		publish(t, hub, recordID(n), testTenant)
	}
	r := startSink(t, hub, Config{Inserter: inserter, FlushInterval: 300 * time.Millisecond})

	// The consumer's AckWait is minutes, and waitFor gives up after 20 seconds,
	// so a half left unacknowledged instead of naked never completes here.
	waitFor(t, "every record acknowledged", func() bool {
		i := consumerInfo(t, r)
		return i.NumAckPending == 0 && i.AckFloor.Stream == 4
	})

	if got := counter(t, r, "flowseer.history.records.retried", "", ""); got != 4 {
		t.Errorf("retried = %d, want 4: both halves wait out the retry delay", got)
	}
	if got := counter(t, r, "flowseer.history.records.inserted", "", ""); got != 4 {
		t.Errorf("inserted = %d, want 4", got)
	}
}

// Only the first half failed, so only it may be held. The second half was
// never sent and returns as new records.
func TestTheNeverSentSecondHalfOfARejectedBatchIsNotRetained(t *testing.T) {
	hub := startHub(t)
	inserter := &fakeInserter{fn: func(_ []Row, call int) error {
		switch call {
		case 1:
			return errs.New().Code(ErrCodeRejected).Msg("send insert")
		case 2:
			return errs.From(errUnavailable).Retryable().Msg("send insert")
		}
		return nil
	}}
	for n := 1; n <= 4; n++ {
		publish(t, hub, recordID(n), testTenant)
	}
	r := bareSink(t, hub, Config{Inserter: inserter})

	r.sink.process(context.Background(), bySequence(t, fetchOffered(t, r.sink, 4)))

	if len(r.sink.batches) != 1 || len(r.sink.batches[0].members) != 2 {
		t.Fatalf("held batches = %d, want one batch of the first half", len(r.sink.batches))
	}
	var held []uint64
	for sequence := range r.sink.retained {
		held = append(held, sequence)
	}
	slices.Sort(held)
	if !slices.Equal(held, []uint64{1, 2}) {
		t.Errorf("retained sequences = %v, want [1 2]: the second half must not be held", held)
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
