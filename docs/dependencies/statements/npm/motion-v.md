---
name: motion-v
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved:
---

## Why it is required

`frontend/web/src/ui/motion/index.ts:1` imports `Motion` and `MotionConfig` from `motion-v` and exports them as `UiMotion` and `UiMotionConfig`. `frontend/web/src/ui/motion/useMotionFeedback.ts:1-7` imports `animateMini`, `frame`, `useMotionConfig`, and `useReducedMotion` from the same package. The pinned direct requirement is `motion-v` at `2.5.1` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/motion-v/2.5.1) lists `hp0844182` as the publisher. The pinned version `2.5.1` was published at `2026-09-28T12:46:55.307Z` and was 4 days old on 2026-10-02. It remains under the 14-day wait until `2026-10-12T12:46:55.307Z`. Its transitive `framer-motion` and `motion-dom` at `13.4.5` remain under the same wait until `2026-10-13`. The OSV lookup dated 2026-10-02 returned no advisory for `motion-v` at `2.5.1`. The dependency tree contains 37 versions, with 4 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement layout animation, shared-element highlight transitions, reduced-motion configuration, and native animation scheduling with cancellation and final-style handling. `UiMotion` and `useMotionFeedback` would then carry browser timing and lifecycle code instead of the animation primitives their tests already exercise. The dependency tree contains 37 versions, with 4 versions only reachable through this direct dependency.
