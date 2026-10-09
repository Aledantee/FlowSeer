package fabric_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
)

func injectActions(t0 time.Time, origin fabric.Endpoint, offsets ...time.Duration) []fabric.Action {
	frame := ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 1, 1},
		Dst:     netaddr.MAC{0, 0, 0, 0, 1, 2},
		Payload: []byte("ping"),
	}
	actions := make([]fabric.Action, len(offsets))
	for i, offset := range offsets {
		actions[i] = fabric.Action{
			ID:     "inject-" + string(rune('a'+i)),
			At:     t0.Add(offset),
			Kind:   fabric.ActionInject,
			Inject: &fabric.Injection{Origin: origin, Frame: frame},
		}
	}

	return actions
}

// requireNoQueuedActions fails unless the fabric's queue holds no scenario
// action and its wake and dequeue indexes name the arrivals they should.
func requireNoQueuedActions(t *testing.T, fab *fabric.Fabric) {
	t.Helper()
	queue := *fabric.ExportFabricQueue(fab)
	for i, arr := range queue {
		if arr.Kind == fabric.ArrivalAction {
			t.Errorf("queue[%d] is a scenario action left by the run", i)
		}
		if i > 0 && fabric.ExportCompareArrival(queue[(i-1)/2], arr) > 0 {
			t.Errorf("queue[%d] sorts before its heap parent queue[%d]", i, (i-1)/2)
		}
	}
	for device, idx := range *fabric.ExportFabricWakeItems(fab) {
		if idx >= len(queue) || queue[idx].Kind != fabric.ArrivalWake || queue[idx].Device != device {
			t.Errorf("wakeItems[%q] = %d, which does not hold that device's wake", device, idx)
		}
	}
	for ep, idx := range *fabric.ExportFabricDequeueItems(fab) {
		if idx >= len(queue) || queue[idx].Kind != fabric.ArrivalDequeue || queue[idx].Device != ep.Node || queue[idx].Port != ep.Port {
			t.Errorf("dequeueItems[%v] = %d, which does not hold that endpoint's dequeue", ep, idx)
		}
	}
	if pending := fab.Run(fabric.RunOptions{Budget: 0}).Pending; pending.Inputs != 0 {
		t.Errorf("pending inputs = %d after the run, want 0", pending.Inputs)
	}
}

func TestRunScenarioDiscardsUnfiredActions(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newTestSTPFabric(t, t0, 8192, 16384)
	actions := injectActions(t0, fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, time.Second, 2*time.Second)

	res, err := fab.RunScenario(fabric.Scenario{Name: "s1", Actions: actions}, fabric.RunOptions{Budget: 1})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if res.Stop != fabric.StopBudget || res.DroppedActions != 2 || !hasIssueCode(res.Issues, fabric.IssueActionsDropped) {
		t.Fatalf("result = stop %v, dropped %d, issues %+v, want Budget with two dropped actions reported", res.Stop, res.DroppedActions, res.Issues)
	}
	if res.Pending.Inputs != 2 {
		t.Errorf("result pending inputs = %d, want the 2 unfired actions it halted with", res.Pending.Inputs)
	}
	requireNoQueuedActions(t, fab)

	next, err := fab.RunScenario(fabric.Scenario{Name: "s2"}, fabric.RunOptions{Budget: 500})
	if err != nil {
		t.Fatalf("second RunScenario: %v", err)
	}
	if next.DroppedActions != 0 {
		t.Errorf("second run dropped %d actions, want 0", next.DroppedActions)
	}
	for _, j := range fab.Report() {
		if j.Origin.Kind == fabric.OriginInjection && !j.Protocol {
			t.Errorf("journey %d came from the first scenario's unfired action", j.FrameID)
		}
	}
}

