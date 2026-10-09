package fabric_test

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"
	"go.aledante.io/FlowSeer/src/common/sim/device"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestScenarioActionsBeforeFabricClock(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	at := t0.Add(-time.Second)
	frame := ethernet.Frame{Payload: []byte("before-clock")}
	actions := []fabric.Action{
		{ID: "scenario_test-1", At: at, Kind: fabric.ActionInject, Inject: &fabric.Injection{At: at, Origin: fabric.Endpoint{Node: "h1"}, Frame: frame}},
		{ID: "scenario_test-2", At: at, Kind: fabric.ActionRecord, Record: &fabric.Record{At: at, Source: "capture", Origin: fabric.Endpoint{Node: "h1"}, Frame: &frame}},
		{ID: "scenario_test-3", At: at, Kind: fabric.ActionFault, Fault: &fabric.FaultAction{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Fault: cablemodel.Fault{Kind: cablemodel.FaultCut}}},
		{ID: "scenario_test-4", At: at, Kind: fabric.ActionConfigure, Configure: new(assembly.ConfigureAction("sw1", vswitch.Config{}))},
		{ID: "scenario_test-5", At: at, Kind: fabric.ActionMcheck, Mcheck: new(assembly.McheckAction("sw1", "1/1/1"))},
	}
	laterFabric := func(t *testing.T, stepped bool) *fabric.Fabric {
		t.Helper()
		if !stepped {
			return newTestHostSwitchFabric(t, t0)
		}
		fab := newTestHostSwitchFabric(t, at)
		if _, err := fab.Inject(fabric.Injection{At: t0, Origin: fabric.Endpoint{Node: "h1"}, Frame: frame}); err != nil {
			t.Fatal(err)
		}
		if res := fab.Run(fabric.RunOptions{Budget: 10, Window: 0}); res.Steps == 0 || !res.Clock.After(at) {
			t.Fatalf("stepped setup = %+v, want steps and clock after action", res)
		}
		return fab
	}
	var checked int
	for _, action := range actions {
		for _, clock := range []struct {
			name    string
			stepped bool
		}{{"constructed", false}, {"stepped", true}} {
			t.Run(string(action.Kind)+"/"+clock.name, func(t *testing.T) {
				if err := action.Validate(); err != nil {
					t.Fatalf("action must be valid before checking clock: %v", err)
				}
				sc := fabric.Scenario{Name: "before-clock", Actions: []fabric.Action{action}}
				fab := laterFabric(t, clock.stepped)
				if _, err := fab.RunScenario(sc, fabric.RunOptions{Budget: 10, Window: 0}); err == nil || !strings.HasPrefix(err.Error(), "action 0 at ") || !strings.Contains(err.Error(), "precedes fabric clock") {
					t.Errorf("RunScenario error = %v, want precedes fabric clock", err)
				}
				checked++
				for _, currentLater := range []bool{true, false} {
					a := newTestHostSwitchFabric(t, at)
					b := newTestHostSwitchFabric(t, at)
					if currentLater {
						a = laterFabric(t, clock.stepped)
					} else {
						b = laterFabric(t, clock.stepped)
					}
					cmp := fabric.Compare(a, b, sc, fabric.RunOptions{Budget: 10, Window: 0})
					if cmp.Err == nil || !strings.HasPrefix(cmp.Err.Error(), "action 0 at ") || !strings.Contains(cmp.Err.Error(), "precedes fabric clock") {
						t.Errorf("Compare currentLater=%t error = %v, want precedes fabric clock", currentLater, cmp.Err)
					}
					checked++
				}
			})
		}
	}
	if checked != 30 {
		t.Fatalf("checked execution clocks = %d, want 30", checked)
	}
}

func newTestTwoSwitchSTPFabric(t *testing.T, t0 time.Time) *fabric.Fabric {
	t.Helper()
	mac1 := netaddr.MAC{0, 0, 0, 0, 1, 1}
	mac2 := netaddr.MAC{0, 0, 0, 0, 1, 2}
	macH1 := netaddr.MAC{0, 0, 0, 0, 0, 1}

	ports := func() port.Table {
		tbl, _ := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		return tbl
	}

	cfg := assembly.Config{
		Start:         t0,
		PhyAssumption: testPhyAssumption(),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  ports(),
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 4096,
					Address:  mac1,
					Ports:    map[string]stp.Port{"1/1/1": {}, "1/1/2": {}},
				},
			},
			"sw2": {
				Ports:  ports(),
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 8192,
					Address:  mac2,
					Ports:    map[string]stp.Port{"1/1/1": {}, "1/1/2": {}},
				},
			},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: macH1},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
			},
			{
				A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B: fabric.Endpoint{Node: "h1"},
			},
		},
	}

	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatalf("New STP fabric: %v", err)
	}
	return fab
}

