package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkinstrumentation "go.opentelemetry.io/otel/sdk/instrumentation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestTelemetryPolicySignalMaskMatrixIsComplete(t *testing.T) {
	policyType := reflect.TypeFor[TelemetryPolicy]()
	wantFields := []string{"Logs", "Metrics", "Traces"}
	if policyType.NumField() != len(wantFields) {
		t.Fatalf("TelemetryPolicy fields = %d, want %d; update the signal-mask matrix", policyType.NumField(), len(wantFields))
	}
	for i, want := range wantFields {
		if got := policyType.Field(i).Name; got != want {
			t.Fatalf("TelemetryPolicy field %d = %q, want %q", i, got, want)
		}
	}
	declarations := []TelemetryDeclaration{TelemetryInherit, TelemetryEnabled, TelemetryDisabled}
	for value, declaration := range declarations {
		if declaration != TelemetryDeclaration(value) {
			t.Fatalf("telemetry declaration %d = %d; update the declaration matrix", value, declaration)
		}
	}
	if err := validateTelemetryPolicy(
		TelemetryPolicy{Logs: TelemetryDeclaration(len(declarations))},
		"Telemetry.Signals",
	); err == nil {
		t.Fatal("the declaration immediately after the completeness inventory was accepted")
	}

	const signalMaskCount = 1 << 3
	seenRoot := make(map[int]bool, signalMaskCount)
	seenInheritedBranch := make(map[int]bool, signalMaskCount)
	seenOverridingLeaf := make(map[int]bool, signalMaskCount)
	available := resolvedTelemetryPolicy{logs: true, metrics: true, traces: true}
	for mask := range signalMaskCount {
		root, err := resolveTelemetryPolicy(
			telemetryPolicyFromMask(mask),
			"Telemetry.Signals",
			"FLOWSEER_EDGE_",
			mapLookup(nil),
			available,
			available,
		)
		if err != nil {
			t.Fatalf("resolve root mask %03b: %v", mask, err)
		}
		seenRoot[telemetryPolicyMask(root)] = true

		inherited, err := resolveTelemetryPolicies([]plannedModule{{
			path: "edge/branch",
			children: []plannedModule{{
				path: "edge/branch/leaf",
			}},
		}}, mapLookup(nil), root, available)
		if err != nil {
			t.Fatalf("resolve inherited branch mask %03b: %v", mask, err)
		}
		seenInheritedBranch[telemetryPolicyMask(inherited[0].telemetryPolicy)] = true
		if got := inherited[0].children[0].telemetryPolicy; got != root {
			t.Fatalf("inherited leaf mask = %03b, want root %03b", telemetryPolicyMask(got), mask)
		}

		opposite := resolvedTelemetryPolicyFromMask(mask ^ (signalMaskCount - 1))
		overridden, err := resolveTelemetryPolicies([]plannedModule{{
			path: "edge/branch",
			children: []plannedModule{{
				path:                 "edge/branch/leaf",
				telemetryDeclaration: telemetryPolicyFromMask(mask),
			}},
		}}, mapLookup(nil), opposite, available)
		if err != nil {
			t.Fatalf("resolve overriding leaf mask %03b: %v", mask, err)
		}
		leaf := overridden[0].children[0].telemetryPolicy
		seenOverridingLeaf[telemetryPolicyMask(leaf)] = true
		if leaf != root {
			t.Fatalf("overriding leaf mask = %03b, want %03b", telemetryPolicyMask(leaf), mask)
		}
	}
	for level, seen := range map[string]map[int]bool{
		"root":             seenRoot,
		"inherited branch": seenInheritedBranch,
		"overriding leaf":  seenOverridingLeaf,
	} {
		if len(seen) != signalMaskCount {
			t.Errorf("%s masks = %v, want all %d combinations", level, seen, signalMaskCount)
		}
	}
}

