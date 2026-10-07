---
title: A happy-dom Motion Mount Test Stubs matchMedia Before the First Mount, and Supplies Its Own Geometry and Clock
date: 2026-09-27
last_verified: 2026-10-07
category: conventions
module: frontend/web
problem_type: convention
component: web-console
severity: low
applies_when:
  - "Writing or debugging component mount tests in frontend/web/ under Vitest with happy-dom that exercise motion-v surfaces (`UiMotion`, `UiMotionConfig`, `useMotionFeedback`)."
  - "A test needs the reduced-motion path of a mounted motion surface, or needs the normal path and the same file also holds reduced-motion cases."
  - "A mount test asserts on a layout animation and needs geometry or a controlled clock that happy-dom does not provide."
  - "A happy-dom mount test unmounts a component while a JavaScript-driven feedback animation is still running."
related_components: [web-console, testing]
tags: [vue, vitest, happy-dom, motion, animations, testing]
---

# A happy-dom motion mount test stubs matchMedia before the first mount, and supplies its own geometry and clock

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

| Fact                                                                                                                                                                                                                                                                                               | Source                                                                                                                                                                                                                                                                                                                                     |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| The web workspace locks motion-v 2.4.4, motion-dom and framer-motion 13.4.1, @vueuse/core 14.4.0, and happy-dom 20.14.5.                                                                                                                                                                           | `frontend/web/pnpm-lock.yaml`                                                                                                                                                                                                                                                                                                              |
| A mounted `UiMotion` element reads the reduced-motion query through motion-dom once per test file. The first element whose config needs the dynamic value calls `initPrefersReducedMotion`, which listens to `(prefers-reduced-motion)`, and the element fixes its choice at mount from that read. | `motion-dom/dist/es/render/VisualElement.mjs:205-216` and `motion-dom/dist/es/render/utils/reduced-motion/index.mjs:4-13`, under `frontend/web/node_modules/.pnpm/motion-dom@13.4.1/node_modules/`                                                                                                                                         |
| `UiMotionConfig` reads no media query. It provides config and renders its slot.                                                                                                                                                                                                                    | `frontend/web/node_modules/motion-v/dist/es/components/motion-config/MotionConfig.vue_vue_type_script_setup_true_lang.mjs`                                                                                                                                                                                                                 |
| `useMotionFeedback` watches `(prefers-reduced-motion: reduce)`, a different query from motion-dom's, and reads it when the composable mounts, so a `matchMedia` stub installed before that mount selects the path.                                                                                 | `frontend/web/src/ui/motion/useMotionFeedback.ts`, `frontend/web/node_modules/motion-v/dist/es/animation/hooks/use-reduced-motion.mjs:4`, `frontend/web/src/ui/motion/useMotionFeedback.test.ts`                                                                                                                                           |
| Under the reduced path the composable keeps the opacity fade and drops movement.                                                                                                                                                                                                                   | `frontend/web/src/ui/motion/useMotionFeedback.ts` (`play`'s reduced branch), `frontend/web/src/ui/motion/useMotionFeedback.test.ts` "filters reduced movement, keeps reduced fades native, and restores their baseline", `frontend/web/src/components/ThemeSwitcher.test.ts` "keeps reduced motion as one running opacity effect per icon" |
| Native feedback tests inspect keyframe endpoints, timing, running state, cleanup, and replacement.                                                                                                                                                                                                 | `frontend/web/src/ui/motion/useMotionFeedback.test.ts` "compiles typed pairs into ordered native effects with deterministic timing", "cancels and restores synchronously before replacing a play, through the next frame and completion", "ignores a stale completion queued before replacement"                                           |
| Under the reduced path `UiMotion` layout animations end at once with no fade.                                                                                                                                                                                                                      | `docs/architecture/2026-09-28-web-component-contract-direction.md`, section `2026-10-01`. `frontend/web/src/ui/motion/UiMotion.reduced.test.ts` "ends layout animation at once when the user prefers reduced motion" asserts no `scale` or `translate` remains.                                                                            |
| happy-dom has no layout, so layout tests provide geometry explicitly.                                                                                                                                                                                                                              | `frontend/web/src/FleetView.motion.test.ts`, `frontend/web/src/ui/motion/UiMotion.reduced.test.ts`                                                                                                                                                                                                                                         |
| motion's frame loop stamps each frame from `performance.now()`, so a layout-animation test installs a mocked clock before the mount and advances it.                                                                                                                                               | `motion-dom/dist/es/frameloop/batcher.mjs:22-24`, under `frontend/web/node_modules/.pnpm/motion-dom@13.4.1/node_modules/`. `frontend/web/src/FleetView.motion.test.ts`, `frontend/web/src/ui/motion/UiMotion.test.ts`, `frontend/web/src/ui/motion/UiMotion.reduced.test.ts`                                                               |

## The rule

A file that mounts `UiMotion` installs the `matchMedia` stub before its first
mount. The first mounted element whose config needs the dynamic value binds the
reduced-motion listener once per file
(`motion-dom/dist/es/render/VisualElement.mjs:205-216` and
`motion-dom/dist/es/render/utils/reduced-motion/index.mjs:4-13`, under
`frontend/web/node_modules/.pnpm/motion-dom@13.4.1/node_modules/`), so a stub
installed after that mount cannot select the path. `UiMotionConfig` reads no
query and mounts without a stub. The pair
`frontend/web/src/ui/motion/UiMotion.test.ts` (no stub, layout and positional
animation) and `UiMotion.reduced.test.ts` (stub in `beforeEach`) shows the split.

```ts
beforeEach(() =>
  vi.stubGlobal("matchMedia", (query: string) =>
    Object.assign(new EventTarget(), {
      matches: query.includes("reduce"),
      media: query,
      onchange: null,
      addListener() {},
      removeListener() {},
    }),
  ),
);
afterEach(() => {
  dispose();
  document.body.replaceChildren();
  // Restores spies too, so the mocked clock cannot leak into a later case.
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
```

A file can still hold both modes, but not by flipping a mounted `UiMotion`'s
path. motion-dom watches `(prefers-reduced-motion)`
(`reduced-motion/index.mjs:9`) and the element reads that preference at mount,
so a `change` after the mount reaches only elements mounted afterwards.
`useMotionFeedback` watches `(prefers-reduced-motion: reduce)`
(`frontend/web/node_modules/motion-v/dist/es/animation/hooks/use-reduced-motion.mjs:4`)
and re-reads it per mount.
`frontend/web/src/FleetView.motion.test.ts` dispatches `change` on the `reduce`
object in `stops layout transforms after the user enables reduced motion` and
`leaves the nav opacity untouched when expanding at desktop width`. Its
reduced-motion case asserts
`FleetView.vue`'s own gate, which skips the layout bump when the composable
reports reduced (`toggleSidebar`), not `UiMotion`'s reduced layout path. A file
that only mounts the composable can hold both modes for the same reason.
`frontend/web/src/components/ThemeSwitcher.test.ts` does this through its
`stubMatchMedia(reducedMotion)` helper.

A test that needs a layout animation to run gives elements geometry with a
stubbed `HTMLElement.prototype.getBoundingClientRect`, as
`FleetView.motion.test.ts` does. It also installs a mocked `performance.now`
before the mount and advances it, because motion's frame loop stamps each frame
from `performance.now()`
(`motion-dom/dist/es/frameloop/batcher.mjs:22-24`). `FleetView.motion.test.ts`,
`UiMotion.test.ts`, and `UiMotion.reduced.test.ts` do this through
`installMotionClock` and `advanceMotion`, and undo the spy in `afterEach`
(`FleetView.motion.test.ts:87`, `UiMotion.test.ts:22`,
`UiMotion.reduced.test.ts:34`), because a leaked mocked clock freezes
`performance.now()` for every later case in the file. A fixed wall-clock wait is
not reliable: on a loaded host the 160 ms layout animation can finish before the
test samples. Do not mock motion-v.

## Cancellation during teardown

happy-dom's `Animation.cancel()` rejects the animation's `finished` promise
with `AbortError: The animation was canceled.` when playback is active
(`frontend/web/node_modules/.pnpm/happy-dom@20.14.5/node_modules/happy-dom/lib/animation/Animation.js:153-162`).
The relevant line is:

```js
this.#rejectFinished?.(
  new this[PropertySymbol.window].DOMException(
    "The animation was canceled.",
    DOMExceptionNameEnum.abortError,
  ),
);
```

`stop` in `frontend/web/src/ui/motion/useMotionFeedback.ts` attaches rejection
handlers to the native and motion playback `finished` promises before it
cancels playback. This also covers scope disposal while feedback is running.
`frontend/web/src/ui/motion/useMotionFeedback.test.ts` holds the cancellation
and cleanup behavior.

## What this does not cover

These tests check which path a surface takes, its native keyframes and timing,
and what it leaves in the DOM. They do not verify interpolation, computed
styles, the first `requestAnimationFrame` samples, active native effects as the
browser runs them, screenshots, or restoration after resize and cancel or
replay. Those need the real-browser measurement loop in
`.agents/skills/web-component/references/review.md`.
