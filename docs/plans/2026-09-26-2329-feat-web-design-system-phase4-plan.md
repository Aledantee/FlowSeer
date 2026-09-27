---
title: Web Design System Phase 4, Data Display - Plan
type: feat
date: 2026-09-26
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
execution: code
parent: docs/plans/2026-09-26-2329-feat-web-design-system-plan.md
---

# Web Design System Phase 4, Data Display - Plan

> Implemented. 5 units, 2026-09-27T07:10Z to 2026-09-27T07:42Z. All checks green, composable table suite and data display components created with CSF 3 stories and axe-core accessibility tests, views migrated, and legacy meters removed.

## Goal

`src/ui/` gains the data display components, each with stories:
`UiTable` (dense, sortable header, sticky header, row selection),
`UiEmptyState`, `UiSkeleton`, `UiMeter` (replacing `ResourceMeter` and
`HealthBar`), `UiProgress`, and `UiPagination`. `TrafficChart`,
`TrafficSparkline`, and the topology link colors read `chart-1` to
`chart-6` and `graph-edge`. The device, client, and dashboard tables move
to `UiTable`.

This plan is wrong if `UiTable` cannot keep the row-order stability the
Devices page promises while values update
(`frontend/web/README.md`, "rows retain their order as values change").

## Decisions

- `UiTable` styles native `<table>` markup and adds no external grid library.
  Why: the README defers choosing a grid until real update rates and fleet
  sizes can be tested.
- `UiTable` adopts a composable subcomponent architecture (`UiTable`,
  `UiTableHeader`, `UiTableBody`, `UiTableRow`, `UiTableHead`, `UiTableCell`,
  `UiTableEmpty`) under `src/ui/table/`. Why: standard HTML table layout
  allows varied column compositions, alignments, and custom cell templates
  across device, client, and dashboard views without rigid schema constraints.
- `UiTable` renders the native `<table>` directly without an outer overflow
  wrapper. Why: existing console views already wrap tables in `<ScrollArea>`,
  and adding a second scroll container creates nested scrollbars and breaks
  sticky header calculations.
- Sticky headers attach `position: sticky; top: 0` to `UiTableHead` (`<th>`)
  rather than `UiTableHeader` (`<thead>`). Why: Chromium and WebKit do not
  reliably anchor sticky offsets on elements with `display: table-header-group`.
- Sorting uses a controlled interface on `UiTableHead` with `sortDirection`
  and a `sort` toggle event. Why: parent views sort data in place with stable
  record identifiers (`device.id`, `client.id`). Controlled sorting prevents
  unwanted row jumping when live telemetry values update.
- Legacy unlayered table rules in `src/style.css` (bare `th`, `td`, and `table`
  selectors) are scoped to `table:not([data-ui-table])`. Why: in CSS Cascade
  Level 5, unlayered selectors override styles in `@layer utilities`. Scoping
  the legacy rules allows `UiTable` Tailwind utilities to take effect without
  breaking unmigrated views.
- `UiEmptyState` delegates icon rendering to a named `#icon` slot. Why:
  `src/ui/` is a reusable module and must not import `src/components/AppIcon.vue`,
  matching the architectural decision established for `UiMetricCard` in Phase 2.
- `UiMeter` provides single resource metering (CPU, memory, bandwidth), and
  `UiSegmentedMeter` provides segmented status distribution metering
  (Healthy, Degraded, Offline), both under `src/ui/meter/`. Together they
  replace `ResourceMeter.vue` and `HealthBar.vue`. Why: resource usage and
  health distributions share tabular figures and meter semantics. Separating
  them into paired components mirrors `UiBadge`/`UiStatusBadge` and
  `UiCard`/`UiMetricCard` from Phase 2.
- `UiMeter` normal tone uses cyan (`bg-chart-1` / `text-accent-foreground`),
  warning uses `bg-warning-foreground`, and critical uses `bg-danger-foreground`.
  Why: coral (`bg-primary`) is strictly reserved for primary brand actions,
  while cyan represents normal system telemetry.
- `UiSegmentedMeter` accepts either an explicit `segments` array or a `counts`
  record with `Health` keys. Why: `DashboardView.vue` already provides
  `Record<Health, number>`, so supporting `counts` enables a drop-in migration.
