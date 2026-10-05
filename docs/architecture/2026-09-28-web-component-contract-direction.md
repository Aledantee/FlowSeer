---
title: Web Component Contract - Direction
type: direction
date: 2026-09-28
topic: web-component-contract
status: accepted-direction
---

# Web Component Contract - Direction

The [web design system record](2026-09-26-web-design-system-direction.md)
settles how components look and where their behavior comes from: tokens,
Tailwind, Reka UI, Storybook. What it leaves open is what a component owes
beyond that. Four gaps showed up in September 2026.

- **Text.** No string in `frontend/web/` is translatable. Labels,
  announcements, and number and date formatting are English literals and
  ad hoc `toLocale*` calls.
- **AI.** Only view code opts into AI. `v-ai-target` is applied by hand in
  views, a highlighted target has no visible outline once the action layer
  goes, and an agent cannot put anything on screen except text in an answer.
- **Overlays.** None of them animate, although the design system plan
  describes 100–160 ms entrances. Stacking is a literal `z-50` on every
  overlay against chrome values 1–8. Focus return is configurable only on
  `UiPopover`, and `FleetView.vue` imports Reka's `TooltipProvider`
  directly.
- **Motion.** The history of `frontend/web/` shows the same failures
  repeating in agent-written work:
  - a CSS transition and a `motion/mini` animation fighting over one
    property (the sidebar, fixed in `62215f7f`)
  - a Reka default the component did not expect (`ComboboxContent`
    rendering inline, same commit)
  - an overlay whose trigger was not a tab stop, so focus was lost on close
    (`f78206e4`)
  - a hand-built layer stacked above modals (`e02774f2`)
  - overlay stories that audited only the closed state (`7cdf983d`)

This record sets the contract every `Ui*` component meets from here on.
The [`web-component` skill](../../.agents/skills/web-component/SKILL.md)
carries it out.

## Decision

### Composable components

A component does one thing and composes with the others through props,
slots, and emitted events. It does not reach into its parent or into a
global store. Parts of a compound component (a table, a menu, a command
list) are separate `Ui*` components sharing one injected context, as Reka
structures its own parts. A wrapper whose root is not the interactive
element sets `inheritAttrs: false` and binds `$attrs` to that element, so
`id`, `aria-*`, and listeners reach the control. State that two views need
lives in a composable, not in either view.

### Text and formatting go through vue-i18n

**Library.** `vue-i18n` 11.4.12 (MIT) is the i18n layer, in Composition
API mode (`legacy: false`).

**Locales.** English (`en`) and German (`de`) ship from the start. German
tests plural rules, number and date formats, and text roughly 30% longer
than English. Messages live in `frontend/web/src/i18n/locales/<locale>.json`,
keyed by owner: `ui.<component>.<key>` for components and
`view.<view>.<key>` for views.

**Rules for components.**
- A component has no user-visible literal. Its default labels,
  announcements, and error text come from `t()`. A caller can override any
  of them through a prop.
- Numbers, dates, relative times, and lists are formatted with vue-i18n's
  `n()` and `d()` or the matching `Intl` API for the active locale, never
  by concatenation.
- Units are joined to values with a non-breaking space.
- Hostnames, MAC addresses, and identifiers carry `translate="no"`.

**App root.** Reka's `ConfigProvider` wraps the app and receives the
active locale and `dir`.

**Checks.** A test fails when a key is present in one locale file and
missing from the other. Storybook gets a locale toolbar next to the theme
toolbar, and every story renders in both locales.

### Every component is AI-addressable

**Registration.** Every `Ui*` component that renders an entity, a value,
or an action accepts an optional `ai` prop and registers itself through a
`useAiTarget` composable. That composable is the directive's logic, moved
so components can call it. The prop takes the `aiTarget()` input already
defined in `src/ai/target.ts`. Views pass it instead of applying
`v-ai-target` themselves. Layout-only components do not take the prop.
Those are separators, scroll areas, and skeletons.

**Highlight.** One shared rule styles `[data-ai-selected]` with the
`--ring` token, and the registry writes that attribute on the highlighted
element. Every registered component therefore shows the outline without
per-component code.

