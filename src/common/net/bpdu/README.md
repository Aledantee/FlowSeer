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
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
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