- `UiProgress` and `UiPagination` wrap Reka UI primitives (`ProgressRoot`,
  `ProgressIndicator`, and `PaginationRoot` family). `ProgressIndicator` binds
  a CSS translateX transform for determinate values and animates for
  indeterminate states. Why: Reka manages ARIA states, keyboard navigation,
  and focus transitions.
- Existing SVG charts (`TrafficChart.vue` and `TrafficSparkline.vue`) are
  restyled with semantic chart tokens: `chart-1` (`var(--chart-1)`) for
  primary series, and `chart-1` through `chart-6` for multi-series tokens
  and sparkline color options. Why: the parent plan restricts Phase 4 to
  restyling existing SVG charts without importing chart libraries.
- Series colors follow the stable assignment order cyan (`chart-1`), coral
  (`chart-2`), violet (`chart-3`), green (`chart-4`), amber (`chart-5`), and
  blue (`chart-6`), with direct text labels or legend. Why: the categorical
  guidance in `frontend/web/design/README.md` and color accessibility (never
  relying on color alone).
- Graph flow dashes in `TopologyLink.vue` use `var(--chart-1)` instead of
  `var(--accent)`, while base edge paths retain `var(--graph-edge)`. Why:
  traffic flow represents live data throughput and belongs in the chart color
  namespace.
- `TrafficChart` styles in `src/dashboard.css` are imported into
  `.storybook/preview.ts`. Why: `.storybook/preview.ts` currently imports
  only `style.css` and `tailwind.css`, so `TrafficChart` stories would
  otherwise render without layout or grid styling.
- All new component stories under `src/ui/` run through automated headless
  axe accessibility audits in CI via `src/ui/a11y.test.ts`. Why: enforces
  WCAG 2.1 AA compliance before components reach production views.

## Requirements

1. `UiTable` sorts on a header click and announces the sort with `aria-sort`.
   Example: clicking "Name" twice sets `aria-sort` to `descending` on
   `UiTableHead` and renders the descending arrow indicator `↓`.
2. `UiTable` preserves DOM row order when bound items update their telemetry
   values in place. Example: updating `device.throughput` on a row in a table
   sorted by name does not alter the row position or trigger DOM recreation.
3. `UiTable` supports dense padding and sticky header positioning via props.
   Example: `<UiTable dense sticky-header>` applies `sticky top-0` and
   `bg-subtle` to `UiTableHead` cells and sets `py-2 px-3` cell padding.
4. `UiMeter` renders single resource percentages with thresholds and
   accessible meter attributes. Example: `<UiMeter label="CPU" :value="92" />`
   renders `role="meter"` with `aria-valuenow="92"` and applies critical tone
   styling (`bg-danger-foreground`).
5. `UiSegmentedMeter` renders segmented distribution bars with accessible
   summaries and optional legends. Example: `<UiSegmentedMeter :counts="{ Healthy: 10, Degraded: 0, Offline: 2 }" legend />`
   renders an `aria-label="10 Healthy, 2 Offline"` track and a legend listing
   counts.
6. `UiProgress` renders determinate and indeterminate progress bars with Reka
   primitives. Example: `<UiProgress :model-value="45" />` sets
   `aria-valuenow="45"` on `ProgressRoot`, while `:model-value="null"` renders
   an indeterminate sliding animation.
7. `UiPagination` provides accessible page navigation with Reka primitives.
   Example: `<UiPagination :total="100" :items-per-page="10" :page="2" />`
   marks page button 2 with `aria-current="page"` and emits `update:page` when
   clicking next.
8. `UiEmptyState` provides a centered layout with heading, description, and
   action slot. Example: `<UiEmptyState title="No clients found" description="Try another filter" />`
   renders an accessible heading and description.
9. Chart series never rely on color alone. Example: every `TrafficChart`
   series has a legend label or direct label and distinct stroke pattern in
   its story, with stroke and fill using `var(--chart-1)` through
   `var(--chart-6)`.
10. `TopologyLink` renders edge paths with `var(--graph-edge)` and traffic flow
    with `var(--chart-1)`. Example: inspecting `.topology-link .vue-flow__edge-path`
    in computed styles resolves stroke to the `graph-edge` semantic color.
11. Views previously consuming `ResourceMeter`, `HealthBar`, and raw table
    markup import their replacements from `src/ui`. Example:
    `git grep -rE "components/(ResourceMeter|HealthBar)" frontend/web/src`
    returns zero matches.

