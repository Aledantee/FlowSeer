# FlowSeer web

An independent Vue operations-console skeleton with local fixtures. Run it without
Go services, generated bindings, environment variables, or access to a device:

```sh
cd frontend/web
pnpm install --frozen-lockfile
pnpm dev
```

Open the URL Vite prints. Use Node 22.12 or newer and pnpm 11.25.0.
The fonts are bundled locally. The app makes no requests to external services.

## Try the UI

Choose **Aurora Hospitality** to see devices in its **Aurora Germany** sub-tenant.
Select **Berlin Mitte**, then search for `gateway`. Open the device, expand
**Move to another site**, and move it to **Hamburg Hafen**: it disappears from
the Berlin scope once the move is observed, because the assignment replaces its
previous site. Until then a notice at the bottom of the page reads "Moving…"
and the device stays where it was; the confirmed notice names both sites and
offers **Undo**. Undoing reports **Move reverted** once the device is back
and keeps keyboard focus on the notice. Keyboard focus follows
the moved row wherever the current sort puts it. A tenant with a single site
shows no move section at all. This demo permits moves within the owning tenant.
Reloading restores fixtures; URL scope and filters survive reloads.
A link that names only a site gains that site's tenant, so the breadcrumb never
reads **All tenants** while one customer's site is in view, and the Devices
count in the sidebar follows the scope.

The app opens on the Dashboard, an overview of the current tenant and site
scope. It leads with **Needs attention**: each failing device with the reason
from its newest warning or critical event and how long ago that was, or when an
offline device last answered. Traffic sits beside it. With **All sites** selected it lists each site with a health bar,
clients, and traffic; choose a site row to focus the dashboard on that site,
which replaces the list with the site's devices grouped by role. The traffic
chart and event feed are synthetic fixtures in `src/domain/overview.ts`. The
chart's right edge is the live traffic total from the heading line. Hover the
chart, or focus it and use the arrow keys, to read hourly values.

Device details lead with why a device needs attention: its open warning and
critical events, when it last answered, and each path FlowSeer reaches it
through (an integration, whether it is reachable, and when that was checked).
Lifecycle sits on its own line because an operator owns it, while
reachability heals on its own. Open **hamburg-ap-01** or **cologne-ap-02** and
choose **Poll now**: the fixture answers after a moment, and an unreachable
device stays offline with only its check time refreshed. An offline device
also says whether the rest of its site is answering, which separates a device
fault from a site that has gone dark, links to that site's dashboard, and
offers **Copy escalation summary**: plain text with the device, its paths, its
open issues, and the site check, ready to paste into a ticket.

A tenant or site in the URL that does not resolve shows **Scope not found**
with a way back, never an empty scope reported as healthy. Offline devices show
"—" for traffic and clients rather than a measured zero, and **Clear search and
status** keeps the tenant and site.

The Devices page lists offline and degraded devices first, and sorts by status,
name, site, or **Last answered**, which shows how long ago each device last
answered a poll so that a device offline for minutes and one offline for days
look different. It supports search, a status filter (including **Needs
attention**), and a keyboard-accessible details dialog. The Dashboard carries its
totals (devices, clients, traffic, and the time of the last refresh) in the
heading line rather than in metric cards; other pages state the scope and
attention count there. Sites lists every site in scope worst first, with a health bar and a
line such as "1 offline", the newest open issue and its age (a warning on a device
that has since recovered is history, not an open issue), and the device count; a site's name opens its dashboard and **Devices** opens its
inventory.
Topology draws each site's gateway and core switch with its access points side
by side beneath them. Links are drawn dashed and a legend marks them as assumed until topology is
discovered. Only nodes that are not healthy carry a status badge, so a degraded
or offline device stands out; every node opens the same device details. Traffic updates automatically; rows retain their order as values change.

A curved tab midway down the sidebar edge collapses navigation to icons on
desktop. The collapsed rail centers the FlowSeer mark and is 64px wide.
On small screens, the arrow sits beside the FlowSeer brand and hides or reveals
the navigation links above the content.

The sidebar, curved tab, and rounded inner corner share the top bar’s diagonal
ribbons and fine highlights, forming one continuous navigation frame.
The top bar blurs content scrolling beneath it. Both themes retain a tinted base
so controls remain readable when backdrop blur is unavailable.

The FlowSeer icon and name sit at the top left of the sidebar. When multiple
tenants are available, the first breadcrumb is a tenant selector, followed by the
page and site scope. Both selectors use themed popovers with arrow-key
navigation, type-ahead, and outside-click dismissal. Changing tenants clears the
site scope. The available tenant list is independent of site filtering.

