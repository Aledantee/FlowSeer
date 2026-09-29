# Reviewing a subject

A subject has no diff. Nothing is new, so a problem that predates today is a
finding: the standing code is what is in doubt. Take the specification from
the `docs/architecture/` record, the convention doc, the package README, or
the user's sentence. Where the subject has none, say so in the report rather
than inventing one.

## Step 1: units and seams

Resolve the subject to a file set, with `Explore` when it names a concept
instead of a directory. Then decide whether the set is one unit or several.
A unit is a body of code one reader holds at once: a package with its tests
and README, or one side of a contract. Split when the subject spans packages
a single reader would end up skimming, when it crosses a generated boundary,
or when it runs past roughly 1,500 lines. Name the units and the seams
between them in the report, since a wrong decomposition is the likeliest way
this scope misses something.

## Step 3: several units

Review each unit alone before anything looks at the whole, since a reader who
starts from the interplay talks himself out of a local bug by assigning it to
somebody else's contract.

Dispatch one `independent-reviewer` per unit, in parallel within the limit
`delegate` sets, each with its own file list, its own specification, and only
the conventions that unit needs. Resolve each reviewer's lane through
`delegate`'s step 3 before it starts: a reviewer on the executor's vendor is
not independent, and that includes a subagent of this session when this
session runs on that vendor. Give a unit reviewer the names of its
neighbours and the contract it is meant to keep, and tell it to judge its own
files: something it suspects about a neighbour comes back as a question, not
a finding.

Then review the interplay from the unit reports and the code at the seams.
Ask what no unit reviewer could see:

- Do both sides of a contract mean the same thing by it, including the cases
  each treats as impossible?
- Does a mirrored artifact still match its origin: the Config/State/Event
  triad, each LocalRef/GlobalRef pair, the conventions doc against the
  `.proto`?
- Does an invariant one unit maintains hold where another relies on it, or
  only where it is written?
- Is one concept modelled twice under different rules, and which copy is the
  code of record?
- Does a boundary a `docs/architecture/` record fixes still hold? If it
  moved, decide whether the record or the code is wrong.
- Do an error, a timeout, and a cancellation cross the seam with their
  meaning intact?

Dispatch a second reviewer for the interplay (logged as `review-seam`) only
when the seam files alone exceed one reader, briefed with the unit reports as
input. Otherwise the interplay is the coordinator's own reading, with the
unit reviewers' unanswered questions as its agenda. The coordinator's own
reading in `SKILL.md` step 3 still runs.

## Step 4: what counts

Confirm each failure is real in the current tree. Drop the "introduced by
this change" test and keep no "pre-existing" section, since age is not what
disqualifies a finding here.

## Step 5: report and verdict

Report the units and seams first, so the reader can see what was covered,
then the interplay findings, then the unit findings under their unit. The
verdict judges the subject: sound, sound with fixes, or unsound.

Record the verdict nowhere: not in the plan, not through `ledger.py`, and
never in the worktree comment, since `land` reads a `review:` entry there as
a verdict on the branch and a subject review has not looked at the branch.

Findings too large to fix in place go to `plan` with what this review
established, not into a fix attempt at the end of an audit.

Read the options table by findings, with sound with fixes and unsound in
place of the branch verdicts: offer the fixes or `plan`, never `compound` or
`land`. A sound subject with nothing to fix ends without a question.
