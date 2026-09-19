package vswitch_test

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/mld"
	"go.aledante.io/FlowSeer/src/common/net/ndp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
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

func mustEncode(t *testing.T, b stp.BPDU, src netaddr.MAC) ethernet.Frame {
	t.Helper()

	frame, err := stp.Encode(b, src)
	if err != nil {
		t.Fatalf("stp.Encode: %v", err)
	}
	return frame
}

func TestSetOperStatusRejectsInvalidStateWithoutMutation(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	for _, portName := range []string{"1/1/2", "missing"} {
		t.Run(portName, func(t *testing.T) {
			sw := mustSwitch(t, vswitch.Config{Ports: ports, Bridge: &bridge.Config{}})
			before := sw.Ports().Ports()

			err := sw.SetOperStatus(portName, port.LinkState("invalid"))
			if err == nil {
				t.Fatal("SetOperStatus with invalid state succeeded, want error")
			}
			attrs := errs.Attributes(err)
			if got := attrs["field"]; got != "ports."+portName+".oper_status" {
				t.Errorf("field attribute = %v, want ports.%s.oper_status", got, portName)
			}
			if got := attrs["oper_status"]; got != port.LinkState("invalid") {
				t.Errorf("oper_status attribute = %v, want invalid", got)
			}
			if after := sw.Ports().Ports(); !slices.Equal(after, before) {
				t.Errorf("ports after invalid update = %+v, want unchanged %+v", after, before)
			}
		})
	}
}

func TestNewRejectsZeroStaticNeighborMACAtRoutingField(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "routed", AdminStatus: port.Up, OperStatus: port.Up}))
	cfg := vswitch.Config{
		Ports: ports,
		Routing: &routing.Config{VRFs: map[string]routing.VRF{
			routing.DefaultVRF: {
				Interfaces: map[string]routing.Interface{
					"routed": {Port: "routed", Prefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")}},
				},
				Neighbors: []routing.Neighbor{{Interface: "routed", Addr: netip.MustParseAddr("192.0.2.2")}},
			},
		}},
	}

	_, err := vswitch.New(cfg)
	if err == nil {
		t.Fatal("vswitch.New accepted a zero static neighbor MAC")
	}
	if got, want := errs.Attributes(err)["field"], "vrfs.default.neighbors.routed/192.0.2.2.mac"; got != want {
		t.Errorf("field = %v, want %q", got, want)
	}
}

func TestLagAggregateOperStatusUsesEffectiveMemberState(t *testing.T) {
	tests := []struct {
		name        string
		members     []port.Port
		wantAtStart port.LinkState
	}{
		{
			name: "definite up wins",
			members: []port.Port{
				{Name: "member-a", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"},
				{Name: "member-b", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown, LagParent: "lag1"},
			},
			wantAtStart: port.Up,
		},
		{
			name: "possible member is unknown",
			members: []port.Port{
				{Name: "member-a", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown, LagParent: "lag1"},
				{Name: "member-b", Kind: port.Physical, AdminStatus: port.Unknown, OperStatus: port.Up, LagParent: "lag1"},
			},
			wantAtStart: port.Unknown,
		},
		{
			name: "administratively or operationally down is definite",
			members: []port.Port{
				{Name: "member-a", Kind: port.Physical, AdminStatus: port.Down, OperStatus: port.Unknown, LagParent: "lag1"},
				{Name: "member-b", Kind: port.Physical, AdminStatus: port.Unknown, OperStatus: port.Down, LagParent: "lag1"},
			},
			wantAtStart: port.Down,
		},
		{
			name:        "no members is definite down",
			wantAtStart: port.Down,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder := port.NewBuilder().Add(port.Port{
				Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up,
			})
			for _, member := range test.members {
				builder.Add(member)
			}
			sw := mustSwitch(t, vswitch.Config{Ports: mustTable(t, builder), Bridge: &bridge.Config{}})

			sw.Start(fixedTime)
			lagPort, _ := sw.Ports().Port("lag1")
			if got := lagPort.OperStatus; got != test.wantAtStart {
				t.Fatalf("oper status after Start = %s, want %s", got, test.wantAtStart)
			}
		})
	}

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "member", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown, LagParent: "lag1"}))
	sw := mustSwitch(t, vswitch.Config{Ports: ports, Bridge: &bridge.Config{}})
	sw.Start(fixedTime)
	sw.LinkChange(fixedTime.Add(time.Second), "member", port.Up, vswitch.PointToPointTrue, 1_000_000_000)
	lagPort, _ := sw.Ports().Port("lag1")
	if got := lagPort.OperStatus; got != port.Up {
		t.Fatalf("oper status after link up = %s, want %s", got, port.Up)
	}
	sw.LinkChange(fixedTime.Add(2*time.Second), "member", port.Down, vswitch.PointToPointTrue, 1_000_000_000)
	lagPort, _ = sw.Ports().Port("lag1")
	if got := lagPort.OperStatus; got != port.Down {
		t.Fatalf("oper status after link down = %s, want %s", got, port.Down)
	}
}

func TestConfigEqualAndDiffNormalizeCompleteConfiguration(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.Lag}).
		Add(port.Port{Name: "member", Kind: port.Physical, LagParent: "lag1"}))
	raw := vswitch.Config{Ports: ports}
	explicit := raw.Normalize()

	if !raw.Equal(explicit) {
		t.Fatal("raw configuration and its normalized form compare unequal")
	}
	if got := vswitch.Diff(raw, explicit); len(got) != 0 {
		t.Errorf("Diff(raw, raw.Normalize()) = %+v, want none", got)
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
		Seeds:    []bridge.Seed{{MAC: newMAC, Port: "out", Lifetime: bridge.Static}},
		NodeID:   "new",
		Metadata: newMetadata,
	}

	next, err := vswitch.Derive(cur, target)
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
		{name: "dynamic target yields to newer current state", wantPort: "current", wantTime: fixedTime},
		{name: "static target remains authoritative", lifetime: bridge.Static, wantPort: "old", wantTime: oldTime, wantLifetime: bridge.Static},
	} {
		t.Run(test.name, func(t *testing.T) {
			next, err := vswitch.Derive(cur, vswitch.ConstructionSpec{
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

func TestHubNoCandidateHasSemanticTerminalDecision(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "only", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	sw := mustSwitch(t, vswitch.Config{Ports: ports})

	res := sw.Forward(fixedTime, "only", ethernet.Frame{Src: macH1, Dst: macH2})
	if res.Outcome != trace.Dropped || res.Reason != bridge.ReasonNoEgress {
		t.Fatalf("hub result = %s/%s, want %s/%s", res.Outcome, res.Reason, trace.Dropped, bridge.ReasonNoEgress)
	}
	if len(res.Steps) != 1 || res.Steps[0].RuleID != "port.hub.no_egress" {
		t.Fatalf("hub steps = %+v, want one no-egress decision", res.Steps)
	}
	if outputs := res.Steps[0].Outputs; len(outputs) != 1 || outputs[0].TypeID() != "port.hub_egress" {
		t.Errorf("hub decision outputs = %+v, want port.hub_egress fact", outputs)
	}
}

func mustSwitch(t *testing.T, cfg vswitch.Config) *vswitch.Switch {
	t.Helper()
	sw, err := vswitch.New(cfg)
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}
	return sw
}

func mustSwitchLearn(t *testing.T, sw *vswitch.Switch, seeds []bridge.Seed) {
	t.Helper()
	if err := sw.Learn(seeds); err != nil {
		t.Fatalf("Learn: %v", err)
	}
}

// TestEntriesCarryOriginAndLifetime is R4's acceptance example at the switch level: a seeded
// entry and a live-learned one for different MACs both appear in Entries() reporting their own
// Origin and Lifetime.
func TestEntriesCarryOriginAndLifetime(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	sw := mustSwitch(t, vswitch.Config{Ports: ports, Bridge: &bridge.Config{}})

	mustSwitchLearn(t, sw, []bridge.Seed{{MAC: macH1, Port: "1/1/1", Lifetime: bridge.Static, LearnedAt: fixedTime}})
	sw.Forward(fixedTime, "1/1/2", ethernet.Frame{Src: macH2, Dst: macH1})

	entries := sw.Entries()
	if len(entries) != 2 {
		t.Fatalf("len(Entries()) = %d, want 2, got %+v", len(entries), entries)
	}
	byMAC := make(map[netaddr.MAC]bridge.Entry, len(entries))
	for _, e := range entries {
		byMAC[e.MAC] = e
	}
	if seeded, ok := byMAC[macH1]; !ok || seeded.Origin != bridge.Configured || seeded.Lifetime != bridge.Static {
		t.Errorf("seeded entry = %+v, ok=%v, want Origin: Configured, Lifetime: Static", seeded, ok)
	}
	if learned, ok := byMAC[macH2]; !ok || learned.Origin != bridge.Observed || learned.Lifetime != bridge.Aging {
		t.Errorf("learned entry = %+v, ok=%v, want Origin: Observed, Lifetime: Aging", learned, ok)
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
				"1/1/2": {PVID: &pvid, Tagged: []vlan.ID{10, 20}},
			},
		}},
	}
	cur := mustSwitch(t, cfg)
	cur.Forward(now, "1/1/1", ethernet.Frame{
		Dst: netaddr.MAC{0, 0, 0, 0, 0, 0xbb}, Src: netaddr.MAC{0, 0, 0, 0, 0, 0xaa},
	})

	next, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: cur.Config()})
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
	cur.Forward(now, "1/1/1", ethernet.Frame{
		Dst: netaddr.MAC{0, 0, 0, 0, 0, 0xbb}, Src: netaddr.MAC{0, 0, 0, 0, 0, 0xaa},
	})

	next, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: cur.Config()})
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
		sw := mustSwitch(t, cfg)
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
			Ports:   tbl,
			Traffic: &traffic.Config{},
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
			port.LayerTraffic,
			port.LayerVlan,
		}
		if caps := cfg.Capabilities(); !slices.Equal(caps, want) {
			t.Errorf("got capabilities %v, want %v", caps, want)
		}
	})
}

func TestHubForwardingUntouched(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{Ports: tbl}
	sw := mustSwitch(t, cfg)

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

func TestSwitchReservesMirrorOutputAndCollectsCopies(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	vid10 := vlan.ID(10)
	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
				"1/1/3": {PVID: &vid10, Untagged: []vlan.ID{10}},
				"1/1/4": {PVID: &vid10, Untagged: []vlan.ID{10}},
			},
		}},
		Traffic: &traffic.Config{Mirrors: []traffic.Mirror{{
			Name:           "m1",
			SelectSrcPorts: []string{"1/1/1"},
			OutputPort:     "1/1/4",
		}}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	sw := mustSwitch(t, cfg)
	frame := ethernet.Frame{
		Dst:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("mirrored flood"),
	}

	res := sw.Forward(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Flooded {
		t.Errorf("Outcome = %v, want %v", res.Outcome, trace.Flooded)
	}
	if len(res.Egress) != 3 {
		t.Fatalf("len(Egress) = %d, want 3", len(res.Egress))
	}
	for i, name := range []string{"1/1/2", "1/1/3"} {
		if got := res.Egress[i]; got.Port != name || got.Dropped != "" {
			t.Errorf("Egress[%d] = %+v, want normal egress on %s", i, got, name)
		}
	}
	if got := res.Egress[2]; got.Port != "1/1/4" || got.Dropped != traffic.ReasonMirrorOutput {
		t.Errorf("Egress[2] = %+v, want mirror-output drop on 1/1/4", got)
	}

	copies := sw.Copies()
	if len(copies) != 1 || copies[0].Mirror != "m1" || copies[0].Port != "1/1/4" {
		t.Fatalf("Copies() = %+v, want m1 copy on 1/1/4", copies)
	}
	if !slices.Equal(copies[0].Frame.Payload, frame.Payload) {
		t.Errorf("copy payload = %q, want %q", copies[0].Frame.Payload, frame.Payload)
	}
	if copies := sw.Copies(); len(copies) != 0 {
		t.Errorf("second Copies() = %+v, want empty", copies)
	}
	mustSwitchLearn(t, sw, []bridge.Seed{{FID: 10, MAC: frame.Dst, Port: "1/1/4", Lifetime: bridge.Static}})
	reservedOnly := sw.Forward(fixedTime, "1/1/1", frame)
	if reservedOnly.Outcome != trace.Dropped || reservedOnly.Reason != traffic.ReasonMirrorOutput {
		t.Errorf("reserved-only trace = %+v, want mirror-output drop", reservedOnly.Trace)
	}
	if len(reservedOnly.Egress) != 1 || reservedOnly.Egress[0].Dropped != traffic.ReasonMirrorOutput {
		t.Errorf("reserved-only egress = %+v, want one mirror-output drop", reservedOnly.Egress)
	}

	dropped := sw.Forward(fixedTime, "1/1/4", frame)
	if dropped.Outcome != trace.Dropped || dropped.Reason != traffic.ReasonMirrorOutput {
		t.Errorf("mirror output ingress trace = %+v, want mirror-output drop", dropped.Trace)
	}
	wantStep := trace.Step{Layer: port.LayerTraffic, Op: trace.OpDrop, RuleID: "traffic.mirror.output_drop", Subject: trace.Subject{Kind: "port", Key: "1/1/4"}}
	if len(dropped.Steps) != 1 {
		t.Errorf("mirror output ingress steps = %+v, want %+v", dropped.Steps, wantStep)
	} else {
		assertSteps(t, dropped.Steps, []trace.Step{wantStep})
	}
	if !traceHasFactType(dropped.Steps, "traffic.mirror_decision") {
		t.Errorf("mirror output trace has no mirror decision fact: %+v", dropped.Steps)
	}

	// Peek leaves copy storage untouched.
	peekSwitch := mustSwitch(t, cfg)
	peekSwitch.Peek(fixedTime, "1/1/1", frame)
	if copies := peekSwitch.Copies(); len(copies) != 0 {
		t.Errorf("Copies() after Peek = %+v, want empty", copies)
	}
}

func TestSwitchEgressKeepsClassifiedPCP(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	vid10 := vlan.ID(10)
	sw := mustSwitch(t, vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {Tagged: []vlan.ID{10}},
				"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
			},
		}},
	})
	frame := ethernet.Frame{
		Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		EtherType: ethernet.EtherTypeIPv4,
		Tags: []vlan.Tag{{
			TPID: uint16(ethernet.EtherTypeDot1Q),
			VID:  10,
			PCP:  5,
		}},
	}

	res := sw.Forward(fixedTime, "1/1/1", frame)
	if len(res.Egress) != 1 {
		t.Fatalf("len(Egress) = %d, want 1", len(res.Egress))
	}
	if got := res.Egress[0].PCP; got != 5 {
		t.Errorf("Egress PCP = %d, want 5", got)
	}
	if len(res.Egress[0].Frame.Tags) != 0 {
		t.Errorf("egress frame tags = %+v, want untagged", res.Egress[0].Frame.Tags)
	}
}

func TestSwitchPolicerAndQueueLookup(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))
	cfg := vswitch.Config{
		Ports: ports,
		Traffic: &traffic.Config{
			Policers: map[string]traffic.Policer{
				"1/1/1": {RateBPS: 1_000_000, BurstOctets: 10_000},
			},
			Queues: map[string]traffic.PortQueues{
				"1/1/2": {MaxRateBPS: map[vlan.PCP]uint64{5: 100_000_000}},
			},
		},
	}
	sw := mustSwitch(t, cfg)
	for i := range 9 {
		if !sw.Police(fixedTime, "1/1/1", 1038) {
			t.Fatalf("Police call %d refused, want admitted", i+1)
		}
	}
	if sw.Police(fixedTime, "1/1/1", 1038) {
		t.Error("tenth Police call admitted, want refused")
	}
	if !sw.Police(fixedTime.Add(10*time.Millisecond), "1/1/1", 1038) {
		t.Error("Police after 10 ms refused, want admitted")
	}
	if !sw.Police(fixedTime, "1/1/2", 1_000_000) {
		t.Error("unconfigured port was policed")
	}
	if rate, ok := sw.QueueMaxRate("1/1/2", 5); !ok || rate != 100_000_000 {
		t.Errorf("QueueMaxRate(1/1/2, 5) = (%d, %t), want (100000000, true)", rate, ok)
	}
	if rate, ok := sw.QueueMaxRate("1/1/2", 0); ok || rate != 0 {
		t.Errorf("QueueMaxRate(1/1/2, 0) = (%d, %t), want (0, false)", rate, ok)
	}

	tests := []struct {
		name            string
		change          func(*vswitch.Config)
		wantMainAdmit   bool
		wantSecondAdmit bool
	}{
		{
			name: "mirror changes preserve the bucket",
			change: func(next *vswitch.Config) {
				next.Traffic.Mirrors = []traffic.Mirror{{Name: "m1", SelectAll: true, OutputPort: "1/1/2"}}
			},
		},
		{
			name: "queue changes preserve the bucket",
			change: func(next *vswitch.Config) {
				next.Traffic.Queues["1/1/2"] = traffic.PortQueues{MaxRateBPS: map[vlan.PCP]uint64{5: 200_000_000}}
			},
		},
		{
			name: "another port policer preserves the bucket",
			change: func(next *vswitch.Config) {
				next.Traffic.Policers["1/1/2"] = traffic.Policer{RateBPS: 1_000_000, BurstOctets: 2_000}
			},
			wantSecondAdmit: true,
		},
		{
			name: "changed policer starts full",
			change: func(next *vswitch.Config) {
				next.Traffic.Policers["1/1/1"] = traffic.Policer{RateBPS: 2_000_000, BurstOctets: 10_000}
			},
			wantMainAdmit: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cur := mustSwitch(t, cfg)
			for i := range 9 {
				if !cur.Police(fixedTime, "1/1/1", 1038) {
					t.Fatalf("derive setup Police call %d refused, want admitted", i+1)
				}
			}

			next := cfg.Clone()
			tt.change(&next)
			derived, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: next})
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			if got := derived.Police(fixedTime, "1/1/1", 1038); got != tt.wantMainAdmit {
				t.Errorf("main policer admit = %t, want %t", got, tt.wantMainAdmit)
			}
			if tt.wantSecondAdmit && !derived.Police(fixedTime, "1/1/2", 1_000) {
				t.Error("new policer did not start full")
			}
		})
	}
}

func TestPeekLeavesPolicerBucketsUntouched(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))
	sw := mustSwitch(t, vswitch.Config{
		Ports: ports,
		Traffic: &traffic.Config{Policers: map[string]traffic.Policer{
			"1/1/1": {RateBPS: 8, BurstOctets: 2},
		}},
	})
	frame := ethernet.Frame{Dst: netaddr.MAC{2}, Src: netaddr.MAC{4}}

	if !sw.Police(fixedTime, "1/1/1", 1) {
		t.Fatal("initial token was refused")
	}
	sw.Peek(fixedTime, "1/1/1", frame)
	if !sw.Police(fixedTime, "1/1/1", 1) {
		t.Error("Peek spent the remaining policer token")
	}
}

func TestMirrorSelectionUsesLogicalLAGIngress(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	vid10 := vlan.ID(10)
	vid99 := vlan.ID(99)
	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "users", 99: "mirror"},
			Switchports: map[string]bridge.Switchport{
				"lag1":  {PVID: &vid10, Untagged: []vlan.ID{10, 99}},
				"1/1/3": {PVID: &vid10, Untagged: []vlan.ID{10}},
				"1/1/4": {Untagged: []vlan.ID{99}},
			},
		}},
		LAG: &lag.Config{LAGs: map[string]lag.LAG{
			"lag1": {Mode: lag.BalanceSLB, Members: map[string]lag.Member{"1/1/1": {}, "1/1/2": {}}},
		}},
		Traffic: &traffic.Config{Mirrors: []traffic.Mirror{
			{Name: "span", SelectSrcPorts: []string{"lag1"}, OutputPort: "1/1/4"},
			{Name: "rspan", SelectSrcPorts: []string{"lag1"}, OutputVLAN: &vid99},
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	sw := mustSwitch(t, cfg)
	frame := ethernet.Frame{Dst: netaddr.MAC{2}, Src: netaddr.MAC{4}}

	sw.Forward(fixedTime, "1/1/1", frame)
	copies := sw.Copies()
	if len(copies) != 2 {
		t.Fatalf("Copies() = %+v, want SPAN and RSPAN copies", copies)
	}
	for _, copy := range copies {
		if copy.Port == "lag1" {
			t.Errorf("RSPAN copied back onto logical ingress: %+v", copy)
		}
		if copy.Port != "1/1/4" {
			t.Errorf("copy port = %q, want 1/1/4", copy.Port)
		}
	}
}

func TestHubWithDownPort(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: 50}))

	sw := mustSwitch(t, vswitch.Config{Ports: tbl})

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
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

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

	return curCfg, expCfg, mustSwitch(t, curCfg), mustSwitch(t, expCfg)
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
	if cmp.Disposition != analysis.Different {
		t.Errorf("got Disposition: %v, want %v", cmp.Disposition, analysis.Different)
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

	derived, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: expCfg})
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
			trace.CompareFact(pvidChange.From, bridge.PVIDFact(10)) != 0 || trace.CompareFact(pvidChange.To, bridge.PVIDFact(20)) != 0 {
			t.Errorf("unexpected pvid change: %+v", pvidChange)
		}

		untaggedChange := changes[1]
		if untaggedChange.Subject.Key != "1/1/2" || untaggedChange.Field != "untagged_vlan_ids" ||
			trace.CompareFact(untaggedChange.From, bridge.VLANsFact([]vlan.ID{10})) != 0 ||
			trace.CompareFact(untaggedChange.To, bridge.VLANsFact([]vlan.ID{20})) != 0 {
			t.Errorf("unexpected untagged_vlan_ids change: %+v", untaggedChange)
		}
	})

	t.Run("added port", func(t *testing.T) {
		morePorts := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

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
			trace.CompareFact(ch.From, phy.PowerFact(60_000)) != 0 || trace.CompareFact(ch.To, phy.PowerFact(90_000)) != 0 {
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
				if ch.From != nil || trace.CompareFact(ch.To, vswitch.LayerFact(port.LayerVlan)) != 0 {
					t.Errorf("unexpected capability change payload: %+v", ch)
				}
			}
		}
		if !foundCap {
			t.Errorf("expected capability change for vlan, got changes: %+v", changes)
		}
	})

	t.Run("traffic mirror changes", func(t *testing.T) {
		cfgA := vswitch.Config{Traffic: &traffic.Config{Mirrors: []traffic.Mirror{{
			Name:       "m1",
			SelectAll:  true,
			OutputPort: "1/1/4",
			SnapLen:    64,
		}}}}
		cfgB := cfgA.Clone()
		cfgB.Traffic.Mirrors[0].SnapLen = 128

		changes := vswitch.Diff(cfgA, cfgB)
		if len(changes) != 1 {
			t.Fatalf("got %d changes, want 1: %+v", len(changes), changes)
		}
		ch := changes[0]
		if ch.Layer != port.LayerTraffic || ch.Subject.Kind != "mirror" || ch.Subject.Key != "m1" ||
			ch.Field != "snap_len" || trace.CompareFact(ch.From, traffic.SnapLenFact(64)) != 0 || trace.CompareFact(ch.To, traffic.SnapLenFact(128)) != 0 {
			t.Errorf("unexpected mirror change: %+v", ch)
		}
	})
}

func TestCompareSameOverFrameTable(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

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

	swA := mustSwitch(t, cfg)
	swB := mustSwitch(t, cfg)

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
			if cmp.Disposition != analysis.Equivalent {
				t.Errorf("got Disposition: %v, want %v for %s", cmp.Disposition, analysis.Equivalent, tc.name)
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
		sw.Forward(fixedTime, "1/1/1", ethernet.Frame{
			Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			Src:       mac,
			EtherType: ethernet.EtherTypeIPv4,
		})

		// Target has 1/1/1 as a LAG with no members.
		targetPorts := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
		targetCfg := vswitch.Config{
			Ports:  targetPorts,
			Bridge: &bridge.Config{},
		}

		derived, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: targetCfg})
		if err != nil {
			t.Fatalf("unexpected derive error: %v", err)
		}
		if len(derived.Entries()) != 0 {
			t.Errorf("expected entries dropped for LAG without members, got %v", derived.Entries())
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

		if _, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: invalidCfg}); err == nil {
			t.Errorf("expected validation error from Derive, got nil")
		}
	})

	t.Run("non-vlan to non-vlan re-keys to FID 0", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports:  ports,
			Bridge: &bridge.Config{},
		}
		sw := mustSwitch(t, cfg)
		mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		sw.Forward(fixedTime, "1/1/1", ethernet.Frame{
			Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			Src:       mac,
			EtherType: ethernet.EtherTypeIPv4,
		})

		derived, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: cfg})
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
		sw := mustSwitch(t, curCfg)
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
		derived, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: nonVlanCfg})
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

	sw := mustSwitch(t, cfg)

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
	if issues := power.Metadata.Issues(); len(issues) != 1 || issues[0].Code != "poe-demand-unknown" {
		t.Errorf("expected 1 poe-demand-unknown issue, got %v", issues)
	}

	// Config clone
	cloned := sw.Config()
	if cloned.Phy == nil {
		t.Errorf("cloned config missing phy")
	}
}

func TestSwitchPowerMetadata(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	spec := vswitch.ConstructionSpec{
		NodeID: "sw1",
		Config: vswitch.Config{
			Ports:  ports,
			Bridge: &bridge.Config{},
			Phy: &phy.Config{
				PoE: &phy.PoE{
					Groups: map[string]phy.Group{"g1": {PowerMilliwatts: 60_000}},
					Ports: map[string]phy.PsePort{
						"1/1/1": {Group: "g1", Enabled: true, MaxClass: 4, PD: phy.PDAttached, PDClass: phy.Class(4)},
						"1/1/2": {Group: "g1", Enabled: true, MaxClass: 4, PD: phy.PDUnknown},
					},
				},
			},
		},
	}

	sw, err := vswitch.NewWithSpec(spec)
	if err != nil {
		t.Fatalf("vswitch.NewWithSpec() error = %v", err)
	}

	power := sw.Power()
	if got, want := power.Ports["1/1/1"].State, phy.PowerDelivered; got != want {
		t.Errorf("port 1/1/1 state = %v, want %v", got, want)
	}
	if got, want := power.Ports["1/1/2"].State, phy.PowerUnknown; got != want {
		t.Errorf("port 1/1/2 state = %v, want %v", got, want)
	}

	wantEvaluatedScope := analysis.FieldScope(analysis.NodeScope("sw1"), "poe")
	if got := power.Metadata.Scope(); got != wantEvaluatedScope {
		t.Errorf("power.Metadata.Scope() = %v, want %v", got, wantEvaluatedScope)
	}
	if got, want := power.Metadata.Status(), analysis.Incomplete; got != want {
		t.Errorf("power.Metadata.Status() = %v, want %v", got, want)
	}

	issues := power.Metadata.Issues()
	if len(issues) != 1 {
		t.Fatalf("len(power.Metadata.Issues()) = %d, want 1", len(issues))
	}
	issue := issues[0]
	if issue.Code != "poe-demand-unknown" {
		t.Errorf("issue.Code = %q, want %q", issue.Code, "poe-demand-unknown")
	}
	if issue.Status != analysis.Incomplete {
		t.Errorf("issue.Status = %v, want %v", issue.Status, analysis.Incomplete)
	}
	wantPortScope := analysis.FieldScope(analysis.NodeScope("sw1"), "poe", "1/1/2")
	if issue.Scope != wantPortScope {
		t.Errorf("issue.Scope = %v, want %v", issue.Scope, wantPortScope)
	}

	// Forwarding metadata must not contain poe-demand-unknown
	frame := ethernet.Frame{
		Dst:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		EtherType: ethernet.EtherTypeIPv4,
	}
	fwd := sw.Forward(fixedTime, "1/1/1", frame)
	for _, fi := range fwd.Metadata.Issues() {
		if fi.Code == "poe-demand-unknown" {
			t.Errorf("forwarding metadata contains poe-demand-unknown: %+v", fi)
		}
	}
}

