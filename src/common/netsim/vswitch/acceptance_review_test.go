package vswitch_test

import (
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

func TestDeriveInvalidatesLAGSelectionWhenMemberStateChanges(t *testing.T) {
	frame := ethernet.Frame{Src: netaddr.MAC{2}, Dst: netaddr.MAC{4}}
	for _, test := range []struct {
		name  string
		admin port.LinkState
		oper  port.LinkState
	}{
		{name: "oper up to down", admin: port.Up, oper: port.Down},
		{name: "oper up to unknown", admin: port.Up, oper: port.Unknown},
		{name: "admin up to down", admin: port.Down, oper: port.Up},
		{name: "admin up to unknown", admin: port.Unknown, oper: port.Up},
	} {
		t.Run(test.name, func(t *testing.T) {
			currentConfig := lagDeriveConfig(t, port.Up, port.Up)
			current := mustSwitch(t, currentConfig)
			if member, ok := current.SelectMember("lag1", frame, 0); !ok || member != "member" {
				t.Fatalf("current selection = (%q, %t), want member", member, ok)
			}

			targetConfig := lagDeriveConfig(t, test.admin, test.oper)
			derived, err := vswitch.Derive(current, vswitch.ConstructionSpec{Config: targetConfig})
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			fresh := mustSwitch(t, targetConfig)

			derivedMember, derivedOK := derived.SelectMember("lag1", frame, 0)
			freshMember, freshOK := fresh.SelectMember("lag1", frame, 0)
			if derivedMember != freshMember || derivedOK != freshOK {
				t.Errorf("derived selection = (%q, %t), fresh = (%q, %t)", derivedMember, derivedOK, freshMember, freshOK)
			}

			derivedResult := derived.Forward(fixedTime, "in", frame)
			freshResult := fresh.Forward(fixedTime, "in", frame)
			if !reflect.DeepEqual(derivedResult.Result, freshResult.Result) {
				t.Errorf("derived forwarding = %+v, fresh = %+v", derivedResult.Result, freshResult.Result)
			}
			if derivedResult.Metadata.Status() != freshResult.Metadata.Status() {
				t.Errorf("derived status = %s, fresh = %s", derivedResult.Metadata.Status(), freshResult.Metadata.Status())
			}
		})
	}
}

func TestPortInsertionOrderDoesNotChangeConfigurationOrForwardingSemantics(t *testing.T) {
	newConfig := func(names ...string) vswitch.Config {
		builder := port.NewBuilder()
		for _, name := range names {
			builder.Add(port.Port{Name: name, AdminStatus: port.Up, OperStatus: port.Up})
		}
		return vswitch.Config{Ports: mustTable(t, builder)}
	}

	a := newConfig("ingress", "output-z", "output-a")
	b := newConfig("output-a", "ingress", "output-z")
	if !a.Equal(b) {
		t.Fatal("Config.Equal distinguished port insertion order")
	}
	if a.Canonical() != b.Canonical() {
		t.Errorf("canonical configurations differ by insertion order:\n a: %s\n b: %s", a.Canonical(), b.Canonical())
	}
	if changes := vswitch.Diff(a, b); len(changes) != 0 {
		t.Errorf("Diff reported insertion-only changes: %+v", changes)
	}

	frame := ethernet.Frame{Src: netaddr.MAC{2}, Dst: netaddr.MAC{4}}
	aResult := mustSwitch(t, a).Forward(fixedTime, "ingress", frame).Result
	bResult := mustSwitch(t, b).Forward(fixedTime, "ingress", frame).Result
	if !reflect.DeepEqual(aResult, bResult) {
		t.Errorf("forwarding differs by insertion order:\n a: %+v\n b: %+v", aResult, bResult)
	}
}

func TestFailedLAGMemberIngressReportsBothStatesAndDecisivePort(t *testing.T) {
	deviceMAC := netaddr.MAC{2, 0, 0, 0, 0, 1}
	frame := ethernet.Frame{Src: netaddr.MAC{2, 0, 0, 0, 0, 2}, Dst: deviceMAC}
	bpdu := stp.Encode(stp.BPDU{
		RootID:       stp.BridgeID{Priority: stp.DefaultBridgePriority, Address: deviceMAC},
		BridgeID:     stp.BridgeID{Priority: stp.DefaultBridgePriority, Address: deviceMAC},
		PortID:       0x8001,
		HelloTime:    stp.DefaultHelloTime,
		MaxAge:       stp.DefaultMaxAge,
		ForwardDelay: stp.DefaultForwardDelay,
	}, deviceMAC)

	for _, state := range []struct {
		name       string
		memberOper port.LinkState
		parentOper port.LinkState
		decisive   string
	}{
		{name: "member down", memberOper: port.Down, parentOper: port.Up, decisive: "member"},
		{name: "parent down", memberOper: port.Up, parentOper: port.Down, decisive: "lag1"},
	} {
		for _, pipeline := range []struct {
			name  string
			frame ethernet.Frame
			cfg   func(port.Table) vswitch.Config
		}{
			{
				name:  "bridge",
				frame: frame,
				cfg: func(ports port.Table) vswitch.Config {
					return vswitch.Config{MAC: deviceMAC, Ports: ports, Bridge: &bridge.Config{}}
				},
			},
			{
				name:  "routed",
				frame: frame,
				cfg: func(ports port.Table) vswitch.Config {
					return vswitch.Config{
						MAC: deviceMAC, Ports: ports,
						Routing: &routing.Config{VRFs: map[string]routing.VRF{
							routing.DefaultVRF: {Interfaces: map[string]routing.Interface{
								"lag1": {Port: "lag1", Prefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")}},
							}},
						}},
					}
				},
			},
			{
				name:  "bpdu",
				frame: bpdu,
				cfg: func(ports port.Table) vswitch.Config {
					return vswitch.Config{
						MAC: deviceMAC, Ports: ports, Bridge: &bridge.Config{},
						STP: &stp.Config{Address: deviceMAC, Ports: map[string]stp.Port{"lag1": {}}},
					}
				},
			},
		} {
			t.Run(pipeline.name+"/"+state.name, func(t *testing.T) {
				ports := mustTable(t, port.NewBuilder().
					Add(port.Port{Name: "member", LagParent: "lag1", AdminStatus: port.Up, OperStatus: state.memberOper}).
					Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: state.parentOper}))
				res := mustSwitch(t, pipeline.cfg(ports)).Forward(fixedTime, "member", pipeline.frame)
				if res.Outcome != trace.Dropped || res.Reason != port.ReasonPortDown {
					t.Fatalf("result = %s/%s, want Dropped/port-down", res.Outcome, res.Reason)
				}
				if len(res.Steps) == 0 || res.Steps[0].Subject != (trace.Subject{Kind: "port", Key: state.decisive}) {
					t.Fatalf("steps = %+v, want decisive port %q", res.Steps, state.decisive)
				}

				facts := append(slices.Clone(res.Steps[0].Inputs), res.Steps[0].Outputs...)
				for _, name := range []string{"member", "lag1"} {
					if !slices.ContainsFunc(facts, func(fact trace.Fact) bool {
						return fact.TypeID() == "port.forwarding" && strings.Contains(fact.Canonical(), `name="`+name+`"`)
					}) {
						t.Errorf("decision facts = %+v, want state for %q", facts, name)
					}
				}
			})
		}
	}
}

