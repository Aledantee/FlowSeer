---
title: A Reka Combobox Popup Needs an Explicit Anchor, and a happy-dom Test Can Assert That It Was Placed
date: 2026-10-07
last_verified: 2026-10-07
category: conventions
module: frontend/web/src/ui
problem_type: bug
component: web-console
severity: high
symptoms:
  - "A combobox opens, moves focus into its popup, and reports aria-expanded='true', while nothing appears on the page."
  - "The popper wrapper in document.body keeps the inline style transform: translate(0, -200%)."
  - "Every mount test of the component passes."
root_cause: "Reka UI 2.10.5 positions popper content against the anchor its PopperRoot holds, and keeps the content at translate(0, -200%) until that anchor exists. ComboboxRoot renders a PopperRoot, and no combobox part except ComboboxAnchor registers an anchor with it. Select and Popover triggers render a PopperAnchor themselves, so the same wrapper shape works there and fails for a combobox."
resolution_type: code_fix
applies_when:
  - "Writing or changing a Ui* wrapper over a Reka primitive whose content uses position='popper', above all a combobox or one with a custom trigger."
  - "A portalled popup opens and takes focus but is not visible, or its wrapper carries transform: translate(0, -200%)."
  - "Deciding what a happy-dom mount test can prove about overlay placement."
related_components: [web-console, testing]
tags: [vue, reka-ui, overlays, combobox, testing, happy-dom]
---

# A Reka Combobox Popup Needs an Explicit Anchor, and a happy-dom Test Can Assert That It Was Placed

## The situation

`UiCombobox` rendered a trigger or an input, then `ComboboxContent` with
`position="popper"` inside a portal. The tenant and site switchers in the
console breadcrumb opened, took focus, and showed nothing. The popup was
rendered above the top of the page.

## What is true

Reka's popper content measures against one reference and stays off the page
until it has been positioned:

```ts
// frontend/web/node_modules/reka-ui/src/Popper/PopperContent.vue:339
const reference = computed(() => props.reference ?? rootContext.anchor.value);
```

```ts
// frontend/web/node_modules/reka-ui/src/Popper/PopperContent.vue:400
transform: isPositioned ? floatingStyles.transform : 'translate(0, -200%)', // keep off the page when measuring
```

Only a `PopperAnchor` sets that anchor
(`frontend/web/node_modules/reka-ui/src/Popper/PopperAnchor.vue:30`).
`ComboboxRoot` renders the `PopperRoot`
(`frontend/web/node_modules/reka-ui/src/Combobox/ComboboxRoot.vue:256`), and
of the combobox parts only `ComboboxAnchor` renders a `PopperAnchor`.
`ComboboxInput` and `ComboboxTrigger` do not. `SelectTrigger`
(`frontend/web/node_modules/reka-ui/src/Select/SelectTrigger.vue:99`) and
`PopoverTrigger`
(`frontend/web/node_modules/reka-ui/src/Popover/PopoverTrigger.vue:31`) do,
which is why a wrapper copied from a select or popover works there and
leaves a combobox unanchored.

## How to apply

Wrap the control the popup should sit under in the family's anchor part:

```vue
<!-- frontend/web/src/ui/combobox/UiCombobox.vue:268 -->
<ComboboxAnchor as-child>
  <UiCustomComboboxTrigger v-if="$slots.trigger" as-child :anchor="setTrigger">
    <slot name="trigger" />
  </UiCustomComboboxTrigger>
  <slot v-else name="input">
    <ComboboxInput ref="input" />
  </slot>
</ComboboxAnchor>
```

Before wrapping another Reka family, open its trigger part under
`frontend/web/node_modules/reka-ui/src/<Family>/` and check whether it
renders `PopperAnchor`.

happy-dom runs no layout, yet it can prove the popup was placed. Floating UI
computes a position from zero-size rectangles once it has a reference, and
the parked transform goes away. With no reference it never does:

```ts
// frontend/web/src/ui/combobox/UiCombobox.test.ts:501
const wrapper = document.body.querySelector<HTMLElement>(
  "[data-reka-popper-content-wrapper]",
);
expect(wrapper?.style.transform).not.toContain("-200%");
```

Without the anchor that test fails with
`expected 'translate(0, -200%)' not to contain '-200%'`. Give every popper
wrapper one such test per control variant.

## What this does not cover

The test proves that a position was computed, not that it is the right one.
Side, alignment, collision, and stacking still need a real browser
(`.claude/skills/web-component/SKILL.md`, step 4). The claims about Reka
hold for version 2.10.5, pinned in `frontend/web/package.json`.
