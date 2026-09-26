# VRRP Protocol Primitives

The `flowseer.net.protocol.vrrp.v1` package models Virtual Router Redundancy
Protocol (VRRP) groups for VRRPv2 (RFC 3768) and VRRPv3 (RFC 5798).

## Boundaries

Imports: net/addr, net/key

Imported by: nothing

Deliberately absent:

- VRRP authentication schemes (RFC 2338/3768 simple text or MD5; deprecated in RFC 5798 §9).
- Tracked objects, interface priority tracking, and sub-second failover policies.
- Proprietary first-hop redundancy protocols (HSRP, GLBP, VSRP); Cisco HSRP will reside in its own package when introduced.

## Design decisions

- **Composite key**: `VrrpGroup` is keyed by `(interface_name, address_family, vrid)`.
  VRRP groups are identified per interface and IP address family (IPv4 or IPv6)
  with a Virtual Router Identifier (VRID) between 1 and 255.
- **Address family and version bounds**: VRRPv2 supports IPv4 only; a VRRPv2
  group with `address_family` set to `IP_VERSION_V6` is rejected. All addresses
  associated with the group (`master_address`, `primary_address`, and
  `virtual_addresses`) must match the group's configured address family.
- **Advertisement intervals and conversions**: VRRPv3 specifies intervals in
  centiseconds (multiples of 10 ms up to 40.95 s, RFC 5798 §5.2.7), while VRRPv2
  specifies whole seconds up to 255 s (RFC 3768 §5.3.7). The field uses a
  `google.protobuf.Duration` bounded to multiples of 10 ms and capped per protocol
  version. Mappers converting from milliseconds (such as IOS-XE) divide by 10 to
  reach centiseconds without loss of precision.
- **Priority reserved meanings**: Priority takes values 0 to 255. Priority 255
  indicates the router owns the virtual IPv4/IPv6 addresses; priority 0 is sent
  by the Master to signal it has ceased participating so Backup routers quickly
  transition to Master without waiting for timeout (RFC 5798 §5.2.4).
- **HSRP separation**: Cisco HSRP does not share these tables and will be modeled
  in its own package in a subsequent phase.

## Contents

- `vrrp_group.proto` — `VrrpGroup`, `VrrpGroupCounters`: VRRP router group configuration, state, timers, and transition counters.
- `vrrp_state.proto` — `VrrpState`: VRRP router operational FSM states (Initialize, Backup, Master).

## Sources

- RFC 3768 (<https://www.rfc-editor.org/rfc/rfc3768.html>) for Virtual Router Redundancy Protocol (VRRP) version 2.
- RFC 5798 (<https://www.rfc-editor.org/rfc/rfc5798.html>) for Virtual Router Redundancy Protocol (VRRP) Version 3 for IPv4 and IPv6.
- IETF VRRP-MIB (`spec/mib/ietf/VRRP-MIB`) for VRRPv2 MIB definitions.
- IETF VRRPV3-MIB (`spec/mib/ietf/VRRPV3-MIB`) for VRRPv3 MIB definitions.
- Cisco IOS-XE `Cisco-IOS-XE-vrrp-oper.yang`.
