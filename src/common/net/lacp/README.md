# LACPDU and Marker PDU Codec

Package `lacp` provides encoding and decoding for IEEE 802.1AX Link Aggregation
Control Protocol Data Units (LACPDUs) and Marker Protocol Data Units (Marker
PDUs). Frames are exchanged over IEEE 802.3 Slow Protocols (EtherType `0x8809`)
using the standard multicast destination address `01:80:c2:00:00:02`.

The codec operates on plain Go structures and produces `ethernet.Frame` values
with a fixed 110-octet LACPDU payload layout. `MarkerResponse` preserves the
received Marker PDU payload while changing its response type and addresses.

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
| 0 | 1 | Subtype | `0x01` for LACP |
| 1 | 1 | Version | `0x01` on transmit. Decode accepts the received value. |
| 2 | 1 | Actor TLV Type | `0x01` on transmit. Decode uses the fixed offsets without validating the type. |
| 3 | 1 | Actor TLV Length | 20 (`0x14`) octets |
| 4..5 | 2 | Actor System Priority | Priority of the actor system |
| 6..11 | 6 | Actor System ID | MAC address identifying the actor |
| 12..13 | 2 | Actor Key | Operational key assigned to the port |
| 14..15 | 2 | Actor Port Priority | Priority assigned to the actor port |
| 16..17 | 2 | Actor Port ID | Port identifier within the actor system |
| 18 | 1 | Actor State | Bitfield of state flags |
| 19..21 | 3 | Actor Reserved | Reserved octets, zeroed on transmit |
| 22 | 1 | Partner TLV Type | `0x02` on transmit. Decode uses the fixed offsets without validating the type. |
| 23 | 1 | Partner TLV Length | 20 (`0x14`) octets |
| 24..25 | 2 | Partner System Priority | Priority of the partner system |
| 26..31 | 6 | Partner System ID | MAC address identifying the partner |
| 32..33 | 2 | Partner Key | Operational key advertised by the partner |
| 34..35 | 2 | Partner Port Priority | Priority assigned to the partner port |
| 36..37 | 2 | Partner Port ID | Port identifier within the partner system |
| 38 | 1 | Partner State | Bitfield of partner state flags |
| 39..41 | 3 | Partner Reserved | Reserved octets, zeroed on transmit |
| 42 | 1 | Collector TLV Type | `0x03` on transmit. Decode uses the fixed offsets without validating the type. |
| 43 | 1 | Collector TLV Length | 16 (`0x10`) octets |
| 44..45 | 2 | Collector Max Delay | Maximum delay in tens of microseconds |
| 46..57 | 12 | Collector Reserved | Reserved octets, zeroed on transmit |
| 58 | 1 | Terminator or next TLV Type | `0x00` on transmit. Decode does not inspect this offset. |
| 59 | 1 | Terminator or next TLV Length | `0x00` on transmit. Decode does not inspect this offset. |
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

`Decode` rejects any frame that fails these structural invariants:

- EtherType differs from `EtherTypeSlowProtocols` (`0x8809`).
- Payload length is shorter than 110 octets.
- Subtype differs from `SubtypeLACP` (`0x01`).
- Actor, Partner, or Collector TLV length differs from 20, 20, or 16.

Rejections return an error wrapping the package sentinel `ErrUnsupported`.
Callers inspect the cause with `errors.Is(err, lacp.ErrUnsupported)` and
retrieve the offending field names from the error attributes. `AX` 6.4.12
forbids a Receive machine from validating the Version Number, TLV_type, and
Reserved fields. It permits validation of the Actor, Partner, Collector, and
Terminator lengths. `Decode` validates the Actor, Partner, and Collector
lengths, then reads the Version 1 actor, partner, and collector fields at their
fixed offsets. It ignores payload octets from offset 58 onward.

## Marker PDU responses

`MarkerResponse` accepts a Slow Protocols frame with subtype `SubtypeMarker`
(`0x02`), a Marker Information TLV type `0x01`, and length 16. It requires at
least 110 payload octets. The response copies the payload, changes the Marker
TLV type to `0x02`, sets the destination to `GroupAddress`, and sets the source
to the supplied address. The Version, port, system, transaction, pad,
terminator, reserved octets, tags, and EtherType remain unchanged. `AX` 6.5.4.2
leaves Version, Pad, and Reserved fields unvalidated. `AX` 6.5.3.3 permits the
response to reflect the ignored pad and reserved octets. `GroupAddress` is the
default `Protocol_DA` in the vendored
`spec/mib/ieee/IEEE8021-AX-MIB-202005290000Z.mib:2158`.

## Sources

- AX: IEEE P802.1AX-REV/D4.54, clauses 6.4.2, 6.4.12, 6.5.3.3, 6.5.4.2,
  and Figures 6-27 and 6-28. This unapproved draft is the reference for IEEE
  Std 802.1AX-2014. The published standard was not read, so equivalence is
  unverified:
  https://www.ietf.org/lib/dt/documents/LIAISON/liaison-2014-11-08-ieee-8021-rtg-completion-of-8021ax-rev-link-aggregation-to-ietf-routing-area-and-routing-area-wg-attachment-2.pdf.
- WS: Wireshark `packet-lacp.c` and `packet-marker.c`, fetched 2026-10-03:
  https://gitlab.com/wireshark/wireshark/-/raw/master/epan/dissectors/packet-lacp.c
  and https://gitlab.com/wireshark/wireshark/-/raw/master/epan/dissectors/packet-marker.c.
- CAP: Wireshark sample capture `lacp1.pcap.gz`, whose first frame supplies the
  literal LACPDU fixture in `lacp_test.go`:
  https://wiki.wireshark.org/uploads/__moin_import__/attachments/SampleCaptures/lacp1.pcap.gz.
- IEEE8023-LAG-MIB clause 7.3.2.1.20
  (`spec/mib/ieee/IEEE8023-LAG-MIB:73`).
- Open vSwitch `struct lacp_pdu` in `lib/lacp.c` on branch-3.3
  (https://raw.githubusercontent.com/openvswitch/ovs/branch-3.3/lib/lacp.c).
