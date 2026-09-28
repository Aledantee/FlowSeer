# Data-dense UI: tables, numbers, and charts

The console is a dense operator tool, not a marketing page. Its floors
differ from general web guidance: 24 px targets and 14 px secondary text
are acceptable. Sources:
- Vercel Web Interface Guidelines (MIT)
- Community-Access `tables-data-specialist` and
  `data-visualization-accessibility` (MIT)
- the built-in `dataviz` skill, whose method applies here but whose
  palette never does

## Numbers and identifiers

- **Formatting.** Numbers and dates are formatted for the active locale
  (`references/i18n-and-ai.md`), never by concatenation. A unit is joined
  to its value with a non-breaking space.
- **Figures.** Aligned columns (tables, axis ticks) use `tabular-nums`. A
  standalone large value keeps proportional figures.
- **Identifiers.** Hostnames, MAC and IP addresses, and serials carry
  `translate="no"`, never wrap mid-token, and truncate with the full value
  in a tooltip or title.
- **Monospace** is only for code and measured values, never as
  "technical" styling.

## Tables

- **Names and headers.** The table has a `<caption>` or an accessible
  name. Header cells carry `scope`.
- **Sorting.** Exactly one header carries `aria-sort` at a time, and the
  sort icon is `aria-hidden`.
- **Selection.** Select-all is tri-state, and a selection change is
  announced.
- **Empty state.** It is one row spanning every column, with guidance.
- **Paging.** After a page change, focus moves to the caption or the
  first row.
- **Live values** update in place without reordering rows or animating
  the numbers. The README already says this.
- **Size.** Past about 50 rows, paginate or virtualize.

## Charts, meters, and stat tiles

Choose the form before the color:
- **One current value:** a stat tile. It has a label, a compacted value,
  and a signed delta against a named period, with an optional sparkline
  of up to about 12 points.
- **A ratio against a limit:** a meter (`UiMeter`) whose unfilled track is
  a lighter step of the same ramp.
- **Comparing categories:** bars.
- **A trend over time:** a line.
- Never a one-bar chart, a two-slice pie, or a dual y-axis.

Color rules:
- Series colors follow the entity, so filtering never repaints the series
  that remain. A ninth series folds into "Other" or into small multiples.
- Status tokens (success, warning, danger) are only for status, and
  always come with a label or icon.
- Series slots map to semantic tokens, never to hex values copied from a
  reference palette.
- Label and value text uses text tokens, never the series color.

Marks and interaction:
- Lines and marks: 2 px lines, markers of at least 8 px, a solid hairline
  grid (never dashed), and a 2 px surface-colored gap between adjacent
  fills instead of borders.
- Line charts show a crosshair with one tooltip listing every series. Bar
  charts show a tooltip per mark.
- Keyboard focus shows the same tooltip as hover. `TrafficChart.vue`
  already supports arrow keys.
- Insert series names with `textContent`, never HTML.

Accessibility:
- A chart has `role="img"` with a name and a one-sentence summary. Every
  value is reachable without hover, through a table view or the summary.
- Adjacent marks keep 3:1 contrast against each other and against the
  surface.
