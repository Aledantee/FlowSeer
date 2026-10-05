# Re-plan a phase

Load this before re-planning a phase plan whose state has a non-null `parent`.
Read it with `.claude/skills/plan/scripts/plan_record.py show <phase>`.

Re-planning a phase starts from a tree that holds the phases before it. Both
tests must pass:

1. For each phase in the parent's `after` state, read its landed range with
   `.claude/skills/plan/scripts/plan_record.py show <prerequisite> --json` and
   pass its `last` commit to `git merge-base --is-ancestor <sha> HEAD`. A
   worktree forked before the previous phase merged fails this, and a
   re-plan from it re-derives that phase as new units.
2. On `main`, use `.claude/skills/plan/scripts/plan_record.py show <parent>
   --json` to confirm this phase is still in `phases`, not `retired`. A phase
   already retired on `main` fails this, and a re-plan from it lands the phase
   twice.

When either fails, do not plan: say which test failed and ask the user
whether to re-fork from a tree that holds the previous phase (recommended)
or stop here.
