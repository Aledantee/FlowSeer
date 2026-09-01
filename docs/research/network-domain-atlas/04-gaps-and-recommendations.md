---
title: Gaps and recommendations
date: 2026-08-30
scope: FlowSeer coverage against the 101 entities, corpus gaps, and what the research says to do next
---

# Gaps and recommendations

Three kinds of gap: what FlowSeer does not model yet, what the `spec/` corpus is
missing, and the design questions the corpus says must be settled before the
current packages stabilise.

---

## 1. FlowSeer coverage today

Measured against the 101 entities, from `spec/proto/flowseer/`.

### Modelled (18 entities)

| Entity | Package |
|---|---|
| interface, if-counters | `net/interface/v1` (+ typed variants for subinterface, lag, loopback, management, tunnel, vlan, physical, other) |
| ethernet-phy, fec, poe, transceiver | `net/phy/v1` |
| vlan, vlan-membership, fdb | `net/switching/v1` |
| lag (partial) | `net/switching/v1/aggregation_facet` |
| ip-address, arp-nd | `net/ip/v1` |
| lldp | `net/protocol/lldp/v1` |
| — (substrate) | `net/packet/v1`, `net/addr/v1` |

### Declared but empty

`net/protocol/lacp/v1`, `net/protocol/stp/v1`, `net/wlan/v1`, and the
`api/{firewall,interface,routing,switching,system,wireless}/v1` service
directories — all `.gitkeep`.

### Not modelled (83 entities)

Everything in [part 1](entities/01-platform.md) (all 10 platform entities), most
of [part 3](entities/03-switching.md) (18 of 21), all of
[parts 5–10](entities/05-routing.md).

### The five that would pay for themselves first

Ranked by (operational value × vendor support × distance from what exists):

