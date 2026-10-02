---
name: tailwind-variants
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/src/ui/button/UiButton.vue:3` imports `tv` from `tailwind-variants`, and the same variant builder is used by the form, badge, dialog, tab, toast, and spinner components. The pinned direct requirement is `tailwind-variants` at `3.3.1` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/tailwind-variants/3.3.1) lists `GitHub Actions` as the publisher. The pinned version `3.3.1` was published at `2026-08-03T14:07:09.933Z` and was 58 days old on 2026-10-01. Its 14-day wait ended at `2026-08-17T14:07:09.933Z`. The OSV lookup dated 2026-10-01 returned no advisory for `tailwind-variants` at `3.3.1`. The dependency tree contains 3 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement variant selection, compound variants, slots, and class composition for each reusable UI component. That would spread Tailwind-specific composition logic across owned components and make the variant contract harder to change consistently. The dependency tree contains 3 versions, with 0 versions only reachable through this direct dependency.
