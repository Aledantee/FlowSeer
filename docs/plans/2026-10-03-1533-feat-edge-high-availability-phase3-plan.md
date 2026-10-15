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

Group members accept inbound traffic addressed to their own endpoints or
the externally managed virtual address and report local address presence.
Central deduplicates copies of device-originated events received by several
members, including syslog and SNMP traps. Sender tests prove fallback,
fan-out, cross-node event deduplication, and virtual-address failover,
and FreeBSD checks prove the supported agent path.

Stop condition: the phase cannot prove its device or receiver behavior from
source evidence and an independent observation.

## Decisions

The [parent's decisions](2026-10-03-1533-feat-edge-high-availability-plan.md) apply.

- Inbound records retain the actual receiving Edge as provenance. Why:
  intake validates the authenticated producer, not an address-holder claim.
- Every member accepts traffic on its individual endpoint even when it does
  not hold the virtual address. Why: the parent permits device-configured
  primary and fallback destinations independently of address movement.
- Cross-node deduplication applies to every supported device-event producer,
  including syslog and SNMP traps. Why: the source event and its receipts
  have different identities regardless of notification protocol. Existing
  syslog illustrates the gap: `syslogsource/source.go` creates a new UUID for
  each receipt, while `intake.prepare` uses tenant plus that UUID as its
  publication identity. Re-plan must define protocol-specific source-event
  identity and receipt provenance. A future event producer must meet the
  same contract before it is supported.
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
6. A sender configured with node A as primary syslog destination and B and C
   as fallbacks delivers to B after A stops. Example: B receives on its own
   address while holding neither the virtual address nor the device read
   assignment. Central records B as provenance without changing placement.
   Verify the selected sender's actual fallback behavior before claiming
   device-family support.

7. One source event delivered to A, B, and C produces one operator-visible
   event, including when central replicas accept the copies concurrently.
   Run this case for syslog and SNMP traps: each source event counts once,
   with all three receiving Edges represented in its receipt evidence.
   Validate every receipt before combining evidence.
8. For each event type, a delayed duplicate within its declared replay
   horizon stays deduplicated after central restart. Example: A delivers
   immediately and B replays its buffered copy after recovery, without
   adding another event to history.
9. Deduplication does not merge different devices, tenants, or event types,
   or distinct occurrences with identical payloads. Example: two separate
   traps with equal payloads remain two events even when both reach all
   nodes. Re-plan must state the policy for source data that cannot
   distinguish occurrences, rather than treating a payload hash as proof
   of identity.
10. Deduplication preserves receiver provenance and separately requested raw
    evidence. Example: B's paid raw-window copy remains available even when
    its typed event has already arrived through A.
11. Receipt handling remains independent of event deduplication. Example:
    where the selected protocol requires a response, a duplicate receipt
    still completes that response while producing no second logical event.
    Re-plan names the protocol-specific source and acceptance fixture.

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

- Canonical source-event identity for each protocol, handling of
  indistinguishable occurrences, bounded replay horizons, and durable
  coordination across central replicas. Receiver UUIDs, receipt timestamps,
  and payload equality alone do not establish one source event.
- The trap producer and ingestion boundary.
  `spec/proto/flowseer/integration/ingest/v1/ingest_record.proto` currently
  carries only a syslog payload. Re-plan must name the trap schema and its
  producer rather than assuming trap ingestion already exists.
- The schema and storage contract for one logical event with multiple receipt
  provenances, and any amendments to the accepted ingestion/history records.
- Security identity for individual endpoints alongside the shared virtual
  endpoint, and which sender supplies evidence for ordered syslog fallback.
- Receiver identity schema, secret rotation, and the authoritative engine
  boots/time strategy. Protocol guarantees remain unverified until sourced.
- Whether an offline restart can recover receiver secrets without violating
  the existing no-device-secret-on-disk decision. The initial behavior is
  unready until fetched, not invented persisted secrets.
- Which real sender and FreeBSD environment provide acceptance evidence.
- The relevant landed delivery transport after Edge Delivery over Connect.
