package fabric_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func steppedReplayFabric(t *testing.T) (*fabric.Fabric, fabric.Scenario) {
	t.Helper()
	fab, h1, h2 := newTwoSwitchTopology(t, cablemodel.Fault{Kind: cablemodel.FaultLoseSequence, Sequence: []uint{4}})
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	for i := range 3 {
		if _, err := fab.Inject(fabric.Injection{At: t0.Add(time.Duration(i) * time.Second), Origin: fabric.Endpoint{Node: "h2"}, Frame: ethernet.Frame{Src: h2, Dst: h1, Payload: []byte("learn")}}); err != nil {
			t.Fatal(err)
		}
		fab.Run(20)
	}
	history := fab.Report()
	if len(history) != 3 {
		t.Fatalf("history journeys = %d, want 3", len(history))
	}
	var crossings int
	for _, j := range history {
		if len(j.Deliveries) != 1 {
			t.Fatalf("history journey = %+v, want one delivery", j)
		}
		for _, e := range j.Entries {
			if e.Kind == fabric.EntryCrossing && e.Device == "sw1" && e.Port == "1/1/24" {
				crossings++
			}
		}
	}
	if crossings != 3 {
		t.Fatalf("history trunk crossings = %d, want 3", crossings)
	}
	entries := fab.Device("sw1").(*vswitch.Switch).Entries()
	if len(entries) != 1 || entries[0].MAC != h2 || entries[0].Port != "1/1/24" {
		t.Fatalf("learned FDB = %+v, want h2 on 1/1/24", entries)
	}
	return fab, fabric.Scenario{Name: "stepped-replay", Budget: 20, Actions: []fabric.Action{{At: t0.Add(3 * time.Second), Kind: fabric.ActionInject, Inject: &fabric.Injection{Origin: fabric.Endpoint{Node: "h1"}, Frame: ethernet.Frame{Src: h1, Dst: h2, Payload: []byte("fourth")}}}}}
}

func equalReplayResults(t *testing.T, a, b fabric.RunResult) {
	t.Helper()
	a.Replay, b.Replay = fabric.ReplaySpec{}, fabric.ReplaySpec{}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("replay result differs: steps/clock/stop = %d/%s/%s, want %d/%s/%s", b.Steps, b.Clock, b.Stop, a.Steps, a.Clock, a.Stop)
	}
}

func assertSteppedReplayJourney(t *testing.T, replay fabric.ReplaySpec) {
	t.Helper()
	fab := replay.Checkpoint()
	if fab == nil {
		t.Fatal("replay has no checkpoint")
	}
	if got := fab.Report(); len(got) != 3 {
		t.Fatalf("checkpoint journeys = %d, want 3", len(got))
	}
	entries := fab.Device("sw1").(*vswitch.Switch).Entries()
	if len(entries) != 1 || entries[0].MAC != replay.Scenario.Actions[0].Inject.Frame.Dst || entries[0].Port != "1/1/24" {
		t.Fatalf("replay checkpoint FDB = %+v, want learned destination on 1/1/24", entries)
	}
	if _, err := fab.RunScenario(replay.Scenario); err != nil {
		t.Fatal(err)
	}
	journeys := fab.Report()
	if len(journeys) != 4 {
		t.Fatalf("replayed journeys = %d, want 4", len(journeys))
	}
	j := journeys[3]
	if j.State != fabric.JourneyDropped || len(j.Deliveries) != 0 || j.Entries[len(j.Entries)-1].Kind != fabric.EntryLoss || j.Entries[len(j.Entries)-1].Reason != cablemodel.ReasonCableLoss {
		t.Fatalf("replayed fourth journey = %+v, want cable loss without delivery", j)
	}
	var hops int
	for _, e := range j.Entries {
		if e.Kind == fabric.EntryHop {
			hops++
			if e.Result.Outcome != trace.Forwarded || len(e.Result.Egress) != 1 || e.Result.Egress[0].Port != "1/1/24" {
				t.Fatalf("replayed fourth hop = %+v, want learned unicast on 1/1/24", e.Result)
			}
		}
	}
	if hops != 1 {
		t.Fatalf("replayed fourth hops = %d, want 1", hops)
	}
}

