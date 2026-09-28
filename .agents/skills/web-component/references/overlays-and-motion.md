# Overlays and motion

Agents get these wrong more than anything else in `frontend/web/`. Each
rule below comes from a failure in this repository's history or from a
primary source. The contract that states the rules is
`docs/architecture/2026-09-28-web-component-contract-direction.md`.

The `--z-*` tokens, the overlay `--animate-*` keyframes, and motion-v
land with the contract's migration plan. Check whether they exist:

```bash
grep -c -- "--z-overlay" frontend/web/src/theme/tokens.css; grep -c -- "--animate-overlay-in" frontend/web/src/theme/tailwind.css; grep -c '"motion-v"' frontend/web/package.json
```

A `0` means that piece has not landed. Until it does:
- Overlays keep `z-50`, and a hand-built layer stays below it.
- A new overlay's keyframes go in the component's own `<style>`, reading
  the duration tokens.
- JavaScript motion stays on `useMotionFeedback` over `motion/mini`.
- Say in the report which piece is waiting on the migration.

## Failures this repository already had

| Commit | Symptom | Cause |
|---|---|---|
| `62215f7f` | Sidebar froze on collapse, then jumped | A CSS `transition` on width and a `motion/mini` animation drove the same property |
| `62215f7f` | Scope combobox list rendered in the page flow | Reka `ComboboxContent` defaults to `position="inline"` |
| `f78206e4` | Focus lost after closing the Ask popover | The trigger was `tabindex="-1"`, and Reka returns focus to the trigger |
| `e02774f2` | AI button drawn over modals and row controls | A body-teleported layer used `z-index: 60`, above the overlays' 50 |
| `7cdf983d` | Overlay audits passed on closed triggers only | Stories had no open state |
| (found 2026-09-28) | `UiDropdownMenu` AccessibilityAudit story is empty in a browser | It opens the menu with no rendered trigger, so Floating UI parks it at `translate(0, -200%)`; happy-dom cannot see it |

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
   - JavaScript motion uses motion-v, which the contract's amendment
     approved: the `motion` component, `layout` animations, or `animate`
     through `useMotionFeedback`.
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
   global `0.01ms` kill switch.
7. **Interruptions.** Hover and open/close toggles use transitions or
   motion-v, which retarget midway. Keyframes restart instead,
   so keep keyframes to enter and exit.

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
