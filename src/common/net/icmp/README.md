# ICMP Codec

Package `icmp` decodes the four-octet header common to ICMPv4 (RFC 792) and
ICMPv6 (RFC 4443 section 2.1) messages. It has no encoder: nothing in this
tree originates an ICMP message, and an encoder without a producer is
untested surface.

```go
package main

import (
	"fmt"
	"log"

	"go.aledante.io/FlowSeer/src/common/net/icmp"
)

func main() {
	echoRequest := []byte{0x08, 0x00, 0xf7, 0xff, 0x00, 0x00, 0x00, 0x00}

	h, payload, err := icmp.Decode(echoRequest)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(h.Type, h.Code, len(payload))
}
```

## Header layout

| Offset | Length | Field |
|---|---:|---|
| 0 | 1 | Type |
| 1 | 1 | Code |
| 2 | 2 | Checksum |

Both protocols use these same three fields at the same offsets; only the
`Type` value space differs (ICMPv4's 8 is an echo request, ICMPv6's 128 is
the same message). `Decode` reads them without knowing which protocol
carried the message, so a caller that needs to tell them apart supplies the
IP version from the enclosing header.

## Errors and what this decoder skips

`Decode` returns `ErrMalformed` for a message shorter than 4 octets.
`Checksum` is the transmitted value as read; `Decode` does not recompute or
verify it, and it does not parse any type-specific body (the byte range
after the header, RFC 792's "rest of header" and RFC 4443's message body).

## Sources

- RFC 792: ICMPv4 message format.
- RFC 4443 section 2.1: ICMPv6 message general format.