func lagDeriveConfig(t *testing.T, admin, oper port.LinkState) vswitch.Config {
	t.Helper()

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "member", Kind: port.Physical, LagParent: "lag1", AdminStatus: admin, OperStatus: oper}))

	return vswitch.Config{
		Ports: ports,
		LAG: &lag.Config{LAGs: map[string]lag.LAG{
			"lag1": {Members: map[string]lag.Member{"member": {}}},
		}},
	}
}

func TestMirrorCopiesRequireReadyOutputs(t *testing.T) {
	frame := ethernet.Frame{Src: netaddr.MAC{2}, Dst: netaddr.MAC{4}, Payload: []byte("mirror")}
	for _, output := range []string{"direct", "output VLAN", "output VLAN LAG"} {
		for _, test := range []struct {
			state      port.LinkState
			wantCopies int
			wantStatus analysis.Status
			wantOp     trace.Op
		}{
			{state: port.Down, wantStatus: analysis.Complete, wantOp: trace.OpDrop},
			{state: port.Unknown, wantStatus: analysis.Incomplete, wantOp: trace.OpDrop},
			{state: port.Up, wantCopies: 1, wantStatus: analysis.Complete, wantOp: trace.OpReplicate},
		} {
			t.Run(output+"/"+string(test.state), func(t *testing.T) {
				cfg, dependency := mirrorOutputConfig(t, output, test.state)
				sw := mustSwitch(t, cfg)

				res := sw.Forward(fixedTime, "in", frame)
				if got := len(sw.Copies()); got != test.wantCopies {
					t.Errorf("Copies() count = %d, want %d", got, test.wantCopies)
				}
				if got := res.Metadata.Status(); got != test.wantStatus {
					t.Errorf("status = %s, want %s; issues: %+v", got, test.wantStatus, res.Metadata.Issues())
				}
				step, ok := mirrorCopyStep(res.Steps)
				if !ok {
					t.Fatalf("steps = %+v, want mirror copy decision", res.Steps)
				}
				if step.Op != test.wantOp {
					t.Errorf("mirror step op = %s, want %s", step.Op, test.wantOp)
				}
				if !slices.ContainsFunc(append(slices.Clone(step.Inputs), step.Outputs...), func(fact trace.Fact) bool {
					return fact.TypeID() == "traffic.mirror_decision"
				}) {
					t.Errorf("mirror step = %+v, want typed mirror decision", step)
				}
				if test.state == port.Unknown {
					issues := res.Metadata.Issues()
					if !slices.ContainsFunc(issues, func(issue analysis.Issue) bool {
						return issue.Code == "unknown-operational-status" && issue.Scope.Compare(analysis.PortScope("", dependency)) == 0
					}) {
						t.Errorf("issues = %+v, want unknown status on %q", issues, dependency)
					}
				}
			})
		}
	}
}

