package service

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/protobuf/types/known/emptypb"

	servicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/service/v1"
)

// TestRuntimeContractMatrix keeps the declarative dimensions that feed gates,
// supervision, delivery, and settlement in one exhaustive fixture. Focused
// tests exercise each transition and crash boundary; this matrix prevents a
// later validation change from making one of their combinations unrepresentable.
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
		state   servicev1.SettlementState
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
		{name: "acknowledge", state: servicev1.SettlementState_SETTLEMENT_STATE_ACKNOWLEDGE},
		{name: "retry", retries: 1, state: servicev1.SettlementState_SETTLEMENT_STATE_RETRY},
		{name: "discard", state: servicev1.SettlementState_SETTLEMENT_STATE_DISCARD},
	}

	for _, gate := range gates {
		for _, override := range overrides {
			for _, shape := range shapes {
				for _, depth := range depths {
					for _, strategy := range strategies {
						for _, exit := range exits {
							for _, action := range actions {
								for _, exhausted := range exhaustion {
									if exhausted && action != Restart {
										continue
									}
									for _, delivery := range deliveries {
										for _, disposition := range dispositions {
											name := fmt.Sprintf("gate=%s/override=%s/shape=%s/depth=%s/strategy=%d/exit=%s/action=%d/exhausted=%t/delivery=%s/disposition=%s",
												gate.name, override.name, shape, depth, strategy, exit.name, action, exhausted, delivery.name, disposition.name)
											t.Run(name, func(t *testing.T) {
												assertRuntimeMatrixTuple(t, gate.gate, gate.enabled, override.value, shape, depth, strategy, exit.index, action, exhausted, delivery.concurrency, disposition.retries, disposition.state)
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

func assertRuntimeMatrixTuple(
	t *testing.T,
	gate Gate,
	gateEnabled bool,
	override, shape, depth string,
	strategy Strategy,
	exitIndex int,
	action Action,
	exhausted bool,
	concurrency, retries int,
	settlementState servicev1.SettlementState,
) {
	t.Helper()
	budgetMax := 2
	if exhausted {
		budgetMax = 1
	}
	outcome := OutcomePolicy{Action: action, Budget: RestartBudget{Max: budgetMax, Window: defaultOutcomeWindow}}
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
			Kind:    messageKindEvent,
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
		fixture = Module{Name: "fixture", Gate: gate, Branch: &Branch{Strategy: strategy, Children: []Module{{Name: "nested", Branch: &Branch{Strategy: strategy, Children: []Module{fixture}}}}}}
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
	settlement := newSettlement(planned.path, "831c4c1f-1942-4dc8-a6f2-9f0ec4d588e5", uint32(retries), settlementState)
	if settlement.GetState() != settlementState || settlement.GetRetryCount() != uint32(retries) {
		t.Fatalf("settlement = %v, want state %v retries %d", settlement, settlementState, retries)
	}
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
