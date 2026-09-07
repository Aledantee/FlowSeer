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
Select **Berlin Mitte**, then search for `gateway`. Open the device and assign it
to **Hamburg Hafen**: it disappears from the Berlin scope because the assignment
replaces its previous site. This demo permits moves within the owning tenant.
Reloading restores fixtures; URL scope and filters survive reloads.

The Devices page supports search, status filters, name sorting, an attention view,
and a keyboard-accessible details dialog. Sites opens the inventory for a location.
Topology illustrates connections and opens the same device details. Traffic updates automatically; rows retain their order as values change.

A curved tab midway down the sidebar edge collapses navigation to icons on
desktop. Tenant icons occupy a fixed slot beside the name; missing or
failed images use a building icon. The collapsed rail centers the icon and keeps
the full tenant name in its accessible label and tooltip. Its width is 64px.
On small screens, the arrow sits beside the tenant selector and hides or reveals
the navigation links above the content; tenant context stays visible.

The sidebar, curved tab, and rounded inner corner share the top bar’s diagonal
ribbons and fine highlights, forming one continuous navigation frame.
The top bar blurs content scrolling beneath it. Both themes retain a tinted base
so controls remain readable when backdrop blur is unavailable.

The FlowSeer icon and name sit at the top right, next to theme and help controls.
The tenant name stays inline at the top of the sidebar. Multiple available tenants
use a themed popover with arrow-key navigation, type-ahead, and outside-click dismissal; one available tenant renders plain text with no selector.
The available list is supplied to `TenantSwitcher` independently of site filtering.

The icon-only theme switch at the top right crossfades and rotates between
sun and moon over 160 ms. It has an accessible state label and a tooltip; reduced
motion swaps the icons immediately. The theme follows the system preference until
a choice is saved in local browser storage. The navigation frame stays connected in both themes: neutral gray in light mode
and charcoal in dark mode. Help opens a keyboard-accessible dialog explaining
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
- `src/style.css` defines the visual language and responsive layout.
- `src/theme/language.css` defines shared typography, spacing, and control tokens.
- `src/ComponentsView.vue` exposes component previews, foundations, and local drafts.

Vue 3 Composition API, strict TypeScript, Vite, and Vue Router provide the shell.
The lockfile pins resolved dependencies. TypeScript stays on 6.0 because the
installed typescript-eslint version does not support TypeScript 7.
Prettier owns formatting; ESLint checks code and Vue semantics with the standard
Prettier compatibility configuration.

## Components workspace

Open `/components` from the sidebar. Try the button variants, inspect labeled
health states, and compare typography under **Foundations**. The page shares
buttons, status badges, and metric cards with the fleet views. Record a component
question and stage, then choose **Save draft** to retain it in this browser.
Drafts are personal local storage; unsaved edits are lost when leaving the page.

The [design language](design/language.md) documents spacing, control states, and
font research. Inter Variable is the interface font; DM Sans remains available
in the comparison specimen. m3connect uses GT Standard, which remains a brand
option with supplied licensed files.

## Design direction

The [palette guide](design/README.md) compares online tools and documents the full
brand-based color system. Open `/design/palette.html` on the dev server to review
all light/dark tones and download the CSS or JSON.

The [m3connect homepage](https://www.m3connect.de/) supplies coral `#FF451D` and
cyan `#5ECAD8`. Large surfaces use neutral charcoal in dark mode and a neutral gray canvas and softly lifted cards in light mode to reduce the amount of saturated color in the workspace. Coral marks
the primary assignment action; cyan identifies navigation. Health uses separate labeled green, amber, and red
states so brand colors do not carry status meanings.

The restrained borders, contextual details panel, and typography take cues from
[Stripe's component guidance](https://docs.stripe.com/stripe-apps/components).
The layout prioritizes persistent tenant/site context, readable tables, and stable
rows. Controls use native semantics and visible keyboard focus. On small screens,
the tenant selector and navigation move above the content. Phone screens use
compact device status cards instead of the desktop table.

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
Further quick actions need their own service contracts.

### Motion

The active navigation highlight uses a full-width translucent blurred fill with
no decorative outline. Keyboard focus indicators remain visible. Its accent sits
on the sidebar’s outer right edge in expanded and collapsed desktop navigation;
horizontal mobile navigation uses an underline. The highlight slides to the
selected page in 140 ms, vertically
on desktop and horizontally on mobile. Reduced motion selects it immediately.

Motion's `motion/mini` animates scope changes, sidebar resizing, details opening,
and action feedback in 100–160 ms with an ease-out curve. Hover feedback takes
90 ms. Closing details and dismissing notices are immediate so animation never
holds focus or delays the next action. Theme changes apply immediately.

Live values, table sorting, typing in search, and the decorative header glow do
not animate. There are no staggered rows, counting numbers, spring overshoots, or
looping effects. Reduced-motion preferences skip transitions, including when the
preference changes during a session. Resizing or unmounting cancels pending
animations and restores the previous inline styles so responsive CSS stays in
control. Motion lifecycle tests cover these cleanup paths and rapid replacement.

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

For self-hosting, `pnpm build` emits `dist/`. Configure the web server to return
`index.html` for application routes such as `/devices` and `/sites`. Serving assets
under a subpath requires setting Vite's base and the router history base together.

## Checks

```sh
pnpm typecheck
pnpm test
pnpm lint
pnpm format:check
pnpm build
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

Tenants may supply an `iconUrl`. When the inline name overflows its available
width, that icon replaces the shortened label. Full names remain in the tooltip
and dropdown. Missing or failed icons fall back to an ellipsis.

The account icon sits at the far right of the top bar. Clicking it opens
a popover containing logout; Escape or an outside click dismisses it. Logout
remains disabled until sign-in is connected.
