---
title: Web Design System Phase 3, Overlays and Navigation - Plan
type: feat
date: 2026-09-26
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-26-2329-feat-web-design-system-plan.md
---

# Web Design System Phase 3, Overlays and Navigation - Plan

## Goal

`frontend/web/src/ui/` gains overlay and navigation components styled with semantic tokens: `UiDialog`, `UiAlertDialog`, `UiPopover`, `UiDropdownMenu`, `UiTabs`, `UiToast`, `UiCombobox`, `UiScrollArea` (replacing `ScrollArea.vue`), and `UiBreadcrumb`. Interactive overlays wrap Reka UI primitives, while `UiBreadcrumb` structures semantic HTML navigation markup. Each component pairs with Storybook stories and axe accessibility tests. `AccountMenu`, `ScopeSwitcher`, `TenantSwitcher`, `HelpButton`, `ReportBugButton`, and `GlobalSearch` rebuild on these primitives. Manual coordinate math, native popover attributes, and hand-rolled keyboard focus loops are removed.

This plan is wrong if Reka's `Combobox` cannot keep `GlobalSearch`'s recent-search and scoped-result behavior (`src/components/recentSearches.ts`, `src/domain/search.ts`) without re-implementing its keyboard model.

## Decisions

- Overlays render in the top layer through Reka portals (`DialogPortal`, `PopoverPortal`, `DropdownMenuPortal`). Scrims use the `overlay` semantic token (`var(--overlay)`). Surface containers use `popover` (`var(--popover)`) with `shadow-lg`. Why: adheres to the phase 1 token foundations and elevation scale in `src/theme/tokens.css`.
- Overlay entrance transitions run in 100–160 ms with `--ease-out` (`cubic-bezier(0.2, 0, 0, 1)`). Overlay exit is immediate. Why: the motion rules in `frontend/web/README.md` require prompt dismissals so animation never holds focus or delays user action.
- Component styling uses `tailwind-variants` (`tv()`) for components with visual variants (`UiToast`, `UiAlertDialog`, `UiTabs`, `UiDialog`). Why: the architecture direction in `docs/architecture/2026-09-26-web-design-system-direction.md` forbids manual class concatenation and delegates conflict resolution to `tailwind-merge`.
- All Phase 3 components reside in disjoint subdirectories under `src/ui/` (`dialog/`, `alert-dialog/`, `popover/`, `dropdown-menu/`, `tabs/`, `toast/`, `combobox/`, `scroll-area/`, `breadcrumb/`). They share only the append-only `src/ui/index.ts` barrel export. Why: guarantees parallel worktree execution alongside Phase 4 (`table/`, `empty/`, `skeleton/`, `meter/`, `progress/`, `pagination/`) without file conflicts.
- `UiDialog` supports both structured dialogs and custom command palettes through a `headless` prop. Standard mode renders a header with title, description, and close button. Headless mode omits the header chrome and inner padding so `GlobalSearch` can render a full-width search input and custom footer. Why: avoids maintaining separate modal wrappers for standard dialogs and command palettes.
- `UiAlertDialog` implements `role="alertdialog"` for destructive confirmations. Outside click dismissal is disabled (`disableOutsidePointerEvents: true`), requiring an explicit Action or Cancel button click. Why: adheres to WAI-ARIA alert dialog patterns for critical operations.
- `AccountMenu` migrates to `UiDropdownMenu`. It uses the `#trigger` slot for its operator avatar and renders the disabled "Log out" item. Manual `getBoundingClientRect` positioning and native `popover="auto"` attributes are deleted. Why: Reka provides roving focus, keyboard navigation, and menuitem semantics out of the box.
- `UiCombobox` wraps Reka's `Combobox` primitives and supports `ignoreFilter: true` for external search providers. It exposes scoped slots `#item="{ option, selected, active }"` and `#group-header="{ group }"`. `ScopeSwitcher` uses `UiCombobox` to render custom avatars and nested hierarchy indents. `GlobalSearch` composes `UiDialog` and `UiCombobox`, keeping its recent searches (`loadRecent`, `rememberRecent`, `clearRecent`) and `searchAll()` queries. Why: Reka manages focus, arrow key navigation, and ARIA attributes, while application components retain domain search filtering and history.
- `UiScrollArea` replaces `src/components/ScrollArea.vue` in place. It wraps Reka `ScrollArea` primitives styled with Tailwind utilities (`bg-border` thumb with `hover:bg-muted-foreground`), and exposes `element` (`ComputedRef<HTMLElement | undefined>`). Importers across the application update to import `UiScrollArea` from `src/ui`. Why: unifies scrollbar styling on semantic tokens and preserves the scrolling element contract required by `src/navigation/PageHost.vue`.
- `UiBreadcrumb` provides semantic navigation markup (`<nav aria-label="Breadcrumb">` wrapping an `<ol>`) with composable subcomponents: `UiBreadcrumbList`, `UiBreadcrumbItem`, `UiBreadcrumbLink`, `UiBreadcrumbPage`, and `UiBreadcrumbSeparator`. `FleetView.vue` migrates topbar and side pane breadcrumbs to this structure, nesting `TenantSwitcher` and `ScopeSwitcher` within breadcrumb items. Why: standardizes accessible breadcrumb semantics while maintaining existing switcher layouts.
- `UiToast` wraps Reka's `Toast` primitives with `UiToastProvider` and a `useToast()` composable. It provides auto-dismiss timers, semantic variants (`default`, `success`, `danger`, `warning`), and action buttons. Why: prepares for asynchronous feedback in subsequent phases while cataloguing toast behavior in Storybook.
- Escape closes only the topmost overlay layer. Why: Reka's `DismissableLayer` stack handles nested layers, dismissing an open combobox or tooltip without closing the parent dialog.
- CI accessibility checks in `src/ui/a11y.test.ts` audit teleported overlay portals mounted to `document.body` with `{ rules: { 'color-contrast': { enabled: false }, region: { enabled: false } } }`. Why: happy-dom teleports portal content outside the local container to `document.body`, and disabling the `region` rule avoids false positives on isolated story snippets lacking top-level landmark elements.

