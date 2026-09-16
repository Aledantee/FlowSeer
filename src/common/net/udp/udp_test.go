package udp_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/udp"
)

// Both fixtures carry a minimal PTR query for _services._dns-sd._udp.local,
// ports 5353 to 5353, length 54. They were computed independently of this
// package's code, from the RFC 768 checksum algorithm, so a byte comparison
// against them exercises the wire format rather than a round trip through
// this package's own encoder and decoder (see
// docs/solutions/conventions/a-codec-round-trip-cannot-locate-a-field-on-the-wire.md).
const (
	ipv4Fixture = "14e914e90036aa94000000000001000000000000095f7365727669636573075f646e732d7364045f756470056c6f63616c00000c0001"
	ipv6Fixture = "14e914e90036a117000000000001000000000000095f7365727669636573075f646e732d7364045f756470056c6f63616c00000c0001"
)

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex.DecodeString() error = %v", err)
	}
	return b
}

// TestDecodeFixtures decodes both mDNS fixtures and checks every field
// against literals independently derived from the specification, not from
// this package's own encoder.
func TestDecodeFixtures(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		checksum uint16
	}{
		{name: "ipv4", fixture: ipv4Fixture, checksum: 0xaa94},
		{name: "ipv6", fixture: ipv6Fixture, checksum: 0xa117},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wire := mustDecodeHex(t, tc.fixture)

			h, payload, err := udp.Decode(wire)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if h.SrcPort != 5353 {
				t.Errorf("SrcPort = %d, want 5353", h.SrcPort)
			}
			if h.DstPort != 5353 {
				t.Errorf("DstPort = %d, want 5353", h.DstPort)
			}
			if h.Length != 54 {
				t.Errorf("Length = %d, want 54", h.Length)
			}
			if h.Checksum != tc.checksum {
				t.Errorf("Checksum = %#x, want %#x", h.Checksum, tc.checksum)
			}
			if !bytes.Equal(payload, wire[8:]) {
				t.Errorf("payload = % x, want % x", payload, wire[8:])
			}
		})
	}
}

// TestDecodeDistinctPorts decodes a header whose source and destination
// ports differ, so a decoder that reads the two port slots the wrong way
// round fails. The fixtures above cannot catch that swap because both use
// port 5353 for source and destination. Two trailing octets beyond Length
// stand in for Ethernet padding on a short frame: the payload must stop at
// Length, not at len(wire), or the pad octets leak into it.
func TestDecodeDistinctPorts(t *testing.T) {
	wire := []byte{0xc3, 0x50, 0x00, 0x35, 0x00, 0x0c, 0x00, 0x00, 0xde, 0xad, 0xbe, 0xef, 0x00, 0x00}

	h, payload, err := udp.Decode(wire)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if h.SrcPort != 50000 {
		t.Errorf("SrcPort = %d, want 50000", h.SrcPort)
	}
	if h.DstPort != 53 {
		t.Errorf("DstPort = %d, want 53", h.DstPort)
	}
	if h.Length != 12 {
		t.Errorf("Length = %d, want 12", h.Length)
	}
	if len(payload) != 4 {
		t.Errorf("len(payload) = %d, want 4", len(payload))
	}
	if !bytes.Equal(payload, wire[8:12]) {
		t.Errorf("payload = % x, want % x", payload, wire[8:12])
	}
}

// TestEncodeMatchesFixture encodes the fixture header and payload with the
// fixture addresses and compares the complete 54-byte result, not just the
// fields Decode extracts.
func TestEncodeMatchesFixture(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		src     string
		dst     string
	}{
		{name: "ipv4", fixture: ipv4Fixture, src: "10.0.10.7", dst: "224.0.0.251"},
		{name: "ipv6", fixture: ipv6Fixture, src: "fe80::1", dst: "ff02::fb"},
		// An IPv4-mapped IPv6 source counts as IPv4 (see the doc comment on
		// addressFamily), so this must select the IPv4 pseudo-header and
		// produce the same bytes, checksum 0xaa94 included, as the plain
		// IPv4 case above.
		{name: "ipv4-mapped ipv6 source", fixture: ipv4Fixture, src: "::ffff:10.0.10.7", dst: "224.0.0.251"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := mustDecodeHex(t, tc.fixture)
			src := netip.MustParseAddr(tc.src)
			dst := netip.MustParseAddr(tc.dst)

			got, err := udp.Encode(udp.Header{SrcPort: 5353, DstPort: 5353}, want[8:], src, dst)
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("Encode() = % x, want % x", got, want)
			}
		})
	}
}

// TestChecksumOffsets asserts where Encode places the length and checksum
// fields, by literal offset rather than by decoding its own output.
func TestChecksumOffsets(t *testing.T) {
	src := netip.MustParseAddr("10.0.10.7")
	dst := netip.MustParseAddr("224.0.0.251")
	payload := mustDecodeHex(t, ipv4Fixture)[8:]

	out, err := udp.Encode(udp.Header{SrcPort: 5353, DstPort: 5353}, payload, src, dst)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if got := binary.BigEndian.Uint16(out[6:8]); got != 0xaa94 {
		t.Errorf("out[6:8] = %#x, want 0xaa94", got)
	}
	if got := binary.BigEndian.Uint16(out[4:6]); got != 54 {
		t.Errorf("out[4:6] = %d, want 54", got)
	}
}

