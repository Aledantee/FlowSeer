---
title: AI Actions, Structured Summaries, and the Assistant Panel - Plan
type: feat
date: 2026-09-28
artifact_contract: flowseer-plan/v2
execution: code
amends: docs/plans/2026-09-27-feat-contextual-ai-controls-plan.md
---

# AI Actions, Structured Summaries, and the Assistant Panel - Plan

> U1 to U6 are on `main` (`7b595a57`, `3a6d2a08`, `708b4029`, `c9d0ed39`,
> `86f6a732`, `6b24f0b4`, `283e6458`). U7 to U11 are the open work,
> planned on 2026-10-07 from `parked/aiact-review3`.

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

**Stop condition:** a `contextmenu` event that a script dispatches on the
layer's own trigger element does not open Reka's menu at the event's point
in a real browser. Every entry path opens the menu that way, so the layer
would need a menu it can open at a point directly and this plan is wrong.

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

- **The layer resolves a target before Reka sees anything.**
  `UiAiContextLayer` renders one `UiContextMenu` whose trigger is an
  empty, hidden element the layer owns, outside its slot. Listeners on
  the layer root take the three gestures: a `contextmenu` event, the
  Menu key or Shift+F10, and a touch or pen long-press. Each resolves a
  target first. Without a target the listener returns and the event
  stays as the browser made it. With one, it calls `preventDefault()` on
  the native event and opens the menu by dispatching one `contextmenu`
  `MouseEvent` on its trigger, at the gesture's point.

  Resolution finds no target, and the event keeps the native menu, in
  four cases:
  - the event originates in `a[href]`, `input`, `textarea`, `select`, or
    `[contenteditable]`
  - the document has a non-empty text selection
  - no target is found
  - the nearest target has kind `view`

  Why: `reka-ui@2.10.5` keeps the menu's `open` in a private ref
  (`ContextMenu/ContextMenuRoot.js:35`), and only the trigger's
  `contextmenu` handler sets the point the menu opens at
  (`ContextMenu/ContextMenuTrigger.js:50-65`). A dispatched event is the
  one way to open it at a point. The first plan wrapped the slot in the
  trigger and hid excluded events from it with `stopPropagation()`. A
  trigger around every pane also arms Reka's long-press timer on every
  touch (`ContextMenuTrigger.js:67-75`) and writes
  `-webkit-touch-callout: none` and `pointer-events: auto` on the
  wrapper (`ContextMenuTrigger.js:95-98`). `parked/aiact-review3`
  answers those with a 2147483647 ms press delay, a `WeakSet` that marks
  the layer's own events, and a function that rewrites the wrapper's
  style (`UiAiContextLayer.vue:2`, `:476`, `:562` on that branch). A
  trigger the layer owns receives only what the layer sends, so none of
  the three is needed.

  Nothing else in `frontend/web/src` listens for `contextmenu`. Vue
  Flow's pane handler calls `preventDefault` only when `panOnDrag` is an
  array, and `TopologyGraph.vue` does not set it.

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
  on the keydown, and opens the menu through the layer's trigger. The
  dispatched event's `clientX` and `clientY` are the bottom-left corner
  of the focused element's `getBoundingClientRect()`. Reka opens the
  menu there.

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

### What the fix rounds showed

`parked/aiact-review3` holds four groups of fix merges, one worker per
file group in each (`aiact-fix-*`, `aiact2-fix-*`, `aiact2-fix2-*`,
`aiact3-fix-*`). The Decision above and the state file count five rounds.
Eight mechanisms were fixed in at least three of the four groups, each
time in code an earlier round had already fixed. Each gets a Requirement
a test can fail on.

| Mechanism | Fix commits, in round order | Requirement |
|---|---|---|
| Which target a gesture resolves to | `f60fd850`, `1c6761dc`, `d55c63e1`, `ac6c46d0` | 13 |
| How a gesture opens the menu | `f60fd850`, `1c6761dc`, `d55c63e1` | 14, 15, 16 |
| Where focus goes when the menu or its result closes | `f60fd850`, `1c6761dc`, `d55c63e1`, `ac6c46d0` | 17 |
| Which run a surface shows | `13556681`, `f60fd850`, `8468275c`, `1c6761dc`, `72ad952a`, `1813e0b2`, `f461f19a` | 18 |
| Focus when one control replaces another | `1a55be06`, `aa2f43f5`, `d55c63e1`, `f461f19a`, `3c372c64` | 19 |
| Focus when the assistant opens and closes | `72ad952a`, `1813e0b2`, `3c372c64` | 20 |
| What a regenerated turn sends | `72ad952a`, `1813e0b2`, `3c372c64` | 21 |
| How a run ends in the registry | `427a6d77`, `1bf44b27`, `edb49a57` | 22 |

The layer gave every console event to one Reka trigger and then undid
what the trigger does. The layer popover, the summary, and the panel
each kept a run and its focus by hand, so a fix in one left the same
defect in the next. The focus tables took their expected column from the
code. The last one expects focus back on the pre-menu element after the
user has clicked another button (`returns focus after
$path/$originKind/$close`, `UiAiContextLayer.test.ts:1915` on that
branch).

That branch's copy of this plan lists 82 gap and convention items under
`## Review gaps`. `f9d4183d` counts 13 behavior and false-test findings
open in the last round's own fixes, and the record does not list them.

### Re-planned mechanics

