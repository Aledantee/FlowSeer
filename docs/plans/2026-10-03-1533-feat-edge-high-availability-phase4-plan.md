---
title: Edge High Availability, Phase 4, Verified Device Fencing and Write Failover - Plan
type: feat
date: 2026-10-15
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Edge High Availability, Phase 4, Verified Device Fencing and Write Failover - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

Central changes a device's write owner only after a successor proves the
predecessor's accounts disabled and sessions terminated using a verified
device-family procedure, while the lane's unresolved mutation stays ordered.

Stop condition: the phase cannot prove its device or receiver behavior from
source evidence and an independent observation.

## Decisions

The [parent's decisions](2026-10-03-1533-feat-edge-high-availability-plan.md) apply.

- The parent's per-node account fence remains the chosen mechanism. A
  lease expiry, placement generation, or lost heartbeat cannot replace it.
- Every device family starts unsupported. Why: no disable-and-terminate
  procedure has been verified. A missing capability keeps writes unavailable.
- Fencing is a separately privileged typed operation with durable evidence.
  Why: it changes device access before successor mutation authority exists.
- Receiver identity is not a prerequisite. Why: a positive outbound fence
  must not depend on which node receives inbound datagrams.

## Requirements

1. A paused predecessor that resumes after the fence cannot authenticate
   with any issued account or continue an existing write session. Source
   evidence and live-device observation must establish this for each profile.
2. The fence names predecessor Edge and enrollment, device, account versions,
   and intended ownership generation. Example: evidence for another account
   version or enrollment cannot authorize the move.
3. Unknown or partial fence outcomes leave successor submission refused.
   Example: disable succeeds, session enumeration fails, central restarts,
   and writes remain blocked.
4. Ownership changes and fence evidence are persisted through the journal's
   compare-and-set. Example: two successors race and only one gains authority.
5. A prior unresolved mutation is reconciled before any new write. Example:
   predecessor submitted before fencing, its delayed effect is observed and
   resolved within the existing horizon rules before successor applies more.
6. A returning node's accounts stay disabled until a separately authorized,
   reconciled account operation re-enables them. Onboarded alone is no fence
   reversal or write grant.

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

- Which device family's documented disable and session termination procedure
  can be proven against the actual target, including delayed device effects.
- Fence operation/grant schema, recovery authority during a blocked mutation,
  evidence lifetime, and refusal behavior for missing account visibility.
- Account re-enable policy and how to prevent a late re-enable operation from
  undoing a newer fence.
- Source and lab evidence for all device behavior. Unverified. Any live-device
  write requires its own stated blast radius and explicit approval.
