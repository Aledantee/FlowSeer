# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Entry point for repository scripts: run.py <group> <command> [args]."""

import sys

# .gitignore hides __pycache__/, so one would not dirty the checkout, but
# without this flag the directories would collect in every checkout.
sys.dont_write_bytecode = True

import ast
import importlib
import unittest
from pathlib import Path

SCRIPTS = Path(__file__).resolve().parent
SKILLS = "skills"
NOT_GROUPS = frozenset({"lib", "tests", SKILLS})


class RegistryError(Exception):
    """A group package that cannot be read as a registry."""


def read_commands(init: Path) -> dict[str, str]:
    """Return the COMMANDS literal of a group's __init__.py without running it."""
    for node in ast.parse(init.read_text(encoding="utf-8"), str(init)).body:
        targets = node.targets if isinstance(node, ast.Assign) else []
        if not any(isinstance(target, ast.Name) and target.id == "COMMANDS" for target in targets):
            continue
        value = node.value
        pairs = list(zip(value.keys, value.values)) if isinstance(value, ast.Dict) else []
        if not isinstance(value, ast.Dict) or not all(
            isinstance(part, ast.Constant) and isinstance(part.value, str)
            for pair in pairs
            for part in pair
        ):
            raise RegistryError(f"{init} must hold COMMANDS as a dict literal of strings")
        return {key.value: item.value for key, item in pairs}
    raise RegistryError(f"{init} has no COMMANDS mapping")


def collect_groups(scripts: Path = SCRIPTS) -> dict[str, dict[str, str]]:
    """Return {group name: {command name: dotted module path}} for every group package."""
    found: dict[str, tuple[Path, str]] = {}
    for root, prefix in ((scripts, ""), (scripts / SKILLS, f"{SKILLS}.")):
        if not root.is_dir():
            continue
        for directory in sorted(root.iterdir()):
            init = directory / "__init__.py"
            if not init.is_file() or (root == scripts and directory.name in NOT_GROUPS):
                continue
            name = directory.name.replace("_", "-")
            if name in found:
                raise RegistryError(f"{init} repeats the group {name}, also at {found[name][0]}")
            found[name] = (init, f"{prefix}{directory.name}")
    return {
        name: {command: f"{package}.{module}" for command, module in read_commands(init).items()}
        for name, (init, package) in found.items()
    }


def registry_lines(groups: dict[str, dict[str, str]]) -> list[str]:
    lines = ["list", "test"]
    lines += [f"{group} {command}" for group, commands in groups.items() for command in commands]
    return sorted(lines)


def missing_init(start: Path) -> Path | None:
    """Return a directory below start that holds tests and that discovery would skip."""
    for test in sorted(start.rglob("test_*.py")):
        directory = test.parent
        while directory != start:
            if not (directory / "__init__.py").is_file():
                return directory
            directory = directory.parent
    return None


def run_tests(argv: list[str], scripts: Path = SCRIPTS) -> int:
    start = Path(argv[0]).resolve() if argv else scripts / "tests"
    if not start.is_dir():
        print(f"test: not a directory: {start}", file=sys.stderr)
        return 2
    skipped = missing_init(start)
    if skipped is not None:
        print(f"test: {skipped} holds tests and has no __init__.py", file=sys.stderr)
        return 2
    top = scripts if start.is_relative_to(scripts) and (start / "__init__.py").is_file() else start
    suite = unittest.TestLoader().discover(str(start), top_level_dir=str(top))
    result = unittest.TextTestRunner(stream=sys.stderr).run(suite)
    if result.testsRun == 0:
        print("test: ran zero tests", file=sys.stderr)
        return 1
    return 0 if result.wasSuccessful() else 1


def main(argv: list[str], scripts: Path = SCRIPTS) -> int:
    try:
        groups = collect_groups(scripts)
    except RegistryError as error:
        print(f"run.py: {error}", file=sys.stderr)
        return 2
    if argv == ["list"]:
        print("\n".join(registry_lines(groups)))
        return 0
    if argv[:1] == ["test"]:
        return run_tests(argv[1:], scripts)
    if len(argv) >= 2 and argv[1] in groups.get(argv[0], {}):
        if str(scripts) not in sys.path:
            sys.path.insert(0, str(scripts))
        return importlib.import_module(groups[argv[0]][argv[1]]).main(argv[2:])
    print(f"run.py: unknown command: {' '.join(argv) or '(none)'}", file=sys.stderr)
    print("\n".join(registry_lines(groups)), file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