func TestSwitchAge(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	t.Run("ages dynamic entries", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: tbl,
			Bridge: &bridge.Config{
				AgingTime: 300 * time.Second,
			},
		}
		sw := mustSwitch(t, cfg)

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
		sw := mustSwitch(t, cfg)

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
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
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
				"1/1/1": {},
				"1/1/2": {},
			},
		},
		Traffic: &traffic.Config{Mirrors: []traffic.Mirror{{Name: "control", SelectAll: true, OutputPort: "1/1/3"}}},
	}

	sw := mustSwitch(t, cfg)
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

	frame := mustEncode(t, bpdu, macRoot)

	res := sw.Forward(now, "1/1/1", frame)
	if res.Outcome != trace.Consumed {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Consumed)
	}
	if len(res.Steps) != 2 || res.Steps[0].Layer != port.LayerStp || res.Steps[0].Op != trace.OpClassify {
		t.Errorf("res.Steps = %+v, want spanning-tree classification followed by the mirror decision", res.Steps)
	}
	if !traceHasFactType(res.Steps, "stp.bpdu_decision") {
		t.Errorf("BPDU trace has no spanning-tree decision fact: %+v", res.Steps)
	}
	if step, ok := mirrorCopyStep(res.Steps); !ok || step.Op != trace.OpReplicate {
		t.Errorf("BPDU trace has no admitted mirror decision: %+v", res.Steps)
	}
	if copies := sw.Copies(); len(copies) != 1 || copies[0].Mirror != "control" || copies[0].Port != "1/1/3" {
		t.Errorf("Copies() = %+v, want received BPDU mirrored to 1/1/3", copies)
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

	sw := mustSwitch(t, cfg)
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

	frame := mustEncode(t, bpdu, macRoot)

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
	sw := mustSwitch(t, cfg)

	macRoot := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}
	bpdu := stp.BPDU{
		RootID:   stp.BridgeID{Priority: 4096, Address: macRoot},
		BridgeID: stp.BridgeID{Priority: 4096, Address: macRoot},
	}
	frame := mustEncode(t, bpdu, macRoot)

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
			if trace.CompareFact(ch.From, stp.PriorityFact(32768)) != 0 || trace.CompareFact(ch.To, stp.PriorityFact(4096)) != 0 {
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

	sw := mustSwitch(t, cfg)
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

	frame := mustEncode(t, bpdu, macRoot)

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

func TestSwitchTreeRoles(t *testing.T) {
	t.Parallel()

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	// 1. Switch without STP returns nil.
	swNoSTP := mustSwitch(t, vswitch.Config{
		Ports:  ports,
		Bridge: &bridge.Config{},
	})
	if got := swNoSTP.TreeRoles(); got != nil {
		t.Fatalf("swNoSTP.TreeRoles() = %v, want nil", got)
	}

	// 2. Switch with plain RSTP returns VLAN 1.
	swRSTP := mustSwitch(t, vswitch.Config{
		Ports:  ports,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	})
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	swRSTP.Start(now)
	swRSTP.Drain()

	treeRolesRSTP := swRSTP.TreeRoles()
	if treeRolesRSTP == nil {
		t.Fatalf("swRSTP.TreeRoles() = nil, want map with VLAN 1")
	}
	if _, ok := treeRolesRSTP[1]; !ok {
		t.Fatalf("swRSTP.TreeRoles() missing VLAN 1: %+v", treeRolesRSTP)
	}
	if info, ok := treeRolesRSTP[1]["1/1/1"]; !ok || info.Role != stp.RoleDesignated {
		t.Errorf("swRSTP.TreeRoles()[1][\"1/1/1\"] = %+v, want designated role", info)
	}

	// 3. Switch with MST returns configured instance VLANs.
	swMST := mustSwitch(t, vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "VLAN10", 20: "VLAN20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {Tagged: []vlan.ID{10, 20}},
					"1/1/2": {Tagged: []vlan.ID{10, 20}},
				},
			},
		},
		STP: &stp.Config{
			Priority: 32768,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
			MST: &stp.MST{
				Name: "region-1",
				Instances: map[stp.MSTID]stp.Instance{
					1: {VLANs: []vlan.ID{10}},
					2: {VLANs: []vlan.ID{20}},
				},
			},
		},
	})
	swMST.Start(now)
	swMST.Drain()
	treeRolesMST := swMST.TreeRoles()
	if treeRolesMST == nil {
		t.Fatalf("swMST.TreeRoles() = nil, want per-VLAN roles")
	}
	for _, vid := range []vlan.ID{1, 10, 20} {
		if _, ok := treeRolesMST[vid]; !ok {
			t.Errorf("swMST.TreeRoles() missing VID %d: %+v", vid, treeRolesMST)
		}
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

	sw := mustSwitch(t, cfg)
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

	sw := mustSwitch(t, cfg)
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
	frame := mustEncode(t, inferiorBPDU, inferiorBridgeID.Address)

	sw.Forward(t0.Add(4*time.Second), "1/1/1", frame)

	emissions := sw.Drain()
	if len(emissions) == 0 {
		t.Fatal("Drain() returned 0 emissions, want reply emission")
	}

	var replyEmission *vswitch.Emission
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

	sw := mustSwitch(t, cfg)
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

	sw.Forward(now, "1/1/1", mustEncode(t, bpdu, macRoot))

	rolesBefore := sw.Roles()
	if rolesBefore["1/1/1"].Role != stp.RoleRoot || rolesBefore["1/1/1"].State != stp.StateForwarding {
		t.Fatalf("1/1/1 before derive = %s/%s, want Root/Forwarding", rolesBefore["1/1/1"].Role, rolesBefore["1/1/1"].State)
	}

	derived, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: cfg})
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

	sw := mustSwitch(t, cfg)
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
	sw.Forward(now, "1/1/1", mustEncode(t, bpdu, macRoot))
	if before := sw.Roles()["1/1/1"]; before.Role != stp.RoleRoot || before.State != stp.StateForwarding {
		t.Fatalf("1/1/1 before derive = %s/%s, want Root/Forwarding", before.Role, before.State)
	}

	derived, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: cfg})
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
	sw := mustSwitch(t, vswitch.Config{
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

	res := sw.Forward(now, "1/1/1", mustEncode(t, bpdu, macRoot))
	if res.Outcome != trace.Dropped || res.Reason != port.ReasonPortDown {
		t.Fatalf("BPDU on down port = %s/%s, want Dropped/%s", res.Outcome, res.Reason, port.ReasonPortDown)
	}
	if root, _, _ := sw.Root(); root.Address == macRoot {
		t.Error("layer adopted a root from a BPDU on a down port")
	}
}

func loopProtectTestSwitch(t *testing.T) *vswitch.Switch {
	t.Helper()

	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	return mustSwitch(t, vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: loopprotect.Manual}},
			},
		},
	})
}

// TestLoopProtectOwnProbeConsumedNotFlooded proves that a probe this switch
// sent, returned by an unmanaged loop, is consumed by the loop-protection
// interception rather than relayed: the mechanism only works if the switch
// recognizes and acts on its own probe instead of flooding it like any other
// frame addressed to an unregistered multicast group.
func TestLoopProtectOwnProbeConsumedNotFlooded(t *testing.T) {
	sw := loopProtectTestSwitch(t)
	now := fixedTime

	mac := sw.Config().MAC
	probe := loopprotect.Probe{OriginMAC: mac, VID: 0, Sequence: 1, Port: "1/1/1"}
	frame := loopprotect.Encode(probe, mac)

	res := sw.Forward(now, "1/1/1", frame)
	if res.Outcome != trace.Consumed {
		t.Fatalf("own probe outcome = %s, want %s", res.Outcome, trace.Consumed)
	}
	if len(res.Egress) != 0 {
		t.Errorf("own probe produced %d egress transmissions, want 0 (consumed, not flooded)", len(res.Egress))
	}
	if !traceHasRuleID(res.Steps, "loopprotect.probe.return") {
		t.Errorf("own probe steps lack a probe-return step: %+v", res.Steps)
	}
	if !traceHasRuleID(res.Steps, "loopprotect.port.block") {
		t.Errorf("own probe steps lack a port-block step for the newly applied action: %+v", res.Steps)
	}
}

// TestLoopProtectForeignProbeFloodsWithOneClassificationStep proves that a
// probe naming another switch as origin is left untouched by the
// loop-protection interception and falls through to the ordinary relay
// path, where it floods as unregistered multicast. Classifying it once on
// the fall-through, rather than once in the interception and again on the
// fall-through, matters because a corpus comparing the ordered step list
// exactly would fail on a duplicate classification.
func TestLoopProtectForeignProbeFloodsWithOneClassificationStep(t *testing.T) {
	sw := loopProtectTestSwitch(t)
	now := fixedTime

	foreignMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}
	probe := loopprotect.Probe{OriginMAC: foreignMAC, VID: 0, Sequence: 1, Port: "1/1/1"}
	frame := loopprotect.Encode(probe, foreignMAC)

	res := sw.Forward(now, "1/1/1", frame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("foreign probe outcome = %s, want %s", res.Outcome, trace.Flooded)
	}

	classifications := 0
	for _, step := range res.Steps {
		if step.Op == trace.OpClassify {
			classifications++
		}
	}
	if classifications != 1 {
		t.Errorf("foreign probe produced %d classification steps, want exactly 1: %+v", classifications, res.Steps)
	}
	if traceHasFactType(res.Steps, "vswitch.loopprotect_decision") {
		t.Errorf("foreign probe steps carry a loop-protection decision: %+v", res.Steps)
	}
}

// TestLoopProtectGatedPortDropsBeforeDetection proves the mechanism that
// keeps a two-port loop from blocking both ports: once a port's action is
// applied, the bridge's ordinary ingress gate denies both learning and
// forwarding on it, so a probe arriving there next is dropped by the gate
// before the interception ever calls Receive again.
func TestLoopProtectGatedPortDropsBeforeDetection(t *testing.T) {
	sw := loopProtectTestSwitch(t)
	now := fixedTime

	mac := sw.Config().MAC
	probe := loopprotect.Probe{OriginMAC: mac, VID: 0, Sequence: 1, Port: "1/1/1"}
	frame := loopprotect.Encode(probe, mac)

	first := sw.Forward(now, "1/1/1", frame)
	if first.Outcome != trace.Consumed {
		t.Fatalf("first probe outcome = %s, want %s", first.Outcome, trace.Consumed)
	}

	second := sw.Forward(now, "1/1/1", frame)
	if second.Outcome != trace.Dropped || second.Reason != bridge.ReasonPortBlocked {
		t.Fatalf("second probe on the now-blocked port = %s/%s, want %s/%s",
			second.Outcome, second.Reason, trace.Dropped, bridge.ReasonPortBlocked)
	}
	if traceHasRuleID(second.Steps, "loopprotect.probe.return") {
		t.Errorf("second probe ran detection after the port was already blocked: %+v", second.Steps)
	}
}

// TestLoopProtectManualClearsOnLAGPortCycle proves that a Manual recovery
// applied to a protected LAG port clears when the LAG's link cycles down and
// back up, the same way it clears on a physical port: updateLagState now
// notifies loop protection of the LAG's aggregate link state, not just
// spanning tree, so a LAG-only switch's protected port is not stuck applied
// forever once blocked.
func TestLoopProtectManualClearsOnLAGPortCycle(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		LAG: &lag.Config{LAGs: map[string]lag.LAG{
			"lag1": {Mode: lag.BalanceSLB, Members: map[string]lag.Member{"1/1/1": {}, "1/1/2": {}}},
		}},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"lag1": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: loopprotect.Manual}},
			},
		},
	})

	now := fixedTime
	sw.Start(now)
	sw.Drain()

	mac := sw.Config().MAC
	probe := loopprotect.Probe{OriginMAC: mac, VID: 0, Sequence: 1, Port: "lag1"}
	res := sw.Forward(now, "lag1", loopprotect.Encode(probe, mac))
	if res.Outcome != trace.Consumed {
		t.Fatalf("probe outcome = %s, want %s", res.Outcome, trace.Consumed)
	}

	other := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x55}
	broadcast := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	data := ethernet.Frame{Dst: broadcast, Src: other, EtherType: 0x0800, Payload: []byte{1, 2, 3}}

	blocked := sw.Forward(now, "lag1", data)
	if blocked.Outcome != trace.Dropped || blocked.Reason != bridge.ReasonPortBlocked {
		t.Fatalf("frame on lag1 after Block applied = %s/%s, want %s/%s",
			blocked.Outcome, blocked.Reason, trace.Dropped, bridge.ReasonPortBlocked)
	}

	sw.LinkChange(now, "1/1/1", port.Down, vswitch.PointToPointUnknown, 0)
	sw.LinkChange(now, "1/1/2", port.Down, vswitch.PointToPointUnknown, 0)
	sw.LinkChange(now, "1/1/1", port.Up, vswitch.PointToPointUnknown, 0)
	sw.LinkChange(now, "1/1/2", port.Up, vswitch.PointToPointUnknown, 0)

	// lag1 is the fixture's only non-member port, so an unblocked broadcast
	// off it has nowhere left to flood to: the positive outcome here is
	// Dropped/no-egress, not the port-blocked drop the precondition proved.
	after := sw.Forward(now, "lag1", data)
	if after.Outcome != trace.Dropped || after.Reason != bridge.ReasonNoEgress {
		t.Fatalf("frame on lag1 after a port cycle = %s/%s, want %s/%s: the Manual action should have cleared",
			after.Outcome, after.Reason, trace.Dropped, bridge.ReasonNoEgress)
	}
}

// TestLoopProtectTrunkWithPVIDEmitsAProbe proves that a protected trunk port
// with no configured VLANs still probes on a VLAN-aware bridge, once it
// carries a PVID: the layer emits VID 0 for such a port, and the switch
// resolves that to the port's PVID before asking the bridge to originate the
// frame, so the probe is not silently discarded for naming a VID the bridge
// does not admit.
func TestLoopProtectTrunkWithPVIDEmitsAProbe(t *testing.T) {
	pvid := vlan.ID(10)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "a", 20: "b"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &pvid, Untagged: []vlan.ID{10}, Tagged: []vlan.ID{20}},
			},
		}},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block},
			},
		},
	})

	now := fixedTime
	sw.Start(now)
	sw.Drain()

	sw.Wake(now.Add(loopprotect.DefaultInterval))
	emissions := sw.Drain()

	found := false
	for _, em := range emissions {
		if em.Port != "1/1/1" {
			continue
		}
		if _, err := loopprotect.Decode(em.Frame); err == nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("emissions after Wake = %+v, want a decodable loop-protection probe on 1/1/1", emissions)
	}
}

// loopProtectReturnFactString locates the "loopprotect.probe.return" step in
// res's trace and returns its output fact's canonical string, or "" if the
// step is not present.
func loopProtectReturnFactString(t *testing.T, res vswitch.ForwardResult) string {
	t.Helper()

	for _, step := range res.Steps {
		if step.RuleID == "loopprotect.probe.return" && len(step.Outputs) > 0 {
			return step.Outputs[0].Canonical()
		}
	}

	return ""
}

// TestLoopProtectAccessPortReturnedProbeIsNotInterVLAN proves that a probe
// emitted on a port with no configured VLANs (VID 0, resolved to the port's
// PVID) does not falsely report an inter-VLAN loop when it returns on a
// VLAN-aware bridge. It drives the real emission path (Wake, Drain, then
// Forward with the frame the switch actually put on the wire): the switch
// must re-encode the probe's payload with the resolved VID before
// transmitting, so the payload names the VLAN the frame rides and a return
// classified into that VLAN is not inter-VLAN.
func TestLoopProtectAccessPortReturnedProbeIsNotInterVLAN(t *testing.T) {
	pvid := vlan.ID(10)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "a"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &pvid, Untagged: []vlan.ID{10}},
			},
		}},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				// No VLANs configured: Wake emits VID 0 for this port.
				"1/1/1": {Action: loopprotect.Block},
			},
		},
	})

	now := fixedTime
	sw.Start(now)
	sw.Drain()

	sw.Wake(now.Add(loopprotect.DefaultInterval))
	emissions := sw.Drain()

	var probeFrame ethernet.Frame
	found := false
	for _, em := range emissions {
		if em.Port != "1/1/1" {
			continue
		}
		if _, err := loopprotect.Decode(em.Frame); err == nil {
			probeFrame = em.Frame
			found = true
		}
	}
	if !found {
		t.Fatalf("emissions after Wake = %+v, want a decodable loop-protection probe on 1/1/1", emissions)
	}

	// An unmanaged loop hands the same probe back on the port it left from.
	res := sw.Forward(now, "1/1/1", probeFrame)
	if res.Outcome != trace.Consumed {
		t.Fatalf("returned probe Outcome = %s, want %s", res.Outcome, trace.Consumed)
	}

	factStr := loopProtectReturnFactString(t, res)
	if factStr == "" {
		t.Fatalf("trace has no loopprotect.probe.return step: %+v", res.Steps)
	}
	if !strings.Contains(factStr, "sent_vid=10;returned_vid=10") {
		t.Errorf("loopprotect.probe.return fact = %q, want sent_vid and returned_vid to agree at 10", factStr)
	}
	if !strings.Contains(factStr, "after={action=\"Block\";inter_vlan=false") {
		t.Errorf("loopprotect.probe.return fact = %q, want after.inter_vlan=false", factStr)
	}
}

// TestLoopProtectCrossVLANReturnedProbeIsInterVLAN proves the genuine
// positive this package must still detect: a probe sent on one VLAN that
// returns classified into a different VLAN is a real inter-VLAN loop. The
// probe here is built and fed back by hand naming VID 10 as the VLAN it was
// sent on, then delivered untagged so the port's own PVID (20) classifies
// the return into a different VLAN, reproducing what a real inter-VLAN loop
// looks like on the wire.
func TestLoopProtectCrossVLANReturnedProbeIsInterVLAN(t *testing.T) {
	pvid := vlan.ID(20)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "a", 20: "b"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &pvid, Untagged: []vlan.ID{20}, Tagged: []vlan.ID{10}},
			},
		}},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{10}},
			},
		},
	})

	now := fixedTime
	sw.Start(now)
	sw.Drain()

	mac := sw.Config().MAC
	probe := loopprotect.Probe{OriginMAC: mac, VID: 10, Sequence: 1, Port: "1/1/1"}
	// Delivered untagged, so bridge ingress classifies it by the port's
	// PVID (20) rather than by the VID 10 the payload names as sent.
	frame := loopprotect.Encode(probe, mac)

	res := sw.Forward(now, "1/1/1", frame)
	if res.Outcome != trace.Consumed {
		t.Fatalf("returned probe Outcome = %s, want %s", res.Outcome, trace.Consumed)
	}

	factStr := loopProtectReturnFactString(t, res)
	if factStr == "" {
		t.Fatalf("trace has no loopprotect.probe.return step: %+v", res.Steps)
	}
	if !strings.Contains(factStr, "sent_vid=10;returned_vid=20") {
		t.Fatalf("loopprotect.probe.return fact = %q, want sent_vid=10;returned_vid=20", factStr)
	}
	if !strings.Contains(factStr, "inter_vlan=true") {
		t.Errorf("loopprotect.probe.return fact = %q, want inter_vlan=true", factStr)
	}
}

// TestLoopProtectAsymmetricVLANReturnedProbeIsNotInterVLAN proves the false
// positive this package must not report: a port that is an untagged member
// of two VLANs (the "asymmetric VLAN" shared-uplink configuration IEEE
// 802.1Q permits, since untagged egress is a per-VLAN port set while ingress
// classification is a per-port PVID scalar) sends a probe on one of those
// VLANs and gets it back classified into the other by its own PVID. Nothing
// bridged the two VLANs: the numbers are local to this port on either side
// of an untagged wire. The loop itself is still real, so the action must
// still apply; only the VLAN finding is false.
func TestLoopProtectAsymmetricVLANReturnedProbeIsNotInterVLAN(t *testing.T) {
	pvid := vlan.ID(10)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "a", 20: "b"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &pvid, Untagged: []vlan.ID{10, 20}},
			},
		}},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{20}},
			},
		},
	})

	now := fixedTime
	sw.Start(now)
	sw.Drain()

	mac := sw.Config().MAC
	probe := loopprotect.Probe{OriginMAC: mac, VID: 20, Sequence: 1, Port: "1/1/1"}
	// Delivered untagged, so bridge ingress classifies it by the port's
	// PVID (10) even though the port also untags egress for VLAN 20.
	frame := loopprotect.Encode(probe, mac)

	res := sw.Forward(now, "1/1/1", frame)
	if res.Outcome != trace.Consumed {
		t.Fatalf("returned probe Outcome = %s, want %s", res.Outcome, trace.Consumed)
	}

	factStr := loopProtectReturnFactString(t, res)
	if factStr == "" {
		t.Fatalf("trace has no loopprotect.probe.return step: %+v", res.Steps)
	}
	if !strings.Contains(factStr, "sent_vid=20;returned_vid=10") {
		t.Fatalf("loopprotect.probe.return fact = %q, want sent_vid=20;returned_vid=10", factStr)
	}
	if !strings.Contains(factStr, "after={action=\"Block\";inter_vlan=false") {
		t.Errorf("loopprotect.probe.return fact = %q, want after.inter_vlan=false with the Block action still applied", factStr)
	}
}

// TestLoopProtectValidateAgreesWithEgressAdmission is evidence that
// vswitch.Config.Validate and bridge.Switchport.CarriesVID never disagree
// about whether a protected port carries the VLAN it probes: for every
// switchport shape below, either Validate refuses the configuration or a
// probe for that port actually reaches the switch's emitted frames after a
// Wake, never neither. A PVID by itself does not admit a VID on egress (only
// Tagged, Untagged, or a tunnel's VID does), so a config that only checked
// PVID at validation time could accept a setup that then emits nothing.
func TestLoopProtectValidateAgreesWithEgressAdmission(t *testing.T) {
	vid10 := vlan.ID(10)

	newTable := func(names ...string) port.Table {
		b := port.NewBuilder()
		for _, name := range names {
			b = b.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		}
		return mustTable(t, b)
	}

	tests := []struct {
		name       string
		cfg        vswitch.Config
		wantRefuse bool
	}{
		{
			name: "VLAN-unaware bridge probes untagged",
			cfg: vswitch.Config{
				Ports:  newTable("1/1/1"),
				Bridge: &bridge.Config{},
				LoopProtect: &loopprotect.Config{
					Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block}},
				},
			},
		},
		{
			name: "access port with PVID in Untagged",
			cfg: vswitch.Config{
				Ports: newTable("1/1/1"),
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "a"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
					},
				}},
				LoopProtect: &loopprotect.Config{
					Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block}},
				},
			},
		},
		{
			name: "trunk with VLAN in Tagged and explicit VLANs",
			cfg: vswitch.Config{
				Ports: newTable("1/1/1"),
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "a", 20: "b"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: &vid10, Tagged: []vlan.ID{20}},
					},
				}},
				LoopProtect: &loopprotect.Config{
					Ports: map[string]loopprotect.Port{
						"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{20}},
					},
				},
			},
		},
		{
			name: "trunk PVID in neither list, no explicit VLANs",
			cfg: vswitch.Config{
				Ports: newTable("1/1/1"),
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "a", 20: "b"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: &vid10, Tagged: []vlan.ID{20}},
					},
				}},
				LoopProtect: &loopprotect.Config{
					Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block}},
				},
			},
			wantRefuse: true,
		},
		{
			name: "trunk PVID in neither list, with explicit VLANs naming the PVID",
			cfg: vswitch.Config{
				Ports: newTable("1/1/1"),
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "a", 20: "b"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: &vid10, Tagged: []vlan.ID{20}},
					},
				}},
				LoopProtect: &loopprotect.Config{
					Ports: map[string]loopprotect.Port{
						"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{10}},
					},
				},
			},
			wantRefuse: true,
		},
		{
			name: "tunnel switchport with no PVID and no explicit VLANs",
			cfg: vswitch.Config{
				Ports: newTable("1/1/1"),
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "a"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Tunnel: &bridge.Tunnel{VID: 10}},
					},
				}},
				LoopProtect: &loopprotect.Config{
					Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block}},
				},
			},
		},
		{
			name: "protected port with no switchport entry at all",
			cfg: vswitch.Config{
				Ports: newTable("1/1/1"),
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table:       map[vlan.ID]string{10: "a"},
					Switchports: map[string]bridge.Switchport{},
				}},
				LoopProtect: &loopprotect.Config{
					Ports: map[string]loopprotect.Port{"1/1/1": {Action: loopprotect.Block}},
				},
			},
			wantRefuse: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sw, err := vswitch.New(tt.cfg)
			if tt.wantRefuse {
				if err == nil {
					t.Fatalf("vswitch.New = nil error, want the configuration refused")
				}
				return
			}
			if err != nil {
				t.Fatalf("vswitch.New: %v", err)
			}

			now := fixedTime
			sw.Start(now)
			sw.Drain()
			sw.Wake(now.Add(loopprotect.DefaultInterval))
			emissions := sw.Drain()

			found := false
			for _, em := range emissions {
				if em.Port != "1/1/1" {
					continue
				}
				if _, err := loopprotect.Decode(em.Frame); err == nil {
					found = true
				}
			}
			if !found {
				t.Fatalf("emissions after Wake = %+v, want a decodable loop-protection probe on 1/1/1", emissions)
			}
		})
	}
}

// TestLoopProtectAppliedActionFlushesLearnedEntries proves that applying a
// loop-protection action flushes the forwarding entries the loop taught the
// blocked port: without the flush, a later unicast to that host keeps
// following the stale entry into the now-blocked port and is dropped as
// port-blocked instead of flooding to relocate it.
func TestLoopProtectAppliedActionFlushesLearnedEntries(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: loopprotect.Manual}},
			},
		},
	})
	now := fixedTime

	host := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x42}
	mustSwitchLearn(t, sw, []bridge.Seed{
		{FID: 0, MAC: host, Port: "1/1/1", LearnedAt: now},
	})

	mac := sw.Config().MAC
	probe := loopprotect.Probe{OriginMAC: mac, VID: 0, Sequence: 1, Port: "1/1/1"}
	sw.Forward(now, "1/1/1", loopprotect.Encode(probe, mac))

	other := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x55}
	data := ethernet.Frame{Dst: host, Src: other, EtherType: 0x0800, Payload: []byte{1, 2, 3}}

	res := sw.Forward(now, "1/1/2", data)
	if res.Outcome != trace.Flooded {
		t.Fatalf("unicast to the host learned through the loop = %s, want %s (stale entry flushed)", res.Outcome, trace.Flooded)
	}
	floodedTo13 := false
	for _, eg := range res.Egress {
		if eg.Port == "1/1/3" {
			floodedTo13 = true
		}
	}
	if !floodedTo13 {
		t.Errorf("flood egress = %+v, want 1/1/3 among the targets", res.Egress)
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
	sw.Forward(now, "1/1/1", loopprotect.Encode(probe, mac))

	derived, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: sw.Config()})
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	other := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x55}
	broadcast := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	data := ethernet.Frame{Dst: broadcast, Src: other, EtherType: 0x0800, Payload: []byte{1, 2, 3}}

	res := derived.Forward(now, "1/1/1", data)
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
// It changes the current switch's live operational state with SetOperStatus,
// which mutates only s.ports and never notifies the loop-protection layer,
// so it isolates what Derive itself does with the mismatch: the target
// configuration (sw.Config()) still reads the port Up, so the derived port
// is usable, and the only thing that can still block it is a retained layer.
func TestDeriveRebuildsLoopProtectOnAdminStatusCycle(t *testing.T) {
	sw := loopProtectTestSwitch(t)
	now := fixedTime

	mac := sw.Config().MAC
	probe := loopprotect.Probe{OriginMAC: mac, VID: 0, Sequence: 1, Port: "1/1/1"}
	sw.Forward(now, "1/1/1", loopprotect.Encode(probe, mac))

	other := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x55}
	broadcast := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	data := ethernet.Frame{Dst: broadcast, Src: other, EtherType: 0x0800, Payload: []byte{1, 2, 3}}

	precondition := sw.Forward(now, "1/1/1", data)
	if precondition.Outcome != trace.Dropped || precondition.Reason != bridge.ReasonPortBlocked {
		t.Fatalf("precondition: current switch on 1/1/1 = %s/%s, want %s/%s",
			precondition.Outcome, precondition.Reason, trace.Dropped, bridge.ReasonPortBlocked)
	}

	if err := sw.SetOperStatus("1/1/1", port.Down); err != nil {
		t.Fatalf("SetOperStatus: %v", err)
	}

	derived, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: sw.Config()})
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}

	res := derived.Forward(now, "1/1/1", data)
	if res.Outcome != trace.Flooded {
		t.Fatalf("derived switch after the current switch's port state diverged from the target: 1/1/1 = %s/%s, want %s: the rebuilt layer's action should have cleared",
			res.Outcome, res.Reason, trace.Flooded)
	}
}

// TestDiffLoopProtectCapabilityAndFieldChange proves that vswitch.Diff
// reports loop protection's capability presence change and, for two
// configurations differing by exactly one loop-protection field, exactly
// one loop-protection change.
func TestDiffLoopProtectCapabilityAndFieldChange(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}))

	base := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
	}
	withLoopProtect := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block},
			},
		},
	}

	capChanges := vswitch.Diff(base, withLoopProtect)
	foundCap := false
	for _, ch := range capChanges {
		if ch.Layer == port.LayerLoopProtect && ch.Subject.Kind == "capability" {
			foundCap = true
		}
	}
	if !foundCap {
		t.Fatalf("Diff() did not report a loop-protection capability change: %+v", capChanges)
	}

	withNoLearn := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.NoLearn},
			},
		},
	}

	fieldChanges := vswitch.Diff(withLoopProtect, withNoLearn)
	loopProtectChanges := 0
	for _, ch := range fieldChanges {
		if ch.Layer == port.LayerLoopProtect && ch.Subject.Kind == "port" {
			loopProtectChanges++
		}
	}
	if loopProtectChanges != 1 {
		t.Errorf("Diff() reported %d loop-protection field changes for one changed field, want 1: %+v", loopProtectChanges, fieldChanges)
	}
}