## Requirements

1. Overlays render into the top layer via portals and trap keyboard focus while active. Example: mounting `UiDialog` with `open="true"` teleports the overlay into `document.body` with `role="dialog"` and `aria-modal="true"`; pressing Tab cycles focus exclusively within the dialog.
2. Escape dismisses only the topmost active overlay layer. Example: when a `UiTooltip` or `UiCombobox` is active inside a `UiDialog`, pressing Escape closes the inner overlay and leaves `UiDialog` open; pressing Escape again closes `UiDialog`.
3. Switchers and menus preserve current keyboard navigation and SSR behavior. Example: `TenantSwitcher.test.ts` passes unchanged using `renderToString(createSSRApp(TenantSwitcher, ...))` asserting `aria-haspopup="listbox"` and unavailable selection labeling; in the browser, Up/Down arrow keys highlight options and Enter selects.
4. `UiCombobox` preserves external search filtering and keyboard model in `GlobalSearch`. Example: opening `GlobalSearch` with an empty query displays recent searches; typing "gw-01" queries `searchAll()`, Up/Down arrow highlights items, Shift+Enter emits `select` with `beside: true`, and Alt+Enter emits `dock`.
5. `UiScrollArea` exposes its underlying viewport element and replaces `ScrollArea.vue` across all current views. Example: `PageHost.vue` accesses `scroller.value?.element`, and `git grep "components/ScrollArea" frontend/web/src` prints nothing after `ScrollArea.vue` is deleted.
6. `UiBreadcrumb` renders accessible breadcrumb semantics. Example: `<UiBreadcrumb><UiBreadcrumbList><UiBreadcrumbItem><UiBreadcrumbLink href="/devices">Devices</UiBreadcrumbLink></UiBreadcrumbItem><UiBreadcrumbSeparator /><UiBreadcrumbItem><UiBreadcrumbPage>gw-01</UiBreadcrumbPage></UiBreadcrumbItem></UiBreadcrumbList></UiBreadcrumb>` renders `<nav aria-label="Breadcrumb"><ol>...` with `aria-current="page"` on "gw-01".
7. Automated axe accessibility checks pass with zero violations across all Phase 3 stories. Example: `pnpm test src/ui/a11y.test.ts` executes axe on composed stories for all nine new components and exits 0.

## Out of scope

- Data display components (`UiTable`, `UiEmptyState`, `UiSkeleton`, `UiMeter`, `UiProgress`, `UiPagination`), scheduled for Phase 4.
- App migration of table views, charts, and dissolving `src/style.css` / enabling Preflight, scheduled for Phase 5.
- Authentication backend integration or real logout endpoint (the "Log out" menuitem remains disabled with explanatory tooltip as documented in `frontend/web/README.md`).

## Units

