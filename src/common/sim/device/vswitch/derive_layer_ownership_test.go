package vswitch_test

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// protocolLayer names the layer whose frame an emission is, decided by decoding the frame
// with the codec the layer builds it with, never by anything the switch recorded about it.
type protocolLayer string

const (
	layerSTP         protocolLayer = "spanning tree"
	layerLAG         protocolLayer = "link aggregation"
	layerLoopProtect protocolLayer = "loop protection"
	layerNone        protocolLayer = ""
)

func decodeProtocolLayer(em device.Emission) protocolLayer {
	if _, err := bpdu.Decode(em.Frame); err == nil {
		return layerSTP
	}
	if _, err := lacp.Decode(em.Frame); err == nil {
		return layerLAG
	}
	if _, err := loopprotect.Decode(em.Frame); err == nil {
		return layerLoopProtect
	}
	return layerNone
}

// layerOwnershipConfig builds a switch whose three protocol layers all emit on a timer. The
// LAG's members come up after upDelay, which makes the LAG layer report a state change to
// spanning tree and loop protection from inside an advance.
func layerOwnershipConfig(t *testing.T, upDelay time.Duration, mode lag.LACPMode) vswitch.Config {
	t.Helper()
	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	vid10 := vlan.ID(10)
	untagged := bridge.Switchport{PVID: &vid10, Untagged: []vlan.ID{10}}
	return vswitch.Config{
		MAC: mac,
		Ports: mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}).
			Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})),
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table:       map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{"1/1/1": untagged, "lag1": untagged},
		}},
		STP: &stp.Config{Priority: 32768, Address: mac, Ports: map[string]stp.Port{"1/1/1": {AdminEdge: true}, "lag1": {}}},
		LoopProtect: &loopprotect.Config{Interval: 5 * time.Second, Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block, VLANs: []vlan.ID{10}},
			"lag1":  {Action: loopprotect.Block, VLANs: []vlan.ID{10}},
		}},
		LAG: &lag.Config{LAGs: map[string]lag.LAG{"lag1": {
			UpDelay: upDelay,
			LACP:    lag.LACPConfig{Mode: mode, Fast: true},
			Members: map[string]lag.Member{"1/1/3": {}, "1/1/4": {}},
		}}},
	}
}

