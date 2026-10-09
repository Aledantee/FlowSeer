package vswitch_test

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestMirrorOutputSuppressesBPDUs(t *testing.T) {
	sw := newAutoSTPSwitch(t, allAutoSTPPorts())
	cfg := sw.Config()
	cfg.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{{Name: "span", SelectAll: true, OutputPort: "1/1/3"}}}
	sw = mustSwitch(t, cfg)
	outs := []device.Output{sw.Start(fixedTime), sw.Advance(fixedTime.Add(2 * time.Second)), sw.Advance(fixedTime.Add(4 * time.Second)), sw.Advance(fixedTime.Add(6 * time.Second))}
	for i, out := range outs {
		normal := 0
		for _, em := range out.Emissions {
			if em.Port == "1/1/3" {
				t.Errorf("call %d emits BPDU on mirror output", i)
			}
			if em.Port == "1/1/1" {
				normal++
			}
		}
		if normal != 1 {
			t.Errorf("call %d normal BPDUs = %d, want 1", i, normal)
		}
	}
}

func TestMirrorCopiesRequireReadyOutputs(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
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

				res := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "in", frame))
				if got := len(takeCopies(callOutputs, sw)); got != test.wantCopies {
					t.Errorf("output copies count = %d, want %d", got, test.wantCopies)
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
	callOutputs := make(map[*vswitch.Switch]device.Output)
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
				Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
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
				Traffic: &traffic.Config{Mirrors: []traffic.Mirror{{Name: "span", SelectAll: true, OutputVLAN: new(outputVLAN)}}},
			}
			sw := mustSwitch(t, cfg)
			primed := sw.Receive(fixedTime, "in", frame)
			if len(primed.Copies) != 1 || primed.Copies[0].Member != "member-a" {
				t.Fatalf("VLAN 99 copies = %+v, want member-a", primed.Copies)
			}
			zeroCfg := cfg.Clone()
			zeroCfg.Bridge = &bridge.Config{}
			zeroCfg.Traffic = nil
			zero, _, err := vswitch.Derive(sw, vswitch.ConstructionSpec{Config: zeroCfg})
			if err != nil {
				t.Fatal(err)
			}
			zeroOut := zero.Receive(fixedTime, "in", frame)
			zeroMembers := 0
			for _, eg := range zeroOut.Egress {
				if eg.Port == "lag1" {
					if eg.Member != "member-b" {
						t.Fatalf("VID zero egress = %+v, want member-b", eg)
					}
					zeroMembers++
				}
			}
			if zeroMembers != 1 {
				t.Fatalf("VID zero LAG egresses = %d, want 1", zeroMembers)
			}
			sw, _, err = vswitch.Derive(zero, vswitch.ConstructionSpec{Config: cfg})
			if err != nil {
				t.Fatal(err)
			}

			res := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "in", frame))
			wantMember := "member-a"
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
			copies := takeCopies(callOutputs, sw)
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

			res := sw.Receive(fixedTime, "in", frame)
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

