---
name: "@storybook/addon-docs"
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/.storybook/main.ts:8` enables `@storybook/addon-docs` for the
component workbench. The direct requirement is pinned at `10.6.0` in
`frontend/web/package.json:43`.

## Why it is safe

The npm registry metadata identifies `GitHub Actions` as the publisher of
version `10.6.0` ([registry record](https://registry.npmjs.org/%40storybook%2Faddon-docs)).
The version was published at `2026-09-02T13:56:43.113Z` and was 29 days old
on 2026-10-01. Its 14-day wait ended at `2026-09-16T13:56:43.113Z`. The OSV
lookup dated 2026-10-01 returned no advisory for `@storybook/addon-docs` at
`10.6.0`. The npm dependency tree contains 160 versions, with 4 versions
only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to maintain Storybook's documentation rendering, docs
blocks, and preview integration for the component stories. The 160-version
run-only tree is a cost, yet owning that integration would add the same
compatibility work to FlowSeer. The npm dependency tree contains 160 versions,
with 4 versions only reachable through this direct dependency.
