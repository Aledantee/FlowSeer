---
title: Console Frame Shared With Login, Phase 2, One Glass - Plan
type: feat
date: 2026-10-07
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Console Frame Shared With Login, Phase 2, One Glass - Plan

## Goal

The console wears the login page's frosted glass. The frame paints one
glass surface behind the sidebar and the top bar and a translucent page
panel on top of it, for both pages, and the console's separate chrome
styles are deleted. The means: two semantic tokens carry the two grounds,
`AppFrame.vue` puts them on its own elements as utility classes, and the
contrast gate composites them over the brand glow.

**Stop condition:** the gate in U1 cannot pass in one theme unless
`glass-panel` is opaque. The parent's glass decision then goes back to the
user. Computed from `frontend/web/design/palette-source.json` and
`frontend/web/src/theme/brand-glow.css` with the values under Decisions,
the lowest ratios are 4.98:1 for `muted-foreground` on the light panel and
3.37:1 for `input` on the dark panel, so the condition does not hold today.

## Decisions

The parent plan
(`docs/plans/2026-10-07-1224-feat-console-frame-login-morph-plan.md`) holds
the decisions that the glass covers the sidebar, the top bar, and the page
panel, that cards and tables stay solid, and that the glass is one surface
the page panel sits on.

- The glass surface is an empty element, the first child of `.shell`,
  absolutely positioned over the whole shell. The sidebar and `.main-shell`
  paint above it. Why: MDN lists "an element with a `backdrop-filter` value
  other than `none`" as a backdrop root, and a descendant's filter then
  reaches only "the content between that parent and the child"
  (https://developer.mozilla.org/en-US/docs/Web/CSS/backdrop-filter). An
  empty surface has no descendants, so the menu highlight
  (`frontend/web/src/FleetView.vue:915`) and the pane header (`:1161`) keep
  blurring what is behind them. The same list does not name `transform`,
  so the moving `.main-shell` cuts nothing off.
- The page panel carries a ground and no blur of its own. Why: the only
  thing behind the panel is the surface, which is already blurred.
- The page panel starts below the top bar, and the top bar loses its own
  blur. Why: the parent says the panel sits on the surface and carries the
  edge between sidebar and top bar, and the login page is already laid out
  this way (`frontend/web/src/LoginView.vue:158-161` sits under the empty
  `#frame-topbar`). With nothing scrolling behind the top bar,
  `.topbar-glass` has nothing to blur. Pages and the topology canvas stop
  running under the top bar, the measured top bar height has no reader
  left, and the panel's rounded corner replaces `.main-notch`.
- Nothing on the panel needs a ground of its own. Why: the gate holds the
  composited panel to every pair it holds `background` to
  (`surfaces` in `frontend/web/src/theme/palette.ts`), and that is the
  guarantee page headings, breadcrumbs, and the topology canvas rest on
  today.
- `glass` is `neutral-2` at 45% in light and at 60% in dark. `glass-panel`
  is `neutral-5` at 55% in light and `neutral-1` at 55% in dark. Why: these
  are the login page's grounds (`bg-card/45` and `bg-background/55` in
  `frontend/web/src/navigation/AppFrame.vue:46` and `LoginView.vue:159`)
  with one change. At 45% the dark glass gives `chrome-muted-foreground`
  4.28:1 over the glow's brightest stop, below the 4.5:1 that WCAG sets for
  text (https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum). At
  60% it gives 5.02:1. The implementer may raise an alpha when the gate
  asks for it and never removes a pair.
- The gate composites over the glow as the stylesheet writes it, hairline
  highlights included and without blur. Why: `frontend/web/README.md`
  promises controls stay readable "when backdrop blur is unavailable", and
  the same WCAG page says computed ratios are not rounded. The gate does
  not model `saturate(150%)`. That effect is unverified here and a person
  checks it in a browser.
- Every glass class the frame needs is a Tailwind utility on an element in
  `AppFrame.vue`. Why: the mount suites load the views directly and
  happy-dom computes no cascade
  (`docs/solutions/conventions/a-stylesheet-to-utilities-migration-must-audit-dynamic-selectors-and-resets.md`),
  so a rule in `frontend/web/src/style.css` can be deleted with every test
  green, while a class in a template is visible to a test. Rules in
  `style.css` are unlayered and win over utilities
  (`docs/architecture/2026-09-26-web-design-system-direction.md`,
  Consequences), so no frame element keeps a legacy rule for a property a
  utility sets.
