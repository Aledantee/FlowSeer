package netsimtest

import (
	"fmt"
	"net/netip"
	"slices"
	"time"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
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

// CasePlanningLAGMemberFaultKeepsSurvivingFlows returns the case evaluating
// OVS-style bucket persistence: a member fault reassigns only the buckets
// that were assigned to the faulted member, and every other bucket keeps its
// member.
func CasePlanningLAGMemberFaultKeepsSurvivingFlows() Case {
	frame := expectedFact("bridge.frame", `src="02:00:00:00:00:52";dst="02:00:00:00:00:d2";ether_type=2048;tags=[];payload_len=5`)
	fdbLearned := expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:00:52";present=true;port="in";static=false`)
	fdbHit := expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:00:d2";present=true;port="lag1";static=true`)
	selection := expectedFact("lag.selection", `lag="lag1";present=true;mode="BalanceSLB";hash_basis=0;src="02:00:00:00:00:52";vid=0;enabled=["member-b"];member="member-b";selected=true;bucket=253;prior="member-b";cause="kept";rebalance_unmodeled=false;`)
	egressDecision := expectedFact("bridge.egress_decision", `port="lag1";member="member-b";fid=0;eligible=true;reason=""`)

	expectedSteps := []StepExpectation{
		expectedStep("relay", trace.OpClassify, "default-vlan", trace.Subject{Kind: "vlan", Key: "0"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="in";fid=0;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:00:52"},
			[]FactExpectation{fdbLearned}, []FactExpectation{fdbLearned}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:00:d2"},
			[]FactExpectation{frame}, []FactExpectation{fdbHit}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "lag1"},
			[]FactExpectation{frame, selection}, []FactExpectation{egressDecision}),
	}

	return Case{
		ID:            "planning/lag-member-fault-keeps-surviving-flows",
		UseCase:       UseCasePlanning,
		Question:      "When one member of a BalanceSLB bond fails, do buckets already assigned to a surviving member keep that member, or does the whole bond reassign?",
		FalseAnswer:   "All flows remap when one member fails",
		CurrentResult: "Two flows land on distinct members (bucket 28 on member-a, bucket 253 on member-b) before member-a is disabled; the bucket 253 flow's next selection keeps member-b with cause \"kept\", the same bucket it held before the fault",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Forwarded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("default-vlan"),
			trace.RuleID("learn"),
			trace.RuleID("unicast-hit"),
			trace.RuleID("transmit"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "0"},
			{Kind: "mac", Key: "02:00:00:00:00:52"},
			{Kind: "mac", Key: "02:00:00:00:00:d2"},
			{Kind: "port", Key: "lag1"},
		},
		ExpectedFacts: []FactExpectation{
			selection,
		},
		ExpectedSteps: expectedSteps,
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			ports, err := port.NewBuilder().
				Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "member-a", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "member-b", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
				Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			dst1 := netaddr.MAC{0x02, 0, 0, 0, 0, 0xd1}
			dst2 := netaddr.MAC{0x02, 0, 0, 0, 0, 0xd2}

			spec := vswitch.ConstructionSpec{
				Config: vswitch.Config{
					Ports:  ports,
					Bridge: &bridge.Config{},
					LAG: &lag.Config{LAGs: map[string]lag.LAG{
						"lag1": {
							Mode:    lag.BalanceSLB,
							Members: map[string]lag.Member{"member-a": {}, "member-b": {}},
						},
					}},
				},
				Seeds: []bridge.Seed{
					{MAC: dst1, Port: "lag1", Static: true},
					{MAC: dst2, Port: "lag1", Static: true},
				},
			}

			sw, err := vswitch.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			flow1 := ethernet.Frame{Dst: dst1, Src: netaddr.MAC{0x02, 0, 0, 0, 0, 0x51}, EtherType: ethernet.EtherTypeIPv4, Payload: []byte("flow1")}
			flow2 := ethernet.Frame{Dst: dst2, Src: netaddr.MAC{0x02, 0, 0, 0, 0, 0x52}, EtherType: ethernet.EtherTypeIPv4, Payload: []byte("flow2")}

			now := time.Unix(1700000000, 0)
			// Both flows land on distinct members: flow1 takes the front of
			// the enabled list (member-a, bucket 28), and flow2 takes the
			// new front after the rotation (member-b, bucket 253).
			sw.Forward(now, "in", flow1)
			sw.Forward(now, "in", flow2)

			// member-a faults. Only the bucket it held (flow1's) needs
			// reassignment; flow2's bucket was never on member-a.
			sw.LinkChange(now.Add(time.Second), "member-a", port.Down, vswitch.PointToPointTrue, 1_000_000_000)
			sw.Forward(now.Add(2*time.Second), "in", flow1)

			fwd := sw.Forward(now.Add(2*time.Second), "in", flow2)

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

// CaseTroubleshootingActiveBackupNoFailback returns the case evaluating that
// active-backup keeps carrying traffic on the member it last chose once a
// higher-named member recovers, rather than returning to it.
func CaseTroubleshootingActiveBackupNoFailback() Case {
	frame := expectedFact("bridge.frame", `src="02:00:00:00:00:01";dst="02:00:00:00:00:d1";ether_type=2048;tags=[];payload_len=1`)
	fdbLearned := expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:00:01";present=true;port="in";static=false`)
	fdbHit := expectedFact("bridge.fdb_decision", `fid=0;mac="02:00:00:00:00:d1";present=true;port="lag1";static=true`)
	selection := expectedFact("lag.selection", `lag="lag1";present=true;mode="";hash_basis=0;primary="";enabled=["b","a"];member="b";selected=true;bucket=0;prior="b";cause="last-active";rebalance_unmodeled=false;`)
	egressDecision := expectedFact("bridge.egress_decision", `port="lag1";member="b";fid=0;eligible=true;reason=""`)

	expectedSteps := []StepExpectation{
		expectedStep("relay", trace.OpClassify, "default-vlan", trace.Subject{Kind: "vlan", Key: "0"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="in";fid=0;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "02:00:00:00:00:01"},
			[]FactExpectation{fdbLearned}, []FactExpectation{fdbLearned}),
		expectedStep("relay", trace.OpLookup, "unicast-hit", trace.Subject{Kind: "mac", Key: "02:00:00:00:00:d1"},
			[]FactExpectation{frame}, []FactExpectation{fdbHit}),
		expectedStep("relay", trace.OpTransmit, "transmit", trace.Subject{Kind: "port", Key: "lag1"},
			[]FactExpectation{frame, selection}, []FactExpectation{egressDecision}),
	}

	return Case{
		ID:            "troubleshooting/active-backup-no-failback",
		UseCase:       UseCaseTroubleshooting,
		Question:      "After an active-backup member without a configured Primary fails and its traffic moves to the other member, does traffic return to the first member once it recovers?",
		FalseAnswer:   "Traffic returns to the lowest-named member once it recovers",
		CurrentResult: "With no Primary configured, member \"a\" carries traffic initially, moves to \"b\" when \"a\" goes down, and stays on \"b\" with cause \"last-active\" once \"a\" comes back up",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Forwarded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("default-vlan"),
			trace.RuleID("learn"),
			trace.RuleID("unicast-hit"),
			trace.RuleID("transmit"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "0"},
			{Kind: "mac", Key: "02:00:00:00:00:01"},
			{Kind: "mac", Key: "02:00:00:00:00:d1"},
			{Kind: "port", Key: "lag1"},
		},
		ExpectedFacts: []FactExpectation{
			selection,
		},
		ExpectedSteps: expectedSteps,
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			ports, err := port.NewBuilder().
				Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "a", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "b", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
				Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			dst := netaddr.MAC{0x02, 0, 0, 0, 0, 0xd1}
			spec := vswitch.ConstructionSpec{
				Config: vswitch.Config{
					Ports:  ports,
					Bridge: &bridge.Config{},
					LAG: &lag.Config{LAGs: map[string]lag.LAG{
						"lag1": {
							Mode:    lag.ActiveBackup,
							Members: map[string]lag.Member{"a": {}, "b": {}},
						},
					}},
				},
				Seeds: []bridge.Seed{{MAC: dst, Port: "lag1", Static: true}},
			}

			sw, err := vswitch.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			frame := ethernet.Frame{Dst: dst, Src: netaddr.MAC{0x02, 0, 0, 0, 0, 1}, EtherType: ethernet.EtherTypeIPv4, Payload: []byte("x")}

			now := time.Unix(1700000000, 0)
			sw.Forward(now, "in", frame)

			sw.LinkChange(now.Add(time.Second), "a", port.Down, vswitch.PointToPointTrue, 1_000_000_000)
			sw.Forward(now.Add(2*time.Second), "in", frame)

			sw.LinkChange(now.Add(3*time.Second), "a", port.Up, vswitch.PointToPointTrue, 1_000_000_000)
			fwd := sw.Forward(now.Add(4*time.Second), "in", frame)

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

// CaseTroubleshootingSSMRejectsUnjoinedSource returns the case evaluating
// that per-source multicast admission rejects a source no port has joined,
// rather than admitting every source to a group any port has joined.
func CaseTroubleshootingSSMRejectsUnjoinedSource() Case {
	frame := expectedFact("bridge.frame", `src="00:11:22:33:44:fe";dst="01:00:5e:01:01:01";ether_type=2048;tags=[];payload_len=24`)
	fdbLearned := expectedFact("bridge.fdb_decision", `fid=10;mac="00:11:22:33:44:fe";present=true;port="p9";static=false`)
	groupDestination := expectedFact("bridge.egress_decision", `port="";member="";fid=10;eligible=true;reason="group-destination"`)
	membership := expectedFact("vswitch.mcast_membership", `fid=10;group="232.1.1.1";source="10.0.0.2";registered=true;decided=true;ports=[]`)
	dropDecision := expectedFact("bridge.egress_decision", `port="";member="";fid=10;eligible=false;reason="unregistered"`)

	expectedSteps := []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="p9";fid=10;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "00:11:22:33:44:fe"},
			[]FactExpectation{fdbLearned}, []FactExpectation{fdbLearned}),
		expectedStep("relay", trace.OpLookup, "group-destination", trace.Subject{Kind: "mac", Key: "01:00:5e:01:01:01"},
			[]FactExpectation{frame}, []FactExpectation{groupDestination}),
		expectedStep("relay", trace.OpDrop, trace.RuleID(mcast.ReasonUnregistered), trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{membership}, []FactExpectation{dropDecision}),
	}

	return Case{
		ID:            "troubleshooting/ssm-rejects-unjoined-source",
		UseCase:       UseCaseTroubleshooting,
		Question:      "After a port joins a group with a source-specific (S,G) report, is a frame from a second, unjoined source to that same group admitted or rejected?",
		FalseAnswer:   "The group-only model admits S2",
		CurrentResult: "p1 joins group 232.1.1.1 with an IS_IN report naming source S1; a frame from S2 to the same group resolves zero admitted ports and drops with reason \"unregistered\"",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Dropped,
		ExpectedReason:  mcast.ReasonUnregistered,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("vlan-classify"),
			trace.RuleID("learn"),
			trace.RuleID("group-destination"),
			trace.RuleID(mcast.ReasonUnregistered),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "10"},
			{Kind: "mac", Key: "00:11:22:33:44:fe"},
			{Kind: "mac", Key: "01:00:5e:01:01:01"},
		},
		ExpectedFacts: []FactExpectation{
			membership,
		},
		ExpectedSteps: expectedSteps,
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			pvid := vlan.ID(10)
			ports, err := port.NewBuilder().
				Add(port.Port{Name: "p1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "p9", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			group := netip.MustParseAddr("232.1.1.1")
			groupMAC := netaddr.MAC{0x01, 0x00, 0x5e, 0x01, 0x01, 0x01}
			hostMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
			routerMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0xfe}
			s1 := netip.MustParseAddr("10.0.0.1")
			s2 := netip.MustParseAddr("10.0.0.2")

			spec := vswitch.ConstructionSpec{
				Config: vswitch.Config{
					Ports: ports,
					Bridge: &bridge.Config{VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "ten"},
						Switchports: map[string]bridge.Switchport{
							"p1": {PVID: &pvid, Untagged: []vlan.ID{10}},
							"p9": {PVID: &pvid, Untagged: []vlan.ID{10}},
						},
					}},
					Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {}}},
				},
			}

			sw, err := vswitch.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			now := time.Unix(1700000000, 0)

			joinPayload, err := igmp.Encode(igmp.Message{
				Type:    igmp.ReportV3,
				Records: []igmp.GroupRecord{{Type: igmp.ChangeToIncludeMode, Group: group, Sources: []netip.Addr{s1}}},
			})
			if err != nil {
				return ExecutionResult{}, err
			}
			joinHdr := ip.Header{Src: netip.MustParseAddr("10.0.0.9"), Dst: group, HopLimit: 1, Protocol: 2, V4: &ip.V4{}}
			joinPacket, err := joinHdr.Encode(joinPayload)
			if err != nil {
				return ExecutionResult{}, err
			}
			joinFrame := ethernet.Frame{Dst: groupMAC, Src: hostMAC, EtherType: ethernet.EtherTypeIPv4, Payload: joinPacket}
			sw.Forward(now, "p1", joinFrame)

			dataFrame := func(src netip.Addr) (ethernet.Frame, error) {
				hdr := ip.Header{Src: src, Dst: group, HopLimit: 32, Protocol: 17, V4: &ip.V4{}}
				pkt, err := hdr.Encode([]byte("data"))
				if err != nil {
					return ethernet.Frame{}, err
				}
				return ethernet.Frame{Dst: groupMAC, Src: routerMAC, EtherType: ethernet.EtherTypeIPv4, Payload: pkt}, nil
			}

			frameS1, err := dataFrame(s1)
			if err != nil {
				return ExecutionResult{}, err
			}
			sw.Forward(now.Add(time.Second), "p9", frameS1)

			frameS2, err := dataFrame(s2)
			if err != nil {
				return ExecutionResult{}, err
			}
			fwd := sw.Forward(now.Add(time.Second), "p9", frameS2)

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

