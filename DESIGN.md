---
name: FlowSeer
description: The m3connect operations console for multi-tenant network fleets.
colors:
  signal-coral: "#ff451d"
  on-coral: "#061518"
  harbor-cyan: "#5ecad8"
  accent-text: "#125263"
  accent-text-dark: "#9ccbd3"
  accent-orange-text: "#8e2c16"
  focus: "#124d5d"
  focus-dark: "#9ccbd3"
  charcoal-desk: "#191b1e"
  chrome-light: "#cbd1d6"
  chrome-line-dark: "#434a52"
  chrome-text-dark: "#d8dce0"
  overcast-page: "#bec3ca"
  surface: "#dce0e4"
  panel-surface: "#cdd3d9"
  surface-subtle: "#d4d9de"
  surface-hover: "#c1cbd3"
  page-dark: "#1c1e21"
  surface-dark: "#222a2e"
  surface-subtle-dark: "#283136"
  surface-hover-dark: "#2c383e"
  text: "#20262d"
  text-dark: "#d8dadd"
  muted: "#424b55"
  muted-dark: "#adb3bb"
  line: "#929da8"
  line-dark: "#3b4046"
  control-border: "#596570"
  control-border-dark: "#858e98"
  healthy-surface: "#c9ddd0"
  healthy-text: "#1e5135"
  warning-surface: "#e8d9a7"
  warning-text: "#60450f"
  offline-surface: "#e3c9cf"
  offline-text: "#792e3b"
typography:
  display:
    fontFamily: "'Inter Variable', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "32px"
    fontWeight: 550
    letterSpacing: "-0.8px"
    fontFeature: "tnum"
  headline:
    fontFamily: "'Inter Variable', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "28px"
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: "-0.8px"
  title:
    fontFamily: "'Inter Variable', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "15px"
    fontWeight: 600
    letterSpacing: "-0.2px"
  body:
    fontFamily: "'Inter Variable', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.6
  label:
    fontFamily: "'Inter Variable', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif"
    fontSize: "11px"
    fontWeight: 500
  mono:
    fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace"
    fontSize: "12px"
rounded:
  badge: "4px"
  nav: "5px"
  control: "8px"
  panel: "12px"
spacing:
  "1": "4px"
  "2": "8px"
  "3": "12px"
  "4": "16px"
  "6": "24px"
  "8": "32px"
components:
  button-default:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.control}"
    padding: "8px 14px"
    height: "36px"
  button-default-hover:
    backgroundColor: "{colors.surface-hover}"
  button-primary:
    backgroundColor: "{colors.signal-coral}"
    textColor: "{colors.on-coral}"
    rounded: "{rounded.control}"
    padding: "8px 14px"
    height: "36px"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.accent-text}"
    rounded: "{rounded.control}"
    padding: "8px 14px"
    height: "36px"
  button-small:
    rounded: "{rounded.control}"
    padding: "6px 12px"
    height: "32px"
  input-search:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.control}"
    padding: "9px 10px 9px 32px"
  status-healthy:
    backgroundColor: "{colors.healthy-surface}"
    textColor: "{colors.healthy-text}"
    typography: "{typography.label}"
    rounded: "{rounded.badge}"
    padding: "4px 8px"
  status-degraded:
    backgroundColor: "{colors.warning-surface}"
    textColor: "{colors.warning-text}"
    typography: "{typography.label}"
    rounded: "{rounded.badge}"
    padding: "4px 8px"
  status-offline:
    backgroundColor: "{colors.offline-surface}"
    textColor: "{colors.offline-text}"
    typography: "{typography.label}"
    rounded: "{rounded.badge}"
    padding: "4px 8px"
  metric-panel:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.panel}"
    padding: "22px 24px"
  table-header:
    backgroundColor: "{colors.surface-subtle}"
    textColor: "{colors.muted}"
    typography: "{typography.label}"
    padding: "11px 15px"
  nav-item:
    textColor: "{colors.muted}"
    rounded: "{rounded.nav}"
    padding: "11px 12px"
