---
name: "@storybook/addon-themes"
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/.storybook/main.ts:6` enables the addon, and
`frontend/web/.storybook/preview.ts:3` imports its theme decorator. The direct
requirement is pinned at `10.6.0` in `frontend/web/package.json:44`.

## Why it is safe

The npm registry metadata identifies `GitHub Actions` as the publisher of
version `10.6.0` ([registry record](https://registry.npmjs.org/%40storybook%2Faddon-themes)).
The version was published at `2026-09-02T13:56:19.936Z` and was 29 days old
on 2026-10-01. Its 14-day wait ended at `2026-09-16T13:56:19.936Z`. The OSV
lookup dated 2026-10-01 returned no advisory for `@storybook/addon-themes` at
`10.6.0`. The npm dependency tree contains 146 versions, with 0 versions
only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement Storybook theme decorators, toolbar state,
and document attribute updates for the light and dark previews. That would
duplicate an integration whose behavior depends on Storybook's preview
lifecycle. The npm dependency tree contains 146 versions, with 0 versions only
reachable through this direct dependency.
