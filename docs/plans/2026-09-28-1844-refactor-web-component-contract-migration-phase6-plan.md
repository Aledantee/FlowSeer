---
title: Web Component Contract Migration, Phase 6 - Generative UI Catalog and Renderer - Plan
type: feat
date: 2026-10-05
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: fixes needed
execution: mixed
amends: docs/architecture/2026-09-28-web-component-contract-direction.md
parent: docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-plan.md
---

# Web Component Contract Migration, Phase 6 - Generative UI Catalog and Renderer - Plan

> Implemented. 4 units, 2026-10-05T16:09Z to 2026-10-05T17:14Z. All ten
> components stayed in the catalog. The browser check ran on the
> `UiAiRender` stories and on `AnswerWithComponents` in both themes, both
> locales, and at narrow and wide widths. The running-app step, asking the
> assistant about a device and following the mock answer's button, did not
> run.

## Goal

An AI handler can put real `Ui*` components on screen. An answer carries an
optional component tree, `UiAiRender` validates it against a catalog of ten
components, and `UiAiResult` shows the rendered tree under the answer text.
A tree that fails validation shows one error state and renders none of its
nodes, while the answer text stays. The means is a pure validator in
`frontend/web/src/ai/catalog.ts`, a renderer in `frontend/web/src/ui/ai/`,
and one new field on `AiAnswer`.

Stop condition: if a catalog component cannot be rendered from validated
plain data without the renderer writing markup of its own beyond layout
wrappers, that component leaves the catalog and the plan says so in its
outcome note.

Paths below are relative to `frontend/web/` unless they start with `docs/`.

## Decisions

- The contract's "Agents compose UI from a catalog" and "Agents act
  through the API" sections govern
  (`docs/architecture/2026-09-28-web-component-contract-direction.md`).
- No schema library is added. Validators are hand-written, as the contract
  decided.
- **The tree enters as `AiAnswer.ui`.** `AiAnswer` gains `ui?: unknown`.
  `validateAiAnswer` does not inspect it, and `UiAiRender` validates it at
  render time. Why: the registry calls `validateAiResult` on every snapshot
  and throws on a bad field (`src/ai/registry.ts`, `generateSnapshots`),
  which would replace the whole answer with the result error. The contract
  asks for an error state for the tree. The field is typed `unknown` so no
  caller treats it as checked before `validateAiUiTree` has run. (decided
  by the user, 2026-10-05)
- **Only the navigate intent exists.** An intent whose `type` is not
  `navigate` rejects the tree. Why: the contract says the API rule "has no
  code" until the console has a service API, and a proposal's shape cannot
  be designed before the call it describes. The proposal intent is added
  with that API. (decided by the user, 2026-10-05)
- **The first catalog holds ten components:** `UiCard`, `UiBadge`,
  `UiStatusBadge`, `UiMetricCard`, `UiMeter`, `UiProgress`, `UiSeparator`,
  `UiEmptyState`, `UiAiEntityChip`, and `UiButton`. Tables wait for a need.
  (decided by the user, 2026-10-05)
- **The catalog is data and the renderer owns the component map.**
  `src/ai/catalog.ts` imports no Vue component. `UiAiRender.vue` maps each
  catalog name to its component, and a test asserts both key sets are
  equal. Why: `src/ui/` imports `src/ai/` today and nothing imports the
  other way (`src/ui/ai/context.ts`), and component imports in the catalog
  would close that into a cycle.
- **A node is `{ component, props, children? }` with `props` required**, as
  the contract writes it. A node, an intent, or a target with any other
  key rejects the tree.
- **Validation returns a copy.** `validateAiUiTree` builds new node and
  prop objects from the listed keys only, and the renderer binds the copy.
  Why: the handler keeps a reference to what it yielded, and a copy built
  from an allow-list cannot carry a key the catalog did not name.
- **Catalog lookup uses own keys.** A component name resolves through
  `Object.hasOwn`. Why: `constructor` and `toString` exist on every object
  and are not catalog entries.
