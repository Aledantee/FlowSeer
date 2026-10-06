---
title: A New Required Key in the Plan State File Breaks the Hand-Written Fixture in the Hook Tests, Which No Skill Suite Runs
date: 2026-10-06
category: conventions
module: .agents/skills/plan
problem_type: convention
component: workflow-skills
severity: medium
applies_when:
  - "Adding, renaming, or removing a key in `BLANK` in `.agents/skills/plan/scripts/plan_record.py`, or tightening `shape_faults`."
  - "Planning a unit that changes the shape of `*-plan.state.json` and listing the files it touches."
  - "Reading a verifier failure in `tools/hooks/tests/run.sh` after a change that touched only skill scripts."
related_components: [plan-record, verify-change, hooks]
tags: [skills, plan-state, fixtures, policy-surface, verifier]
---

`plan_record.py` treats every key of `BLANK` as required. A state file
without one fails `check`, and so does every read:

```python
faults += [f"missing key {key!r}" for key in BLANK if key not in state]
```

(`.agents/skills/plan/scripts/plan_record.py:169`). The skill suites build
their state files with `plan_record.py init`, so a new key reaches their
fixtures for free and all of them stay green.

One fixture is written by hand. The hook tests need states that no
`plan_record.py` command produces, so they spell the whole object in `jq`:

```bash
phase_state() {
  jq -n --argjson fields "$2" '{contract: "flowseer-plan-state/v1", status: "planned",
    readiness: "implementation-ready", review: null, review_rounds: 0, compound: null, outcome: null,
    superseded_by: null, branch: null, parent: null, after: [], landed: null, phases: [], retired: []} + $fields' \
```

(`tools/hooks/tests/run.sh:1092-1095`). The plan status check under test
reads those files through `plan_record.py`
(`.agents/skills/verify-change/scripts/check-plan-status.py:13`,
`tools/hooks/tests/run.sh:1015`). When the `branch` key was added, the
fixture lacked it, the check reported `missing key 'branch'` in place of the
phase fault the case asserts, and the suite stopped at its first phase
assertion.

Three things hid it until the verifier ran:

- The four unit-test suites under `.agents/skills/*/scripts/` passed, and
  `plan_record.py check` passed over every state file on disk.
- Only the verifier runs the hook tests
  (`.agents/skills/verify-change/scripts/verify-change.sh:953-954`), and a
  worker does not run the verifier.
- `tools/hooks/` is a policy surface (`AGENTS.md`, Hard boundaries), so the
  one-line repair needed the user's approval in the middle of the work
  (commit `50226b6f`).

## The rule

A unit that changes the state file's shape names
`tools/hooks/tests/run.sh` in its `Files:` line, and the plan says the edit
is to a policy surface, so the approval is asked for before the work starts.
Find every hand-written copy of the shape with a key every state file holds:

```bash
grep -rln 'superseded_by' --exclude-dir=.git --exclude='*-plan.state.json' .
```

On 2026-10-06 the only fixture it lists is `tools/hooks/tests/run.sh`. The
other hits are `plan_record.py`, its test, and prose. Run the hook tests before
handing the unit over when the coordinator has approval to touch them:

```bash
tools/hooks/tests/run.sh
```

## What this does not cover

A state file on a branch written before the key existed fails the same
check after it merges `main`. The plan that added `branch` accepted that
cost, and the `missing key` message names the repair.
