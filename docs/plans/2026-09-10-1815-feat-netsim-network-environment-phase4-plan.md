---
title: Network Simulation Environment, Phase 4: Routing Capability - Plan
type: feat
date: 2026-09-10
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-10-1815-feat-netsim-network-environment-plan.md
---

# Network Simulation Environment, Phase 4: Routing Capability - Plan

> Re-planned on 2026-09-11 against the tree phases 1 through 3 left.

## Goal

The virtual switch gains the `routing` capability: routed interfaces,
either a VLAN interface or a routed port, with addresses, a forwarding
table of connected and static routes, and a neighbor table, each held per
VRF, so a frame addressed to the device on one interface is routed and
re-emitted on another with the trace naming each step, and two VRFs with
the same prefixes never see each other. A device with routed ports alone
and no bridge is a router. Hosts gain an IP stack that builds the frame
for a destination address itself, through a gateway when the destination
is off its prefix. Every MAC a configuration leaves out is assigned,
unique within the simulation. The means is one `routing` package under
`vswitch` that decides over plain values, a relay split into an ingress
half and an egress half with the routing stage between them, and one IP
header codec under `src/common/net/ip` for both families, for every tree
to use. The phase is wrong if the first consumer needs a routing protocol,
an ARP or ND exchange, or a NAT, which are not tables this device holds.

## Decisions

The parent's Decisions hold: layer 3 is a capability of the same device,
addresses are `net/netip` values, the simulator's core holds plain Go
types and `netmodel` alone imports generated code. The landed phases are
the base: `Switch.Forward` intercepts a BPDU ahead of the relay and
otherwise answers with the relay's `bridge.Result`; the relay classifies,
learns through the gate, looks the destination up, and builds one egress
frame per port with the port's tag form; the fabric runs a total-ordered
queue and records a `Journey` per frame; a `Host` is one address and an
optional VLAN. This phase adds:

- `src/common/net/ip` is the one header codec for both families, as
  `ethernet` is for frames. `ip.Header{Src, Dst netip.Addr; HopLimit
  uint8; Protocol uint8; TrafficClass uint8; V4 *V4; V6 *V6}` holds what
  both headers share under one name each (the IPv4 TTL is `HopLimit`,
  the IPv4 protocol and the IPv6 next header are `Protocol`, the IPv4 TOS
  octet and the IPv6 traffic class are `TrafficClass`) and exactly one
  family part: `V4{ID uint16; Flags uint8; FragmentOffset uint16; Options
  []byte}` or `V6{FlowLabel uint32}`; `Header.Version()` is 4 or 6 from
  the part that is set. `Decode([]byte) (Header, []byte, error)`
  dispatches on the version nibble and returns the header and the payload
  after it, bounded by the IPv4 total length or the IPv6 payload length;
  `Header.Encode(payload []byte) ([]byte, error)` writes the lengths and,
  for IPv4, the IHL from the options and a fresh checksum, and refuses a
  header with both parts or neither, or whose addresses do not match the
  part's family. A version other than 4 or 6, an IPv4 header length under
  20 octets or past the buffer, a length past the buffer, or a wrong IPv4
  checksum is refused with an `errs` error whose attributes name the
  field that failed; the codec imports no trace vocabulary, and the
  routing stage maps any decode error to its `bad-header` reason. The
  checksum is the one's complement of the one's complement sum of the
  header's 16-bit words with the field zeroed (RFC 791 section 3.1, RFC
  1071 section 2), recomputed whole rather than updated incrementally,
  since a full sum over 20 to 60 octets costs nothing here and cannot
  drift; IPv6 has none (RFC 8200 section 3). The package decodes no
  extension header, option, or transport; the routing stage reads and
  rewrites the fields named here and nothing else. `netip.Addr` values are
  4-byte for IPv4 and never IPv4-mapped. Why: every IP rule in the
  routing stage is written once over `HopLimit`, `Src`, and `Dst`, and a
  stage that switched on two header types would carry each rule twice;
  the `gopacket` boundary holds (`src/common/internal/netpenguard`), and
  a router that reads only what it rewrites has no reason to parse
  further. User-directed on 2026-09-11: IPv6 is in this cut and all IP
  header handling lives in one package.
- ARP is not in this phase. Every neighbor is static, so no frame carries
  an ARP body and a codec would have no caller; the parent's phase line
  says so. The phase that adds the exchange adds `src/common/net/arp`
  beside `ip`.
