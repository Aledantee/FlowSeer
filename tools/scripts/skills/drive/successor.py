"""Start the next drive coordinator in this worktree after a phase lands."""

import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import time

from skills.delegate import orca_worker

PLAN_PARENT = re.compile(r"docs/plans/[^/]+-plan\.md")
EFFORT = re.compile(r"[a-z]+")
POLLS = 20
POLL_SECONDS = 3
CLIS = ("claude", "codex", "agy")
SECTIONS = ("pools", "models", "roles")


class Refusal(Exception):
    pass


class MayBeRunning(Exception):
    pass


class NoWorkingHint(Exception):
    pass


def remove_comment(line: str) -> str:
    quoted = False
    escaped = False
    for index, char in enumerate(line):
        if escaped:
            escaped = False
            continue
        if char == "\\" and quoted:
            escaped = True
            continue
        if char in "\"'":
            quoted = not quoted
        elif char == "#" and not quoted:
            return line[:index]
    return line


def split_fields(value: str) -> list[str]:
    fields = []
    start = 0
    depth = 0
    quoted = False
    escaped = False
    for index, char in enumerate(value):
        if escaped:
            escaped = False
            continue
        if char == "\\" and quoted:
            escaped = True
            continue
        if char in "\"'":
            quoted = not quoted
        elif not quoted and char in "[{":
            depth += 1
        elif not quoted and char in "]}":
            depth -= 1
        elif char == "," and not quoted and depth == 0:
            fields.append(value[start:index])
            start = index + 1
    fields.append(value[start:])
    return fields


def scalar(value: str):
    value = value.strip()
    if value.startswith("[") and value.endswith("]"):
        return [scalar(item) for item in split_fields(value[1:-1]) if item.strip()]
    if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
        return value[1:-1]
    return value


def inline_fields(value: str) -> dict:
    value = value.strip()
    if not (value.startswith("{") and value.endswith("}")):
        return {}
    fields = {}
    for item in split_fields(value[1:-1]):
        key, separator, raw = item.partition(":")
        if separator:
            fields[key.strip()] = scalar(raw)
    return fields


def parse_registry(path: Path) -> dict[str, dict]:
    data: dict[str, dict] = {section: {} for section in SECTIONS}
    section = None
    entry = None
    for raw_line in path.read_text().splitlines():
        line = remove_comment(raw_line).rstrip()
        if not line.strip():
            continue
        indent = len(line) - len(line.lstrip())
        text = line.strip()
        if indent == 0:
            key, separator, _ = text.partition(":")
            section = key if separator and key in data else None
            entry = None
            continue
        if section is None:
            continue
        if indent == 2:
            key, separator, raw = text.partition(":")
            if not separator:
                continue
            entry = key.strip()
            data[section][entry] = inline_fields(raw)
            continue
        if entry is not None and indent > 2:
            key, separator, raw = text.partition(":")
            if separator:
                data[section][entry][key.strip()] = scalar(raw)
    return data


def registry_paths() -> list[Path]:
    # The project file overlays the machine file entry by entry, so it is read last.
    home = Path(os.environ.get("HOME") or Path.home())
    found = []
    for path in (home / ".claude/models/registry.yaml", Path.cwd() / ".claude/models/registry.yaml"):
        if path.exists():
            if not path.is_file() or not path.stat().st_mode & 0o444:
                raise Refusal("model registry is not readable")
            found.append(path)
    if not found:
        raise Refusal("model registry is not readable")
    return found


def registry_info(model: str) -> tuple[str, str, str, list[str]]:
    effective: dict[str, dict] = {section: {} for section in SECTIONS}
    try:
        for path in registry_paths():
            parsed = parse_registry(path)
            for section in effective:
                effective[section].update(parsed[section])
    except (OSError, ValueError):
        raise Refusal("model registry is not readable") from None
    fit = effective["roles"].get("plan", {}).get("fit", [])
    if not isinstance(fit, list):
        fit = [fit]
    fields = effective["models"].get(model, {})
    pool = str(fields.get("pool", ""))
    cli = str(effective["pools"].get(pool, {}).get("cli", ""))
    entries = " ".join(map(str, fit)).split()
    return pool, cli, str(fields.get("id_format", "")), entries


def query(args: list[str]) -> subprocess.CompletedProcess | None:
    try:
        return orca_worker.run(args)
    except (OSError, ValueError):
        return None


def orca_result(args: list[str], failure: str) -> dict:
    result = query(["orca", *args])
    if result is None or result.returncode:
        raise Refusal(failure)
    try:
        return json.loads(result.stdout)["result"]
    except (KeyError, TypeError, ValueError):
        raise Refusal(failure) from None


def check_runtime_reachable():
    failure = "Orca runtime is not reachable"
    try:
        if orca_result(["status", "--json"], failure)["runtime"]["reachable"] is not True:
            raise Refusal(failure)
    except (KeyError, TypeError):
        raise Refusal(failure) from None


