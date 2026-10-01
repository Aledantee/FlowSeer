---
name: storybook
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/package.json:18-19` invokes the `storybook` development server and static build, while the stories under `frontend/web/src/ui/` are the component inputs. The pinned direct requirement is `storybook` at `10.6.0` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/storybook/10.6.0) lists `GitHub Actions` as the publisher. The pinned version `10.6.0` was published at `2026-09-02T14:07:30.853Z` and was 28 days old on 2026-10-01. Its 14-day wait ended at `2026-09-16T14:07:30.853Z`. The OSV lookup dated 2026-10-01 returned no advisory for `storybook` at `10.6.0`. The dependency tree contains 144 versions, with 0 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to build a component preview server, story indexing, browser rendering, static export, and the development controls used by the stories. That would make a test and design-review harness part of the repository's owned tooling. The dependency tree contains 144 versions, with 0 versions only reachable through this direct dependency.
