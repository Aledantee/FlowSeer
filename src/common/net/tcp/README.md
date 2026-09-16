# TCP Codec

Package `tcp` decodes TCP segment headers (RFC 9293 section 3.1). It has no
encoder: nothing in this tree originates a TCP segment, and an encoder
without a producer is untested surface.

```go
package main

import (
	"fmt"
	"log"

	"go.aledante.io/FlowSeer/src/common/net/tcp"
)

func main() {
	segment := []byte{
		0x00, 0x50, 0x1f, 0x90, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x02,
		0x50, 0x12, 0xff, 0xff, 0x00, 0x00, 0x00, 0x00,
	}

	h, payload, err := tcp.Decode(segment)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(h.SrcPort, h.DstPort, h.Flags.Has(tcp.SYN|tcp.ACK), len(payload))
}
```

## Header layout

| Offset | Length | Field |
|---|---:|---|
| 0 | 2 | Source Port |
| 2 | 2 | Destination Port |
| 4 | 4 | Sequence Number |
| 8 | 4 | Acknowledgment Number |
| 12 | 4 bits | Data Offset, in 32-bit words |
| 13 | 1 | Control bits (`Flags`) |
| 14 | 2 | Window |

`Decode` stops at the fixed header; it does not parse options. It returns
the payload starting at `DataOffset * 4`, which is where options end and
the segment data begins.

## Flags

`Flags` is the control-bit octet at offset 13, one bit per name: `FIN`,
`SYN`, `RST`, `PSH`, `ACK`, `URG`, `ECE`, `CWR`. `Flags.Has(want)` reports
whether every bit in `want` is set, so a caller checking more than one flag
writes `h.Flags.Has(tcp.SYN | tcp.ACK)` instead of two separate comparisons.

## Errors and what this decoder skips

`Decode` returns `ErrMalformed` for a segment shorter than the 20-octet
fixed header, a data offset below 5 (the minimum header size in 32-bit
words), or a data offset that runs past the end of the buffer. It does not
verify the checksum; nothing downstream of a switch's forwarding path needs
that check, and RFC 9293 leaves checksum offload to the sending stack
anyway.

## Sources

- RFC 9293 section 3.1: TCP header format and control bits.
- RFC 3168: the ECN-Echo and CWR control bits.
