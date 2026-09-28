---
title: Web Design System Phase 1, Token Foundations and Storybook - Plan
type: feat
date: 2026-09-26
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
compound: docs/solutions/architecture-patterns/a-multi-file-generator-validates-in-memory-before-writing.md
execution: code
parent: docs/plans/2026-09-26-2329-feat-web-design-system-plan.md
---

# Web Design System Phase 1, Token Foundations and Storybook - Plan

> Implemented. 4 units, 2026-09-26T21:54Z to 2026-09-26T22:10Z. All checks green, Storybook static build passes, and computed styles verified.

## Goal

After this phase, `frontend/web/` generates its semantic color tokens from
`design/palette-source.json` as references to scale steps, and one module
checks their contrast. Tailwind v4 utilities offer exactly those tokens,
plus a type, radius, shadow, and motion scale. Storybook shows the
foundations in both themes. Its stylesheet uses the new token names
throughout. Every semantic color moves to its nearest step in the same
family, so colors change slightly in both themes. The light canvas and its
surfaces change most. Under Verification, a computed-style capture shows
the result is layout-neutral, and screenshots show the color shift.

This plan is wrong if the Tailwind Vite plugin rewrites or reorders the
unlayered legacy `src/style.css` in a way that changes computed styles. The
U1 check below catches that. If it happens, Tailwind is loaded through a
separate entry that only the new `src/ui/` code imports.

## Decisions

- Tailwind, token tiers, dark variant, and Storybook follow the parent's
  Decisions and the
  [Web Design System](../architecture/2026-09-26-web-design-system-direction.md)
  direction.
- The Tailwind entry imports `tailwindcss/theme.css` and
  `tailwindcss/utilities.css` into their layers, and not
  `preflight.css`. Why: the legacy stylesheet relies on browser defaults.
  The import form is the one in https://tailwindcss.com/docs/preflight.