func traceHasRuleID(steps []trace.Step, ruleID trace.RuleID) bool {
	for _, step := range steps {
		if step.RuleID == ruleID {
			return true
		}
	}

	return false
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
		if got[i].Layer != want[i].Layer || got[i].Op != want[i].Op || got[i].RuleID != want[i].RuleID || got[i].Subject != want[i].Subject {
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
	return mustSwitch(t, cfg)
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
		sw := mustSwitch(t, cfg)
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
		sw := mustSwitch(t, cfg)
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
		cur := mustSwitch(t, cfg)
		nextCfg := vswitch.Config{
			Ports: ports,
		}
		target := cur.Spec()
		target.Config = nextCfg
		target.Config.MAC = cur.Config().MAC
		nextSw, err := vswitch.Derive(cur, target)
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
		{Layer: port.LayerVlan, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
		{Layer: port.LayerRelay, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
		{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
		{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"}},
		{Layer: port.LayerRouting, Op: trace.OpRewrite, RuleID: "decrement-ttl", Subject: trace.Subject{Kind: "interface", Key: "vlan20"}},
		{Layer: port.LayerRelay, Op: trace.OpLookup, RuleID: "unicast-hit", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:77"}},
		{Layer: port.LayerVlan, Op: trace.OpRewrite, RuleID: "vlan-tag-form", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
		{Layer: port.LayerRelay, Op: trace.OpTransmit, RuleID: "transmit", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
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
		{Layer: port.LayerVlan, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
		{Layer: port.LayerRelay, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
		{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
		{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"}},
		{Layer: port.LayerRouting, Op: trace.OpRewrite, RuleID: "decrement-ttl", Subject: trace.Subject{Kind: "interface", Key: "vlan20"}},
		{Layer: port.LayerRelay, Op: trace.OpLookup, RuleID: "unicast-miss", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:77"}},
		{Layer: port.LayerRelay, Op: trace.OpReplicate, RuleID: "flood", Subject: trace.Subject{Kind: "vlan", Key: "20"}},
		{Layer: port.LayerVlan, Op: trace.OpRewrite, RuleID: "vlan-tag-form", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
		{Layer: port.LayerRelay, Op: trace.OpTransmit, RuleID: "transmit", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
	}
	assertSteps(t, resFlood.Steps, wantFloodSteps)
	if resFlood.Outcome != trace.Flooded {
		t.Errorf("resFlood.Outcome = %v, want %v", resFlood.Outcome, trace.Flooded)
	}
	if resFlood.FID != 20 {
		t.Errorf("resFlood.FID = %d, want 20", resFlood.FID)
	}
}

// TestNeighborPendingHolds forwards to a next hop with no configured or
// observed neighbor. Under the default NeighborObserved policy this is a
// hold, not a drop: netsim has not asked about the address yet, which is a
// different claim from the definite failure TestNeighborDisabledMisses
// covers on the same topology.
func TestNeighborPendingHolds(t *testing.T) {
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
	sw := mustSwitch(t, cfg)

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("hello"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := sw.Forward(fixedTime, "1/1/1", frame)

	wantSteps := []trace.Step{
		{Layer: port.LayerVlan, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
		{Layer: port.LayerRelay, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
		{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
		{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"}},
		{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: "neighbor-pending", Subject: trace.Subject{Kind: "ip", Key: "10.0.20.7"}},
	}
	assertSteps(t, res.Steps, wantSteps)

	if res.Outcome != trace.Held {
		t.Errorf("outcome = %v, want %v", res.Outcome, trace.Held)
	}
	if res.Reason != routing.ReasonNeighborPending {
		t.Errorf("reason = %v, want %v", res.Reason, routing.ReasonNeighborPending)
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
	if !slices.ContainsFunc(res.Metadata.Issues(), func(issue analysis.Issue) bool {
		return issue.Code == vswitch.IssueNeighborUnresolved && issue.Status == analysis.Incomplete
	}) {
		t.Errorf("issues = %+v, want IssueNeighborUnresolved at Incomplete", res.Metadata.Issues())
	}
}

// TestNeighborDisabledMisses forwards the same unresolved next hop as
// TestNeighborPendingHolds, but with the VRF's neighbor policy set to
// NeighborDisabled: there netsim does know the outcome, so the frame is a
// definite drop rather than a hold.
func TestNeighborDisabledMisses(t *testing.T) {
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
					NeighborPolicy: routing.NeighborPolicy{Mode: routing.NeighborDisabled},
					// No neighbors configured.
				},
			},
		},
	}
	sw := mustSwitch(t, cfg)

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("hello"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   pkt,
	}

	res := sw.Forward(fixedTime, "1/1/1", frame)

	wantSteps := []trace.Step{
		{Layer: port.LayerVlan, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
		{Layer: port.LayerRelay, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
		{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
		{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"}},
		{Layer: port.LayerRouting, Op: trace.OpDrop, RuleID: "neighbor-miss", Subject: trace.Subject{Kind: "ip", Key: "10.0.20.7"}},
	}
	assertSteps(t, res.Steps, wantSteps)

	if res.Outcome != trace.Dropped {
		t.Errorf("outcome = %v, want %v", res.Outcome, trace.Dropped)
	}
	if res.Reason != routing.ReasonNeighborMiss {
		t.Errorf("reason = %v, want %v", res.Reason, routing.ReasonNeighborMiss)
	}
	if res.Metadata.Status() != analysis.Complete {
		t.Errorf("status = %v, want %v", res.Metadata.Status(), analysis.Complete)
	}
}

// TestNeighborDisabledDoesNotObserve exercises neighborObservationAllowed's
// gate over a NeighborDisabled VRF, but does not pin it: the VRF here holds
// only the statically configured binding for ipH2, and
// [routing.Layer.Observe] already refuses to overwrite a configured binding
// regardless of mode (see the origin guard in routing/neighbor.go), so the
// static MAC survives this test whether or not the switch-side mode gate
// exists. Stubbing neighborObservationAllowed to always return true still
// leaves this test passing.
//
// The gate still stands for what the origin guard does not cover: an
// observed (non-configured) entry on a VRF whose mode is NeighborDisabled.
// That state is not constructible through any exported path today. An
// entry gets origin "observed" only by resolving through
// [routing.vrfState.resolveNeighbor], which creates one only under
// [routing.NeighborObserved]; the only way to reach NeighborDisabled from
// NeighborObserved is a config change through [Derive], and any diff in
// [routing.Config] — a NeighborPolicy.Mode change included — rebuilds the
// routing layer from scratch (see derive.go's routing.Diff check), which
// seeds only configured neighbors, discarding whatever was observed under
// the old mode. So there is no sequence of exported calls that leaves a
// NeighborDisabled VRF holding an observed entry for the gate to protect
// against here; TestDeriveRebuildsRoutingWhenNeighborPolicyChanges pins the
// rebuild-on-policy-change half of that argument directly.
func TestNeighborDisabledDoesNotObserve(t *testing.T) {
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
					NeighborPolicy: routing.NeighborPolicy{Mode: routing.NeighborDisabled},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: ipH2, MAC: macH2},
					},
				},
			},
		},
	}
	sw := mustSwitch(t, cfg)

	spoofedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x99, 0x99}
	reply := makeARPReply(t, ipH2, netip.MustParseAddr("10.0.20.1"), spoofedMAC, macRouter)
	sw.Forward(fixedTime, "1/1/2", reply)

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}
	res := sw.Forward(fixedTime.Add(time.Millisecond), "1/1/1", frame)

	if len(res.Egress) != 1 {
		t.Fatalf("len(res.Egress) = %d, want 1", len(res.Egress))
	}
	if got := res.Egress[0].Frame.Dst; got != macH2 {
		t.Errorf("egress.Frame.Dst = %v, want %v: the observed ARP reply must not have rewritten the static neighbor", got, macH2)
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
			{Layer: port.LayerVlan, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
			{Layer: port.LayerRelay, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
			{Layer: port.LayerRouting, Op: trace.OpDrop, RuleID: "ttl-expired", Subject: trace.Subject{Kind: "ip", Key: "10.0.20.7"}},
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
			{Layer: port.LayerVlan, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
			{Layer: port.LayerRelay, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
			{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: "local-delivery", Subject: trace.Subject{Kind: "ip", Key: "10.0.10.1"}},
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
			{Layer: port.LayerVlan, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
			{Layer: port.LayerRelay, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
			{Layer: port.LayerRouting, Op: trace.OpDrop, RuleID: "bad-header", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
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
			{Layer: port.LayerVlan, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
			{Layer: port.LayerRelay, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
			{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: "no-route", Subject: trace.Subject{Kind: "ip", Key: "10.0.30.7"}},
			{Layer: port.LayerRouting, Op: trace.OpDrop, RuleID: "no-route", Subject: trace.Subject{Kind: "vrf", Key: "default"}},
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
	sw := mustSwitch(t, cfg)

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
			{Layer: port.LayerVlan, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
			{Layer: port.LayerRelay, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
			{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "2001:db8:20::/64"}},
			{Layer: port.LayerRouting, Op: trace.OpRewrite, RuleID: "decrement-ttl", Subject: trace.Subject{Kind: "interface", Key: "vlan20"}},
			{Layer: port.LayerRelay, Op: trace.OpLookup, RuleID: "unicast-hit", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:77"}},
			{Layer: port.LayerVlan, Op: trace.OpRewrite, RuleID: "vlan-tag-form", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
			{Layer: port.LayerRelay, Op: trace.OpTransmit, RuleID: "transmit", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
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
			{Layer: port.LayerVlan, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
			{Layer: port.LayerRelay, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
			{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: "no-route", Subject: trace.Subject{Kind: "ip", Key: "2001:db8:30::7"}},
			{Layer: port.LayerRouting, Op: trace.OpDrop, RuleID: "no-route", Subject: trace.Subject{Kind: "vrf", Key: "default"}},
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
	sw := mustSwitch(t, cfg)

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
			{Layer: port.LayerVlan, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "30"}},
			{Layer: port.LayerRelay, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan30"}},
			{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"}},
			{Layer: port.LayerRouting, Op: trace.OpRewrite, RuleID: "decrement-ttl", Subject: trace.Subject{Kind: "interface", Key: "vlan40"}},
			{Layer: port.LayerRelay, Op: trace.OpLookup, RuleID: "unicast-hit", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:99"}},
			{Layer: port.LayerVlan, Op: trace.OpRewrite, RuleID: "vlan-tag-form", Subject: trace.Subject{Kind: "port", Key: "1/1/4"}},
			{Layer: port.LayerRelay, Op: trace.OpTransmit, RuleID: "transmit", Subject: trace.Subject{Kind: "port", Key: "1/1/4"}},
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
			{Layer: port.LayerVlan, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "30"}},
			{Layer: port.LayerRelay, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan30"}},
			{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: "no-route", Subject: trace.Subject{Kind: "ip", Key: "10.0.88.7"}},
			{Layer: port.LayerRouting, Op: trace.OpDrop, RuleID: "no-route", Subject: trace.Subject{Kind: "vrf", Key: "tenant"}},
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
	sw := mustSwitch(t, cfg)
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
			{Layer: port.LayerVlan, Op: trace.OpClassify, RuleID: "vlan-classify", Subject: trace.Subject{Kind: "vlan", Key: "10"}},
			{Layer: port.LayerRelay, Op: trace.OpLearn, RuleID: "learn", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:11"}},
			{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "vlan10"}},
			{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.50.0/24"}},
			{Layer: port.LayerRouting, Op: trace.OpRewrite, RuleID: "decrement-ttl", Subject: trace.Subject{Kind: "interface", Key: "1/1/5"}},
			{Layer: port.LayerRouting, Op: trace.OpTransmit, RuleID: "routing.transmit", Subject: trace.Subject{Kind: "port", Key: "1/1/5"}},
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
			{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "1/1/5"}},
			{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"}},
			{Layer: port.LayerRouting, Op: trace.OpRewrite, RuleID: "decrement-ttl", Subject: trace.Subject{Kind: "interface", Key: "vlan20"}},
			{Layer: port.LayerRelay, Op: trace.OpLookup, RuleID: "unicast-hit", Subject: trace.Subject{Kind: "mac", Key: "00:11:22:33:44:77"}},
			{Layer: port.LayerVlan, Op: trace.OpRewrite, RuleID: "vlan-tag-form", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
			{Layer: port.LayerRelay, Op: trace.OpTransmit, RuleID: "transmit", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
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
			{Layer: port.LayerRouting, Op: trace.OpDrop, RuleID: "routing.not_bridged", Subject: trace.Subject{Kind: "port", Key: "1/1/5"}},
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
		if err := sw.SetOperStatus("1/1/5", port.Down); err != nil {
			t.Fatalf("SetOperStatus down: %v", err)
		}
		frame := ethernet.Frame{
			Src:       macPort5Neighbor,
			Dst:       baseMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   makeIPv4Packet(t, ipPort5Dst, ipH2, 64, []byte("down")),
		}
		res := sw.Forward(fixedTime, "1/1/5", frame)
		wantSteps := []trace.Step{
			{Layer: port.LayerRouting, Op: trace.OpDrop, RuleID: "port.status.down", Subject: trace.Subject{Kind: "port", Key: "1/1/5"}},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Dropped || res.Reason != port.ReasonPortDown {
			t.Errorf("got outcome=%v reason=%v, want Dropped/port-down", res.Outcome, res.Reason)
		}
		if err := sw.SetOperStatus("1/1/5", port.Up); err != nil {
			t.Fatalf("SetOperStatus up: %v", err)
		}
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
		rSw := mustSwitch(t, rCfg)
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
			{Layer: port.LayerRouting, Op: trace.OpClassify, RuleID: "classify", Subject: trace.Subject{Kind: "interface", Key: "1/1/1"}},
			{Layer: port.LayerRouting, Op: trace.OpLookup, RuleID: "connected", Subject: trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"}},
			{Layer: port.LayerRouting, Op: trace.OpRewrite, RuleID: "decrement-ttl", Subject: trace.Subject{Kind: "interface", Key: "1/1/2"}},
			{Layer: port.LayerRouting, Op: trace.OpTransmit, RuleID: "routing.transmit", Subject: trace.Subject{Kind: "port", Key: "1/1/2"}},
		}
		assertSteps(t, res.Steps, wantSteps)
		if res.Outcome != trace.Forwarded {
			t.Errorf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
		}
	})
}

// TestRoutedSubInterfaceForwardingAndTagMiss covers a firewall cabled to a trunk with no
// bridge at all: the port carries two routed sub-interfaces classified by outer VLAN tag,
// beside a plain untagged routed port and, in a second switch, a bridge switchport sharing
// one sub-interface's VID. Sub-interface egress carries the routed VLAN's tag with the
// ingress priority; a routed port with no interface at the arriving VID or TPID drops rather
// than falling through to a bridge that does not exist for it.
func TestRoutedSubInterfaceForwardingAndTagMiss(t *testing.T) {
	neighbor20 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x88}

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "eth1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "eth3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"eth1.10": {Port: "eth1", VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"eth1.20": {Port: "eth1", VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						"eth3":    {Port: "eth3", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "eth1.20", Addr: ipH2, MAC: neighbor20},
					},
				},
			},
		},
	}
	sw := mustSwitch(t, cfg)

	t.Run("frame tagged 10 leaves tagged 20 carrying the ingress priority", func(t *testing.T) {
		pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("sub-interface routing"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10, PCP: 3, DEI: true}},
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Forward(fixedTime, "eth1", frame)
		if res.Outcome != trace.Forwarded {
			t.Fatalf("outcome = %v, want %v; steps=%+v", res.Outcome, trace.Forwarded, res.Steps)
		}
		if len(res.Egress) != 1 || res.Egress[0].Port != "eth1" {
			t.Fatalf("egress = %+v, want one entry on eth1", res.Egress)
		}
		wantTag := vlan.Tag{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 20, PCP: 3, DEI: true}
		if gotTags := res.Egress[0].Frame.Tags; len(gotTags) != 1 || gotTags[0] != wantTag {
			t.Errorf("egress tags = %+v, want [%+v]", gotTags, wantTag)
		}
	})

	t.Run("frame tagged 30 on the sub-interface port drops as not-bridged", func(t *testing.T) {
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 30}},
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("no sub-interface at vid 30"),
		}
		res := sw.Forward(fixedTime, "eth1", frame)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonNotBridged {
			t.Fatalf("got outcome=%v reason=%v, want Dropped/not-bridged", res.Outcome, res.Reason)
		}
		if len(res.Steps) != 1 || res.Steps[0].RuleID != "routing.tag_miss" {
			t.Fatalf("steps = %+v, want a single routing.tag_miss drop", res.Steps)
		}
		wantFacts := []trace.Fact{routing.PortFact("eth1"), routing.VLANFact(30)}
		gotFacts := res.Steps[0].Inputs
		if !slices.EqualFunc(gotFacts, wantFacts, func(a, b trace.Fact) bool {
			return a.TypeID() == b.TypeID() && a.Canonical() == b.Canonical()
		}) {
			t.Errorf("fact inputs = %+v, want %+v", gotFacts, wantFacts)
		}
	})

	t.Run("untagged frame drops as not-bridged when no untagged sub-interface exists", func(t *testing.T) {
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("no untagged sub-interface here"),
		}
		res := sw.Forward(fixedTime, "eth1", frame)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonNotBridged {
			t.Fatalf("got outcome=%v reason=%v, want Dropped/not-bridged", res.Outcome, res.Reason)
		}
		if len(res.Steps) != 1 || res.Steps[0].RuleID != "routing.tag_miss" {
			t.Fatalf("steps = %+v, want a single routing.tag_miss drop", res.Steps)
		}
	})

	t.Run("S-tagged frame on the sub-interface port drops as not-bridged", func(t *testing.T) {
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeProviderBridging), VID: 10}},
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("s-tagged, no C-TAG sub-interface can match"),
		}
		res := sw.Forward(fixedTime, "eth1", frame)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonNotBridged {
			t.Fatalf("got outcome=%v reason=%v, want Dropped/not-bridged", res.Outcome, res.Reason)
		}
		if len(res.Steps) != 1 || res.Steps[0].RuleID != "routing.tag_protocol_miss" {
			t.Fatalf("steps = %+v, want a single routing.tag_protocol_miss drop", res.Steps)
		}
	})

	t.Run("tagged frame on a plain untagged routed port drops as not-bridged", func(t *testing.T) {
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}},
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("tagged on a plain routed port"),
		}
		res := sw.Forward(fixedTime, "eth3", frame)
		if res.Outcome != trace.Dropped || res.Reason != routing.ReasonNotBridged {
			t.Fatalf("got outcome=%v reason=%v, want Dropped/not-bridged", res.Outcome, res.Reason)
		}
		if len(res.Steps) != 1 || res.Steps[0].RuleID != "routing.tag_miss" {
			t.Fatalf("steps = %+v, want a single routing.tag_miss drop", res.Steps)
		}
	})

	t.Run("the plain untagged routed port still routes beside the sub-interface's port", func(t *testing.T) {
		pkt := makeIPv4Packet(t, netip.MustParseAddr("10.0.30.7"), ipH2, 64, []byte("plain routed port"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := sw.Forward(fixedTime, "eth3", frame)
		if res.Outcome != trace.Forwarded {
			t.Fatalf("outcome = %v, want %v; steps=%+v", res.Outcome, trace.Forwarded, res.Steps)
		}
		if len(res.Egress) != 1 || res.Egress[0].Port != "eth1" || len(res.Egress[0].Frame.Tags) != 1 {
			t.Fatalf("egress = %+v, want one tagged entry on eth1", res.Egress)
		}
	})

	t.Run("sub-interface egress refused for MTU still carries the tag on the egress record", func(t *testing.T) {
		// The egress tag is applied before the transmit and LAG checks, because a refused
		// egress record still embeds the frame those checks were given. Moving the tag
		// assignment below the transmit check would leave this refusal's egress record
		// untagged instead, and a comparison that matches egress records by tag equality
		// would not catch that silently.
		mtuPorts := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "ethA", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "ethB", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: 40}))

		mtuCfg := vswitch.Config{
			Ports: mtuPorts,
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"ethA":    {Port: "ethA", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.40.1/24")}},
							"ethB.10": {Port: "ethB", VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						},
						Neighbors: []routing.Neighbor{
							{Interface: "ethB.10", Addr: ipH2, MAC: macH2},
						},
					},
				},
			},
		}
		mtuSw := mustSwitch(t, mtuCfg)

		oversizedPayload := make([]byte, 50)
		pkt := makeIPv4Packet(t, netip.MustParseAddr("10.0.40.7"), ipH2, 64, oversizedPayload)
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		res := mtuSw.Forward(fixedTime, "ethA", frame)
		if res.Outcome != trace.Dropped || res.Reason != port.ReasonMTUExceeded {
			t.Fatalf("outcome=%v reason=%v, want Dropped/mtu-exceeded", res.Outcome, res.Reason)
		}
		if len(res.Egress) != 1 || res.Egress[0].Port != "ethB" || res.Egress[0].Dropped != port.ReasonMTUExceeded {
			t.Fatalf("egress = %+v, want one refused entry on ethB", res.Egress)
		}
		wantTag := vlan.Tag{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}
		if gotTags := res.Egress[0].Frame.Tags; len(gotTags) != 1 || gotTags[0] != wantTag {
			t.Errorf("egress tags = %+v, want [%+v] even though transmit refused", gotTags, wantTag)
		}
	})

	p10 := vlan.ID(10)
	bridgeSw := mustSwitch(t, vswitch.Config{
		Ports: mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "eth1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "swport", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table:       map[vlan.ID]string{10: "ten"},
				Switchports: map[string]bridge.Switchport{"swport": {PVID: &p10, Untagged: []vlan.ID{10}}},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"eth1.10": {Port: "eth1", VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
					},
				},
			},
		},
	})

	t.Run("a bridge VLAN sharing the sub-interface's VID still floods rather than routing to it", func(t *testing.T) {
		// If the bridge VLAN index answered VLAN 10 with the sub-interface (the regression
		// this pins), Owns would match the frame's router-MAC destination and it would leave
		// tagged out eth1 instead of taking the ordinary no-egress flood drop on swport alone.
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("stay on the bridge"),
		}
		res := bridgeSw.Forward(fixedTime, "swport", frame)
		if res.Outcome != trace.Dropped || res.Reason != bridge.ReasonNoEgress {
			t.Fatalf("got outcome=%v reason=%v, want Dropped/no-egress", res.Outcome, res.Reason)
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
	sw := mustSwitch(t, cfg)

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
	sw := mustSwitch(t, cfg)

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
	sw := mustSwitch(t, cfg)

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
	sw := mustSwitch(t, cfg)

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
	if !traceHasFactType(res.Steps, "lag.selection") {
		t.Errorf("routed LAG trace has no member selection fact: %+v", res.Steps)
	}
	if !traceHasFactType(res.Steps, "routing.lookup_decision") {
		t.Errorf("routed LAG trace has no route lookup fact: %+v", res.Steps)
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
	sw := mustSwitch(t, cfg)

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
	sw := mustSwitch(t, cfg)
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
	sw := mustSwitch(t, cfg)

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
		ch.From != vswitch.DeviceMACFact(cfgA.MAC) || ch.To != vswitch.DeviceMACFact(cfgB.MAC) {
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
			if c.From != routing.MACFact(macA) || c.To != routing.MACFact(macB) {
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

	nextSw, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: nextCfg})
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

// TestDeriveRebuildsRoutingWhenNeighborPolicyChanges covers R9: a
// NeighborPolicy field change is not read by [routing.Diff] without its own
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
	cur := buildBaseRoutingSwitch(t)

	dst := netip.MustParseAddr("10.0.20.77")
	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	pkt := makeIPv4Packet(t, ipH1, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}
	if held := cur.Forward(fixedTime, "1/1/1", frame); held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	cur.Forward(fixedTime.Add(time.Millisecond), "1/1/2", reply)
	cur.Wake(fixedTime.Add(time.Millisecond))
	cur.Drain()

	nextCfg := cur.Config()
	defaultVRF := nextCfg.Routing.VRFs["default"]
	defaultVRF.NeighborPolicy.ReachableTime = 60 * time.Second
	nextCfg.Routing.VRFs["default"] = defaultVRF

	next, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: nextCfg})
	if err != nil {
		t.Fatalf("Derive failed: %v", err)
	}

	res := next.Forward(fixedTime.Add(2*time.Millisecond), "1/1/1", frame)
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
	cur := buildBaseRoutingSwitch(t)

	dstA := netip.MustParseAddr("10.0.20.77")
	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	pktA := makeIPv4Packet(t, ipH1, dstA, 64, []byte("a"))
	frameA := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pktA}
	cur.Forward(fixedTime, "1/1/1", frameA)
	replyA := makeARPReply(t, dstA, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	cur.Forward(fixedTime.Add(time.Millisecond), "1/1/2", replyA)
	cur.Wake(fixedTime.Add(time.Millisecond))
	cur.Drain()

	dstB := netip.MustParseAddr("10.0.20.88")
	pktB := makeIPv4Packet(t, ipH1, dstB, 64, []byte("b"))
	frameB := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pktB}
	if held := cur.Forward(fixedTime.Add(2*time.Millisecond), "1/1/1", frameB); held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	next, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: cur.Config()})
	if err != nil {
		t.Fatalf("Derive failed: %v", err)
	}
	if !next.Retention().Routing.Kept {
		t.Errorf("Routing retention = %+v, want Kept: true", next.Retention().Routing)
	}

	resA := next.Forward(fixedTime.Add(3*time.Millisecond), "1/1/1", frameA)
	if resA.Outcome != trace.Flooded && resA.Outcome != trace.Forwarded {
		t.Fatalf("outcome for the retained neighbor = %v, want Forwarded or Flooded", resA.Outcome)
	}

	// Held frame survived Derive: resolve it via ARP reply and Wake
	learnedMACB := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x88}
	replyB := makeARPReply(t, dstB, netip.MustParseAddr("10.0.20.1"), learnedMACB, macRouter)
	next.Forward(fixedTime.Add(4*time.Millisecond), "1/1/2", replyB)
	next.Wake(fixedTime.Add(4 * time.Millisecond))
	if emissions := next.Drain(); len(emissions) == 0 {
		t.Errorf("emissions = 0, want released held frame")
	}
	if failures := next.DrainNeighborFailures(); len(failures) != 0 {
		t.Errorf("neighbor failures = %d, want 0", len(failures))
	}
}

func TestDeriveFailsHeldFramesOnRoutingKeyChange(t *testing.T) {
	cur := buildBaseRoutingSwitch(t)

	dstB := netip.MustParseAddr("10.0.20.88")
	pktB := makeIPv4Packet(t, ipH1, dstB, 64, []byte("b"))
	frameB := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pktB}
	if held := cur.Forward(fixedTime.Add(2*time.Millisecond), "1/1/1", frameB); held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	// Perturb NeighborPolicy.ResolutionTimeout to change routing retention key
	nextCfg := cur.Config()
	vrf := nextCfg.Routing.VRFs[routing.DefaultVRF]
	vrf.NeighborPolicy.ResolutionTimeout = 10 * time.Second
	nextCfg.Routing.VRFs[routing.DefaultVRF] = vrf

	next, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: nextCfg})
	if err != nil {
		t.Fatalf("Derive failed: %v", err)
	}
	if next.Retention().Routing.Kept {
		t.Errorf("Routing retention = %+v, want Kept: false", next.Retention().Routing)
	}
	// Held frame is reported as failed rather than silently lost
	if failures := next.DrainNeighborFailures(); len(failures) != 1 {
		t.Errorf("neighbor failures = %d, want 1", len(failures))
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
		sw := mustSwitch(t, cfg)

		mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		mustSwitchLearn(t, sw, []bridge.Seed{
			{FID: 0, MAC: mac, Port: "1/1/1", Lifetime: bridge.Static, LearnedAt: now},
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
		sw := mustSwitch(t, cfg)

		mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		err := sw.Learn([]bridge.Seed{
			{FID: 0, MAC: mac, Port: "1/1/1", Lifetime: bridge.Static, LearnedAt: now},
		})
		if err == nil {
			t.Fatal("Learn() on hub = nil error, want bridge-required error")
		}
		if got := errs.Attributes(err)["field"]; got != "seeds" {
			t.Errorf("field = %v, want %q", got, "seeds")
		}
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

	sw := mustSwitch(t, cfg)
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

	bpduFrame := mustEncode(t, bpdu, macRoot)
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

func TestLACPDUHandlingAtSwitch(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	macA := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0a}
	macB := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0b}
	p10 := vlan.ID(10)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		MAC:   macA,
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"lag1":  {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/3": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/4": {PVID: &p10, Untagged: []vlan.ID{10}},
				},
			},
		},
		LAG: &lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {
					LACP: lag.LACPConfig{
						Mode: lag.Active,
					},
				},
			},
		},
		Traffic: &traffic.Config{Mirrors: []traffic.Mirror{{Name: "control", SelectAll: true, OutputPort: "1/1/4"}}},
	}

	sw := mustSwitch(t, cfg)
	sw.Start(now)

	pdu := lacp.PDU{
		Actor: lacp.Info{
			SystemPriority: 32768,
			SystemID:       macB,
			Key:            1,
			PortPriority:   32768,
			PortID:         1,
			State:          lacp.StateActive | lacp.StateShortTimeout | lacp.StateAggregation,
		},
		Partner: lacp.Info{
			SystemPriority: 32768,
			SystemID:       macA,
			Key:            1,
			PortPriority:   32768,
			PortID:         1,
			State:          lacp.StateActive | lacp.StateAggregation,
		},
	}
	validFrame := lacp.Encode(pdu, macB)

	// Peek first: counts do not change
	peekRes := sw.Peek(now, "1/1/1", validFrame)
	if peekRes.Outcome != trace.Consumed {
		t.Errorf("peek outcome = %s, want Consumed", peekRes.Outcome)
	}
	if info := sw.MemberInfo("1/1/1"); info.LACPDUsRx != 0 {
		t.Errorf("after Peek, LACPDUsRx = %d, want 0", info.LACPDUsRx)
	}

	// Forward valid LACPDU on 1/1/1 (member port): Consumed with lag layer step and LACPDUsRx = 1
	res := sw.Forward(now, "1/1/1", validFrame)
	if res.Outcome != trace.Consumed {
		t.Errorf("res.Outcome = %s, want Consumed", res.Outcome)
	}
	if len(res.Steps) == 0 || res.Steps[0].Layer != port.LayerLag || res.Steps[0].Op != trace.OpClassify || res.Steps[0].RuleID != "lag.lacpdu.admit" {
		t.Errorf("res.Steps = %+v, want step with LayerLag, OpClassify, rule lag.lacpdu.admit", res.Steps)
	}
	if !traceHasFactType(res.Steps, "lag.lacp_decision") {
		t.Errorf("LACP trace has no member decision fact: %+v", res.Steps)
	}
	if info := sw.MemberInfo("1/1/1"); info.LACPDUsRx != 1 {
		t.Errorf("after Forward, LACPDUsRx = %d, want 1", info.LACPDUsRx)
	}
	if copies := sw.Copies(); len(copies) != 1 || copies[0].Mirror != "control" || copies[0].Port != "1/1/4" {
		t.Errorf("Copies() = %+v, want received LACPDU mirrored to 1/1/4", copies)
	}

	// Bad TLV length LACPDU: dropped unsupported-lacpdu and counts BadLACPDUs 1
	badPayload := make([]byte, 110)
	copy(badPayload, validFrame.Payload)
	badPayload[23] = 19 // bad partner TLV length (expected 20)
	badFrame := ethernet.Frame{
		Dst:       lacp.GroupAddress,
		Src:       macB,
		EtherType: ethernet.EtherTypeSlowProtocols,
		Payload:   badPayload,
	}

	// Peek bad frame: BadLACPDUs does not change
	peekBad := sw.Peek(now, "1/1/1", badFrame)
	if peekBad.Outcome != trace.Dropped || peekBad.Reason != lag.ReasonUnsupportedLACPDU {
		t.Errorf("peekBad outcome = %s, reason = %s, want Dropped, unsupported-lacpdu", peekBad.Outcome, peekBad.Reason)
	}
	if info := sw.MemberInfo("1/1/1"); info.BadLACPDUs != 0 {
		t.Errorf("after Peek bad, BadLACPDUs = %d, want 0", info.BadLACPDUs)
	}

	resBad := sw.Forward(now, "1/1/1", badFrame)
	if resBad.Outcome != trace.Dropped {
		t.Errorf("resBad.Outcome = %s, want Dropped", resBad.Outcome)
	}
	if resBad.Reason != lag.ReasonUnsupportedLACPDU {
		t.Errorf("resBad.Reason = %s, want %s", resBad.Reason, lag.ReasonUnsupportedLACPDU)
	}
	if info := sw.MemberInfo("1/1/1"); info.BadLACPDUs != 1 {
		t.Errorf("after Forward bad, BadLACPDUs = %d, want 1", info.BadLACPDUs)
	}

	// LACPDU injected at 1/1/3 (no LAG): dropped reserved-address
	resNonMember := sw.Forward(now, "1/1/3", validFrame)
	if resNonMember.Outcome != trace.Dropped {
		t.Errorf("resNonMember.Outcome = %s, want Dropped", resNonMember.Outcome)
	}
	if resNonMember.Reason != bridge.ReasonReservedAddress {
		t.Errorf("resNonMember.Reason = %s, want %s", resNonMember.Reason, bridge.ReasonReservedAddress)
	}
}

func TestDefaultLAGSelectionLowestMember(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	macHost1 := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x11}
	macHost2 := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x22}
	p10 := vlan.ID(10)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/3": {PVID: &p10, Untagged: []vlan.ID{10}},
					"lag1":  {PVID: &p10, Untagged: []vlan.ID{10}},
				},
			},
		},
	}

	sw := mustSwitch(t, cfg)

	frame := ethernet.Frame{
		Dst:       macHost2,
		Src:       macHost1,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("data"),
	}

	res := sw.Forward(now, "1/1/3", frame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("res.Outcome = %s, want Flooded", res.Outcome)
	}
	if len(res.Egress) != 1 {
		t.Fatalf("len(res.Egress) = %d, want 1", len(res.Egress))
	}
	if res.Egress[0].Port != "lag1" {
		t.Errorf("res.Egress[0].Port = %q, want lag1", res.Egress[0].Port)
	}
	if res.Egress[0].Member != "1/1/1" {
		t.Errorf("res.Egress[0].Member = %q, want 1/1/1 (lowest member)", res.Egress[0].Member)
	}
}

