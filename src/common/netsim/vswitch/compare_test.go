package vswitch_test

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

// TestCompareAgreesOverConsecutiveCallsOnUnresolvedDestination covers R20c
// across two switches: [vswitch.Compare] runs [Switch.Peek] on both sides, and
// a pending destination must not perturb either one. Two consecutive Compare
// calls over the same pair of switches and the same unresolved destination
// must therefore return equal results; if a peek queued a frame or created an
// entry, the second call would see a table the first left different from the
// one it found.
func TestCompareAgreesOverConsecutiveCallsOnUnresolvedDestination(t *testing.T) {
	a := buildBaseRoutingSwitch(t)
	b := buildBaseRoutingSwitch(t)

	dst := netip.MustParseAddr("10.0.20.77")
	pkt := makeIPv4Packet(t, ipH1, dst, 64, []byte("hello"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}

	first := vswitch.Compare(a, b, fixedTime, "1/1/1", frame)
	if !first.Same || first.Disposition != analysis.Inconclusive {
		t.Fatalf("first compare: current = %+v, expected = %+v, want inconclusive", first.Current.Result, first.Expected.Result)
	}

	second := vswitch.Compare(a, b, fixedTime, "1/1/1", frame)
	if !second.Same || second.Disposition != analysis.Inconclusive {
		t.Fatalf("second compare: current = %+v, expected = %+v, want inconclusive", second.Current.Result, second.Expected.Result)
	}
	if second.Current.Outcome != first.Current.Outcome || second.Current.Reason != first.Current.Reason {
		t.Errorf("second Compare disagreed with first: %v/%v vs %v/%v",
			second.Current.Outcome, second.Current.Reason, first.Current.Outcome, first.Current.Reason)
	}

	if _, ok := a.NextWake(); ok {
		t.Errorf("a.NextWake() reported a timer after two Compare calls, want none: Peek must not have queued a frame")
	}
	if _, ok := b.NextWake(); ok {
		t.Errorf("b.NextWake() reported a timer after two Compare calls, want none: Peek must not have queued a frame")
	}
}

func TestCompareDetectsDestinationMACRewriteDifference(t *testing.T) {
	t.Parallel()

	swA := buildBaseRoutingSwitch(t)

	p10 := vlan.ID(10)
	p20 := vlan.ID(20)
	macDifferent := netaddr.MAC{0x00, 0x50, 0x56, 0xaa, 0xbb, 0xcc}

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	swB := mustSwitch(t, vswitch.Config{
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
						{Interface: "vlan20", Addr: ipH2, MAC: macDifferent},
					},
				},
			},
		},
	})

	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("test"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}

	cmp := vswitch.Compare(swA, swB, fixedTime, "1/1/1", frame)
	if cmp.Disposition != analysis.Different {
		t.Fatalf("Disposition = %v, want %v", cmp.Disposition, analysis.Different)
	}
	if cmp.Difference.Observable != "frame.dst" {
		t.Fatalf("Difference.Observable = %q, want %q", cmp.Difference.Observable, "frame.dst")
	}
	if cmp.Same {
		t.Errorf("Same = true, want false")
	}
}

func TestCompareDetectsLAGMemberDifference(t *testing.T) {
	t.Parallel()

	vid := vlan.ID(10)
	portsA := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Down}))

	portsB := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Down}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}))

	bCfg := &bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &vid, Untagged: []vlan.ID{10}},
				"lag1":  {PVID: &vid, Untagged: []vlan.ID{10}},
			},
		},
	}

	swA := mustSwitch(t, vswitch.Config{Ports: portsA, Bridge: bCfg, LAG: &lag.Config{}})
	swB := mustSwitch(t, vswitch.Config{Ports: portsB, Bridge: bCfg, LAG: &lag.Config{}})

	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		Dst:       netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x02},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("lag-payload"),
	}

	cmp := vswitch.Compare(swA, swB, fixedTime, "1/1/1", frame)
	if cmp.Disposition != analysis.Different {
		t.Fatalf("Disposition = %v, want %v", cmp.Disposition, analysis.Different)
	}
	if cmp.Difference.Observable != "lag.member" {
		t.Fatalf("Difference.Observable = %q, want %q", cmp.Difference.Observable, "lag.member")
	}
	if cmp.Same {
		t.Errorf("Same = true, want false")
	}
}