The icon-only theme switch at the top right crossfades and rotates between
sun and moon over 160 ms. It has an accessible state label and a tooltip. Under
reduced motion the icons crossfade without rotating. The theme follows the system preference until
a choice is saved in local browser storage. The navigation frame stays connected in both themes: neutral gray in light mode
and charcoal in dark mode. The language switch beside it is one button of the same size. It shows the
active language code, and its tooltip and accessible name offer the other language by its own name,
such as `Switch language to Deutsch`. Pressing it changes every view without a reload and announces
the change in a status region. Help opens a keyboard-accessible dialog explaining
scope, device lookup, and site assignment. The adjacent bug button
opens a report form and copies its summary, description, and page path for sharing.
It does not submit to a service or include tenant/site query parameters.

The content area owns vertical scrolling. The navigation frame stays outside that
scroll container, so reaching the top does not pull down the top bar. Overscroll
is contained in the content area.

## Structure

- `src/main.ts` owns startup and routes; `FleetView.vue` owns the demo workspace.
- `src/domain/fleet.ts` contains fixtures, tenant rollups, and site assignment rules.
  These are UI demo shapes, not protobuf message definitions.
- `src/components/` holds shared presentation elements.
- `src/i18n/` holds `createWebI18n`, English and German catalogs, and shared number formats.
- `src/ui/` holds design system components and headless primitives. `src/ui/app/UiAppRoot.vue` provides the top-level application wrapper (`ConfigProvider`, `TooltipProvider`, and `UiMotionConfig`) and passes the Composer locale to Reka.
- `src/style.css` defines the shell layout, connected chrome frame, and brand glow ribbons.
- `src/theme/tailwind.css` configures Tailwind v4 Preflight, base element normalizations, and `@theme` overlay keyframes (`--animate-overlay-in/out`, `--animate-dialog-in/out`, `--animate-fade-in/out`, `--animate-dialog-fade-in`).
- `src/theme/tokens.css` wires semantic tokens, typography scales, shadows, radii, and z-index tokens (`--z-raised`, `--z-sticky`, `--z-overlay`, `--z-toast`, `--z-skip-link`) into Tailwind theme directives.

Vue 3 Composition API, strict TypeScript, Vite, and Vue Router provide the shell.
The lockfile pins resolved dependencies. TypeScript stays on 6.0 because the
installed typescript-eslint version does not support TypeScript 7.
Prettier owns formatting; ESLint checks code and Vue semantics with the standard
Prettier compatibility configuration.

The [design language](design/language.md) documents spacing, control states, and
font research. Inter Variable is the interface font. m3connect uses GT Standard,
which remains a brand option with supplied licensed files.

## Design direction

The [palette guide](design/README.md) compares online tools and documents the full
brand-based color system. Open `/design/palette.html` on the dev server to review
all light/dark tones and download the CSS or JSON.

