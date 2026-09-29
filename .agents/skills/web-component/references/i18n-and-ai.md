# i18n and the AI contract

Both are decided in
`docs/architecture/2026-09-28-web-component-contract-direction.md`. Both
land through a migration plan, so first check what exists:

```bash
ls frontend/web/src/i18n frontend/web/src/ai/catalog.ts 2>&1
```

## While the migration has not landed

When `src/i18n/` or `useAiTarget` does not exist yet:

- **Strings.** Collect every user-visible string of the component in one
  `const` object at the top of the script, and expose each through a prop
  that has that default. The migration then moves them mechanically.
- **AI registration.** Register with `v-ai-target` on the root element,
  through `aiTarget()`. See the README's "AI targets" section.
- **Report.** Say that the component awaits the migration.

Do not install `vue-i18n` yourself. The approval covers the migration
plan, not incidental use.

## i18n, once `src/i18n/` exists

- Messages live in `src/i18n/locales/en.json` and `de.json`, under the
  key `ui.<component>.<key>`. Views use `view.<view>.<key>`.
- The component calls `const { t, n, d } = useI18n()`. Each visible
  string is a prop whose default is `t('ui.<component>.<key>')`, so a
  caller can override it.
- Plurals use vue-i18n plural messages, never `count === 1 ? … : …`.
- Numbers use `n()`, and dates and times use `d()` with a named format.
  Relative times, lists, and units use the matching `Intl` API for the
  active locale.
- Never build a sentence from fragments. Word order differs between
  English and German. Use one message with named interpolation.
- German runs about 30% longer. The `LongText` story renders the German
  locale, and the layout must hold.
- Add every key to both locale files in the same change. A test fails on
  a key that is missing from one of them.

## AI contract, once `useAiTarget` exists

- **The `ai` prop.** A component that renders an entity, a value, or an
  action takes an optional `ai` prop (the `aiTarget()` input) and calls
  `useAiTarget(rootRef, () => props.ai)`. Layout-only components take no
  `ai` prop: separators, scroll areas, and skeletons.
- **Highlight.** The registry writes `data-ai-selected` on the
  highlighted element, and one shared rule draws the outline. Never style
  the highlight per component.
- **Agent changes.** A value an agent changed carries
  `data-ai-origin="agent"` until the user next interacts with it.
- **Generative UI.** A component is renderable by an agent only once it
  has a catalog entry in `src/ai/catalog.ts`, with a prop validator.
  - Adding the entry is part of the component's change when an agent
    should be able to render it.
  - Text props are plain strings. No `v-html`.
  - Interactive props are declared intents (a `PageTarget`, or an API
    proposal the user confirms), never functions.
- **Actions.** Components expose no agent operations. Agents act through
  the service API, and the UI shows the result through live sync.

## Tests

- **i18n:** the component renders with the `de` locale, and the
  missing-key test passes.
- **AI:** with an `ai` prop, `registry.list()` includes the target, and
  `highlight(id)` sets `data-ai-selected`. Without the prop, nothing
  registers.
- **Catalog:** a valid tree renders. An unknown component, or an unknown
  or invalid prop, rejects the whole tree.
