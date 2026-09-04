package service

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// recordSink collects the records a service writes so a test can assert on the
// attributes the runtime attached to the attempt logger.
type recordSink struct {
	mu      sync.Mutex
	records []slog.Record
}

func (s *recordSink) append(record slog.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
}

func (s *recordSink) find(message string) (map[string]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.records {
		if record.Message != message {
			continue
		}
		attrs := make(map[string]string, record.NumAttrs())
		record.Attrs(func(attr slog.Attr) bool {
			attrs[attr.Key] = attr.Value.String()
			return true
		})
		return attrs, true
	}
	return nil, false
}

// recordingHandler keeps every handler derived through Logger.With writing into
// one sink, so that the attributes the runtime adds stay visible to the test.
type recordingHandler struct {
	sink  *recordSink
	attrs []slog.Attr
}

func (h recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h recordingHandler) Handle(_ context.Context, record slog.Record) error {
	record = record.Clone()
	record.AddAttrs(h.attrs...)
	h.sink.append(record)
	return nil
}

func (h recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return recordingHandler{sink: h.sink, attrs: append(slices.Clip(h.attrs), attrs...)}
}

// WithGroup flattens the group away. The sink is keyed on bare attribute names,
// so a test that needs to observe group nesting must use a real structured
// handler instead; TestTraceCorrelationNestsInsideAnOpenGroup does that.
func (h recordingHandler) WithGroup(string) slog.Handler { return h }

func newRecordingLogger() (*slog.Logger, *recordSink) {
	sink := &recordSink{}
	return slog.New(recordingHandler{sink: sink}), sink
}

// runUntil starts config, waits for ready, then cancels and returns run's error.
func runUntil(t *testing.T, config Config, ready <-chan struct{}) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, config) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("run() returned before the module was ready: %v", err)
	case <-ctx.Done():
		t.Fatalf("module never became ready: %v", ctx.Err())
	}
	cancel()
	return <-done
}

func TestAttemptLoggerCarriesServiceIdentity(t *testing.T) {
	logger, handler := newRecordingLogger()
	ready := make(chan struct{})
	config := Config{
		Identity: Identity{Name: "edge", Namespace: "flowseer", Version: "v1"},
		Logger:   logger,
		Modules: []Module{{Name: "ingest", Branch: &Branch{Children: []Module{{
			Name: "syslog",
			Leaf: &Leaf{Setup: func(context.Context) (Attempt, error) {
				return Attempt{Runner: func(ctx context.Context) error {
					Logger(ctx).InfoContext(ctx, "module ready")
					close(ready)
					<-ctx.Done()
					return ctx.Err()
				}}, nil
			}},
		}}}}},
	}

	if err := runUntil(t, config, ready); err != nil {
		t.Fatalf("run() error: %v", err)
	}
	attrs, ok := handler.find("module ready")
	if !ok {
		t.Fatal("the module record never reached the injected handler")
	}
	want := map[string]string{
		"service.name":      "edge",
		"service.namespace": "flowseer",
		"service.version":   "v1",
		modulePathKey:       "edge/ingest/syslog",
	}
	for key, value := range want {
		if attrs[key] != value {
			t.Errorf("attempt log %s = %q, want %q", key, attrs[key], value)
		}
	}
}

func TestAttemptLogsCarryTraceCorrelation(t *testing.T) {
	logger, handler := newRecordingLogger()
	recorder := tracetest.NewSpanRecorder()
	ready := make(chan struct{})
	var want trace.SpanContext
	config := Config{
		Identity:       Identity{Name: "edge", Namespace: "flowseer", Version: "v1"},
		Logger:         logger,
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)),
		Setup: func(context.Context) (Attempt, error) {
			return Attempt{Runner: func(ctx context.Context) error {
				spanCtx, span := Tracer(ctx).Start(ctx, "poll")
				want = span.SpanContext()
				Logger(spanCtx).InfoContext(spanCtx, "polling")
				span.End()
				close(ready)
				<-ctx.Done()
				return ctx.Err()
			}}, nil
		},
	}

	if err := runUntil(t, config, ready); err != nil {
		t.Fatalf("run() error: %v", err)
	}
	attrs, ok := handler.find("polling")
	if !ok {
		t.Fatal("the module record never reached the injected handler")
	}
	if attrs["trace_id"] != want.TraceID().String() || attrs["span_id"] != want.SpanID().String() {
		t.Errorf("log correlation = %s/%s, want %s/%s",
			attrs["trace_id"], attrs["span_id"], want.TraceID(), want.SpanID())
	}
	if _, ok := handler.find("module lifecycle"); !ok {
		t.Error("runtime lifecycle records did not reach the injected handler")
	}
}

