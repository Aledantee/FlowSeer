---
title: Mount Tests for Animated Views Under happy-dom Must Stub matchMedia for Reduced Motion
date: 2026-09-27
last_verified: 2026-10-01
category: conventions
module: frontend/web
problem_type: bug
component: web-console
severity: medium
symptoms:
  - "Vitest mount tests under happy-dom reach motion-v's layout or animation path and throw while the DOM has no browser layout."
root_cause: "happy-dom lacks browser layout and complete Web Animations behavior. A reduced-motion matchMedia stub selects motion-v's safe path before the first mount."
resolution_type: workaround
applies_when:
  - "Writing or debugging component mount tests in frontend/web/ under Vitest with happy-dom."
  - "A component mount test reaches motion-v layout or animation code and throws."
  - "Testing Vue views that invoke frontend/web/src/ui/motion/useMotionFeedback.ts without mocking motion-v globally."
related_components: [web-console, testing]
tags: [vue, vitest, happy-dom, motion, animations, testing]
---

# Mount tests for animated views under happy-dom must stub matchMedia for reduced motion

## The situation

When mounting view components in Vitest under `happy-dom` (such as `FleetView.vue`),
the view runs its setup script and renders initial DOM nodes. When the view triggers
visual motion feedback, it invokes `useMotionFeedback()` from
`src/ui/motion/useMotionFeedback.ts` or renders `UiMotion`.

## Why it fails

`happy-dom` simulates browser DOM nodes in Node.js, but does not provide browser
layout. motion-v can inspect layout and animation state during mount, so a test
that leaves the normal motion path enabled can throw before it reaches an
assertion. An `offsetParent` stub is not needed to avoid that throw.

## The rule

Activate the reduced-motion path already built into the motion surface. In tests
that exercise reduced motion, install a `matchMedia` stub before the first mount:

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

The reduced-motion matchMedia stub selects motion-v's safe path before the first
mount. The composable keeps opacity fades and filters movement. `UiMotion` layout
animations end immediately with no fade. This exercises the production code path
for users who prefer reduced motion without mocking motion-v or stubbing
`offsetParent`.

## What this does not cover

This convention ensures view components mount and behave correctly in fast headless DOM
suites. It does not verify CSS keyframe interpolation, transition curves, or browser
layout. Those require end-to-end browser execution.
