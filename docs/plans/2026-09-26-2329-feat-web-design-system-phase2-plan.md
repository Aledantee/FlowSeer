---
title: Web Design System Phase 2, Basic Components - Plan
type: feat
date: 2026-09-26
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
parent: docs/plans/2026-09-26-2329-feat-web-design-system-plan.md
---

# Web Design System Phase 2, Basic Components - Plan

> Implemented. 5 units, 2026-09-26T22:37Z to 2026-09-26T23:01Z. All checks green, 16 components with stories and axe-core tests, views migrated, and legacy components removed.

## Goal

`frontend/web/src/ui/` holds the basic `Ui*` components, styled with the
phase 1 semantic tokens and each paired with a stories file covering its
variants, sizes, and states (disabled, focus, invalid, and loading) in both
themes. The existing `UiButton`, `StatusBadge`, `MetricCard`, and
`AppTooltip` components migrate into `src/ui/` as `UiButton`, `UiBadge`
(with `UiStatusBadge`), `UiCard` (with `UiMetricCard`), and `UiTooltip`.
The `/components` route, `ComponentsView.vue`, its test, and
`src/design/workbench.css` are removed. Automated axe accessibility checks
run on every component story in CI through Vitest.

This plan is wrong if Reka's form primitives cannot carry the native form
semantics the existing device-assignment form relies on (submit, reset,
required).

## Decisions

- Library layout, the `Ui` prefix, and `tailwind-variants` follow the
  parent plan and the
  [Web Design System](../architecture/2026-09-26-web-design-system-direction.md)
  direction record.
- Component directories: components live in modular subdirectories under
  `src/ui/` (`src/ui/button/`, `src/ui/badge/`, `src/ui/spinner/`,
  `src/ui/form/`, `src/ui/card/`, `src/ui/separator/`, `src/ui/kbd/`,
  `src/ui/tooltip/`), and `src/ui/index.ts` provides the barrel export.
  Why: the parent plan specifies disjoint component directories so future
  phases (overlays in phase 3, data display in phase 4) can add components
  in separate worktrees without file conflicts.
- Components in this phase: `UiButton` (primary, secondary, ghost, danger;
  sm, md, and icon sizes; loading state with spinner), `UiSpinner`,
  `UiBadge`, `UiStatusBadge` (built on `UiBadge`), `UiField` (linking label,
  description, and error text via ARIA), `UiInput`, `UiTextarea`,
  `UiCheckbox`, `UiSwitch`, `UiRadioGroup`, `UiSelect`, `UiCard`,
  `UiMetricCard` (built on `UiCard`), `UiSeparator`, `UiKbd`, and
  `UiTooltip` (replacing `AppTooltip`). Why: these cover the foundational
  controls and surfaces the console views already hand-build or need next.
- `UiCheckbox`, `UiSwitch`, `UiRadioGroup`, `UiSelect`, `UiSeparator`, and
  `UiTooltip` wrap Reka UI primitives. Why: Reka handles focus and ARIA; our
  components handle styling.
- `UiStatusBadge` specializes `UiBadge` with status variants (`success`,
  `warning`, `danger`) and an indicator dot. Why: status indicators share
  pill geometry and typography with badges to eliminate duplicate CSS.
- `UiCard` provides a styled container for elevated panels. `UiMetricCard`
  builds on `UiCard` container tokens and replaces `MetricCard.vue`. Why:
  `MetricCard` was one of three components previewed in the workbench, and
  standardizing it on `UiCard` container tokens and tabular figures prepares
  for the dashboard cleanup.
- `UiMetricCard` delegates icon rendering to a named `#icon` slot. Why:
  `src/ui/` is a reusable design system module and must not import
  application-specific components from `src/components/`.
- `UiField` coordinates field IDs, helper descriptions, and error text
  through Vue `provide`/`inject` and scoped slot props. Why: automatically
  populating `id`, `aria-describedby`, and `aria-invalid` on child controls
  prevents broken label associations and duplicated markup.
- `UiSelect` supports Reka's popover menu across all screen widths. Why:
  native `<select>` cannot be styled with semantic tokens, and Reka handles
  both touch and keyboard interactions consistently.
