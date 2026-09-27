---
title: Mount Tests for Animated Views Under happy-dom Must Stub matchMedia for Reduced Motion
date: 2026-09-27
last_verified: 2026-09-27
category: conventions
module: frontend/web
problem_type: bug
component: web-console
severity: medium
symptoms:
  - "Vitest mount tests under happy-dom fail with Error: No valid elements provided from motion/mini animate()."
root_cause: "happy-dom lacks Web Animations API support and element geometry required by motion/mini animate(). Calling animate() on happy-dom elements throws. src/motion/useMotionFeedback.ts checks window.matchMedia('(prefers-reduced-motion: reduce)') and exits before animate() when matched."
resolution_type: workaround
applies_when:
  - "Writing or debugging component mount tests in frontend/web/ under Vitest with happy-dom."
  - "A component mount test fails with motion/mini throwing 'No valid elements provided'."
  - "Testing Vue views that invoke src/motion/useMotionFeedback.ts without mocking motion/mini globally."
related_components: [web-console, testing]
tags: [vue, vitest, happy-dom, motion, animations, testing]
---

# Mount tests for animated views under happy-dom must stub matchMedia for reduced motion

## The situation

When mounting view components in Vitest under `happy-dom` (such as `FleetView.vue`),
the view runs its setup script and renders initial DOM nodes. When the view triggers
visual motion feedback (for route transitions, focus indicators, or notice popups),
it invokes `useMotionFeedback()` from `src/motion/useMotionFeedback.ts`.

In a real browser, `motion/mini` animates CSS properties over time. Under `happy-dom`,
the test run immediately aborts with:

```text
Error: No valid elements provided
```

## Why it fails

`happy-dom` simulates browser DOM nodes in Node.js, but omits the Web Animations API
(`Element.prototype.animate`) and computed layout geometry. In
`src/motion/useMotionFeedback.ts:47-50`, `play()` hands the target element to
`motion/mini`:

```ts
    const animation = animate(element, keyframes, {
      duration,
      ease: [0.2, 0, 0, 1],
    })
```

`motion/mini` validates the element reference against browser animation capabilities.
Because `happy-dom` elements lack those interfaces, `animate()` rejects the element
and throws synchronously.

Mocking `motion/mini` across test suites with `vi.mock('motion/mini', ...)` is brittle:
it introduces global module hoisting and replaces module exports that unit tests
rely on.

## The rule

Activate the accessible fallback already built into `useMotionFeedback`.

`src/motion/useMotionFeedback.ts:5` and `29` check user motion preferences:

```ts
  const preference = window.matchMedia('(prefers-reduced-motion: reduce)')
...
  function play(...) {
    if (!element) return
    cancel(element)
    if (preference.matches) return
```

When `preference.matches` evaluates to true, `play()` exits immediately and leaves
static CSS in control.

In mount test setup (`frontend/web/src/FleetView.test.ts:10-17`), stub `matchMedia`
before mounting components:

```ts
beforeEach(() =>
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: query.includes('reduce'),
    media: query,
    addEventListener() {},
    removeEventListener() {},
  })),
)
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.unstubAllGlobals()
})
```

This exercises the production code path for users who prefer reduced motion. The view
mounts, routes, and updates DOM state without touching `motion/mini` internals.

## What this does not cover

This convention ensures view components mount and behave correctly in fast headless DOM
suites. It does not verify CSS keyframe interpolation, transition curves, or layout
animations; those require end-to-end browser execution.