func TestTelemetryOwnershipMatrixIsComplete(t *testing.T) {
	const injectionMaskCount = 1 << 3
	seen := make(map[string]bool, 2*injectionMaskCount)
	for endpoint := range 2 {
		for injections := range injectionMaskCount {
			name := fmt.Sprintf("endpoint=%t/injections=%03b", endpoint == 1, injections)
			t.Run(name, func(t *testing.T) {
				seen[name] = true
				state := newTelemetryOwnershipState()
				config := Config{Identity: testIdentity(), Setup: testSetup()}
				if endpoint == 1 {
					config.Telemetry.Endpoint = "https://collector.example"
				}
				if injections&1 != 0 {
					config.LogHandler = slog.DiscardHandler
				}
				if injections&2 != 0 {
					config.MeterProvider = state.injectedMeter
				}
				if injections&4 != 0 {
					config.TracerProvider = state.injectedTracer
				}
				if injections != 0 {
					config.TelemetryShutdown = func(context.Context) error {
						state.shutdowns["injected"]++
						return nil
					}
				}

				normalized, err := normalizeTelemetryConfig(config, "FLOWSEER_EDGE", mapLookup(nil))
				if err != nil {
					t.Fatalf("normalizeTelemetryConfig() error: %v", err)
				}
				owner, err := newRunTelemetry(context.Background(), config.Identity, normalized, state.factories())
				if err != nil {
					t.Fatalf("newRunTelemetry() error: %v", err)
				}

				wantManaged := [3]bool{
					endpoint == 1 && injections&1 == 0,
					endpoint == 1 && injections&2 == 0,
					endpoint == 1 && injections&4 == 0,
				}
				managedCount := 0
				for _, managed := range wantManaged {
					if managed {
						managedCount++
					}
				}
				wantShared := 0
				if managedCount > 0 {
					wantShared = 1
				}
				if state.resourceCalls != wantShared || state.transportCalls != wantShared {
					t.Errorf("shared resource/transport calls = %d/%d, want %d/%d", state.resourceCalls, state.transportCalls, wantShared, wantShared)
				}
				wantFactoryCalls := map[string]int{
					"logs": boolInt(wantManaged[0]), "metrics": boolInt(wantManaged[1]), "traces": boolInt(wantManaged[2]),
				}
				if !reflect.DeepEqual(state.factoryCalls, wantFactoryCalls) {
					t.Errorf("signal factory calls = %v, want %v", state.factoryCalls, wantFactoryCalls)
				}
				for i, got := range state.resources {
					if got != state.resource {
						t.Errorf("signal resource %d = %p, want shared %p", i, got, state.resource)
					}
				}
				for i, got := range state.transports {
					if got != state.transport {
						t.Errorf("signal transport %d = %p, want shared %p", i, got, state.transport)
					}
				}

				view := owner.view(normalized.rootPolicy)
				if wantManaged[1] {
					if _, exposesShutdown := view.meterProvider.(interface{ Shutdown(context.Context) error }); exposesShutdown {
						t.Error("managed meter facade exposes Shutdown")
					}
					if _, exposesFlush := view.meterProvider.(interface{ ForceFlush(context.Context) error }); exposesFlush {
						t.Error("managed meter facade exposes ForceFlush")
					}
				} else if injections&2 != 0 && view.meterProvider != state.injectedMeter {
					t.Error("injected meter provider identity was not preserved")
				}
				if wantManaged[2] {
					if _, exposesShutdown := view.tracerProvider.(interface{ Shutdown(context.Context) error }); exposesShutdown {
						t.Error("managed tracer facade exposes Shutdown")
					}
					if _, exposesFlush := view.tracerProvider.(interface{ ForceFlush(context.Context) error }); exposesFlush {
						t.Error("managed tracer facade exposes ForceFlush")
					}
				} else if injections&4 != 0 && view.tracerProvider != state.injectedTracer {
					t.Error("injected tracer provider identity was not preserved")
				}

				if err := owner.shutdown(context.Background()); err != nil {
					t.Fatalf("telemetry shutdown: %v", err)
				}
				if err := owner.shutdown(context.Background()); err != nil {
					t.Fatalf("second telemetry shutdown: %v", err)
				}
				wantShutdowns := map[string]int{
					"transport": wantShared,
					"logs":      boolInt(wantManaged[0]),
					"metrics":   boolInt(wantManaged[1]),
					"traces":    boolInt(wantManaged[2]),
					"injected":  boolInt(injections != 0),
				}
				if !reflect.DeepEqual(state.shutdowns, wantShutdowns) {
					t.Errorf("shutdown calls = %v, want %v", state.shutdowns, wantShutdowns)
				}
				if state.injectedMeter.shutdowns != 0 || state.injectedTracer.shutdowns != 0 {
					t.Errorf("borrowed provider shutdowns = %d/%d, want 0/0", state.injectedMeter.shutdowns, state.injectedTracer.shutdowns)
				}
			})
		}
	}
	if len(seen) != 2*injectionMaskCount {
		t.Fatalf("ownership tuples = %d, want %d", len(seen), 2*injectionMaskCount)
	}
}

