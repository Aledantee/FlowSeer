---
title: Matrices — entity coverage and evidence volume
date: 2026-08-30
---

# Matrices

Generated from the corpus by `../_raw/classify.py` and `../_raw/matrix.py`.
Regenerate after changing `spec/` or the taxonomy.

## Legend

Symbols count matched anchors (SNMP tables + YANG nodes) for that entity
in that source family. They measure *how much the family says about the
entity*, not quality:

| | matches |
|---|---|
| ● | 10 or more |
| ◐ | 3–9 |
| ○ | 1–2 |
| *(blank)* | none |

Read a blank as "this family does not model this entity in the vendored
specs" — not as "the product cannot do it". Several vendors expose
features only through a REST API with no vendored schema; see the
[vendor dossiers](../vendors/README.md).

Classifier precision caveats are in
[00-methodology](../00-methodology-and-lineages.md#precision-caveat).

## Entity × source-family coverage

| entity | IETF | IEEE | OC | IETFy | CiscoXE | CiscoSMB | ArubaCX | ArubaW | ProCurve | Comware | HPother | Huawei | D-Link | RuckICX | RuckW | EdgeMAX | UniFi | airMAX | Netgear | MikroTik | LCOS | LCOS-SX | LCOS-LX |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **L0 Platform** | | | | | | | | | | | | | | | | | | | | | | | |
| `system-identity` | ○ |  |  |  | ◐ | ○ | ○ |  |  | ○ |  | ● |  |  | ○ |  |  |  |  |  | ● |  |  |
| `hw-component` | ◐ | ○ | ◐ |  | ● | ○ | ◐ | ○ | ● | ● | ◐ | ● | ◐ | ◐ |  | ◐ |  |  |  |  |  | ● |  |
| `transceiver` |  | ● |  |  | ● |  | ◐ |  | ◐ | ◐ | ◐ | ● | ◐ |  |  |  |  | ○ |  |  | ● | ◐ |  |
| `stacking` | ◐ | ● |  |  | ● | ◐ | ◐ |  | ● | ● |  | ● | ◐ | ◐ |  | ○ |  | ◐ |  |  |  | ● |  |
| `power-supply` | ◐ |  | ◐ |  | ● | ○ | ○ | ○ | ◐ | ○ | ○ | ◐ |  | ◐ |  | ○ |  | ○ |  |  | ● | ◐ | ◐ |
| `environment` | ● |  |  |  | ● | ◐ | ◐ | ○ | ◐ | ◐ | ◐ | ● | ◐ | ◐ |  | ◐ |  | ○ | ○ |  | ● | ● | ◐ |
| `firmware-image` |  | ○ |  |  | ● | ◐ |  |  | ◐ | ● | ◐ | ● | ◐ |  |  |  |  |  |  |  | ● |  |  |
| `config-file` |  | ● |  |  | ● | ◐ |  |  | ○ | ◐ |  | ● | ◐ |  |  |  |  |  |  |  | ● | ◐ |  |
| `license` |  |  | ◐ |  | ● |  |  | ○ |  | ◐ |  | ◐ | ○ | ◐ |  |  |  |  |  |  | ● |  |  |
| `cpu-memory` | ◐ |  | ◐ |  | ● | ○ | ◐ | ◐ | ○ | ● | ◐ | ● | ○ | ◐ |  | ○ |  |  |  |  |  | ◐ |  |
| **L1 Interface** | | | | | | | | | | | | | | | | | | | | | | | |
| `interface` | ◐ |  |  |  | ● | ○ |  | ◐ | ○ | ● |  | ● |  |  |  |  |  |  |  |  |  | ◐ |  |
| `if-counters` | ◐ |  |  |  | ◐ | ◐ |  |  |  |  |  | ◐ | ◐ |  |  |  |  |  |  |  |  | ◐ |  |
| `ethernet-phy` | ● |  | ● |  | ● | ○ | ○ |  |  | ◐ |  | ● |  | ● |  | ◐ |  |  |  |  | ● | ● | ◐ |
| `eee` |  |  |  |  | ○ | ◐ |  |  |  |  |  | ◐ | ◐ |  |  |  |  |  |  |  |  | ○ |  |
| `fec` |  |  |  |  | ○ |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |
| `poe` | ◐ | ◐ |  |  | ● | ◐ | ◐ |  | ◐ | ● |  | ● | ◐ | ● |  | ◐ |  |  |  | ○ | ● | ● |  |
| `subinterface` |  |  |  |  | ● |  | ○ |  |  | ◐ |  | ● |  | ○ |  |  |  |  |  |  |  |  |  |
| `lag` | ○ | ● | ◐ |  | ● | ◐ | ◐ |  | ◐ | ◐ |  | ◐ | ◐ | ● |  | ○ |  |  | ○ |  | ● | ● |  |
| `lacp` |  | ● |  |  | ● | ○ |  |  |  |  | ● |  | ○ | ○ |  | ○ |  |  | ○ |  | ● | ● | ◐ |
| `tunnel-if` | ● |  | ◐ |  | ● | ◐ |  | ○ | ◐ | ● |  | ● | ◐ | ◐ |  | ○ |  |  |  |  | ● | ○ | ◐ |
| `loopback-if` | ○ |  |  |  | ● |  |  |  | ○ | ○ |  | ◐ |  | ○ |  | ○ |  |  |  |  | ● | ◐ |  |
| `mgmt-if` |  |  |  |  |  | ○ |  |  | ○ |  |  |  |  |  |  |  |  |  |  |  |  |  |  |
| `cable-diag` | ◐ |  |  |  | ● |  |  |  | ○ | ◐ |  | ● | ◐ |  |  |  |  |  |  |  |  | ○ |  |
| `link-oam` | ◐ |  |  |  | ● | ○ |  |  |  | ● |  | ● |  |  |  |  |  |  |  |  |  | ● |  |
| `udld` |  |  |  |  | ● | ○ |  |  | ○ | ◐ |  | ◐ | ○ |  |  | ○ |  |  |  |  |  | ◐ |  |
| **L2 Switching** | | | | | | | | | | | | | | | | | | | | | | | |
| `vlan` | ◐ | ● |  |  | ● | ● | ◐ | ○ | ○ | ● |  | ● | ● | ◐ |  | ● |  |  | ● |  | ● | ● | ○ |
| `vlan-membership` | ◐ | ● |  |  | ● | ◐ | ○ |  | ◐ | ◐ |  | ● | ◐ | ○ |  | ◐ |  |  | ◐ |  | ● | ● |  |
| `qinq` |  | ● | ○ |  | ◐ |  | ○ |  | ○ | ● |  | ● | ◐ |  |  |  |  |  |  |  |  | ◐ |  |
| `fdb` | ◐ | ● | ○ |  | ◐ | ○ | ○ |  | ◐ | ● | ● | ● | ● | ◐ |  | ◐ |  |  | ◐ |  | ● | ● |  |
| `stp` | ◐ | ● |  |  | ● | ● | ◐ |  | ○ | ◐ |  | ● | ○ | ● |  | ◐ |  |  | ◐ |  | ● | ◐ | ◐ |
| `loop-protect` |  |  |  |  | ◐ | ○ | ○ |  | ○ |  |  | ● | ○ |  |  |  |  |  |  |  |  | ◐ |  |
| `lldp` |  | ● |  |  | ● | ● | ○ | ○ |  | ◐ |  | ● | ● | ● |  |  |  |  |  |  | ● | ● |  |
| `cdp-like` | ○ |  | ○ |  | ● | ● | ◐ |  | ○ |  |  | ◐ | ◐ |  |  | ◐ |  |  |  |  | ● | ◐ | ◐ |
| `igmp-snooping` |  |  |  |  | ● | ◐ | ◐ |  |  | ● | ◐ | ● | ● |  |  | ● |  |  | ● |  | ● | ● | ○ |
| `mvr` |  | ● |  |  |  |  |  |  |  |  |  | ● | ◐ |  |  | ◐ |  |  |  |  |  | ◐ |  |
| `storm-control` |  |  |  |  | ● | ◐ |  |  | ● | ◐ |  | ● | ◐ | ◐ |  |  |  |  |  |  |  | ◐ |  |
| `mirroring` |  | ● |  |  |  | ◐ |  |  | ○ | ● |  | ● | ◐ |  |  | ◐ |  |  | ◐ |  |  | ◐ |  |
| `port-isolation` |  |  |  |  | ● | ○ |  |  | ○ | ◐ |  | ◐ | ◐ |  |  | ○ |  |  | ○ |  |  | ◐ |  |
| `garp` | ○ | ◐ |  |  | ● | ◐ | ◐ |  | ◐ |  |  |  | ◐ | ○ |  | ○ |  |  |  |  | ● | ● |  |
| `voice-vlan` |  | ◐ |  |  | ○ | ● |  |  | ○ | ◐ |  | ● | ● | ◐ |  | ◐ |  |  | ○ |  |  | ● |  |
| `ring-protection` |  | ● |  |  |  | ○ |  |  |  | ● |  | ● | ◐ |  |  |  |  |  |  |  |  | ◐ |  |
| `macsec` |  | ● |  |  | ● |  |  |  |  | ○ |  | ○ |  | ◐ |  |  |  |  |  |  |  | ● |  |
| `fabric-overlay` | ● | ● | ● |  | ● |  |  |  |  | ● |  | ● | ◐ | ○ |  |  |  |  |  |  |  |  |  |
| `l2pt` |  |  |  |  | ◐ |  |  |  |  |  |  |  | ◐ |  |  |  |  |  |  |  |  |  |  |
| `jumbo-mtu` |  | ◐ |  |  | ● |  |  |  | ○ |  |  |  |  |  |  |  |  |  |  |  |  | ◐ |  |
| **L3 IP** | | | | | | | | | | | | | | | | | | | | | | | |
| `ip-address` | ● |  |  |  | ● | ◐ |  |  | ● | ◐ |  | ● | ○ | ● |  | ◐ |  |  | ◐ |  | ● | ● | ◐ |
| `arp-nd` | ◐ |  | ○ |  | ● | ○ |  |  | ◐ | ○ |  | ● |  | ○ |  |  |  |  |  |  |  | ● |  |
| `rib-fib` | ◐ |  | ● |  | ● | ◐ |  |  | ◐ | ○ |  |  |  | ● |  |  |  |  |  |  | ● | ◐ |  |
| `static-route` |  |  | ◐ |  | ● | ◐ |  |  | ◐ | ○ | ◐ | ◐ | ◐ | ● |  | ○ |  |  |  |  | ● | ◐ |  |
| `vrf` | ● |  | ● |  | ● |  |  |  |  | ● | ○ | ◐ | ◐ | ● |  |  |  |  |  |  |  |  |  |
| `vrrp` | ◐ |  |  |  | ● | ◐ |  | ◐ | ● | ◐ | ● | ● | ○ | ◐ |  | ◐ |  |  |  |  | ● | ● |  |
| `nat` |  |  |  |  | ● |  |  | ○ |  | ● |  | ● |  |  | ○ |  |  |  |  |  |  |  |  |
| `routing-policy` | ○ |  | ● |  | ● | ◐ |  |  | ◐ | ◐ |  |  | ● | ● |  |  |  |  |  |  | ● | ◐ |  |
| `dhcp` |  |  |  |  | ● | ● |  |  | ○ | ● |  | ● | ● | ○ |  | ● |  |  | ● |  | ● | ● | ○ |
| `dns` | ◐ |  |  |  | ● | ● | ○ |  |  | ○ |  |  | ● | ○ |  | ◐ |  |  |  |  |  | ● |  |
| `ip-diagnostics` | ◐ |  |  |  | ● | ○ |  |  |  | ● |  | ● |  | ● |  |  |  |  |  |  | ● | ● |  |
| `l2-security-guard` |  |  |  |  | ● | ● |  |  | ○ | ● |  | ● | ● |  |  |  |  |  | ○ |  |  | ● |  |
| `icmp-tcp-udp` | ● |  | ○ |  | ● | ○ |  |  | ○ |  |  | ◐ |  |  |  |  |  |  |  |  | ● | ◐ |  |
| **L3 Routing** | | | | | | | | | | | | | | | | | | | | | | | |
| `rip` | ◐ |  |  |  | ● |  |  |  | ◐ | ○ |  | ○ | ● |  |  |  |  |  |  |  |  | ◐ |  |
| `ospf` | ● |  | ● |  | ● | ◐ |  |  | ● | ○ | ● | ● | ● | ● |  | ◐ |  |  |  |  | ● | ● |  |
| `isis` | ● |  | ● |  | ● |  |  |  |  | ◐ |  | ● | ◐ | ● |  |  |  |  |  |  |  |  |  |
| `bgp` | ◐ |  | ● |  | ● |  |  |  | ● | ● |  | ● | ● | ● |  |  |  |  |  |  |  | ◐ |  |
| `eigrp` |  |  |  |  | ● |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |
| `multicast-routing` | ● |  | ● |  | ● | ◐ | ◐ |  | ● |  |  | ● | ● | ● |  |  |  |  |  |  |  | ● |  |
| `bfd` | ◐ |  | ● |  | ● |  |  |  |  | ◐ |  | ◐ | ○ | ◐ |  |  |  |  |  |  |  |  |  |
| `mpls` | ● |  | ● |  | ● |  |  |  | ● | ● |  | ● | ● | ● |  |  |  |  |  |  |  | ○ |  |
| **QoS** | | | | | | | | | | | | | | | | | | | | | | | |
| `qos-policy` | ● |  |  |  | ● | ● |  |  | ○ | ● | ● | ● | ● | ○ |  | ● |  |  |  |  | ● | ● |  |
| `qos-queue` | ○ | ● | ○ |  | ● | ● |  |  | ○ | ● |  | ● | ◐ | ○ |  | ◐ |  |  |  | ○ | ◐ | ● |  |
| `acl` |  |  | ● |  | ● | ◐ |  |  | ◐ | ● | ● | ● | ● | ● |  | ● |  |  |  |  | ● | ● |  |
| **Security** | | | | | | | | | | | | | | | | | | | | | | | |
| `dot1x` |  | ● |  |  | ● | ● | ○ | ○ | ● | ● | ● | ● | ● | ◐ |  | ● |  |  |  |  |  | ● |  |
| `aaa` | ○ |  | ● |  | ● | ● | ○ | ● | ● | ● | ◐ | ● | ● | ● | ○ | ◐ |  |  | ○ |  | ● | ● | ● |
| `port-security` |  |  |  |  | ● | ● | ◐ |  |  | ◐ |  | ● | ◐ |  |  | ◐ |  |  |  |  |  | ◐ |  |
| `firewall` |  |  |  |  | ● |  |  |  |  | ● |  | ◐ | ○ |  |  | ○ |  |  |  |  | ● | ○ |  |
| `pki-crypto` |  |  | ● |  | ● | ● |  |  |  | ● |  | ● | ◐ | ○ |  | ○ |  |  |  |  | ● | ○ |  |
| **Wireless** | | | | | | | | | | | | | | | | | | | | | | | |
| `wlan-controller` | ◐ |  |  |  | ● | ◐ |  | ● |  | ○ |  | ● | ◐ |  | ● |  |  |  |  |  |  |  |  |
| `access-point` |  |  |  |  | ● |  |  | ● |  | ● |  | ● |  |  | ○ |  |  |  |  |  | ● |  |  |
| `radio` | ● | ◐ |  |  | ● |  |  | ◐ | ○ | ● |  | ● |  |  | ○ |  | ○ | ○ |  |  | ● | ● | ◐ |
| `bss-vap` |  |  |  |  | ● |  |  | ● |  | ● |  | ● |  |  | ● |  |  |  |  |  |  |  |  |
| `wireless-client` | ● | ○ | ● | ○ | ● | ● | ● | ● | ● | ● | ○ | ● | ● | ● | ◐ | ◐ |  | ○ | ◐ |  | ● | ● | ◐ |
| `wireless-rf` | ◐ | ◐ |  |  | ● |  |  | ◐ | ○ | ● |  | ● | ○ |  |  |  |  |  |  |  | ● | ◐ | ◐ |
| `rogue-wids` |  |  |  |  | ● |  |  | ● |  | ● |  | ● |  |  | ○ |  |  |  |  |  | ● |  |  |
| `mesh` |  |  |  |  | ● |  |  | ○ |  | ● |  | ◐ |  |  |  |  |  |  |  |  | ● |  |  |
| `roaming` |  | ○ | ○ |  | ● |  |  | ◐ |  | ◐ |  | ● |  | ○ |  |  |  |  |  |  | ● |  | ○ |
| `wireless-guest` |  |  |  |  | ● | ◐ |  |  | ○ | ○ |  | ○ |  |  | ○ | ● |  |  |  | ○ | ● |  | ◐ |
| `app-visibility` |  |  |  |  | ● |  |  |  |  | ○ |  | ○ |  |  |  |  |  |  |  |  |  |  |  |
| **Ops** | | | | | | | | | | | | | | | | | | | | | | | |
| `snmp-agent` | ● |  |  |  | ● | ● |  |  | ◐ | ◐ |  | ● | ◐ |  |  | ○ |  |  | ○ |  |  | ● |  |
| `syslog-events` | ● |  | ◐ |  | ● | ● |  |  | ◐ | ● | ◐ | ● | ● | ◐ |  | ○ |  |  |  |  | ● | ● | ◐ |
| `rmon` | ● |  |  |  | ● | ◐ |  | ○ | ● | ● |  | ● | ● | ◐ |  | ○ |  |  |  |  | ● | ● |  |
| `flow-export` | ◐ |  |  |  | ○ | ○ |  |  | ◐ | ◐ |  | ● | ◐ | ◐ |  |  |  |  |  |  | ● | ● |  |
| `time-sync` |  |  |  |  | ● | ● | ◐ |  | ◐ | ○ |  | ● | ● | ◐ |  | ○ |  |  |  |  |  | ● |  |
| `netconf-telemetry` | ○ |  | ○ |  | ● |  |  |  | ○ | ○ |  |  |  | ○ |  |  |  |  |  |  | ● | ◐ |  |
| `scheduling-automation` | ○ |  |  |  | ● | ○ |  |  |  | ◐ |  | ● | ● |  |  | ◐ |  |  |  |  |  | ● |  |
| `openflow-sdn` |  |  |  |  | ● |  |  |  |  | ○ |  | ○ | ◐ |  |  |  |  |  |  |  |  |  |  |
| `host-resources` | ● |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  |
| **WAN** | | | | | | | | | | | | | | | | | | | | | | | |
| `wan-serial` | ● |  | ◐ |  | ● |  |  |  | ○ | ● |  | ● |  | ◐ |  |  |  |  |  |  | ● |  |  |
| `dsl` | ● |  |  |  | ● |  |  |  |  |  |  |  |  |  |  |  |  |  |  |  | ● |  |  |
| `pon` |  |  |  |  | ○ |  |  |  |  | ● |  | ● |  |  |  |  |  | ◐ |  |  |  |  |  |
| `docsis` | ● |  |  |  |  |  |  |  |  | ◐ |  | ○ |  |  |  |  |  |  |  |  |  |  |  |
| `cellular` |  |  |  |  | ● |  |  |  |  | ◐ |  | ○ |  |  |  |  |  |  |  |  | ● |  |  |
| `bras` |  |  |  |  | ● |  |  |  |  | ● |  | ● |  |  |  |  |  |  |  |  | ● |  |  |
| `fibre-channel` | ◐ |  |  |  |  |  |  |  |  | ● |  | ○ |  |  |  |  |  |  |  |  |  |  |  |


## Evidence volume by entity

Total matched anchors per entity, largest first — a rough measure of how
much the industry has written about each thing.

| entity | domain | MIB tables | YANG nodes | total | record |
|---|---|---|---|---|---|
| `wireless-client` | Wireless | 523 | 2326 | 2849 | [→](../entities/08-wireless.md#wireless-client) |
| `ospf` | L3 Routing | 555 | 1698 | 2253 | [→](../entities/05-routing.md#ospf) |
| `bgp` | L3 Routing | 98 | 1713 | 1811 | [→](../entities/05-routing.md#bgp) |
| `mpls` | L3 Routing | 185 | 1272 | 1457 | [→](../entities/05-routing.md#mpls) |
| `aaa` | Security | 859 | 592 | 1451 | [→](../entities/07-security.md#aaa) |
| `isis` | L3 Routing | 42 | 1384 | 1426 | [→](../entities/05-routing.md#isis) |
| `qos-policy` | QoS | 903 | 351 | 1254 | [→](../entities/06-qos.md#qos-policy) |
| `pki-crypto` | Security | 94 | 1114 | 1208 | [→](../entities/07-security.md#pki-crypto) |
| `dhcp` | L3 IP | 851 | 315 | 1166 | [→](../entities/04-ip.md#dhcp) |
| `lldp` | L2 Switching | 973 | 118 | 1091 | [→](../entities/03-switching.md#lldp) |
| `firewall` | Security | 705 | 239 | 944 | [→](../entities/07-security.md#firewall) |
| `wan-serial` | WAN | 398 | 511 | 909 | [→](../entities/10-wan-access.md#wan-serial) |
| `ethernet-phy` | L1 Interface | 199 | 674 | 873 | [→](../entities/02-interface.md#ethernet-phy) |
| `rmon` | Ops | 644 | 227 | 871 | [→](../entities/09-ops.md#rmon) |
| `syslog-events` | Ops | 425 | 443 | 868 | [→](../entities/09-ops.md#syslog-events) |
| `tunnel-if` | L1 Interface | 548 | 313 | 861 | [→](../entities/02-interface.md#tunnel-if) |
| `fabric-overlay` | L2 Switching | 203 | 525 | 728 | [→](../entities/03-switching.md#fabric-overlay) |
| `nat` | L3 IP | 32 | 684 | 716 | [→](../entities/04-ip.md#nat) |
| `acl` | QoS | 295 | 369 | 664 | [→](../entities/06-qos.md#acl) |
| `radio` | Wireless | 254 | 269 | 523 | [→](../entities/08-wireless.md#radio) |
| `access-point` | Wireless | 129 | 387 | 516 | [→](../entities/08-wireless.md#access-point) |
| `routing-policy` | L3 IP | 49 | 435 | 484 | [→](../entities/04-ip.md#routing-policy) |
| `eigrp` | L3 Routing | 0 | 477 | 477 | [→](../entities/05-routing.md#eigrp) |
| `rib-fib` | L3 IP | 95 | 362 | 457 | [→](../entities/04-ip.md#rib-fib) |
| `vlan` | L2 Switching | 438 | 15 | 453 | [→](../entities/03-switching.md#vlan) |
| `stp` | L2 Switching | 221 | 204 | 425 | [→](../entities/03-switching.md#stp) |
| `multicast-routing` | L3 Routing | 211 | 210 | 421 | [→](../entities/05-routing.md#multicast-routing) |
| `ip-diagnostics` | L3 IP | 152 | 242 | 394 | [→](../entities/04-ip.md#ip-diagnostics) |
| `vrf` | L3 IP | 43 | 333 | 376 | [→](../entities/04-ip.md#vrf) |
| `dsl` | WAN | 332 | 44 | 376 | [→](../entities/10-wan-access.md#dsl) |
| `cdp-like` | L2 Switching | 133 | 236 | 369 | [→](../entities/03-switching.md#cdp-like) |
| `rogue-wids` | Wireless | 262 | 91 | 353 | [→](../entities/08-wireless.md#rogue-wids) |
| `time-sync` | Ops | 105 | 247 | 352 | [→](../entities/09-ops.md#time-sync) |
| `dot1x` | Security | 285 | 66 | 351 | [→](../entities/07-security.md#dot1x) |
| `bras` | WAN | 335 | 16 | 351 | [→](../entities/10-wan-access.md#bras) |
| `igmp-snooping` | L2 Switching | 326 | 19 | 345 | [→](../entities/03-switching.md#igmp-snooping) |
| `environment` | L0 Platform | 231 | 112 | 343 | [→](../entities/01-platform.md#environment) |
| `vrrp` | L3 IP | 222 | 94 | 316 | [→](../entities/04-ip.md#vrrp) |
| `wireless-rf` | Wireless | 199 | 112 | 311 | [→](../entities/08-wireless.md#wireless-rf) |
| `pon` | WAN | 308 | 1 | 309 | [→](../entities/10-wan-access.md#pon) |
| `stacking` | L0 Platform | 161 | 137 | 298 | [→](../entities/01-platform.md#stacking) |
| `poe` | L1 Interface | 207 | 84 | 291 | [→](../entities/02-interface.md#poe) |
| `qos-queue` | QoS | 213 | 67 | 280 | [→](../entities/06-qos.md#qos-queue) |
| `lag` | L1 Interface | 162 | 109 | 271 | [→](../entities/02-interface.md#lag) |
| `hw-component` | L0 Platform | 170 | 101 | 271 | [→](../entities/01-platform.md#hw-component) |
| `bss-vap` | Wireless | 217 | 52 | 269 | [→](../entities/08-wireless.md#bss-vap) |
| `icmp-tcp-udp` | L3 IP | 121 | 145 | 266 | [→](../entities/04-ip.md#icmp-tcp-udp) |
| `license` | L0 Platform | 45 | 215 | 260 | [→](../entities/01-platform.md#license) |
| `qinq` | L2 Switching | 253 | 6 | 259 | [→](../entities/03-switching.md#qinq) |
| `rip` | L3 Routing | 24 | 225 | 249 | [→](../entities/05-routing.md#rip) |
| `wlan-controller` | Wireless | 205 | 16 | 221 | [→](../entities/08-wireless.md#wlan-controller) |
| `power-supply` | L0 Platform | 171 | 48 | 219 | [→](../entities/01-platform.md#power-supply) |
| `vlan-membership` | L2 Switching | 144 | 54 | 198 | [→](../entities/03-switching.md#vlan-membership) |
| `fdb` | L2 Switching | 186 | 11 | 197 | [→](../entities/03-switching.md#fdb) |
| `ip-address` | L3 IP | 111 | 77 | 188 | [→](../entities/04-ip.md#ip-address) |
| `bfd` | L3 Routing | 20 | 165 | 185 | [→](../entities/05-routing.md#bfd) |
| `netconf-telemetry` | Ops | 23 | 159 | 182 | [→](../entities/09-ops.md#netconf-telemetry) |
| `macsec` | L2 Switching | 132 | 48 | 180 | [→](../entities/03-switching.md#macsec) |
| `wireless-guest` | Wireless | 106 | 63 | 169 | [→](../entities/08-wireless.md#wireless-guest) |
| `lacp` | L1 Interface | 145 | 22 | 167 | [→](../entities/02-interface.md#lacp) |
| `config-file` | L0 Platform | 80 | 82 | 162 | [→](../entities/01-platform.md#config-file) |
| `firmware-image` | L0 Platform | 93 | 64 | 157 | [→](../entities/01-platform.md#firmware-image) |
| `cpu-memory` | L0 Platform | 103 | 47 | 150 | [→](../entities/01-platform.md#cpu-memory) |
| `cable-diag` | L1 Interface | 36 | 109 | 145 | [→](../entities/02-interface.md#cable-diag) |
| `interface` | L1 Interface | 76 | 63 | 139 | [→](../entities/02-interface.md#interface) |
| `l2-security-guard` | L3 IP | 113 | 24 | 137 | [→](../entities/04-ip.md#l2-security-guard) |
| `app-visibility` | Wireless | 3 | 131 | 134 | [→](../entities/08-wireless.md#app-visibility) |
| `mesh` | Wireless | 34 | 95 | 129 | [→](../entities/08-wireless.md#mesh) |
| `roaming` | Wireless | 70 | 56 | 126 | [→](../entities/08-wireless.md#roaming) |
| `snmp-agent` | Ops | 91 | 22 | 113 | [→](../entities/09-ops.md#snmp-agent) |
| `transceiver` | L0 Platform | 72 | 37 | 109 | [→](../entities/01-platform.md#transceiver) |
| `link-oam` | L1 Interface | 88 | 19 | 107 | [→](../entities/02-interface.md#link-oam) |
| `dns` | L3 IP | 62 | 36 | 98 | [→](../entities/04-ip.md#dns) |
| `voice-vlan` | L2 Switching | 91 | 2 | 93 | [→](../entities/03-switching.md#voice-vlan) |
| `loopback-if` | L1 Interface | 62 | 30 | 92 | [→](../entities/02-interface.md#loopback-if) |
| `subinterface` | L1 Interface | 13 | 74 | 87 | [→](../entities/02-interface.md#subinterface) |
| `static-route` | L3 IP | 47 | 38 | 85 | [→](../entities/04-ip.md#static-route) |
| `scheduling-automation` | Ops | 42 | 43 | 85 | [→](../entities/09-ops.md#scheduling-automation) |
| `port-security` | Security | 63 | 22 | 85 | [→](../entities/07-security.md#port-security) |
| `flow-export` | Ops | 78 | 2 | 80 | [→](../entities/09-ops.md#flow-export) |
| `mirroring` | L2 Switching | 78 | 0 | 78 | [→](../entities/03-switching.md#mirroring) |
| `garp` | L2 Switching | 56 | 22 | 78 | [→](../entities/03-switching.md#garp) |
| `storm-control` | L2 Switching | 57 | 20 | 77 | [→](../entities/03-switching.md#storm-control) |
| `arp-nd` | L3 IP | 44 | 33 | 77 | [→](../entities/04-ip.md#arp-nd) |
| `ring-protection` | L2 Switching | 76 | 0 | 76 | [→](../entities/03-switching.md#ring-protection) |
| `cellular` | WAN | 26 | 36 | 62 | [→](../entities/10-wan-access.md#cellular) |
| `fibre-channel` | WAN | 61 | 0 | 61 | [→](../entities/10-wan-access.md#fibre-channel) |
| `udld` | L1 Interface | 26 | 28 | 54 | [→](../entities/02-interface.md#udld) |
| `system-identity` | L0 Platform | 49 | 5 | 54 | [→](../entities/01-platform.md#system-identity) |
| `mvr` | L2 Switching | 53 | 0 | 53 | [→](../entities/03-switching.md#mvr) |
| `port-isolation` | L2 Switching | 25 | 10 | 35 | [→](../entities/03-switching.md#port-isolation) |
| `if-counters` | L1 Interface | 28 | 6 | 34 | [→](../entities/02-interface.md#if-counters) |
| `docsis` | WAN | 34 | 0 | 34 | [→](../entities/10-wan-access.md#docsis) |
| `loop-protect` | L2 Switching | 25 | 7 | 32 | [→](../entities/03-switching.md#loop-protect) |
| `jumbo-mtu` | L2 Switching | 14 | 16 | 30 | [→](../entities/03-switching.md#jumbo-mtu) |
| `eee` | L1 Interface | 20 | 2 | 22 | [→](../entities/02-interface.md#eee) |
| `openflow-sdn` | Ops | 6 | 14 | 20 | [→](../entities/09-ops.md#openflow-sdn) |
| `host-resources` | Ops | 20 | 0 | 20 | [→](../entities/09-ops.md#host-resources) |
| `l2pt` | L2 Switching | 5 | 5 | 10 | [→](../entities/03-switching.md#l2pt) |
| `mgmt-if` | L1 Interface | 3 | 0 | 3 | [→](../entities/02-interface.md#mgmt-if) |
| `fec` | L1 Interface | 0 | 2 | 2 | [→](../entities/02-interface.md#fec) |

## Source-family breadth

How many of the 101 entities each family says anything about.

| family | entities covered |
|---|---|
| cisco-iosxe | 94 |
| huawei | 88 |
| hp-comware | 85 |
| ruckus-icx | 72 |
| lancom-sx | 71 |
| dlink | 67 |
| cisco-smb | 62 |
| hp-procurve | 59 |
| ietf | 56 |
| lancom-lcos | 52 |
| ubnt-edgemax | 44 |
| aruba-cx | 32 |
| openconfig | 32 |
| ieee | 28 |
| aruba-wireless | 23 |
| hp-other | 19 |
| lancom-lx | 18 |
| netgear | 17 |
| ruckus-wireless | 10 |
| ubnt-airmax | 7 |
| mikrotik | 3 |
| ubnt-unifi | 1 |
| ietf-yang | 1 |
