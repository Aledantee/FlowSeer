---
title: Web Component Contract Migration, Phase 1 - Overlays and Motion - Plan
type: refactor
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
parent: docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-plan.md
---

# Web Component Contract Migration, Phase 1 - Overlays and Motion - Plan

## Goal

Every portalled overlay, the toast, and the sticky header stack on
z-index tokens. The overlays enter and exit with Tailwind `@theme`
keyframes that Reka's `Presence` waits for, pad collisions uniformly, and
let the caller control focus return where Reka emits it. Select and
tooltip gain open-state audit stories. One `UiAppRoot` in `App.vue`
provides Reka's configuration and tooltips, so no view imports Reka.

Stop condition: keyframe exits on `DialogOverlay` or `DialogContent`
leave focus trapped or `body` locked after close. That would mean
`Presence` and `FocusScope` disagree about when the layer is gone.

## Decisions

- **Governing documents.** The contract's overlay and motion sections
  and its 2026-09-28 amendment govern
  (`docs/architecture/2026-09-28-web-component-contract-direction.md`).
  This plan does not restate them.
- **Ordering against the AI plan.** This phase lands before
  `docs/plans/2026-09-28-1804-feat-ai-actions-and-assistant-plan.md`
  starts. Why: both edit `FleetView.vue`, `UiPopover.vue`, `UiDialog.vue`,
  `ui/index.ts`, and the README. That plan's `UiContextMenu` then copies
  the tokens and keyframes instead of `z-50`. The AI plan records the
  same order.
- **z-index tokens.** `--z-raised: 2`, `--z-sticky: 10`,
  `--z-overlay: 50`, `--z-toast: 60`, and `--z-skip-link: 100` go in
  `:root` in `src/theme/tokens.css`. Components use them as
  `z-(--z-overlay)`. Why: Tailwind 4.3.3 maps `(--x)` to `var(--x)` in
  any utility. `UiTableHead`'s sticky header and the top bar in
  `FleetView.vue` move onto `z-(--z-sticky)`, so each token except
  `--z-raised` and `--z-skip-link` has a consumer. Those two wait for
  `src/style.css` to be dissolved.
- **Keyframes.** The keyframes sit inside `@theme` in
  `src/theme/tailwind.css` as `@keyframes` blocks, with matching
  `--animate-*` variables:

  | Variable | Motion | Duration |
  |---|---|---|
  | `--animate-overlay-in` | fade from 0, scale from 0.96 | `--duration-base` |
  | `--animate-overlay-out` | fade to 0, scale to 0.96 | `--duration-fast` |
  | `--animate-dialog-in` | same motion as `overlay-in` | `--duration-slow` |
  | `--animate-dialog-out` | same motion as `overlay-out` | `--duration-fast` |
  | `--animate-fade-in` | fade only | `--duration-base` |
  | `--animate-fade-out` | fade only | `--duration-fast` |

  Every entry uses `--ease-out`. Wrappers apply them on their content
  element only:
  - popper content (popover, dropdown menu, select):
    `data-[state=open]:animate-overlay-in
    data-[state=closed]:animate-overlay-out
    origin-(--reka-popper-transform-origin)`
  - dialog and alert-dialog content: the `dialog-*` pair
  - scrims: the `fade-*` pair
  - the command dialog: `data-[state=closed]:animate-fade-out` only
  - reduced motion: `motion-reduce:` variants that switch every pair to
    the `fade-*` pair

  Why:
  - This is Nuxt UI's pattern for Reka overlays.
  - A variant scoped to the content element never reaches triggers,
    which also carry `data-state`.
  - Popper content inherits `--reka-popper-transform-origin` from its
    wrapper (`Popper/PopperContent.js`), so one class serves every popper.
- **Motion exemptions.** Combobox and tooltip do not animate: combobox is
  a typeahead list, and tooltips are exempt by the amendment. Why: the
  amendment's exemptions.
- **Focus return.** `closeAutoFocus` is re-emitted by `UiDialog`,
  `UiAlertDialog`, `UiCommandDialog`, `UiDropdownMenu`, and `UiSelect`.
  `UiPopover` already does. Why: the amendment's narrowed scope; Reka's
  combobox and tooltip content never emit it.
