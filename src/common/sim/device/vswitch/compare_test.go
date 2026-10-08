package vswitch_test

import (
	"net/netip"
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestCompareDetectsMirrorVLANDifference(t *testing.T) {
	cur := vswitch.Output{Copies: []traffic.Copy{{Mirror: "span", Port: "monitor", VLAN: 98}}}
	exp := vswitch.Output{Copies: []traffic.Copy{{Mirror: "span", Port: "monitor", VLAN: 99}}}
	cmp := vswitch.CompareOutputsForTest(cur, exp)
	if cmp.Disposition != analysis.Different || cmp.Difference != (analysis.Difference{
		Observable: "mirror",
		Current:    `mirror="span";port="monitor";vlan=98;member="";src=00:00:00:00:00:00;dst=00:00:00:00:00:00;ether_type=0;tags=[];payload=`,
		Expected:   `mirror="span";port="monitor";vlan=99;member="";src=00:00:00:00:00:00;dst=00:00:00:00:00:00;ether_type=0;tags=[];payload=`,
	}) {
		t.Fatalf("comparison = %+v, want mirror VLAN difference", cmp)
	}
}

func TestCompareDetectsMirrorMemberDifference(t *testing.T) {
	cur := vswitch.Output{Copies: []traffic.Copy{{Mirror: "span", Port: "lag1", VLAN: 98, Member: "m1"}}}
	exp := vswitch.Output{Copies: []traffic.Copy{{Mirror: "span", Port: "lag1", VLAN: 98, Member: "m2"}}}
	cmp := vswitch.CompareOutputsForTest(cur, exp)
	if cmp.Disposition != analysis.Different || cmp.Difference != (analysis.Difference{
		Observable: "mirror",
		Current:    `mirror="span";port="lag1";vlan=98;member="m1";src=00:00:00:00:00:00;dst=00:00:00:00:00:00;ether_type=0;tags=[];payload=`,
		Expected:   `mirror="span";port="lag1";vlan=98;member="m2";src=00:00:00:00:00:00;dst=00:00:00:00:00:00;ether_type=0;tags=[];payload=`,
	}) {
		t.Fatalf("comparison = %+v, want mirror member difference", cmp)
	}
}

func TestCompareDetectsMirrorFrameDifference(t *testing.T) {
	cur := vswitch.Output{Copies: []traffic.Copy{{Mirror: "span", Port: "monitor", VLAN: 98, Frame: ethernet.Frame{Payload: []byte{1, 2}}}}}
	exp := vswitch.Output{Copies: []traffic.Copy{{Mirror: "span", Port: "monitor", VLAN: 98, Frame: ethernet.Frame{Payload: []byte{1}}}}}
	cmp := vswitch.CompareOutputsForTest(cur, exp)
	if cmp.Disposition != analysis.Different || cmp.Difference != (analysis.Difference{
		Observable: "mirror",
		Current:    `mirror="span";port="monitor";vlan=98;member="";src=00:00:00:00:00:00;dst=00:00:00:00:00:00;ether_type=0;tags=[];payload=0102`,
		Expected:   `mirror="span";port="monitor";vlan=98;member="";src=00:00:00:00:00:00;dst=00:00:00:00:00:00;ether_type=0;tags=[];payload=01`,
	}) {
		t.Fatalf("comparison = %+v, want mirror snap length difference", cmp)
	}
}

func TestCompareNamesEachFirstObservable(t *testing.T) {
	for _, tc := range []struct {
		name              string
		change            func(*vswitch.Output)
		current, expected string
	}{
		{"reason", func(o *vswitch.Output) { o.Reason = "port-down" }, "", "port-down"},
		{"fid", func(o *vswitch.Output) { o.FID = 20 }, "10", "20"},
		{"egress.port", func(o *vswitch.Output) { o.Egress[0].Port = "p2" }, "p1", "<absent>"},
		{"egress.dropped", func(o *vswitch.Output) { o.Egress[0].Dropped = "port-down" }, "", "port-down"},
		{"frame.src", func(o *vswitch.Output) { o.Egress[0].Frame.Src = macH1 }, "00:00:00:00:00:00", "00:11:22:33:44:11"},
		{"frame.ethertype", func(o *vswitch.Output) { o.Egress[0].Frame.EtherType = ethernet.EtherTypeIPv6 }, "IPv4 (0x0800)", "IPv6 (0x86dd)"},
		{"frame.tags", func(o *vswitch.Output) { o.Egress[0].Frame.Tags = []vlan.Tag{{VID: 10}} }, "[]", "[{0 0 false 10}]"},
		{"frame.payload", func(o *vswitch.Output) { o.Egress[0].Frame.Payload = []byte{2} }, "01", "02"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cur := vswitch.Output{ForwardResult: vswitch.ForwardResult{Result: bridge.Result{
				Trace: trace.Trace{Outcome: trace.Forwarded}, FID: 10,
				Egress: []bridge.Egress{{Port: "p1", Frame: ethernet.Frame{EtherType: ethernet.EtherTypeIPv4, Payload: []byte{1}}}},
			}}}
			exp := cur
			exp.Egress = slices.Clone(cur.Egress)
			tc.change(&exp)
			cmp := vswitch.CompareOutputsForTest(cur, exp)
			if cmp.Disposition != analysis.Different || cmp.Difference != (analysis.Difference{Observable: tc.name, Current: tc.current, Expected: tc.expected}) {
				t.Fatalf("comparison = %+v, want %s: %q/%q", cmp, tc.name, tc.current, tc.expected)
			}
		})
	}
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
	swA.Receive(fixedTime, "1/1/2", ethernet.Frame{
		Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		Src:       knownDst,
		EtherType: ethernet.EtherTypeIPv4,
	})
	swB.Receive(fixedTime, "1/1/2", ethernet.Frame{
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
	if first.Disposition != analysis.Inconclusive {
		t.Fatalf("first compare: current = %+v, expected = %+v, want inconclusive", first.Current.Result, first.Expected.Result)
	}

	second := vswitch.Compare(a, b, fixedTime, "1/1/1", frame)
	if second.Disposition != analysis.Inconclusive {
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
}

func TestCompareDetectsLAGMemberDifference(t *testing.T) {
	t.Parallel()

	vid := vlan.ID(10)
	portsA := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Down}))

	portsB := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
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
	swA.Receive(fixedTime, "1/1/2", learnFrame)
	swB.Receive(fixedTime, "1/1/2", learnFrame)

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
	if cmp.Difference.Current != `mirror="span1";port="1/1/3";vlan=0;member="";src=00:00:00:00:00:01;dst=00:00:00:00:00:02;ether_type=2048;tags=[];payload=6d6972726f722d74657374` || cmp.Difference.Expected != "" {
		t.Fatalf("mirror difference = %+v, want current copy and absent expected copy", cmp.Difference)
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
}
