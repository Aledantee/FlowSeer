# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Entry point for repository scripts: run.py <group> <command> [args]."""

import sys

# A __pycache__ directory marks the checkout dirty, so no import writes one.
sys.dont_write_bytecode = True

import importlib
import unittest
from pathlib import Path

SCRIPTS = Path(__file__).resolve().parent
NOT_GROUPS = frozenset({"lib", "tests"})


class RegistryError(Exception):
    """A group package that cannot be read as a registry."""


def collect_groups(scripts: Path = SCRIPTS) -> dict[str, dict[str, str]]:
    """Return {group name: {command name: module name}} for every group package."""
    if str(scripts) not in sys.path:
        sys.path.insert(0, str(scripts))
    groups: dict[str, dict[str, str]] = {}
    for directory in sorted(scripts.iterdir()):
        if not (directory / "__init__.py").is_file() or directory.name in NOT_GROUPS:
            continue
        package = importlib.import_module(directory.name)
        commands = getattr(package, "COMMANDS", None)
        if not isinstance(commands, dict):
            raise RegistryError(f"{directory.name}/__init__.py has no COMMANDS mapping")
        groups[directory.name.replace("_", "-")] = dict(commands)
    return groups


def registry_lines(groups: dict[str, dict[str, str]]) -> list[str]:
    lines = ["list", "test"]
    lines += [f"{group} {command}" for group, commands in groups.items() for command in commands]
    return sorted(lines)


def run_tests(argv: list[str], scripts: Path = SCRIPTS) -> int:
    start = Path(argv[0]).resolve() if argv else scripts / "tests"
    if not start.is_dir():
        print(f"test: not a directory: {start}", file=sys.stderr)
        return 2
    top = scripts if start.is_relative_to(scripts) else start
    suite = unittest.TestLoader().discover(str(start), top_level_dir=str(top))
    result = unittest.TextTestRunner(stream=sys.stderr).run(suite)
    if result.testsRun == 0:
        print("test: ran zero tests", file=sys.stderr)
        return 1
    return 0 if result.wasSuccessful() else 1


def main(argv: list[str]) -> int:
    try:
        groups = collect_groups()
    except RegistryError as error:
        print(f"run.py: {error}", file=sys.stderr)
        return 2
    if argv == ["list"]:
        print("\n".join(registry_lines(groups)))
        return 0
    if argv[:1] == ["test"]:
        return run_tests(argv[1:])
    if len(argv) >= 2 and argv[1] in groups.get(argv[0], {}):
        module = importlib.import_module(f"{argv[0].replace('-', '_')}.{groups[argv[0]][argv[1]]}")
        return module.main(argv[2:])
    print(f"run.py: unknown command: {' '.join(argv) or '(none)'}", file=sys.stderr)
    print("\n".join(registry_lines(groups)), file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
