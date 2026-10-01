---
name: "@storybook/addon-a11y"
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/.storybook/main.ts:7` enables `@storybook/addon-a11y` for the
component workbench. The direct requirement is pinned at `10.6.0` in
`frontend/web/package.json:40`.

## Why it is safe

The npm registry metadata identifies `GitHub Actions` as the publisher of
version `10.6.0` ([registry record](https://registry.npmjs.org/%40storybook%2Faddon-a11y)).
The version was published at `2026-09-02T13:57:15.272Z` and was 29 days old
on 2026-10-01. Its 14-day wait ended at `2026-09-16T13:57:15.272Z`. The OSV
lookup dated 2026-10-01 returned no advisory for `@storybook/addon-a11y` at
`10.6.0`. The npm dependency tree contains 146 versions, with 0 versions
only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to integrate Storybook's manager and preview APIs with an
accessibility rule runner and report its results in the workbench. That would
duplicate the addon lifecycle and accessibility UI while retaining a large
run-only tree. The npm dependency tree contains 146 versions, with 0 versions
only reachable through this direct dependency.
