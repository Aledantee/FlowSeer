package vswitch_test

import (
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

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
				Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up,
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
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "member", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown, LagParent: "lag1"}))
	sw := mustSwitch(t, vswitch.Config{Ports: ports, Bridge: &bridge.Config{}})
	sw.Start(fixedTime)
	if _, err := sw.LinkChange(fixedTime.Add(time.Second), "member", port.Up, vswitch.PointToPointTrue, 1_000_000_000); err != nil {
		t.Fatal(err)
	}
	lagPort, _ := sw.Ports().Port("lag1")
	if got := lagPort.OperStatus; got != port.Up {
		t.Fatalf("oper status after link up = %s, want %s", got, port.Up)
	}
	if _, err := sw.LinkChange(fixedTime.Add(2*time.Second), "member", port.Down, vswitch.PointToPointTrue, 1_000_000_000); err != nil {
		t.Fatal(err)
	}
	lagPort, _ = sw.Ports().Port("lag1")
	if got := lagPort.OperStatus; got != port.Down {
		t.Fatalf("oper status after link down = %s, want %s", got, port.Down)
	}
}

func TestLACPDUHandlingAtSwitch(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	macA := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0a}
	macB := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0b}
	p10 := vlan.ID(10)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}))

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
	captureOutput(callOutputs, sw, sw.Start(now))

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
	if info := sw.MemberInfo("1/1/1"); info.Status != lag.Expired || info.Actor.State&lacp.StateExpired == 0 {
		t.Fatalf("member after Start = %+v, want Expired", info)
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

	// Receive valid LACPDU on 1/1/1 (member port): Consumed with lag layer step and LACPDUsRx = 1
	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", validFrame))
	if res.Outcome != trace.Consumed {
		t.Errorf("res.Outcome = %s, want Consumed", res.Outcome)
	}
	if len(res.Steps) == 0 || res.Steps[0].Layer != lag.LayerName || res.Steps[0].Op != trace.OpClassify || res.Steps[0].RuleID != "lag.lacpdu.admit" {
		t.Errorf("res.Steps = %+v, want step with LayerLAG, OpClassify, rule lag.lacpdu.admit", res.Steps)
	}
	if !traceHasFactType(res.Steps, "lag.lacp_decision") {
		t.Errorf("LACP trace has no member decision fact: %+v", res.Steps)
	}
	if info := sw.MemberInfo("1/1/1"); info.LACPDUsRx != 1 {
		t.Errorf("after Receive, LACPDUsRx = %d, want 1", info.LACPDUsRx)
	}
	if copies := takeCopies(callOutputs, sw); len(copies) != 1 || copies[0].Mirror != "control" || copies[0].Port != "1/1/4" {
		t.Errorf("output copies = %+v, want received LACPDU mirrored to 1/1/4", copies)
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

	resBad := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", badFrame))
	if resBad.Outcome != trace.Dropped {
		t.Errorf("resBad.Outcome = %s, want Dropped", resBad.Outcome)
	}
	if resBad.Reason != lag.ReasonUnsupportedLACPDU {
		t.Errorf("resBad.Reason = %s, want %s", resBad.Reason, lag.ReasonUnsupportedLACPDU)
	}
	if info := sw.MemberInfo("1/1/1"); info.BadLACPDUs != 1 {
		t.Errorf("after Receive bad, BadLACPDUs = %d, want 1", info.BadLACPDUs)
	}
	for _, out := range []vswitch.Output{peekBad, resBad} {
		want := lag.LACPDecodeFact(badFrame, false, lag.ReasonUnsupportedLACPDU)
		if len(out.Steps) == 0 || len(out.Steps[0].Inputs) != 1 || !trace.EqualFact(out.Steps[0].Inputs[0], want) {
			t.Errorf("refused LACP inputs = %+v, want LACP decode fact", out.Steps)
		}
	}
	badMarker := badFrame
	badMarker.Payload = []byte{lacp.SubtypeMarker}
	for _, out := range []vswitch.Output{sw.Peek(now, "1/1/1", badMarker), sw.Receive(now, "1/1/1", badMarker)} {
		want := lag.MarkerDecodeFact(badMarker, false, lag.ReasonUnsupportedLACPDU)
		if out.Outcome != trace.Dropped || out.Reason != lag.ReasonUnsupportedLACPDU || len(out.Steps) == 0 || len(out.Steps[0].Inputs) != 1 || !trace.EqualFact(out.Steps[0].Inputs[0], want) {
			t.Errorf("refused Marker = %+v, want Marker decode fact", out)
		}
	}

	// LACPDU injected at 1/1/3 (no LAG): dropped reserved-address
	resNonMember := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/3", validFrame))
	if resNonMember.Outcome != trace.Dropped {
		t.Errorf("resNonMember.Outcome = %s, want Dropped", resNonMember.Outcome)
	}
	if resNonMember.Reason != bridge.ReasonReservedAddress {
		t.Errorf("resNonMember.Reason = %s, want %s", resNonMember.Reason, bridge.ReasonReservedAddress)
	}
	captureOutput(callOutputs, sw, sw.Advance(now.Add(90*time.Second-time.Nanosecond)))
	if info := sw.MemberInfo("1/1/1"); info.Status != lag.Current {
		t.Fatalf("member before slow receive timeout = %+v, want Current despite peer Short timeout", info)
	}
	captureOutput(callOutputs, sw, sw.Advance(now.Add(90*time.Second)))
	if info := sw.MemberInfo("1/1/1"); info.Status != lag.Expired || info.Enabled || info.Actor.State&lacp.StateExpired == 0 {
		t.Fatalf("member at slow receive timeout = %+v, want Expired and disabled", info)
	}
	captureOutput(callOutputs, sw, sw.Advance(now.Add(93*time.Second)))
	if info := sw.MemberInfo("1/1/1"); info.Status != lag.Defaulted || info.Partner != (lacp.Info{State: 0x18}) || info.Actor.State&0xc0 != 0x40 {
		t.Fatalf("member after Expired timeout = %+v, want Defaulted with administrative Partner", info)
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
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}))

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

	res := sw.Receive(now, "1/1/3", frame)
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
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}))

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
	if _, err := sw.LinkChange(now, "1/1/1", port.Down, vswitch.PointToPointTrue, 10_000_000_000); err != nil {
		t.Fatal(err)
	}

	mem, ok = sw.SelectMember(now, "lag1", dummyFrame, 10)
	if !ok || mem != "1/1/2" {
		t.Fatalf("after link down, SelectMember = (%q, %t), want (1/1/2, true)", mem, ok)
	}

	roles = sw.Roles()
	if roles["lag1"].PathCost != 20000 {
		t.Errorf("after link down, lag1 PathCost = %d, want 20000 (1 Gbps)", roles["lag1"].PathCost)
	}
	// Both members down: lag1 has no enabled member
	if _, err := sw.LinkChange(now, "1/1/2", port.Down, vswitch.PointToPointTrue, 1_000_000_000); err != nil {
		t.Fatal(err)
	}
	mem, ok = sw.SelectMember(now, "lag1", dummyFrame, 10)
	if ok {
		t.Fatalf("after all links down, SelectMember returned %q, want false", mem)
	}
	p, _ := sw.Ports().Port("lag1")
	if p.OperStatus != port.Down {
		t.Errorf("lag1 OperStatus = %v, want Down", p.OperStatus)
	}
}

