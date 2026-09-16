# ARP Codec

Package `arp` encodes and decodes Address Resolution Protocol messages. It
rides Ethernet directly, the way `lacp` does: `Encode` builds an
`ethernet.Frame` with EtherType `0x0806`, and `Decode` checks that EtherType
itself.

A request built and read back:

```go
package main

import (
	"fmt"
	"log"
	"net/netip"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
)

func main() {
	broadcast := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	frame, err := arp.Encode(arp.Message{
		HardwareType: 1,
		ProtocolType: 0x0800,
		Operation:    arp.Request,
		SenderMAC:    netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x55},
		SenderAddr:   netip.MustParseAddr("10.1.2.3"),
		TargetAddr:   netip.MustParseAddr("10.1.2.254"),
	}, broadcast)
	if err != nil {
		log.Fatal(err)
	}

	message, err := arp.Decode(frame)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(message.Operation, message.SenderAddr)
}
```

`Encode` takes the frame's destination as a separate `dst` parameter rather
than deriving it from `Message.TargetMAC`. The two are independent: a
request's payload target hardware address is the zero MAC (unknown — that is
what is being asked) while the frame itself must still reach the segment as a
broadcast, so `Message.TargetMAC` and `dst` coincide only for a reply. `dst`
becomes the frame's `Dst` verbatim; `SenderMAC` still becomes the frame's
`Src`.

## Wire layout

Every message is 28 octets: an Ethernet hardware address (6 octets) paired
with an IPv4 protocol address (4 octets), on both the sender and target
sides.

| Offset | Length | Field |
|---|---:|---|
| 0 | 2 | Hardware Type (1 for Ethernet) |
| 2 | 2 | Protocol Type (0x0800 for IPv4) |
| 4 | 1 | Hardware Length (6) |
| 5 | 1 | Protocol Length (4) |
| 6 | 2 | Operation (1 = Request, 2 = Reply) |
| 8 | 6 | Sender Hardware Address |
| 14 | 4 | Sender Protocol Address |
| 18 | 6 | Target Hardware Address |
| 24 | 4 | Target Protocol Address |

## Errors

`Decode` wraps `ErrUnsupported` for an EtherType other than `0x0806`, a
hardware type other than 1, and a protocol type other than `0x0800`; that
last case is also the only way this package can be handed an address that is
not IPv4, because the only protocol address family it constructs is the one
named by protocol type `0x0800`. `Decode` wraps `ErrMalformed` for a payload
shorter than 28 octets, a hardware length other than 6, and a protocol length
other than 4. `Encode` wraps `ErrMalformed` when `SenderAddr` or `TargetAddr`
is not IPv4.

## Sources

- RFC 826: Address Resolution Protocol, message format and field semantics.
