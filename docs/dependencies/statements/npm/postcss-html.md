---
name: postcss-html
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/.stylelintrc.json:6` selects `postcss-html` as the custom syntax for Vue files. The `frontend/web/package.json:15` lint script runs Stylelint over CSS and Vue files. The pinned direct requirement is `postcss-html` at `2.0.0` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/postcss-html/2.0.0) lists `GitHub Actions` as the publisher. The pinned version `2.0.0` was published at `2026-07-31T23:40:58.869Z` and was 61 days old on 2026-10-01. Its 14-day wait ended at `2026-08-14T23:40:58.869Z`. The OSV lookup dated 2026-10-01 returned no advisory for `postcss-html` at `2.0.0`. The dependency tree contains 13 versions, with 7 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to parse Vue single-file component style blocks into the PostCSS syntax that Stylelint consumes, while preserving selectors, declarations, and source locations. That would duplicate a language adapter and make lint parsing an owned maintenance burden. The dependency tree contains 13 versions, with 7 versions only reachable through this direct dependency.
