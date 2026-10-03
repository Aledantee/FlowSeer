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

## Goal

The views and `src/components/` move their strings to `view.<view>.<key>`
messages. Numbers, dates, relative times, and units format through
vue-i18n or `Intl` for the active locale. Identifiers carry
`translate="no"`. A locale switch in the app re-renders every view in
German. Two composables under `src/i18n/` own formatting and identifier
labels, and each view unit moves its strings into both catalogs.

Stop condition: `frontend/web/src/ai/catalog.ts` or a `UiAiContextLayer`
exists when implementation starts. The AI actions plan then landed
first and rewrote the AI targets of every view inventoried here.

## Decisions

- The contract's i18n section and the parent's Decisions govern.
- **Fixture data under `src/domain/` stays untranslated.** It stands in
  for service data, which arrives in the user's locale-neutral form. A
  fixture value typed `string` renders verbatim: names, locations,
  `Device.kind`, `Integration.kind`, event summaries, models, endpoints.
  A value whose type is a union of literals is an identifier, and its
  display text is a message: `Health`, `Severity`, `Reachability`,
  `Lifecycle`, `PortStatus`, `Band`, link `medium`, port `mode`,
  `duplex`, and the result of `signalQuality`. Why:
  `UiStatusBadge.vue:20-27` already maps `Health` this way.
- **One cluster, landed as a chain.** Every view unit adds its owner's
  subtree to `locales/en.json` and `de.json`, and the record fixes one
  catalog file per locale. A catalog written whole in a first unit would
  widen the graph, but a view's message shapes (plural forms, slots
  around inline markup) are settled while its template is edited, so
  every view unit would reopen it. Seven units stay one plan for the
  same reason (`.agents/skills/plan/references/phases.md`).
- **Owners.** A message is keyed `view.<owner>.<key>`, with the owners
  the String inventory lists. Vocabulary two owners share lives under
  `view.common`. Why: page names, health words, and units repeat across
  files today (`FleetView.vue:337`, `WorkspacePage.vue:87-97`).
- **English keeps today's literals, with three exceptions.** The traffic
  unit reads `Mbit/s` and `Gbit/s`, relative times come from
  `Intl.RelativeTimeFormat`, and clock times from `d()`. Why:
  requirement 2 fixes `Mbit/s` in both locales, `Intl.NumberFormat` with
  `unit: 'megabit-per-second'` prints `Mb/s` in both, and the contract
  sends relative times and dates through `Intl`.
- **`useFormat()` in `src/i18n/format.ts` formats values.** It reads the
  global Composer on every call, so output follows a locale switch.
  - `quantity(value, unit)` joins `n(value, 'decimal')` and the unit's
    message with a non-breaking space through one `valueWithUnit`
    message. A template that styles the unit apart uses `I18nT` slots on
    that message, as `UiMetricCard.vue:34-51` does.
  - `rate(mbps)` is megabits per second, scaled on request to gigabits
    from 1000 (`TopologyInspector.vue:80-83`). `speed(mbps)` is the
    compact `10G` or `100M` (`DevicePorts.vue:16-19`).
  - `counted(key, count)` formats the count with `n()` and selects the
    plural form of `key` by the raw count.
  - `ago(minutes)` uses `Intl.RelativeTimeFormat` with `numeric: 'auto'`
    and `style: 'short'`: zero seconds under one minute, then whole
    minutes under 60, whole hours under 1440, then whole days.
  - `clock(date)` is `d(date, 'time')`, a named format with
    `hour: 'numeric'` and `minute: '2-digit'`.
  - A duration such as uptime is a message with `n()`-formatted parts,
    since `Intl.DurationFormat` is undefined on Node 22.14.0.
- **`useLabels()` in `src/i18n/labels.ts` names identifiers.** It maps
  each identifier above and each page id to its `view.common` message,
  and builds a rollup's health line (`1 offline`, `All healthy`). Why:
  two views need each, and the contract puts such state in a composable.
- **No message is read into a plain constant.** A `t()` call in a
  top-level `const` keeps the mount locale. Each such table becomes a
  `computed` or moves into the template: `VIEW_TITLES`
  (`FleetView.vue:337`), `groups` (`GlobalSearch.vue:86`),
  `severityLabel` (`DashboardView.vue:149`, `DeviceView.vue:105`),
  `bandOptions` (`ClientsView.vue:72`). Every view unit tests a live
  switch for this reason.
