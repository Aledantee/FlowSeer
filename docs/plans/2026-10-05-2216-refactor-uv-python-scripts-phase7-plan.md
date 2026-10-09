---
title: Python Scripts Under uv Phase 7, Layout Gate and Windows Run - Plan
type: refactor
date: 2026-10-05
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Python Scripts Under uv Phase 7, Layout Gate and Windows Run - Plan

## Goal

The tree cannot regress: a gate fails a host-side shell script or a bare
`python3` invocation, the verifier no longer requires `jq` or `shellcheck`,
and the hooks and suites have run on a Windows host.

**Stop condition:** a host-side script the tree needs cannot be written as
Python under uv, so the exempt list would have to grow for a script the
record does not name.

## Decisions

- The parent plan's Decisions and the
  [repository scripting record](../architecture/2026-10-05-repository-scripting-direction.md)
  apply.
- The gate is a Go test under `test/conformance/`, beside the layout
  checks the Stop hook already runs, so it needs no new wiring: the Stop
  gate runs every package under `test/conformance/`.
- The exempt paths are a literal list in the gate, each with its reason:
  the snmpd pass-scripts, the clixon `startup.sh`, vendored files under
  `spec/`, and `docs/research/`. The list is the rule the record states,
  written down once. Adding an entry is a change to the record.
- The gate also fails a tracked `.py` file outside `tools/scripts/` that
  lacks an inline metadata block, with `docs/research/` exempt.
- `CONTRIBUTING.md` and `README.md` name uv as the script runtime and give
  the install command per platform from https://docs.astral.sh/uv/.
- The Windows run is done by a person on a Windows host, and its commands
  and results go into `docs/agent-steering.md`. A failure there reopens the
  phase that owns the failing command.
- The repository scripting record gains its `Landed` lines.

The re-plan of 2026-10-09 settles the rest:

- The gate is the package `test/conformance/scripting/`. Why:
  `tools/scripts/hooks/stop_check.py:68` enumerates the directories under
  `test/conformance/`, and `tools/scripts/hooks/pre_tool_policy.py:43`
  lists only `test/conformance/a11y/` as a policy surface, so the new
  package runs at Stop with no hook edit and no approval prompt.
- The gate lists files with
  `git ls-files -z --cached --others --exclude-standard`. Why: a file is
  judged when it is created, before `git add`, and git already knows what
  `.gitignore` hides (`frontend/web/node_modules/` holds shell scripts). A
  failing `git` fails the gate, since a skipped gate reads as a pass.
- The gate reads with `os.ReadDir` the repository root and every directory
  on the path of each listed file outside the exempt trees. Why: `go test`
  caches a pass and rechecks only what the test opened or stat'ed inside
  the module root. For an opened directory it hashes the entry names
  (`cmd/go/internal/test/test.go:2056` to `:2115` in Go 1.27.1, under
  `$(go env GOROOT)/src`). Output of a child `git` is not an input, so
  without the reads the Stop hook, which runs `go test` with no `-count=1`,
  would report a cached pass after `tools/scripts/x.sh` appears. About
  1,700 directories are read. One case stays cached: a file created in a
  directory that held no listed file and whose parent's entries do not
  change, such as a directory holding only ignored files. The verifier's
  `-count=1` run catches it, and so does the Stop after any listed change.
- A shell script is a file whose name ends in `.sh`, `.bash`, or `.zsh`,
  or whose first line is `#!` and names `sh`, `bash`, `zsh`, `dash`, or
  `ksh` as the interpreter. A first line that names `python` or `python3`
  fails as a bare interpreter. Why: a script without a suffix would
  otherwise pass, and `src/edge/netpen/tools/l2l3-audit/l2l3-audit:1`
  shows the accepted shape, `#!/usr/bin/env -S uv run --quiet --script`.
