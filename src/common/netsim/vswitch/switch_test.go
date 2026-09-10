package vswitch_test

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

var fixedTime = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func mustTable(t *testing.T, b *port.Builder) port.Table {
	t.Helper()
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	return tbl
}

func TestDeriveKeepsAnEntryLearnedUnderThePVID(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	pvid := vlan.ID(1)
	b := port.NewBuilder()
	b.Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	cfg := vswitch.Config{
		Ports: mustTable(t, b),
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{1: "", 10: "", 20: ""},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &pvid, Tagged: []vlan.ID{10, 20}},
				"1/1/2": {PVID: &pvid, Tagged: []vlan.ID{10, 20}},
			},
		}},
	}
	cur := vswitch.New(cfg)
	cur.Forward(now, "1/1/1", ethernet.Frame{
		Dst: netaddr.MAC{0, 0, 0, 0, 0, 0xbb}, Src: netaddr.MAC{0, 0, 0, 0, 0, 0xaa},
	})

	next, err := vswitch.Derive(cur, cur.Config())
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if entries := next.Entries(); len(entries) != 1 || entries[0].FID != 1 {
		t.Errorf("derived Entries() = %+v, want the entry learned under PVID 1", entries)
	}
}

func TestDiffOfAHubAndAHubIsEmpty(t *testing.T) {
	b := port.NewBuilder()
	b.Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical})
	cfg := vswitch.Config{Ports: mustTable(t, b)}
	if changes := vswitch.Diff(cfg, cfg); len(changes) != 0 {
		t.Errorf("Diff = %+v, want none", changes)
	}
}

func TestCapabilitiesFollowConfiguration(t *testing.T) {
	portsOnly := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	t.Run("ports only", func(t *testing.T) {
		cfg := vswitch.Config{Ports: portsOnly}
		if caps := cfg.Capabilities(); len(caps) != 0 {
			t.Errorf("got capabilities %v, want empty set", caps)
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("got validation error %v, want nil", err)
		}
		sw := vswitch.New(cfg)
		if sw.Entries() != nil {
			t.Errorf("got entries %v on hub, want nil", sw.Entries())
		}
	})

	t.Run("relay only", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports:  portsOnly,
			Bridge: &bridge.Config{},
		}
		want := []port.Layer{port.LayerRelay}
		if caps := cfg.Capabilities(); !slices.Equal(caps, want) {
			t.Errorf("got capabilities %v, want %v", caps, want)
		}
	})

	t.Run("all capabilities", func(t *testing.T) {
		tbl := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
			Add(port.Port{Name: "lag1", Kind: port.Lag}))

		cfg := vswitch.Config{
			Ports: tbl,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{10}},
					},
				},
			},
			Phy: &phy.Config{
				Ethernet: map[string]phy.Ethernet{
					"1/1/1": {SupportedSpeedsBPS: []uint64{1_000_000_000}},
				},
				PoE: &phy.PoE{
					Groups: map[string]phy.Group{
						"g1": {PowerMilliwatts: 60_000},
					},
					Ports: map[string]phy.PsePort{
						"1/1/1": {Group: "g1", Enabled: true, MaxClass: 4},
					},
				},
			},
		}

		want := []port.Layer{
			port.LayerEthernet,
			port.LayerLag,
			port.LayerPoe,
			port.LayerRelay,
			port.LayerVlan,
		}
		if caps := cfg.Capabilities(); !slices.Equal(caps, want) {
			t.Errorf("got capabilities %v, want %v", caps, want)
		}
	})
}

