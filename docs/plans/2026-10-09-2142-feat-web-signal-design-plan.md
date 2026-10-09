---
title: Web Signal Design Language - Plan
type: feat
date: 2026-10-09
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Web Signal Design Language - Plan

## Goal

Apply Precision 01 Signal across the web app through shared tokens and
components, then adapt each view. Keep the current brand colors and application
shell. The visual target is the checked-in
[Signal reference](../../frontend/web/design/signal/README.md).

Stop if a requirement needs new backend data or a change to routing, pane
lifecycle or AI target semantics. Separate that behavior change from this visual
migration before proceeding.

## Decisions

- Use Precision 01 Signal, the current coral/cyan colors and the existing
  sidebar/top-bar structure. This preserves the selected visual language and
  familiar navigation (decided by the user, 2026-10-09).
- The [Signal direction](../architecture/2026-10-09-web-signal-design-direction.md)
  fixes the panel, typography and layout rules. It supplements the accepted
  [design system](../architecture/2026-09-26-web-design-system-direction.md)
  and [component contract](../architecture/2026-09-28-web-component-contract-direction.md).
  Keep the current component stack and update shared defaults rather than
  introducing a second theme or a parallel set of Signal components.
- Preserve `src/theme/scales.css`, `semantic.css`, `brand-glow.css` and
  `design/palette-source.json` under `frontend/web/`. Change non-color tokens
  and component styling. The semantic palette already owns brand and status
  roles, including tested foreground pairs.
- Keep `AppFrame.vue`, its teleport regions and `FleetView.vue` navigation
  behavior. The [frame record](../architecture/2026-10-07-web-console-frame-direction.md)
  describes the implemented structure. This task does not replace that separate record.
- Readable application text takes precedence over tiny mockup annotations.
  Use 11px or larger labels, 28px page titles, and named 32px/64px display-metric
  tokens. Use four-pixel control corners and square content panels. Preserve
  shell and functional shapes. Update `frontend/web/design/language.md` with
  the code because its current 12px panel rule would otherwise contradict it.
- Use pane width for dashboard reflow. At content widths of at least 1000px,
  use three columns with approximately 210px and 274px side columns. Between
  640px and 999px use two columns, with summary and traffic spanning both.
  Below 640px stack panels in document order. Heights follow content. These
  thresholds are implementation choices to verify against German and split panes.
- Keep domain bindings in `src/domain/fleet.ts` and `overview.ts`. The traffic
  selector requests 6 or 24 samples from the existing `trafficHistory` helper.
  Its history remains synthetic. No new telemetry backend or sync timestamp
  is implied by the reference.
- Existing device links use the app's page navigation contract. Preserve the
  assistant's docked and narrow-screen presentations. Mock inspector dialogs,
  mock search and invented issue explanations are not application requirements.

## Requirements

1. Shared content panels have square one-pixel frames, restrained corner ticks
   and no raised shadows. Example: the dashboard health card and a device facts
   card share the same panel treatment in both themes.
2. Mono headings and tabular metrics establish hierarchy while Inter carries
   prose and navigation. Example: a German event description remains readable
   at the existing body size and wraps without colliding with its identifier.
3. Brand and status roles remain distinct. Example: the primary action stays
   coral, traffic stays cyan, and an offline device has a text status as well
   as its danger color. Existing contrast tests continue to pass.
4. The current shell stays recognizable and functional. Example: at 1440px the
   sidebar is 204px, collapses to 64px, and retains the active marker. Search,
   scopes, assistant and account controls keep their positions and keyboard
   behavior. Theme and language remain in the current account control.
5. The dashboard matches the reference composition: full-width summary, health
   and roles left, traffic wide above attention and events, and sites below.
   Example: at a 1100px content width events span the attention and sites rows.
   At a 480px split-pane width all panels stack without page overflow.
6. Metrics derive from scoped data. Example: 25 healthy, 2 degraded and 1 offline
   yield 28 devices and 89.3% healthy in English. Zero devices show a localized
   empty state rather than NaN. Represent at most 100 meter ticks, proportionally
   allocated above that count, with exact accessible counts regardless of size.
7. Traffic range changes data and its accessible equivalent. Example: selecting
   6h produces six samples, recalculates the peak and keeps the last sample equal
   to aggregate current throughput. Zero traffic has a finite scale. High rates
   use appropriate localized units rather than a fixed 2 Gbps ceiling.
