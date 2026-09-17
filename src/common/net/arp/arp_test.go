package arp_test

import (
	"bytes"
	"errors"
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
)

// literal is the RFC 826 wire vector shared by the placement and decode
// tests. Hardware type (offset 0-2) and operation (offset 6-8) both hold two
// octets, so this vector pins them to a Reply, 0x0001 against 0x0002, and
// they differ in every octet from each other; the sender and target address
// pairs already differ in every octet of both the hardware and protocol
// addresses, so a codec that transposes any of these three pairs fails
// every octet instead of passing by accident.
var literal = []byte{
	0x00, 0x01, // hardware type: Ethernet
	0x08, 0x00, // protocol type: IPv4
	0x06,       // hardware length
	0x04,       // protocol length
	0x00, 0x02, // operation: Reply
	0x02, 0x11, 0x22, 0x33, 0x44, 0x55, // sender hardware address
	0x0a, 0x01, 0x02, 0x03, // sender protocol address: 10.1.2.3
	0x06, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, // target hardware address
	0xc0, 0xa8, 0x09, 0x07, // target protocol address: 192.168.9.7
}

func literalMessage() arp.Message {
	return arp.Message{
		HardwareType: 1,
		ProtocolType: 0x0800,
		Operation:    arp.Reply,
		SenderMAC:    netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x55},
		SenderAddr:   netip.MustParseAddr("10.1.2.3"),
		TargetMAC:    netaddr.MAC{0x06, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		TargetAddr:   netip.MustParseAddr("192.168.9.7"),
	}
}

func TestARPEncodePlacesEveryFieldAtItsOffset(t *testing.T) {
	t.Parallel()

	frame, err := arp.Encode(literalMessage(), netaddr.MAC{0x06, 0xaa, 0xbb, 0xcc, 0xdd, 0xee})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	payload := frame.Payload
	if len(payload) != len(literal) {
		t.Fatalf("len(payload) = %d, want %d", len(payload), len(literal))
	}

	offsets := []struct {
		name string
		lo   int
		hi   int
	}{
		{"hardware type", 0, 2},
		{"protocol type", 2, 4},
		{"hardware length", 4, 5},
		{"protocol length", 5, 6},
		{"operation", 6, 8},
		{"sender hardware address", 8, 14},
		{"sender protocol address", 14, 18},
		{"target hardware address", 18, 24},
		{"target protocol address", 24, 28},
	}

	for _, o := range offsets {
		got := payload[o.lo:o.hi]
		want := literal[o.lo:o.hi]
		if !bytes.Equal(got, want) {
			t.Errorf("payload[%d:%d] (%s) = % x, want % x", o.lo, o.hi, o.name, got, want)
		}
	}
}

func TestARPDecodeReadsTheLiteralVector(t *testing.T) {
	t.Parallel()

	frame := ethernet.Frame{
		Dst:       netaddr.MAC{0x06, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		Src:       netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x55},
		EtherType: ethernet.EtherTypeARP,
		Payload:   literal,
	}

	got, err := arp.Decode(frame)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	want := literalMessage()
	if got != want {
		t.Errorf("Decode() = %+v, want %+v", got, want)
	}
}

func TestARPRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want arp.Message
	}{
		{
			name: "request",
			want: arp.Message{
				HardwareType: 1,
				ProtocolType: 0x0800,
				Operation:    arp.Request,
				SenderMAC:    netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x55},
				SenderAddr:   netip.MustParseAddr("10.1.2.3"),
				TargetMAC:    netaddr.MAC{0x06, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
				TargetAddr:   netip.MustParseAddr("192.168.9.7"),
			},
		},
		{
			name: "reply",
			want: arp.Message{
				HardwareType: 1,
				ProtocolType: 0x0800,
				Operation:    arp.Reply,
				SenderMAC:    netaddr.MAC{0x06, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
				SenderAddr:   netip.MustParseAddr("192.168.9.7"),
				TargetMAC:    netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x55},
				TargetAddr:   netip.MustParseAddr("10.1.2.3"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			frame, err := arp.Encode(tt.want, tt.want.TargetMAC)
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}

			got, err := arp.Decode(frame)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}

			if got != tt.want {
				t.Errorf("Decode(Encode(m)) = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestARPEncodeRequestGoesToBroadcastWithAnUnknownTargetHardwareAddress
// covers the case the placement vector cannot: a request's payload target
// hardware address is the zero MAC (unknown, which is what is being asked),
// independent of the frame's own destination, which must still reach the
// segment as a broadcast.
func TestARPEncodeRequestGoesToBroadcastWithAnUnknownTargetHardwareAddress(t *testing.T) {
	t.Parallel()

	broadcast := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	m := arp.Message{
		HardwareType: 1,
		ProtocolType: 0x0800,
		Operation:    arp.Request,
		SenderMAC:    netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x55},
		SenderAddr:   netip.MustParseAddr("10.1.2.3"),
		TargetAddr:   netip.MustParseAddr("10.1.2.254"),
	}

	frame, err := arp.Encode(m, broadcast)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	if frame.Dst != broadcast {
		t.Errorf("frame.Dst = %v, want %v", frame.Dst, broadcast)
	}

	var zero netaddr.MAC
	if got := frame.Payload[18:24]; !bytes.Equal(got, zero[:]) {
		t.Errorf("payload[18:24] (target hardware address) = % x, want % x", got, zero[:])
	}
}

func TestARPEncodeRefuses(t *testing.T) {
	t.Parallel()

	valid := literalMessage()
	ipv6 := netip.MustParseAddr("2001:db8::1")

	tests := []struct {
		name string
		m    arp.Message
	}{
		{
			name: "sender address not IPv4",
			m: func() arp.Message {
				m := valid
				m.SenderAddr = ipv6
				return m
			}(),
		},
		{
			name: "target address not IPv4",
			m: func() arp.Message {
				m := valid
				m.TargetAddr = ipv6
				return m
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := arp.Encode(tt.m, netaddr.MAC{0x06, 0xaa, 0xbb, 0xcc, 0xdd, 0xee})
			if !errors.Is(err, arp.ErrMalformed) {
				t.Errorf("Encode() error = %v, want wrapping %v", err, arp.ErrMalformed)
			}
		})
	}
}

func TestARPDecodeRefuses(t *testing.T) {
	t.Parallel()

	validFrame := func() ethernet.Frame {
		payload := make([]byte, len(literal))
		copy(payload, literal)
		return ethernet.Frame{EtherType: ethernet.EtherTypeARP, Payload: payload}
	}

	tests := []struct {
		name  string
		frame ethernet.Frame
		want  error
	}{
		{
			name: "ethertype other than ARP",
			frame: func() ethernet.Frame {
				f := validFrame()
				f.EtherType = ethernet.EtherTypeIPv4
				return f
			}(),
			want: arp.ErrUnsupported,
		},
		{
			name: "payload shorter than 28 octets",
			frame: ethernet.Frame{
				EtherType: ethernet.EtherTypeARP,
				Payload:   literal[:27],
			},
			want: arp.ErrMalformed,
		},
		{
			name: "hardware type other than 1",
			frame: func() ethernet.Frame {
				f := validFrame()
				f.Payload[1] = 6
				return f
			}(),
			want: arp.ErrUnsupported,
		},
		{
			name: "protocol type other than 0x0800",
			frame: func() ethernet.Frame {
				f := validFrame()
				f.Payload[2], f.Payload[3] = 0x08, 0x06
				return f
			}(),
			want: arp.ErrUnsupported,
		},
		{
			name: "hardware length other than 6",
			frame: func() ethernet.Frame {
				f := validFrame()
				f.Payload[4] = 8
				return f
			}(),
			want: arp.ErrMalformed,
		},
		{
			name: "protocol length other than 4",
			frame: func() ethernet.Frame {
				f := validFrame()
				f.Payload[5] = 16
				return f
			}(),
			want: arp.ErrMalformed,
		},
		{
			name: "address that is not IPv4",
			frame: func() ethernet.Frame {
				f := validFrame()
				// 0x86dd is IPv6: a wire claiming this protocol type names an
				// address family the decoder does not construct, which is
				// what an address that is not IPv4 means at this layer.
				f.Payload[2], f.Payload[3] = 0x86, 0xdd
				return f
			}(),
			want: arp.ErrUnsupported,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := arp.Decode(tt.frame)
			if !errors.Is(err, tt.want) {
				t.Errorf("Decode() error = %v, want wrapping %v", err, tt.want)
			}
		})
	}
}
