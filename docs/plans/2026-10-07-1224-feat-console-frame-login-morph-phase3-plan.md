---
title: Console Frame Shared With Login, Phase 3, The Morph - Plan
type: feat
date: 2026-10-07
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Console Frame Shared With Login, Phase 3, The Morph - Plan

## Goal

Signing in turns the login page into the dashboard in one continuous
movement, and logging out turns it back. The page panel moves to the
menu's width while the form gives way to the menu and the description to
the dashboard, with nothing waited for and nothing replaced. The means: the
frame starts its own layout animation when the sidebar changes between the
login width and a console width, and fades in whatever the arriving view
places into its regions.

**Stop condition:** the arriving view's content is not in the frame's
regions one tick after the sidebar mode changes. The morph then needs a
placeholder state, which is a design choice for the user.

## Decisions

The parent plan
(`docs/plans/2026-10-07-1224-feat-console-frame-login-morph-plan.md`) holds
the decisions that signing in does not wait, that the frame stays mounted,
that the panel moves by a transform, and that View Transitions go.

- The morph runs 160 ms, inside the component contract's motion budget, and
  the duration is one named constant. Why: the contract keeps motion within
  100 to 160 ms
  (`docs/architecture/2026-09-28-web-component-contract-direction.md`,
  Motion), so the record needs no amendment. (decided by the user,
  2026-10-07)
- The login page's entrance animation is removed entirely. The login page
  simply appears when a visit starts on `/login`. Why: the morph owns how
  the regions' content appears, and a second entrance would compete with
  it. (decided by the user, 2026-10-07)
- The theme and language switches sit in the top bar on the login page too,
  at the same place the console's top bar has them. They are in the same
  position on both pages, so they do not travel and do not fade. They stay
  where they are through the morph. (decided by the user, 2026-10-07)
- The frame owns the top bar's `header` and renders the two switches in it
  once. Why: an instance per view unmounts and remounts on sign-in, and
  `ThemeSwitcher` keeps its theme, its storage flag, and its status text in
  component state (`frontend/web/src/components/ThemeSwitcher.vue:20-31`).
- The theme and language switches are the last two controls on the right
  of the top bar on both pages. In the console they come after Help,
  Report bug, and the account menu. On the login page they sit flush right
  with no gap. (decided by the user, 2026-10-07)
- Help, Report bug, and the account menu stay in the console view's own
  top bar content. Why: with the switches last, nothing follows them, so
  no decision here needs those controls in the frame. Keyboard order
  follows DOM order, so the switches are reached after the account menu.
- The top bar stops moving with the panel. The layout animation moves from
  `.main-shell` to two frame elements that share the frame's layout
  dependency: the page panel `#frame-page` and the top bar's start region
  `#frame-topbar`, which holds the breadcrumb. The header's right end
  carries no layout element. Why: a transform on `.main-shell` carries the
  switches with it, 116px on sign-in and 140px on every collapse
  (`frontend/web/src/navigation/AppFrame.vue:41-47`). An element that is
  not animated cannot travel. A nested layout element that cancels the
  panel's transform lost: the console's teleported tools find the view as
  their motion parent and would slide across the switches.
- The frame starts the morph, not the views. `AppFrame.vue` watches the
  sidebar mode with `flush: 'sync'`. When the mode crosses between `login`
  and a console mode after the frame has mounted, it calls `moved()` under
  the two gates the console's collapse uses (801px and wider, motion not
  reduced). Why: the bump of the layout dependency has to reach the same
  render as the shell's class change, as the two writes in `toggleSidebar`
  do (`frontend/web/src/FleetView.vue:391-395`). motion-v snapshots a
  layout element only in the render that changes its dependency
  (`frontend/web/node_modules/.pnpm/motion-v@2.4.4*/node_modules/motion-v/dist/es/features/layout/layout.mjs:45-53`).
- `main.ts` mounts the app after `router.isReady()`. Why: a first mount is
  not a morph, and the frame tells one by `mainElement` still being `null`
  (`frontend/web/src/navigation/AppFrame.vue:11-14`). `main.ts` mounts
  before the first navigation resolves today
  (`frontend/web/src/main.ts:36`), so a signed-in reload would write
  `menu` into a mounted frame and morph.
