---
name: eslint-config-prettier
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/eslint.config.js:4` imports `eslint-config-prettier`, and line 11
applies it after the JavaScript, TypeScript, and Vue rules. The direct
requirement is pinned at `10.1.8` in `frontend/web/package.json:49`.

## Why it is safe

The npm registry metadata identifies `jounqin` as the publisher of version
`10.1.8` ([registry record](https://registry.npmjs.org/eslint-config-prettier)).
The version was published at `2025-07-18T18:40:08.244Z` and was 439 days old
on 2026-10-01. Its 14-day wait ended at `2025-08-01T18:40:08.244Z`. The OSV
lookup dated 2026-10-01 returned no advisory for `eslint-config-prettier` at
`10.1.8`. The npm dependency tree contains 80 versions, with 0 versions only
reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer could write a short local list of disabled rules, yet it would then
own the rule-by-rule compatibility map between ESLint and Prettier. This
package maintains that map as both tools change. The npm dependency tree
contains 80 versions, with 0 versions only reachable through this direct
dependency.
