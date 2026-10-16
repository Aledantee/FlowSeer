package drain_test

import (
	"bytes"
	"context"
	"errors"
	"hash/crc32"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/fsconv"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/drain"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/seglog"
)

const logAttrKey = "flowseer.edge.buffer.log"

// scripted is a Send whose answer a test decides per call. It keeps a copy of
// every call's records and the fake time of the call.
type scripted struct {
	mu    sync.Mutex
	calls [][]string
	times []time.Time
	fn    func(call int, records []string) (int, error)
}

func (s *scripted) send(_ context.Context, records [][]byte) (int, error) {
	got := make([]string, len(records))
	for i, r := range records {
		got[i] = string(r)
	}
	s.mu.Lock()
	call := len(s.calls)
	s.calls = append(s.calls, got)
	s.times = append(s.times, time.Now())
	s.mu.Unlock()
	return s.fn(call, got)
}

func (s *scripted) snapshot() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

// countingLog records the sequences acknowledged on the real log.
type countingLog struct {
	*seglog.Log
	mu   sync.Mutex
	acks []uint64
}

func (c *countingLog) Ack(sequence uint64) error {
	c.mu.Lock()
	c.acks = append(c.acks, sequence)
	c.mu.Unlock()
	return c.Log.Ack(sequence)
}

func (c *countingLog) ackCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.acks)
}

// faultLog serves one segment and fails on demand.
type faultLog struct {
	segment seglog.Segment
	nextErr error
	ackErr  error
}

func (f *faultLog) Next(context.Context) (seglog.Segment, error) {
	if f.nextErr != nil {
		return seglog.Segment{}, f.nextErr
	}
	return f.segment, nil
}
func (f *faultLog) Ack(uint64) error             { return f.ackErr }
func (f *faultLog) Stats() (seglog.Stats, error) { return seglog.Stats{}, nil }

type running struct {
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
	err    error
}

func start(cfg drain.Config) *running {
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- drain.Run(ctx, cfg) }()
	return r
}

// join ends the drain and returns what Run returned. A test defers it so the
// bubble's main goroutine never exits with the drain still blocked.
func (r *running) join() error {
	r.once.Do(func() {
		r.cancel()
		r.err = <-r.done
	})
	return r.err
}

// await waits for a drain that ends by itself, such as one whose Wait gave up.
func (r *running) await(t *testing.T) {
	t.Helper()
	r.once.Do(func() { r.err = <-r.done })
	if r.err != nil {
		t.Errorf("Run() = %v, want nil", r.err)
	}
}

// stop is the deferred join. The test's own mustStop or await checks the
// result, so a failed drain is reported where the test can name it.
func (r *running) stop() { _ = r.join() }

func (r *running) mustStop(t *testing.T) {
	t.Helper()
	if err := r.join(); err != nil {
		t.Errorf("Run() = %v, want nil", err)
	}
}

func openLog(t *testing.T, dir string, cfg seglog.Config) *seglog.Log {
	t.Helper()
	cfg.Dir = dir
	l, err := seglog.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// idle lets the fake clock run far enough for every wait a test script causes,
// then returns once the drain is blocked again.
func idle() {
	time.Sleep(time.Minute)
	synctest.Wait()
}

func appendAll(t *testing.T, l *seglog.Log, records ...string) {
	t.Helper()
	for _, r := range records {
		if err := l.Append([]byte(r)); err != nil {
			t.Fatal(err)
		}
	}
}

func decodable(record []byte) bool { return !bytes.HasPrefix(record, []byte("bad")) }

func settleAll(_ int, records []string) (int, error) { return len(records), nil }

type meters struct {
	reader *sdkmetric.ManualReader
	meter  metric.Meter
}

func newMeters() meters {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return meters{reader: reader, meter: provider.Meter("test")}
}

func (m meters) collect(t *testing.T) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := m.reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	return rm
}

func findMetric(rm metricdata.ResourceMetrics, name string) (metricdata.Metrics, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, mm := range sm.Metrics {
			if mm.Name == name {
				return mm, true
			}
		}
	}
	return metricdata.Metrics{}, false
}

