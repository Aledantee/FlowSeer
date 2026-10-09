# Precision 01: Signal

Signal uses thin panel frames, compact rows and monospaced headings to make
operational data easy to scan. Coral marks the primary action. Cyan marks
selection, telemetry and assistant context. Health keeps its semantic colors
and text labels.

The captures below fix the visual target. Their device names, counts, times
and issue descriptions are illustrative. Production content comes from the
current scope and domain model.

| Reference                          | What it fixes                                                                        |
| ---------------------------------- | ------------------------------------------------------------------------------------ |
| [Dark desktop](dark.png)           | Dashboard composition, framed panels, typography and the existing application shell. |
| [Light desktop](light.png)         | The same hierarchy using the existing light palette.                                 |
| [Collapsed sidebar](collapsed.png) | Compact navigation with the page panel moving into the released space.               |
| [Mobile](mobile.png)               | Content stacking and wrapped shell controls.                                         |

## Visual contract

Keep the expanded 204px sidebar, collapsed 64px sidebar and desktop 54px top
bar from `src/style.css` and `src/navigation/AppFrame.vue`. Keep the brand
mark, navigation order, scope breadcrumbs, search, assistant and account
control. The continuous brand glow and rounded upper-left page corner stay.
Widths and heights are desktop reference dimensions. Wrapped controls and
localized text may grow on narrow screens.

Inside the page, use square, one-pixel panel frames with restrained corner
ticks and no raised shadow. Use the existing mono stack for page and panel
headings, identifiers and prominent metrics. Use Inter for prose, navigation
and event descriptions. Small reference annotations must use readable design
system font sizes in the application.

The dashboard starts with the page title and primary action, then a full-width
assistant summary. At wide content widths, health and device roles occupy the
left column. Traffic spans the remaining width above the attention list and
events. Sites sit below the left and center columns. Events extend down the
right. Gaps are 16px. Panel height follows content, including translated text.
Narrow panes stack these regions in reading order without clipping actions.

Use the existing semantic tokens and palette source. Decorative hairlines
use border tokens. Interactive boundaries and focus use their tested semantic
roles. Status labels remain explicit. A subtle border must never be the only
way to identify a control.

## Application behavior

The application retains its existing routing, pane navigation and overlays.
A device link opens through the current navigation contract. The captures do
not specify a replacement inspector, a new search engine or a new assistant.
Existing controls retain keyboard behavior, focus return and accessible names.

Counts, percentages and charts must reflect the selected scope. The reference
health meter has 28 segments because its example has 28 devices. Larger or
empty scopes need a bounded visual representation and exact text counts.
The chart uses the existing traffic history and locale-aware formatting.
A range control must change the displayed data and its accessible equivalent.

The [direction record](../../../../docs/architecture/2026-10-09-web-signal-design-direction.md)
sets the durable scope. `frontend/web/design/language.md` describes the current
implemented styling until the migration updates it.
