---
title: Web Component Contract Migration, Phase 5 - The ai Prop and Shared Highlight - Plan
type: refactor
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
execution: code
amends: docs/architecture/2026-09-28-web-component-contract-direction.md
parent: docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-plan.md
---

# Web Component Contract Migration, Phase 5 - The ai Prop and Shared Highlight - Plan

> Implemented. 6 units, 2026-10-05T12:41Z to 2026-10-05T13:47Z.

## Goal

Semantic components register their rendered element through an optional `ai`
prop, share selection styling, and mark agent-changed values from an optional
originating request. A shared lifecycle composable replaces the directive,
and the existing table scroller fixes the Default story's narrow layout.
Stop condition: a component cannot expose its meaningful element without
changing its DOM semantics or transferring ownership from its caller.

## Decisions

- The accepted component contract and the parent's ordering apply. Phase 4's
  final commit `ea17148b` is an ancestor of `HEAD`, and the parent's U5
  `Landed:` line on `main` is empty. The AI actions prerequisite is implemented
  in `docs/plans/2026-09-28-1804-feat-ai-actions-and-assistant-plan.md`.
- The accepted refinement in
  `docs/architecture/2026-10-05-1251-web-ai-target-lifecycle-direction.md`
  settles prop types, element
  ownership, native anchors, and origin acknowledgement across the kit.
- `ai` takes the resolved `AiTarget` returned by `aiTarget()`, rather than
  `AiTargetInput`. Why: existing view target builders already supply that
  shape, including pane and responsive-segment identity (`src/ai/target.ts`
  under `frontend/web/`). Components register the supplied identity and never
  invent one. The lifecycle record clarifies the accepted record's wording.
- `aiOrigin` takes an originating request without its `signal`, identified by
  `requestId`. Why: `UiAiLabel` already displays that request's targets and
  context. A user interaction acknowledges it, and service provenance remains
  outside this phase (decided by the user, 2026-10-05).
- Registration uses the injected registry from `src/ui/ai/context.ts`, with
  the existing document singleton as its fallback. Why: tests already provide
  an isolated registry, while Storybook's document scope owns window dispatch
  across canvases (`.storybook/aiDecorator.ts`). No new registry per component.
- The registry owns `data-ai-selected` for every registered element, including
  manual registrations. Why: selection currently lives in the registry but
  its attribute is written by `src/ai/directive.ts`. One owner also covers
  Storybook's manually registered wrapper.
- The directive is deleted after all callers migrate. Why: its remaining
  lifetime machinery is shared by components, and no external consumer needs
  the view directive. Native markup uses `UiAiTarget` with the original tag
  or `asChild`, rather than a new layout wrapper.
- Default opts into the existing `tableScroller` decorator. Why: its current
  parameters omit the scroller that Dense, Sortable, RowSelection, and
  LongText already use (`src/ui/table/UiTable.stories.ts`). The old Ask-button
  blocker is superseded by the current context-menu audit in
  `src/ui/a11y.test.ts`. The probe below passes with the scroller enabled.
- No dependency changes. Library behavior is checked in the locked installed
  source: Vue 3.5.43 runtime-core's `inject` and `watchPostEffect`, and Reka UI
  2.10.5's `Primitive/Primitive.js`, `Primitive/Slot.js`,
  `shared/useForwardExpose.js`, and `ScrollArea/ScrollAreaViewport.js`.
  Versions come from `frontend/web/pnpm-lock.yaml`.
- Ruled: `UiAiProps`, `UiAiEmits`, and `AiOriginRequest` live in
  `src/ui/ai/context.ts`, and nested origins acknowledge independently. Why:
  the plan named no file, and an inner value's interaction is not the outer
  value's. Cost if wrong: an import path and one composable branch.
- Ruled: the story audit's highlighted-element check accepts an SVG target
  together with the display components, ahead of the story unit. Why: the
  sparkline registers its SVG root, so the audit is red from that change
  until the check widens. Cost if wrong: one assertion in
  `src/ui/a11y.test.ts`.
