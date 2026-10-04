---
name: vue-i18n
ecosystem: npm
required_by:
  - frontend/web/package.json
criteria: deploy
verdict: keep
approved: 2026-10-03
---

## Why it is required

`frontend/web/src/i18n/index.ts:1` imports `createI18n` from `vue-i18n`, and `frontend/web/src/ui/app/UiAppRoot.vue:2` imports `useI18n`. The pinned direct requirement is `vue-i18n` at `11.4.12` in `frontend/web/package.json`.

## Why it is safe

The [npm registry metadata](https://registry.npmjs.org/vue-i18n/11.4.12) lists `GitHub Actions` as the publisher. The pinned version `11.4.12` was published at `2026-09-16T14:36:39.986Z` and was 16 days old on 2026-10-02. Its 14-day wait ended at `2026-09-30T14:36:39.986Z`. The OSV lookup dated 2026-10-02 returned no advisory for `vue-i18n` at `11.4.12`. The dependency tree contains 30 versions, with 5 versions only reachable through this direct dependency. Source not yet reviewed.

## Why not owned code

FlowSeer would have to own message compilation, pluralization rules, interpolation with Vue components and slots, reactive locale state, and localized number formatting. That would turn internationalization runtime infrastructure into owned code across all web components. The dependency tree contains 30 versions, with 5 versions only reachable through this direct dependency.
