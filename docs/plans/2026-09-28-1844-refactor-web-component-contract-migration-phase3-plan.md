---
title: Web Component Contract Migration, Phase 3 - i18n Foundation and Ui Strings - Plan
type: refactor
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-plan.md
---

# Web Component Contract Migration, Phase 3 - i18n Foundation and Ui Strings - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

vue-i18n 11.4.12 runs in Composition mode, with `en` and `de` locale
files under `frontend/web/src/i18n/locales/`. `UiAppRoot` passes the
active locale to Reka's `ConfigProvider`, and Storybook has a locale
toolbar. Every visible string in `src/ui/` comes from a `ui.<component>.<key>`
message that a caller can override through a prop. Stop condition: the
one given in the parent.

## Decisions

- The contract's i18n section and the parent's Decisions govern.
- The user approved the package on 2026-09-28, recorded in the
  direction record. The `web-component` skill's dependency step still
  applies to every other package.

## Requirements

1. A key present in one locale file and missing from the other fails a
   test.
2. Every story renders in `de` with no missing-key warning. Example:
   `UiPagination` in `de` shows German labels.
3. No `Ui*` template holds a user-visible literal. The phase's review
   checks this by reading, since there is no lint rule for it.
