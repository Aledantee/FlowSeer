# NDP Codec

Package `ndp` encodes and decodes the Neighbor Solicitation and Neighbor
Advertisement messages of IPv6 Neighbor Discovery. The caller supplies an
`ip.Header` because the ICMPv6 checksum covers the IPv6 source and
destination addresses. `Router Solicitation`, `Router Advertisement`,
`Redirect`, and the prefix and MTU options are out of scope; this package
covers message types 135 and 136 only.

This builds a Neighbor Advertisement with a target link-layer address:

```go
package main

import (
	"fmt"
	"log"
	"net/netip"

	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/ndp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
)

func main() {
	hdr := ip.Header{
		Src:      netip.MustParseAddr("2001:db8::1"),
		Dst:      netip.MustParseAddr("2001:db8::2"),
		HopLimit: 255,
		Protocol: 58,
		V6:       &ip.V6{},
	}
	wire, err := ndp.Encode(hdr, ndp.Message{
		Type:             ndp.NeighborAdvertisement,
		Target:           netip.MustParseAddr("2001:db8::1"),
		Solicited:        true,
		Override:         true,
		LinkLayerAddr:    netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x55},
		HasLinkLayerAddr: true,
	})
	if err != nil {
		log.Fatal(err)
	}

	message, err := ndp.Decode(hdr, wire)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(message.Type, message.Target)
}
```

`Encode` returns only the ICMPv6 message; the caller sets the IPv6 Hop Limit
to 255 when it frames the packet (RFC 4861 sections 4.3 and 4.4). `Decode`
rejects any other received Hop Limit, which is how a neighbor tells apart a
message that traversed only the local link from one an off-link attacker
forged.

## Neighbor Solicitation layout

24 octets without the option, 32 with it.

| Offset | Length | Field |
|---|---:|---|
| 0 | 1 | Type (135) |
| 1 | 1 | Code (zero) |
| 2 | 2 | Checksum |
| 4 | 4 | Reserved |
| 8 | 16 | Target Address |
| 24 | 1 | Option Type (1, Source Link-Layer Address) |
| 25 | 1 | Option Length (1, in 8-octet units) |
| 26 | 6 | Source Link-Layer Address |

RFC 4861 section 4.3 gives the solicitation a four-octet Reserved field and
no flags. `LinkLayerAddr` carries the Source Link-Layer Address; a
solicitation whose IPv6 source is the unspecified address, as during
duplicate address detection, must not carry it, and `Decode` refuses one that
does.

## Neighbor Advertisement layout

24 octets without the option, 32 with it.

| Offset | Length | Field |
|---|---:|---|
| 0 | 1 | Type (136) |
| 1 | 1 | Code (zero) |
| 2 | 2 | Checksum |
| 4 | 1 | Flags: R 0x80, S 0x40, O 0x20 |
| 5 | 3 | Reserved |
| 8 | 16 | Target Address |
| 24 | 1 | Option Type (2, Target Link-Layer Address) |
| 25 | 1 | Option Length (1, in 8-octet units) |
| 26 | 6 | Target Link-Layer Address |

RFC 4861 section 4.4 defines R (Router), S (Solicited), and O (Override).
`LinkLayerAddr` carries the Target Link-Layer Address. Per section 7.2.5, an
observation with `Override` clear leaves an existing entry's cached address
alone; this package only encodes and decodes the flags, and leaves that merge
rule to the caller that maintains a neighbor table.

## Checksum

The checksum covers this IPv6 pseudo-header followed by the ICMPv6 message,
with the checksum field itself zeroed during calculation:

| Pseudo-header offset | Length | Field |
|---|---:|---|
| 0 | 16 | IPv6 Source Address |
| 16 | 16 | IPv6 Destination Address |
| 32 | 4 | ICMPv6 message length |
| 36 | 3 | Zero |
| 39 | 1 | Next Header (58) |

Both header addresses must be IPv6 addresses (RFC 8200 section 8.1).

## Errors

`Decode` walks the option chain looking for the link-layer address option
that matches the message (Source for a solicitation, Target for an
advertisement); an option of a different type is skipped by its own declared
length rather than misread as the one `Decode` wants, and a chain with no
matching option leaves `HasLinkLayerAddr` false rather than an error.
`Decode` wraps `ErrMalformed` for: a payload shorter than twenty-four octets
(RFC 4861 sections 7.1.1 and 7.1.2); a Hop Limit other than 255; a Code other
than zero; a multicast Target Address; an option whose declared length is
zero or overruns the payload; a matching link-layer address option whose
declared length is not one 8-octet unit; a Source Link-Layer Address option
on a solicitation from the unspecified address; and a checksum that does not
match. `Encode` wraps `ErrMalformed` for the same invalid target, unspecified-
address, and flags rules `Decode` enforces, so `Encode` never builds a
message `Decode` would refuse: an invalid target, a solicitation from the
unspecified address carrying a link-layer address option, and a solicitation
with Router, Solicited, or Override set (those three apply only to an
advertisement). `Encode` and `Decode` both wrap `ErrUnsupported` for a
message type other than 135 or 136.

## Sources

- RFC 4861 sections 4.3 and 4.4: Neighbor Solicitation and Neighbor
  Advertisement message formats.
- RFC 4861 section 4.6.1: the Source/Target Link-Layer Address option format,
  shared by both message types.
- RFC 4861 sections 7.1.1 and 7.1.2: Neighbor Solicitation and Neighbor
  Advertisement message validation, including the Hop Limit and minimum
  length checks.
- RFC 4861 section 7.2.5: the receipt rules for a Neighbor Advertisement,
  including the Override flag's effect on a cached link-layer address.
- RFC 8200 section 8.1: the ICMPv6 pseudo-header.
