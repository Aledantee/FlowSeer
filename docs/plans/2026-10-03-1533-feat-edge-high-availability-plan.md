---
title: Edge High Availability - Plan
type: feat
date: 2026-10-03
artifact_contract: flowseer-plan/v2
execution: mixed
amends: docs/architecture/2026-09-05-verified-device-access-direction.md
---

# Edge High Availability - Plan

## Goal

A Site runs several enrolled Edges, distributes device reads across them,
and keeps reads available when a node stops. Later phases provision each
node's device accounts through typed mutations, provide the shared inbound
receiver identity, and enable write failover only for verified device fences.

Stop condition: no successor may receive write authority while a predecessor
can still write to the device.

## Decisions

The [edge HA direction](../architecture/2026-10-03-edge-high-availability-direction.md)
is proposed. It records the staged design and awaits acceptance. The
[verified access direction](../architecture/2026-09-05-verified-device-access-direction.md),
decisions 8 and 9, still requires positive fencing and per-operation secrets.

The existing decisions remain:

- A group has one to n nodes. (decided by the user, 2026-10-03)
- The nodes share a virtual IP for syslog and traps. (decided by the user,
  2026-10-03)
- Every node does outbound device access, and central places the lanes.
  (decided by the user, 2026-10-03)
- The virtual IP is moved outside FlowSeer, by CARP for now. (decided by the
  user, 2026-10-03)
- FreeBSD is a deployment target for the agent. (decided by the user,
  2026-10-03)
- A group is in a Site. (decided by the user, 2026-10-03)
- Device credentials reach a node on demand and are never stored. (decided
  by the user, 2026-10-03)
- The fence is the old node's own account on the device: every node has its
  own account, and the successor disables the old one, closes its sessions,
  and reads both back. (decided by the user, 2026-10-03)

- First deliver group placement and read failover. Writes remain bound to
  their original node until a separately verified fence exists. Why: the
  device-specific fence procedure has not been verified for any family
  (decided by the user, 2026-10-15).
- FlowSeer provisions per-node accounts through typed mutations. Why:
  account lifecycle belongs to the managed configuration, rather than an
  operator-maintained prerequisite (decided by the user, 2026-10-15).
- Keep central HA as a prerequisite. Why: placement must work when the
  dispatch connection and placement controller run on different replicas
  (decided by the user, 2026-10-15).
- Add a minimal Site entity with typed refs and group membership. Why:
  Edge and group ownership must refer to the same Site contract. Broader
  inventory administration waits (decided by the user, 2026-10-15).
- Each group contains one to 64 distinct Edges within one Site and one
  tenant. Why: bounded registry validation and deterministic assignment
  need a finite membership set. Tenant scope remains authenticated context.
- Read placement and write ownership are separate durable facts. Why:
  `dispatchapi/relay.go` currently derives both from `Resolver.Devices`,
  and changing that list alone would resend an open mutation to a successor.
- Initial assignments spread devices evenly across healthy members. An
  automatic read move does not rebalance healthy devices or move them back
  when a member returns. Why: recovery should not create another outage.
- The existing `edge_stale_after` threshold, default three minutes, controls
  read failover. Why: `edgeapi/contact.go` already derives contact from
  heartbeat timestamps. Configure that threshold rather than introducing
  a competing clock.
- Phases 2 through 4 require re-planning against their landed predecessors.
  Why: device account management and receiver security contracts do not yet
  exist. Their requirements below stay in scope without inventing vendor
  guarantees or duplicating the FreeBSD packet I/O work.

## Requirements

1. A group and every member name one Site. Example: loading a group with a
   member from another Site or tenant fails before any dispatch.
2. Reads survive a node loss. Example: after A becomes stale, B receives a
   new read assignment within one placement interval, with a new sequence
   for each retried open read. Its result is queryable through central.
3. Write ownership survives read movement. Example: A holds a submitted
   mutation when its reads move to B. B receives no mutation, submission
   credential, checkpoint, or mutation recovery work for that lane.
4. Placement generations prevent old read reports from changing current
   state. Example: A to B to A does not accept A's first generation report.
   The check and the journal update occur in the same compare-and-set.
