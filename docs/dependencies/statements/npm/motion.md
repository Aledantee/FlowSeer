---
name: motion
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/src/motion/useMotionFeedback.ts:2` imports `animate` from `motion/mini`, and `frontend/web/src/motion/useMotionFeedback.test.ts:3` uses the same entry point. The pinned direct requirement is `motion` at `13.4.5` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/motion/13.4.5) lists `popmotion` as the publisher. The pinned version `13.4.5` was published at `2026-09-29T04:40:18.685Z` and was 1 day old on 2026-10-01. It remains under the 14-day wait until `2026-10-13T04:40:18.685Z`. The OSV lookup dated 2026-10-01 returned no advisory for `motion` at `13.4.5`. The dependency tree contains 8 versions, with 3 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement animation scheduling, cancellation, final-style handling, and reduced-motion behavior in `useMotionFeedback`. That would add browser timing and lifecycle code to the application instead of using the animation primitive already exercised by its tests. The dependency tree contains 8 versions, with 3 versions only reachable through this direct dependency.