func TestModuleInstrumentsUseAttemptProviders(t *testing.T) {
	const moduleScope = "go.aledante.io/FlowSeer/src/edge/ingest/syslog"

	recorder := tracetest.NewSpanRecorder()
	reader := sdkmetric.NewManualReader()
	ready := make(chan struct{})
	config := Config{
		Identity:       Identity{Name: "edge", Namespace: "flowseer", Version: "v1"},
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)),
		MeterProvider:  sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
		Modules: []Module{{
			Name: "syslog",
			Leaf: &Leaf{Setup: func(ctx context.Context) (Attempt, error) {
				counter, err := MeterProvider(ctx).Meter(moduleScope).Int64Counter("flowseer.syslog.messages")
				if err != nil {
					return Attempt{}, err
				}
				return Attempt{Runner: func(ctx context.Context) error {
					attributes := Attributes(ctx)
					counter.Add(ctx, 1, metric.WithAttributeSet(attributes))
					_, span := TracerProvider(ctx).Tracer(moduleScope).Start(ctx, "decode")
					span.SetAttributes(attributes.ToSlice()...)
					span.End()
					close(ready)
					<-ctx.Done()
					return ctx.Err()
				}}, nil
			}},
		}},
	}

	if err := runUntil(t, config, ready); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	span := findRecordedSpan(recorder.Ended(), "decode")
	if span == nil {
		t.Fatal("the module span was not recorded")
	}
	if got := span.InstrumentationScope().Name; got != moduleScope {
		t.Errorf("module span scope = %q, want %q", got, moduleScope)
	}
	if got, ok := spanAttribute(span, legacyModulePathKey); !ok || got != "edge/syslog" {
		t.Errorf("module span %s = %q, want %q", legacyModulePathKey, got, "edge/syslog")
	}

	sums := collectCounters(t, reader)
	moduleSum, ok := sums[moduleScope+"/flowseer.syslog.messages"]
	if !ok {
		t.Fatalf("the module counter was not collected, got %v", sums)
	}
	if got, ok := moduleSum.Attributes.Value(legacyModulePathKey); !ok || got.AsString() != "edge/syslog" {
		t.Errorf("module counter %s = %v, want %q", legacyModulePathKey, got, "edge/syslog")
	}
}

func TestLifecycleMetricsCarryModuleAndOutcome(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	ready := make(chan struct{})
	config := Config{
		Identity:      Identity{Name: "edge", Namespace: "flowseer", Version: "v1"},
		MeterProvider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
		Setup: func(context.Context) (Attempt, error) {
			return Attempt{Runner: func(ctx context.Context) error {
				close(ready)
				<-ctx.Done()
				return ctx.Err()
			}}, nil
		},
	}

	if err := runUntil(t, config, ready); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	sums := collectCounters(t, reader)
	point, ok := sums[instrumentationScope+"/flowseer.service.module.lifecycle.transitions"]
	if !ok {
		t.Fatalf("the lifecycle counter was not collected, got %v", sums)
	}
	want := map[attribute.Key]string{
		modulePathKey:             "edge",
		moduleLifecycleActionKey:  "start",
		moduleLifecycleOutcomeKey: "running",
	}
	for key, value := range want {
		got, ok := point.Attributes.Value(key)
		if !ok || got.AsString() != value {
			t.Errorf("lifecycle counter %s = %v, want %q", key, got, value)
		}
	}
}

