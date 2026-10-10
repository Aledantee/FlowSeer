---
title: Exact Diff Attribution - Plan
type: refactor
date: 2026-10-10
artifact_contract: flowseer-plan/v2
execution: code
---

# Exact Diff Attribution - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The perturbation gate of the corpus proves that a change to one
configuration leaf is reported under the subject and field of that leaf,
and every layer's `Diff` reports it so. Today the gate accepts any change
(`src/common/sim/internal/simtest/diffcoverage.go`, the check after each
perturbation), so a diff that names the wrong field passes.

## Decisions

- The parent's Decisions apply.
- The gate proves exact attribution in this parent, not in a later plan.
  A uniform mapping takes each configuration leaf to its diff subject and
  field, and the capability `Diff` fields follow it. Cost: trace and diff
  consumers see changed fields (decided by the user, 2026-10-10).
- Attribution is a phase of its own, run before the close-out phase, so
  the close-out documents the final diff fields. Why: with the close-out's
  nine units it exceeds one bounded phase (decided by the user,
  2026-10-10).
- Fields are granular. Existing subject kinds and keys stay. A change to
  one member of an aggregate gets its own field, a capability that appears
  or disappears gets an explicit presence field, and an identity change
  reports a removed and an added record. Cost: coarse or empty fields
  change, and their tests and corpus consumers change with them. Keeping
  aggregate fields and proving the leaf through complete before and after
  facts was the alternative (decided by the user, 2026-10-10).

## Requirements

R1. For every configuration leaf the gate perturbs, the reported change
names the subject and field the mapping gives that leaf, and no other
record appears. Example: changing one LAG member's port reports that
member's field under the LAG's subject, not the LAG as a whole.

R2. The gate fails when a layer reports the right subject under a wrong
field, and when a seed leaves a capability nil.

R3. Every layer package with a `Diff` follows the mapping, and each
group of layers lands with the tree green.

## Inventory

The close-out phase's planning pass drafted the split and its ownership
groups. Read it from commit `9ee39543`, file
`docs/plans/2026-10-01-2200-refactor-sim-package-overhaul-phase12-plan.md`,
section "Attribution split inventory" and its table: the mapping and the
gate first, then the simple layers, LAG and STP, filter and routing,
traffic, port and switch, and last assembly and the corpus. The draft is
re-checked against the tree at re-planning.

## Open questions

- The field grammar for a member of an aggregate, how a normalized
  effective value is treated, and the name of the presence field are not
  chosen. A source decides them or they are put to the user at
  re-planning.
