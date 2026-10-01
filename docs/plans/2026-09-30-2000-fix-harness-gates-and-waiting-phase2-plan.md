---
title: Harness Gates and Waiting Phase 2, Hook Assertions and Input - Plan
type: fix
date: 2026-09-30
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: accept after fixes
compound: docs/solutions/conventions/a-false-double-bracket-does-not-stop-set-e-under-bash-3-2.md
execution: code
parent: docs/plans/2026-09-30-2000-fix-harness-gates-and-waiting-plan.md
---

# Harness Gates and Waiting Phase 2, Hook Assertions and Input - Plan

> Implemented. 3 units, 2026-10-01T17:12Z to 2026-10-01T17:26Z. Staged,
> not committed, for review of the policy surface.

## Goal

`tools/hooks/tests/run.sh` fails and names the line when an assertion is
false, and fails when a later edit adds an assertion that cannot fail.
Every hook that reads stdin gives the first character 5 seconds to arrive,
so a hook run with a stdin that stays open and silent returns with the
result it gives malformed input today. The one exception is `stop-check.sh`,
which stops running its gates on such input. One session does the units in
order and stops at a staged diff for a person's review, since `tools/hooks/`
is a policy surface (parent Decisions, `AGENTS.md` Hard boundaries).

**Stop condition:** a runtime that starts a hook more than 5 seconds before
it writes the payload. Every guard would then deny a legitimate call.

## Decisions

The parent's Decisions apply. Local to this phase:

- Every bare assertion becomes `[[ … ]] || fail "<expectation>"`. `fail`
  prints its message and the calling line (`${BASH_LINENO[0]}`) to stderr
  and exits 1. Why: `/usr/bin/env bash` resolves to `/bin/bash` here, GNU
  bash 3.2.57, where a failing `[[ ]]` does not trigger `set -e`, either at
  top level or in a loop body.
  `/bin/bash -c 'set -e; [[ a == b ]]; echo reached'` and
  `/bin/bash -c 'set -e; for x in 1 2; do [[ a == b ]]; done; echo reached'`
  both print `reached` and exit 0. `[[` is a shell keyword, so a helper
  that takes the test as arguments cannot run it, and `test` lacks the `==`
  pattern match the suite uses.
- The conversion covers 99 top-level assertions, the 3 in loop bodies
  (lines 220, 731, 738), and the last lines of `assert_deny` and
  `assert_allow` (lines 50, 61). Line numbers are those of `run.sh` as this
  plan was written. The two helpers fail today through the caller's
  `set -e` but print nothing, and converting them lets one structural
  check cover the whole file. Four kinds of `[[` stay. The heredoc line
  that writes the stub `go` (559) and the `printf` strings that write stub
  scripts (393, 1038 to 1041) belong to the stubs. The
  `if [[ … ]]; then exit 1; fi` lines (704 to 719) already fail.
  `create-worktree.sh` line 48 is the last line of `is_within`, whose
  callers test its status.
- `run.sh` checks its own file (`$0`) before any fixture work. A line that
  is only a `[[ … ]]` statement, matched by
  `grep -nE '^[[:space:]]*\[\[.*\]\][[:space:]]*$'`, fails the suite. Why:
  an assertion added later without `|| fail` passes whether it holds or not,
  and nothing else would notice. This is the enumerate-and-count rule of
  `docs/solutions/conventions/a-gate-selected-by-name-stops-running-silently.md`
  applied to assertions. The pattern matches none of the stub lines that
  stay, since each continues past its `]]`.
- Hooks read stdin through one `hook_read_input` in `common.sh`, shaped as

  ```bash
  hook_read_input() {
    local first
    IFS= read -r -n 1 -t 5 first || return 0
    printf '%s' "$first"
    cat
  }
  ```

  and called as `input=$(hook_read_input)`. Why: `hook_init` and six hooks
  run `$(cat)`, which returns only at end of input. That those hooks
  finish in normal use shows the runtimes close stdin after the payload,
  not how soon they write it. Under `/bin/bash` 3.2.57, a `read -t 5` on a
  descriptor opened with `exec 3<>fifo` returns after 5 seconds with status
  1 and nothing read. Through a here-string, a regular file, and a pipe,
  `read` takes the first character and `cat` the rest, a 2,000,009-byte
  payload through a pipe in under a second. The function returns 0 on
  timeout because `create-worktree.sh` runs under `set -e`, where
  `/bin/bash -c 'set -e; x=$(false); echo reached'` prints nothing and
  exits 1. A leading newline is taken as `read`'s delimiter and dropped,
  which JSON does not notice.
- The five hooks that do not source `common.sh` (`create-worktree.sh`,
  `mark-verification-dirty.sh`, `protect-generated-bash.sh`,
  `suppression-warn.sh`, `worktree-guard.sh`) gain the `script_dir` and
  `source` lines `pre-tool-policy.sh` uses. Why: one helper keeps one
  deadline. `common.sh` defines functions and runs `set -uo pipefail`,
  which leaves the `-e` of `create-worktree.sh` on.
