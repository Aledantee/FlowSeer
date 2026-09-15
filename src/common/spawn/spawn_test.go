package spawn

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// recordSink collects the slog records a test's logger writes, so a test can
// assert on the log record the helper emits without depending on the
// handler's own formatting.
type recordSink struct {
	mu      sync.Mutex
	records []slog.Record
}

func (s *recordSink) append(record slog.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record.Clone())
}

func (s *recordSink) all() []slog.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]slog.Record(nil), s.records...)
}

type recordingHandler struct{ sink *recordSink }

func (h recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h recordingHandler) Handle(_ context.Context, record slog.Record) error {
	h.sink.append(record)
	return nil
}

func (h recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h recordingHandler) WithGroup(string) slog.Handler      { return h }

// withRecordingLogger installs a recording slog default logger for the
// duration of the test and returns its sink.
func withRecordingLogger(t *testing.T) *recordSink {
	t.Helper()
	sink := &recordSink{}
	previous := slog.Default()
	slog.SetDefault(slog.New(recordingHandler{sink: sink}))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return sink
}

func recordAttr(t *testing.T, record slog.Record, key string) (slog.Value, bool) {
	t.Helper()
	var (
		val   slog.Value
		found bool
	)
	record.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			val = a.Value
			found = true
			return false
		}
		return true
	})
	return val, found
}

// waitFor polls until cond reports true or the deadline passes, failing the
// test on timeout. It exists so a test can observe an asynchronous goroutine
// without a fixed sleep racing the scheduler.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition not met before deadline")
	}
}

// TestGoRecoversAndReportsPanic pins that a panicking fn costs its own
// goroutine and nothing else: the process survives, and the reported error
// carries the recovered value, the label, and a stack. Without the recover,
// a panic on a spawned goroutine takes the whole process down whatever the
// spawning frame does, so the assertions below never run at all.
func TestGoRecoversAndReportsPanic(t *testing.T) {
	withRecordingLogger(t)

	var (
		mu       sync.Mutex
		reported error
	)

	Go(context.Background(), "unit-under-test", func() {
		panic("boom")
	}, ReportTo(func(err error) {
		mu.Lock()
		defer mu.Unlock()
		reported = err
	}))

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return reported != nil
	})

	mu.Lock()
	err := reported
	mu.Unlock()

	if got, want := err.Error(), "unit-under-test panicked"; got != want {
		t.Errorf("got message %q, want %q", got, want)
	}

	attrs := errs.Attributes(err)
	if got, want := attrs[panicAttrKey], "boom"; got != want {
		t.Errorf("got panic attribute %v, want %v", got, want)
	}
}

// TestGoWrapsAnErrorPanicValue pins the error branch of the reported value:
// a panic carrying an error is wrapped, not stringified, so errors.Is and
// errors.As still reach the original error.
func TestGoWrapsAnErrorPanicValue(t *testing.T) {
	withRecordingLogger(t)

	sentinel := errors.New("device unreachable")
	var (
		mu       sync.Mutex
		reported error
	)

	Go(context.Background(), "unit-under-test", func() {
		panic(sentinel)
	}, ReportTo(func(err error) {
		mu.Lock()
		defer mu.Unlock()
		reported = err
	}))

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return reported != nil
	})

	mu.Lock()
	err := reported
	mu.Unlock()

	if !errors.Is(err, sentinel) {
		t.Errorf("errors.Is(%v, sentinel) = false, want true", err)
	}

	attrs := errs.Attributes(err)
	if got, ok := attrs[panicAttrKey].(error); !ok || got != sentinel {
		t.Errorf("got panic attribute %v, want the sentinel error", attrs[panicAttrKey])
	}
}

// TestGoAttachesAStack pins that the reported error carries a captured stack,
// which errs only omits for a sentinel built with Msg/Msgf rather than the
// builder Go uses.
func TestGoAttachesAStack(t *testing.T) {
	withRecordingLogger(t)

	var (
		mu       sync.Mutex
		reported error
	)

	Go(context.Background(), "unit-under-test", func() {
		panic("boom")
	}, ReportTo(func(err error) {
		mu.Lock()
		defer mu.Unlock()
		reported = err
	}))

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return reported != nil
	})

	mu.Lock()
	err := reported
	mu.Unlock()

	value, ok := logAttrValue(t, err)
	if !ok {
		t.Fatal("reported error does not implement slog.LogValuer")
	}

	var hasStack bool
	for _, a := range value.Group() {
		if a.Key == "stack" {
			hasStack = true
		}
	}
	if !hasStack {
		t.Errorf("got fields %v, want one keyed %q", value.Group(), "stack")
	}
}

func logAttrValue(t *testing.T, err error) (slog.Value, bool) {
	t.Helper()
	valuer, ok := err.(slog.LogValuer)
	if !ok {
		return slog.Value{}, false
	}
	return valuer.LogValue(), true
}

// TestReportToReceivesTheError pins that a site supplying ReportTo observes
// the panic through its own sink, so an existing failure path keeps working.
func TestReportToReceivesTheError(t *testing.T) {
	withRecordingLogger(t)

	p := &sinkPump{}
	Go(context.Background(), "x", func() {
		panic("boom")
	}, ReportTo(p.Fail))

	waitFor(t, func() bool { return p.Err() != nil })

	if p.Err() == nil {
		t.Fatal("p.Err() is nil, want the recovered panic")
	}
}

