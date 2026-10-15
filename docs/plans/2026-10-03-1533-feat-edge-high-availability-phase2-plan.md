---
title: Edge High Availability, Phase 2, Typed Node Account Lifecycle - Plan
type: feat
date: 2026-10-15
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Edge High Availability, Phase 2, Typed Node Account Lifecycle - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

FlowSeer creates, verifies, rotates, disables, and reconciles each node's
accounts on managed devices through typed mutations. The existing write
owner performs the bootstrap operations through the device lane.

Stop condition: the phase cannot prove its device or receiver behavior from
source evidence and an independent observation.

## Decisions

The [parent's decisions](2026-10-03-1533-feat-edge-high-availability-plan.md) apply.

- The parent's account ownership decision applies. Operators supply an
  administrative bootstrap credential through the existing credential
  carrier, not raw strings in registry config. Why: account creation needs
  authority before the per-node account exists.
- This phase creates no automatic write failover. Why: provisioning an
  account is not evidence that disabling it terminates every session.
- The supported device families and account protocols require source and
  lab evidence at re-plan. No vendor support is assumed.

## Requirements

1. Account identity includes the device, Edge, and enrollment. Example: a
   re-enrolled Edge does not receive the prior enrollment's account secret.
2. Create and rotate operations have typed intent, observed state, and
   terminal outcomes under the existing mutation barrier. Example: a lost
   response triggers a fresh read and reconciliation before retry.
3. A failed bootstrap preserves an administrative recovery path. Example:
   credential verification fails and central does not switch that node's
   access policy to the unverified account.
4. Removal or rotation cannot invalidate an unresolved mutation's recovery
   credential without an explicit recovery procedure. Example: rotation
   during POSSIBLY_APPLIED stays blocked rather than destroying evidence.
5. Account secrets remain in the secret provider and per-operation delivery.
   Example: account creation logs and persisted device config contain only
   handles, with no credential material.
6. Devices whose account capacity or protocol behavior does not support the
   group fail admission before any partial rollout is treated as complete.

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

- Which device family and protocol form the first supported profile.
- The typed account intent and observation contract, including persistent
  secret ownership, bootstrap credential scope, privilege limits, and
  crash recovery when device creation succeeds before secret publication.
- Source evidence for account capacity, verify/rotate behavior, and a
  recovery credential that cannot lock out the operator. Unverified.
- The per-node read and write account relationship and how credentials
  transition from phase 1 without changing write ownership.
