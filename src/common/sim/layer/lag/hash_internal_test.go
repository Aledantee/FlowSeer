package lag

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
)

func TestInspectTCPHashInputChecksEtherType(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		etherType ethernet.EtherType
		src       string
		dst       string
		v4        *ip.V4
		v6        *ip.V6
		decoded   bool
	}{
		{name: "IPv4", etherType: ethernet.EtherTypeIPv4, src: "10.0.0.1", dst: "10.0.0.2", v4: &ip.V4{}, decoded: true},
		{name: "IPv6", etherType: ethernet.EtherTypeIPv6, src: "2001:db8::1", dst: "2001:db8::2", v6: &ip.V6{}, decoded: true},
		{name: "ARP carrying IPv4", etherType: ethernet.EtherTypeARP, src: "10.0.0.1", dst: "10.0.0.2", v4: &ip.V4{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			src, dst := netip.MustParseAddr(tc.src), netip.MustParseAddr(tc.dst)
			payload, err := (ip.Header{Src: src, Dst: dst, Protocol: 17, HopLimit: 64, V4: tc.v4, V6: tc.v6}).Encode(
				[]byte{0x9c, 0x40, 0x13, 0x88, 0, 8, 0, 0},
			)
			if err != nil {
				t.Fatalf("encode UDP packet: %v", err)
			}
			input := inspectTCPHashInput(ethernet.Frame{EtherType: tc.etherType, Payload: payload})
			if input.ipDecoded != tc.decoded {
				t.Fatalf("ipDecoded = %t, want %t", input.ipDecoded, tc.decoded)
			}
			if tc.decoded {
				if input.ipSrc != src || input.ipDst != dst || input.ipProtocol != 17 || !input.hasTransport || input.transport4 != [4]byte{0x9c, 0x40, 0x13, 0x88} {
					t.Fatalf("hash input = %+v, want %s -> %s, UDP ports 40000 -> 5000", input, src, dst)
				}
			} else if input != (tcpHashInput{}) {
				t.Fatalf("hash input = %+v, want empty input", input)
			}
		})
	}
}