## Out of scope

- Overlay and navigation components (`UiDialog`, `UiPopover`, `UiDropdownMenu`,
  `UiTabs`, `UiToast`, `UiCombobox`, `UiScrollArea`, `UiBreadcrumb`),
  scheduled for phase 3.
- Dissolving `src/style.css` and enabling Tailwind Preflight, scheduled for
  phase 5.
- Grid virtualization libraries (such as TanStack Table or AG Grid).
- Third-party chart rendering libraries (such as Chart.js, D3, or ECharts).
- Visual regression screenshot diffing in CI.

## Units

### U1. Table component suite: UiTable, Header, Body, Row, Head, Cell, Empty
Files:
- `frontend/web/src/ui/table/UiTable.vue`
- `frontend/web/src/ui/table/UiTableHeader.vue`
- `frontend/web/src/ui/table/UiTableBody.vue`
- `frontend/web/src/ui/table/UiTableRow.vue`
- `frontend/web/src/ui/table/UiTableHead.vue`
- `frontend/web/src/ui/table/UiTableCell.vue`
- `frontend/web/src/ui/table/UiTableEmpty.vue`
- `frontend/web/src/ui/table/UiTable.stories.ts`
- `frontend/web/src/ui/table/UiTable.test.ts`
After: none
Change:
- `UiTable`:
  - Renders native `<table data-ui-table>` element.
  - Props: `dense?: boolean`, `stickyHeader?: boolean`.
  - Provides table context (`{ dense, stickyHeader }`) via Vue `provide` so
    child heads and cells adjust padding and sticky behavior automatically.
  - Styled with `w-full caption-bottom text-sm border-collapse text-left`.
- `UiTableHeader`:
  - Renders `<thead>`.
  - Styled with `[&_tr]:border-b border-border bg-subtle`.
- `UiTableBody`:
  - Renders `<tbody>`.
  - Styled with `[&_tr:last-child]:border-0`.
- `UiTableRow`:
  - Renders `<tr>`.
  - Props: `selected?: boolean`.
  - Sets `:data-state="selected ? 'selected' : undefined"`.
  - Styled with `border-b border-border transition-colors hover:bg-hover data-[state=selected]:bg-subtle`.
- `UiTableHead`:
  - Renders `<th>`.
  - Props: `sortable?: boolean`, `sortDirection?: 'ascending' | 'descending' | 'none'`, `align?: 'left' | 'center' | 'right' | 'numeric'`.
  - Emits: `sort`.
  - Injects `stickyHeader`; when true, applies `sticky top-0 z-10 bg-subtle backdrop-blur-xs`.
  - When `sortable` is true, renders an inner `<button class="inline-flex items-center gap-1 font-medium text-xs text-muted-foreground hover:text-foreground">`
    with sort indicator arrow (`↑` for ascending, `↓` for descending).
  - Sets `aria-sort` to `'ascending' | 'descending' | 'none'` (omitted when not sortable).
  - Handles alignment: `text-right` when `numeric` or `right`, `text-center` when `center`, default `text-left`.
  - Injects `dense` to adjust padding (`py-2 px-3` vs `py-3 px-4`).
- `UiTableCell`:
  - Renders `<td>`.
  - Props: `align?: 'left' | 'center' | 'right' | 'numeric'`, `mono?: boolean`.
  - Handles alignment and typography (`font-mono tabular-nums text-xs` for numeric/mono).
  - Injects `dense` to adjust cell height and padding (`py-2 px-3` vs `py-3.5 px-4`).
- `UiTableEmpty`:
  - Props: `colSpan: number`.
  - Renders `<tr><td :colspan="colSpan" class="p-8 text-center text-muted-foreground"><slot /></td></tr>`.
- Co-located CSF 3 stories in `UiTable.stories.ts` demonstrating: default data table,
  dense table, sortable headers with toggling, sticky header in fixed-height container,
  row selection with checkboxes, and empty state.
Tests:
- `frontend/web/src/ui/table/UiTable.test.ts` asserts:
  - `UiTableHead` renders sort button when `sortable` is true and sets `aria-sort="ascending"` / `aria-sort="descending"`.
  - Clicking a sortable header emits the `sort` event.
  - Dense mode cascades down to adjust cell padding classes.
  - Selected row sets `data-state="selected"`.
  - Row order remains unchanged in DOM when cell values are mutated if keys are stable.
