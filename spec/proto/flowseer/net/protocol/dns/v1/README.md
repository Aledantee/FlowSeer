# DNS Protocol Primitives

The `flowseer.net.protocol.dns.v1` package models Domain Name System (DNS)
client resolver configuration, nameservers, and domain search lists.

## Boundaries

Imports: net/addr, net/key

Imported by: nothing

Deliberately absent:

- DNS server operational daemon state, zones, and resource records (DNS-SERVER-MIB).
- Static host name mappings (/etc/hosts).
- Resolver caches and negative response caches.

## Design decisions

- **Per-instance resolver**: `DnsResolver` is keyed by `network_instance`. Each
  network instance maintains its own list of preference-ordered nameservers and
  search domains.
- **Search domain bounds and syntax**: Search domains are bounded to 1 to 253
  characters (RFC 1035 §2.3.4, RFC 2181 §11 wire limit of 255 octets). Search
  domains are not restricted to hostname syntax because RFC 2181 §11 explicitly
  forbids restricting labels in the DNS protocol.
- **Nameservers**: `DnsServer` identifies server endpoints by `IpAddress`. The
  `port` field defaults to 53 when omitted; for IPv6 link-local addresses,
  `interface_name` scopes the server address.

## Contents

- `dns_resolver.proto` — `DnsResolver`, `DnsServer`: resolver configuration, nameservers, and search domains.
- `dns_server_origin.proto` — `DnsServerOrigin`: source of server configuration (static, DHCP).

## Sources

- RFC 1035 (<https://www.rfc-editor.org/rfc/rfc1035.html>) for Domain Names - Implementation and Specification.
- RFC 2181 (<https://www.rfc-editor.org/rfc/rfc2181.html>) for Clarifications to the DNS Specification.
- OpenConfig `openconfig-system.yang`.
- Cisco IOS-XE `Cisco-IOS-XE-dns-oper.yang`.
- IETF DNS-RESOLVER-MIB (`spec/mib/ietf/DNS-RESOLVER-MIB`).
