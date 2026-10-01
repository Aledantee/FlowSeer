---
name: typescript-eslint
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/eslint.config.js:2` imports `typescript-eslint` to provide TypeScript-aware ESLint configuration for the frontend. The pinned direct requirement is `typescript-eslint` at `8.71.0` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/typescript-eslint/8.71.0) lists `GitHub Actions` as the publisher. The pinned version `8.71.0` was published at `2026-09-28T17:12:00.356Z` and was 2 days old on 2026-10-01. It remains under the 14-day wait until `2026-10-12T17:12:00.356Z`. The OSV lookup dated 2026-10-01 returned no advisory for `typescript-eslint` at `8.71.0`. The dependency tree contains 97 versions, with 3 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to parse TypeScript syntax, expose TypeScript-aware ASTs to ESLint, and maintain the configured rules across compiler releases. That would duplicate language tooling in the repository's lint configuration. The dependency tree contains 97 versions, with 3 versions only reachable through this direct dependency.
