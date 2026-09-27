---
title: Web Design System Phase 5, App Migration and Preflight - Plan
type: refactor
date: 2026-09-26
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-26-2329-feat-web-design-system-plan.md
---

# Web Design System Phase 5, App Migration and Preflight - Plan

## Goal

The legacy `src/style.css` and `src/dashboard.css` are dissolved into `Ui*` components, view-level Tailwind utilities, and a small layout stylesheet for the navigation frame and brand glow. Tailwind Preflight is enabled. The remaining shadow, font-size, and color literals become tokens. stylelint joins `pnpm lint` and rejects color, font-size, and box-shadow literals outside the generated `src/theme/scales.css`. `src/theme/language.css` is removed, because its values are either tokens or component styles by then.

This plan is wrong if the navigation frame's shared glow coordinates (`background-attachment: fixed` across the sidebar, top bar, and corner) cannot be expressed on top of Preflight without per-element overrides.

## Decisions

- `design/palette.html` stays beside Storybook's Colors story. Why: the
  user chose it on 2026-09-27; Storybook is the component catalogue and
  contrast audit, and `palette.html` exports CSS and JSON tokens without a
  running server. Its stylesheet links move to `tailwind.css` and
  `style.css`.
- Migrate one view at a time behind the unlayered legacy rules, and delete each rule once nothing matches it. Enable Preflight last, when `style.css` holds no component rules. Why: the parent plan's Preflight decision; unlayered legacy rules override utility classes until removed.
- Dissolve `src/dashboard.css` completely. `DashboardView.vue` migrates to Tailwind utilities and `Ui*` components (`UiCard`, `UiTable`, `UiStatusBadge`, `UiSegmentedMeter`), while chart SVG presentation and tooltip styling are encapsulated in `TrafficChart.vue` and `TrafficSparkline.vue`. Why: eliminates a standalone stylesheet and removes the redundant import in `.storybook/preview.ts`.
- Retain `src/style.css` strictly as the layout stylesheet for the connected navigation frame and token imports. It keeps `@import` statements for `scales.css`, `semantic.css`, and `brand-glow.css`, and rules for the shell grid, sidebar collapse animation, topbar glass, inner corner notch mask, split pane divider, and custom scrollbars. All component rules are deleted. Why: parent plan Requirement 6; the brand glow requires shared viewport background coordinates across chrome boundaries.
- Encapsulate topology visualizer, inspector, and port map styles within `src/components/topology/` and `src/components/DevicePorts.vue`. Why: topology rules (lines 750–1280 of `src/style.css`) are private to the topology canvas and should not sit in the global layout stylesheet.
- Retain the `scope-trigger` class on `ScopeSwitcher.vue`'s trigger button while applying Tailwind utility classes. Why: existing component tests (`src/components/TenantSwitcher.test.ts`) assert `button.scope-trigger`.
- Replace all remaining `<AppTooltip>` tags in `FleetView.vue` with `<UiTooltip>`. Why: `AppTooltip` was migrated to `UiTooltip` in Phase 2; the remaining usages in `FleetView.vue` were left unmigrated when Phase 3 and Phase 4 merged.
- Remove `src/theme/language.css` entirely and clean hard-coded fallback literals in `src/theme/brand-glow.css`. Why: `--space-*` and `.ui-button` rules are superseded by Tailwind spacing and `UiButton`, and glow variables resolve directly to `--chrome`, `--accent`, and `--primary`.
- Configure stylelint with `stylelint-config-standard`, `postcss-html`, and `stylelint-declaration-strict-value`. Exclude `src/theme/scales.css` from the check. Integrate it into `pnpm lint` as `eslint . && stylelint "src/**/*.{css,vue}"`. Why: catches regressions where hard-coded hex colors, arbitrary pixel font-sizes, or raw box-shadows are written instead of design tokens.

## Requirements

