---
title: Package Guarantees - Plan
type: docs
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: rework
execution: mixed
---

# Package Guarantees - Plan

> Implemented. 3 units, 2026-09-28T19:09:14Z to 2026-09-28T19:22:52Z.
> Pilot outcome: 4 of 7 README contracts were proved by existing tests as-is,
> 2 needed a new test (session closure on peer disconnection and prompt
> earliest-match: TestScanPromptEarliestMatchAndTieOrder and
> TestRunPromptEarliestMatchInStream), and 1 needed a test change (output cap
> truncation without buffer ring saturation). The host-key guarantee also cites
> the Dial-level test because the helper-level one does not call Dial. The stop
> condition held (did not trigger); the convention and check format are
> validated.

## Goal

A Go package's current guaranteed behavior has one normative home: a
`GUARANTEES.md` beside its `README.md`, where each guarantee names the test
that proves it and the verifier fails when that test is gone. This plan
lands the convention, the verifier check, and a pilot on `src/protocol/ssh`.
A follow-up plan, written once the pilot is judged, makes `plan`,
`implement`, and `review` carry guarantee deltas. The idea is borrowed from
OpenSpec's living specs and change deltas
(<https://github.com/Fission-AI/OpenSpec>, `docs/concepts.md`); the tool
itself is not adopted. Stop condition: if the pilot shows that most README
contract statements have no test that asserts them, the file would be
unproven prose, and the format or the check is redesigned before the
follow-up plan is written.

## Decisions

- Guarantees live in `GUARANTEES.md` beside the package's `README.md`, not
  in the README. Why: the README is written for a person learning the
  package and explains why; a list of normative blocks with test citations
  reads as machine text there and duplicates the prose.
- The README explains a behavior and links to its guarantee; it does not
  restate the rule as a rule. Symbol doc comments keep stating their
  symbol's contract, as `docs/code-style.md` requires; a guarantee may
  overlap one, and both change in the diff that changes the behavior. Why:
  `docs/README.md` forbids a second source of truth, and the doc comment
  sits on the code it describes, so it is reviewed with it, while README
  prose is not.
- Only Go package directories carry a `GUARANTEES.md`; `spec/proto/`,
  `frontend/web/`, and skill scripts do not. Why: the citation rule resolves
  Go test functions, and `spec/proto/` is source-only (`AGENTS.md`, Hard
  boundaries; `tools/hooks/pre-tool-policy.sh` denies other files there).
- A guarantee's identity is its `##` heading. A later delta matches on it,
  the way OpenSpec matches a requirement by name. Why: headings are what a
  reader and a diff already see, and a rename shows as a removal plus an
  addition.
- Block format: a `##` heading, one normative sentence using MUST or
  MUST NOT in the RFC 2119 sense as clarified by RFC 8174, one or more
  `- WHEN … THEN …` scenario bullets, and one `Proved by:` line naming
  top-level Go test functions. Why: scenarios map one-to-one onto test
  cases, and the `Proved by:` line is the only part a script needs to
  parse. GIVEN is dropped because a WHEN clause carries the precondition in
  every README contract read for this plan.
- Cited tests resolve to `func Test…(` in a `*_test.go` file in the same
  directory as `GUARANTEES.md`, not in subdirectories. Subtests are not
  citable. Why: Go test names are unique only within a package, so a
  recursive lookup would let a subpackage's `TestX` hide the deletion of
  the parent's; a subtest name is a runtime string, not a symbol.
- A guarantee claims only what its cited tests assert. Why: the check
  proves a test exists, not what it proves; a sentence broader than its
  tests is the unproven prose the stop condition warns about.
- The rule is a convention doc (`docs/conventions/guarantees.md`), not a
  direction record. Why: it describes how documentation and tests are
  shaped, which `docs/README.md` sends to an existing convention or a new
  one under `conventions/`; `docs/architecture/` holds system direction.
- The verifier check fails the run; it does not only report. Why: a
  missing proof is a mechanical fact, and `docs/agent-steering.md` puts a
  checkable rule in enforcement rather than prose.
- Rollout is on touch, once the follow-up plan lands: a package gains a
  `GUARANTEES.md`, holding only the behavior a plan changes, when a plan
  first changes its behavior. Why: a backfill is a large diff with no
  review context. The convention states this now so the pilot is not read
  as a mandate to backfill.

## Requirements

1. Every `GUARANTEES.md` the verifier checks has unique `##` headings, and
   each section has at least one `- WHEN` bullet containing `THEN` and
   exactly one `Proved by:` line. Example: a section with no `Proved by:`
   line fails with `src/protocol/ssh/GUARANTEES.md:14: "Output is capped"
   has no Proved by: line`.
2. Every test a `Proved by:` line names exists as a top-level test function
   in the file's directory. Example: after `TestRunOutputCapTruncates` is
   renamed in `src/protocol/ssh/command_test.go`, `verify-change.sh --
   src/protocol/ssh/command_test.go` fails, naming the guarantee and the
   missing test, although `GUARANTEES.md` itself is unchanged.
3. The check runs for the `GUARANTEES.md` in each changed path's
   directory, whether or not the changed path still exists, and for all of
   them under `--full`. Example: a change to `src/protocol/ssh/scan.go`
   checks `src/protocol/ssh/GUARANTEES.md`; deleting
   `src/protocol/ssh/command_test.go` checks it too; a change to
   `docs/README.md` checks none.
4. `src/protocol/ssh/GUARANTEES.md` exists, passes the check, and each
   README contract section links to its guarantees. Example: "Host-key
   verification has no default" in the README links to the guarantee of the
   same name, which cites `TestHostKeyCallbackRequiresExplicitVerification`.

## Out of scope

- The OpenSpec CLI, its `openspec/` tree, and its `/opsx:*` commands.
- Changes to `plan`, `implement`, and `review` so plans carry guarantee
  deltas and units apply them. That is the follow-up plan (Open
  questions), written after the pilot, since changing the skills first
  would require a format the pilot has not tested.
- Backfilling guarantees for any package except `src/protocol/ssh`.
- An `AGENTS.md` Conventions entry for the new doc. `AGENTS.md` is a policy
  surface; `docs/README.md`'s map links the doc.
- Checking that a cited test asserts its scenario. The check proves only
  that the test exists; `review` judges the rest.
- Changes to `land`, `compound`, `drive`, or `next`, whose scripts parse no
  plan section beyond the frontmatter and unit fields.

## Units

### U1. Guarantees convention

Files: `docs/conventions/guarantees.md`, `docs/README.md`
After: none
Change: `docs/conventions/guarantees.md` states the file name and place
(Go package directories only), what a guarantee is (behavior of the
exported surface a caller relies on, not an implementation detail), the
block format from Decisions with a complete worked block, same-directory
citation resolution, the rule that a guarantee claims only what its tests
assert, the relation to the README and to symbol doc comments, and the
on-touch rollout. `docs/README.md` gains a map row for the convention; its
"Authority and freshness" paragraph on README and source as one contract
names `GUARANTEES.md` as the normative list of guaranteed behavior, with
the README explaining it; and its "Package-level behavior belongs beside
the package" sentence names both files.
Tests: none; prose only. The verifier's markdown link check covers the new
links.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/conventions/guarantees.md docs/README.md`

### U2. Verifier check

Files: `.claude/skills/verify-change/scripts/check-guarantees.py`, `.claude/skills/verify-change/scripts/test_check_guarantees.py`, `.claude/skills/verify-change/scripts/verify-change.sh`, `.claude/skills/verify-change/SKILL.md`
After: U1
Change: `check-guarantees.py` takes changed paths (or `--all`), selects the
`GUARANTEES.md` Requirement 3 names without requiring the changed path to
exist, and exits non-zero listing every violation of Requirements 1 and 2
as `<path>:<line>: <message>`. `verify-change.sh` runs it next to
`check-plan-status.py`, with `--all` under `--full` and the changed paths
otherwise. `SKILL.md` names the gate and links the convention.
`verify-change.sh` is part of the merge gate and no hook prompts for it,
so this unit runs in the coordinating session, and its diff goes to an
`independent-reviewer` as a guardrail review before the unit is committed.
Tests: `test_check_guarantees.py` loads the script with
`importlib.util.spec_from_file_location`, as
`.claude/skills/drive/scripts/test_plan_state.py` does, and builds fixture
trees in a temp directory: a valid file passes; a missing `Proved by:`
line, a duplicated heading, a section without a WHEN/THEN bullet, and a
cited test that does not exist each fail with the expected `path:line`; a
test defined only in a subdirectory or under `testdata/` does not resolve;
a changed path that no longer exists selects its directory's
`GUARANTEES.md`; a changed path whose directory holds none selects nothing.
Watch the missing-test case fail against a checker whose lookup always
succeeds.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/verify-change/scripts/check-guarantees.py .claude/skills/verify-change/scripts/test_check_guarantees.py .claude/skills/verify-change/scripts/verify-change.sh .claude/skills/verify-change/SKILL.md`

### U3. ssh pilot

Files: `src/protocol/ssh/GUARANTEES.md`, `src/protocol/ssh/README.md`, `src/protocol/ssh/scan_test.go`, `src/protocol/ssh/command_test.go`
After: U2
Change: `GUARANTEES.md` holds one guarantee per contract the README
states, each sentence no broader than its tests. Evidence from the plan
review, to confirm by reading each test before citing it:
- Host-key verification has no default: the refusal without a policy is
  proved network-free by `TestHostKeyCallbackRequiresExplicitVerification`
  (`options_test.go`); a mismatch by `TestDialHostKeyMismatchRefused`.
- A failed wait closes the session: `TestRunCommandDeadlineExceeded` and
  `TestRunCancellationMidWait` prove it for deadline and cancellation,
  with unwrapped context errors. The peer-closed case needs a new test in
  `command_test.go` that calls `Run` again after
  `TestRunConnectionLostAfterCommandSent`'s scenario and expects
  `ErrSessionClosed`.
- A command ends at the earliest prompt match, ties going to slice order:
  no test asserts this; add a two-prompt case to `scan_test.go`.
- Pagination markers are answered and never reach the output:
  `TestRunPagination`.
- Output is capped with the true byte count kept: `TestRunOutputCapTruncates`.
  It does not prove the most recent bytes are kept (its payload is uniform);
  claim tail retention only if a case with distinguishable bytes is added.
- Stream draining: `TestRunStderrSaturationDoesNotBlockStdout` proves one
  direction; the guarantee states that direction only.
- Evidence: `TestRunRedactsSecretFromEvidence` proves `Command.Redacted`
  replaces `Command.Line` in `Evidence.Sent`, and that is the guarantee;
  the package does no scrubbing of its own, so "evidence never carries a
  credential" is not claimed unconditionally.
Error and field names come from `options.go`, `command.go`, and
`errors.go`. Each new test is watched failing against a mutation first.
The README keeps its explanations, drops sentences that only restate a
rule now in `GUARANTEES.md`, and links each contract section to its
guarantees.
Tests: the check over the new file; the new cases in `scan_test.go` and
`command_test.go` named above.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/ssh`

Waves: U1 | U2 | U3

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh --base main
```

Then rename `TestRunOutputCapTruncates` in
`src/protocol/ssh/command_test.go`, confirm the verifier fails naming it
(Requirement 2), and restore the file from a copy taken first.

## Definition of done

- [x] Verifier green for every changed path.
- [x] `docs/README.md` authority paragraph, map, and placement sentence
      updated in U1.
- [x] The `verify-change.sh` edit passed the guardrail review before its
      commit.
- [x] The pilot outcome recorded in this plan's outcome note: how many
      README contracts were already proved, how many needed a new test,
      and whether the stop condition held.
- [x] This plan's `status` set, with the outcome note under its title.
- [x] No plan labels in code, scripts, or skill text.

## Open questions

- The follow-up plan: `plan`'s template gains a `## Guarantee changes`
  section with `Added:`, `Changed:`, and `Removed:` groups under one
  `### <package path>` heading each, the blocks in fenced code so their
  `##` headings do not join the plan's outline; `phases.md` puts a
  guarantee change in the phase whose unit implements it; `implement`
  step 4 applies a unit's blocks in the same unit; `review` checks
  `GUARANTEES.md` against the diff and the delta. Write it once this
  plan's outcome note shows the stop condition did not trigger.
- Whether to add `docs/conventions/guarantees.md` to `AGENTS.md`'s
  Conventions list; propose it through `steer` after the follow-up lands.
