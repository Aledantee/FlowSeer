# fabric

A Layer 2 network fabric composes virtual switches, hosts, and cables into
a simulated network. Execution advances synchronously in discrete steps.
Each step takes the earliest scheduled arrival, forwards it through the
destination switch, and queues copies onto connected cables.

## Example

The example builds two switches joined by a 300-meter cable with hosts `h1`
and `h2` in VLAN 10. The trunk uses multimode fiber because 300 m of twisted
pair is past reach. No port or host states its Ethernet facts and the host
cables state no medium, so the configuration assumes gigabit copper for them;
without that assumption every link would be `Unknown` and nothing would move.
A frame injected at `h1` traverses `sw1`, crosses the cable, traverses `sw2`,
and delivers to `h2`.

```go
package main

import (
	"fmt"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func main() {
	b1 := port.NewBuilder()
	b1.Add(port.Port{
		Name:        "1/1/1",
		Kind:        port.Physical,
		AdminStatus: port.Up,
		OperStatus:  port.Up,
	})
	b1.Add(port.Port{
		Name:        "1/1/24",
		Kind:        port.Physical,
		AdminStatus: port.Up,
		OperStatus:  port.Up,
	})
	ports1, err := b1.Build()
	if err != nil {
		panic(err)
	}

	b2 := port.NewBuilder()
	b2.Add(port.Port{
		Name:        "1/1/1",
		Kind:        port.Physical,
		AdminStatus: port.Up,
		OperStatus:  port.Up,
	})
	b2.Add(port.Port{
		Name:        "1/1/24",
		Kind:        port.Physical,
		AdminStatus: port.Up,
		OperStatus:  port.Up,
	})
	ports2, err := b2.Build()
	if err != nil {
		panic(err)
	}

	vid10 := vlan.ID(10)
	makeBridge := func() *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "prod"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/24": {Tagged: []vlan.ID{10}},
				},
			},
		}
	}

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	cfg := fabric.Config{
		Start: time.Unix(1700000000, 0),
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: ports1, Bridge: makeBridge()},
			"sw2": {Ports: ports2, Bridge: makeBridge()},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/24"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/24"},
				LengthMeters: 300,
				Medium:       fabric.MultimodeFiber,
			},
			{
				A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				B: fabric.Endpoint{Node: "h2"},
			},
		},
		PhyAssumption: &fabric.PhyAssumption{
			Medium: fabric.TwistedPair,
			Ethernet: phy.Ethernet{
				SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		panic(err)
	}

	now := time.Unix(1700000000, 0)
	_, err = fab.Inject(fabric.Injection{
		At:     now,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       macH2,
			Src:       macH1,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("hello"),
		},
	})
	if err != nil {
		panic(err)
	}

	steps := fab.Run(10)
	fmt.Printf("Completed in %d steps\n", steps)
}
```

## Traversal journey

Calling `Report()` returns an independent copy of the journey recorded for each
frame id. Mutating frames, packets, step input, output, or evidence lists, or
egress data in that copy does not change later reports. The entries record the
progression across devices and cables:

- `Injection`: Introduces the frame at `h1` at `t0`.
- `Crossing`: Host leg transmission across the 0-meter cable to `sw1:1/1/1`
  (serialization 672 ns at 1 Gbit/s for the 84 wire octets of a 60-octet frame,
  wait 0, latency 0 on a 0 m cable).
- `Hop`: Evaluates forwarding on `sw1:1/1/1` at `t0 + 672ns`. The entry's
  `Result` retains the switch's `*vswitch.ForwardResult`, preserving domain
  bridge outcomes and analysis readiness metadata. Because `macH2` is
  unknown, the bridge floods VLAN 10 out `1/1/24` with a C-TAG of VID 10.
- `Crossing`: Transmits the copy across the 300-meter trunk cable to
  `sw2:1/1/24` (704 ns serialization for 88 wire octets with the C-tag, 1494 ns
  propagation over 300 m at velocity factor 0.67).
- `Hop`: Evaluates forwarding on `sw2:1/1/24` at `t0 + 2870ns`. Flooding VLAN 10
  selects access port `1/1/1` and strips the C-TAG.
- `Arrival`: Records the frame reaching destination host `h2` at
  `t0 + 3542ns` (672 ns more on the 0 m host leg).
