---
name: typescript
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

The `vue-tsc` package resolves `typescript@6.0.3` in `frontend/web/pnpm-lock.yaml:8399-8403`, and `frontend/web/package.json:12-13` invokes `vue-tsc` for the build and typecheck scripts. The pinned direct requirement is `typescript` at `6.0.3` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/typescript/6.0.3) lists `typescript-bot` as the publisher. The pinned version `6.0.3` was published at `2026-04-16T23:38:27.905Z` and was 167 days old on 2026-10-01. Its 14-day wait ended at `2026-04-30T23:38:27.905Z`. The OSV lookup dated 2026-10-01 returned no advisory for `typescript` at `6.0.3`. The dependency tree contains 1 version, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to own TypeScript parsing, type checking, module resolution, declaration emit, and the compiler APIs consumed by `vue-tsc`. That would make the language compiler part of the frontend codebase and would be substantially larger than the direct tree measured here. The dependency tree contains 1 version, with 0 versions only reachable through this direct dependency.