func TestOutputVLANMirrorLAGSelectionUsesLogicalVLAN(t *testing.T) {
	const outputVLAN vlan.ID = 99

	frame := ethernet.Frame{Src: netaddr.MAC{2}, Dst: netaddr.MAC{4}, Payload: []byte("mirror")}
	for _, test := range []struct {
		name       string
		switchport bridge.Switchport
	}{
		{name: "untagged", switchport: bridge.Switchport{Untagged: []vlan.ID{outputVLAN}}},
		{name: "tunnel", switchport: bridge.Switchport{Tunnel: &bridge.Tunnel{VID: outputVLAN}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			inputVLAN := vlan.ID(10)
			ports := mustTable(t, port.NewBuilder().
				Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "member-a", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "member-b", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}))
			cfg := vswitch.Config{
				Ports: ports,
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{inputVLAN: "input", outputVLAN: "mirror"},
					Switchports: map[string]bridge.Switchport{
						"in":   {PVID: &inputVLAN, Untagged: []vlan.ID{inputVLAN}},
						"out":  {PVID: &inputVLAN, Untagged: []vlan.ID{inputVLAN}},
						"lag1": test.switchport,
					},
				}},
				LAG: &lag.Config{LAGs: map[string]lag.LAG{
					"lag1": {
						Mode:    lag.BalanceSLB,
						Members: map[string]lag.Member{"member-a": {}, "member-b": {}},
					},
				}},
				Traffic: &traffic.Config{Mirrors: []traffic.Mirror{{
					Name: "span", SelectAll: true, OutputVLAN: new(outputVLAN),
				}}},
			}
			sw := mustSwitch(t, cfg)

			wantMember, ok := sw.SelectMember("lag1", frame, outputVLAN)
			if !ok {
				t.Fatal("output VLAN has no selected LAG member")
			}
			inferredMember, ok := sw.SelectMember("lag1", frame, 0)
			if !ok || inferredMember == wantMember {
				t.Fatalf("test does not distinguish logical VLAN selection: VLAN 99 = %q, VLAN 0 = %q", wantMember, inferredMember)
			}

			res := sw.Forward(fixedTime, "in", frame)
			step, ok := mirrorCopyStep(res.Steps)
			if !ok {
				t.Fatalf("steps = %+v, want mirror copy decision", res.Steps)
			}
			var selection string
			for _, fact := range step.Inputs {
				if fact.TypeID() == "lag.selection" {
					selection = fact.Canonical()
				}
			}
			if !strings.Contains(selection, `;vid=99;`) || !strings.Contains(selection, `;member="`+wantMember+`";`) {
				t.Errorf("LAG selection fact = %q, want logical VID 99 and member %q", selection, wantMember)
			}
			copies := sw.Copies()
			if len(copies) != 1 || copies[0].Member != wantMember {
				t.Errorf("mirror copies = %+v, want selected member %q retained for transmission", copies, wantMember)
			}
		})
	}
}

