package netsimtest

import (
	"time"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
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
		ID:              "planning/port-vlan-change",
		UseCase:         UseCasePlanning,
		Question:        "Does reconfiguring an access switchport from VLAN 10 to VLAN 20 alter forwarding behavior and emit typed configuration diff facts without string parsing?",
		FalseAnswer:     "Silently ignoring the switchport reconfiguration, masking behavioral divergence behind identical prose strings, or requiring string parsing to observe diffs",
		CurrentResult:   "vswitch.Diff produces typed bridge.PVIDFact and bridge.VLANsFact change facts; vswitch.Compare detects forwarding divergence with the expected switch dropping traffic due to no-egress member ports in VLAN 20",
		ExpectedStatus:  StatusPtr(analysis.Complete),
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

			swNext, err := vswitch.Derive(swCur, cfgNext)
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
	cat, refOperUnknown = cat.Add(analysis.Evidence{
		Kind:    netmodel.EvidenceKindState,
		Origin:  "telemetry-snapshot",
		Context: "conformance-shadowing; interface 1/1/2 oper_status unspecified or unknown",
	})
	_, refAgingDefault = cat.Add(analysis.Evidence{
		Kind:    netmodel.EvidenceKindDefault,
		Origin:  "telemetry-snapshot",
		Context: "conformance-shadowing; default aging_time=300s",
	})
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
		ID:              "topology-shadowing/partial-model-unknown-port",
		UseCase:         UseCaseTopologyShadowing,
		Question:        "Does a device model with one unknown operational port remain constructible, localize Incomplete readiness to that port, and preserve Complete readiness for known-up ports?",
		FalseAnswer:     "Failing model construction as an error, treating unknown operational status as active forwarding, or tainting unrelated known-up ports with incomplete status",
		CurrentResult:   "netmodel.Load returns a constructible ConstructionSpec with Incomplete status scoped strictly to the unknown port; the switch drops frames on the unknown port while forwarding on the known-up port with Complete readiness",
		ExpectedStatus:  StatusPtr(analysis.Incomplete),
		ExpectedOutcome: trace.Dropped,
		ExpectedReason:  port.ReasonPortDown,
		ExpectedRules: []trace.RuleID{
			trace.RuleID("ingress-port-down"),
		},
		ExpectedSubjects: []trace.Subject{
			{Kind: "port", Key: "1/1/2"},
		},
		ExpectedFacts: []FactExpectation{
			unknownPortFact,
		},
		ExpectedSteps: expectedSteps,
		ExpectedIssues: []analysis.IssueCode{
			netmodel.IssueMissingOperStatus,
		},
		ExpectedIssueScopes: []analysis.Scope{
			analysis.PortScope("shadow-sw1", "1/1/2"),
		},
		ExpectedEvidenceRefs: []trace.EvidenceRef{
			refOperUnknown,
			refAgingDefault,
		},
		ExpectedAssumptions: []string{
			"default value applied for aging_time: 300s",
		},
		ExpectedModelMetadata: &MetadataExpectation{
			Status: analysis.Incomplete,
			Scope:  analysis.NodeScope("shadow-sw1"),
		},
		ExpectedForwardMetadata: &MetadataExpectation{
			Status: analysis.Incomplete,
			Scope:  analysis.NodeScope("shadow-sw1"),
		},
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
			frameAdmAll := switchingv1.FrameAdmission_FRAME_ADMISSION_ALL
			ingressFiltFalse := false

			p1Name := "1/1/1"
			p1 := interfacev1.Interface_builder{
				Name:        &p1Name,
				AdminStatus: &adminUp,
				OperStatus:  &operUp,
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
		ID:              "troubleshooting/unicast-fdb-forwarding",
		UseCase:         UseCaseTroubleshooting,
		Question:        "Does forwarding an untagged frame through an access port to a trunk port expose the decisive unicast FDB lookup rule, VLAN classification, tag rewrite, and Complete status?",
		FalseAnswer:     "Reporting frame delivery without exposing the decisive FDB lookup rule, discarding intermediate classification steps, or obscuring tag rewrites",
		CurrentResult:   "Forwarding produces a deterministic trace sequence including vlan-classify, learn, unicast-hit, vlan-tag-form, and transmit with Complete status and outer 802.1Q tagging on egress",
		ExpectedStatus:  StatusPtr(analysis.Complete),
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

// DefaultRegistry returns a newly allocated registry containing the admitted baseline cases.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	RegisterBaselineCases(r)
	return r
}
