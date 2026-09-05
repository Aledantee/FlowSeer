---
name: review
description: Review a FlowSeer change (working tree, branch, commit, or paths) for correctness, regressions, missing tests, and violations of the repository conventions, and report verified findings by severity. Use when asked to review code, check a diff, or judge a change before commit. Report-only unless the user asks to apply fixes.
argument-hint: "[base ref | commit | paths]"
---

# Review a FlowSeer change

## 1. Resolve the scope

| Request | Scope |
| --- | --- |
| no target given | `git diff HEAD` plus untracked files |
| a base ref or "the branch" | `git diff <base>...HEAD` |
| a commit | `git show <sha>` |
| paths | those files, whole |

List the changed files. Read the intended behavior from the plan under
`docs/plans/`, the commit message, or the user's words.

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
convention paths, and the matched solutions. Ask for findings that affect
correctness, the stated requirements, or a repository rule, ordered by
severity, each with path and line, the failure scenario, and the smallest safe
fix. When the plan's `status` is still `planned` because work is mid-flight,
say so in the brief.

One reviewer covers about 1,500 changed lines or one subsystem, its docs
included. Above that, dispatch one reviewer per subsystem with its own diff
file, in parallel. Split by file group, never by persona.

While the reviewer runs, read the hunks that change behavior in full and skim
the rest, asking:

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
Prove a point with a throwaway test in the scratchpad directory when reading
is not enough; never add files to the repository during a review. Never pad
the list.

## 5. Report

Verdict first (accept, accept with fixes, rework), then findings, most severe
first: title, `path:line`, what goes wrong and when, the smallest fix. Then the
residual testing gap. In Orca, append the verdict to the worktree comment,
keeping what `implement` wrote:

```bash
orca worktree set --worktree active --comment "<existing>; review: rework" --json
```

Report only. When the user asks to apply the fixes, make them, run the
verifier on the changed paths, report what changed, and set the verdict to
`review: accept after fixes`. A correction to this procedure is logged as
`compound`, Observe describes.