func TestRuntimeLifecycleSpansAreFiniteCallerSiblings(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	callerCtx, callerSpan := provider.Tracer("caller").Start(context.Background(), "caller")
	ready := make(chan struct{})
	taskCanceled := make(chan struct{})
	releaseTask := make(chan struct{})
	config := Config{
		Identity:       Identity{Name: "edge", Namespace: "flowseer", Version: "v1"},
		TracerProvider: provider,
		Setup: func(ctx context.Context) (Attempt, error) {
			if err := Go(ctx, func(ctx context.Context) error {
				<-ctx.Done()
				close(taskCanceled)
				<-releaseTask
				return ctx.Err()
			}); err != nil {
				return Attempt{}, err
			}
			return Attempt{Runner: func(ctx context.Context) error {
				close(ready)
				<-ctx.Done()
				return ctx.Err()
			}}, nil
		},
	}

	runCtx, cancel := context.WithCancel(callerCtx)
	done := make(chan error, 1)
	go func() { done <- run(runCtx, config) }()
	<-ready
	cancel()
	<-taskCanceled
	for _, span := range recorder.Ended() {
		if span.Name() == "flowseer.service.module.attempt" || span.Name() == "flowseer.service.shutdown" {
			t.Errorf("%s ended before attempt-owned work joined", span.Name())
		}
	}
	close(releaseTask)
	if err := <-done; err != nil {
		t.Fatalf("run() error: %v", err)
	}

	wantNames := []string{
		"flowseer.service.startup",
		"flowseer.service.module.attempt",
		"flowseer.service.shutdown",
	}
	wantDimensions := map[string]map[string]string{
		"flowseer.service.startup":        {moduleLifecycleActionKey: "start", moduleLifecycleOutcomeKey: "running"},
		"flowseer.service.module.attempt": {moduleLifecycleActionKey: "start", moduleLifecycleOutcomeKey: "canceled"},
		"flowseer.service.shutdown":       {moduleLifecycleActionKey: "stop", moduleLifecycleOutcomeKey: "canceled"},
	}
	spans := recorder.Ended()
	for _, name := range wantNames {
		span := findRecordedSpan(spans, name)
		if span == nil {
			t.Errorf("runtime span %q was not recorded", name)
			continue
		}
		if got, want := span.Parent().SpanID(), callerSpan.SpanContext().SpanID(); got != want {
			t.Errorf("%s parent = %s, want caller %s", name, got, want)
		}
		if got, ok := spanAttribute(span, modulePathKey); !ok || got != "edge" {
			t.Errorf("%s module path = %q, %v, want edge", name, got, ok)
		}
		for key, want := range wantDimensions[name] {
			if got, ok := spanAttribute(span, key); !ok || got != want {
				t.Errorf("%s %s = %q, %v, want %q", name, key, got, ok, want)
			}
		}
	}
	callerSpan.End()
}

func TestTraceDisabledRootEmitsNoRuntimeLifecycleSpans(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	ready := make(chan struct{})
	config := Config{
		Identity:       testIdentity(),
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)),
		Telemetry: TelemetryConfig{Signals: TelemetryPolicy{
			Traces: TelemetryDisabled,
		}},
		Setup: func(context.Context) (Attempt, error) {
			return Attempt{Runner: func(ctx context.Context) error {
				close(ready)
				<-ctx.Done()
				return ctx.Err()
			}}, nil
		},
	}

	if err := runUntil(t, config, ready); err != nil {
		t.Fatalf("run() error: %v", err)
	}
	if spans := recorder.Ended(); len(spans) != 0 {
		t.Errorf("trace-disabled runtime ended %d spans, want none", len(spans))
	}
}

func TestModuleAttemptSpanRecordsTerminalOutcome(t *testing.T) {
	tests := []struct {
		name    string
		setup   SetupFunc
		policy  Policy
		outcome string
	}{
		{
			name: "normal",
			setup: func(context.Context) (Attempt, error) {
				return Attempt{Runner: func(context.Context) error { return nil }}, nil
			},
			outcome: "normal",
		},
		{
			name: "setup error",
			setup: func(context.Context) (Attempt, error) {
				return Attempt{}, context.DeadlineExceeded
			},
			policy:  Policy{Error: OutcomePolicy{Action: Stop}},
			outcome: "error",
		},
		{
			name: "setup panic",
			setup: func(context.Context) (Attempt, error) {
				panic("setup panic")
			},
			policy:  Policy{Panic: OutcomePolicy{Action: Stop}},
			outcome: "panic",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			err := run(context.Background(), Config{
				Identity:       testIdentity(),
				TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)),
				Modules: []Module{{
					Name:   "worker",
					Policy: tt.policy,
					Leaf:   &Leaf{Setup: tt.setup},
				}},
			})
			if err != nil {
				t.Fatalf("run() error: %v", err)
			}
			span := findRecordedSpan(recorder.Ended(), attemptSpanName)
			if span == nil {
				t.Fatal("module attempt span was not recorded")
			}
			if got, ok := spanAttribute(span, moduleLifecycleOutcomeKey); !ok || got != tt.outcome {
				t.Errorf("attempt outcome = %q, %v, want %q", got, ok, tt.outcome)
			}
		})
	}
}

