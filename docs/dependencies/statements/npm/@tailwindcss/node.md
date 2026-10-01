---
name: "@tailwindcss/node"
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/src/theme/tailwind.test.ts:2` imports `compile` from
`@tailwindcss/node` to compile the repository's Tailwind stylesheet and test
the generated utilities. The direct requirement is pinned at `4.3.3` in
`frontend/web/package.json:44`.

## Why it is safe

The npm registry metadata identifies `GitHub Actions` as the publisher of
version `4.3.3` ([registry record](https://registry.npmjs.org/%40tailwindcss%2Fnode)).
The version was published at `2026-07-16T12:03:45.588Z` and was 77 days old
on 2026-10-01. Its 14-day wait ended at `2026-07-30T12:03:45.588Z`. The OSV
lookup dated 2026-10-01 returned no advisory for `@tailwindcss/node` at
`4.3.3`. The npm dependency tree contains 26 versions, with 0 versions only
reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to parse Tailwind directives, resolve theme inputs, and
generate the same utility output in the stylesheet test. That would duplicate
the Tailwind compiler used by the deployed Vite plugin. The npm dependency tree
contains 26 versions, with 0 versions only reachable through this direct
dependency.
