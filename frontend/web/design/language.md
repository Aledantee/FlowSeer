# Console design language

The [Signal design record](../../../docs/architecture/2026-10-09-web-signal-design-direction.md)
sets the panel geometry, typography and dashboard hierarchy. This file records
how the app implements that direction. Open Storybook (`pnpm storybook`) and
choose **Foundations** to inspect the scale in both themes.

## Typography decision

Use Inter Variable for navigation, body text and descriptions, with automatic
optical sizing and regular weight. Use the platform monospace stack for page
titles, section headings, metric values, table headers and identifiers. Metric
columns use tabular figures. The interface keeps text at 11px or larger. Display
metrics use the named `display` and `display-xl` tokens. Fonts are bundled
locally.

The choice is a design judgment for this console, not a claim that one font is
universally more readable. The alternatives considered were:

| Family      | Assessment                                                                                                                      |
| ----------- | ------------------------------------------------------------------------------------------------------------------------------- |
| Inter       | Recommended for interface text. Text/display optical sizes, tabular figures, and disambiguation features suit operational data. |
| GT Standard | Closest match to the m3connect website. A brand option when appropriately licensed files are supplied.                          |
| DM Sans     | A softer geometric alternative evaluated during design selection.                                                               |

On 6 September 2026, the [m3connect stylesheet](https://www.m3connect.de/wp-content/themes/bricks-child/style.css)
declared `GT Standard Trial VF`, `GT Standard Trial VF Mono`, and an icon font.
The family is used for body text and headings with different variation settings.
That declaration identifies the website's assets; it does not establish license
rights for this repository. No font files were copied from that website.
[Grilli Type](https://www.grillitype.com/information) distinguishes web and app
licenses and permits trial fonts in mockups, with a reduced character set and
OpenType features. Use supplied licensed files to adopt GT Standard here.

[Inter's specimen and feature guide](https://rsms.me/inter/) documents optical
sizes, tabular numbers, slashed zero, and its SIL Open Font License.
[DM Sans's source repository](https://github.com/googlefonts/dm-fonts) describes
the geometric family and its intended small-text use.

## Shared rules

`src/theme/tokens.css` defines the interface and mono font stacks, spacing
steps, control heights, display sizes and corner radii through Tailwind theme directives, while
Tailwind Preflight in `src/theme/tailwind.css` standardizes base element styles.
The semantic color tokens in `src/theme/semantic.css` remain the contrast-tested
layer over the [palette](README.md).

Use 8px gaps within controls and 16px between related panels. Controls have
4px corners. Content panels have square edges, a one-pixel border and small
corner ticks. Panel headers use a thin bottom rule. Panel heights follow their
contents instead of the screenshot dimensions. Page headings use the 28px title
token. Section headings use monospace uppercase styling. Descriptions and event
summaries remain in Inter. Metrics put their units beside the value.

Cyan identifies selection and telemetry. Coral emphasizes primary actions.
Health statuses keep their semantic colors and include a word. Focus rings
remain visible on buttons, links, selects and text fields. Motion gives short
action feedback. Live values do not animate.

For example, use the design system button and status badge:

```vue
<script setup>
import { UiButton, UiStatusBadge } from './ui'
</script>

<template>
  <UiButton variant="primary" @click="poll">Poll now</UiButton>
  <UiStatusBadge status="Degraded" />
</template>
```

Components are located under `src/ui/` and exported via `src/ui/index.ts`.
Each component includes CSF 3 stories and unit/accessibility tests in CI.

## Working on a component

Component development and visual review happen in Storybook (`pnpm storybook`).
Add or update stories under `src/ui/**/*.stories.ts` to document all variants,
sizes, and interaction states. Automated tests (`pnpm test src/ui/a11y.test.ts`)
run `axe-core` against all component stories to enforce WCAG 2.1 AA conformance.
