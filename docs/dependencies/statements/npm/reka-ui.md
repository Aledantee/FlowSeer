---
name: reka-ui
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/src/ui/app/UiAppRoot.vue:2` and `frontend/web/src/ui/form/UiSelect.vue:22` import Reka UI primitives. The pinned direct requirement is `reka-ui` at `2.10.5` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/reka-ui/2.10.5) lists `GitHub Actions` as the publisher. The pinned version `2.10.5` was published at `2026-09-21T13:18:23.018Z` and was 9 days old on 2026-10-01. It remains under the 14-day wait until `2026-10-05T13:18:23.018Z`. The OSV lookup dated 2026-10-01 returned no advisory for `reka-ui` at `2.10.5`. The dependency tree contains 43 versions, with 16 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to own accessible dialog, tooltip, popover, select, combobox, and menu primitives, including focus management, keyboard behavior, and positioning. That would duplicate UI interaction infrastructure across the components that currently import Reka UI. The dependency tree contains 43 versions, with 16 versions only reachable through this direct dependency.
