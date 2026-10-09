package vswitch_test

import (
	"fmt"
	"maps"
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// mirrorOnceFixture is heldMirrorFixture with a second mirror destination: "span" is a
// switchport carrying VLAN 10, which a VLAN-output mirror copies to.
func mirrorOnceFixture(t *testing.T, mirror traffic.Mirror, resolved bool) (*vswitch.Switch, ethernet.Frame, ethernet.Frame, string) {
	t.Helper()
	cfg, replyPort, replyTags, _, _, _ := routedEgressConfig(t, "routed port", "none")
	b := port.NewBuilder()
	for _, p := range cfg.Ports.Ports() {
		b.Add(p)
	}
	b.Add(port.Port{Name: "mirror", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "span", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	cfg.Ports = mustTable(t, b)
	cfg.Bridge.VLAN.Switchports["span"] = bridge.Switchport{Tagged: []vlan.ID{10}}
	cfg.Traffic = &traffic.Config{Mirrors: []traffic.Mirror{mirror}}
	if resolved {
		vrf := cfg.Routing.VRFs[routing.DefaultVRF]
		vrf.Neighbors = []routing.Neighbor{{Interface: "vlan20", Addr: ipH2, MAC: macH2}}
		cfg.Routing.VRFs[routing.DefaultVRF] = vrf
	}
	sw := mustSwitch(t, cfg)
	frame := ethernet.Frame{
		Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4,
		Tags:    []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}},
		Payload: makeIPv4Packet(t, ipH1, ipH2, 64, []byte("mirrored once")),
	}
	reply := makeARPReply(t, ipH2, netip.MustParseAddr("10.0.20.1"), macH2, macRouter)
	reply.Tags = replyTags
	return sw, frame, reply, replyPort
}

// copiesByPort counts the copies of the IPv4 data frame per destination port and fails on
// a copy that leaves on the frame's ingress port, 1/1/1.
func copiesByPort(t *testing.T, counts map[string]int, copies []device.Copy) {
	t.Helper()
	for _, cp := range dataCopies(copies) {
		if cp.Port == "1/1/1" {
			t.Errorf("copy %+v leaves on the frame's ingress port", cp)
		}
		counts[cp.Port]++
	}
}

// TestMirrorCopiesFrameOncePerDestinationAcrossHistories checks that a frame is mirrored to
// one destination at most once, however long it is held and whatever is derived or forked
// while it waits. The live case, a frame forwarded with its neighbor resolved, is the
// reference for how many copies each destination gets.
func TestMirrorCopiesFrameOncePerDestinationAcrossHistories(t *testing.T) {
	out := vlan.ID(10)
	selectors := []struct {
		name   string
		mirror traffic.Mirror
	}{
		{"select all", traffic.Mirror{Name: "span", SelectAll: true, OutputPort: "mirror"}},
		{"source port", traffic.Mirror{Name: "span", SelectSrcPorts: []string{"1/1/1"}, OutputPort: "mirror"}},
		{"destination port", traffic.Mirror{Name: "span", SelectDstPorts: []string{"1/1/2"}, OutputPort: "mirror"}},
		{"source and destination", traffic.Mirror{Name: "span", SelectSrcPorts: []string{"1/1/1"}, SelectDstPorts: []string{"1/1/2"}, OutputPort: "mirror"}},
		{"output VLAN with select all", traffic.Mirror{Name: "span", SelectAll: true, OutputVLAN: &out}},
	}
	releaseAt := fixedTime.Add(time.Millisecond)
	hold := func(t *testing.T, sw *vswitch.Switch, frame ethernet.Frame) device.Output {
		t.Helper()
		held := sw.Receive(fixedTime, "1/1/1", frame)
		if held.Outcome != trace.Held || held.Hold == 0 {
			t.Fatalf("held = %+v, want a held frame with a token", held)
		}
		return held
	}
	rebuildTraffic := func(spec vswitch.ConstructionSpec) vswitch.ConstructionSpec {
		spec.Config.Traffic.Policers = map[string]traffic.Policer{"1/1/2": {RateBPS: 1_000_000_000, BurstOctets: 1_000_000}}
		return spec
	}
	derive := func(t *testing.T, sw *vswitch.Switch, edit func(vswitch.ConstructionSpec) vswitch.ConstructionSpec) *vswitch.Switch {
		t.Helper()
		derived, _, err := vswitch.Derive(sw, edit(sw.Spec()), fixedTime.Add(time.Microsecond))
		if err != nil {
			t.Fatal(err)
		}
		return derived
	}
	histories := []struct {
		name string
		run  func(t *testing.T, sw *vswitch.Switch, frame, reply ethernet.Frame, replyPort string) (held, released device.Output)
	}{
		{"live", func(t *testing.T, sw *vswitch.Switch, frame, _ ethernet.Frame, _ string) (device.Output, device.Output) {
			live := sw.Receive(releaseAt, "1/1/1", frame)
			if live.Outcome != trace.Forwarded || len(live.Egress) != 1 {
				t.Fatalf("live = %s with egress %+v, want forwarded out one port", live.Outcome, live.Egress)
			}
			return live, device.Output{}
		}},
		{"held then released", func(t *testing.T, sw *vswitch.Switch, frame, reply ethernet.Frame, replyPort string) (device.Output, device.Output) {
			held := hold(t, sw, frame)
			return held, sw.Receive(releaseAt, replyPort, reply)
		}},
		{"held, forked, released on the fork", func(t *testing.T, sw *vswitch.Switch, frame, reply ethernet.Frame, replyPort string) (device.Output, device.Output) {
			held := hold(t, sw, frame)
			return held, sw.Fork().Receive(releaseAt, replyPort, reply)
		}},
		{"held, forked, released on the source", func(t *testing.T, sw *vswitch.Switch, frame, reply ethernet.Frame, replyPort string) (device.Output, device.Output) {
			held := hold(t, sw, frame)
			sw.Fork()
			return held, sw.Receive(releaseAt, replyPort, reply)
		}},
		{"held, derived keeping traffic and routing", func(t *testing.T, sw *vswitch.Switch, frame, reply ethernet.Frame, replyPort string) (device.Output, device.Output) {
			held := hold(t, sw, frame)
			derived := derive(t, sw, func(spec vswitch.ConstructionSpec) vswitch.ConstructionSpec { return spec })
			return held, derived.Receive(releaseAt, replyPort, reply)
		}},
		{"held, derived rebuilding traffic and keeping routing", func(t *testing.T, sw *vswitch.Switch, frame, reply ethernet.Frame, replyPort string) (device.Output, device.Output) {
			held := hold(t, sw, frame)
			derived := derive(t, sw, rebuildTraffic)
			return held, derived.Receive(releaseAt, replyPort, reply)
		}},
	}

	cases := 0
	for _, sel := range selectors {
		reference := map[string]int{}
		{
			sw, frame, reply, replyPort := mirrorOnceFixture(t, sel.mirror, true)
			held, released := histories[0].run(t, sw, frame, reply, replyPort)
			copiesByPort(t, reference, held.Copies)
			copiesByPort(t, reference, released.Copies)
		}
		if len(reference) == 0 {
			t.Fatalf("%s: the live case copies the frame nowhere, so the check has no reference", sel.name)
		}
		for port, n := range reference {
			if n != 1 {
				t.Fatalf("%s: live copies to %s = %d, want 1", sel.name, port, n)
			}
		}
		for _, h := range histories {
			cases++
			t.Run(fmt.Sprintf("%s/%s", sel.name, h.name), func(t *testing.T) {
				sw, frame, reply, replyPort := mirrorOnceFixture(t, sel.mirror, h.name == "live")
				held, released := h.run(t, sw, frame, reply, replyPort)
				got := map[string]int{}
				copiesByPort(t, got, held.Copies)
				copiesByPort(t, got, released.Copies)
				if !maps.Equal(got, reference) {
					t.Errorf("copies per destination = %v (hold %+v, release %+v), want the live case's %v",
						got, dataCopies(held.Copies), dataCopies(released.Copies), reference)
				}
				for _, cp := range dataCopies(released.Copies) {
					if h.name != "live" && cp.Hold != held.Hold {
						t.Errorf("release copy %+v carries hold %d, want the held frame's %d", cp, cp.Hold, held.Hold)
					}
				}
			})
		}
	}

	t.Run("derived with the mirror renamed to the same destination", func(t *testing.T) {
		cases++
		mirror := selectors[0].mirror
		sw, frame, reply, replyPort := mirrorOnceFixture(t, mirror, false)
		held := hold(t, sw, frame)
		derived := derive(t, sw, func(spec vswitch.ConstructionSpec) vswitch.ConstructionSpec {
			spec.Config.Traffic.Mirrors[0].Name = "renamed"
			return rebuildTraffic(spec)
		})
		got := map[string]int{}
		copiesByPort(t, got, held.Copies)
		copiesByPort(t, got, derived.Receive(releaseAt, replyPort, reply).Copies)
		if want := map[string]int{"mirror": 1}; !maps.Equal(got, want) {
			t.Errorf("copies per destination = %v, want %v for a rebuilt mirror that sends where its predecessor did", got, want)
		}
	})

	t.Run("derived rebuilding routing fails the held frame", func(t *testing.T) {
		cases++
		sw, frame, reply, replyPort := mirrorOnceFixture(t, selectors[0].mirror, false)
		hold(t, sw, frame)
		derived := derive(t, sw, func(spec vswitch.ConstructionSpec) vswitch.ConstructionSpec {
			vrf := spec.Config.Routing.VRFs["default"]
			vrf.NeighborPolicy.HoldDepth = 7
			spec.Config.Routing.VRFs["default"] = vrf
			return spec
		})
		released := derived.Receive(releaseAt, replyPort, reply)
		if got := dataCopies(released.Copies); len(got) != 0 {
			t.Errorf("release copies = %+v, want none for a held frame the rebuilt routing failed", got)
		}
		for _, em := range released.Emissions {
			if em.Frame.EtherType == ethernet.EtherTypeIPv4 {
				t.Errorf("release emission %+v, want none for a held frame the rebuilt routing failed", em)
			}
		}
	})

	if want := 32; cases != want {
		t.Fatalf("ran %d cases, want %d", cases, want)
	}
}
