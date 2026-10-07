# Units in workers

Load this before the first wave of two or more units, and for any plan for
which `.claude/skills/plan/scripts/plan_record.py is <plan> parent!=null`
succeeds. A plan whose state has no parent and whose units chain
one after another, with no wave wider than one, does not need it.

A wave is every unit whose `After` prerequisites have landed. Dispatch the
wave's units to workers at once, one unit each, up to the cap `delegate`'s
Wave size computes from the pool rows read just before the wave, or up to
the budget a `drive` brief names. Dispatch goes through Orca when its
runtime is reachable, which puts each unit on the first model in the
role's `fit` order whose pool has room, whatever its CLI, else where its
"Orca or native" section says; when that is this session, the units run
one at a time here and the rest of this file does not apply. A wave wider
than the cap runs in rounds, the cap recomputed before each. A phase plan,
identified with `.claude/skills/plan/scripts/plan_record.py is <phase> parent!=null`, runs even a wave of
one in a worker, so a fresh context per unit keeps the
coordinator's own context to the ledger. A plain plan runs a wave of one
here.

Run `ledger.py set <unit> in_progress` when the unit is dispatched, before
its branch is merged: `passed` counts the commits after the `HEAD` that
call recorded, and a unit marked after its merge has none.

The brief carries the plan path, the unit's text, the conventions for its
files, the focused test command, and the ledger notes of landed units. It
asks for the per-test mutation lines step 2.3 of the skill puts in the
commit body. When a worker's commit lacks one for a new test, run that
mutation here before the merge, since one mutation costs less than another
worker round. Workers do not run the verifier. Two units in one wave never
share a file, by the plan's own rule. When a wave's units would, the plan's
`After` lines are wrong and get fixed before dispatch.

Merge in the order the workers settle. After each report, check the worker's
tree before reading its report as fact, as `delegate` describes. Verify that
the commit it names exists, its tree is clean, and the two or three changes
most expensive to get wrong are what the report says. Run
`.claude/skills/delegate/scripts/orca-worker.sh check <slug>` before the
merge. A non-zero result stops that unit's merge. Merge the worker's branch
here. A branch that conflicts with one already merged goes back to its
worker: run `git merge --abort`, `tell` the worker to rebase on the merged
tree, then check and merge it again once it settles. The worker knows why
each of its lines changed, and a conflict resolved here mixes its change
with the coordinator's guesses. After the merge commit exists, run
`python3 .claude/skills/land/scripts/merge-check.py ORIG_HEAD..HEAD`.
A non-zero result stops the wave. Carry every `missing` block in the report,
then run the focused tests of the merged packages, and
release the worker and remove its worktree as `delegate` describes. A
merged unit stays `in_progress` in the ledger: `passed` needs a
`verified_at`, and only the verifier writes one. Once the wave has
settled, run the verifier once on the union of the wave's changed paths,
then write every unit of the wave `passed` with that run's `verified_at`
and the merge commit. `ledger.py` recomputes `resume` for the next wave on
that write.
