package vswitch_test

import (
	"fmt"
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
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

func TestDeriveKeepsAnEntryLearnedOnATunnelPort(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	b := port.NewBuilder()
	b.Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	cfg := vswitch.Config{
		Ports: mustTable(t, b),
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: ""},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {Tunnel: &bridge.Tunnel{VID: 10}},
				"1/1/2": {Tagged: []vlan.ID{10}},
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
	if entries := next.Entries(); len(entries) != 1 || entries[0].FID != 10 || entries[0].Port != "1/1/1" {
		t.Errorf("derived Entries() = %+v, want the entry learned on the tunnel port in VLAN 10", entries)
	}
	if got := next.RelayCounters(); got.Learned != 1 {
		t.Errorf("derived RelayCounters() = %+v, want the reseed counted as one learn", got)
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
		if res.Egress[1].Port != "1/1/4" || res.Egress[1].Dropped != port.ReasonMTUExceeded {
			t.Errorf("egress 1: got %v, want 1/1/4 dropped for MTU exceeded", res.Egress[1])
		}
	})

	t.Run("ingress port down", func(t *testing.T) {
		res := sw.Forward(fixedTime, "1/1/3", f)
		if res.Outcome != trace.Dropped {
			t.Errorf("got outcome %v, want %v", res.Outcome, trace.Dropped)
		}
		if res.Reason != port.ReasonPortDown {
			t.Errorf("got reason %v, want %v", res.Reason, port.ReasonPortDown)
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

func TestCapabilitiesSTP(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01},
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}

	want := []port.Layer{port.LayerRelay, port.LayerStp}
	if caps := cfg.Capabilities(); !slices.Equal(caps, want) {
		t.Errorf("got capabilities %v, want %v", caps, want)
	}
}

func TestValidateRefusesSTPWithoutBridge(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	cfg := vswitch.Config{
		Ports: tbl,
		STP: &stp.Config{
			Priority: 32768,
			Address:  netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01},
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() = nil, want error when STP is configured without bridge")
	}
}

func TestBPDUConsumedWithEmissionDrained(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macSelf := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	macRoot := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macSelf,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}

	sw := vswitch.New(cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	sw.Start(now)
	sw.Drain()

	bpdu := stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: macRoot},
		RootPathCost: 0,
		BridgeID:     stp.BridgeID{Priority: 4096, Address: macRoot},
		PortID:       0x8001,
		HelloTime:    stp.DefaultHelloTime,
		MaxAge:       stp.DefaultMaxAge,
		ForwardDelay: stp.DefaultForwardDelay,
	}
	bpdu.SetRole(stp.RoleDesignated)
	bpdu.SetProposal(true)

	frame := stp.Encode(bpdu, macRoot)

	res := sw.Forward(now, "1/1/1", frame)
	if res.Outcome != trace.Consumed {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Consumed)
	}
	if len(res.Steps) != 1 || res.Steps[0].Layer != port.LayerStp || res.Steps[0].Op != trace.OpClassify {
		t.Errorf("res.Steps = %+v, want one classify step naming stp", res.Steps)
	}

	emissions := sw.Drain()
	if len(emissions) == 0 {
		t.Fatalf("Drain() returned 0 emissions, want at least 1")
	}
}

func TestBPDUOnLAGMemberConsumedOnLAG(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}).
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macSelf := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	macRoot := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macSelf,
			Ports: map[string]stp.Port{
				"lag1":  {},
				"1/1/3": {},
			},
		},
	}

	sw := vswitch.New(cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	sw.Start(now)
	sw.Drain()

	bpdu := stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: macRoot},
		RootPathCost: 0,
		BridgeID:     stp.BridgeID{Priority: 4096, Address: macRoot},
		PortID:       0x8001,
		HelloTime:    stp.DefaultHelloTime,
	}
	bpdu.SetRole(stp.RoleDesignated)

	frame := stp.Encode(bpdu, macRoot)

	res := sw.Forward(now, "1/1/1", frame)
	if res.Outcome != trace.Consumed {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Consumed)
	}
	if res.Ingress != "lag1" {
		t.Errorf("res.Ingress = %q, want %q", res.Ingress, "lag1")
	}
}

func TestHubRepeatsBPDU(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{Ports: tbl}
	sw := vswitch.New(cfg)

	macRoot := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}
	bpdu := stp.BPDU{
		RootID:   stp.BridgeID{Priority: 4096, Address: macRoot},
		BridgeID: stp.BridgeID{Priority: 4096, Address: macRoot},
	}
	frame := stp.Encode(bpdu, macRoot)

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	res := sw.Forward(now, "1/1/1", frame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Flooded)
	}
	if len(res.Egress) != 1 || res.Egress[0].Port != "1/1/2" {
		t.Errorf("res.Egress = %+v, want flooded to 1/1/2", res.Egress)
	}
}

func TestDiffSTPPriorityChange(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	mac := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	cfgA := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  mac,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}
	cfgB := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 4096,
			Address:  mac,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}

	changes := vswitch.Diff(cfgA, cfgB)
	found := false
	for _, ch := range changes {
		if ch.Layer == port.LayerStp && ch.Subject.Kind == "bridge" && ch.Field == "priority" {
			found = true
			if ch.From != uint16(32768) || ch.To != uint16(4096) {
				t.Errorf("priority change = %+v, want 32768 -> 4096", ch)
			}
		}
	}
	if !found {
		t.Fatalf("Diff() did not find stp priority change: %+v", changes)
	}
}

func TestPeekLeavesSTPLayerUntouched(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macSelf := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	macRoot := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macSelf,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}

	sw := vswitch.New(cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	sw.Start(now)
	sw.Drain()

	bpdu := stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: macRoot},
		RootPathCost: 0,
		BridgeID:     stp.BridgeID{Priority: 4096, Address: macRoot},
		PortID:       0x8001,
		HelloTime:    stp.DefaultHelloTime,
	}
	bpdu.SetRole(stp.RoleDesignated)
	bpdu.SetProposal(true)

	frame := stp.Encode(bpdu, macRoot)

	res := sw.Peek(now, "1/1/1", frame)
	if res.Outcome != trace.Consumed {
		t.Fatalf("Peek outcome = %q, want %q", res.Outcome, trace.Consumed)
	}

	if emissions := sw.Drain(); len(emissions) != 0 {
		t.Errorf("Peek generated emissions: %+v, want none", emissions)
	}

	roles := sw.Roles()
	if roles["1/1/1"].Role == stp.RoleRoot {
		t.Errorf("Peek modified role of 1/1/1 to Root")
	}
}

func TestUndecodableBPDUIncrementsBadBPDUs(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macSelf := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macSelf,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}

	sw := vswitch.New(cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	sw.Start(now)
	sw.Drain()

	badFrame := ethernet.Frame{
		Dst:     netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00},
		Src:     netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02},
		Payload: []byte{0x01, 0x02, 0x03},
	}

	res := sw.Forward(now, "1/1/1", badFrame)
	if res.Outcome != trace.Dropped {
		t.Fatalf("Forward outcome = %q, want %q", res.Outcome, trace.Dropped)
	}
	if res.Reason != stp.ReasonUnsupportedBPDU {
		t.Errorf("Forward reason = %q, want %q", res.Reason, stp.ReasonUnsupportedBPDU)
	}
	if got := sw.Roles()["1/1/1"].BadBPDUs; got != 1 {
		t.Fatalf("Roles()[\"1/1/1\"].BadBPDUs = %d, want 1", got)
	}

	resPeek := sw.Peek(now, "1/1/1", badFrame)
	if resPeek.Outcome != trace.Dropped {
		t.Fatalf("Peek outcome = %q, want %q", resPeek.Outcome, trace.Dropped)
	}
	if resPeek.Reason != stp.ReasonUnsupportedBPDU {
		t.Errorf("Peek reason = %q, want %q", resPeek.Reason, stp.ReasonUnsupportedBPDU)
	}
	if got := sw.Roles()["1/1/1"].BadBPDUs; got != 1 {
		t.Errorf("Roles()[\"1/1/1\"].BadBPDUs after Peek = %d, want 1", got)
	}
}