// TestDeriveReturnsNoProtocolFrameOfRebuiltLayer checks that Derive withholds a protocol
// frame exactly when the layer that built it is rebuilt, whichever layer's timer was running
// when the frame was produced. A LAG transition makes spanning tree and loop protection speak
// from inside the LAG layer's timer, and the frame is still theirs.
func TestDeriveReturnsNoProtocolFrameOfRebuiltLayer(t *testing.T) {
	type rebuilt struct{ stp, lag, loop bool }
	rebuildSets := func(own protocolLayer) []rebuilt {
		switch own {
		case layerSTP:
			return []rebuilt{{}, {stp: true}, {lag: true}, {stp: true, lag: true}}
		case layerLoopProtect:
			return []rebuilt{{}, {loop: true}, {lag: true}, {loop: true, lag: true}}
		default:
			return []rebuilt{{}, {lag: true}}
		}
	}
	scenarios := []struct {
		name    string
		own     protocolLayer
		upDelay time.Duration
		mode    lag.LACPMode
		advance time.Duration
	}{
		{"spanning tree by its own timer", layerSTP, 0, lag.Off, 2 * time.Second},
		{"spanning tree through a LAG transition", layerSTP, 1500 * time.Millisecond, lag.Off, 1500 * time.Millisecond},
		{"LACP", layerLAG, 0, lag.Active, 2 * time.Second},
		{"loop protection by its own timer", layerLoopProtect, 0, lag.Off, 5 * time.Second},
		{"loop protection through a LAG transition", layerLoopProtect, 5 * time.Second, lag.Off, 5 * time.Second},
	}
	cases := 0
	for _, sc := range scenarios {
		for _, r := range rebuildSets(sc.own) {
			cases++
			t.Run(fmt.Sprintf("%s/rebuilt %+v", sc.name, r), func(t *testing.T) {
				sw := mustSwitch(t, layerOwnershipConfig(t, sc.upDelay, sc.mode))
				sw.Start(fixedTime)
				now := fixedTime.Add(sc.advance)
				owed := sw.Fork().(*vswitch.Switch).Advance(now)
				if !emitsLayer(owed.Emissions, sc.own) {
					t.Fatalf("the unrebuilt advance emits no %s frame, so the case checks nothing: %+v", sc.own, owed.Emissions)
				}
				target := sw.Spec()
				if r.stp {
					target.Config.STP.Priority = 4096
				}
				if r.lag {
					l := target.Config.LAG.LAGs["lag1"]
					l.HashBasis = 7
					target.Config.LAG.LAGs["lag1"] = l
				}
				if r.loop {
					target.Config.LoopProtect.Interval = 7 * time.Second
				}
				next, out, err := vswitch.Derive(sw, target, now)
				if err != nil {
					t.Fatal(err)
				}
				retention := next.Retention()
				if retention.STP.Kept == r.stp || retention.LAG.Kept == r.lag || retention.LoopProtect.Kept == r.loop {
					t.Fatalf("retention = stp %v, lag %v, loop protection %v, want the opposite of the rebuilt set %+v", retention.STP.Kept, retention.LAG.Kept, retention.LoopProtect.Kept, r)
				}
				rebuiltLayer := map[protocolLayer]bool{layerSTP: r.stp, layerLAG: r.lag, layerLoopProtect: r.loop}
				for _, em := range out.Emissions {
					if layer := decodeProtocolLayer(em); layer != layerNone && rebuiltLayer[layer] {
						t.Errorf("derive returned a %s frame on %s although that layer was rebuilt", layer, em.Port)
					}
				}
				for _, em := range owed.Emissions {
					layer := decodeProtocolLayer(em)
					if layer == layerNone || rebuiltLayer[layer] {
						continue
					}
					if !containsEmission(out.Emissions, em) {
						t.Errorf("derive dropped a %s frame on %s although that layer was kept", layer, em.Port)
					}
				}
			})
		}
	}
	t.Run("frames with a hold token stay while spanning tree is rebuilt", func(t *testing.T) {
		cases++
		cfg, _, _, _, _, _ := routedEgressConfig(t, "routed port", "none")
		cfg.STP = &stp.Config{Priority: 32768, Address: macRouter, Ports: map[string]stp.Port{"1/1/1": {AdminEdge: true}}}
		sw := mustSwitch(t, cfg)
		sw.Start(fixedTime)
		frame := ethernet.Frame{
			Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4,
			Tags:    []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}},
			Payload: makeIPv4Packet(t, ipH1, ipH2, 64, []byte("held until timeout")),
		}
		held := sw.Receive(fixedTime.Add(time.Second), "1/1/1", frame)
		if held.Outcome != trace.Held || held.Hold == 0 {
			t.Fatalf("held = %+v, want a held frame with a token", held)
		}
		// Walk the timers until one advance both times the held frame out and sends a
		// spanning tree hello, which is the advance Derive is about to replay.
		var now time.Time
		var owed device.Output
		for range 40 {
			wake, ok := sw.NextWake()
			if !ok {
				break
			}
			now = wake
			owed = sw.Fork().(*vswitch.Switch).Advance(now)
			if slices.ContainsFunc(owed.Drops, func(d device.Drop) bool { return d.Hold == held.Hold }) && emitsLayer(owed.Emissions, layerSTP) {
				break
			}
			sw.Advance(now)
		}
		if !slices.ContainsFunc(owed.Drops, func(d device.Drop) bool { return d.Hold == held.Hold }) || !emitsLayer(owed.Emissions, layerSTP) {
			t.Fatalf("no advance times out frame %d and sends a spanning tree frame together, last = %+v", held.Hold, owed)
		}
		target := sw.Spec()
		target.Config.STP.Priority = 4096
		next, out, err := vswitch.Derive(sw, target, now)
		if err != nil {
			t.Fatal(err)
		}
		if next.Retention().STP.Kept {
			t.Fatal("spanning tree kept across a priority change, want rebuilt")
		}
		if !slices.ContainsFunc(out.Drops, func(d device.Drop) bool { return d.Hold == held.Hold }) {
			t.Errorf("derive drops = %+v, want the drop of held frame %d", out.Drops, held.Hold)
		}
		for _, em := range out.Emissions {
			if decodeProtocolLayer(em) == layerSTP {
				t.Errorf("derive returned a spanning tree frame on %s although that layer was rebuilt", em.Port)
			}
		}
		for _, em := range owed.Emissions {
			if em.Hold != 0 && !containsEmission(out.Emissions, em) {
				t.Errorf("derive dropped the emission %+v of held frame %d", em, em.Hold)
			}
		}
		for _, cp := range owed.Copies {
			if cp.Hold != 0 && !slices.ContainsFunc(out.Copies, func(c device.Copy) bool { return c.Hold == cp.Hold && reflect.DeepEqual(c.Frame, cp.Frame) }) {
				t.Errorf("derive dropped the copy %+v of held frame %d", cp, cp.Hold)
			}
		}
	})

	for _, rebuildRouting := range []bool{false, true} {
		t.Run(fmt.Sprintf("neighbor solicitation/routing rebuilt %v", rebuildRouting), func(t *testing.T) {
			cases++
			cfg, _, _, _, _, _ := routedEgressConfig(t, "routed port", "none")
			sw := mustSwitch(t, cfg)
			frame := ethernet.Frame{
				Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4,
				Tags:    []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}},
				Payload: makeIPv4Packet(t, ipH1, ipH2, 64, []byte("awaiting a neighbor")),
			}
			sw.Receive(fixedTime, "1/1/1", frame)
			now := fixedTime.Add(time.Second)
			owed := sw.Fork().(*vswitch.Switch).Advance(now)
			if !slices.ContainsFunc(owed.Emissions, isSolicitation) {
				t.Fatalf("the unrebuilt advance emits no solicitation, so the case checks nothing: %+v", owed.Emissions)
			}
			target := sw.Spec()
			if rebuildRouting {
				vrf := target.Config.Routing.VRFs["default"]
				vrf.NeighborPolicy.HoldDepth = 7
				target.Config.Routing.VRFs["default"] = vrf
			}
			next, out, err := vswitch.Derive(sw, target, now)
			if err != nil {
				t.Fatal(err)
			}
			if next.Retention().Routing.Kept == rebuildRouting {
				t.Fatalf("routing kept = %v, want %v", next.Retention().Routing.Kept, !rebuildRouting)
			}
			if got := slices.ContainsFunc(out.Emissions, isSolicitation); got == rebuildRouting {
				t.Errorf("derive returned a solicitation = %v with routing rebuilt = %v", got, rebuildRouting)
			}
		})
	}

	if want := 21; cases != want {
		t.Fatalf("ran %d cases, want %d", cases, want)
	}
}

func isSolicitation(em device.Emission) bool {
	msg, err := arp.Decode(em.Frame)
	return err == nil && msg.Operation == arp.Request
}

func emitsLayer(emissions []device.Emission, layer protocolLayer) bool {
	for _, em := range emissions {
		if decodeProtocolLayer(em) == layer {
			return true
		}
	}
	return false
}

func containsEmission(emissions []device.Emission, want device.Emission) bool {
	for _, em := range emissions {
		if em.Port == want.Port && reflect.DeepEqual(em.Frame, want.Frame) {
			return true
		}
	}
	return false
}
