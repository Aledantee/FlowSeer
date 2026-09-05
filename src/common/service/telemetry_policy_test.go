package service

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestTelemetryPolicyResolutionMatrixIsComplete(t *testing.T) {
	type signalCase struct {
		name      string
		set       func(*TelemetryPolicy, TelemetryDeclaration)
		read      func(resolvedTelemetryPolicy) bool
		rootEnv   string
		branchEnv string
		leafEnv   string
	}
	signals := []signalCase{
		{name: "logs", set: func(p *TelemetryPolicy, d TelemetryDeclaration) { p.Logs = d }, read: func(p resolvedTelemetryPolicy) bool { return p.logs }, rootEnv: "FLOWSEER_EDGE_TELEMETRY_LOGS_ENABLED", branchEnv: "FLOWSEER_EDGE_BRANCH_TELEMETRY_LOGS_ENABLED", leafEnv: "FLOWSEER_EDGE_BRANCH_LEAF_TELEMETRY_LOGS_ENABLED"},
		{name: "metrics", set: func(p *TelemetryPolicy, d TelemetryDeclaration) { p.Metrics = d }, read: func(p resolvedTelemetryPolicy) bool { return p.metrics }, rootEnv: "FLOWSEER_EDGE_TELEMETRY_METRICS_ENABLED", branchEnv: "FLOWSEER_EDGE_BRANCH_TELEMETRY_METRICS_ENABLED", leafEnv: "FLOWSEER_EDGE_BRANCH_LEAF_TELEMETRY_METRICS_ENABLED"},
		{name: "traces", set: func(p *TelemetryPolicy, d TelemetryDeclaration) { p.Traces = d }, read: func(p resolvedTelemetryPolicy) bool { return p.traces }, rootEnv: "FLOWSEER_EDGE_TELEMETRY_TRACES_ENABLED", branchEnv: "FLOWSEER_EDGE_BRANCH_TELEMETRY_TRACES_ENABLED", leafEnv: "FLOWSEER_EDGE_BRANCH_LEAF_TELEMETRY_TRACES_ENABLED"},
	}
	declarations := []TelemetryDeclaration{TelemetryInherit, TelemetryEnabled, TelemetryDisabled}
	environment := []string{"", "true", "false"}

	combinations := 0
	coveredSignals := make(map[string]bool)
	coveredDeclarations := make(map[TelemetryDeclaration]bool)
	coveredDepths := make(map[string]bool)
	coveredEnvironment := make(map[string]bool)
	coveredAvailability := make(map[bool]bool)
	for _, signal := range signals {
		for _, available := range []bool{false, true} {
			for _, rootDeclaration := range declarations {
				for _, branchDeclaration := range declarations {
					for _, leafDeclaration := range declarations {
						for _, rootEnvironment := range environment {
							for _, branchEnvironment := range environment {
								for _, leafEnvironment := range environment {
									combinations++
									coveredSignals[signal.name] = true
									coveredDeclarations[rootDeclaration] = true
									coveredDeclarations[branchDeclaration] = true
									coveredDeclarations[leafDeclaration] = true
									coveredDepths["root"] = true
									coveredDepths["branch"] = true
									coveredDepths["leaf"] = true
									coveredEnvironment[rootEnvironment] = true
									coveredEnvironment[branchEnvironment] = true
									coveredEnvironment[leafEnvironment] = true
									coveredAvailability[available] = true

									var rootPolicy, branchPolicy, leafPolicy TelemetryPolicy
									signal.set(&rootPolicy, rootDeclaration)
									signal.set(&branchPolicy, branchDeclaration)
									signal.set(&leafPolicy, leafDeclaration)
									env := make(map[string]string)
									for key, value := range map[string]string{signal.rootEnv: rootEnvironment, signal.branchEnv: branchEnvironment, signal.leafEnv: leafEnvironment} {
										if value != "" {
											env[key] = value
										}
									}

									availablePolicy := resolvedTelemetryPolicy{}
									setResolvedSignal(&availablePolicy, signal.name, available)
									root, rootErr := resolveTelemetryPolicy(rootPolicy, "Telemetry.Signals", "FLOWSEER_EDGE_TELEMETRY_", mapLookup(env), availablePolicy, availablePolicy)
									wantRoot, wantRootErr := expectedResolvedSignal(rootDeclaration, rootEnvironment, available, available)
									if (rootErr != nil) != wantRootErr {
										t.Fatalf("%s root declaration=%d env=%q available=%t error=%v, want error=%t", signal.name, rootDeclaration, rootEnvironment, available, rootErr, wantRootErr)
									}
									if rootErr != nil {
										continue
									}
									if got := signal.read(root); got != wantRoot {
										t.Fatalf("%s root = %t, want %t", signal.name, got, wantRoot)
									}

									modules := []plannedModule{{
										path: "edge/branch", envKey: "FLOWSEER_EDGE_BRANCH_ENABLED", telemetryDeclaration: branchPolicy,
										children: []plannedModule{{path: "edge/branch/leaf", envKey: "FLOWSEER_EDGE_BRANCH_LEAF_ENABLED", telemetryDeclaration: leafPolicy}},
									}}
									resolved, err := resolveTelemetryPolicies(modules, mapLookup(env), root, availablePolicy)
									wantBranch, wantBranchErr := expectedResolvedSignal(branchDeclaration, branchEnvironment, wantRoot, available)
									wantLeaf, wantLeafErr := expectedResolvedSignal(leafDeclaration, leafEnvironment, wantBranch, available)
									wantErr := wantBranchErr || (!wantBranchErr && wantLeafErr)
									if (err != nil) != wantErr {
										t.Fatalf("%s descendants declarations=%d/%d env=%q/%q available=%t error=%v, want error=%t", signal.name, branchDeclaration, leafDeclaration, branchEnvironment, leafEnvironment, available, err, wantErr)
									}
									if err == nil && (signal.read(resolved[0].telemetryPolicy) != wantBranch || signal.read(resolved[0].children[0].telemetryPolicy) != wantLeaf) {
										t.Fatalf("%s descendants = %t/%t, want %t/%t", signal.name, signal.read(resolved[0].telemetryPolicy), signal.read(resolved[0].children[0].telemetryPolicy), wantBranch, wantLeaf)
									}
								}
							}
						}
					}
				}
			}
		}
	}

	wantCombinations := len(signals) * 2 * 3 * 3 * 3 * 3 * 3 * 3
	if combinations != wantCombinations || len(coveredSignals) != 3 || len(coveredDeclarations) != 3 || len(coveredDepths) != 3 || len(coveredEnvironment) != 3 || len(coveredAvailability) != 2 {
		t.Fatalf("incomplete matrix: combinations=%d/%d signals=%v declarations=%v depths=%v environment=%v availability=%v", combinations, wantCombinations, coveredSignals, coveredDeclarations, coveredDepths, coveredEnvironment, coveredAvailability)
	}
}