func TestReplayOfSteppedFabric(t *testing.T) {
	fab, sc := steppedReplayFabric(t)
	res, err := fab.RunScenario(sc)
	if err != nil {
		t.Fatal(err)
	}
	j := fab.Report()[3]
	if j.State != fabric.JourneyDropped || j.Entries[len(j.Entries)-1].Reason != cablemodel.ReasonCableLoss {
		t.Fatalf("fourth journey = %+v, want cable loss", j)
	}
	var hops int
	for _, e := range j.Entries {
		if e.Kind == fabric.EntryHop {
			hops++
			if e.Result.Outcome != trace.Forwarded || len(e.Result.Egress) != 1 || e.Result.Egress[0].Port != "1/1/24" {
				t.Fatalf("fourth hop = %+v, want learned unicast on 1/1/24", e.Result)
			}
		}
	}
	if hops != 1 {
		t.Fatalf("fourth hops = %d, want 1", hops)
	}
	replayed, err := fabric.Replay(res.Replay)
	if err != nil {
		t.Fatal(err)
	}
	equalReplayResults(t, res, replayed)
	assertSteppedReplayJourney(t, replayed.Replay)
}

func TestCompareReplayOfSteppedFabrics(t *testing.T) {
	a, sc := steppedReplayFabric(t)
	b, _ := steppedReplayFabric(t)
	cmp := fabric.Compare(a, b, sc, sc.Budget)
	if cmp.Err != nil {
		t.Fatal(cmp.Err)
	}
	var checked int
	for i, original := range []*fabric.Fabric{a, b} {
		want, err := original.Fork().RunScenario(sc)
		if err != nil {
			t.Fatal(err)
		}
		got, err := fabric.Replay(cmp.Replay[i])
		if err != nil {
			t.Fatal(err)
		}
		equalReplayResults(t, want, got)
		assertSteppedReplayJourney(t, got.Replay)
		checked++
	}
	if checked != 2 {
		t.Fatalf("checked comparison sides = %d, want 2", checked)
	}
}

func TestReplayRunsTwice(t *testing.T) {
	fab, sc := steppedReplayFabric(t)
	res, err := fab.RunScenario(sc)
	if err != nil {
		t.Fatal(err)
	}
	var checked int
	for range 2 {
		got, err := fabric.Replay(res.Replay)
		if err != nil {
			t.Fatal(err)
		}
		equalReplayResults(t, res, got)
		assertSteppedReplayJourney(t, got.Replay)
		checked++
	}
	if checked != 2 {
		t.Fatalf("checked replay calls = %d, want 2", checked)
	}
}

func buildReplayPorts(t *testing.T) port.Table {
	t.Helper()

	b := port.NewBuilder()
	b.Range("1/1/%d", 1, 4, port.Port{
		Kind:        port.Physical,
		AdminStatus: port.Up,
		OperStatus:  port.Up,
	})
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build replay ports: %v", err)
	}

	return tbl
}

func makeReplayFabric(t *testing.T, pTable port.Table, brCfg *bridge.Config, trafficCfg ...*traffic.Config) *fabric.Fabric {
	t.Helper()

	hosts := make(map[string]hostmodel.Config)
	var cables []fabric.Cable

	ports := pTable.Ports()
	for i, p := range ports {
		hostName := "h" + string(rune('1'+i))
		mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, byte(i + 1)}

		hosts[hostName] = hostmodel.Config{
			Address: mac,
		}
		cables = append(cables, fabric.Cable{
			Cable: cablemodel.Cable{LengthMeters: 1.0},
			A:     fabric.Endpoint{Node: hostName},
			B:     fabric.Endpoint{Node: "sw1", Port: p.Name},
		})
	}

	var tc *traffic.Config
	if len(trafficCfg) > 0 {
		tc = trafficCfg[0]
	}
	cfg := assembly.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:   pTable,
				Bridge:  brCfg,
				Traffic: tc,
			},
		},
		Hosts:  hosts,
		Cables: cables,
	}

	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("New replay fabric: %v", err)
	}

	return fab
}