- **Text reaches a slot through a catalog prop named `text`.** `UiBadge`
  and `UiButton` take their label in the default slot
  (`src/ui/badge/UiBadge.vue`, `src/ui/button/UiButton.vue`). Their catalog
  entries list a required string `text`, and the renderer passes it as the
  slot's text content. Every other entry uses the component's own props.
- **Only `UiCard` takes children.** `children` on any other component
  rejects the tree. The renderer puts a card's children in its default slot
  inside one layout wrapper.
- **The catalog lists these props and no others.** `ai`, `aiOrigin`, and
  every function-typed prop (`valueText` on `UiMetricCard` and `UiMeter`)
  are absent, so supplying one is an unknown prop.

  | Component | Props (required in bold) |
  |---|---|
  | `UiCard` | `as`: `div`, `article`, `section` |
  | `UiBadge` | **`text`**, `variant`, `size` |
  | `UiStatusBadge` | **`status`**: `Healthy`, `Degraded`, `Offline`. `label`, `size` |
  | `UiMetricCard` | **`label`**, **`value`** (finite number), `unit` |
  | `UiMeter` | **`label`**, **`value`**, `min`, `max`, `unit`, `detail`, `tone` |
  | `UiProgress` | `modelValue` (finite number), `max`, `size`, `variant`, `ariaLabel`, `valueText` (string only) |
  | `UiSeparator` | `orientation`, `decorative` |
  | `UiEmptyState` | **`title`**, `description` |
  | `UiAiEntityChip` | **`entity`** (an `AiEntityRef`), `size` |
  | `UiButton` | **`text`**, **`intent`**, `variant`, `size`: `sm`, `md` |

  Enumerated values are the unions in each component's exported props
  interface, except where the table narrows them. `UiButton` loses `icon`,
  which is sized for a glyph and needs an `ariaLabel`
  (`src/ui/button/UiButton.vue`). A number must pass `Number.isFinite`.
- **A device chip's id must make a page path.** The `UiAiEntityChip` entry
  rejects an `entity` of kind `device` unless `/devices/<id>` passes
  `isPagePath`. Why: the chip navigates there on click
  (`src/ui/ai/UiAiEntityChip.vue`, `handleClick`), so the button's path
  rule would otherwise not cover it.
- **The navigate intent is `{ type: 'navigate', target: { path?, query? } }`.**
  At least one of `path` and `query` is present. `query` is a record of
  strings. `path` must pass a new `isPagePath` exported from
  `src/navigation/page.ts`, true for `/dashboard`, `/devices`, `/clients`,
  `/sites`, `/topology`, and `/devices/<segment>`, where a segment is one
  or more characters other than `/`, `?`, and `#`. It is built from the
  `VIEWS` list that file already holds. Why: these are the routes the
  router defines, and it redirects every other path to `/dashboard`
  (`src/main.ts`, `routes`).
- **Navigation keeps the user's scope.** The renderer calls
  `page.go({ path, query: { ...scopeOf(page.location.value), ...target.query } })`.
  Why: `resolveTarget` carries the whole current query when a target names
  none and replaces it when a target names one (`src/navigation/page.ts`),
  so a bare path would drag a page's filters along and a bare query would
  drop `tenant`. The views build their links the same way
  (`src/DashboardView.vue`, its two `scopeOf` calls).
- **A button without a page does nothing.** `UiAiRender` injects
  `pageContext` with a `null` default, as `UiAiEntityChip.vue` does. With
  no page, a button with an intent renders disabled.
- **A tree is bounded:** at most 64 nodes, at most 4 levels deep, and at
  most 500 characters per string. Why: the tree is untrusted and is
  rendered synchronously.
- **`UiAiRender` takes no `ai` prop.** It is classified `structural` in
  `src/ui/ai/targetContract.test.ts`. Rendered nodes register no target,
  since an agent cannot supply `ai`.
- **The error state is one line of text with `role="alert"`**, from
  `ui.aiRender.error`, with an `errorLabel` prop as the caller override.
  This matches the error line in `UiAiResult.vue`.
