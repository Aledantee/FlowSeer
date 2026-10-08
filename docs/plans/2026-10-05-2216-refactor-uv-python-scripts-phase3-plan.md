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
  (`8c4c85ea:tools/hooks/worktree-guard.sh:200`, `:233`). The loop also
  expands filename patterns in the session directory, and it keeps a name as
  one word whatever the name holds. The port runs that loop. `words` starts
  `bash -c 'for tok in $1; do printf "%s\0" "$tok"; done' _ <command>` in
  the checkout and splits the output on NUL. The command is a positional
  argument and never part of the script, so nothing in it is evaluated. A
  non-zero exit or a missing `bash` returns `None`, and both callers then
  keep the denial. The property: `words` gives the words bash's unquoted
  expansion of the command gives in the checkout. Why: `rm *.tmp` is allowed
  only when every match is untracked, and a split on line feeds cuts a
  tracked `a\nb.tmp` into the untracked words `a` and `b.tmp`. No file name
  holds a NUL. This replaces the Decision that named `glob.glob`, whose
  bracket and `^` rules are not bash's.
  (decided by the user, 2026-10-08)
- The loop gives these words under `/bin/bash` 3.2.57 on macOS, in a
  repository that tracks `a\nb.tmp` and holds an untracked `plain.tmp` and
  an untracked directory `a\n` with `out.tmp` in it. The last column is the
  form it replaces, `printf "%s\n" $1` split on line feeds.

  | Command | Words from the loop | Words from the replaced form |
  | --- | --- | --- |
  | `rm *.tmp` | `rm`, `a\nb.tmp`, `plain.tmp` | `rm`, `a`, `b.tmp`, `plain.tmp` |
  | `rm a?b.tmp` | `rm`, `a\nb.tmp` | `rm`, `a`, `b.tmp` |
  | `touch a?/out.tmp` | `touch`, `a\n/out.tmp` | `touch`, `a`, `/out.tmp` |
  | `rm c?d.tmp` beside `c\rd.tmp` | `rm`, `c\rd.tmp` | not taken |
  | `rm nomatch*.zip` | `rm`, `nomatch*.zip` | not taken |
  | an empty command, and one of blanks and line feeds | none, exit 0 | not taken |

  `git ls-files -- a\nb.tmp` names the tracked file for the loop's word and
  prints nothing for `a` or for `b.tmp`, so `rm *.tmp` is denied with the
  loop and allowed with the replaced form. `a\n/out.tmp` is a relative
  word, so `writes_only_outside` sees no outside path and the denial stays.
  A `bash` that exits 9 gives exit status 9 and no output, and a `bash` that
  is not on `PATH` raises `OSError`. A command given as `rm $(touch
  evaluated) ;touch e3` comes back as its five words and creates no file.
  A command that holds a NUL cannot be an argument, and `subprocess.run`
  raises `ValueError` for it, which `main` already catches.
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
6. `words` gives the words bash's unquoted expansion of the command gives in
   the checkout. Example: in a primary checkout that tracks `a\nb.tmp` and
   holds an untracked `plain.tmp`, `words("rm *.tmp", checkout)` is `["rm",
   "a\nb.tmp", "plain.tmp"]`, and the Bash call `rm *.tmp` is denied.

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

U1 to U6 are built on this branch, in `d99b72b8..e2d38ece` and the fix
commits `cc4094f0` and `3cdc9f28`, and are not built again. The Decisions
that name them describe that work. One unit is open.

### U7. The guard's words from the shell's own loop

Files: tools/scripts/hooks/worktree_guard.py, tools/scripts/tests/hooks/test_worktree.py
After: none
Change: `worktree_guard.words` runs the loop Decisions gives, in the
directory it is passed, with the command as the one positional argument. It
decodes the output with `surrogateescape`, splits it on NUL, and drops the
empty piece after the last NUL. It returns `None` when `bash` cannot be
started and when `bash` exits non-zero. It has one path: the early return
for a command without a word goes, together with the `WORDS` pattern, since
the loop gives no word for such a command and `reason_to_deny` never sends
one. `deletes_only_untracked` and `writes_only_outside` keep their `names
is None` branch. The docstring of `words` states the property of
Requirement 6. The unit stops uncommitted with the verifier green, as
Decisions says for a policy surface. It closes the Review gaps items at
`test_worktree.py:663`, `test_worktree.py:666`, and `worktree_guard.py:232`.
Tests: all in `test_worktree.py`.
- `WordsTest` keeps its matrix against the `bash` on `PATH`, with the
  expected list taken from the loop and split on NUL. Its fixture gains
  `a\nb.tmp`, `c\rd.tmp`, and a directory `a\n` holding `out.tmp`. Its
  patterns gain `*.tmp`, `a?b.tmp`, `c?d.tmp`, `a?/out.tmp`, an empty
  command, and a command of blanks and line feeds.
