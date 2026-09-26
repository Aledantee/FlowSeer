# Wireless LAN Primitives

The `flowseer.net.wlan.v1` package defines reusable 802.11 wireless LAN and RF
primitives for FlowSeer-owned schemas. It provides the radio facet, hosted BSS
rows, channel utilization metrics, neighbor-scan sightings, shared enums, and
predefined channel validation rules.

## Boundaries

Imports: net/addr, net/measure, net/switching

Imported by: model/inventory, model/wireless

Deliberately absent:

- Production telemetry mappers from vendor-specific formats (handled at the adapter edge).
- Wireless client station details and roaming facts (deferred to endpoint primitives).
- Entity lifecycle, persistence state, and tenancy context.
- Radio and BSS hardware counters, radio operating modes (monitor, mesh, sniffer),
  antenna chains, spatial streams, RCPI/RSNI metrics, spectrum interferers, DFS
  radar events, and MLO multi-link descriptors.
- Raw AKM and cipher suite pass-through bitmasks.

## Contents

- `channel.proto` — predefined protovalidate rules for 802.11 channel numbers
  (`wifi_channel`, 1..233) and operating channel widths (`wifi_channel_width_mhz`).
- `wifi_band.proto` — `WifiBand` operating frequency bands (2.4 GHz, 5 GHz, 6 GHz, 60 GHz).
- `dot11_standard.proto` — `Dot11Standard` physical-layer amendments (802.11a/b/g/n/ac/ax/be).
- `wlan_security.proto` — `WlanSecurity` normalized security and authentication suites.
- `pmf_mode.proto` — `PmfMode` Protected Management Frames configuration.
- `radio_admin_status.proto` — `RadioAdminStatus` administrative state.
- `radio_oper_status.proto` — `RadioOperStatus` operational state.
- `country_environment.proto` — `CountryEnvironment` operating class environment registry.
- `channel_utilization.proto` — `ChannelUtilization` busy-time ratios in basis points.
- `bss.proto` — `Bss` hosted Basic Service Set configuration and operational row.
- `radio_facet.proto` — `RadioFacet` radio interface facet.
- `neighbor_classification.proto` — `NeighborClassification` rogue and neighbor classification.
- `neighbor_bss.proto` — `NeighborBss` overheard neighbor and rogue BSS sighting row.

## Sources

The package's field and enum contracts cite:

- [IEEE Std 802.11-2020](https://standards.ieee.org/ieee/802.11/7028/) and [IEEE802dot11-MIB](../../../../../../spec/mib/ieee/IEEE802dot11-MIB)
  for channel numbers, operating classes, Country elements, beacon periods, and DTIM intervals.
- [Wi-Fi Alliance WPA3 Specification v3.5](https://www.wi-fi.org/system/files/WPA3%20Specification%20v3.5.pdf)
  for security modes, AKM selectors, and PMF requirements.
- [OpenConfig WiFi models](https://github.com/openconfig/public/tree/master/release/models/wifi)
  for channel utilization breakdown and radio operational quantities.
- FlowSeer research dossiers:
  - [01 Wi-Fi Technology](../../../../../../docs/research/schema-building-blocks/01-wifi-technology.md)
  - [02 RF and AP Telemetry](../../../../../../docs/research/schema-building-blocks/02-rf-and-ap-telemetry.md)
  - [09 Wi-Fi Registries](../../../../../../docs/research/schema-building-blocks/09-wifi-registries.md)
