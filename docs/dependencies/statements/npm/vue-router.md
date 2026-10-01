---
name: vue-router
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/src/main.ts:2` creates the application router, and `frontend/web/src/FleetView.vue:13` uses route state. The pinned direct requirement is `vue-router` at `5.3.1` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/vue-router/5.3.1) lists `GitHub Actions` as the publisher. The pinned version `5.3.1` was published at `2026-09-02T12:39:20.761Z` and was 28 days old on 2026-10-01. Its 14-day wait ended at `2026-09-16T12:39:20.761Z`. The OSV lookup dated 2026-10-01 returned no advisory for `vue-router` at `5.3.1`. The dependency tree contains 126 versions, with 27 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement route matching, browser and memory history, navigation guards, URL serialization, and reactive route state. That would duplicate application navigation infrastructure and make its edge cases owned code. The dependency tree contains 126 versions, with 27 versions only reachable through this direct dependency.