- **Each snapshot's tree is validated whole.** A snapshot is a complete
  result (`README.md`, "Snapshot delivery and cancellation"), so a tree
  that is valid only once later nodes arrive is invalid.

## Requirements

1. A valid tree renders its components. Example: `[{ component: 'UiCard',
   props: {}, children: [{ component: 'UiStatusBadge', props: { status:
   'Offline' } }] }]` renders one card holding a badge that reads
   "Offline".
2. An unknown component rejects the whole tree. Example: a tree whose
   second node names `div`, `constructor`, or `__proto__` renders the
   error state and no node, the valid first node included.
3. An unknown prop, an invalid value, or a function rejects the whole
   tree. Examples: `UiButton` with `onClick`, `UiStatusBadge` with
   `status: 'Broken'`, `UiMetricCard` with `value: NaN`, `UiMeter` with
   `valueText: () => ''`, and `UiBadge` with `ai: {}` each render the
   error state.
4. Text is rendered as text. Example: `UiBadge` with `text: '<img src=x>'`
   renders a badge whose `textContent` is that string, and the output
   holds no `img` element.
5. A navigate intent goes through `PageContext.go` and keeps the scope.
   Example: at `/devices?tenant=acme&search=cologne`, clicking a
   `UiButton` with `intent: { type: 'navigate', target: { path:
   '/devices/core-sw-1' } }` calls `go` once with `{ path:
   '/devices/core-sw-1', query: { tenant: 'acme' } }`. A target of
   `{ query: { site: 'berlin' } }` from the same location calls `go` with
   `{ query: { tenant: 'acme', site: 'berlin' } }`. The
   same button with no provided page is disabled.
6. Any other intent or page rejects the tree. Examples: `intent: { type:
   'propose' }`, `target: { path: '//example.org' }`, `target: { path:
   '/settings' }`, `target: {}`, `target: { query: { site: 1 } }`, an
   intent or target with an extra key, a `UiButton` with no `intent`, and
   a `UiAiEntityChip` whose device `id` is `a/b`.
7. A tree over a bound rejects. Examples: 65 nodes, a card nested 5 deep,
   and a 501-character `text`.
8. An answer shows its tree under its text, and a bad tree leaves the text
   standing. Example: `UiAiResult` given `{ type: 'answer', text: 'Two
   switches are offline.', refs: [], ui: [{ component: 'div', props: {} }]
   }` shows the sentence and the tree's error state, and its own state
   stays `done`.
9. The registry passes `ui` through unchanged. Example: a handler that
   yields an answer with `ui: 42` produces a snapshot whose `ui` is `42`,
   and the run does not throw.
10. The error text is in both locales. Example: under `de` the error state
    reads the German message, and the locale-parity test passes.

## Out of scope

- The proposal intent and any confirmation dialog.
- Tables, free text or heading nodes, and a card title. Prose belongs in
  `AiAnswer.text`.
- A tree on `AiSummary`.
- The copy action. `UiAiResultActions` still copies the answer text only.
- Input and trust: the validator reads what an AI handler yields, a
  handler installed through `window.flowseerAi.onRequest`
  (`src/ai/window.ts`). That author is not trusted. The validator's own
  catalog is authored in this repository and is trusted.

## Units

### U1. Catalog, tree types, and the validator

