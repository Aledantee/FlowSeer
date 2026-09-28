---
title: Schema Building Blocks - Direction
type: direction
date: 2026-09-25
topic: schema-building-blocks
status: accepted-direction
amends: docs/architecture/2026-08-20-network-model-structure-direction.md
---

# Schema Building Blocks - Direction

FlowSeer's schema covers 18 of the 101 entities the
[network domain atlas](../research/network-domain-atlas/INDEX.md) maps. This
record fixes where the rest go and the rules every new message follows, so
that wireless, RF, endpoint, platform, routing, and service schemas land as
one language instead of nine dialects. It extends the
[network model structure record](2026-08-20-network-model-structure-direction.md),
which still governs the primitive/entity split, the import order, the
interface shape, and the protobuf conventions; where this record changes
one of its decisions it says so under Amended decisions.

The evidence is nine research dossiers written for this record, each
citing the standard, the vendored spec under `spec/`, or the provider API
page it read: [`docs/research/schema-building-blocks/`](../research/schema-building-blocks/README.md).

## Decision in one paragraph

Every physical quantity has one canonical unit, named in the field, in
integer fixed point. Two new leaf packages hold what every domain shares:
`net/key` for the predefined rules that validate a device-local key
(interface name, network-instance name), and `net/measure` for sensor
readings, percentages, and path quality. Every table whose key is not
already scoped by an interface carries a required network-instance name.
Each new domain gets a function-named package under `net/`, each protocol
its own `net/protocol/<x>`, and no schema message unions two protocols'
tables. Four entity families join `model/`: Endpoint (the wired, wireless,
or discovered host), Wlan (the configured wireless network), Alarm, and
the syslog record in `event/`. A radio is a Component, not a new entity.

## The package tree after this record

New packages are marked `+`; changed ones `~`. Everything else is as the
structure record's amendments leave it.

```
net/
  key/v1/          + predefined rules for device-local keys: interface_name, network_instance_name, protocol_instance_name
  measure/v1/      + SensorReading (temperature, voltage, current, power, rotation, humidity) with
                     thresholds; basis_points rule; PathQuality (latency, jitter, loss)
  addr/v1/         ~ EuiAddress renamed MacAddress
  packet/v1/
  phy/v1/          ~ module diagnostics and PoE on net/measure; units normalized; settings carried
  instance/v1/     + NetworkInstance row and NetworkInstanceKind
  switching/v1/    ~ Vlan and FdbEntry keyed by network instance
  ip/v1/           ~ IpFacet names its network instance; AddressOrigin split from IID method
  routing/v1/      + Route, NextHop, NextHopGroup, RouteSourceProtocol, RIB/FIB discriminator
  filter/v1/       ~ L2 match terms
  qos/v1/          + trust mode, classifier terms, queues
  nat/v1/          + NAT mappings and sessions
  wlan/v1/         + RadioFacet, Bss, WlanSecurity, channel utilization, neighbor-scan rows
  cellular/v1/     + cellular radio facts and signal quality
  endpoint/v1/     + wired and wireless attachment, fingerprint, per-endpoint counters
  portaccess/v1/   + port-access sessions (802.1X, MAC authentication, web authentication)
  system/v1/       + resource utilization, software images, licenses
  multicast/v1/    + IGMP/MLD snooping group membership
  aaa/v1/          + RADIUS and TACACS+ server identity
  flow/v1/         + flow-export settings (sFlow, NetFlow, IPFIX)
  log/v1/          + syslog severity and facility (RFC 5424 registries)
  interface/v1/
  capture/v1/
  protocol/
    lldp/v1/       ~ IEEE 802.3 and LLDP-MED extensions
    stp/v1/        ~ MSTIs and the VLAN-to-MSTI map (spanning-tree instances, unrelated to net/instance)
    lacp/v1/
    cdp/v1/        + CDP neighbors
    ntp/v1/        + NTP associations
    dhcp/v1/       + leases, server pools, snooping bindings
    dns/v1/        + resolver configuration and servers
    bgp/v1/        + peers, address families, communities
    ospf/v1/       + neighbors, areas, interface types (v2 and v3)
    isis/v1/       + adjacencies and levels
    vrrp/v1/       + VRRP groups (v2 and v3)
    bfd/v1/        + sessions and diagnostics
model/
  inventory/v1/    ~ Component gains the radio kind, a radio facet, and sensor rows;
                     DeviceState gains system contact, location, and uptime
  wireless/v1/     + Wlan entity (WlanConfig, WlanState, WlanEvent)
  endpoint/v1/     + Endpoint entity (EndpointState, EndpointEvent)
  alarm/v1/        + Alarm entity (AlarmState, AlarmEvent), owned by a device
event/
  log/v1/          + SyslogRecord
```