func TestMemberLinkDownMovesSelectionAndSTPPathCost(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	macBridge := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
	p10 := vlan.ID(10)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		MAC:   macBridge,
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"lag1": {PVID: &p10, Untagged: []vlan.ID{10}},
				},
			},
		},
		STP: &stp.Config{
			Priority: 32768,
			Ports: map[string]stp.Port{
				"lag1": {},
			},
		},
		Phy: &phy.Config{
			Ethernet: map[string]phy.Ethernet{
				"1/1/1": {
					SupportedSpeedsBPS: []uint64{10_000_000_000},
					Setting:            &phy.Setting{SpeedBPS: 10_000_000_000, Duplex: phy.Full},
				},
				"1/1/2": {
					SupportedSpeedsBPS: []uint64{1_000_000_000},
					Setting:            &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full},
				},
			},
		},
	}

	sw := mustSwitch(t, cfg)
	sw.Start(now)

	dummyFrame := ethernet.Frame{Dst: netaddr.MAC{1}, Src: netaddr.MAC{2}}
	mem, ok := sw.SelectMember(now, "lag1", dummyFrame, 10)
	if !ok || mem != "1/1/1" {
		t.Fatalf("SelectMember = (%q, %t), want (1/1/1, true)", mem, ok)
	}

	roles := sw.Roles()
	if roles["lag1"].PathCost != 2000 {
		t.Errorf("lag1 PathCost = %d, want 2000 (10 Gbps)", roles["lag1"].PathCost)
	}

	// Member 1/1/1 goes down through LinkChange
	sw.LinkChange(now, "1/1/1", port.Down, vswitch.PointToPointTrue, 10_000_000_000)

	mem, ok = sw.SelectMember(now, "lag1", dummyFrame, 10)
	if !ok || mem != "1/1/2" {
		t.Fatalf("after link down, SelectMember = (%q, %t), want (1/1/2, true)", mem, ok)
	}

	roles = sw.Roles()
	if roles["lag1"].PathCost != 20000 {
		t.Errorf("after link down, lag1 PathCost = %d, want 20000 (1 Gbps)", roles["lag1"].PathCost)
	}

	// Both members down: lag1 has no enabled member
	sw.LinkChange(now, "1/1/2", port.Down, vswitch.PointToPointTrue, 1_000_000_000)
	mem, ok = sw.SelectMember(now, "lag1", dummyFrame, 10)
	if ok {
		t.Fatalf("after all links down, SelectMember returned %q, want false", mem)
	}
	p, _ := sw.Ports().Port("lag1")
	if p.OperStatus != port.Down {
		t.Errorf("lag1 OperStatus = %v, want Down", p.OperStatus)
	}
}

// TestLagRowFollowsMemberRowsNotTheDelay is evidence that the LAG row reads
// Down the moment every member row is down, while a down delay still holds
// the layer's enablement.
func TestLagRowFollowsMemberRowsNotTheDelay(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	tbl := mustTable(t, b)
	sw := mustSwitch(t, vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		LAG:    &lag.Config{LAGs: map[string]lag.LAG{"lag1": {DownDelay: time.Second}}},
	})
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	sw.Start(t0)
	if err := sw.SetOperStatus("1/1/1", port.Down); err != nil {
		t.Fatalf("SetOperStatus: %v", err)
	}
	sw.LinkChange(t0.Add(time.Second), "1/1/1", port.Down, vswitch.PointToPointTrue, 1_000_000_000)
	p, _ := sw.Ports().Port("lag1")
	if p.OperStatus != port.Down {
		t.Fatalf("lag1 OperStatus = %v, want Down as soon as its member row is down", p.OperStatus)
	}
	if !sw.MemberInfo("1/1/1").LinkUp {
		t.Fatal("the layer dropped the member before its down delay")
	}
}

var (
	mcastHostMAC   = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	mcastRouterMAC = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0xfe}
	mcastGroupMAC  = netaddr.MAC{0x01, 0x00, 0x5e, 0x01, 0x01, 0x01}
	allHostsMAC    = netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0x01}
)

func mcastSwitchConfig(t *testing.T, floodUnregistered *bool) vswitch.Config {
	t.Helper()

	pvid := vlan.ID(10)
	ports := mustTable(t, port.NewBuilder().Range("1/1/%d", 1, 4, port.Port{
		Kind:        port.Physical,
		AdminStatus: port.Up,
		OperStatus:  port.Up,
	}))
	switchports := make(map[string]bridge.Switchport, 4)
	for i := 1; i <= 4; i++ {
		switchports[fmt.Sprintf("1/1/%d", i)] = bridge.Switchport{PVID: &pvid, Untagged: []vlan.ID{10}}
	}

	return vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table:       map[vlan.ID]string{10: "ten"},
			Switchports: switchports,
		}},
		Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
			10: {FloodUnregistered: floodUnregistered},
		}},
	}
}

func makeIGMPControlFrame(t *testing.T, src, dst netip.Addr, srcMAC, dstMAC netaddr.MAC, ttl uint8, message igmp.Message) ethernet.Frame {
	t.Helper()

	payload, err := igmp.Encode(message)
	if err != nil {
		t.Fatalf("encode IGMP: %v", err)
	}
	hdr := ip.Header{Src: src, Dst: dst, HopLimit: ttl, Protocol: 2, V4: &ip.V4{}}
	packet, err := hdr.Encode(payload)
	if err != nil {
		t.Fatalf("encode IPv4: %v", err)
	}

	return ethernet.Frame{Dst: dstMAC, Src: srcMAC, EtherType: ethernet.EtherTypeIPv4, Payload: packet}
}

func makeMLDControlFrame(t *testing.T, src, dst netip.Addr, srcMAC, dstMAC netaddr.MAC, hopLimit uint8, routerAlert bool, message mld.Message) ethernet.Frame {
	t.Helper()

	hdr := ip.Header{Src: src, Dst: dst, HopLimit: hopLimit, Protocol: 0, V6: &ip.V6{}}
	payload, err := mld.Encode(hdr, message)
	if err != nil {
		t.Fatalf("encode MLD: %v", err)
	}
	hopByHop := []byte{58, 0, 0, 0, 0, 0, 0, 0}
	if routerAlert {
		hopByHop = []byte{58, 0, 5, 2, 0, 0, 1, 0}
	}
	packet, err := hdr.Encode(append(hopByHop, payload...))
	if err != nil {
		t.Fatalf("encode IPv6: %v", err)
	}

	return ethernet.Frame{Dst: dstMAC, Src: srcMAC, EtherType: ethernet.EtherTypeIPv6, Payload: packet}
}

func replaceMLDHopByHop(t *testing.T, frame *ethernet.Frame, hopByHop []byte) {
	t.Helper()

	hdr, payload, err := ip.Decode(frame.Payload)
	if err != nil {
		t.Fatalf("decode IPv6: %v", err)
	}
	headerLength := (int(payload[1]) + 1) * 8
	packet, err := hdr.Encode(append(append([]byte(nil), hopByHop...), payload[headerLength:]...))
	if err != nil {
		t.Fatalf("encode IPv6: %v", err)
	}
	frame.Payload = packet
}

func setMLDType(t *testing.T, frame *ethernet.Frame, typ byte) {
	t.Helper()

	hdr, payload, err := ip.Decode(frame.Payload)
	if err != nil {
		t.Fatalf("decode IPv6: %v", err)
	}
	headerLength := (int(payload[1]) + 1) * 8
	suffix := payload[headerLength:]
	suffix[0] = typ
	suffix[2], suffix[3] = 0, 0
	binary.BigEndian.PutUint16(suffix[2:4], testICMPv6Checksum(hdr, suffix))
}

func testICMPv6Checksum(hdr ip.Header, payload []byte) uint16 {
	pseudo := make([]byte, 40+len(payload))
	src := hdr.Src.As16()
	dst := hdr.Dst.As16()
	copy(pseudo[0:16], src[:])
	copy(pseudo[16:32], dst[:])
	binary.BigEndian.PutUint32(pseudo[32:36], uint32(len(payload)))
	pseudo[39] = 58
	copy(pseudo[40:], payload)

	var sum uint32
	for len(pseudo) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(pseudo[:2]))
		pseudo = pseudo[2:]
	}
	if len(pseudo) == 1 {
		sum += uint32(pseudo[0]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

func mcastForwardedPorts(res vswitch.ForwardResult) []string {
	var ports []string
	for _, egress := range res.Egress {
		if egress.Dropped == "" {
			ports = append(ports, egress.Port)
		}
	}

	return ports
}

func TestIGMPControlForwardingAndLearning(t *testing.T) {
	group := netip.MustParseAddr("239.1.1.1")
	sw := mustSwitch(t, mcastSwitchConfig(t, nil))
	query := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.254"), netip.MustParseAddr("224.0.0.1"),
		mcastRouterMAC, allHostsMAC, 1,
		igmp.Message{Type: igmp.Query, Version: igmp.V2},
	)
	res := sw.Forward(fixedTime, "1/1/4", query)
	if got := mcastForwardedPorts(res); !slices.Equal(got, []string{"1/1/1", "1/1/2", "1/1/3"}) {
		t.Errorf("query ports = %v, want [1/1/1 1/1/2 1/1/3]", got)
	}
	if routers := sw.RouterPorts(10); len(routers) != 1 || routers[0].Port != "1/1/4" {
		t.Fatalf("RouterPorts(10) = %+v, want 1/1/4", routers)
	}

	report := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.1"), group,
		mcastHostMAC, mcastGroupMAC, 1,
		igmp.Message{Type: igmp.ReportV2, Group: group},
	)
	res = sw.Forward(fixedTime.Add(time.Second), "1/1/1", report)
	if got := mcastForwardedPorts(res); !slices.Equal(got, []string{"1/1/4"}) {
		t.Errorf("report ports = %v, want [1/1/4]", got)
	}
	if groups := sw.Groups(10); len(groups) != 1 || groups[0].Group != group || groups[0].Port != "1/1/1" {
		t.Errorf("Groups(10) = %+v, want 239.1.1.1 on 1/1/1", groups)
	}
	legacyGroup := netip.MustParseAddr("239.1.1.2")
	legacy := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.2"), legacyGroup,
		netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}, netaddr.MAC{0x01, 0x00, 0x5e, 0x01, 0x01, 0x02}, 1,
		igmp.Message{Type: igmp.ReportV1, Group: legacyGroup},
	)
	sw.Forward(fixedTime.Add(2*time.Second), "1/1/2", legacy)
	if groups := sw.Groups(10); len(groups) != 2 {
		t.Errorf("Groups(10) after IGMPv1 report = %+v, want two memberships", groups)
	}
	if entries := sw.Entries(); len(entries) != 3 {
		t.Errorf("Entries() = %+v, want the query and both report source MACs learned", entries)
	}

	withoutRouter := mustSwitch(t, mcastSwitchConfig(t, nil))
	res = withoutRouter.Forward(fixedTime, "1/1/1", report)
	if res.Outcome != trace.Dropped || res.Reason != mcast.ReasonNoRouterPort {
		t.Errorf("report before query = %s/%s, want Dropped/%s", res.Outcome, res.Reason, mcast.ReasonNoRouterPort)
	}
}

func TestMulticastControlTraceDescribesGroupRecordTransition(t *testing.T) {
	tests := []struct {
		name        string
		firstGroup  netip.Addr
		secondGroup netip.Addr
		frame       func(*testing.T, netip.Addr) ethernet.Frame
	}{
		{
			name:        "IGMPv3",
			firstGroup:  netip.MustParseAddr("239.1.1.1"),
			secondGroup: netip.MustParseAddr("239.1.1.2"),
			frame: func(t *testing.T, group netip.Addr) ethernet.Frame {
				return makeIGMPControlFrame(t,
					netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("224.0.0.22"),
					mcastHostMAC, netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0x16}, 1,
					igmp.Message{Type: igmp.ReportV3, Records: []igmp.GroupRecord{{
						Type: igmp.ChangeToExcludeMode, Group: group, Sources: []netip.Addr{netip.MustParseAddr("10.0.0.9")},
					}}},
				)
			},
		},
		{
			name:        "MLDv2",
			firstGroup:  netip.MustParseAddr("ff05::1"),
			secondGroup: netip.MustParseAddr("ff05::2"),
			frame: func(t *testing.T, group netip.Addr) ethernet.Frame {
				return makeMLDControlFrame(t,
					netip.MustParseAddr("fe80::1"), netip.MustParseAddr("ff02::16"),
					mcastHostMAC, netaddr.MAC{0x33, 0x33, 0x00, 0x00, 0x00, 0x16}, 1, true,
					mld.Message{Type: mld.ReportV2, Records: []mld.AddressRecord{{
						Type: mld.ChangeToExcludeMode, Group: group, Sources: []netip.Addr{netip.MustParseAddr("2001:db8::9")},
					}}},
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first := mustSwitch(t, mcastSwitchConfig(t, nil)).Forward(fixedTime, "1/1/1", test.frame(t, test.firstGroup))
			second := mustSwitch(t, mcastSwitchConfig(t, nil)).Forward(fixedTime, "1/1/1", test.frame(t, test.secondGroup))
			if first.Equal(second.Trace) {
				t.Fatal("equal-length reports for distinct groups produced equal traces")
			}

			found := false
			for _, step := range first.Steps {
				facts := append(slices.Clone(step.Inputs), step.Outputs...)
				for _, fact := range facts {
					if fact.TypeID() != "mcast.control_message" {
						continue
					}
					found = strings.Contains(fact.Canonical(), test.firstGroup.String()) &&
						strings.Contains(fact.Canonical(), "record_type=") &&
						strings.Contains(fact.Canonical(), "sources=[")
				}
			}
			if !found {
				t.Errorf("steps = %+v, want decoded multicast control transition for %s", first.Steps, test.firstGroup)
			}
		})
	}
}

func TestIGMPControlRejectsBadFramesWithoutState(t *testing.T) {
	group := netip.MustParseAddr("239.1.1.1")
	report := igmp.Message{Type: igmp.ReportV2, Group: group}

	tests := []struct {
		name  string
		frame func(*testing.T) ethernet.Frame
	}{
		{
			name: "bad checksum",
			frame: func(t *testing.T) ethernet.Frame {
				frame := makeIGMPControlFrame(t, netip.MustParseAddr("10.0.0.1"), group, mcastHostMAC, mcastGroupMAC, 1, report)
				frame.Payload[len(frame.Payload)-1] ^= 0xff
				return frame
			},
		},
		{
			name: "bad IPv4 header checksum",
			frame: func(t *testing.T) ethernet.Frame {
				frame := makeIGMPControlFrame(t, netip.MustParseAddr("10.0.0.1"), group, mcastHostMAC, mcastGroupMAC, 1, report)
				frame.Payload[10] ^= 0xff
				return frame
			},
		},
		{
			name: "EtherType and version mismatch",
			frame: func(t *testing.T) ethernet.Frame {
				frame := makeIGMPControlFrame(t, netip.MustParseAddr("10.0.0.1"), group, mcastHostMAC, mcastGroupMAC, 1, report)
				frame.Payload[0] = 6<<4 | frame.Payload[0]&0x0f
				return frame
			},
		},
		{
			name: "ttl two",
			frame: func(t *testing.T) ethernet.Frame {
				return makeIGMPControlFrame(t, netip.MustParseAddr("10.0.0.1"), group, mcastHostMAC, mcastGroupMAC, 2, report)
			},
		},
		{
			name: "unicast destination",
			frame: func(t *testing.T) ethernet.Frame {
				return makeIGMPControlFrame(t, netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.2"), mcastHostMAC, mcastGroupMAC, 1, report)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sw := mustSwitch(t, mcastSwitchConfig(t, nil))
			res := sw.Forward(fixedTime, "1/1/1", tt.frame(t))
			if res.Outcome != trace.Dropped || res.Reason != mcast.ReasonBadControl {
				t.Errorf("Forward = %s/%s, want Dropped/%s", res.Outcome, res.Reason, mcast.ReasonBadControl)
			}
			if len(sw.Entries()) != 0 || len(sw.Groups(10)) != 0 || len(sw.RouterPorts(10)) != 0 {
				t.Errorf("bad control changed state: entries=%+v groups=%+v routers=%+v", sw.Entries(), sw.Groups(10), sw.RouterPorts(10))
			}
		})
	}
}

func TestMalformedMLDCandidateDoesNotLearn(t *testing.T) {
	group := netip.MustParseAddr("ff05::1")
	frame := makeMLDControlFrame(t,
		netip.MustParseAddr("fe80::1"), group,
		mcastHostMAC, netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, 1, true,
		mld.Message{Type: mld.ReportV1, Group: group},
	)
	frame.Payload = frame.Payload[:ip.V6HeaderLen+1]

	sw := mustSwitch(t, mcastSwitchConfig(t, nil))
	res := sw.Forward(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Dropped || res.Reason != mcast.ReasonBadControl {
		t.Errorf("Forward = %s/%s, want Dropped/%s", res.Outcome, res.Reason, mcast.ReasonBadControl)
	}
	if len(sw.Entries()) != 0 || len(sw.Groups(10)) != 0 || len(sw.RouterPorts(10)) != 0 {
		t.Errorf("truncated MLD control changed state: entries=%+v groups=%+v routers=%+v", sw.Entries(), sw.Groups(10), sw.RouterPorts(10))
	}
}

func TestUnsupportedIGMPFloodsWithoutSnoopingMutation(t *testing.T) {
	group := netip.MustParseAddr("239.1.1.1")
	frame := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.1"), group,
		mcastHostMAC, mcastGroupMAC, 1,
		igmp.Message{Type: igmp.ReportV2, Group: group},
	)
	payload := frame.Payload[ip.V4HeaderLen:]
	payload[0] = 0x30
	payload[2], payload[3] = 0, 0
	var sum uint32
	for i := 0; i < len(payload); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(payload[i : i+2]))
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	binary.BigEndian.PutUint16(payload[2:4], ^uint16(sum))

	sw := mustSwitch(t, mcastSwitchConfig(t, nil))
	res := sw.Forward(fixedTime, "1/1/1", frame)
	if got := mcastForwardedPorts(res); !slices.Equal(got, []string{"1/1/2", "1/1/3", "1/1/4"}) {
		t.Errorf("unsupported IGMP ports = %v, want every other VLAN port", got)
	}
	if len(sw.Groups(10)) != 0 || len(sw.RouterPorts(10)) != 0 {
		t.Errorf("unsupported IGMP changed snooping state: groups=%+v routers=%+v", sw.Groups(10), sw.RouterPorts(10))
	}
	if entries := sw.Entries(); len(entries) != 1 || entries[0].MAC != mcastHostMAC {
		t.Errorf("Entries() = %+v, want ordinary MAC learning", entries)
	}
}

func TestUnsupportedMLDFloodsWithoutSnoopingMutation(t *testing.T) {
	group := netip.MustParseAddr("ff05::1")
	frame := makeMLDControlFrame(t,
		netip.MustParseAddr("fe80::1"), group,
		mcastHostMAC, netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, 1, true,
		mld.Message{Type: mld.ReportV1, Group: group},
	)
	setMLDType(t, &frame, 200)

	sw := mustSwitch(t, mcastSwitchConfig(t, new(false)))
	res := sw.Forward(fixedTime, "1/1/1", frame)
	if got := mcastForwardedPorts(res); !slices.Equal(got, []string{"1/1/2", "1/1/3", "1/1/4"}) {
		t.Errorf("unsupported MLD ports = %v, want every other VLAN port", got)
	}
	for _, egress := range res.Egress {
		got := egress.Frame
		if got.Dst != frame.Dst || got.Src != frame.Src || got.EtherType != frame.EtherType ||
			!slices.Equal(got.Tags, frame.Tags) || !slices.Equal(got.Payload, frame.Payload) {
			t.Errorf("unsupported MLD frame on %s = %+v, want unchanged %+v", egress.Port, got, frame)
		}
	}
	if len(sw.Groups(10)) != 0 || len(sw.RouterPorts(10)) != 0 {
		t.Errorf("unsupported MLD changed snooping state: groups=%+v routers=%+v", sw.Groups(10), sw.RouterPorts(10))
	}
	if entries := sw.Entries(); len(entries) != 1 || entries[0].MAC != mcastHostMAC {
		t.Errorf("Entries() = %+v, want ordinary MAC learning", entries)
	}
}

func TestMLDControlValidatesOuterHeader(t *testing.T) {
	group := netip.MustParseAddr("ff05::1")
	tests := []struct {
		name        string
		source      netip.Addr
		hopLimit    uint8
		routerAlert bool
	}{
		{name: "missing Router Alert", source: netip.MustParseAddr("fe80::1"), hopLimit: 1},
		{name: "hop limit two", source: netip.MustParseAddr("fe80::1"), hopLimit: 2, routerAlert: true},
		{name: "non-link-local source", source: netip.MustParseAddr("2001:db8::1"), hopLimit: 1, routerAlert: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame := makeMLDControlFrame(t,
				tt.source, group,
				mcastHostMAC, netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, tt.hopLimit, tt.routerAlert,
				mld.Message{Type: mld.ReportV1, Group: group},
			)
			sw := mustSwitch(t, mcastSwitchConfig(t, nil))
			res := sw.Forward(fixedTime, "1/1/1", frame)
			if res.Outcome != trace.Dropped || res.Reason != mcast.ReasonBadControl {
				t.Errorf("Forward = %s/%s, want Dropped/%s", res.Outcome, res.Reason, mcast.ReasonBadControl)
			}
			if len(sw.Entries()) != 0 || len(sw.Groups(10)) != 0 {
				t.Errorf("bad MLD changed state: entries=%+v groups=%+v", sw.Entries(), sw.Groups(10))
			}
		})
	}
}

func TestMLDRouterAlertValidation(t *testing.T) {
	group := netip.MustParseAddr("ff05::1")
	tests := []struct {
		name     string
		hopByHop []byte
	}{
		{name: "non-MLD value", hopByHop: []byte{58, 0, 5, 2, 0, 1, 1, 0}},
		{name: "duplicate", hopByHop: []byte{58, 1, 5, 2, 0, 0, 5, 2, 0, 0, 1, 0, 0, 0, 0, 0}},
		{name: "malformed trailing option", hopByHop: []byte{58, 0, 5, 2, 0, 0, 1, 4}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frame := makeMLDControlFrame(t,
				netip.MustParseAddr("fe80::1"), group,
				mcastHostMAC, netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, 1, true,
				mld.Message{Type: mld.ReportV1, Group: group},
			)
			replaceMLDHopByHop(t, &frame, tc.hopByHop)

			sw := mustSwitch(t, mcastSwitchConfig(t, nil))
			res := sw.Forward(fixedTime, "1/1/1", frame)
			if res.Outcome != trace.Dropped || res.Reason != mcast.ReasonBadControl {
				t.Errorf("Forward = %s/%s, want Dropped/%s", res.Outcome, res.Reason, mcast.ReasonBadControl)
			}
			if len(sw.Entries()) != 0 || len(sw.Groups(10)) != 0 || len(sw.RouterPorts(10)) != 0 {
				t.Errorf("invalid Router Alert changed state: entries=%+v groups=%+v routers=%+v", sw.Entries(), sw.Groups(10), sw.RouterPorts(10))
			}
		})
	}
}

func TestSnoopedControlPrecedesRoutedVLANOwnership(t *testing.T) {
	group := netip.MustParseAddr("239.1.1.1")
	cfg := mcastSwitchConfig(t, nil)
	cfg.Routing = &routing.Config{VRFs: map[string]routing.VRF{
		"default": {
			Interfaces: map[string]routing.Interface{
				"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.254/24")}},
			},
		},
	}}
	frame := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.1"), group,
		mcastHostMAC, macRouter, 2,
		igmp.Message{Type: igmp.ReportV2, Group: group},
	)

	sw := mustSwitch(t, cfg)
	res := sw.Forward(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Dropped || res.Reason != mcast.ReasonBadControl {
		t.Errorf("Forward = %s/%s, want Dropped/%s", res.Outcome, res.Reason, mcast.ReasonBadControl)
	}
	if len(sw.Entries()) != 0 || len(sw.Groups(10)) != 0 || len(sw.RouterPorts(10)) != 0 {
		t.Errorf("routed bad control changed state: entries=%+v groups=%+v routers=%+v", sw.Entries(), sw.Groups(10), sw.RouterPorts(10))
	}
}

func TestMulticastDataResolutionAndFloodExceptions(t *testing.T) {
	flood := false
	sw := mustSwitch(t, mcastSwitchConfig(t, &flood))
	group := netip.MustParseAddr("239.2.2.2")
	data := func(dst netip.Addr, dstMAC netaddr.MAC, etherType ethernet.EtherType) ethernet.Frame {
		frame := ethernet.Frame{Dst: dstMAC, Src: netaddr.MAC{0, 1, 2, 3, 4, 5}, EtherType: etherType}
		if etherType == ethernet.EtherTypeIPv4 {
			frame.Payload = makeIPv4Packet(t, netip.MustParseAddr("10.0.0.3"), dst, 32, []byte("data"))
		}
		return frame
	}
	ipv6Data := func(dst netip.Addr) ethernet.Frame {
		addr := dst.As16()
		return ethernet.Frame{
			Dst:       netaddr.MAC{0x33, 0x33, addr[12], addr[13], addr[14], addr[15]},
			Src:       mcastHostMAC,
			EtherType: ethernet.EtherTypeIPv6,
			Payload:   makeIPv6Packet(t, netip.MustParseAddr("2001:db8::3"), dst, 32, []byte("data")),
		}
	}

	res := sw.Forward(fixedTime, "1/1/3", data(group, netaddr.MAC{0x01, 0x00, 0x5e, 0x02, 0x02, 0x02}, ethernet.EtherTypeIPv4))
	if res.Reason != mcast.ReasonUnregistered {
		t.Errorf("unregistered group without router reason = %q, want %q", res.Reason, mcast.ReasonUnregistered)
	}
	if !traceHasFactType(res.Steps, "vswitch.mcast_membership") {
		t.Errorf("unregistered multicast trace has no membership decision: %+v", res.Steps)
	}

	query := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.254"), netip.MustParseAddr("224.0.0.1"),
		mcastRouterMAC, allHostsMAC, 1,
		igmp.Message{Type: igmp.Query, Version: igmp.V2},
	)
	sw.Forward(fixedTime, "1/1/4", query)
	res = sw.Forward(fixedTime, "1/1/3", data(group, netaddr.MAC{0x01, 0x00, 0x5e, 0x02, 0x02, 0x02}, ethernet.EtherTypeIPv4))
	if got := mcastForwardedPorts(res); !slices.Equal(got, []string{"1/1/4"}) {
		t.Errorf("unregistered group with router ports = %v, want [1/1/4]", got)
	}
	if !traceHasFactType(res.Steps, "vswitch.mcast_membership") {
		t.Errorf("router-port multicast trace has no membership decision: %+v", res.Steps)
	}

	for name, frame := range map[string]ethernet.Frame{
		"IPv4 link-local group": data(netip.MustParseAddr("224.0.0.5"), netaddr.MAC{0x01, 0x00, 0x5e, 0, 0, 5}, ethernet.EtherTypeIPv4),
		"IPv6 all nodes":        ipv6Data(netip.MustParseAddr("ff02::1")),
		"IPv6 scope zero":       ipv6Data(netip.MustParseAddr("ff00::1")),
		"IPv6 scope one":        ipv6Data(netip.MustParseAddr("ff01::1")),
		"limited broadcast":     data(netip.MustParseAddr("255.255.255.255"), netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, ethernet.EtherTypeIPv4),
		"non-IP group":          {Dst: netaddr.MAC{0x01, 0, 0, 0, 0, 1}, Src: mcastHostMAC, EtherType: ethernet.EtherTypeARP},
	} {
		t.Run(name, func(t *testing.T) {
			got := mcastForwardedPorts(sw.Forward(fixedTime, "1/1/3", frame))
			if !slices.Equal(got, []string{"1/1/1", "1/1/2", "1/1/4"}) {
				t.Errorf("forwarded ports = %v, want ordinary flood", got)
			}
		})
	}
}

func TestPeekLeavesMulticastAndMACTablesUntouched(t *testing.T) {
	group := netip.MustParseAddr("239.1.1.1")
	sw := mustSwitch(t, mcastSwitchConfig(t, nil))
	report := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.1"), group,
		mcastHostMAC, mcastGroupMAC, 1,
		igmp.Message{Type: igmp.ReportV2, Group: group},
	)
	sw.Peek(fixedTime, "1/1/1", report)
	if len(sw.Entries()) != 0 || len(sw.Groups(10)) != 0 || len(sw.RouterPorts(10)) != 0 {
		t.Errorf("Peek changed state: entries=%+v groups=%+v routers=%+v", sw.Entries(), sw.Groups(10), sw.RouterPorts(10))
	}
}

