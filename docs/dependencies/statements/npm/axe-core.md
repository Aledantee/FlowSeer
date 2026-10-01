---
name: axe-core
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/src/ui/a11y.test.ts:4` imports `axe-core`, and lines 94-96 run
its WCAG-tagged audit against mounted component stories. The direct
requirement is pinned at `4.13.0` in `frontend/web/package.json:47`.

## Why it is safe

The npm registry metadata identifies `GitHub Actions` as the publisher of
version `4.13.0` ([registry record](https://registry.npmjs.org/axe-core)). The
version was published at `2026-08-05T16:53:07.262Z` and was 56 days old on
2026-10-01. Its 14-day wait ended at `2026-08-19T16:53:07.262Z`. The OSV
lookup dated 2026-10-01 returned no advisory for `axe-core` at `4.13.0`. The
npm dependency tree contains 1 version, with 0 versions only reachable through
this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to implement a DOM accessibility rule engine, including
WCAG mappings, ARIA relationships, and violation reporting for the component
stories. That would turn a test helper into a maintained accessibility engine.
The npm dependency tree contains 1 version, with 0 versions only reachable
through this direct dependency.
