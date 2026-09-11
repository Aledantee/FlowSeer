# LACPDU Codec

Package `lacp` provides encoding and decoding for IEEE 802.1AX Link Aggregation
Control Protocol Data Units (LACPDUs). Frames are exchanged over IEEE 802.3
Slow Protocols (EtherType `0x8809`) using the standard multicast destination
address `01:80:c2:00:00:02`.

The codec operates on plain Go structures and produces `ethernet.Frame` values
with a fixed 110-octet payload layout.

## Example

```go
package main

import (
	"fmt"
	"log"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
)

func main() {
	srcMAC := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
	pdu := lacp.PDU{
		Actor: lacp.Info{
			SystemPriority: 32768,
			SystemID:       srcMAC,
			Key:            1,
			PortPriority:   32768,
			PortID:         1,
			State:          lacp.StateActive | lacp.StateShortTimeout | lacp.StateAggregation,
		},
		Partner: lacp.Info{
			State: lacp.StateDefaulted,
		},
		CollectorMaxDelay: 0,
	}

	frame := lacp.Encode(pdu, srcMAC)
	wireBytes, err := frame.Encode()
	if err != nil {
		log.Fatalf("encode Ethernet frame: %v", err)
	}

	receivedFrame, err := ethernet.Decode(wireBytes)
	if err != nil {
		log.Fatalf("decode Ethernet frame: %v", err)
	}

	decodedPDU, err := lacp.Decode(receivedFrame)
	if err != nil {
		log.Fatalf("decode LACPDU: %v", err)
	}

	fmt.Printf("Actor Port: %d, Partner State: 0x%02x\n",
		decodedPDU.Actor.PortID, decodedPDU.Partner.State)
}
```

## Frame layout

The wire layout matches Open vSwitch's `struct lacp_pdu` (110 octets total).
All multi-octet integer fields use network byte order (big-endian).

| Offset | Length | Field | Value / Description |
|---|---|---|---|
| 0 | 1 | Subtype | Always `0x01` for LACP |
| 1 | 1 | Version | Always `0x01` for version 1 |
| 2 | 1 | Actor TLV Type | `0x01` |
| 3 | 1 | Actor TLV Length | 20 (`0x14`) octets |
| 4..5 | 2 | Actor System Priority | Priority of the actor system |
| 6..11 | 6 | Actor System ID | MAC address identifying the actor |
| 12..13 | 2 | Actor Key | Operational key assigned to the port |
| 14..15 | 2 | Actor Port Priority | Priority assigned to the actor port |
| 16..17 | 2 | Actor Port ID | Port identifier within the actor system |
| 18 | 1 | Actor State | Bitfield of state flags |
| 19..21 | 3 | Actor Reserved | Reserved octets, zeroed on transmit |
| 22 | 1 | Partner TLV Type | `0x02` |
| 23 | 1 | Partner TLV Length | 20 (`0x14`) octets |
| 24..25 | 2 | Partner System Priority | Priority of the partner system |
| 26..31 | 6 | Partner System ID | MAC address identifying the partner |
| 32..33 | 2 | Partner Key | Operational key advertised by the partner |
| 34..35 | 2 | Partner Port Priority | Priority assigned to the partner port |
| 36..37 | 2 | Partner Port ID | Port identifier within the partner system |
| 38 | 1 | Partner State | Bitfield of partner state flags |
| 39..41 | 3 | Partner Reserved | Reserved octets, zeroed on transmit |
| 42 | 1 | Collector TLV Type | `0x03` |
| 43 | 1 | Collector TLV Length | 16 (`0x10`) octets |
| 44..45 | 2 | Collector Max Delay | Maximum delay in tens of microseconds |
| 46..57 | 12 | Collector Reserved | Reserved octets, zeroed on transmit |
| 58 | 1 | Terminator TLV Type | `0x00` |
| 59 | 1 | Terminator TLV Length | `0x00` |
| 60..109 | 50 | Reserved | Trailing padding, zeroed on transmit |

## State bits

State flags map to the `LacpState` textual convention in `IEEE8023-LAG-MIB`:

| Bit | Mask | Constant | MIB Bit Name | Semantics |
|---|---|---|---|---|
| 0 | `0x01` | `StateActive` | `lacpActivity` | Active LACP participation |
| 1 | `0x02` | `StateShortTimeout` | `lacpTimeout` | Short (fast, 1s) periodic transmit timeout |
| 2 | `0x04` | `StateAggregation` | `aggregation` | Link is aggregatable |
| 3 | `0x08` | `StateSynchronization` | `synchronization` | Allocation is synchronized with partner |
| 4 | `0x10` | `StateCollecting` | `collecting` | Port collects incoming traffic |
| 5 | `0x20` | `StateDistributing` | `distributing` | Port distributes outgoing traffic |
| 6 | `0x40` | `StateDefaulted` | `defaulted` | Using default administrative partner info |
| 7 | `0x80` | `StateExpired` | `expired` | Receive state machine timer expired |

## Validation and error handling

`Decode` rejects any frame that fails basic structural invariants:

- EtherType differs from `EtherTypeSlowProtocols` (`0x8809`).
- Payload length is shorter than 110 octets.
- Subtype differs from 1.
- Version differs from 1.
- Actor TLV is not type 1, length 20.
- Partner TLV is not type 2, length 20.
- Collector TLV is not type 3, length 16.
- Terminator TLV is not type 0, length 0.

Rejections return an error wrapping the package sentinel `ErrUnsupported`. Callers
inspect the cause with `errors.Is(err, lacp.ErrUnsupported)` and retrieve the
offending field names from the error attributes.

## Sources

- IEEE 802.1AX-2008 clause 6.4.2 (LACPDU structure).
- IEEE 802.3-2008 clause 57 (Slow Protocols and group address `01:80:c2:00:00:02`).
- IEEE8023-LAG-MIB clause 7.3.2.1.20 (`spec/mib/ieee/IEEE8023-LAG-MIB:73`).
- Open vSwitch `struct lacp_pdu` in `lib/lacp.c` on branch-3.3 (https://raw.githubusercontent.com/openvswitch/ovs/branch-3.3/lib/lacp.c).
