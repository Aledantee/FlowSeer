package simtest

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// casePlanningPortVLANChange returns the baseline planning case evaluating a prospective port VLAN change.
func casePlanningPortVLANChange() Case {
	vid10 := vlan.ID(10)
	vid20 := vlan.ID(20)
	frame := expectedFact("bridge.frame", `src="00:11:22:33:44:01";dst="00:11:22:33:44:02";ether_type=2048;tags=[];payload_len=13`)
	currentSteps := []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="1/1/1";fid=10;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "00:11:22:33:44:02"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.fdb_decision", `fid=10;mac="00:11:22:33:44:02";present=true;port="1/1/2";static=true`)}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "1/1/2"},
			[]FactExpectation{frame},
			[]FactExpectation{
				frame,
				expectedFact("bridge.vlan_decision", `port="1/1/2";fid=10;pcp=0;dei=false;form="egress"`),
			}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "1/1/2"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="1/1/2";member="";fid=10;eligible=true;reason=""`)}),
	}
	expectedSteps := []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "20"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="1/1/1";fid=20;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLookup, "unicast-miss", trace.Subject{Kind: "mac", Key: "00:11:22:33:44:02"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.fdb_decision", `fid=20;mac="00:11:22:33:44:02";present=false;port="";static=false`)}),
		expectedStep("relay", trace.OpDrop, "no-egress", trace.Subject{Kind: "vlan", Key: "20"}, nil,
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="";member="";fid=20;eligible=false;reason="no-egress"`)}),
	}
	pvid10 := NewFactExpectation(bridge.PVIDFact(vid10))
	pvid20 := NewFactExpectation(bridge.PVIDFact(vid20))
	vlans10 := FactExpectation{TypeID: "bridge.vlan_id", Canonical: "10"}
	vlans20 := FactExpectation{TypeID: "bridge.vlan_id", Canonical: "20"}
	expectedChanges := []ChangeExpectation{
		expectedChange("vlan", trace.Subject{Kind: "port", Key: "1/1/1"}, "pvid", &pvid10, &pvid20),
		expectedChange("vlan", trace.Subject{Kind: "port", Key: "1/1/1"}, `untagged_vlan_ids["10"]`, &vlans10, nil),
		expectedChange("vlan", trace.Subject{Kind: "port", Key: "1/1/1"}, `untagged_vlan_ids["20"]`, nil, &vlans20),
	}

	return Case{
		ID:            "planning/port-vlan-change",
		UseCase:       UseCasePlanning,
		Question:      "Does reconfiguring an access switchport from VLAN 10 to VLAN 20 alter forwarding behavior and emit typed configuration diff facts without string parsing?",
		FalseAnswer:   "Silently ignoring the switchport reconfiguration, masking behavioral divergence behind identical prose strings, or requiring string parsing to observe diffs",
		CurrentResult: "vswitch.Diff produces typed PVID and VLAN member facts. vswitch.Compare detects forwarding divergence with the expected switch dropping traffic due to no-egress member ports in VLAN 20",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Dropped,
		ExpectedReason:  bridge.ReasonNoEgress,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("vlan-classify"),
			trace.RuleID("unicast-miss"),
			trace.RuleID("no-egress"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "port", Key: "1/1/1"},
		},
		ExpectedFacts: []FactExpectation{
			pvid10,
			pvid20,
		},
		ExpectedSteps:   expectedSteps,
		ExpectedChanges: expectedChanges,
		ExpectedComparison: &ComparisonExpectation{
			Current: ForwardExpectation{
				Outcome: trace.Forwarded,
				Metadata: MetadataExpectation{
					Status: analysis.Complete,
					Scope:  analysis.NodeScope(""),
				},
				Steps: currentSteps,
			},
			Expected: ForwardExpectation{
				Outcome: trace.Dropped,
				Reason:  bridge.ReasonNoEgress,
				Metadata: MetadataExpectation{
					Status: analysis.Complete,
					Scope:  analysis.NodeScope(""),
				},
				Steps: cloneStepExpectations(expectedSteps),
			},
			Same: false,
		},
		Execute: func() (ExecutionResult, error) {
			return executePlanningPortVLANChange(vid10, vid20)
		},
	}
}