Files: `frontend/web/src/ai/catalog.ts`, `frontend/web/src/ai/catalog.test.ts`, `frontend/web/src/ai/types.ts`, `frontend/web/src/ai/validate.ts`, `frontend/web/src/ai/validate.test.ts`, `frontend/web/src/ai/index.ts`, `frontend/web/src/ai/registry.test.ts`, `frontend/web/src/navigation/page.ts`, `frontend/web/src/navigation/page.test.ts`
After: none
Change: `types.ts` declares `AiUiNode`, `AiUiNavigateIntent`, and
`AiUiIntent`, and `AiAnswer` has `ui?: unknown` with a comment saying who
validates it. `validate.ts` exports `validateEntityRef`. `page.ts` exports
`isPagePath`. `catalog.ts` exports `AI_UI_CATALOG`, a record from component
name to `{ children: boolean, validate(props) }`,
`AI_UI_ERROR_MESSAGE`, the three bounds as constants, and
`validateAiUiTree(value: unknown): AiUiNode[]`, which throws an `Error`
with that message or returns the copy. `index.ts` exports all of them.
Tests: `catalog.test.ts` has one accepting case per catalog component with
every listed prop set, and one rejecting case per example in requirements
2, 3, 6, and 7. It also covers a non-array tree, a node that is not a
plain object, a node with an extra key, missing `props`, `props` that is
an array or `null`, a missing required prop, `children` on `UiBadge`,
`children` on `UiCard` that is not an array, a query-only target that is
accepted, a `query` holding an own `__proto__` key whose copy has an
unchanged prototype, a tree at each bound exactly, and a check that the
returned nodes are not the input objects.
`page.test.ts` (new if absent) covers `isPagePath` for the six accepted
shapes and for `//example.org`, `/devices/a/b`, `/devices/a?b`,
`/devices/`, `/settings`, and an empty string. `validate.test.ts` gains an answer with an invalid `ui` that still
validates. `registry.test.ts` gains requirement 9.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ai frontend/web/src/navigation/page.ts frontend/web/src/navigation/page.test.ts`

### U2. UiAiRender

Files: `frontend/web/src/ui/ai/UiAiRender.vue`, `frontend/web/src/ui/ai/UiAiRender.test.ts`, `frontend/web/src/ui/ai/UiAiRender.stories.ts`, `frontend/web/src/ui/ai/targetContract.test.ts`, `frontend/web/src/ui/index.ts`, `frontend/web/src/i18n/locales/en.json`, `frontend/web/src/i18n/locales/de.json`
After: U1
Change: `UiAiRender` takes `tree: unknown` and `errorLabel?: string`. It
runs `validateAiUiTree` in a computed value and renders either the copy or
the error state, never both. The recursion lives in this one file as a
render function, as `src/ui/combobox/UiCombobox.vue` already holds one,
so no second `Ui*.vue` needs a story. The root carries `data-ai-render`
and is a vertical stack, and a card's children sit in one wrapping row. `text`
becomes the default slot's text. `intent` is removed from the bound props
and becomes a click handler that calls `page.go` with the scope merged in as
the Decisions state, or `disabled` when no page is provided. The component follows the `web-component` skill,
including its browser check.
Tests: `UiAiRender.test.ts` covers requirements 1, 4, and both examples
of 5 from a location holding `tenant` and a page-local key, one rejected
tree from each of requirements 2, 3, 6, and 7 asserting the alert and the
absence of every catalog component's root, the `errorLabel` override, an
empty array rendering no node and no alert, a tree replaced by an invalid
one leaving no node from the first, requirement 10, and the equality of
the component map's keys with `AI_UI_CATALOG`'s. Stories: `AllComponents`
(every catalog component once), `NestedCard`, `NavigateButton` inside a
provided page, `RejectedTree`, and `LongText` with 500-character strings.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui frontend/web/src/i18n`

### U3. Answers render their tree

