---
name: tailwind-merge
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: cut
approved: 2026-10-01
---

## Why it is required

No tracked frontend source file, configuration file, or stylesheet imports `tailwind-merge`. `frontend/web/package.json:33` declares the pinned direct requirement at `3.7.0`, but it is the only direct declaration and has no active importer.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/tailwind-merge/3.7.0) lists `GitHub Actions` as the publisher. The pinned version `3.7.0` was published at `2026-09-12T20:10:15.257Z` and was 18 days old on 2026-10-01. Its 14-day wait ended at `2026-09-26T20:10:15.257Z`. The OSV lookup dated 2026-10-01 returned no advisory for `tailwind-merge` at `3.7.0`. The dependency tree contains 1 version, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

No application source imports `tailwind-merge` directly. Although `frontend/web/pnpm-lock.yaml:8145` records `tailwind-merge` as an optional dependency of `tailwind-variants`, the [Tailwind Variants FAQ](https://www.tailwind-variants.org/docs/faq) documents that the default build includes class merging and a separate `tailwind-merge` install is needed only when application code imports it directly. Keeping it would retain an unused direct package. The dependency tree contains 1 version, with 0 versions only reachable through this direct dependency. If a future importer needs class conflict resolution, that request should add the importer and justify either this package or a focused local implementation.
