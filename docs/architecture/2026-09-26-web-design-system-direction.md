---
title: Web Design System - Direction
type: direction
date: 2026-09-26
topic: web-design-system
status: accepted-direction
---

# Web Design System - Direction

The web console under `frontend/web/` has a reviewed color foundation: eight
Radix-generated 12-step families in `src/theme/scales.css` and a
contrast-tested semantic layer. The rest grew page by page. Its semantic
tokens are 33 hand-picked hex values that reference no scale step, so a
palette change does not reach them. About 3,200 lines of `src/style.css`
style every component by class name. Only the tooltip and the scroll area
sit on Reka UI primitives, and menus, selects, and switchers are hand-built.
A `/components` route previews three components and keeps review notes in
browser storage.

This record decides how the console's visual language is authored,
layered, and catalogued from here on.

## Decision

### Three token tiers, one source

1. **Primitives.** The eight 12-step families (`--m3-<family>-<step>`),
   generated from `design/palette-source.json`. Components never name a
   primitive.
2. **Semantic tokens.** Role names such as `--background`, `--card`,
   `--primary`, and `--danger-foreground`, with a light and a dark value
   each. Every value is a reference to one primitive step, optionally with
   an alpha. No semantic token holds a literal color. The mapping lives in
   `design/palette-source.json` beside the scales, and a generator writes
   the CSS from it.
3. **Tailwind theme.** `@theme inline` maps each semantic token into
   Tailwind's `--color-*` namespace, so `bg-card` and `text-muted-foreground`
   exist and `bg-red-500` does not: the default palette is cleared with
   `--color-*: initial`.

The contrast gate reads the same source file. It resolves each pair to
OKLCH, converts it to sRGB, and checks the WCAG ratio, so a remapped token
cannot bypass the check. A test also fails when the generated CSS on disk
differs from what the source file produces.

Dark mode stays the `data-theme="dark"` attribute on `<html>`. Tailwind's
`dark:` variant is redefined to match it:

```css
@custom-variant dark (&:where([data-theme=dark], [data-theme=dark] *));
```

### Tailwind v4 for styling

Components are styled with Tailwind v4 utilities, configured CSS-first
through `@theme` with no JavaScript config file. Variants are declared with
`tailwind-variants`. Class strings are not concatenated by hand, because
`tailwind-variants` resolves conflicting classes through `tailwind-merge`.
Effects that utilities cannot express, such as the navigation frame's
brand glow, stay hand-written CSS that reads semantic tokens.

Tailwind's Preflight reset is not imported until the legacy stylesheet has
been migrated. Until then the entry imports only the theme and utility
layers, which is the documented way to add Tailwind to an existing
stylesheet
([Preflight, "Disabling Preflight"](https://tailwindcss.com/docs/preflight)).

### Reka UI for behavior, our components for look

Every interactive component is a `Ui`-prefixed Vue component under
`frontend/web/src/ui/`, built on the matching Reka UI primitive where one
exists. Reka owns focus management, keyboard handling, and ARIA, and the
component owns only styling. The prefix satisfies Vue's multi-word
component-name rule, which the ESLint config already enforces. Application
views use `Ui*` components and do not import Reka directly.

### Storybook as the catalogue

Storybook 10 with `@storybook/vue3-vite` is the component catalogue. Every
`Ui*` component has a stories file with its variants and states, and the
token foundations (colors with their contrast, type, spacing, radius,
shadow, and motion) are stories too. Theme switching uses
`withThemeByDataAttribute` from `@storybook/addon-themes`, which sets the
same `data-theme` attribute the app does. `@storybook/addon-a11y` runs axe on
every story. Once the component stories exist, the in-app `/components`
workbench and its browser-local drafts are removed. Review decisions go
into the component source and `frontend/web/design/`.

## Alternatives

- **Plain CSS with scoped component styles.** This was the smallest step
  from today and adds no dependency. It lost because nothing stops an
  off-scale value without a separate lint gate, and every primitive's
  layout would be written from scratch. Tailwind's cleared theme
  namespaces limit authors to the scale by construction.
- **UnoCSS.** It offers the same utility model with a faster, more
  configurable engine. It lost on ecosystem: there are no Reka-based
  component recipes, it is less familiar to contributors, and its Tailwind
  compatibility trails each Tailwind release. Its configurable rules also
  make it easy to grow a dialect only this repository understands.
- **Keeping hand-picked hex semantic tokens.** Those tokens are reviewed,
  but they drift from the scales, and the palette generator cannot change
  them.
- **Growing `/components` into the catalogue.** It needs no new tool. It
  lost because the catalogue would have to rebuild what Storybook already
  provides: controls, docs pages, theme switching, and an accessibility
  audit.
- **Histoire.** It is Vue-native and lighter than Storybook. It lost on
  addon coverage (accessibility, themes) and on familiarity.

## Consequences

- `frontend/web/` gains `tailwindcss`, `@tailwindcss/vite`,
  `tailwind-variants`, `tailwind-merge`, and the Storybook packages, and a
  second dev server (`pnpm storybook`).
- The legacy semantic token names (`--page`, `--text`, `--surface`,
  `--coral`, …) are renamed once, in place, to the new role names. They get
  no aliases.
- Until Preflight is enabled, unlayered legacy rules in `src/style.css`
  win over Tailwind utilities. A component that mixes the two must
  not depend on a utility overriding a legacy rule.
- Colors are checked for contrast. Spacing, type, and shadow literals in
  the legacy stylesheet are migrated when that stylesheet is dissolved,
  and a stylelint gate against literals lands with that migration.
- Components with a Reka primitive stop hand-rolling behavior. The
  switchers, menus, and dialogs in `src/components/` are migration work
  under this direction.

## Sources

Checked on 2026-09-26:

- Tailwind CSS, theme variables, `@theme inline` and `@theme static`:
  https://tailwindcss.com/docs/theme
- Tailwind CSS, disabling default colors with `--color-*: initial`:
  https://tailwindcss.com/docs/colors
- Tailwind CSS, dark mode with a data attribute:
  https://tailwindcss.com/docs/dark-mode
- Storybook addon-themes API, `withThemeByDataAttribute`:
  https://github.com/storybookjs/storybook/blob/next/code/addons/themes/docs/api.md
- npm registry peer ranges: `@storybook/vue3-vite@10.6.0` accepts Vite
  `^8.0.0`, and `@tailwindcss/vite@4.3.3` accepts Vite `^8`.
