# Flow Export Primitives

The `flowseer.net.flow.v1` package defines where a device sends flow data:
sFlow receivers, samplers, and pollers, and NetFlow and IPFIX exporters.

## Boundaries

Imports: net/addr, net/key, net/packet

Imported by: nothing

Deliberately absent:

- Flow record decoding, the IANA IPFIX Information Element registry, flow
  monitors and records, and NetFlow samplers. They describe the record
  format, which the collector decodes on a different plane from the device
  configuration this package holds.
- sFlow receiver ownership leases (`sFlowRcvrOwner`, `sFlowRcvrTimeout`).
- VLAN and entity sFlow data sources. They have no interface name to key a
  row by.
- Network instance key, and the VRF an exporter reaches its collector
  through. OpenConfig and `SFLOW-MIB` report none, so a required field would
  force a mapper to claim `default` it cannot see.

## Contents

- `sflow_receiver.proto`: `SflowReceiver`, an sFlow collector by index.
- `sflow_sampler.proto`: `SflowSampler`, a packet sampler on an interface.
- `sflow_poller.proto`: `SflowPoller`, a counter poller on an interface.
- `flow_export_protocol.proto`: `FlowExportProtocol`, NetFlow v5, v9, or
  IPFIX.
- `flow_exporter.proto`: `FlowExporter`, a NetFlow or IPFIX collector target.

The sFlow rows mirror the three `SFLOW-MIB` tables. The MIB encodes "not
configured" as a receiver address of `0.0.0.0`, a sampling rate of 0, a
polling interval of 0, or receiver 0, so a mapper omits such a row, and the
schema requires a sampling rate of at least 1, an interval of at least one
second, and a receiver index of at least 1. Only interface data sources are
kept.

`FlowExporter` follows the IOS-XE flow exporter, the one typed source for
NetFlow and IPFIX collector targets: Huawei NetStream exposes no collector
objects. An IOS-XE exporter to a local collector has no destination address
and has no row.

## Sources

- `SFLOW-MIB` (`spec/mib/ietf/SFLOW-MIB`) for receivers, flow samplers, and
  counter pollers.
- The [sFlow version 5 specification](https://sflow.org/sflow_version_5.txt),
  as dossier 07 cites it
  ([07 QoS, Security, Operations, and WAN](../../../../../../docs/research/schema-building-blocks/07-qos-security-ops-wan.md)).
- IOS-XE `Cisco-IOS-XE-flow.yang` (`spec/yang/cisco/iosxe/2611/`) for the
  flow exporter.
- [RFC 3954](https://www.rfc-editor.org/rfc/rfc3954.html) and
  [RFC 7011](https://www.rfc-editor.org/rfc/rfc7011.html) for NetFlow version 9
  and IPFIX.