func TestScenarioSameTimeDeclarationOrder(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newTestTwoHostFabric(t, t0)

	frame1 := ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 0, 1},
		Dst:     netaddr.MAC{0, 0, 0, 0, 0, 2},
		Payload: []byte("frame-1"),
	}
	frame2 := ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 0, 2},
		Dst:     netaddr.MAC{0, 0, 0, 0, 0, 1},
		Payload: []byte("frame-2"),
	}

	sc := fabric.Scenario{
		Name: "same-time-order",
		Actions: []fabric.Action{
			{
				ID:   "scenario_test-6",
				At:   t0,
				Kind: fabric.ActionInject,
				Inject: &fabric.Injection{
					Origin: fabric.Endpoint{Node: "h1"},
					Frame:  frame1,
				},
			},
			{
				ID:   "scenario_test-7",
				At:   t0,
				Kind: fabric.ActionInject,
				Inject: &fabric.Injection{
					Origin: fabric.Endpoint{Node: "h2"},
					Frame:  frame2,
				},
			},
		},
	}

	res, err := fab.RunScenario(sc, fabric.RunOptions{Budget: 12, Window: 0})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if res.Stop != fabric.StopQueueDrained {
		t.Errorf("res.Stop = %v, want StopQueueDrained", res.Stop)
	}

	journeys := fab.Report()
	if len(journeys) != 2 {
		t.Fatalf("len(journeys) = %d, want 2", len(journeys))
	}
	if journeys[0].FrameID != 1 || journeys[0].Injection.Origin.Node != "h1" {
		t.Errorf("journeys[0] FrameID=%d Origin=%v, want FrameID=1 from h1", journeys[0].FrameID, journeys[0].Injection.Origin)
	}
	if journeys[1].FrameID != 2 || journeys[1].Injection.Origin.Node != "h2" {
		t.Errorf("journeys[1] FrameID=%d Origin=%v, want FrameID=2 from h2", journeys[1].FrameID, journeys[1].Injection.Origin)
	}
}

func TestScenarioShuffledActionSliceNormalizes(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	frame := ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 0, 1},
		Dst:     netaddr.MAC{0, 0, 0, 0, 0, 2},
		Payload: []byte("ping"),
	}

	act1 := fabric.Action{
		ID:   "scenario_test-8",
		At:   t0,
		Kind: fabric.ActionInject,
		Inject: &fabric.Injection{
			Origin: fabric.Endpoint{Node: "h1"},
			Frame:  frame,
		},
	}
	act2 := fabric.Action{
		ID:   "scenario_test-9",
		At:   t0.Add(time.Second),
		Kind: fabric.ActionInject,
		Inject: &fabric.Injection{
			Origin: fabric.Endpoint{Node: "h2"},
			Frame:  frame,
		},
	}

	// Deliberately shuffled slice: later action first
	sc := fabric.Scenario{
		Name:    "shuffled",
		Actions: []fabric.Action{act2, act1},
	}

	norm, err := sc.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if len(norm.Actions) != 2 {
		t.Fatalf("len(norm.Actions) = %d, want 2", len(norm.Actions))
	}
	if !norm.Actions[0].At.Equal(t0) {
		t.Errorf("norm.Actions[0].At = %v, want %v", norm.Actions[0].At, t0)
	}
	if !norm.Actions[1].At.Equal(t0.Add(time.Second)) {
		t.Errorf("norm.Actions[1].At = %v, want %v", norm.Actions[1].At, t0.Add(time.Second))
	}
}

