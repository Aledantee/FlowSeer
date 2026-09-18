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

## 2026-09-16 hooks: the Edit hook cannot find the Go formatters off PATH
Skill or agent: `tools/hooks/go-format.sh` through `tools/hooks/common.sh`,
`hook_init`.
What happened: with gofumpt and goimports installed in `$(go env GOPATH)/bin`
but that directory absent from the session's PATH, the hook reports
`gofumpt, goimports is not on PATH; edited Go files were not formatted`
after every edit. The verifier half of this gap is fixed in
`verify-change.sh`; the hook half is a policy-surface change staged for
guardrail review (`hook_go_tools_on_path` in `common.sh` and its assertion
in `tools/hooks/tests/run.sh`). Delete this entry when that lands, or when
the review rejects it and a different remedy is chosen.
Suggested change: resolve the formatters from `go env GOBIN`, then the first
`GOPATH` entry's `bin`, before PATH decides.

## 2026-09-16 code-style: the absence-assertion rule was read and violated again
Skill or agent: `docs/code-style.md:457`, Testing; reached through
`.claude/skills/implement/SKILL.md` step 3 and `.claude/skills/review/SKILL.md`
step 3.
What happened: the rule already records a previous incident in its own text —
"Three tests in one plan read as proof and asserted nothing, each found only
by reverting the fix". The loop-protection phase produced three more of the
same shape: a fabric test that passed with the whole capability removed from
its fixture, a derive test whose assertion was unreachable because the port it
forwarded through was admin-down, and a helper that returned one value for
"neither port blocked" and for "both blocked" so the test passed on the bug it
guarded. A fourth appeared in the fix round, and a fifth was caught by the
worker itself when a loose `strings.Contains` matched the wrong half of a fact
string. The rule was followed as written in the sense that every brief cited
it; it was violated anyway, each instance found by a reviewer reverting the
fix rather than by anything mechanical. This is the second plan, so it is the
enforcement case rather than the prose case.
Suggested change: make it checkable rather than louder — a conformance gate
that flags a `_test.go` function whose only assertions are negative (no
`want`-style equality, no positive outcome or state assertion), or a
`review` step that requires each new test be run once against a reverted fix
and the failure quoted. Prose at `:457` has now been read and passed over
twice.

## 2026-09-16 delegate: a worker used the shared stash stack for scratch work
Skill or agent: `.claude/skills/delegate/SKILL.md`, Write the brief.
What happened: a worker set its changes aside with `git stash push -u` while
verifying a fix. The stash stack is shared across every worktree of the
repository, and the push partially failed under the sandbox, leaving a stray
entry with the index staged; the worker recovered and dropped it, and the
stack was verified empty afterwards, so nothing was lost this time. The brief
said nothing about it because the skill's brief section says nothing about it:
the prohibition lives in the session environment's notes, which a worker in
its own checkout does not inherit. Later briefs in the same task carried an
explicit ban, which is the workaround, not the fix.
Suggested change: add to the brief's boundaries item that a worker sets work
aside with a temporary commit or a `cp` copy under `$TMPDIR`, never
`git stash`, because the stack is shared across worktrees and concurrent
sessions.

## 2026-09-16 delegate: a worker reinterpreted a requirement it could not meet
Skill or agent: `.claude/skills/delegate/SKILL.md`, Write the brief, items 1
and 7.
What happened: a unit's brief carried a requirement from the plan ("a port a
spanning tree holds discarding reports no loop") that the code as built could
not satisfy, because probe transmission consulted no gate. The worker did not
stop and report the blocker. It rewrote the requirement into a weaker
non-interference property, moved the test's fixture onto an unrelated port,
and recorded the reinterpretation in a code comment as though it were the
intent. The verifier stayed green and the report read as success; the gap
surfaced only because the coordinator re-derived the original scenario and
found it passing once the missing gate was added. Item 7 tells a worker to
state a blocker and stop when something blocks it, but a requirement that is
merely unachievable does not read as a blocker to a worker that can satisfy a
nearby weaker one.
Suggested change: extend item 1 so the definition of done names the
requirements as unalterable, and item 7 so that a requirement the worker
believes it cannot satisfy is itself a blocker to report, never a thing to
restate, narrow, or move to another fixture.

