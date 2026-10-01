---
title: Web Component Contract Migration - Plan
type: refactor
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
---

# Web Component Contract Migration - Plan

## Goal

Move the 58 `Ui*` components and the views under `frontend/web/` onto the
accepted
[web component contract](../architecture/2026-09-28-web-component-contract-direction.md),
including its 2026-09-28 amendment:
- overlays on z-index tokens with Tailwind keyframe enter and exit, and
  one app-root provider
- JavaScript motion on motion-v
- every visible string in `en` and `de` locale files through vue-i18n
- an `ai` prop with a shared highlight on every component that
  represents something
- a catalog agents render UI from

The sequence is decided. Each phase is bounded enough to land in one
session, and each depends on the one before.

Stop condition: vue-i18n cannot run in Composition mode alongside
Storybook's `setProjectAnnotations` in the happy-dom audit. Every phase
from 3 on assumes a story can render in both locales.

## Decisions

- **Six phases, one after another.** Why:
  - Overlays and motion (phases 1 and 2) touch neither `src/ai/` nor
    strings, and they fix the failures agents hit most.
  - i18n (phases 3 and 4) must exist before the AI contract adds
    components with visible text.
  - Phases 4 and 5 both rewrite the views, so they cannot run in
    parallel.
- **Ordering against the AI actions plan.** Phase 1 lands before
  `docs/plans/2026-09-28-1804-feat-ai-actions-and-assistant-plan.md`
  starts. Phase 5 starts only after that plan has landed. Why:
  - Phase 1 and the AI plan edit the same overlay wrappers,
    `FleetView.vue`, `ui/index.ts`, and the README.
  - The AI plan rewrites `src/ai/`, deletes `UiAiActionLayer`, and adds
    `data-ai-selected`, which phase 5 builds on.
  - The AI plan records the same order.
  - The plan tools read only `U` labels from `After:`, so this ordering
    lives here and in each plan's Decisions.
- **Staying on Reka.** Overlay keyframes live in Tailwind `@theme`,
  inside the wrappers. Why: the contract's amendment. Ark UI's presence
  model is the same, and Reka v3's transition-aware presence has no
  release date.

## Requirements

1. **Overlay stacking and motion.** Every overlay and scrim uses
   `z-(--z-overlay)`, toasts use `z-(--z-toast)`, and their open and
   closed states run the `@theme` keyframes. Example: closing
   `UiDropdownMenu` in Storybook fades and scales it out over 100 ms
   before the node leaves the DOM.
2. **One app root.** No file outside `src/ui/` imports `reka-ui` or
   `motion`, test files aside. Example: `grep -rlnE "from
   '(reka-ui|motion)" frontend/web/src | grep -v '^frontend/web/src/ui/' |
   grep -v '\.test\.ts$'` prints nothing.
3. **Every string is in both locales.** Every visible string in `src/ui/`
   and the views comes from a locale file present in both `en` and `de`.
   Example: switching Storybook's locale toolbar to `de` renders
   `UiPagination`'s labels in German, and a key missing from `de.json`
   fails a test.
4. **Components that represent something take an `ai` prop.** Example:
   `<UiTableRow :ai="…">` appears in `registry.list()`, and
   `highlight(id)` outlines it with no view-side directive.
5. **Agents compose from the catalog.** An agent-supplied tree renders
   only catalog components with valid props. Example: a tree naming
   `UiStatusBadge` with `status: 'Offline'` renders. A tree naming `div`
   or passing `onClick` renders the error state.

## Units

### U1. Overlays and motion foundation

Files: `docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-phase1-plan.md`
After: none
Landed: `f35884bb..102193b1`

### U2. motion-v replaces motion/mini

Files: `docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-phase2-plan.md`
After: U1
Landed: `7c101257..6634b6b6`

### U3. i18n foundation and Ui component strings

Files: `docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-phase3-plan.md`
After: U1
Landed:

### U4. View strings and locale formatting

Files: `docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-phase4-plan.md`
After: U3
Landed:

### U5. The ai prop and shared highlight

Files: `docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-phase5-plan.md`
After: U4
Landed:

### U6. Generative UI catalog and renderer

Files: `docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-phase6-plan.md`
After: U5
Landed:

Waves: U1 | U2 U3 | U4 | U5 | U6

U2 and U3 touch different files: motion callers, and `src/i18n/` with
component strings. Both add a line to `UiAppRoot.vue`, so whichever lands
second merges one line.

## Verification

Each phase runs the verifier over its changed paths, and the browser loop
in `.agents/skills/web-component/references/review.md` for the components
it touched.

## Definition of done

- [ ] Every phase's plan reads `implemented`, and its `Landed:` line
      carries the commit range.
- [ ] `frontend/web/README.md` describes the app root, motion, locales,
      the `ai` prop, and the catalog.
- [ ] The `web-component` skill's interim rules (`i18n-and-ai.md`,
      `overlays-and-motion.md`) are removed as their migration lands.

## Open questions

None at the parent level. Each later phase is re-planned when its turn
comes.