func TestScenarioValidateRefusalTable(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	validInject := &fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Payload: []byte("x")},
	}
	validFault := &fabric.FaultAction{
		A:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		B:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
		Fault: cablemodel.Fault{Kind: cablemodel.FaultCut},
	}

	cases := []struct {
		name string
		sc   fabric.Scenario
	}{
		{
			name: "empty name",
			sc: fabric.Scenario{
				Name: "",
			},
		},
		{
			name: "kind with no pointer",
			sc: fabric.Scenario{
				Name: "no-pointer",
				Actions: []fabric.Action{
					{ID: "scenario_test-10", At: t0, Kind: fabric.ActionInject, Inject: nil},
				},
			},
		},
		{
			name: "kind with two pointers",
			sc: fabric.Scenario{
				Name: "two-pointers",
				Actions: []fabric.Action{
					{ID: "scenario_test-11", At: t0, Kind: fabric.ActionInject, Inject: validInject, Fault: validFault},
				},
			},
		},
		{
			name: "partially numbered index slice",
			sc: fabric.Scenario{
				Name: "partially-numbered",
				Actions: []fabric.Action{
					{ID: "scenario_test-12", At: t0, Index: 1, Kind: fabric.ActionInject, Inject: validInject},
					{ID: "scenario_test-13", At: t0, Index: 0, Kind: fabric.ActionInject, Inject: validInject},
				},
			},
		},
		{
			name: "inner At disagrees with Action.At",
			sc: fabric.Scenario{
				Name: "disagreeing-inner-at",
				Actions: []fabric.Action{
					{
						ID:   "scenario_test-14",
						At:   t0,
						Kind: fabric.ActionInject,
						Inject: &fabric.Injection{
							At:     t0.Add(time.Hour),
							Origin: fabric.Endpoint{Node: "h1"},
							Frame:  ethernet.Frame{Payload: []byte("x")},
						},
					},
				},
			},
		},
		{
			name: "duplicate numbered at and index",
			sc: fabric.Scenario{
				Name: "duplicate-at-index",
				Actions: []fabric.Action{
					{
						ID:     "scenario_test-15",
						At:     t0,
						Index:  1,
						Kind:   fabric.ActionInject,
						Inject: validInject,
					},
					{
						ID:    "scenario_test-16",
						At:    t0,
						Index: 1,
						Kind:  fabric.ActionFault,
						Fault: validFault,
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.sc.Validate(); err == nil {
				t.Errorf("tc.sc.Validate() succeeded, want error for %s", tc.name)
			}
		})
	}
}

func TestScenarioNumberedIndexUniqueness(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	validInject := &fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Payload: []byte("x")},
	}
	validFault := &fabric.FaultAction{
		A:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		B:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
		Fault: cablemodel.Fault{Kind: cablemodel.FaultCut},
	}

	scValidSameTimeDiffIndex := fabric.Scenario{
		Name: "valid-same-time-diff-index",
		Actions: []fabric.Action{
			{ID: "scenario_test-17", At: t0, Index: 1, Kind: fabric.ActionInject, Inject: validInject},
			{ID: "scenario_test-18", At: t0, Index: 2, Kind: fabric.ActionFault, Fault: validFault},
		},
	}
	if err := scValidSameTimeDiffIndex.Validate(); err != nil {
		t.Fatalf("Validate failed for valid distinct indices at same time: %v", err)
	}

	validFaultLater := &fabric.FaultAction{
		A:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		B:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
		Fault: cablemodel.Fault{Kind: cablemodel.FaultCut},
	}
	scValidDiffTimeSameIndex := fabric.Scenario{
		Name: "valid-diff-time-same-index",
		Actions: []fabric.Action{
			{ID: "scenario_test-19", At: t0, Index: 1, Kind: fabric.ActionInject, Inject: validInject},
			{ID: "scenario_test-20", At: t0.Add(time.Second), Index: 1, Kind: fabric.ActionFault, Fault: validFaultLater},
		},
	}
	if err := scValidDiffTimeSameIndex.Validate(); err != nil {
		t.Fatalf("Validate failed for valid same index at different times: %v", err)
	}
}

func TestScenarioDiffEveryField(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	base := fabric.Scenario{
		Name: "base-scenario",
		Actions: []fabric.Action{
			{
				ID:    "scenario_test-21",
				At:    t0,
				Index: 1,
				Kind:  fabric.ActionInject,
				Inject: &fabric.Injection{
					At:     t0,
					Origin: fabric.Endpoint{Node: "h1"},
					Frame:  ethernet.Frame{Payload: []byte("a")},
				},
			},
		},
	}

	t.Run("name", func(t *testing.T) {
		mut := base.Clone()
		mut.Name = "different-name"
		changes, err := fabric.DiffScenarios(base, mut)
		if err != nil {
			t.Fatalf("DiffScenarios: %v", err)
		}
		if !slices.ContainsFunc(changes, func(ch trace.Change) bool { return ch.Field == "name" && ch.Subject.Kind == "scenario" }) {
			t.Errorf("missing name change: %+v", changes)
		}
	})

	t.Run("action at", func(t *testing.T) {
		mut := base.Clone()
		mut.Actions[0].At = t0.Add(time.Second)
		mut.Actions[0].Inject.At = t0.Add(time.Second)
		changes, err := fabric.DiffScenarios(base, mut)
		if err != nil {
			t.Fatalf("DiffScenarios: %v", err)
		}
		if !slices.ContainsFunc(changes, func(ch trace.Change) bool { return ch.Field == "at" && ch.Subject.Kind == "scenario.action" }) {
			t.Errorf("missing action at change: %+v", changes)
		}
	})

	t.Run("action kind", func(t *testing.T) {
		mut := base.Clone()
		mut.Actions[0].Kind = fabric.ActionFault
		mut.Actions[0].Inject = nil
		mut.Actions[0].Fault = &fabric.FaultAction{
			A:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			B:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
			Fault: cablemodel.Fault{Kind: cablemodel.FaultCut},
		}
		changes, err := fabric.DiffScenarios(base, mut)
		if err != nil {
			t.Fatalf("DiffScenarios: %v", err)
		}
		if !slices.ContainsFunc(changes, func(ch trace.Change) bool { return ch.Field == "kind" && ch.Subject.Kind == "scenario.action" }) {
			t.Errorf("missing action kind change: %+v", changes)
		}
	})
}