- Ruled: popups register through a render-less `PopupAnchor` placed first in
  their content (`src/ui/popover/popupAnchor.ts`). Why: in Reka 2.10.5,
  context-menu content and toast roots stay mounted and swap content through
  their own presence, and dropdown and popover content expose the popper
  wrapper. Cost if wrong: one component and eight popup templates.
- Ruled: text controls (input, textarea, combobox input) keep the native
  context menu, so their AgentChanged stories show origin without a target.
  Why: `UiAiContextLayer` leaves editable targets to the browser menu, and a
  registered target there fails the audit's context-menu check. Cost if
  wrong: three stories and the audit's text-control case.

Paths starting with `src/` or `.storybook/` below are under `frontend/web/`.

## Requirements

R1. A supplied target follows the current element and prop. Example: a row
registers d1, changes to d2, removes its `ai` prop, and unmounts. The registry
lists exactly the current target at each step and nothing afterward.
Replacing the rendered element removes the old registration and attributes.

R2. Selection has one owner. Example: highlighting a manually registered
story wrapper or a component row adds `data-ai-selected` to that exact
node. Clearing selection, hiding its segment followed by `refresh()`, or
unregistering it removes the attribute. Updating an equal target object
preserves selection and does not notify listeners.

R3. HTML and SVG targets work through the same registry. Example: a
`TrafficSparkline` SVG is listed, highlighted, and resolves its chart target
when a context menu starts on a child path. No HTML wrapper changes its size.

R4. Every semantic kit component in the inventory takes `ai` and `aiOrigin`
props and emits `aiOriginAcknowledged(requestId)`. Example: a `UiSelect`
registers its trigger button, while a `UiDialog` registers its mounted
content. An absent `ai` prop registers nothing. Structural components remain
outside the registration contract.

R5. Origin is independent of selection and registration. Example: an input
with originating request r1 has `data-ai-origin="agent"` even without `ai`.
Highlighting, hovering, or a programmatic value update preserves it. A
pointerdown, keydown, input, or change on that value acknowledges r1 once.
Re-rendering the same request leaves it acknowledged. Request r2 marks it
again. Clearing `aiOrigin` removes the marker and its listeners.

R6. Origin explanation composes with existing markup. Example: a `UiField`
contains an input with `aiOrigin` and a `UiAiLabel` using the same request.
The caller handles `aiOriginAcknowledged` by clearing its origin state, so
both the marker and explanation disappear and remain absent after remount.
Selection's outline remains if the input is still selected. Portalled
selection acknowledges only that control's origin, never an unrelated field.

R7. All production targets and story targets migrate without changing IDs,
contexts, pane slots, or responsive segments. Example: filtering an inventory
row removes its target, and changing between mobile and desktop exposes only
the corresponding segment. No directive registration remains in test mounts.

R8. At a 320 px viewport, the Default table story has no horizontal page
scroll, its inner viewport can scroll horizontally, and its four device rows
remain listable and highlightable. Their context menu opens in both locales.
The existing target count and axe checks remain enabled.

## Out of scope

The generative catalog and renderer belong to phase 6. Service provenance,
agent UI mutation operations, and new request handlers are outside this phase.
The `ai` and `aiOrigin` props are typed application data supplied by trusted
Vue callers. They add no new wire parser for handler responses, which retain
`src/ai/validate.ts` as their existing untrusted-input boundary.

Policy-surface changes, including the accessibility conformance gate, are
outside this plan. The migration guidance in the web-component skill remains
until phase 6 supplies the catalog its interim rule also checks.

## Component inventory

Each name below means its existing `Ui<Name>.vue` and colocated stories under
`src/ui/`. Existing files are not split between parallel units.

