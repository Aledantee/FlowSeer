package udp_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
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
// port 5353 for source and destination.
func TestDecodeDistinctPorts(t *testing.T) {
	wire := []byte{0xc3, 0x50, 0x00, 0x35, 0x00, 0x0c, 0x00, 0x00, 0xde, 0xad, 0xbe, 0xef}

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

// TestZeroChecksumSentAsAllOnes searches for a payload whose one's-complement
// checksum computes to zero, then asserts that Encode sends it as 0xffff
// (RFC 768) rather than 0x0000, which on IPv4 means "no checksum" and on
// IPv6 is forbidden outright.
func TestZeroChecksumSentAsAllOnes(t *testing.T) {
	src := netip.MustParseAddr("10.0.10.7")
	dst := netip.MustParseAddr("224.0.0.251")
	h := udp.Header{SrcPort: 5353, DstPort: 5353}

	var wire []byte
	found := false
	for candidate := range 0x10000 {
		payload := []byte{byte(candidate >> 8), byte(candidate)}
		out, err := udp.Encode(h, payload, src, dst)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
		if binary.BigEndian.Uint16(out[6:8]) == 0xffff {
			wire = out
			found = true
			break
		}
	}
	if !found {
		t.Fatal("search over two-octet payloads found none whose checksum computes to zero")
	}
	if got := binary.BigEndian.Uint16(wire[6:8]); got != 0xffff {
		t.Errorf("Checksum = %#x, want 0xffff", got)
	}
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
			if _, _, err := udp.Decode(tc.wire); err == nil {
				t.Fatal("Decode() error = nil, want an error")
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
			if _, err := udp.Encode(h, nil, tc.src, tc.dst); err == nil {
				t.Fatal("Encode() error = nil, want an error")
			}
		})
	}
}

// TestCapture pins the ports and checksum of a real mDNS datagram. No
// capture was possible in this environment: tcpdump requires either root or
// the access_bpf group to open /dev/bpf* on this machine
// ("tcpdump: (cannot open BPF device) /dev/bpf0: Operation not permitted"),
// and the sandbox this package was implemented under runs unprivileged with
// no escalation available. This test documents that absence instead of
// pinning a capture.
func TestCapture(t *testing.T) {
	t.Skip("no packet capture was possible in this environment: tcpdump could not open /dev/bpf0 without escalated privileges")
}
