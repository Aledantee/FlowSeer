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

## 2026-10-01 verify-change: targeted skill verification follows an untracked alias
Skill or agent: `.agents/skills/verify-change/SKILL.md`, named-path verification
command and the command table at lines 20-23.
What happened: The instructions and `AGENTS.md` direct a coordinator through
`.claude/skills/`. This checkout tracks the physical target under
`.agents/skills/`, so `git ls-files` returns no files for a symlinked skill
directory. The verifier then fails to enumerate the requested skill tests when
the documented directory spelling is followed.
Suggested change: resolve directory arguments before selecting gates, or teach
the verification skill to use `.agents/skills/` for targeted skill paths, and
add a regression case for the symlinked spelling.

## 2026-10-01 codex: hook command resolution is unverified on lane PATHs
Skill or agent: `.codex/hooks.json` project hook commands and
`/Users/aledante/.codex/hooks.json` SessionStart hook.
What happened: Codex lanes reported `Hook failed: hook exited with code 127`.
The project commands invoke bare `git` while constructing every hook path
(`.codex/hooks.json:9-79`), and the user SessionStart hook invokes bare `bash`
(`/Users/aledante/.codex/hooks.json:47-52`). A lane PATH without either
executable can fail before the target script reports its own error. The exact
failing command was not isolated.
Suggested change: run hook launchers through resolved executable paths, or add a
startup check that reports which command is missing under the lane PATH.

## 2026-10-01 land: sandbox bypass names the symlink alias but not its target
Skill or agent: `.claude/skills/land/SKILL.md`, step 3's sandbox bypass.
What happened: The step says the sandbox denies writes under `.claude/skills/`,
but this checkout stores that path through the `.claude/skills` symlink to
`.agents/skills`. Git writes to the target can fail with `Operation not
permitted` while a coordinator follows the documented `.claude/skills`
condition. The bypass is needed for the target path as well.
Suggested change: name both `.claude/skills` and `.agents/skills` in the
bypass condition, or resolve symlinks before deciding whether the merge needs
the sandbox disabled.

## 2026-09-30 implement: passed-unit commits were not verified against the worker base
Skill or agent: `.claude/skills/implement/SKILL.md`, the unit completion step
that records a passed unit's `HEAD` after verification.
What happened: U8 and U9 were recorded with `139391fe` and `b64fc806`, yet
those commits are earlier ancestors in the current history and contain the
unit changes from an earlier review round. No unit commit represented the
worker's completed pass. The implement guidance says to record `HEAD`, while
the tree check had to detect that the recorded commits predated the relevant
work.
Suggested change: require a passed-unit commit to be reachable from the
worker base and the worker's new `HEAD`, then reject an ancestor reused from
an earlier round.

## 2026-09-30 implement: independent units halted behind a blocked prerequisite
Skill or agent: `.claude/skills/implement/SKILL.md`, the wave grouping and unit
progress steps.
What happened: U5 has `After: none` in
the 2026-09-30 plan that fixed unverified external claims, but it was
halted when U1 was blocked. Work with satisfied prerequisites should have
continued independently.
Suggested change: keep blocked units in their own state and dispatch every
other unit whose `After` prerequisites are satisfied.

## 2026-09-30 implement: plan-deviations rejects unquoted Files lines
Skill or agent: `.claude/skills/implement/scripts/plan-deviations.py`, its
`QUOTED` parser and `no unit with a Files field` guard.
What happened: the helper only collects backtick-quoted paths, while this
plan writes paths after unquoted `Files:` fields. Running it against the plan
returned `no unit with a Files field` with status 2 on the 2026-09-30 plan that fixed unverified external claims.
Suggested change: parse both backtick-quoted and unquoted comma-separated
paths, while retaining the existing symbol and directory handling.

## 2026-09-30 implement: commit bodies contain literal backslash-n text
Skill or agent: `.claude/skills/implement/SKILL.md`, the unit commit step and
its requirement for one line per test failure.
What happened: the bodies of `39ef5aa6`, `07473cae`, and `eee9557b` contain
literal `\\n` sequences where commit paragraphs should contain newlines. The
history therefore records escaped formatting instead of readable test
evidence.
Suggested change: make the commit helper pass separate message paragraphs or
write the body through standard input, then reject a body containing literal
`\\n` before accepting the unit.
