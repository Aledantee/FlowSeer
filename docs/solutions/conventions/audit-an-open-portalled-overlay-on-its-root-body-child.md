---
title: Audit an Open Portalled Overlay on Its Root Body Child, Never by Stripping aria-hidden
date: 2026-09-28
last_verified: 2026-09-28
category: conventions
module: frontend/web
problem_type: bug
component: web-console
severity: high
symptoms:
  - "axe.run(document.body) fails with aria-hidden-focus on outside triggers or focus guards when auditing open modal overlays such as select or dialog."
  - "Stripping aria-hidden globally in test harnesses silences accessibility checks across all stories, violating repository policy against test-suite suppressions."
  - "Passing document.querySelector('[role=\"tooltip\"]') directly to axe checks only a visually hidden element in Reka UI and skips visible tooltip content."
root_cause: "Modal primitives (such as Reka UI's select and dialog) invoke hideOthers to mark background elements outside the modal container with aria-hidden='true'. Because opener triggers and focus guards remain in the document flow, running axe on document.body flags them as focused elements inside an aria-hidden tree. In Reka UI 2.10.5, TooltipContentImpl places role='tooltip' on an internal VisuallyHidden component rather than the popper container. Auditing that element directly checks only hidden text."
resolution_type: code_fix
applies_when:
  - "Writing or debugging automated accessibility (axe-core) tests for portalled UI overlays, menus, select listboxes, dialogs, or tooltips."
  - "An accessibility test on an open modal overlay fails with 'aria-hidden-focus' on outside triggers or focus guards."
  - "Auditing an overlay by querying its ARIA role directly in document.body."
related_components: [web-console, testing]
tags: [vue, a11y, axe-core, reka-ui, overlays, testing]
---

# Audit an open portalled overlay on its root body child, never by stripping aria-hidden

## The situation

Automated accessibility tests verify UI component stories by mounting each story and running
`axe.run(element)` from `axe-core`. When auditing open overlays (such as `UiSelect`, `UiDialog`,
or `UiTooltip`), the overlay content portals into `document.body` outside the story container.

Running `axe.run(document.body)` on open modal overlays fails:

```text
axe violation: aria-hidden-focus (aria-hidden elements must not contain focusable elements)
```

## Why document.body and role queries fail

Headless modal overlay primitives use `hideOthers` to mark background siblings with
`aria-hidden="true"`. In a headless test runner (Vitest with `happy-dom`), the opener trigger
and Reka's focus guards (`[data-reka-focus-guard]`) remain focusable in `document.body` outside
the portalled modal. An audit of `document.body` flags these hidden guards and triggers.

Stripping `aria-hidden` attributes before running axe silences the test:

```ts
// Anti-pattern: violates AGENTS.md policy against test-suite suppressions
element.querySelectorAll('[data-aria-hidden]').forEach((el) => {
  el.removeAttribute('aria-hidden')
})
```

This suppression masks legitimate accessibility failures across all stories.

Conversely, passing `document.body.querySelector('[role="..."]')` directly to `axe.run()`
fails on tooltips. In Reka UI 2.10.5 (`reka-ui/dist/Tooltip/TooltipContentImpl.js:134-138`),
`role="tooltip"` sits on an internal `VisuallyHidden` span:

```js
createVNode(unref(VisuallyHidden_default), {
  id: unref(rootContext).contentId,
  role: "tooltip"
}, {
  default: withCtx(() => [createTextVNode(toDisplayString(ariaLabel.value), 1)]),
  _: 1
})
```

Auditing the role element alone checks only that hidden span and skips the visible tooltip
container, missing color contrast, typography, and child markup issues.

## The rule

Leave all axe rules enabled and strip no attributes. Audit the overlay's portalled root: the
direct child of `document.body` containing the target role element.

In `frontend/web/src/ui/a11y.test.ts:120-153`, locate the portalled root by finding the role
element, walking up to `document.body`, and excluding unrelated sibling portals:

```ts
const selector = `[role="${overlayAudit.role}"]`
const candidates: Element[] = []
const seen = new Set<Element>()
document.body.querySelectorAll(selector).forEach((el) => {
  let root: Element = el
  while (root.parentElement && root.parentElement !== document.body) {
    root = root.parentElement
  }
  if (seen.has(root)) return
  seen.add(root)
  if (
    root.matches('[data-ai-ask-panel]') ||
    root.querySelector('[data-ai-ask-panel]') !== null
  ) {
    return
  }
  candidates.push(root)
})

expect(candidates).toHaveLength(1)
return candidates[0]
```

Pass the resolved root to `axe.run()` (`frontend/web/src/ui/a11y.test.ts:355-362`):

```ts
const auditElement = overlayAudit
  ? await openOverlay({ container, ...overlayAudit })
  : document.body

const results = await runAudit(auditElement)
```

Closed stories continue auditing `document.body`. Open overlays audit their own portalled
root, where `hideOthers` has marked nothing internal as hidden.

## What this does not cover

This rule covers automated axe checks on portalled overlays under headless DOM environments.
It does not verify viewport flipping, CSS exit keyframes, or focus traps under real user
interaction; those require browser execution.
