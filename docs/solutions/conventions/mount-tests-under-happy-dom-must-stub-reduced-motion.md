---
title: A happy-dom Mount Test Installs the Reduced-Motion matchMedia Stub Before Its First Mount, and Normal-Motion Cases Live in a File Without It
date: 2026-09-27
last_verified: 2026-10-01
category: conventions
module: frontend/web
problem_type: convention
component: web-console
severity: low
applies_when:
  - "Writing or debugging component mount tests in frontend/web/ under Vitest with happy-dom that exercise motion-v surfaces (`UiMotion`, `UiMotionConfig`, `useMotionFeedback`)."
  - "A test needs the reduced-motion path of a mounted motion surface, or needs the normal path and the same file also holds reduced-motion cases."
  - "A mount test asserts on a layout animation and happy-dom reports no geometry."
related_components: [web-console, testing]
tags: [vue, vitest, happy-dom, motion, animations, testing]
---

# A happy-dom mount test installs the reduced-motion matchMedia stub before its first mount, and normal-motion cases live in a file without it

## The situation

`FleetView.vue` and the `src/ui/motion/` surfaces animate through motion-v.
Mounted under Vitest with happy-dom, they run on the normal motion path by
default. That path needs no stub. A mount of `FleetView` under `UiAppRoot` that
toggles the sidebar, navigates, and switches the theme reports no error without
any `matchMedia` stub. happy-dom implements `Element.animate`
(`happy-dom@20.14.5` `lib/nodes/element/Element.js:1083`) and answers
`prefers-reduced-motion` from its settings
(`lib/match-media/MediaQueryItem.js:187`).

A `matchMedia` stub is therefore a way to select the reduced path, never a way
to avoid a throw. The same goes for an `offsetParent` stub.

## What is true

| Fact | Source |
| --- | --- |
| motion-dom reads the reduced-motion query once per module and keeps that `MediaQueryList`. | `motion-dom@13.4.5` `dist/es/render/utils/reduced-motion/index.mjs:9` |
| `useMotionFeedback` reads the query itself on every mount through `useMediaQuery`, so it follows a stub installed after an earlier mount. | `motion-v@2.5.1` `dist/es/animation/hooks/use-reduced-motion.mjs` |
| Under the reduced path the composable keeps the opacity fade and drops movement. | `frontend/web/src/ui/motion/useMotionFeedback.ts:73`, `useMotionFeedback.test.ts:80` |
| Under the reduced path `UiMotion` layout animations end at once with no fade. | `frontend/web/src/ui/motion/UiMotion.reduced.test.ts:61` |
| happy-dom has no layout, so a layout animation sees zero-size boxes. | `frontend/web/src/FleetView.motion.test.ts:31` stubs `getBoundingClientRect` |

## The rule

A file that mounts motion components installs the stub before its first mount,
because motion-dom keeps the first `MediaQueryList` it reads. Cases on the
normal path live in a file without the stub. The pair
`frontend/web/src/ui/motion/UiMotion.test.ts` (no stub, layout and positional
animation) and `UiMotion.reduced.test.ts` (stub in `beforeEach`) shows the split.

```ts
beforeEach(() =>
  vi.stubGlobal('matchMedia', (query: string) =>
    Object.assign(new EventTarget(), {
      matches: query.includes('reduce'),
      media: query,
      onchange: null,
      addListener() {},
      removeListener() {},
    }),
  ),
)
afterEach(() => {
  dispose()
  document.body.replaceChildren()
  vi.unstubAllGlobals()
})
```

A file that only mounts the composable can hold both modes, because
`useMotionFeedback` re-reads the query per mount.
`frontend/web/src/components/ThemeSwitcher.test.ts` does this through its
`stubMatchMedia(reducedMotion)` helper.

A test that needs a layout animation to run gives elements geometry with a
stubbed `HTMLElement.prototype.getBoundingClientRect`, as
`FleetView.motion.test.ts` does. Do not mock motion-v.

## What this does not cover

These tests check which path a surface takes and what it leaves in the DOM.
They do not verify CSS keyframe interpolation, transition curves, or browser
layout. Those need a browser.