func TestPeekCalculatesMirrorDependenciesAndKeepsReceivedCopies(t *testing.T) {
	cfg, _ := mirrorOutputConfig(t, "direct", port.Up)
	sw := mustSwitch(t, cfg)
	received := ethernet.Frame{Src: netaddr.MAC{2}, Dst: netaddr.MAC{4}, Payload: []byte("received")}
	peeked := ethernet.Frame{Src: netaddr.MAC{6}, Dst: netaddr.MAC{8}, Payload: []byte("peeked")}
	first := sw.Receive(fixedTime, "in", received)
	if len(first.Copies) != 1 || !slices.Equal(first.Copies[0].Frame.Payload, received.Payload) {
		t.Fatalf("Receive copies = %+v, want one copy of the received frame", first.Copies)
	}
	if _, err := sw.LinkChange(time.Time{}, "mirror", port.Unknown, device.PointToPointTrue, 0); err != nil {
		t.Fatalf("LinkChange: %v", err)
	}
	res := sw.Peek(fixedTime, "in", peeked)
	if got := res.Metadata.Status(); got != analysis.Incomplete {
		t.Errorf("Peek status = %s, want Incomplete; issues: %+v", got, res.Metadata.Issues())
	}
	step, ok := mirrorCopyStep(res.Steps)
	if !ok || step.Op != trace.OpDrop {
		t.Errorf("Peek steps = %+v, want mirror copy drop", res.Steps)
	}
	if len(res.Copies) != 0 {
		t.Errorf("Peek copies = %+v, want none for the refused mirror output", res.Copies)
	}
	if len(first.Copies) != 1 || !slices.Equal(first.Copies[0].Frame.Payload, received.Payload) {
		t.Errorf("Receive copies after Peek = %+v, want the copy of the received frame unchanged", first.Copies)
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
			Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
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
		Add(port.Port{Name: "lag1", Kind: port.LAG}).
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
	if err := logical.Validate(layer.Env{Ports: ports}); err != nil {
		t.Fatalf("Validate rejected logical LAG selectors: %v", err)
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

func TestMirrorCopiesLACPFromLogicalIngress(t *testing.T) {
	sw := mustSwitch(t, vswitch.Config{
		MAC: macRouter, Ports: mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
		Bridge: &bridge.Config{}, LAG: &lag.Config{LAGs: map[string]lag.LAG{"lag1": {LACP: lag.LACPConfig{Mode: lag.Active}}}},
		Traffic: &traffic.Config{Mirrors: []traffic.Mirror{{Name: "span", SelectSrcPorts: []string{"lag1"}, OutputPort: "1/1/3"}}},
	})
	sw.Start(fixedTime)
	f := lacp.Encode(lacp.PDU{Actor: lacp.Info{SystemID: macH1, State: lacp.StateActive | lacp.StateAggregation}}, macH1)
	for _, out := range []device.Output{sw.Peek(fixedTime, "1/1/1", f), sw.Receive(fixedTime, "1/1/1", f)} {
		if out.Outcome != trace.Consumed || len(out.Copies) != 1 || out.Copies[0].Port != "1/1/3" || !slices.Equal(out.Copies[0].Frame.Payload, f.Payload) {
			t.Fatalf("LACP mirror = %+v, want member frame copied by lag1 selector", out)
		}
	}
}

func TestReleasedFrameIsMirroredOnEgress(t *testing.T) {
	cfg, replyPort, replyTags, _, _, _ := routedEgressConfig(t, "routed port", "none")
	b := port.NewBuilder()
	for _, p := range cfg.Ports.Ports() {
		b.Add(p)
	}
	b.Add(port.Port{Name: "mirror", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	cfg.Ports = mustTable(t, b)
	cfg.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{{Name: "span", SelectDstPorts: []string{"1/1/2"}, OutputPort: "mirror"}}}
	sw := mustSwitch(t, cfg)
	frame := ethernet.Frame{
		Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4,
		Tags:    []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}},
		Payload: makeIPv4Packet(t, ipH1, ipH2, 64, []byte("held mirror")),
	}
	held := sw.Receive(fixedTime, "1/1/1", frame)
	if held.Outcome != trace.Held || held.Hold == 0 || len(held.Copies) != 0 {
		t.Fatalf("held = %+v, want held token and no copies", held)
	}
	reply := makeARPReply(t, ipH2, netip.MustParseAddr("10.0.20.1"), macH2, macRouter)
	reply.Tags = replyTags
	released := sw.Receive(fixedTime.Add(time.Millisecond), replyPort, reply)
	em := soleDataEmission(t, released.Emissions, macH2)
	if len(released.Copies) != 1 {
		t.Fatalf("released copies = %+v, want one", released.Copies)
	}
	cp := released.Copies[0]
	if cp.Mirror != "span" || cp.Port != "mirror" || cp.Hold != held.Hold || cp.Frame.Src != em.Frame.Src || cp.Frame.Dst != em.Frame.Dst || !slices.Equal(cp.Frame.Payload, em.Frame.Payload) {
		t.Fatalf("copy = %+v, want released frame with hold %d", cp, held.Hold)
	}
	cp.Frame.Payload[0] ^= 0xff
	if slices.Equal(cp.Frame.Payload, em.Frame.Payload) {
		t.Fatal("copy payload shares the release frame")
	}
}

func TestReleasedFrameRefusesMirrorOutput(t *testing.T) {
	cfg, _, replyTags, _, _, _ := routedEgressConfig(t, "VLAN interface", "none")
	cfg.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{{Name: "span", SelectAll: true, OutputPort: "1/1/2"}}}
	f := ethernet.Frame{Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4, Tags: []vlan.Tag{{VID: 10}}, Payload: makeIPv4Packet(t, ipH1, ipH2, 64, []byte("held"))}
	// Reception on the reserved port is refused. An unreserved VLAN member
	// supplies the neighbor reply without learning its MAC on the output.
	b := port.NewBuilder()
	for _, p := range cfg.Ports.Ports() {
		b.Add(p)
	}
	b.Add(port.Port{Name: "reply", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	var err error
	cfg.Ports, err = b.Build()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Bridge.VLAN.Switchports["reply"] = bridge.Switchport{Tagged: []vlan.ID{20}}
	sw := mustSwitch(t, cfg)
	held := sw.Receive(fixedTime, "1/1/1", f)
	if held.Outcome != trace.Held {
		t.Fatalf("before release = %+v, want held", held)
	}
	reply := makeARPReply(t, ipH2, netip.MustParseAddr("10.0.20.1"), macH2, macRouter)
	reply.Tags = replyTags
	// ARP resolves its payload's sender. A different Ethernet source leaves
	// that neighbor absent from the FDB, so release exercises VLAN flooding.
	reply.Src = macH1
	swOut := sw.Receive(fixedTime.Add(time.Millisecond), "reply", reply)
	if len(swOut.Drops) != 1 || swOut.Drops[0].Port != "1/1/2" || swOut.Drops[0].Reason != traffic.ReasonMirrorOutput || swOut.Drops[0].Hold != held.Hold {
		t.Fatalf("released drops = %+v, want mirror-output with held token", swOut.Drops)
	}
	for _, em := range swOut.Emissions {
		if em.Port == "1/1/2" {
			t.Fatalf("release flooded to mirror output: %+v", em)
		}
	}
}

func TestSwitchReservesMirrorOutputAndCollectsCopies(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	vid10 := vlan.ID(10)
	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
				"1/1/3": {PVID: &vid10, Untagged: []vlan.ID{10}},
				"1/1/4": {PVID: &vid10, Untagged: []vlan.ID{10}},
			},
		}},
		Traffic: &traffic.Config{Mirrors: []traffic.Mirror{{
			Name:           "m1",
			SelectSrcPorts: []string{"1/1/1"},
			OutputPort:     "1/1/4",
		}}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	sw := mustSwitch(t, cfg)
	frame := ethernet.Frame{
		Dst:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("mirrored flood"),
	}

	res := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/1", frame))
	if res.Outcome != trace.Flooded {
		t.Errorf("Outcome = %v, want %v", res.Outcome, trace.Flooded)
	}
	if len(res.Egress) != 3 {
		t.Fatalf("len(Egress) = %d, want 3", len(res.Egress))
	}
	for i, name := range []string{"1/1/2", "1/1/3"} {
		if got := res.Egress[i]; got.Port != name || got.Dropped != "" {
			t.Errorf("Egress[%d] = %+v, want normal egress on %s", i, got, name)
		}
	}
	if got := res.Egress[2]; got.Port != "1/1/4" || got.Dropped != traffic.ReasonMirrorOutput {
		t.Errorf("Egress[2] = %+v, want mirror-output drop on 1/1/4", got)
	}

	copies := takeCopies(callOutputs, sw)
	if len(copies) != 1 || copies[0].Mirror != "m1" || copies[0].Port != "1/1/4" {
		t.Fatalf("output copies = %+v, want m1 copy on 1/1/4", copies)
	}
	if !slices.Equal(copies[0].Frame.Payload, frame.Payload) {
		t.Errorf("copy payload = %q, want %q", copies[0].Frame.Payload, frame.Payload)
	}
	if copies := takeCopies(callOutputs, sw); len(copies) != 0 {
		t.Errorf("second output copies = %+v, want empty", copies)
	}
	mustSwitchLearn(t, sw, []bridge.Seed{{FID: 10, MAC: frame.Dst, Port: "1/1/4", Lifetime: bridge.Static}})
	reservedOnly := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/1", frame))
	if reservedOnly.Outcome != trace.Dropped || reservedOnly.Reason != traffic.ReasonMirrorOutput {
		t.Errorf("reserved-only trace = %+v, want mirror-output drop", reservedOnly.Trace)
	}
	if len(reservedOnly.Egress) != 1 || reservedOnly.Egress[0].Dropped != traffic.ReasonMirrorOutput {
		t.Errorf("reserved-only egress = %+v, want one mirror-output drop", reservedOnly.Egress)
	}

	dropped := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/4", frame))
	if dropped.Outcome != trace.Dropped || dropped.Reason != traffic.ReasonMirrorOutput {
		t.Errorf("mirror output ingress trace = %+v, want mirror-output drop", dropped.Trace)
	}
	wantStep := trace.Step{Layer: traffic.LayerName, Op: trace.OpDrop, RuleID: "traffic.mirror.output_drop", Subject: trace.Subject{Kind: "port", Key: "1/1/4"}}
	if len(dropped.Steps) != 1 {
		t.Errorf("mirror output ingress steps = %+v, want %+v", dropped.Steps, wantStep)
	} else {
		assertSteps(t, dropped.Steps, []trace.Step{wantStep})
	}
	if !traceHasFactType(dropped.Steps, "traffic.mirror_decision") {
		t.Errorf("mirror output trace has no mirror decision fact: %+v", dropped.Steps)
	}

	// Peek returns copies without carrying them into the next call.
	peekSwitch := mustSwitch(t, cfg)
	peekSwitch.Peek(fixedTime, "1/1/1", frame)
	if copies := peekSwitch.Advance(fixedTime).Copies; len(copies) != 0 {
		t.Errorf("output copies after Peek = %+v, want empty", copies)
	}
}

func TestSwitchPolicerAndQueueLookup(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))
	cfg := vswitch.Config{
		Ports: ports,
		Traffic: &traffic.Config{
			Policers: map[string]traffic.Policer{
				"1/1/1": {RateBPS: 1_000_000, BurstOctets: 10_000},
			},
			Queues: map[string]traffic.PortQueues{
				"1/1/2": {MaxRateBPS: map[vlan.PCP]uint64{5: 100_000_000}, BufferOctets: map[vlan.PCP]uint64{5: 4096}},
			},
		},
	}
	sw := mustSwitch(t, cfg)
	for i := range 9 {
		if sw.Receive(fixedTime, "1/1/1", ethernet.Frame{EtherType: ethernet.EtherTypeIPv4, Payload: make([]byte, 1038-38)}).Reason == traffic.ReasonPoliced {
			t.Fatalf("Receive call %d refused, want admitted", i+1)
		}
	}
	if (sw.Receive(fixedTime, "1/1/1", ethernet.Frame{EtherType: ethernet.EtherTypeIPv4, Payload: make([]byte, 1038-38)}).Reason != traffic.ReasonPoliced) {
		t.Error("tenth Receive call admitted, want refused")
	}
	if sw.Receive(fixedTime.Add(10*time.Millisecond), "1/1/1", ethernet.Frame{EtherType: ethernet.EtherTypeIPv4, Payload: make([]byte, 1038-38)}).Reason == traffic.ReasonPoliced {
		t.Error("Receive after 10 ms refused, want admitted")
	}
	if sw.Receive(fixedTime, "1/1/2", ethernet.Frame{EtherType: ethernet.EtherTypeIPv4, Payload: make([]byte, 1_000_000-38)}).Reason == traffic.ReasonPoliced {
		t.Error("unconfigured port was policed")
	}
	if rate, ok := sw.QueueMaxRate("1/1/2", 5); !ok || rate != 100_000_000 {
		t.Errorf("QueueMaxRate(1/1/2, 5) = (%d, %t), want (100000000, true)", rate, ok)
	}
	if rate, ok := sw.QueueMaxRate("1/1/2", 0); ok || rate != 0 {
		t.Errorf("QueueMaxRate(1/1/2, 0) = (%d, %t), want (0, false)", rate, ok)
	}
	if buffer, ok := sw.QueueBuffer("1/1/2", 5); !ok || buffer != 4096 {
		t.Errorf("QueueBuffer(1/1/2, 5) = (%d, %t), want (4096, true)", buffer, ok)
	}
	if buffer, ok := sw.QueueBuffer("1/1/2", 0); ok || buffer != 0 {
		t.Errorf("QueueBuffer(1/1/2, 0) = (%d, %t), want (0, false)", buffer, ok)
	}

	tests := []struct {
		name            string
		change          func(*vswitch.Config)
		wantMainAdmit   bool
		wantSecondAdmit bool
	}{
		{
			name: "mirror changes preserve the bucket",
			change: func(next *vswitch.Config) {
				next.Traffic.Mirrors = []traffic.Mirror{{Name: "m1", SelectAll: true, OutputPort: "1/1/2"}}
			},
		},
		{
			name: "queue changes preserve the bucket",
			change: func(next *vswitch.Config) {
				next.Traffic.Queues["1/1/2"] = traffic.PortQueues{MaxRateBPS: map[vlan.PCP]uint64{5: 200_000_000}}
			},
		},
		{
			name: "another port policer preserves the bucket",
			change: func(next *vswitch.Config) {
				next.Traffic.Policers["1/1/2"] = traffic.Policer{RateBPS: 1_000_000, BurstOctets: 2_000}
			},
			wantSecondAdmit: true,
		},
		{
			name: "changed policer starts full",
			change: func(next *vswitch.Config) {
				next.Traffic.Policers["1/1/1"] = traffic.Policer{RateBPS: 2_000_000, BurstOctets: 10_000}
			},
			wantMainAdmit: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cur := mustSwitch(t, cfg)
			for i := range 9 {
				if cur.Receive(fixedTime, "1/1/1", ethernet.Frame{EtherType: ethernet.EtherTypeIPv4, Payload: make([]byte, 1038-38)}).Reason == traffic.ReasonPoliced {
					t.Fatalf("derive setup Receive call %d refused, want admitted", i+1)
				}
			}

			next := cfg.Clone()
			tt.change(&next)
			derived, _, err := vswitch.Derive(cur, vswitch.ConstructionSpec{Config: next})
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			if got := (derived.Receive(fixedTime, "1/1/1", ethernet.Frame{EtherType: ethernet.EtherTypeIPv4, Payload: make([]byte, 1038-38)}).Reason != traffic.ReasonPoliced); got != tt.wantMainAdmit {
				t.Errorf("main policer admit = %t, want %t", got, tt.wantMainAdmit)
			}
			if tt.wantSecondAdmit && derived.Receive(fixedTime, "1/1/2", ethernet.Frame{EtherType: ethernet.EtherTypeIPv4, Payload: make([]byte, 1_000-38)}).Reason == traffic.ReasonPoliced {
				t.Error("new policer did not start full")
			}
		})
	}
}

