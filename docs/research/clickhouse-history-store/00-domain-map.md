---
title: Data domains for the ClickHouse history store, present and planned
date: 2026-10-08
status: research; a snapshot of the tree on 2026-10-08, re-check paths before relying on a line number
---

# Data domains, present and planned

Every data domain FlowSeer's schema, records, plans, and research name, with
whether it changes over time and whether it belongs in the history store. The
[ingestion pipeline record](../../architecture/2026-10-02-central-ingestion-pipeline-direction.md)
assigns stores by class of data (line 111: append-only records and numeric
time-series to ClickHouse, line 112: current state to JetStream KV, line 113:
API lists to Postgres), not by domain. Only `SyslogRecord` is decided for
ClickHouse (phase 3 plan, line 15). Every other "Y-inf" below is inferred
from that class rule.

Four facts shape every domain:

- Only syslog has a record type. `IngestRecord.payload` has one arm,
  `syslog = 10` (`spec/proto/flowseer/integration/ingest/v1/ingest_record.proto:25-30`),
  and `IngestRecordTypes()` returns only syslog (`src/modules/edgebus/subjects.go:86-88`).
- `net/` rows carry no time and no device (`spec/proto/flowseer/net/README.md:5-9`).
  Both come from `Provenance` on the envelope
  (`spec/proto/flowseer/model/inventory/v1/provenance.proto:14-18`). No model
  message groups table rows (FDB, ARP, routes) per device or per collection
  cycle.
- Many samples sit inside State messages that phase 5 sends to KV:
  `DeviceState` uptime and utilization (`model/inventory/v1/device.proto:210-216`),
  `ComponentState` sensors, module, and radio (`component.proto:203-214`),
  `Interface.counters` (`net/interface/v1/interface.proto:56`),
  `EndpointState.counters` (`model/endpoint/v1/endpoint.proto:70`), and
  `RadioFacet.channel_utilization` (`net/wlan/v1/radio_facet.proto:79`). The
  sink either extracts samples from State records or the record types split.
  Nothing decides which.
- Latest state may not come from ClickHouse materialized views (ingestion
  record, lines 139-142), and ingestion volume follows change: "delta events,
  not snapshots" (`docs/architecture/2026-08-20-device-service-and-inventory-direction.md:380-382`).

## Legend

Store: **Y-dec** decided for ClickHouse, **Y-inf** inferred from the class
rule, **KV** current state, **JS** a JetStream stream today, **N** no, **U**
undecided. Nature: ctr counter samples, gauge, state, trans state transitions,
set set-valued table, evt events, cfg config, inv inventory, aud audit. Lane is
the dossier that models the domain (see the [index](README.md)), or n/a.

## net/ packages (present)