A domain the atlas lists and this tree omits (DSL, PON, DOCSIS, Fibre
Channel, MPLS, EVPN/VXLAN, MACsec, ring protection, tunnels) has no
package until a plan needs it; the atlas found none of them on the device
classes FlowSeer targets now. When one arrives it follows the same rules.

Landed 2026-09-25 to 2026-09-26: protobuf building blocks across eight phases in
`spec/proto/flowseer` and `test/conformance/proto`.

## The schema-language rules

These apply to every FlowSeer-owned message, existing and new. The
conventions doc and the style doc restate the ones a schema author checks
per field; this record holds the reasons.

### 1. One canonical unit per quantity

| Quantity | Wire type | Field suffix | Why this unit |
| --- | --- | --- | --- |
| Point in time | `google.protobuf.Timestamp` | none | Already universal in `net/` and `model/`. |
| Time span | `google.protobuf.Duration` | none | Every span, including protocol intervals stated in TU or centiseconds (beacon interval, VRRP advertisement), converts exactly. |
| Data rate | `uint64` | `_bps` | Matches every `speed_bps` field; `nominal_bit_rate_mbps` converts exactly. |
| Data size and byte counters | `uint64` | `_bytes` | Provider APIs say bytes; octet and byte are the same unit on every target, so one word. |
| Frequency and channel width | `uint32` | `_mhz` | Every 802.11 channel center frequency is a whole number of MHz (2407 + 5n, 5000 + 5n, 5950 + 5n, 56160 + 2160n; dossier 09), as is every width; no targeted band needs kHz. |
| Linear power | `uint64` | `_nanowatts` | Optical diagnostics resolve 0.1 µW (SFF-8472) as an unsigned register, so a reading of zero is possible and has no logarithmic value; a 3 kW supply is 3·10¹² nW, well inside `uint64`. |
| Power level and gain | `sint32` | `_millidbm`, `_millidb`, `_millidbi` | RF sources report whole dBm (dossier 02), cellular RSRQ steps in 0.5 dB (3GPP TS 36.133 as quoted in dossier 07; lower confidence), and OpenConfig optics carry two decimals (`avg-min-max-instant-stats-precision2-dBm`, `spec/yang/cisco/iosxe/2611/openconfig-types.yang:296`); milli-units hold all three exactly. |
| Temperature | `sint32` | `_millidegrees_celsius` | Keeps today's resolution and names the scale. |
| Voltage | `sint32` | `_microvolts` | Signed, because −48 V telecom rails exist. |
| Current | `sint32` | `_microamperes` | Signed for the same reason. |
| Rotation speed | `uint32` | `_rpm` | Fan tachometers report whole RPM. |
| Percentage and ratio | `uint32` | `_basis_points` | 0–10000; holds whole-percent sources exactly and the fractional ones (Meraki channel utilization, CPU load) to 0.01 %. |

The alternative the audit dossier argued for, carrying each source's
native unit, was rejected: a consumer comparing an optical reading from
SNMP with one from a vendor API would have to know which unit each source
used, which is the knowledge a schema exists to remove. A mapper converts
once, at the edge of the system.