### U1. Core overlay components: Dialog, AlertDialog, Popover
Files:
- `frontend/web/src/ui/dialog/UiDialog.vue`
- `frontend/web/src/ui/dialog/UiDialog.stories.ts`
- `frontend/web/src/ui/dialog/UiDialog.test.ts`
- `frontend/web/src/ui/alert-dialog/UiAlertDialog.vue`
- `frontend/web/src/ui/alert-dialog/UiAlertDialog.stories.ts`
- `frontend/web/src/ui/alert-dialog/UiAlertDialog.test.ts`
- `frontend/web/src/ui/popover/UiPopover.vue`
- `frontend/web/src/ui/popover/UiPopover.stories.ts`
- `frontend/web/src/ui/popover/UiPopover.test.ts`
After: none
Change:
- `UiDialog`:
  - Wraps Reka `DialogRoot`, `DialogTrigger`, `DialogPortal`, `DialogOverlay`, `DialogContent`, `DialogTitle`, `DialogDescription`, `DialogClose`.
  - Props: `open?: boolean`, `title?: string`, `description?: string`, `headless?: boolean`. Supports `v-model:open`.
  - Declares dialog size variants with `tailwind-variants` (`tv()`): `sm` (`max-w-sm`), `md` (`max-w-lg`, default), `lg` (`max-w-2xl`).
  - Scrim styles: `bg-overlay fixed inset-0 z-50 backdrop-blur-xs transition-opacity duration-140 ease-out`.
  - Standard content container: `bg-popover text-foreground border border-border shadow-lg rounded-panel fixed top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 z-50 p-6 w-full focus:outline-none`.
  - Headless content container: `bg-popover text-foreground border border-border shadow-lg rounded-panel fixed top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 z-50 p-0 w-full overflow-hidden focus:outline-none` (omits header chrome and padding for command palette layouts).
  - Title styles: `text-lg font-semibold text-foreground`. Description styles: `text-sm text-muted-foreground`.
  - Header close button carries accessible `aria-label="Close"`.
- `UiAlertDialog`:
  - Wraps Reka `AlertDialogRoot`, `AlertDialogTrigger`, `AlertDialogPortal`, `AlertDialogOverlay`, `AlertDialogContent`, `AlertDialogTitle`, `AlertDialogDescription`, `AlertDialogAction`, `AlertDialogCancel`.
  - Props: `open?: boolean`, `title: string`, `description: string`, `confirmText?: string`, `cancelText?: string`, `destructive?: boolean`. Supports `v-model:open`. Emits `confirm` and `cancel`.
  - Declares button variants with `tailwind-variants` (`tv()`): Action uses `danger` when `destructive: true`, else `primary`. Cancel uses `secondary`.
  - Scrim styles: `bg-overlay fixed inset-0 z-50`. Disables outside pointer events (`:disable-outside-pointer-events="true"`).
  - Content container: `bg-popover text-foreground border border-border shadow-lg rounded-panel fixed top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 z-50 p-6 w-full max-w-md focus:outline-none`.
- `UiPopover`:
  - Wraps Reka `PopoverRoot`, `PopoverTrigger`, `PopoverAnchor`, `PopoverPortal`, `PopoverContent`, `PopoverClose`.
  - Props: `open?: boolean`, `side?: 'top' | 'right' | 'bottom' | 'left'`, `align?: 'start' | 'center' | 'end'`, `sideOffset?: number`. Supports `v-model:open`.
  - Content styles: `bg-popover text-foreground border border-border shadow-lg rounded-control p-3 z-50 focus:outline-none max-w-xs`.
  - Supports collision padding, arrow keys, and dismissal on Escape or outside click.
- Co-located CSF 3 stories for each component covering open states, trigger variants, and both light and dark themes.
Tests:
- `frontend/web/src/ui/dialog/UiDialog.test.ts` asserts:
  - Dialog opens on trigger click and mounts content in top-layer portal with `role="dialog"`.
  - Escape closes dialog and returns focus to trigger.
  - Clicking overlay scrim closes dialog.
- `frontend/web/src/ui/alert-dialog/UiAlertDialog.test.ts` asserts:
  - Alert dialog renders with `role="alertdialog"`.
  - Clicking overlay scrim does not close dialog.
  - Action button emits confirm and Cancel button emits cancel.
- `frontend/web/src/ui/popover/UiPopover.test.ts` asserts:
  - Popover opens on trigger click and displays content positioned relative to trigger.
  - Escape dismisses popover.
  - Outside click dismisses popover.
Verify:
`.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui/dialog/UiDialog.vue frontend/web/src/ui/dialog/UiDialog.test.ts frontend/web/src/ui/alert-dialog/UiAlertDialog.vue frontend/web/src/ui/alert-dialog/UiAlertDialog.test.ts frontend/web/src/ui/popover/UiPopover.vue frontend/web/src/ui/popover/UiPopover.test.ts`
then `pnpm typecheck && pnpm test && pnpm lint && pnpm build-storybook` in `frontend/web/`.

