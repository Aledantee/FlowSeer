# Vue component authoring

Rules the Vue compiler and lint do not catch. Sources: vuejs-ai/skills
(`vue-best-practices`, `vue-debug-guides`, MIT,
https://github.com/vuejs-ai/skills), antfu/skills `vue` (MIT,
https://github.com/antfu/skills), and this repository's history.

## Data flow

- Props are read-only. To change a value, emit an event, use
  `defineModel`, or keep a local copy.
- Declare every emitted event in `defineEmits`. An undeclared `click`
  falls through `$attrs` to the root element and fires twice.
- For an object or array `defineModel`, assign a new value:
  `model.value = { ...model.value, x }`. Mutating it in place never emits.
- Don't give `defineModel` a default. A default makes the parent and the
  child disagree when the parent passes nothing.
- Type slots with `defineSlots`. Use `generic="T"` for tables and lists
  typed by row.

## Attributes

- When the root is not the interactive element, set
  `defineOptions({ inheritAttrs: false })` and bind `$attrs` onto the real
  control. `id`, `aria-*`, and listeners then land where assistive
  technology and tests expect them.
- `useAttrs()` is not reactive, so never `watch` it.

## Reactivity

- Use `shallowRef` for primitives and for opaque handles such as chart
  instances, observers, and animations.
- Derive values with `computed`. Never assign derived state inside a
  watcher, and never destructure a `reactive`.
- Keep filtering and sorting out of templates. Compute them once.
- An async watcher cancels the previous request with `onWatcherCleanup`
  and an `AbortController`.
- A token or generation counter that discards late answers must also
  cover unmount. `UiAiSummary.vue` is the landed pattern.

## Composables

- A composable accepts `MaybeRefOrGetter` inputs and reads them with
  `toValue()`.
- Use an options object once there are two or more optional parameters.
- Return readonly state plus explicit actions.
- A composable that adds listeners, observers, or timers removes them in
  `onScopeDispose`, not only in `onUnmounted`. It can run in a scope that
  is not a component.

## Compound components

- Parts of a compound component (table, menu, command, breadcrumb) share
  one `InjectionKey` context provided by the root part, the way Reka
  structures its own parts.
- A part used outside its root throws a clear error in development.
- Don't pass the context through props.

## Performance

- In hot lists, don't wrap each cell in a component. 100 rows × 3 levels
  is 300 instances.
- Past about 50 rows, virtualize or paginate.
- No layout reads (`getBoundingClientRect`, `offsetHeight`) during render.
  Read in `onMounted`, in a `ResizeObserver` callback, or around
  `nextTick`, and batch reads before writes.

## Input

- `v-model` does not update during IME composition. Live search and
  character counters bind `@input` so composed characters count.
