package vswitch_test

import (
	"net/netip"
	"reflect"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// TestPeekARPReplyKeepsPendingNeighborAndHeldFrame proves that previewing a
// resolving reply leaves both neighbor state and the held frame untouched.
func TestPeekARPReplyKeepsPendingNeighborAndHeldFrame(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildBaseRoutingSwitch(t)
	dst := netip.MustParseAddr("10.0.20.77")
	frame := ethernet.Frame{
		Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4,
		Payload: makeIPv4Packet(t, ipH1, dst, 64, []byte("held")),
	}
	if res := captureOutput(callOutputs, sw, sw.Receive(fixedTime, "1/1/1", frame)); res.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", res.Outcome)
	}
	dataAfterSolicitations(t, takeEmissions(callOutputs, sw), 1)
	before := sw.Neighbors()
	wake, hasWake := sw.NextWake()
	learnedMAC := netaddr.MAC{2, 0, 0, 0, 0x20, 0x77}
	reply := makeARPReply(t, dst, netip.MustParseAddr("10.0.20.1"), learnedMAC, macRouter)
	sw.Peek(fixedTime.Add(time.Millisecond), "1/1/2", reply)
	if after := sw.Neighbors(); !reflect.DeepEqual(after, before) {
		t.Errorf("neighbors after Peek = %+v, want %+v", after, before)
	}
	if at, ok := sw.NextWake(); ok != hasWake || !at.Equal(wake) {
		t.Errorf("wake after Peek = %v/%v, want %v/%v", at, ok, wake, hasWake)
	}
	if emissions := takeEmissions(callOutputs, sw); len(emissions) != 0 {
		t.Errorf("emissions after Peek = %+v, want none", emissions)
	}
	if failures := takeDrops(callOutputs, sw); len(failures) != 0 {
		t.Errorf("failures after Peek = %+v, want none", failures)
	}
	captureOutput(callOutputs, sw, sw.Receive(fixedTime.Add(2*time.Millisecond), "1/1/2", reply))
	emission := soleDataEmission(t, takeEmissions(callOutputs, sw), learnedMAC)
	if emission.Port != "1/1/2" {
		t.Errorf("released port = %q, want 1/1/2", emission.Port)
	}
}

// TestAdvanceEmitsNeighborRetransmission proves Advance delivers the protocol frame
// returned by the routing timer at its retransmission deadline.
func TestAdvanceEmitsNeighborRetransmission(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	sw := buildBaseRoutingSwitch(t)
	dst := netip.MustParseAddr("10.0.20.77")
	captureOutput(callOutputs, sw, holdFrameToward(t, sw, fixedTime, dst, []byte("held")))
	dataAfterSolicitations(t, takeEmissions(callOutputs, sw), 1)
	captureOutput(callOutputs, sw, sw.Advance(fixedTime.Add(time.Second-time.Nanosecond)))
	if emissions := takeEmissions(callOutputs, sw); len(emissions) != 0 {
		t.Fatalf("early emissions = %+v, want none", emissions)
	}
	captureOutput(callOutputs, sw, sw.Advance(fixedTime.Add(time.Second)))
	emissions := takeEmissions(callOutputs, sw)
	if len(emissions) != 1 {
		t.Fatalf("retransmissions = %+v, want one ARP request", emissions)
	}
	em := emissions[0]
	msg, err := arp.Decode(em.Frame)
	if err != nil {
		t.Fatal(err)
	}
	if !em.Protocol || em.Port != "1/1/2" || msg.Operation != arp.Request || msg.TargetAddr != dst {
		t.Errorf("retransmission = %+v, message = %+v, want protocol ARP request on 1/1/2 for %s", em, msg, dst)
	}
}

// TestRoutingSolicitationSkipsRefusingVLANMember proves neighbor protocol
// emissions obey the bridge's per-port egress refusal.
func TestRoutingSolicitationSkipsRefusingVLANMember(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]vswitch.Output)
	for _, refusal := range []string{"down", "spanning-tree"} {
		t.Run(refusal, func(t *testing.T) {
			cfg := buildBaseRoutingSwitch(t).Config()
			oper := port.Up
			if refusal == "down" {
				oper = port.Down
			}
			cfg.Ports = mustTable(t, port.NewBuilder().
				Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: oper}).
				Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
			p20 := vlan.ID(20)
			for _, name := range []string{"1/1/3", "1/1/4"} {
				cfg.Bridge.VLAN.Switchports[name] = bridge.Switchport{PVID: &p20, Untagged: []vlan.ID{20}}
			}
			if refusal == "spanning-tree" {
				cfg.STP = &stp.Config{Priority: 32768, Address: macRouter, Ports: map[string]stp.Port{
					"1/1/1": {AdminEdge: true}, "1/1/2": {}, "1/1/3": {AdminEdge: true}, "1/1/4": {AdminEdge: true},
				}}
			}
			sw := mustSwitch(t, cfg)
			captureOutput(callOutputs, sw, sw.Start(fixedTime))
			takeEmissions(callOutputs, sw)
			dst := netip.MustParseAddr("10.0.20.77")
			captureOutput(callOutputs, sw, holdFrameToward(t, sw, fixedTime, dst, []byte("held")))
			var ports []string
			for _, em := range takeEmissions(callOutputs, sw) {
				msg, err := arp.Decode(em.Frame)
				if err != nil {
					t.Fatal(err)
				}
				if !em.Protocol || msg.Operation != arp.Request || msg.TargetAddr != dst {
					t.Errorf("emission = %+v, message = %+v, want solicitation", em, msg)
				}
				ports = append(ports, em.Port)
			}
			slices.Sort(ports)
			if !slices.Equal(ports, []string{"1/1/3", "1/1/4"}) {
				t.Errorf("solicitation ports = %v, want [1/1/3 1/1/4]", ports)
			}
		})
	}
}