### U2. Navigation and disclosure components: DropdownMenu, Tabs, Breadcrumb
Files:
- `frontend/web/src/ui/dropdown-menu/UiDropdownMenu.vue`
- `frontend/web/src/ui/dropdown-menu/UiDropdownMenuItem.vue`
- `frontend/web/src/ui/dropdown-menu/UiDropdownMenuSeparator.vue`
- `frontend/web/src/ui/dropdown-menu/UiDropdownMenu.stories.ts`
- `frontend/web/src/ui/dropdown-menu/UiDropdownMenu.test.ts`
- `frontend/web/src/ui/tabs/UiTabs.vue`
- `frontend/web/src/ui/tabs/UiTabs.stories.ts`
- `frontend/web/src/ui/tabs/UiTabs.test.ts`
- `frontend/web/src/ui/breadcrumb/UiBreadcrumb.vue`
- `frontend/web/src/ui/breadcrumb/UiBreadcrumbList.vue`
- `frontend/web/src/ui/breadcrumb/UiBreadcrumbItem.vue`
- `frontend/web/src/ui/breadcrumb/UiBreadcrumbLink.vue`
- `frontend/web/src/ui/breadcrumb/UiBreadcrumbPage.vue`
- `frontend/web/src/ui/breadcrumb/UiBreadcrumbSeparator.vue`
- `frontend/web/src/ui/breadcrumb/UiBreadcrumb.stories.ts`
- `frontend/web/src/ui/breadcrumb/UiBreadcrumb.test.ts`
After: none
Change:
- `UiDropdownMenu`:
  - Wraps Reka `DropdownMenuRoot`, `DropdownMenuTrigger`, `DropdownMenuPortal`, `DropdownMenuContent`.
  - Exposes `#trigger` slot for custom trigger elements (such as `AccountMenu` avatar button) and default slot for menu items.
  - Content container styles: `bg-popover text-foreground border border-border shadow-lg rounded-control p-1 z-50 min-w-[10rem] focus:outline-none`.
  - `UiDropdownMenuItem` wraps `DropdownMenuItem`. Item styles: `relative flex cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-hover data-[disabled]:pointer-events-none data-[disabled]:opacity-50 text-foreground`.
  - `UiDropdownMenuSeparator` wraps `DropdownMenuSeparator`. Separator styles: `my-1 h-[1px] bg-border`.
  - Roving keyboard navigation, arrow key traversal, typeahead matching, and focus restoration.
- `UiTabs`:
  - Wraps Reka `TabsRoot`, `TabsList`, `TabsTrigger`, `TabsContent`.
  - Props: `modelValue?: string`, `defaultValue?: string`, `orientation?: 'horizontal' | 'vertical'`. Supports `v-model`.
  - Declares trigger variants with `tailwind-variants` (`tv()`): `state: { active: 'bg-card text-foreground shadow-xs', inactive: 'text-muted-foreground hover:text-foreground' }`.
  - List styles: `inline-flex items-center justify-center rounded-control bg-subtle p-1 text-muted-foreground border border-border`.
  - Trigger styles: `inline-flex items-center justify-center whitespace-nowrap rounded-sm px-3 py-1.5 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50`.
  - Content styles: `mt-2 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring`.
- `UiBreadcrumb`:
  - Composable set: `UiBreadcrumb` (`<nav aria-label="Breadcrumb">`), `UiBreadcrumbList` (`<ol class="flex flex-wrap items-center gap-1.5 break-words text-sm text-muted-foreground">`), `UiBreadcrumbItem` (`<li class="inline-flex items-center gap-1.5">`), `UiBreadcrumbLink` (`<a class="transition-colors hover:text-foreground">` or `as-child` support for `RouterLink`), `UiBreadcrumbPage` (`<span role="link" aria-disabled="true" aria-current="page" class="font-medium text-foreground">`), `UiBreadcrumbSeparator` (`<li role="presentation" aria-hidden="true" class="text-muted-foreground">/</li>`).
- Co-located CSF 3 stories for each component covering variants, interactive tabs, submenus, breadcrumbs with switchers, and light/dark themes.
Tests:
- `frontend/web/src/ui/dropdown-menu/UiDropdownMenu.test.ts` asserts:
  - Menu opens on trigger click.
  - Arrow keys navigate items with roving highlight.
  - Disabled item is skipped by arrow navigation and rejects clicks.
  - Selection emits event and closes menu.
- `frontend/web/src/ui/tabs/UiTabs.test.ts` asserts:
  - Tab trigger activates corresponding tab panel content.
  - Left/Right (or Up/Down) arrow keys navigate tab triggers.
  - Disabled tab cannot be activated.
