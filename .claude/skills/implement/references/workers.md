# Units in workers

Load this when the plan carries a `parent:` field (a phase plan) or the user
asked for units to run in parallel; sequential work in the main
conversation does not need it.

A phase plan runs each unit in a worker, one at a time, unless the user asks
for the main conversation: a fresh context per unit keeps the coordinator's
own context to the ledger. Units marked `After: none`, or whose
prerequisites have landed, may run at once when the user asked for it: up
to three workers, one unit each. Both are dispatched as `delegate`
describes. The brief carries the plan path, the unit's text, the
conventions for its files, the focused test command, and the ledger notes
of landed units. Workers do not run the verifier.

After each report, check the worker's tree before reading its report as
fact, as `delegate` describes: the commit it names exists, its tree is
clean, and the two or three changes most expensive to get wrong are what
the report says. Then merge the worker's branch here, run the verifier on
the union of changed paths, write the ledger, and release the worker and
remove its worktree as `delegate` describes, before the next wave. With
concurrent workers, `resume` is written after the wave settles.