func TestCompareDetectsMissingMirrorCopy(t *testing.T) {
	t.Parallel()

	vid := vlan.ID(10)
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	bCfg := &bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &vid, Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: &vid, Untagged: []vlan.ID{10}},
				"1/1/3": {PVID: &vid, Untagged: []vlan.ID{10}},
			},
		},
	}

	swA := mustSwitch(t, vswitch.Config{
		Ports:  ports,
		Bridge: bCfg,
		Traffic: &traffic.Config{
			Mirrors: []traffic.Mirror{
				{Name: "span1", OutputPort: "1/1/3", SelectAll: true},
			},
		},
	})
	swB := mustSwitch(t, vswitch.Config{Ports: ports, Bridge: bCfg})

	dstMAC := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x02}
	learnFrame := ethernet.Frame{
		Src:       dstMAC,
		Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		EtherType: ethernet.EtherTypeIPv4,
	}
	swA.Forward(fixedTime, "1/1/2", learnFrame)
	swB.Forward(fixedTime, "1/1/2", learnFrame)

	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		Dst:       dstMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("mirror-test"),
	}

	cmp := vswitch.Compare(swA, swB, fixedTime, "1/1/1", frame)
	if cmp.Disposition != analysis.Different {
		t.Fatalf("Disposition = %v, want %v", cmp.Disposition, analysis.Different)
	}
	if cmp.Difference.Observable != "mirror" {
		t.Fatalf("Difference.Observable = %q, want %q", cmp.Difference.Observable, "mirror")
	}
	if cmp.Same {
		t.Errorf("Same = true, want false")
	}
}

func TestCompareDetectsPCPDifference(t *testing.T) {
	t.Parallel()

	sw := buildBaseRoutingSwitch(t)
	pkt := makeIPv4Packet(t, ipH1, ipH2, 64, []byte("pcp-test"))
	frame := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}

	resA := sw.Peek(fixedTime, "1/1/1", frame)
	resB := sw.Peek(fixedTime, "1/1/1", frame)
	if len(resA.Egress) == 0 || len(resB.Egress) == 0 {
		t.Fatalf("expected egress on both results")
	}
	resB.Egress[0].PCP = 7

	cmp := vswitch.CompareResults(resA, resB)
	if cmp.Disposition != analysis.Different {
		t.Fatalf("Disposition = %v, want %v", cmp.Disposition, analysis.Different)
	}
	if cmp.Difference.Observable != "pcp" {
		t.Fatalf("Difference.Observable = %q, want %q", cmp.Difference.Observable, "pcp")
	}
	if cmp.Same {
		t.Errorf("Same = true, want false")
	}
}

func TestCompareDetectsOutcomeDifference(t *testing.T) {
	t.Parallel()

	vid10 := vlan.ID(10)
	vid20 := vlan.ID(20)

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	swA := mustSwitch(t, vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
				},
			},
		},
	})
	swB := mustSwitch(t, vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &vid20, Untagged: []vlan.ID{20}},
				},
			},
		},
	})

	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		Dst:       netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x02},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("outcome-diff"),
	}

	cmp := vswitch.Compare(swA, swB, fixedTime, "1/1/1", frame)
	if cmp.Disposition != analysis.Different {
		t.Fatalf("Disposition = %v, want %v", cmp.Disposition, analysis.Different)
	}
	if cmp.Difference.Observable != "outcome" {
		t.Fatalf("Difference.Observable = %q, want %q", cmp.Difference.Observable, "outcome")
	}
	if cmp.Same {
		t.Errorf("Same = true, want false")
	}
}

func TestCompareNoOpTraceDifferenceEquivalent(t *testing.T) {
	t.Parallel()

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	vid := vlan.ID(10)
	bCfg := &bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &vid, Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: &vid, Untagged: []vlan.ID{10}},
			},
		},
	}

	sw := mustSwitch(t, vswitch.Config{Ports: ports, Bridge: bCfg})

	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		Dst:       netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x02},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("trace-diff"),
	}

	resA := sw.Peek(fixedTime, "1/1/1", frame)
	resB := sw.Peek(fixedTime, "1/1/1", frame)
	resB.Steps = append(resB.Steps, trace.Step{
		Layer:  trace.Layer("bridge"),
		Op:     trace.OpLookup,
		RuleID: "trace.noop_diagnostic",
	})

	cmp := vswitch.CompareResults(resA, resB)
	if trace.Equal(cmp.Current.Trace, cmp.Expected.Trace) {
		t.Fatalf("traces are equal, want different diagnostic steps")
	}
	if cmp.Disposition != analysis.Equivalent {
		t.Fatalf("Disposition = %v, want %v", cmp.Disposition, analysis.Equivalent)
	}
	if cmp.Difference.Observable != "" {
		t.Errorf("Difference.Observable = %q, want empty", cmp.Difference.Observable)
	}
	if !cmp.Same {
		t.Errorf("Same = false, want true")
	}
}

func TestCompareIncompleteResultInconclusive(t *testing.T) {
	t.Parallel()

	portsA := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown}))

	portsB := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown}))

	vid := vlan.ID(10)
	bCfg := &bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &vid, Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: &vid, Untagged: []vlan.ID{10}},
			},
		},
	}

	swA := mustSwitch(t, vswitch.Config{Ports: portsA, Bridge: bCfg})
	swB := mustSwitch(t, vswitch.Config{Ports: portsB, Bridge: bCfg})

	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		Dst:       netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x02},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("inconclusive-test"),
	}

	cmp := vswitch.Compare(swA, swB, fixedTime, "1/1/1", frame)
	if cmp.Disposition != analysis.Inconclusive {
		t.Fatalf("Disposition = %v, want %v", cmp.Disposition, analysis.Inconclusive)
	}
	if !cmp.Same {
		t.Errorf("Same = false, want true (observables match)")
	}
}