func TestDisabledTelemetryViewRetainsBackingOwnership(t *testing.T) {
	state := newTelemetryOwnershipState()
	normalized, err := normalizeTelemetryConfig(Config{
		Identity:  testIdentity(),
		Setup:     testSetup(),
		Telemetry: TelemetryConfig{Endpoint: "https://collector.example"},
	}, "FLOWSEER_EDGE", mapLookup(nil))
	if err != nil {
		t.Fatalf("normalizeTelemetryConfig() error: %v", err)
	}
	owner, err := newRunTelemetry(context.Background(), testIdentity(), normalized, state.factories())
	if err != nil {
		t.Fatalf("newRunTelemetry() error: %v", err)
	}
	view := owner.view(resolvedTelemetryPolicy{})
	if got := owner.availableSignals(); got != (resolvedTelemetryPolicy{logs: true, metrics: true, traces: true}) {
		t.Fatalf("available backing signals = %+v, want all retained", got)
	}
	if view.logger != owner.localLogger || view.meterProvider == state.managedMeter || view.tracerProvider == state.managedTracer {
		t.Fatal("disabled view exposed managed signal capabilities")
	}
	for phase, calls := range state.shutdowns {
		if calls != 0 {
			t.Fatalf("%s shutdown calls before owner shutdown = %d, want 0", phase, calls)
		}
	}
	if err := owner.shutdown(context.Background()); err != nil {
		t.Fatalf("owner shutdown: %v", err)
	}
}

func TestTelemetryOwnerViewsKeepDestinationsIsolated(t *testing.T) {
	wantTracer := otel.GetTracerProvider()
	wantMeter := otel.GetMeterProvider()
	wantPropagator := otel.GetTextMapPropagator()
	wantLogger := slog.Default()

	for mask := range 1 << 3 {
		mask := mask
		t.Run(fmt.Sprintf("signals=%03b", mask), func(t *testing.T) {
			localLogger, localSink := newRecordingLogger()
			exportLogger, exportSink := newRecordingLogger()
			state := newTelemetryOwnershipState()
			state.logHandler = exportLogger.Handler()
			identity := testIdentity()
			identity.Name = fmt.Sprintf("service-%03b", mask)
			owner, err := newRunTelemetry(context.Background(), identity, normalizedTelemetryConfig{
				localLogger: localLogger,
				logs:        normalizedLogSignal{backing: signalManaged},
				metrics:     normalizedMetricSignal{backing: signalManaged},
				traces:      normalizedTraceSignal{backing: signalManaged},
			}, state.factories())
			if err != nil {
				t.Fatalf("newRunTelemetry() error: %v", err)
			}

			view := owner.view(resolvedTelemetryPolicyFromMask(mask))
			view.logger.Info("owner probe")
			if _, ok := localSink.find("owner probe"); !ok {
				t.Fatal("local destination did not receive owner record")
			}
			_, exported := exportSink.find("owner probe")
			if exported != (mask&1 != 0) {
				t.Fatalf("managed log export = %t, want %t", exported, mask&1 != 0)
			}
			if got := view.meterProvider != defaultMeterProvider; got != (mask&2 != 0) {
				t.Fatalf("managed meter capability = %t, want %t", got, mask&2 != 0)
			}
			if got := view.tracerProvider != defaultTracerProvider; got != (mask&4 != 0) {
				t.Fatalf("managed tracer capability = %t, want %t", got, mask&4 != 0)
			}
			if owner.meterProvider != state.managedMeter || owner.tracerProvider != state.managedTracer {
				t.Fatal("owner lost its isolated managed providers")
			}
			if len(state.identities) != 1 || state.identities[0] != identity {
				t.Fatalf("resource identities = %+v, want only %+v", state.identities, identity)
			}
			if err := owner.shutdown(context.Background()); err != nil {
				t.Fatalf("owner shutdown: %v", err)
			}
			if otel.GetTracerProvider() != wantTracer || otel.GetMeterProvider() != wantMeter || otel.GetTextMapPropagator() != wantPropagator || slog.Default() != wantLogger {
				t.Fatal("telemetry owner changed process globals")
			}
		})
	}
}

