# Agent observations

Entries here record where a FlowSeer skill, agent definition, or hook did not
fit the work: a correction the user had to make to the procedure, a step that
was skipped or done by hand, a brief that missed something, a sequence that
recurs with no skill behind it. The `compound` skill's Observe mode appends
them. Nothing reads this file on its own; the `steer` skill works the queue
when a maintainer asks for it.

`steer` applies or rejects each entry through the change process in
[`agent-steering.md`](agent-steering.md) and deletes it. Edits to a hook, a
hook registration, or `AGENTS.md` stop at a staged diff for a person's
review, since those are policy surfaces. An entry that sits here is not a
rule; the skill or hook it names stays authoritative until it changes. A
lesson about the code belongs under [`solutions/`](solutions/README.md),
not here.

Entry format:

```markdown
## <YYYY-MM-DD> <skill>: <one-line title>
Skill or agent: <path and step>, or `new skill candidate: <working name>`.
What happened: <what was corrected or did not fit, and whether the step
was followed as written>.
Suggested change: <smallest edit to the skill, agent, or hook>.
```

## Entries

## 2026-09-17 delegate: a brief written to $TMPDIR reached a worker as another session's brief
Skill or agent: `.claude/skills/delegate/SKILL.md`, "Write the brief", and
`references/herdr.md`.
What happened: the brief was written by a sandboxed Bash command, whose
`$TMPDIR` is the per-session sandbox directory, and read by the unsandboxed
`herdr-worker.sh start`, whose `$TMPDIR` is the shared system one. The path
resolved on both sides, so no step failed: the worker was dispatched with a
stale brief another session had left at the same relative path, reported the
task "already implemented", and changed nothing. The skill names the
scratchpad directory only for a reviewer's diff file.
Suggested change: say that a brief, like a diff, is written to the session
scratchpad directory, because every runtime command runs unsandboxed and
`$TMPDIR` does not mean the same directory on both sides of that boundary.

## 2026-09-18 review: a record's premise went stale while its paths were kept current
Skill or agent: `.claude/skills/review/SKILL.md`, and the amendment step every
phase plan writes.
What happened: three phases of a refactor amended six architecture records so
every path they cite matches the tree, and a reviewer then found that one
record's opening premise ("FlowSeer has no streaming RPC and no chunked payload
anywhere in spec/proto") had been false since an earlier phase landed five of
them. Each amendment pass checked the citations it was given and nothing else.
The step was followed as written.
Suggested change: when a change amends a record, check the record's premises
against the tree, not only its paths and full names. A record whose paths are
current and whose premise is false is worse than a stale one, because the fresh
paths make the reader trust the premise.

## 2026-09-18 tune: a calibration lane's effort and base commit were never recorded
Skill or agent: `.claude/skills/tune/SKILL.md`, step 4, and
`references/calibration.md`.
What happened: `bench.sh` accepted `--effort` and dropped it on the `claude`
branch, so the Claude lanes of the 2026-09-09 calibration ran at the CLI's
default effort while the registry routes those roles at `xhigh`. Step 4
writes `local.<role>: {pass, wall_s, cost_usd}` with no effort, run count,
or base commit, so the registry could not show the mismatch, and
`calibration.md`'s rule that a calibration names its base commit was not
followed: `evidence.md` names none. A user who doubted one routing decision
found it nine days later. The step was followed as written, apart from the
base commit.
Suggested change: have step 4 record `effort`, `runs`, and `base` in every
`local` result, run each lane at the effort of the role it is graded for,
and have `bench.sh` exit 2 on an `--effort` a CLI branch cannot apply.

## 2026-09-18 drive: a stage worker was pinned to `claude` because the stage is a skill
Skill or agent: `.claude/skills/drive/SKILL.md`, step 1 preconditions and
step 2's brief; `.claude/skills/delegate/SKILL.md`, "Resolve a role to a
lane".
What happened: a drive reported quota for `claude` and `codex` only, then
chose `claude` and `claude-sonnet-5` for the implement stage because
"implement is a Claude Code skill", with `google` at 1% and `go` at 19%.
`delegate`'s resolution was not followed: step 5 would have taken
`gemini-3.8-flash`. Nothing in the stage skills needs Claude, but the
skills exist only under `.claude/skills/` and `drive` said "runs
`implement`", which left room for the inference. The wording is fixed in
4f60f404; the user had to point both out. The native fallback for editing
work still has no registry answer now that `execute` lists no Claude
model.
Suggested change: have `delegate` say that a constraint on the CLI is
stated by the role in the registry and nowhere else, and name the model
the native `general-purpose` fallback runs on when the role's fit set
holds no Claude model.

## 2026-09-18 tune: one `go test` run undercounts a lane whose code deadlocks
Skill or agent: `.claude/skills/tune/references/calibration.md`,
"Acceptance tests".
What happened: the grading command runs the whole package once. A
`testing/synctest` deadlock panics, which ends the test binary, so the
acceptance tests after the failing one never run and the pass count read
off that output is wrong. Both Sonnet 5 `xhigh` lanes showed it; each of
the 7 tests was run on its own with `-run` to get 6/7 and 5/7. The step
was followed as written first and then replaced by hand.
Suggested change: grade with one `go test -race -count=3 -run
'^<name>$'` per acceptance test, and record partial credit from those.

## 2026-09-18 delegate: worker settlement signals failed across three execution lanes
Skill or agent: `.claude/skills/delegate/SKILL.md`, "Herdr worker", "Reading a worker's report", and `scripts/herdr-worker.sh`.
What happened: during delegated work across multiple lanes, three distinct
failures appeared in how worker completion was signalled, all caught only by
inspecting the child worktree rather than trusting the report:
- A `claude` worker on `claude-sonnet-5` ended its turn while its own
  background verifier was still running, leaving units unverified and plan
  fields unset. The model registry had independently removed that model from
  the `execute` role for "deadlock on source error".
- An `agy` worker's settle signal (`herdr-worker.sh wait`) returned `done`
  repeatedly while the agent was mid-turn, several times per multi-unit stage.
- An `opencode` worker on the `go` pool ran ~36 minutes and then entered a
  silent retry loop on "5 hour usage limit reached", while
  `delegate/scripts/pool-usage.sh` reported that pool at 16%—the script
  reads a local database that holds only this machine's sessions, so spend
  elsewhere is invisible. Two `esc` rounds did not interrupt it; a third did.
Suggested change: none recorded; steer decides the remedy.
