---
title: Web Component Contract Migration, Phase 2 - motion-v Replaces motion/mini - Plan
type: refactor
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: rework
execution: code
amends: docs/architecture/2026-09-28-web-component-contract-direction.md
parent: docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-plan.md
---

# Web Component Contract Migration, Phase 2 - motion-v Replaces motion/mini - Plan

> Implemented. 4 units, 2026-10-01T13:15:44Z to 2026-10-01T14:00:14Z.

## Goal

JavaScript motion runs on motion-v. `UiAppRoot` sets
`MotionConfig reducedMotion="user"` once. The three callers of
`useMotionFeedback` (`FleetView.vue`, `WorkspacePage.vue`,
`ThemeSwitcher.vue`) move to motion-v. Where the sidebar resize is a
layout change, its hand-rolled read, `nextTick`, read, play sequence
gives way to motion-v's `layout` animation. The `motion` package is
removed once nothing imports it. The means: motion-v is imported only
under `frontend/web/src/ui/motion/`, and views take `UiMotion` and
`useMotionFeedback` from the `src/ui` barrel.

Stop condition: in a real browser, `layout` cannot resize the sidebar
without stretching its content. The sidebar would then need a mechanism
this plan does not name.

## Decisions

`dist/` and `lib/` paths are inside the published package at the named
version, under `frontend/web/node_modules/` once U1 has installed it (pnpm
keeps transitive packages under `.pnpm/`). Other bare paths are relative to
`frontend/web/`. The four units form one cluster, so this stays one plan.

- **The contract's 2026-09-28 amendment governs.** It approves `motion-v`
  and `@vueuse/core` and the removal of `motion`, which is the approval the
  `web-component` skill's dependency step asks for. That step still applies
  to any other package. `hey-listen` arrives only as motion-v's dependency.
- **`motion-v` `^2.5.1` and `@vueuse/core` `^14.4.0`.** motion-v 2.5.1 is
  the registry's latest (MIT, published 2026-09-28) and has the peer
  `@vueuse/core >=10.0.0`. The lockfile already holds 14.4.0 for `reka-ui`,
  and the registry's 15.0.0 would add a third copy.
- **One copy each of `motion-dom` and `motion-utils`, checked in the
  lockfile.** motion-v depends on `framer-motion`, `motion-dom`, and
  `motion-utils` at `^13.3.0`. Through `motion` the lockfile holds 13.4.5,
  13.4.5, and 13.3.0. The registry published 13.5.0 of each on 2026-10-01,
  and whether pnpm 11.25.0 reuses the locked ones is unverified. motion-v
  imports `motion-dom` directly and through `framer-motion/dom`
  (`dist/es/index.mjs`), so two copies would each hold their own
  reduced-motion state (`dist/es/render/utils/reduced-motion/state.mjs`).
  U1 and U4 run the check below. It prints three lines. On more, the unit
  stops and reports the versions, since `pnpm dedupe`, the likely remedy,
  can move unrelated packages.

  ```bash
  grep -oE "^  (framer-motion|motion-dom|motion-utils)@[0-9][^:(]*" \
    frontend/web/pnpm-lock.yaml | sort -u
  ```
- **motion-v is imported only under `src/ui/motion/`.** Its `index.ts`
  exports motion-v's `Motion` as `UiMotion`, its `MotionConfig` as
  `UiMotionConfig`, and the moved `useMotionFeedback`. The parent's
  requirement 2 greps for `from '(reka-ui|motion)` outside `src/ui/`, which
  matches `motion-v` and today prints `src/motion/useMotionFeedback.ts`. A
  `UiMotion.vue` wrapper would re-declare `Motion`'s props and need a story
  (`src/ui/a11y.test.ts`). The contract names the old path, so U4 amends it.
  The amendment is accepted. (decided by the user, 2026-10-01)
- **`UiAppRoot` mounts the config with `reducedMotion="user"` and
  `transition { duration: 0.14, ease: [0.2, 0, 0, 1] }`.** motion-v
  defaults to `"never"` (`dist/es/components/motion-config/context.mjs`)
  and gives a component without its own `transition` the config's
  (`dist/es/utils/resolve-motion-props.mjs`). The values are
  `--duration-base` and `--ease-out` in `src/theme/tokens.css`.
