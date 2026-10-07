---
title: Python Scripts Under uv Phase 3, Hooks - Plan
type: refactor
date: 2026-10-05
artifact_contract: flowseer-plan/v2
execution: code
---

# Python Scripts Under uv Phase 3, Hooks - Plan

## Goal

The ten hook scripts and two helpers under `tools/hooks/` are modules under
`tools/scripts/hooks/` and `tools/scripts/lib/`, their suite is unittest,
both runtime configurations start them through `uv`, and `tools/hooks/` is
gone. The means: the suite is ported first and runs every case against the
shell hook and the Python hook until the shell hook is deleted.

**Stop condition:** a Claude Code session does not expand
`${CLAUDE_PROJECT_DIR}` inside `args`, or does not pass standard input to an
exec-form hook. The registration in U5 is then wrong.

## Decisions

- The parent plan's Decisions and the
  [repository scripting record](../architecture/2026-10-05-repository-scripting-direction.md)
  apply.
- The suite is ported before any hook. `tools/hooks/tests/run.sh` (1,367
  lines, 90 cases) becomes unittest cases that take the hook's command line
  as a parameter. The cases run against the shell hooks first. A case that
  cannot be expressed against both is listed under Cases outside the shared
  set, with the reason.
- `run.sh` is deleted by the unit that ports it. Why: the verifier runs it
  (`.agents/skills/verify-change/scripts/verify-change.sh:965`), and its
  registration cases pin the shell command strings, so it would fail the
  first unit that changes a registration.
- A hook case reaches its hook through `tools/scripts/tests/hooks/support.py`.
  `support.implementations(name)` returns the argument lists that exist for
  a hook: `tools/hooks/<name>.sh` while that file exists, and `uv run
  tools/scripts/run.py hooks <name>` once the `hooks` group registers the
  name. Each case loops over them with `subTest`. An empty list fails the
  case. Why: both implementations then run with no switch to forget, and a
  hook that neither form provides cannot pass silently
  (`docs/solutions/conventions/a-gate-selected-by-name-stops-running-silently.md`).
- 35 of the 90 cases do not test a hook: 15 drive `verify-change.sh`, 11
  `verify check-plan-status`, 1 `verify check-test-integrity`, 1 `implement
  plan-deviations`, and 7 `tools/test/service-otel-integration.sh`
  (`tools/hooks/tests/run.sh:845` to `:1365`). They move to the suites of
  what they test, under `tools/scripts/tests/verify/` and
  `tools/scripts/tests/skills/implement/`. Why: deleting `run.sh` would
  otherwise drop them, and Phase 4 extends the verifier suites.
- The command is `uv run tools/scripts/run.py hooks <name>`, not `hook
  <name>` as the parent's Requirement 1 spells it. Why: `run.py` names a
  group after its directory (`tools/scripts/run.py:68`), and the record
  names the directory `tools/scripts/hooks/`.
- `common.sh` becomes `lib/hookio.py`: read the event, resolve paths
  relative to the repository, put the Go tools on `PATH`, and emit `deny`,
  `ask`, or added context. `tree-state.sh` becomes `lib/treestate.py` and
  the command `hooks tree-state`. `json` replaces `jq`.
