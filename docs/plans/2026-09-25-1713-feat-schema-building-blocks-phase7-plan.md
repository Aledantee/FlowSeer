---
title: Schema Building Blocks Phase 7, L3 Protocols and IP Services - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-25-1713-feat-schema-building-blocks-plan.md
---

# Schema Building Blocks Phase 7, L3 Protocols and IP Services - Plan

## Goal

Routing-protocol and IP-service state has a place: `protocol/bgp`,
`protocol/ospf`, `protocol/isis`, `protocol/vrrp`, `protocol/bfd`,
`protocol/dhcp`, `protocol/dns`, and the functional `net/nat`, every
table keyed by network instance where its protocol runs per instance. The
means is one new predefined key rule in `net/key` for the protocol
instance, then one unit per protocol cluster adding its rows and enums,
each pinned by conformance tests and the README and allowlist lines the
gates hold them to. Nothing reshapes a landed message.

Stop condition: a targeted source reports a routing-protocol row that
the key `(network_instance, protocol_instance)` cannot place, such as two
OSPF processes of one version sharing a name inside one network instance,
or a BFD local discriminator that repeats across network instances on one
device. Either would mean the keys chosen here are wrong, and the phase
stops for a re-cut before it lands.

## Decisions

The record's rules 3, 4, 5, 6, and 7 govern
(`docs/architecture/2026-09-25-schema-building-blocks-direction.md`).
Evidence is the dossier `docs/research/schema-building-blocks/06-l3-routing-services.md`,
the atlas `docs/research/network-domain-atlas/entities/05-routing.md`, and
the vendored specs cited per decision.

### Keys

- **A protocol instance has its own predefined key rule,
  `protocol_instance_name`, `StringRules` extension 50003 in `net/key`.**
  Why: one network instance can run several instances of one protocol
  (OSPF process ids, IS-IS tags, OpenConfig named protocol instances), and
  the structure record's sequencing item 6 asked that routing keys
  "distinguish VRFs and multiple routing-protocol instances"
  (`docs/architecture/2026-08-20-network-model-structure-direction.md:571-572`).
  Both standard models key a protocol instance by a string name:
  OpenConfig `protocols/protocol` is keyed `"identifier name"`
  (`spec/yang/openconfig/openconfig-network-instance.yang:866-867`), with
  `name` a string (`:1350-1357`), and ietf-routing keys `routing-protocol`
  by `"type name"` with `name` a string
  (`spec/yang/cisco/iosxe/2611/ietf-routing.yang:417`, `:431-434`). The
  rule lives in `net/key` because three protocol packages use it and a
  protocol package may not import another (record rule 3). It takes the
  bounds of `network_instance_name` (1 to 255 characters) for the same
  reason: the name is device-supplied free text. 50003 is the next free
  `StringRules` number (`docs/code-style-proto.md`, predefined-rule table,
  which ends at 50002). The record's rule 3 already says keys take a
  `net/key` rule; its tree line for `net/key` names the two rules that
  existed then, and this rule extends the list without changing a rule,
  so the record is not amended.
- **The mapper names the protocol instance as the device does, and
  `default` when the device has no name for it.** An OSPF process id
  becomes its decimal string (`10`); IOS-XE reports it as a non-key leaf
  beside `af router-id`
  (`spec/yang/cisco/iosxe/2611/Cisco-IOS-XE-ospf-oper.yang:1974-1978`). An
  IS-IS tag is the name (`Cisco-IOS-XE-isis-oper.yang:142-146`, `:160-164`);
  an untagged IS-IS or a single BGP process is `default`. Why: record
  rule 4 applies the same policy to the network instance, and OpenConfig
  asks for a fixed name when the operator gives none (`DEFAULT`,
  `openconfig-network-instance.yang:1350-1357`); absence never stands for
  the default.
- **Protocol instance-level rows carry both keys; interface-keyed rows
  carry the protocol instance and inherit the network instance.**
  `BgpInstance`, `BgpPeer`, `BgpPath`, `OspfInstance`, `OspfArea`, and
  `IsisInstance` require `network_instance` and `protocol_instance`.
  `OspfInterface`, `OspfNeighbor`, and `IsisAdjacency` are keyed by
  `interface_name`, require `protocol_instance`, and have no
  `network_instance` field: the interface's `IpFacet.network_instance`
  names it (record rule 4). `VrrpGroup` is keyed by interface and has
  neither, because VRRP has no process above the interface.
- **OSPF rows carry `version` (2 or 3) as part of their key.** Why:
  OSPFv2 and OSPFv3 processes are separate instances that devices number
  independently (IOS `router ospf 1` beside `router ospfv3 1`), and
  ietf-routing's key is `type` plus `name` for this reason
  (`ietf-routing.yang:417`, `:424-429`). A uint32 with `gte 2, lte 3`, as
  the CDP row does for its protocol version, rather than an enum of two
  integers.
- **Rows whose key is not interface-scoped require `network_instance`**
  (record rule 4): `BfdSession` (keyed by local discriminator, but its
  addresses are instance-scoped), `Dhcpv4Lease`, `Dhcpv4Pool`,
  `Dhcpv6Binding`, `DhcpSnoopingBinding` (it carries a `vlan_id`, as
  phase 6's `GroupMembership` does), `DnsResolver` (IOS-XE keys resolver
  state by `ni-name ni-type`, `Cisco-IOS-XE-dns-oper.yang:101-112`),
  `NatMapping`, and `NatSession` (IOS-XE carries `vrfid` in the
  translation key, `Cisco-IOS-XE-nat-oper.yang:184-217`). The DHCP server
  counters are device-wide scalars in every source
  (`spec/mib/huawei/HUAWEI-DHCPS-MIB:1736-1835`,
  `spec/mib/lancom/sx/sx-5.30-ys7154cf/fastpath_dhcp6.mib:128-243`), not a
  table, so they carry no key.
- **32-bit OSPF and BGP identifiers are `uint32`, and the OSPF area id is
  one `uint32` in both versions, not a typed variant.** Why: RFC 5340
  §2.2 keeps "OSPF Router IDs, Area IDs, and LSA Link State IDs ... at the
  IPv4 size of 32 bits" and removes their address semantics; RFC 5643
  types both `Unsigned32` (`spec/mib/ietf/OSPFV3-MIB:114-124`, `:139-149`);
  OSPF-MIB spells the same 32 bits as an `IpAddress`
  (`spec/mib/ietf/OSPF-MIB:83-98`); ietf-ospf and OpenConfig type the area
  id as a union of `uint32` and `dotted-quad` for both versions
  (`spec/yang/cisco/iosxe/2611/ietf-ospf.yang:154-161`,
  `spec/yang/openconfig/openconfig-ospf-types.yang:62-71`), and IOS-XE
  operational data uses plain `uint32`
  (`Cisco-IOS-XE-ospf-oper.yang:1916-1923`). The two spellings are one
  value space, so arms that "validate differently" do not exist
  (conventions doc, Typed variants), and a oneof would give area 0.0.0.1
  two encodings that no longer compare equal across versions or sources.
  A mapper converts `a.b.c.d` to `a<<24 | b<<16 | c<<8 | d`. The BGP
  identifier is "a 4-octet, unsigned, non-zero integer" (RFC 6286 §2.1),
  so `router_id` fields take `gte 1`.
  Ruled against the stub, which proposed the typed variant. Cost if
  wrong: `OspfAreaId` becomes a message on four rows and their tests.

### BGP

- **Peer state is `BgpPeerState`, a pass-through of `bgpPeerState`:
  `IDLE = 1` to `ESTABLISHED = 6`** (`spec/mib/ietf/BGP4-MIB:195-210`,
  RFC 4271 §8.2.2), with `_UNSPECIFIED = 0` because the registry assigns
  nothing at zero, as `RouteSourceProtocol` does. H3C uses the same
  integers (`spec/mib/hp/hh3c/HH3C-BGP4V2-MIB:102-111`). IOS-XE numbers
  the same states from 0 (`Cisco-IOS-XE-bgp-oper.yang:126-160`) and Ruckus
  reports a string (`spec/proto/ruckus/icx/switches.proto:4730`); a mapper
  classifies both (record rule 7).
- **ASNs are `uint32` with no bound** (RFC 6793 §3). BGP4-MIB's
  `bgpPeerRemoteAs` is `Integer32 (0..65535)` (`BGP4-MIB:293`), which a
  4-octet AS cannot fit, so a mapper reading a 4-octet-capable source
  never truncates. Zero and the reserved ASNs stay representable because
  a device may report them and rejecting the row loses it
  (`docs/solutions/architecture-patterns/decode-failure-blast-radius-in-generated-walks.md`).
