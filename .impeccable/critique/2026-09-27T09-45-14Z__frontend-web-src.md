---
target: the console
total_score: 27
max_score: 40
na_heuristics:
p0_count: 0
p1_count: 2
target_identity: "file:frontend/web/src"
timestamp: 2026-09-27T09-45-14Z
slug: frontend-web-src
---
Method: dual-agent (A: design review · B: detector + browser overlay)

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | Age of a status is only in the drawer; list views show none |
| 2 | Match System / Real World | 3 | "Fleet health 88%" is a derived number no operator acts on |
| 3 | User Control and Freedom | 3 | Undo exists, but focus drops to body after it; "Moving…" cannot be cancelled |
| 4 | Consistency and Standards | 2 | With a site selected the crumb still says "All tenants" and the nav count stays 16 |
| 5 | Error Prevention | 3 | Move is tenant-fenced, but a single-site tenant opens onto a dead-end select |
| 6 | Recognition Rather Than Recall | 3 | Dashboard site health bars carry counts only in hover titles |
| 7 | Flexibility and Efficiency | 2 | URL-persisted state is good; no shortcuts, bulk actions, or saved views |
| 8 | Aesthetic and Minimalist Design | 3 | Calm, but every topology node wears a "Healthy" badge and the Healthy bar is the brightest dark-mode mark |
| 9 | Error Recovery | 3 | "Scope not found" explains and offers a way out; router failure copy is generic |
| 10 | Help and Documentation | 2 | No in-context explanation of binding, reachability vs lifecycle, or what Poll now does |
| **Total** | | **27/40** | **Acceptable** |

## Design Specificity Verdict

**LLM assessment:** Mostly authored. The device drawer (per-binding reachability with check age, lifecycle apart, tenant / site header), sent-versus-observed poll and move, "Scope not found", and "—" for offline values are things only this product would build. The overview pages are still category-default: four KPI tiles over a large area chart, a 2×2 grid of identical site cards, a boxed topology tree.

**Deterministic scan:** CLI 0 findings. In-page overlay: 7 findings shared by every page, all likely false positives (brand glow ×3, documented popover shadow ×3, deliberate shell clip). Page-specific: 11px `metric-note` text on the dashboard (at the documented floor), one dashboard column running to 225% of viewport height beside one at 33% (agrees with the design review's hierarchy finding), and body text 14px from the phone viewport edge. sr-only text and "—" cell placeholders flagged as tiny text and em-dash overuse are false positives. First run: 15 to 30 flagged elements per page; now 7 to 11, with every 10px and contrast failure gone.

## Overall Impression

The drawer went from the weakest surface to the product's best moment, and the console now tells the truth about what it has observed. The remaining gap is the first screen: the Dashboard still leads with how busy the network is rather than what is wrong, and the scope display can contradict itself.

## What's Working

- **The device drawer:** cause first, per-binding reachability with check age, lifecycle separate, one coral action.
- **Status honesty:** sent kept apart from observed for poll and move; offline shows "—"; unresolved scope is an explicit error.
- **Restraint holds:** one coral control per view, words with every status color, both themes complete, no horizontal scroll at 390px, 11px floor and sentence case hold.

## Priority Issues

**[P1] Scope display contradicts itself**
- Why it matters: Principle 1. With `?site=berlin` the tenant crumb reads "All tenants" (`FleetView.vue:493-497` reads only the tenant query), the subtitle omits the tenant, and the sidebar count stays 16 (`fleet.length`) while the scope holds 4.
- Fix: derive the effective tenant from the selected site for the crumb and subtitle; scope the nav count or drop it.
- Suggested command: /impeccable clarify

**[P1] The Dashboard leads with traffic, not trouble**
- Why it matters: the operator's job is "find the device that needs attention"; the illustrative chart holds the 2fr lead column and "Needs attention" sits in the 1fr side column (`dashboard.css:3`). The detector's column-overflow finding is the same imbalance. In dark mode the Healthy segment is the brightest mark.
- Fix: lead with the attention list, cause and age inline; move traffic to the secondary column; make Healthy the quietest segment.
- Suggested command: /impeccable layout

**[P2] Status age missing from list views**
- Why it matters: Principle 2; a device offline 38 minutes and one offline 3 days look identical in the table and on the dashboard.
- Fix: a tabular, sortable "Last answered" column; top issue plus age on each attention row.
- Suggested command: /impeccable clarify

**[P2] Move flow rough edges**
- Why it matters: a single-site tenant opens a dead-end select; the Move button lands below the drawer fold; focus is lost after Undo; after undoing, the notice offers "Undo" again with no "reverted" wording.
- Fix: say "No other site in <tenant>" instead of the form; scroll the form into view on expand; return focus; word the undo result as reverted without a second Undo.
- Suggested command: /impeccable harden

**[P2] Topology draws guesses like facts**
- Why it matters: assumed links are solid lines identical to what discovered links would be, the disclaimer lives only in the subtitle, and "Healthy" badges on every node drown the one Degraded node.
- Fix: dashed, lower-contrast links with an "Assumed link" legend; badge only non-healthy nodes.
- Suggested command: /impeccable quieter

## Persona Red Flags

**Alex (power user):** no `/` to search, no row keyboard navigation, only the name button opens a row, no bulk poll, no clear-search button, no sort by age.

**Sam (accessibility):** dashboard health bars are color-only for sighted users (counts in `title`); focus goes to body after Undo; disabled "Polling…" drops to 45% opacity.

**Night-shift NOC operator:** the brightest dark-mode mark is the Healthy bar; the traffic line moves every 2.5s in an otherwise still screen; "All tenants" while one site is scoped; nothing marks what is new since the last look; Poll now is the primary action for a radio retry-rate problem it cannot fix.

## Minor Observations

- Raw hexes break the Semantic Layer Rule: `.avatar` (`style.css:302-303`), `.paused` (`style.css:418`).
- Shadow drift from the documented Whisper at `dashboard.css:12`, `style.css:489`, `style.css:837`; check `dashboard.css:155` against Floating-Only.
- Dashboard attention sort has no tiebreak (`DashboardView.vue:34-36`).
- Gateway and core switch share one icon.
- Sites cards duplicate the dashboard site table with less information.
- Phone gutter is 14px; phone chrome still about 290px of 780px.
- "Updates every 2.5s" is implementation detail in product copy.

## Questions to Consider

- If the first screen were only "what is wrong, where, since when", would the traffic chart and the 88% tile survive?
- Should the primary action come from the cause, with Poll now as the quiet default?
- What does an operator do on Sites that the focused Dashboard cannot do? If nothing, should Sites become per-site edge and integration health?