func TestConcurrentServiceTelemetryRunsRemainIsolated(t *testing.T) {
	wantTracer := otel.GetTracerProvider()
	wantMeter := otel.GetMeterProvider()
	wantPropagator := otel.GetTextMapPropagator()
	wantLogger := slog.Default()
	started := make(chan string, 2)
	type serviceRun struct {
		name     string
		endpoint string
		policy   TelemetryPolicy
		state    *telemetryOwnershipState
		export   *recordSink
		cancel   context.CancelFunc
	}
	runs := []serviceRun{
		{name: "edge_a", endpoint: "https://a.example", policy: telemetryPolicyFromMask(1)},
		{name: "edge_b", endpoint: "https://b.example", policy: telemetryPolicyFromMask(6)},
	}
	type runResult struct {
		name string
		err  error
	}
	results := make(chan runResult, len(runs))
	for i := range runs {
		run := &runs[i]
		run.state = newTelemetryOwnershipState()
		run.state.resource = resource.NewSchemaless(attribute.String("flowseer.test.owner", run.name))
		exportLogger, exportSink := newRecordingLogger()
		run.state.logHandler = exportLogger.Handler()
		run.export = exportSink
		ctx, cancel := context.WithCancel(context.Background())
		run.cancel = cancel
		t.Cleanup(cancel)
		identity := testIdentity()
		identity.Name = run.name
		config := Config{
			Identity: identity,
			Logger:   slog.New(slog.DiscardHandler),
			Telemetry: TelemetryConfig{
				Endpoint: run.endpoint,
				Signals:  run.policy,
			},
			Setup: func(context.Context) (Attempt, error) {
				return Attempt{Runner: func(ctx context.Context) error {
					Logger(ctx).Info("service probe", slog.String("flowseer.test.owner", identity.Name))
					started <- identity.Name
					<-ctx.Done()
					return nil
				}}, nil
			},
		}
		go func(ctx context.Context, config Config, name string, state *telemetryOwnershipState) {
			results <- runResult{name: name, err: runWithOptionsAndTelemetryFactories(
				ctx, config, supervisorOptions{lookup: mapLookup(nil)}, state.factories(),
			)}
		}(ctx, config, run.name, run.state)
	}

	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	seenStarted := make(map[string]bool, len(runs))
	for len(seenStarted) < len(runs) {
		select {
		case name := <-started:
			seenStarted[name] = true
		case result := <-results:
			t.Fatalf("service %s exited before start: %v", result.name, result.err)
		case <-timeout.C:
			t.Fatalf("concurrent services started = %v, want both", seenStarted)
		}
	}
	for i := range runs {
		runs[i].cancel()
	}
	for range runs {
		result := <-results
		if result.err != nil {
			t.Errorf("service %s run error: %v", result.name, result.err)
		}
	}

	if attrs, ok := runs[0].export.find("service probe"); !ok || attrs["flowseer.test.owner"] != runs[0].name {
		t.Fatalf("service A export = %v, %t; want only its probe", attrs, ok)
	}
	if _, ok := runs[1].export.find("service probe"); ok {
		t.Fatal("logs-disabled service exported its probe")
	}
	for i := range runs {
		run := &runs[i]
		if len(run.state.identities) != 1 || run.state.identities[0].Name != run.name {
			t.Errorf("service %s resource identities = %+v", run.name, run.state.identities)
		}
		if len(run.state.connections) != 1 || run.state.connections[0].endpoint.String() != run.endpoint {
			t.Errorf("service %s transport destinations = %+v, want %s", run.name, run.state.connections, run.endpoint)
		}
	}
	if runs[0].state.resource == runs[1].state.resource || runs[0].state.transport == runs[1].state.transport {
		t.Fatal("concurrent services shared managed telemetry backing")
	}
	if otel.GetTracerProvider() != wantTracer || otel.GetMeterProvider() != wantMeter || otel.GetTextMapPropagator() != wantPropagator || slog.Default() != wantLogger {
		t.Fatal("concurrent service runs changed process globals")
	}
}

