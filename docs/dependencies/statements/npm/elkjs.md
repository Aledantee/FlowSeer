---
name: elkjs
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/src/components/topology/layout.ts:1-3` imports ELK's layout API
and worker, and line 16 creates the worker-backed layout engine used by the
topology graph. The direct requirement is pinned at `0.12.0` in
`frontend/web/package.json:29`.

## Why it is safe

The npm registry metadata identifies `GitHub Actions` as the publisher of
version `0.12.0` ([registry record](https://registry.npmjs.org/elkjs)). The
version was published at `2026-07-17T08:07:54.693Z` and was 76 days old on
2026-10-01. Its 14-day wait ended at `2026-07-31T08:07:54.693Z`. The OSV
lookup dated 2026-10-01 returned no advisory for `elkjs` at `0.12.0`. The npm
dependency tree contains 1 version, with 0 versions only reachable through
this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement the layered and rectangular graph layout
algorithms used to position sites, devices, and links. The worker boundary also
keeps the compiled layout engine out of the main UI thread. The npm dependency
tree contains 1 version, with 0 versions only reachable through this direct
dependency.