// counterValue sums a counter and checks that every point carries the log
// attribute.
func counterValue(t *testing.T, rm metricdata.ResourceMetrics, name string) int64 {
	t.Helper()
	m, ok := findMetric(rm, name)
	if !ok {
		t.Fatalf("metric %s not recorded", name)
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("metric %s is %T, want Sum[int64]", name, m.Data)
	}
	var total int64
	for _, dp := range sum.DataPoints {
		if got, _ := dp.Attributes.Value(logAttrKey); got.AsString() != "ingest" {
			t.Errorf("metric %s point has %s = %q, want ingest", name, logAttrKey, got.AsString())
		}
		total += dp.Value
	}
	return total
}

func histogramPoints(t *testing.T, rm metricdata.ResourceMetrics, name string) []metricdata.HistogramDataPoint[float64] {
	t.Helper()
	m, ok := findMetric(rm, name)
	if !ok {
		t.Fatalf("metric %s not recorded", name)
	}
	h, ok := m.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("metric %s is %T, want Histogram[float64]", name, m.Data)
	}
	return h.DataPoints
}

// capture keeps every record a drain logs.
type capture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *capture) Enabled(context.Context, slog.Level) bool { return true }
func (c *capture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	c.records = append(c.records, r)
	c.mu.Unlock()
	return nil
}
func (c *capture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *capture) WithGroup(string) slog.Handler      { return c }

func (c *capture) warnings() []slog.Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []slog.Record
	for _, r := range c.records {
		if r.Level == slog.LevelWarn {
			out = append(out, r)
		}
	}
	return out
}

func attrOf(r slog.Record, key string) slog.Value {
	var found slog.Value
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found = a.Value
			return false
		}
		return true
	})
	return found
}

func baseConfig(l drain.Log, s *scripted) drain.Config {
	return drain.Config{LogName: "ingest", Log: l, Send: s.send, Decodable: decodable}
}

func TestRefusedThenAcceptedWithinTheBackoffCeiling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := &countingLog{Log: openLog(t, t.TempDir(), seglog.Config{})}
		origin := time.Now()
		accepting := origin.Add(10 * time.Second)
		delivered := make(chan struct{})
		s := &scripted{fn: func(_ int, records []string) (int, error) {
			if time.Now().Before(accepting) {
				return 0, errors.New("central refuses")
			}
			close(delivered)
			return len(records), nil
		}}
		appendAll(t, l.Log, "r001")

		r := start(baseConfig(l, s))
		defer r.stop()
		<-delivered
		synctest.Wait()

		offsets := make([]time.Duration, len(s.times))
		for i, at := range s.times {
			offsets[i] = at.Sub(origin)
		}
		// Calls at 0.2 s (the linger), then 1, 2, 4 and 8 s apart.
		if last := offsets[len(offsets)-1]; last-10*time.Second > 30*time.Second {
			t.Errorf("accepted call at %v, want within 30s of central accepting at 10s", last)
		}
		wantGaps := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
		for i, gap := range wantGaps {
			if got := offsets[i+1] - offsets[i]; got != gap {
				t.Errorf("gap before call %d = %v, want %v", i+2, got, gap)
			}
		}
		if got := l.ackCount(); got != 1 {
			t.Errorf("Ack ran %d times, want 1", got)
		}
		r.mustStop(t)
	})
}

func TestShortSettleResumesAtTheFirstUnsettledRecord(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := &countingLog{Log: openLog(t, t.TempDir(), seglog.Config{})}
		finished := make(chan struct{})
		s := &scripted{fn: func(call int, records []string) (int, error) {
			if call == 0 {
				return 2, nil
			}
			close(finished)
			return len(records), nil
		}}
		appendAll(t, l.Log, "r001", "r002", "r003", "r004", "r005")

		r := start(baseConfig(l, s))
		defer r.stop()
		<-finished
		synctest.Wait()

		want := [][]string{
			{"r001", "r002", "r003", "r004", "r005"},
			{"r003", "r004", "r005"},
		}
		if got := s.snapshot(); !reflect.DeepEqual(got, want) {
			t.Errorf("calls = %v, want %v", got, want)
		}
		if got := l.ackCount(); got != 1 {
			t.Errorf("Ack ran %d times, want 1", got)
		}
		r.mustStop(t)
	})
}