func mustVLAN(id uint16) *vlan.ID {
	v := vlan.ID(id)
	return &v
}

func TestReplayUntaggedLearnAndFlood(t *testing.T) {
	ports := buildReplayPorts(t)
	cfg := &bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{
				10: "engineering",
			},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/3": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			},
		},
	}

	fab := makeReplayFabric(t, ports, cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	macA := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	macB := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x66}

	_, err := fab.Inject(fabric.Injection{
		At:     now,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       macB,
			Src:       macA,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("payload"),
		},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}

	entry, ok := fab.Step()
	if !ok || entry.Kind != fabric.EntryHop {
		t.Fatalf("Step failed: ok=%t, entry=%+v", ok, entry)
	}

	res := entry.Result
	if res.Outcome != trace.Flooded {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Flooded)
	}
	if res.FID != 10 {
		t.Errorf("res.FID = %d, want 10", res.FID)
	}

	var egressPorts []string
	for _, eg := range res.Egress {
		egressPorts = append(egressPorts, eg.Port)
	}
	if slices.Contains(egressPorts, "1/1/1") {
		t.Errorf("egress ports %v contains ingress port 1/1/1", egressPorts)
	}
	if !slices.Contains(egressPorts, "1/1/2") || !slices.Contains(egressPorts, "1/1/3") {
		t.Errorf("egress ports = %v, want 1/1/2 and 1/1/3", egressPorts)
	}

	entries := fab.Device("sw1").(*vswitch.Switch).Entries()
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.FID != 10 || e.MAC != macA || e.Port != "1/1/1" || e.Lifetime == bridge.Static {
		t.Errorf("learned entry = %+v, want dynamic (10, %s) -> 1/1/1", e, macA)
	}
}

func TestReplayKnownUnicastAndSamePortDrop(t *testing.T) {
	ports := buildReplayPorts(t)
	cfg := &bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{
				10: "vlan10",
			},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			},
		},
	}

	fab := makeReplayFabric(t, ports, cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	macA := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	macB := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x66}

	// 1. Initial frame from h1 learns macA on 1/1/1.
	_, err := fab.Inject(fabric.Injection{
		At:     now,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       macB,
			Src:       macA,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("init"),
		},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Step()

	// 2. Return frame from h2 to macA forwards directly to 1/1/1.
	fab.Step()
	_, err = fab.Inject(fabric.Injection{
		At:     now.Add(time.Second),
		Origin: fabric.Endpoint{Node: "h2"},
		Frame: ethernet.Frame{
			Dst:       macA,
			Src:       macB,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("reply"),
		},
	})
	if err != nil {
		t.Fatalf("Inject return: %v", err)
	}

	entry, ok := fab.Step()
	if !ok || entry.Kind != fabric.EntryHop {
		t.Fatalf("Step return failed: ok=%t, entry=%+v", ok, entry)
	}
	resForward := entry.Result
	if resForward.Outcome != trace.Forwarded {
		t.Fatalf("resForward.Outcome = %q, want %q", resForward.Outcome, trace.Forwarded)
	}
	if len(resForward.Egress) != 1 || resForward.Egress[0].Port != "1/1/1" {
		t.Errorf("resForward.Egress = %+v, want single egress on 1/1/1", resForward.Egress)
	}

	// 3. Frame from h1 with destination macA drops as same-port.
	fab.Step()
	_, err = fab.Inject(fabric.Injection{
		At:     now.Add(2 * time.Second),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       macA,
			Src:       macB,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("reply"),
		},
	})
	if err != nil {
		t.Fatalf("Inject same-port: %v", err)
	}

	entrySame, ok := fab.Step()
	if !ok || entrySame.Kind != fabric.EntryHop {
		t.Fatalf("Step same-port failed: ok=%t, entry=%+v", ok, entrySame)
	}
	resSame := entrySame.Result
	if resSame.Outcome != trace.Dropped {
		t.Fatalf("resSame.Outcome = %q, want %q", resSame.Outcome, trace.Dropped)
	}
	if resSame.Reason != bridge.ReasonSamePort {
		t.Errorf("resSame.Reason = %q, want %q", resSame.Reason, bridge.ReasonSamePort)
	}
}