- **Plurals are vue-i18n plural messages.** `counted` calls
  `t(key, { count: n(total, 'integer') }, total)`. Why: a numeric
  `named.count` would select the form, and a string leaves selection to
  the index (`core-base.mjs:1359-1385`). The ternaries at
  `WorkspacePage.vue:157`, `:438`, `:564` and `ClientsView.vue:167` go.
- **One sentence is one message.** Named interpolation carries the data
  and `I18nT` with `scope="global"` carries inline markup. A sentence
  that embeds an identifier word (`is offline`, `Dark mode enabled.`) is
  one message per identifier value.
- **Punctuation with no letters stays in templates:** the middle dot
  between facts, the arrow between link ends, the dash for no reading.
  Why: it has no translation. Phase 3 moved glyphs into messages to give
  `Ui*` callers an override prop, which a view lacks. This narrows the
  parent's "every visible string" to text a translator would change.
- **AI targets follow the screen.** A target's `label` and the
  display-derived values in its `context` come from the same messages
  and formatters as the visible text. Context keys and identifier values
  (`health: 'Offline'`, `status: 'all'`) stay. Why: each such value is
  the expression the template renders (`DashboardView.vue:59`,
  `:291-293`), and a second English path would double every formatter.
- **Text leaves `src/domain/`.** `formatAgo` and `healthLine` are
  deleted once no file imports them. `searchAll` stops building detail
  text (`search.ts:62`, `:88`, `:104`), and `GlobalSearch.vue` builds it
  at render. A recent search stores `kind`, `id`, and `port` and
  resolves its text from current data. Why: `recentSearches.ts:6-18`
  persists `title` and `detail`, so text stored under one locale would
  show under the other.
- **`translate="no"`** goes on names of devices, clients, sites, and
  tenants, on IP and MAC addresses, serials, port names, models,
  firmware versions, and the brand. Why: the contract names hostnames,
  MAC addresses, and identifiers, and a page translator would also
  rewrite `Berlin Mitte`.
- **A test reads the templates.** `templateLiterals(source)` in
  `src/i18n/testing.ts` parses a `.vue` file with `vue/compiler-sfc` and
  reports each template text node holding a letter, and each static
  `aria-label`, `aria-description`, `title`, `placeholder`, `alt`,
  `label`, `hint`, `description`, `detail`, or `text` attribute holding
  one. Each unit adds its files to `templates.test.ts`. Why: about 400
  literals are too many to check by reading, and `vue` already exports
  the compiler, so no package is added. It cannot see a literal in a
  bound expression or in `<script>`.

### External sources

Versions as locked in `frontend/web/pnpm-lock.yaml`: vue-i18n and
`@intlify/core-base` 11.4.12, Vue 3.5.43. Paths are relative to
`frontend/web/` after `pnpm install --frozen-lockfile`. `core-base.mjs`
is `node_modules/.pnpm/@intlify+core-base@11.4.12/node_modules/@intlify/core-base/dist/core-base.mjs`.

| Source | Behavior used |
| --- | --- |
| `core-base.mjs:1145-1185` | `d(value, key)` builds `new Intl.DateTimeFormat(locale, format)` from `datetimeFormats[locale][key]`. |
| `core-base.mjs:1359-1385` | A numeric `named.count` is the plural index. Otherwise the index argument is, and it fills `count` only when unset. |
| `core-base.mjs:1347-1358` | Two forms select on `count === 1`. Three forms select zero, one, other. |
| `core-base.mjs:1055-1092`, `:769-774` | A named format missing from the active locale warns `Fall back to datetime format` and `Not found`, which the story audit's matcher (`a11y.test.ts:16-33`) catches. |
| `node_modules/vue-i18n/dist/vue-i18n.node.mjs:291-294`, `:658-662` | `datetimeFormats` is kept by reference and `mergeDateTimeFormat` assigns into it, as the [catalog solution](../solutions/conventions/vue-i18n-instances-mutate-catalogs-and-formats-in-place.md) records for number formats. |
| `node_modules/vue/compiler-sfc/index.mjs` | `vue` re-exports the SFC compiler. |

