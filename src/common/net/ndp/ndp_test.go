package ndp_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/ndp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
)

func TestNeighborAdvertisementEncodePlacesEveryFieldAtItsOffset(t *testing.T) {
	t.Parallel()

	hdr := ip.Header{Src: seqAddr(0x10), Dst: seqAddr(0x20), HopLimit: 255, Protocol: 58, V6: &ip.V6{}}
	target := seqAddr(0x30)
	mac := seqMAC(0x40)

	wire, err := ndp.Encode(hdr, ndp.Message{
		Type:             ndp.NeighborAdvertisement,
		Target:           target,
		Router:           true,
		Solicited:        true,
		Override:         true,
		LinkLayerAddr:    mac,
		HasLinkLayerAddr: true,
	})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if len(wire) != 32 {
		t.Fatalf("len(Encode()) = %d, want 32", len(wire))
	}

	if wire[0] != 136 {
		t.Errorf("wire[0] = %d, want 136 (type)", wire[0])
	}
	if wire[1] != 0 {
		t.Errorf("wire[1] = %d, want 0 (code)", wire[1])
	}

	// ICMPv6 checksum (RFC 8200 section 8.1 pseudo-header, RFC 4443 section
	// 2.1 for the ICMPv6 message), hand-summed in one's complement (16-bit
	// words, end-around carry). Pseudo-header: source octets 0x10..0x1f,
	// destination octets 0x20..0x2f, upper-layer length 0x00000020 (32),
	// zero x3, next header 0x3a (58).
	//   0x1011 + 0x1213 = 0x2224
	//   0x2224 + 0x1415 = 0x3639
	//   0x3639 + 0x1617 = 0x4c50
	//   0x4c50 + 0x1819 = 0x6469
	//   0x6469 + 0x1a1b = 0x7e84
	//   0x7e84 + 0x1c1d = 0x9aa1
	//   0x9aa1 + 0x1e1f = 0xb8c0
	//   0xb8c0 + 0x2021 = 0xd8e1
	//   0xd8e1 + 0x2223 = 0xfb04
	//   0xfb04 + 0x2425 = 0x11f29
	//   0x11f29 + 0x2627 = 0x14550
	//   0x14550 + 0x2829 = 0x16d79
	//   0x16d79 + 0x2a2b = 0x197a4
	//   0x197a4 + 0x2c2d = 0x1c3d1
	//   0x1c3d1 + 0x2e2f = 0x1f200
	//   0x1f200 + 0x0000 = 0x1f200 (length high word)
	//   0x1f200 + 0x0020 = 0x1f220 (length low word: 32 octets)
	//   0x1f220 + 0x0000 = 0x1f220 (zero)
	//   0x1f220 + 0x003a = 0x1f25a (zero + next header 58)
	// Message with the checksum field zeroed: type/code 0x8800, flags/reserved
	// 0xe000 (R 0x80 | S 0x40 | O 0x20), reserved 0x0000, target octets
	// 0x30..0x3f, option header 0x0201 (type 2, length 1), link-layer address
	// octets 0x40..0x45.
	//   0x1f25a + 0x8800 = 0x27a5a
	//   0x27a5a + 0x0000 = 0x27a5a
	//   0x27a5a + 0xe000 = 0x35a5a
	//   0x35a5a + 0x0000 = 0x35a5a
	//   0x35a5a + 0x3031 = 0x38a8b
	//   0x38a8b + 0x3233 = 0x3bcbe
	//   0x3bcbe + 0x3435 = 0x3f0f3
	//   0x3f0f3 + 0x3637 = 0x4272a
	//   0x4272a + 0x3839 = 0x45f63
	//   0x45f63 + 0x3a3b = 0x4999e
	//   0x4999e + 0x3c3d = 0x4d5db
	//   0x4d5db + 0x3e3f = 0x5141a
	//   0x5141a + 0x0201 = 0x5161b
	//   0x5161b + 0x4041 = 0x5565c
	//   0x5565c + 0x4243 = 0x5989f
	//   0x5989f + 0x4445 = 0x5dce4
	// Fold: 0x5dce4 -> 0xdce4 + 0x5 = 0xdce9
	// One's complement: 0xffff - 0xdce9 = 0x2316
	wantChecksum := []byte{0x23, 0x16}
	if got := wire[2:4]; !bytes.Equal(got, wantChecksum) {
		t.Errorf("wire[2:4] = % x, want % x (checksum)", got, wantChecksum)
	}

	if wire[4] != 0xe0 {
		t.Errorf("wire[4] = %#x, want 0xe0 (R|S|O flags)", wire[4])
	}
	wantReserved := []byte{0, 0, 0}
	if got := wire[5:8]; !bytes.Equal(got, wantReserved) {
		t.Errorf("wire[5:8] = % x, want % x (reserved)", got, wantReserved)
	}

	wantTarget := target.As16()
	if got := wire[8:24]; !bytes.Equal(got, wantTarget[:]) {
		t.Errorf("wire[8:24] = % x, want % x (target)", got, wantTarget)
	}

	if wire[24] != 2 {
		t.Errorf("wire[24] = %d, want 2 (target link-layer address option)", wire[24])
	}
	if wire[25] != 1 {
		t.Errorf("wire[25] = %d, want 1 (option length)", wire[25])
	}
	if got := wire[26:32]; !bytes.Equal(got, mac[:]) {
		t.Errorf("wire[26:32] = % x, want % x (link-layer address)", got, mac[:])
	}
}

