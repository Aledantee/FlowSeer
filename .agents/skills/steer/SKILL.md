---
name: steer
description: Works the queue in docs/agent-observations.md. Verifies each entry against the current skill, agent, or hook, applies the fix to skills and agents, and stages hook or AGENTS.md changes for a person's review. Also audits hook enforcement and retires landed plans `land` left on disk. Use when asked to steer, tune skills, work the observations, or audit the hooks. Not for logging an observation; compound's Observe mode does that.
argument-hint: "[audit | entry title | the skill to tune]"
---

# Steer the FlowSeer skills, agents, and hooks

Run only when a person asks. An observation about `steer` is worked here
like any other. The steps apply the change process in
`docs/agent-steering.md` to one entry at a time.

## 1. Read the queue

Read every entry under "Entries" in `docs/agent-observations.md`, and the
skills, agents, and hooks they name. With an argument, work only the matching
entry or the entries naming that skill; with `audit`, go to step 5. An empty
queue with no argument ends the skill at step 5.

## 2. Verify each entry against the tree

For each entry, read its whole body, not only the title, then open the named
file at the named step and decide one of:

| Verdict | Meaning |
| --- | --- |
| holds | the step still reads as the entry describes and the gap is real |
| already fixed | the file changed since and covers it; cite the lines |
| task context | it would not recur for another task using the same skill |
| needs the user | the fix is a design choice the entry does not settle |

Work two entries naming the same step as one. "task context" and "already
fixed" are rejections: delete the entry in step 6 with the reason in the
report. Ask "needs the user" at once (`AGENTS.md`, Agent behavior), with the
design options the entry leaves open and a recommendation; the answer moves
the entry to "holds". Only an entry the user declines to decide stays in the
queue, with the question appended.

## 3. Choose the surface

For an entry that holds, place the fix where `docs/agent-steering.md`,
"Steering surfaces" puts it:

- The step was skipped or misread: reword or reorder the step in the skill
  or agent. Add one example when the wording could be read two ways. Fit
  the wording to what went wrong:

  | What went wrong | Form of the fix |
  | --- | --- |
  | The output had the wrong shape: a buried verdict, a restated brief | State what the output is, its parts in order |
  | A required element was left out of something the step already produces | A named slot in the template or report list |
  | The behavior should depend on a condition | A conditional on something the reader has already observed |
  | The rule was known and skipped | A plain prohibition with its reason |

  An exception is its own conditional, never a clause appended to the rule.
- The step was followed as written and still failed, or the same rule was
  violated more than once: the fix is enforcement, a hook under
  `tools/hooks/` or a check in the verifier, and the prose only names it.
- The step never fired: remove it.
- A new skill candidate: write the skill only when the sequence has recurred
  and has several conditional steps; otherwise add a step to an existing
  skill. Its `description` says when it applies and when to skip it, and it
  ends by running the verifier and by pointing corrections to `compound`,
  Observe, like the others. A script it ships is made executable with
  `chmod +x <path>`, sandbox disabled, since the sandbox denies writes
  under the skills directory and the file otherwise lands as mode 100644.
- A rule every task needs: `AGENTS.md`, staged, not applied.

Before editing, grep `AGENTS.md`, `docs/agent-steering.md`,
`docs/agent-knowledge.md`, `.claude/skills/`, and `.claude/agents/` for the
same rule. State it once and link from the other places.

## 4. Apply or stage

Skills, agents, `docs/agent-steering.md`, and `docs/agent-observations.md`:
edit in place. Keep a skill body under about 150 lines; past that, move
episodic material to a `references/` file whose pointer states when to load
it. Before saving, run every command embedded in the edited text once,
verbatim, from a fresh shell in the scratchpad directory.

The policy surfaces are the paths `AGENTS.md`, Hard boundaries, names. For
one of them, write the change, including the matching assertion in
`tools/hooks/tests/run.sh` and the registration in both runtime configs, run
the verifier, then stop with the diff for the user's guardrail review. Do
not commit it and do not mark the entry applied. A hook change without the
Codex registration is incomplete even when Claude is the only runtime in use.

After every applied change run the verifier on the changed paths, then
dispatch `independent-reviewer` once, as `delegate` describes, with the diff,
the entries it answers, and the question "does the edited procedure still
read as a complete, followable step, and does it contradict `AGENTS.md`,
`docs/agent-steering.md`, or another skill?". Apply the findings that hold.

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- <changed paths>
```

## 5. Audit enforcement and plan retirement

Run at the end of every pass and on `audit`:

1. For each rule under "Enforced rules" in `AGENTS.md`, name the hook script
   or verifier check that enforces it. A rule with none is a finding.
2. For each script in `tools/hooks/`, name its registration in
   `.claude/settings.json` and in `.codex/hooks.json`, and the assertion in
   `tools/hooks/tests/run.sh` that pins it. A script registered in one
   runtime only is a finding unless the other runtime has no such event.
3. Run `tools/hooks/tests/run.sh` and `shellcheck` over the hook scripts.
4. Read the phase size from each plan's state with
   `uv run tools/scripts/run.py plan record show <plan> --json`. The
   `outcome` object carries the unit count and verification span. For plans
   that `land` retired, read the same outcome line in the retire commit. Look
   for a phase that ran past one session, or a run of phases with one or two
   units. Change the number in `plan` and the reason in
   `docs/agent-steering.md` together, or record that the data does not yet
   say.

   `land` retires a plan when its work lands and copies the outcome into the
   retire commit, so read both places:

   ```bash
   { for plan in docs/plans/*-plan.md; do uv run tools/scripts/run.py plan record show "$plan" --json; done; git log main --format=%b --grep='^docs(plans): retire' | grep '^> Implemented\. [0-9]* units'; }
   ```
5. Sweep the plans `land` should have retired. Every `retire` line is a
   plan finished on `main` and still on disk:

   ```bash
   uv run tools/scripts/run.py next plan-queue | grep '^retire'
   ```

   Retire each as `land/references/retire-plan.md` describes, one commit
   per plan. More than a handful is delegated as `delegate` describes, one
   family per brief (a parent with its phases, or plans whose decisions
   land in the same record), so two workers do not amend one record; the
   record drafts and amendments are reviewed here before a branch merges. Then log an observation against
   `land` naming the plans it missed.

Report findings; fix them through "Apply or stage", which stages them.

## 6. Close the entries and report

Delete every applied and rejected entry from `docs/agent-observations.md` in
the same change. When `docs/agent-steering.md` explains why a changed skill
is shaped as it is, update that paragraph in the same change.

Report, outcome first: entries applied, rejected with the reason, left for
the user with the question, staged for guardrail review with the diff path;
then the audit findings and the commands run with their results.

End by asking the user (`AGENTS.md`, Agent behavior) whether to commit the
applied edits now or leave them uncommitted. A staged policy-surface diff is
never part of that question or that commit: it stays in the tree for the
guardrail review, named in the report, and an answer given here does not
stand in for that review. A correction to this procedure is logged as
`compound`, Observe describes.
