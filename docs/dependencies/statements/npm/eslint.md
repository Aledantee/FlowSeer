---
name: eslint
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: run
verdict: keep
approved: ""
---

## Why it is required

`frontend/web/package.json:15` runs `eslint .` in the web lint script, which
loads `frontend/web/eslint.config.js`. The direct requirement is pinned at
`10.11.0` in `frontend/web/package.json:48`.

## Why it is safe

The npm registry metadata identifies `eslintbot` as the publisher of version
`10.11.0` ([registry record](https://registry.npmjs.org/eslint)). The version
was published at `2026-09-18T20:15:36.485Z` and was 12 days old on 2026-10-01.
Its 14-day wait ends at `2026-10-02T20:15:36.485Z`, so the pin is still under
the wait. The OSV lookup dated 2026-10-01 returned no advisory for `eslint` at
`10.11.0`. The npm dependency tree contains 79 versions, with 0 versions only
reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to own the JavaScript lint engine, parser integration,
rule execution, diagnostics, and command-line behavior used by the web lint
script. That is a language-tooling product rather than application code. The
npm dependency tree contains 79 versions, with 0 versions only reachable
through this direct dependency.
