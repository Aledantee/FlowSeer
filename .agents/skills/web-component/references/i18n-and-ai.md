# i18n and the AI contract

Both are decided in
`docs/architecture/2026-09-28-web-component-contract-direction.md`. Target
registration goes through `useAiTarget`. The generative UI catalog is
`src/ai/catalog.ts`, and `UiAiRender` (`src/ui/ai/UiAiRender.vue`) renders
a tree from it.

## i18n

- Messages live in `src/i18n/locales/en.json` and `de.json`, under the
  key `ui.<owner>.<suffix>`. Views use `view.<view>.<key>`.
- The component calls `const { t, n, d } = useI18n({ useScope: 'global' })`.
- Optional text props resolve as `props.text ?? t('ui.<owner>.<suffix>')`
  in computed state or the template so translations react to locale changes.
  Never call `t` in a hoisted `withDefaults` default: Vue's
  `checkInvalidScopeReference` rejects it and a one-time translation freezes
  the locale. Structural defaults stay in `withDefaults`.
- Plurals use vue-i18n plural messages, never ternary expressions.
- Numbers use `n()`, and dates and times use `d()` with a named format.
  Relative times use the matching `Intl` API for the active locale, while
  unit labels and the list separator are messages.
- Never build a sentence from fragments. Word order differs between
  English and German. Use one message with named interpolation.
- German runs about 30% longer. LongText stories supply long content and
  leave story-level `locale` unset in globals. The automated audit mounts
  every story in both English and German, and browser checks inspect the
  layout with the German toolbar selection.
- Add every key to both locale files in the same change. A test fails on
  a key that is missing from one of them.

### Working example

`src/ui/command/UiCommandEmpty.vue` resolves its optional text prop
reactively while exposing a default message and a customization slot:

```vue
<script setup lang="ts">
import { computed } from 'vue'
import { ComboboxEmpty } from 'reka-ui'
import { useI18n } from 'vue-i18n'

export interface UiCommandEmptyProps {
  text?: string
}

const props = defineProps<UiCommandEmptyProps>()

const { t } = useI18n({ useScope: 'global' })
const resolvedText = computed(() => props.text ?? t('ui.commandEmpty.text'))
</script>

<template>
  <ComboboxEmpty class="py-6 text-center text-sm text-muted-foreground">
    <slot>{{ resolvedText }}</slot>
  </ComboboxEmpty>
</template>
```

### View rules

These apply to files under `src/*.vue`, `src/components/`, and
`src/navigation/`. The README's "View messages" section has a worked example
from `src/components/DevicePorts.vue`.

- **Owner naming.** A message is `view.<owner>.<key>`, with one owner per
  view or component (`workspace`, `devicePorts`, `topologyInspector`). A
  word that two owners render lives in `view.common`, so it cannot drift.
  Keys are camelCase.
- **Composables.** Format through `useFormat()` (`src/i18n/format.ts`) and
  name identifiers through `useLabels()` (`src/i18n/labels.ts`). A view
  never calls `toLocaleString`, never calls `toUpperCase` on translated
  text, or an English plural ternary, and never keeps a unit or an
  identifier word in a script constant.
- **Fixture data.** A fixture value typed `string` renders verbatim. A value
  typed as a union of literals is an identifier, and its text is a message.
  Names, addresses, serials, port names, and models carry `translate="no"`.
- **The frozen-constant trap.** A `t()` call in a top-level `const` runs
  once and keeps the locale it saw. Make the table a `computed` or move it
  into the template. A view test that switches the locale on a mounted app
  is the only check that catches it.
- **Check.** `src/i18n/templates.test.ts` fails on a literal text node or a
  static label attribute in any `.vue` file its globs match, so a new file
  under those directories is covered without an edit. It does not read
  `<script>` or bound expressions. Read the script of every file you
  migrate for quoted capitalized words, template literals with English
  text, and ternaries that choose between two plural forms.
- **Executable identifier property.** `unmarkedIdentifiers(root, identifiers)`
  in `src/i18n/testing.ts` verifies that rendered text nodes containing fixture
  identifiers sit inside `translate="no"` elements. The fixture set in
  `src/domain/testing.ts` covers device names, addresses, client hostnames,
  MACs, sites, tenants, serials, models, firmware versions, port names, and
  the brand. The property checks text nodes only, so `aria-label` and `title`
  sit outside it. Tooltips sit inside the property: `UiTooltip` exposes
  `label` and `hint` slots so callers can mark identifier spans with
  `translate="no"` while leaving message text unmarked. Each new surface
  requires an explicit test call.

## AI contract

- **The `ai` prop.** A component that renders an entity, a value, or an
  action takes an optional `ai?: AiTarget`, the resolved target that
  `aiTarget()` returns (`UiAiProps` in `src/ui/ai/context.ts`, `AiTarget` in
  `src/ai/types.ts`, the `aiTarget()` builder in `src/ai/target.ts`). It calls `useAiTarget(anchorRef, () => props.ai)`.
  Layout-only components take no `ai` prop: separators, scroll areas, and
  skeletons. `src/ui/ai/targetContract.test.ts` classifies every semantic
  `Ui*.vue` file and fails on one it does not know, so classify a new
  component there.
- **Native markup.** Markup that no kit component owns uses `UiAiTarget`
  (`src/ui/ai/UiAiTarget.vue`) with `as` or `asChild`. A popup registers its
  content through `PopupAnchor` (`src/ui/popover/popupAnchor.ts`).
- **Highlight.** The registry writes `data-ai-selected` on the
  highlighted element, and one shared rule draws the outline. Never style
  the highlight per component.
- **Agent changes.** A value an agent changed takes
  `aiOrigin?: AiOriginRequest`, the request without its `signal`. The
  component calls `useAiOrigin(anchorRef, () => props.aiOrigin, onAck)`
  (`src/ui/ai/useAiOrigin.ts`), which sets `data-ai-origin="agent"` until the
  user interacts with the value. It emits `aiOriginAcknowledged(requestId)`
  after pointerdown, keydown, input, or change, and the caller clears its own
  origin state on that event. Portalled content forwards to the same
  acknowledgement.
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

- **i18n:** the story audit mounts each story in `en` and `de`. The audit
  asserts zero missing-key, fallback, and parent-scope warnings. German
  pagination renders `Zurück` and `Weiter`.
- **AI:** with an `ai` prop, `registry.list()` includes the target, and
  `highlight(id)` sets `data-ai-selected`. Without the prop, nothing
  registers. With `aiOrigin`, the anchor carries `data-ai-origin="agent"`
  until an interaction emits `aiOriginAcknowledged`. Add the component to
  `src/ui/ai/targetContract.test.ts`.
- **Catalog:** a valid tree renders. An unknown component, or an unknown
  or invalid prop, rejects the whole tree.
