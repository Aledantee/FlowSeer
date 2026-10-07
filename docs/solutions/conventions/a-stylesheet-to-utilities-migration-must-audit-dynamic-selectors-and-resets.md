---
title: A Stylesheet-to-Utilities Migration Must Audit Dynamic Selectors, Breakpoints, and Base Resets
date: 2026-09-27
last_verified: 2026-10-07
category: conventions
module: frontend/web
problem_type: convention
component: refactor
severity: high
applies_when:
  - "Dissolving legacy CSS stylesheets into utility classes or scoped component styles"
  - "Enabling or updating a global CSS reset layer such as Tailwind Preflight"
  - "Configuring stylelint declaration-strict-value rules to enforce design tokens"
  - "Moving a Vue view into a shared frame or Teleport target while CSS selectors depend on direct DOM ancestry or chrome styles."
  - "Moving a graphic canvas onto a translucent parent panel when the canvas may paint its own opaque background"
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

Moving the console into a persistent frame exposed the same test gap. The old
`.main-shell > main.panes` selector never matched because `UiAiContextLayer`
rendered a `div.contents` between the two elements. The replacement selector
is `#frame-page main.panes` (`frontend/web/src/style.css:332`,
`frontend/web/src/ui/ai/UiAiContextLayer.vue:314`). The frame's glass is held
by utility classes in `frontend/web/src/navigation/AppFrame.vue:26-27`,
with the translucent panel at `:49-50`. Its mount tests hold those classes
(`frontend/web/src/navigation/AppFrame.test.ts`).
`frontend/web/src/navigation/chrome.test.ts` holds the removal of separate
view blur and the panes' background, so the frame owns both surfaces.
The topology canvas also had its own opaque ground, which covered that panel
because `.pane.canvas-view .pane-scroll` has no padding
(`frontend/web/src/style.css:379-385`). The canvas now leaves its root
background unset (`frontend/web/src/components/topology/TopologyGraph.vue:425-432`).
`frontend/web/src/navigation/chrome.test.ts:42-58` scans the canvas template
and root rules for a replacement ground. A browser check is still needed to
judge how the composited panel looks behind the canvas.

## What is true and why

1. **JSDOM and unit tests do not compute cascade, layout geometry, or pseudo-classes.** Unit tests assert DOM presence, attributes, and emitted events. They cannot verify whether a view computed zero width at a 390px viewport, whether an active indicator bar rendered, or whether keyboard focus was visibly outlined.
2. **Audit stylesheet removals against runtime class mutations, not static template strings.** When removing a CSS selector, check script blocks for reactive class toggles (`:class`), string interpolations, and DOM manipulations (`classList.add`).
3. **Base resets must preserve native accessibility affordances.** If a reset layer clears browser default button outlines, restore visible keyboard focus explicitly in the base layer using `:focus-visible` and design tokens (`outline: 2px solid var(--ring); outline-offset: 2px;`).
4. **Token linting requires shorthand expansion and regex blacklists.** A token gate must enable `expandShorthand` and `recurseLonghand`, inspect SVG presentation properties (`fill`, `stroke`), ban named colors, and disallow literal color function calls (`rgb()`, `hsl()`) in complex values like gradients.
5. **Check computed styles after DOM moves.** A passing class assertion proves the element exists but cannot prove that its CSS selector matches or that its glass rule still paints. In this workspace `main.ts` imports `style.css`, while the happy-dom mount tests load the views directly (`frontend/web/src/main.ts:15`, `frontend/web/src/FleetView.test.ts:1-7,68-80`).

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

- `frontend/web/src/theme/tailwind.css` (`button:focus-visible`) enforces the token ring outline.
- `frontend/web/src/theme/tailwind.test.ts:80-90` verifies Preflight base resets and button focus emission.
- `frontend/web/.stylelintrc.json:12-39` configures strict-value with shorthand expansion, SVG `fill`/`stroke`, and disallowed literal color functions.
- `frontend/web/src/WorkspacePage.vue:1042-1045` restores `tbody tr.peeked` row highlighting dropped in `ed5cf507`.
- `frontend/web/src/FleetView.vue:964` and `1329-1347` hold mobile topbar wrapping at 650px/560px and `.active-pane` indicator borders.
- `frontend/web/src/components/topology/TopologyLink.vue:220-222` holds link hover feedback.
- Review fix commits `1a2ad4b6`, `40969da9`, `e7d740ec`, and `a42a4a68` restored these dropped behaviors after review.
- `frontend/web/src/FleetView.vue:1097-1104` and `frontend/web/src/ui/ai/UiAiContextLayer.vue:314` show the wrapper between the frame region and `main.panes`. The prior direct-child selector is in `8e8c2200`.
- `frontend/web/src/components/topology/TopologyGraph.vue:425-432` leaves the canvas root transparent. `frontend/web/src/navigation/chrome.test.ts:42-58` holds the absence of an opaque root ground.

## What this does not cover

- Visual regression testing across graphic canvases: WebGL, SVG graph layouts, or canvas-based visualizers need visual inspection or screenshot diffing.
- CSS specificity disputes between third-party component libraries and Tailwind layers.