func TestSwitchMigrationToLegacySTPAndMcheck(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macSelf := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x02}
	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macSelf,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}

	sw := vswitch.New(cfg)
	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	sw.Start(t0)
	sw.Drain()

	sw.Wake(t0.Add(2 * time.Second))
	sw.Drain()

	sw.Wake(t0.Add(4 * time.Second))
	sw.Drain()

	inferiorBridgeID := stp.BridgeID{
		Priority: 61440,
		Address:  netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0c},
	}
	inferiorBPDU := stp.BPDU{
		Type:         stp.BPDUTypeConfiguration,
		RootID:       inferiorBridgeID,
		BridgeID:     inferiorBridgeID,
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	frame := stp.Encode(inferiorBPDU, inferiorBridgeID.Address)

	sw.Forward(t0.Add(4*time.Second), "1/1/1", frame)

	emissions := sw.Drain()
	if len(emissions) == 0 {
		t.Fatal("Drain() returned 0 emissions, want reply emission")
	}

	var replyEmission *stp.Emission
	for i := range emissions {
		if emissions[i].Port == "1/1/1" {
			replyEmission = &emissions[i]
			break
		}
	}
	if replyEmission == nil {
		t.Fatalf("no emission found on port 1/1/1: %+v", emissions)
	}

	replyBPDU, err := stp.Decode(replyEmission.Frame)
	if err != nil {
		t.Fatalf("stp.Decode(replyEmission.Frame) failed: %v", err)
	}
	if replyBPDU.Type != stp.BPDUTypeConfiguration {
		t.Errorf("replyBPDU.Type = %v, want %v (Configuration)", replyBPDU.Type, stp.BPDUTypeConfiguration)
	}
	if sw.Roles()["1/1/1"].SendRSTP {
		t.Errorf("Roles()[\"1/1/1\"].SendRSTP = true, want false")
	}

	sw.Mcheck(t0.Add(10*time.Second), "1/1/1")
	if !sw.Roles()["1/1/1"].SendRSTP {
		t.Errorf("Roles()[\"1/1/1\"].SendRSTP after Mcheck = false, want true")
	}
}

func TestDerivedSwitchKeepsRootPortForwarding(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macSelf := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	macRoot := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macSelf,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}

	sw := vswitch.New(cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	sw.Start(now)
	sw.Drain()

	bpdu := stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: macRoot},
		RootPathCost: 0,
		BridgeID:     stp.BridgeID{Priority: 4096, Address: macRoot},
		PortID:       0x8001,
		HelloTime:    stp.DefaultHelloTime,
		MaxAge:       stp.DefaultMaxAge,
		ForwardDelay: stp.DefaultForwardDelay,
	}
	bpdu.SetRole(stp.RoleDesignated)
	bpdu.SetProposal(true)

	sw.Forward(now, "1/1/1", stp.Encode(bpdu, macRoot))

	rolesBefore := sw.Roles()
	if rolesBefore["1/1/1"].Role != stp.RoleRoot || rolesBefore["1/1/1"].State != stp.StateForwarding {
		t.Fatalf("1/1/1 before derive = %s/%s, want Root/Forwarding", rolesBefore["1/1/1"].Role, rolesBefore["1/1/1"].State)
	}

	derived, err := vswitch.Derive(sw, cfg)
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	rolesAfter := derived.Roles()
	if rolesAfter["1/1/1"].Role != stp.RoleRoot || rolesAfter["1/1/1"].State != stp.StateForwarding {
		t.Errorf("1/1/1 after derive = %s/%s, want Root/Forwarding", rolesAfter["1/1/1"].Role, rolesAfter["1/1/1"].State)
	}
}

// TestDerivedSwitchKeepsRolesWithAssignedBridgeAddress is evidence that
// Derive compares the spanning tree configuration New filled in, so a bridge
// address the switch assigned does not read as a change that rebuilds the
// layer.
func TestDerivedSwitchKeepsRolesWithAssignedBridgeAddress(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macRoot := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}
	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Ports:    map[string]stp.Port{"1/1/1": {}, "1/1/2": {}},
		},
	}

	sw := vswitch.New(cfg)
	if sw.Config().STP.Address == (netaddr.MAC{}) {
		t.Fatal("New left the bridge address zero")
	}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	sw.Start(now)
	sw.Drain()

	bpdu := stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: macRoot},
		BridgeID:     stp.BridgeID{Priority: 4096, Address: macRoot},
		PortID:       0x8001,
		HelloTime:    stp.DefaultHelloTime,
		MaxAge:       stp.DefaultMaxAge,
		ForwardDelay: stp.DefaultForwardDelay,
	}
	bpdu.SetRole(stp.RoleDesignated)
	bpdu.SetProposal(true)
	sw.Forward(now, "1/1/1", stp.Encode(bpdu, macRoot))
	if before := sw.Roles()["1/1/1"]; before.Role != stp.RoleRoot || before.State != stp.StateForwarding {
		t.Fatalf("1/1/1 before derive = %s/%s, want Root/Forwarding", before.Role, before.State)
	}

	derived, err := vswitch.Derive(sw, cfg)
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}
	if after := derived.Roles()["1/1/1"]; after.Role != stp.RoleRoot || after.State != stp.StateForwarding {
		t.Errorf("1/1/1 after derive = %s/%s, want Root/Forwarding", after.Role, after.State)
	}
	if derived.BridgeID().Address != sw.BridgeID().Address {
		t.Errorf("derived bridge address = %s, want %s", derived.BridgeID().Address, sw.BridgeID().Address)
	}
}

// TestBPDUOnDownPortIsDropped is evidence that the intercept applies the
// relay's port check: a BPDU on a port that is not up never reaches the
// layer, and its trace says so instead of Consumed.
func TestBPDUOnDownPortIsDropped(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macSelf := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	macRoot := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}
	sw := vswitch.New(vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macSelf,
			Ports:    map[string]stp.Port{"1/1/1": {}, "1/1/2": {}},
		},
	})
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	sw.Start(now)
	sw.Drain()

	bpdu := stp.BPDU{
		RootID:    stp.BridgeID{Priority: 4096, Address: macRoot},
		BridgeID:  stp.BridgeID{Priority: 4096, Address: macRoot},
		PortID:    0x8001,
		HelloTime: stp.DefaultHelloTime,
		MaxAge:    stp.DefaultMaxAge,
	}
	bpdu.SetRole(stp.RoleDesignated)

	res := sw.Forward(now, "1/1/1", stp.Encode(bpdu, macRoot))
	if res.Outcome != trace.Dropped || res.Reason != port.ReasonPortDown {
		t.Fatalf("BPDU on down port = %s/%s, want Dropped/%s", res.Outcome, res.Reason, port.ReasonPortDown)
	}
	if root, _, _ := sw.Root(); root.Address == macRoot {
		t.Error("layer adopted a root from a BPDU on a down port")
	}
}

func makeIPv4Packet(t *testing.T, src, dst netip.Addr, ttl uint8, payload []byte) []byte {
	t.Helper()
	hdr := ip.Header{
		Src:      src,
		Dst:      dst,
		HopLimit: ttl,
		Protocol: 17,
		V4:       &ip.V4{},
	}
	pkt, err := hdr.Encode(payload)
	if err != nil {
		t.Fatalf("encode IPv4 packet: %v", err)
	}
	return pkt
}

func makeIPv6Packet(t *testing.T, src, dst netip.Addr, hopLimit uint8, payload []byte) []byte {
	t.Helper()
	hdr := ip.Header{
		Src:      src,
		Dst:      dst,
		HopLimit: hopLimit,
		Protocol: 17,
		V6:       &ip.V6{},
	}
	pkt, err := hdr.Encode(payload)
	if err != nil {
		t.Fatalf("encode IPv6 packet: %v", err)
	}
	return pkt
}

