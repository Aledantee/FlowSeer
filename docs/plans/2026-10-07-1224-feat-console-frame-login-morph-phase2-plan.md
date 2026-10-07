---
title: Console Frame Shared With Login, Phase 2, One Glass - Plan
type: feat
date: 2026-10-07
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Console Frame Shared With Login, Phase 2, One Glass - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

The console wears the login page's frosted glass. The frame paints one
glass surface behind the sidebar and the top bar and a translucent page
panel on top of it, for both pages, and the console's separate chrome
styles are deleted.

**Stop condition:** text on the translucent page panel cannot reach 4.5:1
against the brand glow in one theme without making the panel opaque. The
parent's glass decision then goes back to the user.

## Decisions

The parent plan
(`docs/plans/2026-10-07-1224-feat-console-frame-login-morph-plan.md`) holds
the decision that the glass covers the frame and the page panel and that
cards and tables stay solid.

- The panel's and the sidebar's grounds become semantic tokens in
  `frontend/web/design/palette-source.json`. Why: the design system record
  lets a semantic token carry an alpha and forbids literal colours
  (`docs/architecture/2026-09-26-web-design-system-direction.md`), and the
  contrast gate reads that file.
- `frontend/web/design/light-mode-contrast.md` is re-measured for the
  frame. Why: its ratios describe the opaque chrome this phase replaces.

## Requirements

1. The login page and the console use the same glass. The example is the
   parent's requirement 3.
2. Text placed directly on the page panel stays readable. The example is
   the parent's requirement 4.

## Open questions

- Which elements of the console sit directly on the panel today and need a
  solid ground of their own, such as page headings, breadcrumbs, and the
  pane header.
- Whether the top bar keeps its own blur once the surface behind it is
  glass.
