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
any `matchMedia` stub. The native feedback tests observe the resulting
`Animation` and `KeyframeEffect` objects through `getAnimations()`.
`frontend/web/pnpm-lock.yaml` pins happy-dom to 20.14.5.

A `matchMedia` stub is therefore a way to select the reduced path, never a way
to avoid a throw. The same goes for an `offsetParent` stub.

## What is true

| Fact | Source |
| --- | --- |
| The web workspace locks motion-v 2.5.1, motion-dom and framer-motion 13.4.5, @vueuse/core 14.4.0, and happy-dom 20.14.5. | `frontend/web/pnpm-lock.yaml` |
| `useMotionFeedback` reads the user reduced-motion state when the composable mounts, so a `matchMedia` stub installed before that mount selects the path. | `frontend/web/src/ui/motion/useMotionFeedback.ts`, `frontend/web/src/ui/motion/useMotionFeedback.test.ts` |
| Under the reduced path the composable keeps the opacity fade and drops movement. | `frontend/web/src/ui/motion/useMotionFeedback.ts` (`play`'s reduced branch), `frontend/web/src/ui/motion/useMotionFeedback.test.ts` "filters reduced movement, keeps reduced fades native, and restores their baseline", `frontend/web/src/components/ThemeSwitcher.test.ts` "keeps reduced motion as one running opacity effect per icon" |
| Native feedback tests inspect keyframe endpoints, timing, running state, cleanup, and replacement. | `frontend/web/src/ui/motion/useMotionFeedback.test.ts` "compiles typed pairs into ordered native effects with deterministic timing", "cancels and restores synchronously before replacing a play, through the next frame and completion", "ignores a stale completion queued before replacement" |
| Under the reduced path `UiMotion` layout animations end at once with no fade. | `frontend/web/src/ui/motion/UiMotion.reduced.test.ts` "ends layout animation at once when the user prefers reduced motion" |
| happy-dom has no layout, so layout tests provide geometry explicitly. | `frontend/web/src/FleetView.motion.test.ts`, `frontend/web/src/ui/motion/UiMotion.reduced.test.ts` |

## The rule

A file that mounts motion components installs the stub before its first mount,
because the locked motion-dom behavior keeps the first `MediaQueryList` it
reads, as recorded in
`docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-phase2-plan.md`.
Cases on the normal path live in a file without the stub. The pair
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

These tests check which path a surface takes, its native keyframes and timing,
and what it leaves in the DOM. They do not verify interpolation, computed
styles, the first `requestAnimationFrame` samples, active native effects as the
browser runs them, screenshots, or restoration after resize and cancel or
replay. Those need the real-browser measurement loop in
`.agents/skills/web-component/references/review.md`.
