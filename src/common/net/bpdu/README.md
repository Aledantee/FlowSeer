# BPDU Codecs

Package `bpdu` provides codecs for IEEE 802.1D Spanning Tree Protocol (STP, RSTP,
MSTP) and Cisco Per-VLAN Spanning Tree Plus (PVST+ / SSTP) Bridge Protocol Data
Units.

Frames are exchanged over IEEE 802.3 LLC to the standard bridge group multicast
destination address `01:80:c2:00:00:00` or via LLC/SNAP framing to the Cisco
SSTP address `01:00:0c:cc:cc:cd`.

The codec operates on Go structures and produces `ethernet.Frame` values.

## Example

```go
package main

import (
	"fmt"
	"log"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
)

func main() {
	srcMAC := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
	b := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: srcMAC},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: srcMAC},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	b.SetRole(bpdu.RoleDesignated)

	frame, err := bpdu.Encode(b, srcMAC)
	if err != nil {
		log.Fatalf("encode BPDU: %v", err)
	}

	decoded, err := bpdu.Decode(frame)
	if err != nil {
		log.Fatalf("decode BPDU: %v", err)
	}

	fmt.Printf("Root: %s, Role: %s\n", decoded.RootID, decoded.Role())
}
```

## Error handling

Decode errors wrap `bpdu.ErrUnsupported`. Callers inspect the cause with
`errors.Is(err, bpdu.ErrUnsupported)`.

## Standards and limits

Package `bpdu` implements encoding and decoding based on IEEE Std 802.1Q-2003 (incorporating IEEE Std 802.1s-2002), with RSTP and MSTP wire structures cross-checked against Wireshark (`epan/dissectors/packet-bpdu.c`) and conformance tests from UNH-IOL.

### Sources and clauses

- IEEE Std 802.1Q-2003 clause 14.4: BPDU validation and decoding rules. Decodes version 3 or greater as MSTP when the body contains complete MSTI records, or as RSTP when truncated or when Version 1 Length is non-zero. Accepts legacy Configuration and TCN BPDUs with version numbers higher than 0 or 1 per Note 2.
- IEEE Std 802.1Q-2003 clause 14.6: Frame formats and field encodings for Configuration BPDUs, RST BPDUs, and MST BPDUs. Priority nibbles for MSTI records are encoded in bits 5 through 8 of octets 14 and 15 (IEEE 802.1Q-2003 clauses 14.6.1 d and e).
- IEEE Std 802.1Q-2003 clause 13.14: Bounds the number of MSTI records in a single BPDU to 64 (`MaxMSTIRecords`).
- UNH-IOL MSTP Conformance Test Suite (MSTP.op.1.3): Protocol version identifiers higher than 3 do not invalidate an MST BPDU.
- UNH-IOL RSTP Conformance Test Suite (RSTP.op.4.3): Hello Time of 0 is accepted and clamped to a 1-second minimum.

### Limits

- An MST BPDU can contain at most 64 MSTI records (`MaxMSTIRecords`). Payloads advertising more than 64 records are decoded as RST BPDUs.
- Received Hello Time values below 1 second are clamped to 1 second on decode.
- Encapsulation supports standard LLC destination `01:80:c2:00:00:00` and Cisco PVST+ LLC/SNAP destination `01:00:0c:cc:cc:cd` with protocol ID `0x010b` and trailing PVID TLV.
