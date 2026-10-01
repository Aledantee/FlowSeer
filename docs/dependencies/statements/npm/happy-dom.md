---
name: happy-dom
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/src/FleetView.test.ts:1` selects the `happy-dom` Vitest environment, and the same directive is used by the component tests under `frontend/web/src/ui/`. The pinned direct requirement is `happy-dom` at `20.14.5` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/happy-dom/20.14.5) lists `GitHub Actions` as the publisher. The pinned version `20.14.5` was published at `2026-09-12T00:07:47.688Z` and was 18 days old on 2026-10-01. Its 14-day wait ended at `2026-09-26T00:07:47.688Z`. The OSV lookup dated 2026-10-01 returned no advisory for `happy-dom` at `20.14.5`. The dependency tree contains 9 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to provide the DOM, events, timers, URL behavior, and browser API stubs required by the Vue component tests. That would make test-environment behavior owned code and would leave every new browser API to local maintenance. The dependency tree contains 9 versions, with 0 versions only reachable through this direct dependency.