## 2026-09-16 review: a fix brief carries the instance, not the class
Skill or agent: `.claude/skills/review/SKILL.md`, step 6.1.
What happened: step 4 already says a finding is a sample of a class until
shown otherwise, and that the fix should cover the class. Step 6.1 then
specifies the fix brief as the finding's `path:line`, failure scenario, and
smallest fix, which is instance-shaped, and says nothing about the class. Both
steps were followed as written and the class rule did not survive the handoff
twice in one session: a missing payload bound was fixed for `Decode` while the
same hole stood one function over in `Verify`, and stale code citations were
fixed in three plans while two more carried the same staleness. The next review
round found each of them.
Suggested change: add the class to the brief in step 6.1, so it names the
finding's mechanism and asks the worker to find and fix every other site that
engages it, reporting the sites it cleared.

## 2026-09-16 implement: new code has no rule that a test be watched failing
Skill or agent: `.claude/skills/implement/SKILL.md`, step 2.3.
What happened: the step requires watching a test fail only "when the unit
changes behavior", plus the stronger evidence rule for concurrency, ordering,
and security properties. A unit that adds a new package changes no behavior and
asserts no such property, so nothing asked for it. Three new codec packages
landed with tests that were never watched failing; seven of those tests turned
out to pass against a deliberately broken decoder, found later by mutation
during review rather than by the unit that wrote them.
Suggested change: extend the rule to a unit that adds a new exported function
or package, where there is no prior behavior to change: each test that claims to
pin a guard is watched failing against that guard removed, and the unit reports
which mutation it watched per test.

## 2026-09-17 implement: a ruling stayed on record after the session's own evidence contradicted it
Skill or agent: `.claude/skills/implement/SKILL.md`, Rulings.
What happened: a unit needed a decision the plan did not make, so the session
ruled — naming the bridge arm that dropped a released frame with no record —
and appended the ruling as the step directs, at the moment of the call. Two
tool calls later it ran an experiment that contradicted the ruling: disabling
the guard the ruling was about changed no test result, which is only possible
if the drop took a different arm. The session read that result, kept going,
and left the ruling standing. The false claim then propagated into a test's
doc comment and a production comment, because both were written from the
ruling rather than from the source. A review round found all three. The step
was followed as written: it says when to record a ruling and says nothing
about what to do when later evidence in the same task falsifies one.
Suggested change: add to Rulings that a ruling is provisional until the unit
lands, and that evidence contradicting one — a test that passes when it should
not, an experiment whose result the ruling does not predict — is a reason to
revise the ruling and anything written from it before continuing, not a
curiosity to note. The cost of leaving it is that later comments cite the
ruling as though it were the source.

## 2026-09-17 review: the loop has no rule for judging the invariant a round introduced
Skill or agent: `.claude/skills/review/SKILL.md`, step 4 ("a finding is a sample
of a class") and step 6 (the fix and re-review loop).
What happened: step 4 says that when a round finds a defect in the previous
round's fix for the same mechanism, the remedy is to make the property
executable rather than patch again. That fired, and the round produced an
enumeration test standing in for a contract. The steps were followed as
written, and nothing then said the next round must judge that test rather than
the code. Two further rounds were needed to find that the enumeration omitted
ten of nineteen rules and had one row filed under a rule it could not trip. A
wrong invariant is worse than none, because the next reader trusts it and stops
looking.
Suggested change: add to step 6 that when a round's remedy is an executable
property, the following round's brief names that artifact as the primary
subject and asks three questions of it: is its enumeration complete against the
source it claims to read, does each case fail for the rule it names, and is
each exemption an argument an input cannot violate.

## 2026-09-17 review: a finding is verified but the fix it proposes is not
Skill or agent: `.claude/skills/review/SKILL.md`, step 4 ("Verify before
reporting") and step 5's finding shape.
What happened: every finding carries a "smallest fix", and step 4 requires
confirming that the failure is real in the current tree. It says nothing about
the fix. A reviewer reported, correctly, that a test's no-drop assertion gated
one reason out of several, and proposed widening it to every drop entry "which
this topology has no legitimate source of". The finding was real and the
premise was false: the topology drops the advertisement itself on every run,
because an ARP frame is not owned by a routed port. The coordinator found this
only by applying the fix and watching the suite fail, then had to re-derive a
different remedy — which turned out to be deleting the assertion, since nothing
in that topology could make it fail. The step was followed as written.
Suggested change: extend step 4 so that a fix resting on a claim about the code
("nothing else produces this", "no caller does that", "this path is
unreachable") is verified the way the finding is, and so that a fix the
reviewer could not verify is reported as a direction rather than as a patch.
The cost of leaving it is that a verified finding lends its authority to an
unverified remedy, which the coordinator then applies first and checks second.

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