// TestLagRowFollowsMemberRowsNotTheDelay proves the LAG row reads Down the
// moment every member row is down, while a down delay still holds the layer's
// enablement until the delay has run from the report.
func TestLagRowFollowsMemberRowsNotTheDelay(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	tbl := mustTable(t, b)
	sw := mustSwitch(t, vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		LAG:    &lag.Config{LAGs: map[string]lag.LAG{"lag1": {DownDelay: time.Second}}},
	})
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	sw.Start(t0)
	if _, err := sw.LinkChange(t0, "1/1/1", port.Down, vswitch.PointToPointTrue, 0); err != nil {
		t.Fatalf("LinkChange: %v", err)
	}
	p, _ := sw.Ports().Port("lag1")
	if p.OperStatus != port.Down {
		t.Fatalf("lag1 OperStatus = %v, want Down as soon as its member row is down", p.OperStatus)
	}
	sw.Advance(t0.Add(time.Second - time.Nanosecond))
	if !sw.MemberInfo("1/1/1").LinkUp {
		t.Fatal("the layer dropped the member before its down delay")
	}
	sw.Advance(t0.Add(time.Second))
	if sw.MemberInfo("1/1/1").LinkUp {
		t.Error("the layer kept the member after its down delay")
	}
}

// balancedLAGConfig builds a switch with one flat (untagged, VLAN-unaware)
// ingress port "in" and a two-member BalanceSLB lag1, so a flooded frame
// always exercises LAG member selection.
func balancedLAGConfig(t *testing.T, rebalanceInterval *time.Duration) vswitch.Config {
	t.Helper()

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
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

// TestReceiveCommitsLAGSelectionPeekDoesNot proves that
// Peek does not commit a bucket assignment or rotate the enabled list, so a
// Receive that follows two Peeks of the same frame reaches the same
// selector state a lone Receive on a fresh switch would.
func TestReceiveCommitsLAGSelectionPeekDoesNot(t *testing.T) {
	cfg := balancedLAGConfig(t, nil)
	frame := ethernet.Frame{Dst: netaddr.MAC{0x02, 0, 0, 0, 0, 9}, Src: netaddr.MAC{0x02, 0, 0, 0, 0, 1}}

	peeked := mustSwitch(t, cfg)
	peeked.Peek(fixedTime, "in", frame)
	peeked.Peek(fixedTime, "in", frame)
	peekedResult := peeked.Receive(fixedTime, "in", frame)

	fresh := mustSwitch(t, cfg)
	freshResult := fresh.Receive(fixedTime, "in", frame)

	if !reflect.DeepEqual(peekedResult.Result, freshResult.Result) {
		t.Errorf("Peek committed LAG selection state:\npeeked = %+v\nfresh  = %+v", peekedResult.Result, freshResult.Result)
	}
}

// TestReceiveRaisesLAGRebalanceUnmodeled proves that a
// balanced selection whose bucket was assigned at least one rebalance
// interval ago carries lag-rebalance-unmodeled, and a zero RebalanceInterval
// disables the signal entirely.
func TestReceiveRaisesLAGRebalanceUnmodeled(t *testing.T) {
	frame := ethernet.Frame{Dst: netaddr.MAC{0x02, 0, 0, 0, 0, 9}, Src: netaddr.MAC{0x02, 0, 0, 0, 0, 1}}
	t0 := fixedTime
	ten := 10 * time.Second

	sw := mustSwitch(t, balancedLAGConfig(t, &ten))
	sw.Receive(t0, "in", frame)

	at9 := sw.Receive(t0.Add(9*time.Second), "in", frame)
	if at9.Metadata.Status() != analysis.Complete {
		t.Errorf("status at t0+9s = %v, want Complete: issues=%+v", at9.Metadata.Status(), at9.Metadata.Issues())
	}

	at10 := sw.Receive(t0.Add(10*time.Second), "in", frame)
	if at10.Metadata.Status() != analysis.Incomplete {
		t.Errorf("status at t0+10s = %v, want Incomplete", at10.Metadata.Status())
	}
	if !hasIssueCode(at10.Metadata.Issues(), vswitch.IssueLAGRebalanceUnmodeled) {
		t.Errorf("issues at t0+10s = %+v, want lag-rebalance-unmodeled", at10.Metadata.Issues())
	}

	zero := time.Duration(0)
	swDisabled := mustSwitch(t, balancedLAGConfig(t, &zero))
	swDisabled.Receive(t0, "in", frame)
	for _, at := range []time.Time{t0.Add(9 * time.Second), t0.Add(10 * time.Second)} {
		res := swDisabled.Receive(at, "in", frame)
		if res.Metadata.Status() != analysis.Complete {
			t.Errorf("RebalanceInterval=0 at %v: status = %v, want Complete", at, res.Metadata.Status())
		}
	}
}

// TestRebalanceIssueScopedToJourneysThroughBalancedLAG proves that the
// lag-rebalance-unmodeled issue names only the journeys that actually used
// the balanced LAG, not every journey a switch with a stale bucket forwards.
func TestRebalanceIssueScopedToJourneysThroughBalancedLAG(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
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
	sw.Receive(t0, "in", lagFrame)

	lagRes := sw.Receive(t0.Add(10*time.Second), "in", lagFrame)
	if !hasIssueCode(lagRes.Metadata.Issues(), vswitch.IssueLAGRebalanceUnmodeled) {
		t.Fatalf("journey through lag1 issues = %+v, want lag-rebalance-unmodeled", lagRes.Metadata.Issues())
	}

	outRes := sw.Receive(t0.Add(10*time.Second), "in", outFrame)
	if hasIssueCode(outRes.Metadata.Issues(), vswitch.IssueLAGRebalanceUnmodeled) {
		t.Errorf("unrelated journey through out issues = %+v, want no lag-rebalance-unmodeled", outRes.Metadata.Issues())
	}
}

func TestFailedLAGMemberIngressReportsBothStatesAndDecisivePort(t *testing.T) {
	deviceMAC := netaddr.MAC{2, 0, 0, 0, 0, 1}
	frame := ethernet.Frame{Src: netaddr.MAC{2, 0, 0, 0, 0, 2}, Dst: deviceMAC}
	bpdu := mustEncode(t, bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: stp.DefaultBridgePriority, Address: deviceMAC},
		BridgeID:     bpdu.BridgeID{Priority: stp.DefaultBridgePriority, Address: deviceMAC},
		PortID:       0x8001,
		HelloTime:    stp.DefaultHelloTime,
		MaxAge:       stp.DefaultMaxAge,
		ForwardDelay: stp.DefaultForwardDelay,
	}, deviceMAC)

	for _, state := range []struct {
		name       string
		memberOper port.LinkState
		parentOper port.LinkState
		decisive   string
		factNames  []string
	}{
		{name: "member down", memberOper: port.Down, parentOper: port.Up, decisive: "member", factNames: []string{"member"}},
		{name: "parent down", memberOper: port.Up, parentOper: port.Down, decisive: "lag1", factNames: []string{"member", "lag1"}},
	} {
		for _, pipeline := range []struct {
			name  string
			frame ethernet.Frame
			cfg   func(port.Table) vswitch.Config
		}{
			{
				name:  "bridge",
				frame: frame,
				cfg: func(ports port.Table) vswitch.Config {
					return vswitch.Config{MAC: deviceMAC, Ports: ports, Bridge: &bridge.Config{}}
				},
			},
			{
				name:  "routed",
				frame: frame,
				cfg: func(ports port.Table) vswitch.Config {
					return vswitch.Config{
						MAC: deviceMAC, Ports: ports,
						Routing: &routing.Config{VRFs: map[string]routing.VRF{
							routing.DefaultVRF: {Interfaces: map[string]routing.Interface{
								"lag1": {Port: "lag1", Prefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")}},
							}},
						}},
					}
				},
			},
			{
				name:  "bpdu",
				frame: bpdu,
				cfg: func(ports port.Table) vswitch.Config {
					return vswitch.Config{
						MAC: deviceMAC, Ports: ports, Bridge: &bridge.Config{},
						STP: &stp.Config{Address: deviceMAC, Ports: map[string]stp.Port{"lag1": {}}},
					}
				},
			},
		} {
			t.Run(pipeline.name+"/"+state.name, func(t *testing.T) {
				ports := mustTable(t, port.NewBuilder().
					Add(port.Port{Name: "member", LagParent: "lag1", AdminStatus: port.Up, OperStatus: state.memberOper}).
					Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: state.parentOper}))
				res := mustSwitch(t, pipeline.cfg(ports)).Receive(fixedTime, "member", pipeline.frame)
				if res.Outcome != trace.Dropped || res.Reason != port.ReasonPortDown {
					t.Fatalf("result = %s/%s, want Dropped/port-down", res.Outcome, res.Reason)
				}
				if len(res.Steps) == 0 || res.Steps[0].Subject != (trace.Subject{Kind: "port", Key: state.decisive}) {
					t.Fatalf("steps = %+v, want decisive port %q", res.Steps, state.decisive)
				}

				facts := append(slices.Clone(res.Steps[0].Inputs), res.Steps[0].Outputs...)
				for _, name := range state.factNames {
					if !slices.ContainsFunc(facts, func(fact trace.Fact) bool {
						return fact.TypeID() == "port.forwarding" && strings.Contains(fact.Canonical(), `name="`+name+`"`)
					}) {
						t.Errorf("decision facts = %+v, want state for %q", facts, name)
					}
				}
			})
		}
	}
}

func lagDeriveConfig(t *testing.T, admin, oper port.LinkState) vswitch.Config {
	t.Helper()

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "member", Kind: port.Physical, LagParent: "lag1", AdminStatus: admin, OperStatus: oper}))

	return vswitch.Config{
		Ports: ports,
		LAG: &lag.Config{LAGs: map[string]lag.LAG{
			"lag1": {Members: map[string]lag.Member{"member": {}}},
		}},
	}
}