func TestADamagedRecordCostsOneSegmentAndIsCounted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		// 64 bytes holds the 20-byte header and three 12-byte records.
		cfg := seglog.Config{SegmentBytes: 64, MaxRecordBytes: 36, MaxBytes: 1 << 20}
		first := openLog(t, dir, cfg)
		appendAll(t, first, "r001", "r002", "r003", "r004", "r005", "r006", "r007", "r008", "r009")
		if err := first.Close(); err != nil {
			t.Fatal(err)
		}
		files, err := filepath.Glob(filepath.Join(dir, "*.seg"))
		if err != nil || len(files) != 3 {
			t.Fatalf("segment files = %v, %v, want 3", files, err)
		}
		slices.Sort(files)
		b, err := os.ReadFile(files[1])
		if err != nil {
			t.Fatal(err)
		}
		b[len(b)-1] ^= 0xff
		if err := os.WriteFile(files[1], b, 0o600); err != nil {
			t.Fatal(err)
		}

		l := &countingLog{Log: openLog(t, dir, cfg)}
		stats, err := l.Stats()
		if err != nil {
			t.Fatal(err)
		}
		if stats.CorruptSegments != 1 {
			t.Errorf("CorruptSegments = %d, want 1", stats.CorruptSegments)
		}
		s := &scripted{fn: settleAll}
		r := start(baseConfig(l, s))
		defer r.stop()
		idle()

		want := [][]string{{"r001", "r002", "r003"}, {"r004", "r005"}, {"r007", "r008", "r009"}}
		if got := s.snapshot(); !reflect.DeepEqual(got, want) {
			t.Errorf("calls = %v, want %v", got, want)
		}
		if got := l.ackCount(); got != 3 {
			t.Errorf("Ack ran %d times, want 3", got)
		}
		r.mustStop(t)
	})
}

// waitRecorder substitutes Config.Wait: it records each delay, never sleeps,
// and ends the drain after limit waits.
type waitRecorder struct {
	limit int
	waits []time.Duration
}

func (w *waitRecorder) wait(ctx context.Context, d time.Duration) bool {
	w.waits = append(w.waits, d)
	return ctx.Err() == nil && len(w.waits) < w.limit
}

func TestBackoffDoublesToTheCeiling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := openLog(t, t.TempDir(), seglog.Config{})
		appendAll(t, l, "r001")
		// An error and a zero settled count both back off.
		s := &scripted{fn: func(call int, _ []string) (int, error) {
			if call%2 == 0 {
				return 0, errors.New("central refuses")
			}
			return 0, nil
		}}
		w := &waitRecorder{limit: 7}
		cfg := baseConfig(l, s)
		cfg.Wait = w.wait

		r := start(cfg)
		defer r.stop()
		r.await(t)

		want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
		if !reflect.DeepEqual(w.waits, want) {
			t.Errorf("waits = %v, want %v", w.waits, want)
		}
	})
}

func TestBackoffResetsAfterAPartialSettle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := openLog(t, t.TempDir(), seglog.Config{})
		appendAll(t, l, "r001", "r002")
		s := &scripted{fn: func(call int, _ []string) (int, error) {
			if call == 2 {
				return 1, nil
			}
			return 0, errors.New("central refuses")
		}}
		w := &waitRecorder{limit: 3}
		cfg := baseConfig(l, s)
		cfg.Wait = w.wait

		r := start(cfg)
		defer r.stop()
		r.await(t)

		// The partial settle waits for nothing, so the next wait is the first
		// of a new sequence.
		want := []time.Duration{time.Second, 2 * time.Second, time.Second}
		if !reflect.DeepEqual(w.waits, want) {
			t.Errorf("waits = %v, want %v", w.waits, want)
		}
		wantCalls := [][]string{{"r001", "r002"}, {"r001", "r002"}, {"r001", "r002"}, {"r002"}}
		if got := s.snapshot(); !reflect.DeepEqual(got, wantCalls) {
			t.Errorf("calls = %v, want %v", got, wantCalls)
		}
	})
}