**Agent changes.** A value an agent changed carries `data-ai-origin="agent"`
until the user next interacts with it. The same highlight styling marks
it, and `UiAiLabel` explains it.

### Agents compose UI from a catalog

**Catalog.** `src/ai/catalog.ts` lists the components an agent may render,
each with a prop schema written as a hand-written validator. No schema
library is added.

**Renderer.** `UiAiRender` takes a component tree,
`{ component, props, children? }[]`, validates it against the catalog,
and renders the real `Ui*` components.

**Rejection.** An unknown component, an invalid prop, or a prop the schema
does not list rejects the whole tree with an error state. Nothing is
partly rendered.

**What an agent cannot supply.**
- Text props are plain strings: no markdown, no HTML, no `v-html`.
- Event handlers are never passed in. An interactive node declares an
  intent: navigate to a `PageTarget`, or propose an API call that the user
  confirms.

This follows the pattern of json-render and A2UI, where an agent picks
from a trusted component set instead of emitting markup.

### Agents act through the API, not the UI

An agent changes state by calling the same service API the UI calls. The
UI shows the result through live synchronization. Components expose no
"activate", "set value", or "select" operations for agents.

Why:
- An API call carries the caller's identity through the service's
  authorization and audit, whereas driving the UI would impersonate the
  user.
- The API does not depend on which pane, focus, or layout happens to be
  mounted.

A proposed change is shown as a confirmable intent (see the catalog)
rather than executed by the UI on the agent's behalf. Until the console
has a service API and live sync, this stays a rule for new contracts and
has no code.

### Overlays

