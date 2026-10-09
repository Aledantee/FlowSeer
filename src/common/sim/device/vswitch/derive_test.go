package vswitch_test

import (
	"net/netip"
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestDeriveKeepsMembershipAcrossSpanningTreeRebuild(t *testing.T) {
	cfg := mcastSwitchConfig(t, nil)
	cfg.STP = &stp.Config{Ports: map[string]stp.Port{"1/1/1": {AdminEdge: true}}}
	sw := mustSwitch(t, cfg)
	sw.Start(fixedTime)
	group := netip.MustParseAddr("239.1.1.1")
	report := makeIGMPControlFrame(t, ipH1, group, macH1, mcastGroupMAC, 1, igmp.Message{Type: igmp.ReportV2, Group: group})
	sw.Receive(fixedTime, "1/1/1", report)
	if got := sw.Groups(10, fixedTime); len(got) != 1 {
		t.Fatalf("source memberships = %+v, want one", got)
	}
	target := sw.Spec()
	target.Config.STP.Priority = 4096
	next, _, err := vswitch.Derive(sw, target)
	if err != nil {
		t.Fatal(err)
	}
	if got := next.Retention().Mcast; !got.Kept || got.Difference != "" {
		t.Fatalf("Mcast retention = %+v, want kept", got)
	}
	if got := next.Groups(10, fixedTime); len(got) != 1 || got[0].Port != "1/1/1" {
		t.Fatalf("derived memberships = %+v, want source membership", got)
	}
	data := ethernet.Frame{
		Src: macH2, Dst: mcastGroupMAC, EtherType: ethernet.EtherTypeIPv4,
		Payload: makeIPv4Packet(t, ipH2, group, 64, []byte("data")),
	}
	before := next.Peek(fixedTime, "1/1/2", data)
	if len(before.Egress) != 1 || before.Egress[0].Port != "1/1/1" || before.Egress[0].Dropped != bridge.ReasonPortBlocked {
		t.Fatalf("before tree forwarding = %+v, want blocked member egress", before)
	}
	next.Start(fixedTime)
	after := next.Peek(fixedTime, "1/1/2", data)
	if len(after.Egress) != 1 || after.Egress[0].Port != "1/1/1" || after.Egress[0].Dropped != "" {
		t.Fatalf("after tree forwarding = %+v, want member egress", after)
	}
}

func TestDeriveReportsRemovedVLANMembership(t *testing.T) {
	cfg := mcastSwitchConfig(t, nil)
	cfg.Bridge.VLAN.Table[20] = "voice"
	cfg.Mcast.VLANs[20] = cfg.Mcast.VLANs[10]
	sp := cfg.Bridge.VLAN.Switchports["1/1/1"]
	sp.Tagged = []vlan.ID{20}
	cfg.Bridge.VLAN.Switchports["1/1/1"] = sp
	sw := mustSwitch(t, cfg)
	group := netip.MustParseAddr("239.1.1.1")
	f := makeIGMPControlFrame(t, ipH1, group, macH1, mcastGroupMAC, 1, igmp.Message{Type: igmp.ReportV2, Group: group})
	f.Tags = []vlan.Tag{{VID: 20}}
	sw.Receive(fixedTime, "1/1/1", f)
	if got := sw.Groups(20, fixedTime); len(got) != 1 {
		t.Fatalf("source memberships = %+v, want one", got)
	}
	target := sw.Spec()
	delete(target.Config.Mcast.VLANs, 20)
	delete(target.Config.Bridge.VLAN.Table, 20)
	sp = target.Config.Bridge.VLAN.Switchports["1/1/1"]
	sp.Tagged = nil
	target.Config.Bridge.VLAN.Switchports["1/1/1"] = sp
	next, _, err := vswitch.Derive(sw, target)
	if err != nil {
		t.Fatal(err)
	}
	if got := next.Retention().Mcast; got.Kept || got.Difference != "config" {
		t.Fatalf("Mcast retention = %+v, want config loss", got)
	}
	if got := next.Groups(20, fixedTime); len(got) != 0 {
		t.Fatalf("derived memberships = %+v, want none", got)
	}
}

func TestForgetRemovesTheSeed(t *testing.T) {
	sw := mustSwitch(t, vswitch.Config{Ports: mustTable(t, port.NewBuilder().Range("p%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})), Bridge: &bridge.Config{}})
	seed := bridge.Seed{MAC: macH1, Port: "p1", Lifetime: bridge.Static}
	mustSwitchLearn(t, sw, []bridge.Seed{seed})
	if !sw.Forget(0, macH1) {
		t.Fatal("Forget refused present seed")
	}
	if got := sw.Spec().Seeds; len(got) != 0 {
		t.Fatalf("Spec seeds = %+v, want none", got)
	}
	next, _, err := vswitch.Derive(sw, sw.Spec())
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Entries()) != 0 {
		t.Fatalf("rebuilt entries = %+v, want none", next.Entries())
	}
	if err := next.Learn([]bridge.Seed{seed}); err != nil {
		t.Fatalf("relearn forgotten key: %v", err)
	}
	if got := next.Entries(); len(got) != 1 || got[0].MAC != macH1 {
		t.Fatalf("relearned entries = %+v, want seed", got)
	}
}

func TestDeriveCountsAnEvictedEntry(t *testing.T) {
	sw := mustSwitch(t, vswitch.Config{Ports: mustTable(t, port.NewBuilder().Range("p%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})), Bridge: &bridge.Config{MaxEntries: 2}})
	sw.Receive(fixedTime, "p1", ethernet.Frame{Src: macH1, Dst: macH2})
	sw.Receive(fixedTime.Add(time.Second), "p2", ethernet.Frame{Src: macH2, Dst: macH1})
	if len(sw.Entries()) != 2 {
		t.Fatalf("source entries = %+v, want two", sw.Entries())
	}
	target := sw.Spec()
	target.Config.Bridge.MaxEntries = 1
	next, _, err := vswitch.Derive(sw, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Entries()) != 1 {
		t.Fatalf("derived entries = %+v, want one", next.Entries())
	}
	f := reflect.ValueOf(next.Retention()).FieldByName("Bridge")
	if !f.IsValid() {
		t.Fatal("Retention omits Bridge")
	}
	if got := f.Interface().(vswitch.LayerRetention); got.Kept || got.Difference != "config" {
		t.Fatalf("Bridge retention = %+v, want config loss", got)
	}
}

func TestDeriveUsesTargetConstructionTrust(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	cfg := vswitch.Config{Ports: ports, Bridge: &bridge.Config{}}
	oldMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x10}
	newMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x20}
	oldMetadata := analysis.NewMetadata(
		analysis.NodeScope("old"),
		[]analysis.Issue{{Code: "test.old", Status: analysis.Incomplete, Scope: analysis.NodeScope("old")}},
		analysis.EvidenceCatalog{},
		nil,
	)
	newMetadata := analysis.NewMetadata(
		analysis.NodeScope("new"),
		[]analysis.Issue{{Code: "test.new", Status: analysis.Incomplete, Scope: analysis.NodeScope("new")}},
		analysis.EvidenceCatalog{},
		nil,
	)
	cur, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		Config:   cfg,
		Seeds:    []bridge.Seed{{MAC: oldMAC, Port: "out", Lifetime: bridge.Static}},
		NodeID:   "old",
		Metadata: oldMetadata,
	})
	if err != nil {
		t.Fatalf("NewWithSpec(current) error = %v", err)
	}
	target := vswitch.ConstructionSpec{
		Config:   cfg,
		Seeds:    []bridge.Seed{{MAC: newMAC, Port: "out", Origin: bridge.Configured, Lifetime: bridge.Static}},
		NodeID:   "new",
		Metadata: newMetadata,
	}

	next, _, err := vswitch.Derive(cur, target)
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}
	got := next.Spec()
	if !got.Equal(target) {
		t.Errorf("derived spec = %+v, want target spec %+v", got, target)
	}
	entries := next.Entries()
	if len(entries) != 1 || entries[0].MAC != newMAC {
		t.Errorf("derived entries = %+v, want only target static seed %s", entries, newMAC)
	}
}

func TestDeriveReplaysCurrentDynamicEntryOverTargetDynamicSeed(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "old", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "current", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	cfg := vswitch.Config{Ports: ports, Bridge: &bridge.Config{}}
	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	oldTime := fixedTime.Add(-time.Minute)

	cur, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		Config: cfg,
		Seeds:  []bridge.Seed{{MAC: mac, Port: "current", LearnedAt: fixedTime}},
	})
	if err != nil {
		t.Fatalf("NewWithSpec(current): %v", err)
	}

	for _, test := range []struct {
		name         string
		lifetime     bridge.Lifetime
		wantPort     string
		wantTime     time.Time
		wantLifetime bridge.Lifetime
	}{
		{name: "dynamic target yields to newer current state", wantPort: "current", wantTime: fixedTime, wantLifetime: bridge.Aging},
		{name: "static target remains authoritative", lifetime: bridge.Static, wantPort: "old", wantTime: oldTime, wantLifetime: bridge.Static},
	} {
		t.Run(test.name, func(t *testing.T) {
			next, _, err := vswitch.Derive(cur, vswitch.ConstructionSpec{
				Config: cfg,
				Seeds:  []bridge.Seed{{MAC: mac, Port: "old", Lifetime: test.lifetime, LearnedAt: oldTime}},
			})
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}

			entries := next.Entries()
			if len(entries) != 1 {
				t.Fatalf("entries = %+v, want one", entries)
			}
			if got := entries[0]; got.Port != test.wantPort || got.LearnedAt != test.wantTime || got.Lifetime != test.wantLifetime {
				t.Errorf("entry = %+v, want port=%q learned_at=%s lifetime=%s", got, test.wantPort, test.wantTime, test.wantLifetime)
			}
		})
	}
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
				"1/1/2": {PVID: &pvid, Tagged: []vlan.ID{1, 10, 20}},
			},
		}},
	}
	cur := mustSwitch(t, cfg)
	cur.Receive(now, "1/1/1", ethernet.Frame{
		Dst: netaddr.MAC{0, 0, 0, 0, 0, 0xbb}, Src: netaddr.MAC{0, 0, 0, 0, 0, 0xaa},
	})

	next, _, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: cur.Config()})
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
	cur := mustSwitch(t, cfg)
	cur.Receive(now, "1/1/1", ethernet.Frame{
		Dst: netaddr.MAC{0, 0, 0, 0, 0, 0xbb}, Src: netaddr.MAC{0, 0, 0, 0, 0, 0xaa},
	})

	next, _, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: cur.Config()})
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