func TestReplayDownPortNeitherIngressesNorEgresses(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Down, OperStatus: port.Down})
	b.Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	cfg := &bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{
				10: "vlan10",
			},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/3": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			},
		},
	}

	fab := makeReplayFabric(t, ports, cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	macA := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	macB := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x66}

	// 1. A host on a down link transmits nothing and its journey records the
	// link's reason; a frame replayed at switch port 1/1/3 drops with ReasonPortDown.
	hostFrame, err := fab.Inject(fabric.Injection{
		At:     now,
		Origin: fabric.Endpoint{Node: "h3"},
		Frame: ethernet.Frame{
			Dst:       macB,
			Src:       macA,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("test"),
		},
	})
	if err != nil {
		t.Fatalf("Inject on host with down link: %v", err)
	}
	if entries := fab.Report()[hostFrame-1].Entries; len(entries) != 2 || entries[1].Kind != fabric.EntryDrop || entries[1].Reason != cablemodel.ReasonPeerDown {
		t.Errorf("host journey entries = %+v, want injection then a peer-down drop", entries)
	}

	_, err = fab.Inject(fabric.Injection{
		At:     now,
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
		Frame: ethernet.Frame{
			Dst:       macB,
			Src:       macA,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("test"),
		},
	})
	if err != nil {
		t.Fatalf("Inject on down port: %v", err)
	}

	entryDown, ok := fab.Step()
	if !ok || entryDown.Kind != fabric.EntryHop {
		t.Fatalf("Step on down port failed: ok=%t, entry=%+v", ok, entryDown)
	}
	if entryDown.Result.Outcome != trace.Dropped {
		t.Fatalf("entryDown.Result.Outcome = %q, want %q", entryDown.Result.Outcome, trace.Dropped)
	}
	if entryDown.Result.Reason != port.ReasonPortDown {
		t.Errorf("entryDown.Result.Reason = %q, want %q", entryDown.Result.Reason, port.ReasonPortDown)
	}

	// 2. Flooded frame from 1/1/1 excludes down port 1/1/3.
	_, err = fab.Inject(fabric.Injection{
		At:     now.Add(time.Second),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       macB,
			Src:       macA,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("test"),
		},
	})
	if err != nil {
		t.Fatalf("Inject on active port: %v", err)
	}

	entryFlood, ok := fab.Step()
	if !ok || entryFlood.Kind != fabric.EntryHop {
		t.Fatalf("Step flood failed: ok=%t, entry=%+v", ok, entryFlood)
	}
	for _, eg := range entryFlood.Result.Egress {
		if eg.Port == "1/1/3" {
			t.Errorf("down port 1/1/3 unexpectedly present in flood egress")
		}
	}
}

func TestReplayReservedDestinationAddressesDropped(t *testing.T) {
	ports := buildReplayPorts(t)
	cfg := &bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			},
		},
	}

	fab := makeReplayFabric(t, ports, cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	macA := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	reservedMAC := netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00}

	_, err := fab.Inject(fabric.Injection{
		At:     now,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       reservedMAC,
			Src:       macA,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("bpdu"),
		},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}

	entry, ok := fab.Step()
	if !ok || entry.Kind != fabric.EntryHop {
		t.Fatalf("Step failed: ok=%t, entry=%+v", ok, entry)
	}

	res := entry.Result
	if res.Outcome != trace.Dropped {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Dropped)
	}
	if res.Reason != bridge.ReasonReservedAddress {
		t.Errorf("res.Reason = %q, want %q", res.Reason, bridge.ReasonReservedAddress)
	}
	if len(fab.Device("sw1").(*vswitch.Switch).Entries()) != 0 {
		t.Errorf("FDB has %d entries, want 0", len(fab.Device("sw1").(*vswitch.Switch).Entries()))
	}
}

