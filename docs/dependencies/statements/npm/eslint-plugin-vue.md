---
name: eslint-plugin-vue
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/eslint.config.js:3` imports `eslint-plugin-vue`, and line 10
enables its flat Vue rule set for the web source. The direct requirement is
pinned at `10.11.1` in `frontend/web/package.json:50`.

## Why it is safe

The npm registry metadata identifies `GitHub Actions` as the publisher of
version `10.11.1` ([registry record](https://registry.npmjs.org/eslint-plugin-vue)).
The version was published at `2026-09-24T01:09:50.484Z` and was 7 days old
on 2026-10-01. Its 14-day wait ends at `2026-10-08T01:09:50.484Z`, so the pin
is still under the wait. The OSV lookup dated 2026-10-01 returned no advisory
for `eslint-plugin-vue` at `10.11.1`. The npm dependency tree contains 100
versions, with 4 versions only reachable through this direct dependency.
Source not yet reviewed.

## Why not owned code

FlowSeer would have to parse Vue single-file components and maintain Vue-aware
lint rules for templates, directives, and script blocks. That would duplicate
the parser and rule set needed to track Vue and ESLint releases. The npm
dependency tree contains 100 versions, with 4 versions only reachable through
this direct dependency.
