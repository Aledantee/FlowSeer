---
name: Python Script Style
last_updated: 2026-10-07
---

# FlowSeer - Python Script Style

Conventions for the host-side scripts under `tools/scripts/`: hooks, the
verifier, and skill commands. The direction is recorded in
[the repository scripting record](architecture/2026-10-05-repository-scripting-direction.md).
The comment discipline and the *Rules for coding agents* in
[`code-style.md`](code-style.md) apply here as in every language: comments
explain why, with no process narration, no planning identifiers, and no TODOs.

## Rules

- **Start every script through `uv run`.** `uv run tools/scripts/run.py <group>
  <command>` gives each host the interpreter that `run.py` declares, so a
  host needs uv and nothing else. Run `uv python install 3.13` once per host
  so uv finds the interpreter `run.py` pins.
- **Give a skill's commands one package under `skills/<skill>/`.**
  `tools/scripts/skills/<skill>/` holds the `COMMANDS` mapping and the command
  modules, and its tests mirror it under `tools/scripts/tests/skills/<skill>/`.
  `run.py` reads each `COMMANDS` by parsing `__init__.py`, so a skill group
  has the same shape as a `tools/scripts/<group>/` one.
- **Use the standard library.** A third-party package is a direct dependency
  and needs a statement and a hash pin under the dependency admission record.
  Without one, a script runs the same on every host.
- **A command is a module with `main(argv: list[str]) -> int`.** `argv` holds
  the arguments after the command name, and `argparse` takes
  `prog="run.py <group> <command>"`. The group
  package lists it in its literal `COMMANDS` mapping. A literal can be
  listed and counted, and a function with no process state is testable
  without a subprocess.
- **Pass a subprocess an argument list.** `lib/proc.py` has no parameter
  that takes a shell string, so no argument is parsed as shell syntax.
- **Build paths with `pathlib`.** It behaves the same on Windows and
  POSIX, where string concatenation does not.
- **Exit non-zero on any failure and name it on standard error.** A caller
  that sees exit 0 may rely on the check having run.
- **A hook is a command of the `hooks` group and reads its event through
  `lib/hookio.py`.** `hookio.read_event` gives the first byte of standard
  input five seconds and then reads to the end, so a silent stream cannot
  hold the tool call behind it. It reads the descriptor, never `sys.stdin`,
  because a thread left blocked inside that stream aborts the interpreter at
  exit. A hook's exit code follows the runtime's hook contract, which is the
  exception to the rule above. A guard that cannot decide prints a decision
  with `hookio.deny` and exits 0, since a failure code other than 2 lets the
  tool call through.
- **Do no module-level work beyond definitions in a command or library
  module.** An import then has no side effect, so a test imports a command
  without running it. `run.py` is the one exception: it sets
  `sys.dont_write_bytecode` before its imports so no `__pycache__/` appears.
- **Write tests with `unittest` under `tools/scripts/tests/`**, in packages
  that mirror the tree. Every existing suite is unittest, and it is in the
  standard library. `uv run tools/scripts/run.py test` runs them and fails
  on zero tests.
- **A package-local script carries its own metadata block and imports
  nothing from `tools/scripts/`.** A bench gate or a lab script that its
  package runs then works without the rest of the tree.
- **Write no host-side shell script and no bare `python3` line.**
  `test/conformance/scripting/` fails a tracked or new file that is a shell
  script (a `.sh`, `.bash`, or `.zsh` name, or a `#!` line naming a shell), a
  command that starts `python` or `python3` instead of `uv run`, and a `.py`
  file outside `tools/scripts/` that lacks an inline metadata block. Its
  exempt list names the files that must stay shell, each with its reason: the
  snmpd pass-scripts and the clixon `startup.sh`, which run inside container
  images without uv, vendored files under `spec/`, and `docs/research/`.
  Adding an entry changes the scripting record.
