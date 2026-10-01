---
name: "@fontsource-variable/dm-sans"
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/src/main.ts:12` imports `@fontsource-variable/dm-sans` so the
application loads the DM Sans variable font. `frontend/web/package.json:22`
pins the direct requirement at `5.3.0`.

## Why it is safe

The npm registry metadata identifies `lotusdevshack` as the publisher of
version `5.3.0` ([registry record](https://registry.npmjs.org/%40fontsource-variable%2Fdm-sans)).
The version was published at `2026-07-19T03:49:56.54Z` and was 74 days old
on 2026-10-01. Its 14-day wait ended at `2026-08-02T03:49:56.54Z`. The OSV
lookup dated 2026-10-01 returned no advisory for
`@fontsource-variable/dm-sans` at `5.3.0`. The npm dependency tree contains
1 version, with 0 versions only reachable through this direct dependency.
Source not yet reviewed.

## Why not owned code

FlowSeer would have to package the DM Sans font files, variable-axis metadata,
and CSS font-face declarations. That would add binary assets and font loading
rules to the application for a typeface package already maintained for this
purpose. The npm dependency tree contains 1 version, with 0 versions only
reachable through this direct dependency.
