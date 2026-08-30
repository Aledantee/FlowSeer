---
title: Entity records — L0, platform and hardware
date: 2026-08-30
part: 1 of 10
scope: device identity, component tree, optics, stacking, power, environment, firmware, config, licence, resources
---

# L0 — platform and hardware

Ten entities. These sit below the network model FlowSeer has built so far and
none of them are modelled yet, which matters because two of them —
`hw-component` and `stacking` — are the keys that half the other entities join
on.

Index: [system-identity](#system-identity) · [hw-component](#hw-component) ·
[transceiver](#transceiver) · [stacking](#stacking) ·
[power-supply](#power-supply) · [environment](#environment) ·
[firmware-image](#firmware-image) · [config-file](#config-file) ·
[license](#license) · [cpu-memory](#cpu-memory)

---

## system-identity

**What it is.** The device's own answer to "what am I" — name, description,
model, serial, OS version, uptime, contact, location.

### Canonical models

`SNMPv2-MIB` scalars: `sysDescr`, `sysObjectID`, `sysUpTime`, `sysContact`,
`sysName`, `sysLocation`, `sysServices`. Seven objects, universally present, and
the only thing you can rely on before you know what the device is.

- **`sysObjectID`** is the discriminator every collector in existence uses to
  pick a driver. It is an OID under the vendor's enterprise arc, usually
  identifying the exact model. LibreNMS's `os` discovery module and SNMP::Info's
  class dispatch both key on it.
- **`sysDescr`** carries the OS version as free text, in a different shape per
  vendor, and is the only place several vendors put it.
- **`sysServices`** is a bitmask of OSI layers the device claims — SNMP::Info
  exposes it as `layers()`. Useful as a coarse capability hint, unreliable in
  practice.

`ietf-system` (RFC 7317) and `openconfig-system` are the YANG equivalents:
`system/state/{hostname, domain-name, boot-time, current-datetime,
software-version}`.

`ENTITY-MIB` is where the *authoritative* serial and model live — see
[hw-component](#hw-component). `sysDescr` parsing is a fallback, not a source.

### Vendor mapping

| Family | Beyond the standard scalars |
|---|---|
| Aruba CX | `ARUBAWIRED-SYSTEMINFO-MIB::arubaWiredSystemInfoTable[moduleType, moduleName]` — per-module identity |
| Cisco SMB | `CISCOSB-rndMng::rlSysNameTable[source, ifIndex]` — sysName by *source* (static / DHCP-supplied / …), which is a genuinely different idea |
| Comware | `HH3C-PEX-MIB::hh3cPexDeviceInfoTable[entPhysicalIndex]`, `HH3C-COMMON-SYSTEM-MIB`, `HH3C-PRODUCT-ID-MIB` |
| Huawei | `HUAWEI-DEVICE-MIB`, `HUAWEI-DEVICE-EXT-MIB`, `HUAWEI-SYS-MAN-MIB` |
| Ruckus wireless | `RUCKUS-HWINFO-MIB`, `RUCKUS-SWINFO-MIB::ruckusSwRevTable[index]`, `RUCKUS-PRODUCTS-MIB` (sysObjectID registry) |
| Ruckus ICX / HP ProCurve | `FOUNDRY-SN-ROOT-MIB` / `HP-SN-ROOT-MIB` carry the sysObjectID registrations; `FOUNDRY-SN-AGENT-MIB::snChasSerNum` is the serial |
| LANCOM LCOS | `lcsSetupHttpShowDeviceInformationTable` plus a very large `lcsStatus*` tree |
| MikroTik | `MIKROTIK-MIB` scalars only; RouterOS REST `/system/resource` and `/system/routerboard` are far richer |

### Traps and pitfalls

- **The serial in `sysDescr` is not the serial.** On stacked switches
  `sysDescr` names the master; the chassis serials are per-unit in ENTITY-MIB.
  FlowSeer's `Device` concept says the vendor serial is "correlation data the
  service merges sightings on" — with a stack, there are *n* serials for one
  Device, and which one merges is a decision the repo has not recorded.
- `sysUpTime` is a TimeTicks wrapping at ~497 days. `snmpEngineTime` from
  `SNMP-FRAMEWORK-MIB` and `sysUpTimeInstance` do not always agree; several
  vendors reset `sysUpTime` on an SNMP agent restart but not a system reboot.
- `sysName` is often the FQDN, often the short name, and occasionally empty.
  It is not an identifier.

### FlowSeer status

Not modelled. `inventory/v1/device.proto` holds FlowSeer's own identity concept
but there is no observed-system-identity message. The likely home is a future
`flowseer.net.system.v1` (the `api/system/v1` directory is an empty
`.gitkeep`).

---

## hw-component

**What it is.** The physical containment tree: chassis → module/linecard →
port/slot → sensor/fan/PSU, each with a model, serial, hardware revision, and
firmware revision.

### Canonical models

**`ENTITY-MIB` (RFC 6933)** — the most under-used important MIB in networking.

| Table | Key | Carries |
|---|---|---|
| `entPhysicalTable` | `entPhysicalIndex` | `entPhysicalDescr`, `entPhysicalVendorType` (OID), `entPhysicalContainedIn`, `entPhysicalClass` (chassis/backplane/container/powerSupply/fan/sensor/module/port/stack/cpu), `entPhysicalParentRelPos`, `entPhysicalName`, `entPhysicalHardwareRev`, `entPhysicalFirmwareRev`, `entPhysicalSoftwareRev`, `entPhysicalSerialNum`, `entPhysicalMfgName`, `entPhysicalModelName`, `entPhysicalAlias`, `entPhysicalAssetID`, `entPhysicalIsFRU`, `entPhysicalMfgDate`, `entPhysicalUris` |
| `entPhysicalContainsTable` | `entPhysicalIndex, entPhysicalChildIndex` | the containment closure, redundant with `entPhysicalContainedIn` but easier to walk |
| `entLogicalTable` | `entLogicalIndex` | logical entities (VRFs, bridges) and their SNMP contexts |
| `entLPMappingTable` | `entLogicalIndex, entLPPhysicalIndex` | logical→physical |
| `entAliasMappingTable` | `entPhysicalIndex, entAliasLogicalIndexOrZero` | **`entPhysicalIndex` → `ifIndex`** — the join everything needs |
| `entPhysicalSensorTable` (ENTITY-SENSOR-MIB, RFC 3433) | `entPhysicalIndex` | typed sensor values, see [environment](#environment) |
| `entStateTable` (ENTITY-STATE-MIB, RFC 4268) | `entPhysicalIndex` | admin/oper/usage/alarm/standby state per component |

**`openconfig-platform`** is the modern equivalent and is *better shaped*:
`components/component[name]` with `state/{type, id, description, mfg-name,
mfg-date, hardware-version, firmware-version, software-version, serial-no,
part-no, removable, oper-status, empty, parent, temperature, memory,
allocated-power, used-power}` plus typed subtrees from `openconfig-platform-cpu`,
`-fan`, `-linecard`, `-port`, `-psu`, `-transceiver`. Crucially the key is a
**name**, and `parent` is a name reference, so the tree is self-describing
without a second table.

### Vendor mapping

| Family | Surface |
|---|---|
| IETF | `ENTITY-MIB`, `ENTITY-SENSOR-MIB`, `ENTITY-STATE-MIB`, `IANA-ENTITY-MIB` |
| HP ProCurve | `HP-ENTITY-MIB::hpEntPhysicalTable[hpEntPhysicalIndex]` + `hpEntPhysicalContainsTable` — a **parallel tree**, not an augment; plus `HP-ICF-CHASSIS::hpicfSlotTable`, `hpicfEntityTable` |
| Comware | `HH3C-ENTITY-EXT-MIB` (augments ENTITY-MIB with CPU/memory/temperature per entity), `HH3C-ENTITY-VENDORTYPE-OID-MIB` (the `entPhysicalVendorType` registry), `HH3C-ENTRELATION-MIB` |
| Huawei | `HUAWEI-ENTITY-EXTENT-MIB` — the single most important Huawei MIB; carries optical module info, temperature, CPU, memory, all keyed `entPhysicalIndex` |
| Aruba CX | `ARUBAWIRED-CHASSIS-MIB`, `ARUBAWIRED-MODULE-MIB`; OpenConfig `platform` via gNMI |
| Aruba wireless | `WLSX-SYSTEMEXT-MIB::wlsxSysExtCardTable[slot]` |
| Cisco SMB | `CISCOSB-RLINVENTORYENT-MIB::rlInventoryEntTable[unitOrIfindex, unitIfindexID]`, `CISCOSB-DEVICEPARAMS-MIB::rlComponentsInfoTable[stackUnit, imageId, component]`, `CISCOSB-Physicaldescription-MIB` |
| D-Link | `DLINKSW-ENTITY-EXT-MIB` — `dEntityExtEnvTempTable[unitId, index]`, `...FanTable`, `...PowerTable`, `...AirFlowTable`; note **unit-scoped, not entPhysicalIndex-scoped** |
| FASTPATH family (EdgeSwitch / Netgear / LANCOM SX) | `agentInventoryUnitTable[unitNumber]`, `agentInventorySlotTable[unit, slot]`, `agentInventoryCardTypeTable`, `agentInventorySupportedUnitTable`, `agentInventoryStackPortTable` — a **unit/slot/card model that predates and ignores ENTITY-MIB** |
| Ruckus ICX | `FOUNDRY-SN-AGENT-MIB` chassis/module tables |
| LLDP | `LLDP-EXT-MED-MIB::lldpXMedRemInventoryTable` — the *neighbour's* inventory (hardware rev, firmware, serial, manufacturer, model, asset id), learned over the wire. An underrated source: you learn a phone's or AP's inventory without polling it. |

### Traps and pitfalls

- **`entAliasMappingTable` is the only standard `entPhysicalIndex` ↔ `ifIndex`
  bridge, and it is frequently unimplemented.** Without it, transceiver DOM
  (entPhysicalIndex-keyed on Huawei/Cisco) cannot be attached to a port
  (ifIndex-keyed). Vendors that dodge this — Aruba CX
  (`arubaWiredPmXcvrTable[ifIndex]`), D-Link (`dDdmIfInfoTable[ifIndex]`),
  ProCurve (`hpicfXcvrInfoTable[ifIndex]`) — key optics on `ifIndex` directly,
  which is why their optics data is easy and Huawei's is not.
- **`entPhysicalIndex` is not stable across reloads** on many platforms, and
  unlike `ifIndex` there is not even a `LastChange` object to detect it
  (`entLastChangeTime` exists but signals *any* change).
- The FASTPATH lineage's unit/slot/card model is genuinely incompatible with
  ENTITY-MIB, not merely an extension. Three of FlowSeer's vendors are on it.
- `entPhysicalClass` has only 12 values and is regularly abused; a transceiver
  might be `module(9)`, `port(10)`, or `other(1)` depending on vendor.

### Modelling recommendation

This is the single highest-value unmodelled entity in the corpus. Almost
everything downstream — optics, PSUs, fans, sensors, stack members, PoE budgets,
per-module firmware — is keyed on it, and FlowSeer currently has nowhere to put
any of that. The OpenConfig shape (name-keyed, `parent` as a name reference,
typed subtrees) is the better target because it survives the FASTPATH lineage
too, where there is no `entPhysicalIndex` to borrow.

---

## transceiver

**What it is.** The pluggable optic or copper module in a port: presence,
identity (vendor, part, serial, wavelength/reach), and DOM telemetry
(temperature, voltage, bias, tx/rx power) with alarm thresholds.

### Canonical models

There is **no IETF or IEEE transceiver MIB**. The identity data lives in the
module's own EEPROM per SFF-8472 / SFF-8636 / CMIS, and every vendor exposes it
differently.

`openconfig-platform-transceiver` is the only cross-vendor model:
`components/component/transceiver` with `state/{present, form-factor,
connector-type, vendor, vendor-part, vendor-rev, serial-no, date-code,
present, enabled, form-factor-preconf, ethernet-pmd, fault-condition}` and
`physical-channels/channel[index]/state/{output-power, input-power, laser-bias-current,
target-output-power, output-frequency}` — note **per-lane channels**, which is
the detail FlowSeer's research doc correctly identified when it removed a single
wavelength value.

### Vendor mapping

| Family | Surface | Key | Per-lane? |
|---|---|---|---|
| Aruba CX | `ARUBAWIRED-PM-MIB`: `arubaWiredPmXcvrTable[ifIndex]`, `arubaWiredPmXcvrDomTable[ifIndex]`, `arubaWiredPmXcvrLaneDomTable[ifIndex, laneIndex]` | ifIndex | **yes** |
| HP ProCurve | `HP-ICF-TRANSCEIVER-MIB`: `hpicfXcvrInfoTable[ifIndex]`, `hpicfXcvrChannelInfoTable[ifIndex, channel]` | ifIndex | **yes** |
| Comware | `HH3C-TRANSCEIVER-INFO-MIB`: `hh3cTransceiverInfoTable[ifIndex]`, `hh3cTransceiverChannelTable[ifIndex, channel]`, `hh3cTransceiverITUChanTable[ifIndex, itu channel]` (DWDM) | ifIndex | **yes** |
| D-Link | `DLINKSW-DDM-MIB`: `dDdmIfInfoTable[ifIndex]`, `dDdmIfCfgTable[ifIndex]`, `dDdmThresholdCfgTable[ifIndex, component, abnormalLevel]`; `DLINKSW-SFPINFO-MIB::dPortSfpInfoTable[ifIndex]` | ifIndex | no |
| Huawei | `HUAWEI-ENTITY-EXTENT-MIB::hwOpticalModuleInfoTable[entPhysicalIndex]`; `HUAWEI-ENVIRONMENT-MIB::hwOpticalModuleTable[frame, type, chnIndex]` | **entPhysicalIndex** | partially |
| LANCOM SX | `LCOS-SX-MIB::lcsSFPInfoTable[index]`; GS2310 line has its own `gs2310SFPInfoTable` | own | no |
| LANCOM LCOS | `lcsStatusEthernetPortsSfpPortsTable[Port]`, `lcsSetupInterfacesSfpPortsTable[Port]` | own | no |
| Ubiquiti UFiber | `UBNT-UFIBER-MIB::ubntSfpsTable[index]` | own | no |
| HP BladeSystem | `BLADETYPE*-NETWORK-MIB::sfpInfoTable[index]` | own | no |
| Cisco IOS-XE | `Cisco-IOS-XE-native` `transceiver`, `Cisco-IOS-XE-ios-events-oper` `sfp-state-change` / `sfp-support-state` | | |
| MikroTik | `MIKROTIK-MIB::mtxrOpticalTable[index]` | own | no |
| Cisco SMB / Ruckus ICX / FASTPATH family | **no transceiver MIB at all** | | |

### Traps and pitfalls

- **Three of the vendor families have no optics telemetry over SNMP.** Any
  FlowSeer optics feature must declare itself partial from day one.
- Threshold semantics differ: D-Link models thresholds as a *configurable*
  table (`dDdmThresholdCfgTable`) while most vendors report the module's own
  EEPROM thresholds. A model that mixes them will compare an operator setting to
  a hardware limit.
- Multi-lane (QSFP 4×, QSFP-DD 8×) and coherent modules make "the" tx power
  meaningless. Aruba, ProCurve, and Comware all ship a per-lane table; a model
  without a lane dimension cannot represent them.
- Form factor and connector are separate facts from medium. FlowSeer's
  `EthernetMedium` (copper/fiber/backplane/other) is deliberately coarse; the
  research doc's decision to keep `TransceiverFacet` to presence and identity is
  consistent with what four of ten vendors can actually answer.

### FlowSeer status

`net/phy/v1/transceiver_facet.proto` exists, deliberately shallow. Per-lane DOM
is explicitly deferred. Both calls hold up against the corpus.

---

## stacking

**What it is.** Several physical chassis presented as one managed device — and
the closely related but distinct case of two chassis presenting one *forwarding*
identity without merging management.

This entity has the worst vendor divergence in the whole atlas: **twelve
different names for two different ideas.**

### The two ideas

1. **Management stacking** — one control plane, one IP, one config, n units.
   Ports are numbered `unit/slot/port`. Cisco StackWise, Aruba VSF, Ruckus ICX
   stacking, D-Link stacking, FASTPATH units, HP ProCurve stacking, Comware IRF,
   Huawei iStack/CSS.
2. **Multi-chassis link aggregation** — two control planes, one LAG towards the
   downstream device. Aruba VSX, Cisco vPC, Huawei M-LAG / E-Trunk, Comware
   DRNI, D-Link MLAG, FASTPATH VPC, and the IEEE standard version, **DRNI
   (802.1AX-2014)**.

They are constantly conflated, including by vendors. From FlowSeer's `Device`
concept the difference is decisive: case 1 is *one* Device with n chassis
serials; case 2 is *two* Devices with a relationship.

### Canonical models

- **`IEEE8023-LAG-MIB::dot3adDrniTable[dot3adDrniIndex]`** plus
  `dot3adDrniConvAdminGatewayTable`, `dot3adDrniIPLEncapMapTable`,
  `dot3adDrniNetEncapMapTable` — the standard MC-LAG model. Vendor support is
  close to nil; everyone shipped a private model first.
- `IF-MIB::ifStackTable` — the only standard hierarchy statement, and not about
  chassis at all.
- No standard management-stacking model exists. `ENTITY-MIB`'s
  `entPhysicalClass = stack(11)` is the only nod to it.

### Vendor mapping

| Family | Management stacking | Multi-chassis |
|---|---|---|
| Aruba CX | `ARUBAWIRED-VSF-MIB::arubaWiredVsfMemberTable[memberIndex]`, `arubaWiredVsfLinkTable[memberId, linkId]`; `ARUBAWIRED-VSFv2-MIB` (a second generation, incompatible) | `ARUBAWIRED-VSX-MIB::arubaWiredVsxAggregatorTable`, `ARUBAWIRED-MCLAG-MIB::arubaWiredMclagAggregatorTable` |
| Cisco SMB | `CISCOSB-STACK-MIB::rlStackActiveUnitIdTable`, `CISCOSB-Physicaldescription-MIB::rlPhdStackTable[unit]`, `rlPhdStackOrderTable[currentUnitPosition]`, `rlPhdUnitStackPortTable[stackUnit, ifIndex]` | — |
| Cisco IOS-XE | `Cisco-IOS-XE-interfaces` `stackwise-virtual` | same |
| Comware | `HH3C-STACK-MIB`, `HH3C-HGMP-MIB::hh3chgmpGrpMemberTable[deviceId]` (cluster management) | `HH3C-DRNI-MIB::hh3cDrniIppTable[ippNumber]`, `hh3cDrniDrPortTable[drGroupId]`, `hh3cDrniPortTable[ifIndex]` |
| Huawei | `HUAWEI-STACK-MIB`, `HUAWEI-HGMP-MIB::hgmpGrpMemberTable[deviceId]`, `hgmpMemberResetTable[MAC]` | `HUAWEI-M-LAG-MIB`, `HUAWEI-E-TRUNK-MIB::hwETrunkTable[id]` + `hwETrunkMemberTable[parentId, type, id]`, `HUAWEI-MC-TRUNK-MIB`, `HUAWEI-SUPERLAG-MIB` |
| HP ProCurve | `HP-ICF-STACK::hpicfStackBoxTable[entPhysicalIndex]`, `hpicfStackAgentTable[entPhysicalIndex]`; also `HP-STACK-MIB`, `HP-SwitchStack-MIB` (three generations) | — |
| D-Link | `DLINKSW-STACK-MIB::dStackUnitInfoTable[boxId]`, `SINGLE-IP-MIB` (single-IP clustering) | `DLINKSW-MLAG-MIB::dMlagGroupTable[groupNo]`, `dMlagAggPortTable[groupNo, deviceID, portIndex]` |
| Ruckus ICX | `FOUNDRY-SN-STACKING-MIB::snStackingConfigUnitTable[index]`, `snStackingOperUnitTable[index]`, `snStackingConfigStackTrunkTable[unit, port1, port2]`; `FOUNDRY-SN-AGENT-MIB::snStackSecSwitchTable` | — |
| FASTPATH family | `agentInventoryUnitTable[unitNumber]`, `agentInventoryStackPortTable` | `FASTPATH-VPC-MIB::agentVpcConfigTable[vpcId]`, `agentVpcDomainConfigTable`, `agentPeerConfigTable`, `agentVpcSelfMemberStatusTable[vpcId, intfId]` (LANCOM SX only in this corpus) |
| Ubiquiti EdgeSwitch | `agentInventoryStackPortTable` | — |

### Traps and pitfalls

- **Port naming changes when a stack forms.** A standalone switch's
  `GigabitEthernet0/1` becomes `GigabitEthernet1/0/1`. Every stored interface
  reference breaks. This is the strongest argument against interface-name keys.
- **A stack has n serials and n MACs.** FlowSeer's Device merges sightings on
  serial and chassis MAC; a stack member swap changes both without changing the
  Device. The rule for which serial identifies a stacked Device is unwritten and
  needs to be.
- Aruba ships VSF *and* VSFv2 as separate incompatible MIBs; HP ProCurve ships
  three stack MIBs from three eras. Version detection is required, not optional.
- Cluster management (HGMP on Huawei/H3C, Single-IP on D-Link) is a *third*
  idea — one device proxies SNMP for its neighbours. It looks like stacking to a
  collector and is not.

---

## power-supply

**What it is.** PSU presence, model, capacity, input feed, and operational
status; and at chassis level the power budget PoE draws from.

No standard MIB. `ENTITY-MIB`'s `entPhysicalClass = powerSupply(6)` plus
`ENTITY-STATE-MIB`'s `entStateOper` is the closest thing, and
`openconfig-platform-psu` (`components/component/power-supply/state/{capacity,
input-current, input-voltage, output-current, output-voltage, output-power}`)
is the modern answer.

| Family | Surface |
|---|---|
| Aruba CX | `ARUBAWIRED-POWERSUPPLY-MIB::arubaWiredPowerSupplyTable[groupIndex, slotIndex]`; `ARUBAWIRED-POWER-STAT-MIB::arubaWiredPowerStatTable[group, type, slot]` |
| HP ProCurve | `HP-ICF-CHASSIS::hpicfPowerSupplyTable[slotNum]`, `hpicfPsTable[bayNum]`, `POWERSUPPLY-MIB`; `HP-ICF-POE-MIB::hpicfPoePowerSupplyTable[entPhysicalIndex]` ties PSUs to the PoE budget |
| Comware | `HH3C-LswDEVM-MIB::hh3cdevMPowerStatusTable[powerNum]`, `HH3C-REDUNDANCY-POWER-MIB` |
| Huawei | `HUAWEI-POWER-MIB`, `HWMUSA-DEV-MIB::hwMusaFramePowerSupplyTable[frame, inputMode, id]` |
| Cisco SMB | `CISCOSB-rndApplications::rsPowerSupplyRedundacyTable[reNumber]`, `CISCOSB-HWENVIROMENT::rlEnvMonSupplyStatusTable` |
| LANCOM SX | `LCOS-SX-GENERAL-MIB::lcsMonitoringPSUTable[unitIndex, psuIndex]` |
| Ubiquiti | `UBNT-EdgeMAX-MIB` / `UBNT-UFIBER-MIB::ubntPsuTable[index]` |
| Aruba wireless | `WLSX-SYSTEMEXT-MIB::wlsxSysExtPowerSupplyTable[index]` |
| D-Link | `DLINKSW-ENTITY-EXT-MIB::dEntityExtEnvPowerTable[unitId, index]`, `EQUIPMENT-MIB` |

Note the key pattern: **group+slot, unit+index, bay, or entPhysicalIndex** —
four shapes across nine vendors, and only ProCurve joins PSUs to PoE budget.

FlowSeer's net-core research deferred chassis PoE budgets. That deferral is
correct but the reason is worth recording: the budget lives on the PSU, and the
PSU has no home in the model yet.

---

## environment

**What it is.** Temperature, fan speed, voltage, current, humidity, airflow.

### Canonical models

**`ENTITY-SENSOR-MIB` (RFC 3433)** — `entPhySensorTable[entPhysicalIndex]` with
`entPhySensorType` (other/unknown/voltsAC/voltsDC/amperes/watts/hertz/celsius/
percentRH/rpm/cmm/truthvalue), `entPhySensorScale` (yocto..yotta),
`entPhySensorPrecision`, `entPhySensorValue`, `entPhySensorOperStatus`,
`entPhySensorUnitsDisplay`. This is the *right* model: a typed, scaled,
self-describing sensor keyed on the component it sits in. It is also
under-implemented.

`openconfig-platform` puts `temperature/{instant, avg, min, max, alarm-status,
alarm-threshold}` directly on a component, and `openconfig-platform-fan` gives
`fan/state/speed`.

### Vendor mapping

Nearly every vendor ships a private per-sensor-class table instead:

| Family | Fans | Temperature |
|---|---|---|
| Aruba CX | `ARUBAWIRED-FAN-MIB::arubaWiredFanTable[group, tray, slot]`, `ARUBAWIRED-FANTRAY-MIB` | `ARUBAWIRED-TEMPSENSOR-MIB::arubaWiredTempSensorTable[group, slotType, slot, index]` |
| Cisco SMB | `CISCOSB-HWENVIROMENT::rlEnvMonFanStatusTable`, `rlEnvFanDataTable[stackUnit]` | `CISCOSB-SENSORENTMIB::rlEntPhySensorTable [AUGMENTS entPhySensorEntry]` — a rare faithful augment |
| D-Link | `dEntityExtEnvFanTable[unitId, index]`, `EQUIPMENT-MIB::swFanTable[unit, fanID]` | `dEntityExtEnvTempTable[unitId, index]`, `swTemperatureTable[unit]` |
| Comware | `HH3C-LswDEVM-MIB::hh3cdevMFanStatusTable[fanNum]`, `hh3credundancyFanTable` | `hh3cdevMSlotEnvironmentTable[frame, slot, type]` |
| Huawei | `HUAWEI-ENVIRONMENT-MIB::hwFanStatusTable[slot, sn]` | `hwTemperatureThresholdTable[slot, i2cId, addr, channel]` — an *I²C-addressed* key, the most hardware-leaky key in the corpus |
| HP ProCurve | `FAN-MIB`, `HP-ICF-CHASSIS::hpicfFanTable[index]` | `HP-ICF-BASIC::hpicfSensorTable[index]` |
| Ruckus ICX | `FOUNDRY-SN-AGENT-MIB::snChasFanTable[index]`, `snChasFan2Table[unit, index]` (the `2` table is the stack-aware replacement) | `snChasActualTemperature`, `snAgentTempTable` |
| FASTPATH family | `boxServicesFansTable[unit, index]`, `boxServicesTempSensorsTable[unit, index]` — byte-identical in Netgear, EdgeSwitch, and LANCOM SX. LANCOM additionally ships its own `lcsMonitoringFansTable[unitIndex, fanIndex]` / `lcsMonitoringTempSensorsTable[unitIndex, sensorIndex]` in `LCOS-SX-GENERAL-MIB`, so **both are present on the same device** and can disagree | same |
| LANCOM LCOS | `lcsStatusTemperatureMonitorExtremesTable[type]`, plus `lcsStatusWlanThermalMitigationTable[Ifc]` — thermal throttling of a radio | |
| Ubiquiti | `ubntFansTable[index]` | |
| Aruba wireless | `WLSX-SYSTEMEXT-MIB::wlsxSysExtFanTable[index]` | |

### Traps and pitfalls

- **Units and scale are per-vendor.** Some report tenths of a degree, some
  whole degrees, some milli-volts. `ENTITY-SENSOR-MIB` solved this with
  `entPhySensorScale`/`Precision`; the vendors that ignore it leave the
  collector to hard-code a divisor per model. LibreNMS's entire
  `includes/discovery/sensors/$class/$os.inc.php` dispatch exists for this.
- Fan "status" is a per-vendor enum with values like `ok/failed/removed/
  notPresent/unsupported/other`, never the same set twice.
- Threshold vs. reading vs. alarm-state are three different facts and most
  vendors ship only one or two.

### Modelling recommendation

Follow ENTITY-SENSOR-MIB's shape, not the vendors': one `Sensor` row keyed on
the component, carrying a typed value with an explicit unit and scale, plus an
optional threshold set. The alternative — a message per sensor class —
reproduces the vendor mess inside FlowSeer.

---

## firmware-image

**What it is.** The software images on the device: which are present, which
boots, which is running, and the state of an upgrade.

No standard model. `entPhysicalSoftwareRev`/`FirmwareRev` in ENTITY-MIB give the
running version per component and nothing else.

| Family | Surface |
|---|---|
| Cisco SMB | `CISCOSB-DEVICEPARAMS-MIB::rndImageInfoTable[stackUnitNumber]` — per-unit active/inactive image; `CISCOSB-WBA-MIB::rlWBAImageTable` |
| D-Link | `AGENT-GENERAL-MIB::swMultiImageInfoTable[imageID]` + `swMultiImageCtrlTable[imageID]`; `DLINKSW-SYSTEM-FILE-MIB::dsfBootImageTable[unitId, index]` |
| ProCurve | `HP-ICF-DOWNLOAD::hpicfDownloadTable[index]`, `hpicfDownloadLogTable`, `hpicfDownloadAutoTftpTable`, `hpicfDownloadInetTable` — an upgrade *operation* model, four generations of it |
| Comware | `HH3C-FLASH-MAN-MIB::hh3cFlashTable[index]` + `hh3cFlhChipTable`, `HH3C-ISSU-MIB` (in-service upgrade), `HH3C-SYS-MAN-MIB` |
| Huawei | `HUAWEI-FLASH-MAN-MIB::hwFlashTable[index]` + `hwFlhChipTable` (same shape as Comware — shared H3C ancestry), `HUAWEI-STACK-MIB` for per-member versions |
| LANCOM LCOS | `lcsFirmwareVersionTableTable[Ifc]`, `lcsFirmwareTableFirmsafeTable[Position]` — LCOS's dual-image "FirmSafe" |
| Aruba CX | `ARUBAWIRED-SWITCH-IMAGE-MIB` |
| Cisco IOS-XE | `Cisco-IOS-XE-install-oper`, plus the smart-licensing modules |

**Pattern:** everybody has a *dual-image* concept (primary/secondary, A/B,
FirmSafe) and nobody names it the same. On stacked devices there is one image
set per member and version skew is a real fault condition — Cisco SMB and
Huawei both model it per unit; most others do not.

---

## config-file

**What it is.** Running vs startup configuration, its last-change time, and the
backup/restore operation.

| Family | Surface |
|---|---|
| Comware | `HH3C-CONFIG-MAN-MIB`: `hh3cCfgLogTable[index]` (who changed what, when), `hh3cCfgOperateTable[index]` (trigger a save/load), `hh3cCfgOperateResultTable`, `hh3cCfgExecuteResultTable` — the most complete config-management MIB in the corpus |
| Huawei | `HUAWEI-CONFIG-MAN-MIB` (same lineage), `HUAWEI-LOAD-BACKUP-MIB` |
| Cisco SMB | `CISCOSB-COPY-MIB`: `rlCopyTable[index]`, `rlCopyHistoryTable`, `rlCopyMessagesTable[copyIndex, messageIndex]` — a job model with a message log |
| ProCurve | `HP-ICF-DOWNLOAD::hpicfDownloadAutoTftpTable` |
| D-Link | `AGENT-GENERAL-MIB::agentFTPFileTable`, `DLINKSW-SDCARDMGMT-MIB`, `ZTP-MIB` |
| LANCOM SX | `LCOS-SX-MIB::lcsConfigFileTable[index]` |
| LANCOM LCOS | `lcsStatusConfigDownloadStatusLastOperationsTable[index]` |
| Cisco enterprise | `CISCO-CONFIG-COPY-MIB` / `CISCO-CONFIG-MAN-MIB` — **absent from this corpus**, see [gaps](../04-gaps-and-recommendations.md) |

**Shape finding, same as cable-diag:** every vendor models this as a *job* with
an id, a status, and a result log — not as state. Half of the "SNMP write" use
cases in a real NMS are exactly these job tables (`rlCopyTable`,
`hh3cCfgOperateTable`). FlowSeer has no operation/job concept, and this is the
second entity that needs one.

Also relevant: `ops/switch-restore/` already exists in this repo as a
backup/restore tool for the lab switches, so the operational need is real and
already being served outside the model.

---

## license

**What it is.** Feature entitlements, capacity limits, expiry.

| Family | Surface |
|---|---|
| Aruba wireless | `WLSX-SWITCH-MIB::wlsxSwitchLicenseTable[licenseIndex]`, `WLSX-SYSTEMEXT-MIB::wlsxSysExtSwitchLicenseTable` — AP-count licensing on a controller is a real capacity constraint |
| Comware | `HH3C-LICENSE-MIB` |
| Huawei | `HUAWEI-GTL-MIB` (Guided Trial Licensing) |
| Cisco IOS-XE | `cisco-smart-license.yang`, `cisco-smart-license-errors.yang`, `CISCO-LICENSE-MGMT-MIB::clmgmtLicenseDeviceInfoTable[entPhysicalIndex]` |
| OpenConfig | `openconfig-license` |
| Comware/Huawei WLAN | `HH3C-DOT11-LIC-MIB` — AP licence counts |

Relevance to FlowSeer: low as telemetry, non-trivial as a *constraint* — a
controller that has run out of AP licences will refuse to adopt an AP, and that
looks like a discovery failure. Worth a note in the wireless domain rather than
a model of its own.

---

## cpu-memory

**What it is.** Control-plane CPU utilisation, memory usage, storage/flash
usage, per-process resources.

### Canonical models

- `HOST-RESOURCES-MIB` (RFC 2790): `hrProcessorTable[hrDeviceIndex]`
  (`hrProcessorLoad`, a 1-minute average), `hrStorageTable[hrStorageIndex]`,
  `hrSWRunTable`. Ubiquitous on Linux-based devices, absent on most switch
  ASICs' firmware.
- `UCD-SNMP-MIB` (`laLoad`, `memTotalReal`, …) and `NET-SNMP-*` on anything
  running net-snmp — which includes UniFi, EdgeMAX, and MikroTik.
- `openconfig-platform-cpu` (`components/component/cpu/utilization`),
  `openconfig-procmon` (`system/processes/process[pid]`),
  `openconfig-system` `memory`.

### Vendor mapping

| Family | Surface |
|---|---|
| Comware | `HH3C-ENTITY-EXT-MIB` — CPU and memory per `entPhysicalIndex`, the right shape |
| Huawei | `HUAWEI-CPU-MIB`, `HUAWEI-MEMORY-MIB`, `HUAWEI-ENTITY-EXTENT-MIB`, `HUAWEI-RES-MON-MIB` |
| Cisco SMB | `CISCOSB-CPU-COUNTERS-MIB` |
| ProCurve | `HP-MEMPROC-MIB` |
| Ruckus ICX | `FOUNDRY-SN-AGENT-MIB` (`snAgGblCpuUtil*`, `snAgentCpuUtilTable`) |
| FASTPATH family | `agentSwitchCpuProcessTable` in the switching MIB |
| Ubiquiti UniFi | `FROGFOOT-RESOURCES-MIB` — a third-party Linux resources MIB Ubiquiti shipped, plus `UBNT-MIB` |
| MikroTik | `MIKROTIK-MIB::mtxrSystem`, plus HOST-RESOURCES |
| D-Link | `DLINKSW-SRM-MIB` (switch resource manager — TCAM/ASIC table utilisation, a *different and more interesting* resource), `DLINKSW-CPU-PROTECT-MIB` |

**The interesting one:** D-Link's `DLINKSW-SRM-MIB` and Huawei's
`HUAWEI-RES-MON-MIB` report *forwarding-table* utilisation (how full the FDB,
ARP, ACL TCAM are). That is a far better operational signal than CPU load on a
switch, and almost nothing collects it.
