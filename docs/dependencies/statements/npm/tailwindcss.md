---
name: tailwindcss
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/src/theme/tailwind.css:2-4` imports Tailwind's theme, preflight, and utility layers. The pinned direct requirement is `tailwindcss` at `4.3.3` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/tailwindcss/4.3.3) lists `GitHub Actions` as the publisher. The pinned version `4.3.3` was published at `2026-07-16T12:03:35.267Z` and was 76 days old on 2026-10-01. Its 14-day wait ended at `2026-07-30T12:03:35.267Z`. The OSV lookup dated 2026-10-01 returned no advisory for `tailwindcss` at `4.3.3`. The dependency tree contains 1 version, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to scan templates, generate utility CSS, provide preflight and theme layers, and preserve Tailwind's class and variant semantics. That would replace a CSS compiler with repository-specific build code and make browser styling behavior owned. The dependency tree contains 1 version, with 0 versions only reachable through this direct dependency.