- **Toast exit.**
  - `UiToast` gets the `fade-*` pair and drops `transition-all`.
  - `UiToastProvider` calls `dismiss` on close and `remove` only when the
    toast emits a new `closed` event.
  - `UiToast` emits `closed` from its root's `animationend` whose
    `animationName` is the fade-out keyframe. It emits at once when the
    computed `animationName` at close is `none`.

  Why:
  - `ToastRoot` sits under `Presence`
    (`reka-ui/dist/Toast/ToastRoot.js`), and removing the item
    immediately unmounts it before any exit.
  - The `none` fallback keeps happy-dom, which loads no app CSS, from
    leaking closed toasts.
- **Uniform look.**
  - `UiSelect` gains `:collision-padding="8"` and `shadow-lg`.
  - `UiAlertDialog`'s scrim gains `backdrop-blur-xs` to match `UiDialog`.
  - `UiDialog`'s inert `transition-opacity duration-140` on the scrim is
    removed.
- **`UiAppRoot`.**
  - Lives in `src/ui/app/`. It renders `ConfigProvider`, with `locale`
    defaulting to `en`, `dir` to `ltr`, and `scroll-body` left at Reka's
    default.
  - Wraps `TooltipProvider` (delay 350 ms, skip 250 ms, today's values in
    `FleetView.vue`).
  - Mounted in `App.vue` around `<RouterView />`. `FleetView.vue` drops
    its provider and its `reka-ui` import.

  Why: the contract puts `ConfigProvider` around the app, and a second
  route component must not lose the providers. `FleetView.test.ts` mounts
  FleetView without `App.vue`, so it wraps the mount in `UiAppRoot`.
- **Audit stories.**
  - `UiDropdownMenu`'s `AccessibilityAudit` story renders its trigger and
    opens from it.
  - `UiSelect` gains `defaultOpen` passthrough and an `AccessibilityAudit`
    story.
  - `UiTooltip` gains an `AccessibilityAudit` story with `defaultOpen`.
  - Both new stories go in `OVERLAY_AUDITS`, with the `listbox` and
    `tooltip` roles.

  Why: the dropdown story renders its menu off-screen in a browser, and
  select and tooltip had no open-state audit. Opening through `defaultOpen`
  avoids teaching `openOverlay` the select's `pointerdown` path.

## Requirements

1. **Tokens replace `z-50`.** No `z-50` remains in a `.vue` file under
   `src/ui/`. Overlays and scrims use `z-(--z-overlay)`, the toast
   viewport uses `z-(--z-toast)`, and `UiTableHead` and the top bar use
   `z-(--z-sticky)`. Example: `grep -rn --include='*.vue' "z-50"
   frontend/web/src/ui` prints nothing.
2. **Exits wait for the keyframe.** A closing popover stays mounted until
   its exit keyframe ends. Example: the Presence test in U2 sees the
   content still mounted after close, and gone after an `AnimationEvent`
   named `overlay-out`.
3. **Focus return is controllable.** Each wrapper in the focus-return
   Decision emits `closeAutoFocus`. Example: `UiDialog` whose handler
   calls `preventDefault()` and focuses a sibling button leaves focus on
   that button after Escape.
4. **Closed toasts leave the store.** A dismissed toast leaves the store
   after its exit. Example: with the computed `animationName` stubbed to
   `none`, closing a toast removes it from `useToast().toasts` in the same
   tick.
5. **Reka stays inside `src/ui/`.** No file outside `src/ui/` imports
   `reka-ui`, test files aside, and tooltips keep their 350 ms and 250 ms
   delays.
6. **Every overlay has an audited open state.** The audit renders select
   and tooltip open, and the dropdown story opens from a rendered trigger.
7. **Reduced motion fades.** Overlays fade without scaling under reduced
   motion. The browser check confirms this, because happy-dom does not
   evaluate CSS media rules.

## Units

### U1. Tokens, keyframes, and the app root

Files:
- `frontend/web/src/theme/tokens.css`, `tailwind.css`
- `frontend/web/src/ui/app/UiAppRoot.vue`, `UiAppRoot.test.ts`,
  `UiAppRoot.stories.ts`
- `frontend/web/src/ui/index.ts`
- `frontend/web/src/App.vue`
- `frontend/web/src/FleetView.vue`, `FleetView.test.ts`
- `frontend/web/src/ui/table/UiTableHead.vue`

After: none

