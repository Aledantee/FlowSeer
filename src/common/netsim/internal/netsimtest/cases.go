package netsimtest

import (
	"slices"
	"time"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

// CasePlanningPortVLANChange returns the baseline planning case evaluating a prospective port VLAN change.
func CasePlanningPortVLANChange() Case {
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
	vlans10 := NewFactExpectation(bridge.VLANsFact([]vlan.ID{vid10}))
	vlans20 := NewFactExpectation(bridge.VLANsFact([]vlan.ID{vid20}))
	expectedChanges := []ChangeExpectation{
		expectedChange("vlan", trace.Subject{Kind: "port", Key: "1/1/1"}, "pvid", &pvid10, &pvid20),
		expectedChange("vlan", trace.Subject{Kind: "port", Key: "1/1/1"}, "untagged_vlan_ids", &vlans10, &vlans20),
	}

	return Case{
		ID:            "planning/port-vlan-change",
		UseCase:       UseCasePlanning,
		Question:      "Does reconfiguring an access switchport from VLAN 10 to VLAN 20 alter forwarding behavior and emit typed configuration diff facts without string parsing?",
		FalseAnswer:   "Silently ignoring the switchport reconfiguration, masking behavioral divergence behind identical prose strings, or requiring string parsing to observe diffs",
		CurrentResult: "vswitch.Diff produces typed bridge.PVIDFact and bridge.VLANsFact change facts; vswitch.Compare detects forwarding divergence with the expected switch dropping traffic due to no-egress member ports in VLAN 20",
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
					FID:    10,
					MAC:    macH2,
					Port:   "1/1/2",
					Static: true,
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

			swNext, err := vswitch.Derive(swCur, vswitch.ConstructionSpec{Config: cfgNext})
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

			return ExecutionResult{
				Outcome:    cmp.Expected.Outcome,
				Reason:     cmp.Expected.Reason,
				Steps:      cmp.Expected.Steps,
				Changes:    changes,
				Metadata:   cmp.Expected.Metadata,
				Switch:     swNext,
				Comparison: &cmp,
			}, nil
		},
	}
}

// CaseShadowingPartialUnknownPort returns the baseline topology shadowing case evaluating a partial model with an unknown port.
func CaseShadowingPartialUnknownPort() Case {
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
		Execute: func() (ExecutionResult, error) {
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
			vlans := []*switchingv1.Vlan{
				switchingv1.Vlan_builder{Id: &vid10, Name: &vname10}.Build(),
			}

			loadRes, err := netmodel.Load(now, src, []*interfacev1.Interface{p1, p2}, vlans, nil, nil, nil, nil, nil, nil, nil, nil, nil)
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

			fwdUnknown := sw.Forward(now, "1/1/2", frame)

			return ExecutionResult{
				Outcome:     fwdUnknown.Outcome,
				Reason:      fwdUnknown.Reason,
				Steps:       fwdUnknown.Steps,
				Metadata:    loadRes.Metadata,
				Switch:      sw,
				ModelResult: &loadRes,
				Forward:     &fwdUnknown,
			}, nil
		},
	}
}

// CaseTroubleshootingUnicastForwarding returns the baseline troubleshooting case evaluating decisive forwarding rules and trace progression.
func CaseTroubleshootingUnicastForwarding() Case {
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
					FID:    10,
					MAC:    macH2,
					Port:   "1/1/2",
					Static: true,
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

			fwd := sw.Forward(now, "1/1/1", frame)

			return ExecutionResult{
				Outcome:  fwd.Outcome,
				Reason:   fwd.Reason,
				Steps:    fwd.Steps,
				Metadata: fwd.Metadata,
				Switch:   sw,
				Forward:  &fwd,
			}, nil
		},
	}
}

// gigabitAuto is a fully reported gigabit auto-negotiating end: known speeds, supported
// auto-negotiation, and an auto Setting.
func gigabitAuto() phy.Ethernet {
	return phy.Ethernet{
		SupportedSpeedsBPS:       []uint64{1_000_000_000},
		AutoNegotiationSupported: phy.CapabilitySupported,
		Setting:                  &phy.Setting{AutoNegotiation: true},
	}
}

// unresolvedTransceiverEvidence is the site fact backing the unspecified-medium cable in
// [unresolvedTransceiverSpec]: cited once by the fabric-level propagation-unknown issue it
// causes, so the fixture and its case expectations resolve to the same evidence reference.
var unresolvedTransceiverEvidence = analysis.Evidence{
	Kind:    "survey",
	Origin:  "rack-walk",
	Context: "h2 uplink transceiver not identified",
}