- **Resolution is a pure function.** `src/ui/ai/contextTarget.ts`
  exports `resolveContextTarget(gesture, element, scope)`. `gesture` is
  `pointer` (a right-click or a long-press) or `key`. `scope` carries
  the layer root, the registry, and whether text is selected. The rules
  apply in order:
  1. Selected text: no target.
  2. `pointer` inside `a[href]`, `input`, `textarea`, `select`, or
     `[contenteditable]`: no target. `key` inside the last four: no
     target.
  3. The nearest registered ancestor decides, the element included. A
     kind other than `view` is the target.
  4. `key` only, when rule 3 found nothing or a view: the focused
     element resolves to the one registered target it contains, when it
     is a tab stop and contains exactly one of a kind other than `view`.
     An explicit `tabindex` decides what a tab stop is: 0 or more.
     Without one, an enabled native control is a tab stop.
  5. Otherwise: no target.

  Why: every round changed resolution while it was tangled with events
  and timers. A function of its inputs is tested over every row without
  a menu. Rule 4 is the record's: a focused tab stop can wrap its one
  registered element (`1c6761dc`), and a pane focused by script through
  `tabindex="-1"` must not open its only item (`ac6c46d0`).

- **The layer times the long-press.** A touch or pen `pointerdown` that
  resolves a target starts one 700 ms timer, Reka's default
  (`ContextMenuRoot.js:16`). A `pointermove`, `pointerup`,
  `pointercancel`, or `contextmenu` clears it, as Reka's trigger does
  (`ContextMenuTrigger.js:57-81`). When it fires, the layer resolves the
  same element again and opens the menu at the `pointerdown` point only
  if a target still resolves. Why: the long-press Decision wants a
  long-press on an item only, and Reka's timer starts on any touch
  inside its trigger. Text can become selected and a target can
  unregister during the delay, so the `pointerdown` result only arms the
  timer.

- **The menu stays modal, and the layer root takes pointer events while
  it is open.** Why: a modal menu sets `pointer-events: none` on `body`
  (`DismissableLayer/DismissableLayer.js:87-88`), so a right-click on a
  second item would land on the document root. Reka's trigger carries
  `pointer-events: auto` for that case (`ContextMenuTrigger.js:95-98`),
  and the layer sets it on its root only while its menu is open. A modal
  menu also restores focus after every close, which is the focus-return
  Decision as written. `ContextMenuContent.js:117-125` skips the restore
  only when `modal` is false.

- **The layer keeps one focus variable.** At each menu open it records
  `document.activeElement` as the origin. Focus inside its own menu or
  result leaves the earlier origin in place, and `body` records none. A
  verb prevents the menu's `closeAutoFocus`, and Reka's mount autofocus
  moves focus into the result (`FocusScope/FocusScope.js:92-110`). The
  layer focuses the result's content element itself when focus is still
  outside it once the menu's scope has unmounted. When the result
  closes, the layer prevents `closeAutoFocus` and focuses the origin if
  it is still connected.

  Why: the focus-return Decision needs "that same element" when the
  result closes, and Reka's focus scope does not expose the element it
  recorded (`FocusScope.js:108`). The consumer's handler runs before
  Reka's own (`Popover/PopoverContentNonModal.js:122-130`).

- **A focus test reads its expected element from Reka.** For a menu
  close without a verb, the test runs the same gesture and close on a
  bare `UiContextMenu` and expects the same `document.activeElement`.
  Why: the Decision says the layer adds nothing there. The parked
  table's hand-written column passed while focus left a control the
  user had clicked.

- **One run owner serves every result surface.** `useAiRun` in
  `src/ui/ai/useAiRun.ts` holds the state, the latest snapshot, the
  request as sent, and the error kind for one surface.
  - Starting a run stops the one before it and clears its result before
    the request is made.
  - Only the latest run writes state.
  - Stop keeps the last snapshot.
  - A stale ending resets the surface.
  - Disposing the scope stops the run, and nothing is written after.
  - The error is a kind. The surface translates it when it renders, so
    a locale switch changes the shown text.

  Why: on `main` four files iterate snapshots by hand (`UiAiResult.vue`,
  `UiAiSummary.vue`, `UiAiContextLayer.vue`, `UiAiAssistant.vue`). The
  rounds fixed the same late write, missing abort, and stored
  translation in each.

- **One focus keeper serves every result surface.**
  `focusIsWithin(root)` in `src/ui/ai/focusWithin.ts` is true when the
  root contains the focused element, or holds the trigger whose
  `aria-controls` names the content the focused element sits in
  (`Popover/PopoverTrigger.js:39`). `useFocusKeeper` in the same file
  remembers the last element for which that held. It watches removals
  under the root and under the portalled content that holds that
  element. When the element has left the DOM and focus has dropped to
  `body`, the keeper picks a successor in this order:
  1. An enabled element with the same `data-focus-slot` value, looked
     for outward from where the element was: its former parent first,
     then each ancestor up to the root. An earlier turn's Regenerate
     therefore never wins over the one beside the Stop that left.
  2. The first tab stop under the root. This is where focus goes when
     the element had no slot or sat in portalled content that left with
     its trigger, such as a chip in the summary's label popover when
     the target id changes.
  3. The root.

  The keeper does nothing once its root is detached. Controls that
  replace each other share a slot: Stop and Regenerate, Send and Stop, a
  chip's Remove and "Add context".

  Why: Reka refocuses the container of a removed element only inside a
  trapped scope (`FocusScope.js:69-78`), and neither the result popover
  nor the docked panel is trapped. `parked/aiact-review3` hands focus
  over by selector in three files (`focusResultElement`,
  `focusAssistantControl`, `focusControl`). A non-modal popover that
  unmounts with its trigger focuses the detached trigger and prevents
  any later restore (`Popover/PopoverContentNonModal.js:122-127`), so
  the keeper's move in the removal callback is the last one.

