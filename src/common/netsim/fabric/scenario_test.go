package fabric

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

func newTestTwoSwitchSTPFabric(t *testing.T, t0 time.Time) (*Fabric, Config) {
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

	cfg := Config{
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
		Hosts: map[string]Host{
			"h1": {Address: macH1},
		},
		Cables: []Cable{
			{
				A: Endpoint{Node: "sw1", Port: "1/1/1"},
				B: Endpoint{Node: "sw2", Port: "1/1/1"},
			},
			{
				A: Endpoint{Node: "sw1", Port: "1/1/2"},
				B: Endpoint{Node: "h1"},
			},
		},
	}

	fab, err := New(cfg)
	if err != nil {
		t.Fatalf("New STP fabric: %v", err)
	}
	return fab, cfg
}

func TestScenarioSameTimeDeclarationOrder(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab, cfg := newTestTwoHostFabric(t, t0)

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

	sc := Scenario{
		Name: "same-time-order",
		Spec: ConstructionSpec{
			Start:         cfg.Start,
			Switches:      fab.Spec().Switches,
			Hosts:         cfg.Hosts,
			Cables:        cfg.Cables,
			PhyAssumption: cfg.PhyAssumption,
		},
		Actions: []Action{
			{
				At:   t0,
				Kind: ActionInject,
				Inject: &Injection{
					Origin: Endpoint{Node: "h1"},
					Frame:  frame1,
				},
			},
			{
				At:   t0,
				Kind: ActionInject,
				Inject: &Injection{
					Origin: Endpoint{Node: "h2"},
					Frame:  frame2,
				},
			},
		},
		Budget: 10,
	}

	res, err := fab.RunScenario(sc)
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if res.Stop != StopQueueDrained {
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

	act1 := Action{
		At:   t0,
		Kind: ActionInject,
		Inject: &Injection{
			Origin: Endpoint{Node: "h1"},
			Frame:  frame,
		},
	}
	act2 := Action{
		At:   t0.Add(time.Second),
		Kind: ActionInject,
		Inject: &Injection{
			Origin: Endpoint{Node: "h2"},
			Frame:  frame,
		},
	}

	// Deliberately shuffled slice: later action first
	sc := Scenario{
		Name:    "shuffled",
		Actions: []Action{act2, act1},
		Budget:  10,
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
	validInject := &Injection{
		At:     t0,
		Origin: Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Payload: []byte("x")},
	}
	validFault := &FaultAction{
		A:     Endpoint{Node: "sw1", Port: "1/1/1"},
		B:     Endpoint{Node: "sw2", Port: "1/1/1"},
		Fault: Fault{Kind: FaultCut},
	}

	cases := []struct {
		name string
		sc   Scenario
	}{
		{
			name: "empty name",
			sc: Scenario{
				Name:   "",
				Budget: 10,
			},
		},
		{
			name: "zero budget",
			sc: Scenario{
				Name:   "zero-budget",
				Budget: 0,
			},
		},
		{
			name: "negative budget",
			sc: Scenario{
				Name:   "negative-budget",
				Budget: -5,
			},
		},
		{
			name: "negative window",
			sc: Scenario{
				Name:   "negative-window",
				Budget: 10,
				Window: -1,
			},
		},
		{
			name: "kind with no pointer",
			sc: Scenario{
				Name:   "no-pointer",
				Budget: 10,
				Actions: []Action{
					{At: t0, Kind: ActionInject, Inject: nil},
				},
			},
		},
		{
			name: "kind with two pointers",
			sc: Scenario{
				Name:   "two-pointers",
				Budget: 10,
				Actions: []Action{
					{At: t0, Kind: ActionInject, Inject: validInject, Fault: validFault},
				},
			},
		},
		{
			name: "partially numbered index slice",
			sc: Scenario{
				Name:   "partially-numbered",
				Budget: 10,
				Actions: []Action{
					{At: t0, Index: 1, Kind: ActionInject, Inject: validInject},
					{At: t0, Index: 0, Kind: ActionInject, Inject: validInject},
				},
			},
		},
		{
			name: "inner At disagrees with Action.At",
			sc: Scenario{
				Name:   "disagreeing-inner-at",
				Budget: 10,
				Actions: []Action{
					{
						At:   t0,
						Kind: ActionInject,
						Inject: &Injection{
							At:     t0.Add(time.Hour),
							Origin: Endpoint{Node: "h1"},
							Frame:  ethernet.Frame{Payload: []byte("x")},
						},
					},
				},
			},
		},
		{
			name: "action before Spec.Start",
			sc: Scenario{
				Name: "before-spec-start",
				Spec: ConstructionSpec{Start: t0},
				Actions: []Action{
					{
						At:     t0.Add(-time.Second),
						Kind:   ActionInject,
						Inject: validInject,
					},
				},
				Budget: 10,
			},
		},
		{
			name: "duplicate numbered at and index",
			sc: Scenario{
				Name:   "duplicate-at-index",
				Budget: 10,
				Actions: []Action{
					{
						At:     t0,
						Index:  1,
						Kind:   ActionInject,
						Inject: validInject,
					},
					{
						At:    t0,
						Index: 1,
						Kind:  ActionFault,
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
	validInject := &Injection{
		At:     t0,
		Origin: Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Payload: []byte("x")},
	}
	validFault := &FaultAction{
		A:     Endpoint{Node: "sw1", Port: "1/1/1"},
		B:     Endpoint{Node: "sw2", Port: "1/1/1"},
		Fault: Fault{Kind: FaultCut},
	}

	scValidSameTimeDiffIndex := Scenario{
		Name:   "valid-same-time-diff-index",
		Budget: 10,
		Actions: []Action{
			{At: t0, Index: 1, Kind: ActionInject, Inject: validInject},
			{At: t0, Index: 2, Kind: ActionFault, Fault: validFault},
		},
	}
	if err := scValidSameTimeDiffIndex.Validate(); err != nil {
		t.Fatalf("Validate failed for valid distinct indices at same time: %v", err)
	}

	validFaultLater := &FaultAction{
		A:     Endpoint{Node: "sw1", Port: "1/1/1"},
		B:     Endpoint{Node: "sw2", Port: "1/1/1"},
		Fault: Fault{Kind: FaultCut},
	}
	scValidDiffTimeSameIndex := Scenario{
		Name:   "valid-diff-time-same-index",
		Budget: 10,
		Actions: []Action{
			{At: t0, Index: 1, Kind: ActionInject, Inject: validInject},
			{At: t0.Add(time.Second), Index: 1, Kind: ActionFault, Fault: validFaultLater},
		},
	}
	if err := scValidDiffTimeSameIndex.Validate(); err != nil {
		t.Fatalf("Validate failed for valid same index at different times: %v", err)
	}
}

func TestScenarioDiffEveryField(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	base := Scenario{
		Name:   "base-scenario",
		Budget: 10,
		Window: 3,
		Spec:   ConstructionSpec{Start: t0},
		Actions: []Action{
			{
				At:    t0,
				Index: 1,
				Kind:  ActionInject,
				Inject: &Injection{
					At:     t0,
					Origin: Endpoint{Node: "h1"},
					Frame:  ethernet.Frame{Payload: []byte("a")},
				},
			},
		},
	}

	t.Run("name", func(t *testing.T) {
		mut := base.Clone()
		mut.Name = "different-name"
		changes, err := DiffScenarios(base, mut)
		if err != nil {
			t.Fatalf("DiffScenarios: %v", err)
		}
		if !slices.ContainsFunc(changes, func(ch trace.Change) bool { return ch.Field == "name" && ch.Subject.Kind == "scenario" }) {
			t.Errorf("missing name change: %+v", changes)
		}
	})

	t.Run("budget", func(t *testing.T) {
		mut := base.Clone()
		mut.Budget = 50
		changes, err := DiffScenarios(base, mut)
		if err != nil {
			t.Fatalf("DiffScenarios: %v", err)
		}
		if !slices.ContainsFunc(changes, func(ch trace.Change) bool { return ch.Field == "budget" && ch.Subject.Kind == "scenario" }) {
			t.Errorf("missing budget change: %+v", changes)
		}
	})

	t.Run("window", func(t *testing.T) {
		mut := base.Clone()
		mut.Window = 5
		changes, err := DiffScenarios(base, mut)
		if err != nil {
			t.Fatalf("DiffScenarios: %v", err)
		}
		if len(changes) != 1 {
			t.Fatalf("len(changes) = %d, want 1", len(changes))
		}
		if changes[0].Field != "window" || changes[0].Subject.Kind != "scenario" {
			t.Errorf("changes[0] = %+v, want window change under scenario", changes[0])
		}
	})

	t.Run("action at", func(t *testing.T) {
		mut := base.Clone()
		mut.Actions[0].At = t0.Add(time.Second)
		mut.Actions[0].Inject.At = t0.Add(time.Second)
		changes, err := DiffScenarios(base, mut)
		if err != nil {
			t.Fatalf("DiffScenarios: %v", err)
		}
		if !slices.ContainsFunc(changes, func(ch trace.Change) bool { return ch.Field == "at" && ch.Subject.Kind == "scenario.action" }) {
			t.Errorf("missing action at change: %+v", changes)
		}
	})

	t.Run("action kind", func(t *testing.T) {
		mut := base.Clone()
		mut.Actions[0].Kind = ActionFault
		mut.Actions[0].Inject = nil
		mut.Actions[0].Fault = &FaultAction{
			A:     Endpoint{Node: "sw1", Port: "1/1/1"},
			B:     Endpoint{Node: "sw2", Port: "1/1/1"},
			Fault: Fault{Kind: FaultCut},
		}
		changes, err := DiffScenarios(base, mut)
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
	raw := Scenario{
		Name:   "raw-scenario",
		Budget: 10,
		Window: 3,
		Spec:   ConstructionSpec{Start: t0},
		Actions: []Action{
			{
				At:   t0,
				Kind: ActionInject,
				Inject: &Injection{
					Origin: Endpoint{Node: "h1"},
					Frame:  ethernet.Frame{Payload: []byte("raw")},
				},
			},
		},
	}

	norm, err := raw.Normalize()
	if err != nil {
		t.Fatalf("raw.Normalize: %v", err)
	}

	changes, err := DiffScenarios(raw, norm)
	if err != nil {
		t.Fatalf("DiffScenarios(raw, norm): %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("DiffScenarios(raw, norm) returned %d changes, want 0: %+v", len(changes), changes)
	}
}

func TestReplayRefusesDifferentContract(t *testing.T) {
	spec := ReplaySpec{
		Contract: "netsim-fabric/v2-invalid",
	}

	_, err := Replay(spec)
	if err == nil {
		t.Fatal("Replay succeeded with wrong contract, want error")
	}
}

func TestScenarioSpanningTreeWindowZeroStopsAtBudget(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab, cfg := newTestTwoSwitchSTPFabric(t, t0)

	sc := Scenario{
		Name: "stp-window-zero",
		Spec: ConstructionSpec{
			Start:         cfg.Start,
			Switches:      fab.Spec().Switches,
			Hosts:         cfg.Hosts,
			Cables:        cfg.Cables,
			PhyAssumption: cfg.PhyAssumption,
		},
		Budget: 5,
		Window: 0,
	}

	res, err := fab.RunScenario(sc)
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}
	if res.Stop != StopBudget {
		t.Errorf("res.Stop = %v, want StopBudget", res.Stop)
	}
	if res.Steps != 5 {
		t.Errorf("res.Steps = %d, want 5", res.Steps)
	}
}

func TestScenarioReplayDoubleExecutionEquality(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab, cfg := newTestTwoSwitchSTPFabric(t, t0)

	frame := ethernet.Frame{
		Src:     netaddr.MAC{0, 0, 0, 0, 0, 1},
		Dst:     netaddr.MAC{0, 0, 0, 0, 1, 1},
		Payload: []byte("journey-payload"),
	}

	sc := Scenario{
		Name: "cut-and-inject",
		Spec: ConstructionSpec{
			Start:         cfg.Start,
			Switches:      fab.Spec().Switches,
			Hosts:         cfg.Hosts,
			Cables:        cfg.Cables,
			PhyAssumption: cfg.PhyAssumption,
		},
		Actions: []Action{
			{
				At:   t0.Add(time.Second),
				Kind: ActionFault,
				Fault: &FaultAction{
					A:     Endpoint{Node: "sw1", Port: "1/1/1"},
					B:     Endpoint{Node: "sw2", Port: "1/1/1"},
					Fault: Fault{Kind: FaultCut},
				},
			},
			{
				At:   t0.Add(2 * time.Second),
				Kind: ActionInject,
				Inject: &Injection{
					Origin: Endpoint{Node: "h1"},
					Frame:  frame,
				},
			},
		},
		Budget: 20,
		Window: 3,
	}

	res1, err := fab.RunScenario(sc)
	if err != nil {
		t.Fatalf("RunScenario 1: %v", err)
	}

	res2, err := Replay(res1.Replay)
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
	fab2, err := NewWithSpec(res1.Replay.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}
	_, _ = fab2.RunScenario(res1.Replay.Scenario)
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

func newTestThreeHostFabric(t *testing.T, t0 time.Time) (*Fabric, Config) {
	t.Helper()
	macH1 := netaddr.MAC{0, 0, 0, 0, 0, 1}
	macH2 := netaddr.MAC{0, 0, 0, 0, 0, 2}
	macH3 := netaddr.MAC{0, 0, 0, 0, 0, 3}

	ports, _ := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()

	cfg := Config{
		Start:         t0,
		PhyAssumption: testPhyAssumption(),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  ports,
				Bridge: &bridge.Config{},
			},
		},
		Hosts: map[string]Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
			"h3": {Address: macH3},
		},
		Cables: []Cable{
			{A: Endpoint{Node: "sw1", Port: "1/1/1"}, B: Endpoint{Node: "h1"}},
			{A: Endpoint{Node: "sw1", Port: "1/1/2"}, B: Endpoint{Node: "h2"}},
			{A: Endpoint{Node: "sw1", Port: "1/1/3"}, B: Endpoint{Node: "h3"}},
		},
	}

	fab, err := New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}
	return fab, cfg
}

func TestScenarioReplaySeededFabricEquality(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab, cfg := newTestThreeHostFabric(t, t0)

	macH2 := cfg.Hosts["h2"].Address
	if err := fab.Switch("sw1").Learn([]bridge.Seed{
		{MAC: macH2, Port: "1/1/2", Lifetime: bridge.Static},
	}); err != nil {
		t.Fatalf("Learn: %v", err)
	}

	frame := ethernet.Frame{
		Src:     cfg.Hosts["h1"].Address,
		Dst:     macH2,
		Payload: []byte("seeded-payload"),
	}

	sc := Scenario{
		Name: "seeded-unicast",
		Spec: ConstructionSpec{
			Start:         cfg.Start,
			Switches:      fab.Spec().Switches,
			Hosts:         cfg.Hosts,
			Cables:        cfg.Cables,
			PhyAssumption: cfg.PhyAssumption,
		},
		Actions: []Action{
			{
				At:   t0.Add(time.Second),
				Kind: ActionInject,
				Inject: &Injection{
					Origin: Endpoint{Node: "h1"},
					Frame:  frame,
				},
			},
		},
		Budget: 20,
		Window: 3,
	}

	res1, err := fab.RunScenario(sc)
	if err != nil {
		t.Fatalf("RunScenario 1: %v", err)
	}

	res2, err := Replay(res1.Replay)
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
	fab2, err := NewWithSpec(res1.Replay.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}
	_, _ = fab2.RunScenario(res1.Replay.Scenario)
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

	// Dropping seeds from the recorded replay spec must fail to match seeded execution.
	droppedSpec := res1.Replay
	swSpec := droppedSpec.Spec.Switches["sw1"]
	swSpec.Seeds = nil
	droppedSpec.Spec.Switches["sw1"] = swSpec

	droppedRes, err := Replay(droppedSpec)
	if err != nil {
		t.Fatalf("Replay dropped seeds: %v", err)
	}
	if slices.Equal(res1.Fingerprints, droppedRes.Fingerprints) {
		t.Errorf("expected fingerprints to differ when seeds are dropped")
	}
	droppedFab, err := NewWithSpec(droppedSpec.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec dropped seeds: %v", err)
	}
	_, _ = droppedFab.RunScenario(droppedSpec.Scenario)
	droppedJourneys := droppedFab.Report()
	if len(droppedJourneys[0].Entries) == len(j1[0].Entries) {
		t.Errorf("expected unseeded flooding to produce more journey entries than seeded unicast (%d == %d)", len(droppedJourneys[0].Entries), len(j1[0].Entries))
	}
}

func TestScenarioMutationIndependence(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab, cfg := newTestTwoHostFabric(t, t0)

	actions := []Action{
		{
			At:   t0,
			Kind: ActionInject,
			Inject: &Injection{
				Origin: Endpoint{Node: "h1"},
				Frame:  ethernet.Frame{Payload: []byte("initial")},
			},
		},
	}

	sc := Scenario{
		Name: "mutation-isolation",
		Spec: ConstructionSpec{
			Start:         cfg.Start,
			Switches:      fab.Spec().Switches,
			Hosts:         cfg.Hosts,
			Cables:        cfg.Cables,
			PhyAssumption: cfg.PhyAssumption,
		},
		Actions: actions,
		Budget:  10,
	}

	res, err := fab.RunScenario(sc)
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}

	// Mutate caller's slice
	actions[0].At = t0.Add(time.Hour)
	actions[0].Inject.Origin = Endpoint{Node: "h2"}

	replayAction := res.Replay.Scenario.Actions[0]
	if !replayAction.At.Equal(t0) {
		t.Errorf("res.Replay.Scenario action was mutated: At = %v, want %v", replayAction.At, t0)
	}
	if replayAction.Inject.Origin.Node != "h1" {
		t.Errorf("res.Replay.Scenario origin was mutated: Node = %v, want h1", replayAction.Inject.Origin.Node)
	}
}

func TestScenarioForkIsolation(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	fab, _ := newTestTwoSwitchSTPFabric(t, t0)

	// Step source once
	fab.Step()

	// Fork
	forked := fab.Fork()

	// Run fork to completion
	_ = forked.Run(100)

	// Verify source fabric steps and clock are unaffected
	sourceRes := fab.Run(5)
	if sourceRes.Steps != 5 {
		t.Errorf("sourceRes.Steps = %d, want 5", sourceRes.Steps)
	}
}