1. **`hw-component`** — [01-platform](entities/01-platform.md#hw-component).
   Nothing else can be modelled properly without it: optics, PSUs, fans,
   sensors, stack members, per-module firmware and PoE budgets all key on it,
   and FlowSeer has nowhere to put any of them. `openconfig-platform`'s
   name-keyed, `parent`-referencing shape is the target because it also fits the
   FASTPATH lineage, which has no `entPhysicalIndex` to borrow.
2. **`stp`** — [03-switching](entities/03-switching.md#stp). Near-universal
   vendor support, an empty package already reserved, and the root/designated
   bridge IDs let you infer topology on networks with no discovery protocol.
3. **`dot1x` sessions** — [07-security](entities/07-security.md#dot1x). The only
   entity in the corpus that attaches an *identity* to an endpoint. Every vendor
   here exposes it; no comparable open system models it.
4. **`environment` + `power-supply`** —
   [01-platform](entities/01-platform.md#environment). The cheapest fault
   signals, present on every device, and `ENTITY-SENSOR-MIB`'s typed/scaled
   sensor is the right shape to copy rather than the twelve vendor variants.
5. **`syslog-events` / alarms** —
   [09-ops](entities/09-ops.md#syslog-events). FlowSeer's triad has an Event
   axis with nothing on it, and the `ALARM-MIB` model/active split plus
   `NOTIFICATION-LOG-MIB` replay are the two standards worth adopting.

---

## 2. Corpus gaps

### Cisco enterprise MIBs — documented but absent

`spec/mib/README.md` describes `spec/mib/cisco/enterprise/` as an "official
cisco/cisco-mibs clone (v1, v2, traps, ucs, …) — IOS/IOS-XE, AIRESPACE/LWAPP,
Catalyst". **The directory does not exist.** The only Cisco MIBs present are the
116 `CISCOSB-*` modules.

The sharpest consequence: `spec/yang/cisco/iosxe/SOURCES.md` states that
"AireOS WLCs and autonomous IOS APs have **no** YANG/RESTCONF support — they
remain SNMP-only (see `spec/mib/cisco/`)", and the SNMP half is missing. There
is currently no way to read a Cisco AireOS controller from anything in this
repo.

Also missing and referenced elsewhere in the atlas: `CISCO-CDP-MIB` (the primary
non-LLDP discovery protocol), `CISCO-VTP-MIB`, `CISCO-STACK-MIB`,
`CISCO-STP-EXTENSIONS-MIB`, `CISCO-ENTITY-SENSOR-MIB`, `CISCO-PROCESS-MIB`,
`CISCO-CONFIG-COPY-MIB`, `CISCO-EIGRP-MIB`.

**Recommendation:** either populate the directory from
`github.com/cisco/cisco-mibs` or correct `spec/mib/README.md` to say Cisco
enterprise is out of scope. The documentation currently promises something the
tree does not contain.

### IEEE 802.1 YANG — not vendored

The 802.1 working group publishes 102 YANG modules at
`ieee802.org/1/files/public/YANGs/`: `ieee802-dot1q-bridge`, `-vlan-bridge`,
`-types`, `-mstp`, `-rstp`, `-pb`, `-tpmr`, `ieee802-dot1ab-lldp`,
`ieee802-dot1ax-linkagg` / `-drni`, `ieee802-dot1x` / `-eapol`,
`ieee802-dot1ae-secy`, `ieee802-dot1as-gptp`, the TSN set, and
`ieee802-types` / `ieee802-ethertype`.

FlowSeer's own net-core research already cites
`ieee802-dot1q-types.yang` and `ieee802-dot1q-bridge.yang` as sources. They are
the authoritative definitions for VLAN types, bridge components, and the
component-id dimension that the bridge-domain question turns on, and they are
not in the tree.

**Recommendation:** vendor at least `ieee802-types`, `ieee802-ethertype`,
`ieee802-dot1q-types`, `ieee802-dot1q-bridge`, and `ieee802-dot1ab-lldp`.

### IETF YANG — four modules of a large catalogue

Present: `ietf-interfaces`, `iana-if-type`, `ietf-inet-types`,
`ietf-yang-types`. Absent and relevant: `ietf-ip` (RFC 8344),
`ietf-system` (7317), `ietf-routing` / `-ipv4-unicast-routing` /
`-ipv6-unicast-routing` (8349), `ietf-hardware` (8348), `ietf-alarms` (8632),
`ietf-access-control-list` (8519), `ietf-network` / `-network-topology` (8345),
`ietf-l2-topology` (8944), `ietf-network-instance` (8529).

The topology pair matters most: [01-prior-art](01-prior-art.md#ietf-topology-models)
argues they are the model to start from if FlowSeer builds a topology graph, and
RFC 8529 is the network-instance definition the bridge-domain work needs.

### OpenConfig — partial closure

`spec/yang/openconfig/SOURCES.md` is explicit that the 90 vendored files are
"exactly the transitive import closure the Aruba set needs — not the full
upstream release". Working as intended. Worth knowing that
`openconfig-nat`, `-relay-agent`, `-telemetry`, `-sampling`, `-oam`, `-ptp`,
`-multicast`, `-security` and the optical family are not reachable from this
tree.

### Vendor sets that are thin relative to the product

| Vendor | Corpus | Reality |
|---|---|---|
| Netgear | 4 modules | the FASTPATH feature set is ~40 modules; use `EdgeSwitch-*` as the reference |
| Ubiquiti UniFi | 3 MIBs | the API is the surface, and it *is* vendored |
| MikroTik | 1 MIB | ditto; but note the bridge/VLAN model has **no** MIB representation at all |
| Aruba CX | 35 MIBs | deliberately thin; REST v10.0x is the real surface and has no vendored spec |
| HPE Comware | 271 MIBs | complete for SNMP, but the NETCONF XSD models are login-walled and absent |
| HP AOS-S | — | REST schema is per-device and absent |

For Aruba CX and AOS-S the `SOURCES.md` files already record that the schema
must be exported from a live switch. `ops/switch-restore/` and the lab network
described in the project memory make that feasible; it is the single highest
value corpus addition available without a vendor login.

---

## 3. Design questions the corpus says to settle

These are not gaps in coverage; they are decisions that get harder to change
after the current packages stabilise.

### 3.1 What is an interface's key?

`CONCEPTS.md` says tables "reference interfaces by name". RFC 8343 explicitly
warns that IF-MIB permits duplicate `ifName` values, and
[02-interface](entities/02-interface.md#the-identity-problem-stated-precisely)
sets out the four competing identifiers. Stacking makes it worse: a port renames
from `Gi0/1` to `Gi1/0/1` when a stack forms.

**Recommendation:** state the key explicitly in the schema comments, including
what happens when it is not unique, and record whether the collector is expected
to resolve `ifIndex`, `ifName`, `ifDescr` or a synthetic id. Every table in
`net/switching/v1` and `net/ip/v1` depends on the answer.

### 3.2 The bridge domain / network instance dimension

Named in the net-core research as "the largest unresolved architectural
dependency". The corpus confirms it and adds detail:

- Modern IEEE MIBs put a bridge-component id first in **every** table key.
- `dot1qTpFdbTable` is keyed on the **FID**, not the VID; under shared VLAN
  learning the mapping is many-to-one and a MAC cannot be attributed to a VLAN
  at all.
- Huawei carries `vsiName` in FDB keys and `vpnName` in route and static-route
  keys; LANCOM carries `rtgTag`; NetBox scopes VLAN uniqueness to a VLANGroup.
- `openconfig-network-instance` is the model that resolves all of it.

FlowSeer's `FdbEntry` keyed on `(VlanId, Eui48)` bakes in the independent-VLAN-
learning assumption. That is a defensible default for enterprise switching.

**Recommendation:** make the assumption explicit in the schema comment, and
decide *now* whether the dimension is added later as an optional field or
whether the messages are shaped for it from the start. Adding a key component
after v1 is a breaking change; adding an optional scope field is not.

### 3.3 There is no home for operations

Four entities — [config-file](entities/01-platform.md#config-file),
[cable-diag](entities/02-interface.md#cable-diag),
[ip-diagnostics](entities/04-ip.md#ip-diagnostics), and firmware upgrade — are
modelled by *every* vendor as a job: a definition, a status, and a result log.
`DISMAN-PING-MIB`'s `pingCtlTable[owner, testName]` /
`pingResultsTable` / `pingProbeHistoryTable` triple is the canonical form, and
Comware's `hh3cCfgOperateTable` / `-ResultTable` and Cisco SMB's `rlCopyTable` /
`rlCopyHistoryTable` / `rlCopyMessagesTable` are the same shape.

FlowSeer's Config/State/Event triad has no place for this. It is not
configuration (it is not desired state), not state (it is a past action), and
not an event (it has a lifecycle and a result document).

**Recommendation:** decide whether an Operation concept belongs in the model
before any of those four entities is attempted. The alternative — modelling a
cable test as State — will be wrong in a way that is expensive to unwind.

### 3.4 One neighbour entity or several?

[cdp-like](entities/03-switching.md#cdp-like) sets out the choice.
`PTOPO-MIB`'s `ptopoConnDiscAlgorithm` and SNMP::Info's unified `c_*` methods
say one entity with a protocol discriminator; SuzieQ's separate `Lldp` and `Cdp`
tables say otherwise. The consumer question — "what is on this port" — is
protocol-blind, which argues for one.

FlowSeer currently has an LLDP-specific `neighbor.proto` in a protocol package.
**Recommendation:** decide before `net/protocol/lldp/v1` stabilises whether the
general neighbour row lives in a neutral package with LLDP as one source, or
whether each protocol keeps its own and a service-layer view unions them.

### 3.5 Where does time live?

`FdbEntry`, `NeighborEntry`, and the wireless client tables are all snapshots of
things that move. Netdisco's answer — `time_first` / `time_last` / `active` in
the store, not in the device model — is right, and FlowSeer's device model is
correctly time-free.

But it means the *question operators actually ask* ("where was this MAC on
Tuesday") is not answerable from anything currently designed, and the design
that answers it does not exist yet. Worth naming as owned work rather than
leaving implicit.

### 3.6 The port-namespace problem deserves a first-class concept

Seven port identifiers, listed in
[00-methodology](00-methodology-and-lineages.md#1-the-port-namespace-problem).
Every cross-entity join crosses at least one boundary, the standard mapping
tables (`dot1dBasePortIfIndex`, `entAliasMappingTable`) are optional in
practice, and a decoder that assumes identity where a mapping is missing
produces confidently wrong data rather than failing.

**Recommendation:** treat "which port namespace is this value in, and how was it
resolved" as collector-layer state with its own representation, not as a
per-decoder detail. It interacts directly with the Declining and Fatal-walk
concepts already in `CONCEPTS.md`: a missing `dot1dBasePortIfIndex` walk should
be fatal for anything that needs it, not a silent identity mapping.

### 3.7 Two known-wrong details in existing schemas

Both already identified by the net-core research and confirmed by the corpus:

- **`ipAddressOrigin` conflates assignment method with interface-identifier
  generation** for IPv6 (`linklayer` and `random` are both SLAAC). The
  corrected taxonomy is still to be done.
- **Routing lives in `net/ip/v1`** and should leave before stability. The corpus
  adds two requirements for the replacement: a network-instance key, and an
  explicit RIB-versus-FIB discriminator, because no SNMP table distinguishes
  them and any SNMP-sourced routing data is ambiguous by construction.

---

## 4. Quick wins

Things that are cheap, useful, and independent of the larger decisions.

| Win | Why |
|---|---|
| Collect `sysORTable` | one walk tells you which MIB modules the agent claims — capability discovery for free ([snmp-agent](entities/09-ops.md#snmp-agent)) |
| Read the YANG `-dev` deviation files | the equivalent for RESTCONF/gNMI platforms; prevents an entire class of 404 ([netconf-telemetry](entities/09-ops.md#netconf-telemetry)) |
| Collect `snmpEngineBoots` | unambiguous reboot detection, where `sysUpTime` wraps at 497 days |
| Compare device clock to collector clock | detects the unsynchronised-clock failure that silently corrupts every timestamp ([time-sync](entities/09-ops.md#time-sync)) |
| Read `ifCounterDiscontinuityTime` | the difference between a correct rate and a wrong one across a reload |
| Expose the U/L bit on `Eui48` | randomised client MACs are now the majority on wireless; the bit is free ([wireless-client](entities/08-wireless.md#wireless-client)) |
| Write one FASTPATH decoder | covers Ubiquiti EdgeSwitch, Netgear, and most of LANCOM SX ([lineages](00-methodology-and-lineages.md#broadcom-fastpath--icos)) |
| Write one Foundry decoder | covers Ruckus ICX and the `HP-SN-*` half of ProCurve |