func TestScenarioDiffScenariosRawVsNormalized(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	raw := fabric.Scenario{
		Name: "raw-scenario",
		Actions: []fabric.Action{
			{
				ID:   "scenario_test-22",
				At:   t0,
				Kind: fabric.ActionInject,
				Inject: &fabric.Injection{
					Origin: fabric.Endpoint{Node: "h1"},
					Frame:  ethernet.Frame{Payload: []byte("raw")},
				},
			},
		},
	}

	norm, err := raw.Normalize()
	if err != nil {
		t.Fatalf("raw.Normalize: %v", err)
	}

	changes, err := fabric.DiffScenarios(raw, norm)
	if err != nil {
		t.Fatalf("DiffScenarios(raw, norm): %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("DiffScenarios(raw, norm) returned %d changes, want 0: %+v", len(changes), changes)
	}
}

func TestReplayRefusesMissingCheckpoint(t *testing.T) {
	spec := fabric.MakeTestReplaySpec(fabric.ExportReplaySpecFields{})

	_, err := fabric.Replay(spec)
	if err == nil {
		t.Fatal("Replay succeeded without checkpoint, want error")
	}
}

func TestScenarioSpanningTreeWindowZeroStopsAtBudget(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newTestTwoSwitchSTPFabric(t, t0)

	sc := fabric.Scenario{
		Name: "stp-window-zero",
	}

	res, err := fab.RunScenario(sc, fabric.RunOptions{Budget: 5, Window: 0})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if res.Stop != fabric.StopBudget {
		t.Errorf("res.Stop = %v, want StopBudget", res.Stop)
	}
	if res.Steps != 5 {
		t.Errorf("res.Steps = %d, want 5", res.Steps)
	}
}

func TestScenarioReplayDoubleExecutionEquality(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newTestTwoSwitchSTPFabric(t, t0)

	frame := ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 0, 1},
		Dst:     netaddr.MAC{0, 0, 0, 0, 1, 1},
		Payload: []byte("journey-payload"),
	}

	sc := fabric.Scenario{
		Name: "cut-and-inject",
		Actions: []fabric.Action{
			{
				ID:   "scenario_test-23",
				At:   t0.Add(time.Second),
				Kind: fabric.ActionFault,
				Fault: &fabric.FaultAction{
					A:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
					B:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
					Fault: cablemodel.Fault{Kind: cablemodel.FaultCut},
				},
			},
			{
				ID:   "scenario_test-24",
				At:   t0.Add(2 * time.Second),
				Kind: fabric.ActionInject,
				Inject: &fabric.Injection{
					Origin: fabric.Endpoint{Node: "h1"},
					Frame:  frame,
				},
			},
		},
	}

	res1, err := fab.RunScenario(sc, fabric.RunOptions{Budget: 20, Window: 3})
	if err != nil {
		t.Fatalf("RunScenario 1: %v", err)
	}

	res2, err := fabric.Replay(res1.Replay)
	if err != nil {
		t.Fatalf("Replay 2: %v", err)
	}

	if res1.Stop != res2.Stop {
		t.Errorf("res1.Stop=%v != res2.Stop=%v", res1.Stop, res2.Stop)
	}
	if res1.Steps != res2.Steps {
		t.Errorf("res1.Steps=%d != res2.Steps=%d", res1.Steps, res2.Steps)
	}
	if !slices.Equal(res1.Fingerprints, res2.Fingerprints) {
		t.Errorf("res1.Fingerprints != res2.Fingerprints")
	}

	j1 := fab.Report()
	fab2 := res1.Replay.Checkpoint()
	if _, err := fab2.RunScenario(res1.Replay.Scenario, res1.Replay.Options); err != nil {
		t.Fatalf("RunScenario checkpoint: %v", err)
	}
	j2 := fab2.Report()

	if len(j1) != len(j2) {
		t.Fatalf("len(j1)=%d != len(j2)=%d", len(j1), len(j2))
	}
	for i := range j1 {
		if j1[i].State != j2[i].State {
			t.Errorf("journey %d state mismatch: %v != %v", i, j1[i].State, j2[i].State)
		}
		if len(j1[i].Entries) != len(j2[i].Entries) {
			t.Errorf("journey %d entries len mismatch: %d != %d", i, len(j1[i].Entries), len(j2[i].Entries))
		}
	}
}

func newTestThreeHostFabric(t *testing.T, t0 time.Time) (*fabric.Fabric, assembly.Config) {
	t.Helper()
	macH1 := netaddr.MAC{0, 0, 0, 0, 0, 1}
	macH2 := netaddr.MAC{0, 0, 0, 0, 0, 2}
	macH3 := netaddr.MAC{0, 0, 0, 0, 0, 3}

	ports, _ := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()

	cfg := assembly.Config{
		Start:         t0,
		PhyAssumption: testPhyAssumption(),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  ports,
				Bridge: &bridge.Config{},
			},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
			"h3": {Address: macH3},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "h1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h2"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, B: fabric.Endpoint{Node: "h3"}},
		},
	}

	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}
	return fab, cfg
}