Floating point is used for a value with no native fixed-point resolution
and nowhere else: geographic coordinates on `Location` and the operator's
decimal attribute value. A float field anywhere else is a review finding.

### 2. Counters and statistics

A counter is `uint64`, named for the unit (`in_bytes`, `in_frames`), and
lives in a `<Domain>Counters` message. Every counters message carries
`google.protobuf.Timestamp last_discontinuity`: the time the counters last
reset or restarted counting. A decrease between two readings is a
necessary reset signal and not a sufficient one (RFC 2863
`ifCounterDiscontinuityTime`, OpenConfig `last-clear`). This is a fact the
device reports about its counters, not an observation time, so it is
admissible on a primitive.

A windowed statistic is named for the statistic and the unit
(`utilization_avg_basis_points`) beside a `Duration` window field. No
shared statistics message exists until two packages need the same shape.

### 3. Keys and cross-references

A primitive names a peer by its device-local key, validated by a
predefined rule from `net/key`. The rule lives in a leaf because
`switching`, `ip`, and every protocol package key rows by interface name
and cannot import `net/interface`. `vlan_id` stays in `net/switching`,
which owns VLANs.

An interface name has two rules, because it has two uses:

- `interface_name` accepts what a device reports: 1 to 255 characters,
  the size of SNMPv2-TC `DisplayString`, which `ifName` uses. Every
  observed row uses it. A stricter rule here would reject a row a device
  really sent, and one bad name would cost the whole table walk (the
  decode-failure solution in `docs/solutions/`).
- `shell_safe_interface_name` adds the character class
  `^[A-Za-z0-9][A-Za-z0-9 ./:_-]*$`. Every field whose value FlowSeer sends
  back to a device (an operation target, a capture source) uses it, because
  the adapter interpolates it into a shell command line.

Today the same split is an exemption list in a conformance test; the two
rules move the decision into the schema, where the field declares which
use it has. `network_instance_name` follows the first rule's bounds.

A string bound states where it comes from: the size a source standard
gives (`DisplayString` 255, SSID 32 octets, DNS name 253), or 1024 for free
text with no standard size. Under `net/`, every bounded string today is
an interface name capped at 64, which the key rules replace. The `model/`,
`api/`, and `edge/` roots carry bounds of 32 to 4096 (credential material,
URLs, PEM blocks) whose sources this record has not audited; that audit is
still open.

### 4. The network instance is part of the key

A forwarding table's key is ambiguous without the instance it belongs to:
Q-BRIDGE-MIB keys the FDB by filtering database, Huawei carries a VSI
name, and OpenConfig hangs every table under `network-instance`
(dossier 05). FlowSeer shapes the key now, while breaking it costs one
regeneration.

- `net/instance/v1.NetworkInstance{name, kind}` is a device table row.
  `NetworkInstanceKind` is normalized from `openconfig-network-instance-types`
  (default, L3 VRF, L2 VSI, L2 point-to-point, L2L3).
- `Vlan`, `FdbEntry`, and `Route` require `network_instance`. `IpFacet`
  requires it when present, so a routed interface names its VRF and every
  row keyed by that interface inherits it. `NeighborEntry` and
  `InterfaceAddress` do not repeat it.
- A device with no instance concept has one instance. The mapper names it
  as the device does, or `default` when the device has no name for it, and
  reports a `NetworkInstance` row of kind default with that name. Absence
  never stands in for the default instance, because absence means "the
  source did not report it" everywhere else.
- `FdbEntry` keeps `vlan_id` and states that its key assumes independent
  VLAN learning. The bridge component and filtering-database id stay out:
  the atlas found them on a minority of agents.

### 5. Facets, settings, and table rows

A facet is per-interface, or per-component for a radio, and named `<Name>Facet`; the requested values for
the same layer are `<Name>Settings`, carried by the facet. A declared
`Settings` that no facet carries is a review finding (`EthernetSettings`
today).