- **Verbs are ids.** `aiActions` returns `{ id }` entries, and a
  surface shows `ui.aiAction.<id>` from the catalogs. The table under
  "Verbs, provenance, and the panel" gives the English messages. Why:
  the component contract allows no literal text, and a request whose
  `action` is a translated label changes with the locale (`427a6d77`).

- **The panel focuses its prompt on open, and its close moves focus to
  the Assistant button only when the panel held it.** `focusIsWithin`
  on the panel root decides "held". Why: the control that opened the
  panel can be a menu item that no longer exists, and overlay rule 5 in
  `.claude/skills/web-component/references/overlays-and-motion.md`
  sends focus to a defined element then. A close that found focus in
  the panes has nothing to restore.

- **The rest of the parked branch is ported by file group.** `main`
  holds none of its fixes. Two hunks are superseded. `96e69273` deleted
  the file of the `directive.test.ts` hunk. The Storybook request
  wrapper of `1bf44b27` and `2ab6fbe8` answers a request with the asking
  canvas's handler, and the record accepted a day later routes by the
  canvas that contains the registered element
  (`docs/architecture/2026-10-05-1251-web-ai-target-lifecycle-direction.md`,
  Consequences, and `.storybook/aiDecorator.ts:44-55`). U7, U9, and U11
  port the core contracts, the result components, and the panel onto
  today's files. Why: a merge of the branch conflicts in 23 files
  (`git merge-tree --write-tree --name-only main
  parked/aiact-review3`). After the fork `main` moved registration to
  `useAiTarget`, gave the components the `ai` and `aiOrigin` props, and
  added the component-tree renderer.

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
  - `AiSeed` is
    `{ targets: AiTargetSnapshot[]; turns: AiTurn[]; request?: Omit<AiRequest, 'signal'> }`.
    `request` is what the seeding verb sent, and Requirement 21 reads it.

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

13. **One function resolves every gesture.** Example:
    `resolveContextTarget('pointer', span, scope)` for a `span` inside
    the `d1` row's link returns nothing, and
    `resolveContextTarget('key', link, scope)` for that link returns
    `d1`. A focused `main` with `tabindex="-1"` around one row returns
    nothing for `key`.

14. **A gesture without a target leaves its event alone.** Example: a
    right-click on the page heading is not default-prevented when
    `dispatchEvent` returns, a `contextmenu` listener on `document`
    still receives it, and the layer's trigger receives no event. A
    touch `pointerdown` there arms no timer.

15. **A gesture with a target opens one menu for it, whatever is
    open.** Example: right-click `d1`, then right-click `d2` while
    `d1`'s menu is open, each as a `pointerdown` followed by a
    `contextmenu`. One menu is open and lists `d2`'s verbs. Each
    `contextmenu` is default-prevented when `dispatchEvent` returns.

16. **One long-press opens once, and Reka's trigger styles stay on the
    layer's trigger.** Example: a touch `pointerdown` on `d1`, a
    `contextmenu` on it 500 ms later, and timers advanced to 1,400 ms
    produce one `update:open` with `true`. The layer writes no inline
    `-webkit-touch-callout` on its root or its slot, and an inline
    `pointer-events` on its root only while the menu is open.

17. **Focus follows one table.**

    | Close | Focus afterwards |
    |---|---|
    | The menu closes without a verb | where a bare `UiContextMenu` leaves it |
    | A verb is chosen | inside the result popover |
    | The result closes by Escape, by its Close button, or because its target left | the origin, when it is still connected |
    | The result closes on a pointer press outside it | the origin, as the Decision is written (Open question 2) |
    | "Ask about this…" or "Continue in assistant" | set by Open question 1 |

    Example: focus the `d1` link, press Shift+F10, choose "Summarize
    this device", and press Escape in the result. Focus is on the link.

18. **A surface shows only its latest run, and every other run is
    aborted.** Example: start a run, receive one snapshot, regenerate,
    then let the first provider yield again. The surface shows nothing
    from the first run, and the first request's signal is aborted.
    Unmounting the surface aborts the second run, and no state changes
    afterwards.

19. **A state change keeps focus inside a surface that had it and
    leaves outside focus alone.** Example: Stop is focused in the result
    popover when the run ends. Focus is on Regenerate. With focus on a
    button outside the popover, the same ending leaves it there.

20. **The assistant takes focus when it opens and gives it back only if
    it held it.** Example, at the docked and the narrow width: Ctrl/Cmd+I
    with focus on a device row focuses the prompt, and Close then puts
    focus on the Assistant button. With focus moved to the main pane
    first, Ctrl/Cmd+I closes the panel and focus stays in the pane. A
    panel opened by "Continue in assistant" focuses its prompt at the
    narrow width. At the docked width Open question 1 decides.

21. **A regenerated turn resends its own request under a new id.**
    Example: the seeded "Summarize this device" turn regenerates with
    the seed's action and targets and no `prompt`. A typed follow-up
    regenerates with its prompt and the turns before it as `history`.

