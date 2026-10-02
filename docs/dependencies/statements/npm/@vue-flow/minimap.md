---
name: "@vue-flow/minimap"
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/src/components/topology/TopologyGraph.vue:20` renders the
topology minimap when the graph does not fit in the viewport, and line 23
loads its stylesheet. The direct requirement is pinned at `1.5.4` in
`frontend/web/package.json:28`.

## Why it is safe

The npm registry metadata identifies `braks` as the publisher of version
`1.5.4` ([registry record](https://registry.npmjs.org/%40vue-flow%2Fminimap)).
The version was published at `2025-08-15T11:29:26.38Z` and was 412 days old
on 2026-10-01. Its 14-day wait ended at `2025-08-29T11:29:26.38Z`. The OSV
lookup dated 2026-10-01 returned no advisory for `@vue-flow/minimap` at
`1.5.4`. The npm dependency tree contains 40 versions, with 0 versions only
reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to render a scaled graph overview, map the viewport to
the main canvas, and keep nodes and edges synchronized with Vue Flow. A local
minimap would duplicate that coordinate and lifecycle logic. The npm
dependency tree contains 40 versions, with 0 versions only reachable through
this direct dependency.
