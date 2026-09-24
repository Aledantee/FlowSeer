package stream_test

import (
	"bytes"
	"net/netip"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/udp"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/stream"
)

func variationSpec(frame ethernet.Frame, count int, variations ...stream.Variation) stream.Spec {
	return stream.Spec{
		Frame: frame, Rate: stream.Rate{FramesPerSecond: 1}, Count: count,
		Variations: variations,
	}
}

func variationSource(t *testing.T, spec stream.Spec) stream.Source {
	t.Helper()
	source, err := spec.Source()
	if err != nil {
		t.Fatalf("Source() error = %v", err)
	}
	return source
}

func TestMACVariationStep(t *testing.T) {
	base := netaddr.MAC{0x02}
	source := variationSource(t, variationSpec(ethernet.Frame{Dst: base}, 257,
		stream.MACVariation{Field: stream.MACDestination, Step: 1, Count: 256}))
	seen := make(map[netaddr.MAC]bool)
	for n := 0; n < 257; n++ {
		_, frame, ok := source.Next()
		if !ok {
			t.Fatalf("Next(%d) exhausted", n)
		}
		if n < 256 {
			if seen[frame.Dst] {
				t.Errorf("frame %d destination = %s, want distinct address", n, frame.Dst)
			}
			seen[frame.Dst] = true
		}
		want := netaddr.MAC{0x02, 0, 0, 0, 0, byte(n % 256)}
		if frame.Dst != want {
			t.Errorf("frame %d destination = %s, want %s", n, frame.Dst, want)
		}
	}
	source = variationSource(t, variationSpec(ethernet.Frame{}, 2,
		stream.MACVariation{Field: stream.MACSource, Step: -1, Count: 2}))
	source.Next()
	_, frame, ok := source.Next()
	if !ok || frame.Src != (netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}) {
		t.Errorf("negative step source = (%s, %t), want (ff:ff:ff:ff:ff:ff, true)", frame.Src, ok)
	}
}

func TestVariationsApplyInOrder(t *testing.T) {
	source := variationSource(t, variationSpec(ethernet.Frame{}, 1,
		stream.SizeVariation{Sizes: []int{64}},
		stream.SizeVariation{Sizes: []int{128}}))
	_, frame, ok := source.Next()
	if !ok || len(frame.Payload)+18 != 128 {
		t.Errorf("frame size = (%d, %t), want (128, true)", len(frame.Payload)+18, ok)
	}
}