func TestAnUndecodableRecordIsCountedAndPassed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := &countingLog{Log: openLog(t, t.TempDir(), seglog.Config{})}
		appendAll(t, l.Log, "r001", "bad1", "r002")
		s := &scripted{fn: settleAll}
		m := newMeters()
		cfg := baseConfig(l, s)
		cfg.Meter = m.meter

		r := start(cfg)
		defer r.stop()
		idle()
		r.mustStop(t)

		if got, want := s.snapshot(), [][]string{{"r001", "r002"}}; !reflect.DeepEqual(got, want) {
			t.Errorf("calls = %v, want %v", got, want)
		}
		if got := l.ackCount(); got != 1 {
			t.Errorf("Ack ran %d times, want 1", got)
		}
		rm := m.collect(t)
		if got := counterValue(t, rm, "flowseer.edge.delivery.undecodable"); got != 1 {
			t.Errorf("undecodable = %d, want 1", got)
		}
		if got := counterValue(t, rm, "flowseer.edge.delivery.settled"); got != 2 {
			t.Errorf("settled = %d, want 2", got)
		}
	})
}

func TestAnUndecodableRecordBehindASettledPrefixIsPassedOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := &countingLog{Log: openLog(t, t.TempDir(), seglog.Config{})}
		appendAll(t, l.Log, "r001", "bad1", "r002")
		s := &scripted{fn: func(call int, records []string) (int, error) {
			if call == 0 {
				return 1, nil
			}
			return len(records), nil
		}}
		m := newMeters()
		cfg := baseConfig(l, s)
		cfg.Meter = m.meter

		r := start(cfg)
		defer r.stop()
		idle()
		r.mustStop(t)

		// The first settled record is followed by the bad one, so the resume
		// sends only the last record.
		want := [][]string{{"r001", "r002"}, {"r002"}}
		if got := s.snapshot(); !reflect.DeepEqual(got, want) {
			t.Errorf("calls = %v, want %v", got, want)
		}
		if got := counterValue(t, m.collect(t), "flowseer.edge.delivery.undecodable"); got != 1 {
			t.Errorf("undecodable = %d, want 1", got)
		}
	})
}

func TestASegmentOfOnlyUndecodableRecordsIsAcknowledgedWithoutACall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		// One record per segment: the second segment proves the drain moved on.
		cfg := seglog.Config{SegmentBytes: 40, MaxRecordBytes: 12, MaxBytes: 1 << 20}
		l := &countingLog{Log: openLog(t, dir, cfg)}
		appendAll(t, l.Log, "bad1", "r001")
		s := &scripted{fn: settleAll}
		m := newMeters()
		dc := baseConfig(l, s)
		dc.Meter = m.meter

		r := start(dc)
		defer r.stop()
		idle()
		r.mustStop(t)

		if got, want := s.snapshot(), [][]string{{"r001"}}; !reflect.DeepEqual(got, want) {
			t.Errorf("calls = %v, want %v", got, want)
		}
		if got := l.ackCount(); got != 2 {
			t.Errorf("Ack ran %d times, want 2", got)
		}
		if got := counterValue(t, m.collect(t), "flowseer.edge.delivery.undecodable"); got != 1 {
			t.Errorf("undecodable = %d, want 1", got)
		}
	})
}

// segmentHeader is a valid 20-byte seglog file header: FSSL, version 1,
// reserved zero, the creation time in Unix nanoseconds, and a CRC-32C of
// those 16 bytes.
func segmentHeader(created time.Time) []byte {
	b := make([]byte, 20)
	copy(b, "FSSL")
	b[4] = 1
	for i := range 8 {
		b[8+i] = byte(created.UnixNano() >> (8 * i))
	}
	crc := crc32.Checksum(b[:16], crc32.MakeTable(crc32.Castagnoli))
	for i := range 4 {
		b[16+i] = byte(crc >> (8 * i))
	}
	return b
}

