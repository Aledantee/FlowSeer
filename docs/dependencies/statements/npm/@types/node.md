---
name: "@types/node"
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/tsconfig.json:17` includes the `node` type library for the Vite,
test, and build configuration included by that project. The direct requirement
is pinned at `22.20.4` in `frontend/web/package.json:47`.

## Why it is safe

The npm registry metadata identifies `types` as the publisher of version
`22.20.4` ([registry record](https://registry.npmjs.org/%40types%2Fnode)). The
version was published at `2026-09-19T00:12:01.551Z` and was 12 days old on
2026-10-01. Its 14-day wait ends at `2026-10-03T00:12:01.551Z`, so the pin is
still under the wait. The OSV lookup dated 2026-10-01 returned no advisory for
`@types/node` at `22.20.4`. The npm dependency tree contains 2 versions, with
0 versions only reachable through this direct dependency. Source not yet
reviewed.

## Why not owned code

FlowSeer would have to maintain declarations for Node's built-in modules,
globals, and version-specific APIs used by the web toolchain. Keeping those
declarations aligned with Node releases is the package's purpose. The npm
dependency tree contains 2 versions, with 0 versions only reachable through
this direct dependency.