Change:
- Adds the tokens and the `@theme` keyframes as the Decisions state.
- `UiAppRoot` renders `ConfigProvider` around `TooltipProvider` around its
  slot, and `App.vue` mounts it.
- `FleetView.vue` loses its provider and its Reka import, and its top bar
  and `UiTableHead` move to `z-(--z-sticky)`.
- `FleetView.test.ts` mounts inside `UiAppRoot`.

Tests:
- `UiAppRoot.test.ts`: a `UiTooltip` inside the root opens after the
  provider delay.
- The existing `FleetView.test.ts` cases pass with the wrapper.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/theme frontend/web/src/ui/app frontend/web/src/ui/index.ts frontend/web/src/App.vue frontend/web/src/FleetView.vue frontend/web/src/FleetView.test.ts frontend/web/src/ui/table/UiTableHead.vue`

### U2. Overlays, toast, and audit stories

Files:
- `frontend/web/src/ui/alert-dialog/UiAlertDialog.vue`
- `frontend/web/src/ui/command/UiCommandDialog.vue`
- `frontend/web/src/ui/combobox/UiCombobox.vue` (z token only)
- `frontend/web/src/ui/dialog/UiDialog.vue`
- `frontend/web/src/ui/dropdown-menu/UiDropdownMenu.vue`,
  `UiDropdownMenu.stories.ts`
- `frontend/web/src/ui/popover/UiPopover.vue`
- `frontend/web/src/ui/form/UiSelect.vue`, `UiSelect.stories.ts`
- `frontend/web/src/ui/tooltip/UiTooltip.vue` (z token only),
  `UiTooltip.stories.ts`
- `frontend/web/src/ui/toast/UiToast.vue`, `UiToastProvider.vue`,
  `UiToast.stories.ts`
- the colocated `*.test.ts` of each component above
- `frontend/web/src/ui/a11y.test.ts`
- `frontend/web/README.md`
- `.agents/skills/web-component/references/overlays-and-motion.md`

After: U1

Change:
- Each overlay takes its token and its keyframe classes as the Decisions
  state, and the focus-return wrappers emit `closeAutoFocus`.
- The toast exit and removal work as decided.
- The audit stories and `OVERLAY_AUDITS` entries land.
- The README names the tokens, the keyframes, and `UiAppRoot`.
- The skill reference loses the interim note for the tokens and
  keyframes. It keeps the note for motion-v, which phase 2 lands.

Tests:
- **Focus return**, per wrapper that emits `closeAutoFocus`: the event
  fires on close, and a prevented default leaves focus where the handler
  put it.
- **Presence** (`UiPopover.test.ts`):
  1. Mount with `defaultOpen: true`.
  2. Wrap the real `window.getComputedStyle` so it overrides only
     `animationName`, returning `overlay-out` when the element's
     `data-state` is `closed` and `none` otherwise. `usePresence` compares
     the name recorded at mount with the one at close, and
     `PopperContent` reads other properties from the same call.
  3. Close the popover and assert the `[role="dialog"]` content is still
     mounted.
  4. Dispatch `new AnimationEvent('animationend', { animationName:
     'overlay-out' })` on that element and assert it is gone.

  This pins the keyframe exit mechanism. It cannot prove the keyframes
  look right; the browser check covers that.
- **Toast:** Requirement 4's case, and a case with `animationName` stubbed
  to the fade-out keyframe that removes the toast only after
  `animationend`.
- **Audit:** passes with the new select and tooltip entries and the
  anchored dropdown story.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/ui frontend/web/README.md .agents/skills/web-component/references/overlays-and-motion.md`

Waves: U1 | U2

## Verification

- Run the verifier over the union of changed paths. Never run `--full`.
- Stylelint requires quoted attribute values in hand-written CSS
  selectors. Tailwind variant classes are exempt because they are not
  CSS source.
- Run the browser loop in `.agents/skills/web-component/references/review.md`
  for each overlay's open story, at 390 and 1280 px wide in both themes:
  - content sits against its trigger and flips at the viewport edge
  - the exit animation plays
  - Escape closes only the top layer
  - after close, `body` carries no leftover `pointer-events` or
    `overflow` style
  - `agent-browser set media light reduced-motion` shows a fade with no
    scale

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] README and the skill reference updated in U2.
- [ ] This plan's `status` set with an outcome note. The parent's
      `Landed:` line is filled.
- [ ] No plan labels in code or comments.

## Open questions

None.
