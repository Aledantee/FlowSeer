# Light-mode contrast audit

Audited on 7 September 2026. The light theme uses darker neutral surfaces with
stronger foreground colors to reduce glare without sacrificing small-text
legibility. The shared glass frame and rounded inner corner retain their layout.

## Findings and corrections

Before correction, the navigation count measured 4.08:1 in axe-core. Orange text
on the canvas measured 4.46:1. Both fall below the 4.5:1 normal-text threshold.
Navigation focus color also appears as text, so its test must require 4.5:1,
not the 3:1 threshold appropriate to a non-text focus indicator.

The canvas changes from `#D8DADE` to `#BEC3CA`. Primary and secondary text darken
together with it. Controls, navigation selection, status fills, and status
foregrounds use adjusted semantic tokens. Sparklines use the tested accent text
color instead of a fixed cyan with reduced opacity.

## Surface palette

| Role                               | Color     |
| ---------------------------------- | --------- |
| Canvas                             | `#BEC3CA` |
| Cards and inputs                   | `#DCE0E4` |
| Panel headers                      | `#CDD3D9` |
| Subtle surfaces                    | `#D4D9DE` |
| Hover surfaces                     | `#C1CBD3` |
| Navigation base                    | `#CBD1D6` |
| Navigation selection               | `#B8CDD2` |
| Navigation hover                   | `#BAC4CC` |
| Primary text                       | `#20262D` |
| Secondary text                     | `#424B55` |
| Control outlines                   | `#596570` |
| Focus and selected navigation text | `#124D5D` |
| Accent text                        | `#125263` |
| Orange text                        | `#8E2C16` |

## Measured semantic pairs

Ratios use linearized sRGB relative luminance and `(Llighter + 0.05) /
(Ldarker + 0.05)`. Assertions use unrounded values. The thresholds follow
[WCAG text contrast](https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum)
and [non-text contrast](https://www.w3.org/WAI/WCAG22/Understanding/non-text-contrast).

| Pair                                        | Ratio   | Minimum            |
| ------------------------------------------- | ------- | ------------------ |
| Primary text / canvas                       | 8.61:1  | 7:1 project target |
| Primary text / cards                        | 11.50:1 | 7:1 project target |
| Secondary text / canvas                     | 5.00:1  | 4.5:1              |
| Orange text / canvas                        | 4.70:1  | 4.5:1              |
| Selected navigation text / navigation hover | 5.28:1  | 4.5:1              |
| Control outline / canvas                    | 3.37:1  | 3:1                |
| Connection stroke / canvas                  | 3.29:1  | 3:1                |
| Healthy text / healthy fill                 | 6.45:1  | 4.5:1              |
| Warning text / warning fill                 | 6.33:1  | 4.5:1              |
| Offline text / offline fill                 | 5.96:1  | 4.5:1              |
| Primary text / information fill             | 10.63:1 | 4.5:1              |

`src/theme/palette.test.ts` checks text and accent colors against every content
surface, control outlines and connection strokes across those surfaces, navigation
text across base/selection/hover surfaces, focus indicators, primary-button text,
and status pairs. Light-mode status outlines have additional checks. Both themes
pass. Decorative separators are not used as the sole indication of a control or
state and are not assigned the control-outline contrast requirement.

## Rendered checks

Tools: axe-core 4.12.1 through agent-browser, the repository's sRGB contrast
calculator in Vitest, and Pillow sampling of Chromium screenshots.

No automated contrast violations were reported for Devices, Sites, Topology,
or Components. Component previews checked: buttons, status badges, inputs and
filters, metric cards, empty states, and foundations. The tenant menu, help
dialog, device details, and primary-button hover also reported no violations.

Axe cannot determine every gradient or overlapping background. For navigation
text, a separate sampling pass collected visible text ranges and their computed
foreground colors, temporarily hid the text without changing layout, and sampled
the background pixels in those ranges. It calculated the lowest ratio for each
range, then restored the text.

| Frame state                    | Lowest sampled ratio |
| ------------------------------ | -------------------- |
| Desktop, 1280 × 850, expanded  | 5.28:1               |
| Desktop, 1280 × 850, collapsed | 5.42:1               |
| Phone, 390 × 844, expanded     | 5.21:1               |

A scrolled Chromium check at 520px also compared the header with isolation
enabled and disabled: the header pixels were identical. Disabling backdrop blur
changed the header pixels, confirming that blur operates with the current
isolated stacking context.

The contrast samples cover the default unscrolled frame in Chromium. They do not prove
contrast for every scroll position, tenant image, operating-system control, or
browser renderer. Automated results and semantic-pair tests supplement visual
inspection; this audit is not a claim of whole-application WCAG conformance.

## Verification

```sh
cd frontend/web
pnpm test
pnpm lint
pnpm format:check
pnpm build
```

The repository's diff-aware verifier also covers every changed file. Palette
source ramps and the historical Radix capture remain reference material; the
semantic tokens in `src/style.css` are the rendered console palette.
