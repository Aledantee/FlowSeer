---
title: Entity records — L1, interfaces and PHY
date: 2026-08-30
part: 2 of 10
scope: interface identity, counters, Ethernet PHY, PoE, aggregation, tunnels, link diagnostics
---

# L1 — interfaces and PHY

Sixteen entities. This is the layer FlowSeer has modelled furthest
(`flowseer.net.interface.v1`, `net/phy/v1`), so each record ends with what the
repo already has and what the corpus says is still missing.

Index: [interface](#interface) · [if-counters](#if-counters) ·
[ethernet-phy](#ethernet-phy) · [eee](#eee) · [fec](#fec) · [poe](#poe) ·
[subinterface](#subinterface) · [lag](#lag) · [lacp](#lacp) ·
[tunnel-if](#tunnel-if) · [loopback-if](#loopback-if) · [mgmt-if](#mgmt-if) ·
[cable-diag](#cable-diag) · [link-oam](#link-oam) · [udld](#udld) ·
[transceiver](#transceiver-see-part-1)

---

## interface

**What it is.** The addressable port or logical port a device exposes. Every
other L1–L3 entity in this atlas is either keyed on it or hangs off it, which is
why its identity question dominates everything downstream.

### Canonical models

| Model | Key | Notes |
|---|---|---|
| `IF-MIB::ifTable` | `ifIndex` (Integer32 1..2147483647) | RFC 2863. `ifDescr`, `ifType`, `ifMtu`, `ifSpeed`, `ifPhysAddress`, `ifAdminStatus`, `ifOperStatus`, `ifLastChange` |
| `IF-MIB::ifXTable` | `AUGMENTS ifEntry` | `ifName`, `ifAlias`, `ifHighSpeed`, `ifConnectorPresent`, the HC counters |
| `RFC1213-MIB::ifTable` | `ifIndex` | The MIB-II original; still the only thing some cheap agents implement |
| `ietf-interfaces` (RFC 8343) | `name` (string) | `if-index` demoted to a state leaf under the `if-mib` feature |
| `openconfig-interfaces` | `name` | `config`/`state` split; `subinterfaces` list under each |
| `iana-if-type` / `IANAifType-MIB` | — | The `ifType` registry, shared by both worlds |

### The identity problem, stated precisely

Four distinct identifiers are in play and no two agree across vendors:

1. **`ifIndex`** — small integer, cheap to poll, and *not stable*. RFC 2863 only
   requires persistence across re-initialisation if `ifTableLastChange` says
   nothing changed. In practice a reload, a module insertion, or a firmware
   upgrade renumbers it on plenty of platforms. Anything FlowSeer persists keyed
   on `ifIndex` alone will silently re-attach to the wrong port.
2. **`ifDescr`** — free text, vendor-shaped ("GigabitEthernet1/0/1",
   "ethernet1/1", "Slot: 0 Port: 1 Gigabit - Level").
3. **`ifName`** — the short CLI name, *only* in `ifXTable`, and absent on agents
   that never implemented it. RFC 8343 explicitly warns IF-MIB permits duplicate
   `ifName` values, so it is not a key either.
4. **`ifAlias`** — the operator's description. Persistent by spec, empty in
   practice, and the one field operators actually care about.

FlowSeer's tables "reference interfaces by name" (`CONCEPTS.md`). That inherits
the duplicate-`ifName` hazard. Two mitigations are visible in the prior art:
Netdisco stores both the name and the raw index and reconciles on `ifDescr`;
SNMP::Info exposes `interfaces()` as an explicit *index → display name* map so
callers choose. Whichever FlowSeer picks, the choice must be recorded in the
schema comments, because every table row depends on it.

### Vendor mapping

| Family | Surface | Notes |
|---|---|---|
| IETF | `IF-MIB` | universal baseline |
| Cisco SMB | `CISCOSB-rlInterfaces` | thin extension; the base is IF-MIB |
| Cisco IOS-XE | `Cisco-IOS-XE-interfaces-oper`, `openconfig-interfaces` (deviated by `cisco-xe-openconfig-interfaces-deviation`) | the deviation file is the authoritative statement of what the box really supports |
| HPE Comware | `HH3C-IF-EXT-MIB::hh3cIfTable[ifIndex]` plus `hh3cIfLinkModeTable`, `hh3cIfPortTypeTable`, `hh3cRTParentIfTable`/`hh3cRTSubIfTable` | Comware separates "port type" from `ifType`, and models the parent/sub relation itself rather than via `ifStackTable` |
| HP ProCurve | `HP-IF-EXT-MIB::hpifStatsTable [AUGMENTS ifEntry]` | augment-only; identity stays IF-MIB |
| Aruba CX | `ARUBAWIRED-INTERFACE-MIB`; OpenConfig `interfaces` via gNMI; REST v10.0x is the real surface | |
| Huawei | `HUAWEI-IF-EXT-MIB::hwIFExtTable[hwIFExtIndex]`, plus `hwIfQueryTable[hwIfName]` — a *name-keyed* lookup table | `hwIfQueryTable` is the useful oddity: Huawei ships a name→index resolver in the MIB |
| Ruckus ICX | `openconfig-interfaces` over RESTCONF (`icx-openconfig-*-dev` for deviations); IF-MIB over SNMP | |
| Aruba wireless | `WLSX-IFEXT-MIB::wlsxIfExtPortTable[slot, port]` | note the key: **slot+port, not ifIndex** — a second identity namespace on the same box |
| LANCOM SX / Netgear / EdgeSwitch | IF-MIB + FASTPATH `agent*` tables | one shared lineage, see [part 0](../00-methodology-and-lineages.md) |
| LANCOM LCOS | `lcsStatusEthernetPortsPortsTable[...EntryPort]` | LCOS does not key on ifIndex at all; its own port numbering is the key |
| MikroTik | IF-MIB; RouterOS REST `/interface` is richer | |

### Traps and pitfalls

- **`ifSpeed` saturates at 4.29 Gbit/s** (Gauge32 in bits/s). Above 1 GE you must
  read `ifHighSpeed` (Mbit/s, so it loses sub-Mbit precision) — and some agents
  report 1000000000 in `ifSpeed` on a 10 GE port rather than the 4294967295
  RFC 2863 mandates. FlowSeer's `uint64 speed_bps` is right; the *decoder* has
  to pick the source and coerce.
- **`ifOperStatus` has seven values**, not two: up, down, testing, unknown,
  dormant, notPresent, lowerLayerDown. `notPresent` (an empty slot) and
  `lowerLayerDown` (an SVI whose members are down) carry real meaning and are
  routinely flattened to "down" by naive collectors.
- **`ifStackTable`** is the only standard statement of the interface hierarchy
  (LAG→member, subif→parent, SVI→VLAN). Support is patchy, and Comware and
  Huawei each ship their own parent/child tables instead. LibreNMS keeps a whole
  discovery module (`ports-stack`) just for it.
- **`ifType` is a registry, not an enum** (IANAifType-MIB, ~300 values and
  growing). FlowSeer's typed-variant interface family (physical / vlan / lag /
  loopback / tunnel / management / subinterface / other) is a *projection* of
  it; the mapping table from ifType to variant is the load-bearing artifact and
  does not exist in the repo yet.

### FlowSeer status

Modelled: `net/interface/v1` with the typed family, `admin_status`,
`oper_status`, `interface_counters`. Missing: the ifType→variant mapping, any
statement of what the interface key *is*, `ifStackTable`-equivalent hierarchy,
and `ifAlias` vs `ifDescr` vs `ifName` disambiguation.

---

## if-counters

**What it is.** Per-interface traffic and error counts.

### Canonical models

- `IF-MIB::ifTable` 32-bit counters, `ifXTable` 64-bit (`ifHCInOctets`,
  `ifHCInUcastPkts`, `ifHCInMulticastPkts`, `ifHCInBroadcastPkts` and the `Out`
  mirrors).
- `EtherLike-MIB::dot3StatsTable[dot3StatsIndex]` — the Ethernet-specific error
  breakdown: alignment errors, FCS errors, single/multiple collisions, late
  collisions, excessive collisions, deferred transmissions, carrier sense
  errors, internal MAC transmit/receive errors, frame-too-long. Plus
  `dot3HCStatsTable` for the 64-bit versions and `dot3ControlTable` /
  `dot3PauseTable` for 802.3x flow control.
- `RMON-MIB::etherStatsTable[etherStatsIndex]` — the frame-size histogram
  (64, 65–127, … 1024–1518) plus drops, and
  `HC-RMON-MIB::etherStatsHighCapacityTable` for its 64-bit form.
- `openconfig-interfaces` `state/counters`, `openconfig-if-ethernet`
  `ethernet/state/counters`.

### Vendor mapping

Everyone implements IF-MIB counters. The divergence is in the *extra* tables:

| Family | Extra |
|---|---|
| D-Link | `DLINKSW-IF-COUNTER-MIB`: `dIfRxStatsTable`, `dIfTxStatsTable`, `dIfCounterRxDropTable`, `dIfCounterTxDropTable`, `dIfCounterIfUtilizationTable` (pre-computed utilisation), `dIfCounterIfLinkChangeTable` (flap count) |
| Cisco SMB | `CISCOSB-PORT-STATISTICS-MIB` — a *generic* counter framework keyed `[ifIndex, subtype, counterName, statID]`, i.e. a string-named counter bag, not fixed columns |
| Huawei | `HUAWEI-IF-EXT-MIB::hwIfEtherStatTable`, plus per-feature counters (`hwXQoSPortStatisticsDropTable`) |
| Comware | `hh3cIfFlowStatTable` / `hh3cIfHCFlowStatTable` (rates, not counters) and `hh3cIfSpeedStatTable` |
| LANCOM LCOS | `lcsStatusEthernetPortsPacketTransportTable` / `...ByteTransportTable` / `...ErrorsTable`, keyed on LCOS's own `Ifc` |
| LANCOM SX / Netgear / EdgeSwitch | EtherLike + RMON, unmodified |

### Traps and pitfalls

- **Counter discontinuity.** `ifCounterDiscontinuityTime` in `ifXTable` exists so
  a poller can tell a wrap from a reset. Almost no collector reads it. Any rate
  FlowSeer derives without it is wrong across a reload.
- **32-bit wrap at 1 GE is ~34 seconds** of line-rate traffic. The HC counters
  are mandatory above 20 Mbit/s per RFC 2863, but plenty of agents populate them
  with zeros while filling the 32-bit ones. A decoder that trusts
  `ifHCInOctets == 0` will report a dead link.
- `ifInNUcastPkts` / `ifOutNUcastPkts` are deprecated in favour of the split
  multicast/broadcast counters; older agents only have the deprecated pair.
- RMON `etherStatsTable` is keyed on `etherStatsIndex`, an *RMON control-row*
  index, not `ifIndex`. You must read `etherStatsDataSource` (an OID pointing at
  `ifIndex.n`) to join. Every collector that skips this step mislabels ports.

### FlowSeer status

`net/interface/v1/interface_counters.proto` exists. Not present: the
discontinuity marker, the Ethernet error breakdown (`dot3Stats*`), the frame
size histogram, or any statement of counter width provenance.

---

## ethernet-phy

**What it is.** Negotiated and configured link facts for an Ethernet port:
speed, duplex, autonegotiation, medium, FEC.

### Canonical models

- `MAU-MIB::ifMauTable[ifMauIfIndex, ifMauIndex]` (RFC 4836) — `ifMauType` (an
  OID into `IANA-MAU-MIB`'s ~100 MAU type registrations), `ifMauMediaAvailable`,
  `ifMauJabberState`, `ifMauAutoNegSupported`. Plus
  `ifMauAutoNegTable[ifMauIfIndex, ifMauIndex]` — `ifMauAutoNegAdminStatus`,
  `ifMauAutoNegCapability`, `ifMauAutoNegCapAdvertised`,
  `ifMauAutoNegCapReceived`, `ifMauAutoNegRemoteFaultAdvertised`.
- `EtherLike-MIB::dot3StatsDuplexStatus` — the one-column answer for duplex.
- `openconfig-if-ethernet`: `ethernet/config/{auto-negotiate, duplex-mode,
  port-speed, enable-flow-control, standalone-link-training, fec-mode}` and the
  matching `state`, plus `state/negotiated-port-speed` and
  `negotiated-duplex-mode`.
- `openconfig-if-ethernet-ext`: the frame-size distribution histogram
  (`ethernet-in-frames-size-dist/in-distribution` — present in the Aruba bundle).

### Vendor mapping

| Family | Surface |
|---|---|
| IETF/IEEE | `MAU-MIB` + `IANA-MAU-MIB`, `EtherLike-MIB` |
| Comware | `HH3C-LswINF-MIB::hh3cethernetTable[ifIndex]` — the single Comware answer for speed/duplex/flow-control/MDI |
| Huawei | `HUAWEI-PORT-MIB::hwEthernetTable[hwEthernetIfIndex]` |
| Cisco SMB | speed/duplex live in `CISCOSB-rlInterfaces`; `CISCOSB-PHY-MIB` is *cable test*, not PHY facts (`rlPhyTestSetTable` / `rlPhyTestGetTable`) |
| LANCOM LCOS | `lcsStatusEthernetPortsPortsTable`, `lcsSetupInterfacesEthernetPortsTable`, `lcsStatusEthernetPortsSfpPortsTable` |
| LANCOM SX | MAU-MIB + EtherLike, plus `lcsEnergyEfficientEthernetConfigTable` |
| Aruba CX / Ruckus ICX | `openconfig-if-ethernet` with `icx-openconfig-if-ethernet-aug` / `-dev` stating the real support set |
| Cisco IOS-XE | `Cisco-IOS-XE-interfaces-oper` + `cisco-xe-openconfig-if-ethernet-ext` |

### Traps and pitfalls

- **`ifMauType` is an OID, not an enum.** Decoding it means carrying the
  IANA-MAU registry. Most collectors give up and read `ifSpeed` + duplex
  instead, losing the medium and the exact PHY. FlowSeer's research doc already
  decided to defer exact MAU types; that decision is right but should be
  recorded as "we deliberately do not decode `ifMauType`".
- **Autonegotiation has three orthogonal facts** — is it enabled, what did we
  advertise, what did the partner advertise — and vendors collapse them
  differently. FlowSeer's split of `EthernetSettings` (intent) from
  `AutoNegotiationFacet` (observed) is the correct shape and is rarer than it
  should be.
- Duplex on a modern 10G+ port is meaningless (always full) but still reported;
  half-duplex on a gigabit access port is a real and important fault signal.
- **FEC has essentially no SNMP representation.** The corpus has *zero* MIB
  tables for FEC across 1,691 modules; the only hits are OpenConfig
  (`fec-mode`) and Cisco YANG. If FlowSeer wants FEC state it must come from
  YANG/REST, never SNMP. See [fec](#fec).

### FlowSeer status

`net/phy/v1` covers `ethernet_settings`, `ethernet_capabilities`,
`ethernet_facet` with its copper, fiber, backplane, and other transport arms,
`ethernet_duplex`, `ethernet_fec_mode`, `mau_type`, `mau_link_mode`,
`ethernet_counters`, `auto_negotiation_facet` / `_status`, and the PoE
messages on the copper arm. That is ahead of most prior art. Missing:
flow-control (802.3x PAUSE) state, which is in `dot3PauseTable` and
`openconfig-if-ethernet` and which operators do read.

---

## eee

**What it is.** 802.3az Energy Efficient Ethernet — low-power idle on a link.

### Canonical models

There is no IETF EEE MIB in the corpus. The IEEE answer is `dot3aEEE*` objects
added to `EtherLike-MIB` by 802.3az, and the LLDP dot3 extension carries EEE
TLVs. OpenConfig has no EEE model. So this entity is **vendor-private
everywhere**.

### Vendor mapping

| Family | Surface | Key |
|---|---|---|
| Cisco SMB | `CISCOSB-EEE-MIB`: `rlEeePortTable`, `rlEeePortLldpTable`, `rlEeePortLldpLocalTable`, `rlEeePortLldpRemoteTable` | `ifIndex` |
| D-Link | `DLINK-POWER-SAVING-MIB` / `DLINKSW-POWER-SAVING-MIB`: `dpsIfEeeTable[ifIndex]`, plus `dpsPortShutdownScheduleTable` (scheduled port power-down) | `ifIndex` |
| LANCOM SX | `LCOS-SX-MIB::lcsEnergyEfficientEthernetConfigTable[Port]` | own port id |
| Huawei | `HUAWEI-ENERGYMNGT-MIB`: a whole energy framework — `hwBoardPowerMngtTable`, `hwEnergySavingMethodTable`, `hwEnergySavingParameterTable`, `hwEnergySavingCapabilityMngtTable` | board / method index |
| Cisco IOS-XE | `Cisco-IOS-XE-interfaces-oper` `interface-state/eee-status` and `eee-caps` | |
| Cisco SMB (again) | `CISCOSB-GREEN-MIB` — short-reach mode and cable-length-based power reduction, a *different* saving mechanism |

### Notes

Four vendors, four incompatible shapes, and Huawei's is not per-port at all.
Worth modelling only as a small optional facet (`enabled`, `active`,
`partner-supports`) with everything else declined. Low priority: it is a
power-tuning knob, not a fault or topology fact.

---

## fec

**What it is.** Forward error correction mode on high-speed Ethernet
(RS-FEC / Clause 91, BASE-R FEC / Clause 74, or none).

### State of the art

**No SNMP representation exists in the entire 1,691-module corpus.** The only
hits are `openconfig-if-ethernet` `fec-mode` (identityref: `FEC_FC`,
`FEC_RS528`, `FEC_RS544`, `FEC_RS544_2X_INTERLEAVE`, `FEC_DISABLED`, `FEC_AUTO`)
plus `fec-status` and `fec-uncorrectable-blocks` counters, and Cisco/Ruckus
OpenConfig deviations of the same.

### Implication

FEC is a YANG-era concept. Any FlowSeer surface for it will be populated only
from AOS-CX, IOS-XE, and ICX RESTCONF, and must degrade to absent for every
SNMP-only vendor — which is most of them. FlowSeer already models
`ethernet_fec_mode`; the schema comment should say the observation is
YANG-only, otherwise a reader assumes an SNMP path exists.

*(The `fec` matches in `Cisco-IOS-XE-mpls-ldp-oper` and `-multicast` are MPLS
Forwarding Equivalence Classes — a different "FEC" entirely. Classifier false
positive, recorded so the next reader does not chase it.)*

---

## poe

**What it is.** Power sourcing over the data pair: what the port can deliver,
what it is delivering, to what class of powered device, at what priority.

### Canonical models

`POWER-ETHERNET-MIB` (RFC 3621) is the one genuinely universal vendor-private
extension point in this corpus — nearly every switch vendor augments it rather
than replacing it.

| Table | Key | Carries |
|---|---|---|
| `pethPsePortTable` | `pethPsePortGroupIndex, pethPsePortIndex` | admin enable, detection status, priority, power class, MPS-absent counter, type |
| `pethMainPseTable` | `pethMainPseGroupIndex` | total power, operational status, consumption, usage threshold |
| `pethNotificationControlTable` | `pethNotificationControlGroupIndex` | trap enable |

Note the key: **group + port, not ifIndex**. On a stack the group is the unit.
Joining PoE rows to interfaces requires a per-vendor rule and is a classic
source of off-by-one bugs.

OpenConfig: `openconfig-if-poe` — `poe/config/enabled`,
`poe/state/{power-used, power-class, powered-device-status}` hanging off
`interfaces/interface/ethernet`, i.e. **ifName-keyed**, resolving the join
problem the MIB creates.

LLDP-MED carries PoE negotiation as TLVs:
`LLDP-EXT-MED-MIB::lldpXMedLocXPoEPSEPortTable`, `lldpXMedRemXPoETable`,
`lldpXMedRemXPoEPSETable`, `lldpXMedRemXPoEPDTable` — the *negotiated* power,
which can differ from what the PSE hardware reports.

### Vendor mapping

| Family | Surface | Shape |
|---|---|---|
| IETF | `POWER-ETHERNET-MIB` | baseline |
| Aruba CX | `ARUBAWIRED-POE-MIB`: `arubaWiredPoePethPsePortTable [AUGMENTS pethPsePortEntry]`, `arubaWiredPoePethPseFourPairPortTable` (802.3bt 4-pair), `arubaWiredPoePethMainPseTable`, `arubaWiredPoePethPseModuleTable` | augment |
| HP ProCurve | `HP-ICF-POE-MIB`: `hpicfPoePethPsePortTable [AUG]`, `hpicfPseFeaturesTable[entPhysicalIndex]`, `hpicfPoePowerSupplyTable[entPhysicalIndex]`, `hpicfPoePethPseOperStateTable` | augment + **entPhysicalIndex-keyed** |
| Comware | `HH3C-POWER-ETH-EXT-MIB::hh3cPsePortTable[pethPsePortGroupIndex, pethPsePortIndex]` | parallel table, same key |
| Huawei | `HUAWEI-POE-MIB`: `hwPoeSlotTable[hwPoeSlotId]`, `hwPoePortTable[hwPoePortIfIndex]`, `hwPoePortJudgeTable`, `hwPoePowerInfoTable` | **ifIndex-keyed** — the odd one out, and the easy one |
| Cisco SMB | `CISCOSB-POE-MIB`: `rlPethPsePortTable[group, port]`, `rlPethMainPseTable`, `rlPethPdPortTable` (the switch as a *powered device*), `rlPhdPoeTable[stackUnit]` | parallel |
| D-Link | `DLINKSW-POE-MIB::dPoeGroupCfgTable` / `dPoeGroupInfoTable[pethMainPseGroupIndex]`; the DGS-1210 consumer line uses `sysPoEPortSettingTable[poeportgroup, poeportid]` instead | two shapes in one vendor |
| Ruckus ICX | `FOUNDRY-POE-MIB`: `snAgentPoePortTable[snAgentPoePortNumber]`, `snAgentPoeModuleTable`, `snAgentPoeUnitTable` | **own port numbering** |
| LANCOM SX | `LCOS-SX-MIB`: `lcsPoeStatusTable[LocalPort]`, `lcsPoeStatusTotalTable[SwitchIndex]`, `lcsPoePowerDelayTable`, `lcsPoeAutoCheckTable` (PoE watchdog — ping a PD, power-cycle if dead) | own |
| EdgeSwitch / Netgear / LANCOM SX | `EdgeSwitch-POWER-ETHERNET-MIB::agentPethPsePortTable [AUG]` | FASTPATH lineage |
| MikroTik | `MIKROTIK-MIB::mtxrPOETable[mtxrPOEInterfaceIndex]` | the whole of MikroTik's PoE surface, one table |
| Cisco IOS-XE | `Cisco-IOS-XE-poe-health-oper`: `port-health/signal-pair-info`, `spare-pair-info`, `poe-meta-data` — 4-pair aware | |

### Traps and pitfalls

- **802.3bt (Type 3/4, up to 90 W, 4-pair)** broke the single-pair model. Aruba
  added a whole parallel table; Cisco added spare-pair oper data. RFC 3621's
  `pethPsePortPowerClassifications` only enumerates classes 0–4. A model that
  stores "class" as a 0–4 enum cannot express Type 4.
- **Three power numbers get conflated**: what the PD requested (LLDP-MED), what
  the PSE allocated (vendor), and what is actually drawn (measured milliwatts,
  vendor). FlowSeer's `PoeFacet` splitting capability / role / status / class /
  allocation / measured draw is the right decomposition and is more careful than
  any single vendor MIB.
- The group↔unit↔stack-member mapping is unspecified. On ProCurve it is
  `entPhysicalIndex`; on Ruckus it is a unit number; on Cisco SMB it is a stack
  unit. There is no portable join.

### FlowSeer status

`net/phy/v1/poe_facet.proto`, `poe_settings`, `poe_priority`, `poe_role`,
`poe_status` — good coverage. Gaps: no 4-pair / 802.3bt type, no chassis power
budget (deliberately deferred per the net-core research), and no representation
of the *join rule* from PSE port to interface.

---

## subinterface

**What it is.** A logical child of a physical or aggregate interface, usually
one 802.1Q VID (or QinQ pair) on a routed port.

### Canonical models

- `openconfig-interfaces`: `interfaces/interface/subinterfaces/subinterface`,
  **key `index` (uint32)**, with `vlan/match/single-tagged/config/vlan-id` from
  `openconfig-vlan` and IP addressing from `openconfig-if-ip`.
- `ietf-interfaces` has no subinterface concept — a subinterface is just another
  interface with its own `name`, related through `lower-layer-if`. This is a
  genuine modelling fork between the two standard families.
- SNMP: `ifStackTable` is the only standard expression, and it is a bare
  higher/lower index pair with no VLAN semantics.

### Vendor mapping

| Family | Surface |
|---|---|
| Comware | `HH3C-IF-EXT-MIB::hh3cRTSubIfTable[parentIfIndex, ordinal]` — an explicit parent/ordinal key, plus `HH3C-VLANTERM-MIB::hh3cVlanTermDot1qTable[ifIndex, vidStart]` and `hh3cVlanTermQinqTable[ifIndex, firstVlan, secondVlanStart]` for the tag match |
| Huawei | `HUAWEI-L3VLAN-MIB::hwSubIfVlanTable[hwSubIfIndex, hwSubIfVlanId]`, `hwSubIfVlanPolicyTable`, and `HUAWEI-QINQ-MIB::hwQinQSubIfVlanStackingTable[ifIndex, ceVlanStart]` |
| Aruba CX / Ruckus ICX | `openconfig-interfaces` subinterfaces |
| Cisco IOS-XE | native model has per-media subinterface lists |
| Everyone else | no subinterface concept exposed (access switches) |

### Notes

Comware and Huawei both model the **VLAN-range match** on the subinterface, not
a single VID — `vidStart`, `secondVlanStart`. OpenConfig's `vlan/match` choice
(`single-tagged`, `double-tagged`, `single-tagged-list`, `single-tagged-range`,
`double-tagged-inner-range`, …) is the general form. FlowSeer's
`subinterface.proto` exists; whether it carries a *match expression* or a single
VID is an unresolved decision, and it maps directly onto the "exact observations
vs match expressions are different contracts" rule in the net-core research.

---

## lag

**What it is.** A bundle of physical links presented as one logical interface.

### Canonical models

- `IEEE8023-LAG-MIB::dot3adAggTable[dot3adAggIndex]`. The detail that catches
  people out: **`dot3adAggIndex` is an `ifIndex`**. The aggregator is itself an
  interface. `dot3adAggPortListTable[dot3adAggIndex]` gives the member list as a
  PortList bitmap; `dot3adAggXTable [AUG]` adds the name.
- `openconfig-if-aggregate`: `interfaces/interface/aggregation` with
  `config/lag-type` (`LACP` | `STATIC`), `min-links`, and `state/member` (a
  leaf-list of member interface names). Members also carry `aggregate-id`
  pointing back.
- `openconfig-lacp` for the protocol state (see [lacp](#lacp)).

### Vendor mapping

| Family | Surface | Key |
|---|---|---|
| IEEE | `IEEE8023-LAG-MIB` | ifIndex-of-aggregator |
| Ruckus ICX | `FOUNDRY-LAG-MIB::fdryLinkAggregationGroupTable[fdryLinkAggregationGroupName]` — **string-keyed**, plus `fdryLinkAggregationGroupPortTable[name, ifIndex]` and `fdryLinkAggregationGroupLacpPortTable[name, ifIndex]` | LAG *name* |
| Cisco SMB | `CISCOSB-TRUNK-MIB`: `rlDot3adAggCreationTable[dot3adAggIndex]`, `rlDot3adAggBalanceTable[aggIndex, forwardType]` (hash policy), `rlDot3adAggLacpMembershipRestrictionsTable` | ifIndex |
| D-Link | `laPortChannelTable[laPortChannelIfIndex]` (DGS-1210 line) | ifIndex |
| Comware | `HH3C-LAG-MIB` | |
| Huawei | `HUAWEI-IF-EXT-MIB::hwTrunkIfTable[hwTrunkIndex]` + `hwTrunkMemTable[hwTrunkIndex, hwTrunkMemifIndex]` — **trunk index ≠ ifIndex** | trunk id |
| Aruba CX | `openconfig-if-aggregate`; plus `ARUBAWIRED-MCLAG-MIB::arubaWiredMclagAggregatorTable` and `ARUBAWIRED-VSX-MIB::arubaWiredVsxAggregatorTable` for the multi-chassis case | |
| FASTPATH family | `agentDot3adAggPortTable[agentDot3adAggPort]` | own port id |
| LANCOM LCOS | `lcsStatusLanInterfaceBundlingLacpAggrsTable[Interface, Aggregator]` | |

### Traps and pitfalls

- **Multi-chassis LAG is a different entity.** MLAG / MC-LAG / VSX / vPC /
  M-LAG / DRNI / E-Trunk / VSF all put one LAG across two chassis, and the
  peer's members are not in this device's `dot3adAggPortListTable`. Aruba,
  Huawei (`HUAWEI-M-LAG-MIB`, `HUAWEI-E-TRUNK-MIB`, `HUAWEI-MC-TRUNK-MIB`),
  Comware (`HH3C-DRNI-MIB`), D-Link (`DLINKSW-MLAG-MIB`) and LANCOM SX
  (`FASTPATH-VPC-MIB`) each ship a private model. See
  [stacking](01-platform.md#stacking) — the two concepts are entangled.
- `dot3adAggPortListTable` returns a **PortList bitmap keyed on
  `dot1dBasePort`**, not `ifIndex`. You need `dot1dBasePortIfIndex` from
  BRIDGE-MIB to translate. This is the single most common LAG-decoding bug.
- Static (non-LACP) bundles are invisible in IEEE8023-LAG-MIB on several
  platforms because the MIB is LACP-shaped.
- Classifier note: the `aggregate` regex pulls in BGP `aggregate-address` and
  OSPF `areaAggregate`. Ignored in this record.

### FlowSeer status

`net/interface/v1/lag_interface.proto` and
`net/switching/v1/aggregation_facet.proto` exist, protocol-independent as the
research doc intended. Missing: the member-list representation and any MLAG
concept.

---

## lacp

**What it is.** The 802.1AX control protocol state on a member link — actor and
partner system ids, keys, port priorities, and the selection/mux state.

### Canonical models

`IEEE8023-LAG-MIB::dot3adAggPortTable[dot3adAggPortIndex]` — actor system
priority / id / admin key / oper key / port priority / port / state, and the
same set of `Partner*` columns. Plus `dot3adAggPortStatsTable` (LACPDUs rx/tx,
marker PDUs, illegal rx) and `dot3adAggPortDebugTable` (rx machine state, mux
state, churn).

`openconfig-lacp`: `lacp/interfaces/interface[name]/members/member[interface]`
with `state/{activity, timeout, synchronization, aggregatable, collecting,
distributing, system-id, oper-key, partner-id, partner-key, port-num,
partner-port-num, counters}`.

### Vendor mapping

Broad and mostly faithful to IEEE. Notable:

| Family | Surface |
|---|---|
| Ruckus ICX | `FOUNDRY-LAG-MIB::fdryLinkAggregationGroupLacpPortTable[groupName, ifIndex]` |
| D-Link | `DLINKSW-LACP-EXT-MIB::dLacpExtGroupTable[channelNo]` |
| Cisco SMB | `rlDot3adAggPortLacpTable[dot3adAggPortIndex]` |
| LANCOM SX (GS2310 line) | `gs2310LACPPortConfigurationTable`, `gs2310LACPSystemStatusTable`, `gs2310LACPStatusTable`, `gs2310LACPStatisticsTable` — a completely separate per-model MIB from the FASTPATH one on other SX models |
| LANCOM LCOS | `lcsStatusLanInterfaceBundlingLacpIfcsTable` / `...PortsTable` / `...AggrsTable` |
| LANCOM LX (APs) | `lcosLXStatusLANLACP[BondName]` — LACP on an access point's uplink |
| HP BladeSystem | `BLADETYPE*-NETWORK-MIB::lacpCurPortCfgTable`, `lacpInfoPortTable`, `lacpStatsTable` |
| Cisco IOS-XE | `Cisco-IOS-XE-lacp-oper` `lacp-member-state/counters` |

### Traps and pitfalls

- **The actor/partner state is a bit field** (activity, timeout, aggregation,
  synchronization, collecting, distributing, defaulted, expired). Reading it as
  an opaque octet loses the diagnosis; `synchronization=0` with `aggregation=1`
  is the signature of a mismatched key, which is the failure you actually want
  to catch.
- Partner system id `00:00:00:00:00:00` means "no partner seen", not "partner
  with a zero MAC". A model with explicit presence handles this; a model with a
  zero-default MAC field does not. This is exactly the Edition 2024 presence
  argument in FlowSeer's net-core research.

### FlowSeer status

No `spec/proto/flowseer/net/protocol/lacp/v1/` package exists yet. The
`AggregationFacet` deliberately excludes actor/partner facts, leaving them for
that future package. Nothing is modelled yet.

---

## tunnel-if

**What it is.** An interface whose frames are encapsulated — GRE, IPv6-in-IPv4,
ISATAP, L2TP, VXLAN, IPsec, CAPWAP, MPLS TE.

### Canonical models

- `TUNNEL-MIB` (RFC 4087): `tunnelIfTable` / `tunnelInetConfigTable`, keyed on
  local+remote address, encapsulation method, and a config id. Covers only IP
  tunnels.
- `openconfig-interfaces` type `tunnel` plus per-technology models
  (`openconfig-mpls-te` `tunnels/tunnel[name]`, `openconfig-evpn` `vxlan`,
  `openconfig-aft-common` `next-hops/next-hop/gre`).

### Vendor mapping (the sprawl is the finding)

| Family | Technologies with their own tables |
|---|---|
| Cisco SMB | `CISCOSB-TUNNEL-MIB::rlTunnelIfTable [AUG tunnelIfEntry]`, ISATAP (`rlTunnelIsatapConfTable`, `rlTunnelIsatapPrlTable`), `CISCOSB-IPv6::rlIpv6TunnelToIPv6DbTable` |
| Comware | GRE (`HH3C-GRE-MIB`), DVPN (`HH3C-DVPN-MIB`, 4 tables), IPsec/IKE monitors (`HH3C-IPSEC-MONITOR-MIB`, `-V2-MIB`, `HH3C-IKE-MONITOR-MIB`), L2TP (`HH3C-L2TP-MIB`), CAPWAP (`hh3cDot11CAPWAPTunnelTable`), VXLAN / NVGRE / EVI |
| Huawei | BRAS GRE/L2TP (`HUAWEI-BRAS-GRE-MIB`, `HUAWEI-BRAS-L2TP-MIB`), `hwBgpVpnTunnelTable`, `hwQueryGreGroupTable`, PWE3, VXLAN, NVO3 |
| ProCurve | `HP-ICF-IPCONFIG::hpicfUdpTunnelTable [AUG tunnelInetConfigEntry]` — a rare faithful augment |
| FASTPATH family | `agentTunnelIPV6Table[agentTunnelID]` + `agentTunnelIPV6PrefixTable` — IPv6 tunnels only |
| LANCOM LCOS | `lcsStatusIpv6Tunnel6in4Table[PeerName]`, WLC CAPWAP tunnels (`lcsStatusWlanMngmtWlcTunnelsWlcConnectionsTable`), HTTP tunnels |
| LANCOM LX | `lcosLXStatusL2TPEndpoints[L2TPEndpoint]`, `lcosLXStatusL2TPEthernet[RemoteEnd, L2TPEndpoint]` |
| Aruba wireless | `WLSX-TUNNELEDNODE-MIB::wlsxTunneledNodeRequestTable[MAC]`, `WLSX-HA-MIB::wlsxHighAvalabilityTunnelTable[haProfileName]` |
| D-Link | `DLINKSW-VLAN-TUNNEL-MIB` (QinQ, not IP tunnels — see [qinq](03-switching.md#qinq)) |

### Assessment

There is no common tunnel model and there will not be one. The tractable
FlowSeer position: model a tunnel *interface* as an interface variant carrying
encapsulation kind plus local/remote endpoint, and push everything protocol
specific into future per-protocol packages. `tunnel_interface.proto` already
takes this shape.

---

## loopback-if

**What it is.** A software interface with a stable address, used as a router id
or management source address.

Almost nothing to model: it is an interface with `ifType = softwareLoopback(24)`
and one or more addresses. The corpus hits are mostly *other* meanings of
"loopback":

- `DOT3-OAM-MIB::dot3OamLoopbackTable[ifIndex]`, Comware
  `hh3cDot3OamLoopbackTable`, Huawei `hwDot3ahEfmLoopbackTable` — **802.3ah
  remote loopback test**, an OAM diagnostic. See [link-oam](#link-oam).
- `HP-ICF-LINKTEST`, D-Link `DLINKSW-LOOPBACK-TEST-MIB` — cable loopback tests.
- LANCOM `lcsSetupTcpIpLoopbackListTable`, `lcsSetupDnsLoopbackAddressesTable` —
  loopback *address* lists for source selection, not interfaces.

Genuine loopback-interface models:
`FASTPATH-LOOPBACK-MIB::agentLoopbackTable[agentLoopbackID]` +
`agentLoopbackIpv6PrefixTable` (shared by EdgeSwitch, Netgear, LANCOM SX),
`FOUNDRY-SN-IP-MIB` / `HP-SN-IP-MIB::snLoopbackIntfConfigTable` (same Foundry
lineage), Cisco IOS-XE native `interface/Loopback`.

FlowSeer has `loopback_interface.proto`. Nothing more is needed; the record
exists mainly to warn that "loopback" in a MIB name usually means a test.

---

## mgmt-if

**What it is.** The out-of-band or designated in-band management interface.

Thin: only two corpus hits.

- `CISCOSB-MNGINF-MIB::rlMngInfListTable[name, priority]` and
  `rlMngInfListInetTable` — Cisco SMB models management access as an *ordered
  list of candidate interfaces*, which is unusual and rather good.
- `HP-ICF-BRIDGE::hpicfBridgeManagementInterfaceConfigTable[ifIndex]`.

Everywhere else the management interface is just an interface, distinguished by
name convention (`mgmt`, `oobm`, `Management0`) or by `ifType`. NetBox models
this as a boolean `mgmt_only` on the interface — the pragmatic answer.

FlowSeer has `management_interface.proto` as a typed variant. Worth confirming
the discriminator is a real observation and not a name-sniffing heuristic; if it
is a heuristic, that belongs in the collector, not the schema.

---

## cable-diag

**What it is.** TDR-style copper cable testing — pair status, fault distance.

Entirely vendor private; no IETF or IEEE model, no OpenConfig model.

| Family | Surface |
|---|---|
| D-Link | `DLINKSW-CABLE-DIAG-MIB::dCableDiagIfTable[ifIndex]` + `dCableDiagResultTable[ifIndex]` |
| Cisco SMB | `CISCOSB-PHY-MIB::rlPhyTestSetTable[ifIndex]` (trigger) + `rlPhyTestGetTable[ifIndex, type]` (result) |
| LANCOM SX | `LCOS-SX-MIB::lcsCableDiagnosticsTable[Port]` |
| LANCOM LCOS | `lcsStatusEthernetPortsCableTestResultsTable[Ifc]` |
| ProCurve | `HP-ICF-LINKTEST::hpicfLinkTestTable[hpicfLinkTestIndex]` |
| Cisco IOS-XE | `Cisco-IOS-XE-cable-diag-oper`: `cable-diag-state[local-interface-name]` → `cable-diag-oper[pair-id]`, triggered by `Cisco-IOS-XE-cable-diag-rpc` |
| Comware / Huawei | present but buried in EPON/ATM diagnostics |

**Shape finding:** every vendor splits this into *trigger* and *result*, because
a TDR test disrupts the link. That makes it an action-plus-result, not state —
FlowSeer's Config/State/Event triad has no natural home for it. It is a
long-running operation with a result document, and modelling it as state would
be wrong. Defer until FlowSeer has an operations concept.

---

## link-oam

**What it is.** 802.3ah Ethernet in the First Mile link OAM — discovery,
loopback, link events, dying gasp.

### Canonical models

`DOT3-OAM-MIB` (RFC 4878): `dot3OamTable[ifIndex]` (admin state, oper status,
mode active/passive, max PDU size, config revision, functions supported),
`dot3OamPeerTable[ifIndex]`, `dot3OamLoopbackTable[ifIndex]`,
`dot3OamStatsTable[ifIndex]`, `dot3OamEventConfigTable[ifIndex]`,
`dot3OamEventLogTable[ifIndex, logIndex]`.

Distinct from **802.1ag / Y.1731 Connectivity Fault Management**, which is a
service-level OAM keyed on maintenance domain / association / MEP — see
`IEEE8021-CFM-MIB`, `IEEE8021-CFM-V2-MIB`, `IEEE8021-DDCFM-MIB` (all present in
the corpus with many revisions), `MEF-SOAM-PM-MIB`, `DLINKSW-CFM-EXT-MIB`,
`HUAWEI-ETHOAM-MIB`. The two are constantly conflated; they are different
entities at different scopes and should not share a message.

### Vendor mapping

Comware `HH3C-EFM-COMMON-MIB` + `HH3C-DOT3-EFM-EPON-MIB`; Huawei
`HUAWEI-ETHOAM-MIB` (`hwDot3ahEfmLoopbackTable`); D-Link CFM extension; Cisco
IOS-XE `Cisco-IOS-XE-ethernet-oam` config grouping plus `ethernet-rpc` for
`eth-lat-loopback`.

Relevance to FlowSeer: low for enterprise switching, high if carrier-handoff or
EPON devices are ever in scope.

---

## udld

**What it is.** Unidirectional link detection — catching a fibre pair where one
strand is dark, which spanning tree cannot see.

No standard. Three incompatible protocols with the same purpose:

| Protocol | Vendors | Surface |
|---|---|---|
| UDLD (Cisco) | Cisco and clones | `EdgeSwitch-UDLD-MIB`, `FASTPATH-UDLD-MIB`, `HP-ICF-UDLD-MIB`, `CISCOSB-UDLD-MIB` |
| DLDP (Huawei/H3C) | Huawei `HUAWEI-DLDP-MIB` (+ `hwDldpPortStatisticsTable`), Comware `HH3C-DLDP-MIB`, `HH3C-DLDP2-MIB` | |
| DULD (D-Link) | `DLINKSW-DULD-MIB` | |

Closely related but distinct: **loop detection** (see
[03-switching](03-switching.md#loop-protect)), which catches a *loop*, not a
unidirectional link, and is a separate set of MIBs again
(`HUAWEI-LOOPDETECT-MIB`, `HH3C-LPBKDT-MIB`, `DLINKSW-LBD-MIB`,
`CISCOSB-LBD-MIB`, `ARUBAWIRED-LOOPPROTECT-MIB`, `HUAWEI-LDT-MIB`).

Modelling note: all three protocols reduce to the same observable —
per-port `{enabled, mode, state ∈ {bidirectional, unidirectional, unknown,
shutdown}}` — with errdisable as the action. One entity with a protocol
discriminator is the right shape; three protocol packages would be waste.

---

## transceiver (see part 1)

Optics live with the hardware component tree in
[01-platform.md](01-platform.md#transceiver) because their identity is
`entPhysicalIndex`-shaped, not `ifIndex`-shaped, on most platforms — which is
itself one of the joins that goes wrong.