`Intl` output is locale data. Node 22.14.0 (ICU 76.1) prints the values
below. A browser's ICU can abbreviate differently, so tests build their
expectation from `Intl` with literal arguments and pin a literal only
for `n()` grouping.

```bash
node -e "for (const l of ['en','de']) { const r = new Intl.RelativeTimeFormat(l, { numeric: 'auto', style: 'short' }); console.log(l, r.format(-38, 'minute'), '|', r.format(-13, 'hour'), '|', r.format(0, 'second'), '|', new Intl.NumberFormat(l, { style: 'unit', unit: 'megabit-per-second' }).format(1234.5), '|', new Intl.DateTimeFormat(l, { hour: 'numeric', minute: '2-digit' }).format(new Date(2026, 9, 3, 14, 0))) }"
```

| Locale | 38 minutes | 13 hours | zero | unit style | 14:00 |
| --- | --- | --- | --- | --- | --- |
| `en` | `38 min. ago` | `13 hr. ago` | `now` | `1,234.5 Mb/s` | `2:00 PM` |
| `de` | `vor 38 Min.` | `vor 13 Std.` | `jetzt` | `1.234,5 Mb/s` | `14:00` |

### String inventory

Paths are relative to `frontend/web/src/`. The last column lists what
needs more than swapping a literal for `t()`. After U1,
`templateLiterals` lists each file's remaining template literals.

| File | Owner | Unit | Beyond a literal swap |
| --- | --- | --- | --- |
| `FleetView.vue` | `fleet` | U2 | Nav text and label derived from the route id (`:888`, `:910`). `describe`, `tabTitle`, `searchPages` (`:622-672`). `document.title` (`:781`). Ternary labels on the sidebar and link-click tooltips. Error notices (`:158`, `:250`, `:300`, `:397`). |
| `navigation/PageDock.vue` | `dock` | U2 | Badge `{count} need attention` (`:25`). Hint joined from parts (`:53`). |
| `navigation/PageHost.vue` | `fleet` | U2 | One label. |
| `components/GlobalSearch.vue` | `search` | U3 | `groups` constant (`:86`). Footer hints. Detail text from `domain/search.ts`. Recent entries. |
| `components/ThemeSwitcher.vue` | `themeSwitcher` | U3 | Announcement and label spliced from a mode word (`:37`, `:76`). |
| `components/ScopeSwitcher.vue`, `TenantSwitcher.vue` | `scopeSwitcher`, `tenantSwitcher` | U3 | `{label}: {name}` (`ScopeSwitcher.vue:42`). Option lists built in the template. |
| `components/HelpButton.vue`, `ReportBugButton.vue`, `AccountMenu.vue` | `help`, `reportBug`, `accountMenu` | U3 | Copied report text (`ReportBugButton.vue:28`). |
| `WorkspacePage.vue` | `workspace`, `devices`, `sites` | U4 | Page title map (`:87-97`). Scope error (`:116`). `scopeSummary` (`:152-158`). Heading totals and `toLocaleTimeString` (`:437-448`). `statusOptions` (`:304`). Move notices (`:368-376`, `:479-487`). Result and device counts. `healthLine`, `formatAgo`. |
| `DashboardView.vue` | `dashboard` | U4 | Peak sentence with inline `strong` (`:289-293`). A `v-text` literal (`:222`). `reason` (`:161-170`). Counts (`:225`, `:433`). Chart label (`:302`). Target labels. |
| `components/TrafficChart.vue` | `trafficChart` | U4 | `hourLabel` (`:45`), `Now`, the label sentence (`:93`), the table header unit. |
| `DeviceView.vue` | `device` | U5 | Escalation summary (`:123-140`). Poll results (`:173-174`). Neighbour sentences (`:297`). Health, reachability, and lifecycle words. |
| `ClientsView.vue` | `clients` | U5 | `bandOptions` (`:72`). Signal `{value} dBm ({quality})` (`:59`). Result count. |
| `components/DevicePorts.vue` | `devicePorts` | U5 | Port counts, `W PoE`, compact speeds (`:16-19`). |
| `components/topology/TopologyInspector.vue` | `topologyInspector` | U6 | `uptime`, `ago`, `rate` (`:69-83`). Capacity detail with `toLocaleString` (`:303`). Three-way `aria-label` (`:94`). |
| `components/topology/TopologyLink.vue`, `TopologyNode.vue`, `TopologySiteNode.vue`, `TopologyGraph.vue` | `topology` | U6 | Link `aria-label` sentence (`TopologyLink.vue:140`). Node `aria-label` (`TopologyNode.vue:51`). The legend that `TopologySemantics.test.ts:123-141` reads from source. |

