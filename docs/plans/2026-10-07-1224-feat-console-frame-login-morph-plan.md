---
title: Console Frame Shared With Login - Plan
type: feat
date: 2026-10-07
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Console Frame Shared With Login - Plan

## Goal

The frame that shows the sign-in form becomes the console without being
replaced. The sidebar element that held the form narrows and shows the menu,
the page panel that held the description shows the dashboard, and both pages
wear the same frosted glass. The means: one frame component, mounted once
above the router, owns the shell's elements, and the login view and the
console view each place their content into its regions.

**Stop condition:** the three `FleetView` suites cannot mount the console
inside the frame without rewriting what their cases assert about pages,
panes, search, or the dock. Placing content by `Teleport` is then the wrong
seam, and the console view has to be split first.

## Branch

The work continues on `Aledantee/mobbin-mcp-capabilities`. The login page,
the preview session, and the route guard this plan builds on are in that
worktree and are not committed at `6edd129c`. They are committed before the
first phase starts, so the first unit has a base to diff against.

## Decisions

- The frame stays mounted across sign-in. Why: a morph of one element is
  the only kind that works in every browser and never shows two pictures of
  the page cross-fading. (decided by the user, 2026-10-07)
- The glass covers the sidebar, the top bar, and the page panel. Cards and
  tables inside the panel stay solid. Why: it matches the login page as it
  is, so the morph has nothing to repaint. (decided by the user, 2026-10-07)
- The flow field background is gone from the login page. Why: the user
  removed it after comparing alternatives. It is already deleted from the
  worktree. (decided by the user, 2026-10-07)