func TestHubForwardingUntouched(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical}))

	cfg := vswitch.Config{Ports: tbl}
	sw := vswitch.New(cfg)

	srcMAC := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee}
	unknownDst := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	f1 := ethernet.Frame{
		Dst:       unknownDst,
		Src:       srcMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("tagged frame"),
		Tags: []vlan.Tag{
			{
				TPID: uint16(ethernet.EtherTypeDot1Q),
				VID:  20,
			},
		},
	}

	res1 := sw.Forward(fixedTime, "1/1/1", f1)
	if res1.Outcome != trace.Flooded {
		t.Errorf("frame 1: got outcome %v, want %v", res1.Outcome, trace.Flooded)
	}
	if res1.Ingress != "1/1/1" {
		t.Errorf("frame 1: got ingress %q, want %q", res1.Ingress, "1/1/1")
	}
	if res1.FID != 0 {
		t.Errorf("frame 1: got FID %d, want 0", res1.FID)
	}
	if len(res1.Egress) != 3 {
		t.Fatalf("frame 1: got %d egress ports, want 3", len(res1.Egress))
	}
	wantEgressPorts1 := []string{"1/1/2", "1/1/3", "1/1/4"}
	for i, eg := range res1.Egress {
		if eg.Port != wantEgressPorts1[i] {
			t.Errorf("frame 1 egress %d: got port %q, want %q", i, eg.Port, wantEgressPorts1[i])
		}
		if len(eg.Frame.Tags) != 1 || eg.Frame.Tags[0].VID != 20 {
			t.Errorf("frame 1 egress %d: tag altered, got %v", i, eg.Frame.Tags)
		}
		if eg.Dropped != "" {
			t.Errorf("frame 1 egress %d: unexpected drop reason %v", i, eg.Dropped)
		}
	}
	for _, step := range res1.Steps {
		if step.Op == trace.OpLearn {
			t.Errorf("frame 1: unexpected learn step in hub trace: %v", step)
		}
		if step.Layer != port.LayerPort {
			t.Errorf("frame 1: got step layer %v, want %v", step.Layer, port.LayerPort)
		}
	}
	if sw.Entries() != nil {
		t.Errorf("hub should have no FDB entries, got %v", sw.Entries())
	}

	f2 := ethernet.Frame{
		Dst:       srcMAC,
		Src:       unknownDst,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("untagged response"),
	}

	res2 := sw.Forward(fixedTime.Add(time.Second), "1/1/3", f2)
	if res2.Outcome != trace.Flooded {
		t.Errorf("frame 2: got outcome %v, want %v", res2.Outcome, trace.Flooded)
	}
	if len(res2.Egress) != 3 {
		t.Fatalf("frame 2: got %d egress ports, want 3", len(res2.Egress))
	}
	wantEgressPorts2 := []string{"1/1/1", "1/1/2", "1/1/4"}
	for i, eg := range res2.Egress {
		if eg.Port != wantEgressPorts2[i] {
			t.Errorf("frame 2 egress %d: got port %q, want %q", i, eg.Port, wantEgressPorts2[i])
		}
		if len(eg.Frame.Tags) != 0 {
			t.Errorf("frame 2 egress %d: expected untagged, got tags %v", i, eg.Frame.Tags)
		}
	}
	for _, step := range res2.Steps {
		if step.Op == trace.OpLearn {
			t.Errorf("frame 2: unexpected learn step in hub trace: %v", step)
		}
	}
}

func TestHubWithDownPort(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, OperStatus: port.Down}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical, MTU: 50}))

	sw := vswitch.New(vswitch.Config{Ports: tbl})

	f := ethernet.Frame{
		Dst:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   make([]byte, 100),
	}

	t.Run("egress port down and MTU exceeded", func(t *testing.T) {
		res := sw.Forward(fixedTime, "1/1/1", f)
		if res.Outcome != trace.Flooded {
			t.Errorf("got outcome %v, want %v", res.Outcome, trace.Flooded)
		}
		if len(res.Egress) != 2 {
			t.Fatalf("got %d egress ports, want 2 (1/1/2 forwarded, 1/1/4 dropped)", len(res.Egress))
		}
		if res.Egress[0].Port != "1/1/2" || res.Egress[0].Dropped != "" {
			t.Errorf("egress 0: got %v, want 1/1/2 forwarded", res.Egress[0])
		}
		if res.Egress[1].Port != "1/1/4" || res.Egress[1].Dropped != bridge.ReasonMTUExceeded {
			t.Errorf("egress 1: got %v, want 1/1/4 dropped for MTU exceeded", res.Egress[1])
		}
	})

	t.Run("ingress port down", func(t *testing.T) {
		res := sw.Forward(fixedTime, "1/1/3", f)
		if res.Outcome != trace.Dropped {
			t.Errorf("got outcome %v, want %v", res.Outcome, trace.Dropped)
		}
		if res.Reason != bridge.ReasonPortDown {
			t.Errorf("got reason %v, want %v", res.Reason, bridge.ReasonPortDown)
		}
		if len(res.Egress) != 0 {
			t.Errorf("expected 0 egress entries, got %d", len(res.Egress))
		}
	})
}

