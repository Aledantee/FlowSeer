package search

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func makeMinimizeFabrics(t *testing.T) (cur, cand *fabric.Fabric, h1MAC, h2MAC, h3MAC netaddr.MAC) {
	t.Helper()
	h1MAC = netaddr.MAC{0x02, 0, 0, 0, 0, 1}
	h2MAC = netaddr.MAC{0x02, 0, 0, 0, 0, 2}
	h3MAC = netaddr.MAC{0x02, 0, 0, 0, 0, 3}

	buildFabric := func(vid2 uint16) *fabric.Fabric {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		pTable, err := b.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}

		v10 := vlan.ID(10)
		v2 := vlan.ID(vid2)
		swCfg := vswitch.Config{
			Ports: pTable,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{
						10: "VLAN10",
						20: "VLAN20",
					},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {
							PVID:     &v10,
							Untagged: []vlan.ID{10},
						},
						"1/1/2": {
							PVID:     &v2,
							Untagged: []vlan.ID{v2},
						},
						"1/1/3": {
							PVID:     &v10,
							Untagged: []vlan.ID{10},
						},
					},
				},
			},
		}

		v99 := vlan.ID(99)
		cfg := fabric.Config{
			PhyAssumption: &fabric.PhyAssumption{
				Medium: fabric.TwistedPair,
				Ethernet: phy.Ethernet{
					SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
					AutoNegotiationSupported: phy.CapabilitySupported,
					Setting:                  &phy.Setting{AutoNegotiation: true},
				},
			},
			Switches: map[string]vswitch.Config{
				"sw1": swCfg,
			},
			Hosts: map[string]fabric.Host{
				"h1": {Address: h1MAC},
				"h2": {Address: h2MAC},
				"h3": {Address: h3MAC, VLAN: &v99},
			},
			Cables: []fabric.Cable{
				{
					A:            fabric.Endpoint{Node: "h1"},
					B:            fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
					LengthMeters: 5,
					Medium:       fabric.TwistedPair,
				},
				{
					A:            fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
					B:            fabric.Endpoint{Node: "h2"},
					LengthMeters: 5,
					Medium:       fabric.TwistedPair,
				},
				{
					A:            fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
					B:            fabric.Endpoint{Node: "h3"},
					LengthMeters: 5,
					Medium:       fabric.TwistedPair,
				},
			},
		}

		fab, err := fabric.New(cfg)
		if err != nil {
			t.Fatalf("fabric.New: %v", err)
		}
		return fab
	}

	cur = buildFabric(10)
	cand = buildFabric(20)
	return
}

func TestMinimizePaddedCounterexampleReducesToMinimal(t *testing.T) {
	t.Parallel()

	cur, cand, h1MAC, h2MAC, h3MAC := makeMinimizeFabrics(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Injections:
	// inj0: h3 (VLAN 99) -> h1 (irrelevant: dropped at sw1:1/1/3 on both fabrics)
	// inj1: h1 -> h2 (critical: delivered on cur, dropped on cand due to VLAN mismatch)
	// inj2: h3 (VLAN 99) -> h2 (irrelevant: dropped at sw1:1/1/3 on both fabrics)
	// inj3: h3 (VLAN 99) -> h1 (irrelevant: dropped at sw1:1/1/3 on both fabrics)
	inj0 := fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h3"},
		Frame: ethernet.Frame{
			Dst:       h1MAC,
			Src:       h3MAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("pad-0"),
		},
	}
	inj1 := fabric.Injection{
		At:     t0.Add(time.Millisecond),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       h2MAC,
			Src:       h1MAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("critical"),
		},
	}
	inj2 := fabric.Injection{
		At:     t0.Add(2 * time.Millisecond),
		Origin: fabric.Endpoint{Node: "h3"},
		Frame: ethernet.Frame{
			Dst:       h2MAC,
			Src:       h3MAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("pad-2"),
		},
	}
	inj3 := fabric.Injection{
		At:     t0.Add(3 * time.Millisecond),
		Origin: fabric.Endpoint{Node: "h3"},
		Frame: ethernet.Frame{
			Dst:       h1MAC,
			Src:       h3MAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("pad-3"),
		},
	}

	paddedCandidate := Candidate{
		Tuple:   "padded-candidate",
		Actions: []fabric.Action{{At: inj0.At, Kind: fabric.ActionInject, Inject: &inj0}, {At: inj1.At, Kind: fabric.ActionInject, Inject: &inj1}, {At: inj2.At, Kind: fabric.ActionInject, Inject: &inj2}, {At: inj3.At, Kind: fabric.ActionInject, Inject: &inj3}},
	}

	initialCmp := runCompare(cur, cand, paddedCandidate, 10)
	if initialCmp.Disposition != analysis.Different {
		t.Fatalf("paddedCandidate disposition = %v, want Different", initialCmp.Disposition)
	}

	result, err := Minimize(cur, cand, paddedCandidate, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertMinimizationReplay(t, cur, cand, paddedCandidate, 10, result)
	reduced, minimality := result.Candidate, result.Minimality

	if minimality != Minimal {
		t.Fatalf("minimality = %v, want %v", minimality, Minimal)
	}
	if len(reduced.Actions) != 1 {
		t.Fatalf("len(reduced.Actions) = %d, want 1 (reduced to single critical injection)", len(reduced.Actions))
	}
	if reduced.Actions[0].Index != 2 || result.Target.Injection.Index != 2 {
		t.Errorf("retained index = %d, target index %d, want 2 after earlier equivalent injection removal", reduced.Actions[0].Index, result.Target.Injection.Index)
	}
	if string(reduced.Actions[0].Inject.Frame.Payload) != "critical" {
		t.Errorf("retained injection payload = %q, want 'critical'", string(reduced.Actions[0].Inject.Frame.Payload))
	}

	// Verify reduced candidate still exhibits the exact same observable
	reducedCmp := runCompare(cur, cand, reduced, 10)
	if reducedCmp.Disposition != analysis.Different {
		t.Errorf("reduced candidate disposition = %v, want Different", reducedCmp.Disposition)
	}
	if reducedCmp.Difference.Observable != initialCmp.Difference.Observable {
		t.Errorf("reduced candidate observable = %q, want %q", reducedCmp.Difference.Observable, initialCmp.Difference.Observable)
	}
	reduced.Actions[0].Inject.Frame.Payload[0] = 'X'
	if string(paddedCandidate.Actions[1].Inject.Frame.Payload) != "critical" || paddedCandidate.Actions[1].Index != 0 {
		t.Fatalf("original target action = %+v, want independent critical payload and unnumbered index", paddedCandidate.Actions[1])
	}
}