func TestMirrorCopyDependenciesRetainLoadedConstructionIssues(t *testing.T) {
	frame := ethernet.Frame{Src: netaddr.MAC{2}, Dst: netaddr.MAC{4}}
	for _, output := range []string{"direct", "output VLAN"} {
		t.Run(output, func(t *testing.T) {
			cfg, dependency := mirrorOutputConfig(t, output, port.Up)
			catalog, ref := analysis.EvidenceCatalog{}.Add(analysis.Evidence{
				Kind:    "snapshot",
				Origin:  output,
				Context: "mirror output readiness was not modeled",
			})
			sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
				Config: cfg,
				NodeID: "sw1",
				Metadata: analysis.NewMetadata(
					analysis.NodeScope("sw1"),
					[]analysis.Issue{{
						Code:     "test.mirror-output",
						Status:   analysis.Unsupported,
						Scope:    analysis.PortScope("sw1", dependency),
						Message:  "mirror output cannot be modeled",
						Evidence: []trace.EvidenceRef{ref},
					}},
					catalog,
					nil,
				),
			})
			if err != nil {
				t.Fatalf("NewWithSpec: %v", err)
			}

			res := sw.Forward(fixedTime, "in", frame)
			if got := res.Metadata.Status(); got != analysis.Unsupported {
				t.Fatalf("status = %s, want Unsupported; issues: %+v", got, res.Metadata.Issues())
			}
			if !slices.ContainsFunc(res.Metadata.Issues(), func(issue analysis.Issue) bool {
				return issue.Code == "test.mirror-output"
			}) {
				t.Errorf("issues = %+v, want loaded mirror output issue", res.Metadata.Issues())
			}
			if _, ok := res.Metadata.Evidence().Lookup(ref); !ok {
				t.Errorf("evidence = %+v, want %q", res.Metadata.Evidence().Entries(), ref)
			}
		})
	}
}

func TestPeekCalculatesMirrorDependenciesWithoutReplacingPendingCopies(t *testing.T) {
	cfg, _ := mirrorOutputConfig(t, "direct", port.Up)
	sw := mustSwitch(t, cfg)
	pending := ethernet.Frame{Src: netaddr.MAC{2}, Dst: netaddr.MAC{4}, Payload: []byte("pending")}
	peeked := ethernet.Frame{Src: netaddr.MAC{6}, Dst: netaddr.MAC{8}, Payload: []byte("peeked")}

	sw.Forward(fixedTime, "in", pending)
	if err := sw.SetOperStatus("mirror", port.Unknown); err != nil {
		t.Fatalf("SetOperStatus: %v", err)
	}
	res := sw.Peek(fixedTime, "in", peeked)
	if got := res.Metadata.Status(); got != analysis.Incomplete {
		t.Errorf("Peek status = %s, want Incomplete; issues: %+v", got, res.Metadata.Issues())
	}
	step, ok := mirrorCopyStep(res.Steps)
	if !ok || step.Op != trace.OpDrop {
		t.Errorf("Peek steps = %+v, want mirror copy drop", res.Steps)
	}

	copies := sw.Copies()
	if len(copies) != 1 || !slices.Equal(copies[0].Frame.Payload, pending.Payload) {
		t.Errorf("pending copies = %+v, want original Forward copy", copies)
	}
}

