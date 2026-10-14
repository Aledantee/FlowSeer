---
title: Web Signal Design Language - Direction
type: direction
date: 2026-10-09
topic: web-design-language
status: accepted-direction
---

# Web Signal Design Language - Direction

## Context

The console already has a semantic palette and shared components. Its current
[design language](../../frontend/web/design/language.md) uses Inter headings,
rounded content panels and generous spacing. Signal changes the visual hierarchy
while retaining those components and the application frame.

The [Signal reference](../../frontend/web/design/signal/README.md) captures
Precision variant 01 in both themes, with the existing sidebar and top bar.
The [web design system](2026-09-26-web-design-system-direction.md),
[component contract](2026-09-28-web-component-contract-direction.md) and
[AI target lifecycle](2026-10-05-1251-web-ai-target-lifecycle-direction.md)
remain binding. The proposed
[console frame record](2026-10-07-web-console-frame-direction.md) describes the
current shell implementation that this design preserves.

## Decision

### Shared visual language

Use square content panels with one-pixel semantic borders, restrained corner
ticks and no raised shadow. Separate headers from content with a thin rule.
Use 16px gaps between panels and content-driven heights so translations and
empty states do not overflow fixed mockup boxes.

Use the existing mono stack for page titles, section headings, prominent
metrics and identifiers. Use Inter for navigation, prose and event descriptions.
Panel headings use uppercase styling and modest tracking. Metrics use tabular
figures with adjacent units. Use the existing 28px page-title and 11px minimum
label tokens. Add named display-metric tokens where needed instead of copying
the reference's arbitrary sizes. Essential information never uses its 8px or
9px decorative annotation sizes.

Shared controls use compact four-pixel corners and the existing control-height
scale. Preserve focus outlines and pointer targets. The shell's page corner,
avatars, status dots, topology symbols and meters keep their functional shapes.
Dialogs and menus use the shared surface vocabulary while keeping their current
behavior and focus management.

Keep the current coral and cyan palette and its semantic mapping. Coral marks
primary actions. Cyan marks selection and telemetry, with AI styling still
following `frontend/web/src/theme/ai.css`. Health and severity keep their semantic
colors and text labels. Required control boundaries use input/focus tokens.
Decorative panel rules use border tokens.

### Existing application frame

Preserve `frontend/web/src/navigation/AppFrame.vue` and the regions populated
by `frontend/web/src/FleetView.vue`. The desktop sidebar remains 204px expanded
and 64px collapsed. The top bar keeps its 54px minimum height. Keep navigation
order, scope breadcrumbs, search, assistant and the account menu in their current
positions. Preserve the continuous brand glow, shared glass surface and rounded
upper-left page corner.

Responsive wrapping may increase the top-bar height. Keep route changes, sidebar
collapse, split panes, docking and reduced-motion behavior. Component styling
must not add a second shell or replace those interactions.

### Dashboard composition

The title and primary action lead into a full-width AI summary. At wide content
widths, fleet health and device roles form a narrow left column. Traffic spans
the middle and right columns. Attention sits beneath traffic in the middle.
Events occupy the right column beside attention and sites. Sites span the left
and middle columns below attention.

Use available pane width to choose three, two or one columns. A narrow split
pane must reflow even when the browser window is wide. Preserve reading order:
summary, health, traffic, roles, attention, sites, events. The captures fix
relative hierarchy and density. They do not require fixed panel heights.

The health meter combines exact text counts with a bounded segmented graphic.
Traffic has a current value, peak for the selected range and 6h/24h controls.
Use `trafficHistory` in `frontend/web/src/domain/overview.ts`, including its
existing synthetic history semantics. Range selection changes both the plot and
accessible data. Chart scale and units derive from the displayed values.

### Apply through existing components

Update shared primitives and then the dashboard, inventory views, device detail,
topology surfaces, assistant and login content. Preserve each view's information
and working controls. Device actions keep the existing page navigation contract.
The illustrative inspector and mock search in the visual study do not replace
application components.

Production values come from the current scope and domain model. Do not introduce
reference device names, hardcoded percentages, invented issue explanations or
fabricated synchronization timestamps. Existing localization, AI target
registration and overlay contracts continue to apply.

## Alternatives

- Retaining the current rounded content cards would preserve the styling in
  `frontend/web/design/language.md`, but would not express the selected framed
  panel language.
- Replacing the shell with a compact icon rail would change navigation density
  and the established scope controls. The selected reference keeps the current
  shell to preserve those workflows.
- Applying Signal only to the dashboard would leave shared tables and detail
  surfaces with a different typography and panel treatment. Shared primitives
  carry the design across the app.

## Consequences

The migration updates `frontend/web/design/language.md` with the implementation.
Palette sources and generated color files remain unchanged. Storybook examples
show new component styling in both themes and interaction states.

Visual acceptance compares the application against the checked-in captures and
checks German text, narrow panes, keyboard navigation and open overlays. Existing
contrast and accessibility gates remain required. The tiny reference annotations
are the deliberate fidelity exception for readable application text.
