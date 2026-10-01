---
name: "@storybook/vue3-vite"
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/.storybook/main.ts:1,11` selects the Vue 3 and Vite framework,
and `frontend/web/.storybook/preview.ts:1` imports its `Preview` type. The
same adapter is used by the accessibility test at
`frontend/web/src/ui/a11y.test.ts:5` and by the stories at
`frontend/web/src/components/TrafficChart.stories.ts:1`. The direct
requirement is pinned at `10.6.0` in `frontend/web/package.json:43`.

## Why it is safe

The npm registry metadata identifies `GitHub Actions` as the publisher of
version `10.6.0` ([registry record](https://registry.npmjs.org/%40storybook%2Fvue3-vite)).
The version was published at `2026-09-02T14:00:09.25Z` and was 29 days old
on 2026-10-01. Its 14-day wait ended at `2026-09-16T14:00:09.25Z`. The OSV
lookup dated 2026-10-01 returned no advisory for `@storybook/vue3-vite` at
`10.6.0`. The npm dependency tree contains 275 versions, with 44 versions
only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to maintain the Storybook Vue renderer, Vite builder,
preview lifecycle, and story composition used by the workbench and tests. The
275-version run-only tree is large, yet replacing it would reproduce the
framework adapter and its version-specific integration points. The npm
dependency tree contains 275 versions, with 44 versions only reachable through
this direct dependency.