func TestRunScenarioOnFaultedFabricDiscardsActions(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	ep := fabric.Endpoint{Node: "sw1", Port: "1/1/1"}
	fab := newEmptyFabric(t, t0)
	*fabric.ExportFabricStepped(fab) = true
	*fabric.ExportFabricEgress(fab) = map[fabric.Endpoint]*fabric.ExportEgressQueue{ep: fabric.MakeTestEgressQueuePointer(fabric.ExportEgressQueueFields{})}
	fabric.ExportFabricScheduleDequeue(fab, ep, t0.Add(-time.Second))
	if res := fab.Run(fabric.RunOptions{Budget: 10}); res.Stop != fabric.StopFault {
		t.Fatalf("fixture stop = %v, want Fault", res.Stop)
	}

	res, err := fab.RunScenario(fabric.Scenario{Name: "faulted", Actions: injectActions(t0, ep, time.Second)}, fabric.RunOptions{Budget: 10})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if res.Stop != fabric.StopFault || res.Steps != 0 || res.DroppedActions != 1 {
		t.Fatalf("result = stop %v, steps %d, dropped %d, want Fault, 0, 1", res.Stop, res.Steps, res.DroppedActions)
	}
	requireNoQueuedActions(t, fab)
}

func TestRefusedActionLeavesFabricRunnable(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newTestHostSwitchFabric(t, t0)
	actions := injectActions(t0, fabric.Endpoint{Node: "nope"}, time.Second)
	actions = append(actions, injectActions(t0, fabric.Endpoint{Node: "h1"}, 2*time.Second)...)
	actions[1].ID = "inject-after"

	res, err := fab.RunScenario(fabric.Scenario{Name: "refused", Actions: actions}, fabric.RunOptions{Budget: 100})
	if err == nil || res.Err == nil {
		t.Fatalf("RunScenario error = %v, result error = %v, want the refusal in both", err, res.Err)
	}
	if res.Stop != fabric.StopActionRefused {
		t.Errorf("stop = %v, want %v", res.Stop, fabric.StopActionRefused)
	}
	if hasIssueCode(res.Issues, fabric.IssueSchedulingFault) {
		t.Errorf("issues = %+v, want no scheduling fault for a refused action", res.Issues)
	}
	if res.DroppedActions != 1 || !hasIssueCode(res.Issues, fabric.IssueActionsDropped) {
		t.Errorf("dropped = %d, issues = %+v, want the action after the refusal reported dropped", res.DroppedActions, res.Issues)
	}
	if fab.Err() != nil {
		t.Fatalf("Err = %v, want the fabric left unfaulted", fab.Err())
	}
	requireNoQueuedActions(t, fab)

	again, err := fab.RunScenario(fabric.Scenario{Name: "again", Actions: injectActions(t0, fabric.Endpoint{Node: "h1"}, 3*time.Second)}, fabric.RunOptions{Budget: 100})
	if err != nil || again.Stop != fabric.StopQueueDrained || again.Steps == 0 {
		t.Errorf("run after refusal = stop %v, steps %d, error %v, want a drained run that steps", again.Stop, again.Steps, err)
	}
}

func TestSchedulingBreachStillFaultsFabric(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	ep := fabric.Endpoint{Node: "sw1", Port: "1/1/1"}
	fab := newEmptyFabric(t, t0)
	*fabric.ExportFabricStepped(fab) = true
	*fabric.ExportFabricEgress(fab) = map[fabric.Endpoint]*fabric.ExportEgressQueue{ep: fabric.MakeTestEgressQueuePointer(fabric.ExportEgressQueueFields{})}
	fabric.ExportFabricScheduleDequeue(fab, ep, t0.Add(-time.Second))

	res := fab.Run(fabric.RunOptions{Budget: 10})
	if res.Stop != fabric.StopFault || fab.Err() == nil || !hasIssueCode(res.Issues, fabric.IssueSchedulingFault) || res.Status != analysis.Unsupported {
		t.Errorf("result = stop %v, status %v, issues %+v, fabric error %v, want a sticky scheduling fault", res.Stop, res.Status, res.Issues, fab.Err())
	}
}