- `UiButton` requires an explicit `aria-label` when `size="icon"`. Why:
  icon-only controls without an accessible name fail accessibility audits.
- Automated axe checks run in CI via Vitest using `axe-core` and
  `@storybook/vue3-vite` `composeStories`. Why: `@storybook/addon-a11y`
  provides interactive audits in the browser during `pnpm storybook`, while
  a Vitest axe test executes headlessly in CI without requiring external
  browser downloads or Playwright daemon processes. Layout-dependent rules
  like `color-contrast` are disabled in headless happy-dom runs because
  happy-dom does not compute layout geometry; contrast is already enforced
  by `src/theme/palette.test.ts`.
- The `/components` workbench, its test, and `workbench.css` are deleted
  once component stories exist, and `/components` redirects to `/dashboard`.
  Why: the parent decision; Storybook replaces the workbench, and drafts were
  stored only in browser local storage.

## Requirements

1. Every component under `src/ui/` has a co-located CSF 3 stories file
   covering all variants, sizes, and states in both themes.
   Example: `UiButton.stories.ts` declares primary, secondary, ghost,
   danger, sm, md, icon, disabled, and loading stories; `pnpm build-storybook`
   exits 0.
2. Automated axe accessibility checks run across all component stories in CI
   and report zero violations.
   Example: running `pnpm test src/ui/a11y.test.ts` executes axe on every
   composed story and passes. A negative test fixture mounting an invalid
   `UiInput` without an accessible name fails axe with a label violation.
3. `UiField` wires label, description, and error text through ARIA attributes.
   Example: `<UiField label="Site" description="Location" error="Required">`
   renders `<label for="f-1">`, `<p id="f-1-desc">`, and `<p id="f-1-err" role="alert">`,
   and sets `aria-describedby="f-1-desc f-1-err"` and `aria-invalid="true"` on
   the nested input.
4. Reka form primitives preserve native HTML form submission semantics.
   Example: submitting a `<form @submit.prevent="save">` containing a
   `UiSelect` with `name="siteId"` and selected value `"hamburg"` includes
   `siteId: "hamburg"` in the submit event payload.
5. Legacy component files in `src/components/` and the `/components`
   workbench route are removed, and `/components` redirects to `/dashboard`.
   Example: `git grep -rE "components/(UiButton|StatusBadge|MetricCard|AppTooltip)" frontend/web/src`
   prints nothing, and requesting `/components` in `pnpm dev` redirects to
   `/dashboard`.
6. Views previously consuming `UiButton`, `StatusBadge`, `MetricCard`, and
   `AppTooltip` import their replacements from `src/ui`.
   Example: `DeviceView.vue` imports `UiButton` and `UiStatusBadge` from
   `./ui`, rendering the site assignment form and healthy device badge.

## Out of scope

- Overlay and navigation components (`UiDialog`, `UiPopover`, `UiDropdownMenu`,
  `UiTabs`, `UiToast`, `UiCombobox`, `UiBreadcrumb`, `UiScrollArea`) scheduled
  for phase 3.
- Data display components (`UiTable`, `UiEmptyState`, `UiSkeleton`, `UiMeter`,
  `UiProgress`, `UiPagination`) and chart token restyling scheduled for
  phase 4.
- Dissolving `src/style.css` and enabling Tailwind Preflight, scheduled for
  phase 5.
- Visual regression screenshot diffing in CI.

## Units

### U1. Action and feedback components: Button, Spinner, Badge, StatusBadge
Files:
- `frontend/web/src/ui/spinner/UiSpinner.vue`
- `frontend/web/src/ui/spinner/UiSpinner.stories.ts`
- `frontend/web/src/ui/button/UiButton.vue`
- `frontend/web/src/ui/button/UiButton.stories.ts`
- `frontend/web/src/ui/button/UiButton.test.ts`
- `frontend/web/src/ui/badge/UiBadge.vue`
- `frontend/web/src/ui/badge/UiBadge.stories.ts`
- `frontend/web/src/ui/badge/UiBadge.test.ts`
- `frontend/web/src/ui/badge/UiStatusBadge.vue`
- `frontend/web/src/ui/badge/UiStatusBadge.stories.ts`
After: none
Change:
- `UiSpinner` renders an SVG circular spinner with `animate-spin` and
  `aria-hidden="true"`. It accepts `size` (`sm` 14px, `md` 16px, `lg` 20px).
