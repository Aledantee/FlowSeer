---
name: "@vue-flow/core"
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/src/components/topology/TopologyGraph.vue:13-14` uses Vue Flow's
graph component, viewport store, node and edge types, and graph helpers.
`frontend/web/src/components/topology/TopologyLink.vue:8` also imports its
types. The direct requirement is pinned at `1.48.2` in
`frontend/web/package.json:27`.

## Why it is safe

The npm registry metadata identifies `braks` as the publisher of version
`1.48.2` ([registry record](https://registry.npmjs.org/%40vue-flow%2Fcore)). The
version was published at `2026-01-28T11:32:24.404Z` and was 246 days old on
2026-10-01. Its 14-day wait ended at `2026-02-11T11:32:24.404Z`. The OSV
lookup dated 2026-10-01 returned no advisory for `@vue-flow/core` at `1.48.2`.
The npm dependency tree contains 39 versions, with 0 versions only reachable
through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to own the graph store, viewport transforms, node and edge
events, selection behavior, and rendering lifecycle used by the topology view.
That is the graph engine's central responsibility and would create a second
implementation beside the Vue component model. The npm dependency tree
contains 39 versions, with 0 versions only reachable through this direct
dependency.
