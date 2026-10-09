"""Fixtures and process helpers the verifier suites share.

A verifier case reaches the verifier through `implementations`, and fake
tools through `Stubs`, so a gate that starts is recorded without a Go
toolchain, a Docker daemon, or a web install.
"""

import functools
import os
import re
import shutil
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path
from typing import NamedTuple

import run

REPO = Path(__file__).resolve().parents[4]
RUN_PY = REPO / "tools" / "scripts" / "run.py"
VERIFY_GROUP = REPO / "tools" / "scripts" / "verify" / "__init__.py"

PASSED = "FlowSeer verification passed."
USAGE_OPTIONS = "[--full] [--print-selection] [--base REF] [-- PATH...]"
USAGE_LAST = "--print-selection reports the selected gates without running them or writing a receipt."
WEB_TOOLS = ("vue-tsc", "vite", "eslint", "stylelint", "prettier", "vitest")
STUB_NAMES = ("go", "gofumpt", "goimports", "golangci-lint", "docker", "node", *WEB_TOOLS)

# The verifier starts git and requires uv to start script suites.
# The go stub needs chmod, cp, dirname, and mkdir. Keeping only these on PATH
# lets cases expose missing gate tools.
ESSENTIAL_TOOLS = ("chmod", "cp", "dirname", "git", "mkdir", "uv")

# Each stub logs its working directory and arguments, tab separated.
LOG_PRELUDE = """\
name=${0##*/}
{
  printf '%s\\t%s' "$(pwd -P)" "$name"
  for arg do printf ' %s' "$arg"; done
  printf '\\n'
} >>"$FLOWSEER_STUB_LOG"
"""

# `list` and `env` go to the real go, unlogged: they are how the verifier
# selects, not a gate. `build -o` leaves an executable that logs itself, and
# `buf generate -o` copies the generated trees, so the drift gate compares
# equal trees.
GO_STUB = """\
#!/bin/sh
case "$1" in
  list|env)
    if [ -n "$FLOWSEER_FAKE_FAIL_ON_ARGS" ]; then
      case "$*" in *"$FLOWSEER_FAKE_FAIL_ON_ARGS"*) exit "${FLOWSEER_FAKE_FAIL_CODE:-1}" ;; esac
    fi
    exec @REAL_GO@ "$@"
    ;;
esac
@PRELUDE@
if [ -n "$FLOWSEER_FAKE_FAIL" ] && [ "$1" = "$FLOWSEER_FAKE_FAIL" ]; then
  exit "${FLOWSEER_FAKE_FAIL_CODE:-1}"
fi
out=
previous=
for arg do
  [ "$previous" = -o ] && out=$arg
  previous=$arg
done
if [ "$1" = build ] && [ -n "$out" ]; then
  cp "$(dirname "$0")/.check-guarantees" "$out"
  chmod +x "$out"
fi
case " $* " in
  *" buf generate "*)
    mkdir -p "$out"
    [ -d generated ] && cp -R generated "$out/generated"
    if [ -d frontend/web/generated ]; then
      mkdir -p "$out/frontend/web"
      cp -R frontend/web/generated "$out/frontend/web/"
    fi
    ;;
esac
exit 0
"""

BUILD_DIRECTORY = re.compile(r"\S*flowseer-build\.\w+")


class Result(NamedTuple):
    code: int
    stdout: str
    stderr: str


def implementations() -> list[list[str]]:
    """The argument lists that start the verifier: `verify change` once registered."""
    commands = []
    if "change" in run.read_commands(VERIFY_GROUP):
        # Resolved here because a case may give the verifier a PATH that holds no uv.
        commands.append([shutil.which("uv") or "uv", "run", str(RUN_PY), "verify", "change"])
    return commands


def across(case):
    """Run a case once per implementation, each on a fresh fixture, with `self.command` set.

    No implementation fails the case, so a verifier that is gone cannot pass silently.
    """

    @functools.wraps(case)
    def wrapper(self):
        commands = implementations()
        self.assertTrue(commands, "no verifier implementation exists")
        for index, command in enumerate(commands):
            if index:
                self.fresh()
            self.command = command
            with self.subTest(verifier=" ".join(command)):
                case(self)

    return wrapper


def isolated_env(**extra: str) -> dict[str, str]:
    """The caller's environment without the user's git configuration."""
    env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
    env["GIT_CONFIG_GLOBAL"] = os.devnull
    env["GIT_CONFIG_NOSYSTEM"] = "1"
    env.update(extra)
    return env