- `routing.Config{VRFs map[string]VRF}` with `VRF{Interfaces
  map[string]Interface; Routes []Route; Neighbors []Neighbor}`,
  `Interface{VLAN vlan.ID; Port string; MAC netaddr.MAC; Prefixes
  []netip.Prefix}`, `Route{Prefix netip.Prefix; NextHop netip.Addr;
  Interface string}`, and `Neighbor{Interface string; Addr netip.Addr;
  MAC netaddr.MAC}`. An interface is keyed by its name, as a device and
  the network model name it, and is one of two shapes: a VLAN interface
  (`VLAN` set, `Port` empty), the routed presence of one VLAN the relay
  classifies into, or a routed port (`Port` set, `VLAN` zero), a physical
  or LAG port that belongs to no VLAN and bridges nothing, the `no
  switchport` port of every layer 3 switch and the only kind a router
  has. A VRF is a routing instance: its own interfaces, its own
  forwarding table, its own neighbor table, and no path between two of
  them, so the same prefix may exist in two VRFs and a lookup never
  leaves the VRF the ingress interface belongs to. `DefaultVRF` is the
  name `default`, the one the loader uses and the one a caller with a
  single table is expected to use; the layer treats it like any other
  name. A route names a next hop, an interface, or both: with a next hop
  alone the interface is the one in the same VRF whose prefix contains
  it; with an interface alone the destination is resolved as a neighbor
  on that interface, the shape of a connected route; with both, the next
  hop is resolved on that interface without a prefix check. Connected
  routes are derived from every interface prefix at `New` and are not
  listed in `Routes`. `Validate(ports port.Table)` refuses an empty VRF
  or interface name, a VRF with no interface, an interface with both or
  neither of `VLAN` and `Port`, a VLAN or a port that two interfaces
  claim across every VRF, a port name absent from the table, a LAG
  member as a routed port, a group MAC, a prefix that is not masked
  (`Prefix.Masked()` differs), an interface address that is a prefix of
  length 0, a route naming neither a next hop nor an interface, a route
  or neighbor naming an interface outside its VRF, a next hop no
  interface prefix in the VRF contains when the route names no
  interface, a neighbor whose address family differs from every prefix
  on its interface, and a duplicate neighbor key within a VRF. Presence
  of `Config` is the `routing` capability. `vswitch.Config.Validate`
  adds the cross-layer rules: a VLAN interface needs `Bridge.VLAN` and
  its VLAN in the table; a routed port must not appear in
  `Bridge.VLAN.Switchports` or in `STP.Ports`, since a routed port is in
  no VLAN and runs no spanning tree; and a device with `routing` and no
  `Bridge` must route every port that is not a LAG member, so no hub
  behaviour hides inside a router. Why: `net/interface`'s `Interface`
  keyed by name with an `ip` facet is the model's routed interface
  whatever its kind (the network model record, "Kind is a `oneof`; the
  routed persona is a cross-kind facet"), `net/ip.InterfaceAddress` and
  `net/ip.NeighborEntry` key by that name, so the loader maps one to
  one; the record reserves network-instance keys that "distinguish VRFs"
  for the routing packages it has not written, and a VRF name is that key
  brought to Go; static routes have no schema yet and stay a Go value
  until `net/routing` is claimed. User-directed on 2026-09-11: VRFs and
  the real world's routed ports are in this cut.
- The stage is a pure function over plain values: `routing.New(cfg
  Config) *Layer`; `Layer.ByVLAN(vid vlan.ID) (string, bool)` and
  `Layer.ByPort(port string) (string, bool)` name the interface a
  classified VLAN or an arrival port belongs to; `Layer.Owns(iface
  string, f ethernet.Frame) bool` is true when the destination MAC is
  that interface's and the EtherType is IPv4 or IPv6; `Layer.Route(iface
  string, f ethernet.Frame) Result` with `Result{Steps []trace.Step;
  Reason trace.Reason; Interface string; Frame ethernet.Frame}`, where an
  empty `Reason` means the frame in `Frame` is to be emitted from
  `Interface` and a non-empty one names why not; and
  `Layer.Interface(name string) (Interface, bool)` returns the
  interface, MAC filled, so the switch knows whether the egress is a
  VLAN or a port; and `Layer.Originate(vrf string, dst netip.Addr,
  protocol uint8, payload []byte) Result` is the host side of the same
  tables, the entry a node uses for a packet of its own: the source
  address is chosen among the VRF's interface addresses of the
  destination's family by longest matching prefix, the rule RFC 6724
  section 5 gives as rule 8 and RFC 1122 section 3.3.4.3 allows a host,
  else the first address of that family; the hop limit is 64, the IANA
  IP parameters registry's recommended default; the route and neighbor
  steps are `Route`'s from the lookup on, without the arrival checks
  and without a decrement, so a host's own datagram goes direct on a
  connected prefix and to its gateway otherwise, as RFC 1122 section
  3.3.1.1 says; and `Result.Frame` is the frame with the interface's MAC
  as source. `Layer` holds no state that changes, so it is safe for
  concurrent use and `Derive` builds a fresh one every time. A frame that
  fails `Owns` on a VLAN interface never enters the stage and the relay
  forwards it as today, so an ARP request to the interface's MAC floods
  as an unknown unicast. Why: a layer with no clock and no state is what
  a run can replay, and the predicates kept beside the decision keep the
  switch ignorant of what routed means.
- `Route` decides in this order, each step a `trace.Step` under
  `port.LayerRouting` (`routing`), and the step set is: `classify`
  naming the VRF and the interface, emitted first on every call;
  `lookup` naming what the table answered; `rewrite` naming the old and
  new hop limit and both MACs; and `drop` naming the reason, last, on
  every refusal but `not-routed`, as the relay's own drops do. A header
  the codec refuses is `bad-header` (RFC 1812 section 5.2.2 says a router
  discards a packet whose header checksum fails); a destination that is
  one of the VRF's own interface addresses is `not-routed` with a
  `lookup` naming the local address and the outcome `Consumed`, the
  device took it; a hop limit of 1 or less on arrival is `ttl-expired`
  (RFC 1812 section 5.3.1: the router decrements by one and discards a
  packet that reaches zero, without forwarding); the longest prefix in
  the VRF's table that contains the destination, the longest match of
  RFC 1812 section 5.2.4.3, `Contains` being family-strict per its
  contract, is the route, and none is `no-route`
  naming the VRF; the next hop, or the destination on a connected or
  interface route, looked up in the VRF's neighbor table on the route's
  interface, is `neighbor-miss` naming the VRF, interface, and address;
  then the header is rewritten with `HopLimit` one lower and, for IPv4,
  the fresh checksum `Encode` writes, the frame's source MAC becomes the
  egress interface's MAC and its destination the neighbor's, and
  `Result.Frame` carries no tags, because the frame is re-encapsulated
  and the egress applies the egress port's form. A route's `lookup` names
  the prefix and the route's kind, a miss names `no route`. No ICMP is
  generated; the reason is the answer. Why: the trace is the product, and
  a generated error packet would be a second propagation the caller did
  not ask for.
- The relay is split into two halves the switch composes:
  `Bridge.Ingress(now, ingress string, f ethernet.Frame, learn bool)`
  runs the port checks, the reserved-address drop, the gate, the
  classification, admission, ingress filtering, the VLAN table check, and
  learning, and returns `(Ingress, Result, bool)`: on a drop the
  `Result` is the whole answer and the bool is false; otherwise
  `Ingress{Port string; FID vlan.ID; PCP vlan.PCP; DEI bool;
  RemainingTags []vlan.Tag; Steps []trace.Step}` carries the resolved
  port, the classification, and the steps so far, and holds no `Result`,
  so no field exists twice. `Bridge.Egress(in Ingress, f ethernet.Frame)
  Result` starts its `Result` from `in.Steps`, sets `Result.Ingress` to
  `in.Port` and `Result.FID` to `in.FID`, and runs the lookup, the
  unicast egress or the flood, and the tag forms, the rest of today's
  `forward`; the same-port rule and the flood's ingress exclusion read
  `in.Port` and nothing else. `Forward` and `Peek` are the two halves in
  sequence with the same result they give today, byte for byte, so every
  landed test holds. An `Ingress` with an empty `Port` is a frame the
  device itself emits: the same-port rule and the flood's ingress
  exclusion match no port, which is what a routed frame leaving on the
  trunk it arrived on needs; a port name is never empty (`port.Table`
  refuses one), so the sentinel collides with nothing. Why: this is the
  one seam the stage sits in, and the alternative, a second
  classification in the switch, would drift from the relay's admission
  and filtering rules.
- `vswitch.Config` gains `Routing *routing.Config`; `Capabilities` adds
  `port.LayerRouting` when it is set. `Switch.Forward` and `Peek` keep
  the BPDU intercept first, then ask `ByPort` for the arrival port
  resolved to its LAG: on a routed port the relay is never consulted;
  `port.Table.Receive` answers the arrival checks (a down or unknown
  port is `port-down`), a frame that fails `Owns` drops with
  `not-bridged` (`routing`, the port belongs to no VLAN and forwards
  nothing it is not addressed), and one that passes goes to `Route`. On
  every other port, a device with a bridge runs `Ingress`, and when the
  frame is not dropped and `ByVLAN(in.FID)` names an interface the frame
  `Owns`, calls `Route`; a device without a bridge takes the hub branch
  (`forwardHub`) as today, which `Validate` reaches only when the device
  has no routing. A `Result` with a reason ends the trace there, with
  the ingress steps, the routing steps, `Ingress` the resolved arrival
  port, `FID` the ingress VLAN (zero on a routed port), and the outcome
  `Dropped` for every reason but `not-routed`, which is `Consumed`.
  Otherwise the egress follows the egress interface's shape: for a VLAN
  interface, `Egress` runs with an `Ingress` whose `Port` is empty, `FID`
  the egress VLAN, `PCP` and `DEI` the ingress values (zero from a routed
  port), no remaining tags, and `Steps` the steps so far, and
  `Result.FID` is the egress VLAN; for a routed port, the switch hands
  the frame untagged to the port layer itself through
  `port.Table.Transmit`, adds a `transmit` step under `routing`, and the
  outcome is `Forwarded`, or a per-port `Egress` drop with the reason
  `Transmit` gave. The switch then
  sets the returned `Result.Ingress` to the resolved arrival port (a LAG
  member's parent), so the fabric counts and journeys read it as they do
  today. `Peek` calls the same functions with `learn` false. `Diff` adds
  `routing.Diff`, whose subjects are keyed by VRF name and then by
  interface name (`vlan`, `port`, `mac`, `prefixes`), by prefix for a
  route (`next_hop`, `interface`), and by interface and address for a
  neighbor (`mac`); a VRF added or removed is one change of kind `vrf`,
  and the capability change comes from `diffCapabilities` as today. Why:
  one stage, one field, one package, as the direction record says a new
  layer is; and a routed port that bypasses the relay is what the
  hardware does, since its frames never reach the bridge's filtering
  database.
- The per-port arrival and transmit rules belong to the port layer, the
  layer below the relay, spanning tree, and routing alike.
  `port.Table.Receive(name string) (Port, trace.Reason)` answers whether
  a frame can arrive on a port now: the port resolved to its LAG, or
  `port-down` when the name is unknown, the LAG parent is missing, or
  the port or its parent does not forward; the relay's ingress half and
  the BPDU intercept, which each spell those checks today, call it, and
  so does the routed port path. `port.Table.Transmit(name string,
  payloadLen int) (member string, reason trace.Reason)` answers whether
  a frame of that size can leave that port now, `port-down` when the
  port or, for a LAG, every member does not forward, the lowest-named
  forwarding member for a LAG, and `mtu-exceeded` when the port's MTU is
  set and smaller than the payload; the reasons move to `port` and the
  bridge re-exports nothing, since the relay's callers read them from
  the `Egress` record. The relay's egress half calls it for the unicast
  port and for every flood candidate in place of the loops it carries
  today, and the switch calls it for a routed port, so every port rule
  exists once. Why: routing hands a frame down to whichever layer is
  below it, the relay for a VLAN interface or the port for a routed
  port, and a rule that lives in the lower layer is the one both paths
  reference rather than copy; the direction record already has every
  layer import the port table.
- A device has one base MAC, and a MAC names one node in a simulation.
  `vswitch.Config` gains `MAC netaddr.MAC`, the device's base address;
  a routed interface whose `MAC` is zero uses it, and so does a
  `stp.Config` whose `Address` is zero, the way a real switch stamps its
  chassis address on every SVI, on its routed ports, and on its bridge
  id. A zero base MAC means "assign one": `fabric.New` fills every zero
  base MAC and every zero `Host.Address` before building anything,
  walking switch names and then host names in sorted order and taking the
  first address from `netaddr.Local(n)`, the nth locally administered
  unicast address, `02:00:00:` followed by n in three big-endian octets
  with the U/L bit of IEEE Std 802-2014 clause 8.2.2 set and the I/G bit
  clear,
  for n from 1 upward, that no explicit MAC anywhere in the configuration
  (a base MAC, a host address, a routed interface, a bridge address)
  already uses; `vswitch.New` outside a fabric does the same over its own
  configuration alone, so a standalone switch is usable and two
  standalone switches may collide, which the doc comment says.
  `Fabric.Config()` and `Switch.Config()` return the filled values, so a
  snapshot shows the address a frame will carry. `fabric.Derive` keeps
  `cur`'s assigned address for a node whose new configuration leaves it
  zero, so an expected fabric answers with the same MACs as the current
  one. `fabric.Config.Validate` refuses a base MAC or host address that
  equals another node's base MAC, host address, routed interface MAC, or
  bridge address; within one device, a routed interface MAC may equal
  the base MAC or another interface's, since the VLAN or the port keeps
  them apart. `stp.Config.Validate` no longer refuses a zero address;
  `vswitch.New` fills it before the layer is built, and `stp.New` with a
  zero address is the caller's error, which its doc comment states. Why:
  the loader has no device MAC input, and a simulation that refused to
  run for a missing address would fail on the ordinary shape of a model;
  an address that identifies a device is what the bridge id and the
  neighbor table both assume. User-directed on 2026-09-11: MACs are
  generated when missing and unique within the simulation.
- `fabric.Host` gains `IP *HostIP{Addresses []netip.Prefix; Gateway
  netip.Addr; Neighbors map[netip.Addr]netaddr.MAC}`, and `Injection`
  gains `Packet *Packet{To netip.Addr; Protocol uint8; Payload []byte}`.
  With `Packet` set, `Origin` names a host with an IP stack, `Frame` is
  empty (no address, EtherType, tag, or payload set; the struct holds
  slices and cannot be compared whole), and the host's stack builds the
  frame. A host's stack is the routing layer with one interface: the
  fabric translates `HostIP` into a `routing.Config` with the VRF
  `default`, one routed port named after the host with the host's MAC
  and prefixes, a default route per family (`0.0.0.0/0` and `::/0`) to
  the gateway when one is set, and the neighbors, validates it against a
  one-port table, and builds one `routing.Layer` per host at `New`;
  `Inject` calls `Originate` on it and queues `Result.Frame` with the
  host's VLAN form applied as today. A `Result` with a reason (`no-route`
  for a destination off every prefix with no gateway, `neighbor-miss`
  for a destination or gateway without an entry) is an `Inject` error
  naming the address, not a journey, because the host could build no
  frame to give a journey to. `Config.Validate` refuses an IP stack with
  no address and otherwise what `routing.Validate` refuses on the
  translated configuration, the gateway included (a next hop no prefix
  contains). Why: a host is an IP stack with one interface, its routing
  table is the same table with a default route, and RFC 1122 section
  3.3.1.1 describes it with the rules the routing layer already holds;
  a second implementation inside `Inject` would be the same rules
  written twice. `fabric.Diff` adds per host the fields
  `addresses` and `gateway`, and one change per neighbor address that
  differs, field `neighbors.<addr>`, in `netip.Addr.Compare` order so the
  diff is deterministic. `Compare` is unchanged: a scenario is
  `[]Injection` either way. Why: "can h1 reach 10.0.20.7" is one
  scenario, and the fabric's `Inject` is where a host already decides
  the form of what it sends.
- `netmodel.Load` takes `addrs []*ipv1.InterfaceAddress` and `neighbors
  []*ipv1.NeighborEntry` after the spanning tree inputs and builds
  `routing.Config` when any `Interface` carries an `ip` facet: a `vlan`
  kind becomes a VLAN interface on its `vlan_id`, a `physical` or `lag`
  kind becomes a routed port on its own name, every other kind with the
  facet (loopback, subinterface, tunnel, management, other) is reported
  and skipped as unsupported; the interface's MAC comes from
  `Interface.mac`, or is zero, the device's base address, with `mac`
  reported as a default for it; its prefixes come from the address rows
  naming it (the row's `prefix`, since the simulator needs the network,
  and the row's `address` is the device's own address that `Route`
  treats as `not-routed`), and its neighbors from the neighbor rows
  naming it that carry a MAC. A routed port's switchport facet, if the
  source reported one beside the IP facet, is reported and skipped, since
  the two cannot both hold. `routing` is inferred into the capability
  set, and `vlan` and `relay` are implied only when a VLAN interface
  loaded. An address row naming an interface that carries no IP facet
  and a neighbor row without a MAC or naming such an interface are
  reported and skipped. A VLAN interface still loads into the port table
  as today, kind `Other` with no switchport, so the relay never floods to
  it and it is a valid name for nothing but the port list; the routing
  configuration keys VLAN interfaces by VID and never names that port.
  Everything loads into the VRF named `routing.DefaultVRF`, because no
  row in `net/interface` or `net/ip` carries a network instance; the
  report lists the VRF as a default once, and the base MAC as a default
  once. `Routes` stays empty: the model carries no route rows. There is
  no export: the layer holds nothing it learned. Why: the loader already
  maps every other row one to one, and an export of configuration back
  into rows would say nothing a run taught.

## Requirements

This phase claims requirements 41 through 48 of the parent, whose list
and phase line were edited with this plan, with these acceptance
examples. Every switch below is `sw1` with VLANs 10 and 20 in its table,
ports `1/1/1` untagged in 10 and `1/1/2` untagged in 20, one VRF
`default` with interfaces `vlan10` (VLAN 10, `00:00:5e:00:01:01`,
10.0.10.1/24) and `vlan20` (VLAN 20, the same MAC, 10.0.20.1/24), and a
neighbor entry `(vlan20, 10.0.20.7) -> 00:11:22:33:44:77` unless the
example says otherwise.

41. A frame to the device's own address on a routed VLAN is routed to
    another VLAN. Acceptance: an untagged frame on `1/1/1` from
    `00:11:22:33:44:11` to `00:00:5e:00:01:01` carrying an IPv4 packet
    from 10.0.10.7 to 10.0.20.7 with TTL 64 traces `classify` (vlan 10),
    `learn` (relay), `classify` (routing, vrf default interface vlan10),
    `lookup` (routing, `10.0.20.0/24 connected vlan20`), `rewrite`
    (routing, TTL 64 to 63 and both MACs), then the relay's `lookup` in
    VLAN 20, and ends `Forwarded` with the one egress `1/1/2` when the
    FDB holds the neighbor's MAC there, or `Flooded` to VLAN 20's members
    when it holds nothing; either way the egress frame is untagged, with
    source `00:00:5e:00:01:01`, destination `00:11:22:33:44:77`, TTL 63,
    and a checksum that decodes clean.
42. A missing neighbor is an outcome, not a flood. Acceptance: the frame
    of 41 with no neighbor entries ends `Dropped` with `neighbor-miss`, a
    step naming vlan20 and 10.0.20.7, and no egress.
43. TTL exhaustion drops. Acceptance: the frame of 41 with TTL 1 ends
    `Dropped` with `ttl-expired` and no egress; a packet to 10.0.10.1
    itself ends `Consumed` with `not-routed`; a packet whose checksum is
    off by one ends `Dropped` with `bad-header`; a packet to 10.0.30.7
    with no route ends `Dropped` with `no-route`.
44. A host with an IP stack sends through its gateway. Acceptance: a
    fabric with `sw1`, host `h1` on `1/1/1` with 10.0.10.7/24, gateway
    10.0.10.1, and neighbor `10.0.10.1 -> 00:00:5e:00:01:01`, and host
    `h2` on `1/1/2` with address `00:11:22:33:44:77`; an injection with
    `Packet{To: 10.0.20.7}` from `h1` queues a frame to
    `00:00:5e:00:01:01` carrying IPv4 10.0.10.7 to 10.0.20.7 with TTL 64,
    the journey's hop on `sw1` is the `Flooded` variant of 41, since `h2`
    has sent nothing for VLAN 20 to learn, and `h2` receives the routed
    frame with TTL 63; an injection to 10.0.10.9 with no neighbor entry
    for it is refused by `Inject` naming the address.
45. IPv6 routes the same way. Acceptance: the interfaces gain
    2001:db8:10::1/64 and 2001:db8:20::1/64 and a neighbor
    `(vlan20, 2001:db8:20::7)`; the frame of 41 with an IPv6 packet from
    2001:db8:10::7 to 2001:db8:20::7 with hop limit 64 ends `Forwarded`
    on VLAN 20 with hop limit 63, and the same packet to 2001:db8:30::7
    ends `no-route` while 10.0.30.7 is unaffected by the IPv6 routes.
46. VRFs route independently. Acceptance: VLANs 30 and 40 join the table
    and ports `1/1/3` untagged in 30 and `1/1/4` untagged in 40; a second
    VRF `tenant` holds `vlan30` (10.0.10.1/24, MAC `00:00:5e:00:01:02`)
    and `vlan40` (10.0.20.1/24, the same MAC) and a neighbor `(vlan40,
    10.0.20.7) -> 00:11:22:33:44:99`; the packet of 41 injected on
    `1/1/3` to `00:00:5e:00:01:02` egresses in VLAN 40 to
    `00:11:22:33:44:99` with the `classify` step naming `tenant`, the
    packet of 41 on `1/1/1` still egresses in VLAN 20 to
    `00:11:22:33:44:77`, a packet on `1/1/3` to an address only `default`
    holds a route for ends `no-route` naming `tenant`, and a
    configuration claiming VLAN 30 in both VRFs fails `Validate`.
47. Missing MACs are assigned and unique within the simulation.
    Acceptance: a fabric with switches `sw1` and `sw2`, each with a zero
    base MAC and a routed interface with a zero MAC, and hosts `h1` and
    `h2` with zero addresses, where `h1`'s IP stack names its gateway's
    MAC as `02:00:00:00:00:01`; `Config()` after `New` reports `sw1` as
    `02:00:00:00:00:01`, `sw2` as `02:00:00:00:00:02`, `h1` as
    `02:00:00:00:00:03`, and `h2` as `02:00:00:00:00:04`, `sw1`'s routed
    interface and bridge id carry `sw1`'s address, and the packet of 44
    from `h1` reaches `sw1`'s interface; with `h2` given
    `02:00:00:00:00:01` explicitly, `sw1` takes `02:00:00:00:00:02` and
    the rest shift; a derived fabric whose configuration leaves every
    MAC zero keeps the assignments; and two hosts with the same explicit
    address, or a host with `sw1`'s explicit base MAC, fail `Validate`.
48. A routed port routes without the relay. Acceptance: `sw1` gains
    port `1/1/5` and the interface `1/1/5` (`Port: "1/1/5"`,
    10.0.50.1/24, MAC zero so the base MAC) with a neighbor `(1/1/5,
    10.0.50.7) -> 00:11:22:33:44:55`; the packet of 41 to 10.0.50.7
    egresses untagged on `1/1/5` with outcome `Forwarded` and a
    `transmit` step under `routing`; an untagged frame on `1/1/5` to the
    base MAC for 10.0.20.7 traces `classify` (routing, vrf default
    interface 1/1/5) with no relay step before it and egresses in VLAN
    20; a frame on `1/1/5` to any other MAC, an ARP included, ends
    `Dropped` with `not-bridged` and no learning; a configuration that
    also lists `1/1/5` in `Switchports` or in `STP.Ports` fails
    `Validate`; and a device with ports `1/1/1` and `1/1/2`, no bridge,
    and both ports routed in `default` routes a packet from one to the
    other, while the same device with `1/1/2` left unrouted fails
    `Validate`.

## Out of scope

Everything the parent lists, ARP and ND exchanges (every neighbor is
static), ICMP of any kind, fragmentation and path MTU (an oversized
routed frame meets the egress port's MTU rule as any frame does),
extension headers and options beyond carrying them, subinterfaces,
loopbacks, and tunnels as routed interfaces, route leaking between VRFs,
loading a VRF name from the network model (it carries none), ECMP (the
table holds one route per prefix), a per-port MAC offset (every routed
interface without its own MAC shares the base address), and loading a
fabric's hosts from the network model.

## Units

### U1. The IP header codec

Files: `src/common/net/ip/ip.go`, `ip_test.go`, `src/common/README.md`
After: none
Change: the package as the Decisions say. `ip.V4HeaderLen` is 20 and
`ip.V6HeaderLen` is 40. `Decode` reads the version nibble first; for
IPv4 it reads the IHL, the total length, and the checksum before trusting
any of them and returns the payload as a sub-slice bounded by the total
length; for IPv6 it bounds the payload by the payload length field.
`Header.Encode` writes, for IPv4, the IHL from the options length, the
total length from the payload, and the checksum last, and for IPv6 the
payload length. The common README gains one row.
Tests: `ip_test.go`, a 20-octet IPv4 header for 10.0.10.7 to 10.0.20.7,
hop limit 64, protocol 17, whose bytes the test spells out with the
checksum summed by hand in a comment, decoding to those fields with
`V4` set and `V6` nil and encoding back to the same bytes; a header with
options round-tripping; a 40-octet IPv6 header for 2001:db8:10::7 to
2001:db8:20::7 decoding with `V6` set and encoding back; each refusal
(version 5, IHL 4, IHL past the buffer, total length past the buffer,
checksum off by one, IPv6 payload length past the buffer, a header with
both parts, a header whose part and addresses disagree) naming the
field; and a payload sub-slice ending at the length field when the
buffer is longer. Each test is evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/ip src/common/README.md`