func mirrorOutputConfig(t *testing.T, output string, state port.LinkState) (vswitch.Config, string) {
	t.Helper()

	vid10 := vlan.ID(10)
	vid99 := vlan.ID(99)
	builder := port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	bridgeConfig := &bridge.Config{VLAN: &bridge.VLAN{
		Table: map[vlan.ID]string{vid10: "input", vid99: "mirror"},
		Switchports: map[string]bridge.Switchport{
			"in":  {PVID: &vid10, Untagged: []vlan.ID{vid10}},
			"out": {PVID: &vid10, Untagged: []vlan.ID{vid10}},
		},
	}}
	mirror := traffic.Mirror{Name: "span", SelectAll: true}
	dependency := "mirror"

	switch output {
	case "direct":
		builder.Add(port.Port{Name: "mirror", Kind: port.Physical, AdminStatus: port.Up, OperStatus: state})
		mirror.OutputPort = "mirror"
	case "output VLAN":
		builder.Add(port.Port{Name: "mirror", Kind: port.Physical, AdminStatus: port.Up, OperStatus: state})
		bridgeConfig.VLAN.Switchports["mirror"] = bridge.Switchport{Untagged: []vlan.ID{vid99}}
		mirror.OutputVLAN = &vid99
	case "output VLAN LAG":
		builder.
			Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "member", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: state})
		bridgeConfig.VLAN.Switchports["lag1"] = bridge.Switchport{Untagged: []vlan.ID{vid99}}
		mirror.OutputVLAN = &vid99
		dependency = "member"
	default:
		t.Fatalf("unknown mirror output %q", output)
	}

	return vswitch.Config{
		Ports:   mustTable(t, builder),
		Bridge:  bridgeConfig,
		Traffic: &traffic.Config{Mirrors: []traffic.Mirror{mirror}},
	}, dependency
}

func mirrorCopyStep(steps []trace.Step) (trace.Step, bool) {
	for _, step := range steps {
		if step.RuleID == traffic.RuleMirrorCopy || step.RuleID == traffic.RuleMirrorCopyDrop {
			return step, true
		}
	}

	return trace.Step{}, false
}

func TestMirrorSelectorsRejectPhysicalLAGMembersAtSubmittedPaths(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.Lag}).
		Add(port.Port{Name: "a-member", Kind: port.Physical, LagParent: "lag1"}).
		Add(port.Port{Name: "z-logical", Kind: port.Physical}).
		Add(port.Port{Name: "output", Kind: port.Physical}))

	for _, test := range []struct {
		name      string
		configure func(*traffic.Mirror)
		wantField string
	}{
		{
			name: "source",
			configure: func(m *traffic.Mirror) {
				m.SelectSrcPorts = []string{"z-logical", "a-member"}
			},
			wantField: "mirrors.0.select_src_ports.1",
		},
		{
			name: "destination",
			configure: func(m *traffic.Mirror) {
				m.SelectDstPorts = []string{"z-logical", "a-member"}
			},
			wantField: "mirrors.0.select_dst_ports.1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mirror := traffic.Mirror{Name: "span", OutputPort: "output"}
			test.configure(&mirror)
			_, err := vswitch.New(vswitch.Config{
				Ports:   ports,
				Traffic: &traffic.Config{Mirrors: []traffic.Mirror{mirror}},
			})
			if err == nil {
				t.Fatal("New accepted physical LAG member selector")
			}
			if got := errs.Attributes(err)["field"]; got != test.wantField {
				t.Errorf("field = %v, want %q", got, test.wantField)
			}
		})
	}

	logical := traffic.Config{Mirrors: []traffic.Mirror{{
		Name:           "span",
		SelectSrcPorts: []string{"lag1"},
		SelectDstPorts: []string{"lag1"},
		OutputPort:     "output",
	}}}
	if err := logical.Validate(ports); err != nil {
		t.Fatalf("Validate rejected logical LAG selectors: %v", err)
	}
}

func TestConstructionSpecCanonicalizesSeedTimesAndComparesInstants(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	withMonotonic := time.Now()
	wall := time.Unix(withMonotonic.Unix(), int64(withMonotonic.Nanosecond())).UTC()
	otherLocation := wall.In(time.FixedZone("same-instant", 3*60*60))
	seed := bridge.Seed{MAC: netaddr.MAC{2}, Port: "in"}

	a := vswitch.ConstructionSpec{Config: vswitch.Config{Ports: ports, Bridge: &bridge.Config{}}, Seeds: []bridge.Seed{seed}}
	b := a.Clone()
	a.Seeds[0].LearnedAt = withMonotonic
	b.Seeds[0].LearnedAt = otherLocation
	if !a.Equal(b) {
		t.Fatal("Equal distinguished the same instant by location or monotonic reading")
	}

	normalizedA, err := a.Normalize()
	if err != nil {
		t.Fatalf("Normalize(a): %v", err)
	}
	normalizedB, err := b.Normalize()
	if err != nil {
		t.Fatalf("Normalize(b): %v", err)
	}
	if normalizedA.Seeds[0].LearnedAt != wall || normalizedB.Seeds[0].LearnedAt != wall {
		t.Errorf("normalized times = %v and %v, want canonical UTC %v", normalizedA.Seeds[0].LearnedAt, normalizedB.Seeds[0].LearnedAt, wall)
	}
}