func TestDeriveRetainsAdmittedDynamicEntries(t *testing.T) {
	_, expCfg, cur, _ := makeComparePair(t)

	macA := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x0a}
	macB := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x0b}

	cur.Receive(fixedTime, "1/1/2", ethernet.Frame{
		Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("frame A"),
	})
	cur.Receive(fixedTime.Add(time.Second), "1/1/1", ethernet.Frame{
		Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		Src:       macB,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("frame B"),
	})

	if entries := cur.Entries(); len(entries) != 2 {
		t.Fatalf("current switch has %d entries, want 2", len(entries))
	}

	derived, _, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: expCfg})
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

func TestDeriveEdgeCases(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	t.Run("lag without members drops entries", func(t *testing.T) {
		curPorts := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
		curCfg := vswitch.Config{
			Ports:  curPorts,
			Bridge: &bridge.Config{},
		}
		sw := mustSwitch(t, curCfg)
		mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		sw.Receive(fixedTime, "1/1/1", ethernet.Frame{
			Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			Src:       mac,
			EtherType: ethernet.EtherTypeIPv4,
		})

		// Target has 1/1/1 as a LAG with no members.
		targetPorts := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
		targetCfg := vswitch.Config{
			Ports:  targetPorts,
			Bridge: &bridge.Config{},
		}

		derived, _, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: targetCfg})
		if err != nil {
			t.Fatalf("unexpected derive error: %v", err)
		}
		if len(derived.Entries()) != 0 {
			t.Errorf("got entries %v for LAG without members, want none", derived.Entries())
		}
	})

	t.Run("derive with invalid target config", func(t *testing.T) {
		cfg := vswitch.Config{Ports: ports}
		sw := mustSwitch(t, cfg)

		invalidCfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{0: "invalid-vid"},
				},
			},
		}

		if _, _, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: invalidCfg}); err == nil {
			t.Errorf("got nil validation error from Derive, want non-nil")
		}
	})

	t.Run("non-vlan to non-vlan re-keys to FID 0", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports:  ports,
			Bridge: &bridge.Config{},
		}
		sw := mustSwitch(t, cfg)
		mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		sw.Receive(fixedTime, "1/1/1", ethernet.Frame{
			Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			Src:       mac,
			EtherType: ethernet.EtherTypeIPv4,
		})

		derived, _, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: cfg})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		entries := derived.Entries()
		if len(entries) != 1 || entries[0].FID != 0 {
			t.Errorf("got entries %v, want exactly 1 with FID 0", entries)
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
		sw := mustSwitch(t, curCfg)
		mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		sw.Receive(fixedTime, "1/1/1", ethernet.Frame{
			Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			Src:       mac,
			EtherType: ethernet.EtherTypeIPv4,
		})

		nonVlanCfg := vswitch.Config{
			Ports:  ports,
			Bridge: &bridge.Config{},
		}
		derived, _, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: nonVlanCfg})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(derived.Entries()) != 0 {
			t.Errorf("got entries %v after transition to non-VLAN, want none", derived.Entries())
		}
	})
}