- `frontend/web/src/ui/breadcrumb/UiBreadcrumb.test.ts` asserts:
  - Renders semantic `<nav aria-label="Breadcrumb">` and `<ol>`.
  - Current page element receives `aria-current="page"`.
  - Separators receive `aria-hidden="true"`.
Verify:
`.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui/dropdown-menu/UiDropdownMenu.vue frontend/web/src/ui/dropdown-menu/UiDropdownMenu.test.ts frontend/web/src/ui/tabs/UiTabs.vue frontend/web/src/ui/tabs/UiTabs.test.ts frontend/web/src/ui/breadcrumb/UiBreadcrumb.vue frontend/web/src/ui/breadcrumb/UiBreadcrumb.test.ts`
then `pnpm typecheck && pnpm test && pnpm lint && pnpm build-storybook` in `frontend/web/`.

### U3. Scroller, notification, and command primitives: ScrollArea, Toast, Combobox
Files:
- `frontend/web/src/ui/scroll-area/UiScrollArea.vue`
- `frontend/web/src/ui/scroll-area/UiScrollArea.stories.ts`
- `frontend/web/src/ui/scroll-area/UiScrollArea.test.ts`
- `frontend/web/src/ui/toast/UiToast.vue`
- `frontend/web/src/ui/toast/UiToastProvider.vue`
- `frontend/web/src/ui/toast/useToast.ts`
- `frontend/web/src/ui/toast/UiToast.stories.ts`
- `frontend/web/src/ui/toast/UiToast.test.ts`
- `frontend/web/src/ui/combobox/UiCombobox.vue`
- `frontend/web/src/ui/combobox/UiCombobox.stories.ts`
- `frontend/web/src/ui/combobox/UiCombobox.test.ts`
After: none
Change:
- `UiScrollArea`:
  - Wraps Reka `ScrollAreaRoot`, `ScrollAreaViewport`, `ScrollAreaScrollbar`, `ScrollAreaThumb`, `ScrollAreaCorner`.
  - Props: `axis?: 'y' | 'x' | 'both'`, `viewportClass?: unknown`, `label?: string`. Defaults `axis: 'y'`.
  - Exposes `element: ComputedRef<HTMLElement | undefined>` for parent components reading scroll position or calling `scrollTo()`.
  - Styled with Tailwind utilities: root `relative overflow-hidden`, viewport `w-full h-full rounded-[inherit]`, scrollbar `flex select-none touch-none p-0.5 transition-colors duration-140 ease-out hover:bg-hover data-[orientation=vertical]:w-2.5 data-[orientation=horizontal]:flex-col data-[orientation=horizontal]:h-2.5`, thumb `relative flex-1 rounded-full bg-border hover:bg-muted-foreground transition-colors`.
- `UiToast`:
  - Wraps Reka `ToastProvider`, `ToastRoot`, `ToastTitle`, `ToastDescription`, `ToastAction`, `ToastClose`, `ToastViewport`.
  - Declares toast surface variants with `tailwind-variants` (`tv()`): `variant: { default: 'border-border', success: 'border-success-border', warning: 'border-warning-border', danger: 'border-danger-border' }`.
  - `UiToastProvider` mounts the viewport in the bottom-right corner (`fixed bottom-0 right-0 z-50 flex flex-col p-4 gap-2 w-full max-w-[420px] pointer-events-none`).
  - `useToast()` composable provides reactive state and dispatch methods (`toast({ title, description, variant, action })`).
  - Surface styles: `pointer-events-auto bg-popover text-foreground border shadow-lg rounded-control p-4 flex items-center justify-between gap-4 transition-all duration-140 ease-out`.
- `UiCombobox`:
  - Wraps Reka `ComboboxRoot`, `ComboboxInput`, `ComboboxTrigger`, `ComboboxCancel`, `ComboboxPortal`, `ComboboxContent`, `ComboboxViewport`, `ComboboxItem`, `ComboboxItemIndicator`, `ComboboxGroup`, `ComboboxLabel`, `ComboboxSeparator`, `ComboboxEmpty`.
  - Props: `modelValue?: string | string[]`, `options?: Array<{ value: string; label: string; disabled?: boolean; group?: string; nested?: boolean; iconUrl?: string }>`, `ignoreFilter?: boolean`, `placeholder?: string`, `open?: boolean`. Supports `v-model` and `v-model:open`. Emits `highlight` and `select`.
  - Exposes scoped slots: `#trigger`, `#item="{ option, selected, active }"`, `#group-header="{ group }"`.
  - When `ignoreFilter: true` is passed, Reka internal filtering is bypassed, allowing dynamic external search results (such as `searchAll` and recent searches).
  - Floating content styles: `bg-popover text-foreground border border-border shadow-lg rounded-control p-1 z-50 max-h-60 overflow-y-auto`.
  - Options styles: `relative flex cursor-pointer select-none items-center rounded-sm px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-hover text-foreground`.