func assertSteps(t *testing.T, got []trace.Step, want []trace.Step) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("step count mismatch: got %d, want %d\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i].Layer != want[i].Layer || got[i].Op != want[i].Op || got[i].Detail != want[i].Detail {
			t.Errorf("step %d mismatch:\ngot:  %+v\nwant: %+v", i, got[i], want[i])
		}
	}
}

var (
	macRouter = netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	macH1     = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	macH2     = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	ipH1      = netip.MustParseAddr("10.0.10.7")
	ipH2      = netip.MustParseAddr("10.0.20.7")
	ipR1      = netip.MustParseAddr("10.0.10.1")
)

func buildBaseRoutingSwitch(t *testing.T) *vswitch.Switch {
	t.Helper()
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
					},
				},
			},
		},
	}
	return vswitch.New(cfg)
}

func TestCapabilitiesWithRouting(t *testing.T) {
	portsOnly := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	t.Run("relay routing vlan", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: portsOnly,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{10}},
					},
				},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		want := []port.Layer{port.LayerRelay, port.LayerRouting, port.LayerVlan}
		if caps := cfg.Capabilities(); !slices.Equal(caps, want) {
			t.Errorf("got capabilities %v, want %v", caps, want)
		}
	})

	t.Run("routing alone for router", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: portsOnly,
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
							"1/1/2": {Port: "1/1/2", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						},
					},
				},
			},
		}
		want := []port.Layer{port.LayerRouting}
		if caps := cfg.Capabilities(); !slices.Equal(caps, want) {
			t.Errorf("got capabilities %v, want %v", caps, want)
		}
	})
}

func TestCrossLayerValidation(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	t.Run("routed VLAN interface requires bridge VLAN", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for missing bridge VLAN")
		}
		attrs := errs.Attributes(err)
		if attrs["vrf"] != "default" || attrs["interface"] != "vlan10" || attrs["vlan"] != vlan.ID(10) {
			t.Errorf("got attrs %v, want vrf=default, interface=vlan10, vlan=10", attrs)
		}
	})

	t.Run("routed port refused on a relay without VLAN", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports:  ports,
			Bridge: &bridge.Config{},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/2": {Port: "1/1/2", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for a routed port on a relay that cannot filter it")
		}
		if attrs := errs.Attributes(err); attrs["port"] != "1/1/2" {
			t.Errorf("got attrs %v, want port=1/1/2", attrs)
		}
	})

	t.Run("routed VLAN interface requires VLAN in table", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{20: "vlan20"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{20}},
					},
				},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for VLAN missing from table")
		}
		attrs := errs.Attributes(err)
		if attrs["vrf"] != "default" || attrs["interface"] != "vlan10" || attrs["vlan"] != vlan.ID(10) {
			t.Errorf("got attrs %v, want vrf=default, interface=vlan10, vlan=10", attrs)
		}
	})

	t.Run("routed port cannot appear in switchports", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{10}},
					},
				},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for routed port in switchports")
		}
		attrs := errs.Attributes(err)
		if attrs["vrf"] != "default" || attrs["interface"] != "1/1/1" || attrs["port"] != "1/1/1" {
			t.Errorf("got attrs %v, want vrf=default, interface=1/1/1, port=1/1/1", attrs)
		}
	})

	t.Run("routed port cannot appear in STP ports", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/2": {Untagged: []vlan.ID{10}},
					},
				},
			},
			STP: &stp.Config{
				Priority: 32768,
				Ports:    map[string]stp.Port{"1/1/1": {Priority: 128}},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for routed port in STP ports")
		}
		attrs := errs.Attributes(err)
		if attrs["vrf"] != "default" || attrs["interface"] != "1/1/1" || attrs["port"] != "1/1/1" {
			t.Errorf("got attrs %v, want vrf=default, interface=1/1/1, port=1/1/1", attrs)
		}
	})

	t.Run("router without bridge must route every non-LAG port", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for unrouted port on router")
		}
		attrs := errs.Attributes(err)
		if attrs["port"] != "1/1/2" {
			t.Errorf("got attrs %v, want port=1/1/2", attrs)
		}
	})

	t.Run("router without bridge routes all non-LAG ports successfully", func(t *testing.T) {
		lagPorts := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1"}).
			Add(port.Port{Name: "lag1", Kind: port.Lag}))

		cfg := vswitch.Config{
			Ports: lagPorts,
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
							"lag1":  {Port: "lag1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						},
					},
				},
			},
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate failed: %v", err)
		}
	})
}

func TestBaseMACAssignment(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	t.Run("all zero MACs assigned base address", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{10}},
					},
				},
			},
			STP: &stp.Config{
				Priority: 32768,
				Ports:    map[string]stp.Port{"1/1/1": {Priority: 128}},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		sw := vswitch.New(cfg)
		wantMAC := netaddr.Local(1)
		if sw.Config().MAC != wantMAC {
			t.Errorf("sw.Config().MAC = %v, want %v", sw.Config().MAC, wantMAC)
		}
		if iface := sw.Config().Routing.VRFs["default"].Interfaces["vlan10"]; iface.MAC != wantMAC {
			t.Errorf("routed interface MAC = %v, want %v", iface.MAC, wantMAC)
		}
		if sw.Config().STP.Address != wantMAC {
			t.Errorf("STP config address = %v, want %v", sw.Config().STP.Address, wantMAC)
		}
		if sw.BridgeID().Address != wantMAC {
			t.Errorf("sw.BridgeID().Address = %v, want %v", sw.BridgeID().Address, wantMAC)
		}
	})

	t.Run("explicit interface MAC preserved beside assigned base", func(t *testing.T) {
		explicitMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
		cfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{10}},
						"1/1/2": {Untagged: []vlan.ID{20}},
					},
				},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, MAC: explicitMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
							"vlan20": {VLAN: 20, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						},
					},
				},
			},
		}
		sw := vswitch.New(cfg)
		wantBase := netaddr.Local(1)
		if sw.Config().MAC != wantBase {
			t.Errorf("sw.Config().MAC = %v, want %v", sw.Config().MAC, wantBase)
		}
		if iface10 := sw.Config().Routing.VRFs["default"].Interfaces["vlan10"]; iface10.MAC != explicitMAC {
			t.Errorf("vlan10 MAC = %v, want %v", iface10.MAC, explicitMAC)
		}
		if iface20 := sw.Config().Routing.VRFs["default"].Interfaces["vlan20"]; iface20.MAC != wantBase {
			t.Errorf("vlan20 MAC = %v, want %v", iface20.MAC, wantBase)
		}
	})

	t.Run("derived switch keeps base MAC", func(t *testing.T) {
		cfg := vswitch.Config{
			MAC:   netaddr.Local(42),
			Ports: ports,
		}
		cur := vswitch.New(cfg)
		nextCfg := vswitch.Config{
			Ports: ports,
		}
		nextSw, err := vswitch.Derive(cur, nextCfg)
		if err != nil {
			t.Fatalf("Derive failed: %v", err)
		}
		if nextSw.Config().MAC != netaddr.Local(42) {
			t.Errorf("nextSw.Config().MAC = %v, want %v", nextSw.Config().MAC, netaddr.Local(42))
		}
	})
}