1. No component rules remain in the legacy stylesheets. Example: `src/dashboard.css` and `src/theme/language.css` are deleted, and `src/style.css` contains only `@import` statements and frame/glow layout rules (`.shell`, `.sidebar`, `.main-shell`, `.main-notch`, `.topbar`, `.panes`, `.dock`).
2. The literal gate catches regressions across all stylesheets and Vue single-file components. Example: adding `color: #123456` or `font-size: 15px` to `ClientsView.vue` causes `pnpm lint` to fail with a stylelint error, while `src/theme/scales.css` passes.
3. Tailwind Preflight is active in the base layer. Example: `src/theme/tailwind.css` includes `@import "tailwindcss/preflight.css" layer(base);`, and unstyled `<h1>` elements inherit font-size from their parent container.
4. All application views and components use `Ui*` primitives and Tailwind utilities. Example: `ClientsView.vue` and `WorkspacePage.vue` toolbars render `UiInput` and `UiSelect` controls, and `git grep "<AppTooltip" frontend/web/src/` outputs zero matches.
5. Interactive navigation trigger contracts remain intact. Example: `TenantSwitcher.test.ts` passes asserting `button.scope-trigger` and its ARIA attributes.
6. The full web verification suite passes without warnings or errors. Example: `pnpm typecheck`, `pnpm test`, `pnpm lint`, `pnpm format:check`, `pnpm build`, and `pnpm build-storybook` all exit 0.
7. The README's "Try the UI" steps behave as they do before this phase, in both themes at 1280px and 390px viewports. Example: navigating to `/dashboard`, `/devices`, `/topology`, `/clients`, and `/sites` renders identical layouts, glowing chrome banners, and responsive pane controls.

## Out of scope

- Connecting to live backend control planes or external APIs.
- Replacing Vue Flow or adding third-party charting libraries.
- Modifying color scales, anchor hex values, or contrast thresholds in `design/palette-source.json`.
- Visual regression screenshot testing (Chromatic).

## Units

### U1. Dashboard and chart styles migration and dashboard.css dissolution
Files: `frontend/web/src/DashboardView.vue`, `frontend/web/src/components/TrafficChart.vue`, `frontend/web/src/components/TrafficSparkline.vue`, `frontend/web/src/dashboard.css`, `frontend/web/.storybook/preview.ts`
After: none
Change:
Migrates `DashboardView.vue` to Tailwind utility classes and `Ui*` components (`UiCard`, `UiTable`, `UiStatusBadge`, `UiSegmentedMeter`). The traffic overview, device health list, site roles breakdown, and recent events feed use semantic utility classes (`bg-card`, `border-border`, `text-muted-foreground`), preserving responsive breakpoints at 1150px and 800px. Encapsulates SVG path, gridline, axis label, and tooltip styles in `TrafficChart.vue` and `TrafficSparkline.vue` using semantic color tokens (`var(--chart-1)`, `var(--border)`, `var(--card-header)`). Deletes `src/dashboard.css`, removes `<style src="./dashboard.css"></style>` from `DashboardView.vue`, and removes `import '../src/dashboard.css'` from `.storybook/preview.ts`.
Tests:
`vitest run src/domain/overview.test.ts` passes; Storybook stories in `src/components/TrafficChart.stories.ts` and `src/components/TrafficSparkline.stories.ts` render with correct chart tokens and pass axe accessibility checks in `src/ui/a11y.test.ts`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/DashboardView.vue frontend/web/src/components/TrafficChart.vue frontend/web/src/components/TrafficSparkline.vue frontend/web/src/dashboard.css frontend/web/.storybook/preview.ts`

### U2. Workspace views migration (Clients, Devices, Sites, and Device Detail)
Files: `frontend/web/src/WorkspacePage.vue`, `frontend/web/src/ClientsView.vue`, `frontend/web/src/DeviceView.vue`
After: none
Change:
Migrates page headings, search and filter toolbars, status dropdowns, filter chips, callout banners (`.attention`, `.notice`), mobile cards (`.mobile-devices`), site cards (`.site-card`, `.site-grid`), and empty states in `WorkspacePage.vue` to Tailwind utility classes, `UiInput`, `UiSelect`, and `UiEmptyState`. Migrates `ClientsView.vue` filter inputs, band selectors, and table cells (signal indicators, throughput, monospace addresses) to Tailwind utilities. Migrates `DeviceView.vue` header, back-link, facts definition list (`<dl>`), connected clients list, and site reassignment form (`.assignment`) to Tailwind utilities, removing view-level class dependencies on `style.css` from those templates.
Tests:
`vitest run src/domain/clients.test.ts src/domain/fleet.test.ts` passes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/WorkspacePage.vue frontend/web/src/ClientsView.vue frontend/web/src/DeviceView.vue`