- `Delivery`: Records `h2` accepting the frame, since it is addressed to
  `macH2`. See [Host acceptance](#host-acceptance).

Time on a cable is two terms: serialization and propagation. Wire octets are
computed as `max(encoded, 60 + 4 per tag) + 24` (4 octets of FCS, 8 of
preamble and start delimiter, and 12 of interpacket gap; IEEE 802.3-2022 clause
4.4.2 for the gap). Serialization is `wire octets × 8 / rate` rounded up to the
nanosecond. Propagation is `length / (velocity factor × 299,792,458 m/s)`
rounded to the nanosecond. `Delay`, when set, replaces the propagation term and
nothing else.

A host's decision is recorded at the arrival time, and a host's cable end is
charged on the busy clock like any port.

Each entry names the endpoint it happened at in `Device` and `Port`; a host's
`Port` is empty, since a host has one unnamed port. `Cable` is the cable the
entry rests on. An injection carries its origin's cable, and a host's arrival
and decision carry the cable the frame came over.

Mirror copies have journeys of their own. `Journey.Mirror` names the mirror
configuration that made the copy, and `Journey.Parent` identifies the original
frame's journey. The copy's injection origin is the switch and mirror output
port, followed by its own crossings, delivery, or drop.

An ingress policer rejection happens before the forwarding pipeline and remains an
`EntryDrop`. Its `Result` records a traffic-layer drop step with a typed
`traffic.PolicerDecisionFact`, including the configured rate and burst, the frame's
wire octets, and the refusal. This keeps the pre-forward decision available to the
same semantic trace consumers as ordinary switch hops.

## Egress queues

Every transmitting endpoint has eight FIFO queues, one for each PCP. Normal
egress keeps the PCP from ingress classification even when the egress frame is
untagged. Protocol emissions, mirror copies, and host injections use the outer
C-tag's PCP, or PCP 0 when the frame has no outer C-tag.

An idle endpoint serves a lone frame immediately. When another frame arrival is
already scheduled for the same instant, the endpoint schedules a dequeue after
those arrivals instead. A dequeue chooses the highest eligible PCP and keeps
FIFO order within that PCP. It schedules another dequeue for the transmission
end while frames remain. `Snapshot.Queued` gives the pending count for each
non-empty endpoint queue, and `Snapshot.Busy` lists endpoints still serializing
after `Clock`.

A configured queue maximum rate controls start-to-start spacing. After a frame
starts, that PCP is next eligible at `start + ceil(wire bits / max rate)`. The
link still serializes the frame at its negotiated speed. If every pending PCP is
rate-limited, the endpoint schedules its dequeue at the earliest rate clock.
`Entry.Wait` is the transmission start minus its enqueue time, and `Entry.PCP`
records the selected priority.

For example, two tagged frames arriving together at `sw1:1/1/2`, PCP 0 first
and PCP 7 second, are both pending before the trunk dequeue. PCP 7 starts at
`t0`, takes 704 ns to serialize, and reaches `sw2` after the trunk's 1494 ns
propagation at `t0 + 2198ns`. PCP 0 starts at `t0 + 704ns` and reaches `sw2` at
`t0 + 2902ns`.

With PCP 0 on `sw1:1/1/24` limited to 100 Mbit/s, an 88-wire-octet tagged frame
opens the next PCP 0 turn 7040 ns after its start. Two untagged host frames
injected at `t0` and `t0 + 1ns` reach `sw1` at `t0 + 672ns` and
`t0 + 1344ns`. Their trunk copies reach `sw2` at `t0 + 2870ns` and
`t0 + 9910ns`; each still serializes in 704 ns at the 1 Gbit/s link rate. An
unlimited PCP 7 frame queued between those copies takes the free trunk before
the second PCP 0 frame.

## Media and reach

Each cable states a transmission medium (`TwistedPair`, `MultimodeFiber`,
`SinglemodeFiber`, or `Twinax`) or leaves it `MediumUnspecified`, the zero
value and the case of an unresolved transceiver. The length must be finite and
non-negative, and a length of 0 is a stated 0 m cable. The medium defines the
signal velocity factor and the maximum reach per link speed:

| Medium            | Factor | 10 Mbps | 100 Mbps | 1 Gbps | 10 Gbps |
| ----------------- | ------ | ------- | -------- | ------ | ------- |
| `TwistedPair`     | 0.64   | 100 m   | 100 m    | 100 m  | 100 m   |
| `MultimodeFiber`  | 0.67   | -       | -        | 550 m  | 300 m   |
| `SinglemodeFiber` | 0.67   | -       | -        | 5 km   | 10 km   |
| `Twinax`          | 0.77   | -       | -        | -      | 15 m    |

Velocity factors derive from https://en.wikipedia.org/wiki/Velocity_factor
(Cat 5e for twisted pair; 10BASE-FL and 100BASE-FX minimum for fiber) and
https://en.wikipedia.org/wiki/Optical_fiber (200,000 km/s in glass). The twinax
factor is a coaxial proxy (generic RG-8 foamed-dielectric coaxial pair) because
direct-attach cabling data was not found. Reach rows derive from
https://en.wikipedia.org/wiki/Gigabit_Ethernet,
https://en.wikipedia.org/wiki/10_Gigabit_Ethernet, and
https://en.wikipedia.org/wiki/Twinaxial_cabling. No row exists above 10 Gbps
on any medium.

`Medium.Reach(length, speed)` gives `ReachInRange` when the length is at or
below the row, `ReachExceeded` above it, and `ReachUnknown` when the medium is
unspecified or has no row for the speed. A missing row is unknown, not zero:
100 m of multimode fiber at 100 Mbps is `ReachUnknown`.

Link resolution checks reach before negotiation. The candidates are the speeds
negotiation could select, at or below the cable's `TopSpeedBPS`: the forced
speeds when an end forces one, otherwise the speeds both ends report.

- A forced speed, or every candidate, `ReachExceeded` leaves both ends `Down`
  with `reach-exceeded`.
- An exceeded candidate is removed, and negotiation is capped at the highest
  remaining one. 400 m of multimode fiber between two 1 and 10 Gbps ends
  negotiates 1 Gbps.
- When the speed negotiation picks, or a higher remaining candidate, is
  `ReachUnknown`, both ends are `Unknown` with `reach-unknown`. A 2 m cable of
  unspecified medium between two gigabit ends is `Unknown`, never
  `reach-exceeded`.
- When both ends report the same nonzero observed speed, the observed rule in
  `phy` resolves the link anyway. `Delay` sets timing only and never resolves
  reach.

An unspecified medium has no velocity factor, so a link that runs over one on
observations alone propagates in zero time unless the cable states `Delay`.
`Fabric.Metadata` reports that link with `propagation-unknown`.

## Host acceptance

A frame that reaches a host appends `Arrival`, and the host then decides. If
it accepts, the journey appends `Delivery` and the frame joins
`Journey.Deliveries`. Otherwise the journey appends `Rejection` with a reason,
and `Deliveries` never holds the frame. Each decision entry carries a
`trace.Step` on layer `host` with op `filter`. The host is its subject, its
rule names the check and clause that decided, and its inputs are the facts
read up to that point.

The checks run in this order, and the first one that decides ends the run:

1. VLAN form (`host.vlan.form`). A host with no `VLAN` takes untagged frames
   and a single VID 0 priority C-TAG. A host with a `VLAN` takes only a single
   C-TAG with that VID. Any other stack is refused with
   `host-vlan-not-accepted`.
2. Destination MAC. The host takes its own address and broadcast. It takes a
   group address when `Accept.AllMulticast` is set or `Accept.Multicast` lists
   it. A host with an IPv4 address also takes `01:00:5e:00:00:01`, the MAC of
   224.0.0.1 ([RFC 1112](https://www.rfc-editor.org/rfc/rfc1112.html) §6.4,
   §7.2). A host with an IPv6 address also takes `33:33:00:00:00:01` for
   ff02::1, and `33:33:ff` followed by the low 24 bits of each own address,
   the MAC of its solicited-node group
   ([RFC 4291](https://www.rfc-editor.org/rfc/rfc4291.html) §2.7.1, §2.8;
   [RFC 2464](https://www.rfc-editor.org/rfc/rfc2464.html) §7). Anything else
   is refused with `host-unicast-not-addressed` or
   `host-multicast-not-accepted`. `Accept.Promiscuous` takes any MAC and skips
   step 3.
3. IP destination. This step runs only for a host with an IP stack and a frame
   with an IPv4 or IPv6 EtherType; any other EtherType was taken at step 2.
   The packet is taken when it is addressed to an own address, to
   255.255.255.255 or the directed broadcast of an own IPv4 prefix, or to a
   group whose MAC step 2 takes. A /31 or /32 prefix has no directed broadcast
   ([RFC 3021](https://www.rfc-editor.org/rfc/rfc3021.html) §2.2). Anything
   else is refused with `host-ip-not-addressed`.

A header that does not decode, or whose version differs from the EtherType,
leaves acceptance unknown. The journey ends `Unresolved` with
`host-ip-header-undecodable`, delivers nothing, and carries an `Incomplete`
issue on its `analysis.JourneyScope`.

For example, a unicast frame to `02:00:00:00:00:09` that floods to `h2`, whose
address is `02:00:00:00:00:02`, ends in `Rejection` with
`host-unicast-not-addressed` under rule `host.mac.unicast_not_addressed`, and
`Deliveries` stays empty. With `Accept: fabric.HostAccept{Promiscuous: true}`
on `h2`, the same frame ends in `Delivery` under `host.mac.promiscuous`.

A group frame whose MAC step 2 takes can still be refused at step 3. A packet
to 224.0.0.5 in a frame to `01:00:5e:00:00:01` passes the MAC check on the
all-hosts clause. But 224.0.0.5 maps to `01:00:5e:00:00:05`, which the host
does not take, so the packet is refused.

`Diff` reports `accept.promiscuous` and `accept.all_multicast` as host fields,
and each multicast MAC added or removed as `accept.multicast.<mac>`.
Validation refuses a unicast entry in `Accept.Multicast`, naming its submitted
position, such as `hosts.h1.accept.multicast.0`.

## Hosts with an IP stack and address assignment

A host configured with `IP *HostIP` translates to an internal routing layer
operating with VRF `default` and a single routed port carrying the host's
assigned MAC and configured IP address prefixes. When an injected packet is
originated:

- Source address selection chooses among the host's interface addresses
  matching the destination family by longest matching prefix, falling back
  to the first configured address of that family.
- Datagrams transmit with hop limit 64 direct on a connected prefix, and
  through the default gateway when destination is off-link.
- Link-layer encapsulation looks up destination or gateway addresses in the
  static neighbor table.

`Inject` validates packet injections and refuses them with an error when:

- `Origin` names a switch or a host without an IP stack.
- `Origin.Port` is non-empty.
- `Frame` specifies any fields alongside `Packet`.
- Packet origination encounters `no-route` (destination off every prefix with
  no gateway) or `neighbor-miss` (destination or gateway missing from neighbors),
  naming the address and reason without creating a journey.

MAC addresses left zero in a fabric configuration are assigned by `New`
and `Derive` before any switch or host stack is built. The allocator walks switch names
then host names in sorted order, assigning the first locally administered
unicast MAC `netaddr.Local(n)` (`02:00:00:` followed by n big-endian, starting
from 1) that is not explicitly used by any switch base MAC, host address, routed
interface MAC, or spanning tree address. `Fabric.Config()` returns the filled
values, and `Derive` preserves existing assignments for nodes whose new
configuration leaves them zero.

## Snapshot after one step

Calling `fab.Run(1)` on the scenario processes the initial arrival at `sw1`
and leaves the transmission in flight:

- `Clock`: Set to `t0 + 672ns`, the arrival timestamp of the completed step.
- `Queue`: Holds one pending arrival at `sw2:1/1/24` scheduled at `t0 + 2870ns`.
- `Busy`: Holds `sw1:1/1/24` until `t0 + 1376ns`.
- `Devices["sw1"]`: FDB holds dynamic entry `(10, macH1) -> 1/1/1`.
- `Groups` for each device: Holds multicast membership entries by snooped VLAN.
- `RouterPorts` for each device: Holds static and learned multicast router ports
  by snooped VLAN.
- `RelayCounters` for `Devices["sw1"]`: Reads `Learned` 1 (the example learns h1's MAC on the first step).
- `Devices["sw2"]`: FDB contains no entries.

## Protocol traffic and wake-ups

`Config.Start` is the fabric's clock when it is built: every protocol layer
hears its links at `Start`, the first proposals are dated `Start`, and a
fabric with a spanning tree switch refuses a zero `Start`, since its hellos
would otherwise fire in year 1, ahead of any frame a caller injects.

Spanning tree and other internal protocols schedule state transitions and
frame emissions through the run's arrival queue. Each device holds at most one
wake entry in the queue (`Arrival.Wake` true, port empty, frame ID 0, sequence
0). Because sequence 0 sorts ahead of positive frame sequences, a wake executes
before frames scheduled for the same instant. `Step` returns `EntryWake` for
these timer events without appending to any frame journey. `Mcheck(node, port)`
is the one out-of-band protocol call: it runs the switch's management check
at the current clock and queues the emission and the next wake like a step.

Frames emitted by switch layers during wake-ups or forwarding cross cables and
hubs as journeys marked `Protocol`. Periodic hellos ensure the queue never
drains; callers supply a step budget to `Run(n)` and evaluate topology
convergence by checking whether consecutive snapshots report identical roles
and forwarding states across all ports.

## Link operational state rule

Switch port operational states derive from the topology during `New`. The
configured `OperStatus` is kept: `Config()` and `Spec()` return it, and the
derived state is in `Switch(name).Ports()` and `Links()`.

Every non-LAG switch port falls into one of three cases:

- Cabled: the link below decides the state.
- Listed in `Config.Uncabled`: `Down`, and `Unlinked` lists it with `no-cable`.
  An entry must name an existing non-LAG switch port that no cable and no
  other entry names.
- Neither: `Unknown`, since nothing says what is attached. `Unlinked` lists it
  with `adjacency-unresolved`.

A cabled link resolves in this order:

1. A severed cable (`FaultCut`) leaves both endpoints `Down` with `cut`.
2. A unidirectional failure (`FaultDeadAToB` or `FaultDeadBToA`) leaves both
   ends `Down` with `dead-direction` when either end auto-negotiates. When both
   force their setting the link resolves on; when a mode is unreported it is
   `Unknown`.
3. When either end is administratively disabled both are `Down`: the disabled
   end reads `admin-down`, the other `peer-down`.
4. Reach, as in [Media and reach](#media-and-reach).
5. Two-ended negotiation (`phy.Negotiate`) over each end's `phy.Ethernet`: a
   switch port's from its `Phy` configuration, a host's from `Host.Ethernet`.
   `LinkFailed` is `Down` with its reason (`speed-mismatch`), `LinkUnknown` is
   `Unknown` (`capability-unknown`), and `LinkUnsupported` is `Unknown` with
   `forced-against-auto-unmodeled`. A duplex mismatch stays on the link's
   `Speed.Reason`.

For a Link Aggregation Group (LAG), the switch's aggregation layer decides the
member and the LAG's state, and the fabric reports member links to it.

Unreported facts stay unknown unless the configuration opts into
`Config.PhyAssumption`. It holds a medium and an Ethernet profile. It fills
only what an end or cable leaves unreported: empty supported speeds, an unknown
auto-negotiation capability, a nil `Setting`, or an unspecified medium. The
filled values appear in `Links()` (the link's `Cable` and each end's
`Ethernet`), while `Config()`, `Spec()`, and `Diff` show only the
`PhyAssumption` field. The assumption is a standards default, so it runs only
on the record: every link it filled carries one assumption naming the facts.

## Topology metadata

`Fabric.Metadata()` returns trust metadata over the whole analysis for the
links as they stand, so a later `SetFault` changes it:

| Code                              | Scope | Status      | Raised when                                        |
| --------------------------------- | ----- | ----------- | -------------------------------------------------- |
| the link's reason                 | link  | Incomplete  | the link is `Unknown`                               |
| `forced-against-auto-unmodeled`   | link  | Unsupported | negotiation of the two modes is unmodeled          |
| `propagation-unknown`             | link  | Incomplete  | an `Up` link has no medium and no `Delay`          |
| `adjacency-unresolved`            | port  | Incomplete  | no cable and no `Uncabled` entry names the port     |
| `oper-status-conflict`            | port  | Incomplete  | a configured `Up` or `Down` differs from a derived `Up` or `Down` |
| `observed-speed-conflict`         | port  | Incomplete  | an end observed a speed its negotiated link lacks  |

A link's scope is `analysis.LinkScope` keyed by its cable's `Diff` subject key,
such as `h1:-sw1:1/1/1`. A host end's port scope is its node scope. An unset or
`Unknown` status observes nothing and never conflicts, and neither does a
derived `Unknown`. The configured value is kept for the reader, but the
derived one executes.

`ConstructionSpec.Evidence` is the catalog that `Cable.Evidence` and
`Uncabled.Evidence` references resolve in; a reference outside it is invalid.
Issues cite the references of the cable or entry they rest on. Evidence is not
behavior, so `Diff` and `DiffSpecs` report no change for it, while `Equal`
compares it.

A valid host injection onto a link that is not `Up` is not an error. On a
`Down` link the journey records an `EntryDrop` with the link's reason; on an
`Unknown` link, an `EntryUnresolved`. Both carry the host's cable and transmit
nothing.

## Journey metadata

`Journey.Metadata` is evaluated over the whole analysis, but it holds only what
the journey depended on. `Fabric.Metadata` describes every link and port,
while a journey's metadata describes the path its frame took.

Each entry is captured when it is recorded. Its dependencies are its endpoint
(a host's node scope, a switch's port scope), its cable, and, for a hop, the
ports and scopes its forwarding result consulted, together with the link of
each such port. The journey keeps these:

- every issue and assumption of each hop result;
- every `Fabric.Metadata` issue and assumption whose scope overlaps a
  dependency;
- the `host-ip-header-undecodable` issue of an acceptance a host could not
  decide.

A consulted port counts even when nothing egresses it. A flood that skips an
`Unknown` port still consulted that port, so the journey carries the port's
link issue: the flood could have reached one more host. A known-unicast frame
consults only its ingress and egress ports and carries none of the issues of
the links it neither crossed nor consulted.

The metadata is fixed once the entry is recorded. A `SetFault` that later cuts
a link whose `propagation-unknown` issue a journey picked up leaves that
journey's metadata as it was, while `Fabric.Metadata` drops the issue.

## Fault kinds

Cables support deterministic defect configurations:

| Kind              | Effect                                           |
| ----------------- | ------------------------------------------------ |
| `None`            | Unimpaired transmission                          |
| `Cut`             | Physical severance; both endpoints remain Down   |
| `DeadAToB`        | Blocks transmission from endpoint A to B         |
| `DeadBToA`        | Blocks transmission from endpoint B to A         |
| `LoseEveryNth`    | Drops every Nth frame crossing the cable         |
| `LoseSequence`    | Drops frames matching 1-based crossing positions |
| `CorruptEveryNth` | Marks every Nth crossing damaged; recipient drop |

`N` is active only for the two every-Nth kinds and must be greater than zero.
`Sequence` is active only for `LoseSequence`; it must be non-empty and every
position must be at least one. Validation rejects parameters on other variants.
Normalization clears inactive parameters and sorts and deduplicates an active
sequence.

`SetFault(a, b Endpoint, fault Fault) error` alters a cable defect mid-run at
the current clock. The fabric re-resolves the link, updates each endpoint's
operational status on its switch, and notifies active spanning tree layers of
the link change. Both endpoints must identify an existing cable.

## Queue ordering

Simulation arrivals follow a deterministic total order in the queue:

1. Arrival time (`At`) in ascending chronological order.
2. Arrival kind: switch wakes, frame arrivals, then egress dequeues.
3. Injection sequence number (`Seq`), which every forwarding copy of a frame keeps.
4. Destination device name.
5. Destination port name.

A run therefore replays in the same order every time.

## Reasons

The package declares reasons for link failures and frame discards:

| Reason           | Meaning                                           |
| ---------------- | ------------------------------------------------- |
| `cut`            | Cable is physically severed                       |
| `peer-down`      | Peer interface is administratively disabled       |
| `admin-down`     | This interface is administratively disabled       |
| `no-cable`       | Switch port is listed as uncabled                 |
| `adjacency-unresolved` | Switch port has no cable and no uncabled entry |
| `reach-unknown`  | Medium reach is unknown at a candidate speed      |
| `dead-direction` | Defect in one direction prevents auto-negotiation |
| `reach-exceeded` | Cable length exceeds medium reach for speed        |
| `cable-loss`     | Configured cable fault dropped frame in transit   |
| `bad-frame`      | Frame was corrupted during cable transit          |
| `host-vlan-not-accepted` | Host does not accept the frame's tag stack |
| `host-unicast-not-addressed` | Unicast frame is addressed to another MAC |
| `host-multicast-not-accepted` | Host does not accept the group MAC |
| `host-ip-not-addressed` | Packet is not addressed to the host |
| `host-ip-header-undecodable` | Host cannot decode the IP header it must check |
| `policed`        | Ingress frame exceeded the port's token bucket     |

## Concurrency contract

A `Fabric` is not safe for concurrent use. Simulators mutate internal clock
timestamps, queues, forwarding entries, and interface counters during execution.
Callers running concurrent simulations must create separate `Fabric` instances.