func TestTelemetryPolicyReconstructionUsesRetainedParent(t *testing.T) {
	module := plannedModule{
		path: "edge/branch/leaf", envKey: "FLOWSEER_EDGE_BRANCH_LEAF_ENABLED",
		telemetryDeclaration: TelemetryPolicy{Logs: TelemetryInherit, Metrics: TelemetryEnabled, Traces: TelemetryDisabled},
	}
	env := map[string]string{"FLOWSEER_EDGE_BRANCH_LEAF_TELEMETRY_LOGS_ENABLED": "true"}
	resolved, err := resolveTelemetryPolicies([]plannedModule{module}, mapLookup(env), resolvedTelemetryPolicy{metrics: false, traces: true}, resolvedTelemetryPolicy{logs: true, metrics: true, traces: true})
	if err != nil {
		t.Fatalf("resolveTelemetryPolicies() error: %v", err)
	}
	if got := resolved[0].telemetryPolicy; got != (resolvedTelemetryPolicy{logs: true, metrics: true, traces: false}) {
		t.Fatalf("reconstructed policy = %+v", got)
	}
}

func TestTelemetryViewDisablesExportButRetainsLocalTraceCorrelation(t *testing.T) {
	owner, localSink, exportSink, _, _ := newTelemetryPolicyTestOwner(t, signalInjected, signalInjected, signalInjected)
	view := owner.view(resolvedTelemetryPolicy{metrics: true, traces: true})
	ctx := withContextValues(context.Background(), view.attemptContextValues(testIdentity(), "FLOWSEER_EDGE_", "edge/worker"))
	spanCtx, span := Tracer(ctx).Start(ctx, "work")
	Logger(spanCtx).InfoContext(spanCtx, "local only")
	span.End()

	attrs, ok := localSink.find("local only")
	if !ok {
		t.Fatal("disabled log did not reach local sink")
	}
	if attrs["trace_id"] == "" || attrs["span_id"] == "" {
		t.Fatalf("local log correlation = %v, want trace and span identifiers", attrs)
	}
	if _, ok := exportSink.find("local only"); ok {
		t.Fatal("disabled log reached export handler")
	}
}