5. Only an authorized operation owner obtains a read credential.
   Example: C's request for B's read sequence and generation is refused.
   Typed onboarding probes check the corresponding read or write owner.
   Credentials already issued cannot be recalled by a placement change.
6. Account management is typed and recoverable. Example: interrupted account
   creation is reconciled from a fresh device read before retry, preserves
   an administrative recovery path, and never enables write failover itself.
7. A write moves only after its device-specific procedure verifies disabled
   predecessor accounts and absent sessions. Example: a partial or unknown
   fence outcome leaves successor write authority refused across restart.
8. Inbound traffic follows the externally managed address. Example: records
   accepted by B after address movement retain B as provenance, while A's
   older buffered records can still arrive. Address holder is not lane owner.
9. Receiver identity and FreeBSD behavior are verified independently of
   placement. Example: a real inform sender accepts the receiver after an
   address move, and a FreeBSD agent passes the selected end-to-end checks.

## Out of scope

- Moving the virtual IP, cross-subnet groups, and delivery of datagrams lost
  during address movement.
- A general Site inventory API, automatic inventory discovery, and adapter
  hosts without a Site.
- Treating central report generations or expired credentials as a device
  fence. Group members are enrolled participants, not mutually trusted with
  unrestricted device credentials. A compromised member that already holds
  a credential is outside the read-placement authorization guarantee.

## Units

### U1. Site, groups, and read failover
Files: docs/plans/2026-10-03-1533-feat-edge-high-availability-phase1-plan.md
After: none
Change: the first phase implements group identity and durable read placement after central HA.
Tests: the phase names the schema, placement, credential, and restart checks.
Verify: `uv run tools/scripts/run.py verify change -- docs/plans/2026-10-03-1533-feat-edge-high-availability-phase1-plan.md`

### U2. Typed per-node account lifecycle
Files: docs/plans/2026-10-03-1533-feat-edge-high-availability-phase2-plan.md
After: U1
Change: FlowSeer provisions and reconciles node-specific device accounts before they become failover credentials.
Tests: the phase defines account interruption and recovery acceptance cases.
Verify: `uv run tools/scripts/run.py verify change -- docs/plans/2026-10-03-1533-feat-edge-high-availability-phase2-plan.md`

### U3. Shared receiver identity and platform validation
Files: docs/plans/2026-10-03-1533-feat-edge-high-availability-phase3-plan.md
After: U1
Change: group receiver identity and address-holder reporting support inbound failover.
Tests: the phase requires real sender and FreeBSD evidence.
Verify: `uv run tools/scripts/run.py verify change -- docs/plans/2026-10-03-1533-feat-edge-high-availability-phase3-plan.md`

### U4. Verified device fencing and write failover
Files: docs/plans/2026-10-03-1533-feat-edge-high-availability-phase4-plan.md
After: U2
Change: verified device-family fences permit successor write ownership.
Tests: the phase requires a paused predecessor, session termination evidence, and durable refusal on ambiguous fences.
Verify: `uv run tools/scripts/run.py verify change -- docs/plans/2026-10-03-1533-feat-edge-high-availability-phase4-plan.md`

Waves: U1 | U2 U3 | U4

## Verification

Each phase runs its focused checks and diff-aware verifier. Schema phases
finish with the full verifier. Device-family evidence requires a separately
approved live-device write with its blast radius stated first. No such
write is authorized by this plan.

## Definition of done

- Every phase has an accepted review, compound outcome, and landed range.
- The accepted access and inventory direction amendments land with code.
- Package READMEs and lab instructions describe actual supported devices.
- No plan labels enter code or commit messages.

## Open questions

- Accept or amend the proposed edge HA direction before implementation.
- Device account protocols, recovery credentials, and supported device
  families are settled when phase 2 is re-planned.
- Inform engine boots/time, receiver secret startup, and platform behavior
  need source evidence and sender tests in phase 3.
- No device family's account disable and session termination procedure is
  verified. Phase 4 cannot be made ready from a self-authored fixture alone.