- Co-located CSF 3 stories for each component covering variants, orientations, and themes.
Tests:
- `frontend/web/src/ui/scroll-area/UiScrollArea.test.ts` asserts:
  - Renders content inside scroll viewport.
  - Exposes `element` reference pointing to the scrollable HTML element.
  - Vertical scrollbar renders for `axis="y"` or `axis="both"`.
- `frontend/web/src/ui/toast/UiToast.test.ts` asserts:
  - Dispatched toast renders title, description, and variant border styling.
  - Action button click invokes callback.
  - Toast auto-dismisses after duration.
- `frontend/web/src/ui/combobox/UiCombobox.test.ts` asserts:
  - Typing in input filters options when `ignoreFilter` is false.
  - Arrow keys highlight items with `data-highlighted` and update `aria-activedescendant`.
  - Enter selects highlighted item and emits update.
  - `ignoreFilter="true"` renders provided options verbatim.
Verify:
`.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui/scroll-area/UiScrollArea.vue frontend/web/src/ui/scroll-area/UiScrollArea.test.ts frontend/web/src/ui/toast/UiToast.vue frontend/web/src/ui/toast/UiToast.test.ts frontend/web/src/ui/combobox/UiCombobox.vue frontend/web/src/ui/combobox/UiCombobox.test.ts`
then `pnpm typecheck && pnpm test && pnpm lint && pnpm build-storybook` in `frontend/web/`.

### U4. UI barrel exports and Storybook accessibility test suite
Files:
- `frontend/web/src/ui/index.ts`
- `frontend/web/src/ui/a11y.test.ts`
After: U1, U2, U3
Change:
- `src/ui/index.ts` appends exports for all Phase 3 components and prop types:
  - `UiDialog` from `./dialog/UiDialog.vue`
  - `UiAlertDialog` from `./alert-dialog/UiAlertDialog.vue`
  - `UiPopover` from `./popover/UiPopover.vue`
  - `UiDropdownMenu`, `UiDropdownMenuItem`, `UiDropdownMenuSeparator` from `./dropdown-menu/...`
  - `UiTabs` from `./tabs/UiTabs.vue`
  - `UiToast`, `UiToastProvider`, `useToast` from `./toast/UiToast.vue`, `./toast/UiToastProvider.vue`, `./toast/useToast.ts`
  - `UiCombobox` from `./combobox/UiCombobox.vue`
  - `UiScrollArea` from `./scroll-area/UiScrollArea.vue`
  - `UiBreadcrumb`, `UiBreadcrumbList`, `UiBreadcrumbItem`, `UiBreadcrumbLink`, `UiBreadcrumbPage`, `UiBreadcrumbSeparator` from `./breadcrumb/...`
- `src/ui/a11y.test.ts`:
  - Updates the test runner to execute `axe.run(document.body, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] }, rules: { 'color-contrast': { enabled: false }, region: { enabled: false } } })`.
  - Audits both trigger markup and teleported overlay portal nodes in `document.body`.
  - Disabling the `region` rule avoids false positive failures on isolated story snippets that lack outer landmark containers.
Tests:
- `pnpm test src/ui/a11y.test.ts` passes with zero violations across all Phase 2 and Phase 3 component stories.
Verify:
`.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui/index.ts frontend/web/src/ui/a11y.test.ts`
then `pnpm typecheck && pnpm test && pnpm lint && pnpm build-storybook` in `frontend/web/`.

### U5. Switcher and navigation migration: ScopeSwitcher, TenantSwitcher, AccountMenu, FleetView Breadcrumbs
Files:
- `frontend/web/src/components/ScopeSwitcher.vue`
- `frontend/web/src/components/TenantSwitcher.vue`
- `frontend/web/src/components/TenantSwitcher.test.ts`
- `frontend/web/src/components/AccountMenu.vue`
- `frontend/web/src/FleetView.vue`
After: U4
Change:
- `ScopeSwitcher.vue`:
  - Rebuilt on `UiCombobox` and `UiScrollArea` from `../ui`.
  - Preserves props (`label: string`, `selected: string`, `options: ScopeOption[]`, `placeholder?: string`) and emit (`change: [value: string]`).
  - Renders trigger button with `aria-label="<label>: <selected-name>"`, `aria-haspopup="listbox"`, current selection name, and chevron icon.
  - Floating content renders search input and scrollable list with avatar icons, checkmark indicators, and indentation for nested options.
  - Deletes manual `getBoundingClientRect` positioning, `showPopover()`, `active` index arithmetic, and hand-rolled `keydown` handlers.
