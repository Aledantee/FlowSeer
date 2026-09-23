---
name: review
description: Review a FlowSeer change (working tree, branch, commit, or paths) or a named subject (a package, a mechanism, a contract, a concern crossing packages) for correctness, regressions, missing tests, and violations of the repository conventions, and report verified findings by severity. Use when asked to review code, check a diff, judge a change before commit, or audit standing code; a subject spanning several units is reviewed unit by unit in parallel, then at its seams. Report-only unless the user asks to apply fixes.
argument-hint: "[base ref | commit | paths | subject]"
---

# Review a FlowSeer change

## 1. Resolve the scope

| Request | Scope |
| --- | --- |
| no target given | `git diff HEAD` plus untracked files |
| a base ref or "the branch" | `git diff <base>...HEAD` |
| a commit | `git show <sha>` |
| paths | those files, whole |
| a subject: a package, a mechanism, a contract, a concern that crosses packages | the code that subject reaches, resolved below |

List the changed files. Read the intended behavior from the plan under
`docs/plans/`, the commit message, or the user's words.

A subject has no diff, and the questions change with it. Nothing is new, so a
problem that predates today is a finding rather than a footnote: the reason to
review a subject is that the standing code is in doubt, not a change to it. Take
the specification from the `docs/architecture/` record, the convention doc, the
package README, or the user's sentence. Where the subject has none, say so in
the report rather than inventing one.

Resolve the subject to a file set first, with `Explore` when it names a concept
instead of a directory. Then decide whether that set is one unit or several. A
unit is a body of code one reader holds at once: a package with its tests and
README, or one side of a contract. Split when the subject spans packages a
single reader would end up skimming, when it crosses a generated boundary, or
when it runs past roughly 1,500 lines. Name the units and the seams between them
in the report, because a wrong decomposition is the likeliest way this scope
misses something.

## 2. Load the applicable rules

Read only the conventions the touched files need: `docs/code-style.md` for
Go, `docs/code-style-proto.md` and `docs/conventions/protobuf.md` for schema,
`docs/conventions/observability.md` for telemetry, `docs/doc-style.md` for
prose and comments. Match the "Read when" column of
`docs/solutions/README.md` and read the guidance of matching solutions. When
the area has a `docs/architecture/` record, read its boundaries. When a
convention or solution is itself in the diff, take the rule from `HEAD` and
review the new version as prose.

## 3. Dispatch the reviewer

`independent-reviewer` has no shell. Write the diff to a file in the
scratchpad directory and brief the agent as `delegate` describes: that path,
the file list, the plan path, the intended behavior as a specification, the
convention paths, the matched solutions, and the pinned version of every
external convention or library a finding could cite (`go.mod`, `buf.lock`,
the semantic-convention version `docs/conventions/observability.md` names),
so the reviewer checks rather than recalls. Ask for findings that affect
correctness, the stated requirements, or a repository rule, ordered by
severity, each with path and line, the failure scenario, and the smallest safe
fix, or, when the fix rests on a claim about the code the reviewer did not
open, a direction and the claim left unchecked. When the plan's `status`
is still `planned` because work is mid-flight,
say so in the brief.

One reviewer covers about 1,500 changed lines or one subsystem, its docs
included. Above that, dispatch one reviewer per subsystem with its own diff
file, in parallel. Split by file group, never by persona.

### A subject with several units

Review each unit alone before anything looks at the whole. A reader who starts
from the interplay talks himself out of a local bug by assigning it to somebody
else's contract.

Dispatch one `independent-reviewer` per unit, in parallel within the limit
`delegate` sets, each with its own file list, its own specification, and only
the conventions that unit needs. Resolve each reviewer's lane through
`delegate`'s step 3 before it starts: a reviewer on the executor's vendor is
not independent, and that includes a subagent of this session when this
session runs on that vendor. Give a unit reviewer the names of its
neighbours and the contract it is meant to keep, and tell it to judge its own
files: something it suspects about a neighbour comes back as a question, not a
finding.

Then review the interplay from the unit reports and the code at the seams. This
pass is what the scope exists for, and it asks what no unit reviewer could see:

- Do both sides of a contract mean the same thing by it, including the cases
  each treats as impossible?
- Does a mirrored artifact still match its origin: the Config/State/Event
  triad, each LocalRef/GlobalRef pair, the conventions doc against the `.proto`?
- Does an invariant one unit maintains hold where another relies on it, or only
  where it is written?
- Is one concept modelled twice under different rules, and which copy is the
  code of record?
- Does a boundary a `docs/architecture/` record fixes still hold? If it moved,
  decide whether the record or the code is wrong.
- Do an error, a timeout, and a cancellation cross the seam with their meaning
  intact?