- The frame is `frontend/web/src/navigation/AppFrame.vue`, rendered by
  `frontend/web/src/App.vue` around `RouterView`. It has a region for
  the sidebar, the top bar, and the page, and one for the skip link. A routed view fills a region with
  `<Teleport defer>`. Why: Vue states that Teleport "does not affect the
  logical hierarchy of the components" and that "injections from a parent
  component work as expected"
  (https://vuejs.org/guide/built-ins/teleport). `FleetView` provides
  `pageContext` and `workspaceContext`
  (`frontend/web/src/FleetView.vue:344-345`), and the sidebar's `AppLink`
  injects them (`frontend/web/src/navigation/AppLink.vue`), so the menu
  keeps working from inside the frame. `defer` exists since Vue 3.5, and the
  workspace pins 3.5.43 (`frontend/web/package.json`). A deferred Teleport
  resolves its target after the render that mounts it, so the frame and a
  view can mount in one tick.
- Named router views lost. Why: they need the sidebar, the top bar, and the
  panes as three components, and `FleetView` shares about sixty pieces of
  state between those parts in one 1,378-line file.
- A change of the sidebar's width moves the page panel by a transform. The
  sidebar surface itself does not scale. Why: the component contract allows
  only `transform` and `opacity` to animate
  (`docs/architecture/2026-09-28-web-component-contract-direction.md`,
  Motion, Properties). A scaled sidebar would need its children scaled
  back, and motion-v finds a child's parent through `inject`
  (`injectMotion` in
  `frontend/web/node_modules/.pnpm/motion-v@2.4.4*/node_modules/motion-v/dist/es/components/motion/use-motion-state.mjs:13`,
  built on Vue's `inject` in `dist/es/utils/createContext.mjs` of the same
  package). A teleported child's parent is the view, not the frame, so
  the frame could not correct it.
- The glass is one surface behind the sidebar and the top bar. The page
  panel sits on it and carries the edge between the two. Why: with one
  surface, a sidebar that changes width needs no animation of its own. Only
  the panel moves.
- Signing in does not wait. The 450 ms loading state on the send button
  goes. Why: the user asked for a morph "directly into the actual
  dashboard", and nothing is being waited for.
- The View Transitions morph (`frontend/web/src/navigation/morph.ts`) is
  removed once the frame carries the morph. Why: the frame's elements
  persist, so there are no two pictures left to move between.
- Three phases, in sequence. Why: every phase edits `AppFrame.vue`,
  `frontend/web/src/style.css`, and the two views, so no two of them are
  independent.

## Requirements

1. The frame's elements survive sign-in. Example: the app starts signed out
   at `/login`. A test keeps references to `aside.sidebar` and
   `.main-shell`, submits `ada@example.com`, and waits for `/dashboard`.
   Both references are still connected to the document, and the sidebar
   contains the menu link to `/devices`.
2. The menu works from inside the frame. Example: with the console mounted
   in the frame, a click on the Devices link in `aside.sidebar` changes the
   route to `/devices`.
3. The login page and the console use the same glass. Example: the class
   lists of `aside.sidebar`, the top bar region, and the page region are
   equal at `/login` and at `/dashboard`. A `UiCard` on the dashboard has an
   opaque `bg-card`.
4. Text placed directly on the page panel stays readable. Example: the
   contrast test composites the panel's translucent ground over the lightest
   and the darkest stop of the brand glow in each theme, and
   `--foreground` and `--muted-foreground` reach 4.5:1 on all four results.
5. Sign-in morphs without a wait. Example: with motion allowed, a valid
   submit sets the session and navigates in the same turn. The page panel's
   transform starts at the offset of the 320px sidebar and ends at zero, and
   the menu and the dashboard start at opacity 0.
6. Reduced motion gets no movement. Example: with
   `prefers-reduced-motion: reduce`, the same submit leaves no transform on
   the page panel at any frame.
7. Logging out runs the morph in reverse. Example: Log out from the account
   menu ends at `/login` with the same `aside.sidebar` node holding the
   form.
8. No View Transitions code remains. Example: a search of
   `frontend/web/src` for `startViewTransition` and `view-transition-name`
   finds nothing.

## Out of scope

- Splitting `FleetView` into smaller components.
- A real identity provider. The session stays the preview one in
  `frontend/web/src/session/session.ts`.
- Translucent cards, tables, popovers, or dialogs.
- A replacement background effect for the login page.

## Units

### U1. The frame

Files: docs/plans/2026-10-07-1224-feat-console-frame-login-morph-phase1-plan.md
After: none
Change: `AppFrame.vue` owns the shell, the sidebar, the main area, and the
inner corner. The login view and the console view teleport into its regions.
The look of both pages at rest is unchanged. Claims requirements 1 and 2.
Tests: named in the phase plan.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web`

### U2. One glass

Files: docs/plans/2026-10-07-1224-feat-console-frame-login-morph-phase2-plan.md
After: U1
Change: the frame paints the frosted sidebar, top bar, and page panel for
both pages, and the console's own chrome styles go. Claims requirements 3
and 4.
Tests: named in the phase plan when it is re-planned.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web`

### U3. The morph

Files: docs/plans/2026-10-07-1224-feat-console-frame-login-morph-phase3-plan.md
After: U2
Change: sign-in and logout move the page panel and swap the regions'
content with the frame in place, and the View Transitions code and the
loading wait are removed. Claims requirements 5 to 8.
Tests: named in the phase plan when it is re-planned.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web docs/architecture`

Waves: U1 | U2 | U3

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh --base main -- frontend/web docs
```

A person watches sign-in and logout once in a browser in both themes, since
no test in this workspace renders motion.

## Definition of done

- The verifier is green for every changed path.
- `frontend/web/README.md` describes the frame, and the motion record is
  amended in the change that needs it.
- Each phase's outcome is recorded with `plan_record.py implemented`.
- No plan label appears in code, comments, or commit messages.

## Open questions

- How long the morph runs. The component contract keeps motion inside
  100 to 160 ms. A panel that travels 116px while two regions swap may read
  as a jump at 160 ms. The third phase either stays inside the budget or
  amends the record with a longer duration for this one morph. Unconfirmed.
  The recommendation is to try 160 ms first and amend only if a person finds
  it abrupt.
- Whether the login content still animates in when a visit starts on
  `/login` (`frontend/web/src/navigation/entrance.ts`). The third phase
  decides, since it owns how the regions' content appears.
