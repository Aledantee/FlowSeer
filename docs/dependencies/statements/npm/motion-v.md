---
name: motion-v
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: 2026-10-02
---

## Why it is required

`frontend/web/src/ui/motion/index.ts:1` imports `Motion` and `MotionConfig` from `motion-v` and exports them as `UiMotion` and `UiMotionConfig`. `frontend/web/src/ui/motion/useMotionFeedback.ts:1-6` imports `animateMini`, `useMotionConfig`, and `useReducedMotion` from the same package. The pinned direct requirement is `motion-v` at `2.4.4` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/motion-v/2.4.4) lists `hp0844182` as the publisher. The pinned version `2.4.4` was published at `2026-09-16T07:33:55.695Z` and was 19 days old on 2026-10-06. Its 14-day wait ended at `2026-09-30T07:33:55.695Z`. Its transitive `framer-motion` and `motion-dom` at `13.4.1` were published on 2026-09-22, and their wait ended on 2026-10-06. The OSV lookup dated 2026-10-06 returned no advisory for `motion-v` at `2.4.4`. The dependency tree contains 37 versions, with 4 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement layout animation, shared-element highlight transitions, reduced-motion configuration, and native animation scheduling with cancellation and final-style handling. `UiMotion` and `useMotionFeedback` would then carry browser timing and lifecycle code instead of the animation primitives their tests already exercise. The dependency tree contains 37 versions, with 4 versions only reachable through this direct dependency.
