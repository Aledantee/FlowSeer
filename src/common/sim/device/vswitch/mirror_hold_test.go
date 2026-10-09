package vswitch_test

import (
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// heldMirrorFixture builds the routed-port switch whose neighbor is unresolved, so the first
// frame toward 10.0.20.x is held, and returns the frame, the ARP reply that releases it, and
// the port the reply arrives on.
func heldMirrorFixture(t *testing.T, mirrors []traffic.Mirror) (*vswitch.Switch, ethernet.Frame, ethernet.Frame, string) {
	t.Helper()
	cfg, replyPort, replyTags, _, _, _ := routedEgressConfig(t, "routed port", "none")
	b := port.NewBuilder()
	for _, p := range cfg.Ports.Ports() {
		b.Add(p)
	}
	b.Add(port.Port{Name: "mirror", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	cfg.Ports = mustTable(t, b)
	cfg.Traffic = &traffic.Config{Mirrors: mirrors}
	sw := mustSwitch(t, cfg)
	frame := ethernet.Frame{
		Src: macH1, Dst: macRouter, EtherType: ethernet.EtherTypeIPv4,
		Tags:    []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}},
		Payload: makeIPv4Packet(t, ipH1, ipH2, 64, []byte("held mirror")),
	}
	reply := makeARPReply(t, ipH2, netip.MustParseAddr("10.0.20.1"), macH2, macRouter)
	reply.Tags = replyTags
	return sw, frame, reply, replyPort
}

// dataCopies keeps the copies of the held IPv4 frame and leaves out the copy a catch-all
// mirror also takes of the ARP reply that releases it.
func dataCopies(copies []device.Copy) []device.Copy {
	var data []device.Copy
	for _, cp := range copies {
		if cp.Frame.EtherType == ethernet.EtherTypeIPv4 {
			data = append(data, cp)
		}
	}
	return data
}

func TestHeldFrameIsMirroredOncePerMirror(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mirror traffic.Mirror
	}{
		{"select all", traffic.Mirror{Name: "span", SelectAll: true, OutputPort: "mirror"}},
		{"source and destination", traffic.Mirror{Name: "span", SelectSrcPorts: []string{"1/1/1"}, SelectDstPorts: []string{"1/1/2"}, OutputPort: "mirror"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sw, frame, reply, replyPort := heldMirrorFixture(t, []traffic.Mirror{tc.mirror})
			held := sw.Receive(fixedTime, "1/1/1", frame)
			if held.Outcome != trace.Held || held.Hold == 0 {
				t.Fatalf("held = %+v, want held token", held)
			}
			released := sw.Receive(fixedTime.Add(time.Millisecond), replyPort, reply)
			soleDataEmission(t, released.Emissions, macH2)
			if got := len(dataCopies(held.Copies)) + len(dataCopies(released.Copies)); got != 1 {
				t.Fatalf("copies at hold %+v and release %+v = %d, want 1", held.Copies, dataCopies(released.Copies), got)
			}
		})
	}
}

func TestHeldFrameReleaseKeepsEgressOnlyMirrorAndOtherMirrors(t *testing.T) {
	sw, frame, reply, replyPort := heldMirrorFixture(t, []traffic.Mirror{
		{Name: "ingress", SelectSrcPorts: []string{"1/1/1"}, OutputPort: "mirror"},
		{Name: "egress", SelectDstPorts: []string{"1/1/2"}, OutputPort: "mirror"},
	})
	held := sw.Receive(fixedTime, "1/1/1", frame)
	if len(held.Copies) != 1 || held.Copies[0].Mirror != "ingress" {
		t.Fatalf("held copies = %+v, want the ingress mirror's one", held.Copies)
	}
	released := sw.Receive(fixedTime.Add(time.Millisecond), replyPort, reply)
	if len(released.Copies) != 1 || released.Copies[0].Mirror != "egress" || released.Copies[0].Hold != held.Hold {
		t.Fatalf("released copies = %+v, want the egress mirror's one stamped %d", released.Copies, held.Hold)
	}
}