22. **A run ends once, however it ends.** Example: Stop on an iterable
    whose `next()` never settles ends the iteration without another
    snapshot, calls the iterator's `return()`, aborts the provider's
    signal, and leaves no abort listener and no registry subscriber.
    Unregistering a bound run's target does the same and rejects with
    `AiStaleError`.

## Out of scope

- An AI backend or model provider. The mock handler stays the only
  answerer in the app.
- Markdown or HTML in AI output.
- Charts generated from AI answers.
- Persisting the assistant thread across reloads.
- Verbs in the command palette.
- A hostile result object. Validation reads what a handler in the page
  returns, and that handler is script in the page, so it can render
  anything without the validator. Validation and the registry's clone
  defend against malformed data. An accessor or a Proxy on the handler's
  own objects is outside the contract
  (`docs/architecture/2026-09-28-web-component-contract-direction.md`,
  the generative catalog rules, and `99542216`).
- The 13 findings the last round left open. The record gives their
  count only, so the next review judges the ported code as new work.
- Merging `parked/aiact-review3`. The units name what they take from
  it.

## Units

### U7. Core contracts and run endings

Files:
- `frontend/web/src/ai/types.ts`, `actions.ts`, `registry.ts`,
  `validate.ts`, `mock.ts`, `index.ts`
- `frontend/web/src/ai/*.test.ts`
- `frontend/web/.storybook/aiDecorator.ts`, `aiDecorator.test.ts`
- `frontend/web/src/i18n/locales/en.json`, `de.json`
- `frontend/web/src/DashboardView.vue`, `WorkspacePage.vue`,
  `FleetView.test.ts`
- `frontend/web/src/ui/ai/UiAiContextLayer.vue`, `UiAiAssistant.vue`,
  `UiAiSummary.vue`, with their tests and stories (adaptation only)
- the stories and tests under `frontend/web/src` that `vue-tsc` reports
  once `view` is required, among them
  `frontend/web/src/components/TrafficChart.stories.ts`,
  `TrafficSparkline.stories.ts`, and
  `frontend/web/src/ui/table/UiTable.stories.ts`
- `docs/solutions/architecture-patterns/storybook-decorator-owns-document-scoped-state-until-the-last-unmount.md`

After: none

Change:
- `aiActions` returns `AiAction[]` keyed on `entity.kind`, and both
  catalogs gain `ui.aiAction.<id>`.
- `AiTarget.view` is required. `AiRequest` loses `kind`, `targetId`, and
  `context`. A handler returns no string. `AiSeed` gains `request`, the
  request without its signal. `index.ts` exports `AiInvalidResultError`.
- The registry ends a run as Requirement 22 says. It races each provider
  read against the abort, validates a value before it yields it, calls
  the iterator's `return()`, and removes its abort listener and its
  registry subscriber on every ending. A stopped run that then loses its
  target ends quietly.
- Each feedback listener gets its own frozen payload, and one that
  throws does not stop the next.
- Dashboard and site targets carry a raw `healthStatus` beside the
  localized health line, so the mock's tone does not change with the
  locale.
- The decorator keeps today's dispatch, by the canvas that contains
  the registered element. Its story target gains `view`, and the
  solution doc's text and excerpts match `aiDecorator.ts`.
- The three `ui/ai` callers compile against the new shapes with the
  least change. U9, U10, and U11 replace that code.

From `parked/aiact-review3`: port `427a6d77`, `1bf44b27`, and
`edb49a57` for `src/ai` and the views. Rewrite the `registry.ts` and
`validate.ts` hunks on today's files, which gained `AiTargetElement`,
`cloneAiTarget`, and the component-tree clone after the fork
(`dac8c038`, `ec48d614`, `99542216`). Keep that clone as it is. Drop
the `directive.test.ts` hunk, and the decorator's request wrapper and
canvas assistant from `1bf44b27` and `2ab6fbe8`. The record's items
under `src/ai/` name the cases still missing. Its `.storybook/` item
asks for the superseded routing and is closed by the direction record.

Tests:
- `registry.test.ts`: one generated table for Requirement 22. Its axes
  are the ending (completes, provider error, invalid snapshot, Stop,
  target unregistered, target replaced by another element), the provider
  (promise, iterable), its timing (settles, never settles, settles after
  the end), and `bound`. Cells that cannot occur are left out by name,
  and the count of executed rows is asserted as a literal
  (`docs/solutions/conventions/a-combinatorial-table-count-guard-must-assert-a-literal.md`).
  Each row asserts the consumer's outcome, no snapshot after the end,
  the provider's signal, the `return()` call, and zero listeners left.
- `validate.test.ts`: `cause: null`, `impact: null`, `summary: null`, a
  null ref, and `findings: [null]` each throw `AiInvalidResultError`. An
  unknown key is absent from the validated result.
- `actions.test.ts`: the ids for every row of the verb table. A target
  with no entity and a kind other than `view` returns only `ask`.
- `aiDecorator.test.ts`: two mounted canvases. A request for a target
  inside the second is answered by the second's handler.
