---
title: Prior art — how comparable systems model this domain
date: 2026-08-30
scope: SNMP::Info, Netdisco, LibreNMS, NAPALM, SuzieQ, NetBox/Nautobot, OpenConfig, IETF topology models
---

# Prior art

Eight systems that have solved some version of FlowSeer's problem. For each:
what it models, the design decisions worth copying, and the ones worth avoiding.

The short version: **nobody has done what FlowSeer is attempting** — a typed,
schema-first, multi-vendor device model with explicit provenance. The closest
approaches split into collectors that normalise ad hoc (SNMP::Info, LibreNMS,
NAPALM), stores that model the answer but not the collection (Netdisco,
NetBox), and schema families that model the device but not the fleet
(OpenConfig, IETF YANG).

---

## SNMP::Info

*Perl library; the SNMP layer under Netdisco. The closest prior art to
FlowSeer's collection layer.*

### The model

A base class defines **normalised method names**, each backed by a MIB object.
Per-vendor subclasses override the mapping or post-process the value. Composition
is by **MIB mixin**: `Bridge`, `CDP`, `LLDP`, `Entity`, `EtherLike`, `MAU`,
`PowerEthernet`, `IEEE802dot11`, `IEEE802dot3ad`, `PortAccessEntity`,
`AdslLine`, `DocsisCM`/`HE`, `EDP`, `FDP`, `AMAP`, `SONMP`, `RapidCity`,
`NortelStack`, `CiscoStack`/`VTP`/`QOS`/`Power`/`PortSecurity`/`StpExtensions`,
and a device class per platform composed from them.

Representative method set (interfaces): `i_index`, `i_description`, `i_name`,
`i_alias`, `i_type`, `i_mtu`, `i_speed` / `i_speed_raw` / `i_speed_high`,
`i_mac`, `i_up`, `i_up_admin`, `i_lastchange`, `if_ignore`. Counters make the
32/64-bit split explicit in the name: `i_octet_in` vs `i_octet_in64`,
`i_pkts_ucast_in(64)`, `i_pkts_multi_in(64)`, `i_pkts_bcast_in(64)`,
`i_errors_in/out`, `i_discards_in/out`, `i_bad_proto_in`, `i_qlen_out`.

### Worth copying

