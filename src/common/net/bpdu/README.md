# BPDU Codecs

Package `bpdu` provides codecs for IEEE 802.1D Spanning Tree Protocol (STP, RSTP,
MSTP) and Cisco Per-VLAN Spanning Tree Plus (PVST+ / SSTP) Bridge Protocol Data
Units.

Frames are exchanged over IEEE 802.3 LLC to the standard bridge group multicast
destination address `01:80:c2:00:00:00` or via LLC/SNAP framing to the Cisco
SSTP address `01:00:0c:cc:cc:cd`.

SSTP supports three wire shapes. Configuration and RST BPDUs share a 50-octet
LLC/SNAP layout with an originating-VLAN TLV. Configuration uses version 0 and
wire type `0x00`, while RST uses version 2 or later and wire type `0x02`. A TCN
uses the same LLC/SNAP header with a 12-octet length, wire type `0x80`, no TLV,
and Ethernet minimum-frame padding. TCN decoding returns VLAN 0 because its
VLAN scope comes from ingress classification.

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

Package `bpdu` implements encoding and decoding based on IEEE Std 802.1Q-2003 (incorporating IEEE Std 802.1s-2002), with RSTP and MSTP wire structures cross-checked against Wireshark (`epan/dissectors/packet-bpdu.c`).

### Sources and clauses

- IEEE Std 802.1Q-2003 clause 14.4: BPDU validation and decoding rules. A version 3 or later type 2 frame of 35 to 101 octets is RST, whatever its length fields say. At 102 octets, Version 1 Length 0 and Version 3 Length 64 select MST with no records, and every other length pair selects RST. At 103 octets or more, Version 1 Length 0 and a Version 3 Length naming 0 to 64 records select MST, with octets after those records ignored. This package refuses a frame whose named records are absent. Configuration and TCN BPDUs are accepted for every version when their type and minimum length match.
- IEEE Std 802.1Q-2003 clause 14.6: Frame formats and field encodings for Configuration BPDUs, RST BPDUs, and MST BPDUs. Priority nibbles for MSTI records are encoded in bits 5 through 8 of octets 14 and 15 (IEEE 802.1Q-2003 clauses 14.6.1 d and e).
- IEEE Std 802.1Q-2003 clause 13.14: Bounds the number of MSTI records in a single BPDU to 64 (`MaxMSTIRecords`).

### PVST and SSTP observation model

PVST and SSTP behavior is modelled on observation. [`CISCO`][CISCO] describes
loop guard blocking per VLAN. [`PVID`][PVID] describes the IEEE destination and
SSTP address used for VLAN-specific BPDUs. [`EXT`][EXT] describes tagged TCN
BPDUs sent per VLAN. [`WS`][WS]
provides the dissector offsets for the SSTP body and Configuration TLV. These
sources establish the observation model, not a claim that every vendor uses
the same implementation.

[CISCO]: https://www.cisco.com/c/en/us/support/docs/lan-switching/spanning-tree-protocol-stp-8021d/218321-configure-stp-with-loop-guard-and-bpdu-s.html
[PVID]: https://web.archive.org/web/20241113152806/https://www.cisco.com/c/en/us/support/docs/lan-switching/spanning-tree-protocol/24063-pvid-inconsistency-24063.html
[EXT]: https://documentation.extremenetworks.com/slxos/SW/20xx/l2config/GUID-FC3E8C8E-3930-4777-825D-3ECD12328F51.shtml
[WS]: https://gitlab.com/wireshark/wireshark/-/raw/master/epan/dissectors/packet-bpdu.c

### Limits

- An MST BPDU can contain at most 64 MSTI records (`MaxMSTIRecords`). Payloads advertising more than 64 records are decoded as RST BPDUs when the length band permits the RST reading.
- Encapsulation supports standard LLC destination `01:80:c2:00:00:00` and Cisco PVST+ LLC/SNAP destination `01:00:0c:cc:cc:cd` with protocol ID `0x010b`. Configuration and RST shapes carry the trailing PVID TLV. TCN shapes do not carry a TLV.
