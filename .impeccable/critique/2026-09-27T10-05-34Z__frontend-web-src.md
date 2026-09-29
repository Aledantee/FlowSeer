---
target: the console
total_score: 27
max_score: 40
na_heuristics:
p0_count: 0
p1_count: 1
target_identity: "file:frontend/web/src"
timestamp: 2026-09-27T10-05-34Z
slug: frontend-web-src
---
Method: dual-agent (A: design review · B: detector + browser overlay)

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 3 | Live dashboard numbers carry no "as of" time; site health bars give counts only to hover and screen readers |
| 2 | Match System / Real World | 3 | Invalid scope reads "Viewing Unavailable selection"; screen reader hears "severity critical" where the screen says Offline |
| 3 | User Control and Freedom | 3 | A pending move has no cancel; sort is not kept in the URL while search and status are |
| 4 | Consistency and Standards | 2 | "Need attention" metric vs "Needs attention" panel; sites are cards on one page and a table on another; focus survives a move under Status sort but not Site sort |
| 5 | Error Prevention | 3 | Tenant-fenced moves; single-site tenants still get a disclosure that only says no |
| 6 | Recognition Rather Than Recall | 3 | Breadcrumb shows "Aurora Germany" without its parent tenant |
| 7 | Flexibility and Efficiency | 2 | No shortcuts or bulk actions; search ignores site and tenant names |
| 8 | Aesthetic and Minimalist Design | 3 | Devices page states the count 16 five times; the metric row repeats the attention panel below it |
| 9 | Error Recovery | 3 | Scope error does not name the bad value and says "selected tenant" under All tenants |
| 10 | Help and Documentation | 2 | No in-context explanation of Degraded vs Offline or reachability vs lifecycle |
| **Total** | | **27/40** | **Acceptable** |

## Design Specificity Verdict

**LLM assessment:** Mostly authored. Tenant filled in from the site, per-path reachability with check age, sent-versus-observed wording for moves and polls, the "Assumed link" legend, and "Last answered" belong only to this product. The metric card row, the 24h area chart, and the Sites card grid remain category-default.

**Deterministic scan:** CLI 0 findings. In-page: the same 7 shell findings on every page, all false positives (documented brand glow ×3, documented popover shadow ×3, deliberate shell clip). Page-specific: 11px sentence-length text in metric notes and one subheading (at the documented floor; a judgment call), sr-only text and "—" placeholders (false positives), the dashboard grid still running one column to 227% of viewport height beside one at 38%, and the 14px phone gutter. The design review adds one real rule break the detector cannot see: status badges are 11px against DESIGN.md's 12px status floor.

## Overall Impression

The second round fixed what it targeted: scope agrees with itself, trouble leads the dashboard, lists carry age, moves and topology are honest. The score held at 27 because the review now reaches deeper problems: the worst moment of a shift (polling an offline device) ends in silence, and a few repeats and rule breaks remain.

## What's Working

- **Truthful status:** moves report done only when observed, polls are observations, reachability is per path and apart from lifecycle.
- **Careful scope:** tenant filled from the site, invalid scope is an error, clearing filters keeps scope, scope visible on phones.
- **Two complete themes and a real phone layout:** one coral control per view, status cards, full-screen drawer.

## Priority Issues

**[P1] Polling an offline device is a dead end** (`FleetView.vue:323-326`, `995-1000`)
- Why it matters: "did my fix work" at its most stressful moment ends at "Still not answering." with no next step.
- Fix: summarise the paths ("Both paths unreachable for 38 min"), say whether other devices at the site are down too, link the site dashboard, offer a copyable escalation summary.
- Suggested command: /impeccable harden

**[P2] The dashboard's first screen repeats itself**
- Why it matters: "Need attention: 2" sits directly above "Needs attention, 2 of 16"; the grid imbalance (227% vs 38%) is detector-confirmed.
- Fix: collapse the metric row to one compact line or drop the attention tile; add an "as of" time to the summary.
- Suggested command: /impeccable distill

**[P2] Move under Site sort loses focus and the row**
- Why it matters: the moved row jumps groups and focus falls to body.
- Fix: keep focus on the moved row wherever it lands, or hold row order until the notice is dismissed; link the device from the notice.
- Suggested command: /impeccable harden

**[P2] Status badges break the 12px status floor** (`style.css:642`)
- Why it matters: DESIGN.md's own rule; DESIGN.md's badge spec also contradicts it. Issue dots carry severity by color alone for sighted users.
- Fix: badges to 12px, correct DESIGN.md's badge spec, a visible severity cue on dots.
- Suggested command: /impeccable typeset

**[P2] Sites is a generic card grid**
- Why it matters: identical cards, a location kicker above each heading (`FleetView.vue:838`, breaks the kicker ban), "1 need attention" grammar, no health order, no link to the focused dashboard.
- Fix: reuse the dashboard's site rollup table sorted by attention, linking each site to `/dashboard?site=…`.
- Suggested command: /impeccable layout

## Persona Red Flags

**Alex:** no `/` or row keys; no bulk poll or move; search misses site and tenant names; sort not in the URL; descending sort also reverses the name tiebreak (`FleetView.vue:174`); "Last answered ↑" direction is ambiguous.

**Sam:** focus falls to body after a move under Site sort; 11px badges; color-only issue dots for low-vision users; site health counts only via title and aria-label.

**Night-shift NOC operator:** nothing marks what changed since the last glance; attention ordered by severity then name, not recency; no "as of" time; dashboard site table in fixture order; the move notice covers rows; the breadcrumb hides the parent tenant; the offline poll dead end.

## Minor Observations

- The move disclosure stays open when the next device is opened (`FleetView.vue:1036`); single-site tenants should not get the disclosure at all.
- Two metrics share the pulse icon; gateway and switch share one icon.
- A focused healthy site shows its name three times and a large attention panel holding one sentence.
- A reserved empty line under Poll now; topology AP nodes change height when a badge appears.
- 14px phone gutter.
- No uppercase labels remain; One Coral and Floating-Only hold.

## Questions to Consider

- If a poll can never change health, is "Poll now" the right coral action, or is the operator's real task confirm, escalate, or fix?
- What if the dashboard showed only what changed since the operator last looked?
- Does Sites need to exist when the dashboard's site table does its job better?