1. **The unified neighbour view.** `has_topo()` returns which of
   `lldp cdp sonmp fdp edp amap` a device speaks; then one set of methods —
   `c_ip`, `c_if`, `c_port`, `c_id`, `c_platform`, `c_cap` — answers "what is
   on this port" regardless of protocol. This is the design
   [cdp-like](entities/03-switching.md#cdp-like) recommends and it agrees with
   `PTOPO-MIB`'s `ptopoConnDiscAlgorithm` column.
2. **Dual-source fallback as a first-class idea.** `ip_index` reads
   `ipAddrTable` and falls back to `ipAddressTable`; `i_speed` reads `ifSpeed`
   and falls back to `ifHighSpeed`. The deprecated/current pair problem is
   handled once, in the accessor, not in every caller.
3. **The MIB mixin as the unit of reuse.** One class per MIB, composed per
   platform. It maps directly onto the lineage finding: a FASTPATH mixin would
   cover three vendors.
4. **`if_ignore`** — an explicit per-platform list of interfaces to skip. Every
   real collector needs this and most bolt it on later.

### Worth avoiding

- Values are stringly typed and sometimes pre-formatted for humans (`i_speed`
  returns "1.0 Gbps"; `i_speed_raw` was added later to get the number back).
  FlowSeer's typed schema is the right answer.
- The method namespace is flat and undocumented as a contract — you discover
  what a driver supports by calling it.
- No provenance: a value carries no record of which OID it came from or when.

---

## Netdisco

*PostgreSQL-backed discovery and search over SNMP::Info. The storage-shape prior
art.*

### The schema

```
device            ip, mac, serial, vendor, model, os, snmp settings, last_discover
device_ip         all IPs of a device
device_port       port, name, descr, up, up_admin, speed, duplex, vlan,
                  remote_ip, remote_port, remote_type, remote_id, pae state, …
device_port_vlan  one row per (port, vlan) with native/tagged flag
device_port_power PoE per port;  device_power  PoE per module
device_port_log   port change history
node              mac, switch, port, vlan, active, time_first, time_last
node_ip           mac, ip, active, time_first, time_last
node_nbt          NetBIOS names
node_monitor      watchlist
admin             the job queue
```

### Worth copying

1. **`time_first` / `time_last` on `node` and `node_ip`.** The device model has
   no history; the *store* adds one. This is how "where was this MAC last
   Tuesday" becomes answerable, and it is the product. FlowSeer's `FdbEntry` and
   `NeighborEntry` correctly have no time axis — the service above them will
   need exactly this.
2. **`active` as a boolean alongside the timestamps.** A row is not deleted when
   the MAC ages out; it is marked inactive. History is append-oriented, which
   matches FlowSeer's Placement design.
3. **`device_port_vlan` as its own table** rather than an array on the port.
   Membership is a relationship with its own attributes.
4. **The `admin` job queue in the database.** Collection work is data, visible
   and restartable.

### Worth avoiding

- `device_port` is very wide and mixes observation with derived state
  (`remote_*` is the LLDP/CDP result denormalised onto the port).
- One row per device with a single `serial` and `model` — the stack problem.
- No concept of *which* integration answered; there is one SNMP path per device.

---

## LibreNMS

*PHP monitoring platform, ~350 supported OS definitions.*

### The model

Discovery is decomposed into **modules**, and this list is the best available
answer to "what is worth discovering":

`os` · `ports` · `ports-stack` · `xdsl` · `entity-physical` · `processors` ·
`mempools` · `cisco-vrf-lite` · `ipv4-addresses` · `ipv6-addresses` · `route` ·
`sensors` · `storage` · `hr-device` · `discovery-protocols` · `arp-table` ·
`fdb-table` · `discovery-arp` · `junose-atm-vp` · `bgp-peers` · `vlans` ·
`mac-accounting` · `cisco-pw` · `vrf` · `cisco-cef` · `slas` · `vminfo` ·
`printer-supplies` · `ucd-diskio` · `services` · `stp` · `ntp` ·
`loadbalancers` · `mef` · `wireless` · `applications`

Per-OS behaviour is dispatched by **file path**:
`includes/discovery/sensors/$class/$os.inc.php`. Adding a vendor means adding
files, not editing a dispatcher.

### Worth copying

- **The module list itself** — it maps closely onto this atlas's entity set and
  is battle-tested against 350 platforms.
- **`ports-stack` as its own module.** The interface hierarchy
  (`ifStackTable`) is hard enough to deserve separate treatment.
- **Path-based per-OS dispatch** — a convention that scales to hundreds of
  platforms without a central registry.

### Worth avoiding

- YAML-plus-PHP definitions with no schema; correctness is by convention.
- Sensor scaling divisors hard-coded per OS because
  `ENTITY-SENSOR-MIB`'s scale/precision is ignored.

---

## NAPALM

*Python multi-vendor driver library, CLI- and API-driven rather than SNMP.*

### The getters

`get_facts`, `get_interfaces`, `get_interfaces_counters`, `get_interfaces_ip`,
`get_arp_table`, `get_ipv6_neighbors_table`, `get_mac_address_table`,
`get_vlans`, `get_lldp_neighbors`, `get_lldp_neighbors_detail`, `get_route_to`,
`get_network_instances`, `get_bgp_config`, `get_bgp_neighbors`,
`get_bgp_neighbors_detail`, `get_optics`, `get_environment`, `get_users`,
`get_snmp_information`, `get_ntp_peers` / `get_ntp_servers` / `get_ntp_stats`,
`get_firewall_policies`, `get_probes_config` / `get_probes_results`,
`get_config`, `is_alive`, `cli`.

Unsupported getters raise `NotImplementedError` — NAPALM's version of a decoder
declining.

### Worth copying

- **A small, closed set of well-named questions.** Twenty-odd getters cover most
  of what an operator asks. FlowSeer's entity set is larger by design, but the
  discipline of naming the *question* rather than the table is right.
- `get_config` returning `{running, candidate, startup}` — the datastore
  triple, made explicit.

### Worth avoiding — the schema warts are instructive

| Getter | Problem |
|---|---|
| `get_facts` | one `serial_number`, one `model` — breaks on stacks |
| `get_interfaces` | `speed` as a **float in Mbps**; `last_flapped` as a float |
| `get_vlans` | `{vid: {name, interfaces}}` — membership inverted onto the VLAN, losing tagged vs untagged |
| `get_arp_table` | no VRF key |
| `get_mac_address_table` | no bridge/FID dimension |

Each of these is a case where a flat dict lost a dimension the underlying data
had. FlowSeer's typed schema with explicit presence avoids all five, provided
the keys are chosen deliberately — which is the recurring recommendation in the
entity records.

---

## SuzieQ

*Network observability with normalised multi-vendor state tables.*

Tables: `Arpnd`, `Bgp`, `Device`, `EvpnVni`, `Filesystem`, `Interfaces`,
`Inventory`, `Lldp`, `Cdp`, `Macs`, `Mlag`, `Ospf`, `Routes`, `Vlan`, plus
**`sqPoller`**.

### Worth copying

**`sqPoller` is a first-class table recording the collection attempt itself** —
which poller, which device, which service, at what time, with what result. That
is precisely FlowSeer's Provenance concept, and SuzieQ's decision to make it a
queryable table rather than metadata is worth noting: it means "why is this data
missing" is answerable with the same query language as everything else.

Also: every table carries a timestamp and SuzieQ keeps all versions, so state is
inherently time-series. "What did the routing table look like an hour ago" is a
filter, not a special feature.

### Worth avoiding

`Lldp` and `Cdp` as separate tables — the opposite of SNMP::Info's unified `c_*`
view. The consumer question ("what is on this port") is protocol-blind, so this
pushes a union onto every caller.

---

## NetBox and Nautobot

*Source-of-truth / intent modelling. Not collectors.*

Core objects: Site → Location → Rack → Device (with DeviceType, DeviceRole,
Platform) → Interface (typed, `mgmt_only`, LAG parent, MTU, MAC) → Cable;
VRF, Prefix, IPAddress, IPRange, Aggregate; VLAN, VLANGroup; Tag, CustomField,
ConfigContext.

### Worth copying

1. **VLAN uniqueness scoped to a VLANGroup**, not global and not per-device.
   This is the bridge-domain problem solved from the intent side, and it is the
   nearest thing to a validated answer for the question
   [03-switching](entities/03-switching.md#the-bridge-domain-problem-once-up-front)
   leaves open.
2. **Tags as a cross-cutting label tree** and **CustomFields as
   operator-defined typed attributes** — FlowSeer's Tag and Attribute
   Definition / Attribute Assignment concepts are the same design, and NetBox's
   experience says the hard part is the *edit* path (what happens to existing
   values when the definition changes), which FlowSeer already gates behind an
   explicit operator decision.
3. **Cable as a first-class object** connecting two termination points. FlowSeer
   has no cable/link concept; LLDP neighbours are the observed equivalent, and
   the two want to be reconcilable.

### Worth avoiding

- Everything is intent; there is no observation model, so "the switch says
  otherwise" has nowhere to live. FlowSeer's Config/State/Event triad is the
  better shape for a system that both records intent and polls.

---

## OpenConfig

*The modern device model. Covered in detail in
[vendors/standards.md](vendors/standards.md#openconfig).*

Three decisions FlowSeer already echoes or should:

1. **`config` / `state` split** with state as a superset of config.
2. **Name keys, not index keys** — sidesteps `ifIndex` and `entPhysicalIndex`
   instability entirely.
3. **`network-instance` as the container** for VLANs, FDB, protocols and AFTs,
   so the bridge-domain and VRF dimensions are structural.

And one caution: OpenConfig models *a device*, not a fleet. It has no tenancy,
no integration, no provenance, and no concept of a device reached through a
controller. Those are exactly the parts FlowSeer's `inventory/v1` adds, and they
do not exist upstream to borrow.

---

## IETF topology models

`ietf-network` + `ietf-network-topology` (RFC 8345) and the L2 augmentation
(RFC 8944). Neither is vendored in this repo.

The model: `networks/network[network-id]` containing
`node[node-id]`, `link[link-id]`, and `node/termination-point[tp-id]`, with
`supporting-network`, `supporting-node`, `supporting-link`, and
`supporting-termination-point` expressing **vertical layering** between
topologies.

### Why it matters to FlowSeer

If FlowSeer ever builds a topology graph — and
[lldp](entities/03-switching.md#lldp), [mesh](entities/08-wireless.md#mesh),
[stp](entities/03-switching.md#stp) and
[fdb](entities/03-switching.md#fdb) are all inputs to one — this is the model
to start from, for three reasons:

1. **Links are unidirectional and point-to-point**, with bidirectionality
   expressed as a pair. That is exactly what LLDP gives you (each end reports
   independently) and it avoids inventing a merge rule prematurely.
2. **Termination points** are the abstraction that lets a wired port, a
   wireless BSSID, and a mesh radio all terminate a link without being the same
   kind of object.
3. **The supporting-* layering** is how a wireless mesh link sits above a radio
   link sits above nothing, and how an L2 adjacency sits above a physical one.

RFC 8944 adds `l2-node-attributes` (name, management address, flags) and
`l2-termination-point-attributes` (MAC, encapsulation, VLAN) and explicitly
notes some of it comes from LLDP and some from local configuration — the same
observed-versus-intended split FlowSeer's triad makes.

---

## Synthesis: what FlowSeer should take

| From | Take |
|---|---|
| SNMP::Info | the protocol-agnostic neighbour row; dual-source fallback as an accessor concern; MIB-shaped decoder mixins matched to the [lineages](00-methodology-and-lineages.md#the-lineages) |
| Netdisco | `time_first`/`time_last`/`active` on endpoint sightings, in the service layer above the device model |
| LibreNMS | the discovery-module decomposition as a checklist; per-platform dispatch by convention |
| NAPALM | the discipline of naming questions, and its five schema warts as things not to repeat |
| SuzieQ | provenance as a queryable first-class table, not metadata |
| NetBox | VLAN scoped to a group, not globally; Cable as a first-class object |
| OpenConfig | name keys, config/state, network-instance as the container |
| RFC 8345/8944 | the topology model, if and when topology is built |