8. All views adopt the shared language while retaining their content and actions.
   Example: Dashboard, Devices, Topology, Clients, Sites, device detail and login
   use consistent heading/control styling. Device selection still opens through
   the same route and pane mechanisms.
9. AI, search and overlays retain their contracts. Example: two dashboard panes
   register distinct AI target addresses. Closing search restores its trigger's
   focus. Opening the assistant preserves its current contextual actions.
10. Both themes and locales work at 320px, 390px, 860px, 1024px and 1440px,
    expanded/collapsed navigation, and narrow split panes. Example: German
    scope names and toolbar controls wrap without hiding the account control.
    Dense tables may scroll inside their own region, with reachable actions.
    Reduced motion, keyboard focus and open overlays remain usable.

## Out of scope

Backend integration, dependency changes, palette changes, new navigation
semantics and a replacement assistant. Reference numbers and mock interactions
are illustrative. No policy surface, generated file or accessibility exception
is part of this migration.

## Units

### U1. Shared visual foundations

Files: `docs/architecture/README.md`, `docs/architecture/2026-10-09-web-signal-design-direction.md`, `docs/plans/2026-10-09-2142-feat-web-signal-design-plan.md`, `docs/plans/2026-10-09-2142-feat-web-signal-design-plan.state.json`, `frontend/web/design/language.md`, `frontend/web/design/signal/`, `frontend/web/src/theme/tokens.css`, `frontend/web/src/theme/tailwind.css`, `frontend/web/src/theme/tailwind.test.ts`, `frontend/web/src/ui/card/`, `frontend/web/src/ui/badge/`, `frontend/web/src/ui/button/`, `frontend/web/src/ui/table/`, `frontend/web/src/ui/meter/`, `frontend/web/src/ui/foundations/`.
After: none
Change: changes content-panel and control radius tokens, adds named display sizes,
removes raised card styling and adds decorative corner ticks. Applies mono
heading/metric/table-header roles and compact spacing to these primitives.
Updates the segmented meter with bounded ticks and exact accessible counts.
Zero counts retain existing meter labels and number formatting. Dashboard
percentage and empty-state messaging belong to U3.
Updates design language prose and all affected stories. Existing inputs and
other controls inherit the shared control token.
Tests: existing card, badge, button, table and meter suites retain their behavior
assertions. Add meter cases for 25/2/1, zero and totals above 100. Update typography,
shape and component stories for both themes, long labels and interaction states.
Verify: `uv run tools/scripts/run.py verify change -- docs/architecture/README.md docs/architecture/2026-10-09-web-signal-design-direction.md docs/plans/2026-10-09-2142-feat-web-signal-design-plan.md docs/plans/2026-10-09-2142-feat-web-signal-design-plan.state.json frontend/web/design/language.md frontend/web/design/signal frontend/web/src/theme/tokens.css frontend/web/src/theme/tailwind.css frontend/web/src/theme/tailwind.test.ts frontend/web/src/ui/card frontend/web/src/ui/badge frontend/web/src/ui/button frontend/web/src/ui/table frontend/web/src/ui/meter frontend/web/src/ui/foundations`

### U2. Preserve the shell and migrate fleet surfaces

Files: `frontend/web/src/style.css`, `frontend/web/src/FleetView.vue`, `frontend/web/src/WorkspacePage.vue`, `frontend/web/src/navigation/AppFrame.vue`, `frontend/web/src/navigation/AppFrame.test.ts`, `frontend/web/src/navigation/chrome.test.ts`, `frontend/web/src/components/AccountMenu.vue`, `frontend/web/src/components/AccountMenu.test.ts`, `frontend/web/src/components/GlobalSearch.vue`, `frontend/web/src/components/GlobalSearch.test.ts`, `frontend/web/src/components/ScopeSwitcher.vue`, `frontend/web/src/components/TenantSwitcher.vue`, `frontend/web/src/components/TenantSwitcher.test.ts`.
After: U1
Change: preserves shell geometry, glass, navigation order and scope controls.
Aligns fleet inventory and sites content in WorkspacePage with shared headings,
rows and actions. Updates shell/search/account styling only where needed for
Signal consistency. Keeps existing shortcuts and all route/pane state handling.
Tests: existing frame, chrome, account, search and scope tests. Assert search
open/close focus, scoped results, theme/language selection and persistent frame
regions. Browser-check expanded/collapsed navigation and wrapped topbar at 320px.
Verify: `uv run tools/scripts/run.py verify change -- frontend/web/src/style.css frontend/web/src/FleetView.vue frontend/web/src/WorkspacePage.vue frontend/web/src/navigation/AppFrame.vue frontend/web/src/navigation/AppFrame.test.ts frontend/web/src/navigation/chrome.test.ts frontend/web/src/components/AccountMenu.vue frontend/web/src/components/AccountMenu.test.ts frontend/web/src/components/GlobalSearch.vue frontend/web/src/components/GlobalSearch.test.ts frontend/web/src/components/ScopeSwitcher.vue frontend/web/src/components/TenantSwitcher.vue frontend/web/src/components/TenantSwitcher.test.ts`