- `FleetView.test.ts`: the dashboard target carries the raw
  `healthStatus`.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ai frontend/web/.storybook frontend/web/src/i18n frontend/web/src/ui/ai frontend/web/src/DashboardView.vue frontend/web/src/WorkspacePage.vue frontend/web/src/FleetView.test.ts frontend/web/src/components frontend/web/src/ui/table docs/solutions/architecture-patterns/storybook-decorator-owns-document-scoped-state-until-the-last-unmount.md`

### U8. Run owner and focus keeper

Files:
- `frontend/web/src/ui/ai/useAiRun.ts`, `useAiRun.test.ts` (new)
- `frontend/web/src/ui/ai/focusWithin.ts`, `focusWithin.test.ts` (new)

After: U7

Change:
- `useAiRun(registry)` returns `state`, `result`, `request`, `error`,
  `start(targets, options)`, `stop()`, and `reset()`, with the rules of
  its Decision. `state` is one of `idle`, `generating`, `done`,
  `stopped`, `error`, and `unavailable`. `error` is `invalid` or
  `failed`. A stale ending calls the `onStale` the surface passed and
  leaves `idle`. A run that completes with no snapshot is `done` with no
  result.
- `focusIsWithin` and `useFocusKeeper` as their Decision describes. The
  keeper watches removals under its root, as Reka's trapped scope does
  (`FocusScope.js:69-90`).

From `parked/aiact-review3`: no hunk is ported. The rules are the ones
its three surfaces converged on (`UiAiContextLayer.vue:355-420`,
`UiAiSummary.vue`, and `UiAiAssistant.vue` on that branch), and
`focusIsWithin` generalizes `hasFocusWithinSummary` from `f461f19a`.

Tests:
- `useAiRun.test.ts`: every sequence of up to four events drawn from
  `start`, `stop`, `reset`, dispose, and, for the latest and for the
  superseded run, yield, complete, and a failure of each kind (generic,
  invalid, unavailable, stale). A sequence that addresses a run that
  does not exist is left out by rule, and the executed count is a
  literal. After every sequence four invariants hold:
  - `result` is a snapshot of the latest run, or empty
  - every run except a latest one that is still generating has an
    aborted signal
  - `state` is `generating` exactly while the latest run is live
  - nothing changes after dispose

  Named cases pin the `state` each ending leads to, by a value only
  that state has
  (`docs/solutions/conventions/a-state-transition-test-must-assert-observables-unique-to-the-destination-state.md`).
- `focusWithin.test.ts`: a table over where focus is (a slot control
  with a successor, one without, a control that stays, content of a
  popover whose trigger is under the root, an element outside) and what
  is removed (the focused control, another control, the popover's
  trigger with its content, nothing), with the executed count as a
  literal. One fixture holds two groups with the same slot, and the
  nearer control wins. Each row asserts
  `document.activeElement`. happy-dom reports `body` once the focused
  element is detached (`happy-dom@20.14.5`,
  `lib/nodes/document/Document.js:1030-1044`), so the drop is observable
  there.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui/ai`

### U9. Result components and the summary

Files:
- `frontend/web/src/ui/ai/UiAiResult.vue`, `UiAiLabel.vue`,
  `UiAiEntityChip.vue`, `UiAiResultActions.vue`, `UiAiSummary.vue`, with
  the tests and stories of each
- `frontend/web/src/ui/ai/resultSources.ts`, `resultSources.test.ts`
  (new)
- `frontend/web/src/ui/ai/UiAiContextLayer.vue`, `UiAiAssistant.vue`,
  with their tests (adaptation only)
- `frontend/web/src/theme/ai.css`
- `frontend/web/src/i18n/locales/en.json`, `de.json`,
  `frontend/web/src/i18n/i18n.test.ts`
- `frontend/web/src/ui/index.ts`

After: U7, U8

Change:
- `UiAiResult` is controlled. It takes `result`, `state`, the error
  kind, and `showStop`, and it iterates no run. It keeps `UiAiRender`
  for an answer's `ui` tree, and the `ai` and `aiOrigin` props.
- Its one polite region is in the DOM before the first state change.
  The region's text is computed from the state. A run that ends with no
  snapshot shows and announces a no-result message.
- Lists are keyed by entity identity with an occurrence suffix, so a
  reordered snapshot keeps its nodes.
- `UiAiLabel` takes `request` and `sources`. `resultSources.ts` holds
  the one helper that merges an answer's refs with its summary's
  sources, each entity once.
- Every default text has a label prop and a catalog key. Copy builds
  each line from one whole message.
- `UiAiSummary` runs on `useAiRun` and `useFocusKeeper`. Its trigger,
  Stop, and Regenerate share a focus slot. A new target id resets it.

From `parked/aiact-review3`: port `13556681`, `1a55be06`, `aa2f43f5`,
and `f461f19a` for the templates, catalog keys, stories, and tests of
these components. Rewrite `UiAiSummary.vue`'s token, `focusControl`, and
`hasFocusWithinSummary` on U8. Re-apply the hunks to today's
`UiAiResult.vue`, `UiAiLabel.vue`, `UiAiEntityChip.vue`, and
`UiAiResultActions.vue`, which gained the `ai` props and the tree
renderer after the fork (`64d9a380`, `380d21a0`). The record's items
under these files name the cases still missing.

Tests:
- `UiAiResult.test.ts`: one announcement per state, read from a region
  that exists before the first change. A variant per severity and a
  label per tone. Reordered refs, and two findings with one title, keep
  their nodes without a duplicate-key warning. `unmarkedIdentifiers`
  over the chips.