func TestScenarioReplaySeededFabricEquality(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab, cfg := newTestThreeHostFabric(t, t0)

	macH2 := cfg.Hosts["h2"].Address
	if err := fabric.Operate(fab, "sw1", func(d device.Device, _ time.Time) (device.Output, error) {
		return device.Output{}, d.(*vswitch.Switch).Learn([]bridge.Seed{
			{MAC: macH2, Port: "1/1/2", Lifetime: bridge.Static},
		})
	}); err != nil {
		t.Fatalf("Learn: %v", err)
	}

	frame := ethernet.Frame{
		Src:     cfg.Hosts["h1"].Address,
		Dst:     macH2,
		Payload: []byte("seeded-payload"),
	}

	sc := fabric.Scenario{
		Name: "seeded-unicast",
		Actions: []fabric.Action{
			{
				ID:   "scenario_test-25",
				At:   t0.Add(time.Second),
				Kind: fabric.ActionInject,
				Inject: &fabric.Injection{
					Origin: fabric.Endpoint{Node: "h1"},
					Frame:  frame,
				},
			},
		},
	}

	res1, err := fab.RunScenario(sc, fabric.RunOptions{Budget: 20, Window: 3})
	if err != nil {
		t.Fatalf("RunScenario 1: %v", err)
	}

	res2, err := fabric.Replay(res1.Replay)
	if err != nil {
		t.Fatalf("Replay 2: %v", err)
	}

	if res1.Stop != res2.Stop {
		t.Errorf("res1.Stop=%v != res2.Stop=%v", res1.Stop, res2.Stop)
	}
	if res1.Steps != res2.Steps {
		t.Errorf("res1.Steps=%d != res2.Steps=%d", res1.Steps, res2.Steps)
	}
	if !slices.Equal(res1.Fingerprints, res2.Fingerprints) {
		t.Errorf("res1.Fingerprints != res2.Fingerprints")
	}

	j1 := fab.Report()
	fab2 := res1.Replay.Checkpoint()
	if _, err := fab2.RunScenario(res1.Replay.Scenario, res1.Replay.Options); err != nil {
		t.Fatalf("RunScenario checkpoint: %v", err)
	}
	j2 := fab2.Report()

	if len(j1) != len(j2) {
		t.Fatalf("len(j1)=%d != len(j2)=%d", len(j1), len(j2))
	}
	if len(j1) != 1 {
		t.Fatalf("len(j1)=%d, want 1 (seeded unicast delivery)", len(j1))
	}
	for i := range j1 {
		if j1[i].State != j2[i].State {
			t.Errorf("journey %d state mismatch: %v != %v", i, j1[i].State, j2[i].State)
		}
		if len(j1[i].Entries) != len(j2[i].Entries) {
			t.Errorf("journey %d entries len mismatch: %d != %d", i, len(j1[i].Entries), len(j2[i].Entries))
		}
	}

	// An unseeded checkpoint retains the flooding contrast to seeded unicast.
	droppedFab, _ := newTestThreeHostFabric(t, t0)
	droppedRes, err := droppedFab.RunScenario(res1.Replay.Scenario, res1.Replay.Options)
	if err != nil {
		t.Fatalf("RunScenario without seeds: %v", err)
	}
	if slices.Equal(res1.Fingerprints, droppedRes.Fingerprints) {
		t.Errorf("expected fingerprints to differ when seeds are dropped")
	}
	droppedJourneys := droppedFab.Report()
	if len(droppedJourneys[0].Entries) == len(j1[0].Entries) {
		t.Errorf("expected unseeded flooding to produce more journey entries than seeded unicast (%d == %d)", len(droppedJourneys[0].Entries), len(j1[0].Entries))
	}
}

