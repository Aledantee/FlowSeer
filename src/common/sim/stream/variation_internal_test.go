package stream

import (
	"bytes"
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/udp"
)

func internalVariationSpec(frame ethernet.Frame, count int, variations ...Variation) Spec {
	return Spec{
		Frame: frame, Rate: Rate{FramesPerSecond: 1}, Count: count,
		Variations: variations,
	}
}

func internalVariationSource(t *testing.T, spec Spec) Source {
	t.Helper()
	source, err := spec.Source()
	if err != nil {
		t.Fatalf("spec.Source(): %v", err)
	}
	return source
}

func internalUDPFrame(t *testing.T) ethernet.Frame {
	t.Helper()
	h := ip.Header{
		Src: netip.MustParseAddr("192.0.2.1"), Dst: netip.MustParseAddr("192.0.2.2"),
		HopLimit: 64, Protocol: 17, V4: &ip.V4{},
	}
	datagram, err := udp.Encode(udp.Header{SrcPort: 5000, DstPort: 1000}, []byte("udp-payload"), h.Src, h.Dst)
	if err != nil {
		t.Fatalf("udp.Encode: %v", err)
	}
	payload, err := h.Encode(datagram)
	if err != nil {
		t.Fatalf("ip.Encode: %v", err)
	}
	return ethernet.Frame{
		Src: netaddr.MAC{0x02, 0, 0, 0, 0, 1}, Dst: netaddr.MAC{0x02, 0, 0, 0, 0, 2},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   payload,
	}
}

func TestDrawVariationDeterminism(t *testing.T) {
	spec := internalVariationSpec(ethernet.Frame{Dst: netaddr.MAC{0x02}}, 12,
		MACVariation{Field: MACDestination, Count: 251, Draw: true})
	spec.Seed = 42
	left := internalVariationSource(t, spec)
	right := internalVariationSource(t, spec)
	rng := newSplitMix64(spec.Seed)
	for n := 0; n < spec.Count; n++ {
		_, a, okA := left.Next()
		_, b, okB := right.Next()
		if !okA || !okB {
			t.Fatalf("frame %d exhausted: left %t, right %t", n, okA, okB)
		}
		encodedA, err := a.Encode()
		if err != nil {
			t.Fatalf("Encode(left): %v", err)
		}
		encodedB, err := b.Encode()
		if err != nil {
			t.Fatalf("Encode(right): %v", err)
		}
		if !bytes.Equal(encodedA, encodedB) {
			t.Errorf("frame %d encoded bytes differ", n)
		}
		want := netaddr.MAC{0x02, 0, 0, 0, 0, byte(rng.Next() % 251)}
		if a.Dst != want {
			t.Errorf("frame %d destination = %s, want %s", n, a.Dst, want)
		}
	}
	source := internalVariationSource(t, spec)
	for range 3 {
		source.Next()
	}
	clone := source.Clone()
	_, a, _ := source.Next()
	_, b, _ := clone.Next()
	if a.Dst != b.Dst {
		t.Errorf("clone draw destination = %s, want %s", b.Dst, a.Dst)
	}
}

func TestUDPPortVariationDrawConsumesInvalidFrame(t *testing.T) {
	v := UDPPortVariation{Dst: true, Count: 7, Draw: true}
	rng := newSplitMix64(42)
	want := newSplitMix64(42)
	v.apply(0, ethernet.Frame{}, &rng)
	want.Next()
	if got, expected := rng.Next(), want.Next(); got != expected {
		t.Errorf("next draw = 0x%x, want 0x%x after invalid frame", got, expected)
	}
}

func TestUDPPortVariationApplyRejectsShortUDPDatagram(t *testing.T) {
	frame := internalUDPFrame(t)
	header, datagram, err := ip.Decode(frame.Payload)
	if err != nil {
		t.Fatal(err)
	}
	frame.Payload, err = header.Encode(append(datagram, 0xa5, 0x5a))
	if err != nil {
		t.Fatal(err)
	}
	got := (UDPPortVariation{Dst: true, Step: 1, Count: 2}).apply(1, frame, nil)
	if !bytes.Equal(got.Payload, frame.Payload) {
		t.Fatal("apply changed short UDP datagram")
	}
}