- `UiButton` is authored with `tailwind-variants`:
  - Variants: `primary` (`bg-primary text-primary-foreground hover:brightness-105`),
    `secondary` (`bg-card text-foreground border border-border hover:bg-hover`),
    `ghost` (`bg-transparent text-foreground hover:bg-hover`),
    `danger` (`bg-danger-surface text-danger-foreground border border-danger-border hover:brightness-95`).
  - Sizes: `sm` (h-7 px-2.5 text-xs), `md` (h-8 px-3.5 text-sm), `icon` (h-8 w-8 p-0).
  - Props: `variant`, `size`, `type` (`button` | `submit` | `reset`), `disabled`,
    `loading`, `aria-label`.
  - When `loading` is true, it displays `UiSpinner`, sets `aria-busy="true"`, and
    suppresses click events.
  - Icon-only buttons validate that an accessible `aria-label` is present.
- `UiBadge` renders a pill badge:
  - Variants: `default`, `outline`, `primary`, `accent`, `success`, `warning`,
    `danger`, `info`, mapped to phase 1 semantic background, text, and border
    tokens.
  - Sizes: `sm`, `md`.
- `UiStatusBadge` wraps `UiBadge`:
  - Props: `status: 'Healthy' | 'Degraded' | 'Offline'`.
  - Maps `Healthy` to `success`, `Degraded` to `warning`, `Offline` to `danger`.
  - Renders a status dot `<i aria-hidden="true">` with a pulse animation for
    Degraded and Offline states.
- Co-located CSF 3 stories for each component covering variants, sizes,
  loading, disabled, and both themes.
Tests:
- `frontend/web/src/ui/button/UiButton.test.ts` asserts:
  - `UiButton` applies primary, secondary, ghost, and danger class names.
  - A disabled button sets the HTML `disabled` attribute and suppresses click
    handlers.
  - A loading button renders `UiSpinner` and sets `aria-busy="true"`.
- `frontend/web/src/ui/badge/UiBadge.test.ts` asserts:
  - `UiStatusBadge` renders matching text and badge variant for Healthy,
    Degraded, and Offline.
  - `UiBadge` renders all eight variant color classes correctly.
Verify:
`.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-09-26-2329-feat-web-design-system-phase2-plan.md`
then `pnpm typecheck && pnpm test && pnpm lint && pnpm build-storybook` in `frontend/web/`.

### U2. Form controls: Field, Input, Textarea, Checkbox, Switch, RadioGroup, Select
Files:
- `frontend/web/src/ui/form/UiField.vue`
- `frontend/web/src/ui/form/UiField.stories.ts`
- `frontend/web/src/ui/form/UiField.test.ts`
- `frontend/web/src/ui/form/UiInput.vue`
- `frontend/web/src/ui/form/UiInput.stories.ts`
- `frontend/web/src/ui/form/UiTextarea.vue`
- `frontend/web/src/ui/form/UiTextarea.stories.ts`
- `frontend/web/src/ui/form/UiCheckbox.vue`
- `frontend/web/src/ui/form/UiCheckbox.stories.ts`
- `frontend/web/src/ui/form/UiSwitch.vue`
- `frontend/web/src/ui/form/UiSwitch.stories.ts`
- `frontend/web/src/ui/form/UiRadioGroup.vue`
- `frontend/web/src/ui/form/UiRadioGroup.stories.ts`
- `frontend/web/src/ui/form/UiSelect.vue`
- `frontend/web/src/ui/form/UiSelect.stories.ts`
- `frontend/web/src/ui/form/UiSelect.test.ts`
After: none
Change:
- `UiField`:
  - Generates an ID with `useId()` and provides `{ id, describedBy, invalid }`
    to nested controls via Vue `provide` and scoped slot props.
  - Renders `<label>` with `for="id"` and optional required indicator.
  - Renders `<p :id="descriptionId">` with `text-xs text-muted-foreground` when
    description is set.
  - Renders `<p :id="errorId" role="alert">` with `text-xs text-danger-foreground`
    when error is set.
  - Sets `aria-describedby` combining description and error IDs.