func TestVLANToVLANRouting(t *testing.T) {
	sw := buildBaseRoutingSwitch(t)

	// Pre-learn neighbor's MAC on 1/1/2 so the relay hits.
	sw.Forward(fixedTime, "1/1/2", ethernet.Frame{
		Src:       macH2,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   makeIPv4Packet(t, ipH2, ipH1, 64, []byte("learn")),
	})

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("hello"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := sw.Forward(fixedTime, "1/1/1", frame)

	wantSteps := []trace.Step{
		{Layer: port.LayerVlan, Op: trace.OpClassify, Detail: "vlan 10"},
		{Layer: port.LayerRelay, Op: trace.OpLearn, Detail: "00:11:22:33:44:11 -> 1/1/1"},
		{Layer: port.LayerRouting, Op: trace.OpClassify, Detail: "vrf default interface vlan10"},
		{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "10.0.20.0/24 connected vlan20"},
		{Layer: port.LayerRouting, Op: trace.OpRewrite, Detail: "hop limit 64 to 63, src 00:00:5e:00:01:01, dst 00:11:22:33:44:77"},
		{Layer: port.LayerRelay, Op: trace.OpLookup, Detail: "hit 1/1/2"},
		{Layer: port.LayerVlan, Op: trace.OpRewrite, Detail: "port 1/1/2 egress tag form"},
		{Layer: port.LayerRelay, Op: trace.OpTransmit, Detail: "port 1/1/2"},
	}
	assertSteps(t, res.Steps, wantSteps)

	if res.Outcome != trace.Forwarded {
		t.Errorf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
	}
	if res.FID != 20 {
		t.Errorf("res.FID = %d, want 20", res.FID)
	}
	if res.Ingress != "1/1/1" {
		t.Errorf("res.Ingress = %q, want 1/1/1", res.Ingress)
	}
	if len(res.Egress) != 1 {
		t.Fatalf("len(res.Egress) = %d, want 1", len(res.Egress))
	}
	egress := res.Egress[0]
	if egress.Port != "1/1/2" {
		t.Errorf("egress.Port = %q, want 1/1/2", egress.Port)
	}
	if egress.Frame.Src != macRouter {
		t.Errorf("egress.Frame.Src = %v, want %v", egress.Frame.Src, macRouter)
	}
	if egress.Frame.Dst != macH2 {
		t.Errorf("egress.Frame.Dst = %v, want %v", egress.Frame.Dst, macH2)
	}
	if len(egress.Frame.Tags) != 0 {
		t.Errorf("egress.Frame.Tags = %v, want untagged", egress.Frame.Tags)
	}

	hdr, _, err := ip.Decode(egress.Frame.Payload)
	if err != nil {
		t.Fatalf("decode routed payload: %v", err)
	}
	if hdr.HopLimit != 63 {
		t.Errorf("hdr.HopLimit = %d, want 63", hdr.HopLimit)
	}
	if hdr.Src != ipH1 || hdr.Dst != ipH2 {
		t.Errorf("hdr.Src=%v, Dst=%v, want %v, %v", hdr.Src, hdr.Dst, ipH1, ipH2)
	}

	// Flooded variant when FDB has not learned the neighbor MAC on 1/1/2.
	swEmpty := buildBaseRoutingSwitch(t)
	resFlood := swEmpty.Forward(fixedTime, "1/1/1", frame)
	wantFloodSteps := []trace.Step{
		{Layer: port.LayerVlan, Op: trace.OpClassify, Detail: "vlan 10"},
		{Layer: port.LayerRelay, Op: trace.OpLearn, Detail: "00:11:22:33:44:11 -> 1/1/1"},
		{Layer: port.LayerRouting, Op: trace.OpClassify, Detail: "vrf default interface vlan10"},
		{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "10.0.20.0/24 connected vlan20"},
		{Layer: port.LayerRouting, Op: trace.OpRewrite, Detail: "hop limit 64 to 63, src 00:00:5e:00:01:01, dst 00:11:22:33:44:77"},
		{Layer: port.LayerRelay, Op: trace.OpLookup, Detail: "unicast miss"},
		{Layer: port.LayerRelay, Op: trace.OpReplicate, Detail: "1 candidate ports"},
		{Layer: port.LayerVlan, Op: trace.OpRewrite, Detail: "port 1/1/2 egress tag form"},
		{Layer: port.LayerRelay, Op: trace.OpTransmit, Detail: "port 1/1/2"},
	}
	assertSteps(t, resFlood.Steps, wantFloodSteps)
	if resFlood.Outcome != trace.Flooded {
		t.Errorf("resFlood.Outcome = %v, want %v", resFlood.Outcome, trace.Flooded)
	}
	if resFlood.FID != 20 {
		t.Errorf("resFlood.FID = %d, want 20", resFlood.FID)
	}
}

func TestNeighborMissDrops(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					// No neighbors configured.
				},
			},
		},
	}
	sw := vswitch.New(cfg)

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("hello"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := sw.Forward(fixedTime, "1/1/1", frame)

	wantSteps := []trace.Step{
		{Layer: port.LayerVlan, Op: trace.OpClassify, Detail: "vlan 10"},
		{Layer: port.LayerRelay, Op: trace.OpLearn, Detail: "00:11:22:33:44:11 -> 1/1/1"},
		{Layer: port.LayerRouting, Op: trace.OpClassify, Detail: "vrf default interface vlan10"},
		{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "10.0.20.0/24 connected vlan20"},
		{Layer: port.LayerRouting, Op: trace.OpDrop, Detail: "vrf default interface vlan20 address 10.0.20.7"},
	}
	assertSteps(t, res.Steps, wantSteps)

	if res.Outcome != trace.Dropped {
		t.Errorf("outcome = %v, want %v", res.Outcome, trace.Dropped)
	}
	if res.Reason != routing.ReasonNeighborMiss {
		t.Errorf("reason = %v, want %v", res.Reason, routing.ReasonNeighborMiss)
	}
	if len(res.Egress) != 0 {
		t.Errorf("len(res.Egress) = %d, want 0", len(res.Egress))
	}
	if res.FID != 10 {
		t.Errorf("res.FID = %d, want 10", res.FID)
	}
	if res.Ingress != "1/1/1" {
		t.Errorf("res.Ingress = %q, want 1/1/1", res.Ingress)
	}
}

