---
title: Console design language and component workspace - Plan
type: feat
date: 2026-09-06
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: mixed
---

# Console design language and component workspace - Plan

> Implemented. Frontend typecheck, lint, formatting, production build, all 113 tests, and the diff-aware verifier passed. Browser checks covered desktop and 390px layouts, both themes, saved drafts after reload, search, site assignment, and sidebar collapse/expand.

## Goal

Make the fleet views visually consistent and provide a Components page for comparing foundations, exercising shared controls, and recording component decisions. Stop if this requires a backend contract: this workspace uses local fixtures and browser-local notes.

## Decisions

- Use Inter for interface text and system monospace for addresses. Inter provides text optical sizing and tabular figures ([specimen](https://rsms.me/inter/)). Keep DM Sans as a comparison in the workbench.
- Preserve the existing brand palette and light/dark semantic contrast pairs. Standardize spacing, radii, typography, and control states around them.
- Share buttons, health badges, and metric cards between fleet and component views so refinements affect the product examples too.
- Keep component notes and planning status in local storage, with explicit save feedback. They are personal drafts, not shared collaboration storage.
- The m3connect stylesheet declares GT Standard Trial VF. Do not bundle those website assets; adopting GT Standard requires supplied licensed files. See [Grilli Type licensing](https://www.grillitype.com/information).

## Requirements

1. Devices, Sites, and Topology use the same type and spacing rules in both themes. Existing search, scope, sorting, and assignment behavior remains usable.
2. `/components` opens within the existing shell and has no fleet metrics or site filter.
3. Components exposes working examples and foundations. Switching a button variant changes its preview; the preview action produces feedback.
4. Save notes and a planning state for a component; reload restores them. Unavailable storage produces a visible error.
5. At 390px width, the workbench fits the viewport and all editing controls remain accessible.

## Units

### U1. Design language and workbench

Files: `frontend/web/` and this plan.
After: none
Change: shared tokens and components style the fleet views; a routed workbench exposes foundations, previews, and saved drafts.
Tests: component workspace interaction and persistence tests; existing palette, fleet, motion, and tenant tests. Browser inspection of desktop/mobile and both themes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh --base HEAD`

## Verification

Run the repository verifier, frontend typecheck, tests, lint, format check, and production build. Inspect the live preview and exercise navigation, search, notes, theme changes, and component variants.

## Definition of done

Every changed path passes the verifier; README explains the workbench and font choice; browser checks confirm responsive behavior; this plan records the outcome.