func TestConstructionSpecIssueMessagesDoNotChangeIdentity(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	issue := analysis.Issue{
		Code:    "test.unobserved",
		Status:  analysis.Incomplete,
		Scope:   analysis.PortScope("sw1", "in"),
		Message: "first wording",
	}
	a := vswitch.ConstructionSpec{
		Config:   vswitch.Config{Ports: ports},
		NodeID:   "sw1",
		Metadata: analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{issue}, analysis.EvidenceCatalog{}, nil),
	}
	b := a.Clone()
	issue.Message = "revised wording"
	b.Metadata = analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{issue}, analysis.EvidenceCatalog{}, nil)

	if !a.Equal(b) {
		t.Fatal("ConstructionSpec.Equal distinguished human-only issue messages")
	}
	sw, err := vswitch.NewWithSpec(a)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}
	if got := sw.Spec().Metadata.Issues()[0].Message; got != "first wording" {
		t.Errorf("copied issue message = %q, want retained human wording", got)
	}
}

func TestConstructionSpecIssueMessageOrderingDoesNotChangeIdentity(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	catalog, firstRef := analysis.EvidenceCatalog{}.Add(analysis.Evidence{Kind: "snapshot", Origin: "first"})
	catalog, secondRef := catalog.Add(analysis.Evidence{Kind: "snapshot", Origin: "second"})
	issue := func(message string, ref trace.EvidenceRef) analysis.Issue {
		return analysis.Issue{
			Code: "test.unobserved", Status: analysis.Incomplete, Scope: analysis.PortScope("sw1", "in"),
			Message: message, Evidence: []trace.EvidenceRef{ref},
		}
	}
	spec := func(issues []analysis.Issue) vswitch.ConstructionSpec {
		return vswitch.ConstructionSpec{
			Config:   vswitch.Config{Ports: ports},
			NodeID:   "sw1",
			Metadata: analysis.NewMetadata(analysis.NodeScope("sw1"), issues, catalog, nil),
		}
	}

	a := spec([]analysis.Issue{issue("a", firstRef), issue("z", secondRef)})
	b := spec([]analysis.Issue{issue("z", firstRef), issue("a", secondRef)})
	if !a.Equal(b) {
		t.Fatal("human message sort order changed construction identity")
	}
}

