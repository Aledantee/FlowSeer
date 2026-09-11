# IGMP Codec

Package `igmp` encodes and decodes the IGMP messages used by multicast
snooping. It has no packet-capture dependency and operates on the bytes after
the IPv4 header.

An IGMPv2 membership report can be built and checked directly:

```go
package main

import (
	"fmt"
	"log"
	"net/netip"

	"go.aledante.io/FlowSeer/src/common/net/igmp"
)

func main() {
	group := netip.MustParseAddr("239.1.1.1")
	wire, err := igmp.Encode(igmp.Message{Type: igmp.ReportV2, Group: group})
	if err != nil {
		log.Fatal(err)
	}

	message, err := igmp.Decode(wire)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(message.Type, message.Group)
}
```

`Version` selects a query layout. Leave it unset for reports and leaves because
their type determines the layout. An invalid `Group` represents the zero group
address in a general query. Group-specific queries and all reports and leaves
require an IPv4 multicast address.

## IGMPv1 and IGMPv2 layout

Queries, IGMPv1 reports, IGMPv2 reports, and leaves use the same eight-octet
base layout. Max Response Time is meaningful only for queries.

| Offset | Length | Field |
|---|---:|---|
| 0 | 1 | Type |
| 1 | 1 | Max Response Time in 100 ms units |
| 2 | 2 | Checksum |
| 4 | 4 | Group Address |

An eight-octet query always decodes as `V2`, including one whose response code
is zero. The decoder accepts extra data after a legacy message and includes it
when checking the checksum, as RFC 2236 requires.

## IGMPv3 query layout

| Offset | Length | Field |
|---|---:|---|
| 0 | 1 | Type (`0x11`) |
| 1 | 1 | Max Resp Code |
| 2 | 2 | Checksum |
| 4 | 4 | Group Address |
| 8 | 1 | Reserved (bits 7..4), Suppress (bit 3), QRV (bits 2..0) |
| 9 | 1 | QQIC |
| 10 | 2 | Number of Sources |
| 12 | 4 each | Source Addresses |

The encoded length is `12 + 4*N`. A query with two sources is 20 octets.
Sources must be IPv4 unicast addresses. The encoder writes reserved bits as
zero; the decoder ignores them.

## IGMPv3 report layout

| Offset | Length | Field |
|---|---:|---|
| 0 | 1 | Type (`0x22`) |
| 1 | 1 | Reserved |
| 2 | 2 | Checksum |
| 4 | 2 | Reserved |
| 6 | 2 | Number of Group Records |
| 8 | variable | Group Records |

Each group record has this layout:

| Record offset | Length | Field |
|---|---:|---|
| 0 | 1 | Record Type (1 through 6) |
| 1 | 1 | Auxiliary Data Length in 32-bit words |
| 2 | 2 | Number of Sources |
| 4 | 4 | Multicast Address |
| 8 | 4 each | Source Addresses |
| `8 + 4*N` | `4*A` | Auxiliary Data |

IGMPv3 defines no auxiliary data, so `Encode` writes an auxiliary length of
zero. `Decode` skips received auxiliary data and unrecognized record types.
It also includes ignored trailing bytes in the checksum.

## Timer codes

IGMPv2 encodes `MaxResp` as an unsigned byte in 100 ms units. IGMPv3 uses the
same units and these rules:

- Codes below 128 are linear.
- Codes at or above 128 decode as `(mant | 0x10) << (exp + 3)`.

`Encode` accepts a duration only when one code represents it exactly. For
example, codes 127 and 128 represent 12.7 s and 12.8 s. A duration such as
12.9 s has no code and returns `ErrMalformed`.

`QQIC` remains a raw wire code. Values below 128 mean seconds directly; values
at or above 128 use the same four-bit mantissa and three-bit exponent formula.

## Checksum and errors

The checksum is the RFC 1071 one's-complement checksum over the complete IGMP
message. `Decode` checks the complete supplied payload before it parses the
message body.

Invalid checksums, fields, counts, and lengths wrap `ErrMalformed`. Unknown
message types and query versions wrap `ErrUnsupported`. Encoded messages are
limited to 65,535 octets because they are IPv4 payloads.

## Sources

- RFC 2236 section 2: IGMPv2 message layout and timer units.
- RFC 3376 sections 4.1 and 4.2: IGMPv3 queries, reports, timer codes, and
  group records.
- RFC 1071: Internet checksum calculation.