### U2. The routing layer

Files: `src/common/netsim/vswitch/routing/config.go`, `layer.go`,
`diff.go`, `config_test.go`, `layer_test.go`,
`src/common/netsim/vswitch/port/port.go`
After: U1
Change: `port.LayerRouting` (`routing`) joins the layer constants.
`routing.Config`, `VRF`, `Interface`, `Route`, `Neighbor`,
`DefaultVRF`, `Validate`, `Clone`, `New`, `Layer.ByVLAN`,
`Layer.ByPort`, `Layer.Owns`, `Layer.Route`, `Layer.Interface`,
`Layer.Originate`, `Result`, the reasons `ReasonNoRoute`, `ReasonTTLExpired`,
`ReasonNeighborMiss`, `ReasonNotRouted`, `ReasonBadHeader`, and
`ReasonNotBridged`, and `Diff(a, b Config) []trace.Change` as the
Decisions say. The table at `New` is, per VRF, the connected routes and
the static routes sorted by prefix length descending, then by prefix,
so a lookup is the first match; a static route on a prefix a connected
route also covers wins nothing, the connected route is listed first at
equal length, and the doc comment says so. `ByVLAN` and `ByPort` are
map lookups built at `New`; `Owns` is a MAC compare and an EtherType
test. `Route` never mutates the frame it is given: the egress frame is a
new value with a new payload slice. A `Layer` is safe for concurrent
use; its doc comment says so.
Tests: `config_test.go`, every `Validate` rule and `Diff` over one change
of each field, a VRF added, and an interface changing shape from VLAN to
port; `layer_test.go`, requirements 41 through 43, 45, 46, and the
layer's part of 48 driven on the layer alone (`ByVLAN` or `ByPort`,
`Owns`, then `Route` on a hand-built frame), a static route to a next
hop resolved on the interface whose prefix contains it, a static route
with an interface alone resolving the destination as a neighbor,
longest prefix winning over a shorter static default route, a next hop
that only another VRF's interface could reach refused by `Validate`
and, when a route names it with an interface, resolved in its own VRF's
neighbor table alone, `Owns` false for the right MAC on an unrouted
VLAN, for a wrong MAC on a routed one, and for the right MAC with an
ARP EtherType; `Originate` choosing the longest-matching source among
two addresses of one family, going direct on a connected prefix and to
the default route's gateway otherwise, sending with hop limit 64 and no
decrement, and answering `no-route` and `neighbor-miss`. Each is
evidence for this unit; the checksum-off-by-one
case is watched failing first by feeding a header whose checksum the
test does not recompute.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/routing src/common/netsim/vswitch/port`

### U3. The relay's two halves

Files: `src/common/netsim/vswitch/port/port.go`, `port_test.go`,
`src/common/netsim/vswitch/bridge/bridge.go`, `result.go`,
`bridge_test.go`
After: none
Change: `port.Table.Receive` and `port.Table.Transmit` with
`port.ReasonPortDown` and `port.ReasonMTUExceeded` moved from `bridge`;
`Ingress`, `Bridge.Ingress`, and `Bridge.Egress` as the Decisions say,
the ingress half calling `Receive` for its port checks and the egress
half calling `Transmit` where it loops over members and checks the MTU
today; `forward` becomes the two calls and every step, reason, outcome,
and egress it produces today is unchanged. The same-port rule
and the flood exclusion compare against `in.Port`, never against the
`Result.Ingress` the egress half writes. A hub is unaffected: the split
is inside the bridge package. The doc comment on `Egress` states the
empty-port rule.
Tests: `port_test.go`, `Receive` on an up port, an unknown name, a
down port, a member of a down LAG, and a member resolving to its
parent; `Transmit` on an up port, a down port, a LAG with two forwarding
members choosing the lowest name, a LAG with none forwarding, an MTU of
0 admitting any size, and an MTU one byte short;
`bridge_test.go`, the existing tests unchanged and green; new cases for
`Egress` with an empty port: in a VLAN bridge, a known unicast
whose port would have been the ingress port is forwarded rather than
`same-port`, and a flood in the VLAN reaches every forwarding member
including that port, with the ingress PCP kept on a tagged member; in a
bridge without VLAN, a flood from an empty port reaches every forwarding
port with the frame's tags untouched. Each is evidence for this unit,
watched failing by comparing against `Result.Ingress` set to that port.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/vswitch/bridge`

