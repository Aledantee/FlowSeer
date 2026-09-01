---
title: Vendor dossier — Cisco
date: 2026-08-30
scope: Cisco Small Business (CISCOSB), Cisco IOS-XE, and the missing enterprise MIB set
---

# Cisco

Three distinct platforms with nothing in common but a company name and
enterprise OID 9.

---

## Small Business (CISCOSB)

**Products:** SG/SF series, CBS250/350, Catalyst 1200/1300.
**Stack:** Marvell silicon with a Radlan-derived OS — the object prefix `rl`
is Radlan. This is *not* IOS and shares no code with anything else Cisco ships.
**Plane:** SNMP only (plus a web UI). No NETCONF, no RESTCONF, no YANG.
**Corpus:** 116 modules in `spec/mib/cisco/smb/`.

### Shape

Unusually broad for an access switch — 116 modules covering everything from
`CISCOSB-BONJOUR-MIB` to `CISCOSB-openflow-MIB`. Notable design habits:

- **Policy in the key.** `rlSysNameTable[source, ifIndex]` (sysName by where it
  came from), `rlIpAddressTable[addrType, addr, origin, generalPrefixName]`
  (address by origin), `rlDnsClv2ServersTable[source, ifIndex, preference,
  addrType, inetAddr, subTree, class]` (resolver by subtree and preference),
  `rlMngInfListTable[name, priority]` (management interfaces as an ordered
  list). Where other vendors put a column, Cisco SMB puts an index. Rows
  therefore multiply and de-duplication is the collector's problem.
- **Faithful augments where a standard exists.** `rldot1qTpFdbTable [AUGMENTS
  dot1qTpFdbEntry]`, `rlEntPhySensorTable [AUG entPhySensorEntry]`,
  `rlVrrpv3OperationsTable [AUG]`, `rlipNetToPhysicalTable [AUG]`,
  `rldot1xExtAuthSessionStatsTable [AUG]`. Good citizenship, and it means the
  base tables are reliable.
- **A generic counter framework** rather than fixed columns:
  `rlPortStatSampleTable[ifIndex, statSubType, counterName, statID]` — counters
  addressed by *name string*. Powerful, and impossible to model statically.
- **Job tables**: `rlCopyTable[index]` + `rlCopyHistoryTable` +
  `rlCopyMessagesTable[copyIndex, messageIndex]` for config transfer;
  `rlPhyTestSetTable`/`rlPhyTestGetTable` for cable diagnostics.

### Entities where Cisco SMB is the reference

`CISCOSB-EEE-MIB` (the most complete EEE surface, including the LLDP-negotiated
view), `CISCOSB-GREEN-MIB` (short-reach power reduction),
`CISCOSB-SMARTPORTS-MIB` (auto-configuration macros),
`CISCOSB-SECURITY-SUITE`, `CISCOSB-TIMEBASED-PORT-SHUTDOWN-MIB`.

### Traps

- `dot1dBasePort` vs `ifIndex` is used inconsistently across CISCOSB modules —
  `rldot1dStpPortBpduGuardTable[dot1dBasePort]` next to
  `rlLbdPortTable[ifIndex]` in the same product.
- The PoE model has three parallel tables (`rlPethPsePortTable`,
  `rlPethMainPseTable`, `rlPethPdPortTable`) plus a stack-unit-keyed
  `rlPhdPoeTable[stackUnit]`.
- `CISCOSB-MIB` and `CISCOSMB-MIB` are different modules; so are
  `CISCOSB-AAA` (no `-MIB` suffix) and several other unsuffixed ones. Do not
  assume the naming is regular.

---

## IOS-XE

**Products:** Catalyst 9000 switches, ISR/ASR routers, Catalyst 9800 wireless
controllers.
**Plane:** NETCONF (port 830), RESTCONF, gNMI. ConfD underneath — the
`tailf-*` modules in the tree are the giveaway.
**Corpus:** `spec/yang/cisco/iosxe/2611/` — the complete published model set for
IOS-XE 26.1.1: 887 modules in the main directory, 184 MIB-derived modules under
`MIBS/`, 57 markdown docs under `BIC/`, and per-platform capability XML
(`capability-cat9k.xml`, `capability-cat9200.xml`, `capability-c8000v.xml`,
`capability-ie3x00.xml`, `capability-wireless.xml`, …).

### Shape

613 modules named `Cisco-IOS-XE-*`, in four flavours distinguished by suffix:

| Suffix | Meaning |
|---|---|
| (none) | native configuration, augmenting `/native` |
| `-oper` | operational state |
| `-cfg` | configuration for a subsystem with its own tree (mostly wireless) |
| `-rpc`, `-actions-rpc`, `-cmd-rpc` | operations |
| `-events` | notification definitions |
| `-types`, `-common` | shared typedefs |
| `-deviation` | what this platform does *not* support |
| `-obsolete` | retained for compatibility |

