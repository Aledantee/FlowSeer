# Console design language

Open `/components` and choose **Foundations** to compare Inter with DM Sans using
interface text, changing numbers, and addresses. Switch the console theme to
inspect the same specimens in light and dark mode.

## Typography decision

Use Inter Variable for the interface, with automatic optical sizing, regular
body text, and 600-weight headings. Its text optical size and tall x-height help
small labels; tabular figures keep metric columns stable. Device addresses use
the platform monospace stack. The `standard.css` font import includes Inter's
weight and optical-size axes. Fonts are bundled locally.

The choice is a design judgment for this console, not a claim that one font is
universally more readable. The alternatives considered were:

| Family      | Assessment                                                                                                                      |
| ----------- | ------------------------------------------------------------------------------------------------------------------------------- |
| Inter       | Recommended for interface text. Text/display optical sizes, tabular figures, and disambiguation features suit operational data. |
| GT Standard | Closest match to the m3connect website. A brand option when appropriately licensed files are supplied.                          |
| DM Sans     | A softer geometric alternative, already bundled. Retained for comparison in the typography specimen.                            |

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

`src/theme/language.css` defines the interface and mono font stacks, spacing
steps, control height, and corner radii. The semantic color tokens in
`src/style.css` remain the contrast-tested layer over the [palette](README.md).

Use 8px gaps within controls, 16px between related elements, and 24px panel
padding. Controls have 8px corners; content panels have 12px corners. Page
headings are 28px, section headings are smaller, and supporting labels remain
quiet. Metrics use tabular figures and place units beside the value.

Cyan identifies selection. Coral emphasizes the primary action with dark text.
Health badges always include a word as well as color. Focus rings must remain
visible on buttons, links, selects, and text fields. Motion is short action
feedback; live values do not animate.

For example, use the same button and badge in a fleet flow and its workbench
preview:

```vue
<UiButton variant="primary" type="submit">Save assignment</UiButton>
<StatusBadge status="Degraded" />
```

`UiButton`, `StatusBadge`, and `MetricCard` are shared by the fleet and component
views. Inputs and empty states demonstrate the console's CSS patterns; they do
not introduce additional wrapper components.

## Working on a component

Choose **Buttons**, change its variant, and try its disabled state. Record a
question or visual decision under **Decisions & next steps**, select a stage,
and save. Each component has a separate draft. Unsaved edits disable switching
to another component until saved. Navigating away or reloading before saving
can discard unsaved edits.

Drafts live only in this browser's local storage. They are not synchronized,
committed to the repository, or shared with another person. A failed save keeps
the notes visible and asks the author to copy them. Promote agreed decisions
into the component source and this document; draft stages are author-selected
planning labels, not automated completion checks.