func TestPeekLeavesPolicerBucketsUntouched(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))
	sw := mustSwitch(t, vswitch.Config{
		Ports: ports,
		Traffic: &traffic.Config{Policers: map[string]traffic.Policer{
			"1/1/1": {RateBPS: 8, BurstOctets: 168},
		}},
	})
	frame := ethernet.Frame{Dst: netaddr.MAC{2}, Src: netaddr.MAC{4}}

	if sw.Receive(fixedTime, "1/1/1", ethernet.Frame{EtherType: ethernet.EtherTypeIPv4, Payload: make([]byte, 46)}).Reason == traffic.ReasonPoliced {
		t.Fatal("initial token was refused")
	}
	sw.Peek(fixedTime, "1/1/1", frame)
	if sw.Receive(fixedTime, "1/1/1", ethernet.Frame{EtherType: ethernet.EtherTypeIPv4, Payload: make([]byte, 46)}).Reason == traffic.ReasonPoliced {
		t.Error("Peek spent the remaining policer token")
	}
}

func TestMirrorSelectionUsesLogicalLAGIngress(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	vid10 := vlan.ID(10)
	vid99 := vlan.ID(99)
	cfg := vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "users", 99: "mirror"},
			Switchports: map[string]bridge.Switchport{
				"lag1":  {PVID: &vid10, Untagged: []vlan.ID{10, 99}},
				"1/1/3": {PVID: &vid10, Untagged: []vlan.ID{10}},
				"1/1/4": {Untagged: []vlan.ID{99}},
			},
		}},
		LAG: &lag.Config{LAGs: map[string]lag.LAG{
			"lag1": {Mode: lag.BalanceSLB, Members: map[string]lag.Member{"1/1/1": {}, "1/1/2": {}}},
		}},
		Traffic: &traffic.Config{Mirrors: []traffic.Mirror{
			{Name: "span", SelectSrcPorts: []string{"lag1"}, OutputPort: "1/1/4"},
			{Name: "rspan", SelectSrcPorts: []string{"lag1"}, OutputVLAN: &vid99},
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	sw := mustSwitch(t, cfg)
	frame := ethernet.Frame{Dst: netaddr.MAC{2}, Src: netaddr.MAC{4}}
	captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/1", frame))
	copies := takeCopies(callOutputs, sw)
	if len(copies) != 2 {
		t.Fatalf("output copies = %+v, want SPAN and RSPAN copies", copies)
	}
	for _, copy := range copies {
		if copy.Port == "lag1" {
			t.Errorf("RSPAN copied back onto logical ingress: %+v", copy)
		}
		if copy.Port != "1/1/4" {
			t.Errorf("copy port = %q, want 1/1/4", copy.Port)
		}
	}
}