`AppIcon.vue`, `DeviceIcon.vue`, `TrafficSparkline.vue`, `App.vue`, and
`AppLink.vue` render no text. Thrown developer errors (`AppLink.vue:21`,
`navigation/page.ts:118`, `topology/live.ts:26`) are not UI text.

These German values are fixed. The implementer chooses every other
translation in the infinitive style of the `ui.*` messages
(`Option auswählen...`), with no direct address.

| English | German |
| --- | --- |
| Dashboard, Devices, Clients, Sites, Topology | Dashboard, Geräte, Clients, Standorte, Topologie |
| Tenant, All tenants, All sites | Mandant, Alle Mandanten, Alle Standorte |
| Healthy, Degraded, Offline | Gesund, Beeinträchtigt, Offline (as `ui.statusBadge`) |
| Needs attention | Handlungsbedarf |
| `{count} device`, `{count} devices` | `{count} Gerät`, `{count} Geräte` |
| `{count} result`, `{count} results` | `{count} Ergebnis`, `{count} Ergebnisse` |

## Requirements

1. With the locale set to `de`, the dashboard, device, clients, sites,
   and topology views show no English UI text. Example: the Sites
   heading and its count read in German, with the count formatted by
   `n()`.
2. Traffic values and timestamps format per locale. Example: `1.234,5
   Mbit/s` in `de` and `1,234.5 Mbit/s` in `en`.
3. Changing the Composer locale on a mounted app re-renders the views
   without a remount. Example: with `/devices` mounted in `en`, setting
   the locale to `de` turns the heading `Devices` into `Geräte`, and
   setting it back restores `Devices`.
4. Counts select a plural form. Example: the inventory reads `16
   results`, and `1 result` once a search leaves one row. In `de` it
   reads `16 Ergebnisse` and `1 Ergebnis`.
5. Relative times follow the locale. Example: a device last seen 38
   minutes ago shows what `Intl.RelativeTimeFormat` prints for `-38,
   'minute'`, which is `38 min. ago` and `vor 38 Min.` on ICU 76.1.
6. Identifiers carry `translate="no"`. Example: the device name cell in
   the inventory table and the MAC address cell in the clients table
   each sit inside an element with `translate="no"`.
7. No app template holds literal text. Example: `<p>Hello</p>` in any
   view fails `templates.test.ts` with the file and line.

## Out of scope

- `src/ai/`. The mock handler's English answers stand in for a model's
  output, and the AI actions plan rewrites the directory.
- The `ui-table--default` story, which scrolls the page at 320 px. Its
  rows register AI targets (`UiTable.stories.ts:79-84`), and wrapping it
  in `UiScrollArea` hid the AI action from the story audit, cause
  unverified. Every view table already sits in a `UiScrollArea`
  (`DashboardView.vue:326`, `ClientsView.vue:170`,
  `WorkspacePage.vue:604`, `:847`), so no view depends on it. It belongs
  to phase 5, the next change to `src/ui/table/` stories.
- An i18n lint package, lazy loading, and locales beyond `en` and `de`.
  No package is added, so no dependency statement is written.
- `templateLiterals` reads this repository's own `.vue` files, written
  by trusted authors. It does not defend against hostile input.

## Units

### U1. Formatting, shared vocabulary, and the template check