Verify:
`.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-09-26-2329-feat-web-design-system-phase4-plan.md`

### U2. Data presentation and feedback components: EmptyState, Skeleton, Meter, Progress, Pagination
Files:
- `frontend/web/src/ui/empty-state/UiEmptyState.vue`
- `frontend/web/src/ui/empty-state/UiEmptyState.stories.ts`
- `frontend/web/src/ui/empty-state/UiEmptyState.test.ts`
- `frontend/web/src/ui/skeleton/UiSkeleton.vue`
- `frontend/web/src/ui/skeleton/UiSkeleton.stories.ts`
- `frontend/web/src/ui/skeleton/UiSkeleton.test.ts`
- `frontend/web/src/ui/meter/UiMeter.vue`
- `frontend/web/src/ui/meter/UiSegmentedMeter.vue`
- `frontend/web/src/ui/meter/UiMeter.stories.ts`
- `frontend/web/src/ui/meter/UiMeter.test.ts`
- `frontend/web/src/ui/progress/UiProgress.vue`
- `frontend/web/src/ui/progress/UiProgress.stories.ts`
- `frontend/web/src/ui/progress/UiProgress.test.ts`
- `frontend/web/src/ui/pagination/UiPagination.vue`
- `frontend/web/src/ui/pagination/UiPagination.stories.ts`
- `frontend/web/src/ui/pagination/UiPagination.test.ts`
After: none
Change:
- `UiEmptyState`:
  - Props: `title: string`, `description?: string`.
  - Slots: `#icon`, `#title`, `#description`, `#actions`, default.
  - Styled with `flex flex-col items-center justify-center p-8 text-center text-muted-foreground gap-3`. Renders heading with `text-sm font-semibold text-foreground` and description with `text-xs text-muted-foreground`.
- `UiSkeleton`:
  - Props: `variant?: 'text' | 'circular' | 'rectangular'`, `width?: string | number`, `height?: string | number`.
  - Renders placeholder element with `bg-subtle animate-pulse rounded-control motion-reduce:animate-none`. `circular` uses `rounded-full`.
- `UiMeter`:
  - Replaces `ResourceMeter.vue`.
  - Props: `label: string`, `value: number`, `min?: number` (default 0), `max?: number` (default 100), `unit?: string` (default `"%"`), `detail?: string`, `tone?: 'normal' | 'warning' | 'critical' | 'auto'`, `thresholds?: { warning: number; critical: number }` (default `{ warning: 75, critical: 90 }`).
  - Automatically computes tone from thresholds when `tone="auto"` or omitted: critical at >=90, warning at >=75, normal below.
  - Renders label, numeric value in monospace tabular figures, optional detail string, and track with `role="meter"`, `aria-label`, `aria-valuemin`, `aria-valuemax`, `aria-valuenow`.
  - Progress fill bar styled according to tone: normal (`bg-chart-1`), warning (`bg-warning-foreground`), critical (`bg-danger-foreground`).
- `UiSegmentedMeter`:
  - Replaces `HealthBar.vue`.
  - Props: `segments?: Array<{ label: string; count: number; tone: 'success' | 'warning' | 'danger' | 'info' | 'empty' }>`, `counts?: Record<string, number>`, `legend?: boolean`, `label?: string`.
  - If `counts` is passed, normalizes into standard segments using the order Healthy (`success`), Degraded (`warning`), Offline (`danger`).
  - Computes total count and formatted summary string for `aria-label`.
  - Renders flex track with `role="img"`, `:aria-label="summary"`. Each non-zero segment renders `<span class="h-full transition-all" :style="{ flexGrow: segment.count }" :title="`${segment.count} ${segment.label}`">` styled with semantic status colors (`bg-success-foreground`, `bg-warning-foreground`, `bg-danger-foreground`, `bg-subtle`).
  - When `legend` is true, renders `<ul class="flex flex-wrap gap-4 text-xs text-muted-foreground mt-2">` with dot indicator `<i class="w-2 h-2 rounded-full inline-block" aria-hidden="true">`, label, and bold count.