func TestMulticastControlRunsThroughTrafficFinishing(t *testing.T) {
	cfg := mcastSwitchConfig(t, nil)
	vlanCfg := cfg.Mcast.VLANs[10]
	vlanCfg.RouterPorts = []string{"1/1/3"}
	cfg.Mcast.VLANs[10] = vlanCfg
	cfg.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{{
		Name: "control", SelectSrcPorts: []string{"1/1/1"}, OutputPort: "1/1/3",
	}}}
	group := netip.MustParseAddr("239.1.1.1")
	report := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.1"), group,
		mcastHostMAC, mcastGroupMAC, 1,
		igmp.Message{Type: igmp.ReportV2, Group: group},
	)

	sw := mustSwitch(t, cfg)
	res := sw.Forward(fixedTime, "1/1/1", report)
	if res.Outcome != trace.Dropped || res.Reason != traffic.ReasonMirrorOutput {
		t.Errorf("control result = %s/%s, want Dropped/%s", res.Outcome, res.Reason, traffic.ReasonMirrorOutput)
	}
	if len(res.Egress) != 1 || res.Egress[0].Port != "1/1/3" || res.Egress[0].Dropped != traffic.ReasonMirrorOutput {
		t.Errorf("control egress = %+v, want mirror-output drop on 1/1/3", res.Egress)
	}
	if copies := sw.Copies(); len(copies) != 1 || copies[0].Mirror != "control" || copies[0].Port != "1/1/3" {
		t.Errorf("Copies() = %+v, want control mirror on 1/1/3", copies)
	}
}

func TestMulticastValidationAndDerivation(t *testing.T) {
	base := mcastSwitchConfig(t, nil)
	if caps := base.Capabilities(); !slices.Contains(caps, port.LayerMcast) {
		t.Errorf("Capabilities() = %v, want mcast", caps)
	}

	t.Run("requires VLAN-aware bridge", func(t *testing.T) {
		cfg := base.Clone()
		cfg.Bridge = &bridge.Config{}
		if err := cfg.Validate(); err == nil {
			t.Fatal("Validate accepted multicast without VLAN-aware bridge")
		}
	})

	t.Run("requires snooped VLAN in bridge table", func(t *testing.T) {
		cfg := base.Clone()
		cfg.Mcast.VLANs[20] = mcast.VLANSnooping{}
		if err := cfg.Validate(); err == nil {
			t.Fatal("Validate accepted a snooped VLAN absent from the bridge table")
		}
	})

	t.Run("refuses physical LAG member as router port", func(t *testing.T) {
		pvid := vlan.ID(10)
		cfg := base.Clone()
		cfg.Ports = mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}))
		cfg.Bridge.VLAN.Switchports = map[string]bridge.Switchport{"lag1": {PVID: &pvid, Untagged: []vlan.ID{10}}}
		cfg.Mcast.VLANs[10] = mcast.VLANSnooping{RouterPorts: []string{"1/1/1"}}
		if err := cfg.Validate(); err == nil {
			t.Fatal("Validate accepted a physical LAG member as a router port")
		}
	})

	t.Run("requires router port forwarding membership", func(t *testing.T) {
		cfg := base.Clone()
		delete(cfg.Bridge.VLAN.Switchports, "1/1/4")
		cfg.Mcast.VLANs[10] = mcast.VLANSnooping{RouterPorts: []string{"1/1/4"}}
		if err := cfg.Validate(); err == nil {
			t.Fatal("Validate accepted a router port outside the snooped VLAN")
		}
	})

	group := netip.MustParseAddr("239.1.1.1")
	cur := mustSwitch(t, base)
	cur.Forward(fixedTime, "1/1/1", makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.1"), group,
		mcastHostMAC, mcastGroupMAC, 1,
		igmp.Message{Type: igmp.ReportV2, Group: group},
	))

	kept, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: base.Clone()})
	if err != nil {
		t.Fatalf("Derive retaining multicast state: %v", err)
	}
	if groups := kept.Groups(10); len(groups) != 1 || groups[0].GroupExpires != fixedTime.Add(mcast.DefaultMembershipInterval) {
		t.Errorf("derived Groups(10) = %+v, want retained entry with original expiry", groups)
	}

	removed := base.Clone()
	delete(removed.Mcast.VLANs, 10)
	dropped, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: removed})
	if err != nil {
		t.Fatalf("Derive removing snooped VLAN: %v", err)
	}
	if groups := dropped.Groups(10); len(groups) != 0 {
		t.Errorf("derived Groups(10) = %+v, want none after snooping removal", groups)
	}

	query := makeIGMPControlFrame(t,
		netip.MustParseAddr("10.0.0.254"), netip.MustParseAddr("224.0.0.1"),
		mcastRouterMAC, allHostsMAC, 1,
		igmp.Message{Type: igmp.Query, Version: igmp.V2},
	)
	cur.Forward(fixedTime, "1/1/4", query)
	changed := base.Clone()
	changedMcast := changed.Mcast.VLANs[10]
	changedMcast.MembershipInterval = 30 * time.Second
	changedMcast.RouterPortInterval = 45 * time.Second
	changedMcast.RouterPorts = []string{"1/1/3"}
	changed.Mcast.VLANs[10] = changedMcast
	retained, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: changed})
	if err != nil {
		t.Fatalf("Derive changing multicast configuration: %v", err)
	}
	if groups := retained.Groups(10); len(groups) != 1 || groups[0].GroupExpires != fixedTime.Add(mcast.DefaultMembershipInterval) {
		t.Errorf("Groups(10) after configuration change = %+v, want retained original expiry", groups)
	}
	routers := retained.RouterPorts(10)
	if len(routers) != 2 || routers[0].Port != "1/1/3" || routers[0].Lifetime != mcast.Static ||
		routers[1].Port != "1/1/4" || routers[1].Lifetime == mcast.Static || routers[1].Expires != fixedTime.Add(mcast.DefaultMembershipInterval) {
		t.Errorf("RouterPorts(10) after configuration change = %+v, want new static 1/1/3 and retained learned 1/1/4", routers)
	}
}

// TestLinkChangeRecordsInvalidOperStatusAsFault proves LinkChange records an
// invalid operational state on the sticky Err() channel instead of panicking:
// a bogus state leaves the switch alive and Err() naming the port, a second
// bogus state does not displace the first, and a valid transition records
// nothing. The Err() assertions are the caller-side read the sticky field
// needs — a fault recorded and never read would otherwise go unnoticed.
func TestLinkChangeRecordsInvalidOperStatusAsFault(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	sw := mustSwitch(t, vswitch.Config{Ports: ports, Bridge: &bridge.Config{}})

	sw.LinkChange(fixedTime, "1/1/1", port.LinkState("bogus"), vswitch.PointToPointTrue, 1_000_000_000)

	first := sw.Err()
	if first == nil {
		t.Fatal("Err() = nil after an invalid oper status, want a fault")
	}
	if got := errs.Attributes(first)["name"]; got != "1/1/1" {
		t.Errorf("fault names port %v, want 1/1/1", got)
	}

	sw.LinkChange(fixedTime.Add(time.Second), "1/1/2", port.LinkState("worse"), vswitch.PointToPointTrue, 1_000_000_000)
	if sw.Err() != first {
		t.Errorf("Err() = %v after a second fault, want the first %v", sw.Err(), first)
	}
}

// TestLinkChangeValidTransitionRecordsNoFault confirms a well-formed link
// transition leaves Err() nil, so the fault channel reports only real faults.
func TestLinkChangeValidTransitionRecordsNoFault(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	sw := mustSwitch(t, vswitch.Config{Ports: ports, Bridge: &bridge.Config{}})

	sw.LinkChange(fixedTime, "1/1/1", port.Down, vswitch.PointToPointTrue, 1_000_000_000)

	if err := sw.Err(); err != nil {
		t.Errorf("Err() = %v after a valid transition, want nil", err)
	}
}

// balancedLAGConfig builds a switch with one flat (untagged, VLAN-unaware)
// ingress port "in" and a two-member BalanceSLB lag1, so a flooded frame
// always exercises LAG member selection.
func balancedLAGConfig(t *testing.T, rebalanceInterval *time.Duration) vswitch.Config {
	t.Helper()

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "member-a", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "member-b", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}))

	return vswitch.Config{
		Ports:  ports,
		Bridge: &bridge.Config{},
		LAG: &lag.Config{LAGs: map[string]lag.LAG{
			"lag1": {
				Mode:              lag.BalanceSLB,
				Members:           map[string]lag.Member{"member-a": {}, "member-b": {}},
				RebalanceInterval: rebalanceInterval,
			},
		}},
	}
}

func hasIssueCode(issues []analysis.Issue, code analysis.IssueCode) bool {
	return slices.ContainsFunc(issues, func(issue analysis.Issue) bool { return issue.Code == code })
}

// TestForwardCommitsLAGSelectionPeekDoesNot is evidence for this change:
// Peek must not commit a bucket assignment or rotate the enabled list, so a
// Forward call that follows two Peeks of the same frame reaches the same
// selector state a lone Forward on a fresh switch would.
func TestForwardCommitsLAGSelectionPeekDoesNot(t *testing.T) {
	cfg := balancedLAGConfig(t, nil)
	frame := ethernet.Frame{Dst: netaddr.MAC{0x02, 0, 0, 0, 0, 9}, Src: netaddr.MAC{0x02, 0, 0, 0, 0, 1}}

	peeked := mustSwitch(t, cfg)
	peeked.Peek(fixedTime, "in", frame)
	peeked.Peek(fixedTime, "in", frame)
	peekedResult := peeked.Forward(fixedTime, "in", frame)

	fresh := mustSwitch(t, cfg)
	freshResult := fresh.Forward(fixedTime, "in", frame)

	if !reflect.DeepEqual(peekedResult.Result, freshResult.Result) {
		t.Errorf("Peek committed LAG selection state:\npeeked = %+v\nfresh  = %+v", peekedResult.Result, freshResult.Result)
	}
}

// TestForwardRaisesLAGRebalanceUnmodeled is evidence for this change: a
// balanced selection whose bucket was assigned at least one rebalance
// interval ago carries lag-rebalance-unmodeled, and a zero RebalanceInterval
// disables the signal entirely.
func TestForwardRaisesLAGRebalanceUnmodeled(t *testing.T) {
	frame := ethernet.Frame{Dst: netaddr.MAC{0x02, 0, 0, 0, 0, 9}, Src: netaddr.MAC{0x02, 0, 0, 0, 0, 1}}
	t0 := fixedTime
	ten := 10 * time.Second

	sw := mustSwitch(t, balancedLAGConfig(t, &ten))
	sw.Forward(t0, "in", frame)

	at9 := sw.Forward(t0.Add(9*time.Second), "in", frame)
	if at9.Metadata.Status() != analysis.Complete {
		t.Errorf("status at t0+9s = %v, want Complete: issues=%+v", at9.Metadata.Status(), at9.Metadata.Issues())
	}

	at10 := sw.Forward(t0.Add(10*time.Second), "in", frame)
	if at10.Metadata.Status() != analysis.Incomplete {
		t.Errorf("status at t0+10s = %v, want Incomplete", at10.Metadata.Status())
	}
	if !hasIssueCode(at10.Metadata.Issues(), vswitch.IssueLAGRebalanceUnmodeled) {
		t.Errorf("issues at t0+10s = %+v, want lag-rebalance-unmodeled", at10.Metadata.Issues())
	}

	zero := time.Duration(0)
	swDisabled := mustSwitch(t, balancedLAGConfig(t, &zero))
	swDisabled.Forward(t0, "in", frame)
	for _, at := range []time.Time{t0.Add(9 * time.Second), t0.Add(10 * time.Second)} {
		res := swDisabled.Forward(at, "in", frame)
		if res.Metadata.Status() != analysis.Complete {
			t.Errorf("RebalanceInterval=0 at %v: status = %v, want Complete", at, res.Metadata.Status())
		}
	}
}

// TestRebalanceIssueScopedToJourneysThroughBalancedLAG is evidence that the
// lag-rebalance-unmodeled issue names only the journeys that actually used
// the balanced LAG, not every journey a switch with a stale bucket forwards.
func TestRebalanceIssueScopedToJourneysThroughBalancedLAG(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "member-a", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "member-b", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}))
	ten := 10 * time.Second
	cfg := vswitch.Config{
		Ports:  ports,
		Bridge: &bridge.Config{},
		LAG: &lag.Config{LAGs: map[string]lag.LAG{
			"lag1": {
				Mode:              lag.BalanceSLB,
				Members:           map[string]lag.Member{"member-a": {}, "member-b": {}},
				RebalanceInterval: &ten,
			},
		}},
	}
	sw := mustSwitch(t, cfg)

	lagDst := netaddr.MAC{0x02, 0, 0, 0, 0, 9}
	outDst := netaddr.MAC{0x02, 0, 0, 0, 0, 8}
	mustSwitchLearn(t, sw, []bridge.Seed{
		{MAC: lagDst, Port: "lag1", Lifetime: bridge.Static},
		{MAC: outDst, Port: "out", Lifetime: bridge.Static},
	})

	lagFrame := ethernet.Frame{Dst: lagDst, Src: netaddr.MAC{0x02, 0, 0, 0, 0, 1}}
	outFrame := ethernet.Frame{Dst: outDst, Src: netaddr.MAC{0x02, 0, 0, 0, 0, 2}}

	t0 := fixedTime
	sw.Forward(t0, "in", lagFrame)

	lagRes := sw.Forward(t0.Add(10*time.Second), "in", lagFrame)
	if !hasIssueCode(lagRes.Metadata.Issues(), vswitch.IssueLAGRebalanceUnmodeled) {
		t.Fatalf("journey through lag1 issues = %+v, want lag-rebalance-unmodeled", lagRes.Metadata.Issues())
	}

	outRes := sw.Forward(t0.Add(10*time.Second), "in", outFrame)
	if hasIssueCode(outRes.Metadata.Issues(), vswitch.IssueLAGRebalanceUnmodeled) {
		t.Errorf("unrelated journey through out issues = %+v, want no lag-rebalance-unmodeled", outRes.Metadata.Issues())
	}
}

// TestObservationReleaseDoesNotChargeLAGRebalanceToObservingFrame is
// evidence for this change: a released held frame that egresses a balanced
// LAG with a stale bucket must not attribute lag-rebalance-unmodeled to the
// unrelated frame whose observation triggered the release. The switch holds
// a frame for a neighbor reachable only over a LAG-backed VLAN interface,
// with that LAG's bucket already stale; an ARP reply resolving the neighbor
// arrives on a plain access port that never touches the LAG at all, and its
// own result must carry no lag-rebalance-unmodeled issue even though
// releasing the held frame raises the same condition on lag1 itself.
func TestObservationReleaseDoesNotChargeLAGRebalanceToObservingFrame(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "member-a", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "member-b", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}))

	p10 := vlan.ID(10)
	p20 := vlan.ID(20)
	ten := 10 * time.Second
	staticMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x09}
	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}

	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
					"lag1":  {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			},
		},
		LAG: &lag.Config{LAGs: map[string]lag.LAG{
			"lag1": {
				Mode:              lag.BalanceSLB,
				Members:           map[string]lag.Member{"member-a": {}, "member-b": {}},
				RebalanceInterval: &ten,
			},
		}},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: netip.MustParseAddr("10.0.20.9"), MAC: staticMAC},
					},
				},
			},
		},
	}
	sw := mustSwitch(t, cfg)

	mustSwitchLearn(t, sw, []bridge.Seed{
		{FID: 20, MAC: staticMAC, Port: "lag1", Lifetime: bridge.Static},
		{FID: 20, MAC: learnedMAC, Port: "lag1", Lifetime: bridge.Static},
	})

	t0 := fixedTime

	// hashSLB keys off the frame's own Src, which routing rewrites to the
	// router's MAC on every vlan20 egress, and the VLAN ID — so any routed
	// frame out vlan20 over lag1 lands in the same bucket regardless of
	// destination. This commits it at t0, using the pre-configured (already
	// Reachable) static neighbor so nothing holds.
	staticPkt := makeIPv4Packet(t, ipH1, netip.MustParseAddr("10.0.20.9"), 64, []byte("warm"))
	staticFrame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: staticPkt}
	warm := sw.Forward(t0, "1/1/1", staticFrame)
	if warm.Outcome != trace.Forwarded && warm.Outcome != trace.Flooded {
		t.Fatalf("outcome = %v, want Forwarded or Flooded", warm.Outcome)
	}
	if hasIssueCode(warm.Metadata.Issues(), vswitch.IssueLAGRebalanceUnmodeled) {
		t.Fatalf("issues at t0 = %+v, want none yet: this is the bucket's first assignment", warm.Metadata.Issues())
	}

	// Hold a frame for an unresolved neighbor well past the rebalance
	// interval, so the bucket the release later reuses is stale.
	dst := netip.MustParseAddr("10.0.20.77")
	pkt := makeIPv4Packet(t, ipH1, dst, 64, []byte("hold"))
	held := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}
	t1 := t0.Add(11 * time.Second)
	if res := sw.Forward(t1, "1/1/1", held); res.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", res.Outcome)
	}

	// The observing frame is an ARP reply arriving on a plain access port,
	// never crossing lag1 itself.
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	observing := sw.Forward(t1.Add(time.Millisecond), "1/1/2", reply)

	if hasIssueCode(observing.Metadata.Issues(), vswitch.IssueLAGRebalanceUnmodeled) {
		t.Errorf("observing ARP reply's own result issues = %+v, want no lag-rebalance-unmodeled: it never traversed lag1", observing.Metadata.Issues())
	}

	emissions := sw.Drain()
	if len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1: the held frame must have released over lag1", len(emissions))
	}
}

// TestMulticastLeaveTimingThroughForward is evidence for this change: a
// non-fast leave keeps forwarding for LMQT after an observed group-specific
// query, for the full membership interval and mcast-query-unobserved without
// one, and Complete regardless when the VLAN has no router port.
func TestMulticastLeaveTimingThroughForward(t *testing.T) {
	group := netip.MustParseAddr("239.6.6.6")
	groupMAC := netaddr.MAC{0x01, 0x00, 0x5e, 0x06, 0x06, 0x06}
	allRoutersMAC := netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0x02}

	build := func(t *testing.T, withRouter bool) *vswitch.Switch {
		t.Helper()
		pvid := vlan.ID(10)
		ports := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "p1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "p9", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
		snooping := mcast.VLANSnooping{LastMemberQueryInterval: time.Second, LastMemberQueryCount: 2}
		if withRouter {
			snooping.RouterPorts = []string{"p9"}
		}
		return mustSwitch(t, vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "ten"},
				Switchports: map[string]bridge.Switchport{
					"p1": {PVID: &pvid, Untagged: []vlan.ID{10}},
					"p9": {PVID: &pvid, Untagged: []vlan.ID{10}},
				},
			}},
			Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: snooping}},
		})
	}

	dataFrame := func(t *testing.T, source netip.Addr) ethernet.Frame {
		t.Helper()

		return ethernet.Frame{
			Dst:       groupMAC,
			Src:       mcastRouterMAC,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   makeIPv4Packet(t, source, group, 32, []byte("data")),
		}
	}

	t0 := fixedTime
	leaveAt := t0.Add(10 * time.Second)
	other := netip.MustParseAddr("10.9.9.9")

	join := func(t *testing.T, sw *vswitch.Switch) {
		sw.Forward(t0, "p1", makeIGMPControlFrame(t,
			netip.MustParseAddr("10.0.0.1"), group, mcastHostMAC, groupMAC, 1,
			igmp.Message{Type: igmp.ReportV2, Group: group}))
	}
	leave := func(t *testing.T, sw *vswitch.Switch) {
		sw.Forward(leaveAt, "p1", makeIGMPControlFrame(t,
			netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("224.0.0.2"), mcastHostMAC, allRoutersMAC, 1,
			igmp.Message{Type: igmp.Leave, Group: group}))
	}

	t.Run("observed query shortens the leave to LMQT", func(t *testing.T) {
		sw := build(t, true)
		join(t, sw)
		leave(t, sw)
		sw.Forward(leaveAt, "p9", makeIGMPControlFrame(t,
			netip.MustParseAddr("10.0.0.254"), group, mcastRouterMAC, groupMAC, 1,
			igmp.Message{Type: igmp.Query, Version: igmp.V2, Group: group}))

		res1 := sw.Forward(leaveAt.Add(time.Second), "p9", dataFrame(t, other))
		if !slices.Contains(mcastForwardedPorts(res1), "p1") {
			t.Errorf("egress at t+1s = false, want true")
		}
		res2 := sw.Forward(leaveAt.Add(2*time.Second), "p9", dataFrame(t, other))
		if slices.Contains(mcastForwardedPorts(res2), "p1") {
			t.Errorf("egress at t+2s = true, want false")
		}
	})

	t.Run("without a query the leave runs the full interval and flags the gap", func(t *testing.T) {
		sw := build(t, true)
		join(t, sw)
		leave(t, sw)

		res := sw.Forward(leaveAt.Add(3*time.Second), "p9", dataFrame(t, other))
		if !slices.Contains(mcastForwardedPorts(res), "p1") {
			t.Errorf("egress at t+3s = false, want true")
		}
		if !hasIssueCode(res.Metadata.Issues(), vswitch.IssueMcastQueryUnobserved) {
			t.Errorf("issues at t+3s = %+v, want mcast-query-unobserved", res.Metadata.Issues())
		}
		if res.Metadata.Status() != analysis.Incomplete {
			t.Errorf("status at t+3s = %v, want Incomplete", res.Metadata.Status())
		}
	})

	t.Run("with no router port nothing is flagged", func(t *testing.T) {
		sw := build(t, false)
		join(t, sw)
		leave(t, sw)

		res := sw.Forward(leaveAt.Add(3*time.Second), "p9", dataFrame(t, other))
		if !slices.Contains(mcastForwardedPorts(res), "p1") {
			t.Errorf("egress at t+3s = false, want true")
		}
		if hasIssueCode(res.Metadata.Issues(), vswitch.IssueMcastQueryUnobserved) {
			t.Errorf("issues at t+3s = %+v, want no mcast-query-unobserved", res.Metadata.Issues())
		}
		if res.Metadata.Status() != analysis.Complete {
			t.Errorf("status at t+3s = %v, want Complete", res.Metadata.Status())
		}
	})
}

// routeLookupFact returns the canonical route-lookup fact carried by a forwarding
// result, which is where the equal-cost candidate set reaches a journey.
func routeLookupFact(t *testing.T, res vswitch.ForwardResult) string {
	t.Helper()
	for _, step := range res.Steps {
		if step.Layer != port.LayerRouting || step.Op != trace.OpLookup {
			continue
		}
		for _, f := range step.Outputs {
			if f.TypeID() == "routing.lookup_decision" && strings.Contains(f.Canonical(), ";matched=true;") {
				return f.Canonical()
			}
		}
	}
	t.Fatalf("no matched route lookup fact in steps: %+v", res.Steps)
	return ""
}

// routeCandidates splits the candidate list out of a route-lookup fact, each member
// rendered as "configured next hop|interface|forwarding next hop".
func routeCandidates(t *testing.T, fact string) (candidates []string, chosen int) {
	t.Helper()
	open := strings.Index(fact, ";candidates=[")
	if open < 0 || !strings.HasSuffix(fact, "]") {
		t.Fatalf("route fact carries no candidate set: %s", fact)
	}
	list := fact[open+len(";candidates=[") : len(fact)-1]
	if list != "" {
		candidates = strings.Split(list, ",")
	}

	chosenAt := strings.Index(fact, ";chosen=")
	if chosenAt < 0 {
		t.Fatalf("route fact names no chosen candidate: %s", fact)
	}
	rest := fact[chosenAt+len(";chosen="):]
	if end := strings.Index(rest, ";"); end >= 0 {
		rest = rest[:end]
	}
	chosen, err := strconv.Atoi(rest)
	if err != nil {
		t.Fatalf("chosen index %q: %v", rest, err)
	}
	if chosen < 0 || chosen >= len(candidates) {
		t.Fatalf("chosen index %d out of range for %v", chosen, candidates)
	}

	return candidates, chosen
}

func TestRoutedEgressNamesTheEqualCostCandidateSet(t *testing.T) {
	macH3 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x88}
	nextHopA := netip.MustParseAddr("10.0.20.7")
	nextHopB := netip.MustParseAddr("10.0.30.7")
	dst := netip.MustParseAddr("10.0.99.5")

	routes := []routing.Route{
		{Prefix: netip.MustParsePrefix("10.0.99.0/24"), NextHop: nextHopA, Preference: 1, Metric: 10},
		{Prefix: netip.MustParsePrefix("10.0.99.0/24"), NextHop: nextHopB, Preference: 1, Metric: 10},
	}
	candidateSet := func(ifaceA, ifaceB string) []string {
		return []string{
			nextHopA.String() + "|" + ifaceA + "|" + nextHopA.String(),
			nextHopB.String() + "|" + ifaceB + "|" + nextHopB.String(),
		}
	}

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   makeIPv4Packet(t, ipH1, dst, 64, []byte("ecmp")),
	}

	t.Run("routed port", func(t *testing.T) {
		sw := mustSwitch(t, vswitch.Config{
			Ports: ports,
			Routing: &routing.Config{VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"1/1/1": {Port: "1/1/1", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"1/1/2": {Port: "1/1/2", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						"1/1/3": {Port: "1/1/3", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
					},
					Routes: routes,
					Neighbors: []routing.Neighbor{
						{Interface: "1/1/2", Addr: nextHopA, MAC: macH2},
						{Interface: "1/1/3", Addr: nextHopB, MAC: macH3},
					},
				},
			}},
		})

		res := sw.Forward(fixedTime, "1/1/1", frame)
		if res.Outcome != trace.Forwarded {
			t.Fatalf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
		}

		got, chosen := routeCandidates(t, routeLookupFact(t, res))
		if want := candidateSet("1/1/2", "1/1/3"); !slices.Equal(got, want) {
			t.Fatalf("candidates = %v, want %v", got, want)
		}

		wantPort := []string{"1/1/2", "1/1/3"}[chosen]
		wantMAC := []netaddr.MAC{macH2, macH3}[chosen]
		if len(res.Egress) != 1 || res.Egress[0].Port != wantPort {
			t.Fatalf("egress = %+v, want one on %s", res.Egress, wantPort)
		}
		if res.Egress[0].Frame.Dst != wantMAC {
			t.Errorf("egress destination MAC = %s, want %s for candidate %d", res.Egress[0].Frame.Dst, wantMAC, chosen)
		}
	})

	t.Run("VLAN interface", func(t *testing.T) {
		p10, p20, p30 := vlan.ID(10), vlan.ID(20), vlan.ID(30)
		sw := mustSwitch(t, vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20", 30: "vlan30"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
					"1/1/3": {PVID: &p30, Untagged: []vlan.ID{30}},
				},
			}},
			Routing: &routing.Config{VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						"vlan30": {VLAN: 30, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
					},
					Routes: routes,
					Neighbors: []routing.Neighbor{
						{Interface: "vlan20", Addr: nextHopA, MAC: macH2},
						{Interface: "vlan30", Addr: nextHopB, MAC: macH3},
					},
				},
			}},
		})
		mustSwitchLearn(t, sw, []bridge.Seed{
			{FID: 20, MAC: macH2, Port: "1/1/2", Lifetime: bridge.Static},
			{FID: 30, MAC: macH3, Port: "1/1/3", Lifetime: bridge.Static},
		})

		res := sw.Forward(fixedTime, "1/1/1", frame)
		if res.Outcome != trace.Forwarded {
			t.Fatalf("outcome = %v, want %v", res.Outcome, trace.Forwarded)
		}

		got, chosen := routeCandidates(t, routeLookupFact(t, res))
		if want := candidateSet("vlan20", "vlan30"); !slices.Equal(got, want) {
			t.Fatalf("candidates = %v, want %v", got, want)
		}

		wantFID := []vlan.ID{20, 30}[chosen]
		wantPort := []string{"1/1/2", "1/1/3"}[chosen]
		wantMAC := []netaddr.MAC{macH2, macH3}[chosen]
		if res.FID != wantFID {
			t.Errorf("FID = %d, want %d for candidate %d", res.FID, wantFID, chosen)
		}
		if len(res.Egress) != 1 || res.Egress[0].Port != wantPort {
			t.Fatalf("egress = %+v, want one on %s", res.Egress, wantPort)
		}
		if res.Egress[0].Frame.Dst != wantMAC {
			t.Errorf("egress destination MAC = %s, want %s for candidate %d", res.Egress[0].Frame.Dst, wantMAC, chosen)
		}
	})
}

// switchCable names a link between two switches in a convergence test.
type switchCable struct {
	swA, swB     int
	portA, portB string
}

// convergeSwitches starts every switch, then alternates delivering the BPDU
// frames each emits to its cabled peer and advancing every switch's earliest
// NextWake, until two consecutive rounds report the same snapshot with an
// empty delivery queue. snapshot renders whatever a test needs to see
// stabilize; the scheduler does not look inside it. It mirrors stp's own
// convergeLayers, driving the same state machine one layer further out
// through vswitch.Switch's BPDU framing and port table.
func convergeSwitches(t *testing.T, start time.Time, switches []*vswitch.Switch, cables []switchCable, snapshot func() string) time.Time {
	t.Helper()

	now := start

	findPeer := func(srcSw int, srcPort string) (int, string, bool) {
		for _, c := range cables {
			if c.swA == srcSw && c.portA == srcPort {
				return c.swB, c.portB, true
			}
			if c.swB == srcSw && c.portB == srcPort {
				return c.swA, c.portA, true
			}
		}
		return 0, "", false
	}

	type packet struct {
		targetSw   int
		targetPort string
		frame      ethernet.Frame
	}

	var queue []packet
	drain := func(srcSw int) {
		for _, em := range switches[srcSw].Drain() {
			if peerSw, peerPort, ok := findPeer(srcSw, em.Port); ok {
				queue = append(queue, packet{targetSw: peerSw, targetPort: peerPort, frame: em.Frame})
			}
		}
	}

	for i, sw := range switches {
		sw.Start(now)
		drain(i)
	}

	var prevSnapshot string
	stableRounds := 0

	for round := 0; round < 400; round++ {
		current := snapshot()
		if current == prevSnapshot {
			stableRounds++
			if stableRounds >= 2 && len(queue) == 0 {
				break
			}
		} else {
			stableRounds = 0
			prevSnapshot = current
		}

		if len(queue) > 0 {
			batch := queue
			queue = nil
			for _, pkt := range batch {
				switches[pkt.targetSw].Forward(now, pkt.targetPort, pkt.frame)
				drain(pkt.targetSw)
			}

			continue
		}

		var earliestWake time.Time
		hasWake := false
		for _, sw := range switches {
			if w, ok := sw.NextWake(); ok {
				if !hasWake || w.Before(earliestWake) {
					earliestWake = w
					hasWake = true
				}
			}
		}
		if !hasWake {
			break
		}

		now = earliestWake
		for i, sw := range switches {
			sw.Wake(now)
			drain(i)
		}
	}

	return now
}