// CaseTroubleshootingLeaveLastMemberQuery returns the case evaluating that a
// non-fast leave with an observed group-specific query stops forwarding
// after the last member query time, rather than after the full membership
// interval.
func CaseTroubleshootingLeaveLastMemberQuery() Case {
	frame := expectedFact("bridge.frame", `src="00:11:22:33:44:fe";dst="01:00:5e:06:06:06";ether_type=2048;tags=[];payload_len=24`)
	fdbLearned := expectedFact("bridge.fdb_decision", `fid=10;mac="00:11:22:33:44:fe";present=true;port="p9";static=false`)
	groupDestination := expectedFact("bridge.egress_decision", `port="";member="";fid=10;eligible=true;reason="group-destination"`)
	membership := expectedFact("vswitch.mcast_membership", `fid=10;group="239.6.6.6";source="10.9.9.9";registered=true;decided=true;ports=["p9"]`)
	dropDecision := expectedFact("bridge.egress_decision", `port="";member="";fid=10;eligible=false;reason="no-egress"`)

	expectedSteps := []StepExpectation{
		expectedStep("vlan", trace.OpClassify, "vlan-classify", trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{frame},
			[]FactExpectation{expectedFact("bridge.vlan_decision", `port="p9";fid=10;pcp=0;dei=false;form="untagged"`)}),
		expectedStep("relay", trace.OpLearn, "learn", trace.Subject{Kind: "mac", Key: "00:11:22:33:44:fe"},
			[]FactExpectation{fdbLearned}, []FactExpectation{fdbLearned}),
		expectedStep("relay", trace.OpLookup, "group-destination", trace.Subject{Kind: "mac", Key: "01:00:5e:06:06:06"},
			[]FactExpectation{frame}, []FactExpectation{groupDestination}),
		expectedStep("relay", trace.OpDrop, trace.RuleID(bridge.ReasonNoEgress), trace.Subject{Kind: "vlan", Key: "10"},
			[]FactExpectation{membership}, []FactExpectation{dropDecision}),
	}

	return Case{
		ID:            "troubleshooting/leave-last-member-query",
		UseCase:       UseCaseTroubleshooting,
		Question:      "After the sole member of a group leaves and the router's group-specific query is observed, does forwarding to that member stop at the last member query time or run for the full membership interval?",
		FalseAnswer:   "Forwarding continues for the full membership interval after a queried leave",
		CurrentResult: "p1 joins group 239.6.6.6, leaves, and an observed group-specific query from router port p9 arrives at the leave time; two last member query intervals later (2s, well short of the 260s membership interval) a frame to the group no longer resolves p1 and drops with reason \"no-egress\"",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Dropped,
		ExpectedReason:  bridge.ReasonNoEgress,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("vlan-classify"),
			trace.RuleID("learn"),
			trace.RuleID("group-destination"),
			trace.RuleID(bridge.ReasonNoEgress),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "vlan", Key: "10"},
			{Kind: "mac", Key: "00:11:22:33:44:fe"},
			{Kind: "mac", Key: "01:00:5e:06:06:06"},
		},
		ExpectedFacts: []FactExpectation{
			membership,
		},
		ExpectedSteps: expectedSteps,
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			pvid := vlan.ID(10)
			ports, err := port.NewBuilder().
				Add(port.Port{Name: "p1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "p9", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			group := netip.MustParseAddr("239.6.6.6")
			groupMAC := netaddr.MAC{0x01, 0x00, 0x5e, 0x06, 0x06, 0x06}
			allRoutersMAC := netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0x02}
			hostMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
			routerMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0xfe}

			spec := vswitch.ConstructionSpec{
				Config: vswitch.Config{
					Ports: ports,
					Bridge: &bridge.Config{VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{10: "ten"},
						Switchports: map[string]bridge.Switchport{
							"p1": {PVID: &pvid, Untagged: []vlan.ID{10}},
							"p9": {PVID: &pvid, Untagged: []vlan.ID{10}},
						},
					}},
					Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{10: {
						LastMemberQueryInterval: time.Second,
						LastMemberQueryCount:    2,
						RouterPorts:             []string{"p9"},
					}}},
				},
			}

			sw, err := vswitch.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			now := time.Unix(1700000000, 0)
			leaveAt := now.Add(10 * time.Second)

			joinPayload, err := igmp.Encode(igmp.Message{Type: igmp.ReportV2, Group: group})
			if err != nil {
				return ExecutionResult{}, err
			}
			joinHdr := ip.Header{Src: netip.MustParseAddr("10.0.0.1"), Dst: group, HopLimit: 1, Protocol: 2, V4: &ip.V4{}}
			joinPacket, err := joinHdr.Encode(joinPayload)
			if err != nil {
				return ExecutionResult{}, err
			}
			sw.Forward(now, "p1", ethernet.Frame{Dst: groupMAC, Src: hostMAC, EtherType: ethernet.EtherTypeIPv4, Payload: joinPacket})

			leavePayload, err := igmp.Encode(igmp.Message{Type: igmp.Leave, Group: group})
			if err != nil {
				return ExecutionResult{}, err
			}
			leaveHdr := ip.Header{Src: netip.MustParseAddr("10.0.0.1"), Dst: netip.MustParseAddr("224.0.0.2"), HopLimit: 1, Protocol: 2, V4: &ip.V4{}}
			leavePacket, err := leaveHdr.Encode(leavePayload)
			if err != nil {
				return ExecutionResult{}, err
			}
			sw.Forward(leaveAt, "p1", ethernet.Frame{Dst: allRoutersMAC, Src: hostMAC, EtherType: ethernet.EtherTypeIPv4, Payload: leavePacket})

			queryPayload, err := igmp.Encode(igmp.Message{Type: igmp.Query, Version: igmp.V2, Group: group})
			if err != nil {
				return ExecutionResult{}, err
			}
			queryHdr := ip.Header{Src: netip.MustParseAddr("10.0.0.254"), Dst: group, HopLimit: 1, Protocol: 2, V4: &ip.V4{}}
			queryPacket, err := queryHdr.Encode(queryPayload)
			if err != nil {
				return ExecutionResult{}, err
			}
			sw.Forward(leaveAt, "p9", ethernet.Frame{Dst: groupMAC, Src: routerMAC, EtherType: ethernet.EtherTypeIPv4, Payload: queryPacket})

			dataHdr := ip.Header{Src: netip.MustParseAddr("10.9.9.9"), Dst: group, HopLimit: 32, Protocol: 17, V4: &ip.V4{}}
			dataPacket, err := dataHdr.Encode([]byte("data"))
			if err != nil {
				return ExecutionResult{}, err
			}
			dataFrame := ethernet.Frame{Dst: groupMAC, Src: routerMAC, EtherType: ethernet.EtherTypeIPv4, Payload: dataPacket}

			sw.Forward(leaveAt.Add(time.Second), "p9", dataFrame)
			fwd := sw.Forward(leaveAt.Add(2*time.Second), "p9", dataFrame)

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