def check_no_children():
    failure = "child query returned no childWorktreeIds"
    try:
        children = orca_result(["worktree", "show", "--worktree", "active", "--json"], failure)["worktree"]["childWorktreeIds"]
        if not isinstance(children, list):
            raise KeyError("childWorktreeIds")
    except (KeyError, TypeError):
        raise Refusal(failure) from None
    remaining = "\n".join(str(child) for child in children).rstrip("\n")
    if remaining:
        raise Refusal(f"child worktrees remain: {remaining}")


def check_clean_tree():
    status = query(["git", "status", "--porcelain"])
    if status is None or status.returncode:
        detail = "" if status is None else (status.stdout + status.stderr).rstrip("\n")
        raise Refusal(f"cannot inspect the working tree: {detail}")
    # A warning on git's stderr also means the tree is not known to be clean.
    dirty = (status.stdout + status.stderr).rstrip("\n")
    if dirty:
        raise Refusal(f"working tree is dirty: {dirty}")


def parse_arguments(argv: list[str]) -> tuple[str, str, str, str | None]:
    if not argv:
        raise Refusal("parent is required")
    parent, rest = argv[0], argv[1:]
    values: dict[str, str] = {}
    while rest:
        flag = rest[0]
        if flag not in ("--cli", "--model", "--effort"):
            raise Refusal(f"unknown argument: {flag}")
        if len(rest) < 2:
            raise Refusal(f"{flag} requires a value")
        values[flag] = rest[1]
        rest = rest[2:]
    return parent, values.get("--cli", ""), values.get("--model", ""), values.get("--effort")


def coordinator_command(cli: str, model: str, effort: str | None, parent: str, id_format: str) -> str:
    launch_model = model
    line_effort = effort or ""
    if id_format:
        if effort is None:
            raise Refusal(f"effort is required for model id format: {model}")
        launch_model = id_format.replace("<effort>", effort)
        line_effort = ""
    try:
        launch = orca_worker.launch_line(cli, launch_model, line_effort)
    except orca_worker.WorkerError:
        raise Refusal("cannot build the coordinator launch line") from None
    prompt = shlex.quote(f"Read .claude/skills/drive/SKILL.md and drive {parent}.")
    # Unverified CLI contracts: Claude and Codex accept a positional prompt,
    # and agy accepts its initial prompt through --prompt-interactive.
    return f"{launch} --prompt-interactive {prompt}" if cli == "agy" else f"{launch} {prompt}"


def create_terminal(command: str) -> str:
    created = query(["orca", "terminal", "create", "--worktree", "active", "--title", "drive", "--command", command, "--json"])
    if created is None or created.returncode:
        raise MayBeRunning
    try:
        handle = json.loads(created.stdout)["result"]["terminal"]["handle"]
    except (KeyError, TypeError, ValueError):
        raise MayBeRunning from None
    if not isinstance(handle, str) or not handle:
        raise MayBeRunning
    return handle


def await_working_hint(handle: str):
    for _ in range(POLLS):
        screen = query(["orca", "terminal", "read", "--terminal", handle, "--screen"])
        if screen is not None and orca_worker.working(screen.stdout):
            return
        time.sleep(POLL_SECONDS)
    closed = query(["orca", "terminal", "close", "--terminal", handle, "--json"])
    if closed is None or closed.returncode:
        raise MayBeRunning(handle)
    raise NoWorkingHint


def start(argv: list[str]) -> str:
    parent, cli, model, effort = parse_arguments(argv)
    if not cli:
        raise Refusal("--cli is required")
    if not model:
        raise Refusal("--model is required")
    if cli not in CLIS:
        raise Refusal(f"unknown cli: {cli}")

    pool, expected_cli, id_format, fit = registry_info(model)
    if not any(entry.rpartition("@")[0] == model if "@" in entry else entry == model for entry in fit):
        raise Refusal(f"model is not in roles.plan.fit: {model}")
    if not pool or not expected_cli:
        raise Refusal(f"model has no pool and CLI in registry: {model}")
    if cli != expected_cli:
        raise Refusal(f"cli {cli} does not match model pool cli {expected_cli}: {model}")

    if effort is not None and not EFFORT.fullmatch(effort):
        raise Refusal(f"effort must contain lowercase letters: {effort}")
    if not PLAN_PARENT.fullmatch(parent) or not Path(parent).is_file():
        raise Refusal(f"parent must be an existing docs/plans/<name>-plan.md: {parent}")

    check_clean_tree()
    check_runtime_reachable()
    check_no_children()
    command = coordinator_command(cli, model, effort, parent, id_format)
    handle = create_terminal(command)
    await_working_hint(handle)
    return handle


def main(argv: list[str]) -> int:
    if orca_worker.windows():
        print("successor: Windows terminal launches are unsupported", file=sys.stderr)
        return 1
    try:
        print(start(argv))
        return 0
    except Refusal as error:
        print(f"successor: {error}", file=sys.stderr)
        return 1
    except NoWorkingHint:
        print("successor refused: coordinator did not show the working hint", file=sys.stderr)
        return 1
    except MayBeRunning as error:
        print(f"successor may be running: {error}" if error.args else "successor may be running", file=sys.stderr)
        return 2
