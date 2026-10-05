---
title: Clone Handler Data Where It Enters, Before Vue Reactive State Holds It
date: 2026-10-05
last_verified: 2026-10-05
category: architecture-patterns
module: frontend/web/src/ai
problem_type: architecture_pattern
component: web-console
severity: medium
applies_when:
  - "Calling structuredClone on a value that a Vue component received as a prop or read from a ref or reactive object in frontend/web/."
  - "Deciding where a value from an AI handler, a window API, or another caller outside the console becomes data the console owns."
  - "Reaching for toRaw in a component to make a clone, a comparison, or a validator accept reactive data."
related_components: [testing]
tags: [vue, reactivity, structured-clone, proxy, to-raw, ai-contract, validation]
---

# Clone Handler Data Where It Enters, Before Vue Reactive State Holds It

## Situation

An AI handler yields an answer whose `ui` field is a component tree. The
console must hold its own copy, so that a later change by the handler cannot
alter what the renderer validated. The accepted record places that copy in
the registry
(`docs/architecture/2026-09-28-web-component-contract-direction.md`, the
2026-10-05 amendment). This solution explains why the copy cannot sit in the
component instead.

## What is true

`structuredClone` refuses a Proxy. The HTML Standard's
StructuredSerializeInternal throws `DataCloneError` for an exotic object and
names "a proxy object" as its example
(https://html.spec.whatwg.org/multipage/structured-data.html).

A value stored in a `ref` or read through a reactive object is a Proxy.
`UiAiResult` keeps each snapshot that way:

```ts
// frontend/web/src/ui/ai/UiAiResult.vue:52
const internalResult = ref<AiResult | null>(null)
```

So a clone taken inside the renderer throws for every tree that arrives
through a run. With the clone in `validateAiUiTree` and no unwrapping, six
mount tests in `frontend/web/src/ui/ai/UiAiRender.test.ts` failed, the plain
"renders a validated component tree and nested card children" among them.

`toRaw` does not repair this. Three defects followed from adding it
(`534bf4ef`), each visible in Vue 3.5.43's source:

```js
// @vue/reactivity/dist/reactivity.esm-bundler.js:1488
function toRaw(observed) {
  const raw = observed && observed["__v_raw"];
  return raw ? toRaw(raw) : observed;
}
```

- It unwraps the object it is given and no nested value. A plain array that
  holds a `reactive` props object still reaches the clone as a Proxy.
- A computed that reads the raw tree registers no nested dependency, so an
  in-place change to the tree no longer re-renders.
- It follows a `__v_raw` property on any object, a Proxy or not. The
  renderer then validates the value that property names.

## How to apply

Take the clone where the outside value enters, before any reactive state
holds it. Let components read the clone through Vue's Proxy like any other
state.

```ts
// frontend/web/src/ai/registry.ts:118
let ui: unknown
try {
  ui = structuredClone(validated.ui)
} catch {
  ui = null
}
return { ...validated, ui }
```

The renderer then validates what it is given, with no clone and no `toRaw`:

```ts
// frontend/web/src/ui/ai/UiAiRender.vue:48
const validatedTree = computed<AiUiNode[] | null>(() => {
  try {
    return validateAiUiTree(props.tree)
  } catch {
    return null
  }
})
```

Reading through the Proxy is what keeps the computed reactive.

## Evidence

- `frontend/web/src/ai/registry.test.ts:503`, "clones an answer ui before
  yielding from an async iterator": the handler changes its tree after the
  yield and the snapshot keeps the earlier text.
- `frontend/web/src/ai/registry.test.ts:616`, "turns %s into a tree error
  without halting the run": a function, a Proxy, and an accessor that throws
  each become `ui: null`.
- `frontend/web/src/ui/ai/UiAiRender.test.ts:217`, "renders reactive trees
  and follows in-place changes": a nested `reactive` props object renders,
  and an in-place edit changes the rendered text. Adding a clone to the
  renderer fails this test.
- `frontend/web/src/ui/ai/UiAiRender.test.ts:238`, "validates the provided
  tree instead of its __v_raw property".

Observed on Node v22.14.0 (2026-10-05, macOS): `structuredClone` throws
`DataCloneError` for a Proxy of an array and of an object, reads an accessor
once, drops non-enumerable and symbol-keyed properties, and keeps an empty
array slot empty.

## What this does not cover

- A browser engine's `structuredClone`. The tests run on Node.
- An accessor or a Proxy on the handler's own answer object. The record
  places that outside the contract.
- A value passed to a component as a prop by a caller in this repository,
  which is not cloned.
