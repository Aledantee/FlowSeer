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

## 2026-09-15 verify-change: a concurrent linter turns the verifier red for no reason
Skill or agent: `.claude/skills/verify-change/SKILL.md`, and the
`golangci-lint run` gate in `scripts/verify-change.sh`.
What happened: `golangci-lint` takes a machine-global lock. Two `--full`
runs died with `Error: parallel golangci-lint is running` and
`FlowSeer verification FAILED (exit 3) in gate: golangci-lint run`, once
because the session ran `golangci-lint` by hand to check a review finding
while the verifier was in flight, once because a targeted
`verify-change.sh -- <path>` overlapped the full run. Every gate before the
linter had passed both times. Nothing in the skill says the gate is
exclusive, so the failure reads as a real one, and a session that believes
it will go looking for a defect that is not there. Roughly forty minutes of
full-suite runtime was spent twice over. The step was followed as written.
Suggested change: say in the skill that the linter gate is mutually
exclusive machine-wide, that only one verification may run at a time and no
separate `golangci-lint` may run alongside it, and that
`parallel golangci-lint is running` means contention rather than a finding
and the run should be repeated. A clearer message from the script itself
when it sees that string would be better than prose.

## 2026-09-15 review: nothing describes the fix-and-re-review loop the skill's own rule assumes
Skill or agent: `.claude/skills/review/SKILL.md`, step 4 and the Report
section.
What happened: the user asked to fix findings in subagents and re-review in
a loop until the work was clean. `review` ends at a report and says
"Report only"; `implement` covers a plan's units, not a review's findings.
So the loop was run ad hoc: seven fix rounds and five review rounds, with
the coordinator choosing each round's scope, dispatching worktree workers,
merging, and deciding when to stop. The skill already carries the rule that
matters most for such a loop, in step 4: when a round finds a defect in the
previous round's fix for the same mechanism, stop patching and make the
property executable. That rule fired twice here and was right both times,
but nothing says whose job it is to notice across rounds, and a session
that reads `review` alone would not know the loop is a shape the repository
expects. The step was followed as written; the gap is that no step covers
the rounds after the first.
Suggested change: a short section in `review` for the iterate case, naming
who runs it, that fixes are dispatched as `delegate` describes rather than
made in the coordinating session, that the verifier runs on the union after
each merge and before the next review round, and that the stop condition is
a round with no correctness findings. Point step 4's same-mechanism rule at
it so the two read as one procedure.

## 2026-09-16 plan: a unit's Tests line prescribed the test that could not catch the risk the plan named
Skill or agent: `.claude/skills/plan/SKILL.md`, the per-unit `Tests:` line.
What happened: phase 3d's Open questions said the MST BPDU octet layout "is
verifiable from no in-repo file" and that "the tests prove only the round-trip
and the body lengths, and no peer capture is available here"
(`docs/plans/2026-09-12-1339-feat-netsim-analysis-completeness-phase3d-plan.md`).
Unit U2's Tests line then asked for "the R14a-wire round-trip byte for byte",
which is symmetric and passes whatever placement the encoder chooses.
`implement` followed it, and two fields shipped in each other's octets until
review found it (fixed in `02ec81b0`). The step was followed as written and
still produced the wrong result.
Suggested change: have `plan` check each unit's Tests line against that unit's
own risks, so a risk the plan states as unverifiable by the tests either gets a
test that pins it or an explicit note that nothing in the unit covers it.

## 2026-09-16 close: every git step in the skill is refused in a worktree-isolated session
Skill or agent: `.claude/skills/close/SKILL.md`, step 2 (lines 86-88) and
step 3 (lines 125-141).
What happened: the skill reads the primary checkout and merges from it with
`git -C "$primary" ...`. A worktree-isolated session refuses every form that
names the shared checkout — `cd … && git`, `git -C`, and compound commands
that merely mention git — with "a worktree-isolated session's git operations
must target its own worktree". Disabling the sandbox does not lift it; the
guard is in the harness, not the sandbox. So `close` cannot check whether the
primary checkout is clean, cannot merge, and cannot run the post-merge
verifier, in exactly the environment the skill is written for. The steps were
followed as written and could not complete. Reading files under the primary
checkout still works, which is how its state was diagnosed.
Suggested change: invert the merge. Have `close` merge `main` into the
session branch inside the worktree, resolve conflicts and run the verifier
there, and leave the primary checkout a `--ff-only` fast-forward for the
person. That fits the isolation guard, and it puts conflict resolution where
the tests and the verifier already are. Whatever remains that only the
primary checkout can do should be emitted as copy-pasteable commands rather
than attempted.

