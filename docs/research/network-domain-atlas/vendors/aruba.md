---
title: Vendor dossier — Aruba (HPE Networking)
date: 2026-08-30
scope: AOS-CX switches, ArubaOS wireless controllers, Instant APs, ClearPass
---

# Aruba

Two unrelated platforms under one brand, plus a policy server. Enterprise OIDs
**14823** (Aruba Networks, the wireless side) and **47196** (HPE Aruba
Networking, the AOS-CX side).

---

## AOS-CX (wired)

**Products:** CX 6000/8000/9000/10000 switches.
**Lineage:** clean-sheet. Shares **zero** table names with HP ProCurve's
`HP-ICF-*` set — AOS-CX is not an evolution of ProCurve, it is a replacement.
**Planes, in order of authority:**

1. **REST API v10.0x** — the primary interface. Token auth, JSON, backed by an
   OVSDB-style configuration database, so REST coverage is essentially total.
   The Swagger definition is generated and served *per switch* (Web UI →
   Settings → "V10.04 API"); HPE publishes no standalone OpenAPI file.
2. **gNMI with OpenConfig** — 19 modules vendored in
   `spec/yang/aruba/cx/aoscx-yang/10.17/openconfig/v5_0_0/`: interfaces
   (+ aggregate, ethernet-ext, poe), platform (+ cpu, fan, psu, types), system,
   types, third-party IETF, and `hpe-anw-cx-openconfig-deviations.yang`.
3. **SNMP** — 35 `ARUBAWIRED-*` MIBs, deliberately thin.

**Corpus:** `spec/mib/aruba/cx/` (35 modules),
`spec/yang/aruba/cx/aoscx-yang/10.17/` (19 modules). Note the OpenConfig set is
only 19 files because the rest of the import closure lives in
`spec/yang/openconfig/` (90 files) — the two directories are one bundle.

### Shape of the MIB set

Very disciplined: one module per feature, `AUGMENTS` where a standard exists,
and consistent `[groupIndex, slotIndex]`-style hardware keys.

| Area | Modules |
|---|---|
| Hardware | `-CHASSIS-MIB`, `-MODULE-MIB`, `-SYSTEMINFO-MIB`, `-FAN-MIB`, `-FANTRAY-MIB`, `-TEMPSENSOR-MIB`, `-POWERSUPPLY-MIB`, `-POWER-STAT-MIB`, `-LED-LOCATOR-MIB` |
| Interfaces | `-INTERFACE-MIB`, `-PM-MIB` (**optics with per-lane DOM**: `arubaWiredPmXcvrLaneDomTable[ifIndex, laneIndex]`), `-POE-MIB` (**802.3bt-aware**: `arubaWiredPoePethPseFourPairPortTable`) |
| L2 | `-PORTVLAN-MIB`, `-MSTP-MIB`, `-RPVST-MIB`, `-MVRP-MIB`, `-LOOPPROTECT-MIB`, `-MACNOTIFY-MIB`, `-PROVIDER-BRIDGE-MIB`, `-MGMD-SNOOPING-MIB` |
| Redundancy | `-VSF-MIB`, `-VSFv2-MIB`, `-VSX-MIB`, `-MCLAG-MIB` |
| Security | `-PORT-ACCESS-MIB`, `-PORTSECURITY-MIB`, `-AAA-MIB` |
| Other | `-LLDP-MIB`, `-CONFIG-MIB`, `-SWITCH-IMAGE-MIB`, `-MDNS-MIB`, `-DIST-SERVICES-MIB`, `-CIPT-MIB`, `-NETWORKING-OID` |

### Where AOS-CX is the reference implementation

- **Optics.** `arubaWiredPmXcvrTable[ifIndex]` + `arubaWiredPmXcvrDomTable[ifIndex]`
  + `arubaWiredPmXcvrLaneDomTable[ifIndex, laneIndex]` — ifIndex-keyed *and*
  per-lane. Nobody else in the corpus does both.
- **PoE 802.3bt.** A dedicated four-pair table rather than overloading the
  RFC 3621 class enum.
