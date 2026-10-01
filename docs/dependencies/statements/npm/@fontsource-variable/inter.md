---
name: "@fontsource-variable/inter"
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/src/main.ts:11` imports Inter's standard variable stylesheet, and
`frontend/web/.storybook/preview.ts:4` loads Inter for stories. The direct
requirement is pinned at `5.3.0` in `frontend/web/package.json:23`.

## Why it is safe

The npm registry metadata identifies `lotusdevshack` as the publisher of
version `5.3.0` ([registry record](https://registry.npmjs.org/%40fontsource-variable%2Finter)).
The version was published at `2026-07-19T03:50:25.793Z` and was 74 days old
on 2026-10-01. Its 14-day wait ended at `2026-08-02T03:50:25.793Z`. The OSV
lookup dated 2026-10-01 returned no advisory for
`@fontsource-variable/inter` at `5.3.0`. The npm dependency tree contains 1
version, with 0 versions only reachable through this direct dependency. Source
not yet reviewed.

## Why not owned code

FlowSeer would have to package Inter's font files, variable-axis metadata, and
CSS font-face declarations for both the application and Storybook. That would
add binary assets and font loading rules to the web tree. The npm dependency
tree contains 1 version, with 0 versions only reachable through this direct
dependency.
