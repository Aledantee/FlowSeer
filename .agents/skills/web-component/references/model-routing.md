# Routing component work to a model

Load this when `delegate` dispatches web component work. `delegate` and
the machine registry (`~/.claude/models/registry.yaml`) still decide
quota, lanes, and vendor separation. This file only ranks models for UI
work, using evidence gathered on 2026-09-28. Re-check it when the
registry gains a model or the date is more than a month old.

## Evidence

Human pairwise votes are weighted above vendor claims:

- **WebDev Arena** (https://arena.ai/leaderboard/code/webdev/, about
  795k votes, updated 2026-09-25): Opus 5.5 1827 (only about 1.6k
  votes), GPT-6 Astra 1792, Fable 5.1 1751, Opus 5 1693, GPT-6 Sol 1681,
  Kimi K3 1660, DeepSeek V4.1 Flash 1621. Gemini 3.8 Flash is outside
  the top 20.
- **Design Arena UI components.** Blind votes on buttons, forms, modals,
  and cards; the two September snapshots disagree on the leader.
  - https://benchmarklist.com/arenas/design_arena_ui_components/: Fable
    5.1 1381, Kimi K3 1379, Opus 5 third.
  - https://modelgrep.com/best/ui-components: Astra 1377, Kimi K3 1363,
    Opus 5 1354, Fable 5.1 1347, Gemini 3.8 Flash 1327.
- **Not measured by anyone:** Vue specifically, fidelity to an existing
  token system, overlay correctness, motion quality, accessibility. The
  gates in this skill enforce those, not the choice of model.

## Routing

| Role | Model |
|---|---|
| Implement a component or an overlay | claude-opus-5-5, xhigh effort |
| Cheaper fallback | kimi-k3, with one local calibration run so far |
| Hardest visual surfaces | claude-fable-5-1 |
| Review | a different vendor from the implementer, as the registry requires: gpt-6-sol or gpt-6-astra when codex quota allows, else kimi-k3 or gemini-3.8-flash |
| Logic-only view code | deepseek-v4.1-flash and gemini-3.8-flash are fine |

Don't route component or overlay work to deepseek-v4.1-flash or
gemini-3.8-flash; both rank mid-pack on UI.

Give every reviewer the rendered screenshots from `review.md`, not just
the diff.

## Brief additions

On top of what `delegate` requires, a web component brief includes:
- this skill's name and which references apply
- the story ids to screenshot
- that installing any package is a blocker to report, never an action to
  take
