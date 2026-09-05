---
name: review
description: Review a FlowSeer change (working tree, branch, commit, or paths) for correctness, regressions, missing tests, and violations of the repository conventions, and report verified findings by severity. Use when asked to review code, check a diff, or judge a change before commit. Report-only unless the user asks to apply fixes.
argument-hint: "[base ref | commit | paths]"
---

# Review a FlowSeer change

One independent reviewer with the repository's own conventions finds what a
generic panel finds, at a fraction of the cost. The value is in verifying each
finding before reporting it.

## 1. Resolve the scope

Pick the narrowest that matches the request:

| Request | Scope |
| --- | --- |
| no target given | `git diff HEAD` plus untracked files |
| a base ref or "the branch" | `git diff <base>...HEAD` |
| a commit | `git show <sha>` |
| paths | those files, whole |

List the changed files and their languages. Read the intended behavior from
whichever exists: the plan under `docs/plans/`, the commit message, or the
user's words.

## 2. Load the applicable rules

Read only the conventions that the touched files need: `docs/code-style.md`
for Go, `docs/code-style-proto.md` and `docs/conventions/protobuf.md` for
schema, `docs/conventions/observability.md` for telemetry, `docs/doc-style.md`
for prose and comments. Read the "Read when" column of
`docs/solutions/README.md`; for a matching solution read its guidance
section, and the rest only when a finding needs it. When the change touches
an area with a `docs/architecture/` record, read the record's boundaries.

## 3. Dispatch the reviewer

`independent-reviewer` has no shell. Write the diff to a file in the
scratchpad directory and brief the agent as `delegate` describes: that path,
the file list, the plan path, the intended behavior as a specification, the
applicable convention paths, and the matched solutions. Leave out any
statement that the change is tested or believed correct, and strip such
statements from forwarded commit text. Ask for findings that affect
correctness, the stated requirements, or a repository rule, ordered by
severity, each with path and line, the failure scenario, and the smallest
safe fix.

One reviewer covers about 1,500 changed lines or one subsystem; the docs that
describe a package belong to its subsystem. Above that, dispatch one reviewer
per subsystem with its own diff file, in parallel. Split by file group, never
by persona.

When a convention or solution you would apply is itself in the diff, take the
rule from the `HEAD` version and review the new version as prose.

While the reviewers run, read the hunks that change behavior in full and skim
the rest, with these questions:

- Does every behavior change have a test that would fail without it, and
  would that test still fail if the check moved to the wrong place?
- Does any comment narrate process, cite history, or carry a plan label?
- Does a README, convention doc, or schema comment now disagree with the code?
- Is anything added that has one caller, one implementation, or no caller?
- Does the change move a boundary that an accepted direction record fixes?
- For schema: does the Config/State/Event triad or a LocalRef/GlobalRef pair
  still match, and are required fields documented as "Must be present."?

Run `go vet` and the existing tests of the touched packages when they take
under a few minutes; they settle compile health and golden fixtures faster
than reading. Tests that bind a listener or need Docker fail in the sandbox
and are rerun unsandboxed.

## 4. Verify before reporting

Open the code for each finding and confirm the failure scenario is real in the
current tree and introduced by this change. Drop findings that depend on a
misreading. Mark the ones you could not confirm as unverified. List problems
that predate the change under a separate "pre-existing" heading, verified to
the same standard. A hunk unrelated to the change inside an in-scope file is
a note, not a finding. Prove a point with a throwaway test in the scratchpad
directory when reading is not enough; never add files to the repository
during a review. Never pad the list.

## 5. Report

Verdict first (accept, accept with fixes, rework), then findings, most severe
first. For each: title, `path:line`, what goes wrong and when, the smallest
fix. Then the residual testing gap, then what is good about the change in one
or two lines if it matters to the decision. In Orca, put the verdict in the
worktree comment.

Report only. When the user asks to apply the fixes, make them, run the
verifier on the changed paths, and report what changed. If the user
corrected this procedure rather than the findings, log it as `compound`,
Observe describes.
