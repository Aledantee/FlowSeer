package simtest

import (
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// stpPVSTTrees returns the per-VLAN trees the PVST corpus case gives a
// switch: one for VLAN 10 and one for VLAN 20, carrying the per-port path
// costs the caller names.
func stpPVSTTrees(costs map[vlan.ID]map[string]uint32) *stp.PVST {
	trees := make(map[vlan.ID]stp.Tree, 2)
	for _, vid := range []vlan.ID{10, 20} {
		tree := stp.Tree{}
		if ports, ok := costs[vid]; ok {
			tree.Ports = make(map[string]stp.InstancePort, len(ports))
			for name, cost := range ports {
				tree.Ports[name] = stp.InstancePort{PathCost: cost}
			}
		}
		trees[vid] = tree
	}

	return &stp.PVST{Trees: trees}
}

// casePlanningPVSTPerVLANRoot returns the case evaluating that VLAN 10,
// running its own rapid spanning tree, crosses the link its own tree elected
// rather than whichever link one shared tree would pick for every VLAN: sw2's
// per-VLAN path cost is inflated on l1 for VLAN 10, so VLAN 10's tree roots
// through l2 and the frame crosses it. It shares [stpMSTFabricSpec]'s
// topology through [stpPVSTFabricSpec], so the per-VLAN answer is compared
// against the MST cases over one fixture rather than two.
func casePlanningPVSTPerVLANRoot() Case {
	spec, err := stpPVSTFabricSpec(
		stpPVSTTrees(nil),
		stpPVSTTrees(map[vlan.ID]map[string]uint32{
			10: {"l1": 200_000},
			20: {"l2": 200_000},
		}),
	)

	frame10Untagged := expectedFact("bridge.frame",
		`src="02:00:00:00:04:12";dst="02:00:00:00:04:11";ether_type=2048;tags=[];payload_len=3`)
	frame10OnL2 := expectedFact("bridge.frame",
		`src="02:00:00:00:04:12";dst="02:00:00:00:04:11";ether_type=2048;`+
			`tags=[{tpid=33024;pcp=0;dei=false;vid=10}];payload_len=3`)

	hitOnL2 := expectedFact("bridge.fdb_decision", `fid=10;mac="02:00:00:00:04:11";present=true;port="l2";static=true`)
	hitOnD10 := expectedFact("bridge.fdb_decision", `fid=10;mac="02:00:00:00:04:11";present=true;port="d10";static=true`)

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
	}

	return Case{
		ID:      "planning/pvst-per-vlan-root",
		UseCase: UseCasePlanning,
		Question: "When every VLAN runs its own rapid spanning tree and sw2's VLAN 10 path cost is " +
			"inflated on l1, does VLAN 10's frame cross l2, the link VLAN 10's own tree elected, or " +
			"does one shared tree send it over whichever link the bridge picks for every VLAN?",
		FalseAnswer: "One spanning tree covers the bridge, so both VLANs follow the same link " +
			"regardless of what any one VLAN's own costs say",
		CurrentResult:    "VLAN 10's tree roots through l2, so the VLAN 10 frame crosses l2 and is delivered to h2v10",
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
			{Kind: "mac", Key: "02:00:00:00:04:11"},
			{Kind: "port", Key: "l2"},
		},
		ExpectedFacts: []FactExpectation{hitOnL2},
		ExpectedSteps: expectedSteps,
		Execute: func() (ExecutionResult, error) {
			if err != nil {
				return ExecutionResult{}, err
			}

			fab, j10, err := stpMSTInjectFrame(spec, 10)
			if err != nil {
				return ExecutionResult{}, err
			}

			return ExecutionResult{
				Outcome:  journeyHopOutcome(j10),
				Reason:   journeyHopReason(j10),
				Steps:    journeySteps(j10),
				Metadata: j10.Metadata,
				Switch:   fab.Device("sw1").(*vswitch.Switch),
				Journey:  &j10,
			}, nil
		},
	}
}