- `UiAiSummary.test.ts`: Requirements 18 and 19 through the DOM, over
  the summary's transitions (start, first snapshot, done, Stop, error,
  unavailable, regenerate, new target id, unmount) and three focus
  positions: the control the transition removes, one that stays, and a
  button outside. A new target id while focus is on a chip in the label
  popover puts focus on the summarize trigger, the case at
  `UiAiSummary.test.ts:393` on that branch.
- `UiAiLabel.test.ts`, `UiAiEntityChip.test.ts`, and
  `UiAiResultActions.test.ts`: the request as sent after the live target
  changed, the label popover's accessible name, a 24 px minimum target
  on each control, and one Copy message per confidence.
- A locale switch on a mounted error changes its text.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui/ai frontend/web/src/theme frontend/web/src/i18n frontend/web/src/ui/index.ts`

### U10. Context layer

Files:
- `frontend/web/src/ui/ai/contextTarget.ts`, `contextTarget.test.ts`
  (new)
- `frontend/web/src/ui/ai/UiAiContextLayer.vue`,
  `UiAiContextLayer.test.ts`, `UiAiContextLayer.stories.ts`
- `frontend/web/src/ui/context-menu/UiContextMenu.vue`,
  `UiContextMenu.test.ts`, `UiContextMenu.stories.ts`
- `frontend/web/src/ui/popover/UiPopover.vue`, `UiPopover.test.ts`
- `frontend/web/src/ui/a11y.test.ts`
- `frontend/web/.storybook/aiDecorator.ts`, `aiDecorator.test.ts`
- `frontend/web/src/i18n/locales/en.json`, `de.json`
- `docs/solutions/conventions/audit-an-open-portalled-overlay-on-its-root-body-child.md`

After: U7, U8, U9

Change:
- `contextTarget.ts` holds `resolveContextTarget` with its five rules.
- `UiContextMenu` forwards its attributes to the trigger. `UiPopover`
  takes `accessibleName`, and a `#trigger` wins over `reference`.
- `UiAiContextLayer` renders its slot directly under its root, beside
  the hidden trigger. Its root listeners resolve, prevent, and open as
  the Decisions say. A layer ignores an event whose nearest layer root
  is another one.
- The long-press delay is one named constant.
- The menu lists `aiActions(target)` by catalog label. A verb starts a
  bound run through `useAiRun` and opens the result on the target's
  element. A press or a focus move outside the result closes it, which
  is Reka's default, so the next menu starts from a closed result.
- The result shows `UiAiLabel` with the request as sent, `UiAiResult`,
  the actions, a restart control when no snapshot arrived, and a
  `UiButton` that closes it. Its content root runs `useFocusKeeper`.
- Focus follows Requirement 17.
- "Continue in assistant" emits `continue` with the targets of
  `run.request`, the verb's label as the user turn, the result, and the
  request. "Ask about this…" emits it with the live target and no
  turns, one tick after the menu has closed (overlay rule 6 in
  `.claude/skills/web-component/references/overlays-and-motion.md`).
- A target that leaves closes its result. Unmounting stops the run and
  removes the listeners.

From `parked/aiact-review3`: the layer is rewritten. Port the result
template (`UiAiContextLayer.vue:582-653` on that branch), the fixture
with the position and path lists of the generated tables
(`UiAiContextLayer.test.ts:61-166`, from `81f01e39`), and the primitive
hunks of `f60fd850`. Leave behind `syntheticContextMenuEvents`,
`restoreNativeTouchCallout`, `pressOpenDelay`, `focusResultElement`, and
the hand-written focus column. The record's items under
`UiAiContextLayer.*`, `context-menu/`, `popover/`, and `a11y.test.ts`
name the cases still missing.

Tests:
- `contextTarget.test.ts`: Requirement 13 as one table over gesture,
  selection, and position. The positions are an item root, a plain
  child, an SVG child, a link in an item, a `span` in that link, each of
  the four field kinds in an item, an item inside a view, a view alone,
  an element outside every target, and an item inside an item. The
  wrapper positions are a tab stop around one item, a `tabindex="-1"`
  element around one item, a disabled button around one item, a tab stop
  around two items, and a tab stop around an item and a view. The
  expected column is written from the five rules, and the row count is a
  literal.
- `UiAiContextLayer.test.ts`:
  - Requirement 14 for every path (right-click, touch and pen
    long-press, Menu key, Shift+F10) at every position the resolver
    table marks as no target.
  - Requirement 15 over every ordered pair of paths, with the menu open
    and closed at the second. Each gesture is the sequence a browser
    sends, a right-click being a `pointerdown` followed by a
    `contextmenu`. Each cell names the lifecycle it expects and asserts
    it through `update:open`:
    - A mouse or pen press outside an open menu dismisses it first, and
      the menu reopens for the second target.
    - A touch press does not dismiss until a click follows
      (`DismissableLayer/utils.js:53-57`), so a touch long-press moves
      the open menu to the second target without a close.
    - A key path second with the menu open is left out by name. The
      modal menu traps focus (`Menu/MenuRootContentModal.js:120`,
      `FocusScope/FocusScope.js:57-67`), so no pane element can be
      focused, and the menu's own keydown does not pass the layer root.

    Each cell also asserts the second target's verbs and, after a verb,
    the result's reference element. Reka registers its outside listener
    on a 0 ms timer after the menu mounts (`utils.js:74-76`), so a cell
    advances timers before the second press.
  - Requirement 16, with a `contextmenu` before the delay and after it,
    and a cancel by `pointermove`, `pointerup`, and `pointercancel`.
    Text selected during the delay opens no menu, and neither does a
    target that unregisters during it.
  - Requirement 17 over path, origin (a link in the item, a button
    outside, nothing focused), and close. The no-verb rows compare with
    a bare `UiContextMenu` mounted in the same case.
  - Requirements 18 and 19 over the result's transitions and the three
    focus positions.
  - Two nested layers open one menu, from the inner one.
  - The `continue` payloads, with a target that re-registered under a
    new label between the request and Continue.
  - Every table installs fake timers inside `try` and `finally`.
