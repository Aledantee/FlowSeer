# Re-plan a phase

Load this before re-planning a phase plan whose state has a non-null `parent`.
Read it with `.claude/skills/plan/scripts/plan_record.py show <phase>`.

Re-planning a phase starts from a tree that holds the phases before it. Both
tests must pass:

1. For each phase in this phase's `after` list, take the `last` commit of its
   landed range and pass it to `git merge-base --is-ancestor <sha> HEAD`. A
   prerequisite still on disk holds the range in its own state
   (`.claude/skills/plan/scripts/plan_record.py show <prerequisite> --json`).
   A retired one has no state file, and its range is in the entry the
   parent's `retired` list keeps for it
   (`.claude/skills/plan/scripts/plan_record.py show <parent> --json`). A
   worktree forked before the previous phase merged fails this, and a
   re-plan from it re-derives that phase as new units.
2. `main` shows this phase neither landed nor retired. `show` reads the work
   tree, so read `main` with git: `git show main:<phase state path>` prints
   `"landed": null`, and `git show main:<parent state path>` names this
   phase under `phases` and not under `retired`. A phase already landed or
   retired on `main` fails this, and a re-plan from it lands the phase
   twice.

`check-plan-status.py` applies both tests on every verifier run once a ledger
names the phase.

When either fails, do not plan: say which test failed and ask the user
whether to re-fork from a tree that holds the previous phase (recommended)
or stop here.
