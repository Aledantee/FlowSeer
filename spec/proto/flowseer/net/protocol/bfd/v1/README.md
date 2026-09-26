# BFD Protocol Primitives

The `flowseer.net.protocol.bfd.v1` package models Bidirectional Forwarding
Detection (BFD) sessions and diagnostics (RFC 5880, RFC 5881, RFC 5883, RFC 7330).

## Boundaries

Imports: net/addr, net/key

Imported by: nothing

Deliberately absent:

- BFD echo function operational packets and echo mode state machines (RFC 5880 §6.8.9).
- BFD cryptographic authentication parameters (Key ID, sequence numbers, SHA-1/MD5 digests).
- BFD for MPLS Label Switched Paths (RFC 5884) or LAG (RFC 7130).

## Design decisions

- **Composite key**: `BfdSession` is keyed by `(network_instance, local_discriminator)`.
  RFC 5880 §6.8.1 mandates that `local_discriminator` must be unique across all
  BFD sessions on the system and nonzero (`gte = 1`). `remote_discriminator`
  is 0 until learned from incoming BFD control packets.
- **Pass-through enums with real zero**: `BfdSessionState` preserves the RFC 5880 §4.1
  state integers with `ADMIN_DOWN = 0`. `BfdDiagnostic` preserves the RFC 5880 §4.1
  diagnostic codes with `NO_DIAGNOSTIC = 0`. Presence carries "unreported";
  consumers must check `HasState()` / `HasLocalDiagnostic()` before reading the
  generated getters.
- **MIB offset and failing state**: The IETF BFD-STD-MIB (`bfdSessState`) shifts
  RFC 5880 states by one (`adminDown(1)` to `up(4)`) and adds `failing(5)` for BFD
  version 0. A mapper reading SNMP MIB state subtracts one (-1) and leaves
  `failing` absent.
- **Interval representation**: Wire timers in BFD are expressed in microseconds
  (up to 2³² - 1 microseconds, or approximately 4294.967295 s). The schema models
  them as `google.protobuf.Duration` types bounded to `[0, 4294.967295s]`,
  preserving exact microsecond fidelity across SNMP and YANG sources.
- **Session types and interface scoping**: `BfdSessionType` models single-hop and
  multi-hop modes. For single-hop sessions, `interface_name` identifies the local
  interface; for multi-hop sessions, `interface_name` is absent.
- **Address family consistency**: When both `local_address` and `remote_address`
  are present, they must share the same IP address family (both IPv4 or both IPv6).

## Contents

- `bfd_diagnostic.proto` — `BfdDiagnostic`: diagnostic reason codes for session state transitions.
- `bfd_session_state.proto` — `BfdSessionState`: BFD session finite state machine states with real zero.
- `bfd_session_type.proto` — `BfdSessionType`: BFD session topologies (single-hop, multi-hop variants).
- `bfd_session.proto` — `BfdSession`, `BfdSessionCounters`: BFD session configuration, state, intervals, and counters.

## Sources

- RFC 5880 (<https://www.rfc-editor.org/rfc/rfc5880.html>) for Bidirectional Forwarding Detection (BFD).
- RFC 5881 (<https://www.rfc-editor.org/rfc/rfc5881.html>) for BFD for IPv4 and IPv6 (Single Hop).
- RFC 5883 (<https://www.rfc-editor.org/rfc/rfc5883.html>) for BFD for Multihop Paths.
- RFC 7330 (<https://www.rfc-editor.org/rfc/rfc7330.html>) for BFD-STD-MIB and IANA-BFD-TC-STD-MIB.
- OpenConfig `openconfig-bfd.yang`.
- Cisco IOS-XE `Cisco-IOS-XE-bfd-oper.yang`.