func makeComparePair(t *testing.T) (vswitch.Config, vswitch.Config, *vswitch.Switch, *vswitch.Switch) {
	t.Helper()

	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical}))

	vlanTable := map[vlan.ID]string{
		10: "vlan10",
		20: "vlan20",
	}

	pvid10 := vlan.ID(10)
	pvid20 := vlan.ID(20)

	curCfg := vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: vlanTable,
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &pvid10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &pvid10, Untagged: []vlan.ID{10}},
					"1/1/3": {PVID: &pvid10, Untagged: []vlan.ID{10}},
				},
			},
		},
	}

	expCfg := vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: vlanTable,
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &pvid10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &pvid20, Untagged: []vlan.ID{20}},
					"1/1/3": {PVID: &pvid10, Untagged: []vlan.ID{10}},
				},
			},
		},
	}

	return curCfg, expCfg, vswitch.New(curCfg), vswitch.New(expCfg)
}

func TestCompareObservesVlanDifference(t *testing.T) {
	_, _, cur, exp := makeComparePair(t)

	f := ethernet.Frame{
		Dst:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test frame"),
	}

	cmp := vswitch.Compare(cur, exp, fixedTime, "1/1/1", f)
	if cmp.Same {
		t.Errorf("got Same: true, want false")
	}

	curPorts := make([]string, len(cmp.Current.Egress))
	for i, eg := range cmp.Current.Egress {
		curPorts[i] = eg.Port
	}
	if !slices.Contains(curPorts, "1/1/2") {
		t.Errorf("current trace missing 1/1/2: %v", curPorts)
	}

	expPorts := make([]string, len(cmp.Expected.Egress))
	for i, eg := range cmp.Expected.Egress {
		expPorts[i] = eg.Port
	}
	if slices.Contains(expPorts, "1/1/2") {
		t.Errorf("expected trace unexpectedly contains 1/1/2: %v", expPorts)
	}

	if len(cmp.Current.Steps) == 0 || len(cmp.Expected.Steps) == 0 {
		t.Errorf("comparison missing steps: cur %d, exp %d", len(cmp.Current.Steps), len(cmp.Expected.Steps))
	}
}

func TestDeriveRetainsAdmittedDynamicEntries(t *testing.T) {
	_, expCfg, cur, _ := makeComparePair(t)

	macA := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x0a}
	macB := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x0b}

	cur.Forward(fixedTime, "1/1/2", ethernet.Frame{
		Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("frame A"),
	})
	cur.Forward(fixedTime.Add(time.Second), "1/1/1", ethernet.Frame{
		Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		Src:       macB,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("frame B"),
	})

	if entries := cur.Entries(); len(entries) != 2 {
		t.Fatalf("current switch has %d entries, want 2", len(entries))
	}

	derived, err := vswitch.Derive(cur, expCfg)
	if err != nil {
		t.Fatalf("derive switch: %v", err)
	}

	entries := derived.Entries()
	if len(entries) != 1 {
		t.Fatalf("derived switch has %d entries, want 1: %v", len(entries), entries)
	}

	if entries[0].FID != 10 || entries[0].MAC != macB || entries[0].Port != "1/1/1" {
		t.Errorf("derived entry mismatch: got %v, want (10, %s) -> 1/1/1", entries[0], macB)
	}
}