### U4. The stage in the switch

Files: `src/common/net/netaddr/mac.go`, `netaddr_test.go`,
`src/common/netsim/vswitch/config.go`, `switch.go`, `diff.go`,
`derive.go`, `switch_test.go`, `src/common/netsim/vswitch/stp/config.go`,
`config_test.go`, `src/common/netsim/vswitch/README.md`
After: U2, U3
Change: `netaddr.Local(n uint32) MAC`; `Config.MAC`, `Config.Routing`,
`Capabilities`, `Validate` (the cross-layer rules of the Decisions and
`routing.Validate` against the port table), `Clone`, `Diff` covering
`mac` on the device, `New` filling a zero base MAC and every zero routed
interface and bridge address from it, `Forward` and `Peek` composing the
halves around the stage, the BPDU intercept and the routed port path
calling `port.Table.Receive`, a routed port's frame handed to
`port.Table.Transmit`, `Diff`
calling `routing.Diff`, `Derive` building the layer fresh from the new
configuration and keeping `cur`'s base MAC when the new one is zero, and
`stp.Config.Validate` accepting a zero address, as the Decisions say.
The README gains the `routing` rung with its rules (the two interface
shapes, the order of checks, the hop limit rule with its RFC 1812
section, the re-encapsulation, the routed port bypassing the relay), the
new drop reasons in the table, the base MAC rule, and the note that a
routed frame's `Result.FID` is the egress VLAN or zero on a routed
port.
Tests: `netaddr_test.go`, `Local(1)` and `Local(0x010203)` spelled out
and the locally administered bit set; `switch_test.go`, capability
`{relay, routing, vlan}` and `{routing}` alone for a router, each
cross-layer `Validate` rule, a switch with every MAC zero reporting one
base address on its routed interfaces and its bridge id, an explicit
interface MAC left alone beside an assigned base, a derived switch
keeping the base, requirements 41 through 43, 45, 46, and 48 through
`Forward` with the full step sequence asserted, the source MAC learned
on VLAN 10 by the routed frame and nothing learned from a routed port,
a routed frame leaving on the port it arrived on when that trunk carries
both VLANs, a routed frame arriving on a LAG member whose
`Result.Ingress` is the LAG, a routed port that is a LAG sending on its
lowest-named forwarding member, a routed frame whose egress port MTU is
smaller than the packet dropped per port with `mtu-exceeded` on a VLAN
egress and on a routed port egress alike, a routed egress onto a VLAN
member a gate blocks dropped with `port-blocked`, a hub unchanged by
the split, `Peek` leaving the FDB unchanged, a frame to a VLAN
interface's MAC with an ARP EtherType flooded by the relay as an unknown
unicast, `Diff` of a neighbor change, and a derived switch routing as
the new configuration says. Each is evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net/netaddr src/common/netsim/vswitch`

### U5. Hosts with an IP stack

Files: `src/common/netsim/fabric/config.go`, `run.go`, `fabric.go`,
`derive.go`, `diff.go`, `config_test.go`, `run_test.go`, `diff_test.go`
if present else `compare_test.go`, `routing_test.go`,
`src/common/netsim/fabric/README.md`
After: U4
Change: `HostIP`, `Host.IP`, `Host.Clone`, `Packet`, `Injection.Packet`,
the translation of a host into a `routing.Config`, one `routing.Layer`
per host built at `New`, `Inject` calling `Originate`,
`Config.Validate`'s IP rules and the
one-node-per-MAC rule, `New` assigning every zero base MAC and host
address, `Derive` keeping `cur`'s assignments, and `Diff`'s `ip` fields
as the Decisions say. `Inject` refuses `Packet` beside a `Frame` with
any field set, on a switch origin, or on a host without an IP stack. The
README's example gains nothing; a new section states what a host with an
IP stack does with a packet, what `Inject` refuses, and how MACs are
assigned.
Tests: `routing_test.go`, requirement 44 end to end with the journey's
hop asserted against 41 and the delivery's TTL, requirement 47 in full,
and a host cabled to a routed port reaching a host on a VLAN through it;
`run_test.go`, `Inject` refusing each malformed packet injection by its
reason, an IPv6 packet to an off-link address using the gateway's entry,
and the translated configuration of a host passing `routing.Validate`
with its default routes present;
`config_test.go`, each `Validate` rule; the diff test, a gateway change
and a neighbor added. Each is evidence for this unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim/fabric`

