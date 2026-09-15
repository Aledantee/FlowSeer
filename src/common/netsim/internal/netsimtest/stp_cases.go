package netsimtest

import (
	"maps"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

var (
	stpCaseHost   = netaddr.MAC{0x02, 0, 0, 0, 2, 0x01}
	stpCaseTarget = netaddr.MAC{0x02, 0, 0, 0, 2, 0x02}
	stpCaseBridge = netaddr.MAC{0x02, 0, 0, 0, 2, 0xfe}
	stpCaseStart  = time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
)

// The three cases share the journey up to the forwarding decision: the frame
// arrives untagged on p9, its source is learned, and the seeded entry resolves
// the destination to p1. What differs is what p1's spanning-tree state makes of
// it, which is what each case is about.
var (
	stpCaseFrame = expectedFact("bridge.frame",
		`src="02:00:00:00:02:01";dst="02:00:00:00:02:02";ether_type=2048;tags=[];payload_len=1`)
	stpCaseLearned = expectedFact("bridge.fdb_decision",
		`fid=0;mac="02:00:00:00:02:01";present=true;port="p9";static=false`)
	stpCaseHit = expectedFact("bridge.fdb_decision",
		`fid=0;mac="02:00:00:00:02:02";present=true;port="p1";static=true`)
)

func stpCaseCommonSteps() []StepExpectation {
	return []StepExpectation{
		expectedStep("relay", trace.OpClassify, "default-vlan", trace.Subject{Kind: "vlan", Key: "0"},
			[]FactExpectation{stpCaseFrame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="p9";fid=0;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:02:01"},
			nil, []FactExpectation{stpCaseLearned}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:02:02"},
			[]FactExpectation{stpCaseFrame}, []FactExpectation{stpCaseHit}),
	}
}

// stpCaseGateFact is the spanning-tree fact the gate contributes to p1's
// forwarding decision. Binding it to the drop step is what lets a reader see
// which guard held the port, rather than only that something did.
func stpCaseGateFact(state string) FactExpectation {
	return expectedFact("stp.forwarding_decision", `port="p1";vid=0;state=`+state+`;learns=false;forwards=false`)
}

func stpCaseBlockedSteps(gate FactExpectation) []StepExpectation {
	return append(stpCaseCommonSteps(),
		expectedStep("relay", trace.OpDrop, "port-blocked", trace.Subject{Kind: "port", Key: "p1"},
			[]FactExpectation{gate},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="p1";member="";fid=0;eligible=false;reason="port-blocked"`)}),
	)
}

func stpCaseCommonSubjects() []trace.Subject {
	return []trace.Subject{
		{Kind: "vlan", Key: "0"},
		{Kind: "mac", Key: "02:00:00:00:02:01"},
		{Kind: "mac", Key: "02:00:00:00:02:02"},
		{Kind: "port", Key: "p1"},
	}
}

func stpCaseCompleteMetadata() *MetadataExpectation {
	return &MetadataExpectation{Status: analysis.Complete, Scope: analysis.NodeScope("sw1")}
}

// stpCaseSwitch builds a two-port bridge running rapid spanning tree, with the
// given administrative spanning-tree settings per port and both links up. p9 is
// the host-facing access port every case injects on, and p1 is the port under
// test, which the seeded forwarding entry sends the frame out of.
func stpCaseSwitch(stpPorts map[string]stp.Port) (*vswitch.Switch, error) {
	builder := port.NewBuilder()
	for _, name := range slices.Sorted(maps.Keys(stpPorts)) {
		builder.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	}
	tbl, err := builder.Build()
	if err != nil {
		return nil, err
	}

	sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		NodeID: "sw1",
		Config: vswitch.Config{
			Ports:  tbl,
			Bridge: &bridge.Config{},
			STP: &stp.Config{
				Priority: 32768,
				Address:  stpCaseBridge,
				Ports:    stpPorts,
			},
		},
		Seeds: []bridge.Seed{{MAC: stpCaseTarget, Port: "p1", Static: true}},
	})
	if err != nil {
		return nil, err
	}
	sw.Start(stpCaseStart)

	return sw, nil
}

// stpCaseSuperiorBPDU encodes an RST BPDU from the root bridge itself, better
// than the one stpCaseSwitch builds, carrying the given message age against a
// 20s max age.
func stpCaseSuperiorBPDU(messageAge time.Duration) ethernet.Frame {
	return stpCaseBPDU(4096, 10, messageAge)
}

// stpCaseBPDU encodes an RST BPDU from bridge `sender` claiming root 4096 at
// `cost`, so a case can place one bridge's claim against another's.
func stpCaseBPDU(sender uint16, cost uint32, messageAge time.Duration) ethernet.Frame {
	b := stp.BPDU{
		Version:      2,
		Type:         stp.BPDUTypeRapid,
		RootID:       stp.BridgeID{Priority: 4096},
		RootPathCost: cost,
		BridgeID:     stp.BridgeID{Priority: sender},
		PortID:       0x8001,
		MessageAge:   messageAge,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	b.SetRole(stp.RoleDesignated)

	return stp.Encode(b, netaddr.MAC{0x02, 0, 0, 0, 3, 0x01})
}

// stpCaseDataFrame is an ordinary unicast frame aimed at the seeded target,
// which the forwarding database resolves to p1.
func stpCaseDataFrame() ethernet.Frame {
	return ethernet.Frame{
		Dst:       stpCaseTarget,
		Src:       stpCaseHost,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("d"),
	}
}

// RegisterSTPCases populates registry with the cases covering information
// lifetime and the port guards: a root that no longer exists aging out, BPDU
// guard disabling an edge port, and loop guard holding a port whose BPDUs
// stopped; and the two cases covering multiple spanning tree instances: two
// VLANs on separate MSTIs choosing different links, and a region boundary
// holding the MSTIs to the CIST's answer.
func RegisterSTPCases(registry *Registry) {
	registry.MustRegister(CaseTroubleshootingStaleRootAgesOut())
	registry.MustRegister(CaseTroubleshootingBPDUGuardDisablesEdge())
	registry.MustRegister(CaseTroubleshootingLoopGuardUnidirectionalLink())
	registry.MustRegister(CasePlanningMSTPVLANInstancesDiverge())
	registry.MustRegister(CaseTopologyShadowingMSTRegionBoundary())
}

// stpMSTAddresses are the bridge addresses the two MST cases share: sw1 is
// the region's root and regional root (the lowest priority), sw2 the
// non-root bridge whose per-instance path costs (or, for the boundary case,
// whose different region revision) decide what each case demonstrates.
var (
	stpMSTSW1Address = netaddr.MAC{0x02, 0, 0, 0, 4, 0x01}
	stpMSTSW2Address = netaddr.MAC{0x02, 0, 0, 0, 4, 0x02}
	stpMSTDst10      = netaddr.MAC{0x02, 0, 0, 0, 4, 0x11}
	stpMSTDst20      = netaddr.MAC{0x02, 0, 0, 0, 4, 0x21}
	stpMSTSrc10      = netaddr.MAC{0x02, 0, 0, 0, 4, 0x12}
	stpMSTSrc20      = netaddr.MAC{0x02, 0, 0, 0, 4, 0x22}
)

// stpMSTFabricSpec returns the two-switch, two-link topology the MST corpus
// cases share: sw1 and sw2 joined by l1 and l2, each carrying VLAN 10 and
// VLAN 20 over an access port on either end. sw1's access ports (v10, v20)
// are where a case injects, and sw2's (d10, d20) are where a case seeds the
// destination MAC each VLAN resolves to, so a case chooses which physical
// link a VLAN's frame must cross by which link its tree leaves forwarding
// rather than by the seed itself.
func stpMSTFabricSpec(mst1, mst2 *stp.MST) fabric.ConstructionSpec {
	vid10, vid20 := vlan.ID(10), vlan.ID(20)

	newPorts := func(access1, access2 string) port.Table {
		tbl, err := port.NewBuilder().
			Add(port.Port{Name: "l1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "l2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: access1, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: access2, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		if err != nil {
			panic(err)
		}

		return tbl
	}

	newBridgeCfg := func(access1, access2 string) *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "VLAN10", 20: "VLAN20"},
				Switchports: map[string]bridge.Switchport{
					"l1":    {Tagged: []vlan.ID{10, 20}},
					"l2":    {Tagged: []vlan.ID{10, 20}},
					access1: {PVID: &vid10, Untagged: []vlan.ID{10}},
					access2: {PVID: &vid20, Untagged: []vlan.ID{20}},
				},
			},
		}
	}

	return fabric.ConstructionSpec{
		Start: stpCaseStart,
		Switches: map[string]vswitch.ConstructionSpec{
			"sw1": {
				NodeID: "sw1",
				Config: vswitch.Config{
					Ports:  newPorts("v10", "v20"),
					Bridge: newBridgeCfg("v10", "v20"),
					STP: &stp.Config{
						Priority: 4096,
						Address:  stpMSTSW1Address,
						Ports:    map[string]stp.Port{"l1": {}, "l2": {}},
						MST:      mst1,
					},
					Phy: &phy.Config{Ethernet: map[string]phy.Ethernet{
						"l1": gigabitAuto(), "l2": gigabitAuto(), "v10": gigabitAuto(), "v20": gigabitAuto(),
					}},
				},
				Seeds: []bridge.Seed{
					{FID: 10, MAC: stpMSTDst10, Port: "l2", Static: true},
					{FID: 20, MAC: stpMSTDst20, Port: "l1", Static: true},
				},
			},
			"sw2": {
				NodeID: "sw2",
				Config: vswitch.Config{
					Ports:  newPorts("d10", "d20"),
					Bridge: newBridgeCfg("d10", "d20"),
					STP: &stp.Config{
						Priority: 32768,
						Address:  stpMSTSW2Address,
						Ports:    map[string]stp.Port{"l1": {}, "l2": {}},
						MST:      mst2,
					},
					Phy: &phy.Config{Ethernet: map[string]phy.Ethernet{
						"l1": gigabitAuto(), "l2": gigabitAuto(), "d10": gigabitAuto(), "d20": gigabitAuto(),
					}},
				},
				Seeds: []bridge.Seed{
					{FID: 10, MAC: stpMSTDst10, Port: "d10", Static: true},
					{FID: 20, MAC: stpMSTDst20, Port: "d20", Static: true},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1v10": {Address: stpMSTSrc10, Ethernet: gigabitAuto()},
			"h1v20": {Address: stpMSTSrc20, Ethernet: gigabitAuto()},
			"h2v10": {Address: stpMSTDst10, Ethernet: gigabitAuto()},
			"h2v20": {Address: stpMSTDst20, Ethernet: gigabitAuto()},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "sw1", Port: "l1"}, B: fabric.Endpoint{Node: "sw2", Port: "l1"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "sw1", Port: "l2"}, B: fabric.Endpoint{Node: "sw2", Port: "l2"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "h1v10"}, B: fabric.Endpoint{Node: "sw1", Port: "v10"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "h1v20"}, B: fabric.Endpoint{Node: "sw1", Port: "v20"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "h2v10"}, B: fabric.Endpoint{Node: "sw2", Port: "d10"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "h2v20"}, B: fabric.Endpoint{Node: "sw2", Port: "d20"}, Medium: fabric.TwistedPair},
		},
	}
}

// stpMSTInjectFrame builds fab, runs it to convergence, injects one frame from
// the VLAN 10 or VLAN 20 host on sw1's side toward that VLAN's host on sw2's
// side, and returns the resulting journey.
func stpMSTInjectFrame(spec fabric.ConstructionSpec, vid vlan.ID) (*fabric.Fabric, fabric.Journey, error) {
	fab, err := fabric.NewWithSpec(spec)
	if err != nil {
		return nil, fabric.Journey{}, err
	}
	fab.Run(1000)

	var (
		originHost string
		src, dst   netaddr.MAC
	)
	if vid == 10 {
		originHost, src, dst = "h1v10", stpMSTSrc10, stpMSTDst10
	} else {
		originHost, src, dst = "h1v20", stpMSTSrc20, stpMSTDst20
	}

	fid, err := fab.Inject(fabric.Injection{
		At:     fab.Snapshot().Clock,
		Origin: fabric.Endpoint{Node: originHost},
		Frame:  ethernet.Frame{Src: src, Dst: dst, EtherType: ethernet.EtherTypeIPv4, Payload: []byte("mst")},
	})
	if err != nil {
		return fab, fabric.Journey{}, err
	}
	fab.Run(50)

	report := fab.Report()

	return fab, report[fid-1], nil
}

// stpMSTRegion returns the MST region the two MST corpus cases share: VLAN 10
// on MSTI 1 and VLAN 20 on MSTI 2, at the given revision, with no per-port
// overrides.
func stpMSTRegion(revision uint16) *stp.MST {
	return &stp.MST{
		Name:     "region-1",
		Revision: revision,
		Instances: map[stp.MSTID]stp.Instance{
			1: {VLANs: []vlan.ID{10}},
			2: {VLANs: []vlan.ID{20}},
		},
	}
}

// stpMSTOverriddenRegion returns stpMSTRegion at the given revision with sw2's
// per-instance path cost inflated on l1 for MSTI 1 and on l2 for MSTI 2: the
// override that, on an internal port, sends MSTI 1 and MSTI 2 root through
// opposite links, and that a boundary port ignores entirely because it takes
// the CIST port's role outright.
func stpMSTOverriddenRegion(revision uint16) *stp.MST {
	r := stpMSTRegion(revision)
	inst1 := r.Instances[1]
	inst1.Ports = map[string]stp.InstancePort{"l1": {PathCost: 200_000}}
	r.Instances[1] = inst1
	inst2 := r.Instances[2]
	inst2.Ports = map[string]stp.InstancePort{"l2": {PathCost: 200_000}}
	r.Instances[2] = inst2

	return r
}

// CasePlanningMSTPVLANInstancesDiverge returns the case evaluating that two
// VLANs running on separate MST instances choose independent links between
// the same pair of switches: sw2's per-instance path cost is inflated on l1
// for MSTI 1 (VLAN 10) and on l2 for MSTI 2 (VLAN 20), so MSTI 1 roots
// through l2 and MSTI 2 through l1, and a VLAN 10 frame and a VLAN 20 frame
// cross opposite links rather than the one link a single shared tree would
// pick for both.
func CasePlanningMSTPVLANInstancesDiverge() Case {
	spec := stpMSTFabricSpec(stpMSTRegion(1), stpMSTOverriddenRegion(1))

	frame10Untagged := expectedFact("bridge.frame",
		`src="02:00:00:00:04:12";dst="02:00:00:00:04:11";ether_type=2048;tags=[];payload_len=3`)
	frame10OnL2 := expectedFact("bridge.frame",
		`src="02:00:00:00:04:12";dst="02:00:00:00:04:11";ether_type=2048;`+
			`tags=[{tpid=33024;pcp=0;dei=false;vid=10}];payload_len=3`)
	frame20Untagged := expectedFact("bridge.frame",
		`src="02:00:00:00:04:22";dst="02:00:00:00:04:21";ether_type=2048;tags=[];payload_len=3`)
	frame20OnL1 := expectedFact("bridge.frame",
		`src="02:00:00:00:04:22";dst="02:00:00:00:04:21";ether_type=2048;`+
			`tags=[{tpid=33024;pcp=0;dei=false;vid=20}];payload_len=3`)

	hitOnL2 := expectedFact("bridge.fdb_decision", `fid=10;mac="02:00:00:00:04:11";present=true;port="l2";static=true`)
	hitOnD10 := expectedFact("bridge.fdb_decision", `fid=10;mac="02:00:00:00:04:11";present=true;port="d10";static=true`)
	hitOnL1 := expectedFact("bridge.fdb_decision", `fid=20;mac="02:00:00:00:04:21";present=true;port="l1";static=true`)
	hitOnD20 := expectedFact("bridge.fdb_decision", `fid=20;mac="02:00:00:00:04:21";present=true;port="d20";static=true`)

	expectedSteps := []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{frame10Untagged},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="v10";fid=10;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:04:12"},
			nil, []FactExpectation{expectedFact("bridge.fdb_decision", `fid=10;mac="02:00:00:00:04:12";present=true;port="v10";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:04:11"},
			[]FactExpectation{frame10Untagged}, []FactExpectation{hitOnL2}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "l2"},
			[]FactExpectation{frame10Untagged},
			[]FactExpectation{frame10OnL2, expectedFact("bridge.vlan_decision", `port="l2";fid=10;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "l2"},
			[]FactExpectation{frame10OnL2},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="l2";member="";fid=10;eligible=true;reason=""`)}),
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{frame10OnL2},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="l2";fid=10;pcp=0;dei=false;form="tagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:04:12"},
			nil, []FactExpectation{expectedFact("bridge.fdb_decision", `fid=10;mac="02:00:00:00:04:12";present=true;port="l2";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:04:11"},
			[]FactExpectation{frame10OnL2}, []FactExpectation{hitOnD10}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "d10"},
			[]FactExpectation{frame10OnL2},
			[]FactExpectation{frame10Untagged, expectedFact("bridge.vlan_decision", `port="d10";fid=10;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "d10"},
			[]FactExpectation{frame10Untagged},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="d10";member="";fid=10;eligible=true;reason=""`)}),
		expectedStep("host", trace.OpFilter, "host.mac.own", trace.Subject{Kind: "host", Key: "h2v10"},
			[]FactExpectation{
				expectedFact("fabric.mac", "02:00:00:00:04:11"),
				expectedFact("fabric.vlan_tags", "[]"),
			}, nil),

		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "20"},
			[]FactExpectation{frame20Untagged},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="v20";fid=20;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:04:22"},
			nil, []FactExpectation{expectedFact("bridge.fdb_decision", `fid=20;mac="02:00:00:00:04:22";present=true;port="v20";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:04:21"},
			[]FactExpectation{frame20Untagged}, []FactExpectation{hitOnL1}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "l1"},
			[]FactExpectation{frame20Untagged},
			[]FactExpectation{frame20OnL1, expectedFact("bridge.vlan_decision", `port="l1";fid=20;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "l1"},
			[]FactExpectation{frame20OnL1},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="l1";member="";fid=20;eligible=true;reason=""`)}),
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "20"},
			[]FactExpectation{frame20OnL1},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="l1";fid=20;pcp=0;dei=false;form="tagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:04:22"},
			nil, []FactExpectation{expectedFact("bridge.fdb_decision", `fid=20;mac="02:00:00:00:04:22";present=true;port="l1";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:04:21"},
			[]FactExpectation{frame20OnL1}, []FactExpectation{hitOnD20}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "d20"},
			[]FactExpectation{frame20OnL1},
			[]FactExpectation{frame20Untagged, expectedFact("bridge.vlan_decision", `port="d20";fid=20;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "d20"},
			[]FactExpectation{frame20Untagged},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="d20";member="";fid=20;eligible=true;reason=""`)}),
		expectedStep("host", trace.OpFilter, "host.mac.own", trace.Subject{Kind: "host", Key: "h2v20"},
			[]FactExpectation{
				expectedFact("fabric.mac", "02:00:00:00:04:21"),
				expectedFact("fabric.vlan_tags", "[]"),
			}, nil),
	}

	return Case{
		ID:      "planning/mstp-vlan-instances-diverge",
		UseCase: UseCasePlanning,
		Question: "When VLAN 10 and VLAN 20 run on separate MST instances with different per-instance " +
			"path costs, does each VLAN's frame cross the link its own instance elected, or does one " +
			"shared tree send both VLANs over the same link?",
		FalseAnswer: "Both VLANs block the same link, because spanning tree computes one shape for the " +
			"whole bridge regardless of which VLAN a frame carries",
		CurrentResult: "MSTI 1 (VLAN 10) roots through l2 and MSTI 2 (VLAN 20) roots through l1, so the " +
			"VLAN 10 frame crosses l2 and the VLAN 20 frame crosses l1: opposite links between the same " +
			"pair of switches",
		ExpectedMetadata: &MetadataExpectation{Status: analysis.Complete, Scope: analysis.WholeScope()},
		ExpectedOutcome:  trace.Forwarded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("vlan-classify"),
			trace.RuleID("learn"),
			trace.RuleID("unicast-hit"),
			trace.RuleID("vlan-tag-form"),
			trace.RuleID("transmit"),
			trace.RuleID("host.mac.own"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "10"},
			{Kind: "vlan", Key: "20"},
			{Kind: "mac", Key: "02:00:00:00:04:11"},
			{Kind: "mac", Key: "02:00:00:00:04:21"},
			{Kind: "port", Key: "l1"},
			{Kind: "port", Key: "l2"},
		},
		ExpectedFacts: []FactExpectation{hitOnL2, hitOnL1},
		ExpectedSteps: expectedSteps,
		Execute: func() (ExecutionResult, error) {
			_, j10, err := stpMSTInjectFrame(spec, 10)
			if err != nil {
				return ExecutionResult{}, err
			}

			fab, j20, err := stpMSTInjectFrame(spec, 20)
			if err != nil {
				return ExecutionResult{}, err
			}

			steps := append(slices.Clone(journeySteps(j10)), journeySteps(j20)...)

			return ExecutionResult{
				Outcome:  journeyHopOutcome(j20),
				Reason:   journeyHopReason(j20),
				Steps:    steps,
				Metadata: j20.Metadata,
				Switch:   fab.Switch("sw1"),
				Journey:  &j20,
			}, nil
		},
	}
}

// CaseTopologyShadowingMSTRegionBoundary returns the case evaluating that a
// region boundary holds the MSTIs to the CIST's answer rather than letting
// them compute one of their own: sw1 and sw2 name the same region but a
// different revision, so every port between them is a boundary port, and
// both VLAN 10 (MSTI 1) and VLAN 20 (MSTI 2) must cross whichever link the
// CIST itself forwards on.
func CaseTopologyShadowingMSTRegionBoundary() Case {
	// sw2 carries the same per-instance path cost overrides
	// [CasePlanningMSTPVLANInstancesDiverge] uses to split the two instances
	// apart, but at a different region revision: the configuration
	// identifier no longer matches sw1's, both links become boundary ports,
	// and an MSTI takes the CIST port's role outright rather than computing
	// its own, so the overrides that flipped MSTI 1 and MSTI 2 onto opposite
	// links there have no effect here.
	spec := stpMSTFabricSpec(stpMSTRegion(1), stpMSTOverriddenRegion(2))

	frame10Untagged := expectedFact("bridge.frame",
		`src="02:00:00:00:04:12";dst="02:00:00:00:04:11";ether_type=2048;tags=[];payload_len=3`)
	frame10OnL2 := expectedFact("bridge.frame",
		`src="02:00:00:00:04:12";dst="02:00:00:00:04:11";ether_type=2048;`+
			`tags=[{tpid=33024;pcp=0;dei=false;vid=10}];payload_len=3`)
	frame20Untagged := expectedFact("bridge.frame",
		`src="02:00:00:00:04:22";dst="02:00:00:00:04:21";ether_type=2048;tags=[];payload_len=3`)
	frame20OnL1 := expectedFact("bridge.frame",
		`src="02:00:00:00:04:22";dst="02:00:00:00:04:21";ether_type=2048;`+
			`tags=[{tpid=33024;pcp=0;dei=false;vid=20}];payload_len=3`)

	hitOnL2 := expectedFact("bridge.fdb_decision", `fid=10;mac="02:00:00:00:04:11";present=true;port="l2";static=true`)
	hitOnL1 := expectedFact("bridge.fdb_decision", `fid=20;mac="02:00:00:00:04:21";present=true;port="l1";static=true`)
	hitOnD20 := expectedFact("bridge.fdb_decision", `fid=20;mac="02:00:00:00:04:21";present=true;port="d20";static=true`)

	// gateBlocksL2 is the CIST's own answer for l2 (Alternate, Discarding),
	// which MSTI 1 (mstid=1) reports outright on the boundary port instead of
	// the role its own path cost override, ignored here, would have elected.
	gateBlocksL2 := expectedFact("stp.forwarding_decision",
		`port="l2";vid=10;state={mstid=1;role="Alternate";state="Discarding";block_reason="";priority=128;`+
			`path_cost=20000;designated_root="0/00:00:00:00:00:00";designated="0/00:00:00:00:00:00";`+
			`designated_port=0;designated_cost=0;point_to_point=true;edge=false;forward_transitions=0;`+
			`tx_bpdus=0;rx_bpdus=0;bad_bpdus=0;send_rstp=true};learns=false;forwards=false`)

	expectedSteps := []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{frame10Untagged},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="v10";fid=10;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:04:12"},
			nil, []FactExpectation{expectedFact("bridge.fdb_decision", `fid=10;mac="02:00:00:00:04:12";present=true;port="v10";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:04:11"},
			[]FactExpectation{frame10Untagged}, []FactExpectation{hitOnL2}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "l2"},
			[]FactExpectation{frame10Untagged},
			[]FactExpectation{frame10OnL2, expectedFact("bridge.vlan_decision", `port="l2";fid=10;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "l2"},
			[]FactExpectation{frame10OnL2},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="l2";member="";fid=10;eligible=true;reason=""`)}),
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{frame10OnL2},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="l2";fid=10;pcp=0;dei=false;form="tagged"`)}),
		expectedStep("relay", trace.OpDrop, "port-blocked", trace.Subject{Kind: "port", Key: "l2"},
			[]FactExpectation{gateBlocksL2},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="l2";member="";fid=10;eligible=false;reason="port-blocked"`)}),

		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "20"},
			[]FactExpectation{frame20Untagged},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="v20";fid=20;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:04:22"},
			nil, []FactExpectation{expectedFact("bridge.fdb_decision", `fid=20;mac="02:00:00:00:04:22";present=true;port="v20";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:04:21"},
			[]FactExpectation{frame20Untagged}, []FactExpectation{hitOnL1}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "l1"},
			[]FactExpectation{frame20Untagged},
			[]FactExpectation{frame20OnL1, expectedFact("bridge.vlan_decision", `port="l1";fid=20;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "l1"},
			[]FactExpectation{frame20OnL1},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="l1";member="";fid=20;eligible=true;reason=""`)}),
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "20"},
			[]FactExpectation{frame20OnL1},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="l1";fid=20;pcp=0;dei=false;form="tagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:04:22"},
			nil, []FactExpectation{expectedFact("bridge.fdb_decision", `fid=20;mac="02:00:00:00:04:22";present=true;port="l1";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:04:21"},
			[]FactExpectation{frame20OnL1}, []FactExpectation{hitOnD20}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "d20"},
			[]FactExpectation{frame20OnL1},
			[]FactExpectation{frame20Untagged, expectedFact("bridge.vlan_decision", `port="d20";fid=20;pcp=0;dei=false;form="egress"`)}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "d20"},
			[]FactExpectation{frame20Untagged},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="d20";member="";fid=20;eligible=true;reason=""`)}),
		expectedStep("host", trace.OpFilter, "host.mac.own", trace.Subject{Kind: "host", Key: "h2v20"},
			[]FactExpectation{
				expectedFact("fabric.mac", "02:00:00:00:04:21"),
				expectedFact("fabric.vlan_tags", "[]"),
			}, nil),
	}

	return Case{
		ID:      "topology-shadowing/mst-region-boundary",
		UseCase: UseCaseTopologyShadowing,
		Question: "When sw1 and sw2 name the same MST region but a different revision, so every port " +
			"between them is a boundary port, do MSTI 1 and MSTI 2 follow the CIST's blocking decision, " +
			"or do they compute an independent answer that could disagree with it?",
		FalseAnswer: "The MSTIs ignore the boundary and elect their own roles on it, which could block a " +
			"different link than the CIST does",
		CurrentResult: "l2 is a boundary port and the CIST itself blocks it (Alternate, Discarding); " +
			"MSTI 1 reports that same block on l2 for VLAN 10 rather than the role its own (ignored) " +
			"path cost override would have elected, and VLAN 20 crosses l1, the link the CIST forwards on",
		ExpectedMetadata: &MetadataExpectation{Status: analysis.Complete, Scope: analysis.WholeScope()},
		ExpectedOutcome:  trace.Dropped,
		ExpectedReason:   bridge.ReasonPortBlocked,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("vlan-classify"),
			trace.RuleID("learn"),
			trace.RuleID("unicast-hit"),
			trace.RuleID("vlan-tag-form"),
			trace.RuleID("transmit"),
			trace.RuleID("port-blocked"),
			trace.RuleID("host.mac.own"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "10"},
			{Kind: "vlan", Key: "20"},
			{Kind: "mac", Key: "02:00:00:00:04:11"},
			{Kind: "mac", Key: "02:00:00:00:04:21"},
			{Kind: "port", Key: "l1"},
			{Kind: "port", Key: "l2"},
		},
		ExpectedFacts: []FactExpectation{gateBlocksL2, hitOnL1},
		ExpectedSteps: expectedSteps,
		Execute: func() (ExecutionResult, error) {
			fab, j10, err := stpMSTInjectFrame(spec, 10)
			if err != nil {
				return ExecutionResult{}, err
			}

			_, j20, err := stpMSTInjectFrame(spec, 20)
			if err != nil {
				return ExecutionResult{}, err
			}

			steps := append(slices.Clone(journeySteps(j10)), journeySteps(j20)...)

			return ExecutionResult{
				Outcome:  journeyHopOutcome(j10),
				Reason:   journeyHopReason(j10),
				Steps:    steps,
				Metadata: j10.Metadata,
				Switch:   fab.Switch("sw1"),
				Journey:  &j10,
			}, nil
		},
	}
}

// CaseTroubleshootingStaleRootAgesOut returns the case evaluating that
// information whose message age has reached the max age its own BPDU carries is
// discarded rather than stored, so a BPDU circulating a root that no longer
// exists cannot refresh a port's timer on every hop and hold it blocked.
func CaseTroubleshootingStaleRootAgesOut() Case {
	return Case{
		Execute: func() (ExecutionResult, error) {
			sw, err := stpCaseSwitch(map[string]stp.Port{
				"p1": {},
				"p2": {},
				"p9": {AdminEdge: true},
			})
			if err != nil {
				return ExecutionResult{}, err
			}

			// Two forward delays carry both uplinks through Learning to
			// Forwarding, one rung per timer.
			sw.Wake(stpCaseStart.Add(16 * time.Second))
			sw.Wake(stpCaseStart.Add(32 * time.Second))

			// p2 hears the root bridge itself and becomes the root port, which
			// fixes this bridge's own path cost to the root.
			sw.Forward(stpCaseStart.Add(33*time.Second), "p2", stpCaseBPDU(4096, 0, 0))

			// A second bridge claims the same root on p1 at a cost that beats
			// this bridge's own, so storing the claim would make p1 Alternate
			// and blocked. Its message age has already reached the max age it
			// carries, and every circulating copy would refresh the timer that
			// holds the port there.
			sw.Forward(stpCaseStart.Add(34*time.Second), "p1", stpCaseBPDU(8192, 100, 20*time.Second))

			fwd := sw.Forward(stpCaseStart.Add(35*time.Second), "p9", stpCaseDataFrame())

			return ExecutionResult{
				Outcome:  fwd.Outcome,
				Reason:   fwd.Reason,
				Steps:    fwd.Steps,
				Metadata: fwd.Metadata,
				Switch:   sw,
				Forward:  &fwd,
			}, nil
		},
		ID:      "troubleshooting/stale-root-ages-out",
		UseCase: UseCaseTroubleshooting,
		Question: "When a BPDU naming a root that no longer exists keeps circulating, does the port " +
			"it arrives on stay blocked by that root?",
		FalseAnswer: "The port stays blocked forever, because every circulating copy refreshes the " +
			"information timer regardless of how many hops the message has already taken",
		CurrentResult: "Information whose message age has reached the max age its own BPDU carries is " +
			"discarded rather than stored, so p1 stays Designated and Forwarding instead of becoming " +
			"Alternate, and the frame is delivered over it",
		ExpectedMetadata: stpCaseCompleteMetadata(),
		ExpectedOutcome:  trace.Forwarded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("default-vlan"),
			trace.RuleID("learn"),
			trace.RuleID("unicast-hit"),
			trace.RuleID("transmit"),
		},
		ExpectedSubjects: stpCaseCommonSubjects(),
		ExpectedFacts:    []FactExpectation{stpCaseHit},
		ExpectedSteps: append(stpCaseCommonSteps(),
			expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "p1"},
				[]FactExpectation{stpCaseFrame},
				[]FactExpectation{expectedFact("bridge.egress_decision", `port="p1";member="";fid=0;eligible=true;reason=""`)}),
		),
		ExpectedForwardMetadata: stpCaseCompleteMetadata(),
	}
}

// CaseTroubleshootingBPDUGuardDisablesEdge returns the case evaluating that a
// BPDU arriving on a guarded edge port disables the port for spanning tree,
// rather than leaving it forwarding as an edge port that never expected one.
func CaseTroubleshootingBPDUGuardDisablesEdge() Case {
	return Case{
		Execute: func() (ExecutionResult, error) {
			sw, err := stpCaseSwitch(map[string]stp.Port{
				"p1": {AdminEdge: true, BPDUGuard: true},
				"p9": {AdminEdge: true},
			})
			if err != nil {
				return ExecutionResult{}, err
			}

			// The unexpected bridge announces itself on the access port.
			sw.Forward(stpCaseStart.Add(time.Second), "p1", stpCaseSuperiorBPDU(0))

			// A frame the forwarding database resolves to p1 now meets a port
			// the guard has disabled.
			fwd := sw.Forward(stpCaseStart.Add(2*time.Second), "p9", stpCaseDataFrame())

			return ExecutionResult{
				Outcome:  fwd.Outcome,
				Reason:   fwd.Reason,
				Steps:    fwd.Steps,
				Metadata: fwd.Metadata,
				Switch:   sw,
				Forward:  &fwd,
			}, nil
		},
		ID:      "troubleshooting/bpdu-guard-disables-edge",
		UseCase: UseCaseTroubleshooting,
		Question: "After an unexpected BPDU arrives on an access port configured with BPDU guard, " +
			"does the port keep forwarding?",
		FalseAnswer: "The edge port keeps forwarding, because an edge port is not part of the tree " +
			"and the BPDU is ignored",
		CurrentResult: "The port is disabled for spanning tree with reason bpdu-guard and discards, " +
			"so the frame is dropped port-blocked and only a link down and up recovers the port",
		ExpectedMetadata: stpCaseCompleteMetadata(),
		ExpectedOutcome:  trace.Dropped,
		ExpectedReason:   bridge.ReasonPortBlocked,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("default-vlan"),
			trace.RuleID("learn"),
			trace.RuleID("unicast-hit"),
			trace.RuleID("port-blocked"),
		},
		ExpectedSubjects: stpCaseCommonSubjects(),
		ExpectedFacts:    []FactExpectation{stpCaseBPDUGuardGate},
		ExpectedSteps:    stpCaseBlockedSteps(stpCaseBPDUGuardGate),

		ExpectedForwardMetadata: stpCaseCompleteMetadata(),
	}
}

// stpCaseBPDUGuardGate is p1's state once the guard has disabled it: Disabled
// and Discarding, with the reason naming the guard rather than leaving a reader
// to infer it from a role that says only "not participating".
var stpCaseBPDUGuardGate = stpCaseGateFact(
	`{mstid=0;role="Disabled";state="Discarding";block_reason="bpdu-guard";priority=128;path_cost=20000;` +
		`designated_root="0/00:00:00:00:00:00";designated="0/00:00:00:00:00:00";designated_port=0;` +
		`designated_cost=0;point_to_point=true;edge=true;forward_transitions=1;tx_bpdus=0;rx_bpdus=1;` +
		`bad_bpdus=0;send_rstp=true}`)

// CaseTroubleshootingLoopGuardUnidirectionalLink returns the case evaluating
// that a guarded port whose designated peer stops sending is held discarding
// rather than becoming designated and opening a loop.
func CaseTroubleshootingLoopGuardUnidirectionalLink() Case {
	return Case{
		Execute: func() (ExecutionResult, error) {
			sw, err := stpCaseSwitch(map[string]stp.Port{
				"p1": {LoopGuard: true},
				"p9": {AdminEdge: true},
			})
			if err != nil {
				return ExecutionResult{}, err
			}

			// p1 hears a better bridge and becomes the root port.
			sw.Forward(stpCaseStart.Add(time.Second), "p1", stpCaseSuperiorBPDU(0))

			// The link breaks in the receive direction only: nothing more
			// arrives, and three hello times later the information expires.
			sw.Wake(stpCaseStart.Add(9 * time.Second))

			fwd := sw.Forward(stpCaseStart.Add(10*time.Second), "p9", stpCaseDataFrame())

			return ExecutionResult{
				Outcome:  fwd.Outcome,
				Reason:   fwd.Reason,
				Steps:    fwd.Steps,
				Metadata: fwd.Metadata,
				Switch:   sw,
				Forward:  &fwd,
			}, nil
		},
		ID:      "troubleshooting/loop-guard-unidirectional-link",
		UseCase: UseCaseTroubleshooting,
		Question: "When a port stops receiving the BPDUs that made it the root port, does it become " +
			"designated and start forwarding?",
		FalseAnswer: "The port becomes Designated and forwards, which on a link that is broken in one " +
			"direction only opens a loop",
		CurrentResult: "Loop guard holds the port Alternate and Discarding with reason " +
			"loop-inconsistent, so the frame is dropped port-blocked and the next BPDU on the port " +
			"restores normal role selection",
		ExpectedMetadata: stpCaseCompleteMetadata(),
		ExpectedOutcome:  trace.Dropped,
		ExpectedReason:   bridge.ReasonPortBlocked,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("default-vlan"),
			trace.RuleID("learn"),
			trace.RuleID("unicast-hit"),
			trace.RuleID("port-blocked"),
		},
		ExpectedSubjects: stpCaseCommonSubjects(),
		ExpectedFacts:    []FactExpectation{stpCaseLoopGuardGate},
		ExpectedSteps:    stpCaseBlockedSteps(stpCaseLoopGuardGate),

		ExpectedForwardMetadata: stpCaseCompleteMetadata(),
	}
}

// stpCaseLoopGuardGate is p1's state once its information expired: Alternate
// and Discarding rather than the Designated and Forwarding it would reach
// without the guard, which on a link broken in one direction is a loop.
var stpCaseLoopGuardGate = stpCaseGateFact(
	`{mstid=0;role="Alternate";state="Discarding";block_reason="loop-inconsistent";priority=128;path_cost=20000;` +
		`designated_root="0/00:00:00:00:00:00";designated="0/00:00:00:00:00:00";designated_port=0;` +
		`designated_cost=0;point_to_point=true;edge=false;forward_transitions=1;tx_bpdus=1;rx_bpdus=1;` +
		`bad_bpdus=0;send_rstp=true}`)
