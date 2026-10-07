---
name: Python Script Style
last_updated: 2026-10-06
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
  host needs uv and nothing else.
- **Use the standard library.** A third-party package is a direct dependency
  and needs a statement and a hash pin under the dependency admission record.
  Without one, a script runs the same on every host.
- **A command is a module with `main(argv: list[str]) -> int`.** The group
  package lists it in its literal `COMMANDS` mapping. A literal can be
  listed and counted, and a function with no process state is testable
  without a subprocess.
- **Pass a subprocess an argument list.** `lib/proc.py` has no parameter
  that takes a shell string, so no argument is parsed as shell syntax.
- **Build paths with `pathlib`.** It behaves the same on Windows and
  POSIX, where string concatenation does not.
- **Exit non-zero on any failure and name it on standard error.** A caller
  that sees exit 0 may rely on the check having run.
- **Do no module-level work beyond definitions.** An import then has no
  side effect, so a test imports a command without running it.
- **Write tests with `unittest` under `tools/scripts/tests/`**, in packages
  that mirror the tree. Every existing suite is unittest, and it is in the
  standard library. `uv run tools/scripts/run.py test` runs them and fails
  on zero tests.
- **A package-local script carries its own metadata block and imports
  nothing from `tools/scripts/`.** A bench gate or a lab script that its
  package runs then works without the rest of the tree.
