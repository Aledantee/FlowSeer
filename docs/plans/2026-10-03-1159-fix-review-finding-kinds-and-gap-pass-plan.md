---
title: Review Finding Kinds and the Gap Pass - Plan
type: fix
date: 2026-10-03
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: fixes needed
execution: docs
---

# Review Finding Kinds and the Gap Pass - Plan

> Implemented. 3 units, 2026-10-03T10:25Z to 2026-10-03T10:25Z.

## Goal

`review` stops running reviewed fix rounds on test gaps, and still never
writes an accept while a defect, a false test, or a gap it found is open.
The means is a table of finding kinds in
`.agents/skills/review/references/fix-loop.md`, one unreviewed gap pass
before the accept verdict, and a record of open items that only `review`
reads. This plan is wrong if a gate outside `review` (`land`, `drive`,
`next`, plan retirement) turns out to need that record to refuse work, since
the design rests on the verdict being the only signal those gates read.

The branch already holds a draft of this text in the four files the units
name (commit `6d1b8cfb`). Each unit edits that draft. Its review ended at
`rework` after three fix rounds, and the Decisions below settle what those
rounds left open.

## Decisions

Kinds:

- A finding has one of four kinds: behavior, false test, gap, convention.
  Why: the loop used to stop only at a round with no correctness finding,
  and a test gap counted as one. Each fix round added tests for the next
  reviewer to mutate, so two phases reached the three-round cap on test
  coverage alone (`d7995d3d`, `fb724477`).
- Behavior is shipped code that gives a wrong result for some input or
  contradicts a numbered Requirement. Why: it is the kind a re-review exists
  to catch.
- A false test gets a reviewed round, the same as a behavior defect. A false
  test fails, errors, passes only on some runs, or passes with the condition
  removed that its title, comment, or commit body states. When a surviving
  mutation can be read as a false test or as a gap, it is a false test.
  Why: a false test makes the suite report a guarantee it does not hold
  (decided by the user, 2026-10-02).