func TestRoutingTTLLocalBadHeaderAndNoRouteDrops(t *testing.T) {
	sw := buildBaseRoutingSwitch(t)

	t.Run("ttl expired", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipH2, 1, []byte("hello"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Forward(fixedTime, "1/1/1", frame)
		wantSteps := []trace.Step{
			{Layer: port.LayerVlan, Op: trace.OpClassify, Detail: "vlan 10"},
			{Layer: port.LayerRelay, Op: trace.OpLearn, Detail: "00:11:22:33:44:11 -> 1/1/1"},
			{Layer: port.LayerRouting, Op: trace.OpClassify, Detail: "vrf default interface vlan10"},
			{Layer: port.LayerRouting, Op: trace.OpDrop, Detail: "ttl-expired"},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonTTLExpired {
			t.Errorf("got outcome=%v reason=%v, want Dropped/ttl-expired", res.Outcome, res.Reason)
		}
		if len(res.Egress) != 0 {
			t.Errorf("egress count = %d, want 0", len(res.Egress))
		}
	})

	t.Run("packet to local device address consumed", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipR1, 64, []byte("hello"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Forward(fixedTime, "1/1/1", frame)
		wantSteps := []trace.Step{
			{Layer: port.LayerVlan, Op: trace.OpClassify, Detail: "vlan 10"},
			{Layer: port.LayerRelay, Op: trace.OpLearn, Detail: "00:11:22:33:44:11 -> 1/1/1"},
			{Layer: port.LayerRouting, Op: trace.OpClassify, Detail: "vrf default interface vlan10"},
			{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "10.0.10.1"},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Consumed || res.Reason != routing.ReasonNotRouted {
			t.Errorf("got outcome=%v reason=%v, want Consumed/not-routed", res.Outcome, res.Reason)
		}
	})

	t.Run("bad header checksum", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("hello"))
		pkt[11] ^= 0x01
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Forward(fixedTime, "1/1/1", frame)
		wantSteps := []trace.Step{
			{Layer: port.LayerVlan, Op: trace.OpClassify, Detail: "vlan 10"},
			{Layer: port.LayerRelay, Op: trace.OpLearn, Detail: "00:11:22:33:44:11 -> 1/1/1"},
			{Layer: port.LayerRouting, Op: trace.OpClassify, Detail: "vrf default interface vlan10"},
			{Layer: port.LayerRouting, Op: trace.OpDrop, Detail: "bad-header"},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonBadHeader {
			t.Errorf("got outcome=%v reason=%v, want Dropped/bad-header", res.Outcome, res.Reason)
		}
	})

	t.Run("no route", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, netip.MustParseAddr("10.0.30.7"), 64, []byte("hello"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Forward(fixedTime, "1/1/1", frame)
		wantSteps := []trace.Step{
			{Layer: port.LayerVlan, Op: trace.OpClassify, Detail: "vlan 10"},
			{Layer: port.LayerRelay, Op: trace.OpLearn, Detail: "00:11:22:33:44:11 -> 1/1/1"},
			{Layer: port.LayerRouting, Op: trace.OpClassify, Detail: "vrf default interface vlan10"},
			{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "no route"},
			{Layer: port.LayerRouting, Op: trace.OpDrop, Detail: "vrf default"},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonNoRoute {
			t.Errorf("got outcome=%v reason=%v, want Dropped/no-route", res.Outcome, res.Reason)
		}
	})
}

func TestIPv6Routing(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	ip6Src := netip.MustParseAddr("2001:db8:10::7")
	ip6Dst := netip.MustParseAddr("2001:db8:20::7")
	ip6Off := netip.MustParseAddr("2001:db8:30::7")

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {
							VLAN: 10,
							MAC:  macRouter,
							Prefixes: []netip.Prefix{
								netip.MustParsePrefix("10.0.10.1/24"),
								netip.MustParsePrefix("2001:db8:10::1/64"),
							},
						},
						"vlan20": {
							VLAN: 20,
							MAC:  macRouter,
							Prefixes: []netip.Prefix{
								netip.MustParsePrefix("10.0.20.1/24"),
								netip.MustParsePrefix("2001:db8:20::1/64"),
							},
						},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
						{Interface: "vlan20", Addr: ip6Dst, MAC: macH2},
					},
				},
			},
		},
	}
	sw := vswitch.New(cfg)

	// Pre-learn on 1/1/2
	sw.Forward(fixedTime, "1/1/2", ethernet.Frame{
		Src:       macH2,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv6,
		Payload:   makeIPv6Packet(t, ip6Dst, ip6Src, 64, []byte("learn")),
	})

	t.Run("forwarded on vlan 20 with hop limit 63", func(t *testing.T) {
		pkt := makeIPv6Packet(t, ip6Src, ip6Dst, 64, []byte("ipv6 hello"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv6,
			Payload:   pkt,
		}

		res := sw.Forward(fixedTime, "1/1/1", frame)
		wantSteps := []trace.Step{
			{Layer: port.LayerVlan, Op: trace.OpClassify, Detail: "vlan 10"},
			{Layer: port.LayerRelay, Op: trace.OpLearn, Detail: "00:11:22:33:44:11 -> 1/1/1"},
			{Layer: port.LayerRouting, Op: trace.OpClassify, Detail: "vrf default interface vlan10"},
			{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "2001:db8:20::/64 connected vlan20"},
			{Layer: port.LayerRouting, Op: trace.OpRewrite, Detail: "hop limit 64 to 63, src 00:00:5e:00:01:01, dst 00:11:22:33:44:77"},
			{Layer: port.LayerRelay, Op: trace.OpLookup, Detail: "hit 1/1/2"},
			{Layer: port.LayerVlan, Op: trace.OpRewrite, Detail: "port 1/1/2 egress tag form"},
			{Layer: port.LayerRelay, Op: trace.OpTransmit, Detail: "port 1/1/2"},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Forwarded {
			t.Errorf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
		}
		if res.FID != 20 {
			t.Errorf("FID = %d, want 20", res.FID)
		}

		hdr, _, err := ip.Decode(res.Egress[0].Frame.Payload)
		if err != nil {
			t.Fatalf("decode egress IPv6: %v", err)
		}
		if hdr.HopLimit != 63 {
			t.Errorf("hdr.HopLimit = %d, want 63", hdr.HopLimit)
		}
	})

	t.Run("off-link destination ends no-route and IPv4 is unaffected", func(t *testing.T) {
		pkt := makeIPv6Packet(t, ip6Src, ip6Off, 64, []byte("off"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv6,
			Payload:   pkt,
		}
		res := sw.Forward(fixedTime, "1/1/1", frame)
		wantSteps := []trace.Step{
			{Layer: port.LayerVlan, Op: trace.OpClassify, Detail: "vlan 10"},
			{Layer: port.LayerRelay, Op: trace.OpLearn, Detail: "00:11:22:33:44:11 -> 1/1/1"},
			{Layer: port.LayerRouting, Op: trace.OpClassify, Detail: "vrf default interface vlan10"},
			{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "no route"},
			{Layer: port.LayerRouting, Op: trace.OpDrop, Detail: "vrf default"},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonNoRoute {
			t.Errorf("outcome=%v reason=%v, want Dropped/no-route", res.Outcome, res.Reason)
		}

		// IPv4 to unrouted destination still ends no-route unaffected by IPv6 routes.
		v4Pkt := makeIPv4Packet(t, ipH1, netip.MustParseAddr("10.0.30.7"), 64, []byte("test"))
		v4Frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   v4Pkt,
		}
		v4Res := sw.Forward(fixedTime, "1/1/1", v4Frame)
		if v4Res.Outcome != trace.Dropped || v4Res.Reason != routing.ReasonNoRoute {
			t.Errorf("v4Res outcome=%v reason=%v, want Dropped/no-route", v4Res.Outcome, v4Res.Reason)
		}
	})
}

func TestVRFIsolation(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)
	p30 := vlan.ID(30)
	p40 := vlan.ID(40)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macTenantRouter := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x02}
	macTenantH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20", 30: "vlan30", 40: "vlan40"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
					"1/1/3": {PVID: &p30, Untagged: []vlan.ID{30}},
					"1/1/4": {PVID: &p40, Untagged: []vlan.ID{40}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					Routes: []routing.Route{
						{Prefix: netip.MustParsePrefix("10.0.88.0/24"), Interface: "vlan20"},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
					},
				},
				"tenant": {
					Interfaces: map[string]routing.Interface{
						"vlan30": {VLAN: 30, MAC: macTenantRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan40": {VLAN: 40, MAC: macTenantRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan40", Addr: ipH2, MAC: macTenantH2},
					},
				},
			},
		},
	}
	sw := vswitch.New(cfg)

	// Pre-learn on 1/1/2 and 1/1/4
	sw.Forward(fixedTime, "1/1/2", ethernet.Frame{
		Src:       macH2,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   makeIPv4Packet(t, ipH2, ipH1, 64, []byte("learn")),
	})
	sw.Forward(fixedTime, "1/1/4", ethernet.Frame{
		Src:       macTenantH2,
		Dst:       macTenantRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   makeIPv4Packet(t, ipH2, ipH1, 64, []byte("learn")),
	})

	t.Run("tenant routes in vlan 40 to tenant neighbor", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("tenant hello"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macTenantRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Forward(fixedTime, "1/1/3", frame)
		wantSteps := []trace.Step{
			{Layer: port.LayerVlan, Op: trace.OpClassify, Detail: "vlan 30"},
			{Layer: port.LayerRelay, Op: trace.OpLearn, Detail: "00:11:22:33:44:11 -> 1/1/3"},
			{Layer: port.LayerRouting, Op: trace.OpClassify, Detail: "vrf tenant interface vlan30"},
			{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "10.0.20.0/24 connected vlan40"},
			{Layer: port.LayerRouting, Op: trace.OpRewrite, Detail: "hop limit 64 to 63, src 00:00:5e:00:01:02, dst 00:11:22:33:44:99"},
			{Layer: port.LayerRelay, Op: trace.OpLookup, Detail: "hit 1/1/4"},
			{Layer: port.LayerVlan, Op: trace.OpRewrite, Detail: "port 1/1/4 egress tag form"},
			{Layer: port.LayerRelay, Op: trace.OpTransmit, Detail: "port 1/1/4"},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Forwarded {
			t.Errorf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
		}
		if res.FID != 40 {
			t.Errorf("FID = %d, want 40", res.FID)
		}
		if res.Egress[0].Frame.Dst != macTenantH2 {
			t.Errorf("egress destination = %v, want %v", res.Egress[0].Frame.Dst, macTenantH2)
		}
	})

	t.Run("default still routes to default neighbor", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("default hello"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Forward(fixedTime, "1/1/1", frame)
		if res.FID != 20 || res.Egress[0].Frame.Dst != macH2 {
			t.Errorf("default routed frame FID=%d Dst=%v, want 20, %v", res.FID, res.Egress[0].Frame.Dst, macH2)
		}
	})

	t.Run("packet in tenant VRF to address only default has route for ends no-route", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, netip.MustParseAddr("10.0.88.7"), 64, []byte("exclusive"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macTenantRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Forward(fixedTime, "1/1/3", frame)
		wantSteps := []trace.Step{
			{Layer: port.LayerVlan, Op: trace.OpClassify, Detail: "vlan 30"},
			{Layer: port.LayerRelay, Op: trace.OpLearn, Detail: "00:11:22:33:44:11 -> 1/1/3"},
			{Layer: port.LayerRouting, Op: trace.OpClassify, Detail: "vrf tenant interface vlan30"},
			{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "no route"},
			{Layer: port.LayerRouting, Op: trace.OpDrop, Detail: "vrf tenant"},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonNoRoute {
			t.Errorf("got outcome=%v reason=%v, want Dropped/no-route", res.Outcome, res.Reason)
		}
	})

	t.Run("duplicate VLAN across VRFs fails Validate", func(t *testing.T) {
		badCfg := cfg.Clone()
		tenantVRF := badCfg.Routing.VRFs["tenant"]
		tenantVRF.Interfaces["dup"] = routing.Interface{VLAN: 10, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.99.1/24")}}
		badCfg.Routing.VRFs["tenant"] = tenantVRF
		if err := badCfg.Validate(); err == nil {
			t.Error("Validate succeeded, want error for duplicate VLAN across VRFs")
		}
	})
}

func TestRoutedPortForwardingAndBypassRelay(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/5", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macPort5Neighbor := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	ipPort5Dst := netip.MustParseAddr("10.0.50.7")

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						"1/1/5":  {Port: "1/1/5", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
						{Interface: "1/1/5", Addr: ipPort5Dst, MAC: macPort5Neighbor},
					},
				},
			},
		},
	}
	sw := vswitch.New(cfg)
	baseMAC := sw.Config().MAC

	t.Run("packet from VLAN to routed port egresses untagged with transmit step", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipPort5Dst, 64, []byte("to port 5"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Forward(fixedTime, "1/1/1", frame)
		wantSteps := []trace.Step{
			{Layer: port.LayerVlan, Op: trace.OpClassify, Detail: "vlan 10"},
			{Layer: port.LayerRelay, Op: trace.OpLearn, Detail: "00:11:22:33:44:11 -> 1/1/1"},
			{Layer: port.LayerRouting, Op: trace.OpClassify, Detail: "vrf default interface vlan10"},
			{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "10.0.50.0/24 connected 1/1/5"},
			{Layer: port.LayerRouting, Op: trace.OpRewrite, Detail: fmt.Sprintf("hop limit 64 to 63, src %s, dst 00:11:22:33:44:55", baseMAC)},
			{Layer: port.LayerRouting, Op: trace.OpTransmit, Detail: "port 1/1/5"},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Forwarded {
			t.Errorf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
		}
		if res.FID != 0 {
			t.Errorf("res.FID = %d, want 0 on routed port", res.FID)
		}
		if res.Ingress != "1/1/1" {
			t.Errorf("res.Ingress = %q, want 1/1/1", res.Ingress)
		}
		if len(res.Egress) != 1 || res.Egress[0].Port != "1/1/5" {
			t.Fatalf("res.Egress = %+v, want 1 entry on 1/1/5", res.Egress)
		}
		if len(res.Egress[0].Frame.Tags) != 0 {
			t.Errorf("res.Egress[0].Frame.Tags = %v, want untagged", res.Egress[0].Frame.Tags)
		}
	})

	t.Run("packet on routed port to base MAC routes to VLAN with no relay ingress step", func(t *testing.T) {
		// Pre-learn on 1/1/2
		sw.Forward(fixedTime, "1/1/2", ethernet.Frame{
			Src:       macH2,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   makeIPv4Packet(t, ipH2, ipH1, 64, []byte("learn")),
		})

		pkt := makeIPv4Packet(t, ipPort5Dst, ipH2, 64, []byte("from port 5"))
		frame := ethernet.Frame{
			Src:       macPort5Neighbor,
			Dst:       baseMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Forward(fixedTime, "1/1/5", frame)
		wantSteps := []trace.Step{
			{Layer: port.LayerRouting, Op: trace.OpClassify, Detail: "vrf default interface 1/1/5"},
			{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "10.0.20.0/24 connected vlan20"},
			{Layer: port.LayerRouting, Op: trace.OpRewrite, Detail: "hop limit 64 to 63, src 00:00:5e:00:01:01, dst 00:11:22:33:44:77"},
			{Layer: port.LayerRelay, Op: trace.OpLookup, Detail: "hit 1/1/2"},
			{Layer: port.LayerVlan, Op: trace.OpRewrite, Detail: "port 1/1/2 egress tag form"},
			{Layer: port.LayerRelay, Op: trace.OpTransmit, Detail: "port 1/1/2"},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Forwarded {
			t.Errorf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
		}
		if res.FID != 20 {
			t.Errorf("res.FID = %d, want 20", res.FID)
		}
		if res.Ingress != "1/1/5" {
			t.Errorf("res.Ingress = %q, want 1/1/5", res.Ingress)
		}
	})

	t.Run("frame on routed port to other MAC drops with not-bridged", func(t *testing.T) {
		otherMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}
		frame := ethernet.Frame{
			Src:       macPort5Neighbor,
			Dst:       otherMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("test"),
		}
		res := sw.Forward(fixedTime, "1/1/5", frame)
		wantSteps := []trace.Step{
			{Layer: port.LayerRouting, Op: trace.OpDrop, Detail: "not-bridged"},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonNotBridged {
			t.Errorf("got outcome=%v reason=%v, want Dropped/not-bridged", res.Outcome, res.Reason)
		}
		if res.FID != 0 {
			t.Errorf("res.FID = %d, want 0", res.FID)
		}
	})

	t.Run("down routed port drops with port-down under routing", func(t *testing.T) {
		sw.SetOperStatus("1/1/5", port.Down)
		frame := ethernet.Frame{
			Src:       macPort5Neighbor,
			Dst:       baseMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   makeIPv4Packet(t, ipPort5Dst, ipH2, 64, []byte("down")),
		}
		res := sw.Forward(fixedTime, "1/1/5", frame)
		wantSteps := []trace.Step{
			{Layer: port.LayerRouting, Op: trace.OpDrop, Detail: "port-down"},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Dropped || res.Reason != port.ReasonPortDown {
			t.Errorf("got outcome=%v reason=%v, want Dropped/port-down", res.Outcome, res.Reason)
		}
		sw.SetOperStatus("1/1/5", port.Up)
	})

	t.Run("router without bridge routes packet between two ports", func(t *testing.T) {
		rPorts := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

		rCfg := vswitch.Config{
			Ports: rPorts,
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
							"1/1/2": {Port: "1/1/2", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						},
						Neighbors: []routing.Neighbor{
							{Interface: "1/1/2", Addr: ipH2, MAC: macH2},
						},
					},
				},
			},
		}
		rSw := vswitch.New(rCfg)
		rBaseMAC := rSw.Config().MAC

		pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("router hello"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       rBaseMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := rSw.Forward(fixedTime, "1/1/1", frame)
		wantSteps := []trace.Step{
			{Layer: port.LayerRouting, Op: trace.OpClassify, Detail: "vrf default interface 1/1/1"},
			{Layer: port.LayerRouting, Op: trace.OpLookup, Detail: "10.0.20.0/24 connected 1/1/2"},
			{Layer: port.LayerRouting, Op: trace.OpRewrite, Detail: fmt.Sprintf("hop limit 64 to 63, src %s, dst 00:11:22:33:44:77", rBaseMAC)},
			{Layer: port.LayerRouting, Op: trace.OpTransmit, Detail: "port 1/1/2"},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Forwarded {
			t.Errorf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
		}
	})
}

func TestSourceMACLearnedOnVLANAndNotOnRoutedPort(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/5", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macPort5Src := netaddr.MAC{0x00, 0x55, 0x55, 0x55, 0x55, 0x55}

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						"1/1/5":  {Port: "1/1/5", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
					},
				},
			},
		},
	}
	sw := vswitch.New(cfg)

	// Ingress on VLAN 10 learns source MAC on VLAN 10.
	vlanPkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("vlan learn"))
	sw.Forward(fixedTime, "1/1/1", ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   vlanPkt,
	})
	entries := sw.Entries()
	var learnedVLAN10 bool
	for _, e := range entries {
		if e.FID == 10 && e.MAC == macH1 && e.Port == "1/1/1" {
			learnedVLAN10 = true
		}
	}
	if !learnedVLAN10 {
		t.Errorf("FDB entries %+v did not contain macH1 on FID 10", entries)
	}

	// Ingress on routed port 1/1/5 must NOT learn anything in FDB.
	portPkt := makeIPv4Packet(t, netip.MustParseAddr("10.0.50.5"), ipH2, 64, []byte("port no learn"))
	sw.Forward(fixedTime, "1/1/5", ethernet.Frame{
		Src:       macPort5Src,
		Dst:       sw.Config().MAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   portPkt,
	})
	for _, e := range sw.Entries() {
		if e.MAC == macPort5Src {
			t.Errorf("routed port source MAC %v unexpectedly learned in FDB: %+v", macPort5Src, e)
		}
	}
}

