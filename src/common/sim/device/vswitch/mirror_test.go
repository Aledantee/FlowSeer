package vswitch_test

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
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
	outs := []vswitch.Output{sw.Start(fixedTime), sw.Advance(fixedTime.Add(2 * time.Second)), sw.Advance(fixedTime.Add(4 * time.Second)), sw.Advance(fixedTime.Add(6 * time.Second))}
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

func TestMirrorCopiesLACPFromLogicalIngress(t *testing.T) {
	sw := mirrorLAGSwitch(t)
	cfg := sw.Config()
	cfg.Traffic.Mirrors[0].OutputPort = "1/1/3"
	cfg.Traffic.Mirrors[0].SelectAll = false
	cfg.Traffic.Mirrors[0].SelectSrcPorts = []string{"lag1"}
	sw = mustSwitch(t, cfg)
	sw.Start(fixedTime)
	f := lacp.Encode(lacp.PDU{Actor: lacp.Info{SystemID: macH1, State: lacp.StateActive | lacp.StateAggregation}}, macH1)
	for _, out := range []vswitch.Output{sw.Peek(fixedTime, "1/1/1", f), sw.Receive(fixedTime, "1/1/1", f)} {
		if out.Outcome != trace.Consumed || len(out.Copies) != 1 || out.Copies[0].Port != "1/1/3" || !slices.Equal(out.Copies[0].Frame.Payload, f.Payload) {
			t.Fatalf("LACP mirror = %+v, want member frame copied by lag1 selector", out)
		}
	}
}

func mirrorLAGSwitch(t *testing.T) *vswitch.Switch {
	t.Helper()
	return mustSwitch(t, vswitch.Config{
		MAC: macRouter, Ports: mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
		Bridge: &bridge.Config{}, LAG: &lag.Config{LAGs: map[string]lag.LAG{"lag1": {LACP: lag.LACPConfig{Mode: lag.Active}}}}, Traffic: &traffic.Config{Mirrors: []traffic.Mirror{{Name: "span", SelectAll: true, OutputPort: "1/1/3"}}},
	})
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
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
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
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
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
