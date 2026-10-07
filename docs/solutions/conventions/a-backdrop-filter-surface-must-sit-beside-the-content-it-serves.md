---
title: A backdrop-filter surface must sit beside the content it serves
date: 2026-10-07
category: conventions
module: frontend/web/src/navigation
problem_type: convention
component: web-console
severity: medium
applies_when:
  - "Placing a backdrop-filter on a shared frame while child regions also need to show content behind the frame"
  - "Debugging a nested backdrop-filter that blurs only content inside its parent"
related_components: [design-system, accessibility]
tags: [css, backdrop-filter, glass, layout]
---

# A backdrop-filter surface must sit beside the content it serves

## Situation

A filtered parent becomes a backdrop root. A descendant's own
`backdrop-filter` can sample only content between that root and the
descendant, so nesting filtered chrome inside a filtered frame cannot keep
sampling the scene behind the frame. The [CSS backdrop-filter reference](https://developer.mozilla.org/en-US/docs/Web/CSS/Reference/Properties/backdrop-filter#backdrop_root)
states this boundary.

## Apply it

Put the frame blur on an empty, positioned sibling before the content regions.
Give that sibling a translucent ground and no pointer events. Put text and
controls in later siblings, where they can share the filtered surface without
becoming descendants of a backdrop root. In this tree the frame glass is the
first child of `.shell`, followed by the sidebar and page region
(`frontend/web/src/navigation/AppFrame.vue:24-51`). The mount test holds its
empty sibling shape and blur classes (`AppFrame.test.ts:95-117`).

```vue
<div class="shell relative">
  <div class="frame-glass pointer-events-none absolute inset-0 bg-glass backdrop-blur-2xl"></div>
  <aside class="sidebar">...</aside>
  <main class="main-shell">...</main>
</div>
```

## Evidence

> `<div class="frame-glass pointer-events-none absolute inset-0 bg-glass backdrop-blur-2xl backdrop-saturate-150"`

`frontend/web/src/navigation/AppFrame.vue:25-28` places the filtered surface
before the sidebar. The same file places `#frame-page` later at `:48-51`.
`frontend/web/src/navigation/AppFrame.test.ts:101-105` checks that the glass
is the first element child and has no children. The CSS reference above
defines why this placement preserves the intended backdrop for the frame.

## Limit

The mount test holds DOM shape and classes. It does not prove rendered blur or
paint order. Check those in a browser when changing the frame's positioning or
stacking.
