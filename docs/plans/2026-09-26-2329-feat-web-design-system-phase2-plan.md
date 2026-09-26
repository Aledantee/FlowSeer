---
title: Web Design System Phase 2, Basic Components - Plan
type: feat
date: 2026-09-26
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-26-2329-feat-web-design-system-plan.md
---

# Web Design System Phase 2, Basic Components - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`frontend/web/src/ui/` holds the basic `Ui*` components, each styled only
with the phase 1 Tailwind tokens and each with a stories file covering its
variants, sizes, and its disabled, focus, invalid, and loading states in
both themes. The existing `UiButton`, `StatusBadge`, and `MetricCard` move
into `src/ui/`. The `/components` route, `ComponentsView.vue`, its test,
and `src/design/workbench.css` are removed. Axe runs on every story in CI
through Storybook's Vitest integration.

This plan is wrong if Reka's form primitives cannot carry the native form
semantics the existing device-assignment form relies on (submit, reset,
required).

## Decisions

- Library layout, the `Ui` prefix, and `tailwind-variants` follow the
  parent's Decisions and the direction record.
- Components: `UiButton` (primary, secondary, ghost, danger; sm, md, and
  icon sizes; loading), `UiBadge` with `UiStatusBadge` built on it,
  `UiInput`, `UiTextarea`, `UiField` (label, description, and error text
  wired through `aria-describedby`), `UiCheckbox`, `UiSwitch`,
  `UiRadioGroup`, `UiSelect`, `UiCard`, `UiSeparator`, `UiKbd`,
  `UiSpinner`, and `UiTooltip`, which replaces `AppTooltip`. Why: these are
  the controls the current views already hand-build or need next.
- `UiCheckbox`, `UiSwitch`, `UiRadioGroup`, `UiSelect`, `UiTooltip`, and
  `UiSeparator` wrap Reka primitives. Why: the direction record.

## Requirements

1. Each component has a stories file whose stories all pass axe.
   Example: `UiInput` with `invalid` and no `UiField` label fails the
   story's axe check.
2. Views that used `UiButton`, `StatusBadge`, `MetricCard`, or
   `AppTooltip` import them from `src/ui/`, and the old files are gone.
   Example: `grep -r "components/UiButton" src` prints nothing.
3. `/components` redirects to `/dashboard`, like any unknown path.