func TestNeighborSolicitationEncodePlacesEveryFieldAtItsOffset(t *testing.T) {
	t.Parallel()

	hdr := ip.Header{Src: seqAddr(0x50), Dst: seqAddr(0x60), HopLimit: 255, Protocol: 58, V6: &ip.V6{}}
	target := seqAddr(0x70)
	mac := seqMAC(0x80)

	wire, err := ndp.Encode(hdr, ndp.Message{
		Type:             ndp.NeighborSolicitation,
		Target:           target,
		LinkLayerAddr:    mac,
		HasLinkLayerAddr: true,
	})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if len(wire) != 32 {
		t.Fatalf("len(Encode()) = %d, want 32", len(wire))
	}

	if wire[0] != 135 {
		t.Errorf("wire[0] = %d, want 135 (type)", wire[0])
	}
	if wire[1] != 0 {
		t.Errorf("wire[1] = %d, want 0 (code)", wire[1])
	}

	// RFC 4861 section 4.3 gives the Neighbor Solicitation a four-octet
	// Reserved field where the Neighbor Advertisement carries R, S, and O
	// flags followed by a three-octet reserved field: the solicitation names
	// no flags octet at all.
	wantReserved := []byte{0, 0, 0, 0}
	if got := wire[4:8]; !bytes.Equal(got, wantReserved) {
		t.Errorf("wire[4:8] = % x, want % x (reserved)", got, wantReserved)
	}

	wantTarget := target.As16()
	if got := wire[8:24]; !bytes.Equal(got, wantTarget[:]) {
		t.Errorf("wire[8:24] = % x, want % x (target)", got, wantTarget)
	}

	if wire[24] != 1 {
		t.Errorf("wire[24] = %d, want 1 (source link-layer address option)", wire[24])
	}
	if wire[25] != 1 {
		t.Errorf("wire[25] = %d, want 1 (option length)", wire[25])
	}
	if got := wire[26:32]; !bytes.Equal(got, mac[:]) {
		t.Errorf("wire[26:32] = % x, want % x (link-layer address)", got, mac[:])
	}
}