func executePlanningPortVLANChange(vid10, vid20 vlan.ID) (ExecutionResult, error) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		return ExecutionResult{}, err
	}

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	cfgCur := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			AgingTime: 300 * time.Second,
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{
					10: "prod",
					20: "dev",
				},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
				},
			},
		},
	}

	specCur := vswitch.ConstructionSpec{
		Config: cfgCur,
		Seeds: []bridge.Seed{{
			FID:      10,
			MAC:      macH2,
			Port:     "1/1/2",
			Lifetime: bridge.Static,
		}},
	}

	swCur, err := vswitch.NewWithSpec(specCur)
	if err != nil {
		return ExecutionResult{}, err
	}

	cfgNext := cfgCur.Clone()
	cfgNext.Bridge.VLAN.Switchports["1/1/1"] = bridge.Switchport{
		PVID:     &vid20,
		Untagged: []vlan.ID{20},
	}

	changes := vswitch.Diff(cfgCur, cfgNext)

	swNext, _, err := vswitch.Derive(swCur, vswitch.ConstructionSpec{Config: cfgNext}, time.Unix(1700000000, 0))
	if err != nil {
		return ExecutionResult{}, err
	}

	now := time.Unix(1700000000, 0)
	frame := ethernet.Frame{
		Dst:       macH2,
		Src:       macH1,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("planning-test"),
	}

	cmp := vswitch.Compare(swCur, swNext, now, "1/1/1", frame)
	if err := validateReceiveResult(cmp.Current.Result); err != nil {
		return ExecutionResult{}, err
	}
	if err := validateReceiveResult(cmp.Expected.Result); err != nil {
		return ExecutionResult{}, err
	}

	return ExecutionResult{
		Outcome:    cmp.Expected.Outcome,
		Reason:     cmp.Expected.Reason,
		Steps:      cmp.Expected.Steps,
		Changes:    changes,
		Metadata:   cmp.Expected.Metadata,
		Switch:     swNext,
		Comparison: &cmp,
	}, nil
}

var (
	candForkH1 = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x07, 0x01}
	candForkH2 = netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x07, 0x02}
)