func TestMinimizeLimitReachedReportedWithPartialReduction(t *testing.T) {
	t.Parallel()

	cur, cand, h1MAC, h2MAC, h3MAC := makeMinimizeFabrics(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	makeInj := func(offset int, payload string, node string, dst netaddr.MAC) fabric.Injection {
		src := h3MAC
		if node == "h1" {
			src = h1MAC
		}
		return fabric.Injection{
			At:     t0.Add(time.Duration(offset) * time.Millisecond),
			Origin: fabric.Endpoint{Node: node},
			Frame: ethernet.Frame{
				Dst:       dst,
				Src:       src,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte(payload),
			},
		}
	}

	// 5 injections, only offset 2 (from h1 to h2) is critical
	candInjs := []fabric.Injection{
		makeInj(0, "pad-0", "h3", h1MAC),
		makeInj(1, "pad-1", "h3", h1MAC),
		makeInj(2, "critical", "h1", h2MAC),
		makeInj(3, "pad-3", "h3", h1MAC),
		makeInj(4, "pad-4", "h3", h1MAC),
	}
	actions := make([]fabric.Action, len(candInjs))
	for i := range candInjs {
		actions[i] = fabric.Action{At: candInjs[i].At, Kind: fabric.ActionInject, Inject: &candInjs[i]}
	}

	paddedCandidate := Candidate{
		Tuple:   "padded-candidate",
		Actions: actions,
	}

	// Max 1 trial: will only attempt 1 removal and halt
	result, err := MinimizeWithLimit(cur, cand, paddedCandidate, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertMinimizationReplay(t, cur, cand, paddedCandidate, 10, result)
	reduced, minimality := result.Candidate, result.Minimality

	if minimality != LimitReached {
		t.Fatalf("minimality = %v, want %v", minimality, LimitReached)
	}
	if len(reduced.Actions) != 4 {
		t.Fatalf("len(reduced.Actions) = %d, want 4 after one successful removal", len(reduced.Actions))
	}
}

func TestMinimizeRejectsReductionThatChangesObservable(t *testing.T) {
	t.Parallel()

	cur, cand, h1MAC, h2MAC, _ := makeMinimizeFabrics(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Only 1 critical injection
	cand1 := Candidate{
		Tuple: "only-critical",
		Actions: []fabric.Action{
			{At: t0, Kind: fabric.ActionInject, Inject: &fabric.Injection{
				At:     t0,
				Origin: fabric.Endpoint{Node: "h1"},
				Frame: ethernet.Frame{
					Dst:       h2MAC,
					Src:       h1MAC,
					EtherType: ethernet.EtherTypeIPv4,
					Payload:   []byte("critical"),
				},
			}},
		},
	}

	result, err := Minimize(cur, cand, cand1, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertMinimizationReplay(t, cur, cand, cand1, 10, result)
	reduced, minimality := result.Candidate, result.Minimality
	if minimality != Minimal {
		t.Errorf("minimality = %v, want %v", minimality, Minimal)
	}
	if len(reduced.Actions) != 1 {
		t.Errorf("critical element was removed: %d elements remain", len(reduced.Actions))
	}
}

func TestMinimizeLimitReachedUpdatesReducedTuple(t *testing.T) {
	t.Parallel()

	cur, cand, h1MAC, h2MAC, h3MAC := makeMinimizeFabrics(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	makeInj := func(offset int, payload string, node string, dst netaddr.MAC) fabric.Injection {
		src := h3MAC
		if node == "h1" {
			src = h1MAC
		}
		return fabric.Injection{
			At:     t0.Add(time.Duration(offset) * time.Millisecond),
			Origin: fabric.Endpoint{Node: node},
			Frame: ethernet.Frame{
				Dst:       dst,
				Src:       src,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte(payload),
			},
		}
	}

	candInjs := []fabric.Injection{
		makeInj(0, "pad-0", "h3", h1MAC),
		makeInj(1, "pad-1", "h3", h1MAC),
		makeInj(2, "critical", "h1", h2MAC),
		makeInj(3, "pad-3", "h3", h1MAC),
		makeInj(4, "pad-4", "h3", h1MAC),
	}
	actions := make([]fabric.Action, len(candInjs))
	for i := range candInjs {
		actions[i] = fabric.Action{At: candInjs[i].At, Kind: fabric.ActionInject, Inject: &candInjs[i]}
	}

	paddedCandidate := Candidate{
		Tuple:   "original-unminimized-tuple",
		Actions: actions,
	}

	result, err := MinimizeWithLimit(cur, cand, paddedCandidate, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertMinimizationReplay(t, cur, cand, paddedCandidate, 10, result)
	reduced, minimality := result.Candidate, result.Minimality
	if minimality != LimitReached {
		t.Fatalf("minimality = %v, want %v", minimality, LimitReached)
	}
	if reduced.Tuple == "original-unminimized-tuple" {
		t.Fatalf("reduced.Tuple = %q, want updated tuple reflecting partial reduction", reduced.Tuple)
	}
	wantTuple := Tuple("h3: -> 02:00:00:00:00:01 etype:0x0800 payload:7061642d31; h1: -> 02:00:00:00:00:02 etype:0x0800 payload:637269746963616c; h3: -> 02:00:00:00:00:01 etype:0x0800 payload:7061642d33; h3: -> 02:00:00:00:00:01 etype:0x0800 payload:7061642d34")
	if reduced.Tuple != wantTuple {
		t.Errorf("reduced.Tuple = %q, want %q", reduced.Tuple, wantTuple)
	}
}

func makeFixedPointFabrics(t *testing.T) (cur, cand *fabric.Fabric, h1MAC, h2MAC, h3MAC netaddr.MAC) {
	t.Helper()
	h1MAC = netaddr.MAC{0x02, 0, 0, 0, 0, 1}
	h2MAC = netaddr.MAC{0x02, 0, 0, 0, 0, 2}
	h3MAC = netaddr.MAC{0x02, 0, 0, 0, 0, 3}

	buildFabric := func(vid2 uint16) *fabric.Fabric {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		pTable, err := b.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}

		v10 := vlan.ID(10)
		v2 := vlan.ID(vid2)
		swCfg := vswitch.Config{
			Ports: pTable,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{
						10: "VLAN10",
						20: "VLAN20",
					},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {
							PVID:     &v10,
							Untagged: []vlan.ID{10},
						},
						"1/1/2": {
							PVID:     &v2,
							Untagged: []vlan.ID{v2},
						},
						"1/1/3": {
							PVID:     &v10,
							Untagged: []vlan.ID{10},
						},
					},
				},
			},
		}

		cfg := fabric.Config{
			PhyAssumption: &fabric.PhyAssumption{
				Medium: fabric.TwistedPair,
				Ethernet: phy.Ethernet{
					SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
					AutoNegotiationSupported: phy.CapabilitySupported,
					Setting:                  &phy.Setting{AutoNegotiation: true},
				},
			},
			Switches: map[string]vswitch.Config{
				"sw1": swCfg,
			},
			Hosts: map[string]fabric.Host{
				"h1": {Address: h1MAC},
				"h2": {Address: h2MAC},
				"h3": {Address: h3MAC},
			},
			Cables: []fabric.Cable{
				{
					A:            fabric.Endpoint{Node: "h1"},
					B:            fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
					LengthMeters: 5,
					Medium:       fabric.TwistedPair,
				},
				{
					A:            fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
					B:            fabric.Endpoint{Node: "h2"},
					LengthMeters: 5,
					Medium:       fabric.TwistedPair,
				},
				{
					A:            fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
					B:            fabric.Endpoint{Node: "h3"},
					LengthMeters: 5,
					Medium:       fabric.TwistedPair,
				},
			},
		}

		fab, err := fabric.New(cfg)
		if err != nil {
			t.Fatalf("fabric.New: %v", err)
		}
		return fab
	}

	cur = buildFabric(10)
	cand = buildFabric(20)
	return
}

func TestMinimizeFixedPointSecondPassRemovals(t *testing.T) {
	t.Parallel()

	cur, cand, h1MAC, h2MAC, h3MAC := makeFixedPointFabrics(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// injA: h3 -> h3 (establishes h3MAC in FDB on sw1 port 1/1/3 without egress)
	injA := fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h3"},
		Frame: ethernet.Frame{
			Dst:       h3MAC,
			Src:       h3MAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("learn-h3"),
		},
	}
	// injB: h1 -> h3 (unicast to 1/1/3 when injA is present; floods to 1/1/2 and 1/1/3 when injA is absent)
	injB := fabric.Injection{
		At:     t0.Add(5 * time.Millisecond),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       h3MAC,
			Src:       h1MAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("traffic-to-h3"),
		},
	}
	// injC: h1 -> h2 (critical: delivered on cur, dropped on cand due to VLAN 20)
	injC := fabric.Injection{
		At:     t0.Add(10 * time.Millisecond),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       h2MAC,
			Src:       h1MAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("critical"),
		},
	}

	candidate := Candidate{
		Tuple:   "fixed-point-test",
		Actions: []fabric.Action{{At: injA.At, Kind: fabric.ActionInject, Inject: &injA}, {At: injB.At, Kind: fabric.ActionInject, Inject: &injB}, {At: injC.At, Kind: fabric.ActionInject, Inject: &injC}},
	}

	// Verify that a single pass (3 trials: try injA [fail], try injB [succeed], try injC [fail])
	// leaves injA in the candidate:
	singlePass, err := MinimizeWithLimit(cur, cand, candidate, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	assertMinimizationReplay(t, cur, cand, candidate, 10, singlePass)
	singlePassReduced, singlePassMinimality := singlePass.Candidate, singlePass.Minimality
	if singlePassMinimality != LimitReached {
		t.Fatalf("single pass minimality = %v, want %v", singlePassMinimality, LimitReached)
	}
	if len(singlePassReduced.Actions) != 2 {
		t.Fatalf("single pass len = %d, want 2 (injA unremovable on pass 1)", len(singlePassReduced.Actions))
	}

	// Full fixed-point minimization repeats passes until 0 removals, successfully removing injA on pass 2:
	result, err := Minimize(cur, cand, candidate, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertMinimizationReplay(t, cur, cand, candidate, 10, result)
	reduced, minimality := result.Candidate, result.Minimality
	if minimality != Minimal {
		t.Fatalf("minimality = %v, want %v", minimality, Minimal)
	}
	if len(reduced.Actions) != 1 {
		t.Fatalf("len(reduced.Actions) = %d, want 1; both A and B should be removed by fixed-point", len(reduced.Actions))
	}
	if string(reduced.Actions[0].Inject.Frame.Payload) != "critical" {
		t.Errorf("remaining injection payload = %q, want 'critical'", string(reduced.Actions[0].Inject.Frame.Payload))
	}
}

func TestMinimizePreservesInjectionIdentity(t *testing.T) {
	cases := []struct {
		name                    string
		firstIndex, secondIndex int
		secondOffset            time.Duration
	}{
		{"different timestamps", 0, 0, time.Millisecond},
		{"duplicate injection timestamp", 7, 8, 0},
		{"reused index different timestamps", 7, 7, time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current, expected, src, dst, _ := makeMinimizeFabrics(t)
			at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			first := fabric.Injection{At: at, Origin: fabric.Endpoint{Node: "h1"}, Frame: ethernet.Frame{Src: src, Dst: dst, EtherType: ethernet.EtherTypeIPv4, Payload: []byte("same")}}
			second := first
			second.At = at.Add(tc.secondOffset)
			input := Candidate{Actions: []fabric.Action{{Kind: fabric.ActionInject, Index: tc.firstIndex, At: first.At, Inject: &first}, {Kind: fabric.ActionInject, Index: tc.secondIndex, At: second.At, Inject: &second}}}
			initial := runCompare(current, expected, input, 30)
			if initial.Disposition != analysis.Different {
				t.Fatalf("disposition = %s, want Different", initial.Disposition)
			}
			onlySecond := Candidate{Actions: []fabric.Action{input.ToScenario(30).Actions[1]}}
			exchanged := runCompare(current, expected, onlySecond, 30)
			if exchanged.Disposition != analysis.Different || exchanged.Difference != initial.Difference || reflect.DeepEqual(exchanged.Location, initial.Location) {
				t.Fatalf("second injection comparison = %+v, want same difference with distinct location", exchanged)
			}
			result, err := Minimize(current, expected, input, 30)
			if err != nil {
				t.Fatal(err)
			}
			assertMinimizationReplay(t, current, expected, input, 30, result)
			reduced := result.Candidate
			replay := runCompare(current, expected, reduced, 30)
			if replay.Disposition != analysis.Different || !reflect.DeepEqual(replay.Location, initial.Location) || replay.Difference != initial.Difference {
				t.Fatalf("reduced target = %+v difference %+v, want %+v difference %+v", replay.Location, replay.Difference, initial.Location, initial.Difference)
			}
			if len(reduced.Actions) != 1 || reduced.Actions[0].Index != initial.Location.Injection.Index {
				t.Errorf("retained actions = %+v, want initial injection %d only", reduced.Actions, initial.Location.Injection.Index)
			}
		})
	}
}

func assertMinimizationReplay(t *testing.T, current, expected *fabric.Fabric, input Candidate, budget int, result Minimization) {
	t.Helper()
	initial := runCompare(current, expected, input, budget)
	if initial.Disposition != analysis.Different {
		t.Fatalf("initial disposition = %s, want Different", initial.Disposition)
	}
	replay := runCompare(current, expected, result.Candidate, budget)
	if replay.Disposition != analysis.Different || replay.Difference != initial.Difference || !reflect.DeepEqual(replay.Location, initial.Location) {
		t.Fatalf("replay = %s %+v %+v, want Different %+v %+v", replay.Disposition, replay.Location, replay.Difference, initial.Location, initial.Difference)
	}
	if result.Difference != initial.Difference || !reflect.DeepEqual(result.Target, initial.Location) {
		t.Fatalf("result target = %+v difference %+v, want %+v %+v", result.Target, result.Difference, initial.Location, initial.Difference)
	}
}

func TestMinimizeRejectsInvalidAndNonDifferenceInputs(t *testing.T) {
	current, expected, src, dst, _ := makeMinimizeFabrics(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	injection := fabric.Injection{At: at, Origin: fabric.Endpoint{Node: "h1"}, Frame: ethernet.Frame{Src: src, Dst: dst}}
	valid := Candidate{Actions: []fabric.Action{{Kind: fabric.ActionInject, At: at, Inject: &injection}}}
	cases := []struct {
		name              string
		current, expected *fabric.Fabric
		input             Candidate
		budget, limit     int
		message           string
	}{
		{"zero budget", nil, nil, valid, 0, 0, "budget must be positive"},
		{"negative budget", nil, nil, valid, -1, 0, "budget must be positive"},
		{"negative trial limit", nil, nil, valid, 30, -1, "trial limit must be nonnegative"},
		{"invalid action", nil, nil, Candidate{Actions: []fabric.Action{{Kind: fabric.ActionConfigure, At: at, Configure: &fabric.ConfigureAction{Node: "sw1"}}}}, 30, 0, "candidate action"},
		{"equivalent", current, current, valid, 30, 0, "initial minimization comparison is equivalent"},
		{"inconclusive", current, expected, valid, 1, 0, "initial minimization comparison is inconclusive"},
		{"comparison error", current, expected, Candidate{Actions: []fabric.Action{{Kind: fabric.ActionInject, At: at, Inject: &fabric.Injection{At: at, Origin: fabric.Endpoint{Node: "missing"}, Frame: ethernet.Frame{Src: src, Dst: dst}}}}}, 30, 0, "initial minimization comparison"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := MinimizeWithLimit(tc.current, tc.expected, tc.input, tc.budget, tc.limit)
			if err == nil || !strings.Contains(err.Error(), tc.message) || result.Minimality == Minimal {
				t.Fatalf("result = %+v, error %v, want error containing %q without Minimal", result, err, tc.message)
			}
			if tc.name == "comparison error" {
				cause := errors.Unwrap(err)
				if cause == nil || cause.Error() != `origin node "missing" not found in fabric` || strings.Contains(err.Error(), "inconclusive") {
					t.Fatalf("error = %v, cause = %v, want wrapped missing-origin error without inconclusive", err, cause)
				}
			}
		})
	}
	result, err := MinimizeWithLimit(current, expected, valid, 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Minimality != Minimal || len(result.Candidate.Actions) != 1 {
		t.Fatalf("zero-limit result = %+v, want default-limit minimal single action", result)
	}
	assertMinimizationReplay(t, current, expected, valid, 30, result)
	result.Candidate.Actions[0].Inject.Frame.Src = netaddr.MAC{9}
	if valid.Actions[0].Inject.Frame.Src != src || valid.Actions[0].Index != 0 {
		t.Fatalf("input = %+v, want independent frame and unnumbered original", valid.Actions[0])
	}
}

func TestMinimizeGlobalTargets(t *testing.T) {
	t.Run("run issues survive empty scenario", func(t *testing.T) {
		current, _, src, dst, _ := makeMinimizeFabrics(t)
		spec := current.Spec()
		sw := spec.Switches["sw1"]
		sw.Metadata = analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{{Code: "complete-test-issue", Status: analysis.Complete, Scope: analysis.PortScope("sw1", "1/1/1")}}, analysis.EvidenceCatalog{}, nil)
		spec.Switches["sw1"] = sw
		expected, err := fabric.NewWithSpec(spec)
		if err != nil {
			t.Fatal(err)
		}
		at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		prior := fabric.Injection{At: at, Origin: fabric.Endpoint{Node: "h1"}, Frame: ethernet.Frame{Src: src, Dst: dst}}
		for _, f := range []*fabric.Fabric{current, expected} {
			if _, err := f.Inject(prior); err != nil {
				t.Fatal(err)
			}
			if r := f.Run(30); r.Status != analysis.Complete {
				t.Fatalf("prior run status = %s, want Complete", r.Status)
			}
		}
		scheduled := prior
		scheduled.At = at.Add(time.Second)
		input := Candidate{Actions: []fabric.Action{{Kind: fabric.ActionInject, At: scheduled.At, Inject: &scheduled}, {Kind: fabric.ActionFault, At: scheduled.At.Add(time.Millisecond), Fault: &fabric.FaultAction{A: spec.Cables[0].A, B: spec.Cables[0].B, Fault: fabric.Fault{Kind: fabric.FaultNone}}}}}
		initial := runCompare(current, expected, input, 30)
		if initial.Disposition != analysis.Different || initial.Location.Area != fabric.DifferenceRun || initial.Location.Field != "Issues.Length" || initial.Location.Injection != nil || len(initial.Location.Lineage) != 0 {
			t.Fatalf("initial = %s %+v %+v, want run Issues.Length without journey", initial.Disposition, initial.Location, initial.Difference)
		}
		result, err := Minimize(current, expected, input, 30)
		if err != nil {
			t.Fatal(err)
		}
		if result.Minimality != Minimal || len(result.Candidate.Actions) != 0 || result.Candidate.Tuple != "" {
			t.Fatalf("result = %+v, want minimal empty scenario and tuple", result)
		}
		assertMinimizationReplay(t, current, expected, input, 30, result)
	})
	t.Run("snapshot fields with identical displayed values", func(t *testing.T) {
		current, _, _, _, _ := makeMinimizeFabrics(t)
		cfg := current.Config()
		cfg.Cables[0].Fault = fabric.Fault{Kind: fabric.FaultCut}
		cfg.Cables[1].Fault = fabric.Fault{Kind: fabric.FaultCut}
		expected, err := fabric.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		input := Candidate{Actions: []fabric.Action{{Kind: fabric.ActionFault, At: at, Fault: &fabric.FaultAction{A: cfg.Cables[0].A, B: cfg.Cables[0].B, Fault: fabric.Fault{Kind: fabric.FaultNone}}}}}
		initial := runCompare(current, expected, input, 30)
		without := runCompare(current, expected, Candidate{}, 30)
		if initial.Disposition != analysis.Different || without.Disposition != analysis.Different || initial.Difference != without.Difference || initial.Location.Field != "Ports.[\"1/1/2\"].OperStatus" || without.Location.Field != "Ports.[\"1/1/1\"].OperStatus" {
			t.Fatalf("initial = %s %+v %+v, empty = %s %+v %+v, want same displayed values on distinct port fields", initial.Disposition, initial.Location, initial.Difference, without.Disposition, without.Location, without.Difference)
		}
		result, err := Minimize(current, expected, input, 30)
		if err != nil {
			t.Fatal(err)
		}
		if result.Minimality != Minimal || len(result.Candidate.Actions) != 1 {
			t.Fatalf("result = %+v, want one retained clear-fault action", result)
		}
		assertMinimizationReplay(t, current, expected, input, 30, result)
	})
}

func TestMinimizePreservesMirrorLineage(t *testing.T) {
	current, _, src, dst, _ := makeMinimizeFabrics(t)
	cfg := current.Config()
	sw := cfg.Switches["sw1"]
	sw.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{{Name: "span", SelectSrcPorts: []string{"1/1/1"}, OutputPort: "1/1/3"}}}
	cfg.Switches["sw1"] = sw
	expected, err := fabric.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pad := fabric.Injection{At: at, Origin: fabric.Endpoint{Node: "h2"}, Frame: ethernet.Frame{Src: dst, Dst: dst}}
	target := fabric.Injection{At: at.Add(time.Millisecond), Origin: fabric.Endpoint{Node: "h1"}, Frame: ethernet.Frame{Src: src, Dst: dst}}
	input := Candidate{Actions: []fabric.Action{{Kind: fabric.ActionInject, At: pad.At, Inject: &pad}, {Kind: fabric.ActionInject, At: target.At, Inject: &target}}}
	initial := runCompare(current, expected, input, 30)
	lineage := []fabric.LineageStep{{Kind: fabric.OriginInjection}, {Kind: fabric.OriginMirror, Mirror: "span"}}
	if initial.Disposition != analysis.Different || initial.Location.Injection.Index != 2 || !reflect.DeepEqual(initial.Location.Lineage, lineage) {
		t.Fatalf("initial = %s %+v, want second injection mirror span lineage", initial.Disposition, initial.Location)
	}
	result, err := Minimize(current, expected, input, 30)
	if err != nil {
		t.Fatal(err)
	}
	if result.Minimality != Minimal || len(result.Candidate.Actions) != 1 || result.Candidate.Actions[0].Index != 2 {
		t.Fatalf("result = %+v, want only injection 2", result)
	}
	assertMinimizationReplay(t, current, expected, input, 30, result)
}

