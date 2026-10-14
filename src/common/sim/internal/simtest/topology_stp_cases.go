package simtest

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

var (
	unknownUplinkH1 = netaddr.MAC{0x02, 0, 0, 0, 1, 0x01}
	unknownUplinkH2 = netaddr.MAC{0x02, 0, 0, 0, 1, 0x02}
)

// unknownUplinkRuntimeEvidence returns the auto-generated runtime evidence protocol-link-unknown
// cites for one switch port: the port's own forwarding fact, exactly as [vswitch.Switch.Receive]
// composes it. Matching this construction lets the case's expected evidence catalog resolve to
// the same references the runtime metadata carries, without asserting message wording.
func unknownUplinkRuntimeEvidence(node, portName string) analysis.Evidence {
	return analysis.Evidence{
		Kind:   "vswitch.runtime",
		Origin: "forward",
		Context: `code="protocol-link-unknown",status="incomplete",scope="node[\"` + node + `\"]/port[\"` + portName + `\"]",` +
			`fact["port.forwarding"]="name=\"` + portName + `\";present=true;kind=\"Physical\";admin=\"Up\";` +
			`oper=\"Up\";mtu=0;lag_parent=\"\";eligible=true;reason=\"\""`,
	}
}

// caseTopologyShadowingUnknownUplinkSTP returns the case for an Unknown link that spanning tree
// depends on: sw2's port 1/1/2 reports no Ethernet facts, so the redundant sw1-sw2 uplink over
// it is Unknown. Spanning tree can no longer trust any port's role on either switch, so
// [vswitch.IssueProtocolLinkUnknown] marks every STP port; a known-unicast journey forwarded
// over the other uplink still consults two of those ports per switch and carries their issues,
// Incomplete, even though it never crosses the Unknown link itself.
func caseTopologyShadowingUnknownUplinkSTP() Case {
	gigabit := gigabitAuto()

	var cat analysis.EvidenceCatalog
	refFor := make(map[string]trace.EvidenceRef, 4)
	var issues []IssueExpectation
	for _, end := range []struct{ node, port string }{
		{"sw1", "1/1/1"}, {"sw1", "1/1/3"}, {"sw2", "1/1/1"}, {"sw2", "1/1/3"},
	} {
		var ref trace.EvidenceRef
		cat, ref = cat.Add(unknownUplinkRuntimeEvidence(end.node, end.port))
		refFor[end.node+":"+end.port] = ref
		issues = append(issues, IssueExpectation{
			Code:     vswitch.IssueProtocolLinkUnknown,
			Status:   analysis.Incomplete,
			Scope:    analysis.PortScope(end.node, end.port),
			Evidence: []trace.EvidenceRef{ref},
		})
	}
	metadata := &MetadataExpectation{
		Status:   analysis.Incomplete,
		Scope:    analysis.WholeScope(),
		Issues:   issues,
		Evidence: cat.Entries(),
	}

	frame := expectedFact("bridge.frame", `src="02:00:00:00:01:01";dst="02:00:00:00:01:02";ether_type=2048;tags=[];payload_len=0`)
	sw1Hit := expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:01:02";present=true;port="1/1/1";static=true`)
	sw2Hit := expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:01:02";present=true;port="1/1/3";static=true`)
	macFact := expectedFact("fabric.mac", "02:00:00:00:01:02")
	steps := []StepExpectation{
		expectedStep("relay", trace.OpClassify, "default-vlan", trace.Subject{Kind: "vlan", Key: "0"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="1/1/3";fid=0;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:01:01"}, nil,
			[]FactExpectation{expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:01:01";present=true;port="1/1/3";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:01:02"},
			[]FactExpectation{frame}, []FactExpectation{sw1Hit}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "1/1/1"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="1/1/1";member="";fid=0;eligible=true;reason=""`)}),
		expectedStep("relay", trace.OpClassify, "default-vlan", trace.Subject{Kind: "vlan", Key: "0"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="1/1/1";fid=0;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:01:01"}, nil,
			[]FactExpectation{expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:01:01";present=true;port="1/1/1";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:01:02"},
			[]FactExpectation{frame}, []FactExpectation{sw2Hit}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "1/1/3"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="1/1/3";member="";fid=0;eligible=true;reason=""`)}),
		expectedStep("host", trace.OpFilter, "host.mac.own", trace.Subject{Kind: "host", Key: "h2"},
			[]FactExpectation{expectedFact("fabric.vlan_tags", "[]"), macFact}, nil),
	}

	return Case{
		ID:      "topology-shadowing/unknown-uplink-stp",
		UseCase: UseCaseTopologyShadowing,
		Question: "When a redundant sw1-sw2 uplink is Unknown because one end reports no " +
			"Ethernet facts, does a journey forwarded over spanning tree's other path still carry " +
			"protocol-link-unknown for the STP ports it consulted?",
		FalseAnswer:      "Reading the journey as Complete because it crossed only ports that are themselves Up, ignoring that spanning tree could re-elect roles once the unknown uplink resolves",
		CurrentResult:    "The frame reaches h2 over the known uplink; the journey is Incomplete with protocol-link-unknown on the two STP ports each switch's hop consulted",
		ExpectedMetadata: metadata,
		ExpectedOutcome:  trace.Forwarded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("unicast-hit"),
			trace.RuleID("host.mac.own"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "mac", Key: "02:00:00:00:01:02"},
			{Kind: "host", Key: "h2"},
		},
		ExpectedFacts: []FactExpectation{sw1Hit, sw2Hit, macFact},
		ExpectedSteps: steps,
		Execute: func() (ExecutionResult, error) {
			return executeTopologyShadowingUnknownUplinkSTP(gigabit)
		},
	}
}

