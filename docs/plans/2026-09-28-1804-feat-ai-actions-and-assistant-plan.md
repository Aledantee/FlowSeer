---
title: AI Actions, Structured Summaries, and the Assistant Panel - Plan
type: feat
date: 2026-09-28
artifact_contract: flowseer-plan/v2
execution: code
amends: docs/plans/2026-09-27-feat-contextual-ai-controls-plan.md
---

# AI Actions, Structured Summaries, and the Assistant Panel - Plan

## Goal

An operator gets AI help where they are looking, from three entry points:

- **Context menu.** Right-clicking a device, client, site, topology item,
  or chart opens a menu of labelled verbs ("Why is this offline?",
  "Summarize this site"). A verb shows an answer-first result anchored to
  that element.
- **Assistant panel.** "Continue in assistant" opens a docked panel that
  already carries the element as context.
- **Summary button.** The dashboard and device pages keep their summary
  placement, rebuilt on the new components.

Summaries and answers render as structured content built from our own
components (tone badge, findings, likely cause with confidence, impact,
entity chips, next steps). They are never prose-only or markdown. Every
result carries a labelled AI marker that explains what was sent, plus
copy, regenerate, and feedback actions.

The handler contract in `frontend/web/src/ai/` changes from returning a
string to returning typed results delivered progressively.

**Stop condition:** a layer-root capture listener cannot hide a
`contextmenu` event from Reka's trigger without breaking the browser's
native menu. In that case the per-element entry needs a different trigger
model and this plan is wrong.

## Decisions

### Surfaces and entry points

