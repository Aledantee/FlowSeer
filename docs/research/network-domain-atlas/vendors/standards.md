---
title: Vendor dossier — standards bodies
date: 2026-08-30
scope: IETF, IEEE 802.1/802.3, IANA registries, OpenConfig
---

# Standards bodies

## IETF (186 MIB modules, 4 YANG modules)

### What is in the corpus

The full classical set: `SNMPv2-*`, `RFC1213-MIB`, `IF-MIB`, `IP-MIB`,
`IP-FORWARD-MIB`, `IPV6-MIB`, `TCP-MIB`, `UDP-MIB`, `BRIDGE-MIB`,
`P-BRIDGE-MIB`, `Q-BRIDGE-MIB`, `RSTP-MIB`, `MSTP-MIB`, `ENTITY-MIB` +
`-SENSOR` + `-STATE`, `EtherLike-MIB`, `MAU-MIB`, `POWER-ETHERNET-MIB`,
`RMON-MIB` / `RMON2-MIB` / `HC-RMON-MIB` / `SMON-MIB`, `HOST-RESOURCES-MIB`,
`DISMAN-{PING,TRACEROUTE,EVENT,SCHEDULE,SCRIPT,NSLOOKUP}-MIB`, `ALARM-MIB`,
`NOTIFICATION-LOG-MIB`, `SFLOW-MIB`, `TUNNEL-MIB`, `VRRP-MIB` / `VRRPV3-MIB`,
`BGP4-MIB`, `OSPF-MIB` / `OSPFV3-MIB`, `ISIS-MIB`, `RIPv2-MIB`, `PIM-MIB`,
`IPMROUTE-STD-MIB`, `MGMD-STD-MIB`, `BFD-STD-MIB`, the MPLS and PW families,
`RADIUS-*-CLIENT-MIB`, `TACACS-CLIENT-MIB`, `CAPWAP-BASE-MIB`, the DSL/ATM/
SONET/DOCSIS access sets, and the IANA registries
(`IANAifType-MIB`, `IANA-MAU-MIB`, `IANA-RTPROTO-MIB`,
`IANA-ADDRESS-FAMILY-NUMBERS-MIB`, `IANA-ENTITY-MIB`, `IANA-ITU-ALARM-TC-MIB`,
`IANA-GMPLS-TC-MIB`, `IANA-PWE3-MIB`, `IANA-PRINTER-MIB`, `IANA-CHARSET-MIB`,
`IANA-LANGUAGE-MIB`).

Plus the net-snmp family (`NET-SNMP-*`, `UCD-*`) which matters because three
vendors in this corpus run net-snmp underneath.

Four IETF YANG modules only: `ietf-interfaces`, `iana-if-type`,
`ietf-inet-types`, `ietf-yang-types`. **The IETF YANG catalogue is otherwise
absent** — no `ietf-ip` (RFC 8344), `ietf-system` (RFC 7317), `ietf-routing`
(RFC 8349), `ietf-network` / `ietf-network-topology` (RFC 8345),
`ietf-l2-topology` (RFC 8944), `ietf-access-control-list` (RFC 8519),
`ietf-hardware` (RFC 8348), `ietf-alarms` (RFC 8632).

### What matters most

**The half-dozen MIBs everything depends on**, in rough order of how often other
entities join to them:

1. `IF-MIB` — the interface table and the counters.
2. `ENTITY-MIB` — the component tree and `entAliasMappingTable`, the only
   standard `entPhysicalIndex` ↔ `ifIndex` bridge.
3. `BRIDGE-MIB` — `dot1dBasePortIfIndex`, without which every PortList bitmap
   and every STP/FDB row is unattributable.
4. `Q-BRIDGE-MIB` — VLANs, membership, and the VLAN↔FID map.
5. `IP-MIB` — addressing and the neighbour cache, in two generations.
6. `SNMPv2-MIB` — `sysObjectID` for driver dispatch, `sysORTable` for
   capability discovery.

**The under-used standards worth adopting anyway**, because where they are
implemented they solve a problem nothing else does:

- `NOTIFICATION-LOG-MIB` — replay traps you missed.
- `ALARM-MIB` — the catalogue/active split for stateful alarms.
- `ENTITY-SENSOR-MIB` — typed, scaled, self-describing sensors.
- `DISMAN-PING-MIB` — the canonical definition/results/history job shape.
- `RMON` history group — free on-device time series.
- `PTOPO-MIB` — a protocol-agnostic neighbour table with a
  `ptopoConnDiscAlgorithm` column naming the protocol that found it.