- Arriving content fades in from opacity 0 over the constant. Departing
  content is removed at once. Why: the departing view unmounts in the
  navigation's own tick, and keeping it for an exit would be the wait the
  parent removed. The frame plays the fade with `useMotionFeedback` on the
  element children of its view-filled regions, one `nextTick` after the
  mode change, when the deferred Teleports have mounted. It also plays
  under reduced motion and below 801px, as the contract's replacement.
- The same fade cures the labels drawn over the page while the sidebar
  expands. `toggleSidebar` fades in the elements the collapsed rail hides.
  The icons stay as they are. The CSS transition on the collapsed rail's
  hover label goes. Why: the `aside` paints above the panel, so the only
  contract property that can keep the labels off the page is opacity, and
  the contract allows one mechanism per property.
- Every move that follows the frame's dependency takes the constant, the
  collapse included. Why: a transition chosen by the kind of move would
  need state in the frame, and two durations in one gesture would show.
- The switches are in the top bar at every width on both pages. Below
  801px the login page shows its top bar under the sidebar block, as the
  console does. Why: the console's collapse button has the place beside
  the brand (`.sidebar-toggle` in `frontend/web/src/style.css`).

## Requirements

1. Sign-in morphs without a wait. Example: at `/login`, 1280px wide, motion
   allowed, the panel's box mocked at left 320 in login mode and 204 in
   menu mode. Submitting `ada@example.com` sets the session and reaches
   `/dashboard` with no timer advanced. After 30 ms of the motion clock
   `#frame-page` carries an x translation between 0 and 116px, and after
   200 ms none. Every element child of `#frame-sidebar`, `#frame-topbar`,
   `#frame-topbar-tools`, and `#frame-page` has a running opacity animation
   from 0 to 1 lasting 160 ms.
2. Reduced motion gets no movement. Example: the same submit with
   `prefers-reduced-motion: reduce` records no inline transform on
   `#frame-page` at any mutation, and the same opacity animations run.
3. Logging out runs the morph in reverse. Example: Log out from the account
   menu ends at `/login` with the same `aside.sidebar` node holding the
   form. After 30 ms `#frame-page` carries an x translation between -116px
   and 0, and the form has an opacity animation from 0 to 1.
4. No View Transitions code remains. Example: no file under
   `frontend/web/src` contains `startViewTransition` or `view-transition`.
5. The login page has no entrance. Example: no file under
   `frontend/web/src` contains `is-entering`, `enter-rise`, or `takeEntrance`.
6. The switches survive and stay put. Example: `button.theme-switcher` and
   `button.locale-switcher` found at `/login` are the same connected nodes
   at `/dashboard` and again after logout. A theme switched on the login
   page is still `aria-checked` in the console. Through sign-in, a
   collapse, and logout, neither `.main-shell`, `header.topbar`, nor any
   ancestor of the switches inside the header gets an inline transform.
7. The switches end the top bar on both pages. Example: at `/login` the
   only buttons in `header.topbar` are the theme switch and then the
   language switch. At `/dashboard` the header's last three buttons in
   document order are the account trigger, the theme switch, and the
   language switch. `aside.sidebar` holds no switch on either page.
8. Expanding the sidebar fades its labels in. Example: at 1280px with
   motion allowed, collapsing and then expanding gives every `.nav-text` an
   opacity animation from 0 to 1 lasting 160 ms and gives no link's icon
   one. With reduced motion the expand starts no animation.
9. One constant sets the duration. Example: `FRAME_MOVE_SECONDS` in
   `navigation/frame.ts` is `0.16`, and the motion tests read it.
10. A page load is not a morph. Example: the app starts signed in at
    `/dashboard` with motion allowed, and `moved()` is never called.

## Out of scope

- An exit animation for the departing view's content.

## Units

### U1. Remove the entrance, the View Transitions morph, and the wait

