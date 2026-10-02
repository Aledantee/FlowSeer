---
name: "@vue-flow/controls"
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/src/components/topology/TopologyGraph.vue:16` renders the
topology graph controls, and line 22 loads their stylesheet. The direct
requirement is pinned at `1.1.3` in `frontend/web/package.json:26`.

## Why it is safe

The npm registry metadata identifies `braks` as the publisher of version
`1.1.3` ([registry record](https://registry.npmjs.org/%40vue-flow%2Fcontrols)).
The version was published at `2025-08-07T13:41:34.476Z` and was 420 days old
on 2026-10-01. Its 14-day wait ended at `2025-08-21T13:41:34.476Z`. The OSV
lookup dated 2026-10-01 returned no advisory for `@vue-flow/controls` at
`1.1.3`. The npm dependency tree contains 40 versions, with 0 versions only
reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement zoom, pan, fit-view, and control-button
events against Vue Flow's viewport state. A local toolbar would need to track
the same graph lifecycle and keyboard behavior. The npm dependency tree
contains 40 versions, with 0 versions only reachable through this direct
dependency.
