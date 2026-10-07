---
title: A Layout Animation Needs a Nonzero Source Box
date: 2026-10-07
category: conventions
module: frontend/web
problem_type: convention
component: web-console
severity: medium
applies_when:
  - "A motion-v layout element is hidden or empty before content arrives and is expected to move when it appears."
  - "A Vue layout-dependency changes but one region has no transform while populated sibling regions move."
related_components: [web-console, testing]
tags: [vue, motion, layout, display-none, testing]
---

# A layout animation needs a nonzero source box

`layout="position"` and a changed layout dependency do not guarantee a move.
Motion-dom discards a snapshot when both measured dimensions are zero. A
region hidden with `display: none` before content arrives has no starting box
for the layout transition.

In the frame, `#frame-topbar` uses `empty:hidden` and starts empty on the login
page (`frontend/web/src/navigation/AppFrame.vue:88-95`). It cannot slide in
with the populated page panel on sign-in. Once the console fills the region,
it can move on sidebar collapse, as the transform assertion in
`frontend/web/src/FleetView.motion.test.ts:197-210` shows. The behavior is
described in `frontend/web/README.md:173-181`.

When adding a layout move, check that the element has a measurable box before
the change. For a region that starts hidden, animate its arriving content
separately or give the region a visible starting box if the design requires
movement. Test the transition from the actual hidden state as well as later
transitions between populated states. A geometry stub that always returns a
nonzero rectangle, like `frontend/web/src/navigation/frameMorph.test.ts:44-62`,
cannot prove the first move occurs in a browser.

Motion-dom's `create-projection-node.mjs:553-561` contains the decisive check:

```js
if (this.snapshot &&
    !calcLength(this.snapshot.measuredBox.x) &&
    !calcLength(this.snapshot.measuredBox.y)) {
    this.snapshot = undefined;
}
```

That file is supplied by the `motion-dom@13.4.1` dependency pinned in
`frontend/web/pnpm-lock.yaml`. This rule concerns layout snapshots. It does
not prevent an opacity animation on the content that arrives in the region.
