---
name: vitest
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/package.json:14` invokes Vitest, and `frontend/web/src/FleetView.test.ts:2` imports its test API. The pinned direct requirement is `vitest` at `5.0.2` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/vitest/5.0.2) lists `GitHub Actions` as the publisher. The pinned version `5.0.2` was published at `2026-09-25T09:00:46.560Z` and was 5 days old on 2026-10-01. It remains under the 14-day wait until `2026-10-09T09:00:46.560Z`. The OSV lookup dated 2026-10-01 returned no advisory for `vitest` at `5.0.2`. The dependency tree contains 97 versions, with 11 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to own test discovery, assertions, mocks, fake timers, browser-environment integration, and the worker lifecycle used by the frontend suite. That would create a local test runner whose behavior would become another maintained runtime. The dependency tree contains 97 versions, with 11 versions only reachable through this direct dependency.