## 2026-09-16 close: outside Orca a review verdict has nowhere durable to live
Skill or agent: `.claude/skills/close/SKILL.md`, step 1, "Outside Orca there
is no card" (line 46).
What happened: the skill says to ask the user for the review verdict and the
compound outcome "and record the answers in the report". The report is
conversational, so even an answered verdict survives only in the transcript:
nothing on disk carries it, and a later session, or a re-run of `close`,
cannot read it. Here the verdict was asked for three times, never given, and
the branch reached `main` anyway with no verdict recorded anywhere. The step
was followed as written.
Suggested change: outside Orca, write the three outcomes to a file the next
session can read — the plan's frontmatter is the natural home for planned
work (a `review:` field beside `status:`), and a note in the merge commit for
planless work. Then `close` reads the same signal whether or not Orca is
present.

## 2026-09-16 close: merging main into a session worktree needs a sandbox bypass
Skill or agent: `.claude/settings.json` sandbox configuration (policy
surface), affecting `.claude/skills/close/SKILL.md` step 3.
What happened: `git merge main` inside the session worktree died with
`unable to unlink old '.claude/skills/close/SKILL.md': Operation not
permitted` and `fatal: cannot create directory at
'.claude/skills/implement/scripts'`, because `.claude/skills` is write-denied
even inside the worktree. The merge aborted with HEAD untouched and only
succeeded with `dangerouslyDisableSandbox`. Any merge of a `main` that has
touched `.claude/skills/` hits this, which is every session that runs after a
`steer` lands. The deny exists to stop an agent editing its own skills; it
cannot distinguish that from git replaying a committed change.
Suggested change: decide deliberately whether a git checkout or merge writing
under `.claude/skills/` should be permitted, and if not, say in `close` that
this merge is expected to need the bypass, so the failure is not read as a
defect. This is a policy surface, so `steer` stages it rather than applying it.

## 2026-09-16 verify-change: a missing tool reads as a red verification
Skill or agent: `.claude/skills/verify-change/scripts/verify-change.sh`, the
tool-presence check.
What happened: the verifier stopped with `required tool is not on PATH:
gofumpt` and `FlowSeer verification FAILED (exit 1)`. Every Go tool the gates
need — gofumpt, goimports, golangci-lint, staticcheck — was installed in
`~/go/bin`, which the session's PATH did not carry. The run had already done
its diff-aware path selection and its markdown gates, so the failure landed
late and read as a gate result rather than a setup problem. The same PATH gap
makes the Edit hook warn "edited Go files were not formatted" after every
single edit. Re-running under an explicit PATH passed.
Suggested change: check every required tool before the first gate and fail
with the whole missing list plus the remedy — Go tools install to
`$(go env GOPATH)/bin`, and that directory belongs on PATH. Better, resolve
them from `$(go env GOPATH)/bin` directly when PATH does not carry them, so a
session's PATH cannot decide whether the repository verifies.

## 2026-09-16 close: the dirty marker is written by the verifier run that should clear it
Skill or agent: `.claude/skills/close/SKILL.md`, step 1's receipt signal,
against `flowseer-verification-dirty`.
What happened: `close` requires `flowseer-verification-receipt` newer than
the last commit and `flowseer-verification-dirty` absent. After a clean
full verification the two files carried the same mtime to the second, so the
marker the run was supposed to clear was written by that same run. The
worktree was genuinely clean (`git status --porcelain` empty) and the receipt
did post-date the last commit, so the session treated the marker as an
artifact and proceeded — which means the signal decided nothing.
Suggested change: find what writes the marker during a verification and stop
it, or state in `close` what the marker means when it is newer than the
receipt it accompanies. A gate that cannot be satisfied by doing the right
thing teaches every session to ignore it, which is worse than not having it.
