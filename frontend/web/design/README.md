# m3connect palette

Open `http://127.0.0.1:5173/design/palette.html` while `pnpm dev` is running.
Switch themes and click any tone to copy its OKLCH value. The sheet offers CSS and
JSON downloads. It is a development design artifact, separate from the production
console entry point.

## Recommended tools

| Tool                                                                     | Best use here                                                                                                 | Limit                                                                                                                    |
| ------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| [Radix Custom Colors](https://www.radix-ui.com/colors/custom)            | Generate paired light/dark UI scales from accent, gray, and background inputs. Used for the delivered scales. | Roles are starting points. Check actual foreground/background combinations.                                              |
| [Adobe Leonardo](https://github.com/adobe/leonardo)                      | Refine tones around explicit contrast targets, especially custom backgrounds.                                 | Requires deliberate targets and review; the documentation was reviewed, but this export was not generated with Leonardo. |
| [Huetone](https://www.huetone.app/)                                      | Inspect hue, chroma, and lightness together and compare contrast while editing.                               | Manual tuning tool; it does not choose product semantics.                                                                |
| [WebAIM Contrast Checker](https://webaim.org/resources/contrastchecker/) | Independently check exact foreground/background pairs and normal-text thresholds.                             | Pair checker, not a full palette generator.                                                                              |

Use Radix for the scale structure, Leonardo when a specific contrast target needs
refinement, and the repository's contrast tests for enforcement. A harmonious
palette alone does not establish readability or visual comfort.

## Anchors and extensions

The anchors were observed in the [m3connect website](https://www.m3connect.de/),
not supplied as an official brand manual:

| Anchor            | Value     | Console role                                         |
| ----------------- | --------- | ---------------------------------------------------- |
| m3 orange / coral | `#FF451D` | Logo and occasional primary action                   |
| Cyan              | `#5ECAD8` | Logo and interaction family                          |
| Deep teal         | `#061518` | Dark foreground on vivid brand fills                 |
| Dark teal         | `#0A1E22` | Brand reference; large console surfaces use charcoal |

The m3 orange is the coral anchor `#FF451D`. Use `--coral` for solid accents
and `--accent-orange-text` for readable warm text in each theme. The navigation frame uses the reusable CSS glow described below.

White text on coral is only 3.43:1; white on cyan is 1.92:1. Neither is suitable
for normal-size white text. Deep teal text on coral reaches 5.43:1 and on cyan
9.68:1. These ratios use the WCAG relative-luminance formula.

The eight families are coral, cyan, neutral, green, amber, rose, violet, and blue.
Each has twelve light and twelve dark tones: 192 tones in total. The supporting
families are proposed UI extensions, not additional official m3connect colors.
The chart families complement the brand without conflating coral with errors.

## How the scales were produced

On 2026-09-06, the Radix web generator was operated with the inputs captured in
`palette-source.json`. Both modes used gray seed `#858E98`. Backgrounds were
`#F3F3F2` for light and `#1C1E21` for dark. Each accent edit was committed by
leaving its field, then the rendered swatch colors were read from computed CSS.
The generated values were preserved in OKLCH, including the tool's rounding.

For example, reproduce the brand coral setup with this
[Radix configuration](https://www.radix-ui.com/colors/custom?accent-light=FF451D&accent-dark=FF451D&gray-light=858E98&gray-dark=858E98&bg-light=F3F3F2&bg-dark=1C1E21).
Change the accent in both modes to `5ECAD8` for cyan. Supporting seeds are in the
source file.

Steps follow [Radix's role structure](https://www.radix-ui.com/colors/docs/palette-composition/understanding-the-scale):
1–2 for subtle surfaces; 3–5 for component fills; 6–8 for borders; 9–10 for solid
emphasis; 11–12 for text candidates. This is not a monotonic brightness ramp.
The generator can adjust the solid tone, especially for a bright seed in light
mode. Exact brand anchors therefore remain separate from generated steps.

## Consume and regenerate

`src/theme/scales.css` exposes `--m3-coral-1` through `--m3-blue-12`, with dark
values selected by `data-theme="dark"`. The app imports these primitives through
`src/style.css`. Existing semantic tokens remain the reviewed, contrast-tested
layer for components; a full primitive ramp does not replace those decisions.

```css
.site-hint {
  background: var(--m3-cyan-3);
  color: var(--m3-cyan-12);
}
```

Check that example's rendered contrast on the actual target display and surface.
Some OKLCH values extend beyond sRGB, so gamut mapping can affect the result.
Never assume that arbitrary steps work together or that white text works on step 9.

The JSON export includes the source scales, seeds, exact brand anchors, and both
sets of semantic hex tokens. After editing source scales or semantic tokens:

```sh
cd frontend/web
node scripts/build-palette.mjs
pnpm format
pnpm test
pnpm build
```

For sequential analytics, use one hue with ordered luminance rather than the
role-based 1–12 sequence directly. For categorical charts, use cyan, coral,
violet, green, amber, and blue with stable assignments, direct labels, and
markers. Check color-vision distinguishability on the actual chart; hue diversity
alone is insufficient.

## Reusable brand glow

[`src/theme/brand-glow.css`](../src/theme/brand-glow.css) adapts the
teal-to-orange illumination from the m3connect Personio login
[background reference](https://assets.cdn.personio.de/brand/40341/login-background/original/Y6SPkv-i_cPEjgTeTJDd6mD6XXRrR1Ghn-UUTMP_Lf8.jpg).
The console uses three broad, softly shaded diagonal ribbons with uneven spacing
in place of the reference’s repeating vertical panels. Feathered highlights give
the ribbons a slight folded-light effect without sharp dividers. The effect uses
CSS gradients only; it does not load that image.

Apply `brand-glow` to every connected chrome surface:

```html
<aside class="sidebar brand-glow">...</aside>
<header class="topbar">
  <span class="topbar-glass brand-glow" aria-hidden="true"></span>
  ...
</header>
```

The layered gradients share viewport coordinates through `background-attachment:
fixed`, `background-position: left top`, and `background-size: 100vw
var(--glow-height)`. This prevents the pattern from restarting at the sidebar
edge, including when the sidebar collapses. The rounded content corner inherits
the same background and uses a radial mask to cut out the inner curve. The glow stays
within the navigation frame, with a longer vertical fade down the sidebar.

Defaults are 30% color strength, a 560px fade height, and a 112-degree ribbon
angle controlled by `--glow-ribbon-angle`. Ribbon positions are intentionally
asymmetric gradient stops; avoid a repeating pattern that makes the chrome
look segmented.
`--glow-base`, `--glow-teal`, and `--glow-orange` use the console's theme-specific navigation background, cyan,
and coral tokens. The root owns these parameters so the sidebar, top bar, rounded corner, and
collapse tab inherit the same values. For example, adjust the light theme at
the root:

```css
:root:not([data-theme='dark']) {
  --glow-strength: 18%;
  --glow-height: 160px;
}
```

Light mode uses neutral gray navigation and 13% glow strength; dark
mode uses charcoal and 30%. Keep text legible across both treatments. The effect
stays static and fades into the navigation background. The top bar applies backdrop blur to scrolling content. Changing
its strength requires checking text and control contrast across the rendered
bands, not only against the base background token.

## Light-mode comfort

The light palette uses a slate-gray canvas `#BEC3CA`, lifted card surfaces
`#DCE0E4`, and navigation `#CBD1D6`. Panel headers use `#CDD3D9` to separate
controls from data rows. These semantic colors reduce glare while keeping the
surfaces distinct. Primary text is `#20262D`; secondary text is `#424B55`.
Darkening backgrounds requires darkening text and focus colors alongside them:
otherwise small accent labels lose contrast first.

The [contrast audit](light-mode-contrast.md) records measured ratios, browser
coverage, and gradient sampling. Run the semantic pair checks with:

```sh
cd frontend/web
pnpm exec vitest run src/theme/palette.test.ts
```

The [Radix review capture](light-palette-review.json) is a historical ramp
reference, not the current semantic role mapping. The eight-family primitive
export is also a reference palette; components use the contrast-tested semantic
tokens in `src/style.css`.

The connected frame keeps its shared ribbons and rounded inner corner. Content
scrolls behind a translucent top-bar layer with 24px backdrop blur. Its measured
height reserves content spacing on desktop and mobile. Required control outlines
use `--control-border`; decorative panel separators use `--line` and do not carry
interaction or state meaning. Status badges retain explicit text labels.
