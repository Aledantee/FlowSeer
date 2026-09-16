package tcp_test

import (
	"errors"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/tcp"
)

// literalSegment is the SYN-ACK from source port 80 to destination port
// 8080, sequence 1, acknowledgment 2.
var literalSegment = []byte{
	0x00, 0x50, 0x1f, 0x90, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x02,
	0x50, 0x12, 0xff, 0xff, 0x00, 0x00, 0x00, 0x00,
}

func TestDecodeLiteral(t *testing.T) {
	h, payload, err := tcp.Decode(literalSegment)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if h.SrcPort != 80 {
		t.Errorf("SrcPort = %d, want 80", h.SrcPort)
	}
	if h.DstPort != 8080 {
		t.Errorf("DstPort = %d, want 8080", h.DstPort)
	}
	if h.Seq != 1 {
		t.Errorf("Seq = %d, want 1", h.Seq)
	}
	if h.Ack != 2 {
		t.Errorf("Ack = %d, want 2", h.Ack)
	}
	if h.DataOffset != 5 {
		t.Errorf("DataOffset = %d, want 5", h.DataOffset)
	}
	if h.Window != 0xffff {
		t.Errorf("Window = %#x, want 0xffff", h.Window)
	}
	if !h.Flags.Has(tcp.SYN | tcp.ACK) {
		t.Errorf("Flags = %#x, want SYN|ACK set", h.Flags)
	}
	if h.Flags.Has(tcp.FIN | tcp.RST | tcp.PSH | tcp.URG | tcp.ECE | tcp.CWR) {
		t.Errorf("Flags = %#x, want no other bits set", h.Flags)
	}
	if len(payload) != 0 {
		t.Errorf("len(payload) = %d, want 0", len(payload))
	}
}

func TestDecodeOffsets(t *testing.T) {
	b := make([]byte, 20)
	copy(b, literalSegment)

	// A decoder that reads the two port slots the wrong way round would
	// still pass a test built from a symmetric fixture, so pin the source
	// port to its own slot with a literal that differs from the
	// destination port.
	b[0], b[1] = 0x01, 0x02
	b[2], b[3] = 0x03, 0x04
	h, _, err := tcp.Decode(b)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if h.SrcPort != 0x0102 {
		t.Errorf("SrcPort = %#x, want 0x0102 (from b[0:2])", h.SrcPort)
	}
	if h.DstPort != 0x0304 {
		t.Errorf("DstPort = %#x, want 0x0304 (from b[2:4])", h.DstPort)
	}

	b[13] = 0x3f
	h, _, err = tcp.Decode(b)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	want := tcp.FIN | tcp.SYN | tcp.RST | tcp.PSH | tcp.ACK | tcp.URG
	if !h.Flags.Has(want) || h.Flags.Has(tcp.ECE|tcp.CWR) {
		t.Errorf("Flags = %#x, want %#x (from b[13]&0x3f)", h.Flags, want)
	}

	b[13] = 0x40
	h, _, err = tcp.Decode(b)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !h.Flags.Has(tcp.ECE) || h.Flags.Has(tcp.CWR) {
		t.Errorf("Flags = %#x, want only ECE set", h.Flags)
	}

	b[13] = 0x80
	h, _, err = tcp.Decode(b)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !h.Flags.Has(tcp.CWR) || h.Flags.Has(tcp.ECE) {
		t.Errorf("Flags = %#x, want only CWR set", h.Flags)
	}
}

func TestDecodeRefusals(t *testing.T) {
	tests := []struct {
		name string
		b    []byte
	}{
		{
			name: "shorter than 20 octets",
			b:    literalSegment[:19],
		},
		{
			name: "data offset under 5",
			b: func() []byte {
				b := append([]byte{}, literalSegment...)
				b[12] = 0x40 // data offset 4
				return b
			}(),
		},
		{
			name: "data offset past the buffer",
			b: func() []byte {
				b := append([]byte{}, literalSegment...)
				b[12] = 0xf0 // data offset 15, 60 octets, past a 20-octet buffer
				return b
			}(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := tcp.Decode(tc.b)
			if !errors.Is(err, tcp.ErrMalformed) {
				t.Errorf("Decode() error = %v, want ErrMalformed", err)
			}
		})
	}
}
