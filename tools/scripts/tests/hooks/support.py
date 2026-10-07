"""Fixtures and process helpers the hook suites share.

A hook case reaches its hook through `implementations`, so the same case runs
against every form of the hook that exists and fails when none does.
"""

import functools
import json
import os
import shutil
import signal
import subprocess
import tempfile
import unittest
from pathlib import Path
from typing import NamedTuple

import run

REPO = Path(__file__).resolve().parents[4]
HOOKS_DIR = REPO / "tools" / "hooks"
RUN_PY = REPO / "tools" / "scripts" / "run.py"
HOOK_GROUP = REPO / "tools" / "scripts" / "hooks" / "__init__.py"
BASH = shutil.which("bash") or "bash"

# A hook that waits on its input outlives this many seconds only when it is stuck.
IDLE_BOUND = 10
IDLE_TIMED_OUT = 124


class Result(NamedTuple):
    code: int
    stdout: str
    stderr: str


def registered() -> set[str]:
    """Names the `hooks` command group registers, or none before it exists."""
    if not HOOK_GROUP.is_file():
        return set()
    return set(run.read_commands(HOOK_GROUP))


def implementations(name: str) -> list[list[str]]:
    """Argument lists that start the hook `name`: the shell script while it exists, the command once registered."""
    found = []
    script = HOOKS_DIR / f"{name}.sh"
    if script.is_file():
        found.append([str(script)])
    if name in registered():
        # Resolved here because a case may give the hook a PATH that does not hold uv.
        found.append([shutil.which("uv") or "uv", "run", str(RUN_PY), "hooks", name])
    return found


def isolated_env(**extra: str) -> dict[str, str]:
    """The environment a hook runs in: the caller's, without the user's git configuration."""
    env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
    env["GIT_CONFIG_GLOBAL"] = os.devnull
    env["GIT_CONFIG_NOSYSTEM"] = "1"
    env.update(extra)
    return env


def encode(payload: object) -> bytes:
    """A payload as the bytes a runtime writes: a string verbatim, anything else as JSON."""
    return (payload if isinstance(payload, str) else json.dumps(payload)).encode("utf-8")


def run_command(
    command: list[str],
    payload: object = "",
    *,
    cwd: Path | None = None,
    env: dict[str, str] | None = None,
    timeout: float = 120,
) -> Result:
    """Run command with payload on standard input. Bytes are decoded by hand so a `\\r` survives."""
    done = subprocess.run(
        command,
        input=encode(payload),
        cwd=cwd,
        env=env,
        capture_output=True,
        timeout=timeout,
        check=False,
    )
    return Result(done.returncode, done.stdout.decode("utf-8"), done.stderr.decode("utf-8"))


def run_idle(command: list[str], *, cwd: Path, env: dict[str, str], bound: float = IDLE_BOUND) -> Result:
    """Run command on a standard input that stays open and silent, and give up after bound seconds."""
    with tempfile.TemporaryFile() as out, tempfile.TemporaryFile() as err:
        process = subprocess.Popen(
            command, stdin=subprocess.PIPE, stdout=out, stderr=err, cwd=cwd, env=env, start_new_session=True
        )
        try:
            code = process.wait(timeout=bound)
        except subprocess.TimeoutExpired:
            # A reader the hook started also holds the pipe, so the whole group goes.
            if hasattr(os, "killpg"):
                os.killpg(process.pid, signal.SIGKILL)
            else:
                process.kill()
            process.wait()
            code = IDLE_TIMED_OUT
        finally:
            if process.stdin:
                process.stdin.close()
        out.seek(0)
        err.seek(0)
        return Result(code, out.read().decode("utf-8"), err.read().decode("utf-8"))


def decision_of(result: Result) -> str:
    """The permission decision a hook printed, `allow` for no output, `output` for anything else."""
    if not result.stdout.strip():
        return "allow"
    return json.loads(result.stdout).get("hookSpecificOutput", {}).get("permissionDecision", "output")


class Hook:
    """One form of one hook."""

    def __init__(self, name: str, command: list[str], case: "HookCase"):
        self.name = name
        self.command = command
        self.case = case

    def __repr__(self) -> str:
        return " ".join(self.command)

    def run(
        self,
        payload: object = "",
        *,
        cwd: Path | None = None,
        env: dict[str, str] | None = None,
        timeout: float = 120,
    ) -> Result:
        return run_command(
            self.command,
            payload,
            cwd=cwd or self.case.parent,
            env=env or self.case.env,
            timeout=timeout,
        )

    def decide(self, payload: object, **kwargs) -> str:
        """Run the hook, require exit 0, and return its decision."""
        result = self.run(payload, **kwargs)
        self.case.assertEqual(result.code, 0, f"{self}: {result.stderr}")
        return decision_of(result)

    def output(self, payload: object, **kwargs) -> dict:
        """Run the hook, require exit 0, and return its output parsed as JSON."""
        result = self.run(payload, **kwargs)
        self.case.assertEqual(result.code, 0, f"{self}: {result.stderr}")
        return json.loads(result.stdout)

    def silent(self, payload: object, **kwargs) -> None:
        """Run the hook and require exit 0 and no output."""
        result = self.run(payload, **kwargs)
        self.case.assertEqual((result.code, result.stdout), (0, ""), f"{self}: {result.stderr}")

    def context(self, payload: object, **kwargs) -> str:
        """Run the hook, require exit 0, and return the context it added, or an empty string."""
        result = self.run(payload, **kwargs)
        self.case.assertEqual(result.code, 0, f"{self}: {result.stderr}")
        if not result.stdout.strip():
            return ""
        return json.loads(result.stdout)["hookSpecificOutput"]["additionalContext"]