**The standards that lost.** `SMON-MIB::portCopyTable` (port mirroring),
`DIFFSERV-MIB` (the functional-block QoS chain), `DISMAN-SCHEDULE-MIB`,
`DNS-RESOLVER-MIB` / `DNS-SERVER-MIB`, `NAT-MIB`, `CAPWAP-BASE-MIB`. Each was
replaced by a per-vendor model. Knowing they exist prevents you from designing
the same thing a third time — and in two cases (`DIFFSERV-MIB`'s block chain,
`CAPWAP-BASE-MIB`'s three-level key) the standard's *design* is better than the
vendors' and worth borrowing even though the wire format is dead.

### The deprecation ladder

Four core entities have two or three live generations at once; see
[cross-cutting pattern 2](../00-methodology-and-lineages.md#2-deprecated-and-current-table-pairs).
A decoder must read both and prefer the newer, because agents implement them
inconsistently and sometimes populate the deprecated one only.

---

## IEEE 802 (212 MIB modules)

### What is in the corpus

This is the most complete IEEE 802.1 MIB collection you are likely to see,
**with all dated revisions kept** — which is the point: `IEEE8021-BRIDGE-MIB`
alone has 9 revisions from 2008 to 2022, and vendors implement different ones.

Coverage by area:

| Area | Modules |
|---|---|
| Bridging | `IEEE8021-BRIDGE-MIB` (9 revs), `IEEE8021-Q-BRIDGE-MIB` (8), `IEEE8021-PB-MIB` (8, provider bridging), `IEEE8021-PBB-MIB` (6), `IEEE8021-PBBTE-MIB`, `IEEE8021-PBBN-AA-MIB`, `IEEE8021-TPMR-MIB`, `IEEE8021-TC-MIB` (10) |
| Spanning tree | `IEEE8021-SPANNING-TREE-MIB` (6), `IEEE8021-MSTP-MIB` (8) |
| Registration | `IEEE8021-MVRPX-MIB` (4), `IEEE8021-MIRP-MIB` (4) |
| LLDP | `LLDP-MIB`, `LLDP-V2-MIB` (4), `LLDP-V2-TC-MIB`, `LLDP-EXT-DOT1-MIB` / `-V2-MIB` (4), `LLDP-EXT-DOT3-MIB` / `-V2-MIB`, `LLDP-EXT-MED-MIB`, `LLDP-EXT-DCBX-MIB`, EVB and PE extensions, `LRP-MIB` / `LLDP-V2-LRP-EXT-MIB` |
| Aggregation | `IEEE8023-LAG-MIB` (4 revs; includes the DRNI tables) |
| Security | `IEEE8021X-PAE-MIB` (4), `IEEE8021-PAE-MIB`, `IEEE8021-SECY-MIB` (6, MACsec), `IEEE8021-DEVID-MIB` (802.1AR secure device identity) |
| OAM | `IEEE8021-CFM-MIB` (7), `-CFM-V2-MIB` (6), `-CFMD8-MIB`, `-DDCFM-MIB` (4) |
| DCB | `IEEE8021-PFC-MIB` (4), `IEEE8021-FQTSS-MIB` (5), `IEEE8021-CN-MIB` (5), `IEEE8021-ECMP-MIB` |
| TSN | `IEEE8021-ST-MIB` (5, 802.1Qbv), `IEEE8021-PSFP-MIB` (5, 802.1Qci), `IEEE8021-Preemption-MIB` (3, 802.1Qbu), `IEEE8021-FRER-MIB` (802.1CB), `IEEE8021-STREAM-IDENTIFICATION-MIB`, `IEEE8021-SRP-MIB` (6), `IEEE8021-AS-MIB` / `-V2` / `-V3` (802.1AS gPTP), `IEEE8021-TSN-REMOTE-MANAGEMENT-MIB` |
| Fabric | `IEEE8021-SPB-MIB` (5), `IEEE8021-TEIPS-MIB` / `-V2` |
| Wireless / other | `IEEE802dot11-MIB`, `IEEE-802DOT17-RPR-MIB`, `IEEE802171-CFM-MIB`, `IEEE8021-EVB-MIB`, `IEEE8021-PE-MIB`, `IEEE8021-PRY-MIB` |

### The one structural thing to know

**Modern IEEE 802.1 MIBs put a bridge-component id first in every table key.**
Look at any table in `IEEE8021-Q-BRIDGE-MIB`, `IEEE8021-MSTP-MIB`,
`IEEE8021-PB-MIB`, `IEEE8021-PSFP-MIB`, or `IEEE8021-FQTSS-MIB` and the
component is the first index.

The index object is named per table rather than shared, which makes it easy to
miss: `ieee8021QBridgeFdbComponentId`, `ieee8021QBridgeVlanCurrentComponentId`,
`ieee8021QBridgeCVlanPortComponentId`, `ieee8021MstpComponentId`,
`ieee8021MstpVlanComponentId`, `ieee8021PbCepCComponentId`,
`ieee8021BridgeBasePortComponentId`. `IEEE8021-BRIDGE-MIB`,
`IEEE8021-FQTSS-MIB` and `IEEE8021-PSFP-MIB` use the shared
`ieee8021BridgeBaseComponentId` directly.

That component is the bridge domain the IETF `BRIDGE-MIB`/`Q-BRIDGE-MIB` pair
lacks, and it resolves the problem FlowSeer's net-core research named as its
largest unresolved dependency.

Vendor implementation of the component-aware MIBs is thin (LANCOM SX ships
`IEEE8021-SECY-MIB` and `IEEE8021-PFC-MIB`; most others stay on the IETF pair),
so FlowSeer will read component-less data from most devices. That is an argument
for making the dimension explicit and defaulting it, not for omitting it.

### IEEE YANG

The 802.1 working group publishes 102 YANG modules
(`ieee802-dot1q-bridge`, `-vlan-bridge`, `-mstp`, `-rstp`, `-types`,
`ieee802-dot1ab-lldp`, `ieee802-dot1ax-linkagg` / `-drni`,
`ieee802-dot1x` / `-eapol`, `ieee802-dot1ae-secy` / `-pry`,
`ieee802-dot1as-gptp`, the full 802.1CB / Qbv / Qci / Qbu TSN set, and the
IEC/IEEE 60802 industrial profile). **None are vendored in this repo.** For
bridge-domain and VLAN-type definitions they are the authoritative source and
worth fetching — `ieee802-dot1q-types.yang` in particular, which the net-core
research already cites.

---

## OpenConfig

### What is in the corpus

Vendored directly in `spec/yang/openconfig/` (90 files, the transitive import
closure of the Aruba bundle), and again inside the Ruckus ICX 9.0.00 and Cisco
IOS-XE 26.1.1 trees. The union is 144 modules covering: interfaces (+ ethernet,
aggregate, ip, poe, types, ext), vlan, lacp, lldp, spanning-tree, macsec, acl,
packet-match, qos, aft, network-instance, local-routing, bgp (+ rib), isis,
ospfv2, pim, igmp, mpls (+ ldp/rsvp/sr/te), segment-routing, srte-policy, bfd,
evpn, ethernet-segments, routing-policy, defined-sets, platform (+ cpu/fan/
linecard/port/psu/transceiver), system (+ logging/terminal/grpc/management),
aaa (+ radius/tacacs), keychain, alarms, messages, procmon, probes, openflow,
license, and the wireless set (access-points, ap-manager, wifi-mac, wifi-phy,
wifi-types).

**Not present** (exists upstream, absent here): `openconfig-nat`,
`openconfig-relay-agent`, `openconfig-telemetry`, `openconfig-sampling`,
`openconfig-oam`, `openconfig-ptp`, `openconfig-multicast`,
`openconfig-gribi`, `openconfig-p4rt`, `openconfig-hashing`,
`openconfig-security`, and the optical-transport family.

### Why it is the right reference model

Three design decisions that the MIB world never made and that FlowSeer's own
conventions already echo:

1. **Config/state split.** Every subtree has `config` (intent) and `state`
   (observed, a superset). FlowSeer's Config/State/Event triad is the same idea
   with an added event axis.
2. **Name keys, not index keys.** `interface[name]`, `component[name]`,
   `network-instance[name]`. The stability problems of `ifIndex` and
   `entPhysicalIndex` simply do not arise.
3. **Network instance as the container for everything L2 and L3.** VLANs, FDB,
   protocols, and AFTs all hang under a named instance, so the bridge-domain and
   VRF dimensions are structural rather than bolted on.

### The deviation files are the real documentation

`icx-openconfig-vlan-dev.yang`, `cisco-xe-openconfig-interfaces-deviation.yang`,
`hpe-anw-cx-openconfig-deviations.yang` and their siblings state exactly which
leaves each platform supports. They are the YANG equivalent of `sysORTable` and
the only reliable answer to "will this path work on this box". Read them before
writing a collector, not after.

---

## IANA registries

The registries reachable from this corpus, and the entities that need them:

| Registry | MIB form | Used by |
|---|---|---|
| ifType | `IANAifType-MIB` (~300 values) | [interface](../entities/02-interface.md#interface) — the ifType→variant projection |
| MAU type | `IANA-MAU-MIB` (OID-valued) | [ethernet-phy](../entities/02-interface.md#ethernet-phy) |
| Routing protocol | `IANA-RTPROTO-MIB` | [rib-fib](../entities/04-ip.md#rib-fib) |
| Address family | `IANA-ADDRESS-FAMILY-NUMBERS-MIB` | everywhere |
| Entity physical class | `IANA-ENTITY-MIB` | [hw-component](../entities/01-platform.md#hw-component) |
| ITU alarm cause | `IANA-ITU-ALARM-TC-MIB` | [syslog-events](../entities/09-ops.md#syslog-events) |
| EtherType, IP protocol, DSCP, ports, ICMP types | not MIBs — the IANA web registries | `flowseer.net.packet.v1`, already cited in the net-core research |

FlowSeer's net-core rule "keep registry enums open, preserve assigned numbers,
validate the full numeric width" is exactly right for every row of this table.
`ifType` and `ifMauType` are the two that will bite hardest, because both are
large and both grow.
