# FlowSeer web

An independent Vue operations-console skeleton with local fixtures. Run it without
Go services, generated bindings, environment variables, or access to a device:

```sh
cd frontend/web
pnpm install --frozen-lockfile
pnpm dev
```

Open the URL Vite prints. Use Node 22.12 or newer and pnpm 11.25.0.
The font is bundled locally. The app makes no requests to external services.

## Try the UI

Choose **Aurora Hospitality** to see devices in its **Aurora Germany** sub-tenant.
Select **Berlin Mitte**, then search for `gateway`. Open the device and assign it
to **Hamburg Hafen**: it disappears from the Berlin scope because the assignment
replaces its previous site. This demo permits moves within the owning tenant.
Reloading restores fixtures; URL scope and filters survive reloads.

The Devices page supports search, status filters, name sorting, an attention view,
and a keyboard-accessible details dialog. Sites opens the inventory for a location.
Topology illustrates connections and opens the same device details. The live toggle
pauses simulated traffic updates; rows retain their order as values change.

## Structure

- `src/main.ts` owns startup and routes; `FleetView.vue` owns the demo workspace.
- `src/domain/fleet.ts` contains fixtures, tenant rollups, and site assignment rules.
  These are UI demo shapes, not protobuf message definitions.
- `src/components/` holds shared presentation elements.
- `src/style.css` defines the visual language and responsive layout.

Vue 3 Composition API, strict TypeScript, Vite, and Vue Router provide the shell.
The lockfile pins resolved dependencies. TypeScript stays on 6.0 because the
installed typescript-eslint version does not support TypeScript 7.
Prettier owns formatting; ESLint checks code and Vue semantics with the standard
Prettier compatibility configuration.

## Design direction

The [m3connect homepage](https://www.m3connect.de/) supplies coral `#FF451D`,
cyan `#5ECAD8`, and dark teal `#0A1E22`. Coral marks the primary assignment action;
cyan identifies navigation. Health uses separate labeled green, amber, and red
states so brand colors do not carry status meanings.

The restrained borders, contextual details panel, and typography take cues from
[Stripe's component guidance](https://docs.stripe.com/stripe-apps/components).
The layout prioritizes persistent tenant/site context, readable tables, and stable
rows. Controls use native semantics and visible keyboard focus. On small screens,
navigation collapses to labeled icons and the table scrolls horizontally.

## Boundaries and next decisions

This is a design preview. Tenant selection filters fixtures and does not enforce
authorization. Backend integration must authorize every tenant/site request and
validate assignment changes. There is no login, persistence, streaming transport,
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

Unit tests cover tenant descendants, combined filters, invalid scopes, attention
states, and replacement of a single site assignment. Browser checks cover the
interactive preview; production accessibility and fleet-scale performance remain
to be evaluated when those features are implemented.
