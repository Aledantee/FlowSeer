# UDP Codec

Package `udp` encodes and decodes User Datagram Protocol headers. Unlike
`igmp` and `mld`, `Encode` takes the source and destination addresses
directly instead of an `ip.Header`, because a UDP checksum is defined over
addresses alone and the reflector this package supports rewrites the source
address on every copy, which means a fresh checksum every time regardless of
what carried the datagram.

This decodes an mDNS query and re-encodes it from the decoded header and
payload:

```go
package main

import (
	"encoding/hex"
	"fmt"
	"log"
	"net/netip"

	"go.aledante.io/FlowSeer/src/common/net/udp"
)

func main() {
	wire, err := hex.DecodeString(
		"14e914e90036aa94000000000001000000000000095f7365727669636573" +
			"075f646e732d7364045f756470056c6f63616c00000c0001")
	if err != nil {
		log.Fatal(err)
	}

	h, payload, err := udp.Decode(wire)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(h.SrcPort, h.DstPort, h.Length) // 5353 5353 54

	src := netip.MustParseAddr("10.0.10.7")
	dst := netip.MustParseAddr("224.0.0.251")
	reencoded, err := udp.Encode(h, payload, src, dst)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(hex.EncodeToString(reencoded) == hex.EncodeToString(wire)) // true
}
```

`Decode` overwrites nothing on `h`; `Encode` ignores `h.Length` and
`h.Checksum` on the `h` it is given and derives both fields itself, from the
payload and the addresses, in the datagram it returns.

## Header layout

| Offset | Length | Field |
|---|---:|---|
| 0 | 2 | Source Port |
| 2 | 2 | Destination Port |
| 4 | 2 | Length (header plus payload, in octets) |
| 6 | 2 | Checksum |

`Decode` refuses fewer than eight octets, a Length under eight, or a Length
that exceeds the supplied buffer. It does not verify the checksum: a
snooping switch reads ports without checking transport checksums, and the
filter and reflector this package supports need the same. Call `Verify` for
callers that do want the check.

## Checksum

The checksum covers a pseudo-header carrying the addresses, the protocol
number, and the UDP length, followed by the UDP header and payload. RFC 768
defines the IPv4 pseudo-header:

| Offset | Length | Field |
|---|---:|---|
| 0 | 4 | Source Address |
| 4 | 4 | Destination Address |
| 8 | 1 | Zero |
| 9 | 1 | Protocol (17) |
| 10 | 2 | UDP Length |

RFC 8200 section 8.1 defines the IPv6 pseudo-header the same way, at twice
the address width:

| Offset | Length | Field |
|---|---:|---|
| 0 | 16 | Source Address |
| 16 | 16 | Destination Address |
| 32 | 4 | Upper-Layer Packet Length |
| 36 | 3 | Zero |
| 39 | 1 | Next Header (17) |

`Encode` picks the pseudo-header shape from `src` and `dst`, which must both
be IPv4 (an IPv4-mapped IPv6 address counts as IPv4) or both be pure IPv6; a
mixed pair returns `ErrMalformed`. A computed checksum of zero is sent as
`0xffff` instead, per RFC 768: a real zero on IPv4 means "no checksum was
computed", and RFC 8200 section 8.1 forbids a zero UDP checksum on IPv6
outright.

## Errors

Invalid lengths and mixed address families wrap `ErrMalformed`.

## Sources

- RFC 768: UDP header layout and the IPv4 pseudo-header checksum.
- RFC 8200 section 8.1: the IPv6 pseudo-header and the ban on a zero
  checksum.
