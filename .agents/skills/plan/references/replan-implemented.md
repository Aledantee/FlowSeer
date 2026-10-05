# Re-plan an implemented plan

Load this before re-planning a plan that reads `status: implemented`. A
review that ended in `rework` sent it back, and the plan now describes open
work again. A plan left `implemented` reads as finished to `next`, `drive`,
and `land`.

Make these changes in the edit that sets `artifact_readiness:
implementation-ready`:

1. Set `status: planned` and delete the `review` and `compound` fields.
2. Delete the `> Implemented.` line under the title. It counts the units of
   the first run, and `next` reads it as the plan's outcome. `implement`
   writes a new one when the re-planned units land.
3. List open work only under Units. A unit that stands as built leaves the
   list, and its id leaves the `After:` lines and the `Waves:` line of the
   units that remain. `implement` starts every listed unit as `pending`,
   and `ledger.py` refuses `passed` while nothing is committed since the
   unit went `in_progress`.
4. When `.claude/skills/verify-change/scripts/ledger.py show` prints a
   ledger naming this plan, delete it. Its `passed` units would make
   `implement` skip a redesigned unit that kept its id, and `implement`
   starts a fresh ledger when none exists:

   ```bash
   find "$(git rev-parse --git-dir)" -maxdepth 1 -name flowseer-plan-status.json -delete
   ```

Example: a plan with `Waves: U1 | U2 U3 | U4` whose U1 and U2 stand as
built keeps U3 and U4. U3's `After: U1` becomes `After: none`, and the
line reads `Waves: U3 | U4`.
