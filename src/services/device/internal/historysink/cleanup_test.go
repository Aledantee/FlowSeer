package historysink

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

func ingestStreamInfo(t *testing.T, hub *edgebus.Hub) *jetstream.StreamInfo {
	t.Helper()
	stream, err := hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("load stream: %v", err)
	}
	info, err := stream.Info(context.Background())
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	return info
}

// limitToRecords bounds the typed stream, in place, to n records of the size
// of those already stored plus half a record, so the stream accepts n records
// and refuses the next. It returns the stored size of one record.
func limitToRecords(t *testing.T, hub *edgebus.Hub, n int) uint64 {
	t.Helper()
	info := ingestStreamInfo(t, hub)
	if info.State.Msgs == 0 {
		t.Fatal("limitToRecords needs a stored record to measure")
	}
	size := info.State.Bytes / info.State.Msgs
	cfg := info.Config
	cfg.MaxBytes = int64(size)*int64(n) + int64(size)/2
	if _, err := hub.JetStream().UpdateStream(context.Background(), cfg); err != nil {
		t.Fatalf("bound the stream: %v", err)
	}
	return size
}

func addStray(t *testing.T, hub *edgebus.Hub) jetstream.Stream {
	t.Helper()
	stream, err := hub.JetStream().Stream(context.Background(), edgebus.IngestStream(edgebus.IngestRecordTypeSyslog))
	if err != nil {
		t.Fatalf("load stream: %v", err)
	}
	if _, err := stream.CreateConsumer(context.Background(), jetstream.ConsumerConfig{Durable: "stray", AckPolicy: jetstream.AckExplicitPolicy}); err != nil {
		t.Fatalf("create stray consumer: %v", err)
	}
	return stream
}

func collect(t *testing.T, r *running) metricdata.ResourceMetrics {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := r.reader.Collect(context.Background(), &data); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	return data
}

// gaugeValues returns every observation of the named gauge.
func gaugeValues(t *testing.T, r *running, name string) []float64 {
	t.Helper()
	var out []float64
	for _, scope := range collect(t, r).ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != name {
				continue
			}
			switch d := m.Data.(type) {
			case metricdata.Gauge[int64]:
				for _, point := range d.DataPoints {
					out = append(out, float64(point.Value))
				}
			case metricdata.Gauge[float64]:
				for _, point := range d.DataPoints {
					out = append(out, point.Value)
				}
			default:
				t.Fatalf("metric %q has data %T, want a gauge", name, m.Data)
			}
		}
	}
	return out
}

// histogramErrorTypes counts the recordings of the named histogram by their
// error.type attribute, with "" for a recording without one.
func histogramErrorTypes(t *testing.T, r *running, name string) map[string]uint64 {
	t.Helper()
	counts := map[string]uint64{}
	for _, scope := range collect(t, r).ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != name {
				continue
			}
			d, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("metric %q has data %T, want a float histogram", name, m.Data)
			}
			for _, point := range d.DataPoints {
				kind, _ := point.Attributes.Value(attribute.Key("error.type"))
				counts[kind.AsString()] += point.Count
			}
		}
	}
	return counts
}

func metricNames(t *testing.T, r *running) []string {
	t.Helper()
	var names []string
	for _, scope := range collect(t, r).ScopeMetrics {
		for _, m := range scope.Metrics {
			names = append(names, m.Name)
		}
	}
	return names
}