func TestScenarioMutationIndependence(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newTestTwoHostFabric(t, t0)

	actions := []fabric.Action{
		{
			ID:   "scenario_test-26",
			At:   t0,
			Kind: fabric.ActionInject,
			Inject: &fabric.Injection{
				Origin: fabric.Endpoint{Node: "h1"},
				Frame:  ethernet.Frame{Payload: []byte("initial")},
			},
		},
	}

	sc := fabric.Scenario{
		Name:    "mutation-isolation",
		Actions: actions,
	}

	res, err := fab.RunScenario(sc, fabric.RunOptions{Budget: 10, Window: 0})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}

	// Mutate caller's slice
	actions[0].At = t0.Add(time.Hour)
	actions[0].Inject.Origin = fabric.Endpoint{Node: "h2"}

	replayAction := res.Replay.Scenario.Actions[0]
	if !replayAction.At.Equal(t0) {
		t.Errorf("res.Replay.Scenario action was mutated: At = %v, want %v", replayAction.At, t0)
	}
	if replayAction.Inject.Origin.Node != "h1" {
		t.Errorf("res.Replay.Scenario origin was mutated: Node = %v, want h1", replayAction.Inject.Origin.Node)
	}
	clone := res.Replay.Scenario.Clone()
	clone.Name = "changed"
	clone.Actions[0].ID = "changed"
	clone.Actions[0].At = t0.Add(time.Hour)
	clone.Actions[0].Index = 20
	clone.Actions[0].Kind = fabric.ActionFault
	clone.Actions[0].Inject.At = t0.Add(time.Hour)
	clone.Actions[0].Inject.Origin = fabric.Endpoint{Node: "h2", Port: "other"}
	clone.Actions[0].Inject.Frame.Dst = netaddr.MAC{2}
	clone.Actions[0].Inject.Frame.Src = netaddr.MAC{4}
	clone.Actions[0].Inject.Frame.Tags = []vlan.Tag{{VID: 20}}
	clone.Actions[0].Inject.Frame.EtherType = ethernet.EtherTypeIPv4
	clone.Actions[0].Inject.Frame.Payload = []byte("changed")
	clone.Actions[0].Inject.Packet = &fabric.Packet{To: netip.MustParseAddr("192.0.2.1"), Protocol: 17, Payload: []byte("changed")}
	clone.Actions[0].Inject.Retention = fabric.RetainAggregate
	clone.Actions[0].Inject.Flow = 2
	clone.Actions[0].Fault = &fabric.FaultAction{}
	clone.Actions[0].Mcheck = &fabric.McheckAction{}
	clone.Actions[0].Record = &fabric.Record{}
	clone.Actions[0].Configure = &fabric.ConfigureAction{}
	clone.Actions = append(clone.Actions, fabric.Action{ID: "extra"})
	got := res.Replay.Scenario
	a := got.Actions[0]
	i := a.Inject
	if got.Name != "mutation-isolation" || len(got.Actions) != 1 || a.ID != "scenario_test-26" || !a.At.Equal(t0) || a.Index != 1 || a.Kind != fabric.ActionInject || a.Fault != nil || a.Mcheck != nil || a.Record != nil || a.Configure != nil {
		t.Fatalf("clone changed replay schedule: %+v", got)
	}
	if !i.At.Equal(t0) || i.Origin != (fabric.Endpoint{Node: "h1"}) || i.Frame.Dst != (netaddr.MAC{}) || i.Frame.Src != (netaddr.MAC{}) || len(i.Frame.Tags) != 0 || i.Frame.EtherType != 0 || string(i.Frame.Payload) != "initial" || i.Packet != nil || i.Retention != fabric.RetainJourney || i.Flow != 0 {
		t.Fatalf("clone changed replay injection: %+v", i)
	}
}

func TestScenarioForkIsolation(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab := newTestTwoSwitchSTPFabric(t, t0)

	// Step source once
	fab.Step()

	// Fork
	forked := fab.Fork()

	// Run fork to completion
	_ = forked.Run(fabric.RunOptions{Budget: 100, Window: 0})

	// Verify source fabric steps and clock are unaffected
	sourceRes := fab.Run(fabric.RunOptions{Budget: 5, Window: 0})
	if sourceRes.Steps != 5 {
		t.Errorf("sourceRes.Steps = %d, want 5", sourceRes.Steps)
	}
}