func TestDrawVariationDeterminism(t *testing.T) {
	spec := variationSpec(ethernet.Frame{Dst: netaddr.MAC{0x02}}, 12,
		stream.MACVariation{Field: stream.MACDestination, Count: 251, Draw: true})
	spec.Seed = 42
	left := variationSource(t, spec)
	right := variationSource(t, spec)
	rng := stream.NewSplitMix64(spec.Seed)
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
	source := variationSource(t, spec)
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

func TestSizeVariation(t *testing.T) {
	sizes := []int{64, 128, 256, 512, 1024, 1280, 1518}
	source := variationSource(t, variationSpec(ethernet.Frame{Payload: []byte{1, 2, 3}}, len(sizes),
		stream.SizeVariation{Sizes: sizes}))
	for n, want := range sizes {
		_, frame, ok := source.Next()
		if !ok {
			t.Fatalf("frame %d exhausted", n)
		}
		encoded, err := frame.Encode()
		if err != nil {
			t.Fatalf("Encode(frame %d): %v", n, err)
		}
		if got := len(encoded) + 4; got != want {
			t.Errorf("frame %d size = %d, want %d", n, got, want)
		}
		if !bytes.Equal(frame.Payload[:3], []byte{1, 2, 3}) {
			t.Errorf("frame %d payload prefix = %v, want [1 2 3]", n, frame.Payload[:3])
		}
	}
}

func TestSizeVariationTaggedFrame(t *testing.T) {
	frame := ethernet.Frame{Tags: []vlan.Tag{{VID: 10}}, Payload: make([]byte, 46)}
	source := variationSource(t, variationSpec(frame, 1, stream.SizeVariation{Sizes: []int{64}}))
	_, got, ok := source.Next()
	if !ok {
		t.Fatal("Next() exhausted, want tagged frame")
	}
	if len(got.Payload) != 42 {
		t.Errorf("tagged payload length = %d, want 42", len(got.Payload))
	}
	encoded, err := got.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(encoded)+4 != 64 {
		t.Errorf("tagged frame size = %d, want 64", len(encoded)+4)
	}
}

func udpFrame(t *testing.T) ethernet.Frame {
	t.Helper()
	h := ip.Header{
		Src: netip.MustParseAddr("192.0.2.1"), Dst: netip.MustParseAddr("192.0.2.2"),
		HopLimit: 64, Protocol: 17, V4: &ip.V4{},
	}
	datagram, err := udp.Encode(udp.Header{SrcPort: 5000, DstPort: 1000}, []byte{1, 2, 3}, h.Src, h.Dst)
	if err != nil {
		t.Fatalf("udp.Encode: %v", err)
	}
	payload, err := h.Encode(datagram)
	if err != nil {
		t.Fatalf("ip.Encode: %v", err)
	}
	return ethernet.Frame{EtherType: ethernet.EtherTypeIPv4, Payload: payload}
}

func TestUDPPortVariationChecksums(t *testing.T) {
	source := variationSource(t, variationSpec(udpFrame(t), 4,
		stream.UDPPortVariation{Dst: true, Step: 1, Count: 4}))
	var previousChecksum uint16
	for n := 0; n < 4; n++ {
		_, frame, ok := source.Next()
		if !ok {
			t.Fatalf("frame %d exhausted", n)
		}
		ipHeader, datagram, err := ip.Decode(frame.Payload)
		if err != nil {
			t.Fatalf("ip.Decode(frame %d): %v", n, err)
		}
		udpHeader, _, err := udp.Decode(datagram)
		if err != nil {
			t.Fatalf("udp.Decode(frame %d): %v", n, err)
		}
		if !udp.Verify(datagram, ipHeader.Src, ipHeader.Dst) {
			t.Errorf("frame %d UDP checksum did not verify", n)
		}
		if udpHeader.DstPort != uint16(1000+n) {
			t.Errorf("frame %d destination port = %d, want %d", n, udpHeader.DstPort, 1000+n)
		}
		if n > 0 && udpHeader.Checksum == previousChecksum {
			t.Errorf("frame %d checksum = 0x%04x, want a changed checksum", n, udpHeader.Checksum)
		}
		previousChecksum = udpHeader.Checksum
	}
}

func TestUDPPortVariationPreservesEthernetPadding(t *testing.T) {
	frame := udpFrame(t)
	packetLen := len(frame.Payload)
	padding := bytes.Repeat([]byte{0xa5}, 46-packetLen)
	frame.Payload = append(frame.Payload, padding...)
	source := variationSource(t, variationSpec(frame, 1,
		stream.UDPPortVariation{Dst: true, Step: 1, Count: 2}))
	_, got, ok := source.Next()
	if !ok {
		t.Fatal("Next() exhausted")
	}
	if len(got.Payload) != 46 || !bytes.Equal(got.Payload[packetLen:], padding) {
		t.Errorf("payload length and padding = (%d, %x), want (46, %x)", len(got.Payload), got.Payload[packetLen:], padding)
	}
	encoded, err := got.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(encoded) != 60 {
		t.Errorf("encoded octets = %d, want 60", len(encoded))
	}
}

func TestUDPPortVariationRejectsShortUDPDatagram(t *testing.T) {
	frame := udpFrame(t)
	ipHeader, datagram, err := ip.Decode(frame.Payload)
	if err != nil {
		t.Fatalf("ip.Decode: %v", err)
	}
	frame.Payload, err = ipHeader.Encode(append(datagram, 0xa5, 0x5a))
	if err != nil {
		t.Fatalf("ip.Encode: %v", err)
	}

	spec := variationSpec(frame, 2, stream.UDPPortVariation{Dst: true, Step: 1, Count: 2})
	if source, err := spec.Source(); err == nil || !strings.Contains(err.Error(), "UDP length") {
		t.Errorf("Source() = (%v, %v), want UDP length refusal", source, err)
	}
	got := spec.Variations[0].Apply(1, frame, nil)
	if !bytes.Equal(got.Payload, frame.Payload) {
		t.Errorf("Apply() changed a short UDP datagram: got %x, want %x", got.Payload, frame.Payload)
	}
}

func TestUDPPortVariationPreservesBytesAfterIPv6Packet(t *testing.T) {
	ipHeader := ip.Header{
		Src: netip.MustParseAddr("2001:db8::1"), Dst: netip.MustParseAddr("2001:db8::2"),
		HopLimit: 64, Protocol: 17, V6: &ip.V6{},
	}
	datagram, err := udp.Encode(udp.Header{SrcPort: 5000, DstPort: 1000}, []byte{1, 2, 3}, ipHeader.Src, ipHeader.Dst)
	if err != nil {
		t.Fatalf("udp.Encode: %v", err)
	}
	packet, err := ipHeader.Encode(datagram)
	if err != nil {
		t.Fatalf("ip.Encode: %v", err)
	}
	extra := []byte{0xa5, 0x5a}
	frame := ethernet.Frame{EtherType: ethernet.EtherTypeIPv6, Payload: append(packet, extra...)}
	source := variationSource(t, variationSpec(frame, 2,
		stream.UDPPortVariation{Dst: true, Step: 1, Count: 2}))
	source.Next()
	_, got, ok := source.Next()
	if !ok {
		t.Fatal("Next() exhausted")
	}
	if len(got.Payload) < len(packet) {
		t.Fatalf("IPv6 payload length = %d, want at least %d", len(got.Payload), len(packet))
	}
	if len(got.Payload) != len(frame.Payload) || !bytes.Equal(got.Payload[len(packet):], extra) {
		t.Errorf("IPv6 payload = (%d octets, trailing %x), want (%d octets, %x)",
			len(got.Payload), got.Payload[len(packet):], len(frame.Payload), extra)
	}
	_, gotDatagram, err := ip.Decode(got.Payload)
	if err != nil {
		t.Fatalf("ip.Decode: %v", err)
	}
	gotUDP, _, err := udp.Decode(gotDatagram)
	if err != nil {
		t.Fatalf("udp.Decode: %v", err)
	}
	if gotUDP.DstPort != 1001 {
		t.Errorf("destination port = %d, want 1001", gotUDP.DstPort)
	}
}

func TestUDPPortVariationPreservesIPv4Options(t *testing.T) {
	options := []byte{1, 1, 1, 0}
	ipHeader := ip.Header{
		Src: netip.MustParseAddr("192.0.2.1"), Dst: netip.MustParseAddr("192.0.2.2"),
		HopLimit: 64, Protocol: 17, V4: &ip.V4{Options: options},
	}
	datagram, err := udp.Encode(udp.Header{SrcPort: 5000, DstPort: 1000}, []byte{1, 2, 3}, ipHeader.Src, ipHeader.Dst)
	if err != nil {
		t.Fatalf("udp.Encode: %v", err)
	}
	packet, err := ipHeader.Encode(datagram)
	if err != nil {
		t.Fatalf("ip.Encode: %v", err)
	}
	frame := ethernet.Frame{EtherType: ethernet.EtherTypeIPv4, Payload: packet}
	source := variationSource(t, variationSpec(frame, 2,
		stream.UDPPortVariation{Dst: true, Step: 1, Count: 2}))
	source.Next()
	_, got, ok := source.Next()
	if !ok {
		t.Fatal("Next() exhausted")
	}
	if len(got.Payload) != len(packet) {
		t.Errorf("IPv4 packet length = %d, want %d", len(got.Payload), len(packet))
	}
	gotIP, gotDatagram, err := ip.Decode(got.Payload)
	if err != nil {
		t.Fatalf("ip.Decode: %v", err)
	}
	if !bytes.Equal(gotIP.V4.Options, options) {
		t.Errorf("IPv4 options = %x, want %x", gotIP.V4.Options, options)
	}
	gotUDP, _, err := udp.Decode(gotDatagram)
	if err != nil {
		t.Fatalf("udp.Decode: %v", err)
	}
	checksumOK := udp.Verify(gotDatagram, gotIP.Src, gotIP.Dst)
	if gotUDP.DstPort != 1001 || !checksumOK {
		t.Errorf("UDP destination and checksum = (%d, %t), want (1001, true)",
			gotUDP.DstPort, checksumOK)
	}
}

func TestSizeThenUDPPortVariationPreservesSizes(t *testing.T) {
	frame := udpFrame(t)
	source := variationSource(t, variationSpec(frame, 2,
		stream.SizeVariation{Sizes: []int{64, 1518}},
		stream.UDPPortVariation{Dst: true, Step: 1, Count: 2}))
	for n, want := range []int{64, 1518} {
		_, got, ok := source.Next()
		if !ok {
			t.Fatalf("Next(%d) exhausted", n)
		}
		encoded, err := got.Encode()
		if err != nil {
			t.Fatalf("Encode(%d): %v", n, err)
		}
		if len(encoded)+4 != want {
			t.Errorf("frame %d size = %d, want %d", n, len(encoded)+4, want)
		}
	}
}

func TestUDPPortVariationDrawConsumesInvalidFrame(t *testing.T) {
	v := stream.UDPPortVariation{Dst: true, Count: 7, Draw: true}
	rng := stream.NewSplitMix64(42)
	want := stream.NewSplitMix64(42)
	v.Apply(0, ethernet.Frame{}, &rng)
	want.Next()
	if got, expected := rng.Next(), want.Next(); got != expected {
		t.Errorf("next draw = 0x%x, want 0x%x after invalid frame", got, expected)
	}
}

func TestUDPPortVariationRejectsUnencodableIPv6(t *testing.T) {
	h := ip.Header{
		Src: netip.MustParseAddr("2001:db8::1"), Dst: netip.MustParseAddr("2001:db8::2"),
		HopLimit: 64, Protocol: 17, V6: &ip.V6{},
	}
	datagram, err := udp.Encode(udp.Header{SrcPort: 5000, DstPort: 1000}, []byte{1}, h.Src, h.Dst)
	if err != nil {
		t.Fatalf("udp.Encode: %v", err)
	}
	payload, err := h.Encode(datagram)
	if err != nil {
		t.Fatalf("ip.Encode: %v", err)
	}
	copy(payload[8:24], netip.MustParseAddr("::ffff:192.0.2.1").AsSlice())
	copy(payload[24:40], netip.MustParseAddr("::ffff:192.0.2.2").AsSlice())
	spec := variationSpec(ethernet.Frame{EtherType: ethernet.EtherTypeIPv6, Payload: payload}, 1,
		stream.UDPPortVariation{Dst: true, Count: 2})
	if err := spec.Validate(); err == nil {
		t.Error("Validate() error = nil, want refusal")
	}
	if source, err := spec.Source(); err == nil {
		t.Errorf("Source() = %v, nil, want refusal", source)
	}
}

func TestUDPPortVariationRejectsTruncatedIPPacket(t *testing.T) {
	spec := variationSpec(udpFrame(t), 2,
		stream.SizeVariation{Sizes: []int{64, 40}},
		stream.UDPPortVariation{Dst: true, Count: 2})
	if err := spec.Validate(); err == nil {
		t.Error("Validate() error = nil, want refusal")
	}
	if source, err := spec.Source(); err == nil {
		t.Errorf("Source() = %v, nil, want refusal", source)
	}
}

type corruptIPVariation struct{}

func (corruptIPVariation) Validate() error { return nil }

func (corruptIPVariation) Apply(_ int, frame ethernet.Frame, _ *stream.SplitMix64) ethernet.Frame {
	frame.Payload = []byte{1}
	return frame
}

func TestUDPPortVariationRejectsEarlierCustomVariation(t *testing.T) {
	spec := variationSpec(udpFrame(t), 1,
		corruptIPVariation{}, stream.UDPPortVariation{Dst: true, Count: 2})
	if err := spec.Validate(); err == nil {
		t.Error("Validate() error = nil, want refusal")
	}
	if source, err := spec.Source(); err == nil {
		t.Errorf("Source() = %v, nil, want refusal", source)
	}
}

func TestUDPPortVariationRejectsIPv4Fragments(t *testing.T) {
	base := udpFrame(t)
	for _, tc := range []struct {
		name   string
		flags  uint8
		offset uint16
	}{
		{name: "non-first fragment", offset: 1},
		{name: "more fragments", flags: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, datagram, err := ip.Decode(base.Payload)
			if err != nil {
				t.Fatalf("ip.Decode: %v", err)
			}
			h.V4.Flags = tc.flags
			h.V4.FragmentOffset = tc.offset
			payload, err := h.Encode(datagram)
			if err != nil {
				t.Fatalf("ip.Encode: %v", err)
			}
			spec := variationSpec(ethernet.Frame{EtherType: ethernet.EtherTypeIPv4, Payload: payload}, 1,
				stream.UDPPortVariation{Dst: true, Count: 2})
			if err := spec.Validate(); err == nil {
				t.Error("Validate() error = nil, want refusal")
			}
		})
	}
}