func TestRoutedFrameLeavesOnSameTrunkPort(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {Tagged: []vlan.ID{10, 20}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
					},
				},
			},
		},
	}
	sw := vswitch.New(cfg)

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("trunk reflection"))
	frame := ethernet.Frame{
		Tags:      []vlan.Tag{{VID: 10}},
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := sw.Forward(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Flooded && res.Outcome != trace.Forwarded {
		t.Fatalf("outcome = %v, want Forwarded or Flooded", res.Outcome)
	}
	if len(res.Egress) != 1 {
		t.Fatalf("len(res.Egress) = %d, want 1", len(res.Egress))
	}
	if res.Egress[0].Port != "1/1/1" {
		t.Errorf("egress port = %q, want 1/1/1", res.Egress[0].Port)
	}
	if len(res.Egress[0].Frame.Tags) != 1 || res.Egress[0].Frame.Tags[0].VID != 20 {
		t.Errorf("egress tags = %v, want tag with VID 20", res.Egress[0].Frame.Tags)
	}
	if res.Ingress != "1/1/1" {
		t.Errorf("res.Ingress = %q, want 1/1/1", res.Ingress)
	}
}

func TestRoutedFrameIngressLAGResolution(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/1a", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/1b", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"lag1":  {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
					},
				},
			},
		},
	}
	sw := vswitch.New(cfg)

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("lag hello"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := sw.Forward(fixedTime, "1/1/1a", frame)
	if res.Ingress != "lag1" {
		t.Errorf("res.Ingress = %q, want lag1", res.Ingress)
	}
}