Files: `frontend/web/src/i18n/index.ts`, `frontend/web/src/i18n/format.ts`, `frontend/web/src/i18n/format.test.ts`, `frontend/web/src/i18n/labels.ts`, `frontend/web/src/i18n/labels.test.ts`, `frontend/web/src/i18n/testing.ts`, `frontend/web/src/i18n/templates.test.ts`, `frontend/web/src/i18n/i18n.test.ts`, `frontend/web/src/i18n/locales/en.json`, `frontend/web/src/i18n/locales/de.json`, `frontend/web/src/ui/a11y.test.ts`
After: none
Change: `createWebI18n` passes `datetimeFormats` with the `time` format for both locales, cloned per instance like `numberFormats`. `useFormat` and `useLabels` exist as the Decisions describe. Both catalogs gain `view.common`: the units `Mbit/s`, `Gbit/s`, `dBm`, `ms`, `°C`, `GB`, `W`, and `GHz`, the `valueWithUnit` message, the compact speeds, a label for every identifier value and page id, the health-line plurals, the device, client, site, and result count plurals, and `Unknown site`, `Unknown tenant`, `Unknown device`, `All sites`, `All tenants`. `testing.ts` exports `templateLiterals` and `i18nWarnings`, the warning matcher moved out of `a11y.test.ts:16-33`, which now imports it. `templates.test.ts` checks every `src/ui/**/Ui*.vue` file, none of which holds a literal today.
Tests: `format.test.ts` checks `1234.5` as `1,234.5\u00a0Mbit/s` and `1.234,5\u00a0Mbit/s`, the scaled rate on both sides of 1000, `counted` at 1, 2, and 1234 in both locales (`1,234 devices`, `1.234 Geräte`), `speed`, `ago` on both sides of each threshold against `Intl.RelativeTimeFormat` called with the literal value and unit, `clock` against `Intl.DateTimeFormat`, and each again after a locale switch on the same instance. `labels.test.ts` checks every identifier value in both locales, the glossary values, and the health line for `{ Healthy: 3, Degraded: 0, Offline: 1 }`, for two non-zero counts, and for none. `i18n.test.ts` extends its isolation case to `mergeDateTimeFormat`, merging before any `setDateTimeFormat`. `templates.test.ts` proves the checker on inline sources: a text node, a static `aria-label`, and a `placeholder` are each reported with their line, and an interpolation, a bound attribute, and a text node holding only a middle dot are not. Nothing here covers a difference between Node's ICU and a browser's.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/i18n frontend/web/src/ui/a11y.test.ts`

### U2. Shell and navigation

Files: `frontend/web/src/FleetView.vue`, `frontend/web/src/navigation/PageDock.vue`, `frontend/web/src/navigation/PageHost.vue`, `frontend/web/src/FleetView.locale.test.ts`, `frontend/web/src/i18n/templates.test.ts`, `frontend/web/src/i18n/locales/en.json`, `frontend/web/src/i18n/locales/de.json`
After: U1
Change: Every string in the three files comes from `view.fleet`, `view.dock`, or `view.common`. Page names come from `useLabels`, and the navigation stops capitalizing the route id. The devices link label is one message with the page name and a counted scope. `describe`, `tabTitle`, and `searchPages` build labels and details from messages, with a docked pair as one message holding both names. `document.title` is one message around the page names. The brand name is a script constant bound into the template with `translate="no"`, since a name has no translation.
Tests: `FleetView.locale.test.ts` holds a `mountLocale(path, locale)` harness shaped like `mountAt` in `FleetView.test.ts:66-97`, with a `console.warn` spy read through `i18nWarnings`. At `/dashboard` in `de` it asserts the five navigation names from the glossary, the devices link label with its count, the document title, the sidebar toggle label, and no i18n warning. It toggles the split with the workspace shortcut and asserts the split controls' German labels. It then sets the locale to `en` and back on the same mount and asserts the navigation and the title follow, which fails when a title table is read once. `templates.test.ts` gains the three files. `FleetView.test.ts` and `FleetView.motion.test.ts` pass unchanged.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/FleetView.vue frontend/web/src/navigation/PageDock.vue frontend/web/src/navigation/PageHost.vue frontend/web/src/FleetView.locale.test.ts frontend/web/src/i18n/templates.test.ts frontend/web/src/i18n/locales/en.json frontend/web/src/i18n/locales/de.json`

### U3. Top bar components and search