- **Surfaces follow the hybrid the evidence favours.** Labelled verbs sit
  on the object, the user triggers them, and each shows an inline
  answer-first result. A docked assistant panel handles follow-up; it is
  closed by default and never opens by itself. Nothing is generated
  without a request.

  Why:
  - NN/g found narrow in-context features easier to use than broad chat
    (https://www.nngroup.com/articles/scope-ai-features/).
  - NN/g also found that users disliked a sidebar that "doesn't know what
    I'm looking at" (https://www.nngroup.com/articles/site-ai-chatbot/).
  - A CHI 2025 study rated proactive AI far more disruptive than AI the
    user invokes (https://arxiv.org/html/2502.18658v4).

- **The floating button is removed.** This covers `UiAiActionLayer`, its
  geometry module, the focus reveal, and Alt+A. The per-element entry is
  the context menu, opened by right-click, the Menu key, or Shift+F10.

  Why: the hover reveal was removed as unhelpful before this plan, and a
  button that chases focus has the same problem.

- **Every AI entry is a verb with a text label.** An icon never stands
  alone. Why: NN/g found that a sparkle by itself is not read as "AI"
  (https://www.nngroup.com/articles/ai-sparkles-icon-problem/).

### Context menu mechanics

- **One `UiContextMenu` trigger wraps the layer's slot.** A capture-phase
  `contextmenu` listener on the layer root resolves the nearest
  registered target. Resolution stops, and the event keeps the native
  menu, in four cases:
  - the event originates in `a[href]`, `input`, `textarea`, `select`, or
    `[contenteditable]`
  - the document has a non-empty text selection
  - no target is found
  - the nearest target has kind `view`

  In each of those cases the listener calls `event.stopPropagation()`
  without `preventDefault()`, so Reka's trigger never sees the event.
  Otherwise the event passes through unchanged.

  Why: `reka-ui@2.10.5` `ContextMenu/ContextMenuTrigger.js` reads
  `disabled` from props synchronously at the start of its handler. A
  reactive `disabled` write made in the capture phase only reaches the
  trigger's props after a render microtask. A dispatched event, as in
  happy-dom and every test, has no microtask checkpoint between
  listeners, so the trigger would read the previous event's value.
  Stopping propagation is synchronous. Scoping the listener to the layer
  root keeps several Storybook canvases independent.

  Nothing in `frontend/web/src` listens for `contextmenu`. Vue Flow's
  pane handler calls `preventDefault` only when `panOnDrag` is an array,
  and `TopologyGraph.vue` does not set it.

- **Unconfirmed: view-level targets are left out of the menu.** They wrap
  whole pages (`DeviceView.vue`, `DashboardView.vue`, `WorkspacePage.vue`,
  `TopologyGraph.vue`), so without this exclusion every right-click in a
  pane would lose "Open link in new tab" and "Copy". Their verbs remain
  reachable through the summary placement and the panel's suggestions.
  Why: it keeps the native menu for everything that is not a specific
  item. This is not decided yet and is repeated under Open
  questions.

- **Shift+F10 and the `ContextMenu` key are handled in the app.** A
  `keydown` listener on the layer root handles them for a focused
  element. It resolves the target the same way, calls `preventDefault()`
  on the keydown, and dispatches a `contextmenu` `MouseEvent` on the
  focused element. The event's `clientX` and `clientY` are the
  bottom-left corner of that element's `getBoundingClientRect()`. Reka
  opens the menu there.

  Why: macOS browsers do not turn Shift+F10 into a `contextmenu` event.
  Preventing the keydown stops Windows and Linux browsers from firing a
  second, native one. With Alt+A gone, the app's own handler is the only
  keyboard route that works on the developer's platform.

- **A long-press opens the AI menu.** A touch or pen long-press on a
  registered item opens its verbs, as a right-click does. A long-press
  elsewhere keeps the browser's behavior. Why: touch parity with the
  mouse path. (decided by the user, 2026-10-04)
- **The Menu key stays native in a field.** The Menu key or Shift+F10 in
  a text field inside a registered item keeps the native menu. On a link
  or any other focusable element inside the item it opens the AI menu.
  Why: a device row's only tab stops are links, so Requirement 2 needs
  them, and a field needs paste and spelling. This narrows the keyboard
  Decision's "resolves the target the same way" for links.
  (decided by the user, 2026-10-04)

- **Focus returns by Reka's own restore.** When the menu or its result
  closes, focus goes to the element that was focused before the menu
  opened, through Reka's focus scope. The layer prevents that restore
  only while a verb hands focus to the result popover, and on popover
  close it focuses that same element. The layer keeps no origin search
  and no close modes. Why: focus handling had defects after each of
  three fixes, and this is the simplest design that meets Requirement 3.
  Focus may land outside the item the pointer opened the menu on.
  (decided by the user, 2026-10-04)

- **Re-plan before more fixing.** The review ended in `rework` after
  five fix rounds across three reviews. The next stage is `plan` on the
  context layer and assistant focus mechanics, started from
  `parked/aiact-review3`, which holds the fixes and the review record.
  The re-plan settles whether focus return exempts the assistant prompt
  on the docked panel. Why: each round left new defects in the same
  mechanisms. (decided by the user, 2026-10-04)

### Result shapes and delivery

- **Results are typed data rendered with `Ui` components.** They are
  never markdown or HTML. All of these types are defined in U1:

  ```ts
  type AiTone = 'ok' | 'warning' | 'critical' | 'unknown'
  type AiSeverity = 'info' | 'warning' | 'critical'
  interface AiEntityRef { kind: 'device' | 'site' | 'client' | 'link' | 'chart'; id: string; label: string }
  interface AiSummary {
    type: 'summary'
    headline: string
    tone: AiTone
    findings: { severity: AiSeverity; title: string; detail?: string; refs: AiEntityRef[] }[]
    cause?: { text: string; confidence: 'low' | 'medium' | 'high'; refs: AiEntityRef[] }
    impact?: { text: string; refs: AiEntityRef[] }
    metrics: { label: string; value: string; tone?: AiTone }[]
    next: { label: string; ref?: AiEntityRef }[]
    sources: AiEntityRef[]
  }
  interface AiAnswer { type: 'answer'; text: string; refs: AiEntityRef[]; summary?: AiSummary }
  type AiResult = AiSummary | AiAnswer
  ```

  Why:
  - A summary built from native components reads as part of the page.
  - The evidence / likely cause / impact shape follows Juniper Marvis
    (https://www.juniper.net/documentation/us/en/software/mist/mist-aiops/topics/concept/marvis-conv-assistant-enhanced.html).
  - Typed data leaves no HTML-injection surface.
  - Metric values are preformatted strings, so no unit logic is needed.

- **A handler returns `Promise<AiResult>` or `AsyncIterable<AiResult>`.**
  Each item an iterable yields is the whole result so far, not a delta.
  The request carries an `AbortSignal`, and Stop aborts it. Every
  snapshot passes a shape check. A snapshot that fails it ends the run
  with the error "The AI returned a result FlowSeer cannot show.", and
  nothing from it is rendered.

  Why: full snapshots let a structured result fill in without a
  patch-merge protocol. A handler that an agent installs is untrusted
  input.

- **Targets and requests change shape.**
  - `AiTarget` gains `view: string` and an optional `entity: AiEntityRef`.
    `aiTarget()` sets `entity` from the input kind:

    | Target kind | Entity |
    |---|---|
    | `device`, `attention-device`, `role-device`, `downlink` | device, with `entityId` as the id |
    | `client` | client |
    | `site` | site |
    | `link` | link |
    | `chart` | chart |
    | `view` | none |

  - `AiTargetSnapshot` is `{ id, kind, view, label, entity?, context }`,
    copied when a request starts.
  - `AiRequest` is
    `{ requestId, action, targets: AiTargetSnapshot[], prompt?, history: AiTurn[], signal }`.
  - `AiTurn` is `{ role: 'user'; prompt: string } | { role: 'assistant'; result: AiResult }`.
  - `AiSeed` is `{ targets: AiTargetSnapshot[]; turns: AiTurn[] }`.

  Why: entity refs and navigation need the raw entity id, which composite
  target ids hide. The panel attaches several targets, and a follow-up
  needs its thread. Breaking the landed single-target shape is intended
  (`AGENTS.md`, pre-stability).

- **Staleness means the element is gone, not that the context changed.**
  A run started with `bound: true` (inline verbs and the summary) ends
  with `AiStaleError` when a target's element is unregistered or replaced
  by a different element. Re-registering the same element with new
  context does not end the run. A run started with `bound: false` (the
  panel) sends the snapshots captured when each chip was added and never
  checks registrations.

  Why: `FleetView.vue` updates throughput every 2.5 s. The dashboard and
  chart targets carry `peak`, so they re-register on every tick. The
  current identity check in `registry.ts` would fail every run that spans
  a tick. Panel chips outlive navigation.

- **`registry.request(targets, options)` returns an `AiRun`:**
  `{ requestId, request: Omit<AiRequest, 'signal'>, snapshots: AsyncIterable<AiResult>, stop() }`.
  Why: `UiAiLabel` must show exactly what was sent, and the live target
  may already have moved on.

- **`window.flowseerAi` keeps its existing calls.** It keeps
  `listTargets`, `highlight`, `clearHighlight`, and `onRequest`, and adds
  `onFeedback(listener)`, whose listener receives
  `{ requestId, rating: 'up' | 'down' }`.

### Verbs, provenance, and the panel

- **`aiActions(target)` in `src/ai/actions.ts` supplies the verbs.** It
  keys on `entity.kind`, the `health` context, and `view`:

  | Target | Verbs |
  |---|---|
  | device with `health` Offline | "Why is this offline?" |
  | device with `health` Degraded | "Why is this degraded?" |
  | every device | "Summarize this device" |
  | site | "Summarize this site" |
  | chart | "Explain this traffic" |
  | client | "Why is this client's signal weak?" |
  | view target, `view` dashboard | "Summarize this dashboard" |
  | view target, `view` device | "Summarize this device" |
  | every target | "Ask about this…" (last) |

  The same table supplies the panel's suggested prompts. Why: views stay
  declarative, and one table keeps the menu and the suggestions
  consistent.

- **Every result shows `UiAiLabel`.** It is a small "AI" text chip that
  opens a popover listing:
  - the targets and context entries from `AiRun.request`
  - the sources
  - the note "AI output can be wrong; check the linked items."

  The label never triggers generation. Why: Carbon's AI label keeps
  explainability apart from actions
  (https://github.com/carbon-design-system/carbon-website/blob/main/src/pages/components/ai-label/usage.mdx).

- **Tone renders as a `UiBadge` whose `variant` maps from `AiTone`:** ok →
  success, warning → warning, critical → danger, unknown → default. The
  label text is "Healthy", "Needs attention", "Critical", or "Unknown".
  Metrics render as a definition list. Why: `UiStatusBadge` accepts only
  device health words and pulses. `UiMetricCard` and `UiMeter` need
  numbers.

- **The assistant panel is a third column in `FleetView.vue`, right of
  the panes.** It opens from a labelled "Assistant" button in the top
  bar, Ctrl/Cmd+I (added to `navigation/shortcuts.ts`), or "Continue in
  assistant". Below the narrow breakpoint it becomes a `UiDialog` with a
  new `side: 'right'` sheet variant. Why: a docked column keeps split
  panes usable, and Datadog Bits uses the same key
  (https://docs.datadoghq.com/bits_ai/bits_chat/).

- **"Add context" is a `UiCommand` list over `registry.list()`.** Why:
  `UiCommand` already provides filtering and keyboard selection.

- **An entity chip links through `PageContext.go`.**
  - devices go to `/devices/<entity.id>`
  - sites go to the dashboard with `site` set
  - other kinds are plain text

  Why: those are the only entity routes `navigation/page.ts` defines.

- **Start after migration phase 1.** This plan starts only after phase 1
  of the web component contract migration (2026-09-28 to 2026-10-05, the
  `Ui*` components and views under `frontend/web/`) has landed. That phase puts overlays on z-index tokens and `@theme`
  keyframes, and moves the tooltip provider into `UiAppRoot`. The new
  components follow the accepted
  `docs/architecture/2026-09-28-web-component-contract-direction.md`
  through the `web-component` skill, including its interim rules for
  strings and AI registration until those migration phases land:
  - `UiContextMenu` uses `z-(--z-overlay)` and the overlay keyframes.
  - `UiAiAssistant`'s narrow-width sheet uses the dialog keyframes.

  Why: both plans edit `FleetView.vue`, `UiPopover.vue`, `UiDialog.vue`,
  `ui/index.ts`, and the README.
- **Keep this as one plan.** Why: every unit lives in the single
  `frontend/web` package, so they form one dependency cluster
  (`references/phases.md`).

## Requirements

1. **Right-clicking a registered item opens its verbs; other right-clicks
   keep the native menu.** Example: on the desktop device row target
   `standalone:devices:device:desktop:d1` with `health: Offline`, a
   cancelable `contextmenu` lists "Why is this offline?", "Summarize this
   device", and "Ask about this…", and is default-prevented after one
   awaited tick. These are not default-prevented and open no menu:
   - the page heading
   - a row's `a[href]`
   - any point inside a view target but outside every item target

2. **Shift+F10 opens the same menu for a focused item.** Example: focus a
   focusable element inside the `d1` row and press Shift+F10. The keydown
   is default-prevented and the menu lists `d1`'s verbs.

3. **A verb shows an anchored, answer-first result with Stop.** Example:
   "Summarize this device" with a handler that yields two snapshots
   renders the headline after the first and the findings after the
   second. The container has `aria-busy="true"` until the iterable ends.
   Stop aborts the signal and keeps the last snapshot, marked "Stopped".
   Focus moves from the closed menu into the result popover. Closing the
   popover returns focus to the element the menu opened from.

4. **Structured results render natively.**
   - `tone` renders as the mapped `UiBadge`.
   - Each finding shows its severity.
   - `cause` shows "Likely cause · medium confidence".
   - Refs render as chips.

   Example: `cause.refs: [{ kind: 'device', id: 'd1', label: 'core-sw-1' }]`
   renders the chip "core-sw-1", which calls `go({ path: '/devices/d1' })`.

5. **A live context update does not end a bound run.** Example:
   re-registering the dashboard view target's element with a new `peak`
   while its summary is pending leaves the run going. Unregistering that
   element ends it with `AiStaleError`, and the result is discarded.

6. **An invalid snapshot shows the error and nothing from that
   snapshot.** Example: `{ type: 'summary', headline: 3 }` shows "The AI
   returned a result FlowSeer cannot show."

7. **Results offer copy, regenerate, and feedback.**
   - Copy writes a plain-text rendering.
   - Regenerate sends a new `requestId`.
   - Thumbs-down on run `r7` delivers `{ requestId: 'r7', rating: 'down' }`
     to `onFeedback` listeners.

8. **`UiAiLabel` lists what was sent and makes no request.** It lists
   exactly the targets and context entries in `AiRun.request`, including
   values that have since changed on screen, and issues no request.
   Example: a summary sent with `peak: 410 Mbps at 14:00` still lists
   that after the chart has moved on.

9. **"Continue in assistant" seeds the panel.** It opens the panel with
   the target snapshot as a chip and the inline result as the first
   assistant turn. A follow-up is sent with `bound: false`, carries that
   chip in `targets`, has a two-entry `history` (the verb's user turn and
   the assistant result), and succeeds after navigating away from the
   target.

10. **The panel starts closed and opens only on a user action.** It lists
    the `aiActions` suggestions for its chips. "Add context" appends a
    registry target. Removing a chip drops it from the next request.

11. **An absent handler shows "AI is unavailable" in every surface.**

12. **State changes are announced once, and the new surfaces pass the
    audit.** Screen readers hear one polite announcement per state
    change: generating, done, stopped, error. The Storybook axe audit
    passes in both themes and covers the open context menu.

## Out of scope

- An AI backend or model provider. The mock handler stays the only
  answerer in the app.
- Markdown or HTML in AI output.
- Charts generated from AI answers.
- Persisting the assistant thread across reloads.
- Verbs in the command palette.

## Units

### U1. Typed result contract, targets, and verbs

Files:
- `frontend/web/src/ai/types.ts`, `registry.ts`, `target.ts`, `window.ts`,
  `mock.ts`, `index.ts`, `validate.ts` (new), `actions.ts` (new)
- `frontend/web/src/ai/*.test.ts`
- `frontend/web/.storybook/aiDecorator.ts`, `aiDecorator.test.ts`
- `frontend/web/src/ui/ai/UiAiSummary.vue`, `UiAiSummary.test.ts`,
  `UiAiSummary.stories.ts`, `UiAiActionLayer.vue`,
  `UiAiActionLayer.test.ts` (adaptation only)

After: none

Change:
- Defines every type in the Decisions.
- `registry.request(targets, { action, prompt?, history?, bound })`
  returns an `AiRun`. It normalizes a promise into a one-snapshot
  iterable and validates every snapshot.
- Bound runs apply the element-identity staleness rule; unbound runs skip
  it.
- The registry's `register` keeps the existing `Registration` object when
  the element is unchanged, and updates only its target.
- The registry exposes `feedback(requestId, rating)`, and the window adds
  `onFeedback`.
- `aiTarget()` fills `view` and `entity`.
- `actions.ts` exports `aiActions` as the table above.
- The mock answers every summary verb with two `AiSummary` snapshots
  built from the target's context (headline, then the rest). It answers
  `ask` with an `AiAnswer` stating it is a placeholder.
- The decorator dispatches by `request.targets[0].id`.
- The two `ui/ai` components and the summary stories compile against the
  new contract with minimal adaptations that later units replace.

Tests:
- Registry:
  - promise normalization and snapshot order
  - an invalid snapshot → error
  - stop aborts the signal
  - a same-element context update keeps a bound run alive
  - unregistering ends a bound run as stale
  - an unbound run survives unregistering
  - `AiRun.request` omits the signal and is frozen at start
  - feedback delivery and unsubscribe
- Validation: every field's wrong type, an unknown enum, and a missing
  `type`.
- Actions: the verbs for offline, degraded, and online devices, a site, a
  chart, a client, and dashboard and device view targets.
- `target.test.ts`: the kind → entity mapping.
- Mock: two snapshots, and the ask placeholder.
- Decorator: dispatch by the first target.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ai frontend/web/.storybook frontend/web/src/ui/ai`

### U2. Native result rendering and the rebuilt summary

Files:
- `frontend/web/src/ui/ai/UiAiResult.vue`, `UiAiEntityChip.vue`,
  `UiAiLabel.vue`, `UiAiResultActions.vue`, `UiAiSummary.vue`, with the
  tests and stories for each
- `frontend/web/src/theme/ai.css`
- `frontend/web/src/ui/index.ts`

After: U1

Change:
- `UiAiResult` renders an `AiRun`'s latest snapshot:
  - the tone badge, the findings list, cause and impact, a metrics
    definition list, next steps, and ref chips
  - a skeleton in the same layout while no snapshot has arrived, using
    the existing shimmer
  - the "Stopped" and error states
  - `aria-busy`, and one polite status region that announces state
    changes, not content

  Empty sections are omitted.
- `UiAiEntityChip` navigates through the injected `PageContext` when one
  exists and is plain text otherwise.
- `UiAiLabel` is the "AI" chip with its `UiPopover` explanation.
- `UiAiResultActions` provides Copy (with a plain-text serializer),
  Regenerate, and thumbs up and down.
- `UiAiSummary` starts as a labelled button ("Summarize <label>"), shows
  `UiAiResult` with Stop, then the actions. It starts runs with
  `bound: true`, keeps a finished result while the target's context
  updates, and resets only when the target id changes.
- The word reveal and `.ai-caret` are removed. Sections fade in once as
  they first appear; a `motion-reduce:` utility turns that off.

Tests:
- `UiAiResult`:
  - every field and the omission of empty sections
  - the tone → variant mapping
  - the "Stopped" and error states
  - one announcement per state
- Chip navigation with and without a page context.
- `UiAiLabel` shows `AiRun.request` context after the live target
  changed, and makes zero handler calls.
- `UiAiResultActions`: the copy text, a new request id on regenerate, and
  the feedback payload.
- `UiAiSummary`: Stop, and that its result survives a same-id context
  update.

CSS reduced-motion rules are not observable in happy-dom. The manual
pass covers them.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui/ai frontend/web/src/theme frontend/web/src/ui/index.ts`

### U3. Context menu primitive

Files:
- `frontend/web/src/ui/context-menu/UiContextMenu.vue`,
  `UiContextMenuItem.vue`, `UiContextMenuSeparator.vue`,
  `UiContextMenu.test.ts`, `UiContextMenu.stories.ts`
- `frontend/web/src/ui/index.ts`
- `frontend/web/src/ui/a11y.test.ts`

After: none

Change:
- A `Ui` wrapper on Reka `ContextMenuRoot`, `ContextMenuTrigger`
  (`as-child`), `ContextMenuPortal`, `ContextMenuContent`,
  `ContextMenuItem`, and `ContextMenuSeparator`, styled like
  `UiDropdownMenu` with semantic tokens.
- Items take a label and an optional `UiKbd` hint.
- The root emits `update:open` and `closeAutoFocus`.
- `a11y.test.ts` gains a `contextmenu` overlay trigger kind, and the
  stories gain an `AccessibilityAudit` story that opens the menu.

Tests:
- A cancelable `contextmenu` opens the menu and is default-prevented
  after an awaited tick.
- Arrow keys and Enter select an item.
- Escape closes the menu and returns focus.
- The audit covers the open menu.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui/context-menu frontend/web/src/ui/index.ts frontend/web/src/ui/a11y.test.ts`

### U4. AI context layer replacing the action layer

Files:
- `frontend/web/src/ui/ai/UiAiContextLayer.vue` (new), with its test and
  story
- `frontend/web/src/ui/ai/UiAiActionLayer.vue`, `UiAiActionLayer.test.ts`,
  `UiAiActionLayer.stories.ts`, `geometry.ts`, `geometry.test.ts` (all
  deleted)
- `frontend/web/src/ui/popover/UiPopover.vue`, `UiPopover.test.ts`
- `frontend/web/src/ai/directive.ts`, `directive.test.ts`
- `frontend/web/src/theme/ai.css`
- `frontend/web/src/ui/index.ts`
- `frontend/web/src/FleetView.vue`
- `frontend/web/.storybook/aiDecorator.ts`, `aiDecorator.test.ts`
- `frontend/web/src/ui/a11y.test.ts`
- `docs/solutions/architecture-patterns/storybook-decorator-owns-document-scoped-state-until-the-last-unmount.md`

After: U1, U2, U3

Change:
- `UiPopover` gains an optional `reference` prop, which it passes to Reka
  `PopoverAnchor` inside `PopoverRoot` when `#trigger` is absent.
- `UiAiContextLayer` wraps its default slot in one `UiContextMenu`. It
  installs the layer-root capture `contextmenu` listener and the
  Shift+F10 / `ContextMenu` key listener from the Decisions.
- The menu lists `aiActions(target)`. A verb starts a bound run and
  opens a `UiPopover` whose `reference` is the target element. The
  popover shows `UiAiLabel`, `UiAiResult`, `UiAiResultActions`, and
  "Continue in assistant".
- Focus order: the menu's `closeAutoFocus` is prevented, the popover's
  content takes focus, and the popover's `closeAutoFocus` focuses the
  element the menu opened from.
- "Continue in assistant" emits `continue` with an `AiSeed` holding the
  target snapshot and the verb's user and assistant turns. "Ask about
  this…" emits `continue` with the target and no turns.
- The directive writes `data-ai-selected` on the highlighted target's
  element as the registry notifies. `ai.css` outlines it with `--ring`.
- `FleetView.vue` renders `UiAiContextLayer` around the panes in place of
  `<UiAiActionLayer />`, and ignores `continue` until U6.
- The decorator wraps each canvas in its own layer. Only the window
  contract and the dispatcher stay document-scoped. The solution doc
  records that listeners scoped to the layer root make per-canvas layers
  safe.

Tests:
- Every case in Requirement 1.
- Shift+F10 on a focused row and on a non-target.
- The verb → pending → result flow.
- A target unmounting mid-run discards the result.
- Focus order through menu → popover → origin.
- The `continue` payload shape.
- The `highlight` outline attribute.
- The a11y audit's component-family check: each family's target opens a
  menu, replacing the `.ai-ask` check.
- Decorator tests without `.ai-ask`.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui frontend/web/src/ai frontend/web/src/theme frontend/web/src/FleetView.vue frontend/web/.storybook docs/solutions/architecture-patterns/storybook-decorator-owns-document-scoped-state-until-the-last-unmount.md`

### U5. Assistant panel

Files:
- `frontend/web/src/ui/ai/UiAiAssistant.vue`, `UiAiAssistant.test.ts`,
  `UiAiAssistant.stories.ts` (new)
- `frontend/web/src/ui/index.ts`

After: U4 (it shares `ui/index.ts` and the audit harness)

Change: A panel with a controlled `open` and a `seed: AiSeed` input. It
has:
- a header with "Assistant" and Close
- a chip row with removable chips and "Add context", which opens a
  `UiCommand` over `registry.list()` and snapshots the chosen target
- suggested prompts from `aiActions`, while the thread is empty
- the thread, with each assistant turn rendered as `UiAiLabel`,
  `UiAiResult`, and `UiAiResultActions`
- a `UiTextarea` prompt: Enter sends, Shift+Enter adds a newline, and
  Stop replaces Send while a run is pending

Runs are unbound, with every chip in `targets` and the earlier turns in
`history`.

Tests:
- A seeded open shows the chip and the first turn.
- A follow-up carries `targets` and the two-entry `history`, and succeeds
  after the seeded target unregisters.
- Removing a chip drops it from the next request.
- Add context appends a chip.
- The suggestions follow the chips.
- Enter versus Shift+Enter.
- Stop.
- The unavailable state.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui/ai frontend/web/src/ui/index.ts frontend/web/src/ui/a11y.test.ts`

### U6. Console wiring and documentation

Files:
- `frontend/web/src/FleetView.vue`, `FleetView.test.ts`
- `frontend/web/src/navigation/shortcuts.ts`
- `frontend/web/src/ui/dialog/UiDialog.vue`, `UiDialog.test.ts`,
  `UiDialog.stories.ts`
- `frontend/web/README.md`

After: U4, U5

Change:
- `shortcuts.ts` gains `assistant: { code: 'KeyI', mod: true }`.
- `UiDialog` gains `side?: 'right'`, which renders a full-height sheet on
  the right edge.
- `FleetView` adds the assistant column and a top-bar "Assistant" button
  whose tooltip advertises the shortcut. The shortcut toggles the panel.
  The context layer's `continue` opens it with the seed. Below the
  narrow breakpoint the panel renders inside the `side: 'right'` dialog.
- The README's "AI targets" section describes:
  - the three surfaces
  - the typed shapes, with one JSON `AiSummary` example
  - snapshot delivery and Stop
  - bound and unbound runs
  - `onFeedback`
  - the context-menu exclusions
  - that the hover and floating-button entries were removed on purpose

Tests:
- `FleetView.test.ts`:
  - the panel starts closed
  - the button and the shortcut open it
  - `continue` opens it seeded
  - the pane split classes persist while it is open
- `UiDialog.test.ts`: the right-sheet variant.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/FleetView.vue frontend/web/src/FleetView.test.ts frontend/web/src/navigation/shortcuts.ts frontend/web/src/ui/dialog frontend/web/README.md`

Waves: U1 U3 | U2 | U4 | U5 | U6

U1 and U3 share no files. U2 needs U1's types. U4 needs all three and
owns `FleetView.vue` and the audit harness until it lands. U5 and U6
follow because they share `ui/index.ts`, the harness, and `FleetView.vue`.

## Verification

- Before wave one: the worktree's hover-removal change and this plan are
  committed, so worker branches start from them.
- Run the verifier over the union of changed paths after each wave.
  Never run `--full`.
- `pnpm build-storybook`, then a manual pass in `pnpm storybook` and
  `pnpm dev` in both themes, at desktop and narrow widths:
  - right-click a device row, a topology node, the traffic chart, a row
    link, and a page heading (the last two keep the native menu)
  - Shift+F10 on a focused row, in macOS Chrome and Safari
  - run a verb, press Stop, and check that reduced motion stills the
    shimmer and the fades
  - continue in the assistant, add context, navigate away, and send a
    follow-up
  - keyboard-only operation throughout

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `frontend/web/README.md` "AI targets" section and the Storybook
      decorator solution doc updated in the same change.
- [ ] No imports of `UiAiActionLayer` or `geometry.ts` remain.
- [ ] This plan's `status` set, with an outcome note under the title.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

- Unconfirmed: view-level targets are excluded from the context menu, so
  page-wide verbs live only in the summary placement and the panel. If
  the user wants them in the menu, a right-click on non-item page areas
  opens the AI menu and loses the native one there.
- Confidence stays as the words low, medium, and high until a real model
  reports calibrated confidence. The implementer adds no percentages.