---

# Design System: FlowSeer

The console lives in `frontend/web/`. The contrast-tested semantic tokens are
defined in `frontend/web/src/style.css`, the twelve-step palette scales in
`frontend/web/src/theme/scales.css`, and spacing, radii, and fonts in
`frontend/web/src/theme/language.css`. When this file and the code disagree,
the code is the current truth and this file is stale.

## Overview

**Creative North Star: "The Night Shift Desk"**

An m3connect operator keeps this console open for a whole shift, across many
hotels and campuses. The workspace stays calm and low in saturation so that
the few signals that matter are the only things asking for attention: a
labelled health state, a device that needs attention, the one action the
operator is about to take. Dark mode is charcoal, light mode is an overcast
gray with softly lifted panels, and both are complete, first-class themes.

The brand is present, but at the edge. Cyan-to-coral light falls across the
navigation frame (sidebar, top bar, and the curved collapse tab share one
fixed-position glow, so they read as one continuous piece of chrome), and
inside the frame the content is quiet: 1px lines, tonal steps, tabular
figures, stable rows. Density follows the desk. Tables are 64px rows at 12px
text; phones get compact status cards and one-tap details instead.

Motion is feedback, never decoration. It confirms a scope change or an
opened panel in 100 to 160 ms and then gets out of the way.

**Key Characteristics:**

- Neutral charcoal and overcast-gray surfaces carry almost all of the screen.
- Coral marks the one primary action; cyan marks where you are.
- Health is always a word plus a color, on its own green, amber, and rose
  families.
- Depth is tonal; shadows are barely there.
- One continuous, glow-lit navigation frame around a scrolling content area.

## Colors

A neutral workspace with two brand anchors used sparingly and three
dedicated status families that never borrow brand hues.

### Primary

- **Signal Coral** (`signal-coral`): the primary action and nothing else.
  "Save assignment" is coral; a filter or a navigation item never is. Text on
  it is near-black `on-coral`, not white. `accent-orange-text` is its
  readable text tone for the rare coral-voiced label.

### Secondary

- **Harbor Cyan** (`harbor-cyan`): selection and location. The active
  navigation accent bar, the selected scope, and the chrome focus ring use
  its readable derivatives (`accent-text`, `focus`, and their `-dark`
  forms). Raw `harbor-cyan` is too light for text on light surfaces and
  appears only as glow and fill.

### Neutral

- **Charcoal Desk** (`charcoal-desk`): dark-mode chrome, menus, and the base
  the brand glow is mixed over.
- **Overcast Gray** (`overcast-page`, `chrome-light`): the light-mode page
  and chrome. Panels sit one step lighter on `surface`.
- **Surfaces** (`surface`, `panel-surface`, `surface-subtle`,
  `surface-hover`, and `-dark` forms): tonal steps that do the work shadows
  do elsewhere. Table headers and device icons use `surface-subtle`; hover
  uses `surface-hover`.
- **Text** (`text`, `muted`): primary text holds 7:1 against its
  background; `muted` holds 4.5:1 and carries labels, units, and supporting
  copy.
- **Lines** (`line`, `control-border`): `line` divides content;
  `control-border` outlines interactive controls at 3:1.

### Status

- **Healthy** (`healthy-surface` / `healthy-text`), **Degraded**
  (`warning-surface` / `warning-text`), **Offline** (`offline-surface` /
  `offline-text`). Each badge pairs a tinted surface, a dot in
  `currentColor`, and the status word.

### Named Rules

**The One Coral Rule.** A view has at most one coral control, and it is the
action the operator came to take. If two things feel primary, one of them is
not.

**The Brand Is Not Status Rule.** Coral never means "error" and cyan never
means "healthy". Status uses its own labelled families so that a brand color
on screen can never be misread as an alarm.

