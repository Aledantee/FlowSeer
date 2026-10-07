# Park a plan, and resume it

Load this when a plan parks (step 4's conditions) or when the user answers a
parked question.

## Park

Append to the plan's Open questions, commit, and run the verifier on the
plan path so the receipt post-dates the commit:

```markdown
- Parked by drive: <question>. Options: <a> (<tradeoff>) | <b>
  (<tradeoff>). Recommended: <a>, because <reason>.
```

Merge what the worker landed before it stopped only when the verifier passes
on it, and never for a phase of a parent: `land` gates every plan this
branch carries past `main`, so a merged partial phase would hold up every
other phase's land. Keep its work on a branch the lane does not own, then
grade and stop the lane without `--keep-worktree`, since `land` and the
successor hand-off both refuse while a lane or child worktree remains.
`<slug>` is the slug of the lane passed to `stop`, the one whose stage
parked. `stop` accepts an unmerged plan branch only when `parked/<slug>`
holds its commits, and it removes the plan's worktree with the state file
of every earlier lane that ran in it:

```bash
git branch parked/<slug> <plan branch>
.claude/skills/delegate/scripts/orca-worker.sh grade <slug> --outcome blocked --verify none
.claude/skills/delegate/scripts/orca-worker.sh stop <slug>
```

The plan's recorded branch is gone with the worktree. Record the parked one
in its place and commit it with the question, so the state readers follow
the parked work and `drive plan-state` stops counting the phase as holding a
land:

```bash
uv run tools/scripts/run.py plan record branch <plan> parked/<slug>
```

Name `parked/<slug>` in the question. A review stage's verdict commit and
its `## Review gaps` record stay on that branch with the fixes they judge. A parked phase and every phase whose
`After:` reaches it leave the round, and independent phases continue. A
plan without phases that parks ends the drive at step 5.

## Answer

Write the user's answer into the plan's Decisions, ending it with
`(decided by the user, <YYYY-MM-DD>)` so workers leave it alone
(`delegate`, Write the brief, item 6), and remove
its `Parked by drive:` line in the same commit. Run the verifier on the plan
path; the plan rejoins the next round. A phase parked with a
`parked/<slug>` branch resumes from it: its next stage worker, whichever
stage parked, starts from that branch merged with this branch's `HEAD`, so
units that passed there are not redone and a review reads the record of
open items the parked review left. Start that worker with
`orca-worker.sh start ... --base parked/<slug>`, and have its brief merge
this branch's `HEAD` before anything else. It is the first lane of a new
worktree, so record the branch its `start` prints in place of the parked
one and commit it, as step 2 does for a first stage:

```bash
uv run tools/scripts/run.py plan record branch <plan> <branch>
```

Whether Orca branches a child
from a branch other than the current one is unverified. When `start`
refuses, report it with the parked branch's name. When the answer restarts the phase
instead, delete the branch with `git branch -D parked/<slug>` and clear the
record with `uv run tools/scripts/run.py plan record branch <plan> --clear`.

A round-limit question never offers another round
(`review/references/fix-loop.md`, When to stop). A review that resumes from
a parked branch continues the round count that branch's plan records, and
the count starts at zero again only when the answer re-planned the work or
replaced the mechanism the rounds were fixing.
