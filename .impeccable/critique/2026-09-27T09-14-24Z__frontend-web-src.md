---
target: the console
total_score: 22
max_score: 40
na_heuristics:
p0_count: 0
p1_count: 2
target_identity: "file:/Users/aledante/orca/workspaces/FlowSeer/palolo/frontend/web/src"
timestamp: 2026-09-27T09-14-24Z
slug: frontend-web-src
---
Method: dual-agent (A: design review · B: detector + browser overlay)

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 2 | No status shows source or age; site assignment reports success on send, not on observation |
| 2 | Match System / Real World | 2 | Topology draws APs daisy-chained; offline devices show a measured "0 Mbps" |
| 3 | User Control and Freedom | 2 | No undo for reassignment; "Reset all filters" also drops tenant and site scope |
| 4 | Consistency and Standards | 3 | Topology uses 10px words and border tints instead of StatusBadge; sites are a table on Dashboard and cards on Sites |
| 5 | Error Prevention | 2 | Assignment is tenant-fenced but commits in one click with no impact preview |
| 6 | Recognition Rather Than Recall | 3 | Scope always visible; device details omit why a device is degraded |
| 7 | Flexibility and Efficiency | 1 | No shortcuts, no bulk actions, only name sorts; two tab stops per row |
| 8 | Aesthetic and Minimalist Design | 3 | Calm, but the KPI strip and banner repeat above every view and push work to y≈430 |
| 9 | Error Recovery | 2 | An unknown `?site=` renders 0% health and "Every device in this scope is healthy" |
| 10 | Help and Documentation | 2 | Help dialog places the tenant selector "in the sidebar"; it is in the top bar |
| **Total** | | **22/40** | **Acceptable** |

## Design Specificity Verdict

**LLM assessment:** Partly authored. The glow-lit frame, the tenant / view / site breadcrumb, tenant-fenced site assignment, role-grouped site focus, and operational event copy ("Retry rate on the 5 GHz radio above 18%") belong to this product. The page composition does not: the same four KPI cards on every view, marketing subtitles ("Monitor health and keep your fleet connected."), an uppercase "NETWORK OPERATIONS" kicker, an icon-tile Sites card grid, and a 24h area chart as the hero are category-default. FlowSeer's own vocabulary (Integration, Binding, reachability vs lifecycle, status source and age) is absent; one Healthy/Degraded/Offline word carries everything.