| Unit | Components | Registered element |
| --- | --- | --- |
| U1 | New UiAiTarget | Original native tag, or the single forwarded child |
| U2 | UiBadge, UiStatusBadge, UiCard, UiMetricCard, UiEmptyState, UiKbd, UiMeter, UiSegmentedMeter, UiProgress | Existing visible root |
| U2 | UiTable, UiTableRow, UiTableCell, UiTableHead, UiTableEmpty | Table, tr, td, th, or the empty row |
| U2 | UiBreadcrumbPage | Existing visible text element |
| U2 | TrafficChart, TrafficSparkline under src/components/ | Existing figure or SVG root |
| U3 | UiButton, UiBreadcrumbLink, UiBreadcrumbEllipsis | Actual button or forwarded link |
| U3 | UiField, UiInput, UiTextarea, UiCheckbox, UiSwitch, UiRadioGroup, UiSelect, UiCombobox | Field root, native control, Reka control root, group, select trigger, or combobox input |
| U3 | UiTabs, UiPagination, UiCommand, UiCommandInput, UiCommandItem, UiCommandEmpty, UiCommandShortcut, UiCommandGroup | Existing visible root or control |
| U3 | UiDropdownMenuItem, UiContextMenuItem | Mounted menu item |
| U3 | UiAlertDialog, UiDialog, UiCommandDialog, UiDropdownMenu, UiContextMenu, UiPopover, UiTooltip, UiToast | Mounted popup or toast content, absent when unmounted |
| U3 | UiAiSummary, UiAiLabel, UiAiEntityChip | Summary section, label trigger, or entity button or fallback text |
| U3 | UiAiAssistant, UiAiResult, UiAiResultActions | Existing visible panel or result root |
| Exempt | UiAppRoot, UiToastProvider, UiAiContextLayer, UiScrollArea, UiSkeleton, UiSpinner, UiSeparator, UiDropdownMenuSeparator, UiContextMenuSeparator, UiCommandSeparator, UiCommandList, UiBreadcrumb, UiBreadcrumbList, UiBreadcrumbItem, UiBreadcrumbSeparator, UiTableHeader, UiTableBody | Structural, provider, or decorative component, no AI props |

## Units

### U1. Shared lifecycle and native target surface
Files: frontend/web/src/ai/{types.ts,index.ts,registry.ts,registry.test.ts,directive.ts}, frontend/web/src/ui/ai/{context.ts,useAiTarget.ts,useAiTarget.test.ts,useAiOrigin.ts,useAiOrigin.test.ts,UiAiTarget.vue,UiAiTarget.stories.ts,UiAiTarget.test.ts,UiAiContextLayer.vue,UiAiContextLayer.test.ts}, frontend/web/src/ui/index.ts, frontend/web/src/ui/popover/UiPopover.vue, frontend/web/src/theme/ai.css
After: none
Change: exported `AiTargetElement` covers HTMLElement and SVGElement.
Registry signatures, bound-run storage, and popover references use it. A
registration stores a copy of the target metadata, so a later in-place edit
can be compared with the previous value. Registry selection
sets and clears its attribute on change, refresh, removal, and failed
highlight. Context-menu lookup walks Element ancestors, including SVG paths.
`useAiTarget(elementRef, () => props.ai)` resolves a DOM ref or a forwarded
component `$el`, checks its type, and watches element and target after render.
Its target watch includes metadata changes. It unregisters only when the
element or identity changes or the target goes,
so an equal target update preserves a bound request. It reconciles forwarded `$el` on the owner's update as well as ref changes,
and disposes on unmount.
`useAiOrigin` tracks the current request ID and acknowledgement, writes the
origin attribute, and owns element-local capture listeners. It returns the
active request and an acknowledgement handler for portalled interactions.
The interim directive delegates selection attributes to the registry, and
context-layer test fixtures use UiAiTarget with an injected registry.
Shared `UiAiProps` and `UiAiEmits` types name the optional props and event.
`UiAiTarget` wraps Reka Primitive with `as` and `asChild` and forwards attrs
and events to the original anchor. One CSS rule covers selection and origin.
Tests: registry tests cover manual highlighting, switching, visibility,
removal, duplicate IDs, SVG targets, and stale bound requests. Lifecycle tests
cover missing props, mutable target metadata, ID and DOM replacement, cleanup,
and two injected registries. Origin tests cover R5, a new request, nested
independent targets, and preservation of selection on acknowledgement.
UiAiTarget tests prove native li and section markup and an asChild AppLink
without a wrapper. SVG context-menu tests start on a child path.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ai frontend/web/src/ui/ai frontend/web/src/ui/index.ts frontend/web/src/ui/popover/UiPopover.vue frontend/web/src/theme/ai.css`

### U2. Display primitives and charts
Files: frontend/web/src/ui/{badge,card,empty-state,kbd,meter,progress,table,breadcrumb/UiBreadcrumbPage.vue}, frontend/web/src/components/{TrafficChart.vue,TrafficChart.stories.ts,TrafficSparkline.vue,TrafficSparkline.stories.ts}, frontend/web/src/ui/ai/displayTargets.test.ts
After: U1
Change: each U2 inventory component uses the shared props and hooks on its
listed element. Chart stories and Card's Default story pass `ai` instead of
the directive. Default table rows pass `ai`, and Default enables
`tableScroller`. Field-value examples compose UiAiLabel outside native table
structure or void elements and clear the origin through acknowledgement.
Tests: displayTargets mounts every U2 component with and without its props,
asserts the exact registered tag, changes and removes the target, and checks
origin acknowledgement. A SVG chart keeps its root and label. Table tests
retain four row targets inside UiScrollArea and verify filtering cleanup.
The focused Default story audit below passes in both locales.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui/badge frontend/web/src/ui/card frontend/web/src/ui/empty-state frontend/web/src/ui/kbd frontend/web/src/ui/meter frontend/web/src/ui/progress frontend/web/src/ui/table frontend/web/src/ui/breadcrumb/UiBreadcrumbPage.vue frontend/web/src/components/TrafficChart.vue frontend/web/src/components/TrafficChart.stories.ts frontend/web/src/components/TrafficSparkline.vue frontend/web/src/components/TrafficSparkline.stories.ts frontend/web/src/ui/ai/displayTargets.test.ts`