func TestScenarioConfigureTransientBetweenSwitches(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	macH1 := netaddr.MAC{0, 0, 0, 0, 0, 1}
	macH2 := netaddr.MAC{0, 0, 0, 0, 0, 2}

	newPorts := func() port.Table {
		tbl, _ := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		return tbl
	}
	vid10 := vlan.ID(10)
	makeBridge10 := func() *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "prod"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/24": {Tagged: []vlan.ID{10}, IngressFiltering: true},
				},
			},
		}
	}
	vid20 := vlan.ID(20)
	makeBridge20 := func() *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1":  {PVID: &vid20, Untagged: []vlan.ID{20}},
					"1/1/24": {Tagged: []vlan.ID{20}, IngressFiltering: true},
				},
			},
		}
	}

	cfg := assembly.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: newPorts(), Bridge: makeBridge10()},
			"sw2": {Ports: newPorts(), Bridge: makeBridge10()},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 1.0}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{Cable: cablemodel.Cable{LengthMeters: 300, Medium: cablemodel.MultimodeFiber}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/24"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/24"}},
			{Cable: cablemodel.Cable{LengthMeters: 1.0}, A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, B: fabric.Endpoint{Node: "h2"}},
		},
		PhyAssumption: &cablemodel.PhyAssumption{
			Medium: cablemodel.TwistedPair,
			Ethernet: phy.Ethernet{
				SupportedSpeedsBPS:       []uint64{1_000_000_000},
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
		},
	}

	fab, err := assembly.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t1 := t0.Add(time.Second)
	t2 := t1.Add(time.Second)

	sc := fabric.Scenario{
		Name: "vlan-migration",
		Actions: []fabric.Action{
			{
				ID:        "scenario_test-27",
				At:        t1,
				Kind:      fabric.ActionConfigure,
				Configure: new(assembly.ConfigureAction("sw1", vswitch.Config{Ports: newPorts(), Bridge: makeBridge20()})),
			},
			{
				ID:   "scenario_test-28",
				At:   t1.Add(100 * time.Millisecond),
				Kind: fabric.ActionInject,
				Inject: &fabric.Injection{
					Origin: fabric.Endpoint{Node: "h1"},
					Frame: ethernet.Frame{
						Dst:       macH2,
						Src:       macH1,
						EtherType: ethernet.EtherTypeIPv4,
						Payload:   []byte("transient-drop"),
					},
				},
			},
			{
				ID:        "scenario_test-29",
				At:        t2,
				Kind:      fabric.ActionConfigure,
				Configure: new(assembly.ConfigureAction("sw2", vswitch.Config{Ports: newPorts(), Bridge: makeBridge20()})),
			},
			{
				ID:   "scenario_test-30",
				At:   t2.Add(100 * time.Millisecond),
				Kind: fabric.ActionInject,
				Inject: &fabric.Injection{
					Origin: fabric.Endpoint{Node: "h1"},
					Frame: ethernet.Frame{
						Dst:       macH2,
						Src:       macH1,
						EtherType: ethernet.EtherTypeIPv4,
						Payload:   []byte("delivered"),
					},
				},
			},
		},
	}

	res, err := fab.RunScenario(sc, fabric.RunOptions{Budget: 100, Window: 0})
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if res.Stop == fabric.StopFault {
		t.Fatalf("RunScenario stopped with Fault: %v", fab.Err())
	}

	report := fab.Report()
	if len(report) < 2 {
		t.Fatalf("len(report) = %d, want at least 2 journeys", len(report))
	}

	// First injection at t1+100ms should be dropped at sw2 due to ingress filtering.
	j1 := report[0]
	if len(j1.Entries) == 0 {
		t.Fatal("first journey has no entries")
	}
	last1 := j1.Entries[len(j1.Entries)-1]
	if last1.Kind != fabric.EntryDrop || last1.Reason != bridge.ReasonIngressFilter || last1.Device != "sw2" {
		t.Fatalf("first journey last entry = %+v, want EntryDrop at sw2 with ingress-filter reason", last1)
	}

	// Second injection at t2+100ms should be delivered to h2.
	j2 := report[1]
	if len(j2.Deliveries) == 0 {
		t.Fatalf("second journey not delivered: entries = %+v", j2.Entries)
	}
	if j2.Deliveries[0].Host != "h2" {
		t.Errorf("second journey delivered to %s, want h2", j2.Deliveries[0].Host)
	}
}