**Portals and positioning.**
- Floating content (menus, popovers, listboxes, tooltips, dialogs, toasts)
  is always a `Ui*` wrapper that renders through its Reka `*Portal` to
  `body`. There is no hand positioning and no native `popover` attribute.
  An ancestor with `transform`, `filter`, `backdrop-filter`, `contain`, or
  `will-change` becomes the containing block for fixed content
  ([MDN, containing block](https://developer.mozilla.org/en-US/docs/Web/CSS/Guides/Display/Containing_block)),
  and a portal is the one escape that holds.
- Primitives that default to inline positioning (`ComboboxContent`,
  `SelectContent`) set `position="popper"`.
- Every popper sets `:collision-padding="8"`.
- Styling reads the resolved side (`data-side`, `data-align`), never the
  requested one.

**Stacking.** Stacking uses z-index tokens in `src/theme/tokens.css`:

| Token | Value | Use |
|---|---|---|
| `--z-raised` | 2 | in-flow chrome |
| `--z-sticky` | 10 | sticky headers and the top bar |
| `--z-overlay` | 50 | every portalled overlay and its scrim |
| `--z-toast` | 60 | toasts, which stay visible over a dialog |
| `--z-skip-link` | 100 | the skip link |

A hand-built layer sits below `--z-overlay`.

**Dismissal and focus.**
- Dismissal belongs to Reka's `DismissableLayer`: only the top layer
  handles Escape, and a component that must stay open calls
  `preventDefault()` in `pointerDownOutside` or `interactOutside`. No
  component adds its own document listener for outside clicks or Escape.
- Every overlay wrapper re-emits `closeAutoFocus`. When the invoker is not
  a tab stop or may unmount, the caller prevents the default and focuses a
  defined element.
- Initial focus goes to the first control, or to the least destructive
  action for an irreversible one
  ([WAI-ARIA APG, dialog](https://www.w3.org/WAI/ARIA/apg/patterns/dialog-modal/)).

**Nesting.** A dialog opened from a menu item is controlled state set on
the next tick after the menu closes. Opening it inside the menu's
selection handler can leave `pointer-events: none` stuck on `body`
([radix-ui/primitives#3317](https://github.com/radix-ui/primitives/issues/3317),
[unovue/reka-ui#2822](https://github.com/unovue/reka-ui/issues/2822)).

**App-root providers.** `ConfigProvider` (locale, `dir`, `scroll-body`) and
one `TooltipProvider` are mounted once, in a `Ui` app-root component. No
view imports either from Reka.

**Menus and tooltips.**
- Menus open on click and keyboard, never on hover alone
  ([APG, menu button](https://www.w3.org/WAI/ARIA/apg/patterns/menu-button/)).
- Tooltips open on hover and focus, hold no controls, and after the first
  one in a group, open without delay or animation.

### Motion

**One mechanism per property.** Each animated property has exactly one
mechanism:
- A JavaScript animation goes through `src/motion/useMotionFeedback.ts`,
  which owns the reduced-motion check and cleanup. Its start and end
  values are read from computed style around `nextTick`.
- A CSS transition on the same property is deleted.

**Overlay enter and exit use CSS keyframes keyed on `data-state`.** Reka's
`Presence` waits for `animationend` and ignores transitions, so a
transition-based exit never plays and the node disappears
([`usePresence.ts`](https://raw.githubusercontent.com/unovue/reka-ui/v2/packages/core/src/Presence/usePresence.ts)).
The keyframes live in `src/theme/motion.css` as our own utilities, with no
animation package:
- **Movement:** fade plus scale from 0.96, with `transform-origin` set from
  Reka's `--reka-*-content-transform-origin` variable.
- **Menus, popovers, and listboxes:** enter over `--duration-base` (140 ms)
  and exit over `--duration-fast` (100 ms).
- **Dialogs:** enter over `--duration-slow` (160 ms) and exit over 100 ms.
- **Easing:** everything uses `--ease-out`.

This stays inside the README's 100–160 ms budget. Exits are shorter than
entrances, which the research below supports.

**Properties.** Only `transform` and `opacity` animate. There is no
`transition: all`, and no animation of `width`, `height`, `top`, or
`margin`.

**What does not animate:**
- keyboard-initiated or high-frequency surfaces: the command palette,
  typeahead lists, and repeat tooltips
- live values, sorting, and typing, as the README already says

**Reduced motion.** Movement is replaced with an opacity change of the
same duration. Feedback is not removed outright.

### Every component is proven in stories and tests

A component is done when its stories cover:
- default, hover, focus, active, disabled, loading, empty, and error
  states, where the component has them
- very long, empty, and large values
- both themes and both locales

An overlay also needs an open-state `AccessibilityAudit` story registered
in `OVERLAY_AUDITS`. Layout, stacking, collision, and exit animations
cannot be observed in happy-dom, which has no layout and no app CSS, so
Presence unmounts at once. They are checked in a real browser against
Storybook.

## Alternatives

- **An in-house layer on `Intl` instead of vue-i18n.** It needs no
  dependency. It lost because we would own interpolation, plural
  selection, and lazy loading. Plural rules and message syntax are exactly
  where German breaks an English-only implementation.
- **`tw-animate-css` for overlay keyframes.** It is widely used with
  Radix- and Reka-based kits. It lost because it adds a package for a
  handful of keyframes we can write against our own duration tokens. The
  approval rule in the `web-component` skill applies to it.
- **`motion-v` with `AnimatePresence` and `forceMount`.** This would give
  JavaScript-driven exits. It lost because it adds a second Motion package
  beside `motion/mini`. Its reduced-motion default is off
  ([MotionConfig](https://motion.dev/docs/vue-motion-config)), and it
  moves mounting out of Reka's hands.
- **Agent operability through the UI.** Components would expose their
  actions to `window.flowseerAi`. It lost on authorization and audit, and
  on fragility, as described above.
- **Keeping `v-ai-target` as a view-only directive.** It is the smallest
  change. It lost because every new component would need a view author to
  remember it, and the highlight would still be per-view.

## Consequences

**Plan-level migration.** Existing `Ui*` components move onto this
contract under a plan:
- move their strings into the locale files
- add the `ai` prop
- add the keyframe classes and `closeAutoFocus` to every overlay
- replace `z-50` with the tokens
- move `TooltipProvider` out of `FleetView.vue` into the app-root
  component

**Plan interaction.** The AI actions and assistant plan
(`docs/plans/2026-09-28-1804-feat-ai-actions-and-assistant-plan.md`)
builds on the registry and summary shapes. It does not conflict with this
record. Its new components meet this contract when they land.

**Dependencies.** `frontend/web/` gains `vue-i18n` and its `@intlify/*`
dependencies. No other package is added by this record.

**Enforcement.**
- Missing locale keys, story coverage, and overlay audits are enforced by
  tests.
- String literals in templates are not machine-checked yet. A lint rule
  (`@intlify/eslint-plugin-vue-i18n`) would be a separate package decision.

## Sources

Checked on 2026-09-28:

- Reka UI 2.10.5 installed source: `ContextMenu/ContextMenuTrigger.js`,
  `ConfigProviderProps` in `dist/index4.d.ts` (`dir`, `locale`,
  `scrollBody`).
- Reka `usePresence.ts`:
  https://raw.githubusercontent.com/unovue/reka-ui/v2/packages/core/src/Presence/usePresence.ts
- Motion durations:
  - NN/g, animation duration: https://www.nngroup.com/articles/animation-duration/
  - Microsoft Fluent motion: https://learn.microsoft.com/en-us/windows/apps/design/signature-experiences/motion
  - Emil Kowalski: https://emilkowal.ski/ui/7-practical-animation-tips
- WAI-ARIA APG patterns:
  - dialog: https://www.w3.org/WAI/ARIA/apg/patterns/dialog-modal/
  - menu button: https://www.w3.org/WAI/ARIA/apg/patterns/menu-button/
  - tooltip: https://www.w3.org/WAI/ARIA/apg/patterns/tooltip/
- npm registry: `vue-i18n@11.4.12`, MIT, published 2026-09-16.

## Amendments

### 2026-09-28: motion-v, Tailwind keyframes, and focus-return scope

Where a line
above conflicts with this section, this section wins.

- **motion-v replaces `motion/mini` for JavaScript motion.**
  - `motion-v` (MIT) and its peer `@vueuse/core` are the approved packages.
  - `useMotionFeedback` and its callers move to motion-v. Layout changes
    such as the sidebar resize may use motion-v's `layout` animations in
    place of the read, `nextTick`, read, play sequence.
  - `MotionConfig` with `reducedMotion="user"` is set once in the app-root
    component. motion-v's default is `"never"`
    ([MotionConfig](https://motion.dev/docs/vue-motion-config)).
  - The `motion` package is removed once no caller remains.
- **Overlay enter and exit stay on CSS keyframes.** They are declared as
  `--animate-*` theme variables with their `@keyframes` inside Tailwind
  v4's `@theme` in `src/theme/tailwind.css`, and applied per
  `data-state` inside each `Ui*` wrapper. That replaces the separate
  `src/theme/motion.css` named above.
  - This is Nuxt UI's production pattern for Reka overlays, and it is
    what Reka's `Presence` waits for.
  - Staying on Reka was checked against alternatives. Ark UI's Zag
    presence machine also watches keyframes only. Headless UI Vue has had
    no release since 2024-09. Reka's transition-aware presence is planned
    for v3 ([#2827](https://github.com/unovue/reka-ui/issues/2827)), with
    no release date.
  - Keeping the classes inside the wrappers lets v3's
    `data-ending-style` replace them without touching call sites.
- **motion-v on an overlay** is allowed only where springs, gestures, or
  layout animation earn it. It uses `forceMount`, `as-child`, and
  `AnimatePresence`, and needs a browser story test for that wrapper.
  Popper-based parts have had exit regressions under Motion
  ([#1663](https://github.com/unovue/reka-ui/issues/1663),
  [motion-vue#231](https://github.com/motiondivision/motion-vue/issues/231)).
- **Focus return.** "Every overlay wrapper re-emits `closeAutoFocus`" is
  narrowed to the wrappers whose Reka content emits it: dialog, alert
  dialog, command dialog, popover, dropdown menu, context menu, and
  select. `ComboboxContent` restores focus itself on unmount, and tooltip
  content never takes focus (reka-ui 2.10.5,
  `Combobox/ComboboxContentImpl.js`).
- **Exempt from motion.** The command dialog and typeahead combobox lists
  do not animate on entry. The command dialog gets an exit fade so it
  does not vanish mid-frame. Tooltips do not animate at all, which is
  simpler than animating only the first tooltip of a group.

### 2026-10-01: motion-v ownership and reduced-motion behavior

This amendment narrows the motion-v ownership described above.

- `useMotionFeedback` lives under `frontend/web/src/ui/motion`.
- Views import motion surfaces from the `src/ui` barrel.
- `UiMotion` is a re-export of motion-v's component and has no story of its own.
- Reduced motion keeps only fades in the composable. Layout animations end
  immediately with no fade.

### 2026-10-03: the app's locale and identifiers in kit text

This amendment extends the vue-i18n section above. It records the view
migration that landed on 2026-10-03.

- The running app chooses its locale from a saved choice under
  `flowseer.locale`, then the first of `navigator.languages` that is `en`
  or `de`, then `en`. A language switch beside the theme switch changes
  it, and `<html lang>` follows the active locale.
- The `translate="no"` rule covers identifiers a kit component renders
  from its own text props. `UiTooltip` takes an `identifier` prop for a
  label or hint that names one, because Reka prints the tooltip's
  accessible text as one hidden node that no slot reaches. That node is
  marked as a whole, and the visible message words stay translatable.

### 2026-10-05: the ai prop, anchors, and origin acknowledgement

This amendment refines "Every component is AI-addressable" above with the
decision in `docs/architecture/2026-10-05-1251-web-ai-target-lifecycle-direction.md`,
as implemented.

- `ai` takes the resolved `AiTarget` from `aiTarget()`, not its input, and the
  caller owns the identity (`frontend/web/src/ui/ai/context.ts`,
  `frontend/web/src/ai/target.ts`). The `v-ai-target` directive and its
  `ai/directive.ts` are deleted, and `useAiTarget`
  (`frontend/web/src/ui/ai/useAiTarget.ts`) follows the prop and the rendered
  element against the injected registry, with the document registry as fallback.
- A component registers its meaningful element. Native markup uses `UiAiTarget`
  with `as` or `asChild` (`frontend/web/src/ui/ai/UiAiTarget.vue`), and an
  SVG root is a valid anchor. Popups register their content through
  `frontend/web/src/ui/popover/popupAnchor.ts`.
- The registry owns `data-ai-selected` for every registration, manual ones
  included (`frontend/web/src/ai/registry.ts`).
- `aiOrigin` takes an `AiOriginRequest` and the component emits
  `aiOriginAcknowledged(requestId)` after pointerdown, keydown, input, or
  change on the value (`frontend/web/src/ui/ai/useAiOrigin.ts`). The caller
  clears its state and composes `UiAiLabel` beside the value. This replaces
  the earlier "until the user next interacts" wording with an explicit event.

### 2026-10-05: generative UI catalog and renderer

The generative UI catalog and renderer are implemented in
`frontend/web/src/ai/catalog.ts`, `frontend/web/src/ui/ai/UiAiRender.vue`,
`frontend/web/src/ui/ai/UiAiResult.vue`, and `frontend/web/src/ai/types.ts`.

- An answer enters the catalog through `AiAnswer.ui`. `UiAiResult` passes the
  value to `UiAiRender`, which validates it before rendering. The catalog
  contains `UiCard`, `UiBadge`, `UiStatusBadge`, `UiMetricCard`, `UiMeter`,
  `UiProgress`, `UiSeparator`, `UiEmptyState`, `UiAiEntityChip`, and
  `UiButton`.
- A node has `component`, `props`, and optional `children`. `text` is the
  catalog prop for text that the renderer places in the default slot. `UiCard`
  is the only component that accepts children.
- Buttons use the `navigate` intent with a `path` or `query`. A path is valid
  only when `isPagePath` in `frontend/web/src/navigation/page.ts` accepts it.
  Proposal intents remain outside the catalog until the console has a service
  API.
- The validator caps a tree at 64 nodes, four levels, and 500 characters per
  string (`frontend/web/src/ai/catalog.ts`). It takes one `structuredClone`
  of the handler's value, which refuses a Proxy or a function, and copies the
  allow-listed data from that snapshot before validation and rendering.
  `UiAiRender` unwraps a tree held in reactive state with `toRaw` first.
- `UiAiRender` has no `ai` prop. It is a container for agent output, so it
  stands for no entity, value, or action of its own. The structural exemption
  is recorded in `frontend/web/src/ui/ai/targetContract.test.ts`.