Dispatch a second reviewer for the interplay only when the seam files alone
exceed one reader, briefed with the unit reports as input. Otherwise this is the
coordinator's own reading, and the unit reviewers' unanswered questions are its
agenda.

### The coordinator's own reading

This pass runs for every scope, a plain diff included. While the reviewer
runs, read the hunks that change behavior in full and skim
the rest. Read each hunk's code before the comment above it, decide what the
code does, then compare: a comment stating intent primes a reader to see that
intent in code doing the opposite. Ask:

- Does every behavior change have a test that would fail without it, and
  would that test still fail if the check moved to the wrong place? The
  commit bodies in scope carry a mutation and a quoted `--- FAIL` line per
  new test (`implement`, step 2.3). For a new test without one, run the
  mutation: copy the source file first (`cp <path> "$TMPDIR/<name>.orig"`),
  mutate it in place, run the focused test, quote the result, and restore
  from the copy, never with `git checkout` or `git restore`. A test that
  passes against the defect is a correctness finding.
- Does any comment narrate process, cite history, or carry a plan label?
- For each line the verifier printed under `Test changes to account for:`
  (a deleted or skipped test, a removed test function, a rewritten
  `testdata/` file), does the implementer's reason hold against the diff,
  and does the suite still prove what that test proved?
- Does a README, convention doc, or schema comment now disagree with the code?
  When the change amends a `docs/architecture/` record, check the record's
  premises (what it says exists or is absent) against the tree, not only the
  paths it cites: fresh paths make a reader trust a premise an earlier
  change made false.
- Is anything added that has one caller, one implementation, or no caller?
- Does the change move a boundary an accepted direction record fixes?
- For schema: do the Config/State/Event triad and each LocalRef/GlobalRef
  pair still match, and are required fields documented as "Must be present."?

Run `go vet` and the existing tests of the touched packages when they take
under a few minutes. Tests that bind a listener or need Docker are rerun
unsandboxed.

## 4. Verify before reporting

Open the code for each finding and confirm the failure is real in the current
tree and introduced by this change. Drop findings that rest on a misreading;
mark the ones you could not confirm as unverified. List problems that predate
the change under "pre-existing", verified to the same standard. A hunk
unrelated to the change inside an in-scope file is a note, not a finding.
A subject scope drops the second half of that test and keeps no pre-existing
section: confirm the failure is real in the current tree, because age is not
what disqualifies a finding here.
Prove a point with a throwaway test in the scratchpad directory when reading
is not enough; never add files to the repository during a review. Never pad
the list.

Four findings need more than a re-read:

- One that cites an external convention, API, or spec is a claim about the
  version the repository pins, not the latest published one; check it
  against the pinned artifact before keeping it.
- A negative probe result (the attack did not land, the path was not taken)
  stays open until the code that refuses it is pointed at; a probe's wire
  detail (a header, a subject, a frame shape) is checked against the
  implementation before either result is trusted.
- A seam finding discharged by an invariant in another unit or service is
  not dropped: it becomes a question for the reviewer that holds those files
  and stays in the report, because the coordinator has read less of the
  neighbour than the reviewer briefed on it.
- A finding is a sample of a class until shown otherwise: name what else
  engages and clears the same mechanism, so the fix covers the class. When
  a round finds a defect in the previous round's fix for the same
  mechanism, stop patching: state the property the mechanism must hold and
  make it executable (a generated state space, an invariant assertion the
  suite can fail on) instead of reviewing the next rewrite. Step 6 says
  whose job it is to notice this across rounds.