| Domain (messages) | Evidence | Nature | Over time | Store | Lane |
| --- | --- | --- | --- | --- | --- |
| net/interface `Interface`, `InterfaceCounters` | `interface.proto:26`, `:56`, `last_change :62`; `interface_counters.proto:15`, `last_discontinuity :48`; mapper `snmpmap/ifmib.go:598` | ctr, state | traffic, flaps | Y-inf (counters, status changes), current row KV | A |
| net/phy Ethernet `EthernetFacet`, `EthernetCounters` | `ethernet_facet.proto:24`, `:44`, `:89`; `ethernet_counters.proto:16`; mapper `phy.go:393` | ctr, state | yes | Y-inf | A |
| net/phy PoE `PoeFacet`, `PoePortDetail`, `PseBudget` | `poe_facet.proto:11` (draw `:36`); `poe_port_detail.proto:13` (faults `:27-39`); `pse_budget.proto:13` (`:30`); mappers `phy.go:754,773,868` | gauge, ctr, state | yes | Y-inf | G |
| net/phy optics `PluggableModule`, `ModuleDiagnostics`, `ModuleLane` | `pluggable_module.proto:18`; `module_diagnostics.proto:11`; `module_lane.proto:13`; mapper `phy_ddm.go:245` | inv, gauge | optics degrade | Y-inf (gauges) | G |
| net/switching `FdbEntry` | `fdb_entry.proto:22`; no mapper | set | host moves, missing-device evidence | Y-inf, shape open | C |
| net/switching `Vlan`, `SwitchportFacet` | `vlan.proto:12`; `switchport_facet.proto:12` | set, cfg-like | rare | U | E |
| net/ip `NeighborEntry` (ARP/ND) | `neighbor_entry.proto:15`; no mapper | set | yes | Y-inf | C |
| net/ip `InterfaceAddress`, `IpFacet` | `interface_address.proto:15`; `ip_facet.proto:13` | set, state | rare | U | E |
| net/instance `NetworkInstance` | `network_instance.proto:11` | cfg, set | rare | key column only | E |
| net/routing `Route`, `NextHop`, `NextHopGroup` | `route.proto:14`; `next_hop.proto:30`; `next_hop_group.proto:10`; `GOALS.md:102` | set, large | yes | U | E |
| net/protocol/bgp `BgpPeer`, counters, address families | `bgp_peer.proto:15`, `:90`, `:122`, discontinuity `:136` | state, ctr | yes | Y-inf | A (counters), E (state) |
| net/protocol/bgp `BgpPath` | `bgp_path.proto:13` | set, very large | yes | U | E |
| net/protocol/bfd `BfdSession` | `bfd_session.proto:15`, `:95` | state, ctr | yes | Y-inf | A, E |
| net/protocol/vrrp `VrrpGroup` | `vrrp_group.proto:14`, `:107` | state, ctr | yes | Y-inf | A, E |
| net/protocol/dhcp server counters, pools | `dhcpv4_server_counters.proto:10`; `dhcpv6_server_counters.proto:10`; `dhcpv4_pool.proto:11` | ctr, gauge | yes | Y-inf | A |
| net/protocol/dhcp leases, bindings | `dhcpv4_lease.proto:13`; `dhcpv6_binding.proto:10`; `dhcp_snooping_binding.proto:14` | set | yes | U | E |
| net/nat `NatSession`, counters | `nat_session.proto:14`, `:50` | set with ctr | high churn | Y-inf (counters), set U | A, E |
| net/nat `NatMapping` | `nat_mapping.proto:14` | cfg | no | N | n/a |
| net/protocol/lldp `Neighbor`, `LocalSystem` | `neighbor.proto:18`; `local_system.proto:13`; mapper `lldp.go:214,459` | set | topology | Y-inf | C |
| net/protocol/cdp `Neighbor` | `cdp/v1/neighbor.proto:19` | set | topology | Y-inf | C |
| net/protocol/stp bridge, ports, MST | `bridge_state.proto:16` (`topology_changes :58`); `port_state.proto:16` (`:65-84`); `mst_instance.proto:14`; `mst_port.proto:14` | state, ctr | topology changes | Y-inf | E |
| net/protocol/lacp ports, aggregators | `lacp port_state.proto:14` (`:50-56`); `aggregator_state.proto:14` | state, ctr | yes | Y-inf | E |
| net/protocol/ospf neighbors, interfaces, areas | `ospf_neighbor.proto:11`; `ospf_interface.proto:12`; `ospf_area.proto:10` | set, state | adjacency flaps | Y-inf | E |
| net/protocol/isis `IsisAdjacency` | `isis_adjacency.proto:14` | set, state | yes | Y-inf | E |
| net/protocol/ntp `NtpAssociation` | `association.proto:13` | state | yes | U | E |
| net/protocol/dns `DnsResolver` | `dns_resolver.proto:11` | cfg | no | N | n/a |
| net/multicast `GroupMembership` | `group_membership.proto:18` | set | yes | U | E |
| net/portaccess `Session` | `session.proto:15`; `CONCEPTS.md:215-217` | set, state | yes | Y-inf | E |
| net/filter `FilterRuleSet` | `filter.proto:103` | cfg | no | N | n/a |
| net/qos | `qos README:3-5`; counters absent `:20` | cfg | no | N | n/a |
| net/aaa `AaaServer` status | `aaa_server.proto:15`, `:36`; statistics absent (README `:21`) | cfg, state | status | U | E |
| net/flow exporter config | `flow README:3-4`; records absent `:14-17` | cfg | no | N; records unplanned | G |
| net/system CPU, storage | `processor_utilization.proto:10`; `storage_utilization.proto:10` | gauge | yes | Y-inf | A |
| net/system `SoftwareImage` | `software_image.proto:9`; `FirmwareEpochChanged` `operation_event.proto:127` | inv, trans | upgrades | Y-inf (transitions) | F |
| net/system `License` | `license.proto:11`, `expires_at :38` | state | slow | U | F |
| net/measure `SensorReading` | `sensor.proto:177`; `component.proto:203` | gauge | yes | Y-inf | A |
| net/measure `PathQuality` | `path_quality.proto:15`; no importer | gauge | yes | Y-inf | G |
| net/wlan `RadioFacet`, `ChannelUtilization` | `radio_facet.proto:16` (`:61`, `:69`, `:79`); `channel_utilization.proto:12` | state, gauge | yes | Y-inf | B |
| net/wlan `Bss` | `bss.proto:14`; `associated_client_count :42` | state, gauge | yes | Y-inf | B |
| net/wlan `NeighborBss` | `neighbor_bss.proto:14`, `rssi :33` | set, gauge | yes | Y-inf | B |
| net/cellular `CellularInterface`, `CellularSignal` | `cellular_interface.proto:14`, `:46`; `cellular_signal.proto:12` | state, gauge | yes | Y-inf | G |
| net/endpoint attachments, counters | `wireless_attachment.proto:12` (`:51-55`); `wired_attachment.proto:10`; `endpoint_counters.proto:8` | gauge, ctr, state | yes | Y-inf | B |
| net/capture `CaptureCounters`, `PacketRecord` | `capture_counters.proto:12`; `packet_record.proto:11` | ctr, packets | per session | N: KV `captures` plus a pcapng file | n/a |
| net/log, net/addr, net/key, net/packet | value types (`net/README.md:31-34,51`) | none | none | N | n/a |