Files: frontend/web/src/navigation/entrance.ts, frontend/web/src/navigation/morph.ts, frontend/web/src/main.ts, frontend/web/src/LoginView.vue, frontend/web/src/style.css, frontend/web/src/i18n/locales/en.json, frontend/web/src/i18n/locales/de.json, frontend/web/src/main.test.ts, frontend/web/src/LoginView.test.ts, frontend/web/src/navigation/chrome.test.ts, frontend/web/README.md, docs/solutions/conventions/mount-tests-under-happy-dom-must-stub-reduced-motion.md
After: none
Change: `entrance.ts` and `morph.ts` are deleted and `main.ts` installs
neither. `style.css` loses the entrance block with its four keyframes and
the three `view-transition-name` rules with the `::view-transition-*`
rule. `LoginView.vue` loses `takeEntrance`, every `is-entering`,
`enter-rise`, and `enter-fade` class with its `--enter-delay`, and the
wrappers that existed only to carry them. `submit` awaits `enterConsole`
directly. `signingIn`, `HANDOVER_MS`, the timer, `aria-busy`, `readonly`,
the button's `loading`, the status `span`, and the `view.login.signingIn`
key in both catalogs go. The send button shows on `sendable` alone. The
README's sign-in section drops the sentences about `morph.ts`,
`entrance.ts`, and the loading state. The solution's teardown section
loses its login entrance example and points at `stop` in
`frontend/web/src/ui/motion/useMotionFeedback.ts`, which attaches the
`finished` handler before it cancels.
Tests: `main.test.ts`: the three "enters login on initial arrival" cases
go, since requirement 5 is held by the scan. The logout case drops
`startViewTransition`, its three modes, and the `afterEach` line that
deletes the property, and keeps the route, the form, and the cleared
session. "starts no panel animation" keeps its assertions without the 600
ms wait. `LoginView.test.ts`: the two cases about the 450 ms handover are
replaced by one that submits with motion allowed and finds the session set
and `/dashboard` reached once the navigation settles, with no timer
advanced and no `aria-busy`. `chrome.test.ts` walks every file under `src`
except itself and holds requirements 4 and 5, the removed stylesheet rules
included.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web docs/solutions`

### U2. The frame owns the top bar, and only the panel and the breadcrumb move

Files: frontend/web/src/navigation/AppFrame.vue, frontend/web/src/navigation/AppFrame.test.ts, frontend/web/src/FleetView.vue, frontend/web/src/LoginView.vue, frontend/web/src/style.css, frontend/web/src/FleetView.motion.test.ts, frontend/web/src/FleetView.locale.test.ts, frontend/web/src/LoginView.test.ts, frontend/web/src/main.test.ts, frontend/web/README.md
After: U1
Change: `AppFrame.vue` renders `<header class="topbar">` with the padding,
height, and wrap classes the console's header has today, as a flex row
without `justify-between`. Its children in order: `#frame-topbar`, a
`UiMotion` with `layout="position"` that grows to fill the row.
`#frame-topbar-tools`, a `display: contents` region. A block with the two
switches, with the 6px gap the row has today and `ml-auto`, so it is flush
right when the regions are empty. At 650px and below the tools and the
switches share the first row, right-aligned, on both pages. The start
region takes the second row and takes no row while it is empty.
`.main-shell` becomes a plain `div`, and `#frame-page` becomes the
`UiMotion` with `layout="position"`. `FleetView.vue` teleports the
contents of `.topbar-start` to `#frame-topbar` and `.topbar-tools`, which
keeps its controls up to the account menu, to `#frame-topbar-tools`. It
has no `header` and neither switch. `LoginView.vue` drops the
switches and their wrapper from the sidebar. `style.css` loses the rule
that hides `#frame-topbar` in login mode below 801px, and the scoped
`min-height` on `#frame-topbar` goes. The README's structure list and
sign-in section describe the header, the frame's switches, and the two
regions. Its paragraph on the theme and language switches says they end
the row, and "The account icon sits at the far right of the top bar" is
corrected.
Tests: `AppFrame.test.ts`: requirement 7 in the three modes, the header
holds exactly one of each switch, and `#frame-page` keeps its class list.
`FleetView.motion.test.ts`: the collapse cases read `#frame-page` instead
of `.main-shell`, the geometry mock gives `#frame-page` and `#frame-topbar`
the box `.main-shell` had, and a new case holds requirement 6's last
sentence for a collapse. `main.test.ts`: the sign-in case reads
`#frame-page`. `LoginView.test.ts`: the sidebar region holds no
`button.theme-switcher`. `FleetView.locale.test.ts`: the language switch
lookup searches `header.topbar` instead of `.topbar-tools`. No case in
`FleetView.test.ts`, `FleetView.locale.test.ts`, `main.test.ts`, or
`components/*.test.ts` asserts the tools' order today, so requirement 7 is
its first holder. No suite loads the stylesheet or computes a width, so a
person checks the wrap at 650px and the switches' place at 1280px, 800px,
560px, and 390px on both pages.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web`

### U3. The frame morphs between login and console

Files: frontend/web/src/navigation/frame.ts, frontend/web/src/navigation/AppFrame.vue, frontend/web/src/navigation/AppFrame.test.ts, frontend/web/src/navigation/frameMorph.test.ts, frontend/web/src/main.ts, frontend/web/src/main.test.ts, frontend/web/README.md, docs/solutions/conventions/mount-tests-under-happy-dom-must-stub-reduced-motion.md
After: U2
Change: `frame.ts` exports `FRAME_MOVE_SECONDS = 0.16` and `frameMove`, a
transition of that duration with the ease `[0.2, 0, 0, 1]` that
`UiAppRoot.vue` uses. Both of the frame's `UiMotion` elements take
`frameMove`. `AppFrame.vue` watches the mode as Decisions describes, calls
`moved()` under the two gates, and after `nextTick` plays opacity 0 to 1
over the constant on each element child of `#frame-sidebar`,
`#frame-topbar`, `#frame-topbar-tools`, and `#frame-page`. `moved()` stays
the only writer of the dependency. `main.ts` mounts after `isReady()`. The
README's sign-in section describes the morph, its reduced-motion form, and
the login page appearing without an entrance. The solution's "140 ms
layout animation" becomes 160.
Tests: `frameMorph.test.ts` mounts `RouterView` through `mountInFrame`
with the login and console routes and `sessionRedirect`, a controlled
motion clock, and `getBoundingClientRect` mocked by the shell's mode
class, as `FleetView.motion.test.ts` does. It holds requirements 1, 2, 3,
6, and 9. Requirement 2 uses a `MutationObserver` on the panel's `style`.
`AppFrame.test.ts`: a mode change between `menu` and `collapsed` leaves
the layout dependency alone. `main.test.ts`: "starts no panel animation"
becomes a case that counts one `moved()` call on sign-in, and a new case
holds requirement 10. No suite renders pixels, so a person watches sign-in
and logout.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web docs/solutions`

### U4. Labels fade in when the sidebar expands

Files: frontend/web/src/FleetView.vue, frontend/web/src/FleetView.motion.test.ts, frontend/web/src/style.css, frontend/web/src/navigation/chrome.test.ts, frontend/web/README.md
After: U3
Change: at 801px and wider with motion allowed, `toggleSidebar` on expand
plays opacity 0 to 1 over `FRAME_MOVE_SECONDS` on the brand's name, the
`.nav-label`, every `.nav-text`, and the device count. The brand, the
`.nav-label`, the `nav`, and the collapse button pass `frameMove` as their
layout transition. The collapsed rail's `.nav-text` rule in `style.css`
loses its `transition`, so the hover label appears at once. The fade below
801px is unchanged. The README's sidebar paragraph says the labels fade
in.
Tests: `FleetView.motion.test.ts`: "starts no nav fade when expanding at
desktop width" becomes requirement 8's first half and still asserts that
the `nav` element itself has no opacity animation. "leaves the nav opacity
untouched" holds the reduced half unchanged. `chrome.test.ts` reads the
collapsed `.nav-text` rule from `style.css` and finds no `transition`.
Whether a fade is enough to keep the labels off the page is a judgment no
test makes, so a person watches the expand in both themes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web`

Waves: U1 | U2 | U3 | U4

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh --base main -- frontend/web docs
```

A person signs in and logs out once in a browser in both themes at 1280px
and 390px, with motion allowed and with reduced motion, and checks what no
suite renders: the panel and the content read as one movement at 160 ms,
the switches do not move, the menu's highlight keeps its blur while the
menu fades in, the labels are not drawn over the page while the sidebar
expands, the breadcrumb reads well sliding while the top bar's right end
stands still, and the login top bar shows only the two switches.

## Definition of done

- The verifier is green for every changed path.
- The README and the solution change in the units that change behaviour.
- The outcome is recorded with `uv run tools/scripts/run.py plan record
  implemented <plan> --units 4 --from <t> --to <t>`, or `partial`, and no
  plan label appears in code, comments, or commit messages.

## Open questions

- Whether the fade alone keeps the expanding sidebar's labels off the
  page. Unverified. The labels and the panel share one duration and one
  ease, so a label is faint while the panel is far from its place. The
  recommendation is to ship the fade and let the person's check decide
  whether more is needed.