Files: `frontend/web/src/ui/ai/UiAiResult.vue`, `frontend/web/src/ui/ai/UiAiResult.test.ts`, `frontend/web/src/ui/ai/UiAiResult.stories.ts`, `frontend/web/src/ai/mock.ts`, `frontend/web/src/ai/mock.test.ts`
After: U2
Change: `UiAiResult` renders `UiAiRender` after the answer's text and
entity chips when the answer's `ui` is not `undefined`. The mock handler's
ask answer carries a tree when the first target has an entity of kind
`device`: a card holding a `UiStatusBadge` from `context.health` when that
is one of the three statuses, and a `UiButton` that navigates to
`/devices/<id>`. With no such target the answer has no `ui`.
Tests: `UiAiResult.test.ts` covers requirement 8, an answer with a valid
tree, and an answer without `ui` mounting no `[data-ai-render]`.
`mock.test.ts` covers both mock branches and passes the mock's tree
through `validateAiUiTree`. A story `AnswerWithComponents` is added.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui/ai frontend/web/src/ai`

### U4. Record amendment and README

Files: `docs/architecture/2026-09-28-web-component-contract-direction.md`, `frontend/web/README.md`
After: U3
Change: the direction record gains a dated amendment below the existing
ones. It states the entry point (`AiAnswer.ui`, validated by the
renderer), the ten components, the `text` prop, the navigate intent and
`isPagePath`, the three bounds, and that the proposal intent is not in the
catalog until the service API exists. It also states that `UiAiRender`
takes no `ai` prop because it is a container for agent output and stands
for no entity, value, or action of its own. It cites the landed files and
no plan. The README's "Typed results" section describes `ui` with one
example payload and names the rejection rules. "Snapshot delivery and
cancellation" names `validateAiResult` in place of `isAiResult`, which
does not exist, and says a malformed `ui` shows the tree's error state
without halting the run. The component list names `UiAiRender`, and
"Boundaries and next decisions" loses any line this phase settles.
Tests: none. The verifier's prose and link checks cover both files.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/architecture/2026-09-28-web-component-contract-direction.md frontend/web/README.md`

Waves: U1 | U2 | U3 | U4

