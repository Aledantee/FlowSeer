# Fix and re-review rounds

The coordinating session runs the rounds. No skill runs them on its own, and
`implement` covers a plan's units, not a review's findings.

Contents:

- Which findings block: the classes, and what each one gets
- The follow-up pass
- One round: dispatch, merge, the briefed reviewer, the unbriefed reviewer
  for a security fix
- When to stop: a clean round, a Requirement question, a repeated
  mechanism, the two-round cap

## Which findings block

Sort the settled findings (`SKILL.md` step 4) before dispatching anything:

| Class | What it is | Holds the verdict | What it gets |
| --- | --- | --- | --- |
| security | a behavior finding that names all six parts `security.md` asks for | yes | a round with both reviewers |
| behavior | shipped code does the wrong thing for some input, or contradicts a numbered Requirement | yes | a round with the briefed reviewer |
| false test, blocking | a test fails, errors, or passes only on some runs, or the test of a fix this review made passes with that fix removed | yes | a fix, closed by the coordinator's rerun |
| false test, other | a test of the reviewed change passes with the condition removed that its title, comment, or commit body states | no | a follow-up |
| gap | a mutation survives in a branch or boundary no test's title, comment, or commit body states | no | a follow-up |
| convention | repository rule broken in code, or a wrong comment or doc | no | a follow-up |
| hardening | a security concern missing one of the six parts | no | a follow-up |

Security and behavior findings hold the verdict and get a reviewed round. A
blocking false test also holds the verdict, but gets no reviewer round. A fix
to shipped code is reviewed because fix workers get fixes wrong often enough
to matter: the projector fix covered tenant ids only for an object no record
can exist under (`bcdf1b9e`). A security fix gets the wider review because a
wrong one costs the most to ship.

A false test never gets a reviewer round. The coordinator's rerun of the
recorded mutation already proves whether the corrected test fails, and one
phase ended its third round with no behavior finding and two false tests
still holding the verdict (`aff8a9fd`). A false test blocks in two cases.
The suite has to be green on every run, so a failing, erroring, or flaky
test blocks. A fix this review made has to be held by its test, so that
test blocks until the mutation fails it.

A follow-up never holds the verdict. Reviewing follow-up fixes does not
converge: each fix adds tests, and every new test is something the next
reviewer can mutate.

The boundary between a false test and a gap is what the test states. A
broadly named test (`TestValidate`) with a surviving edge-case mutation is a
false test when the mutation removes the specific condition its title,
comment, or commit body states (for example, a test titled
`TestRejectsEmptyID` that passes with the empty-id check removed). It is a
gap when it removes a branch or boundary none of them states (for example,
a surviving off-by-one in a branch no test states). A surviving mutation
that can be read either way is a false test.

A convention finding is a repository rule broken in code (for example, a
helper with one caller), or a wrong comment or doc (for example, a README
that names a deleted flag).

The coordinator collects each follow-up and each blocking false test as it is
settled, from the initial review and from every round, in its report and
record: `path:line`, class, the surviving mutation or wrong text, and the
case that would fail on it. A blocking false-test item also records its
mutation, `runs: <n>` when it is flaky, and `review-fix-test: yes|no`. A gap
whose only test would restate the implementation (a buffer capacity, a log
string) is dropped with that reason in the report and is not recorded.

Follow-ups and blocking false tests are written to the same record when the
verdict is recorded, as `SKILL.md` step 5 describes. The pass closes items
only from the record, so an item kept in the report alone is never rerun, and
a session that ends mid-pass would lose it. A planless `gaps:` line carries
every open item, earlier ones included, because the last `gaps:` line wins.

## The follow-up pass

The pass runs once per review, in `SKILL.md` step 6: after the last clean
round, or at once when the review holds no security or behavior finding that
holds the verdict. A review holding only blocking false tests is included.
Step 6 runs on the user's answer, or under a stage brief that includes it,
as `drive`'s does. The initial review stays report-only.

Dispatch one fix worker per file group and run no review after. The pass
fixes every blocking false test still open and every recorded follow-up.
Each fix's commit body quotes the mutation and its `--- FAIL` line
(`implement`, step 2.3). Merge as step 2 of a round describes, then run the
verifier on the changed paths.

The coordinator closes each item from the record: it reruns the recorded
mutation on the merged tree and deletes the item only when the suite fails.
A flaky test closes when the run count its item names passes on the
unmutated tree and fails on the mutated one. A convention item closes when
the corrected lines stand at its `path:line`. The coordinator closes each
item from its own record instead of trusting worker quotes, because
checking only mutations a worker quoted accepts an item the worker skipped
or weakened.

The pass changes tests, comments, and docs only. A fix worker that needs a
change to other source reports the item as a blocker, and the coordinator
does not merge a pass branch whose diff holds such a change. A follow-up that
needs other source stays a follow-up. A blocking false test that needs other
source stays blocking and the verdict stays `fixes needed`. When the test is
right and the shipped code is wrong, the item is a behavior finding. It gets
a round while the scope has one left, and otherwise the verdict is `rework`.

What the pass leaves decides the verdict this way:

| Left open after the pass | Verdict |
| --- | --- |
| an open Requirement question | `fixes needed`, taking precedence over the other rows and using the Requirement-question option in `SKILL.md` |
| nothing | an accept |
| follow-ups only | an accept, with the items still recorded and listed in the report |
| a blocking false test | `fixes needed`, with a question offering one more pass or stopping |

For example, the initial review finds one behavior defect and three gaps.
Round one is clean. The pass closes two gaps, and the third needs a helper
changed. The verdict is `accept after fixes`, and the report lists the
third gap as a follow-up.