- `UiInput`:
  - Native `<input>`, injects field context from `UiField` if present.
  - Styled with `bg-card border border-input rounded-control text-sm px-3 py-1.5 text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring focus-visible:border-ring disabled:opacity-50 disabled:cursor-not-allowed`.
  - Sets `aria-invalid="true"` and `border-danger-border ring-danger-border`
    when invalid.
- `UiTextarea`:
  - Native `<textarea>`, shares `UiInput` styling and field injection, min-h-[80px].
- `UiCheckbox`:
  - Wraps Reka `CheckboxRoot` and `CheckboxIndicator`.
  - Supports `v-model` (boolean or `'indeterminate'`), `name`, `required`,
    `disabled`.
- `UiSwitch`:
  - Wraps Reka `SwitchRoot` and `SwitchThumb`.
  - Supports `v-model` (boolean), `name`, `required`, `disabled`.
  - Track transitions between `bg-input` and `bg-primary`.
- `UiRadioGroup`:
  - Wraps Reka `RadioGroupRoot`, `RadioGroupItem`, `RadioGroupIndicator`.
  - Supports `v-model` (string), horizontal and vertical layout, arrow navigation.
- `UiSelect`:
  - Wraps Reka `SelectRoot`, `SelectTrigger`, `SelectValue`, `SelectPortal`,
    `SelectContent`, `SelectViewport`, `SelectItem`, `SelectItemText`,
    `SelectItemIndicator`.
  - Props: `modelValue`, `options` array (`Array<{ value: string; label: string; disabled?: boolean }>`),
    `placeholder`, `disabled`, `name`, `required`.
  - Injects `id`, `describedBy`, and `invalid` from `UiField` into the trigger.
  - Trigger styled with `bg-card border border-input rounded-control px-3 py-1.5 text-sm inline-flex items-center justify-between gap-2 text-foreground`.
  - Content rendered in `SelectPortal` styled with `bg-popover border border-border shadow-md rounded-control p-1 z-50`.
  - Supports typeahead search, arrow navigation, Enter/Space selection, Escape dismissal.
  - Passes `name` and `required` to `SelectRoot` for native form submit and reset integration.
- Co-located CSF 3 stories for each component covering default, focus, invalid,
  and disabled states in both themes.
Tests:
- `frontend/web/src/ui/form/UiField.test.ts` asserts:
  - `UiField` links `<label for>` with input `id`.
  - `aria-describedby` contains description and error IDs when both are set.
  - Input receives `aria-invalid="true"` when `error` prop is non-empty.
- `frontend/web/src/ui/form/UiSelect.test.ts` asserts:
  - `UiSelect` emits update on selection.
  - Hidden native select/input renders when `name` is provided.
  - Form submit includes the selected value.
Verify:
`.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-09-26-2329-feat-web-design-system-phase2-plan.md`
then `pnpm typecheck && pnpm test && pnpm lint && pnpm build-storybook` in `frontend/web/`.

### U3. Surface and utility components: Card, MetricCard, Separator, Kbd, Tooltip
Files:
- `frontend/web/src/ui/separator/UiSeparator.vue`
- `frontend/web/src/ui/separator/UiSeparator.stories.ts`
- `frontend/web/src/ui/separator/UiSeparator.test.ts`
- `frontend/web/src/ui/kbd/UiKbd.vue`
- `frontend/web/src/ui/kbd/UiKbd.stories.ts`
- `frontend/web/src/ui/card/UiCard.vue`
- `frontend/web/src/ui/card/UiCard.stories.ts`
- `frontend/web/src/ui/card/UiMetricCard.vue`
- `frontend/web/src/ui/card/UiMetricCard.stories.ts`
- `frontend/web/src/ui/card/UiMetricCard.test.ts`
- `frontend/web/src/ui/tooltip/UiTooltip.vue`
- `frontend/web/src/ui/tooltip/UiTooltip.stories.ts`
- `frontend/web/src/ui/tooltip/UiTooltip.test.ts`
After: none
Change:
- `UiSeparator`:
  - Wraps Reka `Separator`.
  - Props: `orientation?: 'horizontal' | 'vertical'`, `decorative?: boolean`.
  - Styled with `bg-border`, `h-[1px] w-full` (horizontal) or `h-full w-[1px]` (vertical).
