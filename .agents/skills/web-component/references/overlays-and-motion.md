# Overlays and motion

Agents get these wrong more than anything else in `frontend/web/`. Each
rule below comes from a failure in this repository's history or from a
primary source. The contract that states the rules is
`docs/architecture/2026-09-28-web-component-contract-direction.md`.

The `--z-*` tokens and overlay `--animate-*` keyframes are defined in
`src/theme/tokens.css` and `src/theme/tailwind.css`. JavaScript motion uses
the motion-v surface under `frontend/web/src/ui/motion/`.

## Failures this repository already had

| Commit | Symptom | Cause |
|---|---|---|
| `62215f7f` | Sidebar froze on collapse, then jumped | A CSS `transition` on width and a `motion/mini` animation drove the same property |
| `62215f7f` | Scope combobox list rendered in the page flow | Reka `ComboboxContent` defaults to `position="inline"` |
| `f78206e4` | Focus lost after closing the Ask popover | The trigger was `tabindex="-1"`, and Reka returns focus to the trigger |
| `e02774f2` | AI button drawn over modals and row controls | A body-teleported layer used `z-index: 60`, above the overlays' 50 |
| `7cdf983d` | Overlay audits passed on closed triggers only | Stories had no open state |
| unfixed | `UiDropdownMenu` AccessibilityAudit story is empty in a browser | It opens the menu with no rendered trigger, so Floating UI parks it at `translate(0, -200%)`; happy-dom cannot see it |

## Overlay rules

