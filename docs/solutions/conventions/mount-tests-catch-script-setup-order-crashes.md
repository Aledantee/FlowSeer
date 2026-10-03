---
title: Component Mount Tests Catch Script Setup Order Crashes That Domain Tests Miss
date: 2026-09-27
last_verified: 2026-09-27
category: conventions
module: frontend/web
problem_type: bug
component: web-console
severity: high
symptoms:
  - "The web console renders a blank screen with ReferenceError: Cannot access '<identifier>' before initialization on page load."
  - "Domain unit tests (src/domain/*.test.ts) pass completely while the application crashes in the browser."
  - "vue-tsc --noEmit and ESLint report no errors despite the runtime initialization crash."
root_cause: "In Vue <script setup>, top-level statements run sequentially as the component setup() function. A watch() or computed getter that reads a ref declared further down in the file accesses a block-scoped binding before initialization, triggering JavaScript's Temporal Dead Zone (TDZ). Because domain tests only run isolated functions, setup crashes are caught only by mounting the view."
resolution_type: code_fix
applies_when:
  - "A Vue 3 component crashes on mount with ReferenceError: Cannot access '<identifier>' before initialization while unit tests pass."
  - "Writing or reviewing <script setup> blocks where watchers or computed properties read local reactive refs."
  - "Deciding test coverage between isolated domain unit tests and component mount tests in frontend/web/."
related_components: [web-console, testing]
tags: [vue, script-setup, tdz, testing, vitest, mount-tests]
---

# Component mount tests catch script setup order crashes that domain tests miss

## The situation

In `frontend/web/src/FleetView.vue`, business logic was extracted into domain helpers
under `src/domain/fleet.ts` and `src/domain/overview.ts`. Unit tests in
`src/domain/fleet.test.ts` and `src/domain/overview.test.ts` passed completely. Both
`vue-tsc --noEmit` and `eslint .` passed without errors.

When loading the console, however, the page failed to render and produced a blank
screen. The browser console reported:

```text
ReferenceError: Cannot access 'move' before initialization
```

## Why static checks and unit tests missed it

Vue compiles `<script setup>` into the body of a component's `setup()` function.
Top-level statements run sequentially when Vue creates a component instance.

A watcher registered an immediate or setup-time check evaluating reactive state:

```ts
watch(
  () => message.value || move.value?.deviceId,
  (value) => { ... },
)
...
const move = ref<Move>()
```

In JavaScript, `const` declarations are block-scoped and remain uninitialized until
execution reaches their declaration (the Temporal Dead Zone). Evaluating `move` inside
the getter before reaching `const move = ref<Move>()` threw a `ReferenceError`.
Because the exception occurred inside `setup()`, component mounting halted completely.

Neither TypeScript nor `vue-tsc` flags identifier access inside closures that execute
before lexical declaration. ESLint default configs do not trace setup evaluation order.
Domain unit tests test helper functions outside the component, so they never execute
`<script setup>`.

## The rules

### 1. Order reactive state above watchers and hooks

Declare reactive primitives (`ref`, `reactive`), route composables, and props before any
watchers, lifecycle callbacks (`onMounted`), or helpers that reference them.

In `frontend/web/src/FleetView.vue:59-77`:

```ts
const notice = ref<HTMLElement>()
const route = useRoute()
const router = useRouter()
const fleet = ref(devices.map((device) => ({ ...device })))
...
const message = ref('')
const move = ref<Move>()
```

Only after state is declared do computed properties and watchers register
(`frontend/web/src/FleetView.vue:129-139`):

```ts
watch(
  () => message.value || move.value?.deviceId,
  (value) => {
    if (value)
      play(notice.value, {
        opacity: [0.6, 1],
        transform: ['translateY(-4px)', 'none'],
      })
  },
  { flush: 'post' },
)
```

### 2. Require component mount tests for views

Domain unit tests alone do not guarantee a view initializes. Every view must have a
mount test in Vitest that instantiates the component within its router context
(`frontend/web/src/FleetView.test.ts:68-98`):

```ts
async function mountAt(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/:view(dashboard|devices|sites|topology|components)',
        component: FleetView,
      },
    ],
  })
  await router.push(path)
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({ template: '<RouterView />' })
  app.use(router)
  app.mount(host)
  dispose = () => app.unmount()
  await router.isReady()
  await nextTick()
  return { host, router }
}
```

Instantiating the view executes `<script setup>` and surfaces declaration-order crashes
in the test suite.

## What this does not cover

Mount tests verify synchronous component setup and initial template rendering. They do
not test edge cases in domain calculation (which belong in domain unit tests) or live
browser rendering.
