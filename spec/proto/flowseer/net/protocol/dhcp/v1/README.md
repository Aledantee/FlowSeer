# DHCP Protocol Primitives

The `flowseer.net.protocol.dhcp.v1` package models Dynamic Host Configuration
Protocol (DHCPv4 and DHCPv6) server leases, pools, message counters, and DHCP
snooping bindings.

## Boundaries

Imports: net/addr, net/key, net/switching

Imported by: nothing

Deliberately absent:

- DHCP client state (a device's own address assignments carry their origin in `net/ip/v1`).
- DHCP relay agent operational states and forwarder statistics.
- DHCPv6 server pools, prefix delegation pools, and option sets.
- Client fingerprinting and vendor option definitions (handled in `net/endpoint/v1/dhcp.proto`).

## Design decisions

- **Composite keys**:
  - `Dhcpv4Lease` is keyed by `(network_instance, address)`.
  - `Dhcpv4Pool` is keyed by `(network_instance, name)`.
  - `Dhcpv6Binding` is keyed by `(network_instance, duid)`.
  - `DhcpSnoopingBinding` is keyed by `(network_instance, vlan_id, mac, address)`.
- **Message type counts as lists**: Both DHCPv4 and DHCPv6 server statistics are
  modeled as lists of per-message-type counters (`Dhcpv4ServerCounters`,
  `Dhcpv6ServerCounters`) rather than fixed schema fields. This accommodates new
  message types registered in IANA registries without schema modifications.
- **IA_NA and IA_PD separation**: In DHCPv6, identity associations for non-temporary
  addresses (`Dhcpv6IaNa`) and prefix delegation (`Dhcpv6IaPd`) are modeled as
  separate structures within `Dhcpv6Binding`, each carrying lifetimes via `IpLifetime`.
- **Expiry oneof**: Lease expiry on `Dhcpv4Lease` and pool lease durations use a
  non-required `expiry` oneof (`expires_at` timestamp versus `infinite = true`).
  This avoids overloading absence to mean both infinite and unreported.
- **Snooping bindings**: `DhcpSnoopingBinding` models Layer 2 security snooping
  entries across vendors (Huawei, H3C, Cisco). The remaining lease duration is
  absent for static bindings or when unreported.

## Contents

- `dhcp_allocation.proto` — `DhcpAllocation`: DHCP lease allocation mechanisms (automatic, dynamic, manual).
- `dhcp_snooping_binding_kind.proto` — `DhcpSnoopingBindingKind`: snooping entry origin (static, dynamic).
- `dhcp_snooping_binding.proto` — `DhcpSnoopingBinding`: DHCP snooping security table bindings.
- `dhcpv4_lease.proto` — `Dhcpv4Lease`: DHCPv4 server lease records with expiry oneof.
- `dhcpv4_message_type.proto` — `Dhcpv4MessageType`: DHCPv4 message types.
- `dhcpv4_pool.proto` — `Dhcpv4Pool`: DHCPv4 server pool configurations and address counters.
- `dhcpv4_server_counters.proto` — `Dhcpv4ServerCounters`, `Dhcpv4MessageCount`: DHCPv4 server message statistics.
- `dhcpv6_binding.proto` — `Dhcpv6Binding`, `Dhcpv6IaNa`, `Dhcpv6IaPd`: DHCPv6 client bindings, IA_NA, and IA_PD.
- `dhcpv6_message_type.proto` — `Dhcpv6MessageType`: DHCPv6 message types.
- `dhcpv6_server_counters.proto` — `Dhcpv6ServerCounters`, `Dhcpv6MessageCount`: DHCPv6 server message statistics.

## Sources

- RFC 2131 (<https://www.rfc-editor.org/rfc/rfc2131.html>) for Dynamic Host Configuration Protocol (DHCPv4).
- RFC 2132 (<https://www.rfc-editor.org/rfc/rfc2132.html>) for DHCP Options and BOOTP Vendor Extensions.
- RFC 8415 (<https://www.rfc-editor.org/rfc/rfc8415.html>) for Dynamic Host Configuration Protocol for IPv6 (DHCPv6).
- Cisco IOS-XE `Cisco-IOS-XE-dhcp-oper.yang`.
- Huawei `HUAWEI-DHCPS-MIB` and `HUAWEI-DHCP-SNOOPING-MIB`.
- H3C `HH3C-DHCP-SNOOP2-MIB`.
- FASTPATH `fastpath_dhcp6.mib`.
- MikroTik RouterOS OpenAPI (`spec/openapi/mikrotik/routeros-7.24-openapi.json`).
