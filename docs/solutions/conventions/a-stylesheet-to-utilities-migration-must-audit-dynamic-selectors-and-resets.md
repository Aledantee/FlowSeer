---
title: A Stylesheet-to-Utilities Migration Must Audit Dynamic Selectors, Breakpoints, and Base Resets
date: 2026-09-27
last_verified: 2026-09-27
category: conventions
module: frontend/web
problem_type: convention
component: refactor
severity: high
applies_when:
  - "Dissolving legacy CSS stylesheets into utility classes or scoped component styles"
  - "Enabling or updating a global CSS reset layer such as Tailwind Preflight"
  - "Configuring stylelint declaration-strict-value rules to enforce design tokens"
related_components: [form_controls, conformance-gates]
tags: [css, tailwind, preflight, stylelint, design-system, responsive, accessibility, refactor]
---

# A stylesheet-to-utilities migration must audit dynamic selectors, breakpoints, and base resets

## The situation

When dissolving legacy stylesheets (`src/style.css`, `src/dashboard.css`) into component-level Tailwind utility classes and scoped CSS, standard web test suites (Vitest in JSDOM, TypeScript typechecking, Vite builds, Storybook axe checks) stay green even when essential presentation and interaction logic drops.

In Phase 5 of the web design system (`86517ce2..b3edbbe0`), four failure classes escaped into review unnoticed by automated tests:

1. Responsive layout collapse: removing legacy media queries from `style.css` without transferring equivalent responsive utility variants (`max-[560px]:...`, `max-[800px]:...`) collapsed the 390px mobile shell to zero width.
2. Base reset accessibility regressions: enabling Tailwind Preflight stripped user-agent button outlines, and a base `button { outline: none; }` rule removed keyboard focus indicators across all native buttons.
3. Vanished dynamic and interactive feedback: deleting legacy rules (`tbody tr.peeked`, `.pane.active-pane::before`, `.panes.docked`, `.topology-link:hover`) broke visual states toggled by runtime JavaScript or pointer interaction because static template scans missed dynamically bound class names.
4. Token linting gate bypasses: `stylelint-declaration-strict-value` missed CSS color values in shorthand properties (`background`), SVG attributes (`fill`, `stroke`), named colors (`black`, `white`), and gradients (`rgb(...)`), providing false confidence of token compliance.

## What is true and why

1. **JSDOM and unit tests do not compute cascade, layout geometry, or pseudo-classes.** Unit tests assert DOM presence, attributes, and emitted events. They cannot verify whether a view computed zero width at a 390px viewport, whether an active indicator bar rendered, or whether keyboard focus was visibly outlined.
2. **Audit stylesheet removals against runtime class mutations, not static template strings.** When removing a CSS selector, check script blocks for reactive class toggles (`:class`), string interpolations, and DOM manipulations (`classList.add`).
3. **Base resets must preserve native accessibility affordances.** If a reset layer clears browser default button outlines, restore visible keyboard focus explicitly in the base layer using `:focus-visible` and design tokens (`outline: 2px solid var(--ring); outline-offset: 2px;`).
4. **Token linting requires shorthand expansion and regex blacklists.** A token gate must enable `expandShorthand` and `recurseLonghand`, inspect SVG presentation properties (`fill`, `stroke`), ban named colors, and disallow literal color function calls (`rgb()`, `hsl()`) in complex values like gradients.

## How to apply

When dissolving a stylesheet into utilities:

1. Audit every `@media` block in the legacy stylesheet before deletion, transferring responsive styles to utility variants (`max-[560px]:...`) or scoped component media queries.
2. When enabling Preflight, pair button resets with `:focus-visible` token outlines:

```css
@layer base {
  button:focus-visible {
    outline: 2px solid var(--ring);
    outline-offset: 2px;
  }
}
```

3. Move dynamic runtime classes to scoped component styles using `:deep()` where child components or table rows are manipulated:

```vue
<style scoped>
:deep(tbody tr.peeked) {
  background: color-mix(in srgb, var(--info-surface) 60%, transparent);
  box-shadow: inset 3px 0 0 var(--accent-foreground);
}
</style>
```

4. Configure `.stylelintrc.json` to expand shorthands, check SVG properties, and ban raw color functions and un-tokenized box-shadows:

```json
{
  "rules": {
    "scale-unlimited/declaration-strict-value": [
      ["/color$/", "fill", "stroke", "font-size", "box-shadow"],
      {
        "expandShorthand": true,
        "recurseLonghand": true,
        "ignoreValues": {
          "": ["transparent", "inherit", "initial", "unset", "currentColor", "none"],
          "box-shadow": ["none", "inset", "/^[-0-9]+(px)?$/"]
        }
      }
    ],
    "color-no-hex": true,
    "color-named": "never",
    "declaration-property-value-disallowed-list": {
      "/.*/": ["/\\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color|device-cmyk)\\((?!\\s*(?:from\\s+)?var\\()/i"],
      "box-shadow": ["/^(?!\\s*(?:none|inherit|initial|unset|revert(?:-layer)?)\\s*$)(?![\\s\\S]*var\\(--[\\s\\S]*$)[\\s\\S]+$/"]
    }
  }
}
```

## Evidence

- `frontend/web/src/theme/tailwind.css:12-15` enforces `button:focus-visible` with token ring outline.
- `frontend/web/src/theme/tailwind.test.ts:56-68` verifies Preflight base resets and button focus emission.
- `frontend/web/.stylelintrc.json:12-39` configures strict-value with shorthand expansion, SVG `fill`/`stroke`, and disallowed literal color functions.
- `frontend/web/src/WorkspacePage.vue:594-599` restores `tbody tr.peeked` row highlighting dropped in `ed5cf507`.
- `frontend/web/src/FleetView.vue:897-903` and `1204-1227` restore mobile topbar wrapping at 650px/560px and `.active-pane` indicator borders.
- `frontend/web/src/components/topology/TopologyLink.vue:164-167` restores link hover feedback.
- Review fix commits `1a2ad4b6`, `40969da9`, `e7d740ec`, and `a42a4a68` restored these dropped behaviors after review.

## What this does not cover

- Visual regression testing across graphic canvases: WebGL, SVG graph layouts, or canvas-based visualizers need visual inspection or screenshot diffing.
- CSS specificity disputes between third-party component libraries and Tailwind layers.