func TestNDPRoundTrip(t *testing.T) {
	t.Parallel()

	hdr := ip.Header{Src: seqAddr(0x10), Dst: seqAddr(0x20), HopLimit: 255, Protocol: 58, V6: &ip.V6{}}
	tests := []struct {
		name string
		want ndp.Message
	}{
		{
			name: "solicitation without option",
			want: ndp.Message{Type: ndp.NeighborSolicitation, Target: seqAddr(0x30)},
		},
		{
			name: "solicitation with option",
			want: ndp.Message{
				Type:             ndp.NeighborSolicitation,
				Target:           seqAddr(0x30),
				LinkLayerAddr:    seqMAC(0x40),
				HasLinkLayerAddr: true,
			},
		},
		{
			name: "advertisement without option",
			want: ndp.Message{Type: ndp.NeighborAdvertisement, Target: seqAddr(0x30), Router: true, Solicited: true},
		},
		{
			name: "advertisement with option",
			want: ndp.Message{
				Type:             ndp.NeighborAdvertisement,
				Target:           seqAddr(0x30),
				Solicited:        true,
				Override:         true,
				LinkLayerAddr:    seqMAC(0x40),
				HasLinkLayerAddr: true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := ndp.Encode(hdr, tc.want)
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}

			got, err := ndp.Decode(hdr, wire)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("Decode(Encode()) = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestNDPDecodeRefuses(t *testing.T) {
	t.Parallel()

	hdr := ip.Header{Src: seqAddr(0x10), Dst: seqAddr(0x20), HopLimit: 255, Protocol: 58, V6: &ip.V6{}}
	hopLimit254 := ip.Header{Src: hdr.Src, Dst: hdr.Dst, HopLimit: 254, Protocol: 58, V6: &ip.V6{}}
	unspecifiedSrc := ip.Header{Src: netip.IPv6Unspecified(), Dst: hdr.Dst, HopLimit: 255, Protocol: 58, V6: &ip.V6{}}

	tests := []struct {
		name    string
		hdr     ip.Header
		payload []byte
	}{
		{
			name:    "payload shorter than 24 octets",
			hdr:     hdr,
			payload: finalizeChecksum(hdr, rawSolicitation(seqAddr(0x30), nil))[:23],
		},
		{
			name:    "hop limit other than 255",
			hdr:     hopLimit254,
			payload: finalizeChecksum(hopLimit254, rawSolicitation(seqAddr(0x30), nil)),
		},
		{
			name: "code other than 0",
			hdr:  hdr,
			payload: func() []byte {
				wire := rawSolicitation(seqAddr(0x30), nil)
				wire[1] = 7
				return finalizeChecksum(hdr, wire)
			}(),
		},
		{
			name:    "multicast target",
			hdr:     hdr,
			payload: finalizeChecksum(hdr, rawAdvertisement(0, netip.MustParseAddr("ff02::1"), nil)),
		},
		{
			name: "option length zero",
			hdr:  hdr,
			payload: finalizeChecksum(hdr, rawSolicitation(seqAddr(0x30), []byte{
				1, 0, 0, 0, 0, 0, 0, 0, // type 1 (source link-layer), length 0
			})),
		},
		{
			name:    "source link-layer address option on a solicitation from the unspecified address",
			hdr:     unspecifiedSrc,
			payload: finalizeChecksum(unspecifiedSrc, rawSolicitation(seqAddr(0x30), []byte{1, 1, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff})),
		},
		{
			name: "matching link-layer address option whose declared length is not one 8-octet unit",
			hdr:  hdr,
			payload: finalizeChecksum(hdr, rawAdvertisement(0, seqAddr(0x30), append(
				[]byte{2, 2}, // type 2 (target link-layer), length 2 (16 octets)
				make([]byte, 14)...,
			))),
		},
		{
			name: "bad checksum",
			hdr:  hdr,
			payload: func() []byte {
				wire := finalizeChecksum(hdr, rawSolicitation(seqAddr(0x30), nil))
				wire[2] ^= 0xff
				return wire
			}(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ndp.Decode(tc.hdr, tc.payload); !errors.Is(err, ndp.ErrMalformed) {
				t.Errorf("Decode() error = %v, want ErrMalformed", err)
			}
		})
	}
}

// TestNeighborAdvertisementDecodeSkipsUnrelatedOptionToFindTargetLinkLayerAddress
// covers a message whose first option is not the Target Link-Layer Address
// option: Decode must walk past it by its own declared length and find the
// matching option that follows, rather than reading the first option's
// value octets as the address.
func TestNeighborAdvertisementDecodeSkipsUnrelatedOptionToFindTargetLinkLayerAddress(t *testing.T) {
	t.Parallel()

	hdr := ip.Header{Src: seqAddr(0x10), Dst: seqAddr(0x20), HopLimit: 255, Protocol: 58, V6: &ip.V6{}}
	mac := netaddr.MAC{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	option := append(
		[]byte{0x0e, 1, 0, 0, 0, 0, 0, 0}, // unrelated option: type 14, length 1
		append([]byte{2, 1}, mac[:]...)...,
	)
	payload := finalizeChecksum(hdr, rawAdvertisement(0, seqAddr(0x30), option))

	got, err := ndp.Decode(hdr, payload)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !got.HasLinkLayerAddr {
		t.Fatalf("Decode() HasLinkLayerAddr = false, want true")
	}
	if got.LinkLayerAddr != mac {
		t.Errorf("Decode() LinkLayerAddr = %v, want %v", got.LinkLayerAddr, mac)
	}
}

// TestNeighborAdvertisementDecodeWithOnlyUnrelatedOptionHasNoLinkLayerAddr
// covers an option chain that never carries the Target Link-Layer Address
// option: Decode must report HasLinkLayerAddr false rather than an error or
// a MAC built from the unrelated option's value octets.
func TestNeighborAdvertisementDecodeWithOnlyUnrelatedOptionHasNoLinkLayerAddr(t *testing.T) {
	t.Parallel()

	hdr := ip.Header{Src: seqAddr(0x10), Dst: seqAddr(0x20), HopLimit: 255, Protocol: 58, V6: &ip.V6{}}
	option := []byte{0x0e, 1, 0, 0, 0, 0, 0, 0} // unrelated option: type 14, length 1
	payload := finalizeChecksum(hdr, rawAdvertisement(0, seqAddr(0x30), option))

	got, err := ndp.Decode(hdr, payload)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.HasLinkLayerAddr {
		t.Errorf("Decode() HasLinkLayerAddr = true, want false")
	}
}

// TestNDPDecodeReadsTheLiteralVector pins Decode's offsets against a literal
// wire vector rather than one produced by Encode, so this test cannot pass
// merely because both sides of a round trip share the same bug.
func TestNDPDecodeReadsTheLiteralVector(t *testing.T) {
	t.Parallel()

	hdr := ip.Header{Src: seqAddr(0x10), Dst: seqAddr(0x20), HopLimit: 255, Protocol: 58, V6: &ip.V6{}}
	target := seqAddr(0x30)
	mac := seqMAC(0x40)
	targetOctets := target.As16()

	wire := []byte{
		136, 0, 0, 0, // type: Neighbor Advertisement, code: 0, checksum: filled below
		0xe0, 0, 0, 0, // flags R|S|O, reserved
	}
	wire = append(wire, targetOctets[:]...)
	wire = append(wire, 2, 1) // option type 2 (target link-layer), length 1
	wire = append(wire, mac[:]...)
	wire = finalizeChecksum(hdr, wire)

	want := ndp.Message{
		Type:             ndp.NeighborAdvertisement,
		Target:           target,
		Router:           true,
		Solicited:        true,
		Override:         true,
		LinkLayerAddr:    mac,
		HasLinkLayerAddr: true,
	}

	got, err := ndp.Decode(hdr, wire)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got != want {
		t.Errorf("Decode() = %+v, want %+v", got, want)
	}
}

func TestNDPEncodeRefuses(t *testing.T) {
	t.Parallel()

	hdr := ip.Header{Src: seqAddr(0x10), Dst: seqAddr(0x20), HopLimit: 255, Protocol: 58, V6: &ip.V6{}}
	unspecifiedSrc := ip.Header{Src: netip.IPv6Unspecified(), Dst: hdr.Dst, HopLimit: 255, Protocol: 58, V6: &ip.V6{}}
	v4Header := ip.Header{Src: netip.MustParseAddr("10.0.0.1"), Dst: netip.MustParseAddr("10.0.0.2"), HopLimit: 255, Protocol: 58, V4: &ip.V4{}}
	v4MappedHeader := ip.Header{
		Src: netip.MustParseAddr("::ffff:10.0.0.1"), Dst: netip.MustParseAddr("::ffff:10.0.0.2"),
		HopLimit: 255, Protocol: 58, V6: &ip.V6{},
	}

	tests := []struct {
		name string
		hdr  ip.Header
		m    ndp.Message
		want error
	}{
		{
			name: "unsupported message type",
			hdr:  hdr,
			m:    ndp.Message{Type: ndp.Type(0), Target: seqAddr(0x30)},
			want: ndp.ErrUnsupported,
		},
		{
			name: "solicitation from the unspecified address with a source link-layer address option",
			hdr:  unspecifiedSrc,
			m: ndp.Message{
				Type: ndp.NeighborSolicitation, Target: seqAddr(0x30),
				LinkLayerAddr: seqMAC(0x40), HasLinkLayerAddr: true,
			},
			want: ndp.ErrMalformed,
		},
		{
			name: "solicitation with Solicited set",
			hdr:  hdr,
			m:    ndp.Message{Type: ndp.NeighborSolicitation, Target: seqAddr(0x30), Solicited: true},
			want: ndp.ErrMalformed,
		},
		{
			name: "solicitation with Router set",
			hdr:  hdr,
			m:    ndp.Message{Type: ndp.NeighborSolicitation, Target: seqAddr(0x30), Router: true},
			want: ndp.ErrMalformed,
		},
		{
			name: "solicitation with Override set",
			hdr:  hdr,
			m:    ndp.Message{Type: ndp.NeighborSolicitation, Target: seqAddr(0x30), Override: true},
			want: ndp.ErrMalformed,
		},
		{
			name: "IPv4 header",
			hdr:  v4Header,
			m:    ndp.Message{Type: ndp.NeighborSolicitation, Target: seqAddr(0x30)},
			want: ndp.ErrMalformed,
		},
		{
			name: "IPv4-mapped addresses on an IPv6 header",
			hdr:  v4MappedHeader,
			m:    ndp.Message{Type: ndp.NeighborSolicitation, Target: seqAddr(0x30)},
			want: ndp.ErrMalformed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ndp.Encode(tc.hdr, tc.m); !errors.Is(err, tc.want) {
				t.Errorf("Encode() error = %v, want wrapping %v", err, tc.want)
			}
		})
	}
}

func TestNDPDecodeRefusesNonIPv6Header(t *testing.T) {
	t.Parallel()

	hdr := ip.Header{Src: seqAddr(0x10), Dst: seqAddr(0x20), HopLimit: 255, Protocol: 58, V6: &ip.V6{}}
	v4Header := ip.Header{Src: netip.MustParseAddr("10.0.0.1"), Dst: netip.MustParseAddr("10.0.0.2"), HopLimit: 255, Protocol: 58, V4: &ip.V4{}}
	v4MappedHeader := ip.Header{
		Src: netip.MustParseAddr("::ffff:10.0.0.1"), Dst: netip.MustParseAddr("::ffff:10.0.0.2"),
		HopLimit: 255, Protocol: 58, V6: &ip.V6{},
	}
	payload := finalizeChecksum(hdr, rawSolicitation(seqAddr(0x30), nil))

	tests := []struct {
		name string
		hdr  ip.Header
	}{
		{name: "IPv4 header", hdr: v4Header},
		{name: "IPv4-mapped addresses on an IPv6 header", hdr: v4MappedHeader},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ndp.Decode(tc.hdr, payload); !errors.Is(err, ndp.ErrMalformed) {
				t.Errorf("Decode() error = %v, want ErrMalformed", err)
			}
		})
	}
}

func rawSolicitation(target netip.Addr, option []byte) []byte {
	wire := make([]byte, 24+len(option))
	wire[0] = 135
	t := target.As16()
	copy(wire[8:24], t[:])
	copy(wire[24:], option)
	return wire
}

func rawAdvertisement(flags byte, target netip.Addr, option []byte) []byte {
	wire := make([]byte, 24+len(option))
	wire[0] = 136
	wire[4] = flags
	t := target.As16()
	copy(wire[8:24], t[:])
	copy(wire[24:], option)
	return wire
}

// finalizeChecksum writes wire's ICMPv6 checksum using an implementation kept
// independent of the package under test, so a decode refusal test exercises
// the field it names rather than an incidentally broken checksum.
func finalizeChecksum(hdr ip.Header, wire []byte) []byte {
	binary.BigEndian.PutUint16(wire[2:4], 0)
	binary.BigEndian.PutUint16(wire[2:4], icmpv6Checksum(hdr, wire))
	return wire
}

func icmpv6Checksum(hdr ip.Header, payload []byte) uint16 {
	pseudo := make([]byte, 40+len(payload))
	src := hdr.Src.As16()
	dst := hdr.Dst.As16()
	copy(pseudo[0:16], src[:])
	copy(pseudo[16:32], dst[:])
	binary.BigEndian.PutUint32(pseudo[32:36], uint32(len(payload)))
	pseudo[39] = 58
	copy(pseudo[40:], payload)
	return internetChecksum(pseudo)
}

func internetChecksum(b []byte) uint16 {
	var sum uint32
	for len(b) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(b[:2]))
		b = b[2:]
	}
	if len(b) == 1 {
		sum += uint32(b[0]) << 8
	}
	for sum > math.MaxUint16 {
		sum = sum&math.MaxUint16 + sum>>16
	}
	return ^uint16(sum)
}

// seqAddr builds an IPv6 address whose sixteen octets increase from start,
// so a field built from it cannot be mistaken for a field built from a
// different seqAddr value even under a single-octet transposition.
func seqAddr(start byte) netip.Addr {
	var b [16]byte
	for i := range b {
		b[i] = start + byte(i)
	}
	return netip.AddrFrom16(b)
}

// seqMAC builds a MAC address whose six octets increase from start.
func seqMAC(start byte) netaddr.MAC {
	var m netaddr.MAC
	for i := range m {
		m[i] = start + byte(i)
	}
	return m
}
