---
title: Edge High Availability, Phase 3, Shared Receiver Identity and FreeBSD Validation - Plan
type: feat
date: 2026-10-15
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Edge High Availability, Phase 3, Shared Receiver Identity and FreeBSD Validation - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

All group members receive the inbound identity and report whether the
externally managed virtual address is present locally. Sender tests prove
receiver failover, and FreeBSD checks prove the supported agent path.

Stop condition: the phase cannot prove its device or receiver behavior from
source evidence and an independent observation.

## Decisions

The [parent's decisions](2026-10-03-1533-feat-edge-high-availability-plan.md) apply.

- Inbound records retain the actual receiving Edge as provenance. Why:
  intake validates the authenticated producer, not an address-holder claim.
- The address holder never selects the outbound read or write owner. Why:
  placement remains central's authority under the parent decisions.
- FreeBSD packet I/O phase 1 supplies the agent build and platform gate.
  This phase validates the HA combination rather than copying its build work.
- A shared SNMP engine ID alone establishes no failover guarantee. Engine
  boots/time and user state require protocol source evidence and sender tests.

## Requirements

1. A heartbeat carries a typed per-group address-presence observation.
   Example: both nodes claim the address and outbound ownership is unchanged.
2. Receiver secrets are fetched into memory before a secure listener starts.
   Example: cold startup without central leaves that listener unready, while
   already-buffered records follow the existing delivery policy.
3. A TLS syslog sender can reconnect through the virtual address after it
   moves, with the same configured trust identity and actual Edge provenance.
4. A real SNMPv3 inform sender accepts acknowledgements before and after a
   receiver move. Example: engine boots/time continuity or rediscovery obeys
   the verified sender protocol, including replay refusal.
5. FreeBSD runs the selected heartbeat, read failover, receiver, and restart
   checks against real kernel networking. A Linux-only fixture is insufficient.

## Out of scope

- Changing the parent's settled account ownership or positive fence decision.
- Claiming support from a round-trip fixture authored alongside the adapter.

## Verification

Re-plan defines concrete files, focused tests, and the verifier command
against the landed preceding phase. Device writes need separate approval.

## Definition of done

- Requirements become concrete implementation units during re-plan.
- Supported behavior has source evidence, acceptance checks, and verifier receipts.
- The phase records its implemented units and landed range through `plan record`.

## Open questions

- Receiver identity schema, secret rotation, and the authoritative engine
  boots/time strategy. Protocol guarantees remain unverified until sourced.
- Whether an offline restart can recover receiver secrets without violating
  the existing no-device-secret-on-disk decision. The initial behavior is
  unready until fetched, not invented persisted secrets.
- Which real sender and FreeBSD environment provide acceptance evidence.
- The relevant landed delivery transport after Edge Delivery over Connect.