Files: `frontend/web/src/components/AccountMenu.vue`, `frontend/web/src/components/HelpButton.vue`, `frontend/web/src/components/ReportBugButton.vue`, `frontend/web/src/components/ScopeSwitcher.vue`, `frontend/web/src/components/TenantSwitcher.vue`, `frontend/web/src/components/ThemeSwitcher.vue`, `frontend/web/src/components/GlobalSearch.vue`, `frontend/web/src/components/recentSearches.ts`, `frontend/web/src/components/recentSearches.test.ts`, `frontend/web/src/domain/search.ts`, `frontend/web/src/domain/search.test.ts`, `frontend/web/src/components/GlobalSearch.test.ts`, `frontend/web/src/components/TenantSwitcher.test.ts`, `frontend/web/src/components/ThemeSwitcher.test.ts`, `frontend/web/src/FleetView.locale.test.ts`, `frontend/web/src/i18n/templates.test.ts`, `frontend/web/src/i18n/locales/en.json`, `frontend/web/src/i18n/locales/de.json`
After: U2
Change: Every string in the seven components comes from its owner or `view.common`. `SearchResult` loses `detail`, and `searchAll` returns only what identifies and titles a result. `GlobalSearch.vue` builds each detail when it renders: a tenant's counted sites, a client's address, MAC, and access point in one message, an interface's port status label and far end. A recent entry stores `kind`, `id`, and `port`, and its title and detail resolve from the current pages, tenants, sites, and fleet. A stored entry in the old shape still loads. The theme announcement and switch label are one message per mode. The copied bug report is one message with `summary`, `page`, and `description`.
Tests: `GlobalSearch.test.ts` mounts in `de` and asserts the group names, the empty and hint texts, a tenant's site count in both plural forms, and no i18n warning. It stores a recent device under `en`, switches to `de`, and asserts the row's detail is German. `recentSearches.test.ts` covers the stored shape and an old-shape entry. `search.test.ts` keeps its ranking cases against the new result shape. `TenantSwitcher.test.ts` asserts `Alle Mandanten` and the unavailable label in `de`. `ThemeSwitcher.test.ts` asserts both announcements in `de` and a live switch of the tooltip label. `FleetView.locale.test.ts` opens the help dialog and the bug form in `de` and asserts their headings, the copied report's page line, and the copy status. `templates.test.ts` gains the seven files.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/components frontend/web/src/domain/search.ts frontend/web/src/domain/search.test.ts frontend/web/src/FleetView.locale.test.ts frontend/web/src/i18n/templates.test.ts frontend/web/src/i18n/locales/en.json frontend/web/src/i18n/locales/de.json`

### U4. Dashboard, inventory, and sites

Files: `frontend/web/src/WorkspacePage.vue`, `frontend/web/src/DashboardView.vue`, `frontend/web/src/components/TrafficChart.vue`, `frontend/web/src/FleetView.test.ts`, `frontend/web/src/FleetView.locale.test.ts`, `frontend/web/src/i18n/templates.test.ts`, `frontend/web/src/i18n/locales/en.json`, `frontend/web/src/i18n/locales/de.json`
After: U3
Change: Every string in the three files comes from `view.workspace`, `view.devices`, `view.sites`, `view.dashboard`, `view.trafficChart`, or `view.common`. The heading line is separate facts, each a message: counted devices and clients, the rate through `useFormat`, and `as of {time}` through `clock`. `scopeSummary` is one plural message per case. The three move notices are one message each. The peak sentence is one message with `rate` and `time` slots through `I18nT`. `reason` joins whole messages. Health lines and ages come from `useLabels` and `useFormat`, so neither view imports `formatAgo` or `healthLine`. Chart hour labels come from `clock`. Device names, addresses, and site and tenant names carry `translate="no"`.
Tests: `FleetView.test.ts:346` asserts the offline row holds no `Mbit/s`, and `:466` and `:476` take their expectation from `Intl.RelativeTimeFormat` with `-38, 'minute'`. Its other English assertions pass unchanged, including the target label at `:587`. `FleetView.locale.test.ts` gains `/dashboard`, `/devices`, and `/sites` in `de`: the Sites heading `Standorte` and its count, `16 Geräte` in the heading line, `16 Ergebnisse` and `1 Ergebnis` under a search, the German relative time of the stalest device, a traffic value with a non-breaking space before `Mbit/s`, the status filter options, the dashboard target's German `label` in the registry, `translate="no"` around a device name and an address, a live switch on `/devices`, and no i18n warning. `templates.test.ts` gains the three files. The `TrafficChart` stories keep passing the both-locale story audit in `a11y.test.ts`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/WorkspacePage.vue frontend/web/src/DashboardView.vue frontend/web/src/components/TrafficChart.vue frontend/web/src/FleetView.test.ts frontend/web/src/FleetView.locale.test.ts frontend/web/src/i18n/templates.test.ts frontend/web/src/i18n/locales/en.json frontend/web/src/i18n/locales/de.json`

