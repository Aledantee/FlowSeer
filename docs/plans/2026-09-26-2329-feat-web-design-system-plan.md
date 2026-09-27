---
title: Web Design System - Plan
type: feat
date: 2026-09-26
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
---

# Web Design System - Plan

## Goal

The web console under `frontend/web/` gets a design system: one token
source from which scales, semantic roles, and Tailwind utilities are
generated and contrast-checked; `Ui*` components on Reka UI primitives;
and a Storybook catalogue of every token and component in both themes.
The app is then rebuilt on those components, and the legacy stylesheet is
dissolved. The phases below deliver it in dependency order, starting with
the token foundations, then the basic components, then overlays and
navigation, then data display, and last the app migration.

This plan is wrong if Tailwind's utility layer cannot coexist with the
unlayered legacy `src/style.css` long enough to migrate page by page. In
that case the migration has to happen in one step, and phases 2 to 5
collapse.

## Decisions

- Tailwind v4, Reka UI, Storybook 10, and three token tiers, as
  [Web Design System](../architecture/2026-09-26-web-design-system-direction.md)
  (proposed) describes. Why: the user chose Tailwind v4 over plain CSS
  and UnoCSS, chose Storybook as the catalogue, and chose to keep the
  m3connect colors while deriving every semantic token from a scale step.
  These decisions constrain all later frontend work, so they are recorded
  as a direction record rather than only in this plan.
- The m3connect anchors stay: coral `#FF451D` for the primary action,
  cyan `#5ECAD8` for selection and navigation, and the eight Radix
  families in `design/palette-source.json`. Why: the user chose to keep
  them, and `frontend/web/design/README.md` records how they were
  produced.
- The legacy semantic tokens are renamed in place, with no aliases. Why:
  `AGENTS.md` permits breaking changes before the first stable release and
  forbids compatibility shims. Each token has a single successor, so the
  rename is mechanical (about 500 references).
- Preflight is enabled only in the final phase. Why: the legacy stylesheet
  relies on browser defaults that Preflight resets. Tailwind documents
  omitting Preflight for exactly this case
  (https://tailwindcss.com/docs/preflight).
- The `/components` workbench is removed in phase 2, once the stories for
  its three components exist. Why: Storybook replaces it, and its drafts
  live only in one browser's local storage.

## Requirements

1. Every semantic color token is a reference to one primitive step, per
   theme. Example: `design/palette-source.json` maps `background` to
   `{"light": "neutral-5", "dark": "neutral-1"}`, and the generated CSS
   reads `--background: var(--m3-neutral-5);`.
2. The contrast gate reads that same mapping. Example: remapping
   `muted-foreground` to `neutral-9` in light mode makes
   `pnpm exec vitest run src/theme/palette.test.ts` fail on the
   `muted-foreground` against `card` pair.
3. Tailwind offers only semantic colors. Example: `class="bg-card"`
   produces a rule and `class="bg-red-500"` produces none.
4. Storybook shows every token and component in light and dark mode.
   Example: `pnpm build-storybook` succeeds, and the Colors story lists
   `primary` with its step and its contrast ratio against
   `primary-foreground`.
5. Interactive `Ui*` components use Reka primitives. Example: `UiSelect`
   renders Reka's `SelectRoot`, and arrow keys move its highlighted
   option.
6. After the last phase, `src/style.css` holds no component rules,
   Preflight is on, and stylelint rejects a color, font-size, or
   box-shadow literal outside the generated `src/theme/scales.css`.

## Out of scope

- Connecting to the control plane, authentication, or real data.
  `GOALS.md` lists these as not decided.
- A licensed brand font. Inter stays the interface font
  (`frontend/web/design/language.md`).
- Visual regression testing (Chromatic or screenshot diffs) and
  publishing the Storybook build.
- Chart rendering libraries. Phase 4 adds chart color tokens and restyles
  the existing SVG charts only.

## Units

### U1. Token foundations and Storybook
Files: `docs/plans/2026-09-26-2329-feat-web-design-system-phase1-plan.md`
After: none
Landed: `37453a31..810273e4`

### U2. Basic components
Files: `docs/plans/2026-09-26-2329-feat-web-design-system-phase2-plan.md`
After: U1
Landed: `ceb59875..922c2a51`

### U3. Overlays and navigation components
Files: `docs/plans/2026-09-26-2329-feat-web-design-system-phase3-plan.md`
After: U2
Landed: `b7a72cfd..7b38526d`

### U4. Data display components
Files: `docs/plans/2026-09-26-2329-feat-web-design-system-phase4-plan.md`
After: U2
Landed: `18de412d..a9b11bff`

### U5. App migration and Preflight
Files: `docs/plans/2026-09-26-2329-feat-web-design-system-phase5-plan.md`
After: U3, U4
Landed:

Waves: U1 | U2 | U3 U4 | U5

U3 and U4 both add stories and files under `src/ui/`, in disjoint
component directories, so they can run in separate worktrees. They share
only `src/ui/index.ts`, and merging it means appending each other's
exports.

## Verification

Each phase runs its own verification. After U5, run the full web check
set from `frontend/web/`: `pnpm typecheck`, `pnpm test`, `pnpm lint`,
`pnpm format:check`, `pnpm build`, and `pnpm build-storybook`. Then walk
the README's "Try the UI" steps in both themes.

## Definition of done

- [ ] Every phase plan reads `implemented`, and its `Landed:` line above
      carries its commit range.
- [ ] The direction record is accepted or amended by a person.
- [ ] `frontend/web/README.md` and `frontend/web/design/` describe the
      token source, Storybook, and the `Ui*` components. The `/components`
      workbench text is gone.
- [ ] This plan's `status` is `implemented`, with an outcome note under
      the title.

## Open questions

- Should the Storybook build be published anywhere (an artifact, an
  internal host)? Nothing in U1 to U5 depends on the answer.