**The Semantic Layer Rule.** Components consume the semantic tokens in
`style.css`, never the `--m3-*` scale steps or raw brand hexes directly. The
semantic layer is what the palette tests hold to WCAG contrast.

## Typography

**Body Font:** Inter Variable (with -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif)
**Mono Font:** the platform monospace stack (ui-monospace, SFMono-Regular, Consolas)

**Character:** One family doing all the work with optical sizing on. Inter's
tall x-height keeps 11 and 12px labels legible, and its tabular figures keep
changing numbers from jittering in a column. GT Standard, the m3connect
website face, replaces it only once licensed files are supplied.

### Hierarchy

- **Display** (550, 32px, -0.8px): metric values on the dashboard and fleet
  panels, with the unit set smaller beside the number.
- **Headline** (600, 28px, 1.3, -0.8px): the page heading, once per view.
- **Title** (600, 15px, -0.2px): section and panel headings, dialog titles.
- **Body** (400, 13px, 1.6): running copy and page descriptions in `muted`.
- **Label** (500, 11 to 12px): table headers, badges, metric labels, control
  text at 12px, sort buttons.
- **Mono** (12px): device addresses, serials, MACs.

### Named Rules

**The Tabular Figures Rule.** Any number that updates live or lines up in a
column uses tabular figures, with its unit beside it, never above.

**The Sentence Case Rule.** Labels are sentence case at regular or medium
weight. Nothing is uppercase, heavy, and letter-spaced at once.

## Layout

A fixed navigation frame (228px sidebar, 63px top bar) wraps one scrolling
content area. The frame never scrolls; the content area owns vertical scroll
and contains its overscroll, so pulling past the top never drags the top bar.
The top bar blurs content passing beneath it.

Spacing is a 4px base with steps at 4, 8, 12, 16, 24, and 32px: 8px inside
controls, 16px between related elements, 24px panel padding and the gap below
the page heading.

Responsive steps: at 1150px the sidebar narrows to 195px; at 800px the
navigation moves above the content with a horizontal underline highlight and
the tenant selector moves with it; at 560px the device table becomes compact
status cards and topology and secondary traffic summaries drop out. Desktop
can also collapse the sidebar to a 64px icon rail through the curved tab on
its edge.

Coarse pointers raise controls to a 44px minimum height.

## Elevation & Depth

Depth is tonal. Panels differ from the page by one surface step and a 1px
line, and hover deepens the surface rather than raising it. Resting shadows
exist but are nearly invisible (1 to 3% alpha, a few pixels), there to soften
a panel edge in light mode, not to lift it. Real shadows belong only to
layers that float above the content.

### Shadow Vocabulary

- **Whisper** (`box-shadow: 0 2px 3px #08232b03`): metric panels and
  topology panels at rest.
- **Field hairline** (`box-shadow: 0 1px 2px #0a263608`): search input.
- **Popover** (`box-shadow: 0 12px 32px #00000026`): scope and account
  menus.
- **Drawer** (`box-shadow: -15px 0 60px #092a3420`): the device details
  panel, over a `#09232b45` backdrop blurred 2px.

### Named Rules

**The Floating-Only Rule.** A shadow you can clearly see means the element
floats above the page (menu, drawer, dialog). Anything in the page flow stays
tonal.

**The Glass Is Chrome Rule.** Backdrop blur is for the navigation frame and
overlay backdrops. Content panels are opaque, and the frame keeps a tinted
base so it stays readable where blur is unavailable.

## Shapes

Corners are gentle and consistent by role: controls at 8px, content panels
and popovers at 12px, navigation items at 5px, badges and in-menu options at
4px, status dots fully round. Borders are 1px. The one deliberate curve is
the collapse tab on the sidebar edge and the rounded inner corner where the
sidebar meets the top bar, which make the frame read as one shape.

## Components

### Buttons

Quiet and exact.

- **Shape:** 8px corners, 36px minimum height (32px small, 44px on coarse
  pointers), 13px text at weight 550.