func TestMinimizeExactValuesCanRetainOtherwiseRemovableAction(t *testing.T) {
	current, expected, _, src, padSrc := makeFixedPointFabrics(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	target := fabric.Injection{At: at, Origin: fabric.Endpoint{Node: "h2"}, Frame: ethernet.Frame{Src: src, Dst: src}}
	pad := fabric.Injection{At: at.Add(time.Millisecond), Origin: fabric.Endpoint{Node: "h3"}, Frame: ethernet.Frame{Src: padSrc, Dst: padSrc}}
	input := Candidate{Actions: []fabric.Action{{Kind: fabric.ActionInject, At: target.At, Inject: &target}, {Kind: fabric.ActionInject, At: pad.At, Inject: &pad}}}
	initial := runCompare(current, expected, input, 30)
	onlyTarget := Candidate{Actions: input.ToScenario(30).Actions[:1]}
	removed := runCompare(current, expected, onlyTarget, 30)
	if initial.Disposition != analysis.Different || removed.Disposition != analysis.Different || !reflect.DeepEqual(initial.Location, removed.Location) || initial.Difference.Observable != "final state" || removed.Difference.Observable != initial.Difference.Observable {
		t.Fatalf("initial = %s %+v %+v, removed = %s %+v %+v, want same snapshot target and label", initial.Disposition, initial.Location, initial.Difference, removed.Disposition, removed.Location, removed.Difference)
	}
	if initial.Difference.Current != "sw1: 2 fdb entries" || initial.Difference.Expected != "sw1: 2 fdb entries" || removed.Difference.Current != "sw1: 1 fdb entries" || removed.Difference.Expected != "sw1: 1 fdb entries" {
		t.Fatalf("initial difference = %+v, removed difference = %+v, want counts 2/2 and 1/1", initial.Difference, removed.Difference)
	}
	result, err := Minimize(current, expected, input, 30)
	if err != nil {
		t.Fatal(err)
	}
	if result.Minimality != Minimal || len(result.Candidate.Actions) != 2 {
		t.Fatalf("result = %+v, want both actions because exact values retain the shared learned entry", result)
	}
	assertMinimizationReplay(t, current, expected, input, 30, result)
}

func TestMinimizeZeroLimitAllowsExactlySixtyFourTrials(t *testing.T) {
	current, expected, src, dst, padSrc := makeMinimizeFabrics(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := Candidate{}
	for i := range 70 {
		when := at.Add(time.Duration(i) * time.Millisecond)
		input.Actions = append(input.Actions, fabric.Action{At: when, Kind: fabric.ActionInject, Inject: &fabric.Injection{Origin: fabric.Endpoint{Node: "h3"}, Frame: ethernet.Frame{Src: padSrc, Dst: dst}}})
	}
	input.Actions = append(input.Actions, fabric.Action{At: at.Add(time.Second), Kind: fabric.ActionInject, Inject: &fabric.Injection{Origin: fabric.Endpoint{Node: "h1"}, Frame: ethernet.Frame{Src: src, Dst: dst}}})
	result, err := MinimizeWithLimit(current, expected, input, 500, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Minimality != LimitReached || len(result.Candidate.Actions) != 7 || result.Candidate.Actions[0].Index != 65 {
		t.Fatalf("default result = %+v, want LimitReached with 7 actions starting at index 65", result)
	}
	assertMinimizationReplay(t, current, expected, input, 500, result)
	result, err = MinimizeWithLimit(current, expected, input, 500, 256)
	if err != nil {
		t.Fatal(err)
	}
	if result.Minimality != Minimal || len(result.Candidate.Actions) != 1 || result.Candidate.Actions[0].Index != 71 {
		t.Fatalf("explicit result = %+v, want Minimal with only injection 71", result)
	}
	assertMinimizationReplay(t, current, expected, input, 500, result)
}

func TestMinimizeRejectsMovingMirrorLineage(t *testing.T) {
	base, _, src, dst, _ := makeFixedPointFabrics(t)
	cfg := base.Config()
	sw := cfg.Switches["sw1"]
	builder := port.NewBuilder()
	for _, p := range sw.Ports.Ports() {
		builder.Add(p)
	}
	builder.Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	sw.Ports = ports
	vid := vlan.ID(10)
	sw.Bridge.VLAN.Switchports["1/1/4"] = bridge.Switchport{PVID: &vid, Untagged: []vlan.ID{10}}
	cfg.Switches["sw1"] = sw
	cfg.Hosts["h4"] = fabric.Host{Address: netaddr.MAC{2, 0, 0, 0, 1, 4}}
	cfg.Cables = append(cfg.Cables, fabric.Cable{A: fabric.Endpoint{Node: "sw1", Port: "1/1/4"}, B: fabric.Endpoint{Node: "h4"}, Medium: fabric.TwistedPair, LengthMeters: 5})
	current, err := fabric.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sw.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{
		{Name: "a-span", SelectDstPorts: []string{"1/1/2"}, OutputPort: "1/1/4"},
		{Name: "b-span", SelectSrcPorts: []string{"1/1/1"}, OutputPort: "1/1/4"},
	}}
	cfg.Switches["sw1"] = sw
	expected, err := fabric.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, f := range []*fabric.Fabric{current, expected} {
		if _, err := f.Inject(fabric.Injection{At: at, Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, Frame: ethernet.Frame{Src: dst, Dst: dst}}); err != nil {
			t.Fatal(err)
		}
		for len(f.Snapshot().Devices["sw1"].Entries) == 0 {
			if _, ok := f.Step(); !ok {
				t.Fatalf("seed queue drained before learning: %+v", f.Report())
			}
		}
		entries := f.Snapshot().Devices["sw1"].Entries
		if len(entries) != 1 || entries[0].MAC != dst || entries[0].Port != "1/1/3" {
			t.Fatalf("seed entries = %+v, want destination learned on 1/1/3", entries)
		}
	}
	at = at.Add(time.Second)
	input := Candidate{Actions: []fabric.Action{
		{At: at, Index: 1, Kind: fabric.ActionInject, Inject: &fabric.Injection{Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, Frame: ethernet.Frame{Src: dst, Dst: dst}}},
		{At: at.Add(time.Millisecond), Index: 2, Kind: fabric.ActionInject, Inject: &fabric.Injection{Origin: fabric.Endpoint{Node: "h1"}, Frame: ethernet.Frame{Src: src, Dst: dst}}},
	}}
	initial := runCompare(current, expected, input, 30)
	removed := runCompare(current, expected, Candidate{Actions: input.Actions[1:]}, 30)
	want := fabric.DifferenceLocation{Area: fabric.DifferenceJourney, Subject: "journey", Field: "Journey", Injection: &fabric.InjectionLocation{Index: 2, At: at.Add(time.Millisecond)}, Lineage: []fabric.LineageStep{{Kind: fabric.OriginInjection}, {Kind: fabric.OriginMirror, Mirror: "a-span"}}}
	wantRemoved := want
	wantRemoved.Lineage = []fabric.LineageStep{{Kind: fabric.OriginInjection}, {Kind: fabric.OriginMirror, Mirror: "b-span"}}
	difference := analysis.Difference{Observable: "multiplicity", Current: "false", Expected: "true"}
	if initial.Err != nil || removed.Err != nil || initial.Disposition != analysis.Different || removed.Disposition != analysis.Different || initial.Difference != difference || removed.Difference != difference || !reflect.DeepEqual(initial.Location, want) || !reflect.DeepEqual(removed.Location, wantRemoved) {
		t.Fatalf("initial = %s %+v %+v, removed = %s %+v %+v, want same multiplicity at a-span then b-span", initial.Disposition, initial.Location, initial.Difference, removed.Disposition, removed.Location, removed.Difference)
	}
	result, err := Minimize(current, expected, input, 30)
	if err != nil {
		t.Fatal(err)
	}
	if result.Minimality != Minimal || len(result.Candidate.Actions) != 2 {
		t.Fatalf("result = %+v, want both actions to preserve a-span", result)
	}
	assertMinimizationReplay(t, current, expected, input, 30, result)
}

func TestMinimizeRejectsInconclusiveTrialWithExactTarget(t *testing.T) {
	base, _, src, dst, _ := makeMinimizeFabrics(t)
	cfg := base.Config()
	cfg.Cables[1].Fault = fabric.Fault{Kind: fabric.FaultCut}
	current, err := fabric.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sw := cfg.Switches["sw1"]
	sw.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{{Name: "span", SelectSrcPorts: []string{"1/1/1"}, OutputPort: "1/1/3"}}}
	cfg.Switches["sw1"] = sw
	expected, err := fabric.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := Candidate{Actions: []fabric.Action{
		{At: at, Index: 1, Kind: fabric.ActionFault, Fault: &fabric.FaultAction{A: cfg.Cables[1].A, B: cfg.Cables[1].B, Fault: fabric.Fault{Kind: fabric.FaultNone}}},
		{At: at.Add(time.Millisecond), Index: 2, Kind: fabric.ActionInject, Inject: &fabric.Injection{Origin: fabric.Endpoint{Node: "h1"}, Frame: ethernet.Frame{Src: src, Dst: dst}}},
	}}
	initial := runCompare(current, expected, input, 30)
	removed := runCompare(current, expected, Candidate{Actions: input.Actions[1:]}, 30)
	want := fabric.DifferenceLocation{Area: fabric.DifferenceJourney, Subject: "journey", Field: "Journey", Injection: &fabric.InjectionLocation{Index: 2, At: at.Add(time.Millisecond)}, Lineage: []fabric.LineageStep{{Kind: fabric.OriginInjection}, {Kind: fabric.OriginMirror, Mirror: "span"}}}
	difference := analysis.Difference{Observable: "multiplicity", Current: "false", Expected: "true"}
	if initial.Err != nil || removed.Err != nil || initial.Disposition != analysis.Different || removed.Disposition != analysis.Inconclusive || initial.Difference != difference || removed.Difference != difference || !reflect.DeepEqual(initial.Location, want) || !reflect.DeepEqual(removed.Location, want) {
		t.Fatalf("initial = %s %+v %+v, removed = %s %+v %+v, want Different then Inconclusive at the exact same span target", initial.Disposition, initial.Location, initial.Difference, removed.Disposition, removed.Location, removed.Difference)
	}
	result, err := Minimize(current, expected, input, 30)
	if err != nil {
		t.Fatal(err)
	}
	if result.Minimality != Minimal || len(result.Candidate.Actions) != 2 {
		t.Fatalf("result = %+v, want both actions to preserve a complete comparison", result)
	}
	assertMinimizationReplay(t, current, expected, input, 30, result)
}