func TestInstrumentationAccessorsAreSafeOutsideAnAttempt(t *testing.T) {
	ctx := context.Background()

	if TracerProvider(ctx) == nil || MeterProvider(ctx) == nil {
		t.Fatal("provider accessors returned a nil default")
	}
	if got := Attributes(ctx); got.Len() != 0 {
		t.Errorf("Attributes() outside an attempt = %v, want an empty set", got)
	}
	// The default providers must be usable without a service run.
	if _, err := MeterProvider(ctx).Meter("test").Int64Counter("test.counter"); err != nil {
		t.Errorf("default meter provider: %v", err)
	}
}

func findRecordedSpan(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}
	return nil
}

func spanAttribute(span sdktrace.ReadOnlySpan, key string) (string, bool) {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == key {
			return attr.Value.AsString(), true
		}
	}
	return "", false
}

// collectCounters returns one data point per collected counter, keyed by
// instrumentation scope and instrument name.
func collectCounters(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.DataPoint[int64] {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	points := make(map[string]metricdata.DataPoint[int64])
	for _, scope := range collected.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			sum, ok := instrument.Data.(metricdata.Sum[int64])
			if !ok || len(sum.DataPoints) == 0 {
				continue
			}
			points[scope.Scope.Name+"/"+instrument.Name] = sum.DataPoints[0]
		}
	}
	return points
}

// lockedBuffer serializes the writes of a real slog handler, which the runtime
// calls from the module goroutine and from its own lifecycle recording.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) records(t *testing.T, message string) (map[string]any, bool) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	for line := range strings.SplitSeq(strings.TrimSpace(b.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("handler wrote a non-JSON line %q: %v", line, err)
		}
		if record["msg"] == message {
			return record, true
		}
	}
	return nil, false
}

// TestTraceCorrelationNestsInsideAnOpenGroup pins the nesting the traceLogHandler
// doc comment promises: a module that opens a group before logging gets the two
// identifiers inside that group, because slog cannot add a record attribute
// above an open group. It uses a real structured handler because the
// recordingHandler used elsewhere in this file flattens groups away.
func TestTraceCorrelationNestsInsideAnOpenGroup(t *testing.T) {
	sink := &lockedBuffer{}
	recorder := tracetest.NewSpanRecorder()
	ready := make(chan struct{})
	var want trace.SpanContext
	config := Config{
		Identity:       Identity{Name: "edge", Namespace: "flowseer", Version: "v1"},
		Logger:         slog.New(slog.NewJSONHandler(sink, nil)),
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)),
		Setup: func(context.Context) (Attempt, error) {
			return Attempt{Runner: func(ctx context.Context) error {
				spanCtx, span := Tracer(ctx).Start(ctx, "poll")
				want = span.SpanContext()
				Logger(spanCtx).WithGroup("request").InfoContext(spanCtx, "polling")
				span.End()
				close(ready)
				<-ctx.Done()
				return ctx.Err()
			}}, nil
		},
	}

	if err := runUntil(t, config, ready); err != nil {
		t.Fatalf("run() error: %v", err)
	}
	record, ok := sink.records(t, "polling")
	if !ok {
		t.Fatal("the module record never reached the injected handler")
	}
	group, ok := record["request"].(map[string]any)
	if !ok {
		t.Fatalf("record has no \"request\" group: %v", record)
	}
	if group["trace_id"] != want.TraceID().String() || group["span_id"] != want.SpanID().String() {
		t.Errorf("grouped correlation = %v/%v, want %s/%s",
			group["trace_id"], group["span_id"], want.TraceID(), want.SpanID())
	}
	if _, nested := record["trace_id"]; nested {
		t.Error("trace_id also appeared at the top level of the record")
	}
}