func TestBitRateRejectsSizeVariation(t *testing.T) {
	spec := variationSpec(ethernet.Frame{Payload: make([]byte, 46)}, 2,
		stream.SizeVariation{Sizes: []int{64, 1518}})
	spec.Rate = stream.Rate{BitsPerSecond: 1_000_000_000}
	if err := spec.Validate(); err == nil {
		t.Error("Validate() error = nil, want refusal")
	}
	if source, err := spec.Source(); err == nil {
		t.Errorf("Source() = %v, nil, want refusal", source)
	}
}

func TestVariationValidationRefusesInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		spec stream.Spec
	}{
		{"bad MAC field", variationSpec(ethernet.Frame{}, 1, stream.MACVariation{Field: "other", Count: 2})},
		{"empty sizes", variationSpec(ethernet.Frame{}, 1, stream.SizeVariation{})},
		{"tagged size too small", variationSpec(ethernet.Frame{Tags: []vlan.Tag{{VID: 1}}}, 1, stream.SizeVariation{Sizes: []int{20}})},
		{"non UDP template", variationSpec(ethernet.Frame{EtherType: ethernet.EtherTypeIPv4, Payload: []byte{1, 2, 3}}, 1, stream.UDPPortVariation{Dst: true, Count: 2})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.spec.Validate(); err == nil {
				t.Error("Validate() error = nil, want refusal")
			}
			if source, err := tc.spec.Source(); err == nil {
				t.Errorf("Source() = %v, nil, want refusal", source)
			}
		})
	}
}

func TestSpecVariationSliceCopy(t *testing.T) {
	spec := variationSpec(ethernet.Frame{}, 1, stream.MACVariation{Field: stream.MACDestination, Count: 1})
	clone := spec.Clone()
	normalized, err := spec.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	spec.Variations[0] = stream.SizeVariation{Sizes: []int{64}}
	if _, ok := clone.Variations[0].(stream.MACVariation); !ok {
		t.Errorf("Clone variation = %T, want MACVariation", clone.Variations[0])
	}
	if _, ok := normalized.Variations[0].(stream.MACVariation); !ok {
		t.Errorf("Normalize variation = %T, want MACVariation", normalized.Variations[0])
	}
}