// unresolvedTransceiverSpec builds one switch, sw1, with three hosts on twisted pair except
// h2: h1 and h3 report full gigabit facts and cable a stated medium, so their links resolve
// definitely. h2's cable states no medium, but both ends report matching 1 Gb/s observations,
// so the observed rule still resolves the link; carrying an unspecified medium and a nonzero
// length, it adds propagation-unknown. A static forwarding entry for each of h2 and h3 makes a
// frame to either of them known unicast, so a hop consults only its ingress and egress ports.
func unresolvedTransceiverSpec() fabric.ConstructionSpec {
	gigabit := gigabitAuto()
	observed := gigabit
	observed.Observed = &phy.Observed{SpeedBPS: 1_000_000_000, Duplex: phy.Full}

	tbl, _ := port.NewBuilder().Range("1/1/%d", 1, 3, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).Build()

	cat, ref := analysis.EvidenceCatalog{}.Add(unresolvedTransceiverEvidence)
	spec := fabric.ConstructionSpec{
		Switches: map[string]vswitch.ConstructionSpec{
			"sw1": {
				NodeID: "sw1",
				Config: vswitch.Config{
					Ports:  tbl,
					Bridge: &bridge.Config{},
					Phy:    &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": gigabit, "1/1/2": observed, "1/1/3": gigabit}},
				},
				Seeds: []bridge.Seed{
					{MAC: transceiverH2, Port: "1/1/2", Static: true},
					{MAC: transceiverH3, Port: "1/1/3", Static: true},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: transceiverH1, Ethernet: gigabit},
			"h2": {Address: transceiverH2, Ethernet: observed},
			"h3": {Address: transceiverH3, Ethernet: gigabit},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, LengthMeters: 2, Evidence: []trace.EvidenceRef{ref}},
			{A: fabric.Endpoint{Node: "h3"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, Medium: fabric.TwistedPair},
		},
		Evidence: cat,
	}

	return spec
}

var (
	transceiverH1 = netaddr.MAC{0x02, 0, 0, 0, 1, 0x01}
	transceiverH2 = netaddr.MAC{0x02, 0, 0, 0, 1, 0x02}
	transceiverH3 = netaddr.MAC{0x02, 0, 0, 0, 1, 0x03}
)

// CaseTopologyShadowingUnresolvedTransceiver returns the case whose journey crosses a link
// whose medium is unresolved. The link still carries the frame, since both ends agree on the
// same observed speed, but its timing rests on an unidentified transceiver, so the journey is
// Incomplete with propagation-unknown scoped to that link. Registered alongside
// [CaseTopologyShadowingUnresolvedTransceiverKnownDelivery], whose journey over the sibling
// resolved host stays Complete: one [Case] asserts one journey, so the two beside-each-other
// deliveries the acceptance example describes are two cases sharing this fixture.
func CaseTopologyShadowingUnresolvedTransceiver() Case {
	linkScope := analysis.LinkScope("h2:-sw1:1/1/2")
	cat, ref := analysis.EvidenceCatalog{}.Add(unresolvedTransceiverEvidence)
	issue := IssueExpectation{
		Code:     fabric.IssuePropagationUnknown,
		Status:   analysis.Incomplete,
		Scope:    linkScope,
		Evidence: []trace.EvidenceRef{ref},
	}
	metadata := &MetadataExpectation{
		Status:   analysis.Incomplete,
		Scope:    analysis.WholeScope(),
		Issues:   []IssueExpectation{issue},
		Evidence: cat.Entries(),
	}

	frame := expectedFact("bridge.frame", `src="02:00:00:00:01:01";dst="02:00:00:00:01:02";ether_type=2048;tags=[];payload_len=0`)
	fdbHit := expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:01:02";present=true;port="1/1/2";static=true`)
	macFact := expectedFact("fabric.mac", "02:00:00:00:01:02")
	steps := []StepExpectation{
		expectedStep("relay", trace.OpClassify, "default-vlan", trace.Subject{Kind: "vlan", Key: "0"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="1/1/1";fid=0;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:01:01"}, nil,
			[]FactExpectation{expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:01:01";present=true;port="1/1/1";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:01:02"},
			[]FactExpectation{frame}, []FactExpectation{fdbHit}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "1/1/2"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="1/1/2";member="";fid=0;eligible=true;reason=""`)}),
		expectedStep("host", trace.OpFilter, "host.mac.own", trace.Subject{Kind: "host", Key: "h2"},
			[]FactExpectation{expectedFact("fabric.vlan_tags", "[]"), macFact}, nil),
	}

	return Case{
		ID:      "topology-shadowing/unresolved-transceiver",
		UseCase: UseCaseTopologyShadowing,
		Question: "When h2's uplink cable states no medium but both ends still agree on an " +
			"observed speed, does the delivery stay Incomplete for the unidentified transceiver " +
			"instead of reading as a fully resolved link?",
		FalseAnswer: "Treating the observed-rule delivery as fully resolved timing, hiding that " +
			"the frame crossed a link whose transceiver was never identified",
		CurrentResult:    "The frame reaches h2, but the journey is Incomplete with propagation-unknown scoped to link h2:-sw1:1/1/2",
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
		ExpectedFacts: []FactExpectation{fdbHit, macFact},
		ExpectedSteps: steps,
		Execute: func() (ExecutionResult, error) {
			fab, err := fabric.NewWithSpec(unresolvedTransceiverSpec())
			if err != nil {
				return ExecutionResult{}, err
			}

			fid, err := fab.Inject(fabric.Injection{
				At:     fab.Snapshot().Clock,
				Origin: fabric.Endpoint{Node: "h1"},
				Frame:  ethernet.Frame{Dst: transceiverH2, Src: transceiverH1, EtherType: ethernet.EtherTypeIPv4},
			})
			if err != nil {
				return ExecutionResult{}, err
			}
			fab.Run(10)

			journey := fab.Report()[fid-1]
			steps := journeySteps(journey)
			fabricMetadata := fab.Metadata()

			return ExecutionResult{
				Outcome:        journeyHopOutcome(journey),
				Reason:         journeyHopReason(journey),
				Steps:          steps,
				Metadata:       journey.Metadata,
				Switch:         fab.Switch("sw1"),
				Journey:        &journey,
				FabricMetadata: &fabricMetadata,
			}, nil
		},
	}
}

// CaseTopologyShadowingUnresolvedTransceiverKnownDelivery returns the companion case sharing
// [unresolvedTransceiverSpec]: h1's known-unicast delivery to h3 crosses only resolved,
// stated-medium links, so it stays Complete beside the Incomplete h2 delivery in
// [CaseTopologyShadowingUnresolvedTransceiver].
func CaseTopologyShadowingUnresolvedTransceiverKnownDelivery() Case {
	frame := expectedFact("bridge.frame", `src="02:00:00:00:01:01";dst="02:00:00:00:01:03";ether_type=2048;tags=[];payload_len=0`)
	fdbHit := expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:01:03";present=true;port="1/1/3";static=true`)
	macFact := expectedFact("fabric.mac", "02:00:00:00:01:03")
	steps := []StepExpectation{
		expectedStep("relay", trace.OpClassify, "default-vlan", trace.Subject{Kind: "vlan", Key: "0"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="1/1/1";fid=0;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:01:01"}, nil,
			[]FactExpectation{expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:01:01";present=true;port="1/1/1";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:01:03"},
			[]FactExpectation{frame}, []FactExpectation{fdbHit}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "1/1/3"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="1/1/3";member="";fid=0;eligible=true;reason=""`)}),
		expectedStep("host", trace.OpFilter, "host.mac.own", trace.Subject{Kind: "host", Key: "h3"},
			[]FactExpectation{expectedFact("fabric.vlan_tags", "[]"), macFact}, nil),
	}

	return Case{
		ID:      "topology-shadowing/unresolved-transceiver-known-delivery",
		UseCase: UseCaseTopologyShadowing,
		Question: "Beside h2's Incomplete delivery over an unresolved transceiver, does a known-unicast " +
			"delivery between two fully resolved hosts on the same switch stay Complete?",
		FalseAnswer:   "Letting the sibling link's unresolved transceiver leak Incomplete status into an unrelated, fully resolved delivery",
		CurrentResult: "The frame reaches h3 over the fully resolved h3 link; the journey stays Complete",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.WholeScope(),
		},
		ExpectedOutcome: trace.Forwarded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("unicast-hit"),
			trace.RuleID("host.mac.own"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "mac", Key: "02:00:00:00:01:03"},
			{Kind: "host", Key: "h3"},
		},
		ExpectedFacts: []FactExpectation{fdbHit, macFact},
		ExpectedSteps: steps,
		Execute: func() (ExecutionResult, error) {
			fab, err := fabric.NewWithSpec(unresolvedTransceiverSpec())
			if err != nil {
				return ExecutionResult{}, err
			}

			fid, err := fab.Inject(fabric.Injection{
				At:     fab.Snapshot().Clock,
				Origin: fabric.Endpoint{Node: "h1"},
				Frame:  ethernet.Frame{Dst: transceiverH3, Src: transceiverH1, EtherType: ethernet.EtherTypeIPv4},
			})
			if err != nil {
				return ExecutionResult{}, err
			}
			fab.Run(10)

			journey := fab.Report()[fid-1]
			steps := journeySteps(journey)
			fabricMetadata := fab.Metadata()

			return ExecutionResult{
				Outcome:        journeyHopOutcome(journey),
				Reason:         journeyHopReason(journey),
				Steps:          steps,
				Metadata:       journey.Metadata,
				Switch:         fab.Switch("sw1"),
				Journey:        &journey,
				FabricMetadata: &fabricMetadata,
			}, nil
		},
	}
}

// journeySteps flattens a journey's decisive trace: each hop's forwarding steps in order,
// followed by a host's acceptance decision step when one was recorded.
func journeySteps(j fabric.Journey) []trace.Step {
	var out []trace.Step
	for _, e := range j.Entries {
		if e.Result != nil {
			out = append(out, e.Result.Steps...)
		}
		if e.Step != nil {
			out = append(out, *e.Step)
		}
	}

	return out
}

// journeyHopResult returns the last recorded hop's forwarding result: the switch-layer outcome
// host-level acceptance or rejection follows. A journey without a hop, such as one that never
// left its origin, returns nil.
func journeyHopResult(j fabric.Journey) *vswitch.ForwardResult {
	for _, e := range slices.Backward(j.Entries) {
		if e.Result != nil {
			return e.Result
		}
	}

	return nil
}

// journeyHopOutcome returns the domain outcome of [journeyHopResult], or the zero outcome
// when the journey never reached a hop.
func journeyHopOutcome(j fabric.Journey) trace.Outcome {
	if r := journeyHopResult(j); r != nil {
		return r.Outcome
	}

	return ""
}

// journeyHopReason returns the reason paired with [journeyHopOutcome].
func journeyHopReason(j fabric.Journey) trace.Reason {
	if r := journeyHopResult(j); r != nil {
		return r.Reason
	}

	return ""
}

var (
	uncabledH1      = netaddr.MAC{0x02, 0, 0, 0, 1, 0x01}
	uncabledBehind3 = netaddr.MAC{0x02, 0, 0, 0, 1, 0x03}
	uncabledBehind4 = netaddr.MAC{0x02, 0, 0, 0, 1, 0x04}
)

// CaseTopologyShadowingUncabledPortDefiniteDrop returns the case for a switch port named as
// definitely absent rather than merely unreported: sw1 port 3 is named in Config.Uncabled with
// a static forwarding entry behind it, so a frame to that entry drops definitely, Complete,
// with port-down. Port 2 and port 4 are omitted from both a cable and Uncabled, so
// [Fabric.Metadata] carries adjacency-unresolved on them; the journey to port 3 never depends
// on that state, so it stays unaffected, which [ExecutionResult.FabricMetadata] exposes for
// direct inspection beside the admitted journey.
func CaseTopologyShadowingUncabledPortDefiniteDrop() Case {
	gigabit := gigabitAuto()
	uncabledEvidence := analysis.Evidence{Kind: "survey", Origin: "rack-walk", Context: "sw1 port 3 has no cable"}
	cat, uncabledRef := analysis.EvidenceCatalog{}.Add(uncabledEvidence)

	frame := expectedFact("bridge.frame", `src="02:00:00:00:01:01";dst="02:00:00:00:01:03";ether_type=2048;tags=[];payload_len=0`)
	fdbHit := expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:01:03";present=true;port="3";static=true`)
	portDownFact := expectedFact("port.forwarding", `name="3";present=true;kind="Physical";admin="Up";oper="Down";mtu=0;lag_parent="";eligible=false;reason="port-down"`)
	steps := []StepExpectation{
		expectedStep("relay", trace.OpClassify, "default-vlan", trace.Subject{Kind: "vlan", Key: "0"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="1";fid=0;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:01:01"}, nil,
			[]FactExpectation{expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:01:01";present=true;port="1";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:01:03"},
			[]FactExpectation{frame}, []FactExpectation{fdbHit}),
		expectedStep("relay", trace.OpDrop, "port-down", trace.Subject{Kind: "port", Key: "3"},
			[]FactExpectation{portDownFact},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="3";member="";fid=0;eligible=false;reason="port-down"`)}),
	}

	return Case{
		ID:      "topology-shadowing/uncabled-port-definite-drop",
		UseCase: UseCaseTopologyShadowing,
		Question: "Does a switch port that Config.Uncabled names as definitely absent drop a " +
			"frame Complete, instead of carrying the Incomplete uncertainty an omitted port would?",
		FalseAnswer:      "Marking the drop Incomplete as if the port's state were merely unreported, or letting the frame transmit as if the port were cabled",
		CurrentResult:    "sw1 port 3 is Down with reason no-cable; a known-unicast frame to its static forwarding entry drops Complete with port-down",
		ExpectedMetadata: &MetadataExpectation{Status: analysis.Complete, Scope: analysis.WholeScope()},
		ExpectedOutcome:  trace.Dropped,
		ExpectedReason:   port.ReasonPortDown,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("unicast-hit"),
			trace.RuleID("port-down"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "mac", Key: "02:00:00:00:01:03"},
			{Kind: "port", Key: "3"},
		},
		ExpectedFacts: []FactExpectation{fdbHit, portDownFact},
		ExpectedSteps: steps,
		Execute: func() (ExecutionResult, error) {
			tbl, err := port.NewBuilder().
				Add(port.Port{Name: "1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Range("%d", 2, 4, port.Port{Kind: port.Physical, AdminStatus: port.Up}).
				Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			spec := fabric.ConstructionSpec{
				Switches: map[string]vswitch.ConstructionSpec{
					"sw1": {
						NodeID: "sw1",
						Config: vswitch.Config{
							Ports:  tbl,
							Bridge: &bridge.Config{},
							Phy:    &phy.Config{Ethernet: map[string]phy.Ethernet{"1": gigabit}},
						},
						Seeds: []bridge.Seed{
							{MAC: uncabledBehind3, Port: "3", Static: true},
							{MAC: uncabledBehind4, Port: "4", Static: true},
						},
					},
				},
				Hosts:    map[string]fabric.Host{"h1": {Address: uncabledH1, Ethernet: gigabit}},
				Cables:   []fabric.Cable{{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1"}, Medium: fabric.TwistedPair}},
				Uncabled: []fabric.Uncabled{{Endpoint: fabric.Endpoint{Node: "sw1", Port: "3"}, Evidence: []trace.EvidenceRef{uncabledRef}}},
				Evidence: cat,
			}

			fab, err := fabric.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			fid, err := fab.Inject(fabric.Injection{
				At:     fab.Snapshot().Clock,
				Origin: fabric.Endpoint{Node: "h1"},
				Frame:  ethernet.Frame{Dst: uncabledBehind3, Src: uncabledH1, EtherType: ethernet.EtherTypeIPv4},
			})
			if err != nil {
				return ExecutionResult{}, err
			}
			fab.Run(10)

			journey := fab.Report()[fid-1]
			fabricMetadata := fab.Metadata()

			return ExecutionResult{
				Outcome:        journeyHopOutcome(journey),
				Reason:         journeyHopReason(journey),
				Steps:          journeySteps(journey),
				Metadata:       journey.Metadata,
				Switch:         fab.Switch("sw1"),
				Journey:        &journey,
				FabricMetadata: &fabricMetadata,
			}, nil
		},
	}
}

var (
	unreportedNegotiationH1 = netaddr.MAC{0x02, 0, 0, 0, 1, 0x01}
	unreportedNegotiationH2 = netaddr.MAC{0x02, 0, 0, 0, 1, 0x02}
)

// CaseTopologyShadowingUnreportedNegotiation returns the case for a host that states no
// Ethernet facts at all: h2's link is Unknown with capability-unknown instead of the false
// answer of an assumed 1 Gb/s full-duplex default. A static forwarding entry makes the frame to
// h2 known unicast; the hop still cannot transmit through the Unknown port and drops with
// port-down, carrying both the switch's own unknown-operational-status issue and the fabric
// link's capability-unknown issue.
func CaseTopologyShadowingUnreportedNegotiation() Case {
	gigabit := gigabitAuto()
	linkEvidence := analysis.Evidence{Kind: "survey", Origin: "rack-walk", Context: "h2 reports no negotiation facts"}
	specCat, linkRef := analysis.EvidenceCatalog{}.Add(linkEvidence)
	cat, _ := analysis.EvidenceCatalog{}.Add(linkEvidence)
	unknownOperEvidence := analysis.Evidence{
		Kind:   "vswitch.runtime",
		Origin: "forward",
		Context: "code=\"unknown-operational-status\",status=\"incomplete\",scope=\"node[\\\"sw1\\\"]/port[\\\"1/1/2\\\"]\"," +
			"fact[\"port.forwarding\"]=\"name=\\\"1/1/2\\\";present=true;kind=\\\"Physical\\\";admin=\\\"Up\\\";" +
			"oper=\\\"Unknown\\\";mtu=0;lag_parent=\\\"\\\";eligible=false;reason=\\\"\\\"\"",
	}
	cat, unknownOperRef := cat.Add(unknownOperEvidence)

	metadata := &MetadataExpectation{
		Status: analysis.Incomplete,
		Scope:  analysis.WholeScope(),
		Issues: []IssueExpectation{
			{
				Code:     "unknown-operational-status",
				Status:   analysis.Incomplete,
				Scope:    analysis.PortScope("sw1", "1/1/2"),
				Evidence: []trace.EvidenceRef{unknownOperRef},
			},
			{
				Code:     analysis.IssueCode(phy.ReasonCapabilityUnknown),
				Status:   analysis.Incomplete,
				Scope:    analysis.LinkScope("h2:-sw1:1/1/2"),
				Evidence: []trace.EvidenceRef{linkRef},
			},
		},
		Evidence: cat.Entries(),
	}

	frame := expectedFact("bridge.frame", `src="02:00:00:00:01:01";dst="02:00:00:00:01:02";ether_type=2048;tags=[];payload_len=0`)
	fdbHit := expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:01:02";present=true;port="1/1/2";static=true`)
	portUnknownFact := expectedFact("port.forwarding", `name="1/1/2";present=true;kind="Physical";admin="Up";oper="Unknown";mtu=0;lag_parent="";eligible=false;reason="port-down"`)
	steps := []StepExpectation{
		expectedStep("relay", trace.OpClassify, "default-vlan", trace.Subject{Kind: "vlan", Key: "0"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="1/1/1";fid=0;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:01:01"}, nil,
			[]FactExpectation{expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:01:01";present=true;port="1/1/1";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:01:02"},
			[]FactExpectation{frame}, []FactExpectation{fdbHit}),
		expectedStep("relay", trace.OpDrop, "port-down", trace.Subject{Kind: "port", Key: "1/1/2"},
			[]FactExpectation{portUnknownFact},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="1/1/2";member="";fid=0;eligible=false;reason="port-down"`)}),
	}

	return Case{
		ID:      "topology-shadowing/unreported-negotiation",
		UseCase: UseCaseTopologyShadowing,
		Question: "When h2 reports no Ethernet facts at all, does its link stay Unknown with " +
			"capability-unknown instead of negotiating the false answer of 1 Gb/s full duplex?",
		FalseAnswer:      "Assuming an idle host with no reported facts auto-negotiates 1 Gb/s full duplex and delivering onto the port as if it were Up",
		CurrentResult:    "The link to h2 stays Unknown with capability-unknown; a known-unicast frame to h2 drops with port-down, Incomplete",
		ExpectedMetadata: metadata,
		ExpectedOutcome:  trace.Dropped,
		ExpectedReason:   port.ReasonPortDown,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("unicast-hit"),
			trace.RuleID("port-down"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "mac", Key: "02:00:00:00:01:02"},
			{Kind: "port", Key: "1/1/2"},
		},
		ExpectedFacts: []FactExpectation{fdbHit, portUnknownFact},
		ExpectedSteps: steps,
		Execute: func() (ExecutionResult, error) {
			tbl, err := port.NewBuilder().Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			spec := fabric.ConstructionSpec{
				Switches: map[string]vswitch.ConstructionSpec{
					"sw1": {
						NodeID: "sw1",
						Config: vswitch.Config{
							Ports:  tbl,
							Bridge: &bridge.Config{},
							Phy:    &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": gigabit}},
						},
						Seeds: []bridge.Seed{{MAC: unreportedNegotiationH2, Port: "1/1/2", Static: true}},
					},
				},
				Hosts: map[string]fabric.Host{
					"h1": {Address: unreportedNegotiationH1, Ethernet: gigabit},
					"h2": {Address: unreportedNegotiationH2},
				},
				Cables: []fabric.Cable{
					{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Medium: fabric.TwistedPair},
					{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, Medium: fabric.TwistedPair, Evidence: []trace.EvidenceRef{linkRef}},
				},
				Evidence: specCat,
			}

			fab, err := fabric.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			fid, err := fab.Inject(fabric.Injection{
				At:     fab.Snapshot().Clock,
				Origin: fabric.Endpoint{Node: "h1"},
				Frame:  ethernet.Frame{Dst: unreportedNegotiationH2, Src: unreportedNegotiationH1, EtherType: ethernet.EtherTypeIPv4},
			})
			if err != nil {
				return ExecutionResult{}, err
			}
			fab.Run(10)

			journey := fab.Report()[fid-1]
			fabricMetadata := fab.Metadata()

			return ExecutionResult{
				Outcome:        journeyHopOutcome(journey),
				Reason:         journeyHopReason(journey),
				Steps:          journeySteps(journey),
				Metadata:       journey.Metadata,
				Switch:         fab.Switch("sw1"),
				Journey:        &journey,
				FabricMetadata: &fabricMetadata,
			}, nil
		},
	}
}

var (
	unknownUplinkH1 = netaddr.MAC{0x02, 0, 0, 0, 1, 0x01}
	unknownUplinkH2 = netaddr.MAC{0x02, 0, 0, 0, 1, 0x02}
)

// unknownUplinkRuntimeEvidence returns the auto-generated runtime evidence protocol-link-unknown
// cites for one switch port: the port's own forwarding fact, exactly as [vswitch.Switch.Forward]
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

// CaseTopologyShadowingUnknownUplinkSTP returns the case for an Unknown link that spanning tree
// depends on: sw2's port 1/1/2 reports no Ethernet facts, so the redundant sw1-sw2 uplink over
// it is Unknown. Spanning tree can no longer trust any port's role on either switch, so
// [vswitch.IssueProtocolLinkUnknown] marks every STP port; a known-unicast journey forwarded
// over the other uplink still consults two of those ports per switch and carries their issues,
// Incomplete, even though it never crosses the Unknown link itself.
func CaseTopologyShadowingUnknownUplinkSTP() Case {
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

			spec := fabric.ConstructionSpec{
				Start: t0,
				Switches: map[string]vswitch.ConstructionSpec{
					"sw1": {NodeID: "sw1", Config: sw1Config, Seeds: []bridge.Seed{{MAC: unknownUplinkH2, Port: "1/1/1", Static: true}}},
					"sw2": {NodeID: "sw2", Config: sw2Config, Seeds: []bridge.Seed{{MAC: unknownUplinkH2, Port: "1/1/3", Static: true}}},
				},
				Hosts: map[string]fabric.Host{
					"h1": {Address: unknownUplinkH1, Ethernet: gigabit},
					"h2": {Address: unknownUplinkH2, Ethernet: gigabit},
				},
				Cables: []fabric.Cable{
					{A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, Medium: fabric.TwistedPair},
					{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/2"}, Medium: fabric.TwistedPair},
					{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, Medium: fabric.TwistedPair},
					{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/3"}, Medium: fabric.TwistedPair},
				},
			}

			fab, err := fabric.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}
			fab.Run(200)

			fid, err := fab.Inject(fabric.Injection{
				At:     fab.Snapshot().Clock,
				Origin: fabric.Endpoint{Node: "h1"},
				Frame:  ethernet.Frame{Dst: unknownUplinkH2, Src: unknownUplinkH1, EtherType: ethernet.EtherTypeIPv4},
			})
			if err != nil {
				return ExecutionResult{}, err
			}
			fab.Run(50)

			journey := fab.Report()[fid-1]
			fabricMetadata := fab.Metadata()

			return ExecutionResult{
				Outcome:        journeyHopOutcome(journey),
				Reason:         journeyHopReason(journey),
				Steps:          journeySteps(journey),
				Metadata:       journey.Metadata,
				Switch:         fab.Switch("sw1"),
				Journey:        &journey,
				FabricMetadata: &fabricMetadata,
			}, nil
		},
	}
}

var (
	foreignUnicastH1      = netaddr.MAC{0x02, 0, 0, 0, 1, 0x01}
	foreignUnicastH2      = netaddr.MAC{0x02, 0, 0, 0, 1, 0x02}
	foreignUnicastForeign = netaddr.MAC{0x02, 0, 0, 0, 1, 0x09}
)

// CaseTroubleshootingHostRejectsForeignUnicast returns a case whose network path is fully
// resolved and Complete, so it isolates the host-layer decision from topology uncertainty: h2's
// switch forwards a known-unicast frame addressed to a foreign MAC onto h2's port, and h2
// refuses it because the destination is neither its own address nor broadcast.
// [CaseTopologyShadowingUnreportedNegotiation] and its siblings cover rejection through an
// uncertain network path; this one shows the same host check deciding a rejection, not a
// delivery, when the network path itself is not in question.
func CaseTroubleshootingHostRejectsForeignUnicast() Case {
	gigabit := gigabitAuto()

	frame := expectedFact("bridge.frame", `src="02:00:00:00:01:01";dst="02:00:00:00:01:09";ether_type=2048;tags=[];payload_len=5`)
	fdbHit := expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:01:09";present=true;port="1/1/2";static=true`)
	macFact := expectedFact("fabric.mac", "02:00:00:00:01:09")
	steps := []StepExpectation{
		expectedStep("relay", trace.OpClassify, "default-vlan", trace.Subject{Kind: "vlan", Key: "0"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="1/1/1";fid=0;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:01:01"}, nil,
			[]FactExpectation{expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:01:01";present=true;port="1/1/1";static=false`)}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:01:09"},
			[]FactExpectation{frame}, []FactExpectation{fdbHit}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "1/1/2"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.egress_decision", `port="1/1/2";member="";fid=0;eligible=true;reason=""`)}),
		expectedStep("host", trace.OpFilter, "host.mac.unicast_not_addressed", trace.Subject{Kind: "host", Key: "h2"},
			[]FactExpectation{expectedFact("fabric.vlan_tags", "[]"), macFact}, nil),
	}

	return Case{
		ID:      "troubleshooting/host-rejects-foreign-unicast",
		UseCase: UseCaseTroubleshooting,
		Question: "When a switch forwards a known-unicast frame onto h2's port for a MAC that " +
			"is not h2's own address, does h2 refuse it, and does the trace expose the deciding " +
			"host check rather than reporting a delivery?",
		FalseAnswer:      "Reporting the frame as delivered once the switch transmits it onto h2's port, treating successful transmission as equivalent to host acceptance",
		CurrentResult:    "The switch forwards the frame onto h2's port Complete; h2 refuses it under host.mac.unicast_not_addressed, and Deliveries stays empty",
		ExpectedMetadata: &MetadataExpectation{Status: analysis.Complete, Scope: analysis.WholeScope()},
		ExpectedOutcome:  trace.Forwarded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("unicast-hit"),
			trace.RuleID("host.mac.unicast_not_addressed"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "mac", Key: "02:00:00:00:01:09"},
			{Kind: "host", Key: "h2"},
		},
		ExpectedFacts: []FactExpectation{fdbHit, macFact},
		ExpectedSteps: steps,
		Execute: func() (ExecutionResult, error) {
			tbl, err := port.NewBuilder().Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			spec := fabric.ConstructionSpec{
				Switches: map[string]vswitch.ConstructionSpec{
					"sw1": {
						NodeID: "sw1",
						Config: vswitch.Config{
							Ports:  tbl,
							Bridge: &bridge.Config{},
							Phy:    &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": gigabit, "1/1/2": gigabit}},
						},
						Seeds: []bridge.Seed{{MAC: foreignUnicastForeign, Port: "1/1/2", Static: true}},
					},
				},
				Hosts: map[string]fabric.Host{
					"h1": {Address: foreignUnicastH1, Ethernet: gigabit},
					"h2": {Address: foreignUnicastH2, Ethernet: gigabit},
				},
				Cables: []fabric.Cable{
					{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Medium: fabric.TwistedPair},
					{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, Medium: fabric.TwistedPair},
				},
			}

			fab, err := fabric.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			fid, err := fab.Inject(fabric.Injection{
				At:     fab.Snapshot().Clock,
				Origin: fabric.Endpoint{Node: "h1"},
				Frame: ethernet.Frame{
					Dst: foreignUnicastForeign, Src: foreignUnicastH1, EtherType: ethernet.EtherTypeIPv4,
					Payload: []byte("hello"),
				},
			})
			if err != nil {
				return ExecutionResult{}, err
			}
			fab.Run(10)

			journey := fab.Report()[fid-1]
			fabricMetadata := fab.Metadata()

			return ExecutionResult{
				Outcome:        journeyHopOutcome(journey),
				Reason:         journeyHopReason(journey),
				Steps:          journeySteps(journey),
				Metadata:       journey.Metadata,
				Switch:         fab.Switch("sw1"),
				Journey:        &journey,
				FabricMetadata: &fabricMetadata,
			}, nil
		},
	}
}

func expectedFact(typeID, canonical string) FactExpectation {
	return FactExpectation{TypeID: typeID, Canonical: canonical}
}

func expectedStep(
	layer trace.Layer,
	op trace.Op,
	ruleID trace.RuleID,
	subject trace.Subject,
	inputs []FactExpectation,
	outputs []FactExpectation,
) StepExpectation {
	return StepExpectation{
		Layer:   layer,
		Op:      op,
		RuleID:  ruleID,
		Subject: subject,
		Inputs:  inputs,
		Outputs: outputs,
	}.Canonical()
}

func expectedChange(
	layer trace.Layer,
	subject trace.Subject,
	field string,
	from *FactExpectation,
	to *FactExpectation,
) ChangeExpectation {
	return ChangeExpectation{
		Layer:   layer,
		Subject: subject,
		Field:   field,
		From:    from,
		To:      to,
	}.Canonical()
}

// RegisterBaselineCases populates registry with the three initial baseline cases.
func RegisterBaselineCases(registry *Registry) {
	registry.MustRegister(CasePlanningPortVLANChange())
	registry.MustRegister(CaseShadowingPartialUnknownPort())
	registry.MustRegister(CaseTroubleshootingUnicastForwarding())
}

// RegisterPhysicalTopologyCases populates registry with the cases covering tri-state physical
// facts, topology-derived link and port state, protocol link-state input, and host acceptance.
func RegisterPhysicalTopologyCases(registry *Registry) {
	registry.MustRegister(CaseTopologyShadowingUnresolvedTransceiver())
	registry.MustRegister(CaseTopologyShadowingUnresolvedTransceiverKnownDelivery())
	registry.MustRegister(CaseTopologyShadowingUncabledPortDefiniteDrop())
	registry.MustRegister(CaseTopologyShadowingUnreportedNegotiation())
	registry.MustRegister(CaseTopologyShadowingUnknownUplinkSTP())
	registry.MustRegister(CaseTroubleshootingHostRejectsForeignUnicast())
}

// DefaultRegistry returns a newly allocated registry containing every admitted case.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	RegisterBaselineCases(r)
	RegisterPhysicalTopologyCases(r)
	return r
}
