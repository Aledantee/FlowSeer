---
name: vitest
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/package.json:14` invokes Vitest, and `frontend/web/src/FleetView.test.ts:2` imports its test API. The pinned direct requirement is `vitest` at `5.0.1` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/vitest/5.0.1) lists `GitHub Actions` as the publisher. The pinned version `5.0.1` was published at `2026-09-15T08:49:35.830Z` and was 20 days old on 2026-10-06. Its 14-day wait ended at `2026-09-29T08:49:35.830Z`. The OSV lookup dated 2026-10-06 returned no advisory for `vitest` at `5.0.1`. The dependency tree contains 99 versions, with 13 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to own test discovery, assertions, mocks, fake timers, browser-environment integration, and the worker lifecycle used by the frontend suite. That would create a local test runner whose behavior would become another maintained runtime. The dependency tree contains 99 versions, with 13 versions only reachable through this direct dependency.