**Deterministic scan:** `impeccable detect` on `frontend/web/src` returned 0 findings (also with `--no-config`); it cannot see computed styles. The in-page overlay found 15 to 30 flagged elements per page. Genuine: 10px text on the "WORKSPACE" nav label, the eyebrow kicker, every "Mbps" unit (×20 on Devices), topology node status labels (~16), and "14 healthy" on phone; "WORKSPACE" at 4.2:1 contrast (needs 4.5:1); kicker above the h1; 11px `metric-note` body text; one dashboard column running to 225% of viewport height; body text 14px from the phone viewport edge; 9 em-dashes in FleetView copy. Likely false positives: `ai-color-palette` on the brand glow (a deliberate, documented brand layer), `gpt-thin-border-wide-shadow` on popovers and `dark-glow` on the drawer (DESIGN.md's Floating-Only shadows), `clipped-overflow-container` on the shell (deliberate overscroll containment). `dark-glow` on the four dashboard panels is worth a look: DESIGN.md calls those whispers, and the detector reads them as glows in dark mode.

The detector caught what the design review underweighted: the 10px type is systemic (units, topology, nav label), not a Components-page drift, and it is the one hard contrast failure on screen.

## Overall Impression

A disciplined, calm frame that already keeps its promise about scope, wrapped around content that is still a generic fleet dashboard. The biggest opportunity is the device details panel: it is where "what is wrong, where, and did my fix work" should be answered, and today it answers none of the three.

## What's Working

- **Scope is structural.** Tenant / view / site sits in one breadcrumb, persists in the URL, and a tenant change clears the site. Principle 1 is real, not decoration.
- **Status discipline.** Every badge pairs dot and word, the health bar has a screen-reader summary, the chart is keyboard-steppable, and in-flow shadows really are whispers. DESIGN.md is mostly executed faithfully.
- **Phone devices become status cards** with name, badge, site, and IP, and details open full-screen: a real field list, not a squeezed table.

## Priority Issues

**[P1] Device details has no diagnosis and the wrong primary action** (`FleetView.vue:705-758`)
- Why it matters: PRODUCT.md promises the operator reaches "the relevant quick action" and that status "carries its source and age". The panel shows tenant, site, clients, and one coral "Save assignment", which is rarely why a degraded device was opened. Disabled, that coral slab is still the loudest thing in the panel.
- Fix: lead with why (triggering events, last seen, reporting Binding and its age; reachability and lifecycle as separate lines), make the primary action context-dependent, and demote site assignment to a collapsed secondary section.
- Suggested command: /impeccable shape

**[P1] Status and scope truthfulness at the edges**
- Why it matters: "a change is done when it is observed" and "scope is always visible" are the product's own principles. Assignment announces success on send; offline devices show "0 Mbps" (`FleetView.vue:591`); an invalid scope renders "Every device in this scope is healthy" (`DashboardView.vue:105`); "Reset all filters" silently widens to all tenants (`FleetView.vue:193`).
- Fix: pending → observed states with "from X to Y" and undo in the notice; "—" for unreachable metrics; unresolved scope as an error with a way back; reset clears only search and status.
- Suggested command: /impeccable harden

**[P2] 10px type and uppercase kickers across the console**
- Why it matters: detector-confirmed on every page: 10px units, nav label, topology node statuses, and the eyebrow; "WORKSPACE" fails 4.5:1. The uppercase, 600-weight, 1.5px-tracked eyebrow is also on "NETWORK OPERATIONS" and "DEVICE DETAILS", so it is spreading beyond the drift DESIGN.md names.
- Fix: 11px floor for units and labels in `muted`, 12px for anything read as status; replace eyebrows with sentence-case context or remove them; lift "WORKSPACE" contrast or drop the label.
- Suggested command: /impeccable typeset

**[P2] Attention is not the default order**
- Why it matters: Devices sorts only by name, so the problem devices sit among healthy ones, especially on the phone. The KPI strip and amber banner repeat above every view and outrank the inventory.
- Fix: default sort by severity then name; make Status and Site sortable; KPI strip on Dashboard only; merge the banner toggle into the status filter.
- Suggested command: /impeccable layout

**[P2] Phone layout fails the field engineer**
- Why it matters: "desk depth, field brevity". About 230px of a 780px phone viewport is chrome; the nav clips at "Compo…" and exposes the Components workspace; hiding tables at 560px (`style.css:1251-1256`) also hides the Dashboard site list, leaving "Select a site to focus the dashboard on it." with nothing to select.
- Fix: fold tools into the account menu on phones, hide Components outside development, give dashboard sites a card list.
- Suggested command: /impeccable adapt

## Persona Red Flags

**Alex (power user):** no `/` to search, no row navigation keys, no scope shortcut; no bulk select, so reassigning several devices is one dialog each; only the name column sorts; each device row and dashboard site row has two buttons opening the same thing (`FleetView.vue:565, 595`; `DashboardView.vue:137, 163`), doubling tab stops.

**Sam (screen reader, keyboard):** nav links carry `aria-label` lowercase "dashboard", overriding the visible label and dropping the count (`FleetView.vue:313`); a literal "!" attention glyph is not `aria-hidden` (`FleetView.vue:465`); topology health is a border tint plus a 10px word; the assignment notice shifts the page; light-mode page (#bec3ca) and panels (#dce0e4) barely separate at low vision; 10px text at 4.2:1 on "WORKSPACE".

**Night-shift NOC operator (project persona):** nothing marks what changed since the last look (no unacknowledged events, no alarm count in tab title); the event list stops at 6 with no "all events" and no acknowledge; the only thing that moves is the traffic number every 2.5s; no "data as of" timestamp; logout is disabled, so a shift handover cannot be represented.

## Minor Observations

- Topology draws every site as gw → sw → ap-01 → ap-02; draw APs as siblings or label the view illustrative, and use StatusBadge on nodes.
- Marketing-register page subtitles conflict with PRODUCT.md's product-facing copy rule.
- The pulse icon means fleet health, device traffic, and access point at once (`FleetView.vue:441, 456, 572`).
- "88 %" has a space before the percent sign.
- Clicking a dashboard site row changes scope without scrolling up, so the updated header stays off-screen.
- The help dialog misplaces the tenant selector.
- The no-results state reflows table columns.
- Dashboard: one column runs to 225% of viewport height beside a column that fits in 33%.
- One Coral, Brand Is Not Status, Tabular Figures, and Floating-Only rules hold; Sentence Case does not.

## Questions to Consider

- If a change is only done when it is observed, why does the console's only mutation close its dialog and declare success in the same frame?
- Would an operator open Sites or Topology in their current form, when the Dashboard site table already carries more per site?
- What does "Degraded" mean for a device reached through two Bindings, one healthy and one failing, and where would the operator see the split?
