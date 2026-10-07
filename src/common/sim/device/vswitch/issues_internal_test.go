package vswitch

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func issueCallSwitch(t *testing.T) *Switch {
	t.Helper()
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).Build()
	if err != nil {
		t.Fatal(err)
	}
	p10, p20 := vlan.ID(10), vlan.ID(20)
	mac := netaddr.MAC{2, 0, 0, 0, 0, 1}
	sw, err := New(Config{
		Ports: ports,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "ten", 20: "twenty"},
			Switchports: map[string]bridge.Switchport{
				"in": {PVID: &p10, Untagged: []vlan.ID{10}}, "out": {PVID: &p20, Untagged: []vlan.ID{20}},
			},
		}},
		Routing: &routing.Config{VRFs: map[string]routing.VRF{
			"default": {
				Interfaces: map[string]routing.Interface{
					"ten":    {VLAN: 10, MAC: mac, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
					"twenty": {VLAN: 20, MAC: mac, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
				},
				Routes: []routing.Route{{Prefix: netip.MustParsePrefix("239.1.2.3/32"), Interface: "twenty", NextHop: netip.MustParseAddr("10.0.20.77")}},
			},
		}},
		Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{20: {RouterPorts: []string{"out"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return sw
}

func issueCallFrame(t *testing.T, dst netip.Addr) ethernet.Frame {
	t.Helper()
	payload, err := (ip.Header{Src: netip.MustParseAddr("10.0.10.7"), Dst: dst, HopLimit: 64, Protocol: 17, V4: &ip.V4{}}).Encode([]byte("held"))
	if err != nil {
		t.Fatal(err)
	}
	return ethernet.Frame{Src: netaddr.MAC{2, 0, 0, 0, 0, 7}, Dst: netaddr.MAC{2, 0, 0, 0, 0, 1}, EtherType: ethernet.EtherTypeIPv4, Payload: payload}
}

func callHasIssue(res ForwardResult, code analysis.IssueCode) bool {
	return slices.ContainsFunc(res.Metadata.Issues(), func(issue analysis.Issue) bool { return issue.Code == code })
}

func TestPolicedDropCarriesNoEarlierIssue(t *testing.T) {
	sw := issueCallSwitch(t)
	held := sw.Forward(time.Unix(100, 0), "in", issueCallFrame(t, netip.MustParseAddr("10.0.20.77")))
	if held.Outcome != trace.Held || !callHasIssue(held, IssueNeighborUnresolved) {
		t.Fatalf("held result = %+v, issues = %+v, want Held and neighbor-unresolved", held.Result, held.Metadata.Issues())
	}
	drop := sw.ComposeForwardResult(bridge.Result{Trace: trace.Trace{Outcome: trace.Dropped, Reason: traffic.ReasonPoliced}, Ingress: "out"}, "out")
	if drop.Outcome != trace.Dropped || drop.Reason != traffic.ReasonPoliced || callHasIssue(drop, IssueNeighborUnresolved) {
		t.Errorf("policed result = %+v, issues = %+v, want policed drop without neighbor-unresolved", drop.Result, drop.Metadata.Issues())
	}
}

func TestReleaseChargesNoMulticastQueryToTheObservingFrame(t *testing.T) {
	sw := issueCallSwitch(t)
	t0 := time.Unix(100, 0)
	group := netip.MustParseAddr("239.1.2.3")
	source := netip.MustParseAddr("10.0.20.7")
	sw.mcast.Learn(t0, 20, "out", source, igmp.Message{Type: igmp.ReportV2, Group: group})
	sw.mcast.Learn(t0, 20, "out", source, igmp.Message{Type: igmp.Leave, Group: group})
	now := t0.Add(3 * time.Second)
	if _, registered, pending := sw.mcast.Resolve(20, group, source, now); !registered || !pending {
		t.Fatalf("multicast registered/pending = %v/%v, want true/true", registered, pending)
	}
	// The resolved neighbor's MAC is a group address, so release reaches snooping.
	groupMAC := netaddr.MAC{1, 0, 0x5e, 1, 2, 3}
	// The configured route makes the group packet wait for an ordinary next hop.
	held := sw.Forward(now, "in", issueCallFrame(t, group))
	if held.Outcome != trace.Held || !callHasIssue(held, IssueNeighborUnresolved) {
		t.Fatalf("held result = %+v, issues = %+v, want Held and neighbor-unresolved", held.Result, held.Metadata.Issues())
	}
	sw.Drain()
	reply, err := arp.Encode(arp.Message{
		HardwareType: 1, ProtocolType: uint16(ethernet.EtherTypeIPv4), Operation: arp.Reply,
		SenderMAC: groupMAC, SenderAddr: netip.MustParseAddr("10.0.20.77"), TargetMAC: netaddr.MAC{2, 0, 0, 0, 0, 1}, TargetAddr: netip.MustParseAddr("10.0.20.1"),
	}, netaddr.MAC{2, 0, 0, 0, 0, 1})
	if err != nil {
		t.Fatal(err)
	}
	observing := sw.Forward(now.Add(time.Millisecond), "out", reply)
	emissions := sw.Drain()
	if len(emissions) != 1 || emissions[0].Protocol || emissions[0].Port != "out" || emissions[0].Frame.Dst != groupMAC {
		t.Fatalf("release emissions = %+v, want one data frame to group MAC on out", emissions)
	}
	if callHasIssue(observing, IssueMcastQueryUnobserved) {
		t.Errorf("reply issues = %+v, want no mcast-query-unobserved", observing.Metadata.Issues())
	}
}

func TestSelectMemberOutsideACallRaisesNothing(t *testing.T) {
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "bond", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "a", Kind: port.Physical, LagParent: "bond", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "b", Kind: port.Physical, LagParent: "bond", AdminStatus: port.Up, OperStatus: port.Up}).Build()
	if err != nil {
		t.Fatal(err)
	}
	interval := 10 * time.Second
	sw, err := New(Config{Ports: ports, LAG: &lag.Config{LAGs: map[string]lag.LAG{
		"bond": {Mode: lag.BalanceSLB, Members: map[string]lag.Member{"a": {}, "b": {}}, RebalanceInterval: &interval},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	f := issueCallFrame(t, netip.MustParseAddr("10.0.20.77"))
	t0 := time.Unix(100, 0)
	if member, ok := sw.SelectMember(t0, "bond", f, 0); !ok || member == "" {
		t.Fatalf("initial selection = %q/%v, want enabled member", member, ok)
	}
	now := t0.Add(11 * time.Second)
	if sel := sw.lag.Peek(now, "bond", f, 0); !sel.OK || !sel.RebalanceUnmodeled {
		t.Fatalf("aged selection = %+v, want enabled stale balanced selection", sel)
	}
	if member, ok := sw.SelectMember(now, "bond", f, 0); !ok || member == "" {
		t.Fatalf("aged selection = %q/%v, want enabled member", member, ok)
	}
	res := sw.ComposeForwardResult(bridge.Result{Ingress: "in"}, "in")
	if callHasIssue(res, IssueLAGRebalanceUnmodeled) {
		t.Errorf("next frame issues = %+v, want no lag-rebalance-unmodeled", res.Metadata.Issues())
	}
	sw.LinkChange(now, "in", port.Down, PointToPointTrue, 1_000_000_000)
	res = sw.Forward(now, "in", f)
	if res.Outcome != trace.Dropped || res.Reason != port.ReasonPortDown || callHasIssue(res, IssueLAGRebalanceUnmodeled) {
		t.Errorf("next arrival = %+v, issues = %+v, want port-down drop without lag-rebalance-unmodeled", res.Result, res.Metadata.Issues())
	}
}

func TestNoCallOutlivesItsEntry(t *testing.T) {
	now := time.Unix(100, 0)
	f := issueCallFrame(t, netip.MustParseAddr("10.0.20.77"))
	entries := []struct {
		name string
		run  func(*Switch)
	}{
		{"Forward", func(sw *Switch) { sw.Forward(now, "in", f) }},
		{"Peek", func(sw *Switch) { sw.Peek(now, "in", f) }},
		{"ComposeForwardResult", func(sw *Switch) { sw.ComposeForwardResult(bridge.Result{Ingress: "in"}, "in") }},
		{"Age", func(sw *Switch) { sw.Age(now) }},
		{"Wake", func(sw *Switch) { sw.Wake(now) }},
		{"Start", func(sw *Switch) { sw.Start(now) }},
		{"LinkChange", func(sw *Switch) { sw.LinkChange(now, "out", port.Down, PointToPointTrue, 1_000_000_000) }},
		{"Mcheck", func(sw *Switch) { sw.Mcheck(now, "in") }},
		{"SelectMember", func(sw *Switch) { sw.SelectMember(now, "absent", f, 0) }},
	}
	if len(entries) != 9 {
		t.Fatalf("entry cases = %d, want 9", len(entries))
	}
	for _, entry := range entries {
		t.Run(entry.name, func(t *testing.T) {
			sw := issueCallSwitch(t)
			entry.run(sw)
			if sw.call != nil {
				t.Errorf("call after %s = %+v, want nil", entry.name, sw.call)
			}
		})
	}
}
