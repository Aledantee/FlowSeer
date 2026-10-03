---
title: Rendered Identifier Translation Guards Require an Executable DOM Property and Positive Cases
date: 2026-10-03
last_verified: 2026-10-03
category: conventions
module: frontend/web/src/i18n
problem_type: convention
component: web-console
severity: high
applies_when:
  - "Guarding domain identifiers (hostnames, MACs, IP addresses, serials, site names) against browser translation in Vue templates or components."
  - "Writing or reviewing tests for translate=\"no\" attribute coverage across rendered views, portalled overlays, or search results."
  - "An identifier translation test passes while rendered DOM elements or hidden accessibility nodes expose untranslated names to page translation."
related_components: [testing, web_ui]
tags: [vue, i18n, testing, translation, happy-dom, reka-ui, vitest]
---

# Rendered identifier translation guards require an executable DOM property and positive cases

## The situation

Web views that render localized UI text must preserve domain identifiers (hostnames, IP addresses, MAC addresses, serial numbers, port names, sites, and brand names) verbatim by enclosing them in elements with `translate="no"`.

In component migrations, developers often mark identifiers by hand in each template and write per-element unit assertions. Review passes routinely uncover missing marks across dynamic props, breadcrumbs, search rows, and portalled components.

## What is true and why

Static review and per-element tests fail to catch omitted translation guards:

- Template authors miss identifiers inside bound expressions, interpolated sentences, search result rows, and tooltips.
- A reliable verification requires an executable property walker (`unmarkedIdentifiers` in `frontend/web/src/i18n/testing.ts:162-206`). The walker traverses rendered text nodes under a container, checks their content against a fixture identifier set (`fixtureIdentifiers` in `frontend/web/src/domain/testing.ts:15-37`), and flags any identifier lacking an ancestor element with `translate="no"`.
- The property walker requires positive test cases and strict boundary checks (`frontend/web/src/i18n/testing.test.ts:6-47`, `:73-102`). Boundary rules must distinguish whole identifiers (`berlin-gw-01 + Devices`, `(Gateway, 10.20.0.1)`) from compound names (`berlin-gw-01-backup`), and handle colons in MAC addresses without truncation (`testing.ts:112-132`). Without positive tests, boundary regex bugs cause the checker to pass vacuously.
- Tests must mount components with portalled overlays open. In headless environments (`happy-dom`), dropdown options, switchers, and tooltips are not rendered into the DOM until opened. Testing `unmarkedIdentifiers` on a closed switcher passes vacuously because options do not exist in the document tree (`frontend/web/src/FleetView.locale.test.ts:333`, `:353`).
- Headless UI primitives can synthesize hidden accessibility text nodes that bypass template slots. In `reka-ui` 2.10.5 (`TooltipContentImpl.js:87`, `:134-140`), `role="tooltip"` renders inside an internal `VisuallyHidden` component from `aria-label`, unaffected by `#label` or `#hint` template slots. Wrapper components must mark that hidden node with `translate="no"` via an explicit component prop (`UiTooltip.vue:24-30`, `:92-103`).

## Working example

In `frontend/web/src/FleetView.locale.test.ts:330-365`, the test opens switcher triggers before asserting that rendered DOM nodes contain no unmarked identifiers:

```ts
const siteTrigger = host.querySelector<HTMLElement>('#site-switcher-trigger')
siteTrigger?.click()
await nextTick()

const identifiers = fixtureIdentifiers()
expect(unmarkedIdentifiers(document.body, identifiers)).toEqual([])
```

In `frontend/web/src/i18n/testing.test.ts:6-18`, positive cases verify that `unmarkedIdentifiers` detects unmarked names across varied surrounding punctuation:

```ts
const container = document.createElement('div')
container.innerHTML = '<p class="title"><span>berlin-gw-01</span></p>'
const found = unmarkedIdentifiers(container, ['berlin-gw-01'])

expect(found).toEqual([
  {
    identifier: 'berlin-gw-01',
    text: 'berlin-gw-01',
    path: 'div > p.title > span',
  },
])
```

## Evidence

- `frontend/web/src/i18n/testing.ts:162-206` implements `unmarkedIdentifiers` text-node traversal and boundary matching.
- `frontend/web/src/i18n/testing.test.ts:6-47` and `:73-102` test positive violation reporting and boundary punctuation handling.
- `frontend/web/src/domain/testing.ts:15-37` collects fixture identifiers across hosts, MACs, IPs, serials, and sites.
- `frontend/web/src/FleetView.locale.test.ts:330-375` asserts `unmarkedIdentifiers` across views and open switcher triggers.
- `frontend/web/src/ui/tooltip/UiTooltip.vue:92-103` marks Reka's hidden `role="tooltip"` element with `translate="no"`.
- `node_modules/reka-ui/dist/Tooltip/TooltipContentImpl.js:87` and `:134-140` show `VisuallyHidden` rendering flat `ariaLabel` text outside the slot tree.
- Commit `b50e84f9` introduces `unmarkedIdentifiers`.
- Commit `4f789f9c` fixes boundary punctuation and adds positive test cases.
- Commit `93e0c8b0` marks the hidden tooltip text node.

## What this does not cover

- Static HTML attributes (such as `aria-label`, `title`, or `placeholder`), which are checked by SFC template compiler AST scans (`frontend/web/src/i18n/testing.ts:50-104`).
- Translations performed by external proxies or browser extensions before client-side hydration.
