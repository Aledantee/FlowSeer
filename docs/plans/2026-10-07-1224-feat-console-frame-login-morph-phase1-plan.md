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

- The frame has four regions, each an element with a fixed id:
  `frame-skip`, `frame-sidebar`, `frame-topbar`, and `frame-page`. Why:
  `Teleport` takes a selector, one frame exists per document, and the
  console's skip link has to stay the first child of `.shell`
  (`frontend/web/src/FleetView.vue:869`,
  `frontend/web/src/FleetView.locale.test.ts:159`).
- `frame-topbar` is a box that is at least 54px tall. `frame-skip`,
  `frame-sidebar`, and `frame-page` are `display: contents`. Why: the login
  page needs the empty strip the console's top bar fills
  (`frontend/web/src/LoginView.vue:162`), and the other regions must not
  change the flex layout their content relies on.
- `useFrame()` in `frontend/web/src/navigation/frame.ts` is the frame's
  whole contract. A view sets `sidebar` to `login`, `menu`, or `collapsed`
  and calls `moved()` when the panel should animate to its new place. It
  reads `topbarHeight`, `sidebarElement`, and `mainElement`. Why:
  `FleetView` reaches those elements today through its own refs, for the
  collapse guard, the scope fade, and the top bar's height
  (`frontend/web/src/FleetView.vue:113-122`, `391-396`, `401`).
- The frame observes `frame-topbar` and writes `--topbar-height` on
  `.main-shell`. Why: the frame owns both elements, and the height is 54px
  on the login page without any view measuring it.
- `moved()` is the only thing that starts the panel's layout animation. The
  console calls it under the two gates it has today: a desktop width and
  motion not reduced (`frontend/web/src/FleetView.vue:403-404`). The value
  of `sidebar` is not the animation's dependency. Why: a mounted `UiMotion`
  fixes its reduced-motion choice at mount, so the console's own gate is
  what stops the animation for a visitor who turns reduced motion on later
  (`docs/solutions/conventions/mount-tests-under-happy-dom-must-stub-reduced-motion.md`).
- Sign-in and logout do not call `moved()` in this phase. Why: the View
  Transitions morph still moves `.main-shell` for them
  (`frontend/web/src/navigation/morph.ts`), and the component contract
  allows one mechanism per property.
- The frame paints the chrome glow once, on `.shell`, and the panes paint
  `--background`. The sidebar has no ground of its own on the console. Why:
  the sidebar no longer scales, so while the panel slides the strip it
  uncovers has to show the chrome, not the canvas.
- The sidebar's inner `layout="position"` wrappers stay: the brand, the
  menu label, the menu, and the collapse button
  (`frontend/web/src/FleetView.vue:878-907`, `953-956`). The collapse
  button stays in the sidebar region with the rules it has in
  `frontend/web/src/style.css`, the mobile ones included. Why: each wrapper
  animates its own position, which needs no scaled parent.
- The frame sets the id `workspace-sidebar` on its `aside` only while
  `sidebar` is `menu` or `collapsed`. Why: the collapse button's
  `aria-controls` names it, and `frontend/web/src/main.test.ts:66` and `:93`
  assert that it is absent on the login page.
- The frame renders the inner corner only while `sidebar` is `menu` or
  `collapsed`. Why: the login page's panel draws its own rounded corner.
- Until the second phase the frame picks the sidebar's surface from
  `sidebar`: the login page's frosted column for `login`, nothing for
  `menu` and `collapsed`. The frosted column's rule moves from
  `LoginView.vue`'s scoped styles to `style.css`. Why: a scoped rule does
  not reach an element another component renders.
- The login view puts `is-entering` on the root element of each block it
  teleports. Why: the entrance rules match descendants of that class
  (`frontend/web/src/style.css`), and the view no longer owns `.shell`.
- The login sidebar keeps its 320px width and the console's keeps 204px
  and 64px. The frame sets the width from `sidebar`.
- Tests mount a view inside the frame through one helper,
  `frontend/web/src/navigation/frameTesting.ts`. Why: three suites and the
  login suite need the same mount, and `src/i18n/testing.ts` is the
  precedent for a test helper beside the code it serves.

## Requirements

1. The frame's elements survive sign-in. The example is the parent's
   requirement 1.