### U5. Device, clients, and ports

Files: `frontend/web/src/DeviceView.vue`, `frontend/web/src/ClientsView.vue`, `frontend/web/src/components/DevicePorts.vue`, `frontend/web/src/domain/overview.ts`, `frontend/web/src/domain/overview.test.ts`, `frontend/web/src/DeviceView.test.ts`, `frontend/web/src/FleetView.locale.test.ts`, `frontend/web/src/i18n/templates.test.ts`, `frontend/web/src/i18n/locales/en.json`, `frontend/web/src/i18n/locales/de.json`
After: U4
Change: Every string in the three files comes from `view.device`, `view.clients`, `view.devicePorts`, or `view.common`. The escalation summary is one message per line, with the health and reachability sentences one message per identifier value. The poll results and the neighbour sentences are whole messages, the latter plural on the total. Signal reads `{value} dBm ({quality})` through one message. Band options and labels come from `useLabels`, so `2.4 GHz` reads `2,4 GHz` in `de`. Port counts go through `counted`, and port speeds through `speed`. `formatAgo` and `healthLine` leave `domain/overview.ts`, which no file imports them from after U4 and this unit. Hostnames, addresses, MAC addresses, and port names carry `translate="no"`.
Tests: `DeviceView.test.ts` mounts the offline device in `de` and asserts the status heading, the lifecycle and reachability labels, the escalation summary's first line, and a poll result. It keeps `Site edge` and the severity names in `en`. `overview.test.ts` drops its `healthLine` assertion, which `labels.test.ts` holds since U1. `FleetView.locale.test.ts` gains `/clients` and a device route in `de`: the table headings, `2,4 GHz` in the band filter, the signal message, both result-count forms, `translate="no"` around a MAC address, a live switch, and no i18n warning. `templates.test.ts` gains the three files.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/DeviceView.vue frontend/web/src/ClientsView.vue frontend/web/src/components/DevicePorts.vue frontend/web/src/domain/overview.ts frontend/web/src/domain/overview.test.ts frontend/web/src/DeviceView.test.ts frontend/web/src/FleetView.locale.test.ts frontend/web/src/i18n/templates.test.ts frontend/web/src/i18n/locales/en.json frontend/web/src/i18n/locales/de.json`

### U6. Topology

Files: `frontend/web/src/components/topology/TopologyGraph.vue`, `frontend/web/src/components/topology/TopologyInspector.vue`, `frontend/web/src/components/topology/TopologyLink.vue`, `frontend/web/src/components/topology/TopologyNode.vue`, `frontend/web/src/components/topology/TopologySiteNode.vue`, `frontend/web/src/components/topology/TopologySemantics.test.ts`, `frontend/web/src/i18n/templates.test.ts`, `frontend/web/src/i18n/locales/en.json`, `frontend/web/src/i18n/locales/de.json`
After: U5
Change: Every string in the five files comes from `view.topology`, `view.topologyInspector`, or `view.common`. The inspector's `ago` and `rate` become `useFormat` calls, uptime becomes duration messages, and the capacity detail drops `toLocaleString`. Port status, mode, duplex, and medium come from `useLabels`, and the link speed from `speed`. The link and node `aria-label` sentences are one message each with named values. The legend and the layout error are messages. Device and port names carry `translate="no"`.
Tests: `TopologySemantics.test.ts` takes a locale and an initial selection in its `mount` helper and provides the page and workspace contexts as `DeviceView.test.ts:62-63` does, so it can mount `TopologyInspector`. In `de` it asserts a node's `aria-label`, the link label's German assumption text, and the inspector's field names, uptime, scaled rate, and port status for a device, a link, and a port selection, with `translate="no"` around a node's device name, no i18n warning, and a live switch on the inspector. Its source case at `:123-141` asserts that the `topology-assumption` element renders the legend's message key and that `en.json` holds `Assumed link, not yet discovered` under it. `templates.test.ts` gains the five files. `TopologyGraph.vue` needs layout that happy-dom lacks, so nothing mounts it. The template check and the read in U7 cover its strings.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/components/topology frontend/web/src/i18n/templates.test.ts frontend/web/src/i18n/locales/en.json frontend/web/src/i18n/locales/de.json`