A table row is device-scoped, carries its key fields, and is named for the
thing it describes (`Vlan`, `Route`, `BgpPeer`, `NtpAssociation`). A row is
named `<Table>Entry` only when the table name is the natural noun and the
row has none of its own (`FdbEntry`, `NeighborEntry`). Tables are repeated
rows, never maps (structure record convention 6).

### 6. Protocols own their tables, and no message unions them

A protocol's own table lives in `net/protocol/<x>` and nowhere else. A
view that is blind to the protocol ("what is on this port", "which gateway
group owns this address") is a projection a service computes over several
protocol tables. The schema does not hold a union message such as a
generic discovery neighbor with a protocol discriminator: its fields would
be the union of the protocols' fields, most of them absent for any one
row, and the landed LLDP `Neighbor` would lose the TLV fidelity it has.
CDP gets `protocol/cdp`; VRRP gets `protocol/vrrp`; a later HSRP gets its
own.

A table that exists whichever protocol fills it is functional and lives in
a function-named package (structure record, "Protocols own their
packages"). Port-access sessions are filled by 802.1X, MAC authentication,
and web authentication alike, so they live in `net/portaccess`, not
`protocol/dot1x`. IGMP and MLD snooping populate one group-membership table,
so it lives in `net/multicast`.

MSTP lives in `protocol/stp`. IEEE 802.1Q defines STP, RSTP, and MSTP as
one family in which MSTP extends RSTP, and the landed `BridgeId`,
`PortRole`, and `ForwardingState` are MSTP's too. A separate package would
have to import `protocol/stp`, which the protocol import rule forbids, or
copy those three.

### 7. Enums

The two classes of the conventions doc stand. Three registries this record
brings in own a real value at zero and are pass-through, like
`IP_PROTOCOL_HOPOPT`: BFD session state (`ADMIN_DOWN = 0`, RFC 5880 §4.1),
syslog severity (`EMERGENCY = 0`, RFC 5424 §6.2.1), and syslog facility
(`KERN = 0`). `IANAipRouteProtocol` starts at 1 with `other(1)`, and a route
source enum keeps those integers. Registry enums that skip a value keep the
gap (OSPF interface type has no 4).

A vendor string with a closed value set (Ruckus radio mode, rogue
classification, BGP peer state) becomes a FlowSeer enum; the mapper
classifies and the schema never carries the string.

### 8. Octet strings are bytes

A value the standard defines as octets is `bytes`, with a length rule. An
SSID is an octet string of 0 to 32 octets with no character encoding
(`dot11DesiredSSID`, `spec/mib/ieee/IEEE802dot11-MIB:290`), so `Bss.ssid`
is `bytes` although every provider API types it as a string.

## Entities

- **Endpoint** (`model/endpoint/v1`) is any host seen through the network:
  a wireless client, a wired client, or an address discovered in an ARP or
  FDB walk. It is top-level and UUID-keyed like Device, because an owner
  would be the AP or switch it is attached to right now and roaming would
  delete it. The MAC is correlation data, never the key: randomized MACs
  rotate per network and per day (IEEE 802c local quadrants, dossier 03),
  and Meraki and UniFi both key clients by an id beside the MAC. The family
  is `EndpointState` and `EndpointEvent`. There is no `EndpointConfig`,
  because nobody configures a host; an operator's label is a Tag or an
  Attribute Assignment, which already exist. The current attachment is a
  required oneof of wired and wireless. Provenance stays on the envelope:
  when DHCP and mDNS disagree about a hostname, the service decides before
  writing State, and the event stream keeps both sightings.
- **Wlan** (`model/wireless/v1`) is the configured wireless network an
  operator creates once and many APs broadcast (Meraki SSID, SmartZone
  WLAN, UniFi WLAN). It is top-level, with Config, State, and Event,
  because intent and observation diverge: an SSID can be configured and
  broadcast by no AP.
- **Radio** is a `Component` of kind radio whose `ComponentState` carries a
  `net/wlan` radio facet, the way a transceiver component carries its
  pluggable module. An AP is a Device. Neither needs a new entity kind.
- **Alarm** (`model/alarm/v1`) is owned by a device and keyed by resource
  and alarm type, following RFC 8632 and ALARM-MIB (RFC 3877). It has
  State and Event and no Config: the device raises and clears it.
- **SyslogRecord** (`event/log/v1`) is a durable stream record, not an
  entity transition. A log line has no state to diff.

None of the four joins `EntityType` until its store answers the existence
check, as with `Location` and `Cable` today (conventions doc, "EntityRef").

## Amended decisions

- The structure record's open question "Host/Client entity family" is
  answered by Endpoint above. `net/wlan.WirelessClient` does not exist; the
  per-association facts live in `net/endpoint`'s wireless attachment.
- The structure record's "Radios are not interfaces" stands, and radios are
  components.
- The structure record's `net/wlan` "imports `addr` and `switching`" widens
  to `addr`, `key`, `measure`, and `switching`.
- The layering table gains every package in the tree above, with the
  imports `test/conformance/proto/layering_test.go` enforces.
- `docs/conventions/protobuf.md` names `MacAddress` where the code named
  `EuiAddress`; the code changes to match the document, because every
  sibling record and dossier already says `MacAddress`.
- `net/key` gains a third predefined rule, `protocol_instance_name`, for the
  per-protocol instance keys (OSPF process ids, IS-IS tags) that phase 7
  introduced; it validates the same 1..255 device-local shape as
  `network_instance_name`.

## Alternatives rejected

- **Native units per source.** Rejected under rule 1.
- **An optional network-instance field.** Leaves the independent-learning
  and single-VRF assumption implicit in every existing row, and adding a
  required key later is a break under every `buf breaking` category.
- **Per-protocol neighbor tables plus a union message.** Rejected under
  rule 6.
- **Access Point and Radio entities.** An AP's identity, firmware, uplink,
  and binding are Device's; a radio's hardware identity is Component's.
  Two more entity kinds would duplicate both.
- **Per-attribute provenance on Endpoint** (Cisco 9800 `dc-client-info`
  tags each field with its source). It would make Endpoint the one State
  that carries provenance, against the structure record's convention 9.

## Consequences

- Every phase breaks landed shapes: `MacAddress`, the phy diagnostics,
  PoE power units, the address origin, counter names, and the table keys.
  Nothing external consumes the schema, and `src/common/netsim` and
  `src/modules/localnet/snmpmap` are updated in the same change.
- A mapper does every unit conversion. The conformance tests cannot check
  that a mapper converted correctly; each phase that adds a mapper field
  adds a fixture test with known source bytes.
- Rule 6 means the operator question "what is on this port" needs a
  service projection before it has an answer. That projection is service
  work and outside this record.

## Sources

- The dossiers in [`docs/research/schema-building-blocks/`](../research/schema-building-blocks/README.md),
  each with its fetched URLs and `spec/` paths.
- The [network domain atlas](../research/network-domain-atlas/INDEX.md),
  especially its gaps and recommendations.
- SNMPv2-TC `DisplayString` (`spec/mib/ietf/SNMPv2-TC`, SIZE 0..255) and
  IF-MIB `ifName` (`spec/mib/ietf/IF-MIB`).
- `openconfig-network-instance-types.yang` for instance kinds; RFC 8529
  for the IETF network instance.
- RFC 5880 §4.1, RFC 5424 §6.2.1, `IANA-RTPROTO-MIB`
  (`spec/mib/ietf/IANA-RTPROTO-MIB`) for the pass-through registries.
- Google AIP-142 (time and duration) and AIP-143 (standardized codes and
  units in names).
