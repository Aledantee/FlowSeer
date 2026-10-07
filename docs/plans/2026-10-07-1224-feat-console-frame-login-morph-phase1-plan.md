---
title: Console Frame Shared With Login, Phase 1, The Frame - Plan
type: refactor
date: 2026-10-07
artifact_contract: flowseer-plan/v2
execution: code
---

# Console Frame Shared With Login, Phase 1, The Frame - Plan

## Goal

One frame component, mounted once, owns the console's shell. The login view
and the console view place their content into it, and the frame's elements
are the same DOM nodes before and after sign-in. Both pages look as they do
now at rest.

**Stop condition:** a case in `FleetView.test.ts` or
`FleetView.locale.test.ts` that asserts pages, panes, search, or the dock
fails for a reason other than where its mount helper looks for the shell.
The parent plan's stop condition then applies.

## Decisions

The parent plan
(`docs/plans/2026-10-07-1224-feat-console-frame-login-morph-plan.md`) holds
the decisions on the frame, on Teleport, and on moving the page panel by a
transform. The decisions below are local to this phase.

- The frame exposes its state through `useFrame()` in
  `frontend/web/src/navigation/frame.ts`. A view sets `sidebar` to `login`,
  `menu`, or `collapsed`, and reads `topbarHeight`. Why: the frame owns the
  `.shell` element, which carries the `sidebar-collapsed` class today
  (`frontend/web/src/FleetView.vue:866-867`), and the console still decides
  when the sidebar collapses.
- The regions are elements with fixed ids: `frame-sidebar`, `frame-topbar`,
  and `frame-page`. Why: `Teleport` takes a selector, and one frame exists
  per document.
- The console's sidebar keeps the id `workspace-sidebar`. It moves to the
  frame's `aside`. Why: the collapse button's `aria-controls` and
  `frontend/web/src/main.test.ts` name it.
- The collapse animation changes from a scaled sidebar to a moved page
  panel in this phase. Why: the parent's transform decision, and the
  sidebar's `layout` wrapper cannot move to the frame without it.
- The login sidebar keeps its 320px width and the console's keeps 204px and
  64px. The frame sets the width from `sidebar`. Why: the email field needs
  the room, and the menu does not.
- Until the second phase the frame picks the surface classes of the sidebar
  and the top bar from `sidebar`: the login page's glass for `login`, the
  console's chrome for `menu` and `collapsed`. Why: this phase changes who
  owns the elements, not how either page looks.
- The View Transitions morph stays in this phase. Why: its names sit on
  `.sidebar`, `.main-shell`, and `.product-brand`, which stay unique with
  one frame, and removing it is the third phase's work.
- Tests mount a view inside the frame through one helper,
  `frontend/web/src/navigation/frameTesting.ts`. Why: three suites and the
  login suite need the same mount, and `src/i18n/testing.ts` is the
  precedent for a test helper beside the code it serves.

## Requirements

1. The frame's elements survive sign-in. The example is the parent's
   requirement 1.
2. The menu works from inside the frame. The example is the parent's
   requirement 2.
3. A view that unmounts leaves its regions empty. Example: the helper
   mounts the login view, then the app navigates to `/dashboard`. The
   sidebar region contains no `form.login-form`.
4. Collapsing the sidebar moves the page panel and scales nothing. Example:
   with motion allowed, a click on the collapse button gives `.main-shell`
   a translate that starts at 140px, the difference between 204px and 64px,
   and `aside.sidebar` never has a scale in its transform.
5. The sidebar's state reaches the frame. Example: after the console sets
   `sidebar` to `collapsed`, `.shell` has the class `sidebar-collapsed`.
   After the login view mounts, `aside.sidebar` is 320px wide at a desktop
   width.

## Out of scope

- Any change to colours, glass, or borders. The second phase owns the look.
- The sign-in morph and the removal of View Transitions. The third phase
  owns them.

## Units

### U1. The frame component

