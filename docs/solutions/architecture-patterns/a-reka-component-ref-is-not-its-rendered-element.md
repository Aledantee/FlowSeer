---
title: A Reka Component's Template Ref Is Not Its Rendered Content Element
date: 2026-10-05
last_verified: 2026-10-05
category: architecture-patterns
module: frontend/web/src/ui
problem_type: architecture_pattern
component: web-console
severity: medium
applies_when:
  - "Reading the DOM element of a Reka UI component through a template ref or its `$el`, to register, measure, focus, or mark it"
  - "Registering or styling the content of a popup, menu, toast, or dialog built on Reka"
  - "A ref on a Reka popup resolves to a comment node, a stale node, or a wrapper div instead of the visible content"
related_components: [vue_components, ai-targets]
tags: [reka-ui, template-ref, presence, popper, fragment, vue]
---

# A Reka Component's Template Ref Is Not Its Rendered Content Element

## Situation

Kit components wrap Reka UI 2.10.5 primitives. Code that needs the element
a component draws (the AI registry, a measurement, an attribute) often
reads it from a template ref's `$el`. For three kinds of Reka component that
element is something else, and the read goes wrong without an error.

## What is true

- **Presence keeps the outer component mounted.** Menu and toast content
  render through `Presence`, which returns the child only while it is
  present or animating out, and otherwise `null`
  (`reka-ui/dist/Presence/Presence.js:36-43`, wrapped by
  `Menu/MenuContent.js:121` and `Toast/ToastRoot.js:63`). The component that
  holds the ref outlives its content, so its `$el` becomes a comment node or
  keeps pointing at removed DOM.
- **Popper content sits inside a wrapper.** Popper-positioned content renders
  a positioning div marked `data-reka-popper-content-wrapper` around the
  content (`Popper/PopperContent.js:246`). A dropdown or popover content ref
  that forwards `$el` lands on that wrapper.
- **Fragment roots expose a marker.** A component that renders a fragment,
  such as the combobox root behind `UiCommand`, has a text or comment node as
  `$el`. Reka resolves its own refs to the next element sibling for this case
  (`shared/useForwardExpose.js:13`).

## How to apply it

To act on popup content, put a render-less child first inside the content
and read its own vnode's parent element. The child mounts and unmounts with
the content, exit presence included, and never needs to know which Reka
component it sits in:

```ts
// frontend/web/src/ui/popover/popupAnchor.ts:29-32
const content = () => {
  const marker = mounted.value ? instance?.vnode.el : undefined
  return marker instanceof Node ? marker.parentElement : undefined
}
```

Every kit popup uses it, for example `<PopupAnchor :ai="ai" ... />` in
`frontend/web/src/ui/dialog/UiDialog.vue:107`.

For a fragment root, follow the marker to its sibling as Reka does
(`frontend/web/src/ui/command/UiCommand.vue:33-39`). For a plain Reka
control whose root is its element, such as `ProgressRoot`, `$el` is safe.

## Evidence

`frontend/web/src/ui/ai/controlTargets.test.ts` mounts each popup and
asserts the registered element:

- "keeps a popup registered through its exit and drops it when the exit
  ends" (line 652) and "keeps a toast registered until its exit ends, then
  drops it" (line 679) cover the Presence lifetime.
- "registers a context menu and its item while the menu is open" (line 575)
  covers menu content that stays mounted.

Pointing the anchor one level up, at
`marker.parentElement?.parentElement`, fails 22 tests in that file, among
them "registers its anchor, follows the prop, and cleans up".

## What it does not cover

It does not cover Reka components whose `asChild` merges into a caller's
element. There the caller's own ref is the element. It also does not cover
SVG content, which the registry accepts through `isAiTargetElement` in
`frontend/web/src/ai/registry.ts`.
