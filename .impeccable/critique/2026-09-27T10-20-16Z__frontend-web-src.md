---
target: the console
total_score: 27
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 3
target_identity: "file:/Users/aledante/orca/workspaces/FlowSeer/palolo/frontend/web/src"
timestamp: 2026-09-27T10-20-16Z
slug: frontend-web-src
---
Method: dual-agent (A: design review · B: detector + browser overlay)

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | Before any poll, unreachable paths read "checked 38 min ago", implying monitoring stopped when the device went down |
| 2 | Match System / Real World | 3 | Coral "Poll now" leads for a degraded AP whose radio problem a poll cannot fix; ages never pass hours ("72 h ago") |
| 3 | User Control and Freedom | 3 | Undo, Esc, and scope-keeping clear work; sort is not in the URL |
| 4 | Consistency and Standards | 3 | "—" means both "not an AP" and "offline"; dashboard site health is a bare bar while Sites adds a text line |
| 5 | Error Prevention | 2 | A move commits in one click with no consequence check (an AP on Hamburg's subnet moves to Berlin unflagged) |
| 6 | Recognition Rather Than Recall | 3 | Sortable headers look like static ones until active |
| 7 | Flexibility and Efficiency | 2 | No shortcuts, no bulk actions; the drawer closes after each move |
| 8 | Aesthetic and Minimalist Design | 3 | Calm, but three KPI cards take the space where the answer should be |
| 9 | Error Recovery | 3 | Scope error says "selected tenant" under All tenants and does not repeat the bad value |
| 10 | Help and Documentation | 2 | Help does not explain reachability vs lifecycle, "Assumed link", or Poll now |
| **Total** | | **27/40** | **Acceptable** |

## Design Specificity Verdict

**LLM assessment:** The drawer is authored for this product (site comparison for an offline device, per-path reachability with check times, separate lifecycle, escalation summary), as is scope handling. The dashboard frame (KPI cards, area chart, sites table, event feed) and Topology's identical boxes remain template-shaped; Sites mostly repeats the dashboard table.

**Deterministic scan:** CLI 0 findings (also with `--no-config`). In-page: 7 shared findings, all false positives (brand glow, documented popover shadow, deliberate shell clip). Real: the dashboard column running to 227% of viewport height beside 38% (unchanged across three runs), the 14px phone gutter. 11px metric notes and one subheading sit on the documented label floor.

## Overall Impression

Third run at 27. Each round closed what it targeted and the review found the next layer at the same depth: this is the plateau of a fixture-backed skeleton, not stalled work. The remaining leverage is structural (what the dashboard opens on, what a failed poll hands the operator) and in features the product has not decided yet (shortcuts, bulk actions, "new since last look").

## What's Working

- **Diagnosis in the drawer:** issues, site comparison, per-path reachability with check times, lifecycle apart.
- **Trustworthy scope:** loud failure for unknown sites, scope-keeping clear, tenant filled from the site.
- **Moves reported on observation,** with Undo and focus that follows the row.

## Priority Issues

**[P1] The dashboard opens on KPI cards, not on what needs attention**
- Why it matters: 16 / 396 / 780 Mbps answer nothing; on phones they push the answer about 430px down. The detector's column-overflow finding persists.
- Fix: attention first; fold counts into the heading line; no metric cards on phones.
- Suggested command: /impeccable layout

**[P1] After a failed poll the coral action is still Poll now**
- Why it matters: the highest-stakes moment steers back to the action that just failed; the result line pushes Copy escalation summary down under the cursor.
- Fix: after a failed poll, make the escalation the one coral action; reserve the result line's height; separate "last checked" from "last answered" per path.
- Suggested command: /impeccable clarify

**[P1] Phone device cards hide status from screen readers** (`FleetView.vue:767`)
- Why it matters: `aria-label="View status for …"` replaces the card content; VoiceOver hears no health, site, or age.
- Fix: drop the aria-label or include health, site, and age in it.
- Suggested command: /impeccable harden

**[P2] A move has no consequence check**
- Fix: an inline line before commit when the address sits in the origin site's range; keep Undo, no modal.
- Suggested command: /impeccable harden

**[P2] Topology and Sites add little**
- Fix: sort Topology worst first and collapse healthy sites; merge Sites into the dashboard or give it columns the dashboard lacks.
- Suggested command: /impeccable distill

## Persona Red Flags

**Alex:** no shortcuts; sort not in the URL and descending reverses the name tiebreak; the drawer closes after each move; only the name button opens a row.

**Sam:** phone card aria-label hides status; dashboard health bars text-less for sighted low-vision users; sortable headers uncued; "Polling…" at 45% opacity on coral.

**Night-shift NOC operator:** tab title never reflects attention; nothing marks what is new; minute-precision "as of" with no stale warning; dim dark-mode status chips beside a bright coral button; "72 h ago" for long outages.

## Minor Observations

- Coral Poll now under a rose Offline badge reads as part of the alarm.
- Clients metric uses the topology icon.
- Phone breadcrumb wraps with a hanging separator; page name splits tenant from site.
- A healthy site's "newest issue" may be resolved; nothing says so.
- A filtered-to-nothing attention search reads as failure when it is good news.
- Sites repeats its name as h1, h2, and breadcrumb.

## Questions to Consider

- If 03:00 needs "what changed since I last looked", why open on totals that barely change?
- After a poll fails, should the one coral control move to escalation?
- Should Topology wait for discovery, or become a per-site reachability view built from the bindings that already exist?