Files: frontend/web/src/navigation/AppFrame.vue, frontend/web/src/navigation/frame.ts, frontend/web/src/navigation/frameTesting.ts, frontend/web/src/navigation/AppFrame.test.ts
After: none
Change: `AppFrame.vue` renders `div.shell`, `aside.sidebar` with the region
`frame-sidebar`, and `div.main-shell` holding the inner corner, the region
`frame-topbar`, and the region `frame-page`. It renders its default slot
after them. `frame.ts` provides `sidebar` and `topbarHeight` and exports
`useFrame()`. The frame sets `sidebar-collapsed` on `.shell` and the
sidebar's width from `sidebar`. `frameTesting.ts` mounts a component inside
`UiAppRoot` and `AppFrame` with a router and the i18n plugin, and returns
the host and the router. Nothing mounts the frame in the app yet.
Tests: `AppFrame.test.ts`. A child that teleports into each region appears
under that region's element. A child that sets `sidebar` to `collapsed` puts
`sidebar-collapsed` on `.shell`. Unmounting the child empties the regions.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/navigation`

### U2. The login view in the frame

Files: frontend/web/src/LoginView.vue, frontend/web/src/LoginView.test.ts
After: U1
Change: `LoginView.vue` sets `sidebar` to `login` and teleports the brand,
the form, and the switches into `frame-sidebar` and the description into
`frame-page`. It renders no `.shell`, `aside`, or `.main-shell` of its own.
The 320px rules move from its scoped styles to the frame.
Tests: `LoginView.test.ts` mounts through `frameTesting.ts`. Its existing
cases pass with selectors rooted at the frame. The frame case asserts
`#frame-sidebar form.login-form` and `#frame-page .login-area`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web`,
run once U3 has landed, since the app shows two shells until `App.vue`
mounts the frame.

### U3. The console view in the frame

Files: frontend/web/src/FleetView.vue, frontend/web/src/FleetView.test.ts, frontend/web/src/FleetView.locale.test.ts, frontend/web/src/FleetView.motion.test.ts, frontend/web/src/App.vue, frontend/web/src/style.css, frontend/web/src/main.test.ts
After: U2
Change: `App.vue` renders `AppFrame` around `RouterView`. `FleetView.vue`
teleports the brand, the menu, and the collapse button into
`frame-sidebar`, the top bar into `frame-topbar`, and the panes, the dock,
and the assistant into `frame-page`. It sets `sidebar` through `useFrame()`
and reads `topbarHeight` from it. The `layout` wrapper leaves the sidebar.
The page panel keeps `layout="position"` in the frame, keyed on `sidebar`.
The collapse button moves to the page panel's left edge. `style.css` loses
the rules that only the scaled sidebar needed.
Tests: the three suites mount through `frameTesting.ts`. In
`FleetView.motion.test.ts`, the case "animates the sidebar size and
main-shell position" becomes "moves the main shell when the sidebar
collapses and scales nothing", with the example of requirement 4. The case
"stops layout transforms after the user enables reduced motion" asserts the
same for `.main-shell`. The other seven cases keep their assertions.
`FleetView.test.ts` holds a case that clicks the Devices link inside
`aside.sidebar` and reads `/devices` from the router, which is requirement 2
and the proof that injection survives the Teleport. `main.test.ts` gains the
case of requirement 1 and the case of requirement 3.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web`

### U4. The record of the frame

Files: frontend/web/README.md
After: U3
Change: the README's Structure section names `AppFrame.vue`, its three
regions, and `useFrame()`. The Sign-in flow section says the frame is one
set of elements for both pages.
Tests: none. The verifier checks the Markdown links and the prose.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/README.md`

Waves: U1 | U2 | U3 | U4

The graph is a chain because each unit edits the contract the next one
mounts through.

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh --base main -- frontend/web
```

A person collapses and expands the sidebar once in a browser, and opens
`/login` and the dashboard, to confirm that nothing looks different at rest.

## Definition of done

- The verifier is green for `frontend/web`.
- The README names the frame in the same change.
- The outcome is recorded with `plan_record.py implemented`.
- No plan label appears in code, comments, or commit messages.

## Open questions

- Whether `Teleport defer` resolves in happy-dom when the frame and the
  view mount in one tick. Unverified. U1's first test answers it. If it
  does not, the frame renders its regions before its slot and the views use
  a plain `Teleport`, which Vue allows when the target is already in the
  document.
- Whether motion-v's `layout="position"` on the page panel animates when
  the change that moves it is a class on an ancestor. The sidebar is
  `position: fixed` and the panel moves through `margin-left`
  (`frontend/web/src/style.css`), as it does today, so the expectation is
  yes. The rewritten motion case in U3 pins it.
