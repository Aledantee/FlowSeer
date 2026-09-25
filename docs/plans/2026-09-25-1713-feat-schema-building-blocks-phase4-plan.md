---
title: Schema Building Blocks Phase 4, Endpoints and Port Access - Plan
type: feat
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-25-1713-feat-schema-building-blocks-plan.md
---

# Schema Building Blocks Phase 4, Endpoints and Port Access - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A host seen through the network (wireless client, wired client, or an
address from an ARP or FDB walk) is one `Endpoint` entity with its current
attachment, fingerprint, and counters, and port-access sessions from
802.1X, MAC authentication, and web authentication are one device table.

## Decisions

The record's Endpoint decision (top-level, UUID key, MAC as correlation
data, `EndpointState` and `EndpointEvent` with no Config, provenance on the
envelope) and rule 6 (`net/portaccess` is functional) govern. From
dossiers 03, 05, and 07, to be confirmed in the re-plan:

- `net/endpoint/v1` holds the wired attachment (switch by name, interface
  name, VLAN id), the wireless attachment (AP by name, BSSID, SSID, band,
  channel, RSSI and SNR in milli-units, negotiated rate, MCS, NSS, guard
  interval), the fingerprint (DHCP option 55 list, option 60 string, user
  agent; classification is a service concern), counters, and
  `ConnectionFailureStage` (association, authentication, DHCP, DNS; from
  Meraki's connection stats).
- `EndpointState` carries a repeated observed-address list rather than one
  MAC, because a randomized MAC rotates; the attachment is a required
  oneof; IPv6 addresses are repeated.
- An `EndpointEvent` covers a lifecycle transition, a roam (from and to
  attachment, a normalized roam reason), and a connection failure.
- `net/portaccess/v1.Session` is keyed by interface name and MAC with the
  method (802.1X, MAB, web authentication), authorization state, assigned
  VLAN, role name, and user name.

## Requirements

1. An `EndpointState` whose attachment has both arms fails; one with
   neither fails.
2. A UniFi wireless-client fixture from
   `spec/openapi/ubiquiti/unifi-network-openapi-v10.4.57.json`'s schema maps
   to an `EndpointState` with a wireless attachment.
3. A port-access session without `interface_name` fails.
4. `EndpointState` and `EndpointEvent` pass the proto hook's family check
   with the absent Config named in the file comment.
