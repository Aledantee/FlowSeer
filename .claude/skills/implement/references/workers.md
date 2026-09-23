# Units in workers

Load this before the first wave of two or more units, and for any plan
with a `parent:` field. A plan whose units chain one after another, with
no wave wider than one, does not need it.

A wave is every unit whose `After` prerequisites have landed. Dispatch the
wave's units to workers at once, one unit each, up to the cap `delegate`'s
Wave size computes from the pool rows read just before the wave, or up to
the budget a `drive` brief names. Dispatch goes through Orca when its
runtime is reachable, which puts each
unit on the pool with the most headroom whatever its CLI, else where its
"Orca or native" section says; when that is this session, the units run
one at a time here and the rest of this file does not apply. A wave wider
than the cap runs in rounds, the cap recomputed before each. A phase plan runs even a wave of one in a worker, so
a fresh context per unit keeps the coordinator's own context to the
ledger; a plain plan runs a wave of one here.

The brief carries the plan path, the unit's text, the conventions for its
files, the focused test command, and the ledger notes of landed units. It
asks for the per-test mutation lines step 2.3 of the skill puts in the
commit body; when a worker's commit lacks one for a new test, run that
mutation here before the merge, since no lane re-prompts a settled worker.
Workers do not run the verifier. Two units in one wave never share a
file, by the plan's own rule; when a wave's units would, the plan's
`After` lines are wrong and get fixed before dispatch.

Merge in the order the workers settle. After each report, check the
worker's tree before reading its report as fact, as `delegate` describes:
the commit it names exists, its tree is clean, and the two or three
changes most expensive to get wrong are what the report says. Merge the
worker's branch here, run the focused tests of the merged packages, and
release the worker and remove its worktree as `delegate` describes. A
merged unit stays `in_progress` in the ledger: `passed` needs a
`verified_at`, and only the verifier writes one. Once the wave has
settled, run the verifier once on the union of the wave's changed paths,
then write every unit of the wave `passed` with that run's `verified_at`
and the merge commit, and write `resume` for the next wave. A unit whose
branch conflicts with one already merged is re-based by its worker on the
merged tree, not resolved here.
