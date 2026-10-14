package vswitch_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestHubNoCandidateHasSemanticTerminalDecision(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "only", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	sw := mustSwitch(t, vswitch.Config{Ports: ports})

	res := sw.Receive(fixedTime, "only", ethernet.Frame{Src: macH1, Dst: macH2})
	if res.Outcome != trace.Dropped || res.Reason != bridge.ReasonNoEgress {
		t.Fatalf("hub result = %s/%s, want %s/%s", res.Outcome, res.Reason, trace.Dropped, bridge.ReasonNoEgress)
	}
	if len(res.Steps) != 1 || res.Steps[0].RuleID != "port.hub.no_egress" {
		t.Fatalf("hub steps = %+v, want one no-egress decision", res.Steps)
	}
	if outputs := res.Steps[0].Outputs; len(outputs) != 1 || outputs[0].TypeID() != "port.hub_egress" {
		t.Errorf("hub decision outputs = %+v, want port.hub_egress fact", outputs)
	}
}

func TestHubForwardingUntouched(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{Ports: tbl}
	sw := mustSwitch(t, cfg)

	srcMAC := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee}
	unknownDst := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	f1 := ethernet.Frame{
		Dst:       unknownDst,
		Src:       srcMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("tagged frame"),
		Tags: []vlan.Tag{
			{
				TPID: uint16(ethernet.EtherTypeDot1Q),
				VID:  20,
			},
		},
	}

	res1 := sw.Receive(fixedTime, "1/1/1", f1)
	if res1.Outcome != trace.Flooded {
		t.Errorf("frame 1: got outcome %v, want %v", res1.Outcome, trace.Flooded)
	}
	if res1.Ingress != "1/1/1" {
		t.Errorf("frame 1: got ingress %q, want %q", res1.Ingress, "1/1/1")
	}
	if res1.FID != 0 {
		t.Errorf("frame 1: got FID %d, want 0", res1.FID)
	}
	if len(res1.Egress) != 3 {
		t.Fatalf("frame 1: got %d egress ports, want 3", len(res1.Egress))
	}
	wantEgressPorts1 := []string{"1/1/2", "1/1/3", "1/1/4"}
	for i, eg := range res1.Egress {
		if eg.Port != wantEgressPorts1[i] {
			t.Errorf("frame 1 egress %d: got port %q, want %q", i, eg.Port, wantEgressPorts1[i])
		}
		if len(eg.Frame.Tags) != 1 || eg.Frame.Tags[0].VID != 20 {
			t.Errorf("frame 1 egress %d: tag altered, got %v", i, eg.Frame.Tags)
		}
		if eg.Dropped != "" {
			t.Errorf("frame 1 egress %d: unexpected drop reason %v", i, eg.Dropped)
		}
	}
	for _, step := range res1.Steps {
		if step.Op == trace.OpLearn {
			t.Errorf("frame 1: unexpected learn step in hub trace: %v", step)
		}
		if step.Layer != port.LayerName {
			t.Errorf("frame 1: got step layer %v, want %v", step.Layer, port.LayerName)
		}
	}
	if sw.Entries() != nil {
		t.Errorf("hub should have no FDB entries, got %v", sw.Entries())
	}

	f2 := ethernet.Frame{
		Dst:       srcMAC,
		Src:       unknownDst,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("untagged response"),
	}

	res2 := sw.Receive(fixedTime.Add(time.Second), "1/1/3", f2)
	if res2.Outcome != trace.Flooded {
		t.Errorf("frame 2: got outcome %v, want %v", res2.Outcome, trace.Flooded)
	}
	if len(res2.Egress) != 3 {
		t.Fatalf("frame 2: got %d egress ports, want 3", len(res2.Egress))
	}
	wantEgressPorts2 := []string{"1/1/1", "1/1/2", "1/1/4"}
	for i, eg := range res2.Egress {
		if eg.Port != wantEgressPorts2[i] {
			t.Errorf("frame 2 egress %d: got port %q, want %q", i, eg.Port, wantEgressPorts2[i])
		}
		if len(eg.Frame.Tags) != 0 {
			t.Errorf("frame 2 egress %d: expected untagged, got tags %v", i, eg.Frame.Tags)
		}
	}
	for _, step := range res2.Steps {
		if step.Op == trace.OpLearn {
			t.Errorf("frame 2: unexpected learn step in hub trace: %v", step)
		}
	}
}

func TestHubWithDownPort(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: 50}))

	sw := mustSwitch(t, vswitch.Config{Ports: tbl})

	f := ethernet.Frame{
		Dst:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   make([]byte, 100),
	}

	t.Run("egress port down and MTU exceeded", func(t *testing.T) {
		res := sw.Receive(fixedTime, "1/1/1", f)
		if res.Outcome != trace.Flooded {
			t.Errorf("got outcome %v, want %v", res.Outcome, trace.Flooded)
		}
		if len(res.Egress) != 2 {
			t.Fatalf("got %d egress ports, want 2 (1/1/2 forwarded, 1/1/4 dropped)", len(res.Egress))
		}
		if res.Egress[0].Port != "1/1/2" || res.Egress[0].Dropped != "" {
			t.Errorf("egress 0: got %v, want 1/1/2 forwarded", res.Egress[0])
		}
		if res.Egress[1].Port != "1/1/4" || res.Egress[1].Dropped != port.ReasonMTUExceeded {
			t.Errorf("egress 1: got %v, want 1/1/4 dropped for MTU exceeded", res.Egress[1])
		}
	})

	t.Run("ingress port down", func(t *testing.T) {
		res := sw.Receive(fixedTime, "1/1/3", f)
		if res.Outcome != trace.Dropped {
			t.Errorf("got outcome %v, want %v", res.Outcome, trace.Dropped)
		}
		if res.Reason != port.ReasonPortDown {
			t.Errorf("got reason %v, want %v", res.Reason, port.ReasonPortDown)
		}
		if len(res.Egress) != 0 {
			t.Errorf("got %d egress entries, want 0", len(res.Egress))
		}
	})
}

func TestHubRepeatsBPDU(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{Ports: tbl}
	sw := mustSwitch(t, cfg)

	macRoot := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}
	b := bpdu.BPDU{
		RootID:   bpdu.BridgeID{Priority: 4096, Address: macRoot},
		BridgeID: bpdu.BridgeID{Priority: 4096, Address: macRoot},
	}
	frame := mustEncode(t, b, macRoot)

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	res := sw.Receive(now, "1/1/1", frame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Flooded)
	}
	if len(res.Egress) != 1 || res.Egress[0].Port != "1/1/2" {
		t.Errorf("res.Egress = %+v, want flooded to 1/1/2", res.Egress)
	}
}

func TestHubFrameEgressLAGNoMember(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		LAG: &lag.Config{LAGs: map[string]lag.LAG{
			"lag1": {LACP: lag.LACPConfig{Mode: lag.Active}, Members: map[string]lag.Member{"1/1/2": {}}},
		}},
	}
	sw := mustSwitch(t, cfg)

	frame := ethernet.Frame{
		Src:       macH1,
		Dst:       macH2,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("hub to lag"),
	}
	res := sw.Receive(fixedTime, "1/1/1", frame)
	step, ok := findStep(res.Steps, trace.RuleID("lag.egress.no_member"))
	if !ok {
		t.Fatalf("no literal lag.egress.no_member step in trace: %+v", res.Steps)
	}
	if step.Layer != port.LayerName {
		t.Errorf("step layer = %v, want %v", step.Layer, port.LayerName)
	}
}