- `UiProgress`:
  - Wraps Reka UI's `ProgressRoot` and `ProgressIndicator`.
  - Props: `modelValue?: number | null`, `max?: number` (default 100), `size?: 'sm' | 'md' | 'lg'`, `variant?: 'default' | 'accent' | 'success' | 'warning' | 'danger'`.
  - `ProgressRoot` styled with `relative overflow-hidden rounded-full bg-subtle w-full`. `ProgressIndicator` styled with `h-full w-full bg-primary transition-transform duration-300 ease-out` and binds `:style="{ transform: `translateX(-${100 - (modelValue ?? 0)}%)` }"`.
  - Handles indeterminate state (when `modelValue == null`) with a moving CSS pulse/slide animation.
- `UiPagination`:
  - Wraps Reka UI's `PaginationRoot`, `PaginationList`, `PaginationListItem`, `PaginationFirst`, `PaginationPrev`, `PaginationNext`, `PaginationLast`, `PaginationEllipsis`.
  - Props: `total: number`, `itemsPerPage?: number` (default 10), `page?: number`, `defaultPage?: number` (default 1), `siblingCount?: number` (default 1), `showEdges?: boolean` (default false).
  - Emits: `update:page`.
  - Renders accessible navigation list with previous/next buttons and page item buttons styled using `UiButton` ghost/outline styles. Active page button carries `bg-primary text-primary-foreground font-medium`.
- Co-located CSF 3 stories for each component.
Tests:
- `UiEmptyState.test.ts` asserts:
  - Renders title, description, and custom action slot.
- `UiSkeleton.test.ts` asserts:
  - Applies variant classes (`text`, `circular`, `rectangular`) and pulse animation.
- `UiMeter.test.ts` asserts:
  - Single meter sets `role="meter"`, ARIA value bounds, and computes warning/critical tones based on percentage.
  - Segmented meter computes summary `aria-label` and renders proportional segment flex-grow values and legend list.
- `UiProgress.test.ts` asserts:
  - Determinate progress sets `aria-valuenow` on `ProgressRoot` and applies transform style.
  - Indeterminate progress sets `data-state="indeterminate"`.
- `UiPagination.test.ts` asserts:
  - Renders correct number of pages and ellipsis for large item counts.
  - Emits `update:page` on button click.
Verify:
`.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-09-26-2329-feat-web-design-system-phase4-plan.md`

### U3. Chart, sparkline, and topology graph semantic color restyling
Files:
- `frontend/web/src/components/TrafficChart.vue`
- `frontend/web/src/components/TrafficChart.stories.ts`
- `frontend/web/src/components/TrafficSparkline.vue`
- `frontend/web/src/components/TrafficSparkline.stories.ts`
- `frontend/web/.storybook/preview.ts`
- `frontend/web/src/dashboard.css`
- `frontend/web/src/style.css`
After: none
Change:
- `TrafficChart.vue`:
  - Update SVG area and line styling:
    - Replace `--accent-foreground` with `--chart-1`.
    - Line uses `stroke: var(--chart-1)` with stroke width 2.
    - Area fill uses `color-mix(in srgb, var(--chart-1) 12%, transparent)`.
    - Crosshair and active dot use `var(--chart-1)` and `var(--card)`.
    - Gridlines use `var(--border)` and text labels use `var(--muted-foreground)`.
  - Accessibility: direct text labels or legend accompany any series display. Screen reader fallback `<table>` retained and kept in sync with points.
- `TrafficSparkline.vue`:
  - Path line stroke uses `var(--chart-1)`.
  - Path area fill uses `color-mix(in srgb, var(--chart-1) 14%, transparent)`.
  - Adds optional prop `color?: 'chart-1' | 'chart-2' | 'chart-3' | 'chart-4' | 'chart-5' | 'chart-6'`, defaulting to `'chart-1'`.
- `TopologyLink.vue` styling in `src/style.css`:
  - Animated traffic flow `.topology-link-flow` uses `stroke: var(--chart-1)`.
  - Baseline edge path `.topology-link .vue-flow__edge-path` retains `stroke: var(--graph-edge)`.
  - Status overrides retain `var(--warning-border)` (degraded) and `var(--danger-border)` (offline).
  - Selected state retains `var(--accent-foreground)`.
- `.storybook/preview.ts`:
  - Add `import '../src/dashboard.css'` so chart classes render properly in Storybook.
