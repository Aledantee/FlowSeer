---
name: "@tailwindcss/vite"
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/vite.config.ts:3` imports the Tailwind Vite plugin, and line 6
installs it in the application build. The direct requirement is pinned at
`4.3.3` in `frontend/web/package.json:24`.

## Why it is safe

The npm registry metadata identifies `GitHub Actions` as the publisher of
version `4.3.3` ([registry record](https://registry.npmjs.org/%40tailwindcss%2Fvite)).
The version was published at `2026-07-16T12:04:06.316Z` and was 77 days old
on 2026-10-01. Its 14-day wait ended at `2026-07-30T12:04:06.316Z`. The OSV
lookup dated 2026-10-01 returned no advisory for `@tailwindcss/vite` at
`4.3.3`. The npm dependency tree contains 107 versions, with 13 versions
only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to connect Tailwind's stylesheet scanning and compilation
to Vite's module graph, watch mode, and build hooks. That would duplicate a
compiler integration that the application uses in production. The npm
dependency tree contains 107 versions, with 13 versions only reachable through
this direct dependency.