func TestRoutedPortLAGLowestForwardingMember(t *testing.T) {
	p10 := vlan.ID(10)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag2", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/5b", Kind: port.Physical, LagParent: "lag2", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/5a", Kind: port.Physical, LagParent: "lag2", AdminStatus: port.Up, OperStatus: port.Up}))

	ipPort5Dst := netip.MustParseAddr("10.0.50.7")
	macPort5Neighbor := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"lag2":   {Port: "lag2", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "lag2", Addr: ipPort5Dst, MAC: macPort5Neighbor},
					},
				},
			},
		},
	}
	sw := vswitch.New(cfg)

	pkt := makeIPv4Packet(t, ipH1, ipPort5Dst, 64, []byte("lag routed egress"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := sw.Forward(fixedTime, "1/1/1", frame)
	if len(res.Egress) != 1 {
		t.Fatalf("len(res.Egress) = %d, want 1", len(res.Egress))
	}
	if res.Egress[0].Port != "lag2" {
		t.Errorf("egress port = %q, want lag2", res.Egress[0].Port)
	}
	if res.Egress[0].Member != "1/1/5a" {
		t.Errorf("egress member = %q, want lowest-named 1/1/5a", res.Egress[0].Member)
	}
}

func TestRoutedFrameMTUExceededVLANAndPort(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: 40}).
		Add(port.Port{Name: "1/1/5", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: 40}))

	ipPort5Dst := netip.MustParseAddr("10.0.50.7")
	macPort5Neighbor := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						"1/1/5":  {Port: "1/1/5", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
						{Interface: "1/1/5", Addr: ipPort5Dst, MAC: macPort5Neighbor},
					},
				},
			},
		},
	}
	sw := vswitch.New(cfg)

	// Pre-learn on 1/1/2
	sw.Forward(fixedTime, "1/1/2", ethernet.Frame{
		Src:       macH2,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   makeIPv4Packet(t, ipH2, ipH1, 64, []byte("x")),
	})

	// 50 bytes payload exceeds port MTU of 40.
	oversizedPayload := make([]byte, 50)

	t.Run("VLAN egress MTU exceeded", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipH2, 64, oversizedPayload)
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Forward(fixedTime, "1/1/1", frame)
		if res.Outcome != trace.Dropped || res.Reason != port.ReasonMTUExceeded {
			t.Errorf("outcome=%v reason=%v, want Dropped/mtu-exceeded", res.Outcome, res.Reason)
		}
		if len(res.Egress) != 1 || res.Egress[0].Dropped != port.ReasonMTUExceeded {
			t.Errorf("egress = %+v, want dropped for mtu-exceeded", res.Egress)
		}
	})

	t.Run("routed port egress MTU exceeded", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipPort5Dst, 64, oversizedPayload)
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Forward(fixedTime, "1/1/1", frame)
		if res.Outcome != trace.Dropped || res.Reason != port.ReasonMTUExceeded {
			t.Errorf("outcome=%v reason=%v, want Dropped/mtu-exceeded", res.Outcome, res.Reason)
		}
		if len(res.Egress) != 1 || res.Egress[0].Dropped != port.ReasonMTUExceeded {
			t.Errorf("egress = %+v, want dropped for mtu-exceeded", res.Egress)
		}
	})
}

func TestRoutedFrameEgressPortBlocked(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			},
		},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macRouter,
			Ports: map[string]stp.Port{
				"1/1/1": {AdminEdge: true},
				"1/1/2": {AdminEdge: false},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
					},
				},
			},
		},
	}
	sw := vswitch.New(cfg)
	sw.Start(fixedTime)
	sw.Drain()

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("blocked egress"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := sw.Forward(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Dropped || res.Reason != bridge.ReasonPortBlocked {
		t.Errorf("outcome=%v reason=%v, want Dropped/port-blocked", res.Outcome, res.Reason)
	}
	if len(res.Egress) != 1 || res.Egress[0].Dropped != bridge.ReasonPortBlocked {
		t.Errorf("egress = %+v, want dropped for port-blocked", res.Egress)
	}
}

func TestPeekLeavesFDBUnchangedWithRouting(t *testing.T) {
	sw := buildBaseRoutingSwitch(t)
	entriesBefore := sw.Entries()

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("peek hello"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := sw.Peek(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Flooded && res.Outcome != trace.Forwarded {
		t.Errorf("Peek outcome = %v, want Forwarded or Flooded", res.Outcome)
	}
	entriesAfter := sw.Entries()
	if len(entriesBefore) != len(entriesAfter) {
		t.Errorf("FDB entry count before=%d, after=%d", len(entriesBefore), len(entriesAfter))
	}
}

func TestARPToVLANInterfaceFloodedByRelay(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
					"1/1/3": {PVID: &p10, Untagged: []vlan.ID{10}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
				},
			},
		},
	}
	sw := vswitch.New(cfg)

	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeARP,
		Payload:   []byte("who has 10.0.10.1?"),
	}

	res := sw.Forward(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Flooded {
		t.Errorf("outcome = %v, want Flooded", res.Outcome)
	}
	if res.FID != 10 {
		t.Errorf("res.FID = %d, want 10", res.FID)
	}
	for _, step := range res.Steps {
		if step.Layer == port.LayerRouting {
			t.Errorf("found routing step in ARP flood trace: %+v", step)
		}
	}
}