def last_line(text: str) -> str:
    """The last line of text, split on line feeds only so a carriage return stays in its line."""
    return text.rstrip("\n").split("\n")[-1]


def verdict(code: int, gate: str | None = None) -> str:
    """The last line of a failed run."""
    return f"FlowSeer verification FAILED (exit {code})." if gate is None else f"FlowSeer verification FAILED (exit {code}) in gate: {gate}"


def minimal_path(directory: Path, *tools: str) -> str:
    """A directory holding links to the named tools and nothing else."""
    directory.mkdir(parents=True, exist_ok=True)
    for tool in tools:
        found = shutil.which(tool)
        if found is None:
            raise AssertionError(f"{tool} is not installed")
        link = directory / tool
        if not link.exists():
            link.symlink_to(found)
    return str(directory)


class Stubs:
    """A directory of recording tools, and the environment that puts it first on PATH."""

    def __init__(self, directory: Path, log: Path):
        self.directory = directory
        self.log = log
        directory.mkdir(parents=True, exist_ok=True)
        self.real_go = shutil.which("go")

    def add(self, name: str, body: str = "") -> Path:
        """Write the tool `name`: it logs, then runs body, then exits 0."""
        if name == "go":
            (self.directory / ".check-guarantees").write_text(f"#!/bin/sh\n{LOG_PRELUDE}exit 0\n")
            text = GO_STUB.replace("@REAL_GO@", self.real_go or "go").replace("@PRELUDE@", LOG_PRELUDE.rstrip("\n"))
        else:
            text = f"#!/bin/sh\n{LOG_PRELUDE}{body}\nexit 0\n"
        return self.write(self.directory / name, text)

    def add_defaults(self, skip: tuple[str, ...] = ()) -> None:
        for name in STUB_NAMES:
            if name not in skip and name not in WEB_TOOLS:
                self.add(name)

    def add_web(self, root: Path) -> None:
        """The six web binaries under frontend/web/node_modules/.bin of root."""
        for name in WEB_TOOLS:
            self.write(root / "frontend/web/node_modules/.bin" / name, f"#!/bin/sh\n{LOG_PRELUDE}exit 0\n")

    def write(self, path: Path, text: str) -> Path:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        path.chmod(path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
        return path

    def env(self, base: dict[str, str], path: str | None = None, **extra: str) -> dict[str, str]:
        """base with this directory first on PATH, or in front of `path` when given."""
        rest = path if path is not None else base["PATH"]
        return {**base, "PATH": f"{self.directory}{os.pathsep}{rest}", "FLOWSEER_STUB_LOG": str(self.log), **extra}

    def calls(self, root: Path) -> list[str]:
        """The logged calls as `<directory relative to root><TAB><command line>`, with root and the build directory masked."""
        if not self.log.exists():
            return []
        lines = []
        for line in self.log.read_text(encoding="utf-8").split("\n")[:-1]:
            cwd, _, command = line.partition("\t")
            command = BUILD_DIRECTORY.sub("<build>", command.replace(str(root), "<root>"))
            lines.append(f"{os.path.relpath(cwd, root)}\t{command}")
        return lines


class VerifierCase(unittest.TestCase):
    """A repository on `main` with one commit, fake tools, and a way to run the verifier in it.

    `fresh` starts the repository over. A subclass fills it in `populate`,
    which runs before the first commit.
    """

    ROOT_NAME = "repo"

    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        # Resolved because git prints physical paths, and on macOS the
        # temporary directory sits behind a symlink.
        self.base = Path(directory.name).resolve()
        self.runs = 0
        self.fresh()

    def fresh(self):
        self.runs += 1
        self.parent = self.base / f"run{self.runs}"
        self.root = self.parent / self.ROOT_NAME
        self.tmp = self.parent / "tmp"
        self.root.mkdir(parents=True)
        self.tmp.mkdir()
        self.gopath = self.parent / "gopath"
        # The verifier puts $GOPATH/bin ahead of PATH, so the host's own Go
        # tools would hide the fake ones unless GOPATH points at an empty directory.
        self.env = isolated_env(TMPDIR=str(self.tmp), GOTOOLCHAIN="local", GOWORK="off", GOPATH=str(self.gopath))
        self.stubs = Stubs(self.parent / "stubs", self.parent / "calls.log")
        self.stubs.add_defaults()
        self.git("init", "-q", "-b", "main")
        self.populate()
        self.git("add", "-A")
        self.git("commit", "-qm", "init")

    def populate(self):
        self.write("README.md", "# Fixture\n")

    def git(self, *args: str, cwd: Path | None = None) -> str:
        done = subprocess.run(
            ["git", "-c", "user.name=Verify", "-c", "user.email=verify@example.invalid", *args],
            cwd=cwd or self.root,
            capture_output=True,
            text=True,
            check=False,
            env=self.env,
        )
        self.assertEqual(done.returncode, 0, f"git {' '.join(args)}: {done.stderr}")
        return done.stdout.strip()

    def write(self, path: Path | str, text: str = "", mode: int | None = None) -> Path:
        target = self.root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text)
        if mode is not None:
            target.chmod(mode)
        return target

    def populate_go(self):
        """One root module with a package."""
        self.write("go.mod", "module example.invalid/m\n\ngo 1.27\n")
        self.write("a.go", "package a\n")

    def populate_hook_tooling(self):
        """The two hook configuration files the JSON check parses, and a suite behind `tools/scripts/run.py`."""
        self.write(".claude/settings.json", "{}\n")
        self.write(".codex/hooks.json", "{}\n")
        (self.root / ".claude").mkdir(exist_ok=True)
        if not (self.root / ".claude/skills").is_symlink():
            (self.root / ".claude/skills").symlink_to("../.agents/skills")
        self.write("tools/scripts/run.py", self.suite_script(tests=1))

    def suite_script(self, tests: int, failing: bool = False) -> str:
        """A `tools/scripts/run.py` that reports a suite of `tests` tests."""
        verdict_text = "FAILED (failures=1)" if failing else "OK"
        return (
            "import sys\n"
            f"print('Ran {tests} tests in 0.001s')\n"
            f"print({verdict_text!r})\n"
            f"sys.exit({1 if failing else 0})\n"
        )

    def populate_full(self):
        """Everything a `--full` run finds: a Go module, the pinned buf module, schemas, mibgen, web, and hook tooling."""
        self.populate_go()
        self.populate_hook_tooling()
        self.write("tools/buf/go.mod", "module example.invalid/buf-tool\n\ngo 1.27\n")
        self.write("spec/proto/x/v1/a.proto", 'syntax = "proto3";\n')
        self.write("generated/go/proto/x.pb.go", "package proto\n")
        self.write("frontend/web/package.json", "{}\n")
        self.write(".gitignore", "node_modules/\n")
        self.stubs.add_web(self.root)

    def verify(self, *args: str, env: dict[str, str] | None = None, cwd: Path | None = None) -> Result:
        """Run the verifier of `self.command` with the fake tools first on PATH."""
        done = subprocess.run(
            [*self.command, *args],
            cwd=cwd or self.root,
            env=env if env is not None else self.stubs.env(self.env),
            capture_output=True,
            check=False,
        )
        return Result(done.returncode, done.stdout.decode("utf-8"), done.stderr.decode("utf-8"))

    def passes(self, *args: str, env: dict[str, str] | None = None) -> Result:
        """Run the verifier and require a pass, which ends standard output."""
        result = self.verify(*args, env=env)
        self.assertEqual(result.code, 0, result.stdout + result.stderr)
        self.assertEqual(last_line(result.stdout), PASSED)
        return result

    def fails(self, *args: str, code: int, gate: str | None = None, env: dict[str, str] | None = None) -> Result:
        """Run the verifier and require the code and a verdict line, naming gate when given, that ends standard error."""
        result = self.verify(*args, env=env)
        self.assertEqual(result.code, code, result.stdout + result.stderr)
        self.assertEqual(last_line(result.stderr), verdict(code, gate))
        return result

    def commit_all(self, message: str):
        self.git("add", "-A")
        self.git("commit", "-qm", message)

    def select(self, *args: str, env: dict[str, str] | None = None) -> str:
        """The selection the verifier prints for args, as `$(...)` returns it."""
        done = self.verify("--print-selection", *args, env=env)
        self.assertEqual(done.code, 0, done.stderr)
        return done.stdout.rstrip("\n")

    def calls(self) -> list[str]:
        return self.stubs.calls(self.root)

    @property
    def marker(self) -> Path:
        return self.root / ".git" / "flowseer-verification-dirty"

    @property
    def receipt(self) -> Path:
        return self.root / ".git" / "flowseer-verification-receipt"

    @property
    def tree_state(self) -> Path:
        return self.root / ".git" / "flowseer-tree-state"
