# NTP Protocol Primitives

The `flowseer.net.protocol.ntp.v1` package defines Network Time Protocol (NTP)
association rows and peer selection statuses.

## Boundaries

Imports: net/addr, net/key

Imported by: nothing FlowSeer-owned

Deliberately absent:

- NTP server daemon configuration, authentication keys, and access controls.
- Per-association packet statistics and counters.
- System clock variables and leap second indicator (device-wide scalars).

## Design decisions

- **Composite key**: An association is keyed by `(network_instance, address)`.
  IOS-XE scopes peer addresses within a VRF (`vrf-name`), and OpenConfig binds
  an NTP server to a `network-instance`. Tables whose key is not interface-scoped
  carry a required `network_instance` name. Devices without VRFs report `default`.
- **Stratum range**: `stratum` is constrained to `lte 255` rather than `lte 16`.
  RFC 5905 §7.3 specifies stratum as an 8-bit integer where 0 is unspecified,
  1 is primary reference, 2..15 are secondary servers, 16 is unsynchronized,
  and 17..255 are reserved. Rejecting real values in the reserved range would
  cause the entire association row to fail validation.
- **Unit conversions**: Reporting sources express time quantities in varying
  units: IOS-XE reports `offset`, `delay`, and `jitter` in decimal milliseconds;
  OpenConfig reports nanoseconds; and NTPv4-MIB provides display strings.
  Mappers convert these into canonical protobuf `Duration` values.
- **Key collisions**: `NtpAssociation` represents an individual row. Enforcing
  uniqueness across peer associations is the responsibility of the containing
  store or service.

## Contents

- `peer_selection.proto` — `NtpPeerSelection`: clock selection status normalized from Cisco IOS-XE.
- `association.proto` — `NtpAssociation`: peer association variables and clock filter metrics.

## Sources

- RFC 5905 (<https://www.rfc-editor.org/rfc/rfc5905.html>) for NTPv4 specification,
  peer variables (§7.3), clock filter (§8, §9.1), and reachability register (§9.2).
- Cisco IOS-XE `Cisco-IOS-XE-ntp-oper.yang` for peer selection status and metrics.
- OpenConfig `openconfig-system.yang` for NTP server network-instance bindings.
- IETF `ntpv4.mib` (RFC 5907) for MIB associations table reference.
