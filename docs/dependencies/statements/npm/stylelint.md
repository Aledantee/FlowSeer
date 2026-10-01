---
name: stylelint
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/package.json:15` runs Stylelint over `src/**/*.{css,vue}`, using the rules in `frontend/web/.stylelintrc.json`. The pinned direct requirement is `stylelint` at `17.15.0` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/stylelint/17.15.0) lists `GitHub Actions` as the publisher. The pinned version `17.15.0` was published at `2026-09-04T14:00:35.419Z` and was 26 days old on 2026-10-01. Its 14-day wait ended at `2026-09-18T14:00:35.419Z`. The OSV lookup dated 2026-10-01 returned no advisory for `stylelint` at `17.15.0`. The dependency tree contains 117 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to parse CSS and Vue style blocks, apply the configured rules, report source locations, and maintain the linter's CLI behavior. That would make syntax parsing and lint diagnostics owned tooling instead of a shared linter. The dependency tree contains 117 versions, with 0 versions only reachable through this direct dependency.