func TestTelemetryRetryExhaustionIsDeterministic(t *testing.T) {
	attempts := 0
	waits := 0
	err := retryOTLPWithPolicy(context.Background(), time.Second, telemetryRetryPolicy{
		limit:       time.Hour,
		initialWait: time.Millisecond,
		maximumWait: 4 * time.Millisecond,
		wait: func(context.Context, time.Duration) error {
			waits++
			if waits == 3 {
				return context.DeadlineExceeded
			}
			return nil
		},
	}, func(context.Context) (time.Duration, bool, error) {
		attempts++
		return 0, true, errors.New("collector unavailable")
	})
	if err == nil || err.Error() != "telemetry retry deadline exceeded" {
		t.Fatalf("retryOTLPWithPolicy() error = %v, want retry deadline", err)
	}
	if attempts != 3 || waits != 3 {
		t.Fatalf("attempts/waits = %d/%d, want 3/3", attempts, waits)
	}
}

func TestManagedOTLPRequestsRemoveSecretSentinels(t *testing.T) {
	const secret = telemetryMatrixSecret

	logCapture := &telemetryRequestCapture{}
	logProvider := sdklog.NewLoggerProvider(
		sdklog.WithResource(resource.NewSchemaless(
			attribute.String("authorization", secret),
			attribute.String("flowseer.safe", secret),
		)),
		sdklog.WithProcessor(sdklog.NewSimpleProcessor(&managedLogExporter{
			transport:   logCapture,
			diagnostics: newTelemetryDiagnostics(io.Discard),
		})),
	)
	logger := logProvider.Logger(
		secret,
		otellog.WithInstrumentationVersion(secret),
		otellog.WithInstrumentationAttributes(
			attribute.String("credential", secret),
			attribute.String("flowseer.safe", secret),
		),
	)
	var record otellog.Record
	record.SetBody(attribute.StringValue(secret))
	record.SetEventName(secret)
	record.SetSeverityText(secret)
	record.AddAttributes(
		attribute.String("authorization", secret),
		attribute.String("flowseer.safe", secret),
	)
	logger.Emit(context.Background(), record)
	if err := logProvider.Shutdown(context.Background()); err != nil {
		t.Fatalf("log provider shutdown: %v", err)
	}
	if logCapture.logs == nil {
		t.Fatal("managed log exporter captured no request")
	}

	metrics, err := transformResourceMetrics(&metricdata.ResourceMetrics{
		Resource: resource.NewSchemaless(
			attribute.String("authorization", secret),
			attribute.String("flowseer.safe", secret),
		),
		ScopeMetrics: []metricdata.ScopeMetrics{{
			Scope: sdkinstrumentation.Scope{
				Name:       secret,
				Version:    secret,
				Attributes: attribute.NewSet(attribute.String("flowseer.safe", secret)),
			},
			Metrics: []metricdata.Metrics{{
				Name: secret, Description: secret, Unit: "1",
				Data: metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{
					Attributes: attribute.NewSet(
						attribute.String("password", secret),
						attribute.String("flowseer.safe", secret),
					),
					Value: 1,
					Exemplars: []metricdata.Exemplar[int64]{{
						FilteredAttributes: []attribute.KeyValue{
							attribute.String("carrier", secret),
							attribute.String("flowseer.safe", secret),
						},
						Value: 1,
					}},
				}}},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("transformResourceMetrics() error: %v", err)
	}

	resourceSpans := &tracepb.ResourceSpans{
		Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
			telemetryMatrixStringAttribute("credential"),
			telemetryMatrixStringAttribute("flowseer.safe"),
		}},
		ScopeSpans: []*tracepb.ScopeSpans{{
			Scope: &commonpb.InstrumentationScope{
				Name:       secret,
				Version:    secret,
				Attributes: []*commonpb.KeyValue{telemetryMatrixStringAttribute("flowseer.safe")},
			},
			Spans: []*tracepb.Span{{
				Name: secret,
				Attributes: []*commonpb.KeyValue{
					telemetryMatrixStringAttribute("payload"),
					telemetryMatrixStringAttribute("flowseer.safe"),
				},
				Events: []*tracepb.Span_Event{{
					Name:       secret,
					Attributes: []*commonpb.KeyValue{telemetryMatrixStringAttribute("flowseer.safe")},
				}},
				Links: []*tracepb.Span_Link{{
					Attributes: []*commonpb.KeyValue{
						telemetryMatrixStringAttribute("traceparent"),
						telemetryMatrixStringAttribute("flowseer.safe"),
					},
				}},
				Status: &tracepb.Status{Message: secret},
			}},
		}},
	}
	sanitizeResourceSpans([]*tracepb.ResourceSpans{resourceSpans})
	traces := &collectortracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{resourceSpans}}
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "credential="+secret)
	ambientResource, err := newTelemetryResource(testIdentity())
	if err != nil {
		t.Fatalf("newTelemetryResource() error: %v", err)
	}
	ambient, err := transformResourceMetrics(&metricdata.ResourceMetrics{Resource: ambientResource})
	if err != nil {
		t.Fatalf("transform ambient resource: %v", err)
	}

	for signal, request := range map[string]proto.Message{
		"logs": logCapture.logs, "metrics": metrics, "traces": traces, "ambient resource": ambient,
	} {
		t.Run(signal, func(t *testing.T) {
			encoded, marshalErr := protojson.Marshal(request)
			if marshalErr != nil {
				t.Fatalf("marshal managed OTLP request: %v", marshalErr)
			}
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("managed OTLP request retained secret sentinel: %s", encoded)
			}
		})
	}

	injectedLogger, injectedSink := newRecordingLogger()
	injected, err := newTelemetry(Config{
		Logger:     slog.New(slog.DiscardHandler),
		LogHandler: injectedLogger.Handler(),
	})
	if err != nil {
		t.Fatalf("newTelemetry() with injected handler: %v", err)
	}
	injected.logger.Info(secret)
	if _, ok := injectedSink.find(secret); !ok {
		t.Fatal("injected handler did not retain caller-owned data")
	}
}