func TestDerivedSwitchKeepsRootPortForwarding(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
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

	sw := mustSwitch(t, cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	b := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: macRoot},
		RootPathCost: 0,
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: macRoot},
		PortID:       0x8001,
		HelloTime:    stp.DefaultHelloTime,
		MaxAge:       stp.DefaultMaxAge,
		ForwardDelay: stp.DefaultForwardDelay,
	}
	b.SetRole(bpdu.RoleDesignated)
	b.SetProposal(true)
	captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", mustEncode(t, b, macRoot)))

	rolesBefore := sw.Roles()
	if rolesBefore["1/1/1"].Role != bpdu.RoleRoot || rolesBefore["1/1/1"].State != stp.StateForwarding {
		t.Fatalf("1/1/1 before derive = %s/%s, want Root/Forwarding", rolesBefore["1/1/1"].Role, rolesBefore["1/1/1"].State)
	}

	derived, deriveOutput1, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: cfg})
	captureOutput(callOutputs, derived, deriveOutput1)
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	rolesAfter := derived.Roles()
	if rolesAfter["1/1/1"].Role != bpdu.RoleRoot || rolesAfter["1/1/1"].State != stp.StateForwarding {
		t.Errorf("1/1/1 after derive = %s/%s, want Root/Forwarding", rolesAfter["1/1/1"].Role, rolesAfter["1/1/1"].State)
	}
}