The smallest fix is verified like the finding when it rests on a claim
about the code ("nothing else produces this", "no caller does that", "this
path is unreachable"): open the code the claim is about. A fix that could
not be verified is reported as a direction, not a patch, and says which
claim is unchecked; a verified finding otherwise lends its authority to a
remedy the coordinator applies first and checks second.

Once every finding is settled, log how each reviewer's findings fared, so
`tune` can rank reviewers by what held. Make one call per reviewer this step
judged, zeros included for a reviewer that found nothing, with the sandbox
disabled: the log lives under `~/.claude/models`, where the sandbox allows
no writes.

```bash
python3 .claude/skills/delegate/scripts/runlog.py review --model claude-opus-5-5 \
  --role review-unit --agent <agent id or run> --plan docs/plans/<plan>.md \
  --findings 4 --held 3 --unverified 1
```

- `--model` is the registry id the reviewer ran on, never an alias such as
  `opus` or `inherit`.
- `--role` is `review-seam` for a reviewer dispatched for the interplay and
  `review-unit` for every other.
- `--agent` is the agent id the Agent tool returned for a native reviewer.
  A reviewer on a pool's CLI gives the `run` from the JSON line
  `orca-worker.sh start` printed, because `stop` deletes the state file
  that also holds it.
- `--plan` is the plan path, left out when the scope has no plan.
- `--findings` counts what the reviewer returned. `--held` counts those
  this step kept, a finding moved to "pre-existing" included, and
  `--unverified` those marked unverified. A finding dropped as a misreading
  counts in `--findings` alone.

The coordinator's own reading logs nothing: this step is where the
coordinator checks findings, so a held share for its own would be the
coordinator grading itself. A failed call prints `runlog: <reason>`. Quote that line in
the report and carry on; the verdict does not depend on the log.

## 5. Report

Verdict first (accept, accept after fixes, rework), then findings, most severe
first: title, `path:line`, what goes wrong and when, and the smallest fix or
the direction with its unchecked claim (step 4). Then the
residual testing gap. In Orca, append the verdict to the worktree comment,
keeping what `implement` wrote:

```bash
orca worktree set --worktree active --comment "<existing>; review: rework" --json
```

A subject scope reports its units and seams first, so the reader can see what
was covered, then the interplay findings, then the unit findings under their
unit. Its verdict judges the subject (sound, sound with fixes, unsound), and it
never goes in the worktree comment: `land` reads a `review:` entry there as a
verdict on the branch, and a subject review has not looked at the branch.
Findings too large to fix in place go to `plan` with what this review
established, not into a fix attempt at the end of an audit.

When the scope is this branch's work (the working tree, the branch, or its
plan's paths), record the verdict where `land` reads it, step 1 of
`land`: the plan's frontmatter gains `review: <verdict>` beside `status`,
committed with a message naming the review, and then the verifier runs on
the plan path so the receipt post-dates that commit; planless work appends
the same line to `$(git rev-parse --git-dir)/flowseer-checkpoints` with
`.claude/skills/verify-change/scripts/ledger.py checkpoint review "<verdict>"`,
which no commit or run is needed for. A subject, commit, or path review of other
work records nothing, for the reason above. The Orca comment is written as
well; a verdict that lives only in the conversation cannot be read by a
later session.

The review itself changes nothing. End the report by asking the user what
happens next (`AGENTS.md`, Agent behavior), with the options the verdict
leaves:

| Verdict | Options, recommended first |
| --- | --- |
| accept | run `compound` now; stop here |
| accept after fixes or rework, findings in one file group | apply the fixes here; fix and review again until clean (step 6); stop |
| accept after fixes or rework, findings across file groups | fix and review again until clean (step 6); apply chosen findings only; stop |
| rework too large to fix in place | take what the review established to `plan`; stop |

On "apply the fixes", make them, run the verifier on the changed paths,
report what changed, and set the verdict to `review: accept after fixes`.
A subject review reads its rows by findings, with sound with fixes and
unsound in place of the branch verdicts, and offers the fixes or `plan`,
never `compound` or `land`. A sound subject with nothing to fix ends
without a question.

## 6. Fix and re-review, when asked

When the user chooses to fix the findings and review again until the work
is clean, the coordinating session runs the rounds; no skill runs them on its
own, and `implement` covers a plan's units, not a review's findings. One
round is:

1. Dispatch the fixes as `delegate` describes, one worker per file group,
   each briefed with its findings' `path:line`, failure scenario, and
   smallest fix, and with the class step 4 named: the mechanism behind
   the finding, and the instruction to find and fix every other site that
   engages it and report the sites it cleared. The coordinating session
   does not make the fixes itself.
2. Merge each worker's branch, then run the verifier once on the union of
   the changed paths, before anything is reviewed again.
3. Repeat steps 3 and 4 of this skill over the branch diff, briefing the reviewer with
   the previous round's findings and the paths that changed, so it judges
   each fix against its finding instead of rediscovering it. When the
   previous round's remedy was an executable property (step 4), that
   artifact is the brief's primary subject, with three questions: is its
   enumeration complete against the source it claims to read, does each
   case fail for the rule it names, and is each exemption an argument no
   input can violate? A wrong invariant is worse than none, because the
   next reader trusts it and stops looking.

The loop stops at a round with no correctness findings; what remains is
listed in the final report, the verdict is `accept after fixes`, and the
report ends with the `accept` row's question. The
coordinator holds the rounds' history, so it is the one that sees a round
find a defect in the previous round's fix for the same mechanism; then
step 4's rule applies before the next round, and after three rounds on the
same mechanism without a clean one the work goes to `plan` with what the
rounds established, as `implement` does with a unit still red after three
verifier rounds.

A correction to this procedure is logged as `compound`, Observe describes.
