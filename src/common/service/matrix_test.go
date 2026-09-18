package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	runtimev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/runtime/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// TestRuntimeContractMatrix executes every valid declarative tuple through
// preflight, the supervisor decision engine, and the delivery disposition
// classifier. Broker-backed tests own persistence and crash boundaries.
func TestRuntimeContractMatrix(t *testing.T) {
	type gateCase struct {
		name    string
		gate    Gate
		enabled bool
	}
	type overrideCase struct {
		name  string
		value string
	}
	type exitCase struct {
		name  string
		index int
	}
	type deliveryCase struct {
		name        string
		concurrency int
	}
	type dispositionCase struct {
		name    string
		retries int
		state   runtimev1.SettlementState
	}

	gates := []gateCase{
		{name: "omitted", enabled: true},
		{name: "fixed", gate: FixedGate(false), enabled: false},
		{name: "probe", gate: ProbeGate(func(context.Context) (bool, error) { return true, nil }), enabled: true},
	}
	overrides := []overrideCase{{name: "none"}, {name: "enabled", value: "true"}, {name: "disabled", value: "false"}}
	shapes := []string{"leaf", "branch"}
	depths := []string{"shallow", "nested"}
	strategies := []Strategy{OneForOne, OneForAll, RestForOne}
	exits := []exitCase{{name: "normal", index: 0}, {name: "error", index: 1}, {name: "panic", index: 2}}
	actions := []Action{Stop, Restart, Escalate}
	exhaustion := []bool{false, true}
	deliveries := []deliveryCase{{name: "sequential", concurrency: 1}, {name: "parallel", concurrency: 4}}
	dispositions := []dispositionCase{
		{name: "acknowledge", state: runtimev1.SettlementState_SETTLEMENT_STATE_ACKNOWLEDGE},
		{name: "retry", retries: 1, state: runtimev1.SettlementState_SETTLEMENT_STATE_RETRY},
		{name: "discard", state: runtimev1.SettlementState_SETTLEMENT_STATE_DISCARD},
	}
	telemetry, err := newTelemetry(Config{})
	if err != nil {
		t.Fatal(err)
	}

	for _, gate := range gates {
		for _, override := range overrides {
			for _, shape := range shapes {
				for _, depth := range depths {
					for _, strategy := range strategies {
						for _, exit := range exits {
							for _, action := range actions {
								for _, exhausted := range exhaustion {
									if !validMatrixTuple(action, exhausted) {
										continue
									}
									for _, delivery := range deliveries {
										for _, disposition := range dispositions {
											name := fmt.Sprintf("gate=%s/override=%s/shape=%s/depth=%s/strategy=%d/exit=%s/action=%d/exhausted=%t/delivery=%s/disposition=%s",
												gate.name, override.name, shape, depth, strategy, exit.name, action, exhausted, delivery.name, disposition.name)
											t.Run(name, func(t *testing.T) {
												assertRuntimeMatrixTuple(t, telemetry, gate.gate, gate.enabled, override.value, shape, depth, strategy, exit.index, action, exhausted, delivery.concurrency, disposition.retries, disposition.state)
											})
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

func TestRuntimeContractMatrixRejectsInvalidTuples(t *testing.T) {
	for _, action := range []Action{Stop, Escalate} {
		if validMatrixTuple(action, true) {
			t.Fatalf("exhausted action %d was treated as a valid matrix tuple", action)
		}
	}
	if !validMatrixTuple(Restart, true) {
		t.Fatal("exhausted restart was rejected")
	}
}

func validMatrixTuple(action Action, exhausted bool) bool {
	return !exhausted || action == Restart
}

func assertRuntimeMatrixTuple(
	t *testing.T,
	telemetry telemetry,
	gate Gate,
	gateEnabled bool,
	override, shape, depth string,
	strategy Strategy,
	exitIndex int,
	action Action,
	exhausted bool,
	concurrency, retries int,
	settlementState runtimev1.SettlementState,
) {
	t.Helper()
	budgetMax := 2
	if exhausted {
		budgetMax = 1
	}
	outcome := OutcomePolicy{
		Action:  action,
		Budget:  RestartBudget{Max: budgetMax, Window: defaultOutcomeWindow},
		Backoff: Backoff{Initial: time.Nanosecond, Maximum: time.Nanosecond, ResetAfter: time.Hour},
	}
	policy := Policy{}
	switch exitIndex {
	case 0:
		policy.Normal = outcome
	case 1:
		policy.Error = outcome
	case 2:
		policy.Panic = outcome
	default:
		t.Fatalf("invalid exit index %d", exitIndex)
	}
	leaf := Module{Name: "worker", Policy: policy, Leaf: &Leaf{
		Setup:               testSetup(),
		DeliveryConcurrency: concurrency,
		Subscriptions: []Subscription{{
			Kind:    MessageKindEvent,
			Message: &emptypb.Empty{},
			Retries: retries,
		}},
	}}
	fixture := leaf
	fixture.Name = "fixture"
	fixture.Gate = gate
	if shape == "branch" {
		leaf.Name = "worker"
		fixture = Module{Name: "fixture", Gate: gate, Policy: policy, Branch: &Branch{Strategy: strategy, Children: []Module{leaf}}}
	}
	if depth == "nested" {
		fixture.Gate = Gate{}
		fixture = Module{Name: "fixture", Gate: gate, Policy: policy, Branch: &Branch{Strategy: strategy, Children: []Module{{Name: "nested", Branch: &Branch{Strategy: strategy, Children: []Module{fixture}}}}}}
	}
	config := Config{
		Identity:  Identity{Namespace: "flowseer", Name: "matrix", Version: "1.0.0"},
		EnvPrefix: "FLOWSEER_MATRIX_",
		Strategy:  strategy,
		Modules: []Module{
			{Name: "anchor", Leaf: &Leaf{Setup: testSetup()}},
			fixture,
		},
	}
	lookup := mapLookup(nil)
	if override != "" {
		lookup = mapLookup(map[string]string{"FLOWSEER_MATRIX_FIXTURE_ENABLED": override})
		gateEnabled = override == "true"
	}
	runtime, err := preflight(context.Background(), config, lookup)
	if err != nil {
		t.Fatal(err)
	}
	planned := matrixLeaf(runtime.modules[1])
	if planned == nil {
		t.Fatal("matrix fixture has no leaf")
	}
	if planned.enabled != gateEnabled {
		t.Fatalf("leaf enabled = %t, want %t", planned.enabled, gateEnabled)
	}
	if planned.leaf.deliveryConcurrency != concurrency {
		t.Fatalf("delivery concurrency = %d, want %d", planned.leaf.deliveryConcurrency, concurrency)
	}
	if got := planned.leaf.subscriptions[0].retries; got != retries {
		t.Fatalf("subscription retries = %d, want %d", got, retries)
	}
	policies := [...]normalizedOutcomePolicy{planned.policy.normal, planned.policy.failure, planned.policy.panic}
	if policies[exitIndex].action != action || policies[exitIndex].budget.Max != budgetMax {
		t.Fatalf("outcome policy = %+v, want action %d budget %d", policies[exitIndex], action, budgetMax)
	}
	if runtime.rootSupervisor.strategy != strategy {
		t.Fatalf("root strategy = %d, want %d", runtime.rootSupervisor.strategy, strategy)
	}
	exerciseMatrixSupervisor(t, runtime.modules, runtime, gateEnabled, exitIndex, action, exhausted, telemetry)

	var handler HandlerFunc
	switch settlementState {
	case runtimev1.SettlementState_SETTLEMENT_STATE_ACKNOWLEDGE:
		handler = func(context.Context, proto.Message) error { return nil }
	case runtimev1.SettlementState_SETTLEMENT_STATE_RETRY:
		handler = func(context.Context, proto.Message) error { return errs.New().Retryable().Msg("matrix retry") }
	case runtimev1.SettlementState_SETTLEMENT_STATE_DISCARD:
		handler = func(context.Context, proto.Message) error { return errors.New("matrix discard") }
	default:
		t.Fatalf("invalid settlement state %v", settlementState)
	}
	panicked, handlerErr := callHandler(context.Background(), handler, &emptypb.Empty{})
	if got := deliverySettlementState(panicked, handlerErr, 0, retries); got != settlementState {
		t.Fatalf("delivery settlement state = %v, want %v", got, settlementState)
	}
}

func exerciseMatrixSupervisor(
	t *testing.T,
	modules []plannedModule,
	runtime runtimeConfig,
	enabled bool,
	exitIndex int,
	action Action,
	exhausted bool,
	telemetry telemetry,
) {
	t.Helper()
	state := newSupervisorState("matrix", runtime.rootSupervisor, modules, supervisorRuntime{
		identity:  runtime.identity,
		envPrefix: runtime.envPrefix,
		telemetry: telemetry,
		options:   immediateSupervisorOptions(),
	})
	const fixtureIndex = 1
	const anchorIndex = 0
	if !state.slots[anchorIndex].active {
		t.Fatal("supervisor anchor is inactive")
	}
	if state.slots[fixtureIndex].active != enabled {
		t.Fatalf("supervisor fixture active = %t, want %t", state.slots[fixtureIndex].active, enabled)
	}
	if !enabled {
		return
	}
	for i := range state.slots {
		if !state.slots[i].active {
			continue
		}
		state.slots[i].cancel = func(error) {}
		state.slots[i].done = make(chan struct{})
		close(state.slots[i].done)
	}
	outcomes := [...]lifecycleOutcome{lifecycleOutcomeNormal, lifecycleOutcomeError, lifecycleOutcomePanic}
	result := childResult{index: fixtureIndex, outcome: outcomes[exitIndex], err: errors.New("matrix outcome")}
	policy, outcomeIndex := outcomePolicy(state.slots[fixtureIndex].module.policy, result.outcome)
	if policy == nil {
		t.Fatalf("outcome %d has no policy", result.outcome)
	}
	if exhausted {
		if !state.slots[fixtureIndex].budgets[outcomeIndex].consume(state.runtime.options.clock.Now()) {
			t.Fatal("could not pre-exhaust restart budget")
		}
	}
	err := state.decide(context.Background(), result)
	switch {
	case action == Escalate && err == nil:
		t.Fatal("escalated matrix outcome returned nil")
	case action == Restart && exhausted && err == nil:
		t.Fatal("exhausted matrix restart returned nil")
	case action != Escalate && (action != Restart || !exhausted) && err != nil:
		t.Fatalf("matrix decision failed: %v", err)
	case action == Stop && state.slots[fixtureIndex].active:
		t.Fatal("stopped matrix fixture remained active")
	}
	if action == Restart && !exhausted {
		wantAnchorGeneration := uint64(0)
		if runtime.rootSupervisor.strategy == OneForAll {
			wantAnchorGeneration = 1
		}
		if got := state.slots[anchorIndex].generation; got != wantAnchorGeneration {
			t.Fatalf("anchor generation = %d, want %d for strategy %d", got, wantAnchorGeneration, runtime.rootSupervisor.strategy)
		}
		if got := state.slots[fixtureIndex].generation; got != 1 {
			t.Fatalf("fixture generation = %d, want 1 for strategy %d", got, runtime.rootSupervisor.strategy)
		}
	}
	state.stopAll(errors.New("matrix complete"))
}

func matrixLeaf(module plannedModule) *plannedModule {
	if module.leaf != nil {
		return &module
	}
	for _, child := range module.children {
		if leaf := matrixLeaf(child); leaf != nil {
			return leaf
		}
	}
	return nil
}