// Records 1 to 3 fill the stream. Sink A holds record 1 and a peer sink
// resolves record 2. A pass removes nothing and a fourth record is refused.
// Once A resolves record 1, the next pass removes records 1 and 2 and the
// fourth record is accepted.
func TestCleanupRemovesOnlyWhatEverySinkResolved(t *testing.T) {
	hub := startHub(t)
	inserterA, inserterB := &fakeInserter{}, &fakeInserter{}
	a := bareSink(t, hub, Config{Inserter: inserterA, MaxBatch: 1})
	b := bareSink(t, hub, Config{Inserter: inserterB, MaxBatch: 1})
	publish(t, hub, recordID(1), testTenant)
	limitToRecords(t, hub, 3)
	publish(t, hub, recordID(2), testTenant)
	publish(t, hub, recordID(3), testTenant)
	ctx := context.Background()

	held := fetchOffered(t, a.sink, 1)
	b.sink.process(ctx, fetchOffered(t, b.sink, 1))
	waitFor(t, "the peer's acknowledgement of record 2", func() bool {
		info := consumerInfo(t, a)
		return info.NumAckPending == 1 && info.NumPending == 1
	})
	a.sink.clean(ctx)

	state := ingestStreamInfo(t, hub).State
	if state.FirstSeq != 1 || state.Msgs != 3 {
		t.Fatalf("stream after the first pass: first sequence %d, %d records, want 1 and 3", state.FirstSeq, state.Msgs)
	}
	if err := tryPublish(t, hub, recordID(4), testTenant); !edgebus.IsStreamFull(err) {
		t.Fatalf("fourth record while record 1 is held = %v, want a full-stream refusal", err)
	}

	a.sink.process(ctx, held)
	waitFor(t, "the acknowledgement of record 1", func() bool { return consumerInfo(t, a).AckFloor.Stream == 2 })
	a.sink.clean(ctx)

	state = ingestStreamInfo(t, hub).State
	if state.FirstSeq != 3 || state.Msgs != 1 {
		t.Errorf("stream after the second pass: first sequence %d, %d records, want 3 and 1", state.FirstSeq, state.Msgs)
	}
	if err := tryPublish(t, hub, recordID(4), testTenant); err != nil {
		t.Errorf("fourth record after cleanup freed room = %v, want it accepted", err)
	}
	if got := inserterA.storedIDs(); !slices.Equal(got, []string{recordID(1)}) {
		t.Errorf("sink A stored %v, want record 1", got)
	}
	if got := inserterB.storedIDs(); !slices.Equal(got, []string{recordID(2)}) {
		t.Errorf("sink B stored %v, want record 2", got)
	}
}

// Sink A negatively acknowledges three records after a failed insert and a
// peer inserts the redeliveries. Each record is stored once and cleanup
// removes them.
func TestARecordNackedByOneSinkAndInsertedByAPeerIsStoredOnceAndRemoved(t *testing.T) {
	hub := startHub(t)
	failing := &fakeInserter{fn: func([]Row, int) error { return errs.From(errUnavailable).Retryable().Msg("send insert") }}
	working := &fakeInserter{}
	a := bareSink(t, hub, Config{Inserter: failing})
	b := bareSink(t, hub, Config{Inserter: working})
	for n := 1; n <= 3; n++ {
		publish(t, hub, recordID(n), testTenant)
	}
	ctx := context.Background()

	a.sink.process(ctx, fetchOffered(t, a.sink, 3))
	b.sink.process(ctx, fetchOffered(t, b.sink, 3))
	waitFor(t, "the peer's acknowledgements", func() bool { return consumerInfo(t, b).AckFloor.Stream == 3 })
	a.sink.clean(ctx)

	if state := ingestStreamInfo(t, hub).State; state.Msgs != 0 || state.FirstSeq != 4 {
		t.Errorf("stream after cleanup: first sequence %d, %d records, want 4 and none", state.FirstSeq, state.Msgs)
	}
	if got, want := working.storedIDs(), []string{recordID(1), recordID(2), recordID(3)}; !slices.Equal(got, want) {
		t.Errorf("peer stored %v, want each record once as %v", got, want)
	}
	if got := failing.storedIDs(); len(got) != 0 {
		t.Errorf("failing sink stored %v, want nothing", got)
	}
	if got := counter(t, b, "flowseer.history.records.inserted", "", ""); got != 3 {
		t.Errorf("peer inserted = %d, want 3", got)
	}
}