### U3. Controls, overlays, and AI surfaces
Files: frontend/web/src/ui/{button,form,combobox,tabs,pagination,command,alert-dialog,dialog,dropdown-menu,context-menu,popover,tooltip,toast,breadcrumb/UiBreadcrumbLink.vue,breadcrumb/UiBreadcrumbEllipsis.vue}, frontend/web/src/ui/ai/UiAi{Summary,Label,EntityChip,Assistant,Result,ResultActions}.vue, frontend/web/src/ui/ai/UiAi{Summary,Label,EntityChip,Assistant,Result,ResultActions}.{stories,test}.ts, frontend/web/src/ui/ai/UiAiContextLayer.stories.ts, frontend/web/src/ui/ai/controlTargets.test.ts, frontend/web/src/i18n/locales/{en,de}.json
After: U1
Change: each U3 inventory component uses the shared hooks on its listed
anchor. Component refs use the forwarded DOM element rather than logical
Reka roots. Popups register only while their content is mounted, including
exit presence. Portalled control content forwards user DOM interactions to
its control's origin acknowledgement handler. Programmatic prop updates
acknowledge nothing. AI stories pass props to their real surfaces or use
UiAiTarget for native example targets. UiAiLabel retains its request-backed
explanation. Origin examples clear caller state through the typed event.
Tests: controlTargets mounts every U3 component's registered state, asserts
its anchor and cleanup, and covers native focus and click behavior. Select
and combobox tests select an option through the portal and clear only their
own origin. Label tests inspect request targets and context in both locales.
Stories show Selected and AgentChanged where those add a distinct state.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui frontend/web/src/i18n/locales`

### U4. View and topology target migration
Files: frontend/web/src/{WorkspacePage.vue,DashboardView.vue,DeviceView.vue,ClientsView.vue,FleetView.test.ts,FleetView.locale.test.ts,FleetView.motion.test.ts,DeviceView.test.ts}, frontend/web/src/components/{GlobalSearch.test.ts,topology/TopologyGraph.vue,topology/TopologyNode.vue,topology/TopologySiteNode.vue,topology/TopologyLink.vue,topology/TopologySemantics.test.ts}
After: U2, U3
Change: existing semantic components receive `ai`. Native section, li, div,
button, and AppLink anchors use UiAiTarget with the same tag or asChild.
Topology's existing view target builders keep their domain data. Mounts
provide the registry key and install no directive. View-side handlers consume
origin acknowledgement when they supply an origin, with no service adapter.
Tests: FleetView and DeviceView tests retain target IDs, contexts, slot swap,
filtering, responsive visibility, and stale-request behavior. Locale and
motion mounts still run. Topology tests target the actual node and link label,
including a context event from nested markup. GlobalSearch keeps its mount.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/WorkspacePage.vue frontend/web/src/DashboardView.vue frontend/web/src/DeviceView.vue frontend/web/src/ClientsView.vue frontend/web/src/FleetView.test.ts frontend/web/src/FleetView.locale.test.ts frontend/web/src/FleetView.motion.test.ts frontend/web/src/DeviceView.test.ts frontend/web/src/components`