### U6. The loader and the records

Files: `src/common/netsim/vswitch/netmodel/netmodel.go`, `report.go`,
`routing_test.go`, `netmodel_test.go`, `stp_test.go`,
`testdata_icx7150_test.go`, `src/common/netsim/README.md`,
`docs/architecture/2026-09-10-virtual-device-direction.md`,
`docs/plans/2026-09-10-1815-feat-netsim-network-environment-plan.md`
After: U4
Change: `Load` with the two new inputs and the inference, skips, and
reports the Decisions name; every existing `Load` call passes `nil` for
both. The `netsim` README lists `vswitch/routing`; the direction record's
package list gains `routing`, its "later routing" wording becomes
present tense, and its consequence on routing names routed ports beside
VLAN interfaces. The parent's phase line is filled at Finish as
`implement` says.
Tests: `routing_test.go` in `netmodel`, a VLAN interface with an IP
facet, a MAC, two address rows, and one neighbor row loading to the
configuration of 41 in the VRF `default` with `routing` inferred and the
VRF listed as a default, a physical interface with an IP facet loading
as a routed port named as the interface with `routing` inferred and no
`vlan` implied, a VLAN interface without a MAC loading with a zero MAC
and `mac` reported as a default for it and for the device, a loopback
with an IP facet reported unsupported, a routed port with a switchport
facet loading as a routed port with the facet skipped, an address row
naming an interface without an IP facet skipped, a neighbor row without
a MAC skipped, a wanted set without `routing` skipping every IP facet,
and the VLAN interface present in the port table as kind `Other` and
absent from a flood in its VLAN; `testdata_icx7150_test.go` unchanged in
outcome (the fixture carries no IP facet). Each is evidence for this
unit.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim docs/architecture`

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh --full
go test -race ./src/common/net/... ./src/common/netsim/... ./src/common/internal/netpenguard/...
```

The full run is owed because the tree's dirty marker from phase 3 names a
Bash mutation; nothing here runs a generator. The second command proves
the codec kept `gopacket` out of the main module.

## Definition of done

- [ ] Verifier green for every changed path and one full run at Finish.
- [ ] The vswitch, fabric, netsim, and common READMEs and the direction
      record match the landed API.
- [ ] This plan's `status` set with an outcome note under its title, and
      the parent's `Landed:` line for this phase filled.
- [ ] No plan labels in code, comments, commit messages, or test names.

## Open questions

- Whether the relay should learn the source MAC of a routed frame on the
  ingress VLAN. It does, since the frame did arrive on that port in that
  VLAN and a real switch learns it; the phase leaves the relay's rule
  alone.
- Whether a routed port should carry its own generated MAC rather than
  the base address. Vendors differ (some offset per port, most share the
  chassis address); sharing is the smaller rule and nothing in a run can
  tell the two apart until two routed ports of one device face the same
  segment, which is a misconfiguration this phase does not model.