The graph is a chain because each unit builds on the previous one's
exports: the renderer on the validator, the result on the renderer, and
the documents on files all three create.

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web docs/architecture/2026-09-28-web-component-contract-direction.md docs/plans
```

Then the browser loop in
`.agents/skills/web-component/references/review.md` for the `UiAiRender`
stories and `UiAiResult`'s `AnswerWithComponents`, in both themes, both
locales, and at narrow and wide widths. In the running app, ask the
assistant about a device and follow the mock answer's button.

## Definition of done

- [x] The verifier is green for every changed path.
- [x] The parent's requirement 5 holds: a tree naming `UiStatusBadge` with
      `status: 'Offline'` renders, and a tree naming `div` or passing
      `onClick` renders the error state.
- [x] The browser check ran, or the outcome note says it did not and why.
- [x] The direction record and the README changed in the same change as
      the code.
- [x] This plan's `status` is set with an outcome note under its title,
      and the parent's `Landed:` line for this phase is filled.
- [x] No requirement or unit label appears in code, comments, or commits.

## Open questions

- Whether a rendered button should be a link instead. A navigate intent is
  a link by meaning, and `src/navigation/AppLink.vue` exists. The plan
  keeps `UiButton` because the catalog decision names it. Unconfirmed.
- The German error text. `src/i18n/locales/de.json` holds the proposed
  "FlowSeer kann diesen Teil der Antwort nicht anzeigen." beside "FlowSeer
  cannot show this part of the answer." Unconfirmed by a German reader.
- `validateAiUiTree` reads each input property once and validates the copy
  it returns (`src/ai/catalog.ts`). The Decisions did not say this. A
  handler's accessor property could otherwise pass the check with one value
  and reach the renderer with another.
- `src/ai/index.ts` also exports `isPagePath` from `src/navigation/page.ts`.
  Whether the AI entry point should re-export a navigation helper is
  unreviewed.
- The mock answer carries no `ui` for a device whose id does not make a page
  path (`src/ai/mock.ts`). The unit text did not name that case.

## Review gaps

Two behavior findings hold the verdict:

- `frontend/web/src/ai/catalog.ts:372`: `validateAiUiTree` and the card's `children` call the input array's own `map`, so an array with an own `map` property, a swapped prototype, or holes returns nodes `validateNode` never saw; fails: a tree array whose own `map` returns `[{ component: 'div', props: {} }]` must throw; class: behavior
- `frontend/web/src/ai/catalog.ts:276`: `copyEntityValue`, `copyQueryValue`, and `copyIntentValue` return the input object when its shape check fails, and a later accessor can repair that object before validation; fails: a `UiButton` whose `intent` has an extra key that a `size` getter deletes must not return the handler's `intent` object; class: behavior

Follow-ups, which do not hold the verdict:

- `frontend/web/src/ai/catalog.test.ts:106`: remove the prop key allow-list check with a valid `intent` on the fixture; fails: `rejects unknown prop`, whose `UiButton` fixture is rejected today for its missing `intent`; class: false test
- `frontend/web/src/ai/catalog.test.ts:109`: delete `optionalString(props.valueText)` for `UiProgress`; fails: a `UiProgress` with `valueText: () => ''`, since the `UiMeter` fixture is an unknown-prop case; class: false test
- `frontend/web/src/ai/catalog.test.ts:118`: remove the prototype check in `isPlainObject`; fails: `Object.assign(new Date(), { component: 'UiSeparator', props: {} })`; class: false test
- `frontend/web/src/ai/catalog.test.ts:120`: remove the node key check; fails: `{ component: 'UiBadge', props: { text: 'Ready' }, extra: true }`; class: false test
- `frontend/web/src/ai/catalog.test.ts:123`: default absent, array, or `null` `props` to `{}`; fails: the same three cases on `UiSeparator`, which has no required prop; class: false test
- `frontend/web/src/ai/catalog.test.ts:254`: set `AI_UI_MAX_DEPTH = 5`; fails: bound cases written with the literals 64 and 65, 4 and 5, 500 and 501; class: convention
- `frontend/web/src/ai/catalog.test.ts:154`: make `copyEntityValue` return its input; fails: a check that the returned `entity` is not the input object; class: gap
- `frontend/web/src/ai/mock.test.ts:1`: pass any `context.health` string through as the badge status; fails: a device target with `health: 'Unknown'` yielding a card with the button only; class: gap
- `frontend/web/src/ui/ai/UiAiResult.vue:349`: change the condition to `v-if="answerResult.ui"`; fails: an answer with `ui: null` showing the tree's error state; class: gap
- `frontend/web/src/ui/ai/UiAiRender.test.ts:91`: render the node beside the alert; fails: an assertion that `[data-ai-render]` has one element child carrying `role="alert"`, since `catalogRoots` matches no `UiBadge` or `UiStatusBadge` root; class: false test
- `frontend/web/src/ui/ai/UiAiRender.test.ts:100`: render the badge beside the card; fails: `.bg-card .flex-wrap` containing "Offline"; class: false test
- `frontend/web/src/ui/ai/UiAiResult.test.ts:171`: let a rejected tree set the result's error state; fails: a mount with a `run` and no `state` prop whose announcement reads the done text, since the test passes `state: 'done'`; class: false test
- `frontend/web/src/ai/index.ts:47`: `export { isPagePath } from '../navigation/page'` has no importer; class: convention
- `frontend/web/src/ai/catalog.ts:146`: `optionalString(x)` directly before `isOneOf(x, ...)` at eleven sites, `Object.hasOwn` before `validateEntity` and `validateIntent`, and the `hasOnlyKeys` half of `catalogEntry` cannot fail; class: convention
- `docs/architecture/2026-09-28-web-component-contract-direction.md:426`: "Buttons use the `navigate` intent" leaves out that a catalog `UiAiEntityChip` navigates by its entity with no declared intent and that a device id must make a page path; class: convention
- `frontend/web/README.md:651`: "The validator rejects" names no file, the mock sentence at `:596` omits the device tree, and the click's query handling is not described; class: convention
- `.agents/skills/web-component/references/i18n-and-ai.md:4`: "The generative UI catalog has not, so check what exists" and the section under it; class: convention
- `frontend/web/src/ui/ai/UiAiRender.stories.ts:117`: `LongText` covers `UiEmptyState` only, while `UiBadge`, `UiButton`, and `UiAiEntityChip` are `whitespace-nowrap` and take 500 characters; class: convention
- `frontend/web/src/ai/catalog.ts:61`: an empty `text` on `UiButton` or `label` on `UiMeter` passes and renders a control with no accessible name; class: hardening
- `frontend/web/src/ui/ai/UiAiEntityChip.vue:60`: a catalog chip calls `go` without the scope merge the button uses, so a device chip keeps page-local keys and a site chip drops `tenant`; class: hardening
