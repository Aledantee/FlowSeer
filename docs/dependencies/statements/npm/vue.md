---
name: vue
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/src/main.ts:1` imports `createApp`, and `frontend/web/src/FleetView.vue:11-12` imports Vue runtime APIs and types. The pinned direct requirement is `vue` at `3.5.43` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/vue/3.5.43) lists `GitHub Actions` as the publisher. The pinned version `3.5.43` was published at `2026-09-17T08:39:03.208Z` and was 13 days old on 2026-10-01. Its 14-day wait ended at `2026-10-01T08:39:03.208Z`. The OSV lookup dated 2026-10-01 returned no advisory for `vue` at `3.5.43`. The dependency tree contains 24 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to own the reactive runtime, component lifecycle, rendering, event handling, and single-file component integration used by every frontend view. That would replace the application framework with a repository-specific runtime and make browser compatibility owned code. The dependency tree contains 24 versions, with 0 versions only reachable through this direct dependency.