- `UiKbd`:
  - Renders `<kbd class="inline-flex items-center justify-center font-mono text-2xs px-1.5 py-0.5 rounded border border-border bg-subtle text-muted-foreground shadow-xs">`.
- `UiCard`:
  - Container component styled with `bg-card border border-border rounded-panel shadow-xs p-4`.
  - Slots: default, optional header, footer.
- `UiMetricCard`:
  - Replaces `src/components/MetricCard.vue`.
  - Props: `label: string`, `value: number`, `unit?: string`.
  - Slots: `#icon` for custom icon component, `#default` for note text.
  - Layout uses `UiCard` container, displaying label and icon, value in tabular
    monospace figures (`font-mono text-2xl font-semibold tabular-nums`), unit,
    and note.
- `UiTooltip`:
  - Replaces `src/components/AppTooltip.vue`.
  - Wraps Reka `TooltipRoot`, `TooltipTrigger`, `TooltipPortal`, `TooltipContent`.
  - Props: `label: string`, `hint?: string`, `shortcut?: Shortcut | string[]`,
    `side?: 'top' | 'right' | 'bottom' | 'left'`, `inline?: boolean`,
    `delayDuration?: number`.
  - Renders shortcut keys with `UiKbd`.
  - Content rendered in `TooltipPortal` styled with `bg-popover text-foreground border border-border rounded-control shadow-md px-2.5 py-1.5 text-xs z-50`.
- Co-located CSF 3 stories for each component.
Tests:
- `frontend/web/src/ui/separator/UiSeparator.test.ts` asserts:
  - `UiSeparator` renders with `role="separator"` and orientation styling.
- `frontend/web/src/ui/card/UiMetricCard.test.ts` asserts:
  - `UiMetricCard` renders label, value with tabular figures, icon slot, and
    note slot content.
- `frontend/web/src/ui/tooltip/UiTooltip.test.ts` asserts:
  - `UiTooltip` renders trigger child directly (`as-child`) and displays content
    on hover and keyboard focus.
Verify:
`.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-09-26-2329-feat-web-design-system-phase2-plan.md`
then `pnpm typecheck && pnpm test && pnpm lint && pnpm build-storybook` in `frontend/web/`.

### U4. UI barrel export and Storybook accessibility test suite in CI
Files:
- `frontend/web/package.json`
- `frontend/web/pnpm-lock.yaml`
- `frontend/web/src/ui/index.ts`
- `frontend/web/src/ui/a11y.test.ts`
After: U1, U2, U3
Change:
- Add `axe-core@4.13.0` as an explicit devDependency in `frontend/web/package.json`.
- `src/ui/index.ts` re-exports all UI components and prop types:
  - `UiButton` from `./button/UiButton.vue`
  - `UiSpinner` from `./spinner/UiSpinner.vue`
  - `UiBadge` from `./badge/UiBadge.vue`
  - `UiStatusBadge` from `./badge/UiStatusBadge.vue`
  - `UiField` from `./form/UiField.vue`
  - `UiInput` from `./form/UiInput.vue`
  - `UiTextarea` from `./form/UiTextarea.vue`
  - `UiCheckbox` from `./form/UiCheckbox.vue`
  - `UiSwitch` from `./form/UiSwitch.vue`
  - `UiRadioGroup` from `./form/UiRadioGroup.vue`
  - `UiSelect` from `./form/UiSelect.vue`
  - `UiCard` from `./card/UiCard.vue`
  - `UiMetricCard` from `./card/UiMetricCard.vue`
  - `UiSeparator` from `./separator/UiSeparator.vue`
  - `UiKbd` from `./kbd/UiKbd.vue`
  - `UiTooltip` from `./tooltip/UiTooltip.vue`
