# Re-plan an implemented plan

Load this before re-planning a plan whose state reads `implemented` and whose
review sent it back. Reset the state with:

```bash
uv run tools/scripts/run.py plan record replan <plan> [--needs-decisions]
```

The command sets the plan to `planned`, clears the previous review, review
rounds, compound outcome, implementation outcome, and landed range, and
resets readiness. It deletes this worktree's ledger when the ledger names the
plan. It leaves a ledger for another plan untouched.

Run it once the re-planned Units are written, with `--needs-decisions` when
the re-plan stops on a decision, and commit the state with the plan. Run
before the Units change, it leaves a plan that reads `planned` and ready
above the Units of the first run, and `implement` would build them again.

List only the open work under Units. A built unit leaves the list, and its id
leaves the `After:` lines of the units that remain. Then
rewrite the `Waves:` line from the remaining `After:` lines. Removing ids from
the old line keeps a wave boundary the graph no longer has. `implement` starts
each listed unit as `pending`, and `verify ledger` refuses `passed` while nothing is
committed since the unit went `in_progress`.

The command preserves the phase's parent relationship. Use
`uv run tools/scripts/run.py plan record after <phase> <prerequisite>...`
when the re-plan changes a prerequisite.
