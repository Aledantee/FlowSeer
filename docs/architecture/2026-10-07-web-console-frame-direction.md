---
title: Web Console Frame - Direction
type: direction
date: 2026-10-07
topic: web-console-frame
status: proposed-direction
---

# Web Console Frame - Direction

The [web component contract](2026-09-28-web-component-contract-direction.md)
settles how one component is built and how it moves. It does not say who
owns the page around the components. Until this work the console view
rendered its own shell, and a second top-level page, the login page, had to
render a copy of it. Going from one to the other replaced every element on
screen.

## Decision

### One frame, mounted once

`frontend/web/src/navigation/AppFrame.vue` owns the shell: the glass
surface, the sidebar, the top bar, and the page panel. `App.vue` renders it
once around `RouterView`, so its elements stay in the document across
every route change, sign-in and logout included.

A routed view owns no shell element. It fills the frame's regions with
`<Teleport defer>`. The regions are elements with fixed ids: `frame-skip`,
`frame-sidebar`, `frame-topbar`, `frame-topbar-tools`, and `frame-page`.

For example, a view that shows a menu and a page:

```vue
<template>
  <Teleport defer to="#frame-sidebar">
    <nav>…</nav>
  </Teleport>
  <Teleport defer to="#frame-page">
    <main>…</main>
  </Teleport>
</template>
```

Teleport moves the rendered nodes and leaves the component tree alone, so
what the view provides still reaches its teleported children
(https://vuejs.org/guide/built-ins/teleport). The console's menu links
inject the page context the console view provides, and keep working from
inside the frame's sidebar.

A view talks to the frame through `useFrame()` in
`frontend/web/src/navigation/frame.ts`. It sets `sidebar` to `login`,
`menu`, or `collapsed`. It calls `moved()` when the page panel should
animate to a new place. The animation's dependency is read-only for views.

Controls that are the same on every page belong to the frame. The account
menu is one element in the frame's top bar, last on the right, and no view
renders its own. It holds the theme and language choices on every page, and
adds Help, Report a bug, and Log out outside the login mode
(`frontend/web/src/components/AccountMenu.vue`).

### The panel moves, the sidebar does not scale

A change of the sidebar's width moves the page panel by a transform. The
sidebar's surface has no animation of its own.

The component contract allows only `transform` and `opacity` to animate. A
scaled sidebar would need its children scaled back, and motion-v finds a
child's parent through Vue's `inject`. A teleported child's parent is the
view, not the frame, so the frame could not correct it.

Content that arrives in a region fades in by opacity. Content that leaves
goes at once, since an exit animation is a wait. Every frame movement runs
for `FRAME_MOVE_SECONDS`, 160 ms, the top of the contract's motion budget.
Below 801px, or with reduced motion, nothing moves and the fade still
plays.

### One glass surface

The glass is one empty element, the first child of the shell. The sidebar
and the main area paint above it. The page panel carries a translucent
ground and no blur of its own. Cards and tables inside the panel stay
opaque.

An element with a `backdrop-filter` is a backdrop root, and a descendant's
own filter then reaches only what lies between the two
(https://developer.mozilla.org/en-US/docs/Web/CSS/backdrop-filter). An
empty surface has no descendants, so a blurred element inside the sidebar
or the panel still blurs what is behind it.

The two grounds are the semantic tokens `glass` and `glass-panel` in
`frontend/web/design/palette-source.json`. The contrast gate composites
them over the stops of the brand glow and holds text to 4.5:1 and outlines
to 3:1 on the result, since a translucent ground has no single colour to
test against.

Every class that carries the glass is a utility on an element in
`AppFrame.vue`. No suite loads the application stylesheet, so a rule there
can be deleted with every test green, and a class in a template is visible
to a test.

## Alternatives

- **Named router views.** The sidebar, the top bar, and the page would be
  three components per route. The console view shares its state between
  those parts in one file, and splitting it was a larger change than the
  frame.
- **The View Transitions API.** The browser morphs between pictures of two
  pages. It shipped first, and was removed: the frame's elements persist,
  so there are no two pictures left to move between, and it left a browser
  without the API with no morph at all.
- **A frame per view.** Each view renders the frame component itself. The
  elements are then replaced on every change of view, which is what this
  record ends.

## Consequences

- A new top-level page teleports into the frame and sets `sidebar`. It
  cannot add a shell element of its own.
- A view is mounted in tests through
  `frontend/web/src/navigation/frameTesting.ts`, since its regions do not
  exist without the frame.
- The page panel starts below the top bar. Pages and the topology canvas
  do not run under it.
- A component inside the panel paints no opaque ground of its own, or it
  hides the glass. `frontend/web/src/navigation/chrome.test.ts` reads the
  stylesheets to hold the cases found so far.
- `frontend/web/src/main.ts` mounts the app after the router resolves the
  first route, so a signed-in reload plays no morph.

Landed 2026-10-07: the frame, the shared glass, and the sign-in morph in
`frontend/web/src/navigation`, `frontend/web/src/LoginView.vue`,
`frontend/web/src/FleetView.vue`, and `frontend/web/design`.