- A gap is a mutation that survives in a branch or boundary no test's
  title, comment, or commit body states. It does not hold the loop open. A
  gap whose only test would restate the implementation is dropped with that
  reason in the report and is not recorded. Why: Google surfaces surviving
  mutants in code review as findings an author need not resolve, and reports
  that tests written for unproductive mutants are brittle
  (https://arxiv.org/abs/2102.11378).
- A convention finding is a repository rule broken in code, or a wrong
  comment or doc. It closes when the corrected lines stand at its
  `path:line`. Why: it has no mutation, so the gap's closing test cannot
  apply, and before this change such findings never held the loop open
  either.
- The table's column is headed "Kind", and the word "class" stays reserved
  for the mechanism rule of `.agents/skills/review/SKILL.md` step 4. Why:
  `fix-loop.md:58` already says "the class `SKILL.md` step 4 named", and two
  meanings of one word in one file misroute a brief.

Where items close:

- Gaps close per phase, inside the review that found them, before the
  accept verdict. Why: cleanup deferred past the change that exposed it
  tends not to happen
  (https://google.github.io/eng-practices/review/reviewer/pushback.html),
  and a gap carried across skills had to be known by `land`, `drive`,
  `next`, and plan retirement, where each fix round found another reader it
  had missed (`c65f3804`, `e83b1305`) (decided by the user, 2026-10-03).
- The initial review stays report-only. The gap pass runs in step 6, on the
  user's answer to fix and review again, or under a stage brief that
  includes step 6, as `drive`'s does (`.agents/skills/drive/SKILL.md:60`).
  Why: step 5 says the review itself changes nothing.
- The other two answers reach an accept the same way. On "apply the fixes
  here" and "apply chosen findings only", the coordinator closes each
  recorded item by the gap pass's rule before it writes
  `accept after fixes`, and an item still recorded leaves `fixes needed`.
  Why: `SKILL.md:220-224` writes an accept on those paths, and an accept
  beside a recorded item is the state this plan exists to prevent.
- The coordinator closes a gap from its own record: it reruns the recorded
  mutation on the merged tree and deletes the item only when the suite
  fails. Why: checking the mutations a fix worker quoted accepts a gap the
  worker skipped or weakened.
- A review runs at most three rounds in total. A gap pass whose diff
  changes source outside tests, comments, and docs is a round and counts as
  one, and items that round finds are recorded and not passed again in this
  review. Why: the old wording, three rounds without a clean one, meant the
  same total while a clean round always ended the loop, and a pass that
  follows a clean round needs the count stated.
- One review runs one gap pass. An item that survives it ends the review at
  `fixes needed` with a question offering one more gap pass or stopping.
  Why: a gap that needs source already becomes a round, so no third option
  is needed, and `drive` can carry out both answers by running the review
  stage again or leaving the plan parked.

The record:

- Open gap and convention findings are recorded on disk, and only `review`
  reads the record. With a plan it is a `## Review gaps` section at the end
  of the plan. Planless work records a `gaps:` line in the checkpoints file
  (decided by the user, 2026-10-03). Why: a review that ends mid-pass
  otherwise loses the list, and a fresh reviewer may not rediscover a known
  gap.
- `review` step 5 writes the record in the same commit as the verdict, and
  only where step 5 records a verdict. A commit or path review of other
  work, and a subject review, write none. Why: `SKILL.md:207-208` and
  `references/subject-review.md:74-76` record nothing for such scopes, and a
  `gaps:` line written for other work would be read as this branch's.
- A plan item is one line, `- <path:line>: <mutation or the wrong text>;
  fails: <the case that would fail>`. The section is deleted with its last
  item. Why: step 1 needs a shape it can brief from, and an empty heading
  reads as an unfinished write.
- The planless line is written with
  `.claude/skills/verify-change/scripts/ledger.py checkpoint gaps "<items>"`,
  never with `--replace`, items separated by ` | `. The last `gaps:` line
  wins, and `gaps: none` is written when the last item closes. Why:
  `ledger.py:238-242` appends, `--replace` rewrites the whole file and would
  drop the `implemented:` and `review:` lines, and `land` already reads the
  last line for a key (`.agents/skills/land/SKILL.md:43-44`).
- No gate reads the record. While it holds an item the verdict is not an
  accept, which `land`, `plan-state.py`, and `plan-queue.py` already refuse
  (`.agents/skills/drive/scripts/plan-state.py:28`,
  `.agents/skills/next/scripts/plan-queue.py:46`). Why: the verdict is the
  one field every gate reads, and `review` is the record's only writer.
- Every verdict the loop writes is recorded as `SKILL.md` step 5 records a
  verdict. Why: that step also updates the Orca card, and `land` stops when
  card and plan disagree (`.agents/skills/land/references/orca-card.md:8-10`).

Requirement questions:

- A finding that needs a plan Requirement changed blocks an accept. The
  verdict is `fixes needed`, and a delegated reviewer states the finding as
  its blocker (decided by the user, 2026-10-03). Why: correctness comes
  before throughput, and `drive` parks only a verdict that is not an accept.
- `review` writes the question under the plan's Open questions in the
  verdict commit, naming the Requirement. Its report's question offers
  taking the Requirement to `plan`, or stopping. The finding is settled once
  the Requirement is amended, or a Decision ending `decided by the user`
  says how to read it. The next review then judges the Requirement by that
  text and removes the Open-questions item. Why: `drive` writes a parked
  answer into the plan's Decisions
  (`.agents/skills/drive/references/parking.md:34-38`), so that is where a
  later review finds it, and a worker may not restate a Requirement itself.

- The four files spell the kind "behavior". Why: both skill files already
  use that spelling outside the draft.

## Requirements

1. `fix-loop.md` sorts findings by a table headed Kind with the rows
   behavior, false test, gap, and convention. Example: a test titled
   `TestRejectsEmptyID` that passes with the empty-id check removed is a
   false test. A surviving off-by-one in a branch no test states is a gap.
   A README that names a deleted flag, or a helper with one caller, is a
   convention finding.
2. A round ends the loop when neither reviewer returns a behavior or
   false-test finding. Example: a round's reviewers return two gaps and one
   convention finding, and the loop ends and the gap pass starts.
3. Every gap not dropped and every convention finding is recorded when
   step 5 records the verdict. Example: after the initial review of a
   plan's branch, the plan ends with `## Review gaps` and one `- ` item per
   finding. A path review of another branch's files writes no record.
4. `review` step 1 reads the record. Example: a fresh session reviewing a
   plan whose section lists two items briefs its reviewers with those two
   as the previous round's findings.
5. The verdict is not an accept while the record holds an item. Example: an
   initial review that finds only one gap records `fixes needed`.
6. `accept after fixes` is written only when the record is empty, after the
   coordinator reran every recorded mutation and read every corrected line.
   Example: the pass worker fixes two of three gaps, the third item stays,
   and the verdict stays `fixes needed`, on "apply the fixes here" too.
7. A gap pass that changes source outside tests, comments, and docs runs
   steps 3 and 4 as a round that counts toward the three. Example: round
   one is not clean, round two is clean, the pass changes a helper and is
   round three, and a behavior finding there ends the loop at the cap.
8. An item that survives the gap pass ends the review with a question
   offering one more gap pass or stopping. A delegated reviewer states each
   surviving item's `path:line` and mutation as its blocker. Example: a
   plan without phases under `drive` parks at `fixes needed`, and the next
   review stage reads the item from the section.
9. A finding that needs a Requirement changed leaves the verdict
   `fixes needed` and an item under the plan's Open questions. Example: a
   reviewer marks Requirement 9 `undecidable` and nothing else is open. The
   verdict is `fixes needed`. After a Decision ending
   `decided by the user` says how to read it, the next review judges by
   that Decision and removes the item.
10. `docs/agent-steering.md` states the kinds, where gaps close, and why,
    citing `d7995d3d` and `fb724477` for the cap reached on coverage alone.
    Example: `git show -s fb724477` reads "Two low findings stay open, both
    test coverage".

## Out of scope

- No change to `land`, `drive`, `next`, `compound`, `plan`, or any script.
  A script that refused an accept beside a listed item was built
  (`18cebbc9`) and removed (`6d1b8cfb`), because each gate that read the
  record needed the next one to read it too.
- The record travels only with the plan file or the checkpoints file. Two
  paths lose it together with the verdict, and the next review there starts
  fresh: a phase of a parent that `drive` parks without merging the stage
  worker's branch (`.agents/skills/drive/references/parking.md:16-21`), and
  a planless review run in a `land` worker's own git directory
  (`.agents/skills/land/references/missing-checkpoint.md:20-24`). Both
  predate this plan and lose behavior findings the same way. The `compound`
  stage logs them as an observation for `steer`.
- No tool that classifies findings. The coordinator sorts them from the
  table.
- The record is written by the reviewing coordinator, which is trusted.
  Nothing parses it.

## Units

### U1. Finding kinds, the gap pass, and the stop rules in the fix loop

Files: `.agents/skills/review/references/fix-loop.md`
After: none
Change: "Which findings get a round" holds the Kind table with four rows,
the definition of each kind, the boundary rule, and the dropped-gap
sentence. "The gap pass" says it runs in step 6 before the accept verdict,
that the coordinator closes each item from the record step 5 wrote, how a
convention item closes, that a pass which changes source is a round, and
that a surviving item ends the review with the two-option question. "When
to stop" ends the loop on no behavior or false-test finding, caps a review
at three rounds in total, keeps a Requirement-change finding at
`fixes needed`, and writes every verdict as `SKILL.md` step 5 records one.
The sentence saying a gap is not written to a plan section is gone.
Tests: nothing in this unit is executable. `review` traces Requirements 1,
2, 5, 6, 7, and 8 through the text, one example at a time.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .agents/skills/review/references/fix-loop.md`

### U2. The record, verdicts, and the question table in the review skill

Files: `.agents/skills/review/SKILL.md`
After: none
Change: Step 1 reads the record (the plan's `## Review gaps` section, or
the last `gaps:` line for planless work) and carries its items into the
reviewer briefs as the previous round's findings. It also reads a Decision
ending `decided by the user` that settles a Requirement question. Step 3
says a test that passes against the defect it names is a false test and a
surviving mutation no test states is a gap. Step 5 holds the item format,
writes the record and any Requirement question in the verdict commit where
it records a verdict, gives `fixes needed` for an open gap, convention
finding, or Requirement-change finding, and keeps the reviewer's residual
testing note apart from the gap list. Its question table adds a row for an
item that survived the gap pass and a row for a Requirement question. The
"apply the fixes" paragraph closes recorded items by the gap pass's rule
before the accept. Step 6 says clean means no behavior defect and no false
test, and that the gap pass follows before the accept verdict.
Tests: nothing in this unit is executable. `review` traces Requirements 3,
4, 5, 6, 8, and 9 through the text.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .agents/skills/review/SKILL.md`

### U3. The mutation reference and the steering record

Files: `.agents/skills/review/references/mutation-check.md`,
`docs/agent-steering.md`
After: none
Change: `mutation-check.md` ends with the false-test and gap sentences and
points to `fix-loop.md` for the boundary. The loop paragraphs of
`docs/agent-steering.md` name the four kinds, say gaps close in one pass
inside the review before the accept verdict, say the record is read only by
`review` and why no gate reads it, and cite `d7995d3d` and `fb724477` for
the cap reached on coverage alone. The citation of `fdd1823c` is gone,
since that commit records two open correctness items.
Tests: `git show -s d7995d3d fb724477 c65f3804 e83b1305` prints four
commits whose messages say what the sentences citing them claim.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .agents/skills/review/references/mutation-check.md docs/agent-steering.md`

Waves: U1 U2 U3

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh --base main
git diff --name-only main...HEAD
grep -n -E "behaviour|of any class|neither class|before any verdict|not written to a plan section|gaps open|review-gaps" \
  .agents/skills/review/SKILL.md .agents/skills/review/references/fix-loop.md \
  .agents/skills/review/references/mutation-check.md docs/agent-steering.md
```

The second command lists this plan and the four files the units name. The
third prints nothing.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `python3 .claude/skills/prose/scripts/check-prose.py` on the four
      files the units name prints no warning on text the units wrote.
- [ ] Every Requirement's example traced through the final text by the
      reviewer.
- [ ] This plan's `status` set, with an outcome note under its title.
- [ ] No plan labels in the skill files.

## Open questions

- Requirement 10 names `d7995d3d` for the cap reached on coverage alone.
  Its message lists "One defect no test catches" and "Two cases weaker than
  their titles" open at round three, so the citation at
  `docs/agent-steering.md:784` claims more than the commit says. Cite
  `fb724477` alone, or name another commit?
- Requirement 7 and the Decision capping a review at three rounds do not
  say what happens when a gap pass changes source after three rounds have
  already run, the third one clean, or what "one more gap pass" starts: a
  new review with a fresh count, or a second pass in this one
  (`.agents/skills/review/references/fix-loop.md:72`, `:79`).

## Review gaps

- `docs/agent-steering.md:778`: "and the Orca card" is in neither `c65f3804` nor `e83b1305`; fails: a reader checking the two cited commits for that reader of the gap list
- `.agents/skills/review/SKILL.md:277`: "with no further review round" contradicts `references/fix-loop.md:72`, where a pass that changes source is a round; fails: a pass that edits a helper is written up as an accept without steps 3 and 4
