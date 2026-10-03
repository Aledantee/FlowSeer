# Fix and re-review rounds

The coordinating session runs the rounds. No skill runs them on its own, and
`implement` covers a plan's units, not a review's findings.

## Which findings get a round

Sort the settled findings (`SKILL.md` step 4) before dispatching anything:

| Kind | What it is | What it gets |
| --- | --- | --- |
| behavior | shipped code does the wrong thing for some input, or contradicts a numbered Requirement | a round: steps 1 to 4 |
| false test | a test fails, errors, or passes only on some runs, or passes with the condition removed that its title, comment, or commit body states | a round: steps 1 to 4 |
| gap | a mutation survives in a branch or boundary no test's title, comment, or commit body states | collected, closed in the gap pass |
| convention | repository rule broken in code, or a wrong comment or doc | collected, closed in the gap pass |

A false test is as serious as a defect, because the suite reports a
guarantee it does not hold and the next reader trusts it. A gap claims
nothing. Reviewing gap fixes does not converge: each fix adds tests, and
every new test is something the next reviewer can mutate.

The boundary is what the test states. A broadly named test (`TestValidate`)
with a surviving edge-case mutation is a false test when the mutation removes
the specific condition its title, comment, or commit body states (for example,
a test titled `TestRejectsEmptyID` that passes with the empty-id check
removed). It is a gap when it removes a branch or boundary none of them states
(for example, a surviving off-by-one in a branch no test states). When a
surviving mutation can be read as a false test or as a gap, it is a false
test, since that side is reviewed.

A convention finding is a repository rule broken in code (for example, a helper
with one caller), or a wrong comment or doc (for example, a README that names
a deleted flag).

The coordinator collects each gap and convention finding as it is settled, from
the initial review and from every round, in its report: `path:line`, the
surviving mutation or wrong text, and the case that would fail on it. A gap
whose only test would restate the implementation (a buffer capacity, a log
string) is dropped with that reason in the report and is not recorded.

A gap or convention finding a round returns is written to the record, with
`fixes needed`, when that round settles and before the pass starts, as
`SKILL.md` step 5 records a verdict. The pass closes items only from the
record, so an item kept in the report alone is never rerun, and a session that
ends mid-pass would lose it. A planless `gaps:` line carries every open item,
earlier ones included, because the last `gaps:` line wins. For example, the
initial review finds one behavior defect and no gap, and round one is clean
and returns two gaps. The plan then holds `## Review gaps` with two items and
`review: fixes needed`, and the pass reruns both mutations.

### The gap pass

The pass runs in `SKILL.md` step 6, before the accept verdict: after the last
clean round, or at once when the review holds gaps or convention findings and
nothing that needs a round. Step 6 runs on the user's answer to fix and review
again, or under a stage brief that includes it, as `drive`'s does. The initial
review stays report-only. The other two answers ("apply the fixes here" and
"apply chosen findings only") reach the accept through `SKILL.md` step 5,
which says what the coordinator closes first and what keeps `fixes needed`.

Run steps 1 and 2 of a round with one fix worker per file group and no review
after. Each fix's commit body quotes the gap's mutation and its `--- FAIL`
line (`implement`, step 2.3). The coordinator closes each item from the record
`SKILL.md` step 5 wrote (plan section `## Review gaps`, or the last `gaps:`
checkpoint line for planless work): it reruns the recorded mutation on the
merged tree and deletes the item only when the suite fails. A convention item
closes when the corrected lines stand at its `path:line`. The coordinator
closes each gap from its own record instead of trusting worker quotes, because
checking only mutations a worker quoted accepts a gap the worker skipped or
weakened.

The verdict is not an accept while the record holds an item. For example, an
initial review that finds only one gap records `fixes needed`.

`accept after fixes` is written only when the record is empty, after the
coordinator reran every recorded mutation and read every corrected line. For
example, if the pass worker fixes two of three gaps and the third item stays,
the verdict stays `fixes needed`.