func routedECMPPorts() (port.Table, error) {
	return port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "out-a", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "out-b", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
}

// CasePlanningECMPCandidatesRecorded returns the case evaluating whether a lookup
// over two equal-cost static routes records the path the packet did not take.
func CasePlanningECMPCandidatesRecorded() Case {
	routerMAC := netaddr.MAC{0x02, 0, 0, 0, 0, 0x01}
	hostMAC := netaddr.MAC{0x02, 0, 0, 0, 0, 0x51}
	nextHopMACA := netaddr.MAC{0x02, 0, 0, 0, 0, 0xa1}
	nextHopMACB := netaddr.MAC{0x02, 0, 0, 0, 0, 0xb1}
	nextHopA := netip.MustParseAddr("10.0.20.7")
	nextHopB := netip.MustParseAddr("10.0.30.7")
	source := netip.MustParseAddr("10.0.10.7")
	destination := netip.MustParseAddr("10.0.99.5")

	packetIn := expectedFact("routing.packet_decision",
		`interface="in";ether_type=2048;src="10.0.10.7";dst="10.0.99.5";hop_limit=64;valid=true;reason=""`)
	lookup := expectedFact("routing.lookup_decision",
		`vrf="default";destination="10.0.99.5";matched=true;prefix="10.0.99.0/24";next_hop="10.0.20.7";`+
			`interface="out-a";kind="static";hash_src="10.0.10.7";hash_dst="10.0.99.5";hash_flow_label=0;`+
			`hash=2072557066;chosen=0;candidates=[10.0.20.7|out-a|10.0.20.7,10.0.30.7|out-b|10.0.30.7]`)
	neighbor := expectedFact("routing.neighbor_decision",
		`interface="out-a";address="10.0.20.7";state="reachable";mac="02:00:00:00:00:a1"`)
	packetOut := expectedFact("routing.packet_decision",
		`interface="out-a";ether_type=2048;src="10.0.10.7";dst="10.0.99.5";hop_limit=63;valid=true;reason=""`)

	return Case{
		ID:      "planning/ecmp-candidates-recorded",
		UseCase: UseCasePlanning,
		Question: "Two equal-cost static routes reach one prefix. Does the trace record both, " +
			"so a reader can tell which path the flow would move to if one failed?",
		FalseAnswer: "One route wins and the alternatives are invisible",
		CurrentResult: "The lookup fact names both next hops in canonical order and the index of " +
			"the one the flow hash chose, so the unused path is visible beside the used one",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Forwarded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("classify"),
			trace.RuleID("static"),
			trace.RuleID("decrement-ttl"),
			trace.RuleID("routing.transmit"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "interface", Key: "in"},
			{Kind: "prefix", Key: "10.0.99.0/24"},
			{Kind: "interface", Key: "out-a"},
			{Kind: "port", Key: "out-a"},
		},
		ExpectedFacts: []FactExpectation{lookup},
		ExpectedSteps: []StepExpectation{
			expectedStep(port.LayerRouting, trace.OpClassify, "classify", trace.Subject{Kind: "interface", Key: "in"},
				[]FactExpectation{packetIn},
				[]FactExpectation{expectedFact("routing.route.interface", "in")}),
			expectedStep(port.LayerRouting, trace.OpLookup, "static", trace.Subject{Kind: "prefix", Key: "10.0.99.0/24"},
				[]FactExpectation{packetIn}, []FactExpectation{lookup}),
			expectedStep(port.LayerRouting, trace.OpRewrite, "decrement-ttl", trace.Subject{Kind: "interface", Key: "out-a"},
				[]FactExpectation{neighbor, packetIn}, []FactExpectation{packetOut}),
			expectedStep(port.LayerRouting, trace.OpTransmit, "routing.transmit", trace.Subject{Kind: "port", Key: "out-a"},
				nil, []FactExpectation{expectedFact("routing.lookup_decision", `interface="out-a";port="out-a";member="";reason=""`)}),
		},
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			ports, err := routedECMPPorts()
			if err != nil {
				return ExecutionResult{}, err
			}

			spec := vswitch.ConstructionSpec{
				Config: vswitch.Config{
					Ports: ports,
					Routing: &routing.Config{VRFs: map[string]routing.VRF{
						routing.DefaultVRF: {
							Interfaces: map[string]routing.Interface{
								"in":    {Port: "in", MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
								"out-a": {Port: "out-a", MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
								"out-b": {Port: "out-b", MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")}},
							},
							Routes: []routing.Route{
								{Prefix: netip.MustParsePrefix("10.0.99.0/24"), NextHop: nextHopA, Preference: 1, Metric: 10},
								{Prefix: netip.MustParsePrefix("10.0.99.0/24"), NextHop: nextHopB, Preference: 1, Metric: 10},
							},
							Neighbors: []routing.Neighbor{
								{Interface: "out-a", Addr: nextHopA, MAC: nextHopMACA},
								{Interface: "out-b", Addr: nextHopB, MAC: nextHopMACB},
							},
						},
					}},
				},
			}

			sw, err := vswitch.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			hdr := ip.Header{Src: source, Dst: destination, HopLimit: 64, Protocol: 17, V4: &ip.V4{}}
			pkt, err := hdr.Encode([]byte("flow"))
			if err != nil {
				return ExecutionResult{}, err
			}

			fwd := sw.Forward(time.Unix(1700000000, 0), "in", ethernet.Frame{
				Dst:       routerMAC,
				Src:       hostMAC,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   pkt,
			})

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

// CaseTroubleshootingNeighborResolutionPending returns the case evaluating what a
// routed forward does when its next hop has no configured or observed neighbor entry:
// whether it is indistinguishable from a definite drop, or a hold netsim can still release.
func CaseTroubleshootingNeighborResolutionPending() Case {
	routerMAC := netaddr.MAC{0x02, 0, 0, 0, 0, 0x01}
	hostMAC := netaddr.MAC{0x02, 0, 0, 0, 0, 0x51}
	learnedMAC := netaddr.MAC{0x02, 0, 0, 0, 0, 0xa1}
	source := netip.MustParseAddr("10.0.10.7")
	destination := netip.MustParseAddr("10.0.20.77")

	packetIn := expectedFact("routing.packet_decision",
		`interface="in";ether_type=2048;src="10.0.10.7";dst="10.0.20.77";hop_limit=64;valid=true;reason=""`)
	lookup := expectedFact("routing.lookup_decision",
		`vrf="default";destination="10.0.20.77";matched=true;prefix="10.0.20.0/24";next_hop="invalid IP";`+
			`interface="out";kind="connected";hash_src="10.0.10.7";hash_dst="10.0.20.77";hash_flow_label=0;`+
			`hash=336306069;chosen=0;candidates=[invalid IP|out|invalid IP]`)
	neighborPending := expectedFact("routing.neighbor_decision",
		`interface="out";address="10.0.20.77";state="incomplete";mac="00:00:00:00:00:00"`)

	neighborScope := routing.NeighborLookupScope("", "default", "out", destination)
	var cat analysis.EvidenceCatalog
	neighborUnresolvedEvidence := analysis.Evidence{
		Kind:   "vswitch.runtime",
		Origin: "forward",
		Context: `code="neighbor-unresolved",status="incomplete",` +
			`scope="node[\"\"]/protocol[\"routing\",\"default\"]/field[\"interfaces\",\"out\",\"neighbors\",\"10.0.20.77\"]"`,
	}
	var refNeighborUnresolved trace.EvidenceRef
	cat, refNeighborUnresolved = cat.Add(neighborUnresolvedEvidence)
	expectedIssue := IssueExpectation{
		Code:     vswitch.IssueNeighborUnresolved,
		Status:   analysis.Incomplete,
		Scope:    neighborScope,
		Evidence: []trace.EvidenceRef{refNeighborUnresolved},
	}
	resultMetadata := MetadataExpectation{
		Status:   analysis.Incomplete,
		Scope:    analysis.NodeScope(""),
		Issues:   []IssueExpectation{expectedIssue},
		Evidence: cat.Entries(),
	}.Canonical()

	expectedSteps := []StepExpectation{
		expectedStep(port.LayerRouting, trace.OpClassify, "classify", trace.Subject{Kind: "interface", Key: "in"},
			[]FactExpectation{packetIn},
			[]FactExpectation{expectedFact("routing.route.interface", "in")}),
		expectedStep(port.LayerRouting, trace.OpLookup, "connected", trace.Subject{Kind: "prefix", Key: "10.0.20.0/24"},
			[]FactExpectation{packetIn}, []FactExpectation{lookup}),
		expectedStep(port.LayerRouting, trace.OpLookup, trace.RuleID(routing.ReasonNeighborPending), trace.Subject{Kind: "ip", Key: "10.0.20.77"},
			[]FactExpectation{lookup}, []FactExpectation{neighborPending}),
	}

	return Case{
		ID:      "troubleshooting/neighbor-resolution-pending",
		UseCase: UseCaseTroubleshooting,
		Question: "A routed forward's next hop has no configured neighbor and none has been " +
			"observed yet. Does the frame drop, or does netsim hold it as an open question?",
		FalseAnswer: "An unresolved next hop is a definite drop, so a plan reads a missing " +
			"neighbor as a misconfiguration rather than as something netsim has not observed",
		CurrentResult: "The frame holds with outcome Held and reason neighbor-pending, and the " +
			"forward's metadata carries IssueNeighborUnresolved at Incomplete scoped to the " +
			"neighbor lookup, rather than a Complete drop",
		ExpectedMetadata: &resultMetadata,
		ExpectedOutcome:  trace.Held,
		ExpectedReason:   routing.ReasonNeighborPending,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("classify"),
			trace.RuleID("connected"),
			trace.RuleID(routing.ReasonNeighborPending),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "interface", Key: "in"},
			{Kind: "prefix", Key: "10.0.20.0/24"},
			{Kind: "ip", Key: "10.0.20.77"},
		},
		ExpectedFacts:           []FactExpectation{lookup, neighborPending},
		ExpectedSteps:           expectedSteps,
		ExpectedForwardMetadata: &resultMetadata,
		Execute: func() (ExecutionResult, error) {
			ports, err := port.NewBuilder().
				Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			spec := vswitch.ConstructionSpec{
				Config: vswitch.Config{
					Ports: ports,
					Routing: &routing.Config{VRFs: map[string]routing.VRF{
						routing.DefaultVRF: {
							Interfaces: map[string]routing.Interface{
								"in":  {Port: "in", MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
								"out": {Port: "out", MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
							},
							// No neighbors configured: the next hop is on-link but unresolved.
						},
					}},
				},
			}

			sw, err := vswitch.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			now := time.Unix(1700000000, 0)

			hdr := ip.Header{Src: source, Dst: destination, HopLimit: 64, Protocol: 17, V4: &ip.V4{}}
			pkt, err := hdr.Encode([]byte("hello"))
			if err != nil {
				return ExecutionResult{}, err
			}

			fwd := sw.Forward(now, "in", ethernet.Frame{
				Dst:       routerMAC,
				Src:       hostMAC,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   pkt,
			})

			// Observe an ARP reply for the pending destination, so the case's own
			// execution proves the hold above is releasable rather than a disguised,
			// permanent drop. The observing Forward already flushes the neighbor's hold
			// queue, so the Wake below is a no-op; it stays to make a future change that
			// moved the release onto Wake visible here rather than silent.
			replyMsg := arp.Message{
				HardwareType: 1,
				ProtocolType: 0x0800,
				Operation:    arp.Reply,
				SenderMAC:    learnedMAC,
				SenderAddr:   destination,
				TargetMAC:    routerMAC,
				TargetAddr:   netip.MustParseAddr("10.0.20.1"),
			}
			reply, err := arp.Encode(replyMsg, routerMAC)
			if err != nil {
				return ExecutionResult{}, err
			}
			sw.Forward(now.Add(time.Second), "out", reply)
			sw.Wake(now.Add(time.Second))

			emissions := sw.Drain()
			if len(emissions) != 1 {
				return ExecutionResult{}, fmt.Errorf("released emissions = %d, want 1", len(emissions))
			}
			if emissions[0].Frame.Dst != learnedMAC {
				return ExecutionResult{}, fmt.Errorf("released frame dst = %v, want %v", emissions[0].Frame.Dst, learnedMAC)
			}
			if emissions[0].Port != "out" || emissions[0].Protocol {
				return ExecutionResult{}, fmt.Errorf("released emission = {Port: %v, Protocol: %v}, want {Port: out, Protocol: false}", emissions[0].Port, emissions[0].Protocol)
			}
			if failures := sw.DrainNeighborFailures(); len(failures) != 0 {
				return ExecutionResult{}, fmt.Errorf("neighbor failures = %d, want 0: %+v", len(failures), failures)
			}

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

// CaseTroubleshootingRecursiveRouteNotInstalled returns the case evaluating what a
// static route whose next hop resolves to nothing does to the packets it would match.
func CaseTroubleshootingRecursiveRouteNotInstalled() Case {
	routerMAC := netaddr.MAC{0x02, 0, 0, 0, 0, 0x01}
	hostMAC := netaddr.MAC{0x02, 0, 0, 0, 0, 0x51}
	nextHopMAC := netaddr.MAC{0x02, 0, 0, 0, 0, 0xa1}
	nextHop := netip.MustParseAddr("10.0.20.7")
	unresolvable := netip.MustParseAddr("192.0.2.1")
	source := netip.MustParseAddr("10.0.10.7")
	destination := netip.MustParseAddr("10.0.99.5")

	packetIn := expectedFact("routing.packet_decision",
		`interface="in";ether_type=2048;src="10.0.10.7";dst="10.0.99.5";hop_limit=64;valid=true;reason=""`)
	lookup := expectedFact("routing.lookup_decision",
		`vrf="default";destination="10.0.99.5";matched=true;prefix="10.0.0.0/8";next_hop="10.0.20.7";`+
			`interface="out";kind="static";hash_src="10.0.10.7";hash_dst="10.0.99.5";hash_flow_label=0;`+
			`hash=2072557066;chosen=0;candidates=[10.0.20.7|out|10.0.20.7]`)
	neighbor := expectedFact("routing.neighbor_decision",
		`interface="out";address="10.0.20.7";state="reachable";mac="02:00:00:00:00:a1"`)
	packetOut := expectedFact("routing.packet_decision",
		`interface="out";ether_type=2048;src="10.0.10.7";dst="10.0.99.5";hop_limit=63;valid=true;reason=""`)

	return Case{
		ID:      "troubleshooting/recursive-route-not-installed",
		UseCase: UseCaseTroubleshooting,
		Question: "A more specific static route names a next hop that no other route reaches. " +
			"What carries a packet the route would have matched?",
		FalseAnswer: "A broken recursive route silently forwards, or fails construction, " +
			"instead of leaving the table",
		CurrentResult: "The device constructs, the unresolvable /24 is withdrawn rather than " +
			"installed, and the packet takes the less specific /8 with a Complete result",
		ExpectedMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		ExpectedOutcome: trace.Forwarded,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("classify"),
			trace.RuleID("static"),
			trace.RuleID("decrement-ttl"),
			trace.RuleID("routing.transmit"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "interface", Key: "in"},
			{Kind: "prefix", Key: "10.0.0.0/8"},
			{Kind: "interface", Key: "out"},
			{Kind: "port", Key: "out"},
		},
		ExpectedFacts: []FactExpectation{lookup},
		ExpectedSteps: []StepExpectation{
			expectedStep(port.LayerRouting, trace.OpClassify, "classify", trace.Subject{Kind: "interface", Key: "in"},
				[]FactExpectation{packetIn},
				[]FactExpectation{expectedFact("routing.route.interface", "in")}),
			expectedStep(port.LayerRouting, trace.OpLookup, "static", trace.Subject{Kind: "prefix", Key: "10.0.0.0/8"},
				[]FactExpectation{packetIn}, []FactExpectation{lookup}),
			expectedStep(port.LayerRouting, trace.OpRewrite, "decrement-ttl", trace.Subject{Kind: "interface", Key: "out"},
				[]FactExpectation{neighbor, packetIn}, []FactExpectation{packetOut}),
			expectedStep(port.LayerRouting, trace.OpTransmit, "routing.transmit", trace.Subject{Kind: "port", Key: "out"},
				nil, []FactExpectation{expectedFact("routing.lookup_decision", `interface="out";port="out";member="";reason=""`)}),
		},
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Complete,
			Scope:  analysis.NodeScope(""),
		},
		Execute: func() (ExecutionResult, error) {
			ports, err := port.NewBuilder().
				Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Build()
			if err != nil {
				return ExecutionResult{}, err
			}

			spec := vswitch.ConstructionSpec{
				Config: vswitch.Config{
					Ports: ports,
					Routing: &routing.Config{VRFs: map[string]routing.VRF{
						routing.DefaultVRF: {
							Interfaces: map[string]routing.Interface{
								"in":  {Port: "in", MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
								"out": {Port: "out", MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
							},
							Routes: []routing.Route{
								{Prefix: netip.MustParsePrefix("10.0.99.0/24"), NextHop: unresolvable},
								{Prefix: netip.MustParsePrefix("10.0.0.0/8"), NextHop: nextHop},
							},
							Neighbors: []routing.Neighbor{
								{Interface: "out", Addr: nextHop, MAC: nextHopMAC},
							},
						},
					}},
				},
			}

			sw, err := vswitch.NewWithSpec(spec)
			if err != nil {
				return ExecutionResult{}, err
			}

			hdr := ip.Header{Src: source, Dst: destination, HopLimit: 64, Protocol: 17, V4: &ip.V4{}}
			pkt, err := hdr.Encode([]byte("flow"))
			if err != nil {
				return ExecutionResult{}, err
			}

			fwd := sw.Forward(time.Unix(1700000000, 0), "in", ethernet.Frame{
				Dst:       routerMAC,
				Src:       hostMAC,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   pkt,
			})

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

// RegisterLAGMulticastCases populates registry with the cases covering LAG
// bucket persistence across a member fault, active-backup failback, and
// per-source multicast admission and leave timing.
func RegisterLAGMulticastCases(registry *Registry) {
	registry.MustRegister(CasePlanningLAGMemberFaultKeepsSurvivingFlows())
	registry.MustRegister(CaseTroubleshootingActiveBackupNoFailback())
	registry.MustRegister(CaseTroubleshootingSSMRejectsUnjoinedSource())
	registry.MustRegister(CaseTroubleshootingLeaveLastMemberQuery())
}

// RegisterRoutingCases populates registry with the cases covering the equal-cost
// candidate set a lookup records and the withdrawal of an unresolvable static route.
func RegisterRoutingCases(registry *Registry) {
	registry.MustRegister(CasePlanningECMPCandidatesRecorded())
	registry.MustRegister(CaseTroubleshootingNeighborResolutionPending())
	registry.MustRegister(CaseTroubleshootingRecursiveRouteNotInstalled())
}

// DefaultRegistry returns a newly allocated registry containing every admitted case.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	RegisterBaselineCases(r)
	RegisterPhysicalTopologyCases(r)
	RegisterLAGMulticastCases(r)
	RegisterSTPCases(r)
	RegisterLoopProtectCases(r)
	RegisterRoutingCases(r)
	RegisterMDNSCases(r)
	RegisterReflectorCases(r)
	return r
}
