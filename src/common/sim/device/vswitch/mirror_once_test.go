package vswitch_test

import (
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// mirrorOnceFixture is heldMirrorFixture with more mirror destinations: "span" and "span2" are
// switchports carrying VLAN 10, which a VLAN-output mirror copies to. The routing layer holds
// one frame per neighbor, so a second held frame evicts the first.
func mirrorOnceFixture(t *testing.T, mirrors []traffic.Mirror, resolved bool) (*vswitch.Switch, ethernet.Frame, ethernet.Frame, string) {
	t.Helper()
	cfg, replyPort, replyTags, _, _, _ := routedEgressConfig(t, "routed port", "none")
	b := port.NewBuilder()
	for _, p := range cfg.Ports.Ports() {
		b.Add(p)
	}
	b.Add(port.Port{Name: "mirror", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "span", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "span2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	cfg.Ports = mustTable(t, b)
	cfg.Bridge.VLAN.Switchports["span"] = bridge.Switchport{Tagged: []vlan.ID{10}}
	cfg.Bridge.VLAN.Switchports["span2"] = bridge.Switchport{Tagged: []vlan.ID{10}}
	cfg.Traffic = &traffic.Config{Mirrors: slices.Clone(mirrors)}
	vrf := cfg.Routing.VRFs[routing.DefaultVRF]
	vrf.NeighborPolicy.HoldDepth = 1
	if resolved {
		vrf.Neighbors = []routing.Neighbor{{Interface: "vlan20", Addr: ipH2, MAC: macH2}}
	}
	cfg.Routing.VRFs[routing.DefaultVRF] = vrf
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

// TestMirrorCopiesFrameOncePerDestinationAcrossHistories checks that a mirror copies a frame to
// its destination at most once, however long the frame is held and whatever is derived or forked
// while it waits. The live case, a frame forwarded with its neighbor resolved, is the reference
// for how many copies each destination gets. A frame that never leaves its hold queue gets at
// most the copies taken on arrival.
func TestMirrorCopiesFrameOncePerDestinationAcrossHistories(t *testing.T) {
	out := vlan.ID(10)
	selectors := []struct {
		name    string
		mirrors []traffic.Mirror
		// each is how many copies the live case gives every destination.
		each int
		snap int
	}{
		{"select all", []traffic.Mirror{{Name: "span", SelectAll: true, OutputPort: "mirror"}}, 1, 0},
		{"source port", []traffic.Mirror{{Name: "span", SelectSrcPorts: []string{"1/1/1"}, OutputPort: "mirror"}}, 1, 0},
		{"destination port", []traffic.Mirror{{Name: "span", SelectDstPorts: []string{"1/1/2"}, OutputPort: "mirror"}}, 1, 0},
		{"source and destination", []traffic.Mirror{{Name: "span", SelectSrcPorts: []string{"1/1/1"}, SelectDstPorts: []string{"1/1/2"}, OutputPort: "mirror"}}, 1, 0},
		{"output VLAN with select all", []traffic.Mirror{{Name: "span", SelectAll: true, OutputVLAN: &out}}, 1, 0},
		{"snap length", []traffic.Mirror{{Name: "span", SelectAll: true, SnapLen: 40, OutputPort: "mirror"}}, 1, 40},
		{"two mirrors to one output", []traffic.Mirror{
			{Name: "first", SelectAll: true, OutputPort: "mirror"},
			{Name: "second", SelectSrcPorts: []string{"1/1/1"}, OutputPort: "mirror"},
		}, 2, 0},
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
	// Each history returns the outputs that carry the frame's copies. A history that
	// releases the frame is compared with the live case exactly, one that does not release it
	// is bounded by it.
	histories := []struct {
		name    string
		release bool
		run     func(t *testing.T, sw *vswitch.Switch, frame, reply ethernet.Frame, replyPort string) []device.Output
	}{
		{"live", true, func(t *testing.T, sw *vswitch.Switch, frame, _ ethernet.Frame, _ string) []device.Output {
			live := sw.Receive(releaseAt, "1/1/1", frame)
			if live.Outcome != trace.Forwarded || len(live.Egress) != 1 {
				t.Fatalf("live = %s with egress %+v, want forwarded out one port", live.Outcome, live.Egress)
			}
			return []device.Output{live}
		}},
		{"held then released", true, func(t *testing.T, sw *vswitch.Switch, frame, reply ethernet.Frame, replyPort string) []device.Output {
			held := hold(t, sw, frame)
			return []device.Output{held, sw.Receive(releaseAt, replyPort, reply)}
		}},
		{"held, forked, released on the fork", true, func(t *testing.T, sw *vswitch.Switch, frame, reply ethernet.Frame, replyPort string) []device.Output {
			held := hold(t, sw, frame)
			return []device.Output{held, sw.Fork().Receive(releaseAt, replyPort, reply)}
		}},
		{"held, forked, released on the source", true, func(t *testing.T, sw *vswitch.Switch, frame, reply ethernet.Frame, replyPort string) []device.Output {
			held := hold(t, sw, frame)
			sw.Fork()
			return []device.Output{held, sw.Receive(releaseAt, replyPort, reply)}
		}},
		{"held, derived keeping traffic and routing", true, func(t *testing.T, sw *vswitch.Switch, frame, reply ethernet.Frame, replyPort string) []device.Output {
			held := hold(t, sw, frame)
			derived := derive(t, sw, func(spec vswitch.ConstructionSpec) vswitch.ConstructionSpec { return spec })
			return []device.Output{held, derived.Receive(releaseAt, replyPort, reply)}
		}},
		{"held, derived rebuilding traffic and keeping routing", true, func(t *testing.T, sw *vswitch.Switch, frame, reply ethernet.Frame, replyPort string) []device.Output {
			held := hold(t, sw, frame)
			derived := derive(t, sw, rebuildTraffic)
			return []device.Output{held, derived.Receive(releaseAt, replyPort, reply)}
		}},
		{"held until the hold times out", false, func(t *testing.T, sw *vswitch.Switch, frame, _ ethernet.Frame, _ string) []device.Output {
			held := hold(t, sw, frame)
			outputs := []device.Output{held}
			for range 20 {
				wake, ok := sw.NextWake()
				if !ok {
					break
				}
				advanced := sw.Advance(wake)
				outputs = append(outputs, advanced)
				if slices.ContainsFunc(advanced.Drops, func(d device.Drop) bool { return d.Hold == held.Hold }) {
					return outputs
				}
			}
			t.Fatalf("hold %d never timed out, outputs = %+v", held.Hold, outputs)
			return nil
		}},
		{"held until a second frame evicts it", false, func(t *testing.T, sw *vswitch.Switch, frame, _ ethernet.Frame, _ string) []device.Output {
			held := hold(t, sw, frame)
			other := frame
			other.Src = netaddr.MAC{0x02, 0, 0, 0, 0, 0x99}
			other.Payload = makeIPv4Packet(t, ipH1, ipH2, 64, []byte("the frame that evicts"))
			evictor := sw.Receive(fixedTime.Add(time.Microsecond), "1/1/1", other)
			if !slices.ContainsFunc(evictor.Drops, func(d device.Drop) bool { return d.Hold == held.Hold }) {
				t.Fatalf("evictor drops = %+v, want the eviction of hold %d", evictor.Drops, held.Hold)
			}
			// The evictor's own arrival copies belong to another frame.
			evictor.Copies = slices.DeleteFunc(slices.Clone(evictor.Copies), func(cp device.Copy) bool { return cp.Frame.Src == other.Src })
			return []device.Output{held, evictor}
		}},
	}
	frameOctets := func(f ethernet.Frame) int { return 14 + 4*len(f.Tags) + len(f.Payload) }

	cases := 0
	for _, sel := range selectors {
		reference := map[string]int{}
		{
			sw, frame, reply, replyPort := mirrorOnceFixture(t, sel.mirrors, true)
			for _, o := range histories[0].run(t, sw, frame, reply, replyPort) {
				copiesByPort(t, reference, o.Copies)
			}
		}
		if len(reference) == 0 {
			t.Fatalf("%s: the live case copies the frame nowhere, so the check has no reference", sel.name)
		}
		for port, n := range reference {
			if n != sel.each {
				t.Fatalf("%s: live copies to %s = %d, want %d", sel.name, port, n, sel.each)
			}
		}
		for _, h := range histories {
			cases++
			t.Run(fmt.Sprintf("%s/%s", sel.name, h.name), func(t *testing.T) {
				sw, frame, reply, replyPort := mirrorOnceFixture(t, sel.mirrors, h.name == "live")
				outputs := h.run(t, sw, frame, reply, replyPort)
				got := map[string]int{}
				for _, o := range outputs {
					copiesByPort(t, got, o.Copies)
					for _, cp := range dataCopies(o.Copies) {
						if sel.snap > 0 && frameOctets(cp.Frame) > sel.snap {
							t.Errorf("copy %+v is %d octets, want at most the snap length %d", cp, frameOctets(cp.Frame), sel.snap)
						}
						if !h.release && cp.Hold != 0 {
							t.Errorf("copy %+v carries hold %d although the frame was never released", cp, cp.Hold)
						}
					}
				}
				if h.release && !maps.Equal(got, reference) {
					t.Errorf("copies per destination = %v, want the live case's %v", got, reference)
				}
				if !h.release {
					for port, n := range got {
						if n > reference[port] {
							t.Errorf("copies to %s = %d, want at most the live case's %d", port, n, reference[port])
						}
					}
				}
			})
		}
	}

	copiesAfterDerive := func(t *testing.T, mirrors []traffic.Mirror, edit func(vswitch.ConstructionSpec) vswitch.ConstructionSpec) map[string]int {
		t.Helper()
		sw, frame, reply, replyPort := mirrorOnceFixture(t, mirrors, false)
		held := hold(t, sw, frame)
		derived := derive(t, sw, edit)
		got := map[string]int{}
		copiesByPort(t, got, held.Copies)
		copiesByPort(t, got, derived.Receive(releaseAt, replyPort, reply).Copies)
		return got
	}

	t.Run("derived with the mirror renamed to the same destination", func(t *testing.T) {
		cases++
		got := copiesAfterDerive(t, selectors[0].mirrors, func(spec vswitch.ConstructionSpec) vswitch.ConstructionSpec {
			spec.Config.Traffic.Mirrors[0].Name = "renamed"
			return rebuildTraffic(spec)
		})
		if want := map[string]int{"mirror": 1}; !maps.Equal(got, want) {
			t.Errorf("copies per destination = %v, want %v for a rebuilt mirror that sends where its predecessor did", got, want)
		}
	})

	t.Run("derived with the mirror sent to a new destination under its name", func(t *testing.T) {
		cases++
		got := copiesAfterDerive(t, selectors[0].mirrors, func(spec vswitch.ConstructionSpec) vswitch.ConstructionSpec {
			spec.Config.Traffic.Mirrors[0].OutputPort = "span2"
			return spec
		})
		if want := map[string]int{"mirror": 1, "span2": 1}; !maps.Equal(got, want) {
			t.Errorf("copies per destination = %v, want %v: the old destination's copy was taken on arrival and the new one is owed at release", got, want)
		}
	})

	t.Run("derived with two mirrors to one output keeping one", func(t *testing.T) {
		cases++
		got := copiesAfterDerive(t, selectors[len(selectors)-1].mirrors, func(spec vswitch.ConstructionSpec) vswitch.ConstructionSpec {
			spec.Config.Traffic.Mirrors = spec.Config.Traffic.Mirrors[:1]
			return spec
		})
		if want := map[string]int{"mirror": 2}; !maps.Equal(got, want) {
			t.Errorf("copies per destination = %v, want %v: both mirrors copied on arrival and the survivor owes nothing more", got, want)
		}
	})

	t.Run("derived rebuilding routing fails the held frame", func(t *testing.T) {
		cases++
		sw, frame, reply, replyPort := mirrorOnceFixture(t, selectors[0].mirrors, false)
		held := hold(t, sw, frame)
		target := sw.Spec()
		vrf := target.Config.Routing.VRFs["default"]
		vrf.NeighborPolicy.HoldDepth = 7
		target.Config.Routing.VRFs["default"] = vrf
		derived, out, err := vswitch.Derive(sw, target, fixedTime.Add(time.Microsecond))
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Drops) != 1 || out.Drops[0].Hold != held.Hold {
			t.Errorf("derive drops = %+v, want one drop of held frame %d", out.Drops, held.Hold)
		}
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

	if want := 60; cases != want {
		t.Fatalf("ran %d cases, want %d", cases, want)
	}
}