// The store is down for three seconds with three records accepted. Cleanup
// keeps passing and the stream keeps every record until the store returns.
func TestRecordsPendingThroughAStoreOutageStayInTheStreamAndAreInserted(t *testing.T) {
	const outage = 3 * time.Second
	started := time.Now()
	inserter := &fakeInserter{fn: func([]Row, int) error {
		if time.Since(started) < outage {
			return errs.From(errUnavailable).Retryable().Msg("send insert")
		}
		return nil
	}}
	hub := startHub(t)
	for n := 1; n <= 3; n++ {
		publish(t, hub, recordID(n), testTenant)
	}
	r := startSink(t, hub, Config{Inserter: inserter, CleanInterval: 50 * time.Millisecond})

	waitFor(t, "inserts and cleanup passes during the outage", func() bool {
		return inserter.callCount() >= 2 && histogramErrorTypes(t, r, "flowseer.history.cleanup.duration")[""] >= 3
	})
	if time.Since(started) >= outage {
		t.Fatal("the outage ended before the stream was checked")
	}
	if state := ingestStreamInfo(t, hub).State; state.Msgs != 3 || state.FirstSeq != 1 {
		t.Errorf("stream during the outage: first sequence %d, %d records, want 1 and 3", state.FirstSeq, state.Msgs)
	}

	waitFor(t, "the stream to empty after the store returned", func() bool { return ingestStreamInfo(t, hub).State.Msgs == 0 })

	if got, want := inserter.storedIDs(), []string{recordID(1), recordID(2), recordID(3)}; !slices.Equal(got, want) {
		t.Errorf("stored %v, want every record once as %v", got, want)
	}
	if got := counter(t, r, "flowseer.history.records.refused", "", ""); got != 0 {
		t.Errorf("refused = %d, want 0: an outage must not terminate a record", got)
	}
}

// ClickHouse rejects the second of three records. The other two are stored,
// the refusal is counted, and the next pass removes all three from the stream.
func TestARefusedRecordDoesNotHoldTheStream(t *testing.T) {
	inserter := &fakeInserter{fn: func(rows []Row, _ int) error {
		for _, row := range rows {
			if row.RecordID.String() == recordID(2) {
				return errs.New().Code(ErrCodeRejected).Msg("send insert")
			}
		}
		return nil
	}}
	hub := startHub(t)
	for n := 1; n <= 3; n++ {
		publish(t, hub, recordID(n), testTenant)
	}
	r := startSink(t, hub, Config{Inserter: inserter, MaxBatch: 3, CleanInterval: 50 * time.Millisecond})

	waitFor(t, "cleanup to remove all three records", func() bool {
		state := ingestStreamInfo(t, hub).State
		return state.Msgs == 0 && state.FirstSeq == 4
	})

	if got, want := inserter.storedIDs(), []string{recordID(1), recordID(3)}; !slices.Equal(got, want) {
		t.Errorf("stored %v, want %v", got, want)
	}
	if got := counter(t, r, "flowseer.history.records.refused", "flowseer.history.reason", "rejected"); got != 1 {
		t.Errorf("refused = %d, want 1", got)
	}
}

// With the stream full and five records pending, the stream gauges report
// them, each series once.
func TestStreamGaugesReportAFullStreamWithPendingRecords(t *testing.T) {
	hub := startHub(t)
	publish(t, hub, recordID(0), testTenant)
	size := limitToRecords(t, hub, 5)
	for n := 1; n < 5; n++ {
		publish(t, hub, recordID(n), testTenant)
	}
	if err := tryPublish(t, hub, recordID(5), testTenant); !edgebus.IsStreamFull(err) {
		t.Fatalf("sixth record = %v, want a full-stream refusal", err)
	}
	r := bareSink(t, hub, Config{Inserter: &fakeInserter{}})

	r.sink.clean(context.Background())

	state := ingestStreamInfo(t, hub)
	for name, want := range map[string]float64{
		"flowseer.history.records.pending": 5,
		"flowseer.history.stream.usage":    float64(state.State.Bytes),
		"flowseer.history.stream.limit":    float64(state.Config.MaxBytes),
	} {
		if got := gaugeValues(t, r, name); len(got) != 1 || got[0] != want {
			t.Errorf("%s = %v, want one series reading %v", name, got, want)
		}
	}
	if limit := state.Config.MaxBytes; limit-int64(state.State.Bytes) >= int64(size) {
		t.Errorf("stream.usage %d is not within one record of stream.limit %d", state.State.Bytes, limit)
	}
	if got := gaugeValues(t, r, "flowseer.history.stream.oldest.age"); len(got) != 1 || got[0] <= 0 {
		t.Errorf("stream.oldest.age = %v, want one positive series", got)
	}
}