- A second case holds literals that no `bash` call supplies, in a
  directory holding `a\nb.tmp`, `plain.tmp`, `c\rd.tmp`, and `a\n/out.tmp`:
  the first four rows of the table in Decisions with the words as
  written there, `rm nomatch*.zip` as its two words, and an empty list for
  the empty and the blank command. Why: the matrix and `words` share the
  loop, so the matrix alone passes whatever the two share.
- `words` returns `None` with `PATH` set to an empty directory, and with a
  `bash` stub first on `PATH` that prints `rm` and a NUL and exits 9.
- Through the hook, in a primary checkout on `main` that tracks `a\nb.tmp`
  and holds an untracked `plain.tmp` and an untracked directory `a\n` with
  `out.tmp`: `rm *.tmp`, `rm a?b.tmp`, and `touch a?/out.tmp` are denied,
  and `rm plain.tmp` prints nothing.
- Through the hook, with a `PATH` that holds `git` and `uv` and no `bash`:
  `rm <untracked file>` and `touch <outside>/f` are denied. The same two
  calls print nothing with `bash` on `PATH`.
- Through the hook, with the stub that exits 9 first on `PATH`: `rm
  README.md` and `rm <untracked file>` are denied.
- The cases for a caret in a bracket expression and for a command that is
  never evaluated stay as they are.
- Each of these changes to `worktree_guard.py` fails a case: either
  `return None` in `words` replaced by `return []`, `return False` after
  `names is None` replaced by `return True` in either caller, the split on
  NUL replaced by a split on line feeds or by `.splitlines()`, the exit
  status not read, the command written into the script, and `cwd` dropped.