### U3. Topology and interactive components styling encapsulation
Files: `frontend/web/src/components/topology/TopologyGraph.vue`, `frontend/web/src/components/topology/TopologyInspector.vue`, `frontend/web/src/components/topology/TopologyLink.vue`, `frontend/web/src/components/topology/TopologyNode.vue`, `frontend/web/src/components/topology/TopologySiteNode.vue`, `frontend/web/src/components/DevicePorts.vue`, `frontend/web/src/components/TenantSwitcher.vue`, `frontend/web/src/components/DeviceIcon.vue`, `frontend/web/src/components/ThemeSwitcher.vue`, `frontend/web/src/components/ScopeSwitcher.vue`, `frontend/web/src/components/AccountMenu.vue`, `frontend/web/src/components/GlobalSearch.vue`, `frontend/web/src/components/HelpButton.vue`, `frontend/web/src/components/ReportBugButton.vue`
After: none
Change:
Encapsulates topology canvas and inspector styles (lines 750–1280 of `style.css`) into `src/components/topology/` and `src/components/DevicePorts.vue`, styling Vue Flow nodes, animated edges, site groups, link labels, port grids, and inspector details with semantic tokens. Migrates `TenantSwitcher.vue` and `DeviceIcon.vue` classes (`.tenant-switcher`, `.tenant-name`, `.device-icon`) to Tailwind utilities. Migrates `ThemeSwitcher.vue`, `HelpButton.vue`, and `ReportBugButton.vue` triggers to Tailwind utilities. Styles `ScopeSwitcher.vue`'s trigger button with Tailwind utilities while preserving `class="scope-trigger"` on the button element. Migrates `AccountMenu.vue` avatar and trigger to Tailwind utilities. Migrates `GlobalSearch.vue` trigger button, kbd shortcut badge, result scroll area, and empty state to Tailwind utilities and `UiKbd`.
Tests:
`vitest run src/components/TenantSwitcher.test.ts src/components/recentSearches.test.ts src/ui/command/UiCommand.test.ts` passes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/components/topology/TopologyGraph.vue frontend/web/src/components/topology/TopologyInspector.vue frontend/web/src/components/topology/TopologyLink.vue frontend/web/src/components/topology/TopologyNode.vue frontend/web/src/components/topology/TopologySiteNode.vue frontend/web/src/components/DevicePorts.vue frontend/web/src/components/TenantSwitcher.vue frontend/web/src/components/DeviceIcon.vue frontend/web/src/components/ThemeSwitcher.vue frontend/web/src/components/ScopeSwitcher.vue frontend/web/src/components/AccountMenu.vue frontend/web/src/components/GlobalSearch.vue frontend/web/src/components/HelpButton.vue frontend/web/src/components/ReportBugButton.vue`

### U4. Shell frame layout, dock, and style.css dissolution
Files: `frontend/web/src/FleetView.vue`, `frontend/web/src/navigation/PageDock.vue`, `frontend/web/src/navigation/PageHost.vue`, `frontend/web/src/style.css`, `frontend/web/src/theme/brand-glow.css`, `frontend/web/src/theme/language.css`
After: U1, U2, U3
Change:
Replaces remaining `<AppTooltip>` tags in `FleetView.vue` with `<UiTooltip>`. Migrates sidebar navigation items, active indicator highlights (`.nav-highlight`), badge counters (`.nav-count`), topbar container, split pane divider, and pane host transitions in `FleetView.vue`, `PageDock.vue`, and `PageHost.vue` to Tailwind utilities. Removes all dissolved component rules from `src/style.css`, leaving only the frame layout rules (root variables, shell grid, sidebar collapse animation, topbar glass, notch mask, split pane layout, and custom scrollbars) and token imports (`scales.css`, `semantic.css`, `brand-glow.css`). Deletes `src/theme/language.css` and its import from `src/style.css`. Removes hard-coded hex fallbacks from `src/theme/brand-glow.css`.
Tests:
`vitest run src/navigation/shortcuts.test.ts src/navigation/dock.test.ts src/motion/useMotionFeedback.test.ts` passes; `pnpm build` succeeds.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/FleetView.vue frontend/web/src/navigation/PageDock.vue frontend/web/src/navigation/PageHost.vue frontend/web/src/style.css frontend/web/src/theme/brand-glow.css frontend/web/src/theme/language.css`