func TestDiffFieldChangesAcrossLayers(t *testing.T) {
	curCfg, expCfg, _, _ := makeComparePair(t)

	t.Run("port field changes across layers", func(t *testing.T) {
		changes := vswitch.Diff(curCfg, expCfg)
		if len(changes) != 2 {
			t.Fatalf("got %d changes, want 2: %v", len(changes), changes)
		}

		pvidChange := changes[0]
		if pvidChange.Subject.Key != "1/1/2" || pvidChange.Field != "pvid" ||
			pvidChange.From != vlan.ID(10) || pvidChange.To != vlan.ID(20) {
			t.Errorf("unexpected pvid change: %+v", pvidChange)
		}

		untaggedChange := changes[1]
		if untaggedChange.Subject.Key != "1/1/2" || untaggedChange.Field != "untagged_vlan_ids" ||
			!slices.Equal(untaggedChange.From.([]vlan.ID), []vlan.ID{10}) ||
			!slices.Equal(untaggedChange.To.([]vlan.ID), []vlan.ID{20}) {
			t.Errorf("unexpected untagged_vlan_ids change: %+v", untaggedChange)
		}
	})

	t.Run("added port", func(t *testing.T) {
		morePorts := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
			Add(port.Port{Name: "1/1/3", Kind: port.Physical}).
			Add(port.Port{Name: "1/1/4", Kind: port.Physical}))

		addedPortCfg := curCfg
		addedPortCfg.Ports = morePorts

		changes := vswitch.Diff(curCfg, addedPortCfg)
		if len(changes) != 1 {
			t.Fatalf("got %d changes, want 1", len(changes))
		}
		ch := changes[0]
		if ch.Subject.Kind != "port" || ch.Subject.Key != "1/1/4" || ch.Field != "" || ch.From != nil || ch.To == nil {
			t.Errorf("unexpected port addition change: %+v", ch)
		}
	})

	t.Run("pse group budget change", func(t *testing.T) {
		cfgA := vswitch.Config{
			Phy: &phy.Config{
				PoE: &phy.PoE{
					Groups: map[string]phy.Group{
						"g1": {PowerMilliwatts: 60_000},
					},
				},
			},
		}
		cfgB := vswitch.Config{
			Phy: &phy.Config{
				PoE: &phy.PoE{
					Groups: map[string]phy.Group{
						"g1": {PowerMilliwatts: 90_000},
					},
				},
			},
		}

		changes := vswitch.Diff(cfgA, cfgB)
		if len(changes) != 1 {
			t.Fatalf("got %d changes, want 1", len(changes))
		}
		ch := changes[0]
		if ch.Subject.Kind != "pse_group" || ch.Subject.Key != "g1" || ch.Field != "power_milliwatts" ||
			ch.From != uint32(60_000) || ch.To != uint32(90_000) {
			t.Errorf("unexpected pse_group change: %+v", ch)
		}
	})

	t.Run("bridge gains VLAN capability", func(t *testing.T) {
		cfgA := vswitch.Config{
			Bridge: &bridge.Config{},
		}
		cfgB := vswitch.Config{
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
				},
			},
		}

		changes := vswitch.Diff(cfgA, cfgB)
		foundCap := false
		for _, ch := range changes {
			if ch.Subject.Kind == "capability" && ch.Subject.Key == "vlan" {
				foundCap = true
				if ch.From != nil || ch.To != port.LayerVlan {
					t.Errorf("unexpected capability change payload: %+v", ch)
				}
			}
		}
		if !foundCap {
			t.Errorf("expected capability change for vlan, got changes: %+v", changes)
		}
	})
}

func TestCompareSameOverFrameTable(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical}))

	pvid10 := vlan.ID(10)
	cfg := vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &pvid10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &pvid10, Untagged: []vlan.ID{10}},
					"1/1/3": {PVID: &pvid10, Untagged: []vlan.ID{10}},
				},
			},
		},
	}

	knownDst := netaddr.MAC{0x00, 0x22, 0x33, 0x44, 0x55, 0x66}

	swA := vswitch.New(cfg)
	swB := vswitch.New(cfg)

	// Preload a known entry on both switches.
	swA.Forward(fixedTime, "1/1/2", ethernet.Frame{
		Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		Src:       knownDst,
		EtherType: ethernet.EtherTypeIPv4,
	})
	swB.Forward(fixedTime, "1/1/2", ethernet.Frame{
		Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		Src:       knownDst,
		EtherType: ethernet.EtherTypeIPv4,
	})

	tests := []struct {
		name    string
		ingress string
		frame   ethernet.Frame
	}{
		{
			name:    "known unicast",
			ingress: "1/1/1",
			frame: ethernet.Frame{
				Dst:       knownDst,
				Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("known unicast payload"),
			},
		},
		{
			name:    "unknown unicast",
			ingress: "1/1/1",
			frame: ethernet.Frame{
				Dst:       netaddr.MAC{0x00, 0x99, 0x88, 0x77, 0x66, 0x55},
				Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("unknown unicast payload"),
			},
		},
		{
			name:    "tagged frame",
			ingress: "1/1/1",
			frame: ethernet.Frame{
				Dst:       netaddr.MAC{0x00, 0x99, 0x88, 0x77, 0x66, 0x55},
				Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
				EtherType: ethernet.EtherTypeIPv4,
				Tags: []vlan.Tag{
					{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10},
				},
				Payload: []byte("tagged payload"),
			},
		},
		{
			name:    "priority tagged frame",
			ingress: "1/1/1",
			frame: ethernet.Frame{
				Dst:       netaddr.MAC{0x00, 0x99, 0x88, 0x77, 0x66, 0x55},
				Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
				EtherType: ethernet.EtherTypeIPv4,
				Tags: []vlan.Tag{
					{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 0, PCP: 3},
				},
				Payload: []byte("priority tagged payload"),
			},
		},
		{
			name:    "reserved bridge group address",
			ingress: "1/1/1",
			frame: ethernet.Frame{
				Dst:       netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00},
				Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
				EtherType: ethernet.EtherTypeIPv4,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmp := vswitch.Compare(swA, swB, fixedTime, tc.ingress, tc.frame)
			if !cmp.Same {
				t.Errorf("got Same: false, want true for %s", tc.name)
			}
		})
	}
}