// TestDerivedSwitchKeepsRolesWithAssignedBridgeAddress proves that
// Derive compares the spanning tree configuration New filled in, so a bridge
// address the switch assigned does not read as a change that rebuilds the
// layer.
func TestDerivedSwitchKeepsRolesWithAssignedBridgeAddress(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
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

	sw := mustSwitch(t, cfg)
	if sw.Config().STP.Address == (netaddr.MAC{}) {
		t.Fatal("New left the bridge address zero")
	}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	b := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: macRoot},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: macRoot},
		PortID:       0x8001,
		HelloTime:    stp.DefaultHelloTime,
		MaxAge:       stp.DefaultMaxAge,
		ForwardDelay: stp.DefaultForwardDelay,
	}
	b.SetRole(bpdu.RoleDesignated)
	b.SetProposal(true)
	captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", mustEncode(t, b, macRoot)))
	if before := sw.Roles()["1/1/1"]; before.Role != bpdu.RoleRoot || before.State != stp.StateForwarding {
		t.Fatalf("1/1/1 before derive = %s/%s, want Root/Forwarding", before.Role, before.State)
	}

	derived, deriveOutput1, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: cfg})
	captureOutput(callOutputs, derived, deriveOutput1)
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}
	if after := derived.Roles()["1/1/1"]; after.Role != bpdu.RoleRoot || after.State != stp.StateForwarding {
		t.Errorf("1/1/1 after derive = %s/%s, want Root/Forwarding", after.Role, after.State)
	}
	if derived.BridgeID().Address != sw.BridgeID().Address {
		t.Errorf("derived bridge address = %s, want %s", derived.BridgeID().Address, sw.BridgeID().Address)
	}
}