### U3. Dashboard composition and scoped telemetry

Files: `frontend/web/src/DashboardView.vue`, `frontend/web/src/DashboardView.test.ts` (new), `frontend/web/src/components/TrafficChart.vue`, `frontend/web/src/components/TrafficChart.test.ts`, `frontend/web/src/components/TrafficChart.stories.ts`, `frontend/web/src/domain/overview.test.ts`, `frontend/web/src/i18n/locales/en.json`, `frontend/web/src/i18n/locales/de.json`.
After: U1
Change: builds the pane-responsive grid and reference panel hierarchy using
existing data helpers. Adds 6h/24h selection, selected-range peak and correctly
scaled traffic labels. Keeps the chart's keyboard and accessible table behavior.
Chooses time-label density for the range and available width, including the
first and last samples. Preserves slot-qualified AI targets and existing device
links. Handles zero-device percentages and empty-state messages. Adds localized
labels only where existing keys do not express the new metrics.
Tests: new mounted dashboard tests for populated, empty and site-scoped data,
range selection, exact counts and preserved links. TrafficChart tests cover
zero/high rates and accessible data matching the plot. Overview tests pin final
sample and sample counts. Browser-check narrow panes and both themes.
Verify: `uv run tools/scripts/run.py verify change -- frontend/web/src/DashboardView.vue frontend/web/src/DashboardView.test.ts frontend/web/src/components/TrafficChart.vue frontend/web/src/components/TrafficChart.test.ts frontend/web/src/components/TrafficChart.stories.ts frontend/web/src/domain/overview.test.ts frontend/web/src/i18n/locales`

### U4. Device, client and topology surfaces

Files: `frontend/web/src/DeviceView.vue`, `frontend/web/src/DeviceView.test.ts`, `frontend/web/src/ClientsView.vue`, `frontend/web/src/components/topology/`, `frontend/web/src/navigation/PageDock.vue`, `frontend/web/src/navigation/PageHost.vue`, `frontend/web/src/navigation/dock.test.ts`, `frontend/web/src/style.css`.
After: U2
Change: aligns detail headers, facts, tables, topology inspectors and dock chrome
with shared styling. Removes conflicting legacy surface styles from style.css.
Preserves topology graph meaning, device actions, client filtering and pane
interactions. Keeps identifiers and accessible controls intact.
Tests: DeviceView, dock and TopologySemantics suites. Browser-check graph controls,
client filters, detail links, dock restore and independently scrolling split panes.
Verify: `uv run tools/scripts/run.py verify change -- frontend/web/src/DeviceView.vue frontend/web/src/DeviceView.test.ts frontend/web/src/ClientsView.vue frontend/web/src/components/topology frontend/web/src/navigation/PageDock.vue frontend/web/src/navigation/PageHost.vue frontend/web/src/navigation/dock.test.ts frontend/web/src/style.css`

### U5. Assistant, overlays and login