// TestDiffReportsDeviceMAC is evidence for the device-level mac change.
func TestDiffReportsDeviceMAC(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}))
	cfgA := vswitch.Config{Ports: ports, MAC: netaddr.MAC{0x02, 0, 0, 0, 0, 1}}
	cfgB := vswitch.Config{Ports: ports, MAC: netaddr.MAC{0x02, 0, 0, 0, 0, 2}}

	changes := vswitch.Diff(cfgA, cfgB)
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want one", changes)
	}
	ch := changes[0]
	if ch.Layer != port.LayerPort || ch.Subject != (trace.Subject{Kind: "device"}) || ch.Field != "mac" ||
		ch.From != cfgA.MAC || ch.To != cfgB.MAC {
		t.Errorf("change = %+v, want layer port, subject device, field mac, %s to %s", ch, cfgA.MAC, cfgB.MAC)
	}
	if got := vswitch.Diff(cfgA, cfgA); len(got) != 0 {
		t.Errorf("Diff(a, a) = %+v, want none", got)
	}
}

func TestDiffRoutingNeighborChange(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}))

	macA := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	macB := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x88}

	cfgA := vswitch.Config{
		Ports: ports,
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan20": {VLAN: 20, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macA},
					},
				},
			},
		},
	}
	cfgB := cfgA.Clone()
	cfgB.Routing.VRFs["default"] = routing.VRF{
		Interfaces: cfgA.Routing.VRFs["default"].Interfaces,
		Neighbors: []routing.Neighbor{
			{Interface: "vlan20", Addr: ipH2, MAC: macB},
		},
	}

	changes := vswitch.Diff(cfgA, cfgB)
	var foundNeighborChange bool
	for _, c := range changes {
		if c.Layer == port.LayerRouting && c.Subject.Kind == "neighbor" && c.Field == "mac" {
			foundNeighborChange = true
			if c.From != macA || c.To != macB {
				t.Errorf("neighbor diff from=%v to=%v, want %v -> %v", c.From, c.To, macA, macB)
			}
		}
	}
	if !foundNeighborChange {
		t.Errorf("Diff changes %+v did not contain neighbor MAC change", changes)
	}
}

func TestDeriveSwitchRoutingUpdated(t *testing.T) {
	cur := buildBaseRoutingSwitch(t)
	nextCfg := cur.Config()

	// Add static route to 10.0.88.0/24 via vlan20 and a neighbor for it.
	ipNew := netip.MustParseAddr("10.0.88.7")
	macNew := netaddr.MAC{0x00, 0x88, 0x88, 0x88, 0x88, 0x88}
	defaultVRF := nextCfg.Routing.VRFs["default"]
	defaultVRF.Routes = append(defaultVRF.Routes, routing.Route{
		Prefix:    netip.MustParsePrefix("10.0.88.0/24"),
		Interface: "vlan20",
	})
	defaultVRF.Neighbors = append(defaultVRF.Neighbors, routing.Neighbor{
		Interface: "vlan20",
		Addr:      ipNew,
		MAC:       macNew,
	})
	nextCfg.Routing.VRFs["default"] = defaultVRF

	nextSw, err := vswitch.Derive(cur, nextCfg)
	if err != nil {
		t.Fatalf("Derive failed: %v", err)
	}

	pkt := makeIPv4Packet(t, ipH1, ipNew, 64, []byte("derived routing"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := nextSw.Forward(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Flooded && res.Outcome != trace.Forwarded {
		t.Fatalf("outcome = %v, want Forwarded or Flooded", res.Outcome)
	}
	if res.FID != 20 {
		t.Errorf("res.FID = %d, want 20", res.FID)
	}
}

func TestSwitchLearnForgetAndRelayCounters(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	b := port.NewBuilder()
	b.Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports := mustTable(t, b)

	t.Run("switch with bridge relay", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports:  ports,
			Bridge: &bridge.Config{},
		}
		sw := vswitch.New(cfg)

		mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		sw.Learn([]bridge.Seed{
			{FID: 0, MAC: mac, Port: "1/1/1", Static: true, LearnedAt: now},
		})
		entries := sw.Entries()
		if len(entries) != 1 || entries[0].MAC != mac {
			t.Fatalf("Entries() after Learn = %+v, want 1 entry with MAC %s", entries, mac)
		}

		if !sw.Forget(0, mac) {
			t.Errorf("Forget() = false, want true")
		}
		if entries := sw.Entries(); len(entries) != 0 {
			t.Errorf("Entries() after Forget = %+v, want empty", entries)
		}

		frame := ethernet.Frame{
			Dst: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x66},
			Src: mac,
		}
		sw.Forward(now, "1/1/1", frame)

		counters := sw.RelayCounters()
		if counters.Learned != 1 {
			t.Errorf("RelayCounters().Learned = %d, want 1", counters.Learned)
		}
	})

	t.Run("switch without bridge relay", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
		}
		sw := vswitch.New(cfg)

		mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		sw.Learn([]bridge.Seed{
			{FID: 0, MAC: mac, Port: "1/1/1", Static: true, LearnedAt: now},
		})
		if sw.Forget(0, mac) {
			t.Errorf("Forget() on hub = true, want false")
		}
		if counters := sw.RelayCounters(); counters != (bridge.Counters{}) {
			t.Errorf("RelayCounters() on hub = %+v, want zero", counters)
		}
	})
}

func TestForwardBPDUWithSpanningTree(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macSelf := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	macRoot := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{
			ForwardBPDU: true,
		},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macSelf,
			Ports: map[string]stp.Port{
				"1/1/1": {AdminEdge: true},
				"1/1/2": {AdminEdge: true},
				"1/1/3": {AdminEdge: true},
			},
		},
	}

	sw := vswitch.New(cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	sw.Start(now)
	sw.Drain()

	bpdu := stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: macRoot},
		RootPathCost: 0,
		BridgeID:     stp.BridgeID{Priority: 4096, Address: macRoot},
		PortID:       0x8001,
		HelloTime:    stp.DefaultHelloTime,
		MaxAge:       stp.DefaultMaxAge,
		ForwardDelay: stp.DefaultForwardDelay,
	}
	bpdu.SetRole(stp.RoleDesignated)
	bpdu.SetProposal(true)

	bpduFrame := stp.Encode(bpdu, macRoot)
	resBPDU := sw.Forward(now, "1/1/1", bpduFrame)
	if resBPDU.Outcome != trace.Consumed {
		t.Errorf("BPDU outcome = %s, want Consumed", resBPDU.Outcome)
	}

	otherReserved := netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x0e}
	otherFrame := ethernet.Frame{
		Dst: otherReserved,
		Src: macRoot,
	}
	resOther := sw.Forward(now, "1/1/1", otherFrame)
	if resOther.Outcome != trace.Flooded {
		t.Errorf("reserved frame outcome = %s, want Flooded", resOther.Outcome)
	}
	if len(resOther.Egress) != 2 || resOther.Egress[0].Port != "1/1/2" || resOther.Egress[1].Port != "1/1/3" {
		t.Errorf("reserved frame egress = %+v, want flooded to 1/1/2 and 1/1/3", resOther.Egress)
	}
}
