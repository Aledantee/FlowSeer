# States, accessibility, forms, and copy

The axe audit catches missing names and roles. These rules cover what it
cannot see. Sources:
- Vercel Web Interface Guidelines (MIT,
  https://github.com/vercel-labs/web-interface-guidelines)
- Community-Access accessibility-agents checklists (MIT,
  https://github.com/Community-Access/accessibility-agents)
- impeccable's audit, harden, and clarify references (Apache-2.0)
- WAI-ARIA APG (https://www.w3.org/WAI/ARIA/apg/)

## States

- An interactive component covers every state it has: default, hover,
  focus-visible, active, disabled, loading, and error, plus selected and
  empty. Each state has a story.
- **Loading.**
  - Show the indicator only after 150–300 ms, then keep it visible for at
    least 300 ms, so fast answers do not flash.
  - A skeleton matches the final layout.
  - A loading button keeps its label and width.
- **Empty states** differ by cause, and each offers the next action:
  - first use
  - no results
  - everything filtered out
  - no permission
  - failure
- **Errors** say what failed, why, and how to recover. They keep what the
  user typed.
- **Optimistic updates** roll back or offer Undo when they fail.
- **Destructive actions** get a confirmation that names the object and
  the verb ("Remove core-sw-1"), never "OK", or an Undo window.
- **Hardening stories:** very long names, empty and one-character values,
  large numbers, and 1000+ rows.
  - Flex and grid children that truncate need `min-w-0`.
  - Choose truncation or line clamping deliberately.
- A pointer-drag control (split-pane handle, slider) clears its state on
  `pointercancel`, `lostpointercapture`, and `blur`. It also offers a
  keyboard and click alternative (WCAG 2.5.7).

## Keyboard and focus

- Every action is reachable by keyboard. Tab order matches visual order.
  No positive `tabindex`.
- A scrollable region with no focusable content gets `tabindex="0"` and a
  label. This covers split panes and table viewports.
- When the focused element is removed, move focus somewhere deliberate,
  never to `body`.
- Sticky headers must not cover the focused element.
- Focus rings use `--ring` and stay visible in both themes.
- Nothing is revealed on hover alone. Hover reveal also has a focus or
  selection path. This repository removed a hover-only AI button in
  `5a26da2e`.

## Announcements

- A live region is always mounted. Change its text; never render it with
  `v-if`.
- Use polite by default and assertive only for critical alerts.
- Set `aria-atomic` on status text.
- Announce state changes (generating, done, stopped, error), not each
  streamed token. Debounce rapid changes.
- Announce a load that takes longer than about 2 s.
- Toasts never take focus. An alert does not vanish on a timer the user
  cannot control.

## Targets and contrast

- Hit targets are at least 24 × 24 px, even when the visible mark is
  smaller. A checkbox and its label share one hit area.
- Non-text contrast (control borders, icons, focus rings) is at least 3:1
  in every state and both themes. The token contrast gate checks text
  pairs; check borders and icons in the browser loop.
- Color never carries meaning alone. Pair it with text, an icon, or a
  shape.

## Forms

- Labels are always visible. A placeholder is an example, not a label.
- Help text is visible and linked with `aria-describedby`, not hidden in
  a tooltip.
- Don't disable submit before the user tries. Disable it only while the
  request is in flight, which also prevents double submits.
- Show errors inline and move focus to the first one.
- Never block typing or paste. Trim values on submit.
- Addresses, IDs, MAC addresses, and codes get `spellcheck="false"` and
  `autocomplete="off"`.
- Warn before leaving with unsaved changes.

## Copy

- Sentence case. No all-caps labels.
- An action keeps its verb through the flow: a "Publish" button leads to
  a "Published" toast.
- Menu items that open further input end with `…` ("Rename…"), and so
  does progress text ("Saving…").
- Filters, tabs, pagination, and expanded panels belong in the URL
  (`PageTarget.query`), so a view can be shared and restored.
- At a decision point, offer one primary action and one or two secondary
  ones. The rest go in a menu.
