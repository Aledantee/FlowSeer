---
title: Network domain atlas — index
date: 2026-08-30
scope: 101 entities across 10 domains, 20 vendor families, 8 prior-art systems
status: research; no schema changes made
---

# Network domain atlas

A mapped survey of the network-management domain as it is actually specified:
every entity worth modelling, what the standards say about it, how each vendor
in `spec/` diverges, and what comparable systems decided.

Built from the machine-readable corpus already in this repo — **1,691 SNMP MIB
modules, 1,295 YANG modules, 19 OpenAPI documents** — plus targeted reading of
the standards and prior-art systems those descend from. Every claim traces back
to an extracted table, node path, or cited document.

**Nothing here changes a schema.** It is input to decisions, not a decision.

---

## Start here

| If you want to… | Read |
|---|---|
| understand how this was built and what the corpus contains | [00 — Methodology, census, lineages](00-methodology-and-lineages.md) |
| know what comparable systems already solved | [01 — Prior art](01-prior-art.md) |
| find what FlowSeer is missing and what to do next | [04 — Gaps and recommendations](04-gaps-and-recommendations.md) |
| look up one entity | the [entity index](#entity-index) below |
| work on one vendor | the [vendor dossiers](vendors/README.md) |
| see who supports what, at a glance | the [coverage matrix](matrices/README.md) |

---

## The three findings that matter most

**1. Twelve vendor directories collapse into eight decoder families.** Ubiquiti
EdgeSwitch, Netgear, and LANCOM SX all run Broadcom FASTPATH — 206 shared table
names, and both Netgear's and Ubiquiti's root MIBs literally declare a node
named `broadcom` under enterprise 4413. Ruckus ICX and half of HP ProCurve are
both Foundry — 174 shared table names. One FASTPATH decoder and one Foundry
decoder cover five vendor names.
→ [00 — Lineages](00-methodology-and-lineages.md#the-lineages)

**2. The bridge-domain dimension is not optional, and the corpus says so
loudly.** Modern IEEE 802.1 MIBs put a bridge-component id first in *every*
table key. `dot1qTpFdbTable` is keyed on the filtering
database ID, not the VLAN ID. Huawei carries `vsiName` in FDB keys and `vpnName`
in route keys; LANCOM carries `rtgTag`; NetBox scopes VLAN uniqueness to a
group; OpenConfig hangs everything under a network instance. FlowSeer's
`FdbEntry` keyed on `(VlanId, Eui48)` bakes in an assumption that should be
stated rather than implied.
→ [03 — The bridge-domain problem](entities/03-switching.md#the-bridge-domain-problem-once-up-front)

**3. Seven different port identifiers, and every cross-entity join crosses at
least one boundary.** `ifIndex`, `dot1dBasePort`, `entPhysicalIndex`,
`pethPsePortGroupIndex`+`Index`, `lldpLocPortNum`, `dot1xPaePortNumber`, and
per-vendor unit/slot/port. The standard mapping tables are optional in practice,
and a decoder that assumes identity where a mapping is missing produces
confidently wrong data instead of failing.
→ [00 — The port-namespace problem](00-methodology-and-lineages.md#1-the-port-namespace-problem)

---

## Contents

### Cross-cutting

- [00 — Methodology, corpus census, and vendor lineages](00-methodology-and-lineages.md)
  — how the taxonomy was derived, what the corpus holds, the eight decoder
  families, and six patterns that recur across every domain.
- [01 — Prior art](01-prior-art.md) — SNMP::Info, Netdisco, LibreNMS, NAPALM,
  SuzieQ, NetBox/Nautobot, OpenConfig, IETF topology models: what to copy and
  what to avoid.
- [04 — Gaps and recommendations](04-gaps-and-recommendations.md) — FlowSeer
  coverage (18 of 101 entities), corpus gaps, seven design questions to settle
  before the current packages stabilise, and eight quick wins.

### Entity records

| Part | Domain | Entities |
|---|---|---|
| [01](entities/01-platform.md) | L0 — platform and hardware | 10 |
| [02](entities/02-interface.md) | L1 — interfaces and PHY | 16 |
| [03](entities/03-switching.md) | L2 — switching | 21 |
| [04](entities/04-ip.md) | L3 — IP | 13 |
| [05](entities/05-routing.md) | L3 — routing protocols | 8 |
| [06](entities/06-qos.md) | QoS and ACLs | 3 |
| [07](entities/07-security.md) | Security and AAA | 5 |
| [08](entities/08-wireless.md) | Wireless | 11 |
| [09](entities/09-ops.md) | Telemetry and operations | 9 |
| [10](entities/10-wan-access.md) | WAN and access technologies | 7 |

Each record carries: what the entity is, the canonical model(s) with exact table
names and keys, a per-vendor mapping table, the traps and pitfalls, and where
relevant what FlowSeer already has.

### Vendor dossiers

[Index and at-a-glance table](vendors/README.md) ·
[standards](vendors/standards.md) · [cisco](vendors/cisco.md) ·
[aruba](vendors/aruba.md) · [hpe](vendors/hpe.md) · [huawei](vendors/huawei.md) ·
[dlink](vendors/dlink.md) · [ruckus](vendors/ruckus.md) ·
[ubiquiti](vendors/ubiquiti.md) · [netgear](vendors/netgear.md) ·
[lancom](vendors/lancom.md) · [mikrotik](vendors/mikrotik.md)

### Matrices

[Entity × source-family coverage, evidence volume, family breadth](matrices/README.md)
— generated from the corpus, regenerable.

### Working files

[`_raw/`](_raw/) — the extraction scripts, the taxonomy, the per-domain
evidence dumps they produced, and the raw prior-art notes. Keep them: they are
how any claim in the atlas is checked or the whole thing regenerated after
`spec/` changes.

---

## Entity index

101 entities, alphabetical.

| entity | name | domain | record |
|---|---|---|---|
| `aaa` | AAA / RADIUS / TACACS | Security | [→](entities/07-security.md#aaa) |
| `access-point` | Access point | Wireless | [→](entities/08-wireless.md#access-point) |
| `acl` | ACLs | QoS | [→](entities/06-qos.md#acl) |
| `app-visibility` | Application visibility | Wireless | [→](entities/08-wireless.md#app-visibility) |
| `arp-nd` | ARP / IPv6 ND cache | L3 IP | [→](entities/04-ip.md#arp-nd) |
| `bfd` | BFD | L3 Routing | [→](entities/05-routing.md#bfd) |
| `bgp` | BGP | L3 Routing | [→](entities/05-routing.md#bgp) |
| `bras` | BRAS / subscriber mgmt | WAN | [→](entities/10-wan-access.md#bras) |
| `bss-vap` | BSS / VAP / SSID profile | Wireless | [→](entities/08-wireless.md#bss-vap) |
| `cable-diag` | Cable diagnostics / TDR | L1 Interface | [→](entities/02-interface.md#cable-diag) |
| `cdp-like` | CDP & other discovery | L2 Switching | [→](entities/03-switching.md#cdp-like) |
| `cellular` | Cellular / 3G/LTE | WAN | [→](entities/10-wan-access.md#cellular) |
| `config-file` | Config file & backup | L0 Platform | [→](entities/01-platform.md#config-file) |
| `cpu-memory` | CPU / memory / storage | L0 Platform | [→](entities/01-platform.md#cpu-memory) |
| `dhcp` | DHCP client/server/relay | L3 IP | [→](entities/04-ip.md#dhcp) |
| `dns` | DNS resolver / server | L3 IP | [→](entities/04-ip.md#dns) |
| `docsis` | Cable / DOCSIS | WAN | [→](entities/10-wan-access.md#docsis) |
| `dot1x` | 802.1X / port access | Security | [→](entities/07-security.md#dot1x) |
| `dsl` | DSL (ADSL/VDSL/SHDSL) | WAN | [→](entities/10-wan-access.md#dsl) |
| `eee` | Energy Efficient Ethernet | L1 Interface | [→](entities/02-interface.md#eee) |
| `eigrp` | EIGRP | L3 Routing | [→](entities/05-routing.md#eigrp) |
| `environment` | Environment sensors | L0 Platform | [→](entities/01-platform.md#environment) |
| `ethernet-phy` | Ethernet PHY / MAU | L1 Interface | [→](entities/02-interface.md#ethernet-phy) |
| `fabric-overlay` | Fabric / overlay (VXLAN,SPB) | L2 Switching | [→](entities/03-switching.md#fabric-overlay) |
| `fdb` | MAC forwarding database | L2 Switching | [→](entities/03-switching.md#fdb) |
| `fec` | FEC / link tuning | L1 Interface | [→](entities/02-interface.md#fec) |
| `fibre-channel` | Fibre Channel / FCoE | WAN | [→](entities/10-wan-access.md#fibre-channel) |
| `firewall` | Firewall / sessions / VPN | Security | [→](entities/07-security.md#firewall) |
| `firmware-image` | Firmware / image / boot | L0 Platform | [→](entities/01-platform.md#firmware-image) |
| `flow-export` | sFlow / NetFlow / IPFIX | Ops | [→](entities/09-ops.md#flow-export) |
| `garp` | GVRP / MVRP / MRP | L2 Switching | [→](entities/03-switching.md#garp) |
| `host-resources` | Host resources / apps | Ops | [→](entities/09-ops.md#host-resources) |
| `hw-component` | Hardware component tree | L0 Platform | [→](entities/01-platform.md#hw-component) |
| `icmp-tcp-udp` | ICMP / TCP / UDP stack | L3 IP | [→](entities/04-ip.md#icmp-tcp-udp) |
| `if-counters` | Interface counters | L1 Interface | [→](entities/02-interface.md#if-counters) |
| `igmp-snooping` | IGMP/MLD snooping | L2 Switching | [→](entities/03-switching.md#igmp-snooping) |
| `interface` | Interface (generic) | L1 Interface | [→](entities/02-interface.md#interface) |
| `ip-address` | IP interface addressing | L3 IP | [→](entities/04-ip.md#ip-address) |
| `ip-diagnostics` | Ping / traceroute / SLA | L3 IP | [→](entities/04-ip.md#ip-diagnostics) |
| `isis` | IS-IS | L3 Routing | [→](entities/05-routing.md#isis) |
| `jumbo-mtu` | Jumbo frames / MTU | L2 Switching | [→](entities/03-switching.md#jumbo-mtu) |
| `l2-security-guard` | DAI / IP source guard / RA guard | L3 IP | [→](entities/04-ip.md#l2-security-guard) |
| `l2pt` | L2 protocol tunneling | L2 Switching | [→](entities/03-switching.md#l2pt) |
| `lacp` | LACP protocol state | L1 Interface | [→](entities/02-interface.md#lacp) |
| `lag` | Link aggregation group | L1 Interface | [→](entities/02-interface.md#lag) |
| `license` | Licensing | L0 Platform | [→](entities/01-platform.md#license) |
| `link-oam` | Ethernet link OAM (802.3ah) | L1 Interface | [→](entities/02-interface.md#link-oam) |
| `lldp` | LLDP | L2 Switching | [→](entities/03-switching.md#lldp) |
| `loop-protect` | Loop / BPDU protection | L2 Switching | [→](entities/03-switching.md#loop-protect) |
| `loopback-if` | Loopback interface | L1 Interface | [→](entities/02-interface.md#loopback-if) |
| `macsec` | MACsec (802.1AE) | L2 Switching | [→](entities/03-switching.md#macsec) |
| `mesh` | Wireless mesh | Wireless | [→](entities/08-wireless.md#mesh) |
| `mgmt-if` | Management interface | L1 Interface | [→](entities/02-interface.md#mgmt-if) |
| `mirroring` | Port mirroring / SPAN | L2 Switching | [→](entities/03-switching.md#mirroring) |
| `mpls` | MPLS / LDP / RSVP-TE / SR | L3 Routing | [→](entities/05-routing.md#mpls) |
| `multicast-routing` | PIM / DVMRP / MSDP / mroute | L3 Routing | [→](entities/05-routing.md#multicast-routing) |
| `mvr` | Multicast VLAN registration | L2 Switching | [→](entities/03-switching.md#mvr) |
| `nat` | NAT | L3 IP | [→](entities/04-ip.md#nat) |
| `netconf-telemetry` | NETCONF / gNMI / telemetry | Ops | [→](entities/09-ops.md#netconf-telemetry) |
| `openflow-sdn` | OpenFlow / SDN | Ops | [→](entities/09-ops.md#openflow-sdn) |
| `ospf` | OSPF v2/v3 | L3 Routing | [→](entities/05-routing.md#ospf) |
| `pki-crypto` | PKI / certificates / crypto | Security | [→](entities/07-security.md#pki-crypto) |
| `poe` | Power over Ethernet | L1 Interface | [→](entities/02-interface.md#poe) |
| `pon` | PON (EPON/GPON) | WAN | [→](entities/10-wan-access.md#pon) |
| `port-isolation` | Port isolation / PVLAN | L2 Switching | [→](entities/03-switching.md#port-isolation) |
| `port-security` | Port security / DoS guard | Security | [→](entities/07-security.md#port-security) |
| `power-supply` | Power supply & budget | L0 Platform | [→](entities/01-platform.md#power-supply) |
| `qinq` | QinQ / provider bridge | L2 Switching | [→](entities/03-switching.md#qinq) |
| `qos-policy` | QoS classification & policy | QoS | [→](entities/06-qos.md#qos-policy) |
| `qos-queue` | Queues / schedulers / WRED | QoS | [→](entities/06-qos.md#qos-queue) |
| `radio` | Radio (802.11 PHY) | Wireless | [→](entities/08-wireless.md#radio) |
| `rib-fib` | Routing table / FIB / AFT | L3 IP | [→](entities/04-ip.md#rib-fib) |
| `ring-protection` | Ring protection (ERPS/RRPP) | L2 Switching | [→](entities/03-switching.md#ring-protection) |
| `rip` | RIP | L3 Routing | [→](entities/05-routing.md#rip) |
| `rmon` | RMON / SMON / history | Ops | [→](entities/09-ops.md#rmon) |
| `roaming` | Roaming / mobility | Wireless | [→](entities/08-wireless.md#roaming) |
| `rogue-wids` | Rogue AP / WIDS / WIPS | Wireless | [→](entities/08-wireless.md#rogue-wids) |
| `routing-policy` | Route maps / policy routing | L3 IP | [→](entities/04-ip.md#routing-policy) |
| `scheduling-automation` | Task scheduling / EEM | Ops | [→](entities/09-ops.md#scheduling-automation) |
| `snmp-agent` | SNMP agent / v3 / traps | Ops | [→](entities/09-ops.md#snmp-agent) |
| `stacking` | Stacking / virtual chassis | L0 Platform | [→](entities/01-platform.md#stacking) |
| `static-route` | Static routes / local routing | L3 IP | [→](entities/04-ip.md#static-route) |
| `storm-control` | Storm control / rate limit | L2 Switching | [→](entities/03-switching.md#storm-control) |
| `stp` | Spanning tree | L2 Switching | [→](entities/03-switching.md#stp) |
| `subinterface` | Subinterface | L1 Interface | [→](entities/02-interface.md#subinterface) |
| `syslog-events` | Syslog / event log / alarms | Ops | [→](entities/09-ops.md#syslog-events) |
| `system-identity` | System identity | L0 Platform | [→](entities/01-platform.md#system-identity) |
| `time-sync` | NTP / SNTP / PTP | Ops | [→](entities/09-ops.md#time-sync) |
| `transceiver` | Transceiver / optics DOM | L0 Platform | [→](entities/01-platform.md#transceiver) |
| `tunnel-if` | Tunnel interfaces | L1 Interface | [→](entities/02-interface.md#tunnel-if) |
| `udld` | UDLD / DLDP | L1 Interface | [→](entities/02-interface.md#udld) |
| `vlan` | VLAN database | L2 Switching | [→](entities/03-switching.md#vlan) |
| `vlan-membership` | Port VLAN membership / PVID | L2 Switching | [→](entities/03-switching.md#vlan-membership) |
| `voice-vlan` | Voice / auto VLAN | L2 Switching | [→](entities/03-switching.md#voice-vlan) |
| `vrf` | VRF / network instance | L3 IP | [→](entities/04-ip.md#vrf) |
| `vrrp` | VRRP / gateway redundancy | L3 IP | [→](entities/04-ip.md#vrrp) |
| `wan-serial` | Serial / TDM / ATM / FR | WAN | [→](entities/10-wan-access.md#wan-serial) |
| `wireless-client` | Wireless client / station | Wireless | [→](entities/08-wireless.md#wireless-client) |
| `wireless-guest` | Guest access / hotspot | Wireless | [→](entities/08-wireless.md#wireless-guest) |
| `wireless-rf` | RF management / neighbors | Wireless | [→](entities/08-wireless.md#wireless-rf) |
| `wlan-controller` | Wireless controller / mgmt | Wireless | [→](entities/08-wireless.md#wlan-controller) |

---

## Regenerating

From the repository root, in order:

```
python3 docs/research/network-domain-atlas/_raw/mine_mib.py        <out>/mib_index.json
python3 docs/research/network-domain-atlas/_raw/mine_tables.py     <out>/mib_tables.json
python3 docs/research/network-domain-atlas/_raw/mine_yang.py       <out>/yang_index.json
python3 docs/research/network-domain-atlas/_raw/mine_yang_nodes.py <out>/yang_nodes.json
python3 docs/research/network-domain-atlas/_raw/classify.py        # writes entity_evidence.json beside itself
python3 docs/research/network-domain-atlas/_raw/matrix.py          <tmp>/_matrix_body.md
python3 docs/research/network-domain-atlas/_raw/matrix2.py         <tmp>/_matrix_body.md matrices/README.md
python3 docs/research/network-domain-atlas/_raw/index_gen.py       <tmp>/entity_index.md
```

The scripts expect the four JSON files to sit beside them. `brief.py`,
`report.py`, `lineage.py` and `enterprise.py` are analysis helpers used to
produce the evidence dumps and the lineage findings.

Adjust `_raw/taxonomy.py` to add or split an entity, then re-run `classify.py`
onward.
