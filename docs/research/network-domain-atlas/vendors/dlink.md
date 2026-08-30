---
title: Vendor dossier — D-Link
date: 2026-08-30
scope: DLINKSW enterprise switches and the DGS-1210 smart-switch line
---

# D-Link

**Enterprise OID:** 171.
**Corpus:** 143 modules — the fourth-largest vendor set, and the only large one
with **no shared lineage with anybody**. D-Link wrote its own.
**Plane:** SNMP. No NETCONF, no RESTCONF, no published REST API, no YANG.

---

## Two product families in one directory

| Family | Modules | Products |
|---|---|---|
| `DLINKSW-*` (~130) | the enterprise/managed line | DGS-3000/3130/3630, DXS series |
| `DLINK-DGS-1210-Fx-SERIES-MIB`, `-Gx-SERIES-MIB`, `SWDGS1250PRIMGMT-MIB`, `SWDGS1510PRIMGMT-MIB`, `SWDGS1520PRIMGMT-MIB`, `SWDGS3630PRIMGMT-MIB`, `AGENT-GENERAL-MIB`, `EQUIPMENT-MIB`, `TIMERANGE-MIB`, `SINGLE-IP-MIB`, `ZONE-DEFENSE-MGMT-MIB`, `ZTP-MIB`, `DLINK-POWER-SAVING-MIB`, `DLINK-ID-REC-MIB` | the smart-switch line | DGS-1210, DGS-1250, DGS-1510, DGS-1520 |

The two families model the same features differently and are not
interchangeable. The DGS-1210 line even **redefines standard table names
privately** — `dot1qVlanTable[dot1qVlanName]` (keyed on the *name*, not the
index) and `dlinklldpConfigManAddrTable` are D-Link's own definitions sitting
under D-Link's OID arc while using IETF/IEEE object names. A collector matching
on object name alone will pick the wrong OID.

---

## Where D-Link is the reference implementation

**First-hop security.** Eight dedicated MIBs, more than any other vendor here:

`DLINKSW-DAI-MIB` (dynamic ARP inspection), `-IP-SOURCE-GUARD-MIB`,
`-IPV6-SRC-GUARD-MIB`, `-RA-GUARD-MIB`, `-ND-INSPECT-MIB`,
`-IPV6-SNOOPING-MIB`, `-DHCP-SNOOPING-MIB`, `-DHCP-FILTER-MIB`,
`-DHCP6-GUARD-MIB`, `-URPF-MIB`, plus **`DLINKSW-IMPB-MIB`** (IP-MAC-Port
Binding): `impbWhiteListTable[ip, mac]`, `impbBlackListTable[mac, vlanId,
port]`, `impbSmartTable[mac, port, ip]`, `impbSettingTable[ifIndex]`,
`impbViolationLogTable[ifIndex, vlan, mac]`.

The IMPB white/black/smart trio is effectively a curated endpoint inventory with
an enforcement action attached — a genuinely useful data source that no other
vendor exposes in this shape.

**Access control shapes.** D-Link ships two distinct ACL models:
`DLINKSW-ACL-MIB` on the enterprise line, and on the DGS-1210 line a
**profile+rule** design — `aclProfileTable[profileNo]` declares which header
fields are matched (a TCAM mask template) and
`aclL2RuleTable[profileID, accessID]` / `aclL3v4RuleTable` /
`aclL3v4ExtRuleTable` carry the values. That exposes the hardware's
mask-then-match structure directly and is the shape most resistant to
normalisation. Plus `DLINKSW-CPU-ACL-MIB` for control-plane policing.

**Resource visibility.** `DLINKSW-SRM-MIB` (switch resource manager) reports
*forwarding-table utilisation* — how full the FDB, ARP table, and ACL TCAM
are. Together with Huawei's `HUAWEI-RES-MON-MIB` this is the only place in the
corpus where you can see a switch running out of table space, which is a far
better operational signal than CPU load.

**Interface counters.** `DLINKSW-IF-COUNTER-MIB` goes beyond IF-MIB with
`dIfCounterIfUtilizationTable[ifIndex]` (pre-computed utilisation) and
`dIfCounterIfLinkChangeTable[ifIndex]` (flap count) — two derived values other
vendors make you compute.

---

## Full module map by area

