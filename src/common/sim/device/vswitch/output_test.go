package vswitch_test

import (
	"net/netip"
	"reflect"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestReceiveAdvancesAgedInformationBeforeABPDU(t *testing.T) {
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	self := netaddr.MAC{0, 0, 0, 0, 0, 9}
	peer := netaddr.MAC{0, 0, 0, 0, 0, 2}
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "p1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "p2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "p3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	pvid := vlan.ID(1)
	cfg := vswitch.Config{Ports: tbl, MAC: self, Bridge: &bridge.Config{VLAN: &bridge.VLAN{
		Table:       map[vlan.ID]string{1: "default"},
		Switchports: map[string]bridge.Switchport{"p1": {PVID: &pvid, Untagged: []vlan.ID{1}}, "p2": {PVID: &pvid, Untagged: []vlan.ID{1}}, "p3": {PVID: &pvid, Untagged: []vlan.ID{1}}},
	}}, STP: &stp.Config{Priority: 32768, Address: self, Ports: map[string]stp.Port{"p1": {}, "p2": {}, "p3": {}}}}
	frame := func(priority uint16) bpdu.BPDU {
		b := bpdu.BPDU{
			Version: 2, Type: bpdu.TypeRapid,
			RootID:   bpdu.BridgeID{Priority: priority, Address: netaddr.MAC{0, 0, 0, 0, 0, 1}},
			BridgeID: bpdu.BridgeID{Priority: priority, Address: peer}, PortID: 0x8001,
			MaxAge: 20 * time.Second, HelloTime: 4 * time.Second, ForwardDelay: 4 * time.Second,
		}
		b.SetRole(bpdu.RoleDesignated)
		return b
	}
	a, b := mustSwitch(t, cfg), mustSwitch(t, cfg)
	for _, sw := range []*vswitch.Switch{a, b} {
		sw.Start(t0)

		sw.Receive(t0, "p1", mustEncode(t, frame(4096), peer))
		for i := 1; i <= 12; i++ {
			at := t0.Add(time.Duration(i) * time.Second)
			sw.Receive(at, "p1", mustEncode(t, frame(4096), peer))
			sw.Advance(at)
		}
		if got := sw.Roles()["p2"]; got.Role != bpdu.RoleDesignated || got.State != stp.StateForwarding {
			t.Fatalf("fixture p2 = %+v, want Designated/Forwarding", got)
		}
	}
	at := t0.Add(24 * time.Second)
	prior := b.Advance(at)
	oa := a.Receive(at, "p2", mustEncode(t, frame(2048), peer))
	ob := b.Receive(at, "p2", mustEncode(t, frame(2048), peer))
	ob.Emissions = append(prior.Emissions, ob.Emissions...)
	compare := func(at time.Time, oa, ob vswitch.Output) {
		t.Helper()
		if !reflect.DeepEqual(a.TreeRoles(), b.TreeRoles()) {
			t.Errorf("%v TreeRoles differ: %v / %v", at, a.TreeRoles(), b.TreeRoles())
		}
		if !reflect.DeepEqual(oa, ob) {
			t.Errorf("%v Output differs: %+v / %+v", at, oa, ob)
		}
		wa, oka := a.NextWake()
		wb, okb := b.NextWake()
		if wa != wb || oka != okb {
			t.Errorf("%v NextWake differs: %v/%v / %v/%v", at, wa, oka, wb, okb)
		}
		t.Logf("at=%v roles=%v next=%v/%v emissions=%d/%d", at, a.Roles(), wa, oka, len(oa.Emissions), len(ob.Emissions))
	}
	compare(at, oa, ob)
	for _, sw := range []*vswitch.Switch{a, b} {
		if _, err := sw.LinkChange(at, "p3", port.Down, vswitch.PointToPointTrue, 1_000_000_000); err != nil {
			t.Fatal(err)
		}
		if _, err := sw.LinkChange(at, "p3", port.Up, vswitch.PointToPointTrue, 1_000_000_000); err != nil {
			t.Fatal(err)
		}

		sw.Receive(at.Add(4*time.Second), "p2", mustEncode(t, frame(2048), peer))
	}
	later := at.Add(5 * time.Second)
	oa = a.Receive(later, "p3", mustEncode(t, frame(0), peer))
	ob = b.Receive(later, "p3", mustEncode(t, frame(0), peer))
	compare(later, oa, ob)
	if got := b.Roles()["p1"]; got.State != stp.StateDiscarding {
		t.Errorf("reroot former root = %+v, want Discarding", got)
	}
	later = at.Add(16 * time.Second)
	compare(later, a.Advance(later), b.Advance(later))
	if got := b.Roles()["p1"]; got.State != stp.StateLearning {
		t.Errorf("former root at T+16s = %+v, want Learning after the reroot cut", got)
	}
}

func outputHeldFrame(t *testing.T) ethernet.Frame {
	t.Helper()
	return ethernet.Frame{
		Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4,
		Payload: makeIPv4Packet(t, ipH1, netip.MustParseAddr("10.0.20.77"), 64, []byte("hello")),
	}
}

func outputReply(t *testing.T) ethernet.Frame {
	t.Helper()
	return makeARPReply(t, netip.MustParseAddr("10.0.20.77"), netip.MustParseAddr("10.0.20.1"),
		netaddr.MAC{2, 0, 0, 0, 0x20, 0x77}, macRouter)
}

func TestReceiveReturnsTheRelease(t *testing.T) {
	sw := buildRoutedPortSwitch(t)
	held := sw.Receive(fixedTime, "1/1/1", outputHeldFrame(t))
	if held.Outcome != trace.Held || len(held.Emissions) != 1 {
		t.Fatalf("first output = %+v, want Held and one solicitation", held)
	}

	out := sw.Receive(fixedTime.Add(time.Millisecond), "1/1/2", outputReply(t))
	if len(out.Emissions) != 1 || out.Emissions[0].Protocol || out.Emissions[0].Port != "1/1/2" || out.Emissions[0].Frame.Dst != outputReply(t).Src {
		t.Errorf("release = %+v, want held data on 1/1/2 to learned MAC", out.Emissions)
	}

	if out := sw.Advance(fixedTime.Add(time.Second)); len(out.Emissions)+len(out.Drops) != 0 {
		t.Errorf("later output = %+v, want no repeated release", out)
	}
}

func TestReceiveAdvancesBeforeTheFrame(t *testing.T) {
	sw := buildRoutedPortSwitch(t)
	held := sw.Receive(fixedTime, "1/1/1", outputHeldFrame(t))
	if held.Outcome != trace.Held || len(held.Emissions) != 1 {
		t.Fatalf("first output = %+v, want Held and one solicitation", held)
	}
	out := sw.Receive(fixedTime.Add(time.Second), "missing", ethernet.Frame{})
	if len(out.Emissions) != 1 {
		t.Fatalf("unrelated arrival emissions = %+v, want second solicitation", out.Emissions)
	}
	msg, err := arp.Decode(out.Emissions[0].Frame)
	if err != nil || msg.Operation != arp.Request || msg.TargetAddr != netip.MustParseAddr("10.0.20.77") {
		t.Errorf("retransmission = %+v/%v, want request for held destination", msg, err)
	}

	out = sw.Advance(fixedTime.Add(2 * time.Second))
	if len(out.Emissions) != 1 {
		t.Fatalf("second advance emissions = %+v, want third solicitation", out.Emissions)
	}
	out = sw.Advance(fixedTime.Add(3 * time.Second))
	if len(out.Drops) != 1 || out.Drops[0].Reason != routing.ReasonNeighborMiss {
		t.Errorf("timeout drops = %+v, want neighbor-miss for held frame", out.Drops)
	}
}

func outputTrafficSwitch(t *testing.T, stpEnabled bool) *vswitch.Switch {
	t.Helper()
	cfg := vswitch.Config{
		Ports: mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "mirror", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
		Bridge: &bridge.Config{}, Traffic: &traffic.Config{
			Policers: map[string]traffic.Policer{"in": {RateBPS: 8, BurstOctets: 84}},
			Mirrors:  []traffic.Mirror{{Name: "span", SelectAll: true, OutputPort: "mirror"}},
		},
	}
	if stpEnabled {
		cfg.STP = &stp.Config{Ports: map[string]stp.Port{"in": {AdminEdge: true}, "out": {AdminEdge: true}}}
	}
	return mustSwitch(t, cfg)
}

func outputData() ethernet.Frame {
	return ethernet.Frame{Src: macH1, Dst: netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, EtherType: ethernet.EtherTypeIPv4, Payload: make([]byte, 46)}
}

func TestReceivePolicesAndReceiveMirroredDoesNot(t *testing.T) {
	sw := outputTrafficSwitch(t, false)
	first := sw.Receive(fixedTime, "in", outputData())
	if first.Outcome != trace.Flooded || len(first.Copies) != 1 {
		t.Fatalf("first output = %+v, want Flooded and one copy", first)
	}
	out := sw.Receive(fixedTime, "in", outputData())
	if out.Outcome != trace.Dropped || out.Reason != traffic.ReasonPoliced || len(out.Steps) != 1 || out.Steps[0].RuleID != traffic.RulePolicerRefuse {
		t.Errorf("exhausted output = %+v, want policer refusal", out)
	}
	mirrored := sw.ReceiveMirrored(fixedTime, "in", outputData())
	if mirrored.Outcome != trace.Flooded || len(mirrored.Copies) != 0 || len(mirrored.Egress) == 0 {
		t.Errorf("mirrored output = %+v, want Flooded with egress and no copies", mirrored)
	}

	cfg := outputTrafficSwitch(t, false).Config()
	cfg.Traffic.Policers["in"] = traffic.Policer{RateBPS: 8, BurstOctets: 83}
	tooSmall := mustSwitch(t, cfg).Receive(fixedTime, "in", outputData())
	if tooSmall.Reason != traffic.ReasonPoliced {
		t.Errorf("84 wire octets in an 83-octet bucket = %+v, want policed", tooSmall)
	}
}

func TestPolicedArrivalStillAdvances(t *testing.T) {
	sw := outputTrafficSwitch(t, true)
	sw.Start(fixedTime)

	sw.Receive(fixedTime, "in", outputData())
	out := sw.Receive(fixedTime.Add(2*time.Second), "in", outputData())
	if out.Reason != traffic.ReasonPoliced {
		t.Fatalf("output reason = %v, want policed", out.Reason)
	}
	if len(out.Emissions) == 0 {
		t.Fatal("policed arrival emitted no due hello")
	}
	for _, em := range out.Emissions {
		if _, err := bpdu.Decode(em.Frame); err != nil {
			t.Errorf("due emission is not a BPDU: %v", err)
		}
	}
}

func outputTimedSwitch(t *testing.T) *vswitch.Switch {
	t.Helper()
	cfg := buildRoutedPortSwitch(t).Config()
	cfg.Ports = mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "protocol", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "member", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}))
	pvid := vlan.ID(1)
	cfg.Bridge = &bridge.Config{VLAN: &bridge.VLAN{Table: map[vlan.ID]string{1: "default"}, Switchports: map[string]bridge.Switchport{"protocol": {PVID: &pvid, Untagged: []vlan.ID{1}}}}}
	cfg.STP = &stp.Config{Ports: map[string]stp.Port{"protocol": {AdminEdge: true}}}
	cfg.LAG = &lag.Config{LAGs: map[string]lag.LAG{"lag1": {Mode: lag.BalanceSLB, LACP: lag.LACPConfig{Mode: lag.Active, Fast: true}, Members: map[string]lag.Member{"member": {}}}}}
	cfg.LoopProtect = &loopprotect.Config{Interval: time.Second, Ports: map[string]loopprotect.Port{"protocol": {Action: loopprotect.Block}}}
	return mustSwitch(t, cfg)
}

