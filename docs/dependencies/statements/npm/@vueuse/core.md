---
name: "@vueuse/core"
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved:
---

## Why it is required

No file under `frontend/web/src/` imports `@vueuse/core`. `motion-v` at `2.5.1` declares it as a peer dependency with the range `>=10.0.0` (`frontend/web/pnpm-lock.yaml`, `motion-v@2.5.1`, `peerDependencies`), so the application must install it for `motion-v` to resolve. The pinned direct requirement is `@vueuse/core` at `14.4.0` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/@vueuse/core/14.4.0) lists `GitHub Actions` as the publisher. The pinned version `14.4.0` was published at `2026-07-29T00:31:47.382Z` and was 65 days old on 2026-10-02. Its 14-day wait ended at `2026-08-12T00:31:47.382Z`. The OSV lookup dated 2026-10-02 returned no advisory for `@vueuse/core` at `14.4.0`. The dependency tree contains 28 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

The requirement comes from `motion-v`, which calls into `@vueuse/core` at runtime. Owned code cannot stand in for a peer that another package imports by name, so removing it means removing or replacing `motion-v`. The dependency tree contains 28 versions, with 0 versions only reachable through this direct dependency.