func TestTelemetryViewDisablesModuleAndRuntimeMetrics(t *testing.T) {
	owner, _, _, reader, _ := newTelemetryPolicyTestOwner(t, signalInjected, signalInjected, signalInjected)
	view := owner.view(resolvedTelemetryPolicy{logs: true, traces: true})
	ctx := withContextValues(context.Background(), view.attemptContextValues(testIdentity(), "FLOWSEER_EDGE_", "edge/worker"))
	if MeterProvider(ctx) != defaultMeterProvider {
		t.Fatal("disabled metrics did not expose the no-op meter provider")
	}
	counter, err := Meter(ctx).Int64Counter("disabled.runtime")
	if err != nil {
		t.Fatalf("create counter: %v", err)
	}
	counter.Add(ctx, 1)
	moduleCounter, err := MeterProvider(ctx).Meter("example/module").Int64Counter("disabled.module")
	if err != nil {
		t.Fatalf("create module counter: %v", err)
	}
	moduleCounter.Add(ctx, 1)
	view.recordLifecycle(ctx, "edge/worker", lifecycleActionStart, lifecycleOutcomeRunning)
	if got := collectCounters(t, reader); len(got) != 0 {
		t.Fatalf("disabled metrics recorded data: %v", got)
	}
}

func TestTelemetryViewManagedProvidersAreBorrowedFacades(t *testing.T) {
	owner, _, _, _, _ := newTelemetryPolicyTestOwner(t, signalManaged, signalManaged, signalManaged)
	view := owner.view(resolvedTelemetryPolicy{logs: true, metrics: true, traces: true})
	ctx := withContextValues(context.Background(), view.attemptContextValues(testIdentity(), "", "edge/worker"))
	if _, ok := TracerProvider(ctx).(interface{ Shutdown(context.Context) error }); ok {
		t.Fatal("managed tracer provider exposed Shutdown")
	}
	if _, ok := TracerProvider(ctx).(interface{ ForceFlush(context.Context) error }); ok {
		t.Fatal("managed tracer provider exposed ForceFlush")
	}
	if _, ok := MeterProvider(ctx).(interface{ Shutdown(context.Context) error }); ok {
		t.Fatal("managed meter provider exposed Shutdown")
	}
	if _, ok := MeterProvider(ctx).(interface{ ForceFlush(context.Context) error }); ok {
		t.Fatal("managed meter provider exposed ForceFlush")
	}
}

func TestTelemetryViewInjectedProvidersPreserveIdentity(t *testing.T) {
	owner, _, _, _, _ := newTelemetryPolicyTestOwner(t, signalInjected, signalInjected, signalInjected)
	view := owner.view(resolvedTelemetryPolicy{logs: true, metrics: true, traces: true})
	ctx := withContextValues(context.Background(), view.attemptContextValues(testIdentity(), "", "edge/worker"))
	if TracerProvider(ctx) != owner.tracerProvider || MeterProvider(ctx) != owner.meterProvider {
		t.Fatal("enabled injected providers did not preserve caller identity")
	}
}