- Empty or malformed input keeps each hook's current result, except in
  `stop-check.sh`.

  | Hook | Event | Empty or malformed input |
  | --- | --- | --- |
  | `pre-tool-policy.sh` | PreToolUse | deny |
  | `protect-generated-bash.sh` | PreToolUse | deny |
  | `worktree-guard.sh` | SessionStart, PreToolUse | exit 0, no output |
  | `create-worktree.sh` | WorktreeCreate | exit 1 |
  | `go-format.sh`, `proto-check.sh`, `suppression-warn.sh`, `mark-verification-dirty.sh` | PostToolUse | exit 0, no output |
  | `stop-check.sh` | Stop | today it runs the gates in its working directory, after this phase it prints `{}` and exits 0 |

  Why: a guard that allowed on empty input would fail open.
  `stop-check.sh` has no input check. On empty input
  `jq -r '.cwd // "."'` prints nothing and exits 0, and `git -C ""` keeps
  the working directory, so a hand run from a checkout runs every
  conformance gate. It gains
  `jq -e . >/dev/null 2>&1 <<<"$input" || { printf '{}\n'; exit 0; }`, the
  check `suppression-warn.sh` makes, with the `{}` it already prints outside
  a repository. `jq -e .` exits 4 on empty input (jq 1.8.2).
- `run.sh` holds stdin open with `exec 3<>"$fifo"` on a FIFO under
  `$fixture_parent` and starts each hook in the background with `<&3`,
  polling it once a second. Why: a descriptor open for reading and writing
  is a writer that never writes, so the hook's `read` waits out its
  deadline with no helper process to clean up. Polling lets a hook that
  still waits after 10 seconds be killed and fail its case, so a regression
  fails the suite instead of hanging it. POSIX leaves opening a FIFO for
  reading and writing undefined. It works under macOS `/bin/bash` 3.2.57.
- A separate session the user starts implements this phase with
  `implement`, stopping at a staged diff for the user's review (decided by
  the user, 2026-10-01). Why: each hook edit needs the user's approval,
  and the drive session's context is already large.
- Ruled: `tools/test/service-otel-integration.sh` runs its kill-and-reap
  block with stderr discarded. Why: once `|| fail` made assertions count,
  the case "service OpenTelemetry wrapper gives up on a Docker daemon that
  does not answer" failed, because bash printed
  `line 40: … Terminated: 15 docker info > /dev/null 2>&1` ahead of the
  wrapper's message. The notice comes when bash reaps the killed probe,
  not from any one command's stderr. Cost if wrong: the one block and the
  case's expected output.

## Requirements

1. A false assertion fails the suite. Example: a scratch copy of `run.sh`
   with one expected value flipped exits non-zero and prints the `fail`
   text.
2. A guard hook fed from a FIFO held open with no data denies within 10
   seconds, timed on the hook process.
3. `create-worktree.sh` on the same input exits 1 within 10 seconds.
4. `stop-check.sh` on the same input exits 0 within 10 seconds without
   running its gates.
5. Every existing case in `run.sh` passes unchanged in meaning.
6. A bare `[[ … ]]` statement in `run.sh` fails the suite. Example: a
   scratch copy with `[[ a == a ]]` appended exits non-zero and names that
   line.

## Out of scope

- `worktree-guard.sh` allowing malformed PreToolUse input. It exits 0 when
  `cwd` is missing (`[ -n "$cwd" ] && [ -d "$cwd" ] || exit 0`) and keeps
  doing so, since parent requirement 5 keeps each hook's current result.
  Making it deny changes what the guard enforces and is its own decision.
- The `if [[ … ]]; then exit 1; fi` and `jq -e` assertions, which already
  fail the suite, keep their silent exit.
- Other scripts. Among the tracked `.sh` files that set `-e`, only
  `run.sh` has bare `[[ ]]` statements. The other match,
  `create-worktree.sh` line 48, is a function's last line.
- Input: the hooks read the JSON payload Claude Code or Codex writes on
  stdin. The runtime is trusted, and the `tool_input` it carries comes from
  the agent the guards constrain. This phase changes when the read gives
  up, not how the payload is parsed. `run.sh` reads only fixtures it
  writes.

## Units

One session does U1, U2, and U3 in that order. A worker may not change or
run anything under `tools/hooks/` (`delegate` item 6), so the waves record
the dependency graph, not parallel dispatch.