- CSF 3 stories for `TrafficChart` and `TrafficSparkline`:
  - `TrafficChart.stories.ts` renders single-series throughput in light and dark mode with accessibility fallback table.
  - `TrafficSparkline.stories.ts` renders sparklines in `chart-1` through `chart-6` color variants.
Tests:
- Storybook build and tests verify:
  - `TrafficChart` renders paths with `var(--chart-1)` and includes accessible fallback `<table>`.
  - `TrafficSparkline` renders with designated chart token.
Verify:
`.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-09-26-2329-feat-web-design-system-phase4-plan.md`

### U4. UI barrel export and Storybook accessibility test suite in CI
Files:
- `frontend/web/src/ui/index.ts`
After: U1, U2
Change:
- Append all new data display components and their exported prop types to `frontend/web/src/ui/index.ts`:
  - `UiTable` from `./table/UiTable.vue`
  - `UiTableHeader` from `./table/UiTableHeader.vue`
  - `UiTableBody` from `./table/UiTableBody.vue`
  - `UiTableRow` from `./table/UiTableRow.vue`
  - `UiTableHead` from `./table/UiTableHead.vue`
  - `UiTableCell` from `./table/UiTableCell.vue`
  - `UiTableEmpty` from `./table/UiTableEmpty.vue`
  - `UiEmptyState` from `./empty-state/UiEmptyState.vue`
  - `UiSkeleton` from `./skeleton/UiSkeleton.vue`
  - `UiMeter` from `./meter/UiMeter.vue`
  - `UiSegmentedMeter` from `./meter/UiSegmentedMeter.vue`
  - `UiProgress` from `./progress/UiProgress.vue`
  - `UiPagination` from `./pagination/UiPagination.vue`
- `src/ui/a11y.test.ts`:
  - Automatically discovers stories across table, empty-state, skeleton, meter,
    progress, and pagination via `import.meta.glob('./**/*.stories.ts')`.
  - Runs axe-core accessibility checks on composed stories and verifies zero
    violations across all data display components.
Tests:
- `pnpm test src/ui/a11y.test.ts` passes with zero violations across all data display stories.
Verify:
`.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-09-26-2329-feat-web-design-system-phase4-plan.md`

### U5. View migration, legacy component removal, and documentation
Files:
- `frontend/web/src/WorkspacePage.vue`
- `frontend/web/src/ClientsView.vue`
- `frontend/web/src/DashboardView.vue`
- `frontend/web/src/components/topology/TopologyInspector.vue`
- `frontend/web/src/style.css`
- `frontend/web/src/components/ResourceMeter.vue`
- `frontend/web/src/components/HealthBar.vue`
- `frontend/web/README.md`
After: U3, U4
Change:
- Scope legacy table rules in `src/style.css`:
  - Update unlayered `table`, `th`, `td` rules to `table:not([data-ui-table])`,
    `table:not([data-ui-table]) th`, `table:not([data-ui-table]) td` so `UiTable`
    Tailwind utility classes take precedence.
- Migrate `WorkspacePage.vue`:
  - Replace raw `<table>` markup inside `<ScrollArea>` with `UiTable`,
    `UiTableHeader`, `UiTableBody`, `UiTableRow`, `UiTableHead`, `UiTableCell`.
  - Connect device sort button to `UiTableHead` sortable prop with
    `:sort-direction="ascending ? 'ascending' : 'descending'"` and `@sort="ascending = !ascending"`.
  - Verify device row stability: updating telemetry (throughput/clients) does
    not reorder table rows.
- Migrate `ClientsView.vue`:
  - Replace raw `<table>` inside `<ScrollArea>` with `UiTable`, `UiTableHeader`,
    `UiTableBody`, `UiTableRow`, `UiTableHead`, `UiTableCell`.
  - Replace empty state `.empty` with `UiEmptyState`, slotting `AppIcon` into `#icon`.
  - Replace `.table-footer` "Showing X of Y / Show more" with `UiPagination`.
  - Add page-based pagination state: `const page = ref(1)`, `const pageSize = 50`,
    `const paginated = computed(() => filtered.value.slice((page.value - 1) * pageSize, page.value * pageSize))`.
