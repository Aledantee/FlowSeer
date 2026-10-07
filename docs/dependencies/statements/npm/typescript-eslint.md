---
name: typescript-eslint
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/eslint.config.js:2` imports `typescript-eslint` to provide TypeScript-aware ESLint configuration for the frontend. The pinned direct requirement is `typescript-eslint` at `8.70.1` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/typescript-eslint/8.70.1) lists `GitHub Actions` as the publisher. The pinned version `8.70.1` was published at `2026-09-21T17:07:55.524Z` and was 14 days old on 2026-10-06. Its 14-day wait ended at `2026-10-05T17:07:55.524Z`. The OSV lookup dated 2026-10-06 returned no advisory for `typescript-eslint` at `8.70.1`. The dependency tree contains 97 versions, with 3 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to parse TypeScript syntax, expose TypeScript-aware ASTs to ESLint, and maintain the configured rules across compiler releases. That would duplicate language tooling in the repository's lint configuration. The dependency tree contains 97 versions, with 3 versions only reachable through this direct dependency.