Plus 56 `cisco-xe-*` deviation/extension modules for the OpenConfig and IETF
models, and `cisco-*` platform modules (`cisco-bridge-domain`,
`cisco-ethernet`, `cisco-policy`, `cisco-pw`, `cisco-storm-control`,
`cisco-smart-license`, `cisco-ia`, `cisco-semver`).

### The wireless subtree is the crown jewel

**90 `Cisco-IOS-XE-wireless-*` modules** — the most complete openly-published
wireless controller model in existence. Coverage:
`-access-point-oper` / `-cfg-rpc` / `-cmd-rpc`, `-ap-cfg` / `-ap-global-oper` /
`-ap-types`, `-client-oper` / `-client-global-oper` / `-client-types` /
`-client-rpc`, `-radio-cfg`, `-rf-cfg`, `-dot11-cfg`, `-dot15-cfg`,
`-rlan-cfg`, `-apf-cfg`, `-hotspot-cfg`, `-mesh-cfg` / `-oper` /
`-global-oper` / `-rpc`, `-mobility-cfg` / `-oper` / `-types`,
`-rogue-cfg` / `-authz-rpc`, `-awips-oper`, `-location-cfg` / `-oper`,
`-geolocation-oper`, `-hyperlocation-oper`, `-nmsp-oper`, `-rfid-*`,
`-ble-mgmt-oper` / `-ltx-oper`, `-cisco-spaces-oper`, `-afc-oper` /
`-afc-cloud-oper` (6 GHz AFC), `-flex-cfg`, `-fabric-cfg`, `-mcast-oper`,
`-mdns-oper`, `-mstream-cfg`, `-lisp-agent-oper`, `-cts-sxp-cfg` / `-oper`,
`-general-cfg` / `-oper`, `-site-cfg`, `-events-oper`.

Even if FlowSeer never manages a 9800, this is the best available reference for
*what a wireless controller model needs to contain* — see
[08-wireless](../entities/08-wireless.md).

### Traps

- **Use the deviation files.** `cisco-xe-openconfig-interfaces-deviation`,
  `-acl-deviation`, `-bgp-deviation`, `-lldp-deviation`, `-if-ip-deviation`,
  `-if-poe-deviation`, `-network-instance-deviation` and the `-ietf-*-deviation`
  set state what actually works. Twenty of them.
- The `capability-*.xml` files per platform are the second half of the same
  answer: which modules a given box advertises.
- `-obsolete` modules (`Cisco-IOS-XE-ospf-obsolete`, `-eigrp-obsolete`) still
  parse and still contain plausible paths. Skip them.
- The 184 `MIBS/` modules are SMIv2-to-YANG translations, useful for mapping OID
  ↔ path but not for anything you would poll.

---

## The gap

**`spec/mib/cisco/enterprise/` is documented in `spec/mib/README.md` and does
not exist.** The directory is described as an "official cisco/cisco-mibs clone
(v1, v2, traps, ucs, …) — IOS/IOS-XE, AIRESPACE/LWAPP, Catalyst" and there is
nothing there. The only Cisco MIBs in the corpus are CISCOSB.

What that means concretely:

| Missing | Consequence |
|---|---|
| `CISCO-CDP-MIB` | CDP neighbours cannot be read from any Cisco device. CDP is referenced in [cdp-like](../entities/03-switching.md#cdp-like) as the primary non-LLDP discovery protocol and there is no MIB for it here |
| `CISCO-VTP-MIB` | `vtpVlanTable` is how you read VLANs from a Catalyst; referenced by the IOS-XE YANG deviations, not vendored |
| `CISCO-STACK-MIB`, `CISCO-STP-EXTENSIONS-MIB`, `CISCO-VLAN-MEMBERSHIP-MIB` | Catalyst L2 state |
| `CISCO-ENTITY-SENSOR-MIB`, `CISCO-ENVMON-MIB`, `CISCO-PROCESS-MIB`, `CISCO-MEMORY-POOL-MIB` | Catalyst platform health |
| `CISCO-POWER-ETHERNET-EXT-MIB` | Cisco PoE beyond RFC 3621 |
| `CISCO-CONFIG-COPY-MIB`, `CISCO-CONFIG-MAN-MIB` | config backup, the job model referenced in [config-file](../entities/01-platform.md#config-file) |
| `AIRESPACE-WIRELESS-MIB`, `AIRESPACE-SWITCHING-MIB`, `CISCO-LWAPP-*` | **AireOS WLCs, which `spec/yang/cisco/iosxe/SOURCES.md` explicitly says are SNMP-only** — so there is currently no way to read a Cisco AireOS controller at all |
| `CISCO-EIGRP-MIB` | EIGRP, which has zero MIB coverage in the corpus |

The AireOS case is the sharpest: the repo's own SOURCES note says those
controllers have no YANG and remain SNMP-only, and then the SNMP half is
missing. Either the directory should be populated from
`github.com/cisco/cisco-mibs`, or `spec/mib/README.md` should be corrected to
say Cisco enterprise is deliberately out of scope. Right now the documentation
promises something the tree does not contain.