- `UiContextMenu.test.ts` and `UiPopover.test.ts`: attribute forwarding,
  the accessible name, and a case that passes both a trigger and a
  reference.
- `a11y.test.ts`: the open menu and the open result, in both themes.

happy-dom focuses any connected element (`happy-dom@20.14.5`,
`lib/nodes/html-element/HTMLElementUtility.js:41-70`) and runs no
default action for a pointer event. No test here can tell a tab stop
from an element that cannot take focus, and a press's own focus change
is simulated. Nothing in this unit's tests covers the Stop condition or
a long-press on a device either. The browser pass in Verification covers
all but the device.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui/ai frontend/web/src/ui/context-menu frontend/web/src/ui/popover frontend/web/src/ui/a11y.test.ts frontend/web/.storybook frontend/web/src/i18n docs/solutions/conventions/audit-an-open-portalled-overlay-on-its-root-body-child.md`

### U11. Assistant panel, console shell, and documentation

Files:
- `frontend/web/src/ui/ai/UiAiAssistant.vue`, `UiAiAssistant.test.ts`,
  `UiAiAssistant.stories.ts`
- `frontend/web/src/ui/dialog/UiDialog.vue`, `UiDialog.test.ts`,
  `UiDialog.stories.ts`
- `frontend/web/src/FleetView.vue`, `FleetView.test.ts`,
  `FleetView.locale.test.ts`
- `frontend/web/src/style.css`
- `frontend/web/src/i18n/locales/en.json`, `de.json`
- `frontend/web/src/ui/a11y.test.ts`
- `frontend/web/README.md`

After: U7, U8, U9, U10

Change:
- The panel's active turn runs on `useAiRun`. A regenerated turn sends
  what Requirement 21 says. A new seed stops the active run.
- Send and Stop share a focus slot. A chip's Remove shares one with the
  other chips and "Add context". The panel root runs `useFocusKeeper`.
- Enter does not send during IME composition. The panel reads the
  registry only while "Add context" is open. Send stays enabled on an
  empty prompt, and the prompt shows its label.
- The shell keeps one panel instance and moves it between the docked
  column and the sheet, so the draft and the thread survive a close and
  a change of width. `UiDialog` gains `forceMount`, `closeButton`, and
  the `openAutoFocus` event for that. A kept-mounted dialog emits no
  `closeAutoFocus` (`FocusScope/FocusScope.js:127-132`). Reka focuses
  the element that was active at its first mount instead
  (`Dialog/DialogContentModal.js:58-60`), so the shell's own focus runs
  after that.
- Focus follows Requirement 20. The panel focuses its prompt when
  `open` turns true. The shell focuses the Assistant button on a close
  for which `focusIsWithin` the panel was true.
- Ctrl/Cmd+I works inside the panel's own sheet and stays inert under
  any other modal. Workspace shortcuts are inert under the sheet.
- The pane layout selects `[data-ai-context-layer] > main.panes`, and
  the docked column carries no `z-` utility.
- The README's "AI targets" section matches the code. It covers the
  layer's own trigger and the three gestures, the focus table, the run
  owner, the verb ids, the four result components in the component
  list, and `closeButton` and `forceMount` on `UiDialog` with who
  returns focus when a kept-mounted dialog closes. It names `UiBadge`,
  `validateAiResult`, and `window.ts` where the record found
  `UiStatusBadge`, `isAiResult`, and `registry.ts`.

From `parked/aiact-review3`: port `8468275c`, `72ad952a`, `7d56693f`,
`1813e0b2`, `e5e4f2be`, and `3c372c64` for the panel thread, the shell,
`UiDialog`, and their tests. Rewrite the panel's run state on
`useAiRun`, its `hasAssistantFocus` and `focusAssistantControl` sites on
focus slots, and the selector test in `updateAssistantOpen`
(`FleetView.vue:138-152` on that branch) on `focusIsWithin`. Write the
README section against today's README, which gained 176 lines after
the fork. `FleetView.vue` is unchanged on `main` since the fork, so its
hunks apply. The record's items under these files name the cases still
missing.

Tests:
- `FleetView.test.ts`: Requirement 20 as one table over how the panel
  opens (button, shortcut, `continue`), the width, how it closes (its
  Close, the button, the shortcut, Escape in the sheet), and where focus
  was (the prompt, a chip, the "Add context" list, a turn's label
  popover, the main pane, the top bar). The row count is a literal.
- `FleetView.test.ts`: the draft and the thread survive a close and a
  change of width. A first mount at the narrow width keeps the closed
  sheet hidden and inert. The shortcut cases use a real dock and a wide
  split. The pane row matches the layout selector.
- `UiAiAssistant.test.ts`: Requirement 21 over the turn's origin (typed
  prompt, suggestion, seeded verb turn) and its place in the thread.
  Requirements 18 and 19 over the panel's transitions and the three
  focus positions. Enter during composition. The registry read.
- `UiDialog.test.ts`: `forceMount` keeps closed content in the DOM and
  out of display, `closeButton: false` renders no close control, and
  `openAutoFocus` is forwarded and can be prevented.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui/ai frontend/web/src/ui/dialog frontend/web/src/FleetView.vue frontend/web/src/FleetView.test.ts frontend/web/src/FleetView.locale.test.ts frontend/web/src/style.css frontend/web/src/i18n frontend/web/src/ui/a11y.test.ts frontend/web/README.md`