### U7. Whole-app locale sweep and documentation

Files: `frontend/web/src/FleetView.locale.test.ts`, `frontend/web/src/i18n/templates.test.ts`, `frontend/web/README.md`, `.agents/skills/web-component/references/i18n-and-ai.md`
After: U6
Change: `templates.test.ts` replaces its file list with globs over `src/*.vue`, `src/components/**/*.vue`, `src/navigation/**/*.vue`, and `src/ui/**/Ui*.vue`. The README's Internationalization section describes view message owners, `useFormat`, `useLabels`, the unit and relative-time rules, `translate="no"`, and the template check, with a working example from a view. The skill reference gains the view rules: owner naming, the two composables, the frozen-constant trap, and the check.
Tests: `FleetView.locale.test.ts` sweeps `/dashboard`, `/devices`, a device route, `/clients`, and `/sites` in both locales and through a live switch in each direction, and asserts no i18n warning at any step. `templates.test.ts` fails on any `.vue` file the globs match that holds a literal. Nothing mechanical finds an English string built in `<script>` or in a bound expression that never reaches a message. The review reads each migrated file's script against the String inventory for those.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/FleetView.locale.test.ts frontend/web/src/i18n/templates.test.ts frontend/web/README.md .agents/skills/web-component/references/i18n-and-ai.md`

Waves: U1 | U2 | U3 | U4 | U5 | U6 | U7

Every unit from U2 on edits both catalogs and `templates.test.ts`, which
makes the graph a chain.

## Verification

From `frontend/web/`:

```bash
pnpm install --frozen-lockfile
./node_modules/.bin/vitest run src/i18n src/FleetView.locale.test.ts src/FleetView.test.ts src/DeviceView.test.ts src/components src/domain src/ui/a11y.test.ts
pnpm build
```

Each unit runs its focused tests and the verifier over its Files. The
browser loop in `.agents/skills/web-component/references/review.md` runs
for the `TrafficChart` stories in both locales and themes. The views
have no stories. Looking at them in German at 320 px and 1280 px needs
the app to start in German, which open question 1 decides.

## Definition of done

- [ ] All seven requirements pass.
- [ ] Every changed path passes the diff-aware verifier.
- [ ] The review read every migrated file's script for strings the
      template check cannot see.
- [ ] The README and the i18n skill reference describe the view rules.
- [ ] This plan reads `status: implemented` with an outcome note.
- [ ] Requirement and unit labels appear only in the plan.

## Open questions

1. **How is the app's locale chosen?** This keeps the plan at
   `needs-decisions`. `main.ts:32` always starts in `en`, and nothing in
   the tree changes the running app's locale. The Goal's "locale switch
   in the app" reads either as a control, the way the README calls the
   theme control "the icon-only theme switch", or as the Composer change
   requirement 3 already tests. The direction record names neither.
   - **A language switch beside the theme switch (recommended).** A
     saved choice wins, then the first of `navigator.languages` that is
     `en` or `de`, then `en`. The theme works this way, with its choice
     saved under `flowseer.theme`. German becomes reachable, and the
     review can look at the German views at 320 px. It costs one more
     unit and one more control in a top bar that is tight on phones.
   - **Browser language at startup, no control.** No new UI. A user
     cannot override it, and the browser check needs a browser started
     in German.
   - **Nothing in this phase.** The locale changes only in tests and
     Storybook. German view layout gets no real-browser check, so an
     overflow at 320 px stays unseen until a later plan.

   The first answer adds a unit after U7: `LocaleSwitcher.vue` under
   `src/components/`, a `src/i18n/locale.ts` that resolves the choice
   and stores it under `flowseer.locale`, `main.ts` starting from it,
   and `<html lang>` kept equal to the Composer locale. The second adds
   that unit without the component and the storage. U1 to U7 stand
   either way.
2. Unverified: why the story audit lost the AI action once
   `ui-table--default` was wrapped in `UiScrollArea`. See Out of scope.
3. Unverified: whether a browser's ICU abbreviates relative times as
   Node 22.14.0 does. Tests do not pin those strings, and the browser
   check is where a difference would show.
