---
title: Web Component Contract Migration, Phase 5 - The ai Prop and Shared Highlight - Plan
type: refactor
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-plan.md
---

# Web Component Contract Migration, Phase 5 - The ai Prop and Shared Highlight - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A `useAiTarget` composable replaces the directive's logic. Every `Ui*`
component that represents an entity, a value, or an action takes an
optional `ai` prop and registers itself. Views pass the prop instead of
applying `v-ai-target`. One shared rule draws `[data-ai-selected]`, and
`data-ai-origin="agent"` marks agent-changed values.

## Decisions

- The contract's "Every component is AI-addressable" section and the
  parent's Decisions govern.
- This phase starts only after
  `docs/plans/2026-09-28-1804-feat-ai-actions-and-assistant-plan.md` has
  landed. That plan reshapes `src/ai/` and already writes
  `data-ai-selected`.

## Requirements

1. A component rendered with an `ai` prop appears in `registry.list()`,
   and `highlight(id)` outlines it. Without the prop, nothing registers.
2. No view applies `v-ai-target` directly, and the directive is removed
   or kept only as a thin wrapper over the composable. Which of the two
   is decided when this phase is re-planned.
3. Layout-only components (separators, scroll areas, skeletons) take no
   `ai` prop.
