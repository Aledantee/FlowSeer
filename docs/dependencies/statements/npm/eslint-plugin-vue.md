---
name: eslint-plugin-vue
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/eslint.config.js:3` imports `eslint-plugin-vue`, and line 10
enables its flat Vue rule set for the web source. The direct requirement is
pinned at `10.11.0` in `frontend/web/package.json:52`.

## Why it is safe

The npm registry metadata identifies `GitHub Actions` as the publisher of
version `10.11.0` ([registry record](https://registry.npmjs.org/eslint-plugin-vue)).
The version was published at `2026-09-06T09:59:52.602Z` and was 29 days old
on 2026-10-06. Its 14-day wait ended at `2026-09-20T09:59:52.602Z`. The OSV
lookup dated 2026-10-06 returned no advisory for `eslint-plugin-vue` at
`10.11.0`. The npm dependency tree contains 100
versions, with 4 versions only reachable through this direct dependency.
Source not yet reviewed.

## Why not owned code

FlowSeer would have to parse Vue single-file components and maintain Vue-aware
lint rules for templates, directives, and script blocks. That would duplicate
the parser and rule set needed to track Vue and ESLint releases. The npm
dependency tree contains 100 versions, with 4 versions only reachable through
this direct dependency.
