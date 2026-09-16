package icmp_test

import (
	"errors"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/icmp"
)

// literalEchoRequest is an ICMPv4 echo request, type 8, code 0.
var literalEchoRequest = []byte{0x08, 0x00, 0xf7, 0xff, 0x00, 0x00, 0x00, 0x00}

func TestDecodeLiteral(t *testing.T) {
	h, payload, err := icmp.Decode(literalEchoRequest)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if h.Type != 8 {
		t.Errorf("Type = %d, want 8", h.Type)
	}
	if h.Code != 0 {
		t.Errorf("Code = %d, want 0", h.Code)
	}
	if h.Checksum != 0xf7ff {
		t.Errorf("Checksum = %#x, want 0xf7ff", h.Checksum)
	}
	if len(payload) != 4 {
		t.Errorf("len(payload) = %d, want 4", len(payload))
	}
}

func TestDecodeICMPv6Literal(t *testing.T) {
	// ICMPv6 echo request, type 128, code 0. The checksum field is left
	// zero rather than a computed value: an ICMPv6 checksum covers the
	// IPv6 pseudo-header, and this literal carries no addresses to compute
	// one from.
	b := []byte{0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	h, _, err := icmp.Decode(b)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if h.Type != 128 {
		t.Errorf("Type = %d, want 128", h.Type)
	}
	if h.Code != 0 {
		t.Errorf("Code = %d, want 0", h.Code)
	}
}

func TestDecodeDestinationUnreachable(t *testing.T) {
	// ICMPv4 destination unreachable, fragmentation needed: type 3, code 4,
	// checksum 0xfcfb. Type and code differ from each other so neither field
	// can stand in for the other. The checksum covers only these eight
	// octets (an ICMPv4 checksum has no pseudo-header), so it can be
	// verified independently: with the checksum field zeroed, the four
	// 16-bit big-endian words are 0x0304, 0x0000, 0x0000, 0x0000, summing to
	// 0x0304; the one's complement is 0xfcfb.
	b := []byte{0x03, 0x04, 0xfc, 0xfb, 0x00, 0x00, 0x00, 0x00}

	h, _, err := icmp.Decode(b)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if h.Type != 3 {
		t.Errorf("Type = %d, want 3", h.Type)
	}
	if h.Code != 4 {
		t.Errorf("Code = %d, want 4", h.Code)
	}
}

func TestDecodeRefusals(t *testing.T) {
	_, _, err := icmp.Decode(literalEchoRequest[:3])
	if !errors.Is(err, icmp.ErrMalformed) {
		t.Errorf("Decode() error = %v, want ErrMalformed", err)
	}
}
