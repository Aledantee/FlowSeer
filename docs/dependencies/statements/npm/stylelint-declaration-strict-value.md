---
name: stylelint-declaration-strict-value
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/.stylelintrc.json:4` loads `stylelint-declaration-strict-value`, and `frontend/web/.stylelintrc.json:8` configures its declaration-value rule. The pinned direct requirement is `stylelint-declaration-strict-value` at `1.12.1` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/stylelint-declaration-strict-value/1.12.1) lists `andyogo` as the publisher. The pinned version `1.12.1` was published at `2026-08-24T18:56:29.569Z` and was 37 days old on 2026-10-01. Its 14-day wait ended at `2026-09-07T18:56:29.569Z`. The OSV lookup dated 2026-10-01 returned no advisory for `stylelint-declaration-strict-value` at `1.12.1`. The dependency tree contains 210 versions, with 77 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement the configured checks for color, fill, stroke, font size, and box-shadow declarations, including shorthand expansion and longhand recursion. That would duplicate a CSS-aware Stylelint rule and its edge cases in repository tooling. The dependency tree contains 210 versions, with 77 versions only reachable through this direct dependency.