func TestConfigureActionValidateAndDiff(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, _ := b.Build()

	cfg1 := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
			},
		},
	}
	cfg2 := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "prod", 20: "qa"},
			},
		},
	}

	// Validation
	if err := (fabric.ConfigureAction{Node: ""}).Validate(); err == nil {
		t.Error("ConfigureAction.Validate accepted empty node")
	}
	if err := (fabric.Action{ID: "scenario_test-31", At: t0, Kind: fabric.ActionConfigure, Configure: nil}).Validate(); err == nil {
		t.Error("Action.Validate accepted ActionConfigure with nil Configure")
	}
	if err := (fabric.Action{ID: "scenario_test-32", At: t0, Kind: fabric.ActionConfigure, Configure: &fabric.ConfigureAction{Node: "sw1"}, Fault: &fabric.FaultAction{}}).Validate(); err == nil {
		t.Error("Action.Validate accepted multiple payloads")
	}
	valid := fabric.Action{ID: "scenario_test-33", At: t0, Kind: fabric.ActionConfigure, Configure: new(assembly.ConfigureAction("sw1", cfg1))}
	if err := valid.Validate(); err != nil {
		t.Errorf("Action.Validate rejected valid action: %v", err)
	}

	// Diff: changed node
	act1 := fabric.Action{ID: "scenario_test-34", Index: 0, At: t0, Kind: fabric.ActionConfigure, Configure: new(assembly.ConfigureAction("sw1", cfg1))}
	act2 := fabric.Action{ID: "scenario_test-34", Index: 0, At: t0, Kind: fabric.ActionConfigure, Configure: new(assembly.ConfigureAction("sw2", cfg1))}
	diffNode := act1.Diff(act2)
	var foundNodeChange bool
	for _, c := range diffNode {
		if c.Field == "configure.node" && c.Subject.Kind == "scenario.action" && c.Subject.Key == act1.ID {
			foundNodeChange = true
			if c.From.Canonical() != "sw1" || c.To.Canonical() != "sw2" {
				t.Errorf("configure.node diff = %v -> %v, want sw1 -> sw2", c.From, c.To)
			}
		}
	}
	if !foundNodeChange {
		t.Fatalf("diff missing configure.node change: %+v", diffNode)
	}

	// Diff: same node and index, changed config. The two VLAN renames must stay
	// distinguishable through the nested action/ID/inner-kind/inner-key.
	act3 := fabric.Action{ID: "scenario_test-34", Index: 0, At: t0, Kind: fabric.ActionConfigure, Configure: new(assembly.ConfigureAction("sw1", cfg2))}
	diffConfig := act1.Diff(act3)
	wantKeys := []string{`"scenario_test-34"/"vlan"/"10"`, `"scenario_test-34"/"vlan"/"20"`}
	for _, wantKey := range wantKeys {
		var found bool
		for _, c := range diffConfig {
			if c.Subject.Kind != "scenario.action" {
				t.Errorf("change subject kind = %q, want scenario.action", c.Subject.Kind)
			}
			if c.Subject.Key == wantKey {
				found = true
				if c.Layer != bridge.LayerNameVLAN || c.Field != "name" {
					t.Errorf("change %s layer/field = (%v, %q), want (%v, name)", wantKey, c.Layer, c.Field, bridge.LayerNameVLAN)
				}
			}
		}
		if !found {
			t.Fatalf("diff missing change for nested key %q: %+v", wantKey, diffConfig)
		}
	}

	// DiffScenarios carries the same nested key.
	scA := fabric.Scenario{
		Name:    "configure-a",
		Actions: []fabric.Action{{ID: "scenario_test-37", At: t0, Index: 1, Kind: fabric.ActionConfigure, Configure: new(assembly.ConfigureAction("sw1", cfg1))}},
	}
	scB := scA.Clone()
	scB.Actions[0].Configure = new(assembly.ConfigureAction("sw1", cfg2))
	scChanges, err := fabric.DiffScenarios(scA, scB)
	if err != nil {
		t.Fatalf("DiffScenarios: %v", err)
	}
	wantScenarioKey := `"scenario_test-37"/"vlan"/"10"`
	if !slices.ContainsFunc(scChanges, func(c trace.Change) bool {
		return c.Subject.Kind == "scenario.action" && c.Subject.Key == wantScenarioKey && c.Field == "name"
	}) {
		t.Fatalf("DiffScenarios missing change for nested key %q: %+v", wantScenarioKey, scChanges)
	}
}

func TestActionCloneInjectionAndFaultIsolation(t *testing.T) {
	original := fabric.Action{
		ID: "scenario_test-38",
		At: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Kind: fabric.ActionInject,
		Inject: &fabric.Injection{Frame: ethernet.Frame{Tags: []vlan.Tag{{VID: 10}}, Payload: []byte{1}}, Packet: &fabric.Packet{Payload: []byte{2}}},
	}
	clone := original.Clone()
	if &clone.Inject.Frame.Tags[0] != &original.Inject.Frame.Tags[0] || &clone.Inject.Frame.Payload[0] != &original.Inject.Frame.Payload[0] {
		t.Fatal("clone did not share immutable frame buffers")
	}
	clone.Inject.Frame.Tags = []vlan.Tag{{VID: 20}}
	clone.Inject.Frame.Payload = []byte{3}
	clone.Inject.Packet.Payload[0] = 4
	if original.Inject.Frame.Tags[0].VID != 10 || original.Inject.Frame.Payload[0] != 1 || original.Inject.Packet.Payload[0] != 2 {
		t.Fatal("clone shares injection buffers")
	}
	fault := fabric.Action{ID: "scenario_test-39", Kind: fabric.ActionFault, Fault: &fabric.FaultAction{Fault: cablemodel.Fault{Kind: cablemodel.FaultLoseSequence, Sequence: []uint{1, 3}}}}
	copyFault := fault.Clone()
	copyFault.Fault.Fault.Sequence[0] = 2
	if fault.Fault.Fault.Sequence[0] != 1 {
		t.Fatal("clone shares loss sequence")
	}
}

func TestFaultActionValidateFaultParameters(t *testing.T) {
	base := fabric.FaultAction{A: fabric.Endpoint{Node: "a"}, B: fabric.Endpoint{Node: "b"}, Fault: cablemodel.Fault{Kind: cablemodel.FaultCut}}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []cablemodel.Fault{{Kind: "unknown"}, {Kind: cablemodel.FaultCut, N: 1}, {Kind: cablemodel.FaultLoseEveryNth}, {Kind: cablemodel.FaultLoseSequence, Sequence: []uint{0}}} {
		action := base
		action.Fault = fault
		if err := action.Validate(); err == nil {
			t.Errorf("fault %+v accepted", fault)
		}
	}
}