- `src/ui/a11y.test.ts`:
  - Configures Storybook project annotations via `setProjectAnnotations(preview)`
    from `@storybook/vue3-vite` using `.storybook/preview.ts`.
  - Discovers all component stories under `src/ui/**/*.stories.ts` via `import.meta.glob`.
  - For each story file, composes stories with `composeStories`.
  - Mounts each composed story into a detached DOM container using `createApp(story)`.
  - Runs `axe.run(container, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] }, rules: { 'color-contrast': { enabled: false } } })`.
  - Asserts `results.violations` has length 0 for every story.
  - Mounts a negative fixture (an invalid `UiInput` without an accessible name)
    and asserts `axe.run()` reports a form control label violation.
Tests:
- `pnpm test src/ui/a11y.test.ts` passes with zero violations across all
  component stories.
- Negative axe test case confirms violation detection on unlabelled input.
Verify:
`.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-09-26-2329-feat-web-design-system-phase2-plan.md`
then `pnpm typecheck && pnpm test && pnpm lint && pnpm build-storybook` in `frontend/web/`.

### U5. View migration, workbench removal, and legacy cleanup
Files:
- `frontend/web/src/main.ts`
- `frontend/web/src/navigation/page.ts`
- `frontend/web/src/navigation/dock.test.ts`
- `frontend/web/src/FleetView.vue`
- `frontend/web/src/WorkspacePage.vue`
- `frontend/web/src/DeviceView.vue`
- `frontend/web/src/DashboardView.vue`
- `frontend/web/src/components/topology/TopologyInspector.vue`
- `frontend/web/src/components/topology/TopologyGraph.vue`
- `frontend/web/src/components/GlobalSearch.vue`
- `frontend/web/src/components/HelpButton.vue`
- `frontend/web/src/components/ReportBugButton.vue`
- `frontend/web/src/components/ThemeSwitcher.vue`
- `frontend/web/src/navigation/PageDock.vue`
- `frontend/web/src/components/AppIcon.vue`
- `frontend/web/src/ComponentsView.vue`
- `frontend/web/src/ComponentsView.test.ts`
- `frontend/web/src/design/workbench.css`
- `frontend/web/src/components/UiButton.vue`
- `frontend/web/src/components/StatusBadge.vue`
- `frontend/web/src/components/MetricCard.vue`
- `frontend/web/src/components/AppTooltip.vue`
- `frontend/web/README.md`
- `frontend/web/design/language.md`
After: U4
Change:
- `/components` route is removed:
  - `src/main.ts` route pattern changes from `/:view(dashboard|devices|clients|sites|topology|components)`
    to `/:view(dashboard|devices|clients|sites|topology)`. Unknown paths,
    including `/components`, redirect to `/dashboard`.
  - `src/navigation/page.ts` removes `'components'` from `PageView` and `VIEWS`.
    `viewOf('/components')` returns `{ view: 'dashboard' }`.
  - `src/navigation/dock.test.ts` updates navigation assertions to verify
    `viewOf('/components')` returns `{ view: 'dashboard' }`.
  - `src/FleetView.vue` removes `'components'` from sidebar navigation links,
    `VIEW_TITLES`, and `VIEW_ICONS`.
  - `src/WorkspacePage.vue` removes `ComponentsView` import, branch, and title.
  - Delete `src/ComponentsView.vue`, `src/ComponentsView.test.ts`, and
    `src/design/workbench.css`.
  - In `src/components/AppIcon.vue`, remove unused `components` icon path.
