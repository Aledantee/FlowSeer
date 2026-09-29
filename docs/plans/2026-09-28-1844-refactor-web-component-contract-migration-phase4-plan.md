---
title: Web Component Contract Migration, Phase 4 - View Strings and Locale Formatting - Plan
type: refactor
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-plan.md
---

# Web Component Contract Migration, Phase 4 - View Strings and Locale Formatting - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The views and `src/components/` move their strings to `view.<view>.<key>`
messages. Numbers, dates, relative times, and units format through
vue-i18n or `Intl` for the active locale. Identifiers carry
`translate="no"`. A locale switch in the app re-renders every view in
German.

## Decisions

- The contract's i18n section and the parent's Decisions govern.
- Fixture data under `src/domain/` stays untranslated. It stands in for
  service data, which arrives in the user's locale-neutral form.

## Requirements

1. With the locale set to `de`, the dashboard, device, clients, sites,
   and topology views show no English UI text. Example: the Sites
   heading and its count read in German, with the count formatted by
   `n()`.
2. Traffic values and timestamps format per locale. Example: `1.234,5
   Mbit/s` in `de` and `1,234.5 Mbit/s` in `en`.