// casePlanningCandidateForkDiverges returns the planning case evaluating that
// a candidate fork diverging mid-run leaves the original simulation's forwarding
// state and outcome untouched.
func casePlanningCandidateForkDiverges() Case {
	gigabit := gigabitAuto()

	frame := expectedFact("bridge.frame", `src="02:00:00:00:07:01";dst="02:00:00:00:07:02";ether_type=2048;tags=[];payload_len=9`)
	fdbHit := expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:07:02";present=true;port="1/1/2";static=true`)
	macFact := expectedFact("fabric.mac", "02:00:00:00:07:02")
	steps := []StepExpectation{
		expectedStep("relay", trace.OpClassify, "default-vlan", trace.Subject{Kind: "vlan", Key: "0"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="1/1/1";fid=0;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:07:01"}, nil,
			[]FactExpectation{expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:07:01";present=true;port="1/1/1";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:07:02"},
			[]FactExpectation{frame}, []FactExpectation{fdbHit}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "1/1/2"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="1/1/2";member="";fid=0;eligible=true;reason=""`)}),
		expectedStep("host", trace.OpFilter, "host.mac.own", trace.Subject{Kind: "host", Key: "h2"},
			[]FactExpectation{expectedFact("fabric.vlan_tags", "[]"), macFact}, nil),
	}

	return Case{
		ID:      "planning/candidate-fork-diverges",
		UseCase: UseCasePlanning,
		Question: "When a candidate simulation is forked mid-run and diverges, does its execution " +
			"leave the source simulation's forwarding state and outcome untouched?",
		FalseAnswer: "Running the candidate fork alters shared simulation state, corrupting the source simulation's answer",
		CurrentResult: "The candidate fork faults a link and runs independently, while the source fabric continues " +
			"to deliver the frame over its intact link with an identical trace",
		ExpectedMetadata: &MetadataExpectation{Status: analysis.Complete, Scope: analysis.WholeScope()},
		ExpectedOutcome:  trace.Forwarded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("unicast-hit"),
			trace.RuleID("host.mac.own"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "mac", Key: "02:00:00:00:07:02"},
			{Kind: "host", Key: "h2"},
		},
		ExpectedFacts: []FactExpectation{fdbHit, macFact},
		ExpectedSteps: steps,
		Execute: func() (ExecutionResult, error) {
			tbl, err := port.NewBuilder().Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			spec := assembly.ConstructionSpec{
				Switches: map[string]vswitch.ConstructionSpec{
					"sw1": {
						NodeID: "sw1",
						Config: vswitch.Config{
							Ports:  tbl,
							Bridge: &bridge.Config{},
							Phy:    &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": gigabit, "1/1/2": gigabit}},
						},
						Seeds: []bridge.Seed{{MAC: candForkH2, Port: "1/1/2", Lifetime: bridge.Static}},
					},
				},
				Hosts: map[string]hostmodel.Config{
					"h1": {Address: candForkH1, Ethernet: gigabit},
					"h2": {Address: candForkH2, Ethernet: gigabit},
				},
				Cables: []fabric.Cable{
					{Cable: cablemodel.Cable{Medium: cablemodel.TwistedPair}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
					{Cable: cablemodel.Cable{Medium: cablemodel.TwistedPair}, A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
				},
			}

			fab, err := assembly.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			fab.Run(fabric.RunOptions{Budget: 5, Window: 0})
			if err := validateReceiveReport(fab.Report()); err != nil {
				return ExecutionResult{}, err
			}

			// Fork a candidate simulation mid-run.
			cand := fab.Fork()

			// The candidate fork diverges: fault the link to h2 and run on the candidate.
			if err := cand.SetFault(fabric.Endpoint{Node: "h2"}, fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, cablemodel.Fault{Kind: cablemodel.FaultCut}); err != nil {
				return ExecutionResult{}, err
			}
			_, err = cand.Inject(fabric.Injection{
				At:     cand.Clock(),
				Origin: fabric.Endpoint{Node: "h1"},
				Frame: ethernet.Frame{
					Dst:       candForkH2,
					Src:       candForkH1,
					EtherType: ethernet.EtherTypeIPv4,
					Payload:   []byte("candidate"),
				},
			})
			if err != nil {
				return ExecutionResult{}, err
			}
			cand.Run(fabric.RunOptions{Budget: 20, Window: 0})
			if err := validateReceiveReport(cand.Report()); err != nil {
				return ExecutionResult{}, err
			}

			// Source fabric injects the planned frame and continues its run unaffected.
			fid, err := fab.Inject(fabric.Injection{
				At:     fab.Clock(),
				Origin: fabric.Endpoint{Node: "h1"},
				Frame: ethernet.Frame{
					Dst:       candForkH2,
					Src:       candForkH1,
					EtherType: ethernet.EtherTypeIPv4,
					Payload:   []byte("fork-safe"),
				},
			})
			if err != nil {
				return ExecutionResult{}, err
			}
			fab.Run(fabric.RunOptions{Budget: 10, Window: 0})
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
		},
	}
}

// caseShadowingPartialUnknownPort returns the baseline topology shadowing case evaluating a partial model with an unknown port.
func caseShadowingPartialUnknownPort() Case {
	var cat analysis.EvidenceCatalog
	var refOperUnknown, refAgingDefault trace.EvidenceRef
	operUnknownEvidence := analysis.Evidence{
		Kind:    netmodel.EvidenceKindState,
		Origin:  "telemetry-snapshot",
		Context: "conformance-shadowing; interface 1/1/2 oper_status unspecified or unknown",
	}
	agingDefaultEvidence := analysis.Evidence{
		Kind:    netmodel.EvidenceKindDefault,
		Origin:  "telemetry-snapshot",
		Context: "conformance-shadowing; default aging_time=300s",
	}
	cat, refOperUnknown = cat.Add(operUnknownEvidence)
	cat, refAgingDefault = cat.Add(agingDefaultEvidence)
	expectedIssue := IssueExpectation{
		Code:     netmodel.IssueMissingOperStatus,
		Status:   analysis.Incomplete,
		Scope:    analysis.PortScope("shadow-sw1", "1/1/2"),
		Evidence: []trace.EvidenceRef{refOperUnknown},
	}
	expectedAssumption := AssumptionExpectation{
		Scope:     analysis.NodeScope("shadow-sw1"),
		Statement: "default value applied for aging_time: 300s",
		Evidence:  []trace.EvidenceRef{refAgingDefault},
	}
	modelMetadata := MetadataExpectation{
		Status:      analysis.Incomplete,
		Scope:       analysis.NodeScope("shadow-sw1"),
		Issues:      []IssueExpectation{expectedIssue},
		Evidence:    cat.Entries(),
		Assumptions: []AssumptionExpectation{expectedAssumption},
	}.Canonical()
	forwardEvidence := analysis.Evidence{
		Kind:    "vswitch.runtime",
		Origin:  "forward",
		Context: `code="unknown-operational-status",status="incomplete",scope="node[\"shadow-sw1\"]/port[\"1/1/2\"]",fact["port.forwarding"]="name=\"1/1/2\";present=true;kind=\"Physical\";admin=\"Up\";oper=\"Unknown\";mtu=0;lag_parent=\"\";eligible=false;reason=\"\""`,
	}
	var refForwardUnknown trace.EvidenceRef
	cat, refForwardUnknown = cat.Add(forwardEvidence)
	forwardMetadata := modelMetadata.Canonical()
	forwardMetadata.Evidence = cat.Entries()
	forwardMetadata.Issues = append(forwardMetadata.Issues, IssueExpectation{
		Code:     "unknown-operational-status",
		Status:   analysis.Incomplete,
		Scope:    analysis.PortScope("shadow-sw1", "1/1/2"),
		Evidence: []trace.EvidenceRef{refForwardUnknown},
	})
	forwardMetadata = forwardMetadata.Canonical()
	unknownPortFact := NewFactExpectation(port.ForwardingFact(
		"1/1/2",
		port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown},
		false,
		port.ReasonPortDown,
	))
	expectedSteps := []StepExpectation{
		expectedStep(
			"relay",
			trace.OpDrop,
			"ingress-port-down",
			trace.Subject{Kind: "port", Key: "1/1/2"},
			nil,
			[]FactExpectation{unknownPortFact},
		),
	}

	return Case{
		ID:               "topology-shadowing/partial-model-unknown-port",
		UseCase:          UseCaseTopologyShadowing,
		Question:         "Does a device model with one unknown operational port remain constructible and localize its Incomplete issue to that port?",
		FalseAnswer:      "Failing model construction as an error, treating unknown operational status as active forwarding, or hiding uncertainty when that port could change flood egress",
		CurrentResult:    "netmodel.Load returns a constructible ConstructionSpec with an Incomplete issue scoped strictly to the unknown port; forwarding that could use the port remains Incomplete",
		ExpectedMetadata: &modelMetadata,
		ExpectedOutcome:  trace.Dropped,
		ExpectedReason:   port.ReasonPortDown,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("ingress-port-down"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "port", Key: "1/1/2"},
		},
		ExpectedFacts: []FactExpectation{
			unknownPortFact,
		},
		ExpectedSteps:           expectedSteps,
		ExpectedModelMetadata:   &modelMetadata,
		ExpectedForwardMetadata: &forwardMetadata,
		Execute:                 executeShadowingPartialUnknownPort,
	}
}

func executeShadowingPartialUnknownPort() (ExecutionResult, error) {
	now := time.Unix(1700000000, 0)
	src := netmodel.SourceContext{
		DeviceID: "shadow-sw1",
		Origin:   "telemetry-snapshot",
		Context:  "conformance-shadowing",
	}

	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	operUnknown := interfacev1.OperStatus_OPER_STATUS_UNKNOWN
	pvid10 := uint32(10)
	mtu := uint32(0)
	frameAdmAll := switchingv1.FrameAdmission_FRAME_ADMISSION_ALL
	ingressFiltFalse := false

	p1Name := "1/1/1"
	p1 := interfacev1.Interface_builder{
		Name:        &p1Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Mtu:         &mtu,
		Physical: interfacev1.PhysicalInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{
				Pvid:             &pvid10,
				UntaggedVlanIds:  []uint32{10},
				FrameAdmission:   &frameAdmAll,
				IngressFiltering: &ingressFiltFalse,
			}.Build(),
		}.Build(),
	}.Build()

	p2Name := "1/1/2"
	p2 := interfacev1.Interface_builder{
		Name:        &p2Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUnknown,
		Mtu:         &mtu,
		Physical: interfacev1.PhysicalInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{
				Pvid:             &pvid10,
				UntaggedVlanIds:  []uint32{10},
				FrameAdmission:   &frameAdmAll,
				IngressFiltering: &ingressFiltFalse,
			}.Build(),
		}.Build(),
	}.Build()

	vid10 := uint32(10)
	vname10 := "prod"
	instance := netmodel.DefaultNetworkInstance
	vlans := []*switchingv1.Vlan{
		switchingv1.Vlan_builder{Id: &vid10, Name: &vname10, NetworkInstance: &instance}.Build(),
	}

	loadRes, err := netmodel.Load(now, src, netmodel.Input{Interfaces: []*interfacev1.Interface{p1, p2}, VLANs: vlans})
	if err != nil {
		return ExecutionResult{}, err
	}

	sw, err := vswitch.NewWithSpec(loadRes.Spec)
	if err != nil {
		return ExecutionResult{}, err
	}

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}
	frame := ethernet.Frame{
		Dst:       macH2,
		Src:       macH1,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("shadow-test"),
	}

	fwdUnknown := sw.Receive(now, "1/1/2", frame)
	if err := validateReceiveResult(fwdUnknown.Result); err != nil {
		return ExecutionResult{}, err
	}

	return ExecutionResult{
		Outcome:     fwdUnknown.Outcome,
		Reason:      fwdUnknown.Reason,
		Steps:       fwdUnknown.Steps,
		Metadata:    loadRes.Metadata,
		Switch:      sw,
		ModelResult: &loadRes,
		Forward:     &fwdUnknown.Result,
	}, nil
}

// caseTroubleshootingUnicastForwarding returns the baseline troubleshooting case evaluating decisive forwarding rules and trace progression.
func caseTroubleshootingUnicastForwarding() Case {
	vid10 := vlan.ID(10)
	frame := expectedFact("bridge.frame", `src="00:11:22:33:44:01";dst="00:11:22:33:44:02";ether_type=2048;tags=[];payload_len=17`)
	taggedFrame := expectedFact("bridge.frame", `src="00:11:22:33:44:01";dst="00:11:22:33:44:02";ether_type=2048;tags=[{tpid=33024;pcp=0;dei=false;vid=10}];payload_len=17`)
	fdbHit := expectedFact("bridge.fdb_decision", `fid=10;mac="00:11:22:33:44:02";present=true;port="1/1/2";static=true`)
	expectedSteps := []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="1/1/1";fid=10;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "00:11:22:33:44:01"}, nil,
			[]FactExpectation{expectedFact("bridge.fdb_decision", `fid=10;mac="00:11:22:33:44:01";present=true;port="1/1/1";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "00:11:22:33:44:02"},
			[]FactExpectation{frame}, []FactExpectation{fdbHit}),
		expectedStep("vlan", trace.OpRewrite, "vlan-tag-form", trace.Subject{Kind: "port", Key: "1/1/2"},
			[]FactExpectation{frame},
			[]FactExpectation{
				taggedFrame,
				expectedFact("bridge.vlan_decision", `port="1/1/2";fid=10;pcp=0;dei=false;form="egress"`),
			}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "1/1/2"},
			[]FactExpectation{taggedFrame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="1/1/2";member="";fid=10;eligible=true;reason=""`)}),
	}

	return Case{
		ID:            "troubleshooting/unicast-fdb-forwarding",
		UseCase:       UseCaseTroubleshooting,
		Question:      "Does forwarding an untagged frame through an access port to a trunk port expose the decisive unicast FDB lookup rule, VLAN classification, tag rewrite, and Complete status?",
		FalseAnswer:   "Reporting frame delivery without exposing the decisive FDB lookup rule, discarding intermediate classification steps, or obscuring tag rewrites",
		CurrentResult: "Forwarding produces a deterministic trace sequence including vlan-classify, learn, unicast-hit, vlan-tag-form, and transmit with Complete status and outer 802.1Q tagging on egress",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Forwarded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("vlan-classify"),
			trace.RuleID("unicast-hit"),
			trace.RuleID("vlan-tag-form"),
			trace.RuleID("transmit"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "10"},
			{Kind: "mac", Key: "00:11:22:33:44:02"},
			{Kind: "port", Key: "1/1/2"},
		},
		ExpectedFacts: []FactExpectation{
			fdbHit,
		},
		ExpectedSteps: expectedSteps,
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			b := port.NewBuilder()
			b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
			b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
			ports, err := b.Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
			macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

			cfg := vswitch.Config{
				Ports: ports,
				Bridge: &bridge.Config{
					AgingTime: 300 * time.Second,
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{
							10: "prod",
						},
						Switchports: map[string]bridge.Switchport{
							"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
							"1/1/2": {Tagged: []vlan.ID{10}},
						},
					},
				},
			}

			spec := vswitch.ConstructionSpec{
				Config: cfg,
				Seeds: []bridge.Seed{{
					FID:      10,
					MAC:      macH2,
					Port:     "1/1/2",
					Lifetime: bridge.Static,
				}},
			}

			sw, err := vswitch.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			now := time.Unix(1700000000, 0)
			frame := ethernet.Frame{
				Dst:       macH2,
				Src:       macH1,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   []byte("troubleshoot-test"),
			}

			fwd := sw.Receive(now, "1/1/1", frame)
			if err := validateReceiveResult(fwd.Result); err != nil {
				return ExecutionResult{}, err
			}

			return ExecutionResult{
				Outcome:  fwd.Outcome,
				Reason:   fwd.Reason,
				Steps:    fwd.Steps,
				Metadata: fwd.Metadata,
				Switch:   sw,
				Forward:  &fwd.Result,
			}, nil
		},
	}
}