def across(name: str):
    """Run a case once for each implementation of the hook `name`, each on a fresh fixture.

    The case receives a Hook. No implementation fails the case, so a hook that
    neither form provides cannot pass silently.
    """

    def decorate(case):
        @functools.wraps(case)
        def wrapper(self):
            commands = implementations(name)
            self.assertTrue(commands, f"no implementation of the hook {name}")
            for number, command in enumerate(commands):
                with self.subTest(hook=" ".join(command)):
                    if number:
                        self.fresh()
                    case(self, Hook(name, command, self))

        return wrapper

    return decorate


class HookCase(unittest.TestCase):
    """A repository in a directory with a space, and the payload shapes the runtimes send.

    `fresh` starts the fixture over, which `across` does between the forms of
    one hook so that the second does not see what the first wrote.
    """

    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        # Resolved because the hooks print physical paths, and on macOS the
        # temporary directory sits behind a symlink.
        self.base = Path(directory.name).resolve()
        self.runs = 0
        self.fresh()

    def fresh(self):
        self.runs += 1
        self.parent = self.base / f"run{self.runs}"
        self.parent.mkdir()
        # A `~` in a command resolves here once a case points HOME at it.
        self.home = self.parent / "home"
        self.home.mkdir()
        self.env = isolated_env()
        self.fixture = self.parent / "repository with spaces"
        self.fixture.mkdir()
        self.git(self.fixture, "init", "-q", "-b", "main")
        (self.fixture / "generated").mkdir()
        (self.fixture / "spec" / "proto").mkdir(parents=True)
        (self.fixture / "generated" / "device.pb.go").touch()
        (self.fixture / "README.md").write_text("# hook fixture\n")
        self.git(self.fixture, "add", "README.md", "generated/device.pb.go")
        self.git(self.fixture, "commit", "-qm", "init")
        self.linked_path = self.parent / "linked worktree"
        self.linked_made = False

    def git(self, repo: Path, *args: str, stdin: str | None = None) -> str:
        done = subprocess.run(
            ["git", "-c", "user.name=Hook", "-c", "user.email=hook@example.invalid", "-C", str(repo), *args],
            input=stdin,
            capture_output=True,
            text=True,
            check=False,
            env=self.env,
        )
        self.assertEqual(done.returncode, 0, f"git {' '.join(args)}: {done.stderr}")
        return done.stdout.strip()

    @property
    def linked(self) -> Path:
        """A linked worktree of the fixture, made on first use."""
        if not self.linked_made:
            self.git(self.fixture, "worktree", "add", "-qb", "hook-test", str(self.linked_path))
            (self.linked_path / "generated").mkdir(exist_ok=True)
            self.linked_made = True
        return self.linked_path

    def make_repo(self, name: str) -> Path:
        """An empty repository beside the fixture."""
        repo = self.parent / name
        repo.mkdir()
        self.git(repo, "init", "-q", "-b", "main")
        return repo

    def write(self, path: Path, text: str = "", mode: int | None = None) -> Path:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        if mode is not None:
            path.chmod(mode)
        return path

    def stub(self, directory: Path, name: str, body: str) -> Path:
        """An executable shell script `name` in directory."""
        return self.write(directory / name, f"#!/usr/bin/env bash\n{body}\n", 0o755)

    def env_with(self, **extra: str) -> dict[str, str]:
        return {**self.env, **extra}

    def path_with(self, *directories: Path) -> str:
        """The caller's PATH behind directories."""
        return os.pathsep.join([*(str(d) for d in directories), self.env["PATH"]])

    def path_of(self, directory: Path, *tools: str) -> str:
        """A PATH holding links to `tools` and nothing else."""
        directory.mkdir(parents=True, exist_ok=True)
        for tool in tools:
            found = shutil.which(tool)
            self.assertIsNotNone(found, f"{tool} is not installed")
            link = directory / tool
            if not link.exists():
                link.symlink_to(found)
        return str(directory)

    def bare_path(self, name: str) -> str:
        """A PATH of the tools the shell hooks use and a `go` that answers nothing, so no Go tool directory is added to it."""
        directory = self.parent / name
        path = self.path_of(directory, "bash", "cat", "jq", "git", "dirname", "basename", "grep", "awk", "sort", "sed")
        self.stub(directory, "go", "exit 0")
        return path

    # Payload shapes. A runtime sends more keys than these, and no hook reads them.

    def edit(self, path: Path | str, cwd: Path | None = None) -> dict:
        return {"cwd": str(cwd or self.fixture), "tool_input": {"file_path": str(path)}}

    def shell(self, command: str, cwd: Path | None = None) -> dict:
        return {"cwd": str(cwd or self.fixture), "tool_input": {"command": command}}

    def patch(self, *lines: str, cwd: Path | None = None) -> dict:
        return self.shell("\n".join(["*** Begin Patch", *lines, "*** End Patch"]), cwd)

    def guard(self, tool: str, cwd: Path | None = None, event: str = "PreToolUse", **tool_input: str) -> dict:
        return {
            "cwd": str(cwd or self.fixture),
            "hook_event_name": event,
            "tool_name": tool,
            "tool_input": tool_input,
        }

    def stop(self, cwd: Path | None = None) -> dict:
        return {"cwd": str(cwd or self.fixture), "hook_event_name": "Stop"}