- **AFI and SAFI are two pass-through enums, `BgpAfi` and `BgpSafi`,
  from their IANA registries**, not BGP4V2-TC-MIB's truncated subset
  (`spec/mib/ietf/BGP4V2-TC-MIB:41-64`). `BgpAfi`: `IPV4 = 1`, `IPV6 = 2`,
  `L2VPN = 25`, `BGP_LS = 16388`
  (`spec/mib/ietf/IANA-ADDRESS-FAMILY-NUMBERS-MIB:81`, `:82`, `:107`,
  `:115`). `BgpSafi`: `UNICAST = 1`, `MULTICAST = 2`,
  `LABELED_UNICAST = 4`, `MCAST_VPN = 5`, `VPLS = 65`, `EVPN = 70`,
  `BGP_LS = 71`, `MPLS_VPN = 128`, `ROUTE_TARGET_CONSTRAINT = 132`,
  `FLOWSPEC = 133`, `FLOWSPEC_VPN = 134`
  (https://www.iana.org/assignments/safi-namespace/safi-namespace.xhtml).
  Both registries reserve 0, so `_UNSPECIFIED = 0` is FlowSeer's. Each use
  site validates the field width, as the conventions doc requires for
  open pass-through enums: AFI `this <= 65535`, SAFI `this <= 255`
  (RFC 4760 §3, 2-octet AFI, 1-octet SAFI), as a field-level CEL rule
  because each enum has one use site; a predefined rule earns its
  extension number only when a second message needs it.
- **A peer's address families are a repeated `BgpPeerAddressFamily`
  inside `BgpPeer`, unique on `(afi, safi)`.** Why: OpenConfig hangs
  `afi-safis/afi-safi` under the neighbor with `active` and `prefixes`
  state (`openconfig-bgp-neighbor.yang:627-671`), and IOS-XE's neighbor
  key includes the AFI-SAFI (`Cisco-IOS-XE-bgp-oper.yang:1093-1094`).
- **Standard and large communities are the typed variant `BgpCommunity`,
  a required oneof `kind` of `uint32 standard` and `BgpLargeCommunity
  large`.** A standard community is "treated as 32 bit values" (RFC 1997,
  Communities Attribute); a large community is three 4-octet fields,
  Global Administrator, Local Data Part 1, and Local Data Part 2
  (RFC 8092 §3). The shapes do not nest, each arm validates its own
  fields, and a consumer asking whether a path carries a community reads
  one list. Both arms at once cannot be represented (a oneof keeps one
  arm; decoding keeps the last on the wire), so the test covers the empty
  case only
  (`docs/solutions/conventions/a-oneof-both-arms-set-is-unrepresentable-so-validate-the-empty-case.md`).
  Standard communities stay one `uint32` rather than an AS:value pair,
  because RFC 1997 defines the value as 32 bits and the well-known
  communities (`NO_EXPORT = 0xFFFFFF01`) have no AS half.
- **Communities are carried by `BgpPath`, an IPv4 or IPv6 unicast
  Loc-RIB path keyed by `(network_instance, protocol_instance, prefix,
  neighbor_address, path_id)`.** Why: communities are path attributes and
  a declared type no row carries is a review finding (record rule 5's
  reasoning for `Settings`). The key follows BGP4-MIB's
  `bgp4PathAttrTable` INDEX (`BGP4-MIB:710-712`) and OpenConfig's
  `"prefix origin path-id"` (`openconfig-rib-bgp-tables.yang:243`), with
  the path identifier from RFC 7911 §3. The atlas advises adjacency first
  and databases rarely (`05-routing.md`, "The shared shape"), so the row
  holds the attributes an operator reads (origin, AS path, next hop, MED,
  local preference, communities, best) and no Adj-RIB-In or Adj-RIB-Out
  views. The prefix family is the AFI; other SAFIs are out of scope.
- **`BgpOrigin` carries BGP4-MIB's integers, `IGP = 1`, `EGP = 2`,
  `INCOMPLETE = 3`** (`BGP4-MIB:782-799`); the ORIGIN attribute's wire
  values are 0 to 2 (RFC 4271 §4.3), so a mapper decoding the attribute
  adds one, and the README names the offset. Why: the MIB integers leave
  zero free for "unreported", so the enum is not a second real-zero
  pass-through. **`BgpAsPathSegmentType` is pass-through: `AS_SET = 1`,
  `AS_SEQUENCE = 2`** (RFC 4271 §4.3), **`AS_CONFED_SEQUENCE = 3`,
  `AS_CONFED_SET = 4`** (RFC 5065 §3).
- **Peer counters live in `BgpPeerCounters` with `last_discontinuity`**
  (record rule 2): `in_update_messages`, `out_update_messages`,
  `in_messages`, `out_messages`, `established_transitions`
  (`BGP4-MIB:304-373`).
- **Negotiated timers are `Duration`s holding whole seconds.** Hold time
  is 0 or 3 to 65535 seconds (RFC 4271 §4.2, "zero or at least three
  seconds"; `BGP4-MIB:406-431`); keepalive is 0 to 21845 seconds
  (`BGP4-MIB:433-456`). A message-level CEL rule enforces both.
- **A peer on an IPv6 link-local address must name its interface.**
  `Ipv6Address` has no zone because "a link-local address is scoped by
  the interface column of whichever table carries it"
  (`spec/proto/flowseer/net/addr/v1/ip.proto`, `Ipv6Address` comment), so
  `BgpPeer.interface_name` is optional and a CEL rule requires it when
  `remote_address` is in fe80::/10.

### OSPF

- **Neighbor state is `OspfNeighborState`, pass-through `DOWN = 1` to
  `FULL = 8`**, identical in OSPF-MIB (`OSPF-MIB:2370-2388`), OSPFV3-MIB
  (`OSPFV3-MIB:2256-2274`), and ietf-ospf (`ietf-ospf.yang:205-250`);
  RFC 2328 §10.1.
- **`OspfInterfaceType` keeps the registry's gap: `BROADCAST = 1`,
  `NBMA = 2`, `POINT_TO_POINT = 3`, `POINT_TO_MULTIPOINT = 5`, no 4**
  (`OSPF-MIB:1542-1560`, `OSPFV3-MIB:1608-1619`; record rule 7). Both
  versions share it.
- **`OspfInterfaceState` is pass-through `DOWN = 1` to
  `OTHER_DESIGNATED_ROUTER = 7` plus `STANDBY = 8`**, which only OSPFv3
  reports (`OSPF-MIB:1657-1673`, `OSPFV3-MIB:1727-1747`).
- **`OspfAreaType` is pass-through from `ospfImportAsExtern`: `NORMAL =
  1` (importExternal), `STUB = 2` (importNoExternal), `NSSA = 3`
  (importNssa)** (`OSPF-MIB:740-756`, `OSPFV3-MIB:674-690`). The values
  are named for the area type and the comment names the MIB label.
- **The neighbor row is keyed by `(interface_name, version,
  protocol_instance, instance_id, neighbor_router_id)`.** OSPFV3-MIB keys
  by `ifIndex, ifInstId, rtrId` (`OSPFV3-MIB:2149-2151`). OSPF-MIB keys by
  `ospfNbrIpAddr, ospfNbrAddressLessIndex` (`OSPF-MIB:2253`); its
  unnumbered-interface trap (atlas, "ospf") is why the row names the
  interface and never relies on the address. `instance_id` is the
  OSPFv3 interface instance (`Ospfv3IfInstIdTC`, 0 to 255,
  `OSPFV3-MIB:151-159`, RFC 5340 §2.4) and is rejected on a version 2 row.
- **The interface row holds area, type, state, priority, cost, and the
  three protocol timers**; designated-router identities, authentication,
  and LSDB state stay out (atlas, "The shared shape").

### IS-IS

- **Adjacency state is `IsisAdjacencyState`, pass-through of
  `isisISAdjState`: `DOWN = 1`, `INITIALIZING = 2`, `UP = 3`,
  `FAILED = 4`** (`spec/mib/ietf/ISIS-MIB:2301-2314`, RFC 4444). OpenConfig
  has the same four labels in another order with implicit ordinals
  (`openconfig-isis-types.yang:323-344`); the MIB's integers are the only
  explicit ones. IOS-XE reports `standby` where the others say failed
  (`Cisco-IOS-XE-isis-oper.yang:66-91`); a mapper leaves that state
  absent rather than guess.
- **`IsisLevel` is pass-through of the `IsisLevel` TC: `LEVEL_1 = 1`,
  `LEVEL_2 = 2`, `LEVEL_1_2 = 3`** (`ISIS-MIB:249-262`), used by the
  instance's `level_type` (`isisSysLevelType`, `:351-367`) and the
  adjacency's `usage` (`isisISAdjUsage`, `:2379-2389`), which is part of
  the adjacency key as it is in IOS-XE's (`system-id level if-name`,
  `Cisco-IOS-XE-isis-oper.yang:147-153`).
- **System ids are 6 octets** (`IsisSystemID`, `ISIS-MIB:110-120`) and
  area addresses 1 to 20 octets (`IsisOSINSAddress`, `SIZE(0..20)`,
  `:103-108`, with the empty value excluded).

### VRRP and HSRP

- **`VrrpGroup` is keyed by `(interface_name, address_family, vrid)`**,
  as VRRPV3-MIB indexes `ifIndex, VrId, InetAddrType`
  (`spec/mib/ietf/VRRPV3-MIB:122-124`) and IOS-XE `if-number group-id
  addr-type` (`Cisco-IOS-XE-vrrp-oper.yang:405-436`). `address_family`
  reuses `flowseer.net.addr.v1.IpVersion`, whose integers are the IANA
  address family numbers that `InetAddressType` ipv4(1) and ipv6(2) also
  use. `vrid` is 1 to 255 (RFC 5798 §5.2.3; `VRRP-MIB:86`).
- **State is `VrrpState`, pass-through `INITIALIZE = 1`, `BACKUP = 2`,
  `MASTER = 3`** (`VRRP-MIB:232-255`, `VRRPV3-MIB:223-246`, RFC 5798
  §6.4). IOS-XE's extra `recover` state (`Cisco-IOS-XE-vrrp-oper.yang:168-193`)
  has no RFC meaning and a mapper leaves it absent.
- **The advertisement interval is a `Duration`** (record rule 1). VRRPv3
  states it in centiseconds, a 12-bit field (RFC 5798 §5.2.7;
  `VRRPV3-MIB:288-299`, `1..4095`); VRRPv2 states it in whole seconds, one
  octet (RFC 3768 §5.3.7). The field takes `gte 10ms`, `lte 255s`, and a
  multiple of 10 ms; a message rule adds `lte 40.95s` for version 3 and
  whole seconds for version 2. IOS-XE reports milliseconds
  (`Cisco-IOS-XE-vrrp-oper.yang:287-291`), which converts exactly.
- **Priority keeps its reserved meanings in the comment, not in a
  rule**: 255 means the router owns the virtual addresses, 0 is sent only
  by a master that stops participating (RFC 5798 §5.2.4;
  `VRRP-MIB:257-278`). Both are values a device reports, so the field
  takes only `lte 255`.
- **VRRPv2 carries IPv4 only** (RFC 3768 §1.2), so a version 2 group with
  `address_family` IPv6 fails; the master, primary, and virtual addresses
  must share the group's family.
- **HSRP gets no package in this phase.** The phase's Goal names eight
  packages and HSRP is not one. The record already decides where it goes
  when it comes: "a later HSRP gets its own" package (record rule 6), so
  answering the parent's open question needs no amendment. Its only
  vendored source is Cisco's (`Cisco-IOS-XE-hsrp-oper.yang:46-91`,
  `:200-206`), and nothing in the tree needs it yet.