func TestAdvanceVisitsEveryTimedLayer(t *testing.T) {
	sw := outputTimedSwitch(t)
	sw.Start(fixedTime)

	held := sw.Receive(fixedTime, "1/1/1", outputHeldFrame(t))
	if held.Outcome != trace.Held {
		t.Fatalf("fixture outcome = %v, want Held", held.Outcome)
	}
	out := sw.Advance(fixedTime.Add(2 * time.Second))
	for _, kind := range []string{"stp", "lag", "loopprotect", "routing"} {
		if !slices.ContainsFunc(out.Emissions, func(em vswitch.Emission) bool {
			switch kind {
			case "stp":
				_, err := bpdu.Decode(em.Frame)
				return err == nil
			case "lag":
				return em.Port == "member" && em.Frame.EtherType == ethernet.EtherTypeSlowProtocols
			case "loopprotect":
				_, err := loopprotect.Decode(em.Frame)
				return err == nil
			default:
				_, err := arp.Decode(em.Frame)
				return err == nil
			}
		}) {
			t.Errorf("Advance has no %s emission: %+v", kind, out.Emissions)
		}
	}
}

func TestNextWakeIsTheEarliestOfTheTable(t *testing.T) {
	cfg := outputTimedSwitch(t).Config()
	cfg.LAG = nil
	cfg.LoopProtect = nil
	sw := mustSwitch(t, cfg)
	sw.Start(fixedTime)

	if at, ok := sw.NextWake(); !ok || at != fixedTime.Add(2*time.Second) {
		t.Fatalf("STP wake = %v/%v, want hello at +2s", at, ok)
	}
	held := sw.Receive(fixedTime, "1/1/1", outputHeldFrame(t))
	if held.Outcome != trace.Held {
		t.Fatalf("fixture outcome = %v, want Held", held.Outcome)
	}
	if at, ok := sw.NextWake(); !ok || at != fixedTime.Add(time.Second) {
		t.Errorf("combined wake = %v/%v, want retransmit at +1s", at, ok)
	}
	untimed := outputTrafficSwitch(t, false)
	if at, ok := untimed.NextWake(); ok || !at.IsZero() {
		t.Errorf("untimed wake = %v/%v, want zero/false", at, ok)
	}
}