- `input` is not gated on `glass`. Why: at 60% the dark glass gives it
  2.54:1 over the brightest stop, and passing 3:1 needs a glass near 70%,
  which is no longer the login page's look. The one outlined control on
  the glass, the login field, has its own solid fill (`!bg-card` in
  `frontend/web/src/ui/form/UiInput.vue:74`), and `input` on `card` stays
  gated.
- The sidebar's mode is a class on `.shell` (`sidebar-login`,
  `sidebar-collapsed`), and the `aside` has one class list in every mode.
  Why: the parent's requirement 3 compares the `aside`'s class list across
  the two pages.
- `layoutDependency` becomes read-only for views in this phase. Why: the
  phase already rewrites `FrameContext` to drop `topbarHeight`, and the
  third phase adds callers of `moved()`. Vue documents `readonly()` as
  taking "a ref" and returning "a readonly proxy to the original"
  (https://vuejs.org/api/reactivity-core.html#readonly). The workspace pins
  Vue 3.5.43 (`frontend/web/package.json`).
- No direction record is added. Why: a semantic token with an alpha and a
  gate that reads `palette-source.json` are what
  `docs/architecture/2026-09-26-web-design-system-direction.md` already
  decides.

## Requirements

1. The login page and the console use the same glass. The example is the
   parent's requirement 3.
2. Text placed directly on the page panel stays readable. The example is
   the parent's requirement 4, checked on every stop of the glow and for
   every pair the gate holds on `background`.
3. Text on the glass stays readable. Example: `chrome-muted-foreground`
   over `glass` over the dark theme's teal stop with the highlight on top
   reaches 4.5:1, and the same pair with the glass alpha set to 0 computes
   below 3:1.
4. The frame has one blurred surface. Example: at `/dashboard`, `.shell`
   has one `.frame-glass` child with no children, `header.topbar` has no
   `.topbar-glass`, and the document has no `.main-notch`.
5. A view cannot start the panel animation by writing the dependency.
   Example: a child of the frame assigns 9 to `frame.layoutDependency.value`
   through a cast to `Ref<number>`, not a suppression comment. The value
   stays 0, and `frame.moved()` then makes it 1.

## Out of scope

- The sidebar's labels drawn over the page while it expands. The surface
  does not cure it. The parent puts the panel on top of the surface, and
  the `aside` has to paint above the panel because the collapse tab and
  the collapsed menu's labels overlap it (`.sidebar-toggle` and
  `.sidebar-collapsed .sidebar nav .nav-text` in
  `frontend/web/src/style.css`). The labels appear at full opacity while
  the panel is still on its way. The third phase owns how region content
  appears, by opacity, and the cure belongs with it.
- The login page's four area cards keep `bg-card/60`
  (`frontend/web/src/LoginView.vue:178`). The parent's requirement 3 names
  a dashboard card.
- The tints inside the chrome that are utilities with a literal alpha
  (`bg-chrome-surface/48`, `hover:bg-chrome-hover/35`, the pane header's
  `bg-background/82`). They are not tokens and the gate does not see them.
- The View Transitions rules in `style.css`. The third phase removes them.

## Units

### U1. Glass tokens and their gate