- Default theme namespaces are cleared: `--color-*`, `--font-*`,
  `--text-*`, `--radius-*`, and `--shadow-*` are set to `initial`.
  Spacing keeps Tailwind's `0.25rem` base, so `p-2` is 8px, which matches
  `--space-2`. Why: a cleared namespace offers only our values
  (https://tailwindcss.com/docs/colors, "Disabling default colors").
- Semantic colors enter Tailwind through `@theme inline`, and the literal
  type and radius scale through `@theme static`. Why: `inline` is
  documented for values that reference other variables. `static` emits
  every variable, so the legacy CSS can read `var(--radius-panel)` before
  any utility uses it (https://tailwindcss.com/docs/theme).
- The palette logic moves to `src/theme/palette.ts`: types, reference
  resolution, OKLCH-to-sRGB conversion, WCAG contrast, and CSS rendering.
  `scripts/build-palette.ts` replaces `scripts/build-palette.mjs` and runs
  with `node --experimental-strip-types`, which Node 22.12 accepts. The
  generator, the contrast test, and the Colors story then share one
  implementation. Why: today the test parses hex values out of
  `style.css` with a regular expression, so it cannot see references.
- The OKLCH-to-sRGB conversion uses the OKLab matrices from
  [CSS Color 4 §17](https://www.w3.org/TR/css-color-4/#color-conversion-code)
  and clamps each channel to [0, 1]. Why: browsers gamut-map rather than
  clamp, so for an out-of-gamut step the ratio the gate computes is
  approximate. Several gated steps are outside sRGB, for example dark
  coral-11 `[0.784, 0.1913]`. Clamping lowers the brighter color's
  luminance, so for light-on-dark pairs the gate is stricter than the
  browser, never more lenient. For dark-on-light pairs with a saturated
  foreground (the step 9 chart and border tokens), the error can go
  either way. Those pairs gate at 3:1 and pass with margin.
- The light theme gets lighter: the canvas moves from `#BEC3CA` (OKLCH L
  0.815) to `neutral-5` (L 0.859), and the other surfaces move one step up
  with it. Why: the Radix light scales were generated against `#F3F3F2`,
  and their step 11 text colors fail 4.5:1 on a canvas as dark as today's.
  A trial mapping onto the current canvas failed 11 pairs, for example
  `muted-foreground` on `background` at 4.01. The mapping below passes
  every pair in both themes (checked 2026-09-26 with the U2 conversion).
  The alternative, regenerating the light
  scales against the darker canvas, is a manual Radix-tool session that
  this phase does not take on. The lighter canvas was kept on 2026-09-27
  after comparing before and after screenshots.
- Legacy tokens are renamed in one sweep, following the table below, with
  no aliases. Why: the parent plan's rename decision.

### Semantic token mapping

Each row lists the new name, the legacy name it replaces, and the light
and dark steps. A contrast pass on 2026-09-26 confirmed this mapping
against today's pairs under the new names and against the new pairs listed
under U2, including `warning-border` on `warning-surface` at 4.93 in light
mode.

| Token | Replaces | Light | Dark |
| --- | --- | --- | --- |
| `background` | `--page` | neutral-5 | neutral-1 |
| `foreground` | `--text` | neutral-12 | neutral-12 |
| `muted-foreground` | `--muted` | neutral-11 | neutral-11 |
| `card` | `--surface` | neutral-2 | neutral-2 |
| `card-header` | `--panel-surface` | neutral-4 | neutral-2 |
| `subtle` | `--surface-subtle` | neutral-3 | neutral-3 |
| `hover` | `--surface-hover` | neutral-5 | neutral-4 |
| `popover` | new | neutral-1 | neutral-3 |
| `border` | `--line` | neutral-8 | neutral-6 |
| `input` | `--control-border` | neutral-11 | neutral-10 |
| `ring` | `--focus` | cyan-12 | cyan-11 |
| `graph-edge` | `--connection` | neutral-11 | neutral-11 |
| `primary` | `--coral` | coral-9 | coral-9 |
| `primary-foreground` | `--on-coral` | neutral-12 | neutral-1 |
| `primary-text` | `--accent-orange-text` | coral-12 | coral-11 |
| `accent` | `--cyan` | cyan-7 | cyan-9 |
| `accent-foreground` | `--accent-text` | cyan-12 | cyan-11 |
| `success-surface` | `--healthy-surface` | green-4 | green-3 |
| `success-foreground` | `--healthy-text` | green-12 | green-11 |
| `success-border` | new | green-9 | green-8 |
| `warning-surface` | `--warning-surface` | amber-4 | amber-4 |
| `warning-foreground` | `--warning-text` | amber-11 | amber-12 |
| `warning-border` | `--warning-border` | amber-11 | amber-9 |
| `danger-surface` | `--offline-surface` | rose-5 | rose-3 |
| `danger-foreground` | `--offline-text` | rose-12 | rose-11 |
| `danger-border` | `--offline-border` | rose-9 | rose-11 |
| `info-surface` | `--info-surface` | cyan-4 | cyan-3 |
| `info-foreground` | new | cyan-12 | cyan-11 |
| `info-border` | `--info-border` | neutral-10 | neutral-10 |
| `chrome` | `--chrome` | neutral-4 | neutral-1 |
| `chrome-surface` | `--chrome-surface` | cyan-4 | neutral-3 |
| `chrome-hover` | `--chrome-hover` | neutral-6 | neutral-4 |
| `chrome-border` | `--chrome-line` | neutral-8 | neutral-7 |
| `chrome-foreground` | `--chrome-text` | neutral-12 | neutral-12 |
| `chrome-muted-foreground` | `--chrome-muted` | neutral-11 | neutral-11 |
| `chrome-ring` | `--chrome-focus` | cyan-12 | cyan-11 |
| `chart-1` … `chart-6` | new | cyan-10, coral-10, violet-9, green-9, amber-10, blue-9 | cyan-9, coral-9, violet-9, green-11, amber-9, blue-9 |
| `overlay` | new | neutral-12 at 45% | neutral-1 at 70% |
| `shadow-color` | new | neutral-12 | neutral-1 |

The chart steps are the lowest in each family that reach 3:1 against
`card`. Status borders keep today's light-only gate against their own
surface and gain a gate against `card` in both themes. Dark `danger-border`
is rose-11 because rose-8 to rose-10 stay below 3:1 on `card` (2.95, 2.70,
2.23). If a pair still fails when U2's test runs, the token moves to the
nearest passing step in the same family, and the table here is updated
in the same commit.

A reference with an alpha is written in the source as
`{"ref": "neutral-12", "alpha": 0.45}` and rendered as
`color-mix(in oklch, var(--m3-neutral-12) 45%, transparent)`.

## Requirements

1. `src/theme/semantic.css` is generated, and every value in it is
   `var(--m3-…)` or a `color-mix` of one. Example:
   `:root { --background: var(--m3-neutral-5); }` and
   `:root[data-theme='dark'] { --background: var(--m3-neutral-1); }`.
2. The generated file matches its source. Example: changing
   `card.light` in the source without running the generator makes
   `palette.test.ts` fail with a mismatch message naming
   `src/theme/semantic.css`.
3. Contrast is computed from references. Example: setting
   `muted-foreground.light` to `neutral-9` fails the pair
   `muted-foreground` against `card` at its 4.5 threshold.
4. Tailwind colors are semantic only. Example: in a test component,
   `bg-card text-muted-foreground` yields rules using `var(--card)` and
   `var(--muted-foreground)`. `bg-red-500` yields no rule.
5. `dark:` follows `data-theme`. Example: `dark:bg-popover` applies under
   `<html data-theme="dark">` and not under `data-theme="light"`.
6. No legacy token name remains. Example: `grep -rE -- '--(page|text|surface|coral|on-coral|cyan|muted|line|control-border|focus|connection|accent-text|accent-orange-text|panel-surface|surface-subtle|surface-hover|healthy-[a-z]+|offline-[a-z]+|chrome-(line|text|muted|focus)|font-ui)([^a-z0-9-]|$)' frontend/web/src frontend/web/design`
   prints nothing. The docs do not carry an old-to-new name table. That
   table lives in this plan and the commit history.
7. Storybook builds and shows the foundations in both themes. Example:
   `pnpm build-storybook` exits 0, and the "Foundations/Colors" story
   lists `primary` as `coral-9` with its ratio against
   `primary-foreground`.

## Out of scope

- `Ui*` components and removing `/components` (phase 2).
- Replacing the raw shadow, font-size, and `#hex` literals in
  `style.css`, `dashboard.css`, and `brand-glow.css` (36 of them), and the
  stylelint gate (phase 5). Phase 1 renames only `var()` references.
- `design/palette.html` beyond renaming the tokens it reads. Whether
  Storybook replaces it is a phase 5 question.
- Regenerating the Radix scales.

## Units

### U1. Tailwind entry and non-color scale
Files: `frontend/web/package.json`, `frontend/web/pnpm-lock.yaml`,
`frontend/web/vite.config.ts`, `frontend/web/src/theme/tailwind.css`,
`frontend/web/src/theme/tokens.css`, `frontend/web/src/main.ts`
After: none
Change:
- Add `tailwindcss@4.3.3`, `@tailwindcss/vite@4.3.3`,
  `tailwind-variants@3.3.1`, and `tailwind-merge@^3` as dependencies, and
  add `tailwindcss()` to the Vite plugins.
- `tailwind.css` declares `@layer theme, base, components, utilities;`,
  imports the theme and utility files into their layers, then imports
  `tokens.css` and declares
  `@custom-variant dark (&:where([data-theme=dark], [data-theme=dark] *));`.
- `tokens.css` clears the five namespaces.
- An `@theme inline` block maps every color token in the table to
  `--color-<name>: var(--<name>);`. `shadow-color` is not mapped.
- An `@theme static` block holds:
  - fonts: `--font-sans` (the Inter stack from `language.css`) and
    `--font-mono`.
  - the type scale, each with a `--line-height` companion: `2xs`
    10/14px, `xs` 11/16, `sm` 12/16, `base` 13/20, `md` 14/20, `lg`
    16/24, `xl` 20/28, `2xl` 24/32, `3xl` 28/36.
  - radii: `--radius-sm` 4px, `--radius-control` 8px, and
    `--radius-panel` 12px.
  - `--ease-out: cubic-bezier(0.2, 0, 0, 1)`, which is the curve in
    `src/motion/useMotionFeedback.ts`.
- A second `@theme inline` block defines `--shadow-xs` (0 1px 2px),
  `--shadow-sm` (0 2px 4px), `--shadow-md` (0 4px 12px), `--shadow-lg`
  (0 12px 32px), and `--shadow-xl` (0 24px 60px). Each takes its color
  from
  `color-mix(in oklch, var(--shadow-color) <n>%, transparent)` at 6, 8,
  12, 20, and 33%.
- A plain `:root` block in `tokens.css` sets `--duration-hover` 90ms,
  `--duration-fast` 100ms, `--duration-base` 140ms, and
  `--duration-slow` 160ms. These have no Tailwind namespace.
- `main.ts` imports `./theme/tailwind.css` before `./style.css`.
- The type scale is sized from the 16 literal font sizes in the legacy
  CSS, where 12, 11, 10, and 13px cover 136 of 150 uses. Headings of 18,
  22, 25, 27, and 32px are folded into the nearest step in phase 5.
Tests: `frontend/web/src/theme/tailwind.test.ts` compiles the real
`tailwind.css` with `compile(css, { base, onDependency })` from
`@tailwindcss/node`, the package `@tailwindcss/vite@4.3.3` itself depends
on. Because pnpm does not expose transitive packages, it is added as an
exact-version dev dependency. The test calls `build([...candidates])` and
asserts:
- the `.bg-card` rule sets `background-color: var(--card)`.
- the output of `build(['bg-red-500'])` contains no `.bg-red-500`
  selector. The static theme block appears in every build, so the test
  checks for the selector, not for empty output.
- the `dark:bg-popover` rule's selector contains `[data-theme=dark]`.
- the `.text-base` rule reads `var(--text-base)` and
  `var(--text-base--line-height)`, and the theme block declares
  `--text-base: 13px` and `--text-base--line-height: 20px`.

Before and after this unit, the implementer runs the computed-style
capture from Verification and confirms the two captures are identical.
Nothing in the automated tests covers that risk.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/package.json frontend/web/vite.config.ts frontend/web/src/theme/tailwind.css frontend/web/src/theme/tokens.css frontend/web/src/main.ts frontend/web/src/theme/tailwind.test.ts`,
then `pnpm typecheck && pnpm test && pnpm lint && pnpm build` in
`frontend/web/`.

### U2. Palette module, semantic source, and contrast gate
Files: `frontend/web/src/theme/palette.ts`,
`frontend/web/src/theme/palette.test.ts`,
`frontend/web/design/palette-source.json`,
`frontend/web/scripts/build-palette.ts` (replaces
`scripts/build-palette.mjs`), `frontend/web/src/theme/semantic.css`,
`frontend/web/src/theme/scales.css`, `frontend/web/design/palette.json`,
`frontend/web/tsconfig.json`, `frontend/web/design/README.md`
After: none
Change:
- `palette-source.json` gains a `semantic` object with the table above.
  Each value is `{"light": Ref, "dark": Ref}`, where a `Ref` is a
  `"<family>-<step>"` string or `{"ref", "alpha"}`.
- `palette.ts` exports:
  - the source types.
  - `resolve(source, token, theme)`, which returns OKLCH L/C/H and alpha.
    It throws on an unknown family, a step outside 1 to 12, or an
    unknown token.
  - `toSrgb(oklch)`, which returns an `Srgb` tuple of three channels in
    [0, 1], and `contrast(a: Srgb, b: Srgb)`, the WCAG 2.2 ratio. It
    also exports the gated `pairs` list.
  - `renderScales(source)` and `renderSemantic(source)`, which return
    CSS strings.
- The existing `semantics` hex blocks in the JSON export are replaced by
  the resolved OKLCH strings.
- `renderSemantic` emits `color-scheme: light` in the `:root` block and
  `color-scheme: dark` in the `:root[data-theme='dark']` block, because
  those declarations leave `style.css` with the hex blocks.
- `build-palette.ts` writes `scales.css`, `semantic.css`, and
  `palette.json` from those functions. It formats each file with
  Prettier's `format` API and the repository's Prettier config before
  writing, so `pnpm format` leaves the files unchanged. Its `scales.css`
  output stays byte-identical to today's file, which is what the
  current `build-palette.mjs` template produces. It iterates `families`
  only.
- The script imports `../src/theme/palette.ts` with the `.ts` extension,
  as `--experimental-strip-types` requires, and `palette.ts` uses only
  erasable syntax: no enums, no namespaces, no parameter properties.
- `tsconfig.json` includes `scripts/**/*.ts` and sets
  `allowImportingTsExtensions: true`, which `noEmit` permits, so
  `vue-tsc` checks the script.
- `design/README.md` "Consume and regenerate" names the new command and
  describes the OKLCH semantics in the JSON export.
- The pairs are today's, under the new names, plus:
  - `foreground` 7 and `muted-foreground` 4.5 on `popover`.
  - `success-border`, `warning-border`, `danger-border`, and `info-border`
    at 3 on `card`.
  - `chart-1` to `chart-6` at 3 on `card`.
  - `info-foreground` at 4.5 on `info-surface`.
Tests: `palette.test.ts` asserts:
- every pair meets its threshold in both themes.
- the formatted output of `renderSemantic(source)` equals the file on
  disk, and the same holds for `renderScales`.
- every declaration value in `renderSemantic(source)` other than
  `color-scheme` matches `^var\(--m3-[a-z]+-(1[0-2]|[1-9])\)$` or
  `^color-mix\(in oklch, var\(--m3-[a-z]+-(1[0-2]|[1-9])\) \d{1,3}%, transparent\)$`.
  The on-disk comparison cannot catch a malformed form, because both
  sides come from the same function.
- `resolve` throws on `"neutral-13"`, on `"teal-3"`, and on a token with
  no mapping.
- `toSrgb` of the coral-9 step lies within 0.004 per channel of
  `#FF451D` (1, 0.271, 0.114). This pins the conversion against a known
  anchor, which a round trip could not do.
- `contrast([1, 1, 1], [0, 0, 0])` is 21.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src/theme frontend/web/design/palette-source.json frontend/web/scripts/build-palette.ts`,
then `node --experimental-strip-types scripts/build-palette.ts && git diff --exit-code src/theme/scales.css && pnpm typecheck && pnpm test && pnpm format:check`.

### U3. Wire semantic tokens and rename sweep
Files: `frontend/web/src/style.css`, `frontend/web/src/dashboard.css`,
`frontend/web/src/design/workbench.css`,
`frontend/web/src/theme/brand-glow.css`,
`frontend/web/src/theme/language.css`,
`frontend/web/src/components/topology/TopologyGraph.vue`,
`frontend/web/src/FleetView.vue`, `frontend/web/src/ComponentsView.vue`,
`frontend/web/design/palette.html`, `frontend/web/design/README.md`,
`frontend/web/design/language.md`,
`frontend/web/design/light-mode-contrast.md`
After: U1, U2
Change:
- `style.css` removes only the custom-property declarations from its two
  `:root` blocks. It keeps `font-family`, `color`, `background`,
  `font-optical-sizing`, and `font-synthesis`, removes `color-scheme`
  (which `semantic.css` now sets), and imports `./theme/semantic.css`
  after `scales.css`.
- `TopologyGraph.vue` builds a token name at runtime
  (`` `var(--${… ? 'connection' : 'coral'})` ``), which becomes
  `'graph-edge' : 'primary'`. `ComponentsView.vue`'s swatch list holds
  legacy names as strings (`token: '--page'` and four others), which are
  renamed too.
- `design/palette.html` links `../src/theme/tailwind.css` before
  `../src/style.css`, so it keeps the fonts and radii that now come from
  `tokens.css`.
- Every `var(--legacy)` is renamed per the table, and `--font-ui` becomes
  `--font-sans`.
- `language.css` drops `--font-ui`, `--font-mono`, `--radius-control`,
  and `--radius-panel`, which now come from `tokens.css`, and keeps
  `--space-*` and `--control-height`.
- `design/README.md` replaces the "Anchors and extensions" roles and the
  "Light-mode comfort" hex values with the semantic step table and the
  new canvas step. Its prose uses the new names (`--primary`,
  `--primary-text`, `--input`, `--border`). `light-mode-contrast.md` says
  its measured ratios predate this change.
Tests: the R6 grep in Verification. The grep cannot see a name built at
runtime, which is why the `TopologyGraph.vue` line is named above, and a
review of the diff checks it. `palette.test.ts` from U2 covers the
values. The computed-style capture in Verification covers layout
regressions, and screenshots cover color.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/src frontend/web/design frontend/web/README.md`,
then `pnpm typecheck && pnpm test && pnpm lint && pnpm format:check && pnpm build`.

### U4. Storybook with foundation stories
Files: `frontend/web/package.json`, `frontend/web/pnpm-lock.yaml`,
`frontend/web/.storybook/main.ts`, `frontend/web/.storybook/preview.ts`,
`frontend/web/tsconfig.json`, `frontend/web/eslint.config.js`,
`frontend/web/src/ui/foundations/Colors.stories.ts`,
`frontend/web/src/ui/foundations/ColorTable.vue`,
`frontend/web/src/ui/foundations/Typography.stories.ts`,
`frontend/web/src/ui/foundations/Shape.stories.ts`,
`frontend/web/README.md`
After: U1, U3
Change:
- Add `storybook@10.6.0`, `@storybook/vue3-vite@10.6.0`,
  `@storybook/addon-themes@10.6.0`, `@storybook/addon-a11y@10.6.0`, and
  `@storybook/addon-docs@10.6.0` as dev dependencies, and the scripts
  `storybook` (`storybook dev -p 6006 --host 127.0.0.1`) and
  `build-storybook`.
- `main.ts` sets the framework to `@storybook/vue3-vite`, includes the
  stories glob `../src/**/*.stories.ts`, and loads the three addons.
- `preview.ts` imports the Inter font, `../src/theme/tailwind.css`, and
  `../src/style.css`. It decorates stories with
  `withThemeByDataAttribute({ themes: { light: 'light', dark: 'dark' },
  defaultTheme: 'light', attributeName: 'data-theme' })` and sets the
  a11y addon's `test` parameter to `'error'`.
- `ColorTable.vue` lists every semantic token from the imported
  `palette-source.json`. For each it shows a swatch drawn with
  `var(--token)`, the step for the active theme, and every gated pair the
  token takes part in, with the ratio from `palette.ts`. It tracks the
  active theme with a `MutationObserver` on the `data-theme` attribute of
  `document.documentElement`, disconnected on unmount. The addon may set
  the attribute after the story renders, so reading it once could show
  the previous theme.
- "Typography" renders each `text-*` step with its size and line height
  in Inter and in mono, and tabular figures.
- "Shape" renders the radii, the shadows on `card`, and the spacing steps
  1 to 8.
- `tsconfig.json` includes `.storybook/**/*.ts` and sets
  `resolveJsonModule: true` for the source import, and the ESLint file
  globs cover `.storybook/`.
- `README.md` gains a "Design system" section covering `pnpm storybook`
  and where tokens come from.
Tests: `pnpm build-storybook` exits 0. The a11y addon, set to `'error'`,
fails a story with an axe violation during the manual pass, but not in
this build. Nothing in this unit runs axe in CI. That test-runner wiring
comes with phase 2's component stories.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- frontend/web/.storybook frontend/web/src/ui frontend/web/package.json frontend/web/tsconfig.json frontend/web/eslint.config.js frontend/web/README.md`,
then `pnpm typecheck && pnpm lint && pnpm build-storybook`.

Waves: U1 U2 | U3 | U4

## Verification

From `frontend/web/`:

```sh
pnpm install --frozen-lockfile
node --experimental-strip-types scripts/build-palette.ts && git diff --exit-code src/theme design/palette.json
pnpm typecheck && pnpm test && pnpm lint && pnpm format:check
pnpm build && pnpm build-storybook
grep -rnE -- '--(page|text|surface|coral|on-coral|cyan|muted|line|control-border|focus|connection|accent-text|accent-orange-text|panel-surface|surface-subtle|surface-hover|healthy-[a-z]+|offline-[a-z]+|chrome-(line|text|muted|focus)|font-ui)([^a-z0-9-]|$)' src design
```

The grep must print nothing.

Computed-style capture: with `pnpm dev` running, open `/dashboard` and
`/devices` at 1280px in each theme. Collect `getComputedStyle` for every
element, keeping `display`, `width`, `height`, the `margin-*`,
`padding-*`, `font-family`, `font-size`, `line-height`, `font-weight`,
and `border-*-width` properties. Save the result as JSON in the
scratchpad, using `agent-browser` or the Chrome tools. Capture once
before U1, once after U1, and once after U3. The first two must be
identical. The third may differ only in color properties, which this
capture does not collect, so it must be identical too.

Screenshots: take the same pages at 1280px and 390px in both themes
before U1 and after U3. Every semantic color moves to its nearest step,
so small shifts are expected everywhere. The screenshots are for the
user's judgment on the Open question, not a pass or fail check. Run
`pnpm storybook` and switch the Colors story between themes.

## Definition of done

- [x] Verifier green for every changed path, and the commands above pass.
- [x] `frontend/web/README.md` and `frontend/web/design/*.md` describe the
      token source, the renamed tokens, the new canvas, and Storybook.
- [x] The parent plan's U1 `Landed:` line carries the commit range.
- [x] This plan's `status` is set, with an outcome note under the title.
- [x] No plan labels in code, comments, or commit messages.

## Open questions

- None.
