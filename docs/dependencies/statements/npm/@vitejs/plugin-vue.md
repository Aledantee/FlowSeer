---
name: "@vitejs/plugin-vue"
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/vite.config.ts:2` imports the Vue Vite plugin, and line 6 adds it
to the application build. The direct requirement is pinned at `6.0.9` in
`frontend/web/package.json:46`.

## Why it is safe

The npm registry metadata identifies `GitHub Actions` as the publisher of
version `6.0.9` ([registry record](https://registry.npmjs.org/%40vitejs%2Fplugin-vue)).
The version was published at `2026-09-14T07:00:40.002Z` and was 17 days old
on 2026-10-01. Its 14-day wait ended at `2026-09-28T07:00:40.002Z`. The OSV
lookup dated 2026-10-01 returned no advisory for `@vitejs/plugin-vue` at
`6.0.9`. The npm dependency tree contains 91 versions, with 0 versions only
reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to transform Vue single-file components, connect their
compiler to Vite's module graph, and preserve hot-module behavior. That would
duplicate the Vue and Vite compatibility work in the plugin. The npm
dependency tree contains 91 versions, with 0 versions only reachable through
this direct dependency.
