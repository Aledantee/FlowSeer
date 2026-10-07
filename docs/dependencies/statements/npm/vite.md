---
name: vite
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: 2026-10-01
---

## Why it is required

`frontend/web/vite.config.ts:1` imports `defineConfig` from `vite`, and `frontend/web/package.json:11` invokes the Vite development server. The pinned direct requirement is `vite` at `8.3.0` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/vite/8.3.0) lists `GitHub Actions` as the publisher. The pinned version `8.3.0` was published at `2026-09-10T11:30:26.283Z` and was 25 days old on 2026-10-06. Its 14-day wait ended at `2026-09-24T11:30:26.283Z`. The OSV lookup dated 2026-10-06 returned no advisory for `vite` at `8.3.0`. The dependency tree contains 70 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to own the development server, module graph, plugin lifecycle, Vue transformation, CSS processing, and production bundling used by the frontend. That would turn build orchestration into application code and duplicate the tool's integration surface. The dependency tree contains 70 versions, with 0 versions only reachable through this direct dependency.