- `hookio.read_event()` gives the first byte five seconds, as
  `hook_read_input` does (`tools/hooks/common.sh:12`). A thread reads
  standard input and signals its first byte. The caller waits five seconds
  for that signal and then for the end of input without a bound. Why: `select`
  accepts only sockets on Windows
  (https://docs.python.org/3.13/library/select.html#select.select).
- A hook's JSON is compared after parsing. Why: `jq -n` prints several
  lines and `json.dumps` one, and both runtimes parse the output.
- `worktree-guard.sh` splits a command with unquoted `for tok in $cmd`
  (`tools/hooks/worktree-guard.sh:200`, `:233`), which also expands
  filename patterns in the session directory. The port splits on whitespace
  and expands each token with `glob.glob` from that directory, keeping the
  token when nothing matches. Why: `rm *.tmp` is allowed today only when
  every match is untracked.
- `grep` and `sed` match line by line. The port applies each pattern to
  each line of a command, so a class such as `[^']*` never spans a newline
  (`worktree-guard.sh:155` to `:185`, `protect-generated-bash.sh:43`). The
  split at `protect-generated-bash.sh:89` is on newline as well as `;&|()`.
- A hook's failure keeps its verdict. `pre-tool-policy` and
  `protect-generated-bash` deny on malformed input, and the port also
  denies on an uncaught exception in either. The other hooks exit 0 where
  the shell exits 0. Outside a repository every hook exits 0 with no output
  (`common.sh:23` to `:25`), which is not an exception.
- The port keeps these shell behaviors, each with a case in U1:
  - `jq -e .` rejects `null` and `false` and accepts any other JSON value
    (`common.sh:21`).
  - `create-worktree.sh` runs under `set -e`. A failing `git rev-parse`
    exits with git's status (`:24` to `:27`), and a registered worktree
    whose directory is gone ends the hook (`:88`).
  - The marker hook's file types (`mark-verification-dirty.sh:74`) differ
    from the listing's (`tree-state.sh:23`). Both lists are kept as they
    are.
  - A `case` pattern's `*` crosses `/`, so `spec/proto/*.proto` matches at
    any depth (`proto-check.sh:18`). The port uses `fnmatch`, not `glob`.
  - `stop-check` stops at the first failing gate (`stop-check.sh:68`) and
    names at most twenty unverified paths. It sorts them by bytes, where
    the shell sorts by locale. This is the one ordering the port fixes.
  - The switches `CLAUDE_WORKTREE_GUARD`, `.claude/no-worktree-guard`,
    `CLAUDE_WORKTREE_GUARD_BRANCHES`, and `FLOWSEER_WORKTREE_ROOT`.
- Claude Code registers each hook in exec form:
  `"command": "uv", "args": ["run",
  "${CLAUDE_PROJECT_DIR}/tools/scripts/run.py", "hooks", "<name>"]`. The
  hooks reference (https://code.claude.com/docs/en/hooks, fetched
  2026-10-07) says under "Exec form and shell form": "Claude Code resolves
  `command` as an executable on `PATH` and spawns it directly with `args` as
  the argument vector", and "path placeholders like `${CLAUDE_PLUGIN_ROOT}`
  are substituted into `command` and into each `args` element as plain
  strings". "Reference scripts by path" lists `${CLAUDE_PROJECT_DIR}` as
  such a placeholder. `uv run tools/scripts/run.py list` writes nothing to
  standard error once the interpreter is installed, so a hook's output is
  its own.
- Codex has no exec form. Its runner starts `$SHELL -lc <command>` on
  POSIX and `%COMSPEC% /C "<command>"` on Windows, in the session
  directory (`codex-rs/hooks/src/engine/command_runner.rs`, `build_command`
  and `default_shell_program`, at tag `rust-v0.160.0` of
  https://github.com/openai/codex). The registration stays a POSIX shell
  string that resolves the root with git, names what it could not find, and
  ends in `exec uv run "$root/tools/scripts/run.py" hooks <name>`. A
  `commandWindows` override exists
  (`codex-rs/hooks/src/engine/discovery.rs:513`). It is left to Phase 7,
  which has the Windows host to run it on.
- A Codex `PreToolUse` command that cannot find the root or `uv` exits 2
  with the reason on standard error. Other events keep exit 1. Why: Codex
  blocks a tool call on exit 2 with a reason and reports any other code as a
  failed hook that does not block
  (`codex-rs/hooks/src/events/pre_tool_use.rs:261` to `:284`). Exit 2 on
  `Stop` would keep the session from stopping. The launcher exits 1 for
  every event today (`tools/hooks/tests/run.sh:226`). This is the one
  verdict the phase changes on purpose, and Requirement 2 asks for it.
- Claude Code cannot be made to fail closed when `uv` is missing. The
  reference says under "Other exit codes": "A hook that can't start lands in
  the same non-blocking bucket", and "a mistyped path in `settings.json`
  leaves the gate silently disabled". That passage describes shell form,
  which is how the hooks are registered today. What exec form does when
  `command` is not on `PATH` is unverified. Requirement 2 states what can
  be held.
- `create-worktree.sh` and `worktree-guard.sh` are ported last, since a
  fault in either blocks every edit in a session.
- The Stop gate is timed in U5 on an empty and a warm Go build cache, with
  `UV_CACHE_DIR` set to an empty directory for the cold run. The timeout
  stays 30 seconds when the cold run takes at most 24. Otherwise both
  configurations take twice the cold time, rounded up to ten seconds. Why:
  a hook cancelled at its timeout renders no decision
  (`docs/agent-steering.md:1195`), and the shell version took 20 seconds.
- The shell verifier outlives this phase, so its references move here:
  `verify-change.sh:963` to `:967` (the `shellcheck` globs and the hook
  suite) in U1, `:1070` to `:1076` (the tree-state listing) in U5, and
  `:320` and `:359` (path classes) with the rest of the `:963` glob in U6.
  The listing call is `uv run "$run_py" hooks tree-state "$root"`, since
  `$run_py` is the checkout that holds the verifier (`:95`) and a fixture
  repository has no `tools/scripts`. It loses its existence test and a
  non-zero exit fails the run. Why: a missing
  `tree-state.sh` is skipped silently today, and the dirty marker then
  re-marks verified files. `jq` and `shellcheck` stay in
  `required_tools` (`:572`) until Phase 7.
- `AGENTS.md`, `pre-tool-policy`, `docs/agent-steering.md`, and the `steer`
  skill name the new paths in the unit that removes the old ones.
- Every unit touches a policy surface. Each stops uncommitted with the
  verifier green, and the person approves it by diff before the commit. A
  prompt inside a worker is not reliable (`docs/agent-observations.md`,
  entry of 2026-10-07 on policy-surface approval). The units run on Claude
  workers, since Codex does not support the hook's `ask`
  (`codex-rs/hooks/src/events/pre_tool_use.rs:581`).
- Files Phase 5 may also change: `verify-change.sh` (`:963` globs
  `.claude/skills/*/scripts/*.sh`), `.agents/skills/delegate/SKILL.md`,
  `.agents/skills/delegate/references/hookless-merge.md`, the five briefs
  under `.agents/skills/tune/references/calibration/`, and
  `docs/agent-steering.md`. This plan does not change `lib/proc.py`. A hook
  sets `os.environ["PATH"]` in its own process.
- Ruled: the `jq` check is `git grep -w jq -- tools/scripts/hooks
  tools/scripts/lib tools/scripts/verify` printing nothing, not the wider
  `tools/scripts` of Requirement 3's example. Why: the OTel wrapper that
  `test_service_otel_integration.py` drives is outside this port and calls
  `jq`, so its `WRAPPER_TOOLS` must keep the name. `support.py` drops `jq`
  from the fake `PATH` of the hook tests once no hook needs it. Cost if
  wrong: the Verification line and the example in Requirement 3 change back
  to `tools/scripts`, and the wrapper test loses its `jq` entry together
  with the wrapper's own port.

### Cases outside the shared set

| Case in `run.sh` | Why | What replaces it |
| --- | --- | --- |
| a false assertion stops the run (`:71`) | Tests the bash harness | Nothing. unittest assertions raise. |
| settings map every event to executable hooks (`:195`) | Pins command strings and executable bits | `test_registration.py`, rewritten in U5 and U6 |
| configured edit, Bash, and WorktreeCreate cases (`:216`, `:314`, `:404`) | Run the configured string through `bash -c` | The same file runs the configured command of each runtime |
| a Codex hook command names what it could not find (`:236`) | Pins the launcher text | The same file, with `uv` absent added in U5 |
| no hook reads stdin through a bare cat (`:843`) | Greps shell source | No module under `hooks/` names `sys.stdin` |

## Requirements

1. Every ported case passes against the shell hook and the Python hook.
   Example: the case "an edit under `generated/` is denied" passes with the
   command set to `tools/hooks/pre-tool-policy.sh` and to `uv run
   tools/scripts/run.py hooks pre-tool-policy`.
2. A guard that starts and cannot decide denies, and Codex blocks when the
   command cannot start. Example: `hooks pre-tool-policy` given `not json`
   prints a `deny` decision. The Codex `PreToolUse` command run with a
   `PATH` that lacks `uv` exits 2 and names `uv` on standard error. No
   requirement covers Claude Code with `uv` missing (Open questions).
3. No hook calls `jq`. Example: `git grep -w jq -- tools/scripts/hooks
   tools/scripts/lib tools/scripts/verify` prints nothing once `tools/hooks/`
   is gone.
4. The Stop gate finishes inside its timeout on an empty Go build cache.
   Example: the measured time is recorded in `docs/agent-steering.md`
   beside the command that reproduces it.
5. A registered hook name is a registered command. Example: an entry whose
   `args` end in `hooks no-such-hook` fails `test_registration.py`.

## Out of scope

- The hooks read events the agent runtime writes and files of the
  repository's own tree. Both authors are trusted. A hook checks for a
  malformed event, not a hostile one.
- `commandWindows` for Codex and any run on Windows. Phase 7 owns both.
- The verifier's own port, its `Verify:` command, and the `jq` calls in
  `.agents/skills/delegate/references/hookless-merge.md`. This phase
  changes only the hook paths there.
- New hook behavior. A verdict that differs from the shell is a defect,
  apart from the Codex launcher's exit code that Decisions names.

## Units

### U1. The suite as unittest

Files: tools/scripts/tests/hooks/__init__.py, tools/scripts/tests/hooks/support.py, tools/scripts/tests/hooks/test_registration.py, tools/scripts/tests/hooks/test_pre_tool_policy.py, tools/scripts/tests/hooks/test_protect_generated_bash.py, tools/scripts/tests/hooks/test_post_tool.py, tools/scripts/tests/hooks/test_mark_verification_dirty.py, tools/scripts/tests/hooks/test_stop_check.py, tools/scripts/tests/hooks/test_worktree.py, tools/scripts/tests/hooks/test_idle_stdin.py, tools/scripts/tests/verify/test_verify_selection.py, tools/scripts/tests/verify/test_check_plan_status.py, tools/scripts/tests/verify/test_check_test_integrity.py, tools/scripts/tests/verify/test_service_otel_integration.py, tools/scripts/tests/skills/implement/test_plan_deviations.py, tools/hooks/tests/run.sh, .agents/skills/verify-change/scripts/verify-change.sh, .agents/skills/steer/SKILL.md
After: none
Change: Every case of `run.sh` is a unittest case with the same name in its
docstring, apart from the first row of the table above. Hook cases build
their fixtures in `setUp` (a repository in a directory with a space, a
linked worktree, fake tools written to a `PATH` directory) and reach the
hook through `support.implementations`. `test_post_tool.py` holds
`go-format`, `proto-check`, `suppression-warn`, and `commit-message-check`.
`test_idle_stdin.py` holds the open-and-silent standard input cases with a
ten-second bound per hook. `test_registration.py` pins the registrations as
they stand. `test_protect_generated_bash.py` and `test_worktree.py` each
add a table case: one command per alternative of each pattern in
`protect-generated-bash.sh:38` to `:86` and `worktree-guard.sh:155` to
`:251`, with the decision the shell hook gives written as a literal. The
table holds a command of two lines, an `rm` of an untracked file, of a
tracked file, and of a pattern matching each, and a write whose only
absolute path lies outside the checkout. `run.sh` has no case for
`worktree-guard.sh:191` to `:251`, for the four switches, for
`stop_hook_active` (`stop-check.sh:87`), or for the shell behaviors
Decisions lists. U1 adds one each. The 35
non-hook cases move as Decisions says. A case `test_plan_deviations.py`
already holds is not added twice. `run.sh` is deleted, the verifier stops
running it and drops `tools/hooks/tests/*.sh` from its `shellcheck` glob,
and `steer` names `uv run tools/scripts/run.py test tools/scripts/tests/hooks`
where it names `run.sh` (`.agents/skills/steer/SKILL.md:83`, `:106`,
`:108`). The selection case at `run.sh:937` passes
`tools/scripts/tests/hooks/support.py` as its path.
Tests: `uv run tools/scripts/run.py test` reports the 89 ported cases and
the added ones passing against the shell hooks. Before `run.sh` is deleted, its `ok` names are
diffed against the docstrings, and the diff holds one name.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/scripts/tests tools/hooks .agents/skills/verify-change/scripts/verify-change.sh .agents/skills/steer/SKILL.md`

### U2. Hook library and the tree-state command

Files: tools/scripts/lib/hookio.py, tools/scripts/lib/treestate.py, tools/scripts/hooks/__init__.py, tools/scripts/hooks/tree_state.py, tools/scripts/tests/lib/test_hookio.py, tools/scripts/tests/lib/test_treestate.py, docs/code-style-python.md
After: none
Change: `lib/hookio.py` holds `read_event`, `Event` (the parsed input, the
physical root, and the prefix of the session directory), `tool_paths` (the
file path, the notebook path, and the `*** Add/Update/Delete File:` and
`*** Move to:` lines of a patch), `relative_path` with the four outcomes of
`hook_relative_path` (`tools/hooks/common.sh:108`) as an enum, `go_on_path`
and `go_tools_on_path`, and `deny`, `ask`, and `context`. `lib/treestate.py`
returns the listing `tree-state.sh` prints, sorted by bytes, with the
`MERGE_HEAD` subtraction. `hooks tree-state <root>` prints it and exits 2
without a root. `docs/code-style-python.md` gains the rule that a hook is a
command of the `hooks` group that reads its event through `lib/hookio.py`,
and that its exit code follows the runtime's hook contract, which is the
exception to "Exit non-zero on any failure" (`docs/code-style-python.md:39`).
Tests: `test_hookio.py` covers a silent open standard input (empty event
inside six seconds), each `relative_path` outcome (inside, outside every
checkout, another checkout of the repository, a `..` in a missing tail),
and `go_on_path` given a candidate directory that holds a fake `go`.
`test_treestate.py` asserts the output of `hooks tree-state` equals the
output of `tools/hooks/tree-state.sh` byte for byte on a fixture holding a
modified, a deleted, and an untracked file, a path with a space, an
unlisted file type, and a merge in progress.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/scripts/lib tools/scripts/hooks tools/scripts/tests/lib docs/code-style-python.md`

### U3. The two PreToolUse guards

Files: tools/scripts/hooks/__init__.py, tools/scripts/hooks/pre_tool_policy.py, tools/scripts/hooks/protect_generated_bash.py
After: U1, U2
Change: `hooks pre-tool-policy` and `hooks protect-generated-bash` give the
decisions of the shell scripts, with the reason strings copied verbatim.
The policy-surface list keeps `tools/hooks/*` until U6. Each POSIX
character class becomes an explicit set, and the split on `;&|()`
(`protect-generated-bash.sh:89`) stays a split, not a parser.
Tests: `test_pre_tool_policy.py`, `test_protect_generated_bash.py`, and
the two guards' cases in `test_idle_stdin.py` pass for both
implementations. The table case gives equal decisions for both.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/scripts/hooks`

### U4. The PostToolUse hooks and the Stop gate

Files: tools/scripts/hooks/__init__.py, tools/scripts/hooks/go_format.py, tools/scripts/hooks/proto_check.py, tools/scripts/hooks/suppression_warn.py, tools/scripts/hooks/mark_verification_dirty.py, tools/scripts/hooks/commit_message_check.py, tools/scripts/hooks/stop_check.py
After: U3
Change: The six commands behave as their scripts do. `go-format` and
`proto-check` exit 2 with the tool's output on standard error when a
formatter or `buf lint` fails. `mark-verification-dirty` reads the listing
from `lib/treestate.py`, writes the new one beside the old and renames it
with `os.replace`, and keeps the marker and state file names.
`go-format` compares content with `hashlib`. `stop-check` runs `go test`
once per child of `test/conformance/` and prints the same three shapes.
Tests: `test_post_tool.py`, `test_mark_verification_dirty.py`,
`test_stop_check.py`, and the Stop case in `test_idle_stdin.py` pass for
both implementations.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/scripts/hooks`

### U5. Registration of the eight hooks

Files: .claude/settings.json, .codex/hooks.json, tools/scripts/tests/hooks/test_registration.py, tools/hooks/pre-tool-policy.sh, tools/hooks/protect-generated-bash.sh, tools/hooks/go-format.sh, tools/hooks/proto-check.sh, tools/hooks/suppression-warn.sh, tools/hooks/mark-verification-dirty.sh, tools/hooks/commit-message-check.sh, tools/hooks/stop-check.sh, .agents/skills/verify-change/scripts/verify-change.sh, .agents/skills/verify-change/SKILL.md, .agents/skills/delegate/references/hookless-merge.md, docs/conventions/protobuf.md, test/conformance/a11y/a11y_policy_test.go, docs/agent-steering.md
After: U4
Change: Both configurations start the eight hooks as Decisions gives, and
the eight scripts are deleted. `worktree-guard.sh` and `create-worktree.sh`
keep their shell registration. The verifier rewrites the listing through
`uv run "$run_py" hooks tree-state "$root"` and fails on its non-zero
exit. The four documents and the comment name the commands.
`docs/agent-steering.md` records the two Stop timings and the command.
Tests: `test_registration.py` expects the new entries, runs each runtime's
configured `pre-tool-policy` command on a `generated/` edit (substituting
`${CLAUDE_PROJECT_DIR}` itself for Claude Code), asserts every registered
name is in `COMMANDS`, and runs the Codex command outside a repository and
with a `PATH` lacking `uv`. The table cases keep their literals. Nothing in
this unit covers Claude Code's own substitution. Verification's session
check does.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/settings.json .codex/hooks.json tools/scripts/tests/hooks tools/hooks .agents/skills/verify-change docs/conventions/protobuf.md test/conformance/a11y/a11y_policy_test.go docs/agent-steering.md .agents/skills/delegate/references/hookless-merge.md`

### U6. The worktree hooks and the end of `tools/hooks/`

Files: tools/scripts/hooks/__init__.py, tools/scripts/hooks/worktree_guard.py, tools/scripts/hooks/create_worktree.py, tools/scripts/hooks/pre_tool_policy.py, .claude/settings.json, .codex/hooks.json, tools/scripts/tests/hooks/test_registration.py, tools/scripts/tests/hooks/support.py, tools/scripts/tests/hooks/test_pre_tool_policy.py, tools/scripts/tests/hooks/test_post_tool.py, tools/scripts/tests/lib/test_treestate.py, tools/hooks/worktree-guard.sh, tools/hooks/create-worktree.sh, tools/hooks/common.sh, tools/hooks/tree-state.sh, .agents/skills/verify-change/scripts/verify-change.sh, .agents/skills/verify-change/SKILL.md, AGENTS.md, docs/agent-steering.md, .agents/skills/steer/SKILL.md, .agents/skills/delegate/SKILL.md, .agents/skills/implement/SKILL.md, tools/scripts/skills/next/plan_queue.py, tools/scripts/tests/skills/next/test_plan_queue.py, .agents/skills/delegate/references/hookless-merge.md, .agents/skills/tune/references/calibration/brief.md, .agents/skills/tune/references/calibration/brief-complex.md, .agents/skills/tune/references/calibration/brief-integration.md, .agents/skills/tune/references/calibration/brief-sensitive.md, .agents/skills/tune/references/calibration/brief-simple.md
After: U5
Change: `hooks worktree-guard` and `hooks create-worktree` replace the last
two scripts in both configurations. `create-worktree` prints only the
target path on standard output and exits 1 with the shell's messages.
`tools/hooks/` no longer exists. `support.implementations` returns the
Python command alone and fails on an unregistered name.
`test_treestate.py` compares against the listing recorded as a literal.
The policy list in `pre_tool_policy.py`, the path classes and the
`shellcheck` glob in the verifier, `AGENTS.md` (Hard boundaries, Enforced
rules), `HARNESS` in `plan_queue.py`, the policy case of
`test_pre_tool_policy.py` (`tools/hooks/new-guard.sh`), and the skill,
brief, and steering documents drop `tools/hooks/` and name
`tools/scripts/hooks/`. `test_post_tool.py` drops `jq` from its fixture
`PATH`.
Tests: `test_worktree.py` and the WorktreeCreate case of
`test_idle_stdin.py` pass for both implementations before the scripts are
deleted, the table case included. `test_registration.py` expects ten
exec-form entries for Claude Code. `git ls-files tools/hooks` prints
nothing, and `git grep -n tools/hooks` names only files under `docs/plans/`,
`docs/solutions/`, `docs/research/`, and `docs/architecture/`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh --full`

Waves: U1 U2 | U3 | U4 | U5 | U6

U3 to U6 form a chain because each edits `tools/scripts/hooks/__init__.py`
or a runtime configuration the next one edits.

## Verification

- `uv run tools/scripts/run.py test` passes after every unit.
- `git grep -w jq -- tools/scripts/hooks tools/scripts/lib
  tools/scripts/verify` prints nothing after U6.
- After U5 and after U6, in a Claude Code session started in the work
  tree: an Edit under `generated/` is denied, and the transcript shows no
  `hook error` notice. This is the check of the stop condition.
- After U6, `EnterWorktree` creates a worktree under the sibling
  `worktrees/<repo>/` directory.
- After U5, the Stop timing command from `docs/agent-steering.md`, run
  once cold and once warm.

## Definition of done

- [ ] The verifier is green for every changed path of each unit.
- [ ] Each unit was approved by diff before its commit.
- [ ] `AGENTS.md`, `docs/agent-steering.md`, `docs/code-style-python.md`,
      and the skills name the new commands.
- [ ] This plan's outcome is recorded with `uv run tools/scripts/run.py
      plan record implemented <plan> --units 6 --from <t> --to <t>` or
      `partial`.
- [ ] No plan labels in code.

## Open questions

- Claude Code proceeds when `uv` is not on `PATH`, so a host without uv
  has no guards and sees a `hook error` notice per call. The plan accepts
  this, since the shell registration fails open the same way and shell
  form would bring back the shell on Windows. Unconfirmed.
- What exec form does when `command` cannot be resolved is unverified. The
  reference describes the failure for shell form only.
- Codex starts hooks through `$SHELL -lc`. Whether the launcher string
  runs under a login shell that is not POSIX, such as fish, is unverified.
  The shell registration has the same exposure today.
- The `commandWindows` string for Codex is Phase 7's to write and run.
- The parent's Requirement 1 example says `hook worktree-guard`, and its
  U3 says both configurations register in exec form. The command is `hooks
  worktree-guard`, and Codex keeps a shell string because it has no exec
  form. The parent's text is left as it is.