The [m3connect homepage](https://www.m3connect.de/) supplies coral `#FF451D` and
cyan `#5ECAD8`. Large surfaces use neutral charcoal in dark mode and a neutral gray canvas and softly lifted cards in light mode to reduce the amount of saturated color in the workspace. Coral marks
the primary action on a failing device; cyan identifies navigation. Health uses separate labeled green, amber, and red
states so brand colors do not carry status meanings.

The restrained borders, contextual details panel, and typography take cues from
[Stripe's component guidance](https://docs.stripe.com/stripe-apps/components).
The layout prioritizes persistent tenant/site context, readable tables, and stable
rows. Controls use native semantics and visible keyboard focus. On small screens,
the tenant selector and navigation move above the content. Phone screens use
compact device status cards instead of the desktop table.

## Design system

FlowSeer components and layouts consume semantic design tokens built on a 12-step OKLCH scale.
Tokens originate from `design/palette-source.json` (captured from Radix Custom Colors with anchors
coral `#FF451D` and cyan `#5ECAD8`, plus neutral gray scales) and compile into `src/theme/scales.css`
and `src/theme/semantic.css` via:

```sh
node --experimental-strip-types scripts/build-palette.ts
```

Semantic tokens (`--background`, `--foreground`, `--card`, `--primary`, `--accent`, etc.) replace
legacy custom properties (`--page`, `--text`, `--surface`, `--coral`, etc.), with light mode using
a comfortable `neutral-5` background canvas. Tailwind v4 Preflight is active in `src/theme/tailwind.css`
to normalize element baselines across browsers. Legacy view-level stylesheets (`src/dashboard.css`,
`src/theme/language.css`) are dissolved into `Ui*` primitives and Tailwind utilities, leaving
`src/style.css` as a dedicated layout stylesheet for the connected navigation frame and brand glow.
Token adherence is continuously enforced: `pnpm lint` runs stylelint to reject raw color, font-size,
and box-shadow literals outside `src/theme/scales.css`.

Foundation tokens and components are documented and visually audited in Storybook:

```sh
pnpm storybook
```

This starts the Storybook dev server on `http://127.0.0.1:6006` with theme switching (`data-theme="light"`
or `data-theme="dark"`), locale switching (`en` or `de`), accessibility auditing (`@storybook/addon-a11y`), and stories covering:

- **Colors**: renders every semantic token, its active theme step, and WCAG contrast audit against gated surfaces
- **Typography**: renders the type scale steps (`2xs` through `3xl`) across Inter, Mono, and tabular figures
- **Shape**: renders border radii, card elevation shadows, and spacing steps 1 to 8
- **Components**: design system components under `src/ui/` covering actions, inputs, feedback, and data presentation with CSF 3 stories, automated WCAG 2.1 AA checks via `axe-core`, and missing-key checks across English and German

To build the static Storybook bundle:

```sh
pnpm build-storybook
```

### Data display components

Data display components under `src/ui/` present tables, metrics, and progress:

- `UiTable` suite: composable table with `UiTableHeader`, `UiTableBody`, `UiTableRow`, `UiTableHead`, `UiTableCell`, and `UiTableEmpty`. Provides `dense` compact padding, `stickyHeader` pinning, controlled sorting with `aria-sort`, and selected row states.
- `UiEmptyState`: centered state indicator with `#icon`, title, description, and action buttons for empty filters or searches.
- `UiSkeleton`: placeholder shape with `text`, `circular`, and `rectangular` variants, respecting `prefers-reduced-motion`.
- `UiMeter`: single-metric resource gauge with automated threshold tones (`warning` at 75%, `critical` at 90%) and ARIA `role="meter"` attributes.
- `UiSegmentedMeter`: multi-segment status bar for health distributions, computing accessible summary descriptions and optional dot legends.
- `UiProgress`: determinate and indeterminate progress bars wrapping Reka UI with semantic status variants.
- `UiPagination`: page navigation wrapping Reka UI with first, previous, page number, ellipsis, and next controls.

### Overlay, navigation, and command components

Overlay, navigation, and command primitives under `src/ui/` wrap Reka UI headless components styled with semantic tokens:

- `UiDialog` & `UiAlertDialog`: modal overlays with accessible titles, descriptions, scrim backdrops, focus trapping, keyboard escape dismissal, and optional right-edge sheet layout (`side="right"`).
- `UiPopover`: floating popover anchored to triggers with configurable alignment and collision padding.
- `UiContextMenu` suite: contextual right-click and keyboard menus with roving focus and outside-click dismissal (`UiContextMenu`, `UiContextMenuTrigger`, `UiContextMenuContent`, `UiContextMenuItem`, `UiContextMenuSeparator`).
- `UiDropdownMenu` suite: dropdown action menus with nested submenus, roving focus, keyboard navigation, and separators (`UiDropdownMenuItem`, `UiDropdownMenuSeparator`).
- `UiTabs`: a single tab container that creates accessible triggers and panels from the `tabs` prop. Use `v-model` for controlled selection or `defaultValue` for initial selection; `trigger-${value}` and `${value}` slots replace a tab's label and panel content.
- `UiBreadcrumb` suite: hierarchical breadcrumb navigation (`UiBreadcrumbList`, `UiBreadcrumbItem`, `UiBreadcrumbLink`, `UiBreadcrumbPage`, `UiBreadcrumbSeparator`, `UiBreadcrumbEllipsis`) featuring responsive auto-collapsing of intermediate links into a dropdown menu on narrow viewports.
- `UiScrollArea` suite: custom-styled scroll containers wrapping Reka ScrollArea primitives, exposing underlying viewport element references for programmatic scrolling.
- `UiToast` suite: reactive notification toasts with variants (`default`, `success`, `warning`, `danger`), auto-dismissal, and `useToast` dispatch composable.
- `UiCombobox`: searchable select combobox with option grouping, avatar icons, keyboard roving focus, and custom trigger slots.
- `UiCommand` suite: command palette primitives (`UiCommand`, `UiCommandDialog`, `UiCommandInput`, `UiCommandList`, `UiCommandEmpty`, `UiCommandGroup`, `UiCommandItem`, `UiCommandSeparator`, `UiCommandShortcut`) supporting modal presentation and custom filtering.

```vue
<script setup lang="ts">
import { UiTabs } from './ui'

const tabs = [
  { value: 'overview', label: 'Overview', content: 'Current fleet status' },
  { value: 'alerts', label: 'Alerts', content: 'Open alerts' },
]
</script>

<template>
  <UiTabs default-value="overview" :tabs="tabs" />
</template>
```

Application switchers (`ScopeSwitcher`, `TenantSwitcher`), menus (`AccountMenu`), command palettes (`GlobalSearch`), dialogs (`HelpButton`, `ReportBugButton`), and scrollers (`UiScrollArea`) run on these Reka primitives, replacing legacy native dialogs, manual positioning math, and custom scrollers.

Overlays stack on the z-index tokens in `src/theme/tokens.css`: `--z-overlay` (50) for dialogs, popovers, and menus, `--z-toast` (60) for the toast viewport, and `--z-sticky` (10) for the top bar and table headers. Their entrances and exits are CSS keyframes, declared in `src/theme/tailwind.css` as `--animate-overlay-in/out` for popovers, menus, and select lists, `--animate-dialog-in/out` for dialog content, and `--animate-fade-in/out` for scrims and toasts. Each wrapper applies them on `data-[state=open]` and `data-[state=closed]` of its content element. The keyframes matter because Reka's `Presence` keeps a closing node mounted until its `animationend` and ignores CSS transitions, so an exit written as a transition never plays. Under reduced motion every pair switches to a fade of the same duration. Dialog entry uses `--animate-dialog-fade-in`, which runs the fade at the dialog's 160 ms. Tooltips and combobox lists do not animate, and the command dialog content has an exit fade only. `UiAppRoot` (`src/ui/app/UiAppRoot.vue`) mounts `ConfigProvider`, `TooltipProvider`, and `UiMotionConfig` once for the whole view tree. It passes the active Composer locale to Reka.

### Chart color tokens and accessibility

Data visualizations consume 6 categorical tokens (`--chart-1` through `--chart-6`), mapped across cyan, coral, violet, green, amber, and blue. Each token maintains at least 3:1 contrast against card and panel surfaces for non-text graphical elements.

Color alone never conveys information. Line and area charts include visible text labels, direct series legends, and a keyboard-navigable fallback `<table>` for screen readers.

### Mobile direction

Mobile prioritizes current status and quick actions for an on-site engineer or
support person checking a site at a glance. Feature parity with desktop is not
required; advanced features may be hidden to keep the mobile workflow focused.
Keep tenant and site context visible so quick actions target the intended device.

For example, an engineer at Campus Aachen should be able to check site health,
find a device needing attention, and open its status and relevant quick actions.
Favor concise status summaries and touch-friendly controls. Dense table tooling,
full topology exploration, bulk configuration, and configurable OLAP dashboards
can remain desktop workflows. The skeleton shows device count and health first on phones, hides topology
navigation and secondary traffic summaries, and offers one-tap device details.
The theme, language, help, and account controls join the brand row, the bug report
button is left to desktop, and the Dashboard keeps its site list with a health
bar per site.
Further quick actions need their own service contracts.

### Motion

The active navigation highlight uses a full-width translucent blurred fill with
no decorative outline. Keyboard focus indicators remain visible. Its accent sits
on the sidebar’s outer right edge in expanded and collapsed desktop navigation;
horizontal mobile navigation uses an underline. The highlight slides to the
selected page in 140 ms, vertically
on desktop and horizontally on mobile. Reduced motion selects it immediately.

Motion-v provides `UiMotion` for layout and positional animation. The
`useMotionFeedback` composable in
`frontend/web/src/ui/motion/useMotionFeedback.ts` handles local feedback such as
scope changes, details opening, and notices. It accepts typed
`[from, to]` pairs for `opacity`, `x`, `y`, `rotate`, and `scale`. Supplied
movement keys compile into one ordered native transform effect in
`translateX`, `translateY`, `rotate`, and `scale` order. Opacity remains a
separate native effect. Under reduced motion, movement is filtered while fades
remain. Each play owns only the CSS properties and native effects it supplied.
Completion, cancellation, resize, preference or configuration changes, and
unmount restore the previous inline values. Views import both motion surfaces
through the `src/ui` barrel. `UiAppRoot` mounts the one app-wide
`UiMotionConfig` with `reducedMotion="user"`, so the browser preference applies
to every motion surface.

Motion uses an ease-out curve. Layout and positional changes take 100–160 ms,
and hover feedback takes 90 ms. Closing details and dismissing notices are
immediate so animation never holds focus or delays the next action. Theme icons
crossfade over 160 ms without rotation when reduced motion is enabled.

Live values, table sorting, typing in search, and the decorative header glow do
not animate. There are no staggered rows, counting numbers, spring overshoots, or
looping effects. Reduced-motion preferences skip transitions, including when the
preference changes during a session. Resizing or unmounting cancels pending
animations and restores the previous inline styles so responsive CSS stays in
control. Motion lifecycle tests cover these cleanup paths and rapid replacement.

### Internationalization

FlowSeer uses vue-i18n in Composition mode with English and German catalogs:

- `src/i18n/index.ts` exports `createWebI18n(locale = 'en')` with `fallbackLocale: 'en'`, `en.json` and `de.json` catalogs, and decimal, integer, and percent number formats. Each call returns a fresh plugin instance because vue-i18n binds its lifecycle to the app: `install` wraps `app.unmount` to call `i18n.dispose()`, so sharing an instance disposes it when the first app unmounts.
- `src/main.ts` installs one plugin instance on the Vue application before mount, created with the locale `initialLocale()` resolves. Storybook's `setup` callback registers a fresh instance per app, and tests mount components with their own instance.
- `src/i18n/locale.ts` picks the starting locale. A saved choice wins, then the first entry of `navigator.languages` whose primary subtag is `en` or `de` without regard to case, then `en`. A tag such as `den` does not match, since only its first letters equal `de`. The choice lives in `localStorage` under `flowseer.locale`, and blocked storage reads as nothing saved. `bindDocumentLang` keeps `<html lang>` equal to the Composer locale, including after a switch.
- `src/components/LocaleSwitcher.vue` sets the Composer locale and saves it. When the browser refuses to save, the locale still changes and the status region says the choice was not saved. Storybook keeps its own locale toolbar and does not read `flowseer.locale`.
- Locale state lives in the global Composer. `UiAppRoot` reads the active Composer locale and passes it to Reka's `ConfigProvider`. That keeps translated template text and headless primitives synchronized.
- In Storybook, the `withLocale` decorator watches `reactive(context.globals).locale` and updates the active Composer. The Storybook toolbar provides English and German options without per-story provider wrappers.
- Component defaults belong to `ui.<owner>.<suffix>` in `src/i18n/locales/en.json` and `de.json`. Identifiers, keys, and slot content remain caller data, while the owning component renders localized display text.
- Optional text props resolve reactively as `props.text ?? t(key)` in computed properties or templates. Hoisted `withDefaults` defaults never call `t`. Calling `t` inside `withDefaults` causes scope errors and freezes translations across locale switches. Structural defaults stay in `withDefaults`.

`src/ui/command/UiCommandEmpty.vue` demonstrates an optional text override with a localized catalog default:

```vue
<script setup lang="ts">
import { computed } from 'vue'
import { ComboboxEmpty } from 'reka-ui'
import { useI18n } from 'vue-i18n'

export interface UiCommandEmptyProps {
  text?: string
}

const props = defineProps<UiCommandEmptyProps>()

const { t } = useI18n({ useScope: 'global' })
const resolvedText = computed(() => props.text ?? t('ui.commandEmpty.text'))
</script>

<template>
  <ComboboxEmpty class="py-6 text-center text-sm text-muted-foreground">
    <slot>{{ resolvedText }}</slot>
  </ComboboxEmpty>
</template>
```

#### View messages

Views and the components under `src/components/` keep their strings in `view.<owner>.<key>` messages in both catalogs. The owner is the file's area: `fleet`, `dock`, `search`, `workspace`, `devices`, `sites`, `dashboard`, `device`, `clients`, `devicePorts`, `topology`, `topologyInspector`, and the like. Words that two owners share live under `view.common`, so a page name, a health word, or a unit reads the same everywhere. Fixture data under `src/domain/` stays untranslated because it stands in for service data. A value typed as a union of literals (`Health`, `PortStatus`, `Band`) is an identifier, and its display text is a message.

Two composables keep formatting out of the views:

- `useFormat()` in `src/i18n/format.ts` formats values for the active locale. `quantity` and `rate` print a number and its unit, `speed` prints `10G`, `counted` picks a plural form, `ago` and `clock` print relative and clock times, and `facts` joins parts with the separator message.
- `useLabels()` in `src/i18n/labels.ts` names identifiers and page ids, and builds a rollup's health line.

Unit labels are messages. `Intl.NumberFormat` prints `Mb/s` for megabits per second in every locale, while the catalogs read `Mbit/s`. Relative times come from `Intl.RelativeTimeFormat` and clock times from `d()`, so both follow the locale without a message.

`src/components/DevicePorts.vue` shows the pieces together. `n()` formats each count, `quantity` joins the PoE power with its unit, and `facts` drops the PoE part when no port has power:

```ts
const summary = computed(() =>
  format.facts([
    t('view.devicePorts.summary', {
      active: n(active.value.length, 'integer'),
      total: n(props.ports.length, 'integer'),
    }),
    props.ports.some((port) => port.poe) &&
      t('view.devicePorts.poe', { power: format.quantity(power.value, 'w') }),
  ]),
)
```

The port count is a plural message that `counted` selects by the raw count, formatted by `n()` for display:

```vue
    <ol
      class="port-map"
      :aria-label="format.counted('view.devicePorts.ports', ports.length)"
    >
```

The catalogs hold the matching messages in `view.devicePorts`:

```json
"devicePorts": {
  "poe": "{power} PoE",
  "ports": "{count} port | {count} ports",
  "summary": "{active} of {total} up"
},
```

```json
"devicePorts": {
  "poe": "{power} PoE",
  "ports": "{count} Port | {count} Ports",
  "summary": "{active} von {total} verbunden"
},
```

Names of devices, clients, sites, tenants, addresses, serials, port names, and models carry `translate="no"`, so a page translator leaves them alone:

```vue
        <button
          translate="no"
          class="port-name font-mono justify-self-start p-0 border-0 bg-transparent text-foreground text-left hover:text-accent-foreground hover:underline cursor-pointer"
          @click="emit('port', port.name)"
        >
          {{ port.name }}
```

A message read into a top-level `const` keeps the locale the module was set up in, because `t()` runs once. A table of labels is a `computed`, or it moves into the template, and every view test switches the locale on a mounted app to catch the difference. Do not build a sentence from fragments, since word order differs between English and German. One message carries named values, and `I18nT` with `scope="global"` carries inline markup.

`src/i18n/templates.test.ts` reads every `.vue` file directly under `src/`, every one under `src/components/` and `src/navigation/`, and the `Ui*` files under `src/ui/`. It fails with the file and line for a literal text node and for a static `aria-label`, `title`, `placeholder`, or similar attribute. It cannot see a string built in `<script>` or inside a bound expression, so a reviewer reads those. `src/FleetView.locale.test.ts` mounts the dashboard, devices, a device, clients, and sites in both locales, switches between them on one mount, fails on any `vue-i18n` warning, and verifies with `unmarkedIdentifiers` that fixture identifiers carry `translate="no"` across rendered views, dock states, and switchers. The property inspects text nodes only and ignores attributes such as `aria-label` or `title`. Tooltips sit inside the property: `UiTooltip` exposes `label` and `hint` slots so callers can mark identifier spans with `translate="no"` while leaving message text unmarked.

## AI targets

The console can expose meaningful instances to an agent or model without an AI
backend. A component registers the element that stands for it through an
optional `ai` prop. The prop takes the resolved `AiTarget` that `aiTarget()`
returns (`src/ai/target.ts`), so the caller owns the identity and the component
never invents one. A registered element is listed while it is mounted and
removed when it unmounts, so a row that leaves a filter stops being
addressable. An absent `ai` prop registers nothing. Target IDs are qualified
by physical pane slot (`a`, `b`, or `standalone` for a Storybook story or a test), keeping IDs stable across pane
swaps even when primary and secondary roles change. Responsive components that
mount simultaneous mobile and desktop layouts in CSS register distinct
`mobile` and `desktop` segments, for example `a:devices:device:desktop:d1`.
Only mounted elements in the active responsive segment that are not hidden by
the `hidden` attribute or CSS (`display: none`, `visibility: hidden`, or
`visibility: collapse` on the target or an ancestor) are listed or selectable.
Offscreen elements remain addressable so `highlight()` can scroll them into view.

### Anchor selection

Each component registers its meaningful element and nothing around it. A
plain control registers the control itself, a select registers its trigger,
and a popup registers its content while it is mounted. Layout-only components
register nothing. `useAiTarget` in `src/ui/ai/useAiTarget.ts` follows the prop
and the element, using the registry injected through `src/ui/ai/context.ts`
and the console-wide registry when none is provided. HTML and SVG elements
are both valid anchors.

A kit component takes the prop directly, so a table row is a `UiTableRow`
with `:ai`. Native markup that no component owns, such as a list item, a
section, or a link, uses `UiAiTarget` (`src/ui/ai/UiAiTarget.vue`). With `as`
it renders that tag. With `asChild` it merges into the one child it is given.
Neither adds a layout element. Both shapes appear in `src/DashboardView.vue`:

```vue
<UiTableRow
  v-for="rollup in rollups"
  :key="rollup.site.id"
  :ai="siteTarget(rollup)"
>
  <UiTableCell>{{ rollup.site.name }}</UiTableCell>
</UiTableRow>

<UiAiTarget
  v-for="device in attention"
  :key="device.id"
  as="li"
  :ai="attentionTarget(device)"
>
  <AppLink :to="deviceTo(device.id)">{{ device.name }}</AppLink>
</UiAiTarget>
```

The registry writes `data-ai-selected` on the highlighted element, including
a manual `registry.register()` call, and one rule in `src/theme/ai.css`
draws the outline with `--ring`.

### Origin acknowledgement

A value an agent changed takes `aiOrigin`, the originating request without its
`signal` (`AiOriginRequest` in `src/ui/ai/context.ts`), and emits
`aiOriginAcknowledged` with its `requestId`. The component sets
`data-ai-origin="agent"` on the value until the user interacts with it
(pointerdown, keydown, input, or change). Hover and programmatic
updates leave it, and so does selection. The origin does not depend on `ai`. After an
acknowledgement the component ignores that `requestId` while mounted, so the
caller clears its own state on the event, and a new `requestId` marks the
value again. Portalled content, such as a select's list, forwards its
interactions to the same acknowledgement. `UiAiLabel` explains the request
beside the value, and the caller removes it on the same event. This field and
label pair is the `AgentChanged` story in `src/ui/form/UiField.stories.ts`:

```vue
<UiField label="Device Name">
  <UiInput
    v-model="name"
    :ai-origin="changed"
    @ai-origin-acknowledged="changed = undefined"
  />
  <UiAiLabel v-if="changed" :request="changed" />
</UiField>
```

Text controls keep the browser's native context menu, so the story passes no
`ai` there. Pass `:ai="nameTarget"` where the control should also be an
agent target.

Earlier prototypes explored hover triggers and a floating button that followed
keyboard focus. Both were removed on purpose: hover triggers fired by accident
during pointer travel, and focus-following buttons created visual clutter and
nested interactive elements. AI interactions now center on three surfaces:

1. **Context menu (`UiAiContextLayer`):** Right-clicking an element or pressing
   Shift+F10 (or the `ContextMenu` key) on a focused item opens a menu of
   labelled action verbs ("Why is this offline?", "Summarize this site").
   Triggering a verb opens an answer-first popover anchored to that element.
2. **Summary button (`UiAiSummary`):** The dashboard and device views retain a
   dedicated summary placement, rendering structured results directly in the
   content flow.
3. **Assistant panel (`UiAiAssistant`):** A docked right column on wide screens,
   or a full-height right sheet (`UiDialog` with `side="right"`) on narrow
   viewports, opened via the top-bar button or Mod+I (`shortcuts.assistant`).
   "Continue in assistant" from an inline popover transfers targets and turns
   into the panel without losing context.

### Interaction contract

`window.flowseerAi` in `src/ai/registry.ts` is the inspectable browser API:

```ts
window.flowseerAi.listTargets() // visible targets, sorted by id
window.flowseerAi.highlight('a:devices:device:desktop:d1') // scrolls into view; true when visible
window.flowseerAi.clearHighlight()

const unsubscribeRequest = window.flowseerAi.onRequest(
  async function* (request) {
    // request: { requestId, action, targets, prompt?, history, signal }
    yield {
      type: 'summary',
      headline: 'Core switch uplink experiencing frame loss',
      tone: 'warning',
      findings: [
        {
          severity: 'warning',
          title: 'CRC error rate elevated',
          detail:
            'Port ge-0/0/1 reports 2.4% FCS error rate over the last 15 minutes.',
          refs: [
            { kind: 'device', id: 'cologne-core-01', label: 'cologne-core-01' },
          ],
        },
      ],
      cause: {
        text: 'Marginal optical transceiver on uplink port.',
        confidence: 'medium',
        refs: [
          { kind: 'device', id: 'cologne-core-01', label: 'cologne-core-01' },
        ],
      },
      metrics: [{ label: 'FCS errors', value: '2.4%', tone: 'warning' }],
      next: [{ label: 'Poll switch optical diagnostic levels' }],
      sources: [
        { kind: 'device', id: 'cologne-core-01', label: 'cologne-core-01' },
      ],
    }
  },
)

const unsubscribeFeedback = window.flowseerAi.onFeedback(
  ({ requestId, rating }) => {
    // Record operator feedback
  },
)

unsubscribeRequest()
unsubscribeFeedback()
```

`highlight(id)` selects and scrolls the exact mounted instance into view and
returns `true`. Unknown or CSS-hidden IDs return `false`. `onRequest` installs
the asynchronous handler, and the returned function removes it. With no handler
installed, Ask and summary report **AI is unavailable** rather than inventing an
answer. The application has no model provider yet, so `main.ts` installs
`createMockAiHandler` (`src/ai/mock.ts`): it answers summaries from the target's
context in two snapshots with a pending delay, and answers Ask with a placeholder.
A handler that rejects produces an error state.

### Typed results

Results render using typed objects defined in `src/ai/types.ts` rather than
raw Markdown or HTML strings. This avoids HTML-injection risks and allows native
design-system components (`UiStatusBadge`, `UiAiEntityChip`, `UiAiLabel`) to
present structured insights:

- `AiSummary`: contains a headline, overall tone (`ok`, `warning`, `critical`,
  `unknown`), structured findings with individual severities and entity
  references, an optional likely cause with confidence rating, optional impact,
  key metrics with status tones, recommended next steps, and entity sources.
- `AiAnswer`: conversational or question responses containing prose text,
  associated entity references, and an optional nested `AiSummary`.

An example structured `AiSummary` payload:

```json
{
  "type": "summary",
  "headline": "Core switch uplink experiencing frame loss",
  "tone": "warning",
  "findings": [
    {
      "severity": "warning",
      "title": "CRC error rate elevated",
      "detail": "Port ge-0/0/1 reports 2.4% FCS error rate over the last 15 minutes.",
      "refs": [
        {
          "kind": "device",
          "id": "cologne-core-01",
          "label": "cologne-core-01"
        }
      ]
    }
  ],
  "cause": {
    "text": "Marginal optical transceiver on uplink port.",
    "confidence": "medium",
    "refs": [
      { "kind": "device", "id": "cologne-core-01", "label": "cologne-core-01" }
    ]
  },
  "impact": {
    "text": "Downstream access points report intermittent packet retransmissions.",
    "refs": [
      { "kind": "device", "id": "cologne-core-01", "label": "cologne-core-01" }
    ]
  },
  "metrics": [{ "label": "FCS errors", "value": "2.4%", "tone": "warning" }],
  "next": [{ "label": "Poll switch optical diagnostic levels" }],
  "sources": [
    { "kind": "device", "id": "cologne-core-01", "label": "cologne-core-01" }
  ]
}
```

### Snapshot delivery and cancellation

A handler returns `Promise<AiResult>` or an `AsyncIterable<AiResult>`. Streaming
delivers progressive snapshots where each yielded object is a complete result
state so far, eliminating fragile delta-patching protocols. The active request
carries a standard `AbortSignal`. Activating Stop triggers `abort()`, halting
iteration and freezing the current rendered snapshot. Every received snapshot
must satisfy the `isAiResult` validator. Malformed payloads immediately halt the
run and display an error.

### Bound and unbound runs

Requests initiate in either bound or unbound mode:

- **Bound runs (`bound: true`):** Used by contextual menus and inline summaries.
  The run monitors the registered DOM element. If that element unmounts or is
  replaced by a different DOM node, the request aborts with `AiStaleError` to
  prevent outdated results from settling on stale targets. Updating context on
  the same element (such as periodically refreshing traffic counters) preserves
  the run.
- **Unbound runs (`bound: false`):** Used by the assistant panel. The panel
  snapshots target context into chips when context is added. The session
  remains active across page transitions and filter changes even after targets
  unmount.

### Context menu exclusions

`UiAiContextLayer` intercepts right-click events in the capture phase to find the
closest registered AI target. To avoid blocking expected browser controls, the
listener halts propagation without calling `preventDefault()` when:

- The click originates inside `a[href]`, `input`, `textarea`, `select`, or
  `[contenteditable]` elements.
- The document has an active, non-empty text selection.
- No registered target is found under the pointer.
- The closest target has `kind: 'view'` (such as background canvas clicks on
  `DeviceView` or `TopologyGraph`).

In all four cases, the native browser context menu appears normally.

## Boundaries and next decisions

The UI uses product-facing copy and omits decorative placeholder text and demo
badges. This is still a design preview backed by local fixtures. Tenant selection filters fixtures and does not enforce
authorization. Backend integration must authorize every tenant/site request and
validate assignment changes. The logout icon beside the operator name is disabled until authentication is
connected. There is no login, persistence, streaming transport,
or production telemetry. Traffic is synthetic; aggregate device traffic may count
traffic at multiple network hops. Topology links are illustrative.

The 16-row native table establishes density and interactions. It is not a
large-fleet performance benchmark. Before adopting a grid, test real update rates,
virtualization, selection stability, keyboard access, and licensing against a
representative dataset. Keep incoming stream batching separate from rendered row
order. A future topology renderer and configurable OLAP dashboard can use the
existing scoped routes, but need their own data/query contracts and load tests.

For self-hosting, `pnpm build` emits `dist/app/`. Configure the web server to return
`index.html` for application routes such as `/devices` and `/sites`. Serving assets
under a subpath requires setting Vite's base and the router history base together.

## Checks

```sh
pnpm typecheck
pnpm test
pnpm lint
pnpm format:check
pnpm build
pnpm build-storybook
```

Palette tests enforce 4.5:1 for normal text, 7:1 for primary text, and 3:1 for
control outlines, focus indicators, and topology links against their backgrounds.
These checks use the contrast calculation from
[WCAG text contrast](https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum.html)
and [non-text contrast](https://www.w3.org/WAI/WCAG22/Understanding/non-text-contrast.html).
Decorative separators stay quieter; disabled controls are excluded. Passing these
checks does not establish full WCAG conformance or guarantee visual comfort.

Unit tests cover tenant descendants, combined filters, invalid scopes, attention
states, and replacement of a single site assignment. Browser checks cover the
interactive preview; production accessibility and fleet-scale performance remain
to be evaluated when those features are implemented.

The account icon sits at the far right of the top bar. Clicking it opens
a popover containing logout; Escape or an outside click dismisses it. Logout
remains disabled until sign-in is connected.
