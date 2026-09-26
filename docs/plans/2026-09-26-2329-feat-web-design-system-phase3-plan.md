---
title: Web Design System Phase 3, Overlays and Navigation - Plan
type: feat
date: 2026-09-26
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-26-2329-feat-web-design-system-plan.md
---

# Web Design System Phase 3, Overlays and Navigation - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

`src/ui/` gains the overlay and navigation components, all on Reka
primitives, each with stories: `UiDialog`, `UiAlertDialog`, `UiPopover`,
`UiDropdownMenu`, `UiTabs`, `UiToast`, `UiCombobox` (the command palette
base), `UiScrollArea` (replacing `ScrollArea.vue`), and `UiBreadcrumb`.
`AccountMenu`, `ScopeSwitcher`, `TenantSwitcher`, `HelpButton`,
`ReportBugButton`, and `GlobalSearch` are rebuilt on them, and their
hand-written focus and keyboard code is deleted.

This plan is wrong if Reka's `Combobox` cannot keep `GlobalSearch`'s
recent-search and scoped-result behavior (`src/components/recentSearches.ts`,
`src/domain/search.ts`) without re-implementing its keyboard model.

## Decisions

- Overlays render in the top layer through Reka's portals, use `overlay`
  for the scrim and `popover` for their surface, and use `shadow-lg`.
  Why: the phase 1 tokens.
- Opening an overlay animates in 100–160 ms with `--ease-out`, and
  closing it is immediate. Why: the motion rules in
  `frontend/web/README.md`.

## Requirements

1. Every migrated switcher keeps its current keyboard behavior. Example:
   the existing `TenantSwitcher.test.ts` cases pass against the rebuilt
   component unchanged.
2. Escape closes the topmost overlay only. Example: with a tooltip open
   inside a dialog, Escape closes the tooltip and the dialog stays open.
