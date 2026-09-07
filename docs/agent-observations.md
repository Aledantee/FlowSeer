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

## 2026-09-05 verify-change: targeted mode fails on a brand-new package at an explicit path
Skill or agent: `.claude/skills/verify-change/scripts/verify-change.sh`, the `buf breaking` step (around line 346).
What happened: running `verify-change.sh -- <new proto files>` for schema landing entirely new packages (`spec/proto/flowseer/errs/v1/`, `integration/device/v1/`, `event/device/v1/`) made `buf breaking --against .git#branch=master --path <new file>` exit 1 with "no .proto files were targeted", because the ref being compared against (`master`) does not contain the file the `--path` names, so `buf breaking` has nothing to check and reports failure rather than a no-op success. The script's `set -e` then aborts the whole run before the later `buf generate` diff, hook tests, and OTel tier ever execute — with no output naming which step failed if the invocation is only skimmed for its final line. `verify-change.sh --full` does not hit this: it never passes `--path` to `buf breaking` (`full == false` guards `proto_path_args`), so it correctly diffed the whole tree against `master` and passed.
Suggested change: either have the doc/skill text call out that a brand-new schema package's first verification pass needs `--full` (or `--base master`) rather than the targeted `-- <paths>` form, or have the script itself detect the "no .proto files were targeted" case and treat it as success (a new package has nothing to break against, by definition) instead of letting `buf breaking`'s own exit code fail the run.
## 2026-09-05 plan: a plan's Prompt.Pattern and prompt-collision decisions need a discriminating-transcript check before implementation
Skill or agent: `.claude/skills/plan/SKILL.md`, step 4 ("Review the plan").
What happened: planning the FastIron interface capability, the plan
specified four `src/protocol/ssh.Prompt.Pattern` regexes (unprivileged,
privileged, config, config-if) by their apparent English shape ("ends
in `#`"). The independent-reviewer dispatch (already triggered, since
the plan had 6 units) caught that the patterns lacked `(?m)` and could
collide with each other, but only because the review happened to trace
`scanPrompt`'s actual matching rule against a multi-line buffer rather
than reading the patterns' intent — nothing in step 4's question ("what
would block or mislead an implementer") specifically asks a reviewer to
drive a caller-supplied regex against the package it targets before any
code exists. See
`docs/solutions/architecture-patterns/ssh-prompt-patterns-need-multiline-anchors-and-must-exclude-siblings.md`.
Suggested change: when a plan specifies a caller-supplied pattern
against an existing scanning/matching primitive (a prompt regex, a
header parser, a routing predicate), step 4's review question could add:
trace the primitive's actual matching semantics (anchor scope, tie-break
rule) against the pattern, not just its apparent intent, and check it
against every sibling value it must not also match.

## 2026-09-05 plan: a security-load-bearing stdlib API's exact semantics went unverified into a Decision
Skill or agent: `.claude/skills/plan/SKILL.md`, step 2 ("Gather evidence").
What happened: planning `docs/plans/2026-09-05-2154-feat-edge-bus-credentials-plan.md`'s U6 unit, the Decisions section committed to `os.OpenRoot`/`os.OpenInRoot` (Go 1.24+) as the mechanism for refusing a symlinked credential file, on the strength of its name and general reputation for closing symlink races. The stdlib doc for `os.Root` states plainly that it "will follow symbolic links" that stay inside the root — the opposite of what a single credential leaf needs, which is that the leaf itself is never a symlink. Implementing the unit and then dispatching `independent-reviewer` caught the gap; nothing in the plan step that chose the API had read `go doc os.Root` first. Step 2 tells the planner to "read the RFCs, vendor specs... and library source... the change depends on" for a third-party library via Context7, but a stdlib API used for a security property was not required to clear the same bar, and would not have needed Context7 to catch this — `go doc` was sufficient.
Suggested change: extend step 2 to say that a Decision resting on a specific standard-library API's exact behavior (not just its existence) gets the same evidence bar as a third-party library: read its doc (`go doc <symbol>`) or source before writing the Decision, especially when the property being relied on is a security or correctness guarantee rather than a convenience.

