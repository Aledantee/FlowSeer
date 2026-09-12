package vswitch_test

import (
	"reflect"
	"slices"
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
	sw.SetOperStatus("mirror", port.Unknown)
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