### U1. Assertions that fail
Files: `tools/hooks/tests/run.sh`, `docs/agent-observations.md`
After: none
Change: `run.sh` defines `fail` next to `ok`. Right after, it greps `$0`
with the Decisions' pattern and exits 1 naming each bare line found, then
runs a probe `( [[ a == b ]] || fail probe )` under the suite's
`set +e … set -e` idiom and exits 1 unless the probe's status is 1 and its
stderr holds `probe`. That check uses an explicit `if … exit 1`, since it
tests `fail` itself. Every assertion the Decisions list reads
`[[ … ]] || fail "<expectation>"`, with the expectation in a few words
(`fail "configured Claude edit guard did not deny"`). The entry
"2026-09-30 steer: Bash test assertions are not fail-fast" leaves
`docs/agent-observations.md`, since this unit applies its suggested change.
Tests: the probe and the self-check in `run.sh` cover requirements 1 and 6.
Every existing case passes (requirement 5). The hand checks under
Verification show a real assertion and a bare line each fail a copy of the
suite.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/hooks/tests/run.sh docs/agent-observations.md`

### U2. Bounded hook input
Files: `tools/hooks/common.sh`, `tools/hooks/create-worktree.sh`, `tools/hooks/mark-verification-dirty.sh`, `tools/hooks/protect-generated-bash.sh`, `tools/hooks/stop-check.sh`, `tools/hooks/suppression-warn.sh`, `tools/hooks/worktree-guard.sh`
After: none
Change: `common.sh` defines `hook_read_input` as the Decisions show, with
a comment saying why the first character has a deadline, and `hook_init`
sets `HOOK_INPUT=$(hook_read_input)`. The six hooks that ran `$(cat)`
assign from `hook_read_input`, and the five that did not source
`common.sh` now do. `stop-check.sh` prints `{}` and exits 0 when
`jq -e .` rejects its input, before it resolves a directory.
Tests: every existing case in `run.sh` passes unchanged (requirement 5).
Each now sends its payload through `hook_read_input`, by here-string or
pipe, which covers the first character reaching the payload. U3 adds the
idle-input cases. Nothing in the suite covers a runtime that writes late,
and the Open questions hold that risk.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/hooks`

### U3. Idle-input cases
Files: `tools/hooks/tests/run.sh`
After: U1, U2
Change: after the last Stop case ("Stop blocks when a checkout has no
conformance gate to run"), `run.sh` makes the FIFO, opens descriptor 3 on
it, and runs four hooks on it one at a time as the Decisions describe,
then closes descriptor 3. A last case fails when a line of any
`tools/hooks/*.sh` outside a comment matches `^[^#]*\$\(cat\)`, so a hook
added later reads through the helper. The four cases add about 20 seconds
to the suite.
Tests:
- `pre-tool-policy.sh` and `protect-generated-bash.sh` each print a `deny`
  decision within 10 seconds (requirement 2).
- `create-worktree.sh` exits 1 within 10 seconds (requirement 3).
- `stop-check.sh`, run from a fresh `git init` fixture holding `go.mod`
  and one directory under `test/conformance/`, with a stub `go` first on
  `PATH` that creates a marker file and exits 1, prints `{}` and exits 0
  within 10 seconds, and the marker does not exist (requirement 4). Each
  part of the fixture keeps the case from passing vacuously. Outside a
  repository the hook prints `{}` whatever its input. The last Stop case
  leaves `$gate_fixture` without `test/conformance/`, where a hook that
  ran its gates would block without calling `go`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/hooks/tests/run.sh`

Waves: U1 U2 | U3

## Verification

```bash
.claude/skills/verify-change/scripts/verify-change.sh -- tools/hooks docs/agent-observations.md
```

Then two hand checks from the repository root, each expected to exit 1
with the `fail` text on stderr (requirements 1 and 6):

```bash
sed '/claude_edit_output") == deny/s/== deny/== allow/' tools/hooks/tests/run.sh >"$TMPDIR/run-flipped.sh"
bash "$TMPDIR/run-flipped.sh"; echo "rc=$?"
{ cat tools/hooks/tests/run.sh; printf '[[ a == a ]]\n'; } >"$TMPDIR/run-bare.sh"
bash "$TMPDIR/run-bare.sh"; echo "rc=$?"
```

## Definition of done

- [x] Verifier green on `tools/hooks` and `docs/agent-observations.md`.
- [x] Both hand checks exit 1 with the `fail` text.
- [x] The diff staged, not committed, for a person's review of the policy
      surface.
- [x] This plan's `status` set with an outcome note under its title.
- [x] No plan labels in code, comments, or commit messages.

## Open questions

- How soon Claude Code and Codex write the payload after starting a hook.
  Neither documents it, so the 5-second deadline is unverified against
  them. A slower runtime would make the guards deny and the reporting
  hooks skip, which fails closed on the guards.
- Whether `exec 3<>` on a FIFO holds it open on Linux as it does on macOS.
  Not checked. If not, a background `sleep 60 >"$fifo" &` writer, killed
  after the cases, takes its place.
