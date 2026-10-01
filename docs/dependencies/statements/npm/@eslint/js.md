---
name: "@eslint/js"
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/eslint.config.js:1` imports `@eslint/js`, and line 8 applies its
recommended rules to the web source. `frontend/web/package.json:39` pins the
direct requirement at `10.0.1`.

## Why it is safe

The npm registry metadata identifies `eslintbot` as the publisher of version
`10.0.1` ([registry record](https://registry.npmjs.org/%40eslint%2Fjs)). The
version was published at `2026-02-06T22:34:56.29Z` and was 236 days old on
2026-10-01. Its 14-day wait ended at `2026-02-20T22:34:56.29Z`. The OSV
lookup dated 2026-10-01 returned no advisory for `@eslint/js` at `10.0.1`.
The npm dependency tree contains 80 versions, with 0 versions only reachable
through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to maintain a copy of ESLint's recommended core rule set
and update it with every ESLint release. That would duplicate compatibility
work and let the lint baseline drift from the ESLint version in use. The npm
dependency tree contains 80 versions, with 0 versions only reachable through
this direct dependency.
