# Re-plan a phase

Load this before re-planning a phase plan (one with a `parent:` field).

Re-planning a phase starts from a tree that holds the phases before it. Both
tests must pass:

1. For each phase the parent's `After:` names, the last commit of its
   `Landed:` line passes `git merge-base --is-ancestor <sha> HEAD`. A
   worktree forked before the previous phase merged fails this, and a
   re-plan from it re-derives that phase as new units.
2. The parent on `main` shows this phase's own `Landed:` still empty. A
   phase already landed on `main` fails this, and a re-plan from it lands
   the phase twice.

When either fails, do not plan: say which test failed and ask the user
whether to re-fork from a tree that holds the previous phase (recommended)
or stop here.
