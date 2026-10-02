# Fix and re-review rounds

The coordinating session runs the rounds; no skill runs them on its own, and
`implement` covers a plan's units, not a review's findings.

## Which findings get a round

Sort the settled findings (`SKILL.md` step 4) before dispatching anything:

| Class | What it is | What it gets |
| --- | --- | --- |
| behaviour | shipped code does the wrong thing for some input, or contradicts a numbered Requirement | a round: steps 1 to 4 |
| false test | a test fails, errors, or passes only on some runs, or its title, comment, or commit body names a behaviour and it passes with that behaviour removed | a round: steps 1 to 4 |
| gap | a mutation survives in a branch or boundary no test's title, comment, or commit body states, or a comment or doc is wrong | recorded now, closed in the gap pass below |

A false test is as serious as a defect, because the suite reports a
guarantee it does not hold and the next reader trusts it. A gap claims
nothing. Reviewing gap fixes does not converge: each fix adds tests, and
every new test is something the next reviewer can mutate.

The boundary is what the test states. A broadly named test (`TestValidate`)
with a surviving edge-case mutation is a false test when the mutation removes
the specific condition its title, comment, or commit body states, and a gap
when it removes a branch or boundary none of them states. When both readings
hold, it is a false test, since that side is reviewed.

Record each gap when it is settled, as one line under a `## Review gaps`
heading at the end of the plan: `path:line`, the surviving mutation, and the
case that would fail on it. A gap whose only test would restate the
implementation (a buffer capacity, a log string) is dropped with that reason
in the report, not recorded. Planless work has no plan section, so it lists
its gaps in the report and records the verdict `fixes needed` while one is
open, since `land` reads that verdict and not the report. The gap pass
replaces it with `accept after fixes`.

### The gap pass

Once the loop has ended, or straight away when the initial review holds
gaps and nothing that needs a round, close the recorded gaps in one pass:
steps 1 and 2, no review after. Each fix's commit body quotes the gap's
mutation and its `--- FAIL` line (`implement`, step 2.3). The coordinator
reruns each quoted mutation on the merged tree and deletes the gap's line
only when the suite fails on it. A pass whose diff changes source outside
tests, comments, and docs is a round after all, and steps 3 and 4 run on
it. `land` refuses a plan that still lists a gap (`land`, step 1).

## One round

1. Dispatch the fixes as `delegate` describes, one worker per file group.
   Brief each with its findings' `path:line`, failure scenario, and smallest
   fix, and with the class `SKILL.md` step 4 named: the mechanism, and the
   instruction to find and fix every other site that engages it and report
   the sites it cleared. A fix worker may edit its findings' files, the file
   in the owning layer where the fix belongs (a helper a test needs goes in
   that layer, not in the test), every file the fix leaves stale (a Taskfile
   description, a README), and the files of the other sites it finds, each
   listed in its report. It never edits a plan Decision marked
   `decided by the user` (`delegate`, Write the brief, item 6). The review's changed paths do not
   bound the fix. A fix worker that needs a file outside these classes
   reports a blocker naming the file and reason. A comment, skipped or
   weakened test, or partial change is not a fix. When that blocker returns,
   the coordinator extends the allowed file list and dispatches the worker
   again.
   The coordinating session does not make the fixes itself.
2. Before each merge, run
   `.claude/skills/delegate/scripts/orca-worker.sh check <slug>`. A non-zero
   result stops the round. Merge each worker's branch. After the merge commit
   exists, including a resolved conflict, run
   `python3 .claude/skills/land/scripts/merge-check.py ORIG_HEAD..HEAD`.
   A non-zero result also stops the round. Carry every `missing` block in the
   report, then run the verifier once on the union of the changed paths,
   before anything is reviewed again.
3. Repeat `SKILL.md` steps 3 and 4 over the branch diff, briefing the
   reviewer with the previous round's findings and the changed paths, so it
   judges each fix against its finding instead of rediscovering it. When the
   previous round's remedy was an executable property (step 4), that
   artifact is the brief's primary subject: is its enumeration complete
   against the source it claims to read, does each case fail for the rule it
   names, and is each exemption an argument no input can violate? A wrong
   invariant is worse than none, since the next reader trusts it and stops
   looking. The brief asks for a required "New findings" section, written
   as `none` when empty, for defects no earlier finding names.
4. In the same round, dispatch one more reviewer over the round's changed
   paths with the brief of a first review (`SKILL.md` step 3) and none of
   the earlier findings. A reviewer handed the findings judges the fixes
   against them and anchors there, so a defect a fix introduced elsewhere
   goes unseen. Settle its findings with the briefed reviewer's under
   `SKILL.md` step 4.

## When to stop

- A round in which neither reviewer returns a behaviour or false-test
  finding ends the loop. Run the gap pass, list what remains in the final
  report, replace the recorded `fixes needed` or `rework` with
  `accept after fixes` as `SKILL.md` step 5 records a verdict, and end with
  the `accept` row's question. No earlier round writes `accept after fixes`,
  since the gates read it as passing.
- A finding that needs a Requirement changed is neither class. It goes in
  the final report as a decision for the plan's owner and does not hold the
  loop open.
- The coordinator holds the rounds' history, so it is the one that sees a
  round find a defect in the previous round's fix for the same mechanism.
  Apply step 4's class rule before the next round, and read how an
  established implementation solves that mechanism before another patch is
  briefed: the pinned library's own primitive, a vendored spec, or a
  `docs/solutions/` entry, cited as `AGENTS.md`, Investigation discipline,
  defines a source. The next brief names that source, or the work goes to
  `plan` because none was found.
- Count every round, whatever it fixed. Three rounds without a clean one
  end the loop:
  - All three on one mechanism: take the work to `plan` with what the
    rounds established.
  - Rounds on different mechanisms: ask the user (`AGENTS.md`, Agent
    behavior) whether to take the work to `plan` (recommended, since each
    round has surfaced a new defect) or to run one more round. A fourth
    round runs only on that answer. A delegated worker does not ask: it
    sets the verdict to `rework` and states the round count as its blocker.
    Under `drive`, that report names the limit, and `drive` step 4 parks it
    with another round as an option.

  Example: rounds one and two rework an emission mechanism, and round three
  fixes its staging permissions and finds a rollback defect. That is three
  rounds, and the fourth is the user's call.
- The commit that records the verdict names the number of fix rounds.

Before the final report, stop each fix worker's lane: `orca worktree list
--json` lists no worktree whose `parentWorktreeId` is this one, other than
those the report names with the reason they stayed. A lane left behind
blocks the coordinator's `stop` of this worktree.