func TestConstructionSpecValidatesMetadataScopesAgainstNode(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	config := vswitch.Config{Ports: ports}
	nodeScope := analysis.NodeScope("sw1")

	valid := []struct {
		name     string
		metadata analysis.Metadata
	}{
		{name: "zero value", metadata: analysis.Metadata{}},
		{name: "canonical zero", metadata: analysis.NewMetadata(analysis.WholeScope(), nil, analysis.EvidenceCatalog{}, nil)},
		{name: "node", metadata: analysis.NewMetadata(nodeScope, nil, analysis.EvidenceCatalog{}, nil)},
		{name: "child", metadata: analysis.NewMetadata(analysis.PortScope("sw1", "in"), nil, analysis.EvidenceCatalog{}, nil)},
		{name: "whole issue", metadata: analysis.NewMetadata(nodeScope, []analysis.Issue{{
			Code: "test.global", Status: analysis.Incomplete, Scope: analysis.WholeScope(),
		}}, analysis.EvidenceCatalog{}, nil)},
		{name: "whole assumption", metadata: analysis.NewMetadata(nodeScope, nil, analysis.EvidenceCatalog{}, []analysis.Assumption{{
			Scope: analysis.WholeScope(), Statement: "global premise",
		}})},
	}
	for _, test := range valid {
		t.Run("valid "+test.name, func(t *testing.T) {
			if _, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{Config: config, NodeID: "sw1", Metadata: test.metadata}); err != nil {
				t.Fatalf("NewWithSpec rejected compatible metadata: %v", err)
			}
		})
	}

	invalid := []struct {
		name      string
		metadata  analysis.Metadata
		wantField string
	}{
		{
			name: "whole evaluated scope with content",
			metadata: analysis.NewMetadata(analysis.WholeScope(), []analysis.Issue{{
				Code: "test.local", Status: analysis.Incomplete, Scope: nodeScope,
			}}, analysis.EvidenceCatalog{}, nil),
			wantField: "metadata.scope",
		},
		{
			name:      "foreign evaluated node",
			metadata:  analysis.NewMetadata(analysis.NodeScope("sw2"), nil, analysis.EvidenceCatalog{}, nil),
			wantField: "metadata.scope",
		},
		{
			name: "foreign issue node",
			metadata: analysis.NewMetadata(nodeScope, []analysis.Issue{{
				Code: "test.foreign", Status: analysis.Incomplete, Scope: analysis.PortScope("sw2", "in"),
			}}, analysis.EvidenceCatalog{}, nil),
			wantField: "metadata.issues.0.scope",
		},
		{
			name: "foreign assumption node",
			metadata: analysis.NewMetadata(nodeScope, nil, analysis.EvidenceCatalog{}, []analysis.Assumption{{
				Scope: analysis.PortScope("sw2", "in"), Statement: "foreign premise",
			}}),
			wantField: "metadata.assumptions.0.scope",
		},
	}
	for _, test := range invalid {
		t.Run("invalid "+test.name, func(t *testing.T) {
			_, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{Config: config, NodeID: "sw1", Metadata: test.metadata})
			if err == nil {
				t.Fatal("NewWithSpec accepted incompatible metadata")
			}
			if got := errs.Attributes(err)["field"]; got != test.wantField {
				t.Errorf("field = %v, want %q", got, test.wantField)
			}
		})
	}

	anonymousNode := analysis.NodeScope("")
	anonymousInvalid := []struct {
		name      string
		metadata  analysis.Metadata
		wantField string
	}{
		{
			name:      "foreign evaluated node",
			metadata:  analysis.NewMetadata(analysis.NodeScope("sw2"), nil, analysis.EvidenceCatalog{}, nil),
			wantField: "metadata.scope",
		},
		{
			name: "foreign issue node",
			metadata: analysis.NewMetadata(anonymousNode, []analysis.Issue{{
				Code: "test.foreign", Status: analysis.Incomplete, Scope: analysis.PortScope("sw2", "in"),
			}}, analysis.EvidenceCatalog{}, nil),
			wantField: "metadata.issues.0.scope",
		},
		{
			name: "foreign assumption node",
			metadata: analysis.NewMetadata(anonymousNode, nil, analysis.EvidenceCatalog{}, []analysis.Assumption{{
				Scope: analysis.PortScope("sw2", "in"), Statement: "foreign premise",
			}}),
			wantField: "metadata.assumptions.0.scope",
		},
	}
	for _, test := range anonymousInvalid {
		t.Run("anonymous invalid "+test.name, func(t *testing.T) {
			_, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{Config: config, Metadata: test.metadata})
			if err == nil {
				t.Fatal("NewWithSpec accepted incompatible anonymous metadata")
			}
			if got := errs.Attributes(err)["field"]; got != test.wantField {
				t.Errorf("field = %v, want %q", got, test.wantField)
			}
		})
	}

	if _, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		Config: config,
		Metadata: analysis.NewMetadata(anonymousNode, []analysis.Issue{{
			Code: "test.anonymous", Status: analysis.Incomplete, Scope: analysis.PortScope("", "in"),
		}}, analysis.EvidenceCatalog{}, nil),
	}); err != nil {
		t.Fatalf("NewWithSpec rejected anonymous-node metadata: %v", err)
	}
}