const telemetryMatrixSecret = "do-not-leak-managed-sentinel"

func telemetryMatrixStringAttribute(key string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{
		Value: &commonpb.AnyValue_StringValue{StringValue: telemetryMatrixSecret},
	}}
}

func telemetryPolicyFromMask(mask int) TelemetryPolicy {
	declaration := func(bit int) TelemetryDeclaration {
		if mask&bit != 0 {
			return TelemetryEnabled
		}
		return TelemetryDisabled
	}
	return TelemetryPolicy{
		Logs: declaration(1), Metrics: declaration(2), Traces: declaration(4),
	}
}

func resolvedTelemetryPolicyFromMask(mask int) resolvedTelemetryPolicy {
	return resolvedTelemetryPolicy{logs: mask&1 != 0, metrics: mask&2 != 0, traces: mask&4 != 0}
}

func telemetryPolicyMask(policy resolvedTelemetryPolicy) int {
	mask := 0
	if policy.logs {
		mask |= 1
	}
	if policy.metrics {
		mask |= 2
	}
	if policy.traces {
		mask |= 4
	}
	return mask
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type telemetryOwnershipState struct {
	resource       *resource.Resource
	transport      *telemetryMatrixTransport
	injectedMeter  *telemetryMatrixMeterProvider
	injectedTracer *telemetryMatrixTracerProvider
	managedMeter   *telemetryMatrixMeterProvider
	managedTracer  *telemetryMatrixTracerProvider
	resourceCalls  int
	transportCalls int
	factoryCalls   map[string]int
	shutdowns      map[string]int
	resources      []*resource.Resource
	transports     []otlpTransport
	identities     []Identity
	connections    []normalizedOTLPConnection
	logHandler     slog.Handler
}

func newTelemetryOwnershipState() *telemetryOwnershipState {
	return &telemetryOwnershipState{
		resource:       resource.Empty(),
		transport:      &telemetryMatrixTransport{},
		injectedMeter:  &telemetryMatrixMeterProvider{MeterProvider: metricnoop.NewMeterProvider()},
		injectedTracer: &telemetryMatrixTracerProvider{TracerProvider: tracenoop.NewTracerProvider()},
		managedMeter:   &telemetryMatrixMeterProvider{MeterProvider: metricnoop.NewMeterProvider()},
		managedTracer:  &telemetryMatrixTracerProvider{TracerProvider: tracenoop.NewTracerProvider()},
		logHandler:     slog.DiscardHandler,
		factoryCalls:   map[string]int{"logs": 0, "metrics": 0, "traces": 0},
		shutdowns:      map[string]int{"transport": 0, "logs": 0, "metrics": 0, "traces": 0, "injected": 0},
	}
}

func (s *telemetryOwnershipState) factories() telemetryFactorySet {
	recordSignal := func(signal string, res *resource.Resource, transport otlpTransport) telemetryShutdown {
		s.factoryCalls[signal]++
		s.resources = append(s.resources, res)
		s.transports = append(s.transports, transport)
		return func(context.Context) error {
			s.shutdowns[signal]++
			return nil
		}
	}
	return telemetryFactorySet{
		newResource: func(identity Identity) (*resource.Resource, error) {
			s.identities = append(s.identities, identity)
			s.resourceCalls++
			return s.resource, nil
		},
		newTransport: func(_ context.Context, connection normalizedOTLPConnection) (otlpTransport, telemetryShutdown, error) {
			s.connections = append(s.connections, connection)
			s.transportCalls++
			return s.transport, func(context.Context) error {
				s.shutdowns["transport"]++
				return nil
			}, nil
		},
		newLogs: func(res *resource.Resource, transport otlpTransport, _ *telemetryDiagnostics, _ normalizedOTLPConnection) (slog.Handler, telemetryShutdown, error) {
			return s.logHandler, recordSignal("logs", res, transport), nil
		},
		newMetrics: func(res *resource.Resource, transport otlpTransport, _ *telemetryDiagnostics, _ normalizedOTLPConnection) (metric.MeterProvider, telemetryShutdown, error) {
			return s.managedMeter, recordSignal("metrics", res, transport), nil
		},
		newTraces: func(res *resource.Resource, transport otlpTransport, _ *telemetryDiagnostics, _ normalizedOTLPConnection) (trace.TracerProvider, telemetryShutdown, error) {
			return s.managedTracer, recordSignal("traces", res, transport), nil
		},
	}
}

type telemetryMatrixTransport [1]byte

func (*telemetryMatrixTransport) uploadLogs(context.Context, *collectorlogspb.ExportLogsServiceRequest) error {
	return nil
}

func (*telemetryMatrixTransport) uploadMetrics(context.Context, *collectormetricspb.ExportMetricsServiceRequest) error {
	return nil
}

func (*telemetryMatrixTransport) uploadTraces(context.Context, []*tracepb.ResourceSpans) error {
	return nil
}

type telemetryRequestCapture struct {
	logs *collectorlogspb.ExportLogsServiceRequest
}

func (c *telemetryRequestCapture) uploadLogs(_ context.Context, request *collectorlogspb.ExportLogsServiceRequest) error {
	c.logs = request
	return nil
}

func (*telemetryRequestCapture) uploadMetrics(context.Context, *collectormetricspb.ExportMetricsServiceRequest) error {
	return nil
}

func (*telemetryRequestCapture) uploadTraces(context.Context, []*tracepb.ResourceSpans) error {
	return nil
}

type telemetryMatrixMeterProvider struct {
	metric.MeterProvider
	shutdowns int
}

func (p *telemetryMatrixMeterProvider) Shutdown(context.Context) error {
	p.shutdowns++
	return nil
}

func (*telemetryMatrixMeterProvider) ForceFlush(context.Context) error { return nil }

type telemetryMatrixTracerProvider struct {
	trace.TracerProvider
	shutdowns int
}

func (p *telemetryMatrixTracerProvider) Shutdown(context.Context) error {
	p.shutdowns++
	return nil
}

func (*telemetryMatrixTracerProvider) ForceFlush(context.Context) error { return nil }
