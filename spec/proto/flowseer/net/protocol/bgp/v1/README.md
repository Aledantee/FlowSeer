# BGP Protocol Primitives

The `flowseer.net.protocol.bgp.v1` package models Border Gateway Protocol (BGP)
instances, peer sessions, address families, and Loc-RIB paths with communities.

## Boundaries

Imports: net/addr, net/key

Imported by: nothing

Deliberately absent:

- BGP Adj-RIB-In and Adj-RIB-Out views (atlas, "The shared shape": adjacency first, databases rarely).
- Non-unicast paths and multi-protocol route distribution beyond IPv4 and IPv6 unicast prefixes.
- Extended communities (RFC 4360), route targets, and site-of-origin attributes.
- BGP NOTIFICATION error codes and subcodes.
- Peer groups, template configuration, and routing policy.

## Design decisions

- **Composite keys**: Instance-level rows (`BgpInstance`, `BgpPeer`, `BgpPath`)
  are keyed by `(network_instance, protocol_instance)`. The mapper names the
  protocol instance as the device names it, and `default` when the device has no
  name for it (such as a single BGP process).
- **Peer sessions**: `BgpPeer` models one BGP neighbor connection, carrying
  local and remote addresses and ASNs, router identifiers, timers, active
  address families, and message counters. An IPv6 link-local remote address
  requires naming the interface it is scoped by.
- **Paths and attributes**: `BgpPath` carries an IPv4 or IPv6 unicast Loc-RIB
  path with its origin, AS path segments, next hop, MED, local preference,
  communities, and best-path selection.
- **Typed communities**: Standard and large communities are unified in the
  `BgpCommunity` typed variant, a required oneof of 32-bit standard community
  or 12-octet `BgpLargeCommunity`. Both arms at once cannot be represented on
  the wire, so validation rejects the empty case only.
- **Origin offset**: `BgpOrigin` preserves the BGP4-MIB integers `IGP = 1`,
  `EGP = 2`, `INCOMPLETE = 3`. Because RFC 4271 §4.3 defines wire values 0 to 2,
  a mapper decoding the attribute adds one (+1) to obtain the schema value,
  leaving zero for "unreported".

## Contents

- `bgp_afi.proto` — `BgpAfi`: IANA address family numbers for BGP.
- `bgp_as_path_segment_type.proto` — `BgpAsPathSegmentType`: AS path segment types (AS_SET, AS_SEQUENCE, confederation types).
- `bgp_community.proto` — `BgpCommunity`, `BgpLargeCommunity`: standard and large community attributes.
- `bgp_instance.proto` — `BgpInstance`: BGP routing process instance and local ASN.
- `bgp_origin.proto` — `BgpOrigin`: BGP path origin attribute.
- `bgp_path.proto` — `BgpPath`, `BgpAsPathSegment`: Loc-RIB path attributes and AS path segments.
- `bgp_peer_state.proto` — `BgpPeerState`: BGP peer connection finite state machine states.
- `bgp_peer.proto` — `BgpPeer`, `BgpPeerAddressFamily`, `BgpPeerCounters`: peer neighbor sessions, negotiated families, and counters.
- `bgp_safi.proto` — `BgpSafi`: IANA subsequent address family identifiers.

## Sources

- RFC 4271 (<https://www.rfc-editor.org/rfc/rfc4271.html>) for BGP-4 specification,
  FSM states, path attributes, and message formats.
- RFC 1997 (<https://www.rfc-editor.org/rfc/rfc1997.html>) for BGP Communities Attribute.
- RFC 5065 (<https://www.rfc-editor.org/rfc/rfc5065.html>) for Autonomous System Confederations for BGP.
- RFC 6286 (<https://www.rfc-editor.org/rfc/rfc6286.html>) for Autonomous-System-Independent BGP Identifier.
- RFC 6793 (<https://www.rfc-editor.org/rfc/rfc6793.html>) for 4-Octet AS Numbers.
- RFC 7911 (<https://www.rfc-editor.org/rfc/rfc7911.html>) for BGP ADD-PATH.
- RFC 8092 (<https://www.rfc-editor.org/rfc/rfc8092.html>) for BGP Large Communities Attribute.
- IETF BGP4-MIB (`spec/mib/ietf/BGP4-MIB`) for peer and path attribute table semantics.
- IANA registries for Address Family Numbers and SAFI Parameters.
- OpenConfig `openconfig-bgp.yang` and `openconfig-rib-bgp-tables.yang`.
- Cisco IOS-XE `Cisco-IOS-XE-bgp-oper.yang`.
