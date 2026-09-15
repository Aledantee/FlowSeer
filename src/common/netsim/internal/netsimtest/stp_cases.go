package netsimtest

import (
	"maps"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
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
// stopped.
func RegisterSTPCases(registry *Registry) {
	registry.MustRegister(CaseTroubleshootingStaleRootAgesOut())
	registry.MustRegister(CaseTroubleshootingBPDUGuardDisablesEdge())
	registry.MustRegister(CaseTroubleshootingLoopGuardUnidirectionalLink())
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
	`{role="Disabled";state="Discarding";block_reason="bpdu-guard";priority=128;path_cost=20000;` +
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
	`{role="Alternate";state="Discarding";block_reason="loop-inconsistent";priority=128;path_cost=20000;` +
		`designated_root="0/00:00:00:00:00:00";designated="0/00:00:00:00:00:00";designated_port=0;` +
		`designated_cost=0;point_to_point=true;edge=false;forward_transitions=1;tx_bpdus=1;rx_bpdus=1;` +
		`bad_bpdus=0;send_rstp=true}`)