2. The menu works from inside the frame. The example is the parent's
   requirement 2.
3. A view that unmounts leaves its regions empty. Example: the login view
   is mounted in the frame, then the app navigates to `/dashboard`. The
   sidebar region contains no `form.login-form`.
4. Collapsing the sidebar moves the page panel and scales nothing. Example:
   with motion allowed at a desktop width, a click on the collapse button
   gives `.main-shell` a translate between 0 and 140px, the difference
   between 204px and 64px, and the `aside` never has `scale(` in its
   transform.
5. Reduced motion and narrow widths start no panel animation. Example: the
   two existing cases "stops layout transforms after the user enables
   reduced motion" and "fades nav on expand below desktop width and restores
   inline opacity" in `FleetView.motion.test.ts` pass with their assertions
   unchanged.
6. Sign-in starts no panel animation in this phase. Example: after the
   app signs in from `/login` and reaches `/dashboard`, `.main-shell` has no
   inline transform.
7. The sidebar's state reaches the frame. Example: after the console sets
   `sidebar` to `collapsed`, `.shell` has the class `sidebar-collapsed`.
   With the login view mounted, the `aside` has no id. With the console
   mounted, its id is `workspace-sidebar`.

## Out of scope

- Any change to colours, glass, or borders. The second phase owns the look.
- The sign-in morph and the removal of View Transitions. The third phase
  owns them.

## Units

### U1. The frame component

Files: frontend/web/src/navigation/AppFrame.vue, frontend/web/src/navigation/frame.ts, frontend/web/src/navigation/frameTesting.ts, frontend/web/src/navigation/AppFrame.test.ts
After: none
Change: `AppFrame.vue` renders `div.shell` with the chrome glow. Inside it,
in this order: the region `frame-skip`, `aside.sidebar` holding the region
`frame-sidebar`, and `div.main-shell` holding the inner corner, the region
`frame-topbar`, and the region `frame-page`. Its default slot renders
inside `.shell` after `.main-shell`. `.main-shell` is a `UiMotion` with
`layout="position"` whose dependency is a counter that `moved()` raises.
`frame.ts` provides the contract in Decisions and exports `useFrame()`.
`frameTesting.ts` mounts a component inside `UiAppRoot` and `AppFrame` with
a router and the i18n plugin, and returns the host and the router. Nothing
mounts the frame in the app yet.
Tests: `AppFrame.test.ts`. A child that teleports with `defer` into each of
the four regions appears under that region's element. Setting `sidebar` to
`collapsed` puts `sidebar-collapsed` on `.shell` and `workspace-sidebar` on
the `aside`. Setting it to `login` removes both and the inner corner.
Unmounting the child empties the regions. `topbarHeight` is 54 with nothing
in `frame-topbar`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/navigation`

### U2. The login view in the frame

Files: frontend/web/src/LoginView.vue, frontend/web/src/LoginView.test.ts
After: U1
Change: `LoginView.vue` sets `sidebar` to `login`. It teleports the brand,
the form, and the switches into `frame-sidebar` and `main.login-page` into
`frame-page`, each block carrying `is-entering` on the visit's first page.
It renders no `.shell`, `aside`, `.main-shell`, or top bar spacer of its
own. Its scoped rules for the 320px width and the frosted column are
deleted here and added to `style.css` in U3.
Tests: `LoginView.test.ts` mounts through `frameTesting.ts`. Its existing
cases pass with selectors rooted at the frame. The frame case asserts
`#frame-sidebar form.login-form` and `#frame-page .login-area`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web`,
run once U3 has landed. Until `App.vue` mounts the frame the login view has
no target to teleport into, so `main.test.ts` fails on a missing form.

### U3. The console view in the frame

