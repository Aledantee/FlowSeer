package fabric_test

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

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

func makeReplayFabric(t *testing.T, pTable port.Table, brCfg *bridge.Config) *fabric.Fabric {
	t.Helper()

	hosts := make(map[string]fabric.Host)
	var cables []fabric.Cable

	ports := pTable.Ports()
	for i, p := range ports {
		hostName := "h" + string(rune('1'+i))
		mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, byte(i + 1)}

		hosts[hostName] = fabric.Host{
			Address: mac,
		}
		cables = append(cables, fabric.Cable{
			A:            fabric.Endpoint{Node: hostName},
			B:            fabric.Endpoint{Node: "sw1", Port: p.Name},
			LengthMeters: 1.0,
		})
	}

	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  pTable,
				Bridge: brCfg,
			},
		},
		Hosts:  hosts,
		Cables: cables,
	}

	fab, err := fabric.New(cfg)
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

	entries := fab.Switch("sw1").Entries()
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.FID != 10 || e.MAC != macA || e.Port != "1/1/1" || e.Static {
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

	// 1. Ingress on down port 1/1/3 drops with ReasonPortDown.
	_, err = fab.Inject(fabric.Injection{
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
		t.Fatalf("Inject on down port: %v", err)
	}

	entryDown, ok := fab.Step()
	if !ok || entryDown.Kind != fabric.EntryHop {
		t.Fatalf("Step on down port failed: ok=%t, entry=%+v", ok, entryDown)
	}
	if entryDown.Result.Outcome != trace.Dropped {
		t.Fatalf("entryDown.Result.Outcome = %q, want %q", entryDown.Result.Outcome, trace.Dropped)
	}
	if entryDown.Result.Reason != bridge.ReasonPortDown {
		t.Errorf("entryDown.Result.Reason = %q, want %q", entryDown.Result.Reason, bridge.ReasonPortDown)
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
	if len(fab.Switch("sw1").Entries()) != 0 {
		t.Errorf("FDB has %d entries, want 0", len(fab.Switch("sw1").Entries()))
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

	var egressTagged, egressUntagged *bridge.Egress
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

	var egress2, egress3 *bridge.Egress
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
	if egress2.Dropped != bridge.ReasonMTUExceeded {
		t.Errorf("1/1/2 Dropped = %q, want %q", egress2.Dropped, bridge.ReasonMTUExceeded)
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