Waves: U7 | U8 | U9 | U10 | U11

The graph is a chain. U8 tests against the registry endings U7 lands.
U9, U10, and U11 each edit the two locale catalogs and the callers U7
adapted, so no two of them are independent.

## Verification

- Run the verifier over the union of changed paths after each unit.
  Never run `--full`.
- `pnpm build-storybook`, then the browser loop of
  `.claude/skills/web-component/references/review.md` on the context
  layer and assistant stories and on `pnpm dev`, in both themes, at the
  docked and the narrow width:
  - the Stop condition first: right-click a device row, a topology
    node, and the traffic chart, and the menu opens at the pointer
  - a row link and the page heading keep the native menu
  - right-click a second item while the first menu is open
  - the Menu key and Shift+F10 on a focused row link in Chrome and
    Safari on macOS, and the Menu key in a field
  - a long-press under touch emulation, on an item and beside it
  - every row of Requirement 17 with a real pointer
  - open the panel by button, by shortcut, and by "Continue in
    assistant" at both widths, type a draft, close, cross the
    breakpoint, and reopen
  - keyboard-only operation throughout, and reduced motion on the
    shimmer and the fades
- No check here covers a long-press on a touch device (Open questions).

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `frontend/web/README.md` "AI targets" section and both solution
      docs updated in the unit that changes what they describe.
- [ ] No component under `frontend/web/src/ui/ai/` reads
      `run.snapshots` or focuses a control by selector.
- [ ] The browser pass ran, or the report names what it skipped and
      why.
- [ ] This plan's outcome recorded with
      `.claude/skills/plan/scripts/plan_record.py implemented <plan>
      --units 5 --from <t> --to <t>`, or `partial`.
- [ ] No plan labels in code, comments, or commit messages.

## Open questions

1. **Does a handoff to the assistant keep focus in its prompt on the
   docked panel?** The focus-return Decision prevents Reka's restore
   "only while a verb hands focus to the result popover". "Ask about
   this…" and "Continue in assistant" close the menu or the result and
   open the panel, and the Decision names neither. Reka restores focus
   one timer tick after the content unmounts
   (`FocusScope/FocusScope.js:119-121`), which is after the panel has
   focused its prompt. At the narrow width the sheet is modal and keeps
   focus either way. The options:
   - The handoff is exempt (recommended). The layer prevents the restore
     for these two entries as it does for a verb, and the prompt keeps
     focus. A keyboard user lands where they asked to go, and both
     widths behave alike. The Decision gains a second exemption.
   - No exemption. Focus returns to the pre-menu element, and the docked
     panel opens seeded and without focus. The layer keeps one rule. A
     keyboard user reaches the prompt by tabbing through the panes,
     since Ctrl/Cmd+I would close the panel.
   - No exemption, and Ctrl/Cmd+I moves focus into an open panel that
     does not hold it and closes one that does. The layer keeps one
     rule, and the keyboard has a route. The shortcut stops being a
     plain toggle.

   The answer sets the last row of Requirement 17 and the last sentence
   of Requirement 20. U10 and U11 change by one table row each.

2. **Does a pointer press outside the menu or the result still send
   focus back?** The focus-return Decision sends focus to the pre-menu
   element "when the menu or its result closes" and names no close that
   differs. Read that way, a click on a search field while a result is
   open closes the result and then moves focus off the field. The parked
   focus table expects that. Reka returns focus to a trigger only when
   the close did not follow an outside interaction
   (`Popover/PopoverContentNonModal.js:124-126`, and
   `ContextMenu/ContextMenuContent.js:117-125` for a menu with `modal`
   false). The options:
   - Reka's rule (recommended). Focus stays where the user pressed. The
     menu takes `modal: false`, where Reka skips the restore itself, and
     the layer root needs no `pointer-events` while the menu is open.
     `UiPopover` re-emits `interactOutside` so the result can skip its
     own restore.
   - As written. Focus returns after every close. The units implement
     this until the question is answered.

   The answer sets the fourth row of Requirement 17.

- Unconfirmed: view-level targets are excluded from the context menu, so
  page-wide verbs live only in the summary placement and the panel. If
  the user wants them in the menu, a right-click on non-item page areas
  opens the AI menu and loses the native one there.
- Confidence stays as the words low, medium, and high until a real model
  reports calibrated confidence. The implementer adds no percentages.
- Unverified: a `contextmenu` event dispatched by script opens Reka's
  menu at its point in Chrome, Firefox, and Safari. The tree shows it
  under happy-dom only (`frontend/web/src/ui/a11y.test.ts:139-141`). It
  is the Stop condition, and the browser pass checks it first.
- Unverified: which touch browsers fire a native `contextmenu` on a
  long-press, and whether a long-press on text selects it first. The
  timer resolves again when it fires, so a selection made by then opens
  no menu. Touch emulation in a desktop browser answers neither.