// TestDeriveRetainsLoopProtectOverUnchangedConfig proves that Derive keeps
// the loop-protection layer, and the action it applied, when the target
// configuration and every protected port's link state are unchanged: an
// ordinary frame on the blocked port is still denied after Derive.
func TestDeriveRetainsLoopProtectOverUnchangedConfig(t *testing.T) {
	sw := loopProtectTestSwitch(t)
	now := fixedTime

	mac := sw.Config().MAC
	probe := loopprotect.Probe{OriginMAC: mac, VID: 0, Sequence: 1, Port: "1/1/1"}
	sw.Receive(now, "1/1/1", mustEncodeProbe(t, probe, mac))

	derived, _, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: sw.Config()})
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	other := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x55}
	broadcast := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	data := ethernet.Frame{Dst: broadcast, Src: other, EtherType: 0x0800, Payload: []byte{1, 2, 3}}

	res := derived.Receive(now, "1/1/1", data)
	if res.Outcome != trace.Dropped || res.Reason != bridge.ReasonPortBlocked {
		t.Fatalf("derived switch, ordinary frame on the blocked port = %s/%s, want %s/%s",
			res.Outcome, res.Reason, trace.Dropped, bridge.ReasonPortBlocked)
	}
}

// TestDeriveRebuildsLoopProtectOnAdminStatusCycle proves that Derive rebuilds
// the loop-protection layer, clearing any applied action, when a protected
// port's administrative or operational state on the current switch differs
// from the target's port table: the layer's action and recovery timer are
// keyed to the link staying up throughout, so a state change invalidates the
// retained runtime state rather than just the configuration.
//
// It changes the current switch's live operational state with LinkChange.
// The action uses Timer recovery, which a link-down report leaves applied
// (only Manual recovery lifts it there), so the action is still held when
// Derive runs. The target configuration (sw.Config()) still
// reads the port Up, so the derived port is usable and the only thing that
// can still block it is a loop-protection layer carried over from the current
// switch.
func TestDeriveRebuildsLoopProtectOnAdminStatusCycle(t *testing.T) {
	sw := mustSwitch(t, vswitch.Config{
		Ports: mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
		Bridge: &bridge.Config{},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: loopprotect.Timer, Duration: time.Hour}},
			},
		},
	})
	now := fixedTime

	mac := sw.Config().MAC
	probe := loopprotect.Probe{OriginMAC: mac, VID: 0, Sequence: 1, Port: "1/1/1"}
	sw.Receive(now, "1/1/1", mustEncodeProbe(t, probe, mac))

	other := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x55}
	broadcast := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	data := ethernet.Frame{Dst: broadcast, Src: other, EtherType: 0x0800, Payload: []byte{1, 2, 3}}

	precondition := sw.Receive(now, "1/1/1", data)
	if precondition.Outcome != trace.Dropped || precondition.Reason != bridge.ReasonPortBlocked {
		t.Fatalf("precondition: current switch on 1/1/1 = %s/%s, want %s/%s",
			precondition.Outcome, precondition.Reason, trace.Dropped, bridge.ReasonPortBlocked)
	}

	if _, err := sw.LinkChange(now, "1/1/1", port.Down, vswitch.PointToPointTrue, 0); err != nil {
		t.Fatalf("LinkChange: %v", err)
	}

	derived, _, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: sw.Config()})
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	res := derived.Receive(now, "1/1/1", data)
	if res.Outcome != trace.Flooded {
		t.Fatalf("derived switch after the current switch's port state diverged from the target: 1/1/1 = %s/%s, want %s: the rebuilt layer's action should have cleared",
			res.Outcome, res.Reason, trace.Flooded)
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

	nextSw, _, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: nextCfg})
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

	res := nextSw.Receive(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("outcome = %v, want Flooded", res.Outcome)
	}
	if res.FID != 20 {
		t.Errorf("res.FID = %d, want 20", res.FID)
	}
}

