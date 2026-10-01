---
name: stylelint-config-standard
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/.stylelintrc.json:2` extends `stylelint-config-standard` for the CSS and Vue files selected by the `frontend/web/package.json:15` lint script. The pinned direct requirement is `stylelint-config-standard` at `40.0.0` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/stylelint-config-standard/40.0.0) lists `GitHub Actions` as the publisher. The pinned version `40.0.0` was published at `2026-01-15T12:10:27.794Z` and was 258 days old on 2026-10-01. Its 14-day wait ended at `2026-01-29T12:10:27.794Z`. The OSV lookup dated 2026-10-01 returned no advisory for `stylelint-config-standard` at `40.0.0`. The dependency tree contains 119 versions, with 1 version only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to define and maintain the standard CSS rule set, including changes needed as Stylelint and CSS syntax evolve. That would make lint policy a local fork instead of a maintained shared configuration. The dependency tree contains 119 versions, with 1 version only reachable through this direct dependency.