// TestGoRecoversAPanicRaisedInsideTheSink pins that a sink which panics costs
// the report rather than the process. Several call sites hand Go a sink that
// re-enters the component that just panicked, so a panic there is reachable
// and lands inside Go's own deferred recover, where a second panic would be
// unrecoverable. The surviving signal is a log record naming the sink.
func TestGoRecoversAPanicRaisedInsideTheSink(t *testing.T) {
	logs := withRecordingLogger(t)

	Go(context.Background(), "unit-under-test", func() {
		panic("boom")
	}, ReportTo(func(error) {
		panic("the sink itself failed")
	}))

	sawSink := func() bool {
		for _, record := range logs.all() {
			value, found := recordAttr(t, record, labelAttrKey)
			if found && value.String() == "unit-under-test sink" {
				return true
			}
		}
		return false
	}
	waitFor(t, sawSink)

	if !sawSink() {
		t.Fatalf("no log record named the panicking sink; got %d records", len(logs.all()))
	}
}

// sinkPump stands in for a call site's own error sink (e.g. pump.Pump.Fail),
// guarding its field with a mutex the way a real sink must.
type sinkPump struct {
	mu  sync.Mutex
	err error
}

func (p *sinkPump) Fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err == nil {
		p.err = err
	}
}

func (p *sinkPump) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// TestGoReportsWithNoSink pins the reporting floor: a site with no ReportTo
// still gets one error-level log record, per docs/conventions/observability.md's
// severity table (the owned operation failed and was abandoned). The sites
// with no sink are the fire-and-forget loops and watchdogs, where this record
// is the only signal a panic happened at all.
func TestGoReportsWithNoSink(t *testing.T) {
	sink := withRecordingLogger(t)

	Go(context.Background(), "watchdog", func() {
		panic("boom")
	})

	waitFor(t, func() bool { return len(sink.all()) > 0 })

	records := sink.all()
	if len(records) != 1 {
		t.Fatalf("got %d log records, want 1", len(records))
	}

	record := records[0]
	if record.Level != slog.LevelError {
		t.Errorf("got level %v, want %v", record.Level, slog.LevelError)
	}
	if record.Message != "goroutine panicked" {
		t.Errorf("got message %q, want %q", record.Message, "goroutine panicked")
	}

	label, ok := recordAttr(t, record, labelAttrKey)
	if !ok || label.String() != "watchdog" {
		t.Errorf("got label attribute %v (found=%v), want %q", label, ok, "watchdog")
	}
}

// TestGoAddsASpanEventWhenRecording pins the conditional half of reporting:
// a recording span in ctx gets one span event.
func TestGoAddsASpanEventWhenRecording(t *testing.T) {
	withRecordingLogger(t)

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer := provider.Tracer("spawn_test")

	ctx, span := tracer.Start(context.Background(), "parent")

	// done closes from ReportTo, which spawn.Go calls after report() has
	// already run, so the span event is guaranteed recorded by the time
	// this test reads it back.
	done := make(chan struct{})
	Go(ctx, "unit-under-test", func() {
		panic("boom")
	}, ReportTo(func(error) { close(done) }))
	<-done

	span.End()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("got %d ended spans, want 1", len(spans))
	}

	events := spans[0].Events()
	if len(events) != 1 {
		t.Fatalf("got %d span events, want 1", len(events))
	}
	if events[0].Name != spanEventName {
		t.Errorf("got event name %q, want %q", events[0].Name, spanEventName)
	}
}

// TestGoAddsNoSpanEventWhenNotRecording pins the other conditional branch:
// a context whose span is not recording gets no event.
//
// The span is a real SDK span that has already ended, which is what makes
// this an assertion about spawn rather than about OTel. A no-op span records
// nothing either way, so a test using context.Background() passes whether or
// not Go consults IsRecording; an ended SDK span is still inspectable through
// the recorder, so an unguarded AddEvent would show up here.
func TestGoAddsNoSpanEventWhenNotRecording(t *testing.T) {
	withRecordingLogger(t)

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ctx, span := provider.Tracer("spawn_test").Start(context.Background(), "ended")
	span.End()

	if span.IsRecording() {
		t.Fatal("span still recording after End; the test cannot prove the guard")
	}

	done := make(chan struct{})
	Go(ctx, "unit-under-test", func() {
		panic("boom")
	}, ReportTo(func(error) { close(done) }))
	<-done

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("got %d ended spans, want 1", len(ended))
	}
	if events := ended[0].Events(); len(events) != 0 {
		t.Errorf("got %d span events on an ended span, want 0", len(events))
	}
}

// TestGoDoesNotJoin pins that Go returns before fn completes, and that a call
// site's own WaitGroup — not the helper — governs the join.
//
// It is written so that a helper which joined would deadlock rather than fail
// an assertion: fn blocks on release, which only closes after started proves
// Go has already returned. A joining Go never reaches that line.
func TestGoDoesNotJoin(t *testing.T) {
	withRecordingLogger(t)

	var wg sync.WaitGroup
	release := make(chan struct{})
	started := make(chan struct{})

	wg.Add(1)
	Go(context.Background(), "unit-under-test", func() {
		defer wg.Done()
		close(started)
		<-release
	})

	<-started // fn is running, but Go must already have returned to reach here
	close(release)
	wg.Wait()
}