func TestDiffEqualConfigsEmpty(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	pvid10 := vlan.ID(10)

	cases := []struct {
		name string
		cfg  vswitch.Config
	}{
		{
			name: "ports only",
			cfg:  vswitch.Config{Ports: tbl},
		},
		{
			name: "bridge without vlan",
			cfg: vswitch.Config{
				Ports:  tbl,
				Bridge: &bridge.Config{AgingTime: 300 * time.Second},
			},
		},
		{
			name: "bridge with vlan",
			cfg: vswitch.Config{
				Ports: tbl,
				Bridge: &bridge.Config{
					AgingTime: 300 * time.Second,
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "vlan10"},
						Switchports: map[string]bridge.Switchport{
							"1/1/1": {PVID: &pvid10, Untagged: []vlan.ID{10}},
						},
					},
				},
			},
		},
		{
			name: "full config",
			cfg: vswitch.Config{
				Ports: tbl,
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "vlan10"},
					},
				},
				Phy: &phy.Config{
					Ethernet: map[string]phy.Ethernet{
						"1/1/1": {SupportedSpeedsBPS: []uint64{1_000_000_000}},
					},
					PoE: &phy.PoE{
						Groups: map[string]phy.Group{
							"g1": {PowerMilliwatts: 60_000},
						},
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changes := vswitch.Diff(tc.cfg, tc.cfg)
			if len(changes) != 0 {
				t.Errorf("got %d changes for equal configs, want 0: %v", len(changes), changes)
			}
		})
	}
}

func TestDeriveEdgeCases(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	t.Run("lag without members drops entries", func(t *testing.T) {
		curPorts := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical}))
		curCfg := vswitch.Config{
			Ports:  curPorts,
			Bridge: &bridge.Config{},
		}
		sw := vswitch.New(curCfg)
		mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		sw.Forward(fixedTime, "1/1/1", ethernet.Frame{
			Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			Src:       mac,
			EtherType: ethernet.EtherTypeIPv4,
		})

		// Target has 1/1/1 as a LAG with no members.
		targetPorts := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Lag}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical}))
		targetCfg := vswitch.Config{
			Ports:  targetPorts,
			Bridge: &bridge.Config{},
		}

		derived, err := vswitch.Derive(sw, targetCfg)
		if err != nil {
			t.Fatalf("unexpected derive error: %v", err)
		}
		if len(derived.Entries()) != 0 {
			t.Errorf("expected entries dropped for LAG without members, got %v", derived.Entries())
		}
	})

	t.Run("derive with invalid target config", func(t *testing.T) {
		cfg := vswitch.Config{Ports: ports}
		sw := vswitch.New(cfg)

		invalidCfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{0: "invalid-vid"},
				},
			},
		}

		if _, err := vswitch.Derive(sw, invalidCfg); err == nil {
			t.Errorf("expected validation error from Derive, got nil")
		}
	})

	t.Run("non-vlan to non-vlan re-keys to FID 0", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports:  ports,
			Bridge: &bridge.Config{},
		}
		sw := vswitch.New(cfg)
		mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		sw.Forward(fixedTime, "1/1/1", ethernet.Frame{
			Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			Src:       mac,
			EtherType: ethernet.EtherTypeIPv4,
		})

		derived, err := vswitch.Derive(sw, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		entries := derived.Entries()
		if len(entries) != 1 || entries[0].FID != 0 {
			t.Errorf("expected 1 entry with FID 0, got %v", entries)
		}
	})

	t.Run("vlan to non-vlan drops entries", func(t *testing.T) {
		pvid10 := vlan.ID(10)
		curCfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: &pvid10, Untagged: []vlan.ID{10}},
						"1/1/2": {PVID: &pvid10, Untagged: []vlan.ID{10}},
					},
				},
			},
		}
		sw := vswitch.New(curCfg)
		mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		sw.Forward(fixedTime, "1/1/1", ethernet.Frame{
			Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			Src:       mac,
			EtherType: ethernet.EtherTypeIPv4,
		})

		nonVlanCfg := vswitch.Config{
			Ports:  ports,
			Bridge: &bridge.Config{},
		}
		derived, err := vswitch.Derive(sw, nonVlanCfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(derived.Entries()) != 0 {
			t.Errorf("expected entries dropped when transitioning to non-VLAN, got %v", derived.Entries())
		}
	})
}