- A bare interpreter call is `python3` or `python` as a command word, in
  one of two places. The first is the start of a line, after blanks, an
  optional `$ ` prompt, and any `NAME=value` words. In a `.yml` or `.yaml`
  file a leading `- ` is skipped too. The second is inside the string of a
  `"command":` key, at its start or after a blank or one of `;`, `&`, `|`,
  and `(`. A trailing carriage return is trimmed from every line first.
  Why: the first is how a `SKILL.md` or a runbook starts a script, a task
  file writes `- COUNT=1 uv run bench-gate.py`
  (`src/protocol/smi/bench/Taskfile.yml:54`), and a hook is an exec form
  (`.claude/settings.json:33`) or a shell string (`.codex/hooks.json:9`).
  A Markdown bullet is prose, so `- ` is skipped only in YAML. The tree
  holds no `.gitattributes`, so a Windows checkout may hold carriage
  returns. The scanned set is the parent's
  Requirement 4: every listed file under `.agents/`, `.claude/`,
  `.codex/`, and `tools/`, every `Taskfile.yml`, `AGENTS.md`,
  `CONTRIBUTING.md`, and `docs/runbooks/`. A file holding a NUL byte and a
  path that is not a regular file (the `.claude/skills` link) are skipped.
  `git grep -n -E '^\s*(\$ )?(python3?|bash|sh) '` over that set prints
  nothing on `2795fa9c`.
- `bash` and `sh` at a line start do not fail. Why: the parent's
  Requirement 4 names Python, a line `bash x.sh` already fails through the
  script it names, and `sh` opens a device command in a runbook
  (`sh ip route`).
