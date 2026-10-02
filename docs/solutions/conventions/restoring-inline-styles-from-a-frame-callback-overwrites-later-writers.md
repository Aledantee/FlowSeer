---
title: Restoring Inline Styles From a Frame-Scheduled Callback Overwrites Later Writers
date: 2026-10-02
last_verified: 2026-10-02
category: conventions
module: frontend/web
problem_type: convention
component: web-console
severity: high
applies_when:
  - "Restoring inline styles on animation cancellation, resize, replacement, or completion in frontend/web/."
  - "Writing or reviewing motion composables, animation helpers, or components that manage DOM inline style lifecycles."
  - "Testing that animation cleanup preserves subsequent synchronous style modifications through the next frame batch."
related_components: [web-console, testing]
tags: [animations, motion, inline-styles, cleanup, synchronization, testing]
---

# Restoring inline styles from a frame-scheduled callback overwrites later writers

## The situation

Animation helpers snapshot an element's inline styles at start and restore
them when the animation ends, cancels, or gets replaced. When an animation
library might mutate inline styles during its render cycle, a defensive
reaction is to schedule restoration on the next animation frame or post-render
batch alongside the synchronous restore.

Scheduling a deferred restore creates a race condition with subsequent writers.
If a caller cancels an animation and immediately writes a new inline style, or
starts another animation before the next frame, the deferred callback runs
later in the event loop and overwrites the caller's write with the old snapshot.

## What is true and why

Restoring inline styles on animation termination must execute once and
synchronously.

1. **Synchronous restore releases ownership immediately.** When `stop()` or
   `cancel()` runs, clearing the active animation map and restoring baseline
   inline values in the same turn leaves the DOM ready for immediate reuse.
2. **Deferred callbacks cannot distinguish subsequent writes from stale state.**
   A callback queued with `requestAnimationFrame`, `frame.render`,
   `frame.postRender`, or `setTimeout` holds a snapshot taken before the
   cancellation. It cannot tell whether an inline style changed because of
   residual animation engine activity or because caller code wrote a new value
   after cancellation.
3. **Property tests require a later-write invariant.** Testing only that
   restoration occurred in the same turn misses deferred overwrites. Proving
   clean restoration requires mutating the owned properties immediately after
   termination, awaiting the next frame batch and timer tick, and asserting
   that the later values survived.

## Working example

In `frontend/web/src/ui/motion/useMotionFeedback.ts:73-85`, `stop()` cancels the
active animation and runs `restore()` synchronously without scheduling any frame
callbacks:

```ts
function stop(element: HTMLElement) {
  const current = active.get(element)
  if (!current) return
  active.delete(element)

  for (const animation of current.nativeAnimations) {
    void animation.finished.catch(() => {})
  }
  void current.animation.finished.catch(() => {})
  current.animation.cancel()
  current.restore()
}
```

In `frontend/web/src/ui/motion/useMotionFeedback.test.ts:550-556`, the test
suite asserts that subsequent synchronous writes survive through frame and timer
settlement:

```ts
if (invariant.id === 'later-write') {
  for (const key of actualOwned.keys)
    mounted.element.style[key] = writtenAfterPath[key]
  await settleFrames()
  for (const key of actualOwned.keys)
    expect(mounted.element.style[key]).toBe(writtenAfterPath[key])
  return
}
```

## Evidence

- `frontend/web/src/ui/motion/useMotionFeedback.ts:73-85` executes synchronous
  restoration without deferred frame tasks.
- `frontend/web/src/ui/motion/useMotionFeedback.test.ts:200-211` implements
  `settleFrames()` to drain `frame.postRender()`, `requestAnimationFrame()`,
  and zero-delay timer queues.
- `frontend/web/src/ui/motion/useMotionFeedback.test.ts:465-468` and `:550-557`
  enforce the `later-write` invariant across every terminal path.
- Commit `63f97bfc` proves that re-introducing deferred `frame.render` and
  `frame.postRender` restores in `stop()` fails 70 generated cases in
  `useMotionFeedback.test.ts`.

## What this does not cover

This convention applies to DOM inline style cleanup managed by application
composables. It does not govern Web Animations API animation replacement
rules, CSS transition cancellation events, or headless layout animations
managed directly by `motion-v`'s `VisualElement`.
