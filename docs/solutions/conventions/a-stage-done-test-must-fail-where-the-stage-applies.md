---
title: A Stage's Done Test Must Fail Wherever the Stage Applies, and Must Run Through the Wrapper That Polls It
date: 2026-10-06
category: conventions
module: .agents/skills/drive
problem_type: convention
component: workflow-skills
severity: medium
applies_when:
  - "Writing or changing a stage table whose rows pair an \"applies when\" test with a \"done when\" test, such as the one in `drive` step 2."
  - "Writing a condition a skill hands to `orca-worker.sh wait --until '<test>'` or to any other wrapper that quotes and re-runs it."
  - "Adding a state that a stage's worker writes (a new `plan_record.py` command, a new status) and checking which stage tests it satisfies."
  - "Reviewing a skill step that a driver re-enters by reading which stages the files already show done."
related_components: [plan-state, plan-record, delegate]
tags: [skills, drive, state-machine, wait-condition, shell-quoting]
---

`drive` picks its next stage from the files alone: it runs "the first that
applies and whose 'done when' the files do not already show" and then waits
on that stage's done test (`.agents/skills/drive/SKILL.md:52`, `:91-97`).
That design has two silent failure modes. Both shipped with the move of plan
state into `*-plan.state.json` files (2026-10-05, the plan, implement, drive,
next, land, and review skills) and were fixed after it.

## The done test held before the stage ran

The re-plan stage applies to an implemented plan whose review reads
`rework`. Its done test was `is <plan> readiness=implementation-ready`. A
plan sent back by a review keeps the readiness it was implemented with, so
the done test already held on entry. Step 2 skipped re-plan, and a
`wait --until` on it returned at once. `replan` is what changes the state:

```python
state.update(status="planned", review=None, review_rounds=0, compound=None, outcome=None, landed=None)
...
state["readiness"] = "needs-decisions" if args.needs_decisions else "implementation-ready"
```

(`.agents/skills/plan/scripts/plan_record.py:521-523`). The fix makes the
done test name the field the stage's worker changes, `status!=implemented`,
beside the readiness (`.agents/skills/drive/SKILL.md:61`, commit `a1ce89ad`).

## The condition broke inside its own quotes

`drive` passes every done test as `wait <slug> --until '<test>'`, and
`orca-worker.sh` runs it with `bash -c "$until_cmd"`
(`.agents/skills/delegate/scripts/orca-worker.sh:414`). The review stage's
test was `show <plan> --json | grep -q '"review": "accept'`. Its single
quotes close the wrapper's, so the shell split it and `orca-worker.sh`
refused the stray word. The fix uses `is` calls with double quotes only
(`.agents/skills/drive/SKILL.md:96`, commit `c4893467`).

## The rule

For each stage row, take every state its "applies when" accepts and check
that its "done when" fails there. Name a field the stage's worker writes,
not one that may already hold. Within a row the two tests then exclude each
other, so the added "applies" clause in step 2 never skips work.

Run each wait condition once, verbatim, inside the wrapper that will poll it:

```bash
bash -c '.claude/skills/plan/scripts/plan_record.py is docs/plans/x-plan.md status!=implemented && .claude/skills/plan/scripts/plan_record.py is docs/plans/x-plan.md readiness=implementation-ready'; echo $?
```

Run it once on a plan where the stage applies, where it must exit 1, and
once after the worker's command, where it must exit 0. A condition that
holds a single quote fails before it reaches `plan_record.py`.

## Evidence

- The states the table must cover are those `stage` can report:
  `.agents/skills/drive/scripts/plan-state.py:36-59`.
- `sent_back` reads an implemented plan with `rework` as owed a re-plan, with
  readiness untouched: `.agents/skills/plan/scripts/plan_record.py:314-319`.
- The old review condition under `bash -c '<test>'` failed with
  `unmatched '`. The new one exits 0 on a plan whose review is
  `accept after fixes` and 1 on one whose review is `fixes needed`.

## What it does not cover

It does not check that a worker which ends in `supersede` or `abandon` is
routed correctly. `drive`'s implement stage applies on `status!=implemented`,
which those states also satisfy. Whether a re-plan may run either command is
unchecked.
