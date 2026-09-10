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
fix. When the plan's `status` is still `planned` because work is mid-flight,
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
the conventions that unit needs. Give a unit reviewer the names of its
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
  would that test still fail if the check moved to the wrong place?
- Does any comment narrate process, cite history, or carry a plan label?
- Does a README, convention doc, or schema comment now disagree with the code?
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
  suite can fail on) instead of reviewing the next rewrite.

## 5. Report

Verdict first (accept, accept with fixes, rework), then findings, most severe
first: title, `path:line`, what goes wrong and when, the smallest fix. Then the
residual testing gap. In Orca, append the verdict to the worktree comment,
keeping what `implement` wrote:

```bash
orca worktree set --worktree active --comment "<existing>; review: rework" --json
```

A subject scope reports its units and seams first, so the reader can see what
was covered, then the interplay findings, then the unit findings under their
unit. Its verdict judges the subject (sound, sound with fixes, unsound), and it
never goes in the worktree comment: `close` reads a `review:` entry there as a
verdict on the branch, and a subject review has not looked at the branch.
Findings too large to fix in place go to `plan` with what this review
established, not into a fix attempt at the end of an audit.

Report only. When the user asks to apply the fixes, make them, run the
verifier on the changed paths, report what changed, and set the verdict to
`review: accept after fixes`. A correction to this procedure is logged as
`compound`, Observe describes.