func TestHeldFrameMirrorMemorySurvivesForkAndDerive(t *testing.T) {
	mirror := traffic.Mirror{Name: "span", SelectAll: true, OutputPort: "mirror"}
	sw, frame, reply, replyPort := heldMirrorFixture(t, []traffic.Mirror{mirror})
	held := sw.Receive(fixedTime, "1/1/1", frame)
	if len(held.Copies) != 1 {
		t.Fatalf("held copies = %+v, want one", held.Copies)
	}
	derived, _, err := vswitch.Derive(sw, sw.Spec(), fixedTime.Add(time.Microsecond))
	if err != nil {
		t.Fatal(err)
	}
	for name, dev := range map[string]device.Device{"fork": sw.Fork(), "derive": derived} {
		released := dev.Receive(fixedTime.Add(time.Millisecond), replyPort, reply)
		if got := dataCopies(released.Copies); len(got) != 0 {
			t.Errorf("%s: released copies = %+v, want none for a frame mirrored at hold", name, got)
		}
	}
}

func TestHeldFrameReleaseVLANMirrorSkipsIngressPort(t *testing.T) {
	out := vlan.ID(10)
	sw, frame, reply, replyPort := heldMirrorFixture(t, []traffic.Mirror{{Name: "span", SelectDstPorts: []string{"1/1/2"}, OutputVLAN: &out}})
	held := sw.Receive(fixedTime, "1/1/1", frame)
	if held.Outcome != trace.Held {
		t.Fatalf("held = %+v, want held", held)
	}
	released := sw.Receive(fixedTime.Add(time.Millisecond), replyPort, reply)
	for _, cp := range released.Copies {
		if cp.Port == "1/1/1" {
			t.Fatalf("copy %+v leaves on the held frame's own ingress port", cp)
		}
	}
}

func TestReceiveMirroredKeepsCopiesOfReleasedFrames(t *testing.T) {
	sw, frame, reply, replyPort := heldMirrorFixture(t, []traffic.Mirror{{Name: "span", SelectDstPorts: []string{"1/1/2"}, OutputPort: "mirror"}})
	held := sw.Receive(fixedTime, "1/1/1", frame)
	if held.Outcome != trace.Held {
		t.Fatalf("held = %+v, want held", held)
	}
	released := sw.ReceiveMirrored(fixedTime.Add(time.Millisecond), replyPort, reply)
	soleDataEmission(t, released.Emissions, macH2)
	if len(released.Copies) != 1 || released.Copies[0].Hold != held.Hold {
		t.Fatalf("copies = %+v, want the released frame's one stamped %d", released.Copies, held.Hold)
	}
}

func TestReceiveMirroredProducesNoCopiesForItsOwnFrame(t *testing.T) {
	sw, frame, reply, replyPort := heldMirrorFixture(t, []traffic.Mirror{{Name: "span", SelectDstPorts: []string{"1/1/2"}, OutputPort: "mirror"}})
	held := sw.ReceiveMirrored(fixedTime, "1/1/1", frame)
	if held.Outcome != trace.Held || len(held.Copies) != 0 {
		t.Fatalf("held = %+v, want held without copies", held)
	}
	released := sw.Receive(fixedTime.Add(time.Millisecond), replyPort, reply)
	if got := dataCopies(released.Copies); len(got) != 0 {
		t.Fatalf("released copies = %+v, want none for a frame that arrived as a mirror copy", got)
	}
}

func TestHeldFrameReleaseSelectsVLANByIngressVLAN(t *testing.T) {
	sw, frame, reply, replyPort := heldMirrorFixture(t, []traffic.Mirror{{Name: "span", SelectDstPorts: []string{"1/1/2"}, SelectVLANs: []vlan.ID{10}, OutputPort: "mirror"}})
	held := sw.Receive(fixedTime, "1/1/1", frame)
	if held.Outcome != trace.Held {
		t.Fatalf("held = %+v, want held", held)
	}
	released := sw.Receive(fixedTime.Add(time.Millisecond), replyPort, reply)
	if got := dataCopies(released.Copies); len(got) != 1 || got[0].Hold != held.Hold {
		t.Fatalf("released copies = %+v, want the one a VLAN 10 selector takes of a frame that arrived in VLAN 10", got)
	}
}