func TestReplaySTaggedFrameCarriedThroughBothEgressForms(t *testing.T) {
	ports := buildReplayPorts(t)
	cfg := &bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{20: "vlan20"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(20), Untagged: []vlan.ID{20}},
				"1/1/2": {Tagged: []vlan.ID{20}},
				"1/1/3": {Untagged: []vlan.ID{20}},
			},
		},
	}

	fab := makeReplayFabric(t, ports, cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	macA := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	macB := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x66}

	sTaggedFrame := ethernet.Frame{
		Dst: macB,
		Src: macA,
		Tags: []vlan.Tag{
			{
				TPID: uint16(ethernet.EtherTypeProviderBridging),
				VID:  100,
				PCP:  3,
			},
		},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("payload"),
	}

	// A host emits one form, so a provider-tagged frame is a capture replay
	// at the device port.
	_, err := fab.Inject(fabric.Injection{
		At:     now,
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		Frame:  sTaggedFrame,
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}

	entry, ok := fab.Step()
	if !ok || entry.Kind != fabric.EntryHop {
		t.Fatalf("Step failed: ok=%t, entry=%+v", ok, entry)
	}

	res := entry.Result
	if res.Outcome != trace.Flooded {
		t.Fatalf("res.Outcome = %q, want Flooded", res.Outcome)
	}

	var egressTagged, egressUntagged *device.Egress
	for i := range res.Egress {
		if res.Egress[i].Port == "1/1/2" {
			egressTagged = &res.Egress[i]
		}
		if res.Egress[i].Port == "1/1/3" {
			egressUntagged = &res.Egress[i]
		}
	}

	if egressTagged == nil || egressUntagged == nil {
		t.Fatalf("missing egress results: tagged=%v, untagged=%v", egressTagged, egressUntagged)
	}

	if len(egressTagged.Frame.Tags) != 2 {
		t.Fatalf("tagged port tag count = %d, want 2", len(egressTagged.Frame.Tags))
	}
	if egressTagged.Frame.Tags[0].TPID != uint16(ethernet.EtherTypeDot1Q) || egressTagged.Frame.Tags[0].VID != 20 {
		t.Errorf("outer tag = %+v, want C-TAG VID 20", egressTagged.Frame.Tags[0])
	}
	if egressTagged.Frame.Tags[1].TPID != uint16(ethernet.EtherTypeProviderBridging) || egressTagged.Frame.Tags[1].VID != 100 {
		t.Errorf("inner tag = %+v, want S-TAG VID 100", egressTagged.Frame.Tags[1])
	}

	if len(egressUntagged.Frame.Tags) != 1 {
		t.Fatalf("untagged port tag count = %d, want 1", len(egressUntagged.Frame.Tags))
	}
	if egressUntagged.Frame.Tags[0].TPID != uint16(ethernet.EtherTypeProviderBridging) || egressUntagged.Frame.Tags[0].VID != 100 {
		t.Errorf("untagged port tag = %+v, want S-TAG VID 100", egressUntagged.Frame.Tags[0])
	}
}