## 2026-09-06 review: four parallel independent-reviewer dispatches all failed with an identical stream-watchdog stall
Skill or agent: `.claude/skills/review/SKILL.md`, step 3 ("Dispatch the reviewer").
What happened: reviewing the device access lane change (~5,200 changed lines across 41 files, split into four subsystem diffs of roughly 1,100-1,600 lines each per step 3's own splitting rule), all four `independent-reviewer` agents dispatched in parallel failed after their first or second tool call with the identical error "Agent stalled: no progress for 600s (stream watchdog did not recover)". Each was briefed with one diff file plus 3-5 proto/convention/architecture-record context files to read — well within the tool's Read/Grep/Glob-only surface, and each had produced a normal opening move ("I'll start by reading the diff...") before stalling. This was not a content problem (the diffs and context files were all readable, ordinary repository files) and not an isolated flake (4 of 4 failed the same way at the same stage). The review was completed by the coordinating session reading the diff directly and stress-testing the highest-risk file under `-race -count=5`, which did surface two real bugs (see `docs/solutions/architecture-patterns/single-active-drainer-must-not-leak-its-context-or-its-shutdown-flag.md`), so a manual fallback is viable but skipped the "one reviewer per subsystem" parallelism the step calls for.
Suggested change: no code or skill fix is evident from a single occurrence — record this so a repeat (another large review where every dispatched `independent-reviewer` stalls the same way) becomes a pattern report to the harness rather than three more identical retries. If it recurs, the trigger to isolate is likely diff/context file size per dispatch or the number of simultaneous `independent-reviewer` dispatches, not the review content.

## 2026-09-07 verify: a directory argument makes verify-change.sh report success having run no gates
Skill or agent: `.claude/skills/verify-change/scripts/verify-change.sh`.
What happened: the script classifies its arguments by file extension to decide which gates to run, so a directory argument matches no classifier and selects nothing. `verify-change.sh -- src/modules/localnet/access` runs only `git diff --check` and prints "FlowSeer verification passed" with exit 0, having run no build, no vet, no race test and no lint; the same invocation naming a file under that directory runs all four. Confirmed by running both. This is silent: the output is indistinguishable from a real pass, and the plans for several units of `docs/plans/2026-09-06-1405-feat-central-device-service-and-hosts-plan.md` and its predecessors record their Verify lines as directories, so those recorded commands have been no-ops. The gap was found by an implementing session that noticed two unreported `unparam` findings in a package whose unit had been verified with a directory argument and reported green. Exposure in this build is bounded because later units also ran repo-wide `go build ./... && go vet ./... && go test -race ./...` and the Stop hook runs repository-wide checks, but a session that trusted the unit-level Verify line alone would have shipped unlinted code believing it gated.
Suggested change: the failure mode to remove is the silent pass, not the unsupported argument. Either expand a directory argument to the files under it before classifying, or refuse an argument the classifier cannot place with a non-zero exit naming it — "verification passed" must never be printable for a run that executed no gates. A third option, printing which gates ran, would also have made this visible on first use. Separately, the Verify lines in the plans under `docs/plans/` should name files or globs rather than directories until the script handles them.

## 2026-09-07 verify-change: a gate specified without lint, and run before the last edit, reports on a tree that no longer exists
Skill or agent: `.claude/skills/verify-change/SKILL.md`.
What happened: after the directory-argument finding above, the coordinating session specified a repo-wide gate as `go build ./...`, `go vet ./...` and `go test -race ./...` and omitted lint. An implementing session ran exactly that, reported green, and separately reported four lint findings as pre-existing; they were its own, introduced in the same commit, and it had linted before its final edits rather than after. Two distinct holes in one gate: the specification did not name every check the repository enforces, and nothing said when the gate runs relative to the last edit. A gate run before the final edit is evidence about a tree that no longer exists, and it looks identical to a gate run after.
Suggested change: state the gate as a closed list including lint on every package the change touches, and state that it runs on the final tree after the last edit. Both belong in the skill rather than in a coordinator's message, because a coordinator restating a gate from memory is exactly how the lint check went missing.

## 2026-09-07 implement: three tests passed against the defect they were written for, and only reverting the fix revealed it
Skill or agent: `.claude/skills/implement/SKILL.md`.
What happened: across one plan, three separate tests read as proof and asserted nothing. A concurrency test for an enrollment race passed with the fix reverted, because the window needs a competing write to land between two reads microseconds apart and the enrolling goroutine wins essentially always. An invariant test claimed to quantify "over every state above" but iterated a hand-written list of four records disjoint from the fifteen-row table it named — and feeding the table's own states in made it fail on a state that was already stranded in the tree. A forged-header security probe published to a subject that matched no subscription and built a header the server's parser could not recognise, so the attack it claimed to disprove never fired. Each was found only by deliberately breaking the thing under test and watching what happened; none would have been found by reading the test.
Suggested change: for any test asserting a concurrency, ordering, or security property, require that it be watched failing — revert the fix, or feed it a deliberately wrong implementation — before it counts as evidence. And require the implementer to state, per test, whether it is evidence for this change or a guard for future code; the distinction is cheap to write and it stops a guard being mistaken for a proof.

A later session, hitting the same trap twice in two slices, named the sharper rule: assert what the fix causes, not what it prevents, because prevention has more than one source. Both of its failing tests asserted the absence of a success — a command not sent, an error returned — and in both cases something unrelated supplied that absence (a cancelled wait context; a request deadline expiring while blocked on an acknowledgement nobody delivered). Rewriting each to count the thing the fix causes — commands that actually reached the device — made both fail on reversal. That is a rule for writing the test, not only for verifying it afterwards, and it catches the case where the branch under test is unreachable by construction from a single-threaded test.

## 2026-09-07 review: a probe that could not reach a mechanism was read as evidence the mechanism was unreachable
Skill or agent: `.claude/skills/review/SKILL.md`.
What happened: an implementing session probed whether a compromised peer could reach a delivery subject, could not reach it in a bounded attempt, and reported the unreachability as a defence — correctly labelling it a defence rather than a guarantee, which was the right instinct. An independent reviewer then read the broker's source and found the value was published in the clear on a subject the peer is granted, and that the probe's own header could not have triggered the code path it targeted. The negative result was evidence about the probe, not about the system, in both directions at once: wrong mechanism model, wrong subject.
Suggested change: add to the review step that a negative probe result is an open question until the code that refuses the probe can be pointed at. When a probe depends on a wire-level detail — a header format, a literal subject, a frame shape — the detail gets verified against the implementation before either a positive or a negative result is trusted.

## 2026-09-07 review: seven rounds of patching one mechanism, each round defective in the rule the previous round wrote
Skill or agent: `.claude/skills/review/SKILL.md`.
What happened: one derived-outbox mechanism went through seven review rounds across plan and code. Every round found a defect, and every defect was in the rule the previous round had just rewritten; twice a fix introduced a fresh defect of the same class. The two classes never varied — an obligation with no reachable terminator, and a state owing nothing while something outside it still held open work. What ended it was structural rather than another round: making the invariant executable over a generated cross-product of the record's finite dimensions, behind an explicit reachability predicate, and validating it by perturbation in both directions. That move arrived at round five and would have been available at round two.
Suggested change: name a trigger. When a review round finds a defect in the previous round's fix for the same mechanism, stop patching and ask what property the mechanism is supposed to hold and whether it can be made executable — a generated state space, an invariant assertion, a property the test suite can fail on — rather than reviewing the next rewrite of the same prose.

## 2026-09-07 delegate: a coordinator's instruction is a fact established elsewhere and relied on here, and it decays the same way
Skill or agent: `.claude/skills/delegate/SKILL.md`.
What happened: coordinating one plan, two instructions to implementing sessions were factually wrong about the codebase. One named `errs.From` as the way to recover an error's message for logging, which the sanitizing wrapper truncates, so the interceptor would have logged nothing useful. The other asserted that `api/edge` imports `api/inventory` and directed a schema field that would have been an import cycle — the dependency runs the other way, `api/inventory` importing `api/edge` for the edge reference. Both were caught because the implementing sessions checked before writing and reported back. Neither would have been caught by a session that took the instruction as given, which is the reasonable default when the instruction comes from the coordinator.
Suggested change: brief content that asserts something checkable about the codebase — an import direction, a call that behaves a certain way, a field that exists — is marked as verified or as unverified for the worker to confirm. And say in the skill that a worker checking a coordinator's factual claim before building on it is expected rather than insubordinate; two of this plan's schema errors were caught only that way.

## 2026-09-07 delegate: work was reviewed on a worker's report of the tree rather than on the tree
Skill or agent: `.claude/skills/delegate/SKILL.md`.
What happened: for several units the coordinating session accepted "committed, gates green" and reviewed the described change rather than the actual one. One report named two commits that did not exist — an edit script had failed on a wrapped match and written nothing, and the session reported before checking the result. The worker caught and corrected it unprompted, which is what made it visible. `git log --oneline -1` plus `git status --short` is a five-second check and was adopted only after that incident, several units in.
Suggested change: state in the skill that a coordinator verifies the tree — the commit exists, the tree is clean, and a spot check of the two or three changes that would be most expensive to get wrong — before reviewing or merging a delegate's work. Not distrust: a report describes what a session believes it did, and the gap between that and the tree is exactly where a silent tool failure lives.

## 2026-09-07 verify: a per-package gate cannot see a repository-wide invariant, and reports success
Skill or agent: `.claude/skills/verify-change/scripts/verify-change.sh`, and the per-unit Verify lines the `plan` and `implement` skills write.
What happened: implementing the central device service's host, a new `telemetry` package declared the error code `telemetry/instrument`, which `src/modules/localnet/access`'s own telemetry package already owned. Error codes are unique repository-wide, enforced by a source scan that lives in `src/common/errs` and only runs when that package's tests run. Every per-slice verification passed — the changed packages built, vetted, raced and linted clean — because the invariant is not checked in either package that violates it. Only the repo-wide `go test -race ./...` on the final tree failed, and only because it happened to include `src/common/errs`. Two packages choosing the same package name is exactly the case that collides, so the collision is likelier the more the repository grows. This is the second failure of the same shape as the directory-argument entry above: the narrow gate reports success on something the wide gate refuses, and the two are indistinguishable from the output.
Suggested change: the two are not the same bug and need different fixes. The directory case is an argument the classifier cannot place; this one is an invariant no changed-path run can see, because the test that enforces it lives somewhere the diff does not touch. Options: have `verify-change.sh` always run the packages that hold repository-wide invariant checks (`src/common/errs` at least) regardless of the changed paths; or state in the `implement` skill that a unit adding an exported name from a repository-wide namespace — error codes, telemetry scopes, metric and event names, NATS subjects, KV bucket names — runs the wide gate rather than the diff-aware one. The second is cheaper and generalises; the first cannot be forgotten. Note also that the plans' per-unit Verify lines are what most sessions actually run, so whichever fix lands should be reflected there rather than only in the skill. A later session found the same blindness from the opposite direction: not a name a unit adds, but one a unit fails to remove. `flowseer.device.drift.detected` had two live emitters on one branch — the access module's, with no attributes, and the device service's, with five — because the unit that added the second did not remove the first, and no per-package gate can see two packages using one event name. Whichever fix lands should cover both directions. The session that hit the first leaned toward the skill rule over always running the wide packages, on the grounds that the trigger is legible at the moment of the edit — a unit adds a name to a shared namespace — where always running them is a cost every unit pays for a case few hit.
