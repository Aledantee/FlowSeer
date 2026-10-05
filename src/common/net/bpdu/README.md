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

## Wire format

`Decode` classifies a frame by IEEE Std 802.1Q-2003 14.4 (a public copy is at
<https://bittwist.sourceforge.io/doc/802.1Q-2003.pdf>), counting octets from the
Protocol Identifier. Type `0x00` with 35 octets or more is a Configuration BPDU
and type `0x80` with 4 or more is a TCN, whatever the version. Type `0x02` with
version 3 or above is an MST BPDU when it holds 102 octets or more, a Version 1
Length of 0, and a Version 3 Length naming 0 to 64 whole MSTI records. Any other
such frame with 35 octets or more is an RST BPDU. A payload that differs in
length from the Version 3 Length it names is refused, which 14.4 does not
address. `Decode` reads a Hello Time of zero as zero.

An MSTI record carries its bridge priority and port priority in bits 5 to 8 of
one octet each (14.6.1 d and e). `Encode` sends bits 1 to 4 as zero, `Decode`
ignores them, and `MSTIRecord.BridgePriority` and `PortPriority` hold the octet
with those bits clear. `MaxMSTIRecords` is 64 (13.14).

`TestMSTFixtureDecodes` and `TestMSTFixtureEncodes` check an MST BPDU with two
MSTI records against a byte literal written octet by octet from Figures 14-1
and 14-2. Wireshark's `packet-bpdu.c` reads the same offsets. No device capture
backs it.