- Migrate views to `src/ui` barrel imports:
  - In `src/DeviceView.vue`: import `UiButton`, `UiStatusBadge`, `UiField`, and
    `UiSelect` from `./ui`. Convert the site assignment form to `UiField` and
    `UiSelect` with `allowedSites.map(s => ({ value: s.id, label: s.name }))`.
  - In `src/DashboardView.vue`: import `UiStatusBadge` from `./ui`.
  - In `src/WorkspacePage.vue`: import `UiStatusBadge`, `UiMetricCard`, and
    `UiTooltip` from `./ui`. Pass icon to `UiMetricCard` via `#icon` slot
    (`<template #icon><AppIcon :name="..." /></template>`).
  - In `src/components/topology/TopologyInspector.vue`: import `UiStatusBadge`
    from `../../ui`.
  - In `src/FleetView.vue`: import `UiTooltip` from `./ui`.
  - In `src/components/GlobalSearch.vue`, `src/components/HelpButton.vue`,
    `src/components/ReportBugButton.vue`, `src/components/ThemeSwitcher.vue`:
    import `UiTooltip` from `../ui`.
  - In `src/components/topology/TopologyGraph.vue`: import `UiTooltip` from
    `../../ui`.
  - In `src/navigation/PageDock.vue`: import `UiTooltip` from `../ui`.
- Delete legacy component files:
  - `src/components/UiButton.vue`
  - `src/components/StatusBadge.vue`
  - `src/components/MetricCard.vue`
  - `src/components/AppTooltip.vue`
- Update documentation:
  - `frontend/web/README.md` removes references to `ComponentsView.vue`,
    `/components` route, and workbench drafts, and introduces `src/ui/` components.
  - `frontend/web/design/language.md` updates component examples to use
    `UiStatusBadge` and `UiButton` from `src/ui`, and remove workbench references.
Tests:
- `src/navigation/dock.test.ts` passes, verifying `viewOf('/components')`
  returns `{ view: 'dashboard' }`.
- `pnpm test` passes with zero regressions across all test suites.
- Grep assertions confirm no legacy references remain:
  - `git grep "components/UiButton" frontend/web/src` prints nothing.
  - `git grep "components/StatusBadge" frontend/web/src` prints nothing.
  - `git grep "components/MetricCard" frontend/web/src` prints nothing.
  - `git grep "components/AppTooltip" frontend/web/src` prints nothing.
  - `git grep "ComponentsView" frontend/web/src` prints nothing.
Verify:
`.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-09-26-2329-feat-web-design-system-phase2-plan.md frontend/web/README.md frontend/web/design/language.md`
then `pnpm typecheck && pnpm test && pnpm lint && pnpm build && pnpm build-storybook` in `frontend/web/`.

Waves: U1 U2 U3 | U4 | U5

## Verification

From `frontend/web/`:

```sh
pnpm install --frozen-lockfile
pnpm typecheck
pnpm test
pnpm lint
pnpm format:check
pnpm build
pnpm build-storybook
```

Verify legacy references and route cleanup:

```sh
git grep -rnE "components/(UiButton|StatusBadge|MetricCard|AppTooltip)" src
git grep -rn "ComponentsView" src
```

Both grep commands must print nothing.

Manual and browser checks:
1. Run `pnpm storybook` and navigate to each component story under `Ui/`.
   Verify all variants, sizes, and states (focus, disabled, invalid, loading)
   render correctly. Switch the theme toggle in the Storybook toolbar and
   verify contrast and colors in both light and dark mode.
2. Run `pnpm dev` and navigate to `/components`. Verify the browser redirects
   to `/dashboard`.
3. In `pnpm dev`, open `/devices/gw-01` and verify the site assignment form
   renders using `UiField` and `UiSelect`. Select a different site, choose
   "Save assignment", and confirm the assignment updates.

## Definition of done

- [x] Verifier green for every changed path, and all verification commands pass.
- [x] All 15 `Ui*` components created under `src/ui/` with CSF 3 stories and
      passing axe accessibility checks.
- [x] Views migrated from legacy components to `src/ui` barrel imports.
- [x] `/components` route, `ComponentsView.vue`, its test, and `workbench.css`
      removed.
- [x] `frontend/web/README.md` and `frontend/web/design/language.md` updated.
- [x] This plan's `status` is set to `implemented`.
- [x] No plan labels in code, comments, or commit messages.

## Open questions

- None. All design choices (mobile select UX, icon button labels, field validation bindings, and metric card icon slotting) are settled under Decisions.