func TestNoInstrumentCountsDiscardedRecords(t *testing.T) {
	hub := startHub(t)
	publish(t, hub, recordID(1), testTenant)
	r := bareSink(t, hub, Config{Inserter: &fakeInserter{}})
	r.sink.process(context.Background(), fetchOffered(t, r.sink, 1))
	r.sink.clean(context.Background())

	names := metricNames(t, r)

	if !slices.Contains(names, "flowseer.history.records.pending") {
		t.Fatalf("instruments = %v, want the stream gauges among them", names)
	}
	for _, name := range names {
		if strings.Contains(name, "discard") {
			t.Errorf("instrument %q counts discards, want none", name)
		}
	}
}

// A pass that fails leaves the gauges at the last report. The stray consumer
// makes the pass refuse, and the third record would show in a fresh report.
func TestAFailedCleanupPassKeepsTheGaugesAtTheirLastReport(t *testing.T) {
	hub := startHub(t)
	publish(t, hub, recordID(0), testTenant)
	publish(t, hub, recordID(1), testTenant)
	r := bareSink(t, hub, Config{Inserter: &fakeInserter{}})
	ctx := context.Background()
	r.sink.clean(ctx)
	before := gaugeValues(t, r, "flowseer.history.records.pending")
	if len(before) != 1 || before[0] != 2 {
		t.Fatalf("pending after the first pass = %v, want one series reading 2", before)
	}

	addStray(t, hub)
	publish(t, hub, recordID(2), testTenant)
	r.sink.clean(ctx)

	if got := gaugeValues(t, r, "flowseer.history.records.pending"); !slices.Equal(got, before) {
		t.Errorf("pending after a failed pass = %v, want the last report %v", got, before)
	}
	if got := histogramErrorTypes(t, r, "flowseer.history.cleanup.duration"); got["cleanup"] != 1 || got[""] != 1 {
		t.Errorf("cleanup passes by error type = %v, want one success and one cleanup failure", got)
	}
}

// Every pass fails while a stray consumer is on the stream. The sink keeps
// consuming, and cleanup resumes once the stray is gone.
func TestFailedCleanupPassesDoNotStopTheRunner(t *testing.T) {
	hub := startHub(t)
	stream := addStray(t, hub)
	inserter := &fakeInserter{}
	r := startSink(t, hub, Config{Inserter: inserter, CleanInterval: 20 * time.Millisecond})
	waitFor(t, "two failed passes", func() bool {
		return histogramErrorTypes(t, r, "flowseer.history.cleanup.duration")["cleanup"] >= 2
	})

	publish(t, hub, recordID(0), testTenant)
	waitFor(t, "the record acknowledged", func() bool { return consumerInfo(t, r).AckFloor.Stream == 1 })

	if state := ingestStreamInfo(t, hub).State; state.Msgs != 1 {
		t.Errorf("records stored while the stray is present = %d, want the acknowledged record kept", state.Msgs)
	}
	if err := stream.DeleteConsumer(context.Background(), "stray"); err != nil {
		t.Fatalf("delete stray consumer: %v", err)
	}
	waitFor(t, "cleanup to resume", func() bool { return ingestStreamInfo(t, hub).State.Msgs == 0 })
}

func TestCleanIntervalDefaultsToFiveSeconds(t *testing.T) {
	hub := startHub(t)

	r := bareSink(t, hub, Config{Inserter: &fakeInserter{}})

	if got, want := r.sink.cfg.CleanInterval, 5*time.Second; got != want {
		t.Errorf("clean interval = %s, want %s", got, want)
	}
}