## model/, event/, and other roots (present)

| Domain | Evidence | Nature | Over time | Store | Lane |
| --- | --- | --- | --- | --- | --- |
| Device `DeviceConfig`, `DeviceState`, `DeviceEvent` | `device.proto:67`, `:122`, `:220` | cfg, inv, gauges, trans | yes | State KV; event and gauges Y-inf | A (gauges), F (event) |
| Component `ComponentState`, `ComponentEvent` | `component.proto:87`, `:196`, `:219` | inv, state, gauges, trans | part swaps | State KV; event Y-inf | A, F |
| Binding `BindingState`, `BindingEvent` | `binding.proto:102` (`:135`, `:138`, `:142`), `:164` | state, trans | availability | State KV; event Y-inf | F |
| Integration, IntegrationScope | `integration.proto:144`, `:184`; `integration_scope.proto:33`, `:70` | state, trans | low | State KV; event U | F |
| Placement | `placement.proto:43`, `:75`; append-only (inventory README `:223-232`) | dated relation | yes | as-of dimension | F |
| Link | `link.proto:100` (`:138-140`), `:148` | state, trans | topology | State KV; event Y-inf | F |
| Location, Cable, PatchPanel, Tag, Attribute, Capability | `location.proto:56`; `cable.proto:108`; `patch_panel.proto:96`; `tag.proto:26`; `attribute.proto:89` | cfg | no | N | n/a |
| Alarm `AlarmState`, `AlarmEvent` | `alarm.proto:88`, `:114`; alarm README `:50-58` | state, trans | yes | State KV; event Y-inf | D |
| Endpoint `EndpointState`, `EndpointEvent` | `endpoint.proto:47`, `:142` (`:151`, `:153`, `:155`) | state, evt | yes | State KV; events Y-inf | B |
| Wlan `WlanConfig`, `WlanState`, `WlanEvent` | `wlan.proto:53`, `:108` (`:142`), `:146` | cfg, set, trans | yes | State KV; event Y-inf | B |
| Edge `EdgeState`, `EdgeEvent` | `edge.proto:118` (`:142`, `:152`, `:162`), `:166` | state, trans | contact | KV; history U | F |
| Capture session | `capture_session.proto:214`, `:254` | state, trans | per session | N: KV `captures` | n/a |
| Identity (tenant, role, member, partner) | `tenant.proto:71`, `:87`; `access.proto:39-95` | cfg | no | N | n/a |
| Access operations, `InterfaceObservation` | `operation.proto:118`, `:152`; `access/interface.proto:50` | state | journal | N (KV); drift reads F | F |
| Credential, policy | `material.proto:120`; `handle.proto:14-59` | secret | no | N | n/a |
| event/log `SyslogRecord` | `syslog_record.proto:17`; phase 3 plan `:15` | evt | yes | **Y-dec** | D |
| event/access `DeviceOperationEvent` | `operation_event.proto:20`, `:146`, `:127`, `:116`; `FLOWSEER_DEVICE_AUDIT` (`subjects.go:28-29`, `hub.go:66-68,402-406`) | aud, trans | yes | JS today; long-term U | F |
| event/operator `OperatorActionEvent` | `operator_action_event.proto:131`; eviction risk (operator authorization record `:895-899`) | aud | yes | JS today; long-term U | F |
| `IngestRecord` `RawEvidence` | `ingest_record.proto:34`; 24 h evidence stream | raw bytes | short | N, by decision | n/a |
| Agent OpenTelemetry signals | edgebus README `:257-259` | telemetry | yes | N (collector backend) | n/a |
| spec/proto/ruckus SmartZone GPB | ruckus README `:4-6` | vendor input | n/a | maps into B and D record types (inferred) | n/a |