### U5. Preflight enablement, token literals cleanup, and stylelint gate
Files: `frontend/web/src/theme/tailwind.css`, `frontend/web/src/theme/tailwind.test.ts`, `frontend/web/package.json`, `frontend/web/pnpm-lock.yaml`, `frontend/web/.stylelintrc.json`, `frontend/web/design/palette.html`, `frontend/web/design/language.md`, `frontend/web/design/README.md`, `frontend/web/README.md`
After: U4
Change:
Enables Tailwind v4 Preflight in `src/theme/tailwind.css` via `@import "tailwindcss/preflight.css" layer(base);`. Adds base element resets (such as `svg { display: block; }` and button outline normalization) to maintain visual appearance across browsers. Adds `stylelint`, `stylelint-config-standard`, `postcss-html`, and `stylelint-declaration-strict-value` to `devDependencies` in `frontend/web/package.json`. Configures `.stylelintrc.json` to disallow literal hex colors, pixel font-sizes, and raw box-shadows outside `src/theme/scales.css`. Adds stylelint to `pnpm lint`. Updates `design/palette.html` stylesheet imports. Updates `frontend/web/design/language.md` to remove references to `src/theme/language.css`. Updates `frontend/web/README.md` and `frontend/web/design/README.md` to describe the completed design system architecture, dissolved stylesheets, and Preflight integration.
Tests:
Linting a temporary fixture with `color: #123456` fails stylelint; `vitest run src/theme/tailwind.test.ts` passes and verifies Preflight base resets are emitted in the compiled Tailwind output; `pnpm typecheck`, `pnpm test`, `pnpm lint`, `pnpm format:check`, `pnpm build`, and `pnpm build-storybook` all pass cleanly.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/theme/tailwind.css frontend/web/src/theme/tailwind.test.ts frontend/web/package.json frontend/web/pnpm-lock.yaml frontend/web/.stylelintrc.json frontend/web/design/palette.html frontend/web/design/language.md frontend/web/design/README.md frontend/web/README.md`

Waves: U1 U2 U3 | U4 | U5

## Verification

From `frontend/web/`:
1. `pnpm typecheck` — TypeScript and Vue SFC type-checking.
2. `pnpm test` — all Vitest unit, domain, navigation, and accessibility tests pass.
3. `pnpm lint` — ESLint and stylelint pass without warnings or errors.
4. `pnpm format:check` — Prettier formatting passes across all web source files.
5. `pnpm build` — Vite production build succeeds.
6. `pnpm build-storybook` — Storybook static build succeeds.
7. Manual verification in `pnpm dev`:
   - Walk the README's "Try the UI" steps at 1280px desktop and 390px mobile in light and dark modes.
   - Verify the brand glow ribbons span continuously across the sidebar and topbar.
   - Verify table column sorting, search palette (Cmd+K), and split-pane resizing work as expected.

## Definition of done

- [ ] All 5 units implemented and verified.
- [ ] `src/dashboard.css` and `src/theme/language.css` are deleted; `src/style.css` contains only frame and glow layout rules.
- [ ] Preflight is active in `src/theme/tailwind.css`.
- [ ] stylelint gate runs in `pnpm lint` and rejects color, font-size, and box-shadow literals outside `src/theme/scales.css`.
- [ ] `frontend/web/README.md`, `frontend/web/design/README.md`, and `frontend/web/design/language.md` updated to reflect the dissolved stylesheets and Preflight integration.
- [ ] Full web verification suite passes: `pnpm typecheck`, `pnpm test`, `pnpm lint`, `pnpm format:check`, `pnpm build`, `pnpm build-storybook`.
- [ ] This plan's `status` is set to `implemented` with an outcome note under the title when landed.

## Open questions

- None.
