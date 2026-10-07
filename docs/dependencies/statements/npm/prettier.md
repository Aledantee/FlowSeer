---
name: prettier
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/package.json:16-17` uses `prettier` for repository formatting, and `frontend/web/scripts/palette-outputs.ts:4` imports it to format generated palette output. The pinned direct requirement is `prettier` at `3.9.8` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/prettier/3.9.8) lists `GitHub Actions` as the publisher. The pinned version `3.9.8` was published at `2026-09-17T21:41:05.085Z` and was 18 days old on 2026-10-06. Its 14-day wait ended at `2026-10-01T21:41:05.085Z`. The OSV lookup dated 2026-10-06 returned no advisory for `prettier` at `3.9.8`. The dependency tree contains 1 version, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement stable formatting for TypeScript, Vue, JSON, and the other files covered by `frontend/web/package.json:16-17`, then keep the formatter aligned with syntax changes. That would create a repository-specific formatter instead of a shared formatting contract. The dependency tree contains 1 version, with 0 versions only reachable through this direct dependency.