func TestReplayOversizedFrameDropsAtEgress(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: 1500})
	b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: 9000})
	b.Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	cfg := &bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/3": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			},
		},
	}

	fab := makeReplayFabric(t, ports, cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	macA := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	macB := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x66}

	oversizedPayload := make([]byte, 1600)
	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   oversizedPayload,
	}

	_, err = fab.Inject(fabric.Injection{
		At:     now,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  frame,
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}

	entry, ok := fab.Step()
	if !ok || entry.Kind != fabric.EntryHop {
		t.Fatalf("Step failed: ok=%t, entry=%+v", ok, entry)
	}

	res := entry.Result
	if res.Outcome != trace.Flooded {
		t.Fatalf("res.Outcome = %q, want Flooded", res.Outcome)
	}

	var egress2, egress3 *device.Egress
	for i := range res.Egress {
		if res.Egress[i].Port == "1/1/2" {
			egress2 = &res.Egress[i]
		}
		if res.Egress[i].Port == "1/1/3" {
			egress3 = &res.Egress[i]
		}
	}

	if egress2 == nil {
		t.Fatal("missing egress on 1/1/2")
	}
	if egress2.Dropped != port.ReasonMTUExceeded {
		t.Errorf("1/1/2 Dropped = %q, want %q", egress2.Dropped, port.ReasonMTUExceeded)
	}

	if egress3 == nil {
		t.Fatal("missing egress on 1/1/3")
	}
	if egress3.Dropped != "" {
		t.Errorf("1/1/3 Dropped = %q, want empty", egress3.Dropped)
	}
}

