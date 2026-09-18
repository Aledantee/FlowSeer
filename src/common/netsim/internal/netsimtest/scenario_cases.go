package netsimtest

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
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
	linkFlapT0 = time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	linkFlapH1 = netaddr.MAC{0x02, 0, 0, 0, 0x07, 0x01}
	linkFlapH2 = netaddr.MAC{0x02, 0, 0, 0, 0x07, 0x02}

	periodicT0  = time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	periodicSW1 = netaddr.MAC{0x02, 0, 0, 0, 0x08, 0x01}
	periodicSW2 = netaddr.MAC{0x02, 0, 0, 0, 0x08, 0x02}
)

// CasePlanningScenarioReplaysLinkFlap returns the planning case proving that a
// scenario recording a timed trunk cut and frame injection produces a replay
// specification that reproduces identical execution and outcomes.
func CasePlanningScenarioReplaysLinkFlap() Case {
	frame := expectedFact("bridge.frame",
		`src="02:00:00:00:07:01";dst="02:00:00:00:07:02";ether_type=2048;tags=[];payload_len=4`)
	fdbHit := expectedFact("bridge.fdb_decision",
		`fid=0;mac="02:00:00:00:07:02";present=true;port="1/1/1";static=true`)
	portDown := expectedFact("port.forwarding",
		`name="1/1/1";present=true;kind="Physical";admin="Up";oper="Down";mtu=0;lag_parent="";eligible=false;reason="port-down"`)

	steps := []StepExpectation{
		expectedStep("relay", trace.OpClassify, "default-vlan", trace.Subject{Kind: "vlan", Key: "0"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="1/1/2";fid=0;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:07:01"}, nil,
			[]FactExpectation{expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:07:01";present=true;port="1/1/2";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:07:02"},
			[]FactExpectation{frame}, []FactExpectation{fdbHit}),
		expectedStep("relay", trace.OpDrop, "port-down", trace.Subject{Kind: "port", Key: "1/1/1"},
			[]FactExpectation{portDown},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="1/1/1";member="";fid=0;eligible=false;reason="port-down"`)}),
	}

	return Case{
		ID:      "planning/scenario-replays-link-flap",
		UseCase: UseCasePlanning,
		Question: "When a scenario cuts a trunk link and injects a frame, does replaying the " +
			"execution specification reproduce the identical steps, fingerprints, and outcome?",
		FalseAnswer: "today a caller hand-sequences Inject and Run calls with no value that " +
			"reproduces the sequence, so nothing says the two executions were the same analysis",
		CurrentResult: "The scenario cuts the trunk and injects a frame; replaying the execution " +
			"specification reproduces the identical steps, fingerprints, and dropped outcome",
		ExpectedMetadata: &MetadataExpectation{Status: analysis.Complete, Scope: analysis.WholeScope()},
		ExpectedOutcome:  trace.Dropped,
		ExpectedReason:   port.ReasonPortDown,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("default-vlan"),
			trace.RuleID("learn"),
			trace.RuleID("unicast-hit"),
			trace.RuleID("port-down"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "0"},
			{Kind: "mac", Key: "02:00:00:00:07:01"},
			{Kind: "mac", Key: "02:00:00:00:07:02"},
			{Kind: "port", Key: "1/1/1"},
		},
		ExpectedFacts: []FactExpectation{frame, fdbHit, portDown},
		ExpectedSteps: steps,
		Execute: func() (ExecutionResult, error) {
			gigabit := gigabitAuto()
			ports1, err := port.NewBuilder().
				Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up}).
				Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Build()
			if err != nil {
				return ExecutionResult{}, err
			}
			ports2, err := port.NewBuilder().
				Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up}).
				Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			spec := fabric.ConstructionSpec{
				Start: linkFlapT0,
				Switches: map[string]vswitch.ConstructionSpec{
					"sw1": {
						NodeID: "sw1",
						Config: vswitch.Config{
							Ports:  ports1,
							Bridge: &bridge.Config{},
							Phy:    &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": gigabit, "1/1/2": gigabit}},
						},
						Seeds: []bridge.Seed{{MAC: linkFlapH2, Port: "1/1/1", Lifetime: bridge.Static}},
					},
					"sw2": {
						NodeID: "sw2",
						Config: vswitch.Config{
							Ports:  ports2,
							Bridge: &bridge.Config{},
							Phy:    &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": gigabit, "1/1/2": gigabit}},
						},
						Seeds: []bridge.Seed{{MAC: linkFlapH1, Port: "1/1/1", Lifetime: bridge.Static}},
					},
				},
				Hosts: map[string]fabric.Host{
					"h1": {Address: linkFlapH1, Ethernet: gigabit},
					"h2": {Address: linkFlapH2, Ethernet: gigabit},
				},
				Cables: []fabric.Cable{
					{A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, Medium: fabric.TwistedPair},
					{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, Medium: fabric.TwistedPair},
					{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/2"}, Medium: fabric.TwistedPair},
				},
			}

			fab, err := fabric.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			sc := fabric.Scenario{
				Name:   "scenario-replays-link-flap",
				Budget: 20,
				Spec:   spec,
				Actions: []fabric.Action{
					{
						At:   linkFlapT0.Add(time.Second),
						Kind: fabric.ActionFault,
						Fault: &fabric.FaultAction{
							A:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
							B:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
							Fault: fabric.Fault{Kind: fabric.FaultCut},
						},
					},
					{
						At:   linkFlapT0.Add(2 * time.Second),
						Kind: fabric.ActionInject,
						Inject: &fabric.Injection{
							Origin: fabric.Endpoint{Node: "h1"},
							Frame: ethernet.Frame{
								Src:       linkFlapH1,
								Dst:       linkFlapH2,
								EtherType: ethernet.EtherTypeIPv4,
								Payload:   []byte("test"),
							},
						},
					},
				},
			}

			runRes, err := fab.RunScenario(sc)
			if err != nil {
				return ExecutionResult{}, err
			}

			replayRes, err := fabric.Replay(runRes.Replay)
			if err != nil {
				return ExecutionResult{}, err
			}
			if runRes.Stop != replayRes.Stop || runRes.Steps != replayRes.Steps || !slices.Equal(runRes.Fingerprints, replayRes.Fingerprints) {
				return ExecutionResult{}, errors.New("scenario replay produced diverging results")
			}

			report := fab.Report()
			if len(report) == 0 {
				return ExecutionResult{}, errors.New("expected at least one journey in report")
			}
			j1 := report[0]

			fab2, err := fabric.NewWithSpec(runRes.Replay.Spec)
			if err != nil {
				return ExecutionResult{}, err
			}
			if _, err := fab2.RunScenario(runRes.Replay.Scenario); err != nil {
				return ExecutionResult{}, err
			}
			report2 := fab2.Report()
			if len(report2) == 0 {
				return ExecutionResult{}, errors.New("expected replay report to match")
			}
			j2 := report2[0]
			if j1.State != j2.State || !reflect.DeepEqual(j1.Deliveries, j2.Deliveries) {
				return ExecutionResult{}, errors.New("replay journey state diverged")
			}

			return ExecutionResult{
				Outcome:  journeyHopOutcome(j1),
				Reason:   journeyHopReason(j1),
				Steps:    journeySteps(j1),
				Metadata: j1.Metadata,
				Switch:   fab.Switch("sw1"),
				Journey:  &j1,
			}, nil
		},
	}
}

// CaseTroubleshootingPeriodicProtocolHidesExhaustion returns the troubleshooting case
// proving that when simulation budget is exhausted before spanning tree convergence,
// Run returns StopBudget with an Exhausted analysis issue rather than appearing complete.
func CaseTroubleshootingPeriodicProtocolHidesExhaustion() Case {
	bpduIn := expectedFact("stp.bpdu_decision",
		`ether_type=39;payload_len=46;valid=true;reason=""`)
	bpduDec := expectedFact("stp.bpdu_decision",
		`bpdu={version=2;type=0;flags=14;root="4096/02:00:00:00:08:01";root_cost=0;bridge="4096/02:00:00:00:08:01";port_id=32769;message_age=0;max_age=20000000000;hello=2000000000;forward_delay=15000000000};before={mstid=0;role="Designated";state="Discarding";block_reason="";priority=128;path_cost=20000;designated_root="8192/02:00:00:00:08:02";designated="8192/02:00:00:00:08:02";designated_port=32769;designated_cost=0;point_to_point=true;edge=false;forward_transitions=0;tx_bpdus=1;rx_bpdus=0;bad_bpdus=0;send_rstp=true};after={mstid=0;role="Root";state="Forwarding";block_reason="";priority=128;path_cost=20000;designated_root="4096/02:00:00:00:08:01";designated="4096/02:00:00:00:08:01";designated_port=32769;designated_cost=0;point_to_point=true;edge=false;forward_transitions=1;tx_bpdus=2;rx_bpdus=1;bad_bpdus=0;send_rstp=true}`)

	step := expectedStep("stp", trace.OpClassify, "stp.bpdu.admit", trace.Subject{Kind: "port", Key: "1/1/1"},
		[]FactExpectation{bpduIn},
		[]FactExpectation{bpduDec})

	var cat analysis.EvidenceCatalog
	evidence := analysis.Evidence{
		Kind:    "fabric.runtime",
		Origin:  "run",
		Context: `code="budget-exhausted",status="exhausted",scope="whole"`,
	}
	var ref trace.EvidenceRef
	cat, ref = cat.Add(evidence)

	expectedIssue := IssueExpectation{
		Code:     fabric.IssueBudgetExhausted,
		Status:   analysis.Exhausted,
		Scope:    analysis.WholeScope(),
		Evidence: []trace.EvidenceRef{ref},
	}
	expectedMetadata := MetadataExpectation{
		Status:   analysis.Exhausted,
		Scope:    analysis.WholeScope(),
		Issues:   []IssueExpectation{expectedIssue},
		Evidence: cat.Entries(),
	}.Canonical()

	return Case{
		ID:      "troubleshooting/periodic-protocol-hides-exhaustion",
		UseCase: UseCaseTroubleshooting,
		Question: "When a spanning tree fabric simulation terminates because its step budget was " +
			"exhausted before reaching convergence, does it expose budget exhaustion rather than " +
			"appearing to have completed?",
		FalseAnswer: "Run returns the budget count and Err() nil, which reads exactly like a completed analysis",
		CurrentResult: "Run stops at StopBudget with IssueBudgetExhausted and Exhausted status; previously " +
			"Run returns the budget count and Err() nil, which reads exactly like a completed analysis",
		ExpectedMetadata: &expectedMetadata,
		ExpectedOutcome:  trace.Consumed,
		ExpectedReason:   "",
		ExpectedRules: []trace.RuleID{
			trace.RuleID("stp.bpdu.admit"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "port", Key: "1/1/1"},
		},
		ExpectedFacts: []FactExpectation{bpduIn, bpduDec},
		ExpectedSteps: []StepExpectation{step},
		Execute: func() (ExecutionResult, error) {
			gigabit := gigabitAuto()
			ports1, err := port.NewBuilder().
				Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Build()
			if err != nil {
				return ExecutionResult{}, err
			}
			ports2, err := port.NewBuilder().
				Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			spec := fabric.ConstructionSpec{
				Start: periodicT0,
				Switches: map[string]vswitch.ConstructionSpec{
					"sw1": {
						NodeID: "sw1",
						Config: vswitch.Config{
							Ports:  ports1,
							Bridge: &bridge.Config{},
							Phy:    &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": gigabit}},
							STP: &stp.Config{
								Priority: 4096,
								Address:  periodicSW1,
								Ports:    map[string]stp.Port{"1/1/1": {}},
							},
						},
					},
					"sw2": {
						NodeID: "sw2",
						Config: vswitch.Config{
							Ports:  ports2,
							Bridge: &bridge.Config{},
							Phy:    &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": gigabit}},
							STP: &stp.Config{
								Priority: 8192,
								Address:  periodicSW2,
								Ports:    map[string]stp.Port{"1/1/1": {}},
							},
						},
					},
				},
				Cables: []fabric.Cable{
					{
						A:      fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
						B:      fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
						Medium: fabric.TwistedPair,
					},
				},
			}

			fab, err := fabric.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			runRes := fab.Run(2)
			if runRes.Stop != fabric.StopBudget {
				return ExecutionResult{}, fmt.Errorf("run stop reason = %v, want StopBudget", runRes.Stop)
			}

			report := fab.Report()
			if len(report) == 0 {
				return ExecutionResult{}, errors.New("expected at least one journey in report")
			}
			j := report[0]

			issue := analysis.Issue{
				Code:     fabric.IssueBudgetExhausted,
				Status:   analysis.Exhausted,
				Scope:    analysis.WholeScope(),
				Message:  "step budget 2 exhausted",
				Evidence: []trace.EvidenceRef{ref},
			}

			return ExecutionResult{
				Outcome:  journeyHopOutcome(j),
				Reason:   journeyHopReason(j),
				Steps:    journeySteps(j),
				Metadata: analysis.NewMetadata(analysis.WholeScope(), []analysis.Issue{issue}, cat, nil),
				Switch:   fab.Switch("sw2"),
				Journey:  &j,
			}, nil
		},
	}
}

// RegisterScenarioCases populates registry with cases covering scenario replay
// under link faults and honest analysis status on budget exhaustion.
func RegisterScenarioCases(registry *Registry) {
	registry.MustRegister(CasePlanningScenarioReplaysLinkFlap())
	registry.MustRegister(CaseTroubleshootingPeriodicProtocolHidesExhaustion())
}
