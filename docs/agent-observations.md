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

## 2026-09-06 verify-change: the gate misses tagged files and a nested module
Skill or agent: `.claude/skills/verify-change/SKILL.md` and
`scripts/verify-change.sh`.
What happened: a breaking type change across the protocol libraries passed
the verifier on every changed path while two files did not compile —
`src/protocol/snmp/bench/macro_test.go` (behind `snmp_bench_macro`, in the
nested bench module) and `src/protocol/snmp/usm_parity_test.go` (behind
`snmp_parity`). The verifier builds untagged targets only, so nothing in the
default gate reaches either. Both were found later by sweeping build tags by
hand. Separately, one invocation whose paths spanned the main module and
`src/protocol/snmp/bench` put the bench package in the main module's target
list and failed with "main module does not contain package
go.aledante.io/FlowSeer/src/protocol/snmp/bench"; splitting the invocation per
module works. The steps were followed as written.
Suggested change: group changed paths by their nearest `go.mod` before
building the target list, and add a step that names the build tags touching
the changed packages (`grep -rh '^//go:build'`) and vets each one after a
change to an exported signature or field type.

## 2026-09-06 implement: Bash edits force a full verifier run
Skill or agent: `.claude/skills/implement/SKILL.md`, step 2 and Finish.
What happened: several unit edits went through `sed` and a Python
heredoc in Bash. The Bash hook recorded a `<Bash mutation; verify with
--full>` marker, so a documentation-only change ended in a full module
race run of about twenty minutes, and the first attempt aborted because
another worktree's golangci-lint was running. The step was followed as
written; nothing in it says which tool to edit with.
Suggested change: in step 2, say that edits go through the editor tools
and that a Bash write to a source file costs a `--full` run at Finish.
## 2026-09-05 implement: a self-built fake-server test harness only exercised the golden path it was written to reach
Skill or agent: `.claude/skills/implement/SKILL.md`, step 2.3 ("Write or
extend the tests the unit names").
What happened: implementing `src/protocol/ssh` (an expect-style prompt
scanner over a fake SSH shell), every test handler I wrote started
responding only after reading the client's command line, so the read
buffer was always empty when the scanner ran. That structurally could
not exercise "the buffer already holds something before this command" —
a login banner, or the shell's own echo landing ahead of a prompt-shaped
character in the sent command — which was exactly the real bug class
`review`'s independent-reviewer found (`src/protocol/ssh/command.go`,
now fixed; see
`docs/solutions/architecture-patterns/expect-style-prompt-scanner-must-reset-its-window-per-command.md`).
The step was followed as written; nothing in it prompts for a non-empty
starting state when the implementer is also the one designing the fake
peer.
Suggested change: for a stateful protocol client under test against a
self-authored fake peer/server, step 2.3 could add: seed the fake peer
with at least one case of unsolicited or leftover state ahead of the
call under test (a banner, a retained buffer, an out-of-order message),
since an implementer's own fake naturally only produces the sequence
they already coded the client to expect.

## 2026-09-05 close: no rule for work that skipped the plan
Skill or agent: `.claude/skills/close/SKILL.md`, step 1.
What happened: the branch's work followed plan's skip rule (no design choice), so no plan under `docs/plans/` carries `status: implemented`; the first signal has nothing to read and the step was not followed as written.
Suggested change: when the branch changed no plan of its own, accept the commit range plus a fresh verifier receipt as the implementation signal, and say so in the report.