func TestReplayAdmissionAndIngressFiltering(t *testing.T) {
	ports := buildReplayPorts(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	macA := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	macB := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x66}

	untaggedFrame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("data"),
	}
	priorityTaggedFrame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		Tags:      []vlan.Tag{{TPID: 0x8100, VID: 0, PCP: 3}},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("data"),
	}
	tagged20Frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		Tags:      []vlan.Tag{{TPID: 0x8100, VID: 20}},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("data"),
	}

	t.Run("tagged-only drops untagged frame", func(t *testing.T) {
		cfg := &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {Admission: bridge.TaggedOnly, Tagged: []vlan.ID{20}},
					"1/1/2": {Tagged: []vlan.ID{20}},
				},
			},
		}
		fab := makeReplayFabric(t, ports, cfg)

		// Untagged frame injected from untagged host h1 into tagged-only port.
		_, err := fab.Inject(fabric.Injection{
			At:     now,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame:  untaggedFrame,
		})
		if err != nil {
			t.Fatalf("Inject: %v", err)
		}

		entry, ok := fab.Step()
		if !ok || entry.Kind != fabric.EntryHop {
			t.Fatalf("Step failed: ok=%t, entry=%+v", ok, entry)
		}
		if entry.Result.Outcome != trace.Dropped || entry.Result.Reason != bridge.ReasonAdmission {
			t.Errorf("Result = %s/%s, want Dropped/%s", entry.Result.Outcome, entry.Result.Reason, bridge.ReasonAdmission)
		}
	})

	t.Run("untagged-only drops frame tagged 20", func(t *testing.T) {
		cfg := &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {
						PVID:      mustVLAN(10),
						Untagged:  []vlan.ID{10},
						Admission: bridge.UntaggedAndPriorityTaggedOnly,
					},
					"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				},
			},
		}
		fab := makeReplayFabric(t, ports, cfg)

		// Tagged frame injected at device port because an access host on port 1/1/1 emits untagged frames.
		_, err := fab.Inject(fabric.Injection{
			At:     now,
			Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			Frame:  tagged20Frame,
		})
		if err != nil {
			t.Fatalf("Inject: %v", err)
		}

		entry, ok := fab.Step()
		if !ok || entry.Kind != fabric.EntryHop {
			t.Fatalf("Step failed: ok=%t, entry=%+v", ok, entry)
		}
		if entry.Result.Outcome != trace.Dropped || entry.Result.Reason != bridge.ReasonAdmission {
			t.Errorf("Result = %s/%s, want Dropped/%s", entry.Result.Outcome, entry.Result.Reason, bridge.ReasonAdmission)
		}
	})

	t.Run("untagged-only classifies priority-tagged frame to PVID", func(t *testing.T) {
		cfg := &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {
						PVID:      mustVLAN(10),
						Untagged:  []vlan.ID{10},
						Admission: bridge.UntaggedAndPriorityTaggedOnly,
					},
					"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				},
			},
		}
		fab := makeReplayFabric(t, ports, cfg)

		// Priority-tagged frame injected at device port because a standard host does not emit VID 0.
		_, err := fab.Inject(fabric.Injection{
			At:     now,
			Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			Frame:  priorityTaggedFrame,
		})
		if err != nil {
			t.Fatalf("Inject: %v", err)
		}

		entry, ok := fab.Step()
		if !ok || entry.Kind != fabric.EntryHop {
			t.Fatalf("Step failed: ok=%t, entry=%+v", ok, entry)
		}
		if entry.Result.Outcome != trace.Flooded || entry.Result.FID != 10 {
			t.Errorf("Result = %s/FID %d, want Flooded/FID 10", entry.Result.Outcome, entry.Result.FID)
		}
	})

	t.Run("ingress filtering enabled drops non-member tagged frame", func(t *testing.T) {
		cfg := &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {
						PVID:             mustVLAN(10),
						Untagged:         []vlan.ID{10},
						IngressFiltering: true,
						Admission:        bridge.All,
					},
					"1/1/2": {PVID: mustVLAN(20), Untagged: []vlan.ID{20}},
				},
			},
		}
		fab := makeReplayFabric(t, ports, cfg)

		// Frame tagged 20 injected at device port to test ingress filtering drop on non-member port.
		_, err := fab.Inject(fabric.Injection{
			At:     now,
			Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			Frame:  tagged20Frame,
		})
		if err != nil {
			t.Fatalf("Inject: %v", err)
		}

		entry, ok := fab.Step()
		if !ok || entry.Kind != fabric.EntryHop {
			t.Fatalf("Step failed: ok=%t, entry=%+v", ok, entry)
		}
		if entry.Result.Outcome != trace.Dropped || entry.Result.Reason != bridge.ReasonIngressFilter {
			t.Errorf("Result = %s/%s, want Dropped/%s", entry.Result.Outcome, entry.Result.Reason, bridge.ReasonIngressFilter)
		}
	})

	t.Run("ingress filtering disabled forwards non-member tagged frame", func(t *testing.T) {
		cfg := &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {
						PVID:             mustVLAN(10),
						Untagged:         []vlan.ID{10},
						IngressFiltering: false,
						Admission:        bridge.All,
					},
					"1/1/2": {PVID: mustVLAN(20), Untagged: []vlan.ID{20}},
				},
			},
		}
		fab := makeReplayFabric(t, ports, cfg)

		// Frame tagged 20 injected at device port to test ingress filtering disabled admitting non-member frame.
		_, err := fab.Inject(fabric.Injection{
			At:     now,
			Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			Frame:  tagged20Frame,
		})
		if err != nil {
			t.Fatalf("Inject: %v", err)
		}

		entry, ok := fab.Step()
		if !ok || entry.Kind != fabric.EntryHop {
			t.Fatalf("Step failed: ok=%t, entry=%+v", ok, entry)
		}
		if entry.Result.Outcome != trace.Flooded || entry.Result.FID != 20 {
			t.Errorf("Result = %s/FID %d, want Flooded/FID 20", entry.Result.Outcome, entry.Result.FID)
		}
	})
}

func TestReplayedRunReproducesArrivalTimes(t *testing.T) {
	run := func() []time.Time {
		ports := buildReplayPorts(t)
		cfg := &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{
					10: "engineering",
				},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
					"1/1/3": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
					"1/1/4": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				},
			},
		}
		fab := makeReplayFabric(t, ports, cfg, &traffic.Config{Queues: map[string]traffic.PortQueues{
			"1/1/2": {MaxRateBPS: map[vlan.PCP]uint64{0: 100_000_000}},
		}})
		t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

		injections := []fabric.Injection{
			{
				At:     t0,
				Origin: fabric.Endpoint{Node: "h1"},
				Frame: ethernet.Frame{
					Dst:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
					Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
					EtherType: ethernet.EtherTypeIPv4,
					Payload:   []byte("packet-1"),
				},
			},
			{
				At:     t0,
				Origin: fabric.Endpoint{Node: "h1"},
				Frame: ethernet.Frame{
					Dst:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x03},
					Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
					EtherType: ethernet.EtherTypeIPv4,
					Payload:   []byte("packet-2"),
				},
			},
		}

		for _, inj := range injections {
			if _, err := fab.Inject(inj); err != nil {
				t.Fatalf("Inject: %v", err)
			}
		}

		var times []time.Time
		for {
			entry, ok := fab.Step()
			if !ok {
				break
			}
			times = append(times, entry.At)
		}
		return times
	}

	times1 := run()
	times2 := run()

	if len(times1) == 0 {
		t.Fatal("expected step entries from run, got none")
	}
	if !slices.Equal(times1, times2) {
		t.Errorf("replayed run arrival times differ:\nrun 1: %v\nrun 2: %v", times1, times2)
	}
}