| Area | Modules |
|---|---|
| System | `DLINKSW-GENMGMT-MIB`, `-SYSTEM-FILE-MIB`, `-REBOOT-MIB`, `-SDCARDMGMT-MIB`, `-ENTITY-EXT-MIB`, `-LED-MIB`, `-STACK-MIB`, `-DLMS-MIB` (licensing), `ZTP-MIB`, `SINGLE-IP-MIB`, `-SRM-MIB` |
| Interfaces | `-IF-COUNTER-MIB`, `-CABLE-DIAG-MIB`, `-DDM-MIB`, `-SFPINFO-MIB`, `-DULD-MIB`, `-LOOPBACK-TEST-MIB`, `-POE-MIB`, `-POWER-SAVING-MIB` |
| L2 | `-VLAN-MIB`, `-SWITCHPORT-MIB`, `-L2FDB-MIB`, `-STP-EXT-MIB`, `-LACP-EXT-MIB`, `-LLDP-EXT-MIB`, `-GVRP-MIB`, `-MVRP-MIB`, `-LBD-MIB`, `-BPDU-PROTECTION-MIB`, `-PRIVATE-VLAN-MIB`, `-SUPER-VLAN-MIB`, `-VOICE-VLAN-MIB`, `-SURVEILLANCE-VLAN-MIB`, `-MCAST-VLAN-MIB`, `-VLAN-TUNNEL-MIB`, `-L2PT-MIB`, `-TRAFFIC-SEGMENT-MIB`, `-STORM-CTRL-MIB`, `-ERPS-MIB`, `-FLEXLINKS-MIB`, `-MLAG-MIB`, `-CFM-EXT-MIB`, `-DCB-EXT-MIB`, `-ISCSI-AWARENESS-MIB`, `-NLB-MIB` |
| L3 | `-IP-EXT-MIB`, `-IP-ROUTING-MIB`, `-RIP-MIB`, `-RIPNG-MIB`, `-OSPFV2-MIB`, `-OSPFV3-MIB`, `-ISIS-MIB`, `-BGP-MIB`, `-BFD-MIB`, `-VRRP-EXT-MIB`, `-VRF-LITE-MIB`, `-PBR-MIB`, `-ROUTE-MAP-MIB`, `-DHCP-SERVER-MIB`, `-DHCP-RELAY-MIB`, `-DHCP6-*`, `-DNS-MIB`, `-IPV6-DEVICE-MONITOR-MIB` |
| Multicast | `-MGMD-SNOOPING-MIB`, `-MGMD-EXT-MIB`, `-MGMD-PROXY-MIB`, `-PIM-EXT-MIB`, `-PIM-SNOOPING-MIB`, `-MSDP-EXT-MIB`, `-DVMRP-EXT-MIB`, `-IPMCAST-EXT-MIB` |
| QoS | `-QOS-MIB`, `-WRED-MIB`, `-TIME-RANGE-MIB` |
| Security | the first-hop set above, plus `-AAA-COMMON/-AUTH/-ACCOUNTING/-SERVER-MIB`, `-DOT1X-EXT-MIB`, `-MAC-AUTH-MIB`, `-WEB-AUTH-MIB`, `-JWAC-MIB`, `-NETWORK-ACCESS-MIB`, `-PORT-SECURITY-MIB`, `-SAFEGUARD-ENGINE-MIB`, `-DOS-PREVENT-MIB`, `-CPU-PROTECT-MIB`, `-NETWORK-PROTOCOL-PORT-PROTECT-MIB`, `-SSH-MIB`, `-SSH-CLIENT-MIB`, `-SSL-MIB`, `-SFTP-SERVER-MIB` |
| Ops | `-SYSLOG-MIB`, `-SNMP-MIB`, `-NTP-MIB`, `-PTP-MIB`, `-TIME-MIB`, `-SFLOW-MIB`, `-PACKET-MONITOR-MIB`, `-CPU-PORT-COUNTER-MIB`, `-EXTERNAL-ALARM-MIB`, `-ERROR-DISABLE-MIB`, `-SMTP-MIB`, `-CLI-ALIAS-MIB`, `-TELNET-MIB`, `-WEB-COMMON-MIB`, `-OPENFLOW-MIB`, `-CONTROLLER-MIB`, `-DDP-CLIENT-MIB`, `-NBF-MIB`, `-ASP-MIB`, `-FS-MIB` |
| Carrier | `-MPLS-MIB`, `-VPLS-MIB`, `-VPWS-MIB` |

---

## Traps

- **Two families, one directory.** Always establish which line you are talking
  to from `sysObjectID` before choosing a MIB.
- The DGS-1210 line's private redefinitions of standard names are the sharpest
  hazard in the whole corpus for name-based matching.
- `DLINKSW-CONTROLLER-MIB` is a wireless-controller stub, not an SDN controller.
- `ZONE-DEFENSE-MGMT-MIB` is the switch acting on quarantine instructions from a
  D-Link *firewall* — an inter-product integration, not a switch feature.
- `-DDP-CLIENT-MIB` is D-Link Discovery Protocol, a proprietary neighbour
  protocol; see [cdp-like](../entities/03-switching.md#cdp-like).