- `TenantSwitcher.vue`:
  - Uses rebuilt `ScopeSwitcher.vue` without API changes.
  - Preserves single-tenant display text and multi-tenant switcher options.
- `TenantSwitcher.test.ts`:
  - Asserts SSR tests pass: single tenant renders without dropdown, multiple tenants offer switching with `aria-haspopup="listbox"` and `aria-label="Tenant scope"`, and unavailable selection labels as `Tenant scope: Unavailable selection`.
- `AccountMenu.vue`:
  - Rebuilt on `UiDropdownMenu` from `../ui`.
  - Trigger renders operator avatar button (`aria-label="Operator account"`).
  - Menu displays disabled "Log out" action with `AppIcon name="logout"` and title "Logout is unavailable until sign-in is connected".
  - Deletes manual `getBoundingClientRect` positioning, `style.top`/`style.right` manipulation, and native `popover="auto"` attributes.
- `FleetView.vue`:
  - Breadcrumbs in topbar (lines 856–901) and side pane header (lines 995–1020) migrate to `UiBreadcrumb`, `UiBreadcrumbList`, `UiBreadcrumbItem`, and `UiBreadcrumbSeparator`.
  - Replaces hand-coded `<span class="breadcrumb-separator">/</span>` with `UiBreadcrumbSeparator`.
  - Preserves switcher placement and active device page display with `UiBreadcrumbPage`.
Tests:
- `pnpm test src/components/TenantSwitcher.test.ts` passes with zero regressions.
- Browser test verifies opening tenant switcher and site switcher via keyboard (Down arrow, Enter) switches active scope.
- Browser test verifies clicking AccountMenu opens dropdown and Escape dismisses it.
Verify:
`.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/components/ScopeSwitcher.vue frontend/web/src/components/TenantSwitcher.vue frontend/web/src/components/TenantSwitcher.test.ts frontend/web/src/components/AccountMenu.vue frontend/web/src/FleetView.vue`
then `pnpm typecheck && pnpm test && pnpm lint && pnpm build` in `frontend/web/`.

### U6. Dialogs, search migration, and legacy ScrollArea removal
Files:
- `frontend/web/src/components/HelpButton.vue`
- `frontend/web/src/components/ReportBugButton.vue`
- `frontend/web/src/components/GlobalSearch.vue`
- `frontend/web/src/components/ScrollArea.vue`
- `frontend/web/src/ClientsView.vue`
- `frontend/web/src/DashboardView.vue`
- `frontend/web/src/WorkspacePage.vue`
- `frontend/web/src/components/topology/TopologyInspector.vue`
- `frontend/web/src/navigation/PageDock.vue`
- `frontend/web/src/navigation/PageHost.vue`
- `frontend/web/README.md`
After: U5
Change:
- `HelpButton.vue`:
  - Rebuilt on `UiDialog` from `../ui`.
  - Trigger is wrapped in `UiTooltip`.
  - Dialog renders title "Workspace help", accessible close button, and guidance copy for scopes, finding devices, and moving devices.
  - Deletes native `<dialog ref="dialog">`, `showModal()`, and manual `useMotionFeedback` animation calls.
- `ReportBugButton.vue`:
  - Rebuilt on `UiDialog` from `../ui`.
  - Replaces native form inputs with `UiField`, `UiInput`, `UiTextarea`, and `UiButton` from `../ui`.
  - Preserves copy-to-clipboard functionality, feedback status, and fallback textarea.
  - Deletes native `<dialog ref="dialog">`, `showModal()`, and manual `useMotionFeedback` animation calls.
- `GlobalSearch.vue`:
  - Rebuilt on `UiDialog` (`headless: true`), `UiCombobox` (`ignoreFilter: true`), `UiScrollArea`, and `UiTooltip` from `../ui`.
  - Preserves global keyboard triggers (`⌘K` / `Ctrl K` and `/`), input query binding, recent searches (`loadRecent`, `rememberRecent`, `clearRecent`), and `searchAll()` scoped querying.
  - Preserves selection shortcuts: Enter opens primary result, Shift+Enter or Cmd/Ctrl+Enter opens result beside current page (`canSplit`), Alt+Enter sends result to dock.
  - Deletes manual `active` index arithmetic, native `<dialog ref="dialog">`, `showModal()`, and hand-written `scrollIntoView()` calls.
- Delete `src/components/ScrollArea.vue`.
- Migrate all remaining `ScrollArea.vue` importers to `UiScrollArea` from `src/ui`:
  - `src/ClientsView.vue`
  - `src/DashboardView.vue`
  - `src/WorkspacePage.vue`
  - `src/components/topology/TopologyInspector.vue`
  - `src/navigation/PageDock.vue`
  - `src/navigation/PageHost.vue` (preserves `scroller.value?.element` reference and page transition scroll reset)
