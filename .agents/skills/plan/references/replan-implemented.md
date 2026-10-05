# Re-plan an implemented plan

Load this before re-planning a plan that reads `status: implemented`. A
review that ended in `rework` sent it back, and the plan now describes open
work again. The `review: rework` field is the only mark it arrives with.
`next` and `drive` read that field as a re-plan owed. A plan left `implemented` reads as finished to `next`, `drive`,
and `land`.

Make these changes in the commit that records the re-plan, whether it
leaves `artifact_readiness` at `implementation-ready` or `needs-decisions`:

1. Set `status: planned` and delete the `review` and `compound` fields.
2. Delete the `> Implemented.` line under the title. It counts the units of
   the first run, and `next` reads it as the plan's outcome. `implement`
   writes a new one when the re-planned units land.
3. List open work only under Units. A unit that stands as built leaves the
   list, and its id leaves the `After:` lines of the units that remain.
   Then rewrite the `Waves:` line from the `After:` lines that remain.
   Removing ids from the old line keeps a wave boundary the graph no longer
   has. `implement` starts every listed unit as `pending`, and `ledger.py`
   refuses `passed` while nothing is committed since the unit went
   `in_progress`.
4. When `.claude/skills/verify-change/scripts/ledger.py show` prints a
   ledger naming this plan, delete it. Its `passed` units would make
   `implement` skip a redesigned unit that kept its id, and `implement`
   starts a fresh ledger when none exists:

   ```bash
   find "$(git rev-parse --git-dir)" -maxdepth 1 -name flowseer-plan-status.json -delete
   ```
5. When the plan carries a `parent:` field, empty this phase's `Landed:`
   line in the parent in the same commit, and set a parent that reads
   `status: implemented` back to `planned`. The range is the first run's.
   While it stands, the verifier's ledger check passes a phase that runs
   after this one as free to start. `implement` fills the
   line again at Finish.

Example: a plan with `Waves: U1 | U2 U3 | U4` whose U1 and U2 stand as
built keeps U3 and U4. U3's `After: U1` becomes `After: none`, U4 keeps
`After: U3`, and the line reads `Waves: U3 | U4`. Had U4 read `After: U2`,
both would end `After: none` and the line would read `Waves: U3 U4`.