// TestZeroChecksumSentAsAllOnes pins a payload whose one's-complement
// checksum computes to zero and asserts that Encode sends it as 0xffff
// (RFC 768) rather than 0x0000, which on IPv4 means "no checksum" and on
// IPv6 is forbidden outright. Its sibling below pins the adjacent payload,
// whose checksum computes to one, so the boundary is checked from both
// sides.
//
// Both checksums were derived by hand from RFC 768, independently of this
// package: sum the IPv4 pseudo-header, the UDP header with checksum zero,
// and the payload as sixteen-bit big-endian words, fold carries past
// sixteen bits, then complement.
//
//	pseudo-header: 0a00 0a07 e000 00fb 0011 000a
//	UDP header:    14e9 14e9 000a 0000
//	payload {0xe1, 0x05}: e105
//	raw sum = 0x1fffe, folded = 0xffff, complement = 0x0000 -> sent as 0xffff
//
//	pseudo-header and UDP header are the same
//	payload {0xe1, 0x04}: e104
//	raw sum = 0x1fffd, folded = 0xfffe, complement = 0x0001 -> sent as 0x0001
func TestZeroChecksumSentAsAllOnes(t *testing.T) {
	src := netip.MustParseAddr("10.0.10.7")
	dst := netip.MustParseAddr("224.0.0.251")
	h := udp.Header{SrcPort: 5353, DstPort: 5353}

	out, err := udp.Encode(h, []byte{0xe1, 0x05}, src, dst)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if got := binary.BigEndian.Uint16(out[6:8]); got != 0xffff {
		t.Errorf("Checksum = %#x, want 0xffff", got)
	}
}

// TestChecksumOneSentAsOne pins the payload adjacent to the one above, whose
// checksum computes to one rather than zero, so Encode must leave it
// untouched instead of substituting 0xffff. See the derivation on
// [TestZeroChecksumSentAsAllOnes].
func TestChecksumOneSentAsOne(t *testing.T) {
	src := netip.MustParseAddr("10.0.10.7")
	dst := netip.MustParseAddr("224.0.0.251")
	h := udp.Header{SrcPort: 5353, DstPort: 5353}

	out, err := udp.Encode(h, []byte{0xe1, 0x04}, src, dst)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if got := binary.BigEndian.Uint16(out[6:8]); got != 0x0001 {
		t.Errorf("Checksum = %#x, want 0x0001", got)
	}
}

// TestEncodeOddLengthPayload covers a payload whose length is odd, which every
// other Encode case in this file avoids: both pseudo-headers and every other
// payload here are even length, so a checksum that drops the trailing octet's
// contribution would still pass them.
//
// The expected checksum is 0x0156, computed by a standalone Python script
// implementing the RFC 768 algorithm from scratch (not by calling this
// package): sum the pseudo-header and the UDP header-plus-payload as
// big-endian 16-bit words, treating the payload's trailing odd octet as the
// high byte of a padded word, fold carries past 16 bits, and complement.
func TestEncodeOddLengthPayload(t *testing.T) {
	src := netip.MustParseAddr("10.0.10.7")
	dst := netip.MustParseAddr("224.0.0.251")
	h := udp.Header{SrcPort: 5353, DstPort: 5353}
	payload := []byte{0xde, 0xad, 0x01}

	out, err := udp.Encode(h, payload, src, dst)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if got := binary.BigEndian.Uint16(out[4:6]); got != 11 {
		t.Errorf("out[4:6] = %d, want 11", got)
	}
	if got := binary.BigEndian.Uint16(out[6:8]); got != 0x0156 {
		t.Errorf("Checksum = %#x, want 0x0156", got)
	}
}