func TestASegmentRecoveredWithZeroRecordsIsAcknowledgedWithoutACall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "0000000000000001.seg"), segmentHeader(time.Now()), 0o600); err != nil {
			t.Fatal(err)
		}
		l := &countingLog{Log: openLog(t, dir, seglog.Config{})}
		stats, err := l.Stats()
		if err != nil || stats.Segments != 1 || stats.Records != 0 {
			t.Fatalf("Stats() = %+v, %v, want one segment with no records", stats, err)
		}
		appendAll(t, l.Log, "r001")
		s := &scripted{fn: settleAll}

		r := start(baseConfig(l, s))
		defer r.stop()
		idle()
		r.mustStop(t)

		if got, want := s.snapshot(), [][]string{{"r001"}}; !reflect.DeepEqual(got, want) {
			t.Errorf("calls = %v, want %v", got, want)
		}
		if got := l.ackCount(); got != 2 {
			t.Errorf("Ack ran %d times, want 2", got)
		}
	})
}

// runAgeBound fills a segment, settles one record of it, then lets the age
// bound delete it before a second segment arrives. It returns the calls the
// scripted send saw.
func runAgeBound(t *testing.T, logger *slog.Logger) [][]string {
	t.Helper()
	l := openLog(t, t.TempDir(), seglog.Config{MaxAge: time.Hour, RollAge: 10 * time.Minute})
	done := make(chan struct{})
	s := &scripted{fn: func(call int, records []string) (int, error) {
		switch {
		case records[0] == "b001":
			close(done)
			return len(records), nil
		case records[0] == "c001":
			return len(records), nil
		case call == 0:
			return 1, nil
		default:
			return 0, errors.New("central refuses")
		}
	}}
	appendAll(t, l, "a001", "a002", "a003")
	cfg := baseConfig(l, s)
	cfg.Logger = logger

	r := start(cfg)
	defer r.stop()
	time.Sleep(59 * time.Minute)
	appendAll(t, l, "b001", "b002")
	<-done
	// A further segment makes another attempt after the discard was reported.
	appendAll(t, l, "c001")
	idle()
	r.mustStop(t)
	return s.snapshot()
}

func TestAPartialSettleThenTheAgeBoundRestartsTheNextSegmentFromItsFirstRecord(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := runAgeBound(t, nil)

		if len(calls) < 3 {
			t.Fatalf("calls = %v, want at least three", calls)
		}
		if want := []string{"a001", "a002", "a003"}; !reflect.DeepEqual(calls[0], want) {
			t.Errorf("first call = %v, want %v", calls[0], want)
		}
		if want := []string{"a002", "a003"}; !reflect.DeepEqual(calls[1], want) {
			t.Errorf("second call = %v, want %v", calls[1], want)
		}
		// The call before the final c001 delivered the new segment whole.
		if want := []string{"b001", "b002"}; !reflect.DeepEqual(calls[len(calls)-2], want) {
			t.Errorf("call before the last = %v, want %v", calls[len(calls)-2], want)
		}
	})
}

func TestTheDiscardWarningFollowsTheAgeBoundDroppingASegment(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &capture{}
		runAgeBound(t, slog.New(c))

		warnings := c.warnings()
		if len(warnings) != 1 {
			t.Fatalf("warnings = %d, want 1: %v", len(warnings), warnings)
		}
		w := warnings[0]
		if got := attrOf(w, "otel.event.name"); got.String() != "flowseer.edge.buffer.discarded" {
			t.Errorf("event name = %q, want flowseer.edge.buffer.discarded", got.String())
		}
		if got := attrOf(w, logAttrKey); got.String() != "ingest" {
			t.Errorf("%s = %q, want ingest", logAttrKey, got.String())
		}
		if got := attrOf(w, "flowseer.edge.buffer.discarded_records"); got.Int64() != 3 {
			t.Errorf("discarded records = %d, want 3", got.Int64())
		}
	})
}

