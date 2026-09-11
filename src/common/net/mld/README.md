# MLD Codec

Package `mld` encodes and decodes the ICMPv6 messages used by multicast
snooping. The caller supplies an `ip.Header` because the ICMPv6 checksum covers
IPv6 source and destination addresses.

This builds a complete MLDv1 report body:

```go
package main

import (
	"fmt"
	"log"
	"net/netip"

	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/mld"
)

func main() {
	hdr := ip.Header{
		Src:      netip.MustParseAddr("fe80::1"),
		Dst:      netip.MustParseAddr("ff05::1"),
		Protocol: 58,
		V6:       &ip.V6{},
	}
	wire, err := mld.Encode(hdr, mld.Message{
		Type:  mld.ReportV1,
		Group: netip.MustParseAddr("ff05::1"),
	})
	if err != nil {
		log.Fatal(err)
	}

	message, err := mld.Decode(hdr, wire)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(message.Type, message.Group)
}
```

`Encode` returns only the ICMPv6 message. A caller that needs the IPv6 Router
Alert option must prepend its own Hop-by-Hop header. `Version` selects a query
layout; report types already identify their layout. An invalid `Group`
represents the unspecified address in a general query.

## MLDv1 layout

MLDv1 queries, reports, and done messages are 24 octets.

| Offset | Length | Field |
|---|---:|---|
| 0 | 1 | Type |
| 1 | 1 | Code (zero when encoded) |
| 2 | 2 | Checksum |
| 4 | 2 | Maximum Response Delay in milliseconds |
| 6 | 2 | Reserved |
| 8 | 16 | Multicast Address |

Maximum Response Delay is meaningful only for queries. The decoder accepts
extra bytes after an MLDv1 message and includes them in the checksum.

## MLDv2 query layout

| Offset | Length | Field |
|---|---:|---|
| 0 | 1 | Type (130) |
| 1 | 1 | Code |
| 2 | 2 | Checksum |
| 4 | 2 | Maximum Response Code |
| 6 | 2 | Reserved |
| 8 | 16 | Multicast Address |
| 24 | 1 | Reserved (bits 7..4), Suppress (bit 3), QRV (bits 2..0) |
| 25 | 1 | QQIC |
| 26 | 2 | Number of Sources |
| 28 | 16 each | Source Addresses |

The encoded length is `28 + 16*N`. A query with one source is 44 octets.
Sources must be IPv6 unicast addresses.

## MLDv2 report layout

| Offset | Length | Field |
|---|---:|---|
| 0 | 1 | Type (143) |
| 1 | 1 | Reserved |
| 2 | 2 | Checksum |
| 4 | 2 | Reserved |
| 6 | 2 | Number of Address Records |
| 8 | variable | Address Records |

Each address record has this layout:

| Record offset | Length | Field |
|---|---:|---|
| 0 | 1 | Record Type (1 through 6) |
| 1 | 1 | Auxiliary Data Length in 32-bit words |
| 2 | 2 | Number of Sources |
| 4 | 16 | Multicast Address |
| 20 | 16 each | Source Addresses |
| `20 + 16*N` | `4*A` | Auxiliary Data |

MLDv2 defines no auxiliary data. `Encode` writes none; `Decode` skips received
auxiliary data and unrecognized record types. Ignored trailing data remains
part of the checksum.

## Hop-by-Hop headers and checksum

If `hdr.Protocol` is 58, `Decode` treats the supplied payload as the ICMPv6
message. If it is zero, the payload starts with a Hop-by-Hop header. The decoder
uses `(Hdr Ext Len + 1) * 8` to locate the suffix and requires the Hop-by-Hop
Next Header field to be 58.

The checksum covers this IPv6 pseudo-header followed by the ICMPv6 suffix:

| Pseudo-header offset | Length | Field |
|---|---:|---|
| 0 | 16 | IPv6 Source Address |
| 16 | 16 | IPv6 Destination Address |
| 32 | 4 | ICMPv6 suffix length |
| 36 | 3 | Zero |
| 39 | 1 | Next Header (58) |

The Hop-by-Hop bytes are excluded from both the upper-layer length and checksum
body. Both header addresses must be IPv6 addresses.

## Timer codes

MLDv1 represents `MaxResp` as an unsigned 16-bit millisecond count. MLDv2 uses
milliseconds with these rules:

- Codes below 32768 are linear.
- Codes at or above 32768 decode as `(mant | 0x1000) << (exp + 3)`.

`Encode` requires an exact representation. Codes 32767 and 32768 represent
32.767 s and 32.768 s; 32.769 s is not representable and returns
`ErrMalformed`.

`QQIC` remains a raw wire code. It follows the IGMPv3 rule: values below 128
mean seconds directly, and larger values use a four-bit mantissa and a
three-bit exponent.

## Errors

Invalid IPv6 context, extension-header structure, checksums, fields, counts,
and lengths wrap `ErrMalformed`. Unknown message types and query versions wrap
`ErrUnsupported`. Encoded messages are limited to 65,535 octets.

## Sources

- RFC 2710 section 3: MLDv1 message layout, timer units, and checksum.
- RFC 3810 sections 5.1 and 5.2: MLDv2 queries, reports, timer codes, and
  address records.
- RFC 8200 sections 4.3 and 8.1: Hop-by-Hop Header Extension Length and the
  ICMPv6 pseudo-header.