func TestTelemetryViewTraceDisablementPreservesContextAndIsolatesParentSpan(t *testing.T) {
	owner, _, _, _, recorder := newTelemetryPolicyTestOwner(t, signalInjected, signalInjected, signalInjected)
	view := owner.view(resolvedTelemetryPolicy{logs: true, metrics: true})

	type identityKey struct{}
	deadline := time.Now().Add(time.Minute)
	parent, cancelDeadline := context.WithDeadline(context.WithValue(context.Background(), identityKey{}, "caller"), deadline)
	defer cancelDeadline()
	parent, parentSpan := owner.tracerProvider.Tracer("parent").Start(parent, "parent")
	parentSpanContext := parentSpan.SpanContext()
	isolated := view.context(parent)
	viewCtx := withContextValues(isolated, view.attemptContextValues(testIdentity(), "FLOWSEER_EDGE_", "edge/worker"))
	if TracerProvider(viewCtx) != defaultTracerProvider {
		t.Fatal("disabled traces did not expose the no-op tracer provider")
	}
	if got := isolated.Value(identityKey{}); got != "caller" {
		t.Fatalf("identity value = %v, want caller", got)
	}
	if gotDeadline, ok := isolated.Deadline(); !ok || !gotDeadline.Equal(deadline) {
		t.Fatalf("deadline = %v, %t, want %v", gotDeadline, ok, deadline)
	}
	if got := trace.SpanContextFromContext(isolated); !got.Equal(parentSpanContext) {
		t.Fatalf("span context = %v, want %v", got, parentSpanContext)
	}
	isolatedSpan := trace.SpanFromContext(isolated)
	if isolatedSpan == parentSpan || isolatedSpan.IsRecording() {
		t.Fatal("disabled trace context retained the mutable recording parent span")
	}
	isolatedSpan.AddEvent("must not reach parent")
	isolatedSpan.End()
	_, child := view.tracer.Start(isolated, "disabled child")
	child.End()
	if len(recorder.Ended()) != 0 {
		t.Fatalf("trace-disabled view ended or recorded spans: %d", len(recorder.Ended()))
	}
	parentSpan.End()
	if got := len(recorder.Ended()); got != 1 {
		t.Fatalf("ended spans = %d, want only parent", got)
	}

	cancelParent, cancel := context.WithCancel(isolated)
	cancelChild := view.context(cancelParent)
	cancel()
	select {
	case <-cancelChild.Done():
	case <-time.After(time.Second):
		t.Fatal("trace-disabled context lost cancellation")
	}
	if !reflect.DeepEqual(view.propagator, owner.telemetry.propagator) {
		t.Fatal("trace-disabled view did not preserve propagator identity")
	}
}

func expectedResolvedSignal(declaration TelemetryDeclaration, environment string, parent, available bool) (bool, bool) {
	enabled := parent
	explicit := declaration != TelemetryInherit
	switch declaration {
	case TelemetryEnabled:
		enabled = true
	case TelemetryDisabled:
		enabled = false
	}
	if environment != "" {
		explicit = true
		enabled = environment == "true"
	}
	return enabled, enabled && !available && explicit
}

func setResolvedSignal(policy *resolvedTelemetryPolicy, signal string, enabled bool) {
	switch signal {
	case "logs":
		policy.logs = enabled
	case "metrics":
		policy.metrics = enabled
	case "traces":
		policy.traces = enabled
	default:
		panic(fmt.Sprintf("unknown signal %q", signal))
	}
}

func newTelemetryPolicyTestOwner(
	t *testing.T,
	logsBacking, metricsBacking, tracesBacking signalBacking,
) (*telemetryOwner, *recordSink, *recordSink, *sdkmetric.ManualReader, *tracetest.SpanRecorder) {
	t.Helper()
	localLogger, localSink := newRecordingLogger()
	exportLogger, exportSink := newRecordingLogger()
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	capabilities, err := telemetryFromComponents(
		slog.New(multiSlogHandler{handlers: []slog.Handler{traceLogHandler{Handler: localLogger.Handler()}, exportLogger.Handler()}}),
		tracerProvider,
		meterProvider,
		defaultPropagator,
	)
	if err != nil {
		t.Fatalf("telemetryFromComponents() error: %v", err)
	}
	owner := &telemetryOwner{
		telemetry:      capabilities,
		localLogger:    slog.New(traceLogHandler{Handler: localLogger.Handler()}),
		tracerProvider: tracerProvider,
		meterProvider:  meterProvider,
		logsBacking:    logsBacking,
		metricsBacking: metricsBacking,
		tracesBacking:  tracesBacking,
	}
	owner.telemetry.owner = owner
	return owner, localSink, exportSink, reader, recorder
}
