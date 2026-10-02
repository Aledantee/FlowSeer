---
name: "@vue-flow/background"
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/src/components/topology/TopologyGraph.vue:15` renders the
background layer behind the topology graph. The direct requirement is pinned
at `1.3.2` in `frontend/web/package.json:25`.

## Why it is safe

The npm registry metadata identifies `braks` as the publisher of version
`1.3.2` ([registry record](https://registry.npmjs.org/%40vue-flow%2Fbackground)).
The version was published at `2024-11-13T19:23:40.062Z` and was 686 days old
on 2026-10-01. Its 14-day wait ended at `2024-11-27T19:23:40.062Z`. The OSV
lookup dated 2026-10-01 returned no advisory for `@vue-flow/background` at
`1.3.2`. The npm dependency tree contains 40 versions, with 0 versions only
reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to draw and update the graph background in the same
viewport coordinate system as Vue Flow, including pan, zoom, and theme
behavior. A local layer would duplicate those interactions. The npm dependency
tree contains 40 versions, with 0 versions only reachable through this direct
dependency.
