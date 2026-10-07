---
title: Repository Scripting - Direction
type: direction
date: 2026-10-05
topic: repository-scripting
status: proposed-direction
---

# Repository Scripting - Direction

FlowSeer's hooks, verifier, and skill scripts are written in two languages
and need four tools on the host: bash, `jq`, `python3`, and `shellcheck`.
This record fixes one language and one launcher for them, where they live,
and which scripts stay outside the rule.

## Context

The tree holds 37 shell scripts and 41 Python scripts
(`git ls-files '*.sh' '*.py'`). Three facts about them shape the decision:

- Four shell scripts already embed Python in here-documents, 678 lines in
  total. `.agents/skills/delegate/scripts/pool-usage.sh` is 567 lines, of
  which 534 are one `python3 - <<'PY'` block.
- Every Python script outside `src/edge/netpen/layers/harvest.py` imports
  only the standard library.
- Scripts already share code by path. `.agents/skills/tune/scripts/field.py`
  inserts `delegate/scripts` into `sys.path` to import `runlog`, and five
  scripts each parse plan frontmatter on their own.

None of the shell runs on Windows without Git Bash or WSL. The host-side
scripts call `jq` 126 times. Python alone leaves each host to install an
interpreter of a version the scripts were never tested on.

Facts from outside the repository:

- uv runs a script that declares its Python version and dependencies inline,
  and "the Python version will download if it is not installed"
  (https://docs.astral.sh/uv/guides/scripts/).
- uv's `required-version` setting makes uv "exit with an error" when its own
  version does not meet the requirement
  (https://docs.astral.sh/uv/reference/settings/). uv 0.12.23 reads a
  `uv.toml` at the repository root when it runs a script under that root
  from any working directory: a root `uv.toml` holding
  `required-version = ">=99.0.0"` fails `uv run tools/scripts/run.py` with
  `Required uv version`.
- A Claude Code command hook with an `args` field runs in exec form: the
  command "is resolved as an executable and spawned directly with no shell
  involved", on every platform (https://code.claude.com/docs/en/hooks).
- A hook that reads one JSON event from standard input took 20 ms as bash
  with `jq` and 24 ms as `uv run script.py` on macOS arm64, timing 30 runs
  of each with the interpreter cache warm. Startup does not separate the
  candidates.

## Decision

### Python launched by uv is the one runtime for host-side scripts

Every script a contributor or an agent runs on the host is Python, started
as `uv run <path>`. uv supplies the interpreter, so a host needs uv and
nothing else to run the hooks and the verifier's own logic. The gates still
call the tools they wrap (`go`, `buf`, `git`, `golangci-lint`, `pnpm`).

### Scripts live under `tools/scripts/` behind one entry point

```text
tools/scripts/
  run.py      the entry point, with the inline metadata block
  lib/        shared modules: hook events, git, plan files, processes, locks
  hooks/      one module per hook
  verify/     the verifier and its checks, the prose check among them
  skills/     one package per skill that owns commands
  tests/      unittest suites, mirroring the tree
```

`uv run tools/scripts/run.py <group> <command> [args]` starts every command.
Python puts the directory of `run.py` on the import path, so
`from lib import hookio` works in every module with no path edits. The
interpreter version and any dependency are declared once, in `run.py`.

Each group package lists its commands in a literal `COMMANDS` mapping in its
own `__init__.py`, and `run.py` collects the groups it finds. Two changes
that add commands to different groups then share no file. An unknown name
exits non-zero and prints the list, so a renamed command cannot be selected by an
old name and pass silently
(`docs/solutions/conventions/a-gate-selected-by-name-stops-running-silently.md`).

### Hooks import the shared library, and the library is a policy surface

`tools/scripts/hooks/`, `tools/scripts/verify/`, `tools/scripts/lib/`, and
`tools/scripts/run.py` are policy surfaces in the sense of `AGENTS.md`, Hard
boundaries. A hook's behavior changes when a module it imports changes, so
the prompt that guards a hook has to guard those modules too.

### Scripts use the standard library unless a statement admits a dependency

A third-party package in a script is a direct dependency under the
[dependency admission record](2026-10-01-dependency-admission-direction.md):
it needs a statement and a hash pin (`uv lock --script`). uv and the
interpreter are inputs of the "other inputs" kind that record names.
`required-version` in `uv.toml` sets a minimum uv version and
`requires-python` in `run.py` fixes the interpreter's minor version. Neither
is a hash pin. The exact bytes are recorded by whatever form that record's
work gives the Go toolchain and pnpm.

### A script coupled to its package stays beside it

A script that a package's own test, task file, or runbook runs stays in that
package as a standalone uv script with its own metadata block. It imports
nothing from `tools/scripts/`. The bench gates under `src/protocol/*/bench/`
and the lab scripts under `deploy/lab/` are of this kind.

### What stays outside the rule

- Scripts that run inside a container image: the snmpd pass-scripts and the
  clixon `startup.sh`. The image's shell runs them, on Linux, whatever the
  host is.
- Vendored files under `spec/`, such as
  `spec/yang/cisco/iosxe/2611/check-models.sh`.
- The finished research scripts under
  `docs/research/network-domain-atlas/_raw/`. They record how a research
  pass was computed and nothing invokes them.

`src/edge/netpen/layers/harvest.py` already follows the rule. It stays a
standalone uv script because scapy is the independent source its fixtures
are checked against.

## Alternatives

- TypeScript on Bun, with the frontend moved from pnpm to Bun. Bun 1.4,
  published 2026-08-20, "rewrites Bun in Rust", and 1.4.1 "fixes 202
  issues" two weeks later (https://bun.com/blog). Whether Storybook 10,
  Vitest 5, and `vue-tsc` run under the Bun runtime is unverified, and a
  frontend tool that needs Node would leave two runtimes. It also replaces
  `pnpm-lock.yaml`, which `tools/deps/inventory` parses and the dependency
  admission record names.
- TypeScript on Node, which runs `.ts` files directly from 22.18 and marks
  that stable from 24.12 (https://nodejs.org/api/typescript.html). It would
  share a runtime with the frontend. It means rewriting about 9,200 lines of
  Python and its tests on top of the shell, roughly three times the work,
  to remove a tool that only frontend contributors would otherwise skip.
- Go, which every contributor already has. `go run` took 74 ms for the same
  minimal hook, and text-processing scripts are longer in Go. The
  conformance gates stay in Go, where they test Go code.
- Keeping each script in its skill's directory with a path prelude. That is
  what `field.py` does today. Each script would carry its own metadata
  block, and on Windows the `.claude/skills` link to `.agents/skills` needs
  symbolic-link support in the checkout before any script path resolves.

## Consequences

- A frontend contributor still installs Node and pnpm. The single runtime
  covers scripts and hooks, and the web toolchain keeps its own.
- uv's cache directory must be writable wherever a script runs. Under the
  Claude Code sandbox the default `~/.cache/uv` is refused, and `uv run`
  exits 2 with `Failed to initialize cache`, so the sandbox configuration
  allows that path. The interpreter is a separate download that the sandbox
  network rules refuse, so it is installed once outside the sandbox.
- A skill no longer carries its scripts in its own directory. Its
  `SKILL.md` names commands of `run.py`.
- `shellcheck` and `jq` leave the verifier's required tools once the last
  host-side shell script is gone.
- Agent runtimes other than Claude Code start hooks their own way. How Codex
  runs a hook command on Windows is unverified.
