# AAA Primitives

The `flowseer.net.aaa.v1` package defines the RADIUS and TACACS+ servers a
device is configured to use: `AaaServer`, with a `RadiusServer` or
`TacacsServer` arm, and `AaaServerStatus`.

## Boundaries

Imports: net/addr

Imported by: nothing

Deliberately absent:

- Shared secrets, keys, hashed secrets, and passwords. A server's secret is
  credential material: it lives in the secret store behind a credential ref
  and is carried only as `model/credential` material, never in a `net/` row.
  OpenConfig's `secret-key` and `secret-key-hashed` leaves have no
  counterpart here, and a conformance test fails if a field name in this
  package contains `secret`, `key`, `password`, or `passphrase`.
- Server statistics (the RFC 4668 counters and round-trip time), method
  lists, and per-command authorization.
- Network instance key. A server is identified by its address; the VRF a
  device reaches it through is not reported by OpenConfig AAA, so a required
  field would force a mapper to claim `default` it cannot see.

## Contents

- `aaa_server.proto`: `AaaServer`, one configured server with the common
  fields and a required `protocol` oneof.
- `radius_server.proto`: `RadiusServer`, the RADIUS ports and retransmit
  count.
- `tacacs_server.proto`: `TacacsServer`, the TACACS+ port.
- `aaa_server_status.proto`: `AaaServerStatus`, alive or dead.

This package is server identity only. A server is keyed by its IP address,
because no source names one by hostname. Ports are bounded by the MIBs'
`0..65535` and nothing more, and an absent port means the source did not
report one, not that the RFC default applies. `priority` is lower-first, as
D-Link reports it.

Comware and Huawei key AAA by ISP domain, each domain naming its own server
schemes. A normalized `AaaServer` list keeps the servers and their groups but
not the domain scheme, so a mapper for those platforms does not round-trip it.

## Sources

- OpenConfig AAA (`spec/yang/openconfig/openconfig-aaa.yang`,
  `openconfig-aaa-radius.yang`, `openconfig-aaa-tacacs.yang`) for server
  groups, the common server fields, and the per-protocol containers.
- `RADIUS-AUTH-CLIENT-MIB` (`spec/mib/ietf/`) for the server address and port
  ranges.
- IOS-XE `Cisco-IOS-XE-aaa-oper.yang` (`spec/yang/cisco/iosxe/2611/`) for
  server keys and alive and dead states.
- D-Link `DLINKSW-AAA-SERVER-MIB` (`spec/mib/dlink/`) for server priority.
- [RFC 2865](https://www.rfc-editor.org/rfc/rfc2865.html),
  [RFC 2866](https://www.rfc-editor.org/rfc/rfc2866.html), and
  [RFC 8907](https://www.rfc-editor.org/rfc/rfc8907.html) for RADIUS
  authentication, RADIUS accounting, and TACACS+.