// TestVerify checks both fixtures against their correct addresses, with two
// trailing zero octets appended to stand in for Ethernet padding on a short
// frame, and that flipping one checksum octet turns the report false. The
// fixture's Length field (54) is smaller than the padded buffer, so a
// checksum that covered the whole buffer instead of stopping at Length
// would disagree with the correct one and report false.
func TestVerify(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		src     string
		dst     string
	}{
		{name: "ipv4", fixture: ipv4Fixture, src: "10.0.10.7", dst: "224.0.0.251"},
		{name: "ipv6", fixture: ipv6Fixture, src: "fe80::1", dst: "ff02::fb"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := netip.MustParseAddr(tc.src)
			dst := netip.MustParseAddr(tc.dst)
			wire := append(mustDecodeHex(t, tc.fixture), 0x00, 0x00)

			if !udp.Verify(wire, src, dst) {
				t.Error("Verify() = false, want true for an unmodified fixture with trailing pad octets")
			}

			corrupted := bytes.Clone(wire)
			corrupted[6] ^= 0xff
			if udp.Verify(corrupted, src, dst) {
				t.Error("Verify() = true, want false with a checksum octet flipped")
			}
		})
	}

	t.Run("mixed address family", func(t *testing.T) {
		// addressFamily rejects this pair (10.0.10.7 is IPv4, ff02::fb is pure
		// IPv6), so if its error were ignored, checksum's v4 argument would
		// keep its zero value (false) and the code would checksum the IPv4
		// fixture's bytes against the RFC 8200 section 8.1 IPv6 pseudo-header
		// instead, treating src as its IPv4-mapped form ::ffff:10.0.10.7.
		// That pseudo-header is 16 (src) + 16 (dst) + 4 (UDP length, 54) +
		// 3 zero bytes + 1 next-header byte (17):
		//   00000000 00000000 0000ffff 0a000a07
		//   ff020000 00000000 00000000 000000fb
		//   00000036 00000011
		// Summing that against the fixture's own bytes with its checksum
		// field zeroed folds to 0x746d, so the checksum field that makes the
		// total fold to 0xffff (checksum() returns its one's complement, and
		// Verify wants that complement to be zero) is 0xffff-0x746d = 0x8b92.
		// With the guard in place, addressFamily's error stops Verify before
		// any of this runs, so only the guard can produce the false here.
		wire := mustDecodeHex(t, ipv4Fixture)
		wire[6], wire[7] = 0x8b, 0x92
		src := netip.MustParseAddr("10.0.10.7")
		dst := netip.MustParseAddr("ff02::fb")

		if udp.Verify(wire, src, dst) {
			t.Error("Verify() = true, want false for a mixed address family pair")
		}
	})

	t.Run("datagram Decode refuses", func(t *testing.T) {
		// Length is 4, so Decode refuses before any checksum work happens.
		// For the checksum to agree too and leave the refusal as the only
		// possible cause, the eight octets must check out for 10.0.10.7 ->
		// 224.0.0.251 under the RFC 768 pseudo-header: src (4) + dst (4) +
		// zero + protocol (17) + UDP length (8):
		//   0a000a07 e00000fb 00110008
		// Summing that against the octets with the checksum field zeroed
		// (14e9 14e9 0004 0000) folds to 0x1ef2, so the checksum field that
		// makes the total fold to 0xffff is 0xffff-0x1ef2 = 0xe10d.
		src := netip.MustParseAddr("10.0.10.7")
		dst := netip.MustParseAddr("224.0.0.251")
		wire := []byte{0x14, 0xe9, 0x14, 0xe9, 0x00, 0x04, 0xe1, 0x0d}

		if udp.Verify(wire, src, dst) {
			t.Error("Verify() = true, want false for a datagram Decode itself would refuse")
		}
	})
}

// TestDecodeRefusals covers the inputs Decode must reject: a datagram
// shorter than a header, a Length field under the header size, and a Length
// field that exceeds the supplied buffer.
func TestDecodeRefusals(t *testing.T) {
	tests := []struct {
		name string
		wire []byte
	}{
		{
			name: "shorter than eight octets",
			wire: []byte{0x14, 0xe9, 0x14, 0xe9, 0x00, 0x36},
		},
		{
			name: "length under eight",
			wire: []byte{0x14, 0xe9, 0x14, 0xe9, 0x00, 0x04, 0xaa, 0x94},
		},
		{
			name: "length over the buffer",
			wire: []byte{0x14, 0xe9, 0x14, 0xe9, 0x00, 0x10, 0xaa, 0x94, 0x00, 0x00},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := udp.Decode(tc.wire); !errors.Is(err, udp.ErrMalformed) {
				t.Fatalf("Decode() error = %v, want ErrMalformed", err)
			}
		})
	}
}

// TestEncodeRefusesMixedFamilies covers an IPv4 source paired with an IPv6
// destination, and the reverse.
func TestEncodeRefusesMixedFamilies(t *testing.T) {
	v4 := netip.MustParseAddr("10.0.10.7")
	v6 := netip.MustParseAddr("fe80::1")
	h := udp.Header{SrcPort: 5353, DstPort: 5353}

	tests := []struct {
		name     string
		src, dst netip.Addr
	}{
		{name: "ipv4 source, ipv6 destination", src: v4, dst: v6},
		{name: "ipv6 source, ipv4 destination", src: v6, dst: v4},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := udp.Encode(h, nil, tc.src, tc.dst); !errors.Is(err, udp.ErrMalformed) {
				t.Fatalf("Encode() error = %v, want ErrMalformed", err)
			}
		})
	}
}

// TestCapture pins the ports and checksum of a real mDNS datagram, which the
// hand-computed fixtures above cannot settle on their own: only a capture
// proves the field offsets match what a Bonjour responder puts on the wire.
// It is skipped because no capture has been taken. Reading one needs root or
// the access_bpf group to open /dev/bpf*, which the test host does not grant.
// Fill it from "tcpdump -i en0 -x udp port 5353 -c 1" on a machine that does.
func TestCapture(t *testing.T) {
	t.Skip("no captured mDNS datagram is pinned yet; opening /dev/bpf* needs privileges the test host does not grant")
}