// TestMSTITopologyChangeFlushesOnlyItsOwnVLAN is end-to-end evidence for this
// unit: a topology change confined to one MST instance flushes, on the
// switch's other ports, only the FIDs of the VLANs that instance carries,
// leaving a different VLAN's entries on the same port untouched. It runs
// through vswitch.Switch rather than stp.Layer directly, so it exercises the
// stp.FlushTarget-to-bridge.FlushTarget translation in applySTPEffects and
// the FID filtering in Bridge.Flush, not just the layer's own Effects.
//
// sw2's MSTI 1 carries an inflated path cost on l1 (its own instance
// override, same shape as stp's TestMSTInstancesSelectIndependentRoots), so
// once the two switches converge, MSTI 1's root port is l2 while the CIST
// and MSTI 2 both keep l1 (equal cost on both links resolves to the lower
// port ID). Failing l2 then raises a topology change on MSTI 1 alone: the
// CIST and MSTI 2 were never forwarding on l2, so neither transitions.
func TestMSTITopologyChangeFlushesOnlyItsOwnVLAN(t *testing.T) {
	start := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	mac1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	mac2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	region := func(instances map[stp.MSTID]stp.Instance) *stp.MST {
		return &stp.MST{Name: "region-1", Revision: 1, Instances: instances}
	}
	pvid := vlan.ID(10)
	trunk := bridge.Switchport{PVID: &pvid, Tagged: []vlan.ID{10, 20}}
	vlanTable := map[vlan.ID]string{10: "ten", 20: "twenty"}

	sw1 := mustSwitch(t, vswitch.Config{
		Ports: mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "l1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "l2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
		MAC: mac1,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table:       vlanTable,
			Switchports: map[string]bridge.Switchport{"l1": trunk, "l2": trunk},
		}},
		STP: &stp.Config{
			Priority: 4096,
			Address:  mac1,
			Ports:    map[string]stp.Port{"l1": {}, "l2": {}},
			MST: region(map[stp.MSTID]stp.Instance{
				1: {VLANs: []vlan.ID{10}},
				2: {VLANs: []vlan.ID{20}},
			}),
		},
	})

	sw2 := mustSwitch(t, vswitch.Config{
		Ports: mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "l1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "l2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "p3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
		MAC: mac2,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table:       vlanTable,
			Switchports: map[string]bridge.Switchport{"l1": trunk, "l2": trunk, "p3": trunk},
		}},
		STP: &stp.Config{
			Priority: 32768,
			Address:  mac2,
			Ports:    map[string]stp.Port{"l1": {}, "l2": {}, "p3": {}},
			MST: region(map[stp.MSTID]stp.Instance{
				1: {VLANs: []vlan.ID{10}, Ports: map[string]stp.InstancePort{"l1": {PathCost: 200_000}}},
				2: {VLANs: []vlan.ID{20}},
			}),
		},
	})

	switches := []*vswitch.Switch{sw1, sw2}
	cables := []switchCable{
		{swA: 0, portA: "l1", swB: 1, portB: "l1"},
		{swA: 0, portA: "l2", swB: 1, portB: "l2"},
	}

	snapshot := func() string {
		var b strings.Builder
		for _, sw := range switches {
			roles := sw.Roles()
			for _, name := range []string{"l1", "l2"} {
				fmt.Fprintf(&b, "%v/%v;", roles[name].Role, roles[name].State)
			}
		}
		return b.String()
	}

	now := convergeSwitches(t, start, switches, cables, snapshot)

	// vswitch.Switch exposes only the CIST's roles; MSTI 1's own root port
	// landing on l2 instead of l1 is confirmed indirectly below, by which
	// FDB entry the flush after l2 fails leaves behind.
	cist := sw2.Roles()["l1"]
	if cist.Role != stp.RoleRoot || cist.State != stp.StateForwarding {
		t.Fatalf("sw2 CIST on l1 = role %v state %v, want Root Forwarding", cist.Role, cist.State)
	}

	macVLAN10 := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x10}
	macVLAN20 := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x20}
	mustSwitchLearn(t, sw2, []bridge.Seed{
		{FID: 10, MAC: macVLAN10, Port: "p3", LearnedAt: now},
		{FID: 20, MAC: macVLAN20, Port: "p3", LearnedAt: now},
	})

	sw2.LinkChange(now, "l2", port.Down, vswitch.PointToPointTrue, 1_000_000_000)

	entries := sw2.Entries()
	if len(entries) != 1 || entries[0].FID != 20 || entries[0].Port != "p3" {
		t.Fatalf("after MSTI 1's topology change, Entries() = %+v, want only the VLAN 20 entry on p3 kept", entries)
	}
}

// pvstSwitchConfig builds a one-trunk PVST switch carrying VLAN 1 and VLAN 10,
// with the trunk's untagged VLAN set to pvid and every other carried VLAN
// tagged. That is the shape Cisco's tagging rules turn on: VLAN 1 untagged on
// a native-VLAN-1 trunk, tagged on any other.
func pvstSwitchConfig(t *testing.T, pvid vlan.ID, carried ...vlan.ID) vswitch.Config {
	t.Helper()

	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	table := make(map[vlan.ID]string, len(carried))
	var tagged []vlan.ID
	for _, vid := range carried {
		table[vid] = fmt.Sprintf("VLAN%d", vid)
		if vid != pvid {
			tagged = append(tagged, vid)
		}
	}
	slices.Sort(tagged)

	trees := make(map[vlan.ID]stp.Tree, len(carried))
	for _, vid := range carried {
		trees[vid] = stp.Tree{}
	}

	return vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: table,
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &pvid, Untagged: []vlan.ID{pvid}, Tagged: tagged},
			},
		}},
		STP: &stp.Config{
			Priority: 4096,
			Address:  netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01},
			Ports:    map[string]stp.Port{"1/1/1": {}},
			PVST:     &stp.PVST{Trees: trees},
		},
	}
}

// emittedBPDUShape renders one drained emission as the group address it went
// to, the VLAN tag it actually carries on the wire, and the VLAN its payload
// names. The tag is the part this unit decides; the payload VLAN comes from
// the layer.
func emittedBPDUShape(t *testing.T, em vswitch.Emission) string {
	t.Helper()

	tag := "untagged"
	if len(em.Frame.Tags) == 1 {
		tag = fmt.Sprintf("tag=%d", em.Frame.Tags[0].VID)
	} else if len(em.Frame.Tags) > 1 {
		tag = fmt.Sprintf("tags=%d", len(em.Frame.Tags))
	}

	if em.Frame.Dst != stp.GroupAddressSSTP {
		return fmt.Sprintf("ieee/%s", tag)
	}
	_, tlvVID, err := stp.DecodeSSTP(em.Frame)
	if err != nil {
		t.Fatalf("decode drained SSTP emission: %v", err)
	}

	return fmt.Sprintf("sstp/%s/payload=%d", tag, tlvVID)
}

func emittedBPDUShapes(t *testing.T, emissions []vswitch.Emission) []string {
	t.Helper()

	shapes := make([]string, 0, len(emissions))
	for _, em := range emissions {
		shapes = append(shapes, emittedBPDUShape(t, em))
	}
	slices.Sort(shapes)

	return shapes
}

// TestPVSTBPDUsRideTheirOwnVLANsEgressTagging is evidence for Cisco's tagging
// rules on a PVST trunk: each VLAN's BPDU leaves the way any other frame on
// that VLAN would, and VLAN 1's tree adds one untagged IEEE-addressed frame
// whatever the native VLAN is. That second frame is what an RSTP or MSTP
// neighbor converges with, so it must not pick up a tag.
func TestPVSTBPDUsRideTheirOwnVLANsEgressTagging(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	t.Run("native vlan 1", func(t *testing.T) {
		t.Parallel()

		sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
		sw.Start(now)
		sw.Drain()
		sw.Wake(now.Add(2 * time.Second))

		got := emittedBPDUShapes(t, sw.Drain())
		want := []string{"ieee/untagged", "sstp/tag=10/payload=10", "sstp/untagged/payload=1"}
		if !slices.Equal(got, want) {
			t.Fatalf("emissions = %v, want %v", got, want)
		}
	})

	t.Run("native vlan 20", func(t *testing.T) {
		t.Parallel()

		sw := mustSwitch(t, pvstSwitchConfig(t, 20, 1, 10, 20))
		sw.Start(now)
		sw.Drain()
		sw.Wake(now.Add(2 * time.Second))

		got := emittedBPDUShapes(t, sw.Drain())
		want := []string{
			"ieee/untagged",
			"sstp/tag=1/payload=1",
			"sstp/tag=10/payload=10",
			"sstp/untagged/payload=20",
		}
		if !slices.Equal(got, want) {
			t.Fatalf("emissions = %v, want %v", got, want)
		}
	})
}

// TestPVSTBPDUIsWithheldFromAPortThatDoesNotCarryTheVLAN is evidence that the
// emission goes through the bridge's ordinary origination rules rather than
// around them: a tree configured for a VLAN the trunk does not carry puts no
// frame on that trunk.
func TestPVSTBPDUIsWithheldFromAPortThatDoesNotCarryTheVLAN(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	cfg := pvstSwitchConfig(t, 1, 1, 10)
	// VLAN 30 has a tree but no place in the bridge's VLAN table, so the
	// trunk cannot carry it.
	cfg.STP.PVST.Trees[30] = stp.Tree{}

	sw := mustSwitch(t, cfg)
	sw.Start(now)
	sw.Drain()
	sw.Wake(now.Add(2 * time.Second))

	for _, shape := range emittedBPDUShapes(t, sw.Drain()) {
		if strings.HasSuffix(shape, "payload=30") {
			t.Fatalf("a VLAN 30 BPDU left a trunk that does not carry VLAN 30: %s", shape)
		}
	}
}

// TestNewRefusesAPVSTSwitchCarryingAVLANWithNoTree is evidence for the
// construction check: a VLAN the bridge carries with no tree of its own has
// no defined forwarding state, which is the false answer per-VLAN spanning
// tree exists to remove.
func TestNewRefusesAPVSTSwitchCarryingAVLANWithNoTree(t *testing.T) {
	t.Parallel()

	cfg := pvstSwitchConfig(t, 1, 1, 10, 20)
	delete(cfg.STP.PVST.Trees, 20)

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want a refusal naming the VLAN with no tree")
	}
	attrs := errs.Attributes(err)
	if got := attrs["field"]; got != "stp.pvst.trees" {
		t.Errorf("error field = %v, want %q", got, "stp.pvst.trees")
	}
	if got := attrs["vlan"]; got != vlan.ID(20) {
		t.Errorf("error vlan = %v, want 20", got)
	}

	cfg.STP.PVST.Trees[20] = stp.Tree{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() with every carried VLAN covered = %v, want nil", err)
	}
}

// pvstSSTPFrame builds an SSTP BPDU that a peer bridge would send for vid,
// tagged for the wire as tagVID names.
func pvstSSTPFrame(t *testing.T, vid vlan.ID, tagVID vlan.ID, src netaddr.MAC) ethernet.Frame {
	t.Helper()

	frame, err := stp.EncodeSSTP(stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096 | uint16(vid), Address: src},
		BridgeID:     stp.BridgeID{Priority: 4096 | uint16(vid), Address: src},
		PortID:       0x8001,
		Version:      2,
		Type:         stp.BPDUTypeRapid,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}, vid, src)
	if err != nil {
		t.Fatalf("EncodeSSTP for vlan %d: %v", vid, err)
	}
	if tagVID != 0 {
		frame.Tags = []vlan.Tag{{VID: tagVID}}
	}

	return frame
}

func findStep(steps []trace.Step, ruleID trace.RuleID) (trace.Step, bool) {
	for _, step := range steps {
		if step.RuleID == ruleID {
			return step, true
		}
	}

	return trace.Step{}, false
}

// TestPVSTPVIDInconsistencyTraceNamesBothVLANs is evidence that a PVID
// inconsistency is readable from the trace rather than only from the port
// state: the admit step carries the VLAN the BPDU named and the VLAN the
// bridge classified it into, which is the whole finding.
func TestPVSTPVIDInconsistencyTraceNamesBothVLANs(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	sw.Start(now)
	sw.Drain()

	// A BPDU naming VLAN 20, tagged for VLAN 10, so the bridge classifies it
	// into VLAN 10 and the two disagree.
	res := sw.Forward(now, "1/1/1", pvstSSTPFrame(t, 20, 10, peer))

	step, ok := findStep(res.Steps, "stp.sstp.admit")
	if !ok {
		t.Fatalf("no stp.sstp.admit step in the trace: %+v", res.Steps)
	}

	var canonical string
	for _, fact := range step.Inputs {
		if fact.TypeID() == "stp.sstp.vlans" {
			canonical = fact.Canonical()
		}
	}
	want := "tlv=20,arrival=10,consistent=false"
	if canonical != want {
		t.Fatalf("stp.sstp.vlans fact = %q, want %q", canonical, want)
	}
}

// TestPVSTBoundaryIssueIsRaisedPerVLANAndNotForVLAN1 is evidence for the
// boundary this phase reports: a journey on a VLAN whose tree stops at the
// port carries the issue, and a VLAN 1 journey through the same port does
// not, because VLAN 1 is the one tree that does converge across the boundary.
func TestPVSTBoundaryIssueIsRaisedPerVLANAndNotForVLAN1(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peerMAC := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	sw.Start(now)
	sw.Drain()

	// An MST BPDU from the neighbor marks the port a boundary.
	mstFrame, err := stp.Encode(stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: peerMAC},
		BridgeID:     stp.BridgeID{Priority: 4096, Address: peerMAC},
		PortID:       0x8001,
		Version:      3,
		Type:         stp.BPDUTypeRapid,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
		ConfigID:     new(stp.MST{Name: "elsewhere", Revision: 7}.ConfigID()),
	}, peerMAC)
	if err != nil {
		t.Fatalf("Encode MST BPDU: %v", err)
	}
	sw.Forward(now, "1/1/1", mstFrame)
	sw.Drain()

	vlan10 := sw.Forward(now, "1/1/1", ethernet.Frame{
		Src:  netaddr.MAC{2, 0, 0, 0, 0, 0x10},
		Dst:  netaddr.MAC{2, 0, 0, 0, 0, 0x11},
		Tags: []vlan.Tag{{VID: 10}},
	})
	if !hasIssueCode(vlan10.Metadata.Issues(), vswitch.IssuePVSTBoundary) {
		t.Fatalf("vlan 10 journey carries no %s issue: %+v", vswitch.IssuePVSTBoundary, vlan10.Metadata.Issues())
	}
	for _, issue := range vlan10.Metadata.Issues() {
		if issue.Code != vswitch.IssuePVSTBoundary {
			continue
		}
		want := analysis.ProtocolScope("", string(port.LayerStp), "1/1/1/10")
		if issue.Scope.Compare(want) != 0 {
			t.Errorf("issue scope = %s, want %s", issue.Scope, want)
		}
		if issue.Status != analysis.Unsupported {
			t.Errorf("issue status = %s, want %s", issue.Status, analysis.Unsupported)
		}
	}

	vlan1 := sw.Forward(now, "1/1/1", ethernet.Frame{
		Src: netaddr.MAC{2, 0, 0, 0, 0, 0x01},
		Dst: netaddr.MAC{2, 0, 0, 0, 0, 0x02},
	})
	if hasIssueCode(vlan1.Metadata.Issues(), vswitch.IssuePVSTBoundary) {
		t.Errorf("vlan 1 journey carries a %s issue, want none: %+v",
			vswitch.IssuePVSTBoundary, vlan1.Metadata.Issues())
	}
}

// TestPVSTBoundaryIssueIsRaisedOnAnMSTPBridgeToo is the reciprocal: a bridge
// that does not run per-VLAN trees and meets one reports the same boundary
// for the VLANs it cannot answer about.
func TestPVSTBoundaryIssueIsRaisedOnAnMSTPBridgeToo(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peerMAC := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := pvstSwitchConfig(t, 1, 1, 10)
	cfg.STP.PVST = nil
	cfg.STP.MST = &stp.MST{Name: "region-1", Revision: 1, Instances: map[stp.MSTID]stp.Instance{
		1: {VLANs: []vlan.ID{10}},
	}}

	sw := mustSwitch(t, cfg)
	sw.Start(now)
	sw.Drain()

	sw.Forward(now, "1/1/1", pvstSSTPFrame(t, 10, 10, peerMAC))
	sw.Drain()

	vlan10 := sw.Forward(now, "1/1/1", ethernet.Frame{
		Src:  netaddr.MAC{2, 0, 0, 0, 0, 0x10},
		Dst:  netaddr.MAC{2, 0, 0, 0, 0, 0x11},
		Tags: []vlan.Tag{{VID: 10}},
	})
	if !hasIssueCode(vlan10.Metadata.Issues(), vswitch.IssuePVSTBoundary) {
		t.Fatalf("vlan 10 journey on the MSTP bridge carries no %s issue: %+v",
			vswitch.IssuePVSTBoundary, vlan10.Metadata.Issues())
	}

	vlan1 := sw.Forward(now, "1/1/1", ethernet.Frame{
		Src: netaddr.MAC{2, 0, 0, 0, 0, 0x01},
		Dst: netaddr.MAC{2, 0, 0, 0, 0, 0x02},
	})
	if hasIssueCode(vlan1.Metadata.Issues(), vswitch.IssuePVSTBoundary) {
		t.Errorf("vlan 1 journey carries a %s issue, want none: %+v",
			vswitch.IssuePVSTBoundary, vlan1.Metadata.Issues())
	}
}

// TestPVSTForeignVLANDoesNotRewriteVLAN1sRoot is evidence that a BPDU tagged
// for a VLAN the bridge does not admit on the ingress port never reaches a
// tree lookup: the layer's SSTPNotAdmitted outcome stops it before treeFor
// runs, so a foreign VLAN's BPDU cannot fall back to the CIST, which under
// PVST is VLAN 1's own tree.
func TestPVSTForeignVLANDoesNotRewriteVLAN1sRoot(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	sw.Start(now)
	sw.Drain()

	rootBefore, _, portBefore := sw.Root()

	// VLAN 20 is not in the bridge's VLAN table, so it is not admitted on
	// any port. Its root priority is far better than this switch's own, so
	// if the BPDU falls back to the CIST (VLAN 1's tree under PVST) instead
	// of being refused, the peer displaces this switch as VLAN 1's root.
	foreign, err := stp.EncodeSSTP(stp.BPDU{
		RootID:       stp.BridgeID{Priority: 0, Address: peer},
		BridgeID:     stp.BridgeID{Priority: 0, Address: peer},
		PortID:       0x8001,
		Version:      2,
		Type:         stp.BPDUTypeRapid,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}, 20, peer)
	if err != nil {
		t.Fatalf("EncodeSSTP: %v", err)
	}
	foreign.Tags = []vlan.Tag{{VID: 20}}
	res := sw.Forward(now, "1/1/1", foreign)

	if _, ok := findStep(res.Steps, "stp.sstp.vlan-not-admitted"); !ok {
		t.Fatalf("no stp.sstp.vlan-not-admitted step in the trace: %+v", res.Steps)
	}
	if res.Reason != stp.ReasonVLANNotAdmitted {
		t.Errorf("trace reason = %q, want %q", res.Reason, stp.ReasonVLANNotAdmitted)
	}

	rootAfter, _, portAfter := sw.Root()
	if rootAfter != rootBefore || portAfter != portBefore {
		t.Errorf("a VLAN 20 BPDU not admitted on the ingress port changed VLAN 1's root from %v/%q to %v/%q",
			rootBefore, portBefore, rootAfter, portAfter)
	}
}

// TestPVSTPriorityTagIsNotReadAsVLAN0 is evidence that a tag whose VID is 0
// (a priority tag, which PriorityTags: Always emits on an otherwise
// untagged BPDU) resolves the same as no tag at all. Reading VID 0 as a VLAN
// selection would classify the frame into VLAN 0 where the bridge classifies
// into the PVID, so two identically configured PVST peers would disagree
// about VLAN 1 on every single BPDU and permanently block it.
func TestPVSTPriorityTagIsNotReadAsVLAN0(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	sw.Start(now)
	sw.Drain()

	// VLAN 1's own BPDU, tagged with a priority tag (VID 0) the way
	// PriorityTags: Always emits it.
	frame := pvstSSTPFrame(t, 1, 0, peer)
	frame.Tags = []vlan.Tag{{VID: 0}}
	sw.Forward(now, "1/1/1", frame)

	info := sw.Roles()["1/1/1"]
	if info.BlockReason == stp.BlockReasonPVIDInconsistent {
		t.Errorf("a correctly addressed VLAN 1 BPDU with a priority tag was read as VLAN 0 and blocked VLAN 1: %+v", info)
	}
}

// TestValidateRefusesPVSTOnAVLANUnawareBridge is evidence that PVST requires
// a VLAN-aware bridge: on a bridge with no VLAN table, untaggedVID returns 0
// for every port, so each end of a link reads the other's BPDU as arriving
// on the wrong VLAN and blocks VLAN 1 permanently. There is nothing
// per-VLAN about a bridge with no VLAN table to run trees over.
func TestValidateRefusesPVSTOnAVLANUnawareBridge(t *testing.T) {
	t.Parallel()

	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01},
			Ports:    map[string]stp.Port{"1/1/1": {}},
			PVST:     &stp.PVST{Trees: map[vlan.ID]stp.Tree{1: {}}},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want refusal of PVST on a VLAN-unaware bridge")
	}
	if got, want := errs.Attributes(err)["field"], "stp.pvst"; got != want {
		t.Errorf("error field = %v, want %q", got, want)
	}
}

// TestValidateAcceptsPVSTWithImplicitVLAN1Tree is evidence for the ordinary
// switch shape: a bridge carrying VLAN 1 alongside other VLANs, with a PVST
// configuration that never names VLAN 1's tree explicitly. Normalize fills
// it in, so the covered-VLAN check must see it too.
func TestValidateAcceptsPVSTWithImplicitVLAN1Tree(t *testing.T) {
	t.Parallel()

	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	pvid := vlan.ID(1)

	cfg := vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{1: "default", 10: "VLAN10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &pvid, Untagged: []vlan.ID{1}, Tagged: []vlan.ID{10}},
			},
		}},
		STP: &stp.Config{
			Priority: 32768,
			Address:  netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01},
			Ports:    map[string]stp.Port{"1/1/1": {}},
			PVST:     &stp.PVST{Trees: map[vlan.ID]stp.Tree{10: {}}},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil for the ordinary shape where VLAN 1's tree is implicit", err)
	}
}

// TestPVSTBoundaryNotRaisedForAnFID0Drop is evidence that recordPVSTBoundaries
// only records against a classified journey: res.FID is 0 on every path
// where classification never ran, including a pre-classification drop such
// as a reserved group address, and that has nothing to do with spanning
// tree.
func TestPVSTBoundaryNotRaisedForAnFID0Drop(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peerMAC := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	sw.Start(now)
	sw.Drain()

	// An MST BPDU from the neighbor marks the port a boundary.
	mstFrame, err := stp.Encode(stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: peerMAC},
		BridgeID:     stp.BridgeID{Priority: 4096, Address: peerMAC},
		PortID:       0x8001,
		Version:      3,
		Type:         stp.BPDUTypeRapid,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
		ConfigID:     new(stp.MST{Name: "elsewhere", Revision: 7}.ConfigID()),
	}, peerMAC)
	if err != nil {
		t.Fatalf("Encode MST BPDU: %v", err)
	}
	sw.Forward(now, "1/1/1", mstFrame)
	sw.Drain()

	// A frame to a reserved group address is dropped before classification,
	// so FID is 0.
	res := sw.Forward(now, "1/1/1", ethernet.Frame{
		Src: netaddr.MAC{2, 0, 0, 0, 0, 1},
		Dst: netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x0e},
	})
	if hasIssueCode(res.Metadata.Issues(), vswitch.IssuePVSTBoundary) {
		t.Errorf("an FID-0 journey carries a %s issue, want none: %+v",
			vswitch.IssuePVSTBoundary, res.Metadata.Issues())
	}
}

// TestPVSTAdmitBeforeSnapshotUsesTheArrivalVLANsTree is evidence that the
// admit step's before/after snapshot both come from the tree the BPDU
// actually affects. Taking before from the CIST (VLAN 1's tree under PVST)
// while after comes from the arrival VLAN's tree renders a VLAN 10 BPDU
// that changes nothing on VLAN 10 as a spurious transition, once VLAN 1's
// tree and VLAN 10's tree disagree about port state.
func TestPVSTAdmitBeforeSnapshotUsesTheArrivalVLANsTree(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	sw.Start(now)
	sw.Drain()

	// A peer BPDU with a far better (lower) priority than this switch's own
	// makes the peer root of VLAN 10, moving that port's VLAN 10 role away
	// from the default Designated. Nothing is ever sent for VLAN 1, so the
	// CIST stays at its default Designated/Forwarding, and the two trees now
	// disagree about this port's role.
	converge, err := stp.EncodeSSTP(stp.BPDU{
		RootID:       stp.BridgeID{Priority: 0, Address: peer},
		BridgeID:     stp.BridgeID{Priority: 0, Address: peer},
		PortID:       0x8001,
		Version:      2,
		Type:         stp.BPDUTypeRapid,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}, 10, peer)
	if err != nil {
		t.Fatalf("EncodeSSTP: %v", err)
	}
	converge.Tags = []vlan.Tag{{VID: 10}}
	sw.Forward(now, "1/1/1", converge)
	sw.Drain()

	// The identical BPDU again: VLAN 10's own before and after must match,
	// since nothing about VLAN 10 changes on this second delivery.
	res := sw.Forward(now, "1/1/1", converge)

	step, ok := findStep(res.Steps, "stp.sstp.admit")
	if !ok {
		t.Fatalf("no stp.sstp.admit step in the trace: %+v", res.Steps)
	}

	var decision trace.Fact
	for _, fact := range step.Outputs {
		if fact.TypeID() == "stp.bpdu_decision" {
			decision = fact
		}
	}
	if decision == nil {
		t.Fatalf("no stp.bpdu_decision output in the admit step: %+v", step.Outputs)
	}

	canonical := decision.Canonical()
	beforeAt := strings.Index(canonical, ";before=")
	afterAt := strings.Index(canonical, ";after=")
	if beforeAt < 0 || afterAt < 0 || afterAt < beforeAt {
		t.Fatalf("stp.bpdu_decision canonical %q does not carry before/after sections", canonical)
	}
	beforePart := canonical[beforeAt+len(";before=") : afterAt]
	afterPart := canonical[afterAt+len(";after="):]

	// The received-BPDU counter is the one field that must move: it counts the
	// frame this very step is reporting on, and it is a link property both
	// snapshots read from the same place. Everything else describes the tree
	// the snapshot was taken from, which is what this test is about.
	rxCount := regexp.MustCompile(`rx_bpdus=\d+`)
	beforePart = rxCount.ReplaceAllString(beforePart, "rx_bpdus=N")
	afterPart = rxCount.ReplaceAllString(afterPart, "rx_bpdus=N")

	if beforePart != afterPart {
		t.Errorf("a second identical VLAN 10 BPDU reported before != after:\nbefore=%s\nafter=%s", beforePart, afterPart)
	}
}

// TestSSTPFrameConsultsTheSTPScopeWhenSpanningTreeIsMissing is evidence that
// a per-VLAN BPDU reaching a switch whose spanning tree state never loaded is
// answered the way an IEEE-addressed one is: the result names the spanning
// tree scope it depended on, rather than reporting a confident drop.
func TestSSTPFrameConsultsTheSTPScopeWhenSpanningTreeIsMissing(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peerMAC := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}
	vid := vlan.ID(1)

	stpScope := analysis.ProtocolScope("sw1", string(port.LayerStp), "0")
	sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		Config: vswitch.Config{
			MAC: netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01},
			Ports: mustTable(t, port.NewBuilder().
				Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
			Bridge: &bridge.Config{VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{1: "VLAN1"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid, Untagged: []vlan.ID{1}},
				},
			}},
		},
		NodeID: "sw1",
		Metadata: analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{{
			Code:    "test.stp.protocol",
			Status:  analysis.Unsupported,
			Scope:   stpScope,
			Message: "the spanning tree protocol state is incomplete",
		}}, analysis.EvidenceCatalog{}, nil),
	})
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	// Tagged for a VLAN the access port does not carry, so the bridge drops
	// the frame at classification, before any gate runs. That is the path
	// where the spanning tree scope would otherwise go unnamed.
	res := sw.Forward(now, "1/1/1", pvstSSTPFrame(t, 99, 99, peerMAC))
	if res.Outcome != trace.Dropped {
		t.Fatalf("outcome = %s, want %s", res.Outcome, trace.Dropped)
	}

	want := analysis.FieldScope(stpScope, "ports", "1/1/1")
	if !slices.ContainsFunc(res.ConsultedScopes(), func(s analysis.Scope) bool {
		return s.Compare(want) == 0
	}) {
		t.Fatalf("consulted scopes = %v, want one naming %s", res.ConsultedScopes(), want)
	}
}

// TestSSTPForAnUnadmittedVLANStillFiresBPDUGuard is the regression test for
// the defect this unit fixes: a rogue bridge cannot evade BPDU guard by
// tagging its BPDU with a VLAN the ingress port does not admit. BPDU guard
// lives in the link half of a receive, which now runs before the switch's
// admission answer decides anything about the tree, so a frame that decodes
// still disables the port even when the layer goes on to refuse the VLAN.
func TestSSTPForAnUnadmittedVLANStillFiresBPDUGuard(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := pvstSwitchConfig(t, 1, 1, 10)
	cfg.STP.Ports["1/1/1"] = stp.Port{BPDUGuard: true}

	sw := mustSwitch(t, cfg)
	sw.Start(now)
	sw.Drain()

	// VLAN 30 is not in the bridge's VLAN table, so it is not admitted on
	// this port. A gate that refused the frame before the layer saw it would
	// never let BPDU guard fire; the fix is that the layer's link half runs
	// regardless.
	rogue, err := stp.EncodeSSTP(stp.BPDU{
		RootID:       stp.BridgeID{Priority: 0, Address: peer},
		BridgeID:     stp.BridgeID{Priority: 0, Address: peer},
		PortID:       0x8001,
		Version:      2,
		Type:         stp.BPDUTypeRapid,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}, 30, peer)
	if err != nil {
		t.Fatalf("EncodeSSTP: %v", err)
	}
	rogue.Tags = []vlan.Tag{{VID: 30}}
	sw.Forward(now, "1/1/1", rogue)

	info := sw.Roles()["1/1/1"]
	if info.BlockReason != stp.BlockReasonBPDUGuard {
		t.Errorf("block reason = %q, want %q: an SSTP BPDU tagged for an unadmitted VLAN did not fire BPDU guard", info.BlockReason, stp.BlockReasonBPDUGuard)
	}
}