A session that ends mid-pass leaves the earlier verdict on disk, and the
remedy is `review` again.

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
3. Dispatch one reviewer over the round's diff, the range the fix merges
   added, not the branch diff. Brief it as `SKILL.md` step 3 describes, with
   the findings the round fixed, so it judges each fix against its finding
   instead of rediscovering it. When the previous round's remedy was an
   executable property (step 4), that artifact is the brief's primary
   subject: is its enumeration complete against the source it claims to
   read, does each case fail for the rule it names, and is each exemption an
   argument no input can violate? A wrong invariant is worse than none,
   since the next reader trusts it and stops looking. The brief asks for a
   required "New findings" section, written as `none` when empty, for
   defects the round's diff introduced. A new finding in code the round did
   not change is reported under "pre-existing" and does not make the round
   unclean, since the initial review already judged that code. A finding the
   round was briefed to fix that still holds is not pre-existing, wherever its
   code is, and makes the round unclean.
4. When the round fixed a security finding, dispatch one more reviewer over
   the round's changed paths with the brief of a first review (`SKILL.md`
   step 3) and none of the earlier findings. A reviewer handed the findings
   judges the fixes against them and anchors there, so a defect a fix
   introduced elsewhere goes unseen. Settle its findings with the briefed
   reviewer's under `SKILL.md` step 4. A round without a security fix runs
   no second reviewer: the wide second pass raised recall a little and
   returned mostly follow-ups, and a benchmark comparing a single-shot review
   agent with an iterative review agent on the same model measured recall at
   27.0% and 32.8%, respectively, while its signal-to-noise ratio fell from
   5.11 to 1.95 (https://arxiv.org/html/2603.11078v1).

After the round settles, write the round count beside the verdict
(`SKILL.md` step 5).

## When to stop

- A round in which no reviewer returns a security or behavior finding that
  holds the verdict is clean, and a clean round ends the loop. For example,
  if a round returns two gaps and one false test on a fix's own test, the loop
  ends and the follow-up pass starts. Write `accept after fixes` once no
  security, behavior, or blocking false-test finding is open and no
  Requirement question is open, directly after the clean round or at the end
  of the pass. List what remains in the final report and end with the
  `accept` row's question when the verdict reads an accept. No earlier round
  writes `accept after fixes`, since the gates read it as passing.
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
  Apply step 4's class rule, and read how an established implementation
  solves that mechanism: the pinned library's own primitive, a vendored
  spec, or a `docs/solutions/` entry, cited as `AGENTS.md`, Investigation
  discipline, defines a source. The report names that source, and states
  two things, since rounds that patch a mechanism never ask whether the
  work needs it:
  - Requirement served: the plan Requirement the mechanism exists for,
    quoted.
  - Simplest design: the least code that meets that Requirement, whatever
    the plan's Decision chose.

  When the simplest design differs from the one being patched (it drops a
  gate, a coupling, or the mechanism), the difference is a plan question. It
  goes under the plan's Open questions as a Requirement question does. For
  example, a device index resolves a syslog sender only for devices the
  management lane onboarded. The Requirement is to resolve a sender to a
  listed device, and the listing already carries every address. The
  simplest design drops the gate, so the question is whether anything needs
  it.
- A scope gets at most two rounds, whatever each round fixed, and the count
  carries across reviews. `SKILL.md` step 5 records it, and a review of a
  scope with a recorded count continues from it. A parked review that
  resumes therefore has the rounds its earlier run left, since a count that
  started fresh on every resume let one plan park a second time after two
  more rounds (`430d1ccc`). The count
  starts at zero again only when the plan changed since the recorded
  verdict through `plan`, or gained a Decision ending `decided by the user`
  that replaces the mechanism the rounds were fixing. The review that
  starts after such a change records the count as `0`.

  A review that starts with a count of two and has a security or behavior
  finding is `rework` immediately. The rework options apply, and no option
  offers another round. A review at count two that holds only blocking false
  tests is `fixes needed` and may run the follow-up pass, since that pass is
  not a round.

  A second round that is not clean ends the loop with the verdict `rework`.
  Agents maintaining a codebase over successive iterations showed regressions
  becoming more frequent with iteration count in 12 of 20 models in one
  benchmark (https://arxiv.org/html/2603.03823v4), and a fourth round on one
  mechanism still found two behavior defects in that round's own change
  (`18c7187e`):
  - Both rounds on one mechanism: take the work to `plan` with what the
    rounds established.
  - Rounds on different mechanisms: ask the user (`AGENTS.md`, Agent
    behavior) whether to take the work to `plan` (recommended, since each
    round has surfaced a new defect), to drop or replace the mechanism the
    last round fixed, naming its simplest design as above, or to stop.
  - A security finding open: the question names it first, with its six
    parts, and offers the same options.

  No answer buys a third round on the same plan text. A delegated worker
  does not ask: it sets the verdict to `rework` and states the round count
  as its blocker. Under `drive`, that report names the limit, and `drive`
  step 4 parks it with the same options.

  Example: round one reworks an emission mechanism, and round two fixes its
  staging permissions and finds a rollback defect. That is two rounds, the
  verdict is `rework`, and the question offers `plan` first.
- Every verdict the loop writes is recorded as `SKILL.md` step 5 records a
  verdict, which also updates the Orca card. The commit that records the
  verdict names the number of fix rounds.

Before the final report, stop each fix worker's lane: `orca worktree list
--json` lists no worktree whose `parentWorktreeId` is this one, other than
those the report names with the reason they stayed. A lane left behind
blocks the coordinator's `stop` of this worktree.