- Migrate `DashboardView.vue`:
  - Replace `.dash-table` with `UiTable`, `UiTableHeader`, `UiTableBody`,
    `UiTableRow`, `UiTableHead`, `UiTableCell`.
  - Replace `HealthBar` imports and usages with `UiSegmentedMeter` from `src/ui`,
    passing `:counts="counts"` and `:counts="rollup.health"`.
  - Confirm `TrafficChart` renders updated `chart-1` styling.
- Migrate `TopologyInspector.vue`:
  - Replace `ResourceMeter` imports and usages with `UiMeter` from `src/ui`,
    passing `:value="telemetry.cpu"`, `:value="telemetry.memory"`, and
    `:value="telemetry.bandwidth"`.
  - Confirm `TrafficSparkline` renders updated `chart-1` styling.
- Delete legacy component files:
  - `frontend/web/src/components/ResourceMeter.vue`
  - `frontend/web/src/components/HealthBar.vue`
- Update `frontend/web/README.md`:
  - Document `UiTable`, `UiEmptyState`, `UiSkeleton`, `UiMeter`, `UiSegmentedMeter`,
    `UiProgress`, and `UiPagination`.
  - Document categorical chart color tokens (`chart-1` to `chart-6`) and color
    accessibility rules.
Tests:
- All view test suites pass (`src/navigation/dock.test.ts`, `src/navigation/shortcuts.test.ts`, `src/domain/*.test.ts`).
- Grep assertions verify no legacy references remain:
  - `git grep "components/ResourceMeter" frontend/web/src` prints nothing.
  - `git grep "components/HealthBar" frontend/web/src` prints nothing.
Verify:
`.claude/skills/verify-change/scripts/verify-change.sh -- docs/plans/2026-09-26-2329-feat-web-design-system-phase4-plan.md frontend/web/README.md`

Waves: U1 U2 U3 | U4 | U5

## Verification

From `frontend/web/`:

```sh
pnpm typecheck
pnpm test
pnpm lint
pnpm format:check
pnpm build
pnpm build-storybook
```

Verify legacy component references cleanup:

```sh
git grep -rnE "components/(ResourceMeter|HealthBar)" src
```

The command must print nothing.

Manual and browser checks:
1. Run `pnpm storybook` and navigate to `Ui/Table`, `Ui/Meter`, `Ui/Progress`,
   `Ui/Pagination`, `Ui/EmptyState`, `Ui/Skeleton`, and `TrafficChart`
   stories. Verify light and dark mode appearance, states, and accessibility
   tags.
2. In `pnpm dev`, open `/devices` and click the "Device name" sort header.
   Verify the sort arrow toggles and row order updates. As live throughput
   updates, verify that rows retain their position without jumping.
3. In `pnpm dev`, open `/clients`. Verify the clients table renders with
   `UiTable`, page navigation operates with `UiPagination`, and filtering to a
   non-matching query displays `UiEmptyState`.
4. In `pnpm dev`, open `/dashboard`. Verify `TrafficChart` displays `chart-1`
   cyan stroke and fill, and the sites table renders `UiSegmentedMeter`
   health bars.
5. In `pnpm dev`, open `/topology` and select a node. Verify the inspector
   displays `UiMeter` for CPU, memory, and bandwidth, and sparklines render
   with `chart-1`.

## Definition of done

- [x] Verifier green for every changed path, and all verification commands pass.
- [x] All data display components (`UiTable`, `UiEmptyState`, `UiSkeleton`,
      `UiMeter`, `UiSegmentedMeter`, `UiProgress`, `UiPagination`) created
      under disjoint directories in `src/ui/` with CSF 3 stories and passing
      axe accessibility checks.
- [x] `TrafficChart`, `TrafficSparkline`, and `TopologyLink` restyled to read
      `chart-1` through `chart-6` and `graph-edge` tokens, meeting the
      categorical color and non-color-alone accessibility rules.
- [x] Device, client, and dashboard tables migrated to `UiTable`; legacy
      `ResourceMeter.vue` and `HealthBar.vue` removed.
- [x] `frontend/web/README.md` updated.
- [x] This plan's `status` is set to `implemented`.
- [x] No plan labels in code, comments, or commit messages.

## Open questions

- None. Design choices (controlled table sorting, discrete pagination in ClientsView, legacy CSS scoping, and chart token restyling) are settled under Decisions.
