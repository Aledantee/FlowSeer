---
name: globals
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/eslint.config.js:5` imports `globals` and `frontend/web/eslint.config.js:15` reads its `browser` map for the frontend ESLint environment. The pinned direct requirement is `globals` at `17.12.0` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/globals/17.12.0) lists `sindresorhus` as the publisher. The pinned version `17.12.0` was published at `2026-09-01T11:00:43.545Z` and was 29 days old on 2026-10-01. Its 14-day wait ended at `2026-09-15T11:00:43.545Z`. The OSV lookup dated 2026-10-01 returned no advisory for `globals` at `17.12.0`. The dependency tree contains 1 version, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to maintain the browser global names and update them as browser APIs change, then wire those names into ESLint's environment configuration. That would turn an editor-tooling data set into owned maintenance. The dependency tree contains 1 version, with 0 versions only reachable through this direct dependency.
