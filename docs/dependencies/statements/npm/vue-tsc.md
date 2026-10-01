---
name: vue-tsc
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/package.json:12-13` invokes `vue-tsc` for the production build and the standalone typecheck command. The pinned direct requirement is `vue-tsc` at `3.3.11` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/vue-tsc/3.3.11) lists `GitHub Actions` as the publisher. The pinned version `3.3.11` was published at `2026-08-21T10:11:21.360Z` and was 40 days old on 2026-10-01. Its 14-day wait ended at `2026-09-04T10:11:21.360Z`. The OSV lookup dated 2026-10-01 returned no advisory for `vue-tsc` at `3.3.11`. The dependency tree contains 21 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to transform Vue single-file components, connect their templates to TypeScript's type checker, and report diagnostics at Vue source locations. That would duplicate a compiler integration and make Vue language-service behavior owned tooling. The dependency tree contains 21 versions, with 0 versions only reachable through this direct dependency.