- **Components take reduced motion from that config.** motion-dom 13.4.5
  then ends positional keys and layout animations at once and keeps playing
  opacity (`dist/es/animation/interfaces/visual-element-target.mjs:85`,
  `dist/es/projection/node/create-projection-node.mjs:328`). A component
  reads the preference when it mounts (`dist/es/render/VisualElement.mjs:205`).
  The highlight remounts on every navigation. The sidebar nodes stay
  mounted, so `toggleSidebar` checks the preference itself.
- **`useMotionFeedback` drops movement itself under reduced motion and
  plays only `opacity`.** motion-v's `animate` ignores the config, since
  `useAnimate` forwards only `skipAnimations`
  (`dist/es/animation/hooks/use-animate.mjs`). motion-dom's `reduceMotion`
  option jumps a positional key to its end value, which would show the
  outgoing theme icon rotated before it fades. `play` therefore takes
  `opacity`, `x`, `y`, `rotate`, and `scale` as `[from, to]` number pairs,
  where a `transform` string would hide which part is movement.
- **The nav highlight is a shared layout element, `layoutId`.** The span
  unmounts in one link and mounts in the next (`src/FleetView.vue:918`),
  which replaces the hand-written FLIP at `src/FleetView.vue:354`.
- **The sidebar resize is a layout change and uses `layout`.** Only the
  `sidebar-collapsed` class on `.shell` changes `.sidebar`'s width and
  `.main-shell`'s `margin-left` (`src/style.css:50`, `156`, `162`, `218`).
  - `layout` animates size through `scale`, which stretches children that
    are not layout nodes
    ([layout animation](https://motion.dev/docs/vue-layout-animations)).
    `layout="position"` slides a node and lets its size snap, so
    `.main-shell` and the sidebar's children take it.
  - motion-v measures a node in `onBeforeUpdate`
    (`dist/es/components/motion/use-motion-state.mjs:95`), and with
    `layoutDependency` set only when that value changes
    (`dist/es/features/layout/layout.mjs:50`). The class sits on an
    ancestor, so the nodes share a counter. It moves only at
    `(min-width: 801px)` with reduced motion off. Below that width today's
    code animates nothing except the nav fade (`src/FleetView.vue:395`).
  - Projection writes inline `transform`, which would override the
    toggle's `transform: translateY(-50%)` (`src/style.css:91`).
- **Test shim: the repository's `matchMedia` stub stays, and no
  `offsetParent` stub is added.**
  - motion-v's own setup has none to copy. Its
    [`vitest.config.ts`](https://raw.githubusercontent.com/motiondivision/motion-vue/v2.5.1/packages/motion/vitest.config.ts)
    sets `environment: 'jsdom'` and no `setupFiles`, its `test` script is
    `vitest --dom`, and its layout, config, and `useAnimate` tests stub
    neither.
  - happy-dom 20.14.5 defines no `offsetParent`, which motion-v's
    `isHidden` compares with `null` (`dist/es/utils/is-hidden.mjs`). It
    implements `Element.animate` (`lib/nodes/element/Element.js:1083`) and
    answers the media query from its settings
    (`lib/match-media/MediaQueryItem.js:187`), so the stub only selects the
    reduced path.
  - motion-dom keeps the first `MediaQueryList` it reads
    (`dist/es/render/utils/reduced-motion/index.mjs:4`). A file that mounts
    motion components installs the stub before its first mount, and
    normal-motion cases live in a file without it.

## Requirements

1. No file imports `motion/mini` or `motion`, and `package.json` no
   longer lists `motion`.
2. Under reduced motion, the sidebar and the scope highlight change
   without movement. Example: with the media query emulated, collapsing
   the sidebar sets its final width in the same frame.
3. Tests that trigger motion run under happy-dom without throwing. The
   re-plan decides the shim (an `offsetParent` stub, or the reduced-motion
   stub the repository already uses) from motion-v's own test setup.

## Out of scope

- Overlay enter and exit, which stay on CSS keyframes as the amendment says.
- CSS transitions motion-v does not drive: the toggle icon's rotation, the
  pane `flex-basis`, and `TopologyGraph.vue`'s own reduced-motion check.
- Strings in the touched views (phase 4), and a story for the shell.
- A preference turned off mid-session, which mounted sidebar nodes miss.

## Units

### U1. motion-v, the app-root config, and the src/ui motion surface

Files: `frontend/web/package.json`, `frontend/web/pnpm-lock.yaml`, `frontend/web/src/ui/app/UiAppRoot.vue`, `frontend/web/src/ui/motion/index.ts`, `frontend/web/src/ui/motion/useMotionFeedback.ts`, `frontend/web/src/ui/motion/useMotionFeedback.test.ts`, `frontend/web/src/ui/motion/UiMotion.test.ts`, `frontend/web/src/ui/motion/UiMotion.reduced.test.ts`, `frontend/web/src/ui/index.ts`
After: none
Change:
- `package.json` lists `motion-v` and `@vueuse/core`. `motion` and
  `src/motion/` stay until U4, so the callers keep working between units.
- `src/ui/motion/index.ts` holds the three exports the Decisions name.
  `src/ui/index.ts` re-exports `UiMotion` and `useMotionFeedback`, and
  `UiAppRoot` renders `UiMotionConfig` inside `TooltipProvider`.
- `useMotionFeedback()` returns `play(element, keyframes, duration = 0.14)`,
  `cancel(element)`, and `reduced`. `reduced` is true when motion-v's
  `useMotionConfig()` says `"always"`, or `"user"` while its
  `useReducedMotion()` is true. `play` runs motion-v's `animate` with ease
  `[0.2, 0, 0, 1]`, and passes on only the `opacity` pair while `reduced`.
  After an animation ends or is cancelled, the element's inline `opacity`
  and `transform` hold their values from before `play`. A window `resize`,
  a preference change, and unmount cancel every running animation.

Tests: all mount under `UiAppRoot`, and none mocks motion-v or Vue.
- `useMotionFeedback.test.ts`: `play(el, { y: [-4, 0], opacity: [0.6, 1] })`
  holds an intermediate `translateY` mid-flight and leaves the inline
  `transform` and `opacity` empty once it ends. Stubbed to reduced, the
  inline `transform` stays empty while `el.getAnimations()` holds the fade.
  The resize, preference-change, replacement, and unmount cases of
  `src/motion/useMotionFeedback.test.ts` carry over without mocks.
- `UiMotion.test.ts`, with no stub. `<UiMotion :animate="{ x: 100 }">`
  reads exactly 100px after 250 ms, which motion-v's default spring does
  not. A parent's class toggles while two children carry a changing
  `layout-dependency`, one with `layout` and one with `layout="position"`,
  and `getBoundingClientRect` answers from the parent's class. Mid-flight
  the first holds `scale(` and the second a translate only, which pins that
  the first box measured predates the class change. A re-render that leaves
  the dependency alone never calls the stub.
- `UiMotion.reduced.test.ts` installs the reduced stub before its first
  mount. Both cases end at once, which fails when the config is missing or
  at `"never"`.
- The lockfile check prints three lines.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/package.json frontend/web/pnpm-lock.yaml frontend/web/src/ui/app/UiAppRoot.vue frontend/web/src/ui/motion/index.ts frontend/web/src/ui/motion/useMotionFeedback.ts frontend/web/src/ui/motion/useMotionFeedback.test.ts frontend/web/src/ui/motion/UiMotion.test.ts frontend/web/src/ui/motion/UiMotion.reduced.test.ts frontend/web/src/ui/index.ts`

### U2. FleetView: shared highlight, layout sidebar, feedback from src/ui

Files: `frontend/web/src/FleetView.vue`, `frontend/web/src/FleetView.test.ts`, `frontend/web/src/FleetView.motion.test.ts`, `frontend/web/src/style.css`
After: U1
Change:
- The highlight span is
  `<UiMotion as="span" layout-id="nav-highlight" :layout-dependency="section">`.
  The `watch(section, …)` at `src/FleetView.vue:354` is deleted.
- `.sidebar` is a `UiMotion` with `layout`. `.main-shell`, `.product-brand`,
  the `nav`, and `.sidebar-toggle` are `UiMotion` with `layout="position"`.
  All five bind one counter as `layout-dependency`.
- `toggleSidebar` flips `sidebarCollapsed`. At `(min-width: 801px)` with
  `reduced` false it also increments the counter. Below that width an
  expand runs `play(nav, { opacity: [0.6, 1] }, 0.1)`. The
  `getComputedStyle` reads, the `nextTick`, and the `cancel` calls go.
- The scope-change feedback at `src/FleetView.vue:376` keeps
  `{ opacity: [0.85, 1] }` over 0.12 s, with `useMotionFeedback` from `./ui`.
- `sidebar`, `mainShell`, and `navigation` become component refs. The
  observer at `src/FleetView.vue:85` writes `--topbar-height` through the
  main shell's `$el`.
- `src/style.css`: `.sidebar-toggle` centres with `top: calc(50% - 32px)`.
  Its `transform: translateY(-50%)` and the `transform: none` under
  `(max-width: 800px)` are deleted.

Tests:
- `FleetView.test.ts` already stubs reduced motion and a matching
  `min-width`, and its comment at line 15 about the Web Animations API is
  corrected. Both files stub `getBoundingClientRect` to 204 and 64 px by
  the `sidebar-collapsed` class. A click on the toggle sets that class and
  `aria-expanded="false"` and leaves the sidebar and main shell without a
  transform. Navigating to `/devices` leaves one `.nav-highlight`, inside
  the Devices link. Hovering the toggle shows `Collapse sidebar`, which
  pins Reka's `TooltipTrigger as-child` over `UiMotion`.
- `FleetView.motion.test.ts` stubs `matchMedia` to match only `min-width`
  queries. Mid-flight the sidebar's inline transform holds `scale(` and the
  main shell's a translate with no scale. The stub then turns reduced on
  and fires `change`, and the next toggle leaves both without a transform.
- happy-dom loads no app CSS, so the browser step checks requirement 2's
  width example and the absence of stretched content. No test here does.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/FleetView.vue frontend/web/src/FleetView.test.ts frontend/web/src/FleetView.motion.test.ts frontend/web/src/style.css`

### U3. WorkspacePage and ThemeSwitcher feedback from src/ui

Files: `frontend/web/src/WorkspacePage.vue`, `frontend/web/src/components/ThemeSwitcher.vue`, `frontend/web/src/components/ThemeSwitcher.test.ts`
After: U1
Change: both import `useMotionFeedback` from the `src/ui` barrel. The
notice at `src/WorkspacePage.vue:256` plays
`{ opacity: [0.6, 1], y: [-4, 0] }`. The theme icons play `opacity`,
`rotate`, and `scale` pairs with today's numbers: the sun going dark is
`{ opacity: [1, 0], rotate: [0, 45], scale: [1, 0.65] }`.
Tests:
- `ThemeSwitcher.test.ts` is new and mounts under `UiAppRoot`. The
  composable reads the media query itself, so one file holds both modes. A
  click flips `aria-checked`, and mid-flight the sun's inline transform
  holds `rotate(`. Once it ends both icons have an empty inline `transform`
  and `opacity`. With the reduced stub the transforms stay empty and each
  icon holds one running animation.
- `WorkspacePage.vue` has no test of its own. `vue-tsc` rejects the old
  `transform` string, and the `FleetView.test.ts` cases that mount it keep
  passing. Nothing in this unit covers the notice animation at runtime.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/WorkspacePage.vue frontend/web/src/components/ThemeSwitcher.vue frontend/web/src/components/ThemeSwitcher.test.ts`

### U4. Remove motion and update the documents

Files: `frontend/web/package.json`, `frontend/web/pnpm-lock.yaml`, `frontend/web/src/motion/useMotionFeedback.ts`, `frontend/web/src/motion/useMotionFeedback.test.ts`, `frontend/web/README.md`, `.agents/skills/web-component/SKILL.md`, `.agents/skills/web-component/references/overlays-and-motion.md`, `docs/solutions/conventions/mount-tests-under-happy-dom-must-stub-reduced-motion.md`, `docs/solutions/README.md`, `docs/architecture/2026-09-28-web-component-contract-direction.md`
After: U2 U3
Change:
- `src/motion/` is deleted, and `motion` leaves `package.json` and the
  lockfile.
- `README.md` names motion-v, `UiMotion`, `useMotionFeedback` under
  `src/ui/motion/`, and the config in the app root, and says the theme
  icons crossfade without rotating under reduced motion. It holds every
  README change U2 and U3 cause, so no unit of that wave touches the file.
- `overlays-and-motion.md` loses its "Until motion-v lands" block and its
  `grep` check, and its motion rule 1 names `UiMotion` and the
  `src/ui/motion/` import rule. `SKILL.md`, the solution document, and its
  row in `docs/solutions/README.md` say what the stub selects, that a file
  installs it before its first mount, and that no stub is needed to avoid a
  throw.
- The direction record gains a dated amendment, which a person re-reads
  before landing. It says `useMotionFeedback` lives at
  `frontend/web/src/ui/motion/useMotionFeedback.ts`, views take motion from
  the `src/ui` barrel, and `UiMotion` is a re-export with no story. Under
  reduced motion the composable keeps only the fade, and layout animations
  end at once with no fade.

Tests: `grep -rnE "from 'motion(/mini)?'" frontend/web/src` and
`grep -rlE "from 'motion-v'" frontend/web/src | grep -v '^frontend/web/src/ui/motion/'`
print nothing, as does the parent's requirement 2 command.
`grep -c '"motion"' frontend/web/package.json` and
`grep -c "^  motion@" frontend/web/pnpm-lock.yaml` print 0. The lockfile
check prints three lines, and `pnpm test` in `frontend/web` passes.

Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/package.json frontend/web/pnpm-lock.yaml frontend/web/src/motion/useMotionFeedback.ts frontend/web/src/motion/useMotionFeedback.test.ts frontend/web/README.md .agents/skills/web-component/SKILL.md .agents/skills/web-component/references/overlays-and-motion.md docs/solutions/conventions/mount-tests-under-happy-dom-must-stub-reduced-motion.md docs/solutions/README.md docs/architecture/2026-09-28-web-component-contract-direction.md`

Waves: U1 | U2 U3 | U4

Phase 3 also edits `frontend/web/src/ui/app/UiAppRoot.vue`,
`frontend/web/package.json`, and `frontend/web/pnpm-lock.yaml`. Whichever
phase lands second merges those and regenerates the lockfile with
`pnpm install`.

## Verification

- The verifier over the union of changed paths, without `--full`, and
  `pnpm test` from `frontend/web`.
- A browser step for U2 against `pnpm dev`, at 1280 × 800 and 390 × 844,
  since `FleetView` has no story. When agent-browser is missing, the report
  names these checks for the user (`web-component` skill, step 4).
  On collapse and expand no text or icon in the sidebar is drawn stretched,
  the toggle stays vertically centred, the panes still run up under the top
  bar, and the highlight slides to the selected page. At 390 a toggle moves
  nothing and an expand fades the nav in. With reduced motion emulated, the
  sidebar's computed width is final in the frame after the click, no
  element carries a transform, and the theme icons crossfade.

## Definition of done

- [ ] The verifier is green for every changed path, and the browser step
      ran or the report says why it did not.
- [ ] Requirements 1 to 3 hold, the lockfile check prints three lines, and
      the documents U4 names changed with the code.
- [ ] This plan's `status` is set with an outcome note, the parent's U2
      `Landed:` line carries the commit range, and no plan label is in code.

## Open questions

- `.nav-label` is `display: none` while the sidebar is collapsed
  (`src/style.css:171`) and stays a plain element. How motion-v projects a
  `layout="position"` node with no box in one state is unverified, so the
  browser step decides whether the label becomes one.
- Reka's `TooltipTrigger as-child` over a motion-v component is unverified.
  If U2's tooltip test fails, the `layout="position"` node becomes a
  wrapper around `UiTooltip` that takes the toggle's absolute positioning.
- pnpm 11.25.0's reuse of the locked versions is unverified. The cited
  lines are from 13.4.5, and U1's reduced tests catch a version that differs.