func TestConstructionSpecRejectsDanglingMetadataEvidence(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	const missing trace.EvidenceRef = "evidence:missing"

	for _, test := range []struct {
		name      string
		metadata  analysis.Metadata
		wantField string
	}{
		{
			name: "issue",
			metadata: analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{{
				Code: "test.missing", Status: analysis.Incomplete, Scope: analysis.PortScope("sw1", "in"),
				Evidence: []trace.EvidenceRef{missing},
			}}, analysis.EvidenceCatalog{}, nil),
			wantField: "metadata.issues.0.evidence.0",
		},
		{
			name: "assumption",
			metadata: analysis.NewMetadata(analysis.NodeScope("sw1"), nil, analysis.EvidenceCatalog{}, []analysis.Assumption{{
				Scope: analysis.PortScope("sw1", "in"), Statement: "missing support",
				Evidence: []trace.EvidenceRef{missing},
			}}),
			wantField: "metadata.assumptions.0.evidence.0",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
				Config: vswitch.Config{Ports: ports}, NodeID: "sw1", Metadata: test.metadata,
			})
			if err == nil {
				t.Fatal("NewWithSpec accepted a dangling evidence reference")
			}
			if got := errs.Attributes(err)["field"]; got != test.wantField {
				t.Errorf("field = %v, want %q", got, test.wantField)
			}
		})
	}
}

func TestConfigValidatesMirrorVLANsAgainstBridge(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	const referencedVLAN vlan.ID = 10

	for _, test := range []struct {
		name      string
		bridge    *bridge.Config
		mirror    traffic.Mirror
		wantField string
	}{
		{
			name:      "output VLAN without bridge",
			mirror:    traffic.Mirror{Name: "span", SelectAll: true, OutputVLAN: new(referencedVLAN)},
			wantField: "traffic.mirrors.0.output_vlan",
		},
		{
			name:      "output VLAN without VLAN awareness",
			bridge:    &bridge.Config{},
			mirror:    traffic.Mirror{Name: "span", SelectAll: true, OutputVLAN: new(referencedVLAN)},
			wantField: "traffic.mirrors.0.output_vlan",
		},
		{
			name: "output VLAN absent from table",
			bridge: &bridge.Config{VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{20: "other"},
			}},
			mirror:    traffic.Mirror{Name: "span", SelectAll: true, OutputVLAN: new(referencedVLAN)},
			wantField: "traffic.mirrors.0.output_vlan",
		},
		{
			name:      "selector VLAN without bridge",
			mirror:    traffic.Mirror{Name: "span", SelectAll: true, SelectVLANs: []vlan.ID{referencedVLAN}, OutputPort: "out"},
			wantField: "traffic.mirrors.0.select_vlans.0",
		},
		{
			name: "selector VLAN absent from table",
			bridge: &bridge.Config{VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{20: "other"},
			}},
			mirror:    traffic.Mirror{Name: "span", SelectAll: true, SelectVLANs: []vlan.ID{referencedVLAN}, OutputPort: "out"},
			wantField: "traffic.mirrors.0.select_vlans.0",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := (vswitch.Config{
				Ports: ports, Bridge: test.bridge, Traffic: &traffic.Config{Mirrors: []traffic.Mirror{test.mirror}},
			}).Validate()
			if err == nil {
				t.Fatal("Validate accepted a mirror VLAN without bridge VLAN support")
			}
			if got := errs.Attributes(err)["field"]; got != test.wantField {
				t.Errorf("field = %v, want %q", got, test.wantField)
			}
		})
	}

	configured := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{referencedVLAN: "mirror"},
		}},
		Traffic: &traffic.Config{Mirrors: []traffic.Mirror{{
			Name: "span", SelectAll: true, SelectVLANs: []vlan.ID{referencedVLAN}, OutputVLAN: new(referencedVLAN),
		}}},
	}
	if err := configured.Validate(); err != nil {
		t.Fatalf("Validate rejected mirror VLANs present in the bridge table: %v", err)
	}

	withSubmittedOrder := configured
	withSubmittedOrder.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{
		{Name: "z-valid", SelectAll: true, OutputVLAN: new(referencedVLAN)},
		{Name: "a-invalid", SelectAll: true, SelectVLANs: []vlan.ID{20}, OutputPort: "out"},
	}}
	_, err := (vswitch.ConstructionSpec{Config: withSubmittedOrder}).Normalize()
	if err == nil {
		t.Fatal("Normalize accepted a selector VLAN absent from the bridge table")
	}
	if got := errs.Attributes(err)["field"]; got != "traffic.mirrors.1.select_vlans.0" {
		t.Errorf("submitted field = %v, want traffic.mirrors.1.select_vlans.0", got)
	}
}
