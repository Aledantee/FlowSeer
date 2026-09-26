# Endpoint Primitives

The `flowseer.net.endpoint.v1` package defines reusable host and station
attachment primitives, client fingerprints, counters, connection failure
stages, and roam reasons for FlowSeer-owned schemas.

## Boundaries

Imports: net/addr, net/key, net/switching, net/wlan

Imported by: model/endpoint

Deliberately absent:

- Production telemetry mappers from vendor-specific formats (handled at the adapter edge).
- Top-level `Endpoint` entity identity and lifecycle (modeled under `model/endpoint/v1`).
- Multi-link device (MLD) per-link attachment lists (deferred until multi-link producers arrive).
- Client identity classification heuristics (a service concern).
- Port-access sessions (modeled under `net/portaccess/v1`).

## Contents

- `connection_failure_stage.proto` — `ConnectionFailureStage` normalized connection failure stage enum.
- `roam_reason.proto` — `RoamReason` normalized wireless roam reason enum.
- `dhcp.proto` — predefined protovalidate rule for DHCP option codes (`dhcp_option_code`, 1..254).
- `wired_attachment.proto` — `WiredAttachment` wired switchport attachment primitive.
- `wireless_attachment.proto` — `WirelessAttachment` wireless BSS association attachment primitive.
- `endpoint_fingerprint.proto` — `EndpointFingerprint` client DHCP and HTTP fingerprint facts.
- `endpoint_counters.proto` — `EndpointCounters` per-endpoint traffic counters and reset discontinuity.

## Sources

The package's field and enum contracts cite:

- [RFC 2132](https://www.rfc-editor.org/rfc/rfc2132.html) for DHCP option codes (Option 55 parameter request list, Option 60 vendor class).
- [IEEE Std 802.11-2020](https://standards.ieee.org/ieee/802.11/7028/) for roaming mechanisms (clauses 11.24.8 and 13.11), channel numbers, and SSIDs.
- Cisco Meraki Wireless Connection Stats API for connection failure stages (`assoc`, `auth`, `dhcp`, `dns`).
- Cisco IOS XE wireless mobility types (`Cisco-IOS-XE-wireless-mobility-types.yang`) for roaming reasons.
- FlowSeer research dossiers:
  - [01 Wi-Fi Technology](../../../../../../docs/research/schema-building-blocks/01-wifi-technology.md)
  - [03 Clients and Endpoints](../../../../../../docs/research/schema-building-blocks/03-clients-endpoints.md)
  - [09 Wi-Fi Registries](../../../../../../docs/research/schema-building-blocks/09-wifi-registries.md)