- Update `frontend/web/README.md` to document the new `Ui*` overlay and navigation components and record that switchers, menus, dialogs, and scrollers now run on Reka primitives.
Tests:
- `pnpm test` passes across all test suites with zero regressions.
- `pnpm test src/components/recentSearches.test.ts` passes.
- Grep assertions confirm no legacy references remain:
  - `git grep "components/ScrollArea" frontend/web/src` prints nothing.
  - `git grep -E "(showModal|showPopover)" frontend/web/src` prints nothing.
Verify:
`.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/components/HelpButton.vue frontend/web/src/components/ReportBugButton.vue frontend/web/src/components/GlobalSearch.vue frontend/web/README.md frontend/web/src/ClientsView.vue frontend/web/src/DashboardView.vue frontend/web/src/WorkspacePage.vue frontend/web/src/components/topology/TopologyInspector.vue frontend/web/src/navigation/PageDock.vue frontend/web/src/navigation/PageHost.vue`
then `pnpm typecheck && pnpm test && pnpm lint && pnpm build && pnpm build-storybook` in `frontend/web/`.

Waves: U1 U2 U3 | U4 | U5 | U6

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

Verify legacy popover, dialog, and scroller cleanup:

```sh
git grep -rnE "components/ScrollArea" src
git grep -rnE "(showModal|showPopover)" src
git grep -rnE "popover=\"auto\"" src
```

All grep commands must print nothing.

Manual and browser checks:
1. Run `pnpm storybook` and navigate to each component story under `Ui/`: Dialog, AlertDialog, Popover, DropdownMenu, Tabs, Toast, Combobox, ScrollArea, Breadcrumb. Verify opening transitions run in 100–160 ms with `--ease-out`, closing is immediate, and contrast holds in both light and dark themes.
2. Run `pnpm dev`. Press `⌘K` (or `Ctrl K`) to open GlobalSearch. Verify recent searches display when the query is empty. Type a device name, press Down arrow to highlight, and press Enter to navigate. Open search again, type a name, and press Alt+Enter to verify the item is sent to the dock.
3. Open Help dialog via topbar help icon. Verify focus is trapped inside the dialog. Press Escape to verify the dialog closes immediately and returns focus to the help button.
4. Click Operator account menu avatar in topbar. Verify dropdown opens with "Log out" disabled and closes on outside click or Escape.
5. In FleetView, navigate between tenant options and site options using keyboard arrows and Enter. Verify the breadcrumb displays the active hierarchy and `TenantSwitcher.test.ts` passes.

## Definition of done

- [ ] Verifier green for every changed path, and all verification commands pass.
- [ ] All 9 `Ui*` components created under `src/ui/` (`dialog/`, `alert-dialog/`, `popover/`, `dropdown-menu/`, `tabs/`, `toast/`, `combobox/`, `scroll-area/`, `breadcrumb/`) with CSF 3 stories and passing axe accessibility checks.
- [ ] `AccountMenu`, `ScopeSwitcher`, `TenantSwitcher`, `HelpButton`, `ReportBugButton`, and `GlobalSearch` rebuilt on `Ui*` primitives without manual coordinate math or native popover/modal calls.
- [ ] `src/components/ScrollArea.vue` deleted and all callers migrated to `UiScrollArea` from `src/ui`.
- [ ] `frontend/web/README.md` updated with Phase 3 component documentation.
- [ ] This plan's `status` set to `implemented` with an outcome note under the title upon landing.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- Toast placement and stacking direction:
  - Options: bottom-right stacking upwards (`bottom-0 right-0`) (Recommended) vs top-right stacking downwards.
  - Recommendation: bottom-right stacking upwards keeps notifications away from topbar navigation and dock controls while remaining accessible on desktop and mobile.
- `GlobalSearch` command palette architecture:
  - Options: compose `UiDialog` and `UiCombobox` directly in `GlobalSearch.vue` (Recommended) vs introduce a dedicated `UiCommand` abstraction.
  - Recommendation: compose `UiDialog` (`headless: true`) and `UiCombobox` directly. Reka's `Combobox` already manages input focus, keyboard arrows, and option roles; creating a separate `UiCommand` wrapper is redundant when only a single command palette consumer exists in this phase.
- Breadcrumb item overflow behavior on small viewports:
  - Options: flex wrapping with responsive font sizing (Recommended) vs collapsed ellipsis dropdown.
  - Recommendation: flex wrapping (`flex flex-wrap items-center gap-1.5`) matches existing `FleetView.vue` responsive behavior without requiring complex resize-observer measurement logic.