func TestEntriesReturnTheirOutput(t *testing.T) {
	sw := outputTimedSwitch(t)
	if out := sw.Start(fixedTime); len(out.Emissions) == 0 {
		t.Fatal("Start returned no output")
	}
	if _, err := sw.LinkChange(fixedTime.Add(time.Second), "protocol", port.Down, vswitch.PointToPointTrue, 1_000_000_000); err != nil {
		t.Fatal(err)
	}
	out, err := sw.LinkChange(fixedTime.Add(time.Second), "protocol", port.Up, vswitch.PointToPointTrue, 1_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Emissions) == 0 {
		t.Fatal("LinkChange returned no output")
	}
	if out := sw.Mcheck(fixedTime.Add(2*time.Second), "protocol"); len(out.Emissions) == 0 {
		t.Fatal("Mcheck returned no output")
	}
	routed := buildRoutedPortSwitch(t)
	if out := routed.Receive(fixedTime, "1/1/1", outputHeldFrame(t)); len(out.Emissions) != 1 {
		t.Fatal("Receive returned no solicitation")
	}
	cfg := routed.Config()
	cfg.Routing = nil
	_, out, err = vswitch.Derive(routed, vswitch.ConstructionSpec{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Drops) != 1 || out.Drops[0].Reason != routing.ReasonNeighborMiss {
		t.Errorf("derive drops = %+v, want neighbor-miss", out.Drops)
	}
}

func TestPeekReturnsCopiesAndLeavesNoState(t *testing.T) {
	cfg := outputTrafficSwitch(t, true).Config()
	cfg.Traffic.Policers["in"] = traffic.Policer{RateBPS: 8, BurstOctets: 168}
	sw := mustSwitch(t, cfg)
	sw.Start(fixedTime)
	sw.Receive(fixedTime, "in", outputData())
	baseline := sw.Fork()
	at := fixedTime.Add(2 * time.Second)
	peekFrame := outputData()
	peekFrame.Src = netaddr.MAC{2, 0, 0, 0, 0, 99}
	out := sw.Peek(at, "in", peekFrame)
	if len(out.Copies) != 1 || out.Copies[0].Port != "mirror" || !reflect.DeepEqual(out.Copies[0].Frame, peekFrame) {
		t.Fatalf("Peek copies = %+v, want one copy to mirror", out.Copies)
	}
	if len(out.Emissions) != 0 || len(out.Drops) != 0 {
		t.Fatalf("Peek output = %+v, want no emissions or drops", out)
	}
	if a, b := sw.Receive(at, "in", outputData()), baseline.Receive(at, "in", outputData()); !reflect.DeepEqual(a, b) {
		t.Errorf("Receive after Peek = %+v, without Peek = %+v", a, b)
	}
	probe := outputData()
	probe.Dst = peekFrame.Src
	if a, b := sw.ReceiveMirrored(at, "out", probe), baseline.ReceiveMirrored(at, "out", probe); !reflect.DeepEqual(a, b) {
		t.Errorf("learning changed by Peek: %+v / %+v", a, b)
	}
	if a, b := sw.Advance(at.Add(time.Second)), baseline.Advance(at.Add(time.Second)); !reflect.DeepEqual(a, b) {
		t.Errorf("Advance after Peek = %+v, without Peek = %+v", a, b)
	}
	routed := buildRoutedPortSwitch(t)
	routed.Receive(fixedTime, "1/1/1", outputHeldFrame(t))
	fork := routed.Fork()
	routed.Peek(fixedTime.Add(time.Millisecond), "1/1/2", outputReply(t))
	if a, b := routed.Receive(fixedTime.Add(time.Millisecond), "1/1/2", outputReply(t)), fork.Receive(fixedTime.Add(time.Millisecond), "1/1/2", outputReply(t)); !reflect.DeepEqual(a, b) {
		t.Errorf("neighbor reception changed by Peek: %+v / %+v", a, b)
	}
}