// TestDeriveRebuildsRoutingWhenNeighborPolicyChanges verifies that a NeighborPolicy field
// change is not read by [routing.Diff] without its own
// arm, and a missing arm would make Derive silently reuse the old layer.
// This asserts the neighbor table, not the held-frame queue: derive.go
// unconditionally discards held frames on the layer it retains, so a
// held-queue assertion holds whether or not the layer was actually rebuilt.
// An observed neighbor is different: it exists only on a layer that was
// retained rather than rebuilt fresh from nextCfg. Changing ReachableTime
// alone must rebuild, so the address the old switch had observed a reply
// for does not resolve on the derived switch; contrast
// [TestDeriveKeepsObservedNeighborsAndDropsHeldFrames], which changes
// nothing and requires the opposite, a resolved forward.
func TestDeriveRebuildsRoutingWhenNeighborPolicyChanges(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	cur := buildBaseRoutingSwitch(t)

	dst := netip.MustParseAddr("10.0.20.77")
	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	pkt := makeIPv4Packet(t, ipH1, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}
	if held := captureOutput(callOutputs, cur, cur.Receive(fixedTime, "1/1/1", frame)); held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	captureOutput(callOutputs, cur, cur.Receive(fixedTime.Add(time.Millisecond), "1/1/2", reply))
	captureOutput(callOutputs, cur, cur.Advance(fixedTime.Add(time.Millisecond)))
	takeEmissions(callOutputs, cur)

	nextCfg := cur.Config()
	defaultVRF := nextCfg.Routing.VRFs["default"]
	defaultVRF.NeighborPolicy.ReachableTime = 60 * time.Second
	nextCfg.Routing.VRFs["default"] = defaultVRF

	next, deriveOutput1, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: nextCfg})
	captureOutput(callOutputs, next, deriveOutput1)
	if err != nil {
		t.Fatalf("Derive failed: %v", err)
	}

	res := captureOutput(callOutputs, next, next.Receive(fixedTime.Add(2*time.Millisecond), "1/1/1", frame))
	if res.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held: a NeighborPolicy change must rebuild the routing layer, losing the observed neighbor", res.Outcome)
	}
}

// TestDeriveKeepsObservedNeighborsAndDropsHeldFrames covers the Decisions
// section's retention rule: an observed neighbor survives Derive over an
// unchanged configuration, so a later frame to it resolves without holding,
// Derive retains the routing layer's neighbor table and hold queues when the
// routing retention key is unchanged, and drops them when it is not.
func TestDeriveRetainsRoutingStateAndHeldFramesUnderUnchangedKey(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	cur := buildBaseRoutingSwitch(t)

	dstA := netip.MustParseAddr("10.0.20.77")
	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	pktA := makeIPv4Packet(t, ipH1, dstA, 64, []byte("a"))
	frameA := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pktA}
	captureOutput(callOutputs, cur, cur.Receive(fixedTime, "1/1/1", frameA))
	replyA := makeARPReply(t, dstA, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	captureOutput(callOutputs, cur, cur.Receive(fixedTime.Add(time.Millisecond), "1/1/2", replyA))
	captureOutput(callOutputs, cur, cur.Advance(fixedTime.Add(time.Millisecond)))
	takeEmissions(callOutputs, cur)

	dstB := netip.MustParseAddr("10.0.20.88")
	pktB := makeIPv4Packet(t, ipH1, dstB, 64, []byte("b"))
	frameB := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pktB}
	if held := captureOutput(callOutputs, cur, cur.Receive(fixedTime.Add(2*time.Millisecond), "1/1/1", frameB)); held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	next, deriveOutput1, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: cur.Config()})
	captureOutput(callOutputs, next, deriveOutput1)
	if err != nil {
		t.Fatalf("Derive failed: %v", err)
	}
	if !next.Retention().Routing.Kept {
		t.Errorf("Routing retention = %+v, want Kept: true", next.Retention().Routing)
	}

	resA := captureOutput(callOutputs, next, next.Receive(fixedTime.Add(3*time.Millisecond), "1/1/1", frameA))
	if resA.Outcome != trace.Forwarded {
		t.Fatalf("outcome for the retained neighbor = %v, want Forwarded", resA.Outcome)
	}

	// Held frame survived Derive: resolve it via ARP reply and Advance
	learnedMACB := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x88}
	replyB := makeARPReply(t, dstB, netip.MustParseAddr("10.0.20.1"), learnedMACB, macRouter)
	captureOutput(callOutputs, next, next.Receive(fixedTime.Add(4*time.Millisecond), "1/1/2", replyB))
	captureOutput(callOutputs, next, next.Advance(fixedTime.Add(4*time.Millisecond)))
	if emissions := takeEmissions(callOutputs, next); len(emissions) == 0 {
		t.Errorf("emissions = 0, want released held frame")
	}
	if failures := takeDrops(callOutputs, next); len(failures) != 0 {
		t.Errorf("neighbor failures = %d, want 0", len(failures))
	}
}