- **Default:** `surface` fill, `control-border` outline, `text` label.
- **Primary:** Signal Coral fill and border with `on-coral` text. Hover mixes
  12% of `on-coral` into the coral.
- **Ghost:** no fill or border, `accent-text` label, for low-stakes actions
  beside a primary.
- **States:** hover changes background in 100ms ease-out; active drops 1px;
  disabled is 45% opacity; focus is a 3px `focus` outline offset 3px.

### Status badges

- **Style:** 4px corners, 4px by 8px padding, 11px label at 500, a 5px dot in
  `currentColor`, the status word always present.
- **States:** Healthy, Degraded, Offline, each on its own surface/text pair.

### Cards / Containers

- **Corner Style:** 12px.
- **Background:** `surface`, dividers in `line`.
- **Shadow Strategy:** Whisper at most (see Elevation & Depth).
- **Border:** 1px `line`.
- **Internal Padding:** 22 to 24px; metric panels divide their cells with
  1px vertical rules instead of separate cards.

### Inputs / Fields

- **Style:** 1px `control-border`, 8px corners, `surface` fill, 12px text;
  search carries a 14px leading icon in `muted`.
- **Focus:** the shared 3px `focus` outline, offset 3px.
- **Selects:** native, with a labelled prefix inside the same outline for
  filters ("Status: All").

### Tables

- **Header:** `surface-subtle` band, 11px `muted` labels at 500, lines above
  and below.
- **Rows:** 64px tall, 12px text, 1px `line` dividers, `surface-hover` on
  hover, first column inset 24px. Rows keep their order while values update.
- **Device cell:** a 32px icon tile in `surface-subtle`, the name at 13px/550
  turning `accent-text` on hover, and a `muted` secondary line.

### Navigation

- **Style:** chrome-muted items at 500 weight with 11px by 12px padding and
  5px corners.
- **Active:** a translucent blurred fill slides to the selected item in 140ms
  (instantly under reduced motion) with a 3px cyan accent bar on the
  sidebar's outer edge; the label turns `chrome-focus`.
- **Mobile:** the list turns horizontal above the content and the highlight
  becomes an underline.

### Navigation frame (signature)

The sidebar, top bar, and collapse tab share one fixed brand glow: two soft
radial washes (cyan from the left, coral from the right) crossed by thin
diagonal ribbons at 112 degrees with 1px white highlights, over the chrome
base. Glow strength is 30% in dark mode and 13% in light mode. It lives in
`frontend/web/src/theme/brand-glow.css` and nowhere else.

## Do's and Don'ts

### Do:

- **Do** use exactly one coral control per view, for the action the operator
  came to take.
- **Do** pair every status color with its word, and every live number with
  tabular figures and an inline unit.
- **Do** consume semantic tokens from `style.css` so the palette tests keep
  covering the new surface.
- **Do** keep a visible 3px focus outline on every interactive element.
- **Do** keep motion to 100 to 160ms ease-out feedback and skip it entirely
  under reduced motion.
- **Do** keep tenant and site scope visible on every surface, phone
  included.

### Don't:

- **Don't** set labels uppercase, heavy, and letter-spaced together. The
  Components workspace eyebrows ("DESIGN WORKSPACE", 10px, 600, 1.5px
  tracking) are drift to fix, not a pattern to copy. The sidebar's
  "WORKSPACE" label is uppercase and tracked at regular weight; it sits at
  the edge of this rule and should not spread.
- **Don't** color text with raw brand hexes or `--m3-*` scale steps; use the
  semantic accent tokens.
- **Don't** use coral or cyan to mean status.
- **Don't** lift in-flow panels with visible shadows; that is reserved for
  menus, drawers, and dialogs.
- **Don't** animate live values, sorting, typing, or the header glow, and
  don't add staggered rows, counting numbers, spring overshoot, or looping
  effects.
- **Don't** let closing a panel or dismissing a notice wait on an animation.