// TestSSTPRefusalTracesADecodedFrame is evidence that a VLAN refusal is
// rendered as what it is: a frame that decoded and was judged against the
// bridge's admission rule, not a malformed frame. The decode fact reports
// valid=true, and the VLAN fact names both the TLV's VLAN and the VLAN the
// switch resolved on arrival.
func TestSSTPRefusalTracesADecodedFrame(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	sw.Start(now)
	sw.Drain()

	before := sw.Roles()["1/1/1"]

	foreign := pvstSSTPFrame(t, 30, 30, peer)
	res := sw.Forward(now, "1/1/1", foreign)

	step, ok := findStep(res.Steps, "stp.sstp.vlan-not-admitted")
	if !ok {
		t.Fatalf("no stp.sstp.vlan-not-admitted step in the trace: %+v", res.Steps)
	}

	var decodeCanonical, vlansCanonical string
	for _, fact := range step.Inputs {
		switch fact.TypeID() {
		case "stp.bpdu_decision":
			decodeCanonical = fact.Canonical()
		case "stp.sstp.vlans":
			vlansCanonical = fact.Canonical()
		}
	}
	if !strings.Contains(decodeCanonical, "valid=true") {
		t.Errorf("decode fact = %q, want valid=true: a refusal on VLAN grounds is not a decode failure", decodeCanonical)
	}
	if want := "tlv=30,arrival=30,consistent=true"; vlansCanonical != want {
		t.Errorf("stp.sstp.vlans fact = %q, want %q", vlansCanonical, want)
	}

	after := sw.Roles()["1/1/1"]
	if after.BadBPDUs != before.BadBPDUs {
		t.Errorf("BadBPDUs = %d, want %d unchanged: a decoded frame refused on VLAN grounds must not count as a bad BPDU", after.BadBPDUs, before.BadBPDUs)
	}
}

// sstpStepShape summarizes an SSTP step's structural shape: which layer and
// op it belongs to, its rule and subject, and the kind of fact each input and
// output carries. It deliberately excludes the facts' own canonical values,
// since those report port state that mutation changes and Peek does not —
// the trace shape this unit promises is independent of mutation is the shape
// alone, not the counters a mutating call went on to move.
func sstpStepShape(step trace.Step) string {
	inputs := make([]string, 0, len(step.Inputs))
	for _, f := range step.Inputs {
		inputs = append(inputs, f.TypeID())
	}
	outputs := make([]string, 0, len(step.Outputs))
	for _, f := range step.Outputs {
		outputs = append(outputs, f.TypeID())
	}

	return fmt.Sprintf("layer=%s;op=%s;rule=%s;subject=%s/%s;inputs=%v;outputs=%v",
		step.Layer, step.Op, step.RuleID, step.Subject.Kind, step.Subject.Key, inputs, outputs)
}

// TestSSTPTraceShapeIsTheSameWithAndWithoutMutation is the claim nothing else
// pins: interceptSSTP renders the same step shape whether or not mutate is
// set, because the outcome the non-mutating path derives from admitted and
// TracksVLAN is exactly the outcome the mutating path reaches by calling
// ReceiveSSTP. Every reachable outcome but SSTPPortDown is covered: a port
// admitted into the bridge's VLAN table always has a per-VLAN spanning tree
// under a validly constructed switch (Config.Validate refuses a carried VLAN
// with no tree), so SSTPUntrackedVLAN — which requires an admitted VLAN with
// no tree — is not reachable through Switch.Forward and is not one of the
// cases below; stp.Layer's own tests cover it directly.
func TestSSTPTraceShapeIsTheSameWithAndWithoutMutation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cases := []struct {
		name   string
		cfg    func(t *testing.T) vswitch.Config
		frame  func(t *testing.T) ethernet.Frame
		ruleID trace.RuleID
	}{
		{
			name: "applied",
			cfg:  func(t *testing.T) vswitch.Config { return pvstSwitchConfig(t, 1, 1, 10) },
			frame: func(t *testing.T) ethernet.Frame {
				return pvstSSTPFrame(t, 10, 10, peer)
			},
			ruleID: "stp.sstp.admit",
		},
		{
			name: "bpdu-guard",
			cfg: func(t *testing.T) vswitch.Config {
				cfg := pvstSwitchConfig(t, 1, 1, 10)
				cfg.STP.Ports["1/1/1"] = stp.Port{BPDUGuard: true}
				return cfg
			},
			frame: func(t *testing.T) ethernet.Frame {
				return pvstSSTPFrame(t, 10, 10, peer)
			},
			ruleID: "stp.sstp.admit",
		},
		{
			name: "pvst-boundary",
			cfg: func(t *testing.T) vswitch.Config {
				cfg := pvstSwitchConfig(t, 1, 1, 10)
				cfg.STP.PVST = nil
				cfg.STP.MST = &stp.MST{Name: "region-1", Revision: 1, Instances: map[stp.MSTID]stp.Instance{
					1: {VLANs: []vlan.ID{10}},
				}}
				return cfg
			},
			frame: func(t *testing.T) ethernet.Frame {
				return pvstSSTPFrame(t, 10, 10, peer)
			},
			ruleID: "stp.sstp.admit",
		},
		{
			name: "vlan-not-admitted",
			cfg:  func(t *testing.T) vswitch.Config { return pvstSwitchConfig(t, 1, 1, 10) },
			frame: func(t *testing.T) ethernet.Frame {
				return pvstSSTPFrame(t, 30, 30, peer)
			},
			ruleID: "stp.sstp.vlan-not-admitted",
		},
		{
			name: "pvid-inconsistent",
			cfg:  func(t *testing.T) vswitch.Config { return pvstSwitchConfig(t, 1, 1, 10) },
			frame: func(t *testing.T) ethernet.Frame {
				return pvstSSTPFrame(t, 20, 10, peer)
			},
			ruleID: "stp.sstp.admit",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			forwardSw := mustSwitch(t, c.cfg(t))
			forwardSw.Start(now)
			forwardSw.Drain()
			forwardRes := forwardSw.Forward(now, "1/1/1", c.frame(t))

			peekSw := mustSwitch(t, c.cfg(t))
			peekSw.Start(now)
			peekSw.Drain()
			peekRes := peekSw.Peek(now, "1/1/1", c.frame(t))

			forwardStep, ok := findStep(forwardRes.Steps, c.ruleID)
			if !ok {
				t.Fatalf("Forward: no %s step in the trace: %+v", c.ruleID, forwardRes.Steps)
			}
			peekStep, ok := findStep(peekRes.Steps, c.ruleID)
			if !ok {
				t.Fatalf("Peek: no %s step in the trace: %+v", c.ruleID, peekRes.Steps)
			}

			if got, want := sstpStepShape(peekStep), sstpStepShape(forwardStep); got != want {
				t.Errorf("Peek step shape = %s, want %s (Forward's shape)", got, want)
			}
			if forwardRes.Outcome != peekRes.Outcome {
				t.Errorf("Forward outcome = %s, Peek outcome = %s, want equal", forwardRes.Outcome, peekRes.Outcome)
			}
			if forwardRes.Reason != peekRes.Reason {
				t.Errorf("Forward reason = %q, Peek reason = %q, want equal", forwardRes.Reason, peekRes.Reason)
			}
		})
	}
}

// TestSSTPGuardedFrameOnAnUnadmittedVLANTracesAsNotAdmitted is the regression
// test for the defect where a bpdu-guard hit on a VLAN the port does not
// admit rendered as stp.sstp.admit with an all-zero decision fact: ReceiveSSTP
// returns SSTPGuarded because the link half of a receive runs before
// admission is judged, and the outcome alone does not say whether the
// frame's own VLAN was ever admitted. The journey must say
// vlan-not-admitted, not admit, for this frame — the port carries VLANs 1
// and 10 only.
func TestSSTPGuardedFrameOnAnUnadmittedVLANTracesAsNotAdmitted(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := pvstSwitchConfig(t, 1, 1, 10)
	cfg.STP.Ports["1/1/1"] = stp.Port{BPDUGuard: true}

	sw := mustSwitch(t, cfg)
	sw.Start(now)
	sw.Drain()

	rogue := pvstSSTPFrame(t, 30, 30, peer)
	res := sw.Forward(now, "1/1/1", rogue)

	if res.Outcome != trace.Dropped {
		t.Errorf("Outcome = %s, want %s: bpdu guard firing on an unadmitted VLAN is not an admit", res.Outcome, trace.Dropped)
	}
	if res.Reason != stp.ReasonVLANNotAdmitted {
		t.Errorf("Reason = %q, want %q", res.Reason, stp.ReasonVLANNotAdmitted)
	}

	step, ok := findStep(res.Steps, "stp.sstp.vlan-not-admitted")
	if !ok {
		t.Fatalf("no stp.sstp.vlan-not-admitted step in the trace: %+v", res.Steps)
	}

	var decodeCanonical, outputCanonical string
	for _, fact := range step.Inputs {
		if fact.TypeID() == "stp.bpdu_decision" {
			decodeCanonical = fact.Canonical()
		}
	}
	for _, fact := range step.Outputs {
		if fact.TypeID() == "stp.bpdu_decision" {
			outputCanonical = fact.Canonical()
		}
	}
	if !strings.Contains(decodeCanonical, "valid=true") {
		t.Errorf("decode fact = %q, want valid=true: bpdu guard firing is not a decode failure", decodeCanonical)
	}

	// VLAN 30 has no tree on this bridge, so before and after both read as
	// the zero PortInfo; that is consistent with a refusal on VLAN grounds,
	// unlike rendering the same zero state under a rule that claims the
	// frame was admitted.
	beforeIdx := strings.Index(outputCanonical, ";before=")
	afterIdx := strings.Index(outputCanonical, ";after=")
	if beforeIdx < 0 || afterIdx < 0 || afterIdx <= beforeIdx {
		t.Fatalf("decision fact = %q, want before/after segments", outputCanonical)
	}
	beforeSeg := outputCanonical[beforeIdx+len(";before=") : afterIdx]
	afterSeg := outputCanonical[afterIdx+len(";after="):]
	if beforeSeg != afterSeg {
		t.Errorf("decision fact before != after: %q vs %q, want equal for an unadmitted, untracked VLAN", beforeSeg, afterSeg)
	}

	// BPDU guard still disabled the port: the fix must not weaken the guard
	// while correcting how the frame is traced.
	info := sw.Roles()["1/1/1"]
	if info.BlockReason != stp.BlockReasonBPDUGuard {
		t.Errorf("block reason = %q, want %q", info.BlockReason, stp.BlockReasonBPDUGuard)
	}
}

// TestSSTPOnAPortTheLayerDoesNotTrackTracesAsPortDown is the regression test
// for the defect where stp.SSTPPortDown fell through to the default arm and
// rendered as stp.sstp.admit: Config.Validate checks that every STP port
// exists in the port table, not the reverse, so an operationally-up port
// stp.Config.Ports omits passes the port table's own receive check and
// reaches ReceiveSSTP, which is what actually refuses it.
func TestSSTPOnAPortTheLayerDoesNotTrackTracesAsPortDown(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := pvstSwitchConfig(t, 1, 1, 10)
	cfg.Ports = mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	// "1/1/2" stays out of cfg.STP.Ports on purpose: the layer never
	// configured it even though the port table calls it up.

	sw := mustSwitch(t, cfg)
	sw.Start(now)
	sw.Drain()

	frame := pvstSSTPFrame(t, 10, 10, peer)
	res := sw.Forward(now, "1/1/2", frame)

	if res.Outcome != trace.Dropped {
		t.Errorf("Outcome = %s, want %s: a port the layer never configured did not process this frame at all", res.Outcome, trace.Dropped)
	}
	if res.Reason != port.ReasonPortDown {
		t.Errorf("Reason = %q, want %q", res.Reason, port.ReasonPortDown)
	}

	step, ok := findStep(res.Steps, "port.status.down")
	if !ok {
		t.Fatalf("no port.status.down step in the trace: %+v", res.Steps)
	}
	if step.Layer != port.LayerStp {
		t.Errorf("step layer = %s, want %s", step.Layer, port.LayerStp)
	}
}

// TestSSTPClassifiesAQinQTaggedFrameLikeBridgeIngress is the regression test
// for the defect where arrivalVID took an outer tag's VID under any TPID
// while tagged required a dot1Q-shaped TPID: a frame with an 0x88A8 outer tag
// carrying VID 10 read as arrivalVID=10, tagged=false, which
// AdmitsVIDOnIngress judges as an untagged VLAN 10 frame on a port whose
// untagged VLAN is 1 and refuses — while bridge.Bridge.Ingress treats an
// outer tag whose TPID names neither dot1Q nor the codec's untagged zero
// value as not a VLAN tag at all, and classifies the same frame into the
// port's untagged VLAN 1.
func TestSSTPClassifiesAQinQTaggedFrameLikeBridgeIngress(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	sw.Start(now)
	sw.Drain()

	frame, err := stp.EncodeSSTP(stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096 | 1, Address: peer},
		BridgeID:     stp.BridgeID{Priority: 4096 | 1, Address: peer},
		PortID:       0x8001,
		Version:      2,
		Type:         stp.BPDUTypeRapid,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}, 1, peer)
	if err != nil {
		t.Fatalf("EncodeSSTP: %v", err)
	}
	frame.Tags = []vlan.Tag{{TPID: uint16(ethernet.EtherTypeProviderBridging), VID: 10}}

	res := sw.Forward(now, "1/1/1", frame)

	step, ok := findStep(res.Steps, "stp.sstp.admit")
	if !ok {
		t.Fatalf("no stp.sstp.admit step in the trace: %+v", res.Steps)
	}
	if res.FID != 1 {
		t.Errorf("FID = %d, want 1: a QinQ outer tag is not a VLAN selection, so the frame lands on the port's untagged VLAN the same way bridge.Ingress classifies it", res.FID)
	}

	var vlansCanonical string
	for _, fact := range step.Inputs {
		if fact.TypeID() == "stp.sstp.vlans" {
			vlansCanonical = fact.Canonical()
		}
	}
	if want := "tlv=1,arrival=1,consistent=true"; vlansCanonical != want {
		t.Errorf("stp.sstp.vlans fact = %q, want %q", vlansCanonical, want)
	}
}

// TestSSTPPortDownTracesTheSameUnderPeekAndForward pins the agreement a
// read-only inspection owes a committing one. Peek derives its outcome from
// what the switch can observe without mutating, and the port-down case is the
// one the layer alone knows: a port the port table calls up that the spanning
// tree layer has not seen a link-up event for. A Peek that renders it as
// admitted describes a journey the switch would not have taken.
func TestSSTPPortDownTracesTheSameUnderPeekAndForward(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := pvstSwitchConfig(t, 1, 1, 10)
	cfg.Ports = mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	// Start is deliberately not called: "1/1/1" is a configured spanning tree
	// port that the layer has not yet seen a link transition for. That is the
	// case a port-name lookup alone cannot tell from a configured, linked
	// port, so it is the one that distinguishes the two.
	sw := mustSwitch(t, cfg)

	frame := pvstSSTPFrame(t, 10, 10, peer)

	peeked := sw.Peek(now, "1/1/1", frame)
	forwarded := sw.Forward(now, "1/1/1", frame)

	if peeked.Outcome != forwarded.Outcome {
		t.Errorf("Peek outcome = %s, Forward outcome = %s, want agreement", peeked.Outcome, forwarded.Outcome)
	}
	if peeked.Reason != forwarded.Reason {
		t.Errorf("Peek reason = %q, Forward reason = %q, want agreement", peeked.Reason, forwarded.Reason)
	}
	if _, ok := findStep(peeked.Steps, "port.status.down"); !ok {
		t.Errorf("no port.status.down step under Peek: %+v", peeked.Steps)
	}
}

// buildRoutedPortSwitch builds a switch with two routed ports and no bridge,
// for the neighbor lifecycle tests that need a routed interface bound
// directly to a port rather than to a VLAN.
func buildRoutedPortSwitch(t *testing.T) *vswitch.Switch {
	t.Helper()
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"1/1/1": {Port: "1/1/1", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"1/1/2": {Port: "1/1/2", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
					},
				},
			},
		},
	}
	return mustSwitch(t, cfg)
}

// buildIPv6MulticastRoutingSwitch builds a two-VLAN routed switch with MLD
// snooping enabled on both VLANs, so a Neighbor Discovery test can prove
// observation reaches the neighbor path and not the multicast one on a VLAN
// where the multicast path is genuinely live, not merely absent.
func buildIPv6MulticastRoutingSwitch(t *testing.T) *vswitch.Switch {
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
		Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {}, 20: {}}},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("2001:db8:10::1/64")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("2001:db8:20::1/64")}},
					},
				},
			},
		},
	}
	return mustSwitch(t, cfg)
}

// makeARPReply builds an ARP reply mapping senderIP to senderMAC, addressed
// to dstMAC the way a reply confirming a forward path is: the frame goes
// straight to the requester rather than to the broadcast address a request
// would use.
func makeARPReply(t *testing.T, senderIP, targetIP netip.Addr, senderMAC, dstMAC netaddr.MAC) ethernet.Frame {
	t.Helper()
	msg := arp.Message{
		HardwareType: 1,
		ProtocolType: 0x0800,
		Operation:    arp.Reply,
		SenderMAC:    senderMAC,
		SenderAddr:   senderIP,
		TargetMAC:    dstMAC,
		TargetAddr:   targetIP,
	}
	frame, err := arp.Encode(msg, dstMAC)
	if err != nil {
		t.Fatalf("encode ARP reply: %v", err)
	}
	return frame
}

// makeNDPFrame builds a Neighbor Discovery frame at the given hop limit, so
// a test can exercise both the valid RFC 4861 value (255) and an invalid one
// a codec refuses.
func makeNDPFrame(t *testing.T, src, dst netip.Addr, srcMAC, dstMAC netaddr.MAC, hopLimit uint8, msg ndp.Message) ethernet.Frame {
	t.Helper()
	hdr := ip.Header{Src: src, Dst: dst, HopLimit: hopLimit, Protocol: 58, V6: &ip.V6{}}
	payload, err := ndp.Encode(hdr, msg)
	if err != nil {
		t.Fatalf("encode NDP: %v", err)
	}
	packet, err := hdr.Encode(payload)
	if err != nil {
		t.Fatalf("encode IPv6: %v", err)
	}
	return ethernet.Frame{Dst: dstMAC, Src: srcMAC, EtherType: ethernet.EtherTypeIPv6, Payload: packet}
}

// TestARPObservationReleasesHeldFrameOnRoutedPort covers R21a and R21b on a
// routed port: a routed frame with no neighbor entry holds, an observed ARP
// reply moves the entry, and Wake releases the held frame as ordinary data
// (Protocol: false) rather than a protocol frame of the switch's own.
func TestARPObservationReleasesHeldFrameOnRoutedPort(t *testing.T) {
	sw := buildRoutedPortSwitch(t)

	dst := netip.MustParseAddr("10.0.20.77")
	pkt := makeIPv4Packet(t, ipH1, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}

	held := sw.Forward(fixedTime, "1/1/1", frame)
	if held.Outcome != trace.Held || held.Reason != routing.ReasonNeighborPending {
		t.Fatalf("outcome/reason = %v/%v, want Held/%v", held.Outcome, held.Reason, routing.ReasonNeighborPending)
	}

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	// An ARP frame is not owned by a routed port (Owns admits only IPv4 and
	// IPv6), so its ordinary path here is a not-bridged drop; observation is
	// a side effect of that path, not an interception of it.
	sw.Forward(fixedTime.Add(time.Second), "1/1/2", reply)

	sw.Wake(fixedTime.Add(time.Second))
	emissions := sw.Drain()
	if len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1: %+v", len(emissions), emissions)
	}
	if emissions[0].Port != "1/1/2" {
		t.Errorf("released port = %q, want 1/1/2", emissions[0].Port)
	}
	if emissions[0].Frame.Dst != learnedMAC {
		t.Errorf("released dst MAC = %v, want %v", emissions[0].Frame.Dst, learnedMAC)
	}
	if emissions[0].Protocol {
		t.Errorf("released frame Protocol = true, want false")
	}
	if failures := sw.DrainNeighborFailures(); len(failures) != 0 {
		t.Errorf("neighbor failures = %d, want 0: %+v", len(failures), failures)
	}
}

var (
	ipSubHost   = netip.MustParseAddr("10.0.10.77")
	ipSubSrc    = netip.MustParseAddr("10.0.30.7")
	ipSubHost3  = netip.MustParseAddr("10.0.30.77")
	macSubHost  = netaddr.MAC{0x02, 0, 0, 0, 0x10, 0x77}
	macSubHost3 = netaddr.MAC{0x02, 0, 0, 0, 0x30, 0x77}
)

// buildSubInterfaceSwitch builds a bridgeless routed switch carrying two C-TAG
// sub-interfaces on eth1 and a plain untagged routed port on eth3, with no
// static neighbors, so a frame routed out any of the three holds. A
// sub-interface's ordinary shape is a switch with no bridge at all, which is
// why this fixture configures none.
func buildSubInterfaceSwitch(t *testing.T) *vswitch.Switch {
	t.Helper()
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "eth1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "eth3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"eth1.10": {Port: "eth1", VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"eth1.20": {Port: "eth1", VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						"eth3":    {Port: "eth3", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
					},
				},
			},
		},
	}
	return mustSwitch(t, cfg)
}

// holdOnSubInterface routes a frame in on eth3 for a host behind eth1.10, which
// has no neighbor, leaving the frame in that interface's hold queue.
func holdOnSubInterface(t *testing.T, sw *vswitch.Switch) {
	t.Helper()
	pkt := makeIPv4Packet(t, ipSubSrc, ipSubHost, 64, []byte("held on a sub-interface"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}
	res := sw.Forward(fixedTime, "eth3", frame)
	if res.Outcome != trace.Held || res.Reason != routing.ReasonNeighborPending {
		t.Fatalf("outcome/reason = %v/%v, want Held/%v", res.Outcome, res.Reason, routing.ReasonNeighborPending)
	}
}

// subInterfaceARPReply builds ipSubHost's reply to eth1.10's ARP request,
// carrying the tags the caller asks for.
func subInterfaceARPReply(t *testing.T, tags []vlan.Tag) ethernet.Frame {
	t.Helper()
	reply := makeARPReply(t, ipSubHost, netip.MustParseAddr("10.0.10.1"), macSubHost, macRouter)
	reply.Tags = tags

	return reply
}

// TestSubInterfaceObservationBindsOnlyItsOuterVID pins which advertisement on a
// parent port answers a sub-interface's pending neighbor. The reply's own outer
// C-TAG names the interface, so a reply at a sibling's VID, at a VID no
// interface carries, under an S-TAG, untagged, or arriving while the port is
// operationally down binds nothing at all rather than falling back to whichever
// interface the port would carry untagged. The last subtest is the positive
// control: without it the five above would all pass against an
// observationInterface that never observes anything. The reply that does bind
// a sub-interface is TestARPObservationReleasesHeldFrameOnSubInterface, which
// needs the release path: Observe updates a pending entry and creates none, so
// binding shows only as the held frame leaving.
func TestSubInterfaceObservationBindsOnlyItsOuterVID(t *testing.T) {
	cTag := uint16(ethernet.EtherTypeDot1Q)

	for _, tc := range []struct {
		name string
		tags []vlan.Tag
		down bool
	}{
		{name: "the sibling sub-interface's VID", tags: []vlan.Tag{{TPID: cTag, VID: 20}}},
		{name: "a VID no interface carries", tags: []vlan.Tag{{TPID: cTag, VID: 30}}},
		{name: "an S-TAG at the right VID", tags: []vlan.Tag{{TPID: uint16(ethernet.EtherTypeProviderBridging), VID: 10}}},
		{name: "untagged", tags: nil},
		{name: "the right C-TAG while the parent port is down", tags: []vlan.Tag{{TPID: cTag, VID: 10}}, down: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sw := buildSubInterfaceSwitch(t)
			holdOnSubInterface(t, sw)
			if tc.down {
				if err := sw.SetOperStatus("eth1", port.Down); err != nil {
					t.Fatalf("SetOperStatus(eth1, Down) = %v", err)
				}
			}

			sw.Forward(fixedTime.Add(time.Second), "eth1", subInterfaceARPReply(t, tc.tags))
			sw.Wake(fixedTime.Add(time.Second))

			if emissions := sw.Drain(); len(emissions) != 0 {
				t.Errorf("emissions = %+v, want none: the reply answered a neighbor it does not name", emissions)
			}
			if failures := sw.DrainNeighborFailures(); len(failures) != 0 {
				t.Errorf("neighbor failures = %+v, want none", failures)
			}
		})
	}

	t.Run("an untagged reply on a plain routed port binds that port's interface", func(t *testing.T) {
		sw := buildSubInterfaceSwitch(t)

		pkt := makeIPv4Packet(t, ipSubHost, ipSubHost3, 64, []byte("held on the untagged port"))
		frame := ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			Tags:      []vlan.Tag{{TPID: cTag, VID: 10}},
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   pkt,
		}
		if res := sw.Forward(fixedTime, "eth1", frame); res.Outcome != trace.Held {
			t.Fatalf("outcome = %v, want Held; steps=%+v", res.Outcome, res.Steps)
		}

		reply := makeARPReply(t, ipSubHost3, netip.MustParseAddr("10.0.30.1"), macSubHost3, macRouter)
		sw.Forward(fixedTime.Add(time.Second), "eth3", reply)
		sw.Wake(fixedTime.Add(time.Second))

		emissions := sw.Drain()
		if len(emissions) != 1 || emissions[0].Port != "eth3" {
			t.Fatalf("emissions = %+v, want one on eth3", emissions)
		}
		if emissions[0].Frame.Dst != macSubHost3 {
			t.Errorf("released dst MAC = %v, want %v", emissions[0].Frame.Dst, macSubHost3)
		}
	})
}

// TestARPObservationReleasesHeldFrameOnSubInterface covers a held frame whose
// neighbor resolves on a sub-interface: it leaves by the parent port, tagged
// with the sub-interface's VLAN, the way the live path leaves the same frame.
// The fixture configures no bridge, which is a sub-interface's ordinary shape,
// so a release routed through the bridge does not merely take the wrong path
// here — it dereferences a nil bridge and panics.
func TestARPObservationReleasesHeldFrameOnSubInterface(t *testing.T) {
	sw := buildSubInterfaceSwitch(t)
	holdOnSubInterface(t, sw)

	sw.Forward(fixedTime.Add(time.Second), "eth1", subInterfaceARPReply(t, []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}}))
	sw.Wake(fixedTime.Add(time.Second))

	emission := soleDataEmission(t, sw.Drain(), macSubHost)
	if emission.Port != "eth1" {
		t.Errorf("released port = %q, want eth1, the sub-interface's parent", emission.Port)
	}
	// The held frame arrived untagged on eth3, so the egress tag carries its
	// priority: zero, and drop eligibility clear.
	wantTag := vlan.Tag{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}
	if got := emission.Frame.Tags; len(got) != 1 || got[0] != wantTag {
		t.Errorf("released tags = %+v, want [%+v]", got, wantTag)
	}
	if failures := sw.DrainNeighborFailures(); len(failures) != 0 {
		t.Errorf("neighbor failures = %+v, want none", failures)
	}
}

// TestHeldFrameOnSubInterfaceTimesOutAgainstParentPort pins HeldFrame.Port's
// contract for a sub-interface: the drop is counted against the parent port,
// which is the only port the interface has. finishHeld fills Port from the
// interface, so this pins that contract rather than the release path; a timed
// out frame never reaches releaseHeldFrame at all.
func TestHeldFrameOnSubInterfaceTimesOutAgainstParentPort(t *testing.T) {
	sw := buildSubInterfaceSwitch(t)
	holdOnSubInterface(t, sw)

	sw.Wake(fixedTime.Add(3 * time.Second))
	if emissions := sw.Drain(); len(emissions) != 0 {
		t.Errorf("emissions at timeout = %+v, want none", emissions)
	}
	failures := sw.DrainNeighborFailures()
	if len(failures) != 1 {
		t.Fatalf("neighbor failures = %d, want 1: %+v", len(failures), failures)
	}
	if failures[0].Port != "eth1" {
		t.Errorf("drop port = %q, want eth1, the sub-interface's parent", failures[0].Port)
	}
	if failures[0].Reason != routing.ReasonNeighborMiss {
		t.Errorf("reason = %v, want %v", failures[0].Reason, routing.ReasonNeighborMiss)
	}
}

// TestReleasedSubInterfaceFrameStaysOffTheBridge covers the same release on a
// switch that does have a bridge, where routing it through the bridge would not
// fault but would flood the frame at the FID the sub-interface's VLAN id
// happens to name. A sub-interface's parent port is outside the bridge, so the
// two VLAN 10s are unrelated namespaces and the released frame belongs on eth1
// alone.
func TestReleasedSubInterfaceFrameStaysOffTheBridge(t *testing.T) {
	p10 := vlan.ID(10)
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "eth1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "eth3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "swport", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table:       map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{"swport": {PVID: &p10, Untagged: []vlan.ID{10}}},
		}},
		Routing: &routing.Config{VRFs: map[string]routing.VRF{
			"default": {
				Interfaces: map[string]routing.Interface{
					"eth1.10": {Port: "eth1", VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
					"eth3":    {Port: "eth3", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
				},
			},
		}},
	})

	pkt := makeIPv4Packet(t, ipSubSrc, ipSubHost, 64, []byte("held beside a bridge"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}
	if res := sw.Forward(fixedTime, "eth3", frame); res.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held; steps=%+v", res.Outcome, res.Steps)
	}

	sw.Forward(fixedTime.Add(time.Second), "eth1", subInterfaceARPReply(t, []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}}))
	sw.Wake(fixedTime.Add(time.Second))

	emission := soleDataEmission(t, sw.Drain(), macSubHost)
	if emission.Port != "eth1" {
		t.Errorf("released port = %q, want eth1: the bridge's VLAN 10 is a different namespace", emission.Port)
	}
}

