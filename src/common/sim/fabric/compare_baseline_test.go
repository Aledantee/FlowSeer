package fabric_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/assembly"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
)

// offerBefore injects count aggregate frames of flow 1 at the origin and runs
// them to delivery, so the fabric holds statistics the scenario did not add.
func offerBefore(t *testing.T, f *fabric.Fabric, inj fabric.Injection, count int) {
	t.Helper()
	for range count {
		inj.At = f.Clock()
		if _, err := f.Inject(inj); err != nil {
			t.Fatal(err)
		}
	}
	if res := f.Run(fabric.RunOptions{Budget: 1000}); res.Stop != fabric.StopQueueDrained {
		t.Fatalf("pre-scenario run stop = %v, want QueueDrained", res.Stop)
	}
}

func TestCompareAggregateIgnoresPreScenarioFlowTotals(t *testing.T) {
	a, b, frame := comparisonAcceptanceFabrics(t)
	frame.Dst = assembly.Spec(a).Config().Hosts["h2"].Address
	h1 := fabric.Endpoint{Node: "h1"}
	offerBefore(t, a, fabric.Injection{Origin: h1, Frame: frame, Flow: 1, Retention: fabric.RetainAggregate}, 1)
	offerBefore(t, b, fabric.Injection{Origin: h1, Frame: frame, Flow: 1, Retention: fabric.RetainAggregate}, 2)

	at := a.Clock().Add(time.Second)
	if bAt := b.Clock().Add(time.Second); bAt.After(at) {
		at = bAt
	}
	scenario := scenarioFromInjections([]fabric.Injection{{At: at, Origin: h1, Frame: frame, Flow: 1, Retention: fabric.RetainAggregate}})
	cmp := fabric.Compare(a, b, scenario, fabric.RunOptions{Budget: 1000})
	if cmp.Err != nil || cmp.Disposition != analysis.Inconclusive {
		t.Errorf("comparison = %+v, want Inconclusive: both sides added the same to flow 1", cmp)
	}
}

// TestCompareAggregateKeepsLatencyExtremesUncertain gives the sides
// pre-scenario deliveries over paths of different length. Their latency
// extremes differ before the scenario starts and cannot be split from what the
// scenario added, so the pair stays uncertain and is not Different.
func TestCompareAggregateKeepsLatencyExtremesUncertain(t *testing.T) {
	a, b, frame := comparisonAcceptanceFabrics(t)
	frame.Dst = assembly.Spec(a).Config().Hosts["h2"].Address
	h1 := fabric.Endpoint{Node: "h1"}
	offerBefore(t, a, fabric.Injection{Origin: h1, Frame: frame, Flow: 1, Retention: fabric.RetainAggregate}, 1)
	offerBefore(t, b, fabric.Injection{Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Frame: frame, Flow: 1, Retention: fabric.RetainAggregate}, 1)
	if la, lb := a.Flows()[1].Latency, b.Flows()[1].Latency; la.Min == lb.Min {
		t.Fatalf("pre-scenario latency minima are both %v, the fixture needs them to differ", la.Min)
	}

	at := a.Clock().Add(time.Second)
	if bAt := b.Clock().Add(time.Second); bAt.After(at) {
		at = bAt
	}
	scenario := scenarioFromInjections([]fabric.Injection{{At: at, Origin: h1, Frame: frame, Flow: 1, Retention: fabric.RetainAggregate}})
	cmp := fabric.Compare(a, b, scenario, fabric.RunOptions{Budget: 1000})
	if cmp.Err != nil || cmp.Disposition != analysis.Inconclusive {
		t.Errorf("comparison = %+v, want Inconclusive: the scenario added the same on both sides", cmp)
	}
}

// TestCompareAggregateFindsWhatTheScenarioAdded keeps a real difference: one
// side's host rejects what the other delivers, whatever each side offered before.
func TestCompareAggregateFindsWhatTheScenarioAdded(t *testing.T) {
	a, b, frame := comparisonAcceptanceFabrics(t)
	h1 := fabric.Endpoint{Node: "h1"}
	offerBefore(t, a, fabric.Injection{Origin: h1, Frame: frame, Flow: 1, Retention: fabric.RetainAggregate}, 2)
	offerBefore(t, b, fabric.Injection{Origin: h1, Frame: frame, Flow: 1, Retention: fabric.RetainAggregate}, 2)

	at := a.Clock().Add(time.Second)
	scenario := scenarioFromInjections([]fabric.Injection{{At: at, Origin: h1, Frame: frame, Flow: 1, Retention: fabric.RetainAggregate}})
	cmp := fabric.Compare(a, b, scenario, fabric.RunOptions{Budget: 1000})
	if cmp.Err != nil || cmp.Disposition != analysis.Different || cmp.Location.Field != "Flow.Delivered" {
		t.Errorf("comparison = %+v, want Different at Flow.Delivered", cmp)
	}
}