### BFD

- **Session state is `BfdSessionState`, pass-through of the RFC 5880
  §4.1 wire values with a real zero: `ADMIN_DOWN = 0`, `DOWN = 1`,
  `INIT = 2`, `UP = 3`** (record rule 7). The IANA MIB TC shifts them by
  one, `adminDown(1) ... up(4)`, and adds `failing(5)` for BFD version 0
  only (RFC 7330, `IANAbfdSessStateTC`); a mapper reading
  `bfdSessState` subtracts one and leaves `failing` absent. The README
  names the offset. Presence carries "unreported"; a consumer checks it
  before the getter (conventions doc, Enums). The 2-bit field is fully
  named, so `enum.defined_only` is the whole domain.
- **Diagnostics are `BfdDiagnostic`, pass-through with a real zero:
  `NO_DIAGNOSTIC = 0` to `REVERSE_CONCATENATED_PATH_DOWN = 8`** (RFC 5880
  §4.1) **and `MIS_CONNECTIVITY_DEFECT = 9`** (RFC 7330,
  `IANAbfdDiagTC`). The Diag field is 5 bits, so each use site takes a
  CEL rule `this <= 31` and later registrations pass through unnamed.
- **`BfdSession` is keyed by `(network_instance, local_discriminator)`.**
  `bfd.LocalDiscr` "MUST be unique across all BFD sessions on this
  system, and nonzero" (RFC 5880 §6.8.1), so it takes `gte 1`
  (`spec/mib/ietf/BFD-STD-MIB:240-247`), and OpenConfig keys peers by it
  (`openconfig-bfd.yang:576-577`). `remote_discriminator` keeps 0,
  which means not yet learned (RFC 5880 §6.8.1; `BFD-STD-MIB:249-261`).
- **Session type is `BfdSessionType`, pass-through of
  `IANAbfdSessTypeTC`: `SINGLE_HOP = 1`, `MULTI_HOP_TOTALLY_ARBITRARY_PATHS
  = 2`, `MULTI_HOP_OUT_OF_BAND_SIGNALING = 3`,
  `MULTI_HOP_UNIDIRECTIONAL_LINKS = 4`** (RFC 7330). `interface_name` is
  optional, since a multihop session has none.
- **Intervals are `Duration`s** stated in microseconds on the wire
  (RFC 5880 §6.8.1; `BFD-STD-MIB:530-570`), which a `Duration` holds
  exactly; IOS-XE reports milliseconds (`Cisco-IOS-XE-bfd-oper.yang:358-369`).
  The detect multiplier is 1 to 255 (RFC 5880 §4.1, one octet; §6.8.1,
  "MUST be a nonzero integer").

### DHCP

- **DHCPv4 and DHCPv6 message types are separate pass-through enums.**
  `Dhcpv4MessageType`: `DISCOVER = 1` to `INFORM = 8` (RFC 2132 §9.6) and
  `FORCERENEW = 9` to `TLS = 18` (IANA BOOTP and DHCP Parameters,
  message type 53). `Dhcpv6MessageType`: `SOLICIT = 1` to
  `RELAY_REPL = 13` (RFC 8415 §7.3); the IANA registry reserves 0 and has
  later values that pass through unnamed. Both use `_UNSPECIFIED = 0`,
  and each use site takes `this <= 255`, the one-octet field of each
  protocol.
- **Their carrier is a per-type count list, `Dhcpv4ServerCounters` and
  `Dhcpv6ServerCounters`**, each a repeated `Dhcpv{4,6}MessageCount
  {message_type, received_messages, sent_messages}` unique on the type,
  plus `last_discontinuity`. Why a list over one field per type: both
  sources count per message type (Huawei `hwDHCPS*PktNum`,
  `HUAWEI-DHCPS-MIB:1736-1835`; FASTPATH `agentDhcp6Server*Messages*`,
  `fastpath_dhcp6.mib:128-243`), and the registries keep growing
  (LEASEQUERY, TLS), which a list absorbs without a schema change.
- **IA_NA and IA_PD are separate messages**, `Dhcpv6IaNa` holding
  `Dhcpv6IaAddress` rows and `Dhcpv6IaPd` holding `Dhcpv6IaPrefix` rows
  (RFC 8415 §21.4, §21.6, §21.21, §21.22; IOS-XE `iana-lst` and
  `iapd-lst`, `Cisco-IOS-XE-dhcp-oper.yang:1346-1394`, `:1463-1468`).
  Each address or prefix carries the landed `IpLifetime`, whose infinity
  handling is already RFC 8415 §7.7's. T1 and T2 stay out: the server
  sends them and the client acts on them, and the binding's state is the
  lifetimes.
- **A DHCPv6 binding is keyed by `(network_instance, duid)`**, the DUID
  3 to 130 octets (a 2-octet type plus 1 to 128 octets, RFC 8415 §11.1);
  MikroTik reports the same `duid` and `iaid`
  (`spec/openapi/mikrotik/routeros-7.24-openapi.json:252015-252089`).
- **A DHCPv4 lease's expiry is a non-required oneof of `expires_at` and
  `infinite`**, as IOS-XE's `dhcp-expiry` choice is
  (`Cisco-IOS-XE-dhcp-oper.yang:499-521`; RFC 2131 §3.3 reserves
  0xffffffff for infinity). Why: a single `Timestamp` would make absence
  mean both "infinite" and "unreported"
  (`docs/solutions/architecture-patterns/one-slot-two-roles-is-a-defect-class-not-a-defect.md`).
  Absence of the oneof means unreported. The pool's lease time uses the
  same shape. `DhcpAllocation` is FlowSeer-normalized from RFC 2131 §1's
  three mechanisms: `AUTOMATIC = 1`, `DYNAMIC = 2`, `MANUAL = 3`.
- **DHCP snooping bindings live in `protocol/dhcp` as
  `DhcpSnoopingBinding`, keyed by `(network_instance, vlan_id, mac,
  address)` with a required `interface_name`.** The VLAN is in every
  vendored key (D-Link `DLINKSW-DHCP-SNOOPING-MIB:405-427`; H3C
  `HH3C-DHCP-SNOOP2-MIB:145-166`; Huawei `HUAWEI-DHCP-SNOOPING-MIB:855-877`),
  and every source carries the port. `expires_in` is the remaining lease
  (H3C "Left lease time", `HH3C-DHCP-SNOOP2-MIB:213-220`); a static
  binding never expires and leaves it absent, as phase 6's
  `GroupMembership.expires_in` does. `DhcpSnoopingBindingKind` is
  `STATIC = 1`, `DYNAMIC = 2`, since Huawei keeps them in two tables
  (`HUAWEI-DHCP-SNOOPING-MIB:855`, `:1388`).
- **Nothing here duplicates `net/endpoint`**: its `dhcp.proto` holds only
  the `dhcp_option_code` rule and its fingerprint the client's request
  list and vendor class
  (`spec/proto/flowseer/net/endpoint/v1/dhcp.proto`,
  `endpoint_fingerprint.proto`).

### DNS

