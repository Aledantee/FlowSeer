---
title: Python Scripts Under uv Phase 1, Entry Point and Library - Plan
type: refactor
date: 2026-10-05
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: mixed
parent: docs/plans/2026-10-05-2216-refactor-uv-python-scripts-plan.md
---

# Python Scripts Under uv Phase 1, Entry Point and Library - Plan

## Goal

`uv run tools/scripts/run.py <group> <command>` exists, is pinned, runs in
the sandbox, and is covered by the verifier, with one real check moved onto
it as proof. The means: a dispatcher with a registry, two shared modules, a
`uv.toml`, one sandbox path, and a verifier gate over `tools/scripts/`.

**Stop condition:** `uv run` cannot start a command inside the Claude Code
sandbox even with its cache path allowed. Every later phase assumes it can.

## Decisions

The parent plan's Decisions and the
[repository scripting record](../architecture/2026-10-05-repository-scripting-direction.md)
apply. Local to this phase:

- `run.py` carries the only inline metadata block, with
  `requires-python = "==3.13.*"` and an empty `dependencies` list. Why: one
  minor version gives every host the same standard library, and uv resolves
  3.13 on the development host today (`uv run` of a script requiring
  `>=3.13` reports `(3, 13, 2)`).
- `uv.toml` at the repository root holds `required-version` as a lower
  bound. Why: uv
  0.12.23 reads it for a script under that root from any working directory,
  as the record's Context shows. The bound is the newest uv release at
  least 14 days old on the day the unit is implemented
  (https://github.com/astral-sh/uv/releases), following
  `docs/conventions/dependencies.md`, Change a pin. uv 0.12.23 is dated
  2026-10-03 and is too new to be the bound before 2026-10-17.
- `python-downloads` stays at its default, `automatic`. Why: a Windows host
  has no interpreter until uv fetches one
  (https://docs.astral.sh/uv/guides/scripts/).
- The sandbox allows writes to `~/.cache/uv`, beside the Go and pnpm caches
  already listed in `.claude/settings.json`. Why: without it `uv run` exits
  2 with `Failed to initialize cache at ~/.cache/uv`. `uv run --no-cache`
  works without the path and took 57 ms against 25 ms for the same command,
  and it rebuilds the environment on every hook call.
- The interpreter is installed outside the sandbox, once per host, with
  `uv python install 3.13`. Why: the sandbox allows the network to the npm
  registry only (`.claude/settings.json`), so a first sandboxed `uv run` on
  a host without 3.13 fails on the download. Where uv keeps managed
  interpreters is unverified, and the sandbox needs to read that directory,
  not write it.
- `run.py` sets `sys.dont_write_bytecode = True` before it imports a
  command. Why: a `__pycache__` directory in the tree marks the checkout
  dirty, which is why the verifier passes `-Xpycache_prefix` today
  (`.agents/skills/verify-change/scripts/verify-change.sh:941`).
- Each group is a package under `tools/scripts/` whose `__init__.py` holds
  a literal `COMMANDS` mapping from command name to module name. `run.py`
  collects every such package outside `lib/` and `tests/`, and the group
  name is the package's directory name with hyphens. A command module
  exposes `main(argv: list[str]) -> int`. Why: a literal can be listed and
  counted, two units that add commands to different groups share no file,
  and a module with one function is testable without a subprocess.
- Command names use hyphens and module names underscores:
  `verify check-markdown-links` loads `verify/check_markdown_links.py`.
- Tests are `unittest`, under `tools/scripts/tests/` in packages that
  mirror the tree. Why: every existing suite is unittest, and it is in the
  standard library.
- The pilot is `check-markdown-links.py`. Why: it has one caller
  (`verify-change.sh:612`), 53 lines, and no test, so the move is small and
  adds coverage.
- `tools/scripts/run.py`, `tools/scripts/lib/`, `tools/scripts/verify/`,
  and `tools/scripts/hooks/` become policy surfaces in this phase. Why: the
  verifier runs code from the first three as soon as the pilot lands.

## Requirements

1. A registered command runs. Example: `uv run tools/scripts/run.py verify
   check-markdown-links docs/README.md` exits 0 on the current tree.
2. An unknown group or command exits 2 and prints the registry. Example:
   `uv run tools/scripts/run.py verify no-such-check` prints a line
   `verify check-markdown-links` on standard error and exits 2.
3. `list` prints one line per registered command, sorted. Example: its
   output after this phase is `list`, `test`, and
   `verify check-markdown-links`, each on its own line.
4. `test` runs every suite under `tools/scripts/tests/`, or under a start
   directory given as its argument, and fails on zero tests. Example:
   `uv run tools/scripts/run.py test <empty package directory>` exits
   non-zero with `ran zero tests`.
5. A registry entry whose module is missing fails the suite. Example:
   adding `"ghost": "ghost"` to `verify/__init__.py` with no such file
   fails `test_registry_modules_import`.
6. The command runs from any working directory. Example: from `docs/`,
   `uv run ../tools/scripts/run.py list` prints the same lines.
7. A host with an older uv is refused. Example: with `required-version`
   edited to `>=99.0.0`, any command exits 2 with `Required uv version`.
8. The command runs in the sandbox. Example: `uv run tools/scripts/run.py
   list` exits 0 from a sandboxed Bash call in a Claude Code session.
9. A change under `tools/scripts/` selects a gate. Example:
   `verify-change.sh -- tools/scripts/lib/repo.py` compiles every module
   under `tools/scripts/` and runs `run.py test`, and reports the count.
10. An edit to a new policy surface prompts. Example: a `PreToolUse` event
    for `Edit` on `tools/scripts/lib/repo.py` makes `pre-tool-policy.sh`
    answer `ask`.
11. No run leaves a `__pycache__` directory in the tree. Example: after
    `uv run tools/scripts/run.py test`, `git status --porcelain` is empty.

## Out of scope

- Moving any script other than the pilot. U2 of the parent does that.
- The hook library (`lib/hookio.py`), plan-file parsing (`lib/plans.py`),
  and the lock (`lib/lock.py`). Each lands with the phase that first needs
  it.
- A statement for uv or the interpreter, as the parent's Open questions
  say.
- `run.py` reads its arguments from a contributor or an agent runtime and
  files from the repository's own tree. Both are trusted.

## Units

### U1. Entry point, library, uv pin, and the verifier gate

Files: uv.toml, .claude/settings.json, tools/scripts/run.py, tools/scripts/lib/__init__.py, tools/scripts/lib/repo.py, tools/scripts/lib/proc.py, tools/scripts/tests/__init__.py, tools/scripts/tests/test_run.py, tools/scripts/tests/lib/__init__.py, tools/scripts/tests/lib/test_repo.py, tools/scripts/tests/lib/test_proc.py, .agents/skills/verify-change/scripts/verify-change.sh, .agents/skills/verify-change/scripts/test_verify_paths.py, .agents/skills/verify-change/references/gate-coverage.md
After: none
Change: `uv.toml` holds `required-version`. The sandbox `allowWrite` list
holds `~/.cache/uv`. `run.py` holds the metadata block, the registry, and
the built-in commands `list` and `test`. It resolves the command, imports
its module, calls `main` with the remaining arguments, and exits with the
returned code. `lib/repo.py` returns the repository root from
`git rev-parse --show-toplevel` and the tracked files under a path from
`git ls-files`. `lib/proc.py` runs an argument list and returns the exit
code, standard output, and standard error, and has no parameter that
accepts a shell string. The verifier classifies `uv.toml` and
`tools/scripts/*` with the hook tooling paths (`verify-change.sh:297` and
`:336` classify `tools/hooks/*` today), prints `hook_tooling=true` in its
`--print-selection` output when that class is selected, requires `uv` for
it, compiles every `.py` file under `tools/scripts/` with the
`-Xpycache_prefix` flag the skill scripts get at `verify-change.sh:941`,
and runs `uv run tools/scripts/run.py test` with the same
zero-test guard the skill suites have at `verify-change.sh:950`.
Every edit to `.claude/settings.json` and to the verifier's scripts is to a
policy surface and prompts the person.
Tests: `test_run.py` covers requirements 2 to 6 by running `run.py` as a
subprocess through `uv run`, with a temporary empty package for
requirement 4, and `test_registry_modules_import` imports
every registered module. `test_repo.py` runs against a temporary git
repository. `test_proc.py` asserts that an argument holding `; echo x` is
passed as one argument. `test_verify_paths.py` gains a case that a path
under `tools/scripts/`, and `uv.toml`, each print `hook_tooling=true` under
`--print-selection`. `tools/hooks/tests/run.sh:860` and `:944` read that
output, and the unit confirms the added line does not break their parsing.
Requirements 7, 8, and 11 are checked by hand and the result goes in the
ledger note with the host state it was checked on (whether 3.13 was
already installed), since a test cannot change the uv version or leave the
sandbox. Requirement 11 is `git status --porcelain` printing nothing after
a verifier run over `tools/scripts`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- uv.toml .claude/settings.json tools/scripts .agents/skills/verify-change`

### U2. Python convention

Files: docs/code-style-python.md, docs/README.md
After: none
Change: `docs/code-style-python.md` states the rules for scripts: started
through `uv run`, standard library unless a dependency statement exists, a
command is a module with `main(argv) -> int`, subprocesses take an argument
list, paths go through `pathlib`, a command exits non-zero on any failure
and names it on standard error, no module-level work beyond definitions,
tests are unittest under `tools/scripts/tests/`, and a package-local script
carries its own metadata block and imports nothing from `tools/scripts/`.
Each rule carries its reason. The comment rules of `docs/code-style.md`
apply. `docs/README.md` gains the row beside `code-style-web.md`.
Tests: the prose and link checks the verifier runs on Markdown.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/code-style-python.md docs/README.md`

### U3. Policy surfaces and repository guidance

Files: AGENTS.md, tools/hooks/pre-tool-policy.sh, tools/hooks/tests/run.sh
After: U2
Change: `AGENTS.md` lists `docs/code-style-python.md` under Conventions,
adds `tools/scripts/run.py`, `tools/scripts/lib/`, `tools/scripts/verify/`,
and `tools/scripts/hooks/` to the policy surfaces under Hard boundaries,
and names `tools/scripts/` under Layout. `pre-tool-policy.sh` matches the
same four paths in its policy-surface pattern (`pre-tool-policy.sh:54`).
Every edit in this unit is to a policy surface and prompts the person.
Tests: `tools/hooks/tests/run.sh` gains cases that an edit to
`tools/scripts/lib/repo.py`, `tools/scripts/run.py`, and
`tools/scripts/hooks/x.py` each answer `ask`, and that
`tools/scripts/tests/test_run.py` and `tools/scripts/skills/x.py` do not.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- AGENTS.md tools/hooks`

### U4. Pilot: the Markdown link check

Files: tools/scripts/verify/__init__.py, tools/scripts/verify/check_markdown_links.py, tools/scripts/tests/verify/__init__.py, tools/scripts/tests/verify/test_check_markdown_links.py, .agents/skills/verify-change/scripts/check-markdown-links.py, .agents/skills/verify-change/scripts/verify-change.sh
After: U1
Change: the check moves to `verify/check_markdown_links.py` as a module
with `main`, registered in `verify/__init__.py` as `check-markdown-links`.
The old file is deleted. `verify-change.sh:612` calls `uv run
tools/scripts/run.py verify check-markdown-links` with the same arguments,
and `need_tool uv` replaces `need_tool python3` at `:611`, so a docs-only
run on a host without uv stops at the tool check. Behavior is unchanged:
same findings, same exit codes. The verifier edit is to a policy surface
and prompts the person.
Tests: `test_check_markdown_links.py` builds a temporary tree and covers a
link to a present file, a link to a missing file (exit non-zero, path and
line named), a link with a fragment (the fragment is dropped and the path
checked, as `check-markdown-links.py:26` to `:29` do), an `http` link
(skipped), and a link inside a fenced block (skipped, `:19` to `:23`).
Before the move, the same cases run against the old
script by path, and the ledger note records that they passed.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/scripts .agents/skills/verify-change docs/README.md`

Waves: U1 U2 | U3 U4

## Verification

- `uv run tools/scripts/run.py test` reports the suites of U1 and U4.
- `uv run tools/scripts/run.py list` prints three lines.
- `.claude/skills/verify-change/scripts/verify-change.sh -- docs` passes
  with the link check running from its new place.
- `tools/hooks/tests/run.sh` passes with the new policy cases.
- By hand, in a sandboxed Bash call: `uv run tools/scripts/run.py list`
  exits 0.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `docs/code-style-python.md`, `AGENTS.md`, and `gate-coverage.md`
      describe what landed.
- [ ] The parent's `Landed:` line for U1 holds the commit range.
- [ ] This plan's `status` is set, with an outcome note under its title.
- [ ] No plan labels in code.

## Open questions

- Does the sandbox on Linux refuse the same cache path? Unverified. The
  entry `~/.cache/uv` matches uv's documented default on Linux and macOS
  (https://docs.astral.sh/uv/reference/settings/), and a host that sets
  `XDG_CACHE_HOME` needs its own entry.