// TestARPObservationReleasesHeldFrameOnVLANInterface covers the same release
// as TestARPObservationReleasesHeldFrameOnRoutedPort for an interface bound
// to a VLAN rather than a port, and checks the released frame egresses
// through the bridge the way a routed frame's egress already does.
func TestARPObservationReleasesHeldFrameOnVLANInterface(t *testing.T) {
	sw := buildBaseRoutingSwitch(t)

	dst := netip.MustParseAddr("10.0.20.77")
	pkt := makeIPv4Packet(t, ipH1, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}

	held := sw.Forward(fixedTime, "1/1/1", frame)
	if held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}
	if !slices.ContainsFunc(held.Metadata.Issues(), func(issue analysis.Issue) bool {
		return issue.Code == vswitch.IssueNeighborUnresolved && issue.Status == analysis.Incomplete
	}) {
		t.Errorf("issues = %+v, want IssueNeighborUnresolved at Incomplete", held.Metadata.Issues())
	}

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	sw.Forward(fixedTime.Add(time.Second), "1/1/2", reply)

	sw.Wake(fixedTime.Add(time.Second))
	emissions := sw.Drain()
	if len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1: %+v", len(emissions), emissions)
	}
	if emissions[0].Frame.Dst != learnedMAC {
		t.Errorf("released dst MAC = %v, want %v", emissions[0].Frame.Dst, learnedMAC)
	}
	if emissions[0].Protocol {
		t.Errorf("released frame Protocol = true, want false")
	}
}

// TestReleasedFrameCarriesIngressPCPAndDEI covers a held frame's egress
// priority: a tagged frame arrives on a tagged vlan10 port with PCP 5, holds
// on an unresolved vlan20 next hop, and once the neighbor resolves the frame
// this releases onto a tagged vlan20 port carries the same PCP 5 the live,
// non-held path would have used. Before the item 1 fix, releaseHeldFrame
// built its synthetic bridge.Ingress with PCP and DEI left at zero, so the
// released frame always egressed at priority 0 regardless of what it
// arrived with.
func TestReleasedFrameCarriesIngressPCPAndDEI(t *testing.T) {
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
					"1/1/1": {PVID: &p10, Tagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Tagged: []vlan.ID{20}},
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
	sw := mustSwitch(t, cfg)

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("hello"))
	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macRouter,
		EtherType: ethernet.EtherTypeIPv4,
		Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), PCP: 5, VID: 10}},
		Payload:   pkt,
	}

	held := sw.Forward(fixedTime, "1/1/1", frame)
	if held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	learnedMAC := macH2
	reply := makeARPReply(t, ipH2, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	sw.Forward(fixedTime.Add(time.Second), "1/1/2", reply)

	sw.Wake(fixedTime.Add(time.Second))
	emissions := sw.Drain()
	if len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1: %+v", len(emissions), emissions)
	}
	released := emissions[0]
	if released.Frame.Dst != learnedMAC {
		t.Fatalf("released dst MAC = %v, want %v", released.Frame.Dst, learnedMAC)
	}
	if len(released.Frame.Tags) != 1 {
		t.Fatalf("released tags = %+v, want exactly one tag", released.Frame.Tags)
	}
	if released.Frame.Tags[0].VID != p20 {
		t.Errorf("released VID = %v, want %v", released.Frame.Tags[0].VID, p20)
	}
	if released.Frame.Tags[0].PCP != 5 {
		t.Errorf("released PCP = %v, want 5: the ingress priority must survive the hold", released.Frame.Tags[0].PCP)
	}
	if released.Frame.Tags[0].DEI {
		t.Errorf("released DEI = true, want false")
	}
}

// TestARPReplyUnderPeekLeavesTableUntouched covers R20c: Peek observes
// nothing, so the same reply that releases a held frame under Forward
// changes nothing under Peek, and two consecutive Peeks over the pending
// destination agree.
func TestARPReplyUnderPeekLeavesTableUntouched(t *testing.T) {
	sw := buildBaseRoutingSwitch(t)

	dst := netip.MustParseAddr("10.0.20.77")
	pkt := makeIPv4Packet(t, ipH1, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}

	first := sw.Peek(fixedTime, "1/1/1", frame)
	if first.Outcome != trace.Held || first.Reason != routing.ReasonNeighborPending {
		t.Fatalf("outcome/reason = %v/%v, want Held/%v", first.Outcome, first.Reason, routing.ReasonNeighborPending)
	}
	second := sw.Peek(fixedTime, "1/1/1", frame)
	if second.Outcome != first.Outcome || second.Reason != first.Reason {
		t.Errorf("second peek disagreed with first: %v/%v vs %v/%v", second.Outcome, second.Reason, first.Outcome, first.Reason)
	}

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	sw.Peek(fixedTime.Add(time.Second), "1/1/2", reply)

	if _, hasTimer := sw.NextWake(); hasTimer {
		t.Errorf("NextWake reports a timer after Peek alone, want none: a preview must not create an entry")
	}
	sw.Wake(fixedTime.Add(10 * time.Second))
	if emissions := sw.Drain(); len(emissions) != 0 {
		t.Errorf("emissions after Wake with only Peeks = %d, want 0: %+v", len(emissions), emissions)
	}
	if failures := sw.DrainNeighborFailures(); len(failures) != 0 {
		t.Errorf("neighbor failures after Wake with only Peeks = %d, want 0: %+v", len(failures), failures)
	}
}

// TestHeldFrameTimesOutToFailedWithDropStep covers R21b's failure half: with
// no reply, Wake at the resolution deadline produces no emission, moves the
// entry to Failed, and reports the held frame as a drop step rather than
// discarding it silently.
func TestHeldFrameTimesOutToFailedWithDropStep(t *testing.T) {
	sw := buildBaseRoutingSwitch(t)

	dst := netip.MustParseAddr("10.0.20.88")
	pkt := makeIPv4Packet(t, ipH1, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}

	held := sw.Forward(fixedTime, "1/1/1", frame)
	if held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	sw.Wake(fixedTime.Add(3 * time.Second))
	if emissions := sw.Drain(); len(emissions) != 0 {
		t.Errorf("emissions at timeout = %d, want 0: %+v", len(emissions), emissions)
	}
	failures := sw.DrainNeighborFailures()
	if len(failures) != 1 {
		t.Fatalf("neighbor failures = %d, want 1: %+v", len(failures), failures)
	}
	if failures[0].Step.Op != trace.OpDrop {
		t.Errorf("op = %v, want %v", failures[0].Step.Op, trace.OpDrop)
	}
	if failures[0].Reason != routing.ReasonNeighborMiss {
		t.Errorf("reason = %v, want %v", failures[0].Reason, routing.ReasonNeighborMiss)
	}
	if failures[0].Step.RuleID != trace.RuleID(routing.ReasonNeighborMiss) {
		t.Errorf("rule = %v, want %v", failures[0].Step.RuleID, routing.ReasonNeighborMiss)
	}
}

// TestNDPInvalidHopLimitDoesNotObserve covers a Neighbor Advertisement whose
// hop limit is not 255: the codec refuses to decode it, so the guard never
// calls Observe and the held frame still times out exactly as if nothing had
// arrived.
func TestNDPInvalidHopLimitDoesNotObserve(t *testing.T) {
	sw := buildIPv6MulticastRoutingSwitch(t)

	src := netip.MustParseAddr("2001:db8:10::7")
	dst := netip.MustParseAddr("2001:db8:20::77")
	pkt := makeIPv6Packet(t, src, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv6, Payload: pkt}

	if held := sw.Forward(fixedTime, "1/1/1", frame); held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	na := makeNDPFrame(t, dst, netip.MustParseAddr("2001:db8:20::1"), learnedMAC, macRouter, 254,
		ndp.Message{Type: ndp.NeighborAdvertisement, Target: dst, Solicited: true, Override: true, LinkLayerAddr: learnedMAC, HasLinkLayerAddr: true})
	sw.Forward(fixedTime.Add(500*time.Millisecond), "1/1/2", na)

	sw.Wake(fixedTime.Add(3 * time.Second))
	if emissions := sw.Drain(); len(emissions) != 0 {
		t.Errorf("emissions = %d, want 0: the hop-limit-254 advertisement must not have resolved the entry", len(emissions))
	}
	if failures := sw.DrainNeighborFailures(); len(failures) != 1 {
		t.Fatalf("neighbor failures = %d, want 1", len(failures))
	}
}

// TestNDPReachesNeighborPathNotMulticastPath covers R21a for IPv6: a valid
// Neighbor Advertisement releases the held frame even on a VLAN where MLD
// snooping is live, because an NDP frame carries no Hop-by-Hop header and so
// is never a multicast control candidate — it never reaches
// forwardMulticastControl, and its observation never reaches mcast group
// state either.
func TestNDPReachesNeighborPathNotMulticastPath(t *testing.T) {
	sw := buildIPv6MulticastRoutingSwitch(t)

	src := netip.MustParseAddr("2001:db8:10::7")
	dst := netip.MustParseAddr("2001:db8:20::77")
	pkt := makeIPv6Packet(t, src, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv6, Payload: pkt}

	if held := sw.Forward(fixedTime, "1/1/1", frame); held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	na := makeNDPFrame(t, dst, netip.MustParseAddr("2001:db8:20::1"), learnedMAC, macRouter, 255,
		ndp.Message{Type: ndp.NeighborAdvertisement, Target: dst, Solicited: true, Override: true, LinkLayerAddr: learnedMAC, HasLinkLayerAddr: true})
	sw.Forward(fixedTime.Add(time.Second), "1/1/2", na)

	if got := sw.Groups(20); len(got) != 0 {
		t.Errorf("mcast groups on vlan20 after NDP = %+v, want none: NDP must not reach multicast control", got)
	}

	sw.Wake(fixedTime.Add(time.Second))
	emissions := sw.Drain()
	if len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1: NDP observation must have released the held frame", len(emissions))
	}
	if emissions[0].Frame.Dst != learnedMAC {
		t.Errorf("released dst MAC = %v, want %v", emissions[0].Frame.Dst, learnedMAC)
	}
	if emissions[0].Protocol {
		t.Errorf("released frame Protocol = true, want false")
	}
}

// TestMLDReportReachesMulticastPathNotNeighborPath covers the converse of
// TestNDPReachesNeighborPathNotMulticastPath: an MLD report is a multicast
// control candidate carrying a Hop-by-Hop header, so it is learned as
// multicast group state and never reaches [routing.Layer.Observe] — a
// separately held frame on the same switch still times out on schedule,
// proving the report never touched it.
func TestMLDReportReachesMulticastPathNotNeighborPath(t *testing.T) {
	sw := buildIPv6MulticastRoutingSwitch(t)

	src := netip.MustParseAddr("2001:db8:10::7")
	dst := netip.MustParseAddr("2001:db8:20::77")
	pkt := makeIPv6Packet(t, src, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv6, Payload: pkt}

	if held := sw.Forward(fixedTime, "1/1/1", frame); held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	group := netip.MustParseAddr("ff05::1")
	report := makeMLDControlFrame(t, netip.MustParseAddr("fe80::1"), group,
		mcastHostMAC, netaddr.MAC{0x33, 0x33, 0, 0, 0, 1}, 1, true,
		mld.Message{Type: mld.ReportV1, Group: group})
	sw.Forward(fixedTime.Add(time.Second), "1/1/1", report)

	if got := sw.Groups(10); len(got) != 1 {
		t.Fatalf("mcast groups on vlan10 after MLD report = %+v, want the reported group learned", got)
	}

	sw.Wake(fixedTime.Add(3 * time.Second))
	if emissions := sw.Drain(); len(emissions) != 0 {
		t.Errorf("emissions = %d, want 0", len(emissions))
	}
	failures := sw.DrainNeighborFailures()
	if len(failures) != 1 {
		t.Fatalf("neighbor failures = %d, want 1: the MLD report must not have resolved the held entry", len(failures))
	}
}

// TestNDPSolicitationResolvesItsOwnSourceNotTheTarget covers RFC 4861
// section 7.2.3: a Neighbor Solicitation's Source Link-Layer Address option
// names the solicitor's own address, carried in the IPv6 header's source,
// not the Target field the solicitation is asking about. The switch holds a
// frame for heldDst and a separate frame for solicitorAddr. An unrelated
// host at solicitorAddr then solicits heldDst, carrying its own MAC in the
// SLLA option. That must refresh only the entry for solicitorAddr — the
// solicitation's actual source — releasing the frame held for it; the frame
// held for heldDst must still be pending, and later time out, because a
// solicitation asking about an address is not evidence of who holds it.
func TestNDPSolicitationResolvesItsOwnSourceNotTheTarget(t *testing.T) {
	sw := buildIPv6MulticastRoutingSwitch(t)

	src := netip.MustParseAddr("2001:db8:10::7")
	heldDst := netip.MustParseAddr("2001:db8:20::77")
	heldPkt := makeIPv6Packet(t, src, heldDst, 64, []byte("target"))
	heldFrame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv6, Payload: heldPkt}
	if held := sw.Forward(fixedTime, "1/1/1", heldFrame); held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	solicitorAddr := netip.MustParseAddr("2001:db8:20::7")
	solicitorMAC := netaddr.MAC{0x02, 0, 0, 0, 0x10, 0x07}
	solicitorPkt := makeIPv6Packet(t, src, solicitorAddr, 64, []byte("solicitor"))
	solicitorFrame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv6, Payload: solicitorPkt}
	if held := sw.Forward(fixedTime.Add(time.Millisecond), "1/1/1", solicitorFrame); held.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", held.Outcome)
	}

	// The solicitation's own IP destination is the router's interface
	// address, as a real solicited-node multicast one would resolve to on
	// this link: it must not be either held address, or the switch's
	// ordinary forwarding of the NDP packet itself — observation is a side
	// effect, not an interception — would queue a second frame on whichever
	// entry it named and confuse the counts below.
	ns := makeNDPFrame(t, solicitorAddr, netip.MustParseAddr("2001:db8:20::1"), solicitorMAC, macRouter, 255,
		ndp.Message{Type: ndp.NeighborSolicitation, Target: heldDst, LinkLayerAddr: solicitorMAC, HasLinkLayerAddr: true})
	sw.Forward(fixedTime.Add(2*time.Millisecond), "1/1/2", ns)

	emissions := sw.Drain()
	if len(emissions) != 1 {
		t.Fatalf("emissions = %d, want 1: the solicitation must resolve its own source address, not the target it asked about", len(emissions))
	}
	if emissions[0].Frame.Dst != solicitorMAC {
		t.Errorf("released dst MAC = %v, want %v: the solicitor's own held frame must be the one released", emissions[0].Frame.Dst, solicitorMAC)
	}
	hdr, _, err := ip.Decode(emissions[0].Frame.Payload)
	if err != nil {
		t.Fatalf("decode released IP payload: %v", err)
	}
	if hdr.Dst != solicitorAddr {
		t.Errorf("released packet dst = %s, want %s: the target's held frame must not have been the one released", hdr.Dst, solicitorAddr)
	}

	sw.Wake(fixedTime.Add(3 * time.Second))
	if emissions := sw.Drain(); len(emissions) != 0 {
		t.Errorf("emissions at timeout = %d, want 0", len(emissions))
	}
	failures := sw.DrainNeighborFailures()
	if len(failures) != 1 {
		t.Fatalf("neighbor failures = %d, want 1: the frame held for the solicitation's target must never have resolved", len(failures))
	}
}

// buildHeldEgressSwitch builds a switch with both egress shapes a released held frame can take:
// vlan20, an SVI over 1/1/2, and rp1, a routed port on 1/1/3 whose MTU is small enough that a
// test can build a frame the port refuses. depth bounds each neighbor's hold queue.
// Nothing is configured for the next hops on purpose — every test below needs the frame held
// first, which only an unresolved neighbor produces.
func buildHeldEgressSwitch(t *testing.T, depth int) *vswitch.Switch {
	t.Helper()
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: 600}))

	return mustSwitch(t, vswitch.Config{
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
						"rp1":    {Port: "1/1/3", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
					},
					NeighborPolicy: routing.NeighborPolicy{HoldDepth: depth},
				},
			},
		},
	})
}

// holdFrameToward forwards a frame from 1/1/1 toward dst and asserts it was held.
func holdFrameToward(t *testing.T, sw *vswitch.Switch, at time.Time, dst netip.Addr, payload []byte) {
	t.Helper()
	pkt := makeIPv4Packet(t, ipH1, dst, 64, payload)
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}
	if res := sw.Forward(at, "1/1/1", frame); res.Outcome != trace.Held {
		t.Fatalf("outcome for %s = %v, want Held", dst, res.Outcome)
	}
}

// TestReleaseOntoRefusingRoutedPortRecordsThePortsOwnReason covers a released held frame the
// port table refuses: the record must carry the refusal the port gave, not neighbor-miss, which
// would say the next hop never answered when it answered a moment ago. The frame is too large
// for 1/1/3 rather than the port being down, because a down port could not have carried the ARP
// reply that resolved the neighbor in the first place — the observing Forward is the call that
// releases, so the egress has to be refusing at that moment.
func TestReleaseOntoRefusingRoutedPortRecordsThePortsOwnReason(t *testing.T) {
	sw := buildHeldEgressSwitch(t, 3)

	dst := netip.MustParseAddr("10.0.30.77")
	holdFrameToward(t, sw, fixedTime, dst, make([]byte, 1000))

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x30, 0x77}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.30.1"), learnedMAC, macRouter)
	sw.Forward(fixedTime.Add(time.Second), "1/1/3", reply)

	if emissions := sw.Drain(); len(emissions) != 0 {
		t.Fatalf("emissions = %d, want 0: 1/1/3 has an MTU of 600", len(emissions))
	}
	drops := sw.DrainNeighborFailures()
	if len(drops) != 1 {
		t.Fatalf("neighbor drops = %d, want 1: %+v", len(drops), drops)
	}
	if drops[0].Reason != port.ReasonMTUExceeded {
		t.Errorf("reason = %v, want %v, the port table's own refusal", drops[0].Reason, port.ReasonMTUExceeded)
	}
	if drops[0].Port != "1/1/3" {
		t.Errorf("port = %q, want 1/1/3", drops[0].Port)
	}
	if want := trace.RuleID("port.status." + string(port.ReasonMTUExceeded)); drops[0].Step.RuleID != want {
		t.Errorf("rule = %v, want %v: the step and the reason must agree", drops[0].Step.RuleID, want)
	}
}

// TestReleaseOntoSVIWithNoSelectableMemberRecordsTheBridgesReason covers a released frame whose
// SVI egress is a LAG that fails member selection: the bridge's selectMember records its own
// [bridge.ReasonNoMember] entry, naming the LAG, and releaseHeldFrame carries that reason and
// that port name onto the [NeighborDrop] unchanged, rather than reporting a reason of its own
// invention.
//
// The shape: vlan20's member is a LAG whose LACP never converged, so the aggregation forwards —
// its member links are up — but distributes to nothing. The advertisement arrives on 1/1/5, a
// second vlan20 port, and carries a different Ethernet source from the address it advertises, so
// the forwarding database keeps pointing the destination at the LAG. Nothing simpler reaches
// this branch: the observing Forward is the call that releases, so the neighbor has to resolve
// over a live port while the egress the frame would take is already refusing.
func TestReleaseOntoSVIWithNoSelectableMemberRecordsTheBridgesReason(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}).
		Add(port.Port{Name: "1/1/5", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
					"lag1":  {PVID: &p20, Untagged: []vlan.ID{20}},
					"1/1/5": {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			},
		},
		LAG: &lag.Config{LAGs: map[string]lag.LAG{
			// LACP Active with no partner ever answering: the members stay out of the
			// distributing set, so Select finds nothing to hash onto.
			"lag1": {LACP: lag.LACPConfig{Mode: lag.Active}, Members: map[string]lag.Member{"1/1/2": {}}},
		}},
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
	})

	dst := netip.MustParseAddr("10.0.20.77")
	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	if err := sw.Learn([]bridge.Seed{{FID: 20, MAC: learnedMAC, Port: "lag1", Lifetime: bridge.Static}}); err != nil {
		t.Fatalf("Learn: %v", err)
	}

	holdFrameToward(t, sw, fixedTime, dst, []byte("svi"))

	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	reply.Src = netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x01}
	sw.Forward(fixedTime.Add(time.Second), "1/1/5", reply)

	for _, em := range sw.Drain() {
		if em.Frame.Dst == learnedMAC {
			t.Fatalf("the held frame egressed on %s, want no emission: lag1 distributes to nothing", em.Port)
		}
	}
	drops := sw.DrainNeighborFailures()
	if len(drops) != 1 {
		t.Fatalf("neighbor drops = %d, want 1: %+v", len(drops), drops)
	}
	if drops[0].Reason != bridge.ReasonNoMember {
		t.Errorf("reason = %v, want %v, the bridge's own answer", drops[0].Reason, bridge.ReasonNoMember)
	}
	if drops[0].Port != "lag1" {
		t.Errorf("port = %q, want lag1, the aggregation that refused it", drops[0].Port)
	}
	if drops[0].Step.RuleID != trace.RuleID(drops[0].Reason) {
		t.Errorf("rule = %v, want %v: the step and the reason must agree", drops[0].Step.RuleID, drops[0].Reason)
	}
}

// TestReleaseOntoFloodVLANWithNoMemberRecordsTheBridgesReason covers the arm
// releaseHeldFrame's len(res.Egress) == 0 guard exists for: vlan20 is a
// flood VLAN with no switchport carrying it at all, so [bridge.Bridge.Egress]
// never reaches a candidate port and returns with no Egress entry, naming
// the reason on the result instead — the same shape
// TestReleaseOntoSVIWithNoSelectableMemberRecordsTheBridgesReason covers for
// a LAG that fails member selection, but reached through the
// candidates-stay-empty branch of replicate rather than through
// selectMember, which always records an Egress entry of its own even when
// it fails.
//
// The ARP reply that resolves the neighbor arrives tagged for VID 20 on
// 1/1/2, a port with no switchport entry at all: a tagged frame classifies
// by its own VID regardless of switchport membership, which is the only way
// to attribute the observation to vlan20's interface without also handing
// the release a live vlan20 port to flood onto.
func TestReleaseOntoFloodVLANWithNoMemberRecordsTheBridgesReason(t *testing.T) {
	p10 := vlan.ID(10)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	sw := mustSwitch(t, vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
				},
			},
			FloodVLANs: []vlan.ID{20},
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
	})

	dst := netip.MustParseAddr("10.0.20.77")
	holdFrameToward(t, sw, fixedTime, dst, []byte("flood-vlan"))

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	reply.Tags = []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 20}}
	sw.Forward(fixedTime.Add(time.Second), "1/1/2", reply)

	for _, em := range sw.Drain() {
		if em.Frame.Dst == learnedMAC {
			t.Fatalf("the held frame egressed on %s, want no emission: vlan20 has no member port", em.Port)
		}
	}
	drops := sw.DrainNeighborFailures()
	if len(drops) != 1 {
		t.Fatalf("neighbor drops = %d, want 1: %+v", len(drops), drops)
	}
	if drops[0].Reason != bridge.ReasonNoEgress {
		t.Errorf("reason = %v, want %v, the bridge's own answer", drops[0].Reason, bridge.ReasonNoEgress)
	}
	if drops[0].Port != "" {
		t.Errorf("port = %q, want empty: no port was chosen", drops[0].Port)
	}
	if drops[0].Step.RuleID != trace.RuleID(drops[0].Reason) {
		t.Errorf("rule = %v, want %v: the step and the reason must agree", drops[0].Step.RuleID, drops[0].Reason)
	}
}

// TestEvictedHeldFrameIsNotReportedAsANeighborMiss covers a frame the hold queue pushed out to
// make room. It is not a neighbor that failed to answer — in this very run the neighbor answers,
// and the two frames behind it release on the same Wake.
func TestEvictedHeldFrameIsNotReportedAsANeighborMiss(t *testing.T) {
	sw := buildHeldEgressSwitch(t, 2)

	dst := netip.MustParseAddr("10.0.20.77")
	for i, payload := range [][]byte{[]byte("first"), []byte("second"), []byte("third")} {
		holdFrameToward(t, sw, fixedTime.Add(time.Duration(i)*time.Millisecond), dst, payload)
	}

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x20, 0x77}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	sw.Forward(fixedTime.Add(time.Second), "1/1/2", reply)

	var released int
	for _, em := range sw.Drain() {
		if em.Frame.Dst == learnedMAC {
			released++
		}
	}
	if released != 2 {
		t.Fatalf("released frames = %d, want 2: the two frames the queue kept", released)
	}
	drops := sw.DrainNeighborFailures()
	if len(drops) != 1 {
		t.Fatalf("neighbor drops = %d, want 1: %+v", len(drops), drops)
	}
	if drops[0].Reason != routing.ReasonNeighborHoldOverflow {
		t.Errorf("reason = %v, want %v", drops[0].Reason, routing.ReasonNeighborHoldOverflow)
	}
	if drops[0].Step.RuleID != trace.RuleID(routing.ReasonNeighborHoldOverflow) {
		t.Errorf("rule = %v, want %v", drops[0].Step.RuleID, routing.ReasonNeighborHoldOverflow)
	}
	if drops[0].Port != "" {
		t.Errorf("port = %q, want empty: vlan20 is an SVI, which owns no single port", drops[0].Port)
	}
}

// TestReleasedFrameCarriesIngressPriorityOntoEgress covers the half
// TestReleasedFrameCarriesIngressPCPAndDEI could not see. That test releases onto a tagged port,
// where the priority survives in the egress tag. On an untagged access port and on a routed port
// there is no tag to carry it, so the priority has to travel on the Emission itself — a reader
// deriving it from the frame gets 0, and the frame queues behind traffic the live, non-held path
// would have overtaken. The sub-interface row has it both ways, on the tag it prepends and on
// the Emission, and each release is compared against the live path through the same interface.
func TestReleasedFrameCarriesIngressPriorityOntoEgress(t *testing.T) {
	p10 := vlan.ID(10)
	p20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	newSwitch := func(t *testing.T, neighbors []routing.Neighbor) *vswitch.Switch {
		t.Helper()
		return mustSwitch(t, vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Tagged: []vlan.ID{10}},
					"1/1/2": {PVID: &p20, Untagged: []vlan.ID{20}},
				},
			}},
			Routing: &routing.Config{VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						"vlan20": {VLAN: 20, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						"rp1":    {Port: "1/1/3", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
						"rp2.40": {Port: "1/1/4", VLAN: 40, MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.40.1/24")}},
					},
					Neighbors: neighbors,
				},
			}},
		})
	}

	// The held and the live frame take the same route to the same next hop; only the order of
	// the advertisement and the frame differs, so any divergence is the hold.
	taggedFrame := func(t *testing.T, dst netip.Addr) ethernet.Frame {
		t.Helper()
		return ethernet.Frame{
			Src:       macH1,
			Dst:       macRouter,
			EtherType: ethernet.EtherTypeIPv4,
			Tags:      []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), PCP: 5, DEI: true, VID: 10}},
			Payload:   makeIPv4Packet(t, ipH1, dst, 64, []byte("priority")),
		}
	}

	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0x30, 0x77}
	subTag := vlan.Tag{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 40, PCP: 5, DEI: true}
	for _, tc := range []struct {
		name      string
		iface     string
		dst       netip.Addr
		gateway   netip.Addr
		replyOn   string
		wantEgres string
		wantTags  []vlan.Tag
		replyTags []vlan.Tag
	}{
		{"untagged access port", "vlan20", netip.MustParseAddr("10.0.20.77"), netip.MustParseAddr("10.0.20.1"), "1/1/2", "1/1/2", nil, nil},
		{"routed port", "rp1", netip.MustParseAddr("10.0.30.77"), netip.MustParseAddr("10.0.30.1"), "1/1/3", "1/1/3", nil, nil},
		// The sub-interface's reply arrives on its parent port, where only its
		// own C-TAG names it.
		{"sub-interface", "rp2.40", netip.MustParseAddr("10.0.40.77"), netip.MustParseAddr("10.0.40.1"), "1/1/4", "1/1/4", []vlan.Tag{subTag}, []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 40}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held := newSwitch(t, nil)
			if res := held.Forward(fixedTime, "1/1/1", taggedFrame(t, tc.dst)); res.Outcome != trace.Held {
				t.Fatalf("outcome = %v (%v), want Held; steps=%+v", res.Outcome, res.Reason, res.Steps)
			}
			reply := makeARPReply(t, tc.dst, tc.gateway, learnedMAC, macRouter)
			reply.Tags = tc.replyTags
			held.Forward(fixedTime.Add(time.Second), tc.replyOn, reply)
			heldEmission := soleDataEmission(t, held.Drain(), learnedMAC)

			// The baseline is a switch that never held: the binding is configured, so the same
			// frame forwards straight through and its priority rides res.Egress, which is what
			// the fabric transmits on the live path. Observe alone would not do — an
			// advertisement for an address nothing routed to creates no entry.
			live := newSwitch(t, []routing.Neighbor{{Interface: tc.iface, Addr: tc.dst, MAC: learnedMAC}})
			res := live.Forward(fixedTime, "1/1/1", taggedFrame(t, tc.dst))
			if res.Outcome == trace.Held || res.Outcome == trace.Dropped {
				t.Fatalf("live outcome = %v (%v), want the frame on its way: the binding is configured", res.Outcome, res.Reason)
			}
			if len(res.Egress) != 1 {
				t.Fatalf("live egress = %+v, want exactly one entry", res.Egress)
			}

			if heldEmission.PCP != res.Egress[0].PCP {
				t.Errorf("held PCP = %d, live PCP = %d: a hold must not change the priority", heldEmission.PCP, res.Egress[0].PCP)
			}
			if heldEmission.PCP != 5 {
				t.Errorf("released PCP = %d, want 5, the priority the frame arrived with", heldEmission.PCP)
			}
			if !slices.Equal(heldEmission.Frame.Tags, tc.wantTags) {
				t.Errorf("released tags = %+v, want %+v", heldEmission.Frame.Tags, tc.wantTags)
			}
			if !slices.Equal(heldEmission.Frame.Tags, res.Egress[0].Frame.Tags) {
				t.Errorf("held tags = %+v, live tags = %+v: a hold must not change what reaches the wire",
					heldEmission.Frame.Tags, res.Egress[0].Frame.Tags)
			}
			if heldEmission.Port != tc.wantEgres {
				t.Errorf("released port = %q, want %q", heldEmission.Port, tc.wantEgres)
			}
		})
	}
}

// soleDataEmission returns the one emission addressed to dst, failing when there is not exactly
// one: a Forward that observes an advertisement also relays the advertisement itself.
func soleDataEmission(t *testing.T, emissions []vswitch.Emission, dst netaddr.MAC) vswitch.Emission {
	t.Helper()
	var found []vswitch.Emission
	for _, em := range emissions {
		if em.Frame.Dst == dst {
			found = append(found, em)
		}
	}
	if len(found) != 1 {
		t.Fatalf("emissions to %s = %d, want exactly 1: %+v", dst, len(found), emissions)
	}
	return found[0]
}