func TestSwitchReadableState(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}))

	cfg := vswitch.Config{
		Ports: tbl,
		Phy: &phy.Config{
			Ethernet: map[string]phy.Ethernet{
				"1/1/1": {SupportedSpeedsBPS: []uint64{1_000_000_000}},
			},
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{"g1": {PowerMilliwatts: 15_400}},
				Ports:  map[string]phy.PsePort{"1/1/1": {Group: "g1", Enabled: true, MaxClass: 3}},
			},
		},
	}

	sw := vswitch.New(cfg)

	// Ports
	if sw.Ports().Len() != 1 {
		t.Errorf("got %d ports, want 1", sw.Ports().Len())
	}

	// Speeds
	speeds := sw.Speeds()
	if speeds == nil || len(speeds) != 1 {
		t.Errorf("expected 1 resolved speed, got %v", speeds)
	}

	// Power
	power := sw.Power()
	if power.Groups == nil || len(power.Groups) != 1 {
		t.Errorf("expected 1 group allocation, got %v", power.Groups)
	}

	// Config clone
	cloned := sw.Config()
	if cloned.Phy == nil {
		t.Errorf("cloned config missing phy")
	}
}

func TestSwitchAge(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	t.Run("ages dynamic entries", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: tbl,
			Bridge: &bridge.Config{
				AgingTime: 300 * time.Second,
			},
		}
		sw := vswitch.New(cfg)

		t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
		macA := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		macB := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee}

		sw.Forward(t0, "1/1/1", ethernet.Frame{Dst: macB, Src: macA})

		t200 := t0.Add(200 * time.Second)
		sw.Forward(t200, "1/1/2", ethernet.Frame{Dst: macA, Src: macB})

		if len(sw.Entries()) != 2 {
			t.Fatalf("got %d entries, want 2", len(sw.Entries()))
		}

		t250 := t0.Add(250 * time.Second)
		sw.Age(t250)
		if len(sw.Entries()) != 2 {
			t.Fatalf("at t0+250s got %d entries, want 2", len(sw.Entries()))
		}

		t301 := t0.Add(301 * time.Second)
		sw.Age(t301)
		entries := sw.Entries()
		if len(entries) != 1 {
			t.Fatalf("at t0+301s got %d entries, want 1", len(entries))
		}
		if entries[0].MAC != macB {
			t.Errorf("remaining entry MAC = %v, want %v", entries[0].MAC, macB)
		}

		t501 := t0.Add(501 * time.Second)
		sw.Age(t501)
		if len(sw.Entries()) != 0 {
			t.Fatalf("at t0+501s got %d entries, want 0", len(sw.Entries()))
		}
	})

	t.Run("no-op on hub", func(t *testing.T) {
		cfg := vswitch.Config{Ports: tbl}
		sw := vswitch.New(cfg)

		t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
		sw.Age(t0)
		if entries := sw.Entries(); entries != nil {
			t.Errorf("hub entries = %v, want nil", entries)
		}
	})
}