Files: `frontend/web/src/ui/ai/`, `frontend/web/src/ui/dialog/`, `frontend/web/src/ui/popover/`, `frontend/web/src/ui/dropdown-menu/`, `frontend/web/src/ui/command/`, `frontend/web/src/ui/combobox/`, `frontend/web/src/ui/context-menu/`, `frontend/web/src/ui/toast/`, `frontend/web/src/ui/tooltip/`, `frontend/web/src/ui/alert-dialog/`, `frontend/web/src/components/HelpDialog.vue`, `frontend/web/src/components/ReportBugDialog.vue`, `frontend/web/src/LoginView.vue`, `frontend/web/src/LoginView.test.ts`.
After: U1
Change: applies the shared panel and type treatment to AI and overlay surfaces,
including open states and generated result cards. Uses the current AI color roles
and presentation modes. Login uses the shared controls inside its existing frame.
Keeps current copy, overlay stacking, dismissal, focus and AI registration.
Tests: existing AI, dialog, popover, dropdown, command and login suites plus their
stories. Cover docked/narrow assistant, contextual target selection, nested overlay
focus return, loading/error AI states and German login fit. Run AI target contract
and story accessibility tests without weakening their assertions.
Verify: `uv run tools/scripts/run.py verify change -- frontend/web/src/ui/ai frontend/web/src/ui/dialog frontend/web/src/ui/popover frontend/web/src/ui/dropdown-menu frontend/web/src/ui/command frontend/web/src/ui/combobox frontend/web/src/ui/context-menu frontend/web/src/ui/toast frontend/web/src/ui/tooltip frontend/web/src/ui/alert-dialog frontend/web/src/components/HelpDialog.vue frontend/web/src/components/ReportBugDialog.vue frontend/web/src/LoginView.vue frontend/web/src/LoginView.test.ts`

### U6. Integrated visual acceptance

Files: `frontend/web/src/FleetView.vue`, `frontend/web/src/FleetView.test.ts`, `frontend/web/src/FleetView.locale.test.ts`, `frontend/web/src/FleetView.motion.test.ts`, `frontend/web/src/DashboardView.vue`, `frontend/web/src/components/GlobalSearch.vue`, `frontend/web/src/components/GlobalSearch.test.ts`, `frontend/web/src/components/AppIcon.vue`, `frontend/web/src/components/topology/TopologyGraph.vue`, `frontend/web/src/components/topology/TopologySemantics.test.ts`, `frontend/web/src/i18n/locales/en.json`, `frontend/web/src/i18n/locales/de.json`, `frontend/web/src/style.css`, `frontend/web/README.md`, `frontend/web/design/signal/implementation-review.md` (new), `frontend/web/design/signal/implementation/` (new captures).
After: U2, U3, U4, U5
Change: records application captures and concrete visual comparisons without
replacing the target references. Updates frontend documentation for the resulting
layout. Checks every route, theme and locale against the acceptance matrix.
Tests: fleet integration, locale and motion suites retain existing behavior.
Run the full web suite, palette/AI target/story accessibility gates and Storybook
build. Manually verify layout, open overlays, keyboard navigation and contrast
on rendered glass surfaces. Record any remaining mismatch as incomplete work.
Verify: `uv run tools/scripts/run.py verify change -- frontend/web/src/FleetView.vue frontend/web/src/FleetView.test.ts frontend/web/src/FleetView.locale.test.ts frontend/web/src/FleetView.motion.test.ts frontend/web/src/DashboardView.vue frontend/web/src/components/GlobalSearch.vue frontend/web/src/components/GlobalSearch.test.ts frontend/web/src/components/AppIcon.vue frontend/web/src/components/topology/TopologyGraph.vue frontend/web/src/components/topology/TopologySemantics.test.ts frontend/web/src/i18n/locales/en.json frontend/web/src/i18n/locales/de.json frontend/web/src/style.css frontend/web/README.md frontend/web/design/signal/implementation-review.md frontend/web/design/signal/implementation`

Waves: U1 | U2 U3 U5 | U4 | U6

## Verification

Run each unit's verifier over every changed path, including stories and tests.
Run `pnpm --dir frontend/web build-storybook` after integration. The final
`uv run tools/scripts/run.py verify change -- frontend/web` covers the web
build, type, lint, formatting and test gates. Do not replace those gates with
visual inspection.

Compare the running app with the reference in both themes. Check all navigation
destinations, empty and populated scopes, 6h/24h traffic, keyboard chart access,
sidebar collapse, search dismissal, assistant, account controls and device links.
Repeat at the sizes in requirement 10 and with a narrow split pane. Happy-dom
unit tests do not prove rendered geometry or contrast on blurred surfaces.

## Definition of done

- [x] Signal direction is accepted before implementation begins.
- [ ] Every routed view uses the shared design language and preserves its actions.
- [ ] Reference comparison and responsive/theme/locale matrix are recorded.
- [ ] Every changed component has updated stories and required behavior coverage.
- [ ] Verifier is green for every changed path and Storybook builds.
- [ ] Frontend documentation describes the implemented styling.
- [ ] Outcome is recorded with `plan record implemented` or `partial`.
- [ ] No plan labels appear in application code or comments.

## Open questions

None. The pane-width thresholds are implementation choices governed by the
responsive acceptance matrix.