func TestTheDurationHistogramNamesAnErrorAndAZeroSettle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := openLog(t, t.TempDir(), seglog.Config{})
		appendAll(t, l, "r001")
		s := &scripted{fn: func(call int, records []string) (int, error) {
			switch call {
			case 0:
				return 0, errors.New("central refuses")
			case 1:
				return 0, nil
			}
			return len(records), nil
		}}
		m := newMeters()
		cfg := baseConfig(l, s)
		cfg.Meter = m.meter

		r := start(cfg)
		defer r.stop()
		idle()
		r.mustStop(t)

		counts := map[string]uint64{}
		for _, dp := range histogramPoints(t, m.collect(t), "flowseer.edge.delivery.duration") {
			if got, _ := dp.Attributes.Value(logAttrKey); got.AsString() != "ingest" {
				t.Errorf("point has %s = %q, want ingest", logAttrKey, got.AsString())
			}
			et, _ := dp.Attributes.Value(attribute.Key(fsconv.ErrorTypeKey))
			counts[et.AsString()] += dp.Count
		}
		want := map[string]uint64{fsconv.ErrorTypeOther: 1, "nothing_settled": 1, "": 1}
		if !reflect.DeepEqual(counts, want) {
			t.Errorf("call counts by error.type = %v, want %v", counts, want)
		}
	})
}

func TestASettledCountAboveTheNumberSentIsAllSent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := &countingLog{Log: openLog(t, t.TempDir(), seglog.Config{})}
		appendAll(t, l.Log, "r001", "r002", "r003")
		s := &scripted{fn: func(_ int, records []string) (int, error) { return len(records) + 5, nil }}
		m := newMeters()
		cfg := baseConfig(l, s)
		cfg.Meter = m.meter

		r := start(cfg)
		defer r.stop()
		idle()
		r.mustStop(t)

		if got := len(s.snapshot()); got != 1 {
			t.Errorf("calls = %d, want 1", got)
		}
		if got := l.ackCount(); got != 1 {
			t.Errorf("Ack ran %d times, want 1", got)
		}
		if got := counterValue(t, m.collect(t), "flowseer.edge.delivery.settled"); got != 3 {
			t.Errorf("settled = %d, want 3", got)
		}
	})
}

func TestRunReturnsNilWhenTheLogCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := openLog(t, t.TempDir(), seglog.Config{})
		s := &scripted{fn: settleAll}

		r := start(baseConfig(l, s))
		defer r.stop()
		idle()
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		// Joined without canceling: Close alone must end the drain.
		r.await(t)
	})
}

func TestRunReturnsANextErrorOtherThanClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fault := errs.New().Code(seglog.ErrCodeIO).Msg("read sealed segment")
		l := &faultLog{nextErr: fault}
		s := &scripted{fn: settleAll}

		err := drain.Run(context.Background(), baseConfig(l, s))
		if !errors.Is(err, fault) {
			t.Errorf("Run() = %v, want %v", err, fault)
		}
	})
}

func TestRunReturnsAnAckError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fault := errs.New().Code(seglog.ErrCodeIO).Msg("acknowledge segment")
		l := &faultLog{segment: seglog.Segment{Sequence: 7, Records: [][]byte{[]byte("r001")}}, ackErr: fault}
		s := &scripted{fn: settleAll}

		err := drain.Run(context.Background(), baseConfig(l, s))
		if !errors.Is(err, fault) {
			t.Errorf("Run() = %v, want %v", err, fault)
		}
	})
}

func TestRunReturnsNilWhenAckFindsTheLogClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		closed := errs.New().Code(seglog.ErrCodeClosed).Msg("log is closed")
		l := &faultLog{segment: seglog.Segment{Sequence: 7, Records: [][]byte{[]byte("r001")}}, ackErr: closed}
		s := &scripted{fn: settleAll}

		if err := drain.Run(context.Background(), baseConfig(l, s)); err != nil {
			t.Errorf("Run() = %v, want nil when Ack reports a closed log", err)
		}
	})
}

func TestRunRefusesAConfigWithoutALogOrASend(t *testing.T) {
	if err := drain.Run(context.Background(), drain.Config{LogName: "ingest"}); err == nil {
		t.Error("Run() = nil, want an error for a Config without a Log and a Send")
	}
}