func executeTopologyShadowingUnknownUplinkSTP(gigabit phy.Ethernet) (ExecutionResult, error) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	switchConfig := func(priority uint16, address netaddr.MAC, uplinkFacts map[string]phy.Ethernet) (vswitch.Config, error) {
		tbl, err := port.NewBuilder().Range("1/1/%d", 1, 3, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).Build()
		if err != nil {
			return vswitch.Config{}, err
		}

		return vswitch.Config{
			Ports:  tbl,
			Bridge: &bridge.Config{},
			STP: &stp.Config{
				Priority: priority,
				Address:  address,
				Ports:    map[string]stp.Port{"1/1/1": {}, "1/1/2": {}, "1/1/3": {}},
			},
			Phy: &phy.Config{Ethernet: uplinkFacts},
		}, nil
	}
	sw1Config, err := switchConfig(4096, netaddr.MAC{0, 0, 0, 0, 1, 1}, map[string]phy.Ethernet{"1/1/1": gigabit, "1/1/2": gigabit, "1/1/3": gigabit})
	if err != nil {
		return ExecutionResult{}, err
	}
	sw2Config, err := switchConfig(8192, netaddr.MAC{0, 0, 0, 0, 1, 2}, map[string]phy.Ethernet{"1/1/1": gigabit, "1/1/3": gigabit})
	if err != nil {
		return ExecutionResult{}, err
	}

	spec := assembly.ConstructionSpec{
		Start: t0,
		Switches: map[string]vswitch.ConstructionSpec{
			"sw1": {NodeID: "sw1", Config: sw1Config, Seeds: []bridge.Seed{{MAC: unknownUplinkH2, Port: "1/1/1", Lifetime: bridge.Static}}},
			"sw2": {NodeID: "sw2", Config: sw2Config, Seeds: []bridge.Seed{{MAC: unknownUplinkH2, Port: "1/1/3", Lifetime: bridge.Static}}},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: unknownUplinkH1, Ethernet: gigabit},
			"h2": {Address: unknownUplinkH2, Ethernet: gigabit},
		},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{Medium: cablemodel.TwistedPair}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
			{Cable: cablemodel.Cable{Medium: cablemodel.TwistedPair}, A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/2"}},
			{Cable: cablemodel.Cable{Medium: cablemodel.TwistedPair}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}},
			{Cable: cablemodel.Cable{Medium: cablemodel.TwistedPair}, A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/3"}},
		},
	}

	fab, err := assembly.NewWithSpec(spec)
	if err != nil {
		return ExecutionResult{}, err
	}
	fab.Run(fabric.RunOptions{Budget: 200, Window: 0})
	if err := validateReceiveReport(fab.Report()); err != nil {
		return ExecutionResult{}, err
	}

	fid, err := fab.Inject(fabric.Injection{
		At:     fab.Clock(),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Dst: unknownUplinkH2, Src: unknownUplinkH1, EtherType: ethernet.EtherTypeIPv4},
	})
	if err != nil {
		return ExecutionResult{}, err
	}
	fab.Run(fabric.RunOptions{Budget: 50, Window: 0})
	if err := validateReceiveReport(fab.Report()); err != nil {
		return ExecutionResult{}, err
	}

	journey, err := reportJourney(fab, fid)
	if err != nil {
		return ExecutionResult{}, err
	}
	fabricMetadata := fab.Metadata()

	return ExecutionResult{
		Outcome:        journeyHopOutcome(journey),
		Reason:         journeyHopReason(journey),
		Steps:          journeySteps(journey),
		Metadata:       journey.Metadata,
		Switch:         fab.Device("sw1").(*vswitch.Switch),
		Journey:        &journey,
		FabricMetadata: &fabricMetadata,
	}, nil
}