- A metadata block is a line `# /// script` followed later by a line
  `# ///`. Why: uv's guide gives that form and says "The `dependencies`
  field must be provided even if empty"
  (https://docs.astral.sh/uv/guides/scripts/, fetched 2026-10-09). The
  gate checks the two delimiter lines and leaves the fields to uv, which
  refuses a block it cannot read. A trailing carriage return is trimmed
  before the comparison. The eight `.py` files outside
  `tools/scripts/` and `docs/research/` all carry one today.
- One exempt list serves the three checks, and an entry that matches no
  listed file fails the gate. Why: an exemption nothing uses is a rule
  nobody reads.
- The shellcheck gate is deleted and nothing lints shell. In its place the
  hook-tooling gate runs
  `go test -count=1 ./test/conformance/scripting/`. Why: no host-side
  shell is left to lint, and a change to a `SKILL.md` selects no Go module,
  so `check_repository_wide` (`tools/scripts/verify/change.py:970`) would
  not run the gate for it. A path that selects neither hook tooling nor a
  Go module (`CONTRIBUTING.md`, a runbook, a `Taskfile.yml`,
  `deploy/lab/x.py`) gets the gate from the Stop hook and from
  `go test ./...` only. That is accepted, since both run before a change
  lands.
- `jq empty` is replaced by a new command, `verify check-json`, which
  parses each named file with `json.loads` and names the path, line, and
  column of the first error. Why: the verifier prints each gate as a
  command a person can run again (`run_check`, `change.py:807`). It is
  stricter than `jq empty`: an empty file and two values in one file now
  fail. Each of the two files is one JSON document, so that is intended.
- `python3` leaves `required_tools` and `change.py:1243` with no
  replacement. Why: nothing in `tools/scripts/verify/` starts `python3`.
  The entries came from the shell verifier.
- U2 and U3 edit policy surfaces. Each worker stops uncommitted, the
  coordinator shows the user the diff, and the approval names the blobs
  (`git hash-object`) the commit must match. Both run on Claude workers.
  Why: `docs/agent-observations.md`, the entry of 2026-10-07 on
  policy-surface units, records that a hook's `ask` does not reach a
  person inside a worker.
- Follow-ups of the earlier phases taken here, because a unit already
  edits the file or the fix is one line:
  - Phase 4, all five: the stale comment at
    `tools/scripts/hooks/pre_tool_policy.py:28` (U3), and in `change.py`
    the six section-divider comments, the tree-state failure that drops
    its cause (`:1171`), the explicit path that keeps a trailing line feed
    (`:500`), and `os.listdir` outside the `try` in `_diff_pair` (`:380`)
    (U2).
  - Phase 3, one of fifteen: a NUL in a Bash command ends
    `worktree_guard.words` with `ValueError: embedded null byte`
    (`tools/scripts/hooks/worktree_guard.py:228`), and the hook exits 1
    with no decision. `subprocess.run(['bash','-c','true','_','rm a\0b'])`
    raises that error under uv's Python 3.13 on this host.
- The other fourteen phase 3 items stay out. Why: eight are test gaps or
  false tests in suites no unit here edits, three change a hook's output
  order or text decoding across several modules, the wider wrong-type and
  NUL handling in `lib/hookio.py` needs the decision
  `docs/agent-observations.md` asks for in its entry of 2026-10-09, and two
  are sentences in a plan that is retired. `steer` works them from that
  queue.
- `AGENTS.md` is not edited. Why: its Layout section already states that
  host-side scripts are Python behind `run.py`, and
  `docs/code-style-python.md` is where the gate is named.

## Requirements

1. A new host-side shell script fails the gate. Example:
   `tools/scripts/x.sh` fails with the path named.
2. A bare interpreter call fails the gate. Example: a `SKILL.md` line
   starting `python3 ` fails, and `uv run tools/scripts/run.py` passes.
3. The verifier's required tools hold no `jq`, `shellcheck`, or `python3`.
4. On Windows with uv, git, and Go installed and no Git Bash: `uv run
   tools/scripts/run.py test` reports no failure, the verifier passes on
   `docs`, and an edit under `generated/` is denied in a Claude Code
   session.
5. A tracked `.py` file outside `tools/scripts/` without a metadata block
   fails the gate. Example: `deploy/lab/x.py` holding `print(1)` fails with
   the path named, and `deploy/lab/write-registry.py` passes.

## Out of scope

- Making the hooks and the verifier portable before the Windows run. U5
  finds what fails, and Open questions lists what reading the code
  predicts.
- A linter for the exempt shell scripts. The container images run them and
  their integration suites exercise them.
- The gate reads paths and files of the repository's own tree, written by
  contributors, who are trusted. It catches a script added by habit, not
  one hidden on purpose: a shell script named `x.txt` with no first-line
  `#!` passes.
- `verify check-json` reads the two hook configuration files, written by
  contributors. It reports malformed JSON and judges no key or value.

## Units

### U1. Scripting gate

Files: test/conformance/scripting/scripting_test.go, docs/code-style-python.md, docs/agent-steering.md
After: none
Change: `TestScriptingLayout` lists the files as the Decisions say and
reports one finding per offending path through `t.Error`: a shell script,
a bare interpreter first line, a bare interpreter call with its line
number, and a `.py` file without a metadata block. A pure function takes
the listed paths and a reader and returns the findings, so the table test
needs no file in the tree. The exempt list is a slice of entries, each a
path prefix or exact path with its reason as a comment:
`src/protocol/snmp/test/integration/testdata/snmpd/pass-scripts/`,
`src/protocol/yang/test/integration/testenv/testdata/clixon/startup.sh`,
`spec/`, and `docs/research/`. The test fails when `git` fails, when the
listing is empty, and when an exempt entry matches no file.
`docs/code-style-python.md` gains the rule and the gate's path beside its
package-local script rule. `docs/agent-steering.md`, section
"`verify-change` and the hooks", gains the Stop gate's cold and warm
timings measured with the command printed there, since that section says
to measure again when a conformance package is added. A cold run over 24
seconds is a blocker to report, not a reason to edit the timeout in
`.claude/settings.json` or `.codex/hooks.json`, which this unit does not
own.
Tests: `TestFindings` in the same file, one row per rule and its
counterpart: `tools/scripts/x.sh` fails with the path in the finding and
`.../pass-scripts/endofmibview.sh` passes. A file `tools/x` whose first
line is `#!/bin/bash` fails and `#!/usr/bin/env -S uv run --script`
passes. A `SKILL.md` line `python3 a.py` fails with its line number, as do
`  $ python a.py`, `A=1 python3 a.py`, `- python3 a.py` in a
`Taskfile.yml`, `"command": "python3"`, and
`"command": "cd x && exec python3 a.py"`, while `- python3 is gone` in a
`SKILL.md`,
`uv run tools/scripts/run.py list`, `("python3 fix.py", "deny"),`, and
the same `python3` line in `docs/plans/x.md` pass. `deploy/lab/x.py`
holding `print(1)` fails, one holding the two delimiter lines passes, and
so does one whose lines end in a carriage return and a line feed.
`docs/research/a/b.py` without a block passes. An exempt entry with no
matching path is a finding. No row can prove the cache keying, so run this
by hand and expect the third run to fail without `(cached)`:
`go test ./test/conformance/scripting/` twice, then create an empty
`tools/scripts/x.sh` and run it again. Delete the file afterwards.
Verify: `uv run tools/scripts/run.py verify change -- test/conformance/scripting/scripting_test.go docs/code-style-python.md docs/agent-steering.md`

### U2. Verifier tools

Files: tools/scripts/verify/change.py, tools/scripts/verify/check_json.py, tools/scripts/verify/__init__.py, tools/scripts/tests/verify/support.py, tools/scripts/tests/verify/test_verify_gates.py, tools/scripts/tests/verify/test_check_json.py, tools/scripts/tests/verify/test_verify_paths.py, tools/scripts/tests/verify/test_change.py
After: U1
Change: `required_tools` returns no `jq`, `shellcheck`, or `python3`, and
`need_tool("python3")` is gone. `check_hook_tooling` runs
`verify check-json .claude/settings.json .codex/hooks.json` through
`run_check`, then `go test -count=1 ./test/conformance/scripting/`, then
the suites. `HOST_SCRIPT_PATTERNS`, `host_scripts`, and the
`shellcheck skipped` line are deleted. The `on_path` docstring states its
rule without naming `python3` as a required tool.
`verify/check_json.py` is registered as `check-json`: it exits 0 when
every named file parses as one JSON value, 1 with
`<path>:<line>:<column>: <message>` on standard error for the first error
in each file, 1 with `<path>: <error>` for a file it cannot read, and 2
with a usage line when no path is given. The four follow-ups: the six
section-divider comments in `Verification` are removed, a failed
tree-state listing prints its existing line and then a line holding the
error it caught, `collect_paths` strips trailing
line feeds from an explicit path before `canonical_path`, and
`_diff_pair` reports an unreadable directory as a `diff:` line with
status 2.
Tests: `support.py` drops both names from `STUB_NAMES`, and
`populate_hook_tooling` writes `.claude/settings.json` and
`.codex/hooks.json` as `{}` and no longer writes the two `.sh` fixtures,
so every fixture that selects hook tooling (`test_ledger.py` and
`test_verify_marker.py` through `populate_full`) has files to parse.
`check-json` runs inside the verifier's process, so its line never enters
the stub log. In `test_verify_gates.py` the hook-tooling log is the
`uv.toml` guard rows and
`.\tgo test -count=1 ./test/conformance/scripting/`, in the targeted case
and in `full_calls`, and
`+ run.py verify check-json .claude/settings.json .codex/hooks.json` is
asserted on standard output. The `skips_shellcheck` case is deleted. In
`test_missing_tools_stop_the_run_before_any_gate` the first run expects
`required tools are not on PATH: gofumpt goimports golangci-lint`, and
the second run, whose `PATH` holds no `jq`, `shellcheck`, or `python3`,
now passes. A case with a malformed `.codex/hooks.json` expects exit 1
and `in gate: run.py verify check-json`. `test_check_json.py` covers a
valid file, a truncated one with its line and column, an empty file, two
values in one file, a missing file, and no arguments.
`test_verify_paths.py` gains `-- $'README.md\n'` verifying `README.md`.
In `test_change.py` the existing listing-failure case
(`test_change.py:104`) also expects `listing failed` on standard error,
and a new case gives `diff_trees` a directory without read permission
under one side, expecting status 2 and a `diff:` line, skipped on Windows
where the mode bits do not deny a read.
Verify: `uv run tools/scripts/run.py verify change -- tools/scripts/verify/change.py tools/scripts/verify/check_json.py tools/scripts/verify/__init__.py tools/scripts/tests/verify/support.py tools/scripts/tests/verify/test_verify_gates.py tools/scripts/tests/verify/test_check_json.py tools/scripts/tests/verify/test_verify_paths.py tools/scripts/tests/verify/test_change.py`

### U3. Hook follow-ups

Files: tools/scripts/hooks/worktree_guard.py, tools/scripts/hooks/pre_tool_policy.py, tools/scripts/tests/hooks/test_worktree.py
After: none
Change: `worktree_guard.words` returns None when `subprocess.run` raises
`ValueError`, as it does for `OSError`, so its callers keep the denial.
The comment above `POLICY_FILES` in `pre_tool_policy.py` describes the
lists as they stand: it names `tools/scripts/verify/` as the gate an
agent's own work must pass and no longer mentions a verifier script or the
`.claude/skills` link.
Tests: `test_worktree.py` gains a Bash event in the primary checkout on a
protected branch whose command is `rm a\0b`, expecting exit 0 and a `deny`
decision. It fails on `2795fa9c` with exit 1 and no output.
Verify: `uv run tools/scripts/run.py verify change -- tools/scripts/hooks/worktree_guard.py tools/scripts/hooks/pre_tool_policy.py tools/scripts/tests/hooks/test_worktree.py`

### U4. Contributor documents and the record

Files: CONTRIBUTING.md, README.md, docs/architecture/2026-10-05-repository-scripting-direction.md
After: none
Change: `CONTRIBUTING.md`, Set up, says a host needs Go 1.27 and uv
0.12.18 or newer (`uv.toml:1`), that uv is the one runtime for the hooks,
the verifier, and every script, and that uv downloads the interpreter. It
gives the commands of https://docs.astral.sh/uv/getting-started/installation/
(fetched 2026-10-09) with that link:
`curl -LsSf https://astral.sh/uv/install.sh | sh` for macOS and Linux,
`powershell -ExecutionPolicy ByPass -c "irm https://astral.sh/uv/install.ps1 | iex"`
for Windows, and `brew install uv` and `winget install --id=astral-sh.uv -e`
as the package-manager forms. It adds to its hook-enforced boundaries
that a host-side shell script or a `python3` line fails
`test/conformance/scripting/`. `README.md`, Get the repository green,
names uv beside Go with the same link and one command per platform. The
record gains a `## Landed` section with one line per phase in the shape
the other records use (`Landed <date>: <what> in <paths>`): 2026-10-06
the entry point and shared library, 2026-10-07 the moved Python,
2026-10-08 the hooks, 2026-10-09 the verifier, 2026-10-07 the delegate,
drive, and tune commands, 2026-10-06 the package-local scripts, and this
phase's gate with the date it is implemented. Its Consequences line on
`shellcheck` and `jq` is put in the past tense.
Tests: none, the unit is prose. `verify check-markdown-links` and
`verify check-prose` run in the verifier.
Verify: `uv run tools/scripts/run.py verify change -- CONTRIBUTING.md README.md docs/architecture/2026-10-05-repository-scripting-direction.md`

### U5. Windows run

Files: docs/agent-steering.md
After: U1, U2, U3, U4

Owned by the user. No worker can do it, and `drive` parks here with U1 to
U4 implemented. The person runs the commands below in PowerShell on a
Windows host, on a checkout of the branch that holds U1 to U4, and pastes
each command's last lines and exit code (`$LASTEXITCODE`). A session then
writes them into `docs/agent-steering.md` and closes the unit.

| Step | Command | Expected |
| --- | --- | --- |
| 1 | `uv --version`, `git --version`, `go version` | uv 0.12.18 or newer, Go 1.27 |
| 2 | `where.exe bash` | no path and a non-zero exit (the message text is unverified). A `bash.exe` from WSL under `System32` is recorded, not removed |
| 3 | `uv run tools/scripts/run.py list` | the command list, exit 0 |
| 4 | `uv run tools/scripts/run.py test` | on standard error a `Ran <n> tests` line, then `OK` or `OK (skipped=<n>)`, exit 0 |
| 5 | `go test ./test/conformance/...` | `ok` for every package, exit 0 |
| 6 | `uv run tools/scripts/run.py verify change -- docs` | last line `FlowSeer verification passed.`, exit 0 |
| 7 | In a Claude Code session in the checkout: "create the file generated/x.txt holding one line" | the Write is denied with `generated/x.txt is generated output.` and the file does not exist |

Change: `docs/agent-steering.md`, section "`verify-change` and the hooks",
gains a dated paragraph naming the Windows version, the uv, git, Go, and
Claude Code versions, the seven commands, and each result. A step that
fails is recorded with its last lines. It does not fail this unit. It
opens new work, since the phase that owns the command is retired, and the
paragraph names the failing command and the module that printed the
error.
Tests: none, the unit records a manual run.
Verify: `uv run tools/scripts/run.py verify change -- docs/agent-steering.md`

Waves: U1 U3 U4 | U2 | U5

## Verification

- `go test -count=1 ./test/conformance/scripting/` passes on the tree.
- `uv run tools/scripts/run.py test` reports no failure.
- `uv run tools/scripts/run.py verify change --full` passes after U4, on a
  host whose `PATH` holds no `jq` and no `shellcheck`. On this host,
  `uv run tools/scripts/run.py verify change -- .claude/settings.json`
  prints the `check-json` line and no `jq` line.
- `git ls-files '*.sh'` lists eight paths, each under an exempt entry.
- The Windows run of U5.

## Definition of done

- [ ] The verifier is green for every changed path.
- [ ] `docs/code-style-python.md`, `CONTRIBUTING.md`, `README.md`, and the
      record are updated in the units that change what they describe.
- [ ] The commits of U2 and U3 hold the blobs named in their approval.
- [ ] The Windows results are in `docs/agent-steering.md`.
- [ ] This plan's outcome is recorded with `uv run tools/scripts/run.py
      plan record implemented <plan> --units 5 --from <t> --to <t>`, or
      `partial` while U5 waits.
- [ ] No plan labels in code.

## Open questions

- Reading the code predicts that steps 6 and 7 of U5 fail. Unverified, no
  Windows host is reachable. The hooks and the verifier test for an
  absolute path with `startswith("/")` (`tools/scripts/lib/hookio.py:215`,
  `tools/scripts/hooks/worktree_guard.py:267`,
  `tools/scripts/verify/change.py:296` and `:1111`), the verifier creates
  its build directory under `/tmp` when `TMPDIR` is unset (`change.py:1224`),
  and it joins `PATH` with `:` (`change.py:758`). The phase's Decisions
  say a failure reopens the owning phase, and those phases are retired, so
  the run is planned as written and a failure becomes a new plan. The user
  may prefer that plan before the run.
- `worktree_guard.words` starts `bash` (`worktree_guard.py:228`). On a
  host without it the callers keep the denial, so a Bash command the guard
  would have allowed on `main` is denied. U5 step 2 records whether the
  host has one.
- The record's status is `proposed-direction`. The gate enforces its
  exempt list, so accepting the record is a person's step at or before
  `land`.
- How Codex starts a hook on Windows stays unverified, as the parent says.
  U5 covers Claude Code only.
- Parked by drive: U5 is the user's Windows run; U1 to U4 are implemented
  on `parked/uv-p7-impl`. Options: run the seven U5 commands on a Windows host
  now and paste the results, so a session writes them into
  `docs/agent-steering.md` and the phase goes on to review (a predicted
  failure of steps 6 and 7 is recorded and opens new work) | plan the
  Windows path fixes first and run afterwards (delays this phase and the
  parent's retirement for work the run has not yet shown). Recommended: run
  now, because the run is the evidence the fixes would be planned from.