func TestDeriveFailsHeldFramesOnRoutingKeyChange(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	cur := buildBaseRoutingSwitch(t)

	dstB := netip.MustParseAddr("10.0.20.88")
	pktB := makeIPv4Packet(t, ipH1, dstB, 64, []byte("b"))
	frameB := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pktB}
	if held := captureOutput(callOutputs, cur, cur.Receive(fixedTime.Add(2*time.Millisecond), "1/1/1", frameB)); held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	// Perturb NeighborPolicy.RetransTimer to change routing retention key
	nextCfg := cur.Config()
	vrf := nextCfg.Routing.VRFs[routing.DefaultVRF]
	vrf.NeighborPolicy.RetransTimer = 10 * time.Second
	nextCfg.Routing.VRFs[routing.DefaultVRF] = vrf

	next, deriveOutput1, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: nextCfg})
	captureOutput(callOutputs, next, deriveOutput1)
	if err != nil {
		t.Fatalf("Derive failed: %v", err)
	}
	if next.Retention().Routing.Kept {
		t.Errorf("Routing retention = %+v, want Kept: false", next.Retention().Routing)
	}
	// Held frame is reported as failed rather than silently lost
	if failures := takeDrops(callOutputs, next); len(failures) != 1 {
		t.Errorf("neighbor failures = %d, want 1", len(failures))
	}
}

func TestDeriveInvalidatesLAGSelectionWhenMemberStateChanges(t *testing.T) {
	frame := ethernet.Frame{Src: netaddr.MAC{2}, Dst: netaddr.MAC{4}}
	for _, test := range []struct {
		name  string
		admin port.LinkState
		oper  port.LinkState
	}{
		{name: "oper up to down", admin: port.Up, oper: port.Down},
		{name: "oper up to unknown", admin: port.Up, oper: port.Unknown},
		{name: "admin up to down", admin: port.Down, oper: port.Up},
		{name: "admin up to unknown", admin: port.Unknown, oper: port.Up},
	} {
		t.Run(test.name, func(t *testing.T) {
			currentConfig := lagDeriveConfig(t, port.Up, port.Up)
			current := mustSwitch(t, currentConfig)
			if member, ok := current.SelectMember(fixedTime, "lag1", frame, 0); !ok || member != "member" {
				t.Fatalf("current selection = (%q, %t), want member", member, ok)
			}

			targetConfig := lagDeriveConfig(t, test.admin, test.oper)
			derived, _, err := vswitch.Derive(current, vswitch.ConstructionSpec{Config: targetConfig})
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			fresh := mustSwitch(t, targetConfig)

			derivedMember, derivedOK := derived.SelectMember(fixedTime, "lag1", frame, 0)
			freshMember, freshOK := fresh.SelectMember(fixedTime, "lag1", frame, 0)
			if derivedMember != freshMember || derivedOK != freshOK {
				t.Errorf("derived selection = (%q, %t), fresh = (%q, %t)", derivedMember, derivedOK, freshMember, freshOK)
			}

			derivedResult := derived.Receive(fixedTime, "in", frame)
			freshResult := fresh.Receive(fixedTime, "in", frame)
			if !reflect.DeepEqual(derivedResult.Result, freshResult.Result) {
				t.Errorf("derived forwarding = %+v, fresh = %+v", derivedResult.Result, freshResult.Result)
			}
			if derivedResult.Metadata.Status() != freshResult.Metadata.Status() {
				t.Errorf("derived status = %s, fresh = %s", derivedResult.Metadata.Status(), freshResult.Metadata.Status())
			}
		})
	}
}
