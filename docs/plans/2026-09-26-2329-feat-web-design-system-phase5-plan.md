---
title: Web Design System Phase 5, App Migration and Preflight - Plan
type: refactor
date: 2026-09-26
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-26-2329-feat-web-design-system-plan.md
---

# Web Design System Phase 5, App Migration and Preflight - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The legacy `src/style.css` and `src/dashboard.css` are dissolved into
`Ui*` components, view-level Tailwind utilities, and a small layout
stylesheet for the navigation frame and the brand glow. Tailwind
Preflight is enabled. The remaining shadow, font-size, and color literals
become tokens. stylelint joins `pnpm lint` and rejects color, font-size,
and box-shadow literals outside the generated `src/theme/scales.css`.
`src/theme/language.css` is removed, because its values are either
tokens or component styles by then.

This plan is wrong if the navigation frame's shared glow coordinates
(`background-attachment: fixed` across the sidebar, top bar, and corner)
cannot be expressed on top of Preflight without per-element overrides.

## Decisions

- Migrate one view at a time behind the unlayered legacy rules, and
  delete each rule once nothing matches it. Enable Preflight last, when
  `style.css` holds no component rules. Why: the parent plan's Preflight
  decision.
- Decide whether Storybook's Colors story replaces `design/palette.html`
  and its CSS and JSON downloads, or whether both stay.

## Requirements

1. No component rules remain in the legacy stylesheets. Example:
   `src/style.css` contains only imports and frame/glow layout rules.
2. The literal gate catches regressions. Example: adding
   `color: #123456` to any `.vue` file fails `pnpm lint`.
3. The README's "Try the UI" steps behave as they do before this phase,
   in both themes at 1280px and 390px.