Files: frontend/web/design/palette-source.json, frontend/web/design/palette.json, frontend/web/src/theme/semantic.css, frontend/web/src/theme/tokens.css, frontend/web/src/theme/palette.ts, frontend/web/src/theme/palette.test.ts, frontend/web/src/theme/tailwind.test.ts
After: none
Change: `palette-source.json` gains `glass` and `glass-panel` with the
values under Decisions, in the `{ "ref", "alpha" }` shape `overlay` uses.
`node --experimental-strip-types scripts/build-palette.ts` regenerates
`semantic.css` and `palette.json`. `tokens.css` maps `--color-glass` and
`--color-glass-panel`. `palette.ts` gains `over(top, alpha, under)`, which
composites two sRGB colours channel by channel, and `glowStops(source,
theme, glow)`, which returns seven colours built from the numbers in
`brand-glow.css`: the base (`chrome` at 82% over `background`), `chrome`
alone, and the base under each of teal, orange, and the highlight at their
strongest. Teal and orange stack the radial at `--glow-strength` with
their largest ribbon alpha, and each is also taken with the highlight in
place of the ribbon. `glow` holds the strengths (30% by default, 13% in
light), the largest alpha per glow colour, and which token each glow
colour reads in each theme.
Tests: `palette.test.ts`. `over([1, 1, 1], 0.5, [0, 0, 0])` is
`[0.5, 0.5, 0.5]`. The test reads `brand-glow.css` as text and fails when
a strength, a token mapping, or a largest alpha differs from what it
passes to `glowStops`. For both themes and every stop, `foreground`,
`muted-foreground`, `chrome-foreground`, `chrome-muted-foreground`, and
`chrome-ring` reach 4.5:1 on `glass` over the stop, and the seven pairs
`surfaces` gets reach their minimums on `glass-panel` over `glass` over
the stop. Requirement 3's second half pins the direction of the
compositing. Nothing holds `input` on `glass`, as Decisions says. `tailwind.test.ts`: `bg-glass` compiles to
`background-color: var(--glass)` and `bg-glass-panel` to
`var(--glass-panel)`. The existing generator test holds the regenerated
files.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/design frontend/web/src/theme`

### U2. The frame owns the page panel

Files: frontend/web/src/navigation/AppFrame.vue, frontend/web/src/navigation/frame.ts, frontend/web/src/navigation/AppFrame.test.ts, frontend/web/src/navigation/chrome.test.ts, frontend/web/src/style.css, frontend/web/src/FleetView.vue, frontend/web/src/FleetView.test.ts, frontend/web/src/LoginView.vue, frontend/web/src/components/topology/TopologyGraph.vue
After: none
Change: `#frame-page` is a box: the scoped rule in `AppFrame.vue` that
gives it `display: contents` no longer names it. It carries
`flex min-h-0 flex-1 flex-col overflow-hidden rounded-tl-[18px] border-t
border-l border-chrome-border max-[800px]:rounded-tl-none
max-[800px]:border-l-0`, and `main.login-page` drops the rounding and
border classes it had. `main.panes` loses its negative top margin. The
frame drops `.main-notch`, the `ResizeObserver`, and the watch that reset
the height. `frame.ts` drops `topbarHeight` and exposes `layoutDependency`
through `readonly()`, typed `Readonly<Ref<number>>`. `FleetView.vue` drops
`span.topbar-glass`. The custom property `--topbar-height` is renamed
`--pane-inset-top` at every site: `0px` on `main.panes`, `36px` on the
split pane in place of `topbarHeight + 36`
(`FleetView.vue:1114`, `:1150`, `:1161`, `:1292`, `:1335`, the
declaration at `style.css:314`, the pane and scroll bar rules in
`style.css`, and `TopologyGraph.vue:126-164` and `:435-436`, whose helper
is renamed with it). The comments beside those sites that speak of the top
bar are rewritten with them. `style.css` loses the `.topbar-glass` and
`.main-notch` rules. The grounds stay where they are:
the console's panes stay opaque and the login page keeps its own glass
until U3.
Tests: `AppFrame.test.ts`: the two top bar height cases go.
`#frame-page` carries each class above in all three modes, no mode renders
`.main-notch`, and requirement 5's example. `FleetView.test.ts`: the case
"keeps the top bar glass in front of the page" becomes one that finds no
`.topbar-glass` in `header.topbar` and `--pane-inset-top: 36px` on the
split pane's inline style. `chrome.test.ts` is new and runs in node. It
reads `style.css`, `AppFrame.vue`, `FleetView.vue`, `LoginView.vue`, and
`TopologyGraph.vue` as text and finds none of `topbar-glass`,
`main-notch`, `--topbar-height`, and no `#frame-page` in the selector of
`AppFrame.vue`'s `display: contents` rule. Nothing holds that the panes clear the top
bar at 1280px and 390px or that the topology overlays sit right. A person
checks both.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src`

### U3. One glass

Files: frontend/web/src/navigation/AppFrame.vue, frontend/web/src/navigation/AppFrame.test.ts, frontend/web/src/navigation/chrome.test.ts, frontend/web/src/style.css, frontend/web/src/LoginView.vue, frontend/web/src/main.test.ts
After: U1, U2
Change: `.shell` gains `relative` and, as its first child,
`<div class="frame-glass pointer-events-none absolute inset-0 bg-glass
backdrop-blur-2xl backdrop-saturate-150" aria-hidden="true">`.
`#frame-page` gains `bg-glass-panel`. The `aside`'s class binding by mode
goes: it loses `login-sidebar`, `login-glass`, the borders, `bg-card/45`,
`text-foreground`, both backdrop classes, and `max-[800px]:pb-6`, which
the unlayered `.sidebar` padding already overrides, and `.shell` carries
`sidebar-login` in login mode. In `style.css` the `.login-sidebar`
selectors become `.sidebar-login .sidebar`, `.sidebar-login .main-shell`,
and `.sidebar-login #frame-topbar`, `.login-glass` is deleted,
and `#frame-page main.panes` loses its `background`. `main.login-page`
drops `bg-background/55`, `text-foreground`, and both backdrop classes.
`.sidebar-toggle::before` keeps the glow and gains
`box-shadow: inset 0 0 0 64px var(--glass)`, so the tab matches the
surface it sticks out of. An inset shadow paints over the element's own
background, follows the `clip-path`, and does not inherit the glow's fixed
560px size. `frontend/web/.stylelintrc.json` accepts a `box-shadow` that
holds a `var()`. That reading is not run here.
Tests: `AppFrame.test.ts`: `.frame-glass` is the first element child of
`.shell`, has no children, and carries `bg-glass`, `backdrop-blur-2xl`,
and `backdrop-saturate-150`. `#frame-page` carries `bg-glass-panel`. The
`aside`'s class list is the same string in `login`, `menu`, and
`collapsed`, and `.shell` carries the mode class. `main.test.ts`, in the
case that signs in: the class lists of `aside.sidebar`, `#frame-topbar`,
and `#frame-page` read at `/login` equal those at `/dashboard`,
and at `/dashboard` `#frame-page .rounded-panel.bg-card` exists and no
class under `#frame-page` matches `bg-card/`. `chrome.test.ts` also finds none of
`login-glass`, `login-sidebar`, and no `backdrop-blur-2xl` in
`LoginView.vue` or `FleetView.vue`, and the `#frame-page main.panes` block
of `style.css` holds no `background`. U1's gate holds the token values and
`tailwind.test.ts` the utilities. Nothing holds the paint order of surface,
panel, and sidebar, that the blur renders, the tab's tint, or the layout
below 800px. A person checks them as Verification says.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src`

### U4. Documentation

Files: frontend/web/README.md, frontend/web/design/README.md, frontend/web/design/light-mode-contrast.md, docs/solutions/conventions/a-stylesheet-to-utilities-migration-must-audit-dynamic-selectors-and-resets.md
After: U3
Change: `frontend/web/README.md` describes one glass surface with the page
panel on it, drops "The top bar blurs content scrolling beneath it" and
the top bar height from the `useFrame()` sentence, and keeps the sentence
about readability without blur. `design/README.md` gains `glass` and
`glass-panel` in the semantic step table, and its glow section says the
glow is on `.shell` under the surface and no longer tells authors to put
`brand-glow` on a top bar layer or describe a notch mask. The sentence
about the rounded inner corner in `frontend/web/README.md` goes the same
way. `light-mode-contrast.md` drops its scrolled header blur check and gains a table of
the gate's lowest ratio per token for glass and panel in both themes, and
says its sampled navigation ratios predate the glass. The solution's
sentences that cite `FleetView.vue:967` and `style.css:336-342` for the top
bar glass say instead that the frame's glass is held by classes in
`AppFrame.vue` and `chrome.test.ts`, and its other citations of
`style.css` and `FleetView.test.ts` lines are brought up to the tree.
Tests: `uv run tools/scripts/run.py verify check-prose` through the
verifier. The ratios in the table are the ones U1's gate computes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/README.md frontend/web/design docs/solutions`

Waves: U1 U2 | U3 | U4

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh --base main -- frontend/web docs
```

A person opens `/login` and `/dashboard` in both themes at 1280px and
390px and checks what no suite holds: the sidebar and top bar are frosted
and the glow shows through, the panel's corner and edge are where the
notch was, pages and the topology canvas stop at the top bar, the collapse
tab matches the sidebar, and text over the brightest part of the dark glow
reads.

## Definition of done

- The verifier is green for every changed path.
- `frontend/web/README.md` and `frontend/web/design/` describe the glass in
  the same change.
- The outcome is recorded with `uv run tools/scripts/run.py plan record
  implemented <plan> --units 4 --from <t> --to <t>`, or `partial`.
- No plan label appears in code, comments, or commit messages.

## Open questions

- Whether `saturate(150%)` on the surface lowers a ratio the gate passes.
  Unverified. The recommendation is to keep it, since the login page has it
  today, and to drop `backdrop-saturate-150` if the browser check finds
  text that reads worse than the gate predicts.
- Whether the sampled ratios in `light-mode-contrast.md` are taken again.
  The recommendation is to repeat the sampling when a browser is at hand
  and otherwise to leave the document saying they predate the glass.
