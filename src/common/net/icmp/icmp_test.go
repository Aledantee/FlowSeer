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
	// ICMPv6 echo request, type 128, code 0.
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
	// ICMPv4 destination unreachable, fragmentation needed: type 3, code 4.
	// Type and code differ from each other so neither field can stand in
	// for the other.
	b := []byte{0x03, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

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