- **Port access.** `arubaWiredPortAccessClientTable[portName, mac]` +
  `arubaWiredPortAccessRoleTable[roleName]` — the authenticated-client-with-role
  model that [dot1x](../entities/07-security.md#dot1x) identifies as the highest
  value unmodelled entity.
- **AAA with VRF in the key.** `arubaWiredRadiusServerTable[vrfName, address,
  port, portType]` — the only vendor here that models management-plane VRF
  correctly in SNMP.

### Traps

- **VSF and VSFv2 are separate incompatible MIBs** on the same product line.
  Detect which is populated; do not assume.
- The MIB set has no VLAN *database* table — `arubaWiredPortVlanMemberTable` is
  membership only. VLANs come from `Q-BRIDGE-MIB` or REST.
- `ARUBAWIRED-NETWORKING-OID` is the sysObjectID registry; you need it to map a
  device to a model.

---

## ArubaOS wireless

**Products:** Mobility Controllers / Mobility Conductor and the campus APs they
manage.
**Plane:** SNMP (`WLSX-*`) plus the controller's own CLI/API; Aruba Central for
cloud-managed deployments (no spec vendored).
**Corpus:** 25 modules in `spec/mib/aruba/wireless/`.

| Module | Covers |
|---|---|
| `WLSX-SWITCH-MIB` | the controller: `wlsxSwitchListTable[switchIPAddress]` (cluster), `wlsxSwitchLicenseTable[licenseIndex]` |
| `WLSX-SYSTEMEXT-MIB` | chassis: cards, fans, PSUs, licences, CPU, storage |
| `WLSX-WLAN-MIB` | the core wireless model: `wlsxWlanAPTable[apMac]`, `wlsxWlanAPGroupTable[group]`, `wlsxWlanRadioTable[apMac, radioNumber]`, `wlsxWlanAPBssidTable[apMac, radioNumber, bssid]` |
| `WLSX-USER-MIB` / `-USER6-MIB` | associated users **with authenticated identity** (v4 and v6) |
| `WLSX-MON-MIB` | air monitoring: `wlsxMonAPStatsTable[monitorMac, monitorRadio, monitoredBssid]`, `wlsxMonStationStatsTable[..., monitoredStaMac]` — observer-keyed |
| `WLSX-RS-MIB` | rogue detection, and `wlsxLldpNeighborTable[apMac, remotePortNumber, index]` — **LLDP neighbours of APs, reported by the controller** |
| `WLSX-MESH-MIB` | `wlsxMeshNodeTable[apMac]` |
| `WLSX-MOBILITY-MIB`, `WLSX-HA-MIB` | roaming and controller HA |
| `WLSX-IFEXT-MIB` | controller interfaces, keyed `[slot, port]` — **a second port namespace** |
| `WLSX-SNR-MIB`, `WLSX-STATS-MIB` | RF and traffic statistics |
| `WLSX-AUTH-MIB`, `WLSX-CTS-MIB`, `WLSX-ESI-MIB`, `WLSX-VOICE-MIB`, `WLSX-TUNNELEDNODE-MIB` | auth, TrustSec, external services, voice, tunnelled node |
| `WLSR-AP-MIB` | the *AP-resident* view: `wlsrChannelStatsTable[channel]` and per-frame-type airtime breakdowns |
| `AI-AP-MIB` | **Aruba Instant** — the controller-less architecture: `aiAccessPointTable[apMac]`, `aiRadioTable[apMac, radioIndex]`, `aiWlanSSIDTable[ssidIndex]`, `aiClientTable[clientMac]`, `aiMeshTable[index]`, `aiVoiceClientTable[clientMac]` |
| `ARUBA-MIB`, `ARUBA-TC`, `ARUBA-MGMT-MIB` | root, textual conventions, management |
| `CPPM-MIB` | **ClearPass** policy server: `radiusServerTable[hostname]`, `radiusServerAuthTable[sourceIdx]`, `policyServerAutzTable[sourceIdx]`, `tacacsAuthTable[hostname]`, `webAuthProtoTable[protocolIdx]` |

### Notes

- `AI-AP-MIB` and the `WLSX-*` set model the same entities with different keys
  and different names. A deployment is one or the other, never both, and the
  discriminator is `sysObjectID`.
- `wlsxLldpNeighborTable` is unusual and useful: it lets you learn what an AP is
  plugged into without polling the switch.
- The observer-keyed monitoring tables (`wlsxMon*`) produce one row per
  (observing radio, observed device). Treat them as *sightings*, not entities —
  which is precisely FlowSeer's Provenance concept applied to RF.

---

## Cross-cutting

HPE now sells all of this — AOS-CX, AOS-S (ProCurve), ArubaOS wireless, and
Comware — under one brand, and they share nothing technically. See
[hpe.md](hpe.md) for the other two. When a customer says "HPE switches", the
first question is which of four platforms.