- **`DnsResolver` is one row per network instance** with an ordered
  `servers` list and a `search_domains` list. OpenConfig's
  `system/dns` carries the servers and the ordered `search` leaf-list
  (`spec/yang/cisco/iosxe/2611/openconfig-system.yang:522-534`,
  `:539-696`); IOS-XE's operational resolver state is keyed by network
  instance and lists `name-server {ip-addr, source}`
  (`Cisco-IOS-XE-dns-oper.yang:42-112`); DNS-RESOLVER-MIB carries only
  its safety-belt servers (`spec/mib/ietf/DNS-RESOLVER-MIB:104-229`).
  `DnsServerOrigin` is `STATIC = 1`, `DHCP = 2`, IOS-XE's
  `dns-source-type`. A search domain is 1 to 253 characters (record
  rule 3, "DNS name 253"; RFC 1035 §2.3.4 and RFC 2181 §11 bound the
  wire form at 255 octets) and is not held to hostname syntax, because
  RFC 2181 §11 forbids restricting labels.

### NAT

- **The mapping and the session stay apart, as RFC 4008 keeps
  `natAddrMapTable` and `natSessionTable`** (RFC 4008 §4.2, §4.5).
  `NatMapping` is a configured rule; `NatSession` is one live
  translation. A session is "a session as seen in the private realm and
  in the public realm" (§4.5), so it carries four endpoints named for
  RFC 4008's columns: `private_source`, `private_destination`,
  `public_source`, `public_destination` (`natSessionPrivateSrcAddr` to
  `natSessionPublicDstPort`). IOS-XE's inside local, inside global,
  outside local, and outside global map onto them
  (`Cisco-IOS-XE-nat-oper.yang:184-257`). The sources are required and
  the destinations optional, because H3C reports only inside, global,
  and peer (`HH3C-NAT-MIB:1094-1129`).
- **A mapping's translation is a repeated `NatTranslation`, one value
  per RFC 4008 `NatTranslationEntity` bit, numbered bit position plus
  one**: `INBOUND_SOURCE = 1`, `OUTBOUND_DESTINATION = 2`,
  `INBOUND_DESTINATION = 3`, `OUTBOUND_SOURCE = 4`. Why: a static
  bidirectional mapping sets two bits, and `_UNSPECIFIED = 0` needs the
  offset; the README names it. MikroTik `srcnat` is `OUTBOUND_SOURCE`
  and `dstnat` is `INBOUND_DESTINATION`
  (`routeros-7.24-openapi.json:172989-172997`).
- **`NatMappingKind` is pass-through of `NatAssociationType`:
  `STATIC = 1`, `DYNAMIC = 2`**. `NatSessionDirection` is pass-through of
  `natSessionDirection`: `INBOUND = 1`, `OUTBOUND = 2`.
- **A mapping without `global_addresses` translates to its interface's
  own address** (MikroTik `masquerade`, IOS `overload` on an interface),
  so a CEL rule requires `interface_name` when `global_addresses` is
  absent. RFC 4787's mapping and filtering behaviors stay out: no
  vendored source reports them.
- **`net/nat` imports exactly what its allowlist row permits**:
  `net/addr`, `net/packet` (`IpProtocol`, the `ip_protocol` rule,
  `TransportPortRange`), and `net/key`
  (`test/conformance/proto/layering_test.go:52`).

### Shared

- **Every enum is prefixed with its protocol** (`BgpPeerState`,
  `OspfInterfaceType`) and every row is named for the thing it describes
  (`BgpPeer`, `VrrpGroup`, record rule 5, whose own example is
  `BgpPeer`). The landed `lldp.v1.Neighbor` and `cdp.v1.Neighbor` keep
  their names.
- **Optional enums carry no rule; required ones take `required`,
  `defined_only`, and `not_in: [0]`**, the landed pattern
  (`net/multicast/v1/group_membership.proto`,
  `net/instance/v1/network_instance.proto`). A pass-through enum whose
  field is wider than its named values adds the width rule at the use
  site.
- **No mapper fills these rows in this phase** (parent, Out of scope),
  so nothing here converts a unit, subtracts the BFD MIB offset, adds
  the BGP origin offset, or maps a NAT translation bit. No test in this
  phase covers those conversions; each README records them for the
  mapper that will, whose fixture test must pin them (record,
  Consequences).
- Ruled: rule tests use `fieldCase` and `runFieldCases`
  (`test/conformance/proto/stp_rules_test.go:17-37`), asserting the
  violated field path or rule id, not valid or invalid alone. Why: a
  message rejected by another rule would otherwise pass for the rule
  under test (`docs/solutions/conventions/a-refusal-test-needs-an-input-only-the-refusal-rejects.md`).

## Requirements

1. A protocol-instance name is 1 to 255 characters and every field
   spelling one carries the rule. Example: a `BgpInstance` with
   `protocol_instance ""` fails; a 256-character name fails; `"10"`
   passes. A synthetic message whose `protocol_instance` field carries
   no rule fails `TestKeyFieldsUseKeyRules` with "spells a protocol
   instance name and carries none of protocol_instance_name".
2. A BGP peer accepts a 4-octet AS and enforces its keys and timers.
   Example: `BgpPeer{network_instance "default", protocol_instance
   "default", remote_address 192.0.2.1, remote_asn 4200000000}` passes;
   without `protocol_instance` it fails on that field; `remote_address
   fe80::1` without `interface_name` fails rule `bgp_peer.link_local_interface`
   and with `interface_name "eth0"` passes; `negotiated_hold_time` 2s
   fails, 0s and 3s pass, 90.5s fails; `negotiated_keepalive` 21846s
   fails.
3. A peer's address families are unique and in width. Example: two
   `{afi IPV4, safi UNICAST}` entries fail rule
   `bgp_peer.address_families_unique`; `{afi L2VPN, safi EVPN}` passes;
   `afi` 0 fails; `afi` 65536 fails; `safi` 256 fails; `safi` 200
   (unnamed) passes.
4. A community is exactly one of its two shapes. Example:
   `BgpCommunity{}` fails at oneof `kind`; `{standard: 0xFFFFFF01}`
   passes; `{large: {global_administrator 4200000000,
   local_data_part1 1, local_data_part2 2}}` passes; a large community
   without `local_data_part2` fails. A `BgpAsPathSegment` with no ASNs
   fails; a `BgpPath` without `prefix` fails.
5. OSPF keeps the registry gap and the key split. Example:
   `OspfInterfaceType.Descriptor().Values().ByNumber(4)` is nil;
   `OspfArea` without `network_instance` fails; `OspfArea{area_id 0}`
   with its other keys passes (the backbone); `OspfNeighbor`'s
   descriptor has no field named `network_instance`; an `OspfNeighbor`
   with `version 2` and an IPv6 `neighbor_address` fails; `instance_id 1`
   with `version 2` fails and with `version 3` passes; `version 4`
   fails.
6. IS-IS adjacency state has four values and the adjacency key is
   complete. Example: `IsisAdjacencyState` names exactly 1 to 4 beside
   `_UNSPECIFIED`; an `IsisAdjacency` without `usage` fails; `usage`
   `LEVEL_1_2` passes; a 5-octet `neighbor_system_id` fails; an
   `IsisInstance` area address of 0 or 21 octets fails.
7. VRRP bounds its key and converts no interval ambiguously. Example:
   `vrid` 0 and 256 fail, 1 and 255 pass; `priority` 256 fails, 0 and 255
   pass; `version 2` with `address_family IP_VERSION_V6` fails;
   `advertisement_interval` 1005ms fails, 1s with version 3 passes,
   41s with version 3 fails, 1.5s with version 2 fails, 255s with
   version 2 passes; an IPv6 virtual address in an IPv4 group fails.
8. BFD's real zero is told apart by presence. Example: a `BfdSession`
   with `state ADMIN_DOWN` has `HasState()` true and `GetState()` 0, and
   one without `state` has `HasState()` false; `local_discriminator 0`
   fails; `detect_multiplier` 0 and 256 fail; `local_diagnostic` 9 passes
   and 32 fails; an IPv4 `local_address` with an IPv6 `remote_address`
   fails; the session without `network_instance` fails.
9. DHCP rows hold both protocols' shapes apart. Example: a
   `Dhcpv4Lease` with no expiry arm passes, `infinite: false` fails, a
   1-octet `client_identifier` fails; a `Dhcpv6Binding` with a 2-octet
   `duid` fails, 3 and 130 octets pass, 131 fails; two `ia_na` entries
   with one `iaid` fail; a binding with neither `ia_na` nor `ia_pd`
   fails; a `Dhcpv6IaPrefix` of `2001:db8::1/48` fails (host bits);
   `Dhcpv4ServerCounters` with two counts for `REQUEST` fail, and a count
   with `message_type` 0 fails; a `DhcpSnoopingBinding` without `vlan_id`
   fails and with `vlan_id 4095` fails.
10. A resolver is keyed and bounded. Example: `DnsResolver` without
    `network_instance` fails; a 254-character search domain fails; a
    253-character one passes; a server with `port 0` fails and without
    `port` passes.
