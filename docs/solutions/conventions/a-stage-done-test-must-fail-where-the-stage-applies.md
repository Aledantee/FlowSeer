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
  - "Writing a condition a skill hands to `delegate orca-worker wait --until '<json-argv>'` or to any other wrapper that quotes and re-runs it."
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

(`tools/scripts/skills/plan/record.py`, `cmd_replan`). The fix makes the
done test name the field the stage's worker changes, `status!=implemented`,
beside the readiness (`.agents/skills/drive/SKILL.md:61`, commit `a1ce89ad`).

## The condition broke inside its own quotes

The shell wrapper passed every done test as `wait <slug> --until '<test>'`, and
`orca-worker.sh` ran it with `bash -c "$until_cmd"`
(commit `c4893467`). The review stage's
test was `show <plan> --json | grep -q '"review": "accept'`. Its single
quotes close the wrapper's, so the shell split it and `orca-worker.sh`
refused the stray word. The fix uses `is` calls with double quotes only
(commit `c4893467`).

## The rule

For each stage row, take every state its "applies when" accepts and check
that its "done when" fails there. Name a field the stage's worker writes,
not one that may already hold. Within a row the two tests then exclude each
other, so the added "applies" clause in step 2 never skips work.

Run each wait condition verbatim inside the wrapper that polls it:

```bash
uv run tools/scripts/run.py delegate orca-worker wait lane --until '["uv","run","tools/scripts/run.py","drive","stage-done","docs/plans/x-plan.md","re-plan"]'
```

The JSON array carries literal arguments, so the predicate needs no shell
expression. Run it on a plan where the stage applies, where `stage-done`
returns 1 and a quiet lane prints `idle`. Run it after the stage changes the
state, where the predicate returns 0 and the lane prints `done`.

## Evidence

- `tools/scripts/skills/drive/plan_state.py`, `stage`, names the states the
  stage table must cover.
- `tools/scripts/lib/plans.py`, `sent_back`, reads an implemented plan
  with `rework` as owed a re-plan, with readiness untouched.
- `tools/scripts/skills/drive/stage_done.py` reads the followed state and
  requires both a non-implemented status and ready state for re-plan.
- `tools/scripts/tests/skills/drive/test_stage_done.py` executes all four
  documented wait predicates on pending and done states.
- The old review condition under `bash -c '<test>'` failed with
  `unmatched '`. The corrected shell condition exited 0 on a plan whose
  review was `accept after fixes` and 1 for `fixes needed` (`c4893467`).

## What it does not cover

It does not check that a worker which ends in `supersede` or `abandon` is
routed correctly. `drive`'s implement stage applies on `status!=implemented`,
which those states also satisfy. Whether a re-plan may run either command is
unchecked.