1. **Portals and wrappers.** Floating content is a `Ui*` wrapper that
   renders through its Reka `*Portal`.
   - No hand positioning and no native `popover` attribute.
   - An ancestor with `transform`, `filter`, `backdrop-filter`, `contain`,
     or `will-change` becomes the containing block for `position: fixed`
     content, and clipping follows
     (https://developer.mozilla.org/en-US/docs/Web/CSS/Guides/Display/Containing_block).
2. **Positioning.**
   - `ComboboxContent` and `SelectContent` need `position="popper"`.
   - Every popper sets `:collision-padding="8"`.
   - Size lists with `--reka-*-content-available-height`, and match the
     trigger with `--reka-*-trigger-width`.
   - Style by `data-side` and `data-align`, never by the side you asked
     for.
3. **Stacking.**
   - Overlays and their scrims use `z-(--z-overlay)`, and toasts use
     `z-(--z-toast)`.
   - A hand-built layer stays below `--z-overlay`.
   - Never escalate z-index numbers to win a fight. Find the ancestor that
     creates the stacking context instead.
4. **Dismissal** belongs to Reka's `DismissableLayer`.
   - To keep an overlay open, call `preventDefault()` in
     `pointerDownOutside`, `focusOutside`, or `interactOutside`.
   - Never add a document listener for outside clicks or Escape.
   - Escape closes only the top layer.
5. **Focus.**
   - Every overlay wrapper whose Reka content emits `closeAutoFocus`
     re-emits it: dialog, alert dialog, command dialog, popover, dropdown
     menu, context menu, and select. `ComboboxContent` restores focus
     itself, and tooltip content never takes focus.
   - When the invoker is not a tab stop or may unmount (a deleted row, a
     menu item), prevent the default and focus a defined element.
   - Initial focus goes to the first control, or to the least destructive
     action when the action cannot be undone
     (https://www.w3.org/WAI/ARIA/apg/patterns/dialog-modal/).
   - A dialog always renders its title and description.
6. **Menu to dialog.** Open the dialog from controlled state on the next
   tick after the menu closes. Opening it inside the item's `select`
   handler can leave `pointer-events: none` stuck on `body`
   (https://github.com/radix-ui/primitives/issues/3317,
   https://github.com/unovue/reka-ui/issues/2822).
7. **Modal or not.**
   - Use a dialog when the page behind must be inert.
   - Use a popover for interactive content anchored to its trigger.
   - Menus open on click and on the keyboard (Enter, Space, ArrowDown),
     never on hover alone
     (https://www.w3.org/WAI/ARIA/apg/patterns/menu-button/).
8. **Tooltips.**
   - Tooltips open on hover and focus, close on Escape, and hold no
     controls.
   - A disabled trigger is wrapped in a focusable span.
   - Tooltips use the one app-root `TooltipProvider`. Never add a
     provider per component.
9. **App-root providers.** `ConfigProvider` (locale, `dir`,
   `scroll-body`) and `TooltipProvider` are mounted once, by the app-root
   `Ui` component. A view never imports them from Reka.

## Motion rules

1. **One mechanism per property.**
   - JavaScript motion uses motion-v through `UiMotion`, its `layout`
     animations, or `useMotionFeedback` from `frontend/web/src/ui/motion/`.
     Views import these surfaces from the `src/ui` barrel, never from
     motion-v directly.
   - Reduced motion comes from the one app-root
     `MotionConfig reducedMotion="user"`. Never set `reducedMotion` per
     component, and never leave it at motion-v's default of `"never"`.
   - Delete any CSS `transition` on a property motion-v drives.
2. **Overlay enter and exit use CSS keyframes on `data-state`.** They
   are `--animate-*` theme variables with their `@keyframes` inside
   `@theme` in `src/theme/tailwind.css`, applied inside the `Ui*` wrapper
   as `data-[state=open]:animate-overlay-in` and
   `data-[state=closed]:animate-overlay-out`. This is Nuxt UI's
   pattern.
   - Reka's `Presence` waits for `animationend` and ignores transitions,
     so a `transition-*` exit class never plays and the node vanishes
     (https://raw.githubusercontent.com/unovue/reka-ui/v2/packages/core/src/Presence/usePresence.ts).
   - Never scope a keyframe to a bare `[data-state]` selector. Triggers
     carry `data-state` too, so they would animate.
   - motion-v on an overlay (`forceMount`, `as-child`, and
     `AnimatePresence`) is allowed only where springs, gestures, or
     layout animation earn it. It needs a browser story test for that
     wrapper, because popper parts have had exit regressions under
     Motion.
   - Don't add `tw-animate-css`.
3. **What moves.** Only `transform` and `opacity`. Never
   `transition: all`, and never `width`, `height`, `top`, or `margin`.
   - Scale starts from 0.96, never 0.
   - Set `transform-origin` from the primitive's
     `--reka-*-content-transform-origin`.
4. **Durations and easing** come from tokens:

   | Surface | Enter | Exit |
   |---|---|---|
   | Hover feedback | `--duration-hover` (90 ms) | |
   | Menus, popovers, listboxes | `--duration-base` (140 ms) | `--duration-fast` (100 ms) |
   | Dialogs | `--duration-slow` (160 ms) | 100 ms |

   Exits are shorter than entrances. Everything uses `--ease-out`. The
   research ranges these sit in:
   - https://www.nngroup.com/articles/animation-duration/
   - https://learn.microsoft.com/en-us/windows/apps/design/signature-experiences/motion
   - https://emilkowal.ski/ui/7-practical-animation-tips
5. **What does not animate:**
   - keyboard-initiated or high-frequency surfaces (the command palette,
     typeahead lists, repeat tooltips)
   - live values, sorting, typing, and theme changes
   - anything on mount of every render
6. **Reduced motion** replaces movement with an opacity change of the
   same duration. It does not remove all feedback, and it is never a
   global `0.01ms` kill switch. Layout animations are the exception: they
   end immediately with no fade (amendment of 2026-10-01 in
   `docs/architecture/2026-09-28-web-component-contract-direction.md`).
   - Popper surfaces and dialog exits switch to `animate-fade-in` and
     `animate-fade-out` under `motion-reduce:`. Dialog entry switches to
     `animate-dialog-fade-in`, because `animate-fade-in` runs at
     `--duration-base` and would shorten it.
7. **Interruptions.** Hover and open/close toggles use transitions or
   motion-v, which retarget midway. Keyframes restart instead,
   so keep keyframes to enter and exit.

## Native feedback tests

`useMotionFeedback` accepts typed opacity and movement pairs. It compiles the
supplied movement keys into one ordered native transform effect and keeps
opacity in its own native effect. Its happy-dom tests inspect native
`KeyframeEffect` endpoints, computed offsets, timing duration and easing, and
the running state before an effect is finished or cancelled. They also cover
owned-style cleanup, cancellation, replacement, stale completions, reduced
motion, independent elements, and unrelated native animations. The named
cases live in `frontend/web/src/ui/motion/useMotionFeedback.test.ts`:

- `compiles typed pairs into ordered native effects with deterministic timing`
- `compiles only supplied transform keys and keeps opacity in a separate effect`
- the terminal-path property of `useMotionFeedback.test.ts`, whose rows run `keeps a later write to an owned property through the next frame batch and the queued timer`, `restores the owned inline values in the same turn`, and `lets the play started in the same turn snapshot the baseline`. A non-synchronous row's restore and snapshot titles name the watcher or completion that settles the path instead of "the same turn".
- `cancels and restores synchronously before replacing a play, through the next frame and completion`
- `cancels into a play and keeps the original transform through the next frame and completion`
- `resizes into a play and keeps the original transform through the next frame and completion`
- `ignores a stale completion queued before replacement`
- `ends a replacement play through cancel, resize, preference, config, and unmount`
- `filters reduced movement, keeps reduced fades native, and restores their baseline`
- `preserves a transform changed during an opacity-only play and unrelated native animations`
- `removes movement under an always preference without the query and cancels a movement-only play`
- `keeps two elements independent and leaves unrelated native animations alive`

Component coverage is in `frontend/web/src/components/ThemeSwitcher.test.ts`
and `frontend/web/src/FleetView.motion.test.ts`. ThemeSwitcher covers:

- `starts native opacity and ordered transform effects in both icon directions`
- `keeps reduced motion as one running opacity effect per icon`
- `replaces running effects on rapid toggles and restores empty inline styles on finish`
- `restores empty inline styles on window resize during rapid toggle`
- `restores empty inline styles on unmount during rapid toggle`

FleetView covers:

- `leaves the nav opacity untouched when expanding at desktop width`
- `starts no nav fade when expanding at desktop width`
- `fades the pane scope when the tenant or site changes`
- `fades nav on expand below desktop width and restores inline opacity`
- `replaces mobile expand fades within one turn`
- `restores and replays a mobile fade after resize`

Use a real browser for interpolation, computed styles, the first
`requestAnimationFrame` samples after an action, active native effects,
screenshots, and restoration after resize or cancel and replay. The browser
loop in `references/review.md` is the measurement boundary. Keep the first
sample and compare it with the latest effect's endpoints.

## Checks happy-dom cannot make

Run these in the browser loop in `review.md`:

- The content stays unclipped inside an `overflow: hidden` or transformed
  parent. Write a story that places it in one.
- It flips at the viewport edge, and a long list scrolls within the
  available height.
- The exit animation plays, and the node leaves the DOM afterwards.
- Nested overlays: Escape closes only the top one. Afterwards `body`
  carries no leftover `pointer-events` or `overflow` style.
- A menu that opens a dialog does not freeze the page.
- Emulated reduced motion leaves a fade, or nothing.

Reka 2.10.5 opens `DropdownMenuTrigger` on `click`, but `SelectTrigger`
opens on a plain left `pointerdown`. In a happy-dom test, dispatch the
event the trigger listens for; the a11y harness's `openOverlay` only
clicks. agent-browser clicks with real pointer events.

## Mount-test limits

happy-dom runs no layout and loads no app CSS. Stub
`HTMLElement.prototype.getBoundingClientRect` when a test needs geometry for
layout or positional motion. Do not mock motion-v.

A mounted `UiMotion` element reads the reduced-motion query through motion-dom
once per test file. The first element whose config needs the dynamic value
calls `initPrefersReducedMotion`, and the `change` listener binds to the
`MediaQueryList` that call returned
(`motion-dom/dist/es/render/VisualElement.mjs:205-216` and
`motion-dom/dist/es/render/utils/reduced-motion/index.mjs:4-13`, under
`frontend/web/node_modules/.pnpm/motion-dom@13.4.5/node_modules/`). The
element fixes its choice at mount, so a stub installed after the first mount
cannot select the path and a `change` after it reaches only elements mounted
afterwards. A file that mounts `UiMotion` therefore installs its
reduced-motion `matchMedia` stub before the first mount. `UiMotionConfig` reads
no query, so it mounts without a stub.

`frontend/web/src/FleetView.motion.test.ts` holds both modes by changing the
composable's query instead. Its stub returns one `MediaQueryList` per query
string (`:19-32`). motion-dom asked for `(prefers-reduced-motion)`
(`motion-dom/dist/es/render/utils/reduced-motion/index.mjs:9`) and the
composable asked for `(prefers-reduced-motion: reduce)`
(`frontend/web/node_modules/motion-v/dist/es/animation/hooks/use-reduced-motion.mjs:4`),
so these are two objects. The cases `stops layout transforms after the user
enables reduced motion` and `leaves the nav opacity untouched when expanding at
desktop width` dispatch `change` on the `reduce` object, which the composable
listens to, while `starts no nav fade when expanding at desktop width`
dispatches nothing. `FleetView.vue` gates its layout dependency on the
composable's `reduced` (`toggleSidebar`). The reduced-motion case asserts that
gate, not `UiMotion`'s reduced layout path. A file that only mounts the
composable can hold both modes for the same reason, because the composable reads
the query on each mount.

Layout animation needs a controlled clock. motion's frame loop stamps each
frame from `performance.now()`
(`motion-dom/dist/es/frameloop/batcher.mjs:22-24`), so
`FleetView.motion.test.ts`, `UiMotion.test.ts`, and `UiMotion.reduced.test.ts`
install a mocked `performance.now` before the mount and advance it
(`installMotionClock`, `advanceMotion`). Each file ends the spy in `afterEach`
(`FleetView.motion.test.ts:86`, `UiMotion.test.ts:21`,
`UiMotion.reduced.test.ts:33`), because a mocked clock left installed freezes
`performance.now()` for every later case. A fixed wall-clock wait is not
reliable: on a loaded host the 140 ms layout animation can finish before the
test samples.

Reduced motion keeps feedback fades in `useMotionFeedback` and removes
movement. `UiMotion` layout and positional animations end immediately with no
fade. `frontend/web/src/ui/motion/UiMotion.reduced.test.ts` covers both reduced
layout paths with "ends positional animation at once when the user prefers
reduced motion" and "ends layout animation at once when the user prefers
reduced motion".
