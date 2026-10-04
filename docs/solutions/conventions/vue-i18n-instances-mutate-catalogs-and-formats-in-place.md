---
title: A vue-i18n Plugin Instance Mutates Message Catalogs and Number Formats in Place
date: 2026-10-03
last_verified: 2026-10-03
category: conventions
module: frontend/web/src/i18n
problem_type: convention
component: web-console
severity: high
applies_when:
  - "Configuring vue-i18n in Composition mode with imported JSON message catalogs or shared number and datetime format definitions."
  - "Investigating message overrides or format changes in one Vue app or test instance leaking into subsequent instances."
  - "Writing or reviewing cross-instance isolation tests for vue-i18n message catalogs, number formats, and datetime formats."
related_components: [testing]
tags: [vue, vue-i18n, i18n, catalogs, structured-clone, immutability, vitest]
---

# A vue-i18n plugin instance mutates message catalogs and number formats in place

## The situation

When configuring vue-i18n (v11.4.12) in Composition mode (`legacy: false`), a factory creates plugin instances from imported JSON message files (`import en from './locales/en.json'`) and exported number and datetime format objects.

In multi-app setups, Storybook docs pages hosting several canvases, or Vitest suites, each app or test mounts its own plugin. Callers expect separate instances to keep isolated translations and format settings.

`mergeLocaleMessage` and `mergeNumberFormat` write directly into the objects passed at creation. Because ES module imports of JSON and exported configuration records are singletons in the module registry, modifying translations or formats on one plugin instance mutates the shared objects for all subsequent instances in the runtime.

## What is true and why

vue-i18n retains direct references to options objects rather than copying them at instantiation:

- `getLocaleMessages` (`node_modules/vue-i18n/dist/vue-i18n.node.mjs:151-158`) assigns `options.messages` directly to the internal reactive `_messages` ref without copying. `options.numberFormats` is assigned directly to `_numberFormats` (`:296-299`).
- `mergeLocaleMessage(locale, message)` (`:633-645`) executes `@intlify/shared`'s `deepCopy(message, _messages.value[locale])`. `deepCopy` (`node_modules/@intlify/shared/dist/shared.mjs:325-355`) mutates the destination dictionary in place.
- `mergeNumberFormat(locale, format)` (`:674-678`) executes `assign(_numberFormats.value[locale] || {}, format)`, which mutates the target locale's format dictionary in place.
- Passing imported JSON files or exported format definitions directly binds the plugin to the module cache. Merging messages or formats in one instance alters those objects for future instances.
- Factory functions must deep-clone inputs (`structuredClone(en)`, `structuredClone(de)`, `structuredClone(numberFormats)`, `structuredClone(datetimeFormats)`). A shallow spread (`{ ...en }`) protects only the top-level locale keys and leaves nested translation groups shared.
- An isolation test verifying this behavior must call `merge*` before `set*`. `setLocaleMessage` and `setNumberFormat` replace the locale's dictionary slot with a new object. If `set*` runs first, subsequent `merge*` calls modify the replacement object rather than the original catalog, hiding shared-catalog leaks in that locale.
- An isolation test must assert that `merge*` on existing nested keys leaves both a second instance and the imported module objects unaffected across all supported locales.

## Working example

In `frontend/web/src/i18n/index.ts:52-64`, `createWebI18n` passes deep clones of each catalog and format set:

```ts
export function createWebI18n(locale: WebLocale = 'en') {
  return createI18n({
    legacy: false,
    locale,
    fallbackLocale: 'en',
    messages: {
      en: structuredClone(en),
      de: structuredClone(de),
    },
    numberFormats: structuredClone(numberFormats),
    datetimeFormats: structuredClone(datetimeFormats),
  })
}
```

In `frontend/web/src/i18n/i18n.test.ts:150-170`, the isolation test merges into existing nested keys before any call to `setLocaleMessage` replaces the slot:

```ts
const first = createWebI18n('en')
const second = createWebI18n('en')

first.global.mergeLocaleMessage('de', {
  ui: { dialog: { fallbackTitle: 'probe-de-merge' } },
})
expect(first.global.t('ui.dialog.fallbackTitle', 1, { locale: 'de' })).toBe(
  'probe-de-merge',
)
expect(
  second.global.t('ui.dialog.fallbackTitle', 1, { locale: 'de' }),
).toBe('Dialog')
expect(de.ui.dialog.fallbackTitle).toBe('Dialog')
```

## Evidence

- `frontend/web/src/i18n/index.ts:58-62` passes `structuredClone` copies of `en`, `de`, `numberFormats`, and `datetimeFormats` to `createI18n`.
- `frontend/web/src/i18n/i18n.test.ts:150-182` tests catalog isolation across instances and against imported catalogs for `en` and `de`.
- `frontend/web/src/i18n/i18n.test.ts:184-224` tests number format isolation across instances and against exported formats for `en` and `de`.
- `frontend/web/src/i18n/i18n.test.ts:226-260` tests date format isolation across instances and against exported formats for `en` and `de`.
- `node_modules/vue-i18n/dist/vue-i18n.node.mjs:151-158` and `:644` show in-place message catalog reference retention and mutation.
- `node_modules/vue-i18n/dist/vue-i18n.node.mjs:291-294` and `:658-662` show in-place datetime format reference retention and mutation.
- `node_modules/vue-i18n/dist/vue-i18n.node.mjs:296-299` and `:674-678` show in-place format reference retention and mutation.
- `node_modules/@intlify/shared/dist/shared.mjs:325-355` demonstrates `deepCopy` in-place object mutation.
- Commit `2887d581` clones the imported catalogs per instance and adds the catalog isolation test.
- Commit `2b79303a` clones `numberFormats` per instance and adds the number format isolation test.
- Commit `1dd17f9a` moves the German merge ahead of `setLocaleMessage`. Before it, the test passed with the German catalog left uncloned.

## What this does not cover

- Fallback locale chains across root and local scopes.
- Single-instance production deployments (`frontend/web/src/main.ts:32`) where only one Vue app mounts in the process.
- Dynamic locale switching via `locale.value` after mount.