A pass whose diff changes source outside tests, comments, and docs is a round,
counts toward the three rounds in total, and runs steps 3 and 4. Items that
round finds are recorded and not passed again in this review. For example, if
round one is not clean, round two is clean, and the pass changes a helper,
that pass is round three, and a behavior finding there ends the loop at the
cap.

Once the review has run three rounds, the pass changes no source outside
tests, comments, and docs. A fix worker that needs such a change reports the
item as a blocker, and the item stays recorded. The coordinator does not merge
a pass branch whose diff holds such a change, because the worker's duty alone
does not say what happens when the worker ignores it. For example, rounds one
and two are not clean, round three is clean, and the one recorded gap needs a
helper changed. The worker reports it, the item stays, and the verdict is
`fixes needed` with the question below.

One review runs one gap pass. Once the loop has ended (When to stop), every
item still recorded after the pass ends the review at `fixes needed` with a
question offering one more gap pass or stopping, whether the item survived
the pass or a later round found it. Until then a pass that became a round
keeps the loop open like any other round, and its items stay recorded. A
gap that needs source becomes a round, or after three rounds waits for the
next review, so no third option exists. When the loop ended at the
three-round limit with a behavior or false-test finding open, that limit's
outcome is the review's and the items stay recorded. When a Requirement question is also
open, the question offers taking the Requirement to `plan`, one more gap pass,
or stopping. One more gap pass is a new review: `review` runs again from
step 1, reads the record, and has a fresh round count and one pass, and step 6
runs in it. A delegated reviewer does not ask: it states each item's
`path:line` and its mutation or wrong text as its blocker. For example, a plan
without phases under `drive` parks at `fixes needed`, and the next review
stage reads the item from the plan's `## Review gaps` section.

A session that ends mid-pass leaves `fixes needed` on disk, which every
gate refuses, so the remedy is `review` again.

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
   bound the fix. A fix worker that needs a file outside these categories
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

- A round in which neither reviewer returns a behavior or false-test
  finding ends the loop. For example, if a round returns two gaps and one
  convention finding, the loop ends and the gap pass starts. After a clean
  round, run the gap pass in `SKILL.md` step 6 only when the record holds an
  item and this review has not run one. Write `accept after fixes` only with
  an empty record and no Requirement question open, directly after the clean
  round or at the end of the pass. List what remains in the final report and
  end with the `accept` row's question when the verdict reads an accept, or
  with the question the gap pass or the Requirement bullet below describes
  when it does not. No earlier round writes `accept after fixes`, since the
  gates read it as passing.
- A finding that needs a plan Requirement changed blocks an accept and keeps
  the verdict `fixes needed`. It goes under the plan's Open questions in the
  verdict commit, naming the Requirement. The report's question offers taking
  the Requirement to `plan`, or stopping. A delegated reviewer does not ask:
  it states the finding as its blocker. The finding is settled once the
  Requirement is amended, or a Decision ending `decided by the user` says how
  to read it. The next review then judges the Requirement by that text and
  removes the Open-questions item.
- The coordinator holds the rounds' history, so it is the one that sees a
  round find a defect in the previous round's fix for the same mechanism.
  Apply step 4's class rule before the next round, and read how an
  established implementation solves that mechanism before another patch is
  briefed: the pinned library's own primitive, a vendored spec, or a
  `docs/solutions/` entry, cited as `AGENTS.md`, Investigation discipline,
  defines a source. The next brief names that source, or the work goes to
  `plan` because none was found.
- A review runs at most three rounds in total, whatever each round fixed,
  and a gap pass that became a round counts as one. A third round that is
  not clean ends the loop:
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
- Every verdict the loop writes is recorded as `SKILL.md` step 5 records a
  verdict, which also updates the Orca card. The commit that records the
  verdict names the number of fix rounds.

Before the final report, stop each fix worker's lane: `orca worktree list
--json` lists no worktree whose `parentWorktreeId` is this one, other than
those the report names with the reason they stayed. A lane left behind
blocks the coordinator's `stop` of this worktree.