11. NAT keeps mapping and session apart and bounded. Example: a
    `NatMapping` with no `translations` fails; `[OUTBOUND_SOURCE,
    OUTBOUND_SOURCE]` fails; one without `global_addresses` and without
    `interface_name` fails and with `interface_name "ether1"` passes; a
    `NatSession` without `public_source` fails; an endpoint `port 65536`
    fails; `protocol` 256 fails; `protocol IP_PROTOCOL_TCP` with all four
    endpoints passes.
12. The conformance gates hold the new shapes: `TestImportOrder`,
    `TestProtoReadmeImports`, `TestProtoReadmeCoverage`,
    `TestKeyFieldsUseKeyRules`, `TestCanonicalUnitSuffixes`,
    `TestCountersCarryDiscontinuity`, and
    `TestEveryDeclaredProtoPackageIsLinked` pass with all eight packages
    present.

## Out of scope

- HSRP, GLBP, VSRP, and a first-hop-redundancy union (record rule 6).
- IKE, IPsec, and WireGuard (parent, Out of scope); PIM and multicast
  routing, MPLS, and RIP.
- Mappers that fill any of these rows from SNMP, YANG, a REST API, or a
  CLI (parent, Out of scope).
- BGP Adj-RIB-In and Adj-RIB-Out, non-unicast paths, extended
  communities (RFC 4360), NOTIFICATION error codes, peer groups, and
  policy.
- OSPF LSDB, virtual links, designated-router identities, and
  authentication; IS-IS circuits, LSPs, and the LSDB.
- VRRP authentication and tracked objects; BFD echo mode and
  authentication.
- DHCP client state (the device's own addresses carry their origin in
  `net/ip`), relay configuration, DHCPv6 pools, T1 and T2, and binding
  states.
- DNS servers the device runs (DNS-SERVER-MIB), static host entries, and
  caches.
- RFC 4787 NAT behavior classes, NAT ALGs, and timeout profiles.
- Protocol-blind projections, such as which gateway owns an address.

## Units

### U1. The protocol-instance key rule in `net/key`

Files: `spec/proto/flowseer/net/key/v1/{key.proto,README.md}`,
`generated/go/proto/flowseer/net/key/v1/`,
`spec/proto/flowseer/net/README.md`,
`test/conformance/proto/{key_rules_test.go,schema_language_test.go}`,
`docs/code-style-proto.md`, `docs/conventions/protobuf.md`, `CONCEPTS.md`
After: none

Change:

- `key.proto` gains `bool protocol_instance_name = 50003` on
  `buf.validate.StringRules`, id `string.protocol_instance_name`,
  message "value must be a protocol instance name of 1 to 255
  characters", expression `!rule || (this.size() >= 1 && this.size() <=
  255)`. The file comment's list of names gains the protocol instance.
- `schema_language_test.go`: `keyClasses` gains `{noun: "a protocol
  instance name", names: namesAProtocolInstance, rules:
  []keyRule{{ext: keyv1.E_ProtocolInstanceName}}}`, where
  `namesAProtocolInstance` is `name == "protocol_instance" ||
  strings.HasSuffix(name, "_protocol_instance")`. The synthetic table of
  `TestKeyFieldsUseKeyRules` gains `ProtocolInstanceWithoutRule` beside
  `NetworkInstanceWithoutRule` with its expected violation text.
- `key_rules_test.go` gains `TestProtocolInstanceNameRule`, built on
  the file's `stringRuleCarrier` (`key_rules_test.go:50`) because no
  schema field carries the rule until U2: empty and 256 characters fail;
  `"1"`, `"10"`, and 255 characters pass.
- `docs/code-style-proto.md`, predefined-rule table: a row
  `StringRules | 50003 | protocol_instance_name |
  spec/proto/flowseer/net/key/v1/key.proto`.
- `docs/conventions/protobuf.md`, Units and keys: a bullet "Protocol
  instance key" after the network-instance one. A routing protocol's
  instance-level rows carry `network_instance` and a required
  `protocol_instance` validated by `protocol_instance_name`; rows keyed
  by an interface carry `protocol_instance` and inherit the network
  instance; the mapper names the instance as the device does, `default`
  when it has none.
- The key README's Contents names the third rule and why it exists;
  `net/README.md`'s `key/v1/` line lists `protocol_instance_name`.
- `CONCEPTS.md`, Network model, gains "Protocol instance" after
  "Network instance": one running instance of a routing protocol inside
  a network instance, such as an OSPF process or an IS-IS tag, named as
  the device names it; its rows name both, and rows keyed by an
  interface name only the protocol instance.

Tests: `TestProtocolInstanceNameRule`; `TestKeyFieldsUseKeyRules` with
the new synthetic case. Requirement 1.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/key/v1 generated/go/proto/flowseer/net/key/v1 spec/proto/flowseer/net/README.md test/conformance/proto/key_rules_test.go test/conformance/proto/schema_language_test.go docs/code-style-proto.md docs/conventions/protobuf.md CONCEPTS.md`

### U2. BGP in `net/protocol/bgp`

Files: `spec/proto/flowseer/net/protocol/bgp/v1/{bgp_instance.proto,bgp_peer.proto,bgp_peer_state.proto,bgp_afi.proto,bgp_safi.proto,bgp_path.proto,bgp_community.proto,bgp_origin.proto,bgp_as_path_segment_type.proto,README.md}`,
`generated/go/proto/flowseer/net/protocol/bgp/v1/`,
`spec/proto/flowseer/net/protocol/README.md`,
`spec/proto/flowseer/net/{addr,key}/v1/README.md`,
`test/conformance/proto/bgp_rules_test.go` (new)
After: U1 (imports its rule; edits the key README it edits)

Change:

- `bgp_instance.proto`, `BgpInstance`: `network_instance = 1`
  (required, `network_instance_name`), `protocol_instance = 2`
  (required, `protocol_instance_name`), `asn = 3` (required uint32; the
  local AS, `bgpLocalAs`, `BGP4-MIB:98`), `router_id = 4` (uint32,
  `gte 1`, `bgpIdentifier`, `:556`).
- `bgp_peer.proto` declares `BgpPeer`, `BgpPeerAddressFamily`, and
  `BgpPeerCounters`, one family. `BgpPeer`: `network_instance = 1`,
  `protocol_instance = 2` (both required with their rules),
  `remote_address = 3` (required `IpAddress`, `bgpPeerRemoteAddr`),
  `interface_name = 4` (`interface_name` rule), `local_address = 5`,
  `remote_asn = 6`, `local_asn = 7` (uint32, no bound), `remote_router_id
  = 8` (uint32, `gte 1`; a source's 0.0.0.0 before the OPEN leaves it
  absent, `:182-193`), `state = 9` (`BgpPeerState`), `enabled = 10`
  (bool, `bgpPeerAdminStatus` start, `:212-231`), `established_time = 11`
  (Duration, `gte 0`; how long the peer has been in, or out of,
  Established, `:375-390`), `negotiated_hold_time = 12` and
  `negotiated_keepalive = 13` (Duration), `address_families = 14`
  (repeated `BgpPeerAddressFamily`), `counters = 15`
  (`BgpPeerCounters`). Message rules: `bgp_peer.link_local_interface`
  (`!has(this.remote_address) || !has(this.remote_address.v6) ||
  !'%x'.format([this.remote_address.v6.octets]).matches('^fe[89ab]') ||
  has(this.interface_name)`); `bgp_peer.hold_time` (absent, or 0s, or
  whole seconds from 3s to 65535s); `bgp_peer.keepalive` (absent, or
  whole seconds up to 21845s), both whole-second checks in the
  `IpLifetime` idiom `this == duration(string(this.getSeconds()) +
  's')`; `bgp_peer.address_families_unique` (no two entries share
  `afi` and `safi`).
  `BgpPeerAddressFamily`: `afi = 1` (required, `not_in [0]`, cel
  `this <= 65535`), `safi = 2` (required, `not_in [0]`, cel
  `this <= 255`), `active = 3` (bool), `received_prefixes = 4`,
  `sent_prefixes = 5`, `installed_prefixes = 6` (uint32,
  `openconfig-bgp-neighbor.yang:632-671`).
  `BgpPeerCounters`: `in_update_messages = 1`, `out_update_messages = 2`,
  `in_messages = 3`, `out_messages = 4`, `established_transitions = 5`
  (uint64), `last_discontinuity = 6` (Timestamp).
- `bgp_peer_state.proto`, `bgp_afi.proto`, `bgp_safi.proto`,
  `bgp_origin.proto`, `bgp_as_path_segment_type.proto`: the enums of the
  Decisions, each value commented with its registry label.
- `bgp_community.proto` declares `BgpCommunity` (required oneof `kind`:
  `uint32 standard = 1`, `BgpLargeCommunity large = 2`) and
  `BgpLargeCommunity` (`global_administrator = 1`, `local_data_part1 =
  2`, `local_data_part2 = 3`, each required uint32, RFC 8092 §3;
  digits cannot follow an underscore under STYLE2024). The
  oneof's comment carries the both-arms sentence from the solution.