func TestScenarioReplayConfigureEquality(t *testing.T) {
	cfg, macH1, macH2 := makeTwoSwitchConfigs(t)
	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	sw2Orig := cfg.Switches["sw2"]
	sw2New := sw2Orig.Clone()
	vid20 := vlan.ID(20)
	sw2New.Bridge = &bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{
				20: "VLAN20",
			},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {
					PVID:     &vid20,
					Untagged: []vlan.ID{20},
				},
				"1/1/24": {
					Tagged:           []vlan.ID{20},
					IngressFiltering: true,
				},
			},
		},
	}

	sc := fabric.Scenario{
		Name: "replay-configure",
		Actions: []fabric.Action{
			{
				At:   t0,
				Kind: fabric.ActionInject,
				Inject: &fabric.Injection{
					Origin: fabric.Endpoint{Node: "h1"},
					Frame: ethernet.Frame{
						Src:       macH1,
						Dst:       macH2,
						EtherType: ethernet.EtherTypeIPv4,
						Payload:   []byte("before-configure"),
					},
				},
			},
			{
				At:        t0.Add(time.Second),
				Kind:      fabric.ActionConfigure,
				Configure: new(assembly.ConfigureAction("sw2", sw2New)),
			},
			{
				At:   t0.Add(2 * time.Second),
				Kind: fabric.ActionInject,
				Inject: &fabric.Injection{
					Origin: fabric.Endpoint{Node: "h1"},
					Frame: ethernet.Frame{
						Src:       macH1,
						Dst:       macH2,
						EtherType: ethernet.EtherTypeIPv4,
						Payload:   []byte("after-configure"),
					},
				},
			},
		},
		Budget: 50,
	}

	res1, err := fab.RunScenario(sc)
	if err != nil {
		t.Fatalf("RunScenario: %v", err)
	}

	report1 := fab.Report()
	if len(report1) < 2 {
		t.Fatalf("len(report1) = %d, want at least 2 journeys", len(report1))
	}
	if report1[0].State != fabric.JourneyDelivered {
		t.Errorf("report1[0].State = %v, want JourneyDelivered", report1[0].State)
	}
	if report1[1].State != fabric.JourneyDropped {
		t.Errorf("report1[1].State = %v, want JourneyDropped", report1[1].State)
	}

	res2, err := fabric.Replay(res1.Replay)
	if err != nil {
		t.Fatalf("Replay: %v", err)
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

	fab2 := res1.Replay.Checkpoint()
	if _, err := fab2.RunScenario(res1.Replay.Scenario); err != nil {
		t.Fatalf("fab2.RunScenario: %v", err)
	}
	report2 := fab2.Report()

	if len(report1) != len(report2) {
		t.Fatalf("len(report1)=%d != len(report2)=%d", len(report1), len(report2))
	}
	for i := range report1 {
		if report1[i].State != report2[i].State {
			t.Errorf("journey %d state mismatch: %v != %v", i, report1[i].State, report2[i].State)
		}
		if len(report1[i].Entries) != len(report2[i].Entries) {
			t.Errorf("journey %d entries len mismatch: %d != %d", i, len(report1[i].Entries), len(report2[i].Entries))
		}
	}
}