## Planned, with no package or record type

| Domain | Evidence | Nature | Store | Lane |
| --- | --- | --- | --- | --- |
| SNMP traps | `GOALS.md:23`, `:59-66`; listener `src/protocol/snmp/trap_listen.go:65`; notification MIBs under `spec/mib/` | evt | Y-inf | D |
| Webhooks, controller streams | device service record `:135`, `:165`, `:180-182`; device-inventory README `:138-140` | evt | Y-inf after mapping | D (source), G |
| Discovery sightings | device service record `:93`, `:107-118`, `:230-232` | evt, set | U | G |
| Site | `GOALS.md:21`; `location.proto:36` | dimension | enrichment column | F |
| Config snapshots, drift | atlas `config-file` (INDEX `:140`); `GOALS.md:59-66` | cfg, trans | U | F |
| VPN tunnels | `tunnel_interface.proto:8`; dossier 07 `:355`, `:437` | state | U | G |
| SD-WAN uplinks | dossier 07 `:149-150` | state, gauge | U | G |
| Path quality, ICMP probes | `PathQuality`; device service record `:265-267` | gauge | Y-inf | G |
| Flow records | flow README `:14-17` | evt, huge | U | G |
| QoS, AAA, ACL statistics | qos README `:20`, aaa README `:21` (deliberately absent) | ctr | U | n/a until a schema exists |
| Radio hardware counters, DFS events | wlan README `:19-21` (deliberately absent) | ctr, evt | U | B (future) |
| Simulation, offered load, analysis | virtual-device record `:960`; offered-load record `:40-41` | results | N for now | n/a |
| netpen findings | `src/edge/README.md:10` | evt | U | n/a |

The web console fixtures show which history the views expect: 24 h traffic per
scope (`frontend/web/src/domain/overview.ts:11-14,116-130`), an event feed
(`:4-10`), CPU, memory, temperature, and boot time (`telemetry.ts:6-16`), port
speed, throughput, and PoE (`:18-29`), link latency, errors, and utilization
(`:205-217`), and client signal (`clients.ts:4-14`).

## Open points

- That each triad splits State to KV and Event to ClickHouse is inferred. No
  record assigns triad events or audit streams to ClickHouse.
- Retention per record type is open (ingestion record, line 191).
- The audit streams are byte-bounded JetStream (`src/modules/edgebus/hub.go:66-77`),
  and no record names a long-term audit store.
- The set-valued domains must reconcile the delta-events rule with presence
  rows, which need periodic presence evidence.
- FDB, ARP, and routing have schema but no mapper. Interfaces, LLDP,
  Ethernet, PoE, and transceiver diagnostics are produced today
  (`src/modules/localnet/README.md:11`).