- `bgp_path.proto` declares `BgpPath` and `BgpAsPathSegment`.
  `BgpPath`: `network_instance = 1`, `protocol_instance = 2` (required,
  rules), `prefix = 3` (required `IpPrefix`), `neighbor_address = 4`
  (`IpAddress`; absent for a locally originated path), `path_id = 5`
  (uint32; absent unless ADD-PATH was negotiated, RFC 7911 §3),
  `origin = 6` (`BgpOrigin`), `as_path = 7` (repeated
  `BgpAsPathSegment`, in path order), `next_hop = 8` (`IpAddress`),
  `med = 9` and `local_preference = 10` (uint32, RFC 4271 §4.3 and
  §5.1.5; BGP4-MIB's -1 for "absent" leaves them absent, `:856-894`),
  `communities = 11` (repeated `BgpCommunity`), `best = 12` (bool,
  `bgp4PathAttrBest`, `:970-983`). `BgpAsPathSegment`: `type = 1`
  (required, `defined_only`, `not_in [0]`; the four RFC values are the
  whole set), `asns = 2` (repeated uint32, `min_items 1`,
  `max_items 255`, the one-octet segment length of RFC 4271 §4.3).
- The package README: Boundaries (`Imports: net/addr, net/key`;
  `Imported by: nothing`), the keys and the `default` naming, the peer
  and path rows, why communities are one typed list, the origin offset,
  what stays out, and Sources (BGP4-MIB, IANA registries, RFCs 4271,
  6286, 6793, 7911, 1997, 8092, 5065, the OpenConfig modules, IOS-XE).
- `net/protocol/README.md`: the `bgp/v1/` line loses its planned marker
  and names instances, peers, address families, and paths with
  communities. `Imported by:` of `net/addr` and `net/key` gain
  `net/protocol/bgp`.

Tests: `bgp_rules_test.go`, `TestBgpInstanceRules`, `TestBgpPeerRules`,
`TestBgpPeerAddressFamilyRules`, `TestBgpCommunityRules`,
`TestBgpPathRules`: Requirements 2 to 4, each failing case asserting its
field path or rule id.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/protocol/bgp/v1 generated/go/proto/flowseer/net/protocol/bgp/v1 spec/proto/flowseer/net/protocol/README.md spec/proto/flowseer/net/addr/v1/README.md spec/proto/flowseer/net/key/v1/README.md test/conformance/proto/bgp_rules_test.go`

### U3. OSPF and IS-IS in `net/protocol/ospf` and `net/protocol/isis`

Files: `spec/proto/flowseer/net/protocol/ospf/v1/{ospf_instance.proto,ospf_area.proto,ospf_area_type.proto,ospf_interface.proto,ospf_interface_type.proto,ospf_interface_state.proto,ospf_neighbor.proto,ospf_neighbor_state.proto,README.md}`,
`spec/proto/flowseer/net/protocol/isis/v1/{isis_instance.proto,isis_adjacency.proto,isis_adjacency_state.proto,isis_level.proto,README.md}`,
`generated/go/proto/flowseer/net/protocol/{ospf,isis}/v1/`,
`spec/proto/flowseer/net/protocol/README.md`,
`spec/proto/flowseer/net/{addr,key}/v1/README.md`,
`test/conformance/proto/{ospf_rules_test.go,isis_rules_test.go}` (new)
After: U2 (edits the READMEs U2 edits)

Change:

- `OspfInstance`: `network_instance = 1`, `version = 2` (required,
  `gte 2`, `lte 3`), `protocol_instance = 3` (required keys with rules),
  `router_id = 4` (uint32, `OSPF-MIB:210-216`), `enabled = 5` (bool,
  `ospfAdminStat`, `:227-233`).
- `OspfArea`: `network_instance = 1`, `version = 2`, `protocol_instance
  = 3` (required), `area_id = 4` (required uint32; 0 is the backbone),
  `area_type = 5` (`OspfAreaType`), `lsa_count = 6`,
  `area_border_routers = 7`, `as_border_routers = 8` (uint32 gauges,
  `OSPF-MIB:773-803`).
- `OspfInterface`: `interface_name = 1` (required, rule), `version = 2`,
  `protocol_instance = 3` (required), `instance_id = 4` (uint32,
  `lte 255`), `area_id = 5` (required), `interface_type = 6`,
  `state = 7`, `priority = 8` (`lte 255`, `DesignatedRouterPriority`,
  `OSPF-MIB:153-159`), `cost = 9` (`lte 65535`, `Metric`, `:101-108`,
  `:1955-1963`), `hello_interval = 10`, `dead_interval = 11`,
  `retransmit_interval = 12` (Duration, `gte 0`, `:1604-1643`). Message
  rule `ospf_interface.instance_id_is_v3` (`!has(this.instance_id) ||
  this.version == 3u`).
- `OspfNeighbor`: `interface_name = 1`, `version = 2`, `protocol_instance
  = 3` (required), `instance_id = 4` (as above), `neighbor_router_id = 5`
  (required uint32), `neighbor_address = 6` (`IpAddress`), `state = 7`
  (`OspfNeighborState`), `priority = 8` (`lte 255`). Rules
  `ospf_neighbor.instance_id_is_v3` and
  `ospf_neighbor.address_family` (version 2 requires `v4`, version 3
  requires `v6`, when the address is present). No `network_instance`
  field on the interface or neighbor row.
- The four OSPF enums of the Decisions; `OspfInterfaceType`'s comment
  says value 4 is unassigned in the MIB and stays so.
- `IsisInstance`: `network_instance = 1`, `protocol_instance = 2`
  (required), `system_id = 3` (bytes, `len 6`), `level_type = 4`
  (`IsisLevel`), `area_addresses = 5` (repeated bytes, `unique`, items
  `min_len 1`, `max_len 20`).
- `IsisAdjacency`: `interface_name = 1`, `protocol_instance = 2`
  (required), `neighbor_system_id = 3` (required, `len 6`), `usage = 4`
  (required `IsisLevel`, `defined_only`, `not_in [0]`), `state = 5`
  (`IsisAdjacencyState`), `neighbor_snpa = 6` (`MacAddress`; absent on a
  point-to-point circuit), `neighbor_addresses = 7` (repeated
  `IpAddress`, `isisISAdjIPAddrTable`, `ISIS-MIB:2493-2519`),
  `hold_time = 8` (Duration, `gte 0`, `lte 65535s`, `:2391-2404`),
  `priority = 9` (`lte 127`, `IsisISPriority`, `:277-283`).
- Both READMEs follow the lldp README's shape: Boundaries (`Imports:
  net/addr, net/key`; `Imported by: nothing`), the key split between
  instance-level and interface-keyed rows, the version key (ospf), why the
  area id is one `uint32` and how a mapper converts a dotted quad, the
  state and level integers (isis, naming the OpenConfig and IOS-XE
  divergence), what stays out, and Sources.
- `net/protocol/README.md`: the `ospf/v1/` and `isis/v1/` lines lose
  their planned markers. `Imported by:` of `net/addr` and `net/key` gain
  both packages.

Tests: `ospf_rules_test.go` (`TestOspfInterfaceTypeKeepsTheGap`,
`TestOspfAreaRules`, `TestOspfInterfaceRules`, `TestOspfNeighborRules`,
the last asserting through the descriptor that no `network_instance`
field exists) and `isis_rules_test.go` (`TestIsisAdjacencyStateValues`,
`TestIsisInstanceRules`, `TestIsisAdjacencyRules`): Requirements 5 and 6.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/protocol/ospf/v1 spec/proto/flowseer/net/protocol/isis/v1 generated/go/proto/flowseer/net/protocol/ospf/v1 generated/go/proto/flowseer/net/protocol/isis/v1 spec/proto/flowseer/net/protocol/README.md spec/proto/flowseer/net/addr/v1/README.md spec/proto/flowseer/net/key/v1/README.md test/conformance/proto/ospf_rules_test.go test/conformance/proto/isis_rules_test.go`

### U4. VRRP and BFD in `net/protocol/vrrp` and `net/protocol/bfd`

Files: `spec/proto/flowseer/net/protocol/vrrp/v1/{vrrp_group.proto,vrrp_state.proto,README.md}`,
`spec/proto/flowseer/net/protocol/bfd/v1/{bfd_session.proto,bfd_session_state.proto,bfd_diagnostic.proto,bfd_session_type.proto,README.md}`,
`generated/go/proto/flowseer/net/protocol/{vrrp,bfd}/v1/`,
`spec/proto/flowseer/net/protocol/README.md`,
`spec/proto/flowseer/net/{addr,key}/v1/README.md`,
`docs/conventions/protobuf.md`,
`test/conformance/proto/{vrrp_rules_test.go,bfd_rules_test.go}` (new)
After: U3 (edits the READMEs U3 edits), U1 (edits the conventions doc
U1 edits)

Change:

- `vrrp_group.proto` declares `VrrpGroup` and `VrrpGroupCounters`.
  `VrrpGroup`: `interface_name = 1` (required, rule), `address_family =
  2` (required `IpVersion`, `defined_only`, `not_in [0]`), `vrid = 3`
  (required, `gte 1`, `lte 255`), `version = 4` (`gte 2`, `lte 3`),
  `state = 5` (`VrrpState`), `priority = 6` (`lte 255`; the comment
  states the 0 and 255 meanings), `master_address = 7`,
  `primary_address = 8` (`IpAddress`), `virtual_addresses = 9`
  (repeated `IpAddress`), `virtual_mac = 10` (`MacAddress`),
  `advertisement_interval = 11` (Duration, `gte 10ms`, `lte 255s`, cel
  `vrrp_group.advertisement_interval_centiseconds`:
  `this.getMilliseconds() % 10 == 0 && this ==
  duration(string(this.getMilliseconds()) + 'ms')`), `preempt = 12`,
  `accept_mode = 13` (bool, `VRRP-MIB:332-355`), `counters = 14`.
  Message rules: `vrrp_group.v2_is_ipv4`,
  `vrrp_group.v3_interval` (version 3 caps the interval at 40.95s),
  `vrrp_group.v2_interval` (version 2 requires whole seconds), and
  `vrrp_group.address_family` (master, primary, and every virtual
  address carry the arm `address_family` names).
  `VrrpGroupCounters`: `master_transitions = 1`,
  `received_advertisements = 2` (uint64, `VRRP-MIB:610-638`),
  `last_discontinuity = 3`.
- `VrrpState` as the Decisions state.
- `bfd_session.proto` declares `BfdSession` and `BfdSessionCounters`.
  `BfdSession`: `network_instance = 1` (required, rule),
  `local_discriminator = 2` (required, `gte 1`), `remote_discriminator =
  3` (uint32; 0 means not yet learned), `session_type = 4`,
  `interface_name = 5` (rule; absent for a multihop session),
  `local_address = 6`, `remote_address = 7` (required `IpAddress`),
  `state = 8` and `remote_state = 9` (`BfdSessionState`,
  `defined_only`), `local_diagnostic = 10` and `remote_diagnostic = 11`
  (`BfdDiagnostic`, cel `this <= 31`), `desired_min_tx_interval = 12`,
  `required_min_rx_interval = 13`, `remote_min_rx_interval = 14`
  (Duration, `gte 0`, `lte 4294.967295s`, the uint32 microsecond field),
  `detect_multiplier = 15` (`gte 1`, `lte 255`), `counters = 16`. Rule
  `bfd_session.address_family` (local and remote addresses share a
  family when both are present).
  `BfdSessionCounters`: `in_packets = 1`, `out_packets = 2`,
  `up_transitions = 3` (uint64, `BFD-STD-MIB:746-884`),
  `last_discontinuity = 4`.
- `BfdSessionState`, `BfdDiagnostic`, and `BfdSessionType` as the
  Decisions state. The state and diagnostic comments say zero is a real
  value and presence carries "unreported".
- `docs/conventions/protobuf.md`, Enums: the pass-through paragraph's
  real-zero examples gain `BFD_SESSION_STATE_ADMIN_DOWN` and
  `BFD_DIAGNOSTIC_NO_DIAGNOSTIC`, and its width sentence names
  `BfdDiagnostic`'s `0..31`.
- READMEs: vrrp (Boundaries `Imports: net/addr, net/key`; the key, the
  two interval units and their conversion, the priority meanings, v2 as
  IPv4 only, HSRP's future package) and bfd (Boundaries `Imports:
  net/addr, net/key`; the key and discriminator rules, the RFC 5880
  integers and the MIB's offset of one, `failing`, microsecond
  intervals). Both list what stays out and their Sources.
- `net/protocol/README.md`: the `vrrp/v1/` and `bfd/v1/` lines lose
  their planned markers. `Imported by:` of `net/addr` and `net/key` gain
  both packages.

Tests: `vrrp_rules_test.go`, `TestVrrpGroupRules`, holds Requirement 7;
`bfd_rules_test.go`, `TestBfdSessionStatePresence` and
`TestBfdSessionRules`, holds Requirement 8.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/protocol/vrrp/v1 spec/proto/flowseer/net/protocol/bfd/v1 generated/go/proto/flowseer/net/protocol/vrrp/v1 generated/go/proto/flowseer/net/protocol/bfd/v1 spec/proto/flowseer/net/protocol/README.md spec/proto/flowseer/net/addr/v1/README.md spec/proto/flowseer/net/key/v1/README.md docs/conventions/protobuf.md test/conformance/proto/vrrp_rules_test.go test/conformance/proto/bfd_rules_test.go`

### U5. DHCP and DNS in `net/protocol/dhcp` and `net/protocol/dns`

Files: `spec/proto/flowseer/net/protocol/dhcp/v1/{dhcpv4_message_type.proto,dhcpv6_message_type.proto,dhcp_allocation.proto,dhcpv4_lease.proto,dhcpv4_pool.proto,dhcpv6_binding.proto,dhcpv4_server_counters.proto,dhcpv6_server_counters.proto,dhcp_snooping_binding.proto,dhcp_snooping_binding_kind.proto,README.md}`,
`spec/proto/flowseer/net/protocol/dns/v1/{dns_resolver.proto,dns_server_origin.proto,README.md}`,
`generated/go/proto/flowseer/net/protocol/{dhcp,dns}/v1/`,
`spec/proto/flowseer/net/protocol/README.md`,
`spec/proto/flowseer/net/{addr,key,switching}/v1/README.md`,
`test/conformance/proto/{dhcp_rules_test.go,dns_rules_test.go}` (new)
After: U4 (edits the READMEs U4 edits)

Change:

- `Dhcpv4Lease`: `network_instance = 1` (required, rule), `address = 2`
  (required `Ipv4Address`), `pool_name = 3` (`min_len 1`, `max_len 255`),
  `client_hardware_address = 4` (`MacAddress`), `client_identifier = 5`
  (bytes, `min_len 2`, `max_len 255`, RFC 2132 §9.14), `host_name = 6`
  (`min_len 1`, `max_len 255`, RFC 2132 §3.14), `allocation = 7`
  (`DhcpAllocation`), oneof `expiry` { `expires_at = 10` (Timestamp),
  `infinite = 11` (bool, `const true`) }. The oneof's arms start at 10,
  leaving 1 to 9 for the other fields (conventions doc, Field
  numbering).
- `Dhcpv4Pool`: `network_instance = 1`, `name = 2` (required,
  `min_len 1`, `max_len 255`), `subnet = 3` (`Ipv4Prefix`), `ranges = 4`
  (repeated `Ipv4Range`), `default_routers = 5` and `dns_servers = 6`
  (repeated `Ipv4Address`), `domain_name = 7` (`min_len 1`,
  `max_len 253`), `total_addresses = 8`, `leased_addresses = 9` (uint32,
  `Cisco-IOS-XE-dhcp-oper.yang:1116-1170`), oneof `lease_time`
  { `lease_duration = 10` (Duration, `gt 0s`), `infinite = 11` (bool,
  `const true`) }. Rule `dhcpv4_pool.leased_le_total`. IOS-XE's
  excluded-address count stays out, so the other fields fit below the
  oneof's block.
- `dhcpv6_binding.proto` declares the family `Dhcpv6Binding`,
  `Dhcpv6IaNa`, `Dhcpv6IaPd`, `Dhcpv6IaAddress`, `Dhcpv6IaPrefix`.
  `Dhcpv6Binding`: `network_instance = 1` (required, rule), `duid = 2`
  (required bytes, `min_len 3`, `max_len 130`), `pool_name = 3`,
  `ia_na = 4` (repeated `Dhcpv6IaNa`), `ia_pd = 5` (repeated
  `Dhcpv6IaPd`); rules `dhcpv6_binding.has_ia` (at least one IA),
  `dhcpv6_binding.ia_na_iaid_unique`, `dhcpv6_binding.ia_pd_iaid_unique`.
  `Dhcpv6IaNa`: `iaid = 1` (required uint32), `addresses = 2` (repeated
  `Dhcpv6IaAddress`). `Dhcpv6IaPd`: `iaid = 1`, `prefixes = 2`.
  `Dhcpv6IaAddress`: `address = 1` (required `Ipv6Address`),
  `lifetime = 2` (`IpLifetime`). `Dhcpv6IaPrefix`: `prefix = 1`
  (required `Ipv6Prefix`), `lifetime = 2`.
- `dhcpv4_server_counters.proto`: `Dhcpv4ServerCounters` (`messages = 1`,
  repeated `Dhcpv4MessageCount`, rule `unique message_type`;
  `last_discontinuity = 2`) and `Dhcpv4MessageCount` (`message_type = 1`,
  required, `not_in [0]`, cel `this <= 255`; `received_messages = 2`,
  `sent_messages = 3`, uint64). `dhcpv6_server_counters.proto` the same
  for v6.
- `DhcpSnoopingBinding`: `network_instance = 1`, `vlan_id = 2`
  (required, `vlan_id` rule from `net/switching`, 1 to 4094, as
  `GroupMembership.vlan_id` takes), `mac = 3` (required `MacAddress`), `address = 4` (required
  `IpAddress`; IPv6 bindings from DHCPv6 snooping fit the same row),
  `interface_name = 5` (required, rule), `kind = 6`
  (`DhcpSnoopingBindingKind`), `expires_in = 7` (Duration, `gte 0`;
  absent means unreported, or a static binding that never expires).
- The four enums of the Decisions.
- `dns_resolver.proto` declares `DnsResolver` and `DnsServer`.
  `DnsResolver`: `network_instance = 1` (required, rule), `servers = 2`
  (repeated `DnsServer`, in preference order), `search_domains = 3`
  (repeated string, `unique`, items `min_len 1`, `max_len 253`, in
  search order). `DnsServer`: `address = 1` (required `IpAddress`),
  `port = 2` (`gte 1`, `lte 65535`; absent means 53), `origin = 3`
  (`DnsServerOrigin`), `interface_name = 4` (rule; names the interface a
  link-local server address is scoped by).
- READMEs: dhcp (Boundaries `Imports: net/addr, net/key,
  net/switching`; the per-row keys, why message types are counted in a
  list, IA_NA and IA_PD, the expiry oneof and why, snooping bindings and
  their sources, the `net/endpoint` boundary) and dns (Boundaries
  `Imports: net/addr, net/key`; per-instance resolver, the 253 bound and
  the RFC 2181 reason for no hostname rule).
- `net/protocol/README.md`: the `dhcp/v1/` and `dns/v1/` lines lose
  their planned markers. `Imported by:` of `net/addr` and `net/key` gain
  both packages; `net/switching`'s gains `net/protocol/dhcp`.

Tests: `dhcp_rules_test.go` (`TestDhcpv4LeaseRules`,
`TestDhcpv4PoolRules`, `TestDhcpv6BindingRules`,
`TestDhcpServerCountersRules`, `TestDhcpSnoopingBindingRules`) holds
Requirement 9; `dns_rules_test.go` (`TestDnsResolverRules`) holds
Requirement 10.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/protocol/dhcp/v1 spec/proto/flowseer/net/protocol/dns/v1 generated/go/proto/flowseer/net/protocol/dhcp/v1 generated/go/proto/flowseer/net/protocol/dns/v1 spec/proto/flowseer/net/protocol/README.md spec/proto/flowseer/net/addr/v1/README.md spec/proto/flowseer/net/key/v1/README.md spec/proto/flowseer/net/switching/v1/README.md test/conformance/proto/dhcp_rules_test.go test/conformance/proto/dns_rules_test.go`

### U6. NAT mappings and sessions in `net/nat`

Files: `spec/proto/flowseer/net/nat/v1/{nat_mapping.proto,nat_mapping_kind.proto,nat_translation.proto,nat_session.proto,nat_session_direction.proto,README.md}`,
`generated/go/proto/flowseer/net/nat/v1/`,
`spec/proto/flowseer/net/README.md`,
`spec/proto/flowseer/net/{addr,key,packet}/v1/README.md`,
`test/conformance/proto/nat_rules_test.go` (new), `CONCEPTS.md`
After: U5 (edits the `addr` and `key` READMEs U5 edits), U1
(`net/README.md`, `CONCEPTS.md`)

Change:

- `NatMapping`: `network_instance = 1` (required, rule), `name = 2`
  (`min_len 1`, `max_len 255`; `natAddrMapName`), `interface_name = 3`
  (rule; the interface the mapping applies on), `kind = 4` (required
  `NatMappingKind`, `defined_only`, `not_in [0]`), `translations = 5`
  (repeated `NatTranslation`, `min_items 1`, `unique`, items
  `not_in [0]`), `protocol = 6` (`IpProtocol`, `ip_protocol` rule;
  absent means every protocol), `local_addresses = 7` (required
  `IpRange`), `local_ports = 8` (`TransportPortRange`),
  `global_addresses = 9` (`IpRange`), `global_ports = 10`
  (`TransportPortRange`). Rule `nat_mapping.global_or_interface`
  (`has(this.global_addresses) || has(this.interface_name)`).
- `nat_session.proto` declares `NatSession`, `NatEndpoint`, and
  `NatSessionCounters`. `NatSession`: `network_instance = 1`
  (required, rule), `protocol = 2` (required `IpProtocol`, `ip_protocol`
  rule), `private_source = 3` (required), `private_destination = 4`,
  `public_source = 5` (required), `public_destination = 6`
  (`NatEndpoint`), `direction = 7` (`NatSessionDirection`),
  `expires_in = 8` (Duration, `gte 0`; the idle time left before the
  session is removed: MikroTik `timeout`, H3C `LeftTime`, RFC 4008
  `natSessionMaxIdleTime` minus `natSessionCurrentIdleTime`),
  `counters = 9`. `NatEndpoint`: `address = 1` (required `IpAddress`),
  `port = 2` (`lte 65535`; absent for a protocol without ports).
  `NatSessionCounters`: `in_packets = 1`, `out_packets = 2`,
  `in_bytes = 3`, `out_bytes = 4` (uint64; RFC 4008
  `natSessionInTranslates`, MikroTik `repl-packets`, `orig-bytes`),
  `last_discontinuity = 5`.
- The three enums of the Decisions; `NatTranslation`'s comment names
  the RFC 4008 bit each value is, plus one.
- The package README: Boundaries (`Imports: net/addr, net/key,
  net/packet`; `Imported by: nothing`), why mapping and session are two
  rows, the four-endpoint session and the IOS-XE and H3C mappings onto
  it, the translation offset, the interface-address case, what stays
  out, and Sources (RFC 4008, RFC 2663, RFC 3022, the IOS-XE, H3C,
  Huawei, and MikroTik sources).
- `net/README.md`: the `nat/v1/` line loses its planned marker.
  `Imported by:` of `net/addr`, `net/key`, and `net/packet` gain
  `net/nat`.
- `CONCEPTS.md`, Network model, gains "NAT mapping and session": a
  mapping is a configured rule that says which addresses and ports
  translate to which; a session is one live translation, the same
  conversation seen in the private and the public realm.

Tests: `nat_rules_test.go`, `TestNatMappingRules` and
`TestNatSessionRules`, holds Requirement 11.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- spec/proto/flowseer/net/nat/v1 generated/go/proto/flowseer/net/nat/v1 spec/proto/flowseer/net/README.md spec/proto/flowseer/net/addr/v1/README.md spec/proto/flowseer/net/key/v1/README.md spec/proto/flowseer/net/packet/v1/README.md test/conformance/proto/nat_rules_test.go CONCEPTS.md`

Waves: U1 | U2 | U3 | U4 | U5 | U6

The chain is forced by shared lines, not by imports after U1: every
unit adds its packages to the one `Imported by:` line of
`net/key/v1/README.md` and of `net/addr/v1/README.md`, and
`TestProtoReadmeImports` fails a unit whose own packages are missing
from those lines, so no unit can defer the edit to a later one. Phase 5
(on `Aledantee/p5-implement`) and phase 8 edit some of the same
`Imported by:` lines, `CONCEPTS.md`, and the conventions doc's Enums
paragraph; resolve those merges by taking the union of both sides, and
renumber this phase's `StringRules` extension only if another phase
lands 50003 first.

## Verification

```bash
buf lint
buf generate && git status --porcelain generated/   # empty after each unit's commit
go build ./... && go vet ./...
go test -race ./test/conformance/proto/...
.claude/skills/verify-change/scripts/verify-change.sh -- <union of the six units' Verify paths>
```

Run the verifier over the union of changed paths, not with `--full`,
which builds and race-tests `generated/go/yang` and exhausts host memory.
No Go consumer outside `test/conformance` imports these packages, and no
landed message changes, so `netmodel` and `snmpmap` need no run. No lab
check: no mapper fills these rows in this phase.

## Definition of done

- [ ] Verifier green for every changed path of every unit.
- [ ] The eight package READMEs, `net/README.md`,
      `net/protocol/README.md`, the `addr`, `key`, `packet`, and
      `switching` READMEs, `CONCEPTS.md`, `docs/conventions/protobuf.md`,
      and the predefined-rule table in `docs/code-style-proto.md` match
      the tree in the unit that changed them.
- [ ] Every requirement above has its test case, each failing case
      asserting its field path or rule id.
- [ ] No plan label appears in schema, code, comments, or commit
      messages.
- [ ] This plan's `status` is `implemented` with an outcome note under
      the title; the parent's U7 `Landed:` line carries the commit range,
      and the parent's HSRP open question is closed with a pointer to
      this plan's HSRP decision.

## Open questions

None block implementation.

- Whether the record's package-tree line for `net/key`
  (`docs/architecture/2026-09-25-schema-building-blocks-direction.md:47`,
  "interface_name, network_instance_name") should name
  `protocol_instance_name` once U1 lands. Options: leave the record as a
  snapshot, since rule 3's text already covers any `net/key` rule (this
  plan's reading); or add one line under its Amended decisions.
  Recommendation: the one-line amendment, because readers use the tree
  as an inventory. Amending an accepted record is the user's call, so
  this plan does not edit it.
- `BgpPath` has the least second-source support of any decision here.
  The record's tree promises communities in `protocol/bgp`, and a path
  row is the only carrier the sources offer, while the atlas advises
  against modelling BGP databases. If review prefers no path table,
  communities lose their carrier and `BgpCommunity` goes with it, which
  the record's tree line would then need to drop.
