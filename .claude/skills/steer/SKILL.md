---
name: steer
description: Work the queue in docs/agent-observations.md by verifying each entry against the current skill, agent, or hook, deciding whether the fix is prose, a skill step, or enforcement, applying it to skills and agents, and staging hook or AGENTS.md changes for a person's review. Also audits whether every enforced rule has a registered hook or verifier check. Use when asked to steer, tune skills, work the observations, or audit the hooks. Not for logging an observation; compound's Observe mode does that.
argument-hint: "[audit | entry title | the skill to tune]"
---

# Steer the FlowSeer skills, agents, and hooks

`compound` logs where a procedure did not fit; this skill decides what to do
about it. It runs when a person asks, never on its own, and it edits the same
files it is subject to: an observation about `steer` is worked here like any
other. The change process in `docs/agent-steering.md` is the procedure; the
steps below are that process applied to one entry at a time.

## 1. Read the queue

Read every entry under "Entries" in `docs/agent-observations.md`, and the
skills, agents, and hooks they name. With an argument, work only the matching
entry or the entries naming that skill; with `audit`, go to step 5. An empty
queue with no argument ends the skill at step 5.

## 2. Verify each entry against the tree

For each entry, open the named file at the named step and decide one of:

| Verdict | Meaning |
| --- | --- |
| holds | the step still reads as the entry describes and the gap is real |
| already fixed | the file changed since and covers it; cite the lines |
| task context | it would not recur for another task using the same skill |
| needs the user | the fix is a design choice the entry does not settle |

Read the entry's whole body before deciding; the title compresses away the
failure. Two entries naming the same step are worked as one. "task context"
and "already fixed" are rejections: delete the entry in step 6 with the
reason in the report. "needs the user" stays in the queue with one question
appended to it.

## 3. Choose the surface

For an entry that holds, place the fix where `docs/agent-steering.md`,
"Steering surfaces" puts it:

- The step was skipped or misread: reword or reorder the step in the skill
  or agent. Add one example when the wording could be read two ways.
- The step was followed as written and still failed, or the same rule was
  violated more than once: the fix is enforcement, a hook under
  `tools/hooks/` or a check in the verifier, and the prose only names it.
  Rewording a rule that was read and broken is the wrong fix.
- The step never fired: remove it. A skill that restates what the model
  does by default costs context and gains nothing.
- A new skill candidate: write the skill only when the sequence has recurred
  and has several conditional steps; otherwise a step in an existing skill.
  Its `description` says when it applies and when to skip it, and it ends by
  running the verifier and by pointing corrections to `compound`, Observe,
  like the others.
- A rule every task needs: `AGENTS.md`, staged, not applied.

Before editing, grep `AGENTS.md`, `docs/agent-steering.md`,
`docs/agent-knowledge.md`, `.claude/skills/`, and `.claude/agents/` for the
same rule. State it once and link from the other places.

## 4. Apply or stage

Skills, agents, `docs/agent-steering.md`, and `docs/agent-observations.md`:
edit in place. Keep a skill body under about 150 lines; past that, move
episodic material to a `references/` file whose pointer states when to load
it. Run every command embedded in the edited text once, verbatim, from a
fresh shell in the scratchpad directory, before saving; an embedded command
that was never run is the line most likely to be wrong.

`tools/hooks/`, `.claude/settings.json`, `.codex/hooks.json`, and `AGENTS.md`
are policy surfaces: write the change, including the matching assertion in
`tools/hooks/tests/run.sh` and the registration in both runtime configs, run
the verifier, then stop with the diff for the user's guardrail review. Do
not commit it and do not mark the entry applied. A hook change that skips
the Codex config is incomplete even when the Claude one is the only runtime
in use.

After every applied change run the verifier on the changed paths, then
dispatch `independent-reviewer` once, as `delegate` describes, with the diff,
the entries it answers, and the question "does the edited procedure still
read as a complete, followable step, and does it contradict `AGENTS.md`,
`docs/agent-steering.md`, or another skill?". Apply the findings that hold.

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

## 5. Audit enforcement

Run this at the end of every pass and on `audit`:

1. For each rule under "Enforced rules" in `AGENTS.md`, name the hook script
   or verifier check that enforces it. A rule with none is a finding.
2. For each script in `tools/hooks/`, name its registration in
   `.claude/settings.json` and in `.codex/hooks.json`, and the assertion in
   `tools/hooks/tests/run.sh` that pins it. A script registered in one
   runtime only is a finding unless the other runtime has no such event.
3. Run `tools/hooks/tests/run.sh` and `shellcheck` over the hook scripts.

Report findings; fix them through step 4, which stages them.

## 6. Close the entries and report

Delete every applied and rejected entry from `docs/agent-observations.md` in
the same change. When `docs/agent-steering.md` explains why a skill is shaped
as it is, update that paragraph in the same change rather than leaving the
reason stale.

Report, outcome first: entries applied, rejected with the reason, left for
the user with the question, staged for guardrail review with the diff path;
then the audit findings and the commands run with their results. A
correction to this procedure is logged as `compound`, Observe describes.
