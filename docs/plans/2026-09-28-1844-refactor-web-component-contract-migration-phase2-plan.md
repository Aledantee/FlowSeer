---
title: Web Component Contract Migration, Phase 2 - motion-v Replaces motion/mini - Plan
type: refactor
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-plan.md
---

# Web Component Contract Migration, Phase 2 - motion-v Replaces motion/mini - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

JavaScript motion runs on motion-v. `UiAppRoot` sets
`MotionConfig reducedMotion="user"` once. The three callers of
`useMotionFeedback` (`FleetView.vue`, `WorkspacePage.vue`,
`ThemeSwitcher.vue`) move to motion-v. Where the sidebar resize is a
layout change, its hand-rolled read, `nextTick`, read, play sequence
gives way to motion-v's `layout` animation. The `motion` package is
removed once nothing imports it.

## Decisions

- The contract's 2026-09-28 amendment governs. The user approved
  `motion-v` and `@vueuse/core` that day.
- `motion-v` 2.5.1 and `motion` 13.x resolve `motion-dom` and
  `motion-utils` from the same 13.x range. The re-plan confirms the
  lockfile holds a single copy.

## Requirements

1. No file imports `motion/mini` or `motion`, and `package.json` no
   longer lists `motion`.
2. Under reduced motion, the sidebar and the scope highlight change
   without movement. Example: with the media query emulated, collapsing
   the sidebar sets its final width in the same frame.
3. Tests that trigger motion run under happy-dom without throwing. The
   re-plan decides the shim (an `offsetParent` stub, or the reduced-motion
   stub the repository already uses) from motion-v's own test setup.