### U5. Story lifecycle and contract coverage
Files: frontend/web/.storybook/{aiDecorator.ts,aiDecorator.test.ts}, frontend/web/src/ui/{a11y.test.ts,ai/targetContract.test.ts}
After: U2, U3
Change: the decorator retains its module-level document scope and per-canvas
handler routing. Its manual wrapper registration now receives registry-owned
highlighting. It installs no directive. Nested fixture targets use UiAiTarget.
The audit accepts the actual HTML or SVG target element and keeps the target
counts, component-anchor checks, and context-menu assertions. targetContract
has an explicit entry for every current Ui Vue file, including exempt files,
and fails on a new unclassified component or a semantic component without the
props and its mount coverage.
Tests: existing multi-canvas tests retain window and handler lifetime, duplicate
ID refusal, and nested-target routing. The full story audit passes in both
locales. Browser checks cover Default table scrolling, row highlight and menu,
SVG highlight, and portalled-origin acknowledgement at narrow and wide widths
in both themes. No audit rule, target expectation, or story is removed.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/.storybook frontend/web/src/ui/a11y.test.ts frontend/web/src/ui/ai/targetContract.test.ts`

### U6. Remove the directive and document the contract
Files: frontend/web/src/{ai/directive.ts,ai/directive.test.ts,ai/index.ts,main.ts}, frontend/web/README.md, docs/architecture/2026-09-28-web-component-contract-direction.md
After: U4, U5
Change: the directive files, exports, and main registration are gone. Its
lifetime assertions now live in the composable tests. The README documents
anchor selection, aiTarget output, origin acknowledgement, and a working
field-plus-label example. The accepted refinement is appended as an amendment
to the existing component contract with the code change.
Tests: a repository search finds no directive symbol or template use under
frontend/web. The full web tests, typecheck, build, and Storybook build pass.
The verifier's deleted-test report accounts for directive.test.ts by naming
the preserved lifecycle assertions in useAiTarget.test.ts.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web docs/architecture/2026-09-28-web-component-contract-direction.md`

Waves: U1 | U2 U3 | U4 U5 | U6

## Verification

Run the verifier on every unit's changed paths and on their union at finish.
From `frontend/web/`, the existing focused probe is:

```bash
./node_modules/.bin/vitest run src/ui/a11y.test.ts -t 'UiTable.stories.ts.*Default'
```

Both locale cases pass with Default's `tableScroller: true`. The existing
TrafficSparkline AllColorVariants audit also passes. They establish the
current baseline, not the future composable's correctness.

Browser verification uses the table story's actual highlighted row, not
`document.querySelector('tbody tr')`: Storybook also mounts hidden measurement
tables. At 320 px, compare document scrollWidth with clientWidth, then the
`[data-reka-scroll-area-viewport]` scrollWidth with clientWidth. Highlight one
of the four device IDs, find `[data-ai-selected]`, and open its context menu.
This probe passes with the existing scroller decorator. Follow the browser
loop in `.agents/skills/web-component/references/review.md` after migration.

## Definition of done

- [x] Verifier green for every changed path.
- [x] Every inventory component meets its prop and anchor contract.
- [x] Origin and selection have independent cleanup and stories.
- [x] No directive registration or use remains in frontend/web.
- [x] README and accepted direction amendment match the final code.
- [x] Browser checks include 320 px Default table and SVG targets.
- [x] This plan's status is set with an outcome note under its title.
- [x] No plan labels appear in code or comments.

## Open questions

No design question remains. Origin acknowledgement for compound portalled
controls is the least certain implementation detail. U3 proves it through
their DOM interactions and isolated sibling values.