Files: frontend/web/src/FleetView.vue, frontend/web/src/FleetView.test.ts, frontend/web/src/FleetView.locale.test.ts, frontend/web/src/FleetView.motion.test.ts, frontend/web/src/components/GlobalSearch.test.ts, frontend/web/src/App.vue, frontend/web/src/style.css, frontend/web/src/theme/brand-glow.css, frontend/web/src/main.test.ts
After: U2
Change: `App.vue` renders `AppFrame` around `RouterView`. `FleetView.vue`
teleports the skip link into `frame-skip`, the brand, the menu, and the
collapse button into `frame-sidebar`, `header.topbar` into `frame-topbar`,
and the panes, the dock, and the assistant into `frame-page`. It sets
`sidebar` and calls `moved()` from `toggleSidebar` under its two existing
gates. It reads `topbarHeight`, `sidebarElement`, and `mainElement` from
the frame where it used its own refs. The sidebar's `layout` wrapper and
the main area's wrapper are gone from its template. In `style.css` the
selector `.main-shell > main.panes` becomes `#frame-page main.panes` because
`UiAiContextLayer` renders a `div.contents` between the region and the panes
(`frontend/web/src/ui/ai/UiAiContextLayer.vue`). The panes gain
`background: var(--background)`, the sidebar's width rules key
on the frame's state, and the frosted column rule arrives from the login
view. `brand-glow.css` loses the selectors for elements that no longer
carry their own glow.
Tests: the three suites mount through `frameTesting.ts`. In
`FleetView.motion.test.ts` the case "animates the sidebar size and
main-shell position" becomes "moves the main shell when the sidebar
collapses and scales nothing", with the example of requirement 4. The other
eight cases keep their assertions, which is requirement 5.
`FleetView.test.ts` holds a case that clicks the Devices link inside the
`aside` and reads `/devices` from the router, which is requirement 2 and the
proof that injection survives the Teleport. `main.test.ts` keeps its two
assertions that `#workspace-sidebar` is absent on the login page, and gains
the cases of requirements 1, 3, and 6.
`GlobalSearch.test.ts` also mounts `FleetView`, so its search result case uses
the same frame helper.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web`

### U4. The record of the frame

Files: frontend/web/README.md
After: U3
Change: the README's Structure section names `AppFrame.vue`, its four
regions, and `useFrame()`. The Sign-in flow section says the frame is one
set of elements for both pages. The paragraph on the collapse tab says the
page panel slides and the rail's content changes in place.
Tests: none. The verifier checks the Markdown links and the prose.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/README.md`

Waves: U1 | U2 | U3 | U4

The graph is a chain because each unit edits the contract the next one
mounts through.

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh --base main -- frontend/web
```

No test in this workspace loads the application's stylesheet, so a person
checks four things in a browser, in both themes: the login page and the
dashboard look as before at rest, the panes still run under the top bar,
the collapse tab sits on the sidebar's edge on a desktop and beside the
brand on a phone, and no strip of canvas colour shows while the panel
slides.

## Definition of done

- The verifier is green for `frontend/web`.
- The README names the frame in the same change.
- The outcome is recorded with `plan_record.py implemented`.
- No plan label appears in code, comments, or commit messages.

## Open questions

- Whether a `layout="position"` wrapper inside the teleported sidebar
  still animates its own position when its Motion parent is the view's root
  and not the frame's `aside`. motion-v resolves the parent with Vue's
  `inject`
  (`frontend/web/node_modules/.pnpm/motion-v@2.4.4*/node_modules/motion-v/dist/es/utils/createContext.mjs`),
  so the expectation is yes. The eight unchanged motion cases pin it.
- `docs/solutions/conventions/mount-tests-under-happy-dom-must-stub-reduced-motion.md`
  names motion-v 2.5.1, and `frontend/web/pnpm-lock.yaml` holds 2.4.4. This
  plan relies on 2.4.4. The solution's version line is stale and is
  `compound`'s to refresh, not this phase's.

## Review gaps

- frontend/web/src/main.test.ts:91: call `frame.moved()` in the console view's setup; fails: "starts no panel animation when sign-in uses the handover", and the transform assertion at `:87`; class: false test
- frontend/web/src/navigation/AppFrame.vue:26: remove the watch that sets `topbarHeight` to 54 in `login` mode; fails: a case that reads 54 once the console's taller top bar has left the region; class: gap
- frontend/web/src/LoginView.vue:20: remove `frame.sidebar.value = 'login'`; fails: a case that mounts the login view after the console and finds no `workspace-sidebar` id; class: gap
- frontend/web/src/navigation/frame.ts:11: `layoutDependency` is a writable `Ref` on the context, so a view can start the panel animation without `moved()`; fails: the Decision that `moved()` is the only thing that starts it; class: convention