- Nothing in the unit covers a `bash` other than the one on the `PATH` of
  the host that runs the suite.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/scripts/hooks/worktree_guard.py tools/scripts/tests/hooks/test_worktree.py`

Waves: U7

## Verification

- `uv run tools/scripts/run.py test` passes.
- `git grep -w jq -- tools/scripts/hooks tools/scripts/lib
  tools/scripts/verify` prints nothing.
- In a Claude Code session started in the work tree: an Edit under
  `generated/` is denied, and the transcript shows no `hook error` notice.
  This is the check of the stop condition.
- In a Claude Code session started in the primary checkout on `main`,
  after U7: `rm` of an untracked file runs, `rm README.md` is denied, and
  the transcript shows no `hook error` notice.
- `EnterWorktree` creates a worktree under the sibling `worktrees/<repo>/`
  directory.
- The Stop timing command from `docs/agent-steering.md`, run once cold and
  once warm.

## Definition of done

- [ ] The verifier is green for every changed path of each unit.
- [ ] Each unit was approved by diff before its commit.
- [ ] `AGENTS.md`, `docs/agent-steering.md`, `docs/code-style-python.md`,
      and the skills name the new commands.
- [ ] This plan's outcome is recorded with `uv run tools/scripts/run.py
      plan record implemented <plan> --units 1 --from <t> --to <t>` or
      `partial`.
- [ ] No plan labels in code.

## Open questions

- The loop is checked under `/bin/bash` 3.2.57 only. Its words under bash 5
  are unverified. The matrix of U7 runs against the `bash` on `PATH`, so it
  holds the property on each host that runs the suite.
- A `BASH_ENV` file that sets `nullglob` or `IFS` changes the words:
  with `shopt -s nullglob` in it, `rm nomatch*.zip` gives the one word
  `rm`. The shell hook started under `bash` and read the same file, so the
  verdict does not differ from the shell. The plan accepts this.
  Unconfirmed.
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

## Review gaps

Open items of the review of `f9663a99...b4a80a84`, which accepted the phase
with no fix round. None holds the verdict. The first twelve were left by the
review of `8c4c85ea..633242b6` and its two fix rounds and still hold, with
their line numbers at `b4a80a84`. The last three are new. U7 closed the
three items on `worktree_guard.words`: each recorded mutation now fails a
case in `test_worktree.py`.

- tools/scripts/tests/hooks/test_mark_verification_dirty.py:67: the append in `mark_edit` wrapped in `except OSError: pass`; fails: an Edit event with the marker path a directory, expecting a non-zero exit; class: gap
- tools/scripts/hooks/mark_verification_dirty.py:106: the marks are one write where the shell appended one line per `printf`, so an append that fails after the first line drops the later marks while the listing is saved; fails: an append that fails once after `a.go`, expecting `b.go` marked; class: gap
- tools/scripts/tests/hooks/test_pre_tool_policy.py:169: the `except Exception` deny replaced by `raise` at `pre_tool_policy.py:115` and at `protect_generated_bash.py:121`; fails: a case that makes each guard raise and expects `deny`; class: gap
- tools/scripts/tests/hooks/test_protect_generated_bash.py:215: the `MUTATE` reason sentence replaced by `CHANGED REASON.`; fails: a table row that pins the reason of each branch that prints one; class: gap
- tools/scripts/tests/hooks/test_stop_check.py:126: `failed_gate` runs every gate and returns the first failure's output; fails: a recording `go` stub under two gate directories, expecting one `go test` call; class: false test
- tools/scripts/tests/hooks/test_post_tool.py:226: the hook prints `{"hookSpecificOutput":{"additionalContext":""}}` where it printed nothing, also at `:284`, `:286`, `:289`, `:291`; fails: `hook.silent` on those calls; class: false test
- tools/scripts/tests/verify/test_service_otel_integration.py:107: the wrapper also prints a line on standard output, also at `:114`, `:127`, `:149`, `:161`, `:173`; fails: an empty standard output beside the exact standard error; class: false test
- tools/scripts/tests/hooks/test_idle_stdin.py:93: the `*.sh` scan and `BARE_READ` search a directory that holds no shell script; fails: none, the lines cannot fail; class: convention
- tools/scripts/lib/hookio.py:125: git output read in text mode (`lib/proc.py:20`) turns a carriage return into a line feed, also at `worktree_guard.py:152`, `create_worktree.py:69` and `:125`, `mark_verification_dirty.py:53`, `proto_check.py:52`, `stop_check.py:52` and `:88`; fails: an Edit of `<root>/generated/x` in a checkout directory named with a carriage return, expecting `deny`; class: convention
- tools/scripts/hooks/go_format.py:64: standard output is printed before standard error where the shell merged them in the order written, also at `proto_check.py:113`; fails: a stub that writes error, output, error, expecting that order; class: convention
- tools/scripts/lib/hookio.py:106: a field of the wrong JSON type, a second JSON value, a line feed inside or after a field, and a NUL in a command give a verdict the shell did not, also at `hookio.py:193`, `worktree_guard.py:174` and `:228` (an uncaught `ValueError: embedded null byte`, exit 1 and no decision), `create_worktree.py:84`, `stop_check.py:124`, `treestate.py:35`; fails: a Write with `file_path: 7` in the primary checkout, expecting `deny`; class: hardening
- tools/scripts/hooks/worktree_guard.py:328: `CLAUDE_WORKTREE_GUARD_BRANCHES` is split on every blank and its words are not expanded; fails: the value `ma*` beside a file named `main`, expecting `deny`; class: hardening
- tools/scripts/tests/hooks/test_protect_generated_bash.py:202: `data is False` dropped from the malformed check at `protect_generated_bash.py:115`; fails: the input `false`, expecting `deny`; class: gap
- docs/plans/2026-10-05-2216-refactor-uv-python-scripts-phase3-plan.md:287: U7 says the literal rows hold "the words as written there" in a directory that also holds `c\rd.tmp`, where `rm *.tmp` gives the four words `test_worktree.py:741` asserts and the table's row gives three for its own fixture; fails: none; class: convention
- docs/plans/2026-10-05-2216-refactor-uv-python-scripts-phase3-plan.md:105: "which `main` already catches" is wrong, since `worktree_guard.main` catches only the JSON error at `:367` and a NUL in a command ends the hook with exit 1 and no decision; fails: none; class: convention
