"""Verify a change by running every gate its paths select.

Usage:

    run.py verify change [--full] [--print-selection] [--base REF] [-- PATH...]

Without explicit paths the command verifies the files changed from REF
(default HEAD) and the untracked ones. `--full` selects every gate and every
Go module. `--print-selection` prints the selected gates and stops, with no
tool started and no receipt written.

A run that selects no gate fails with exit 2 and leaves the receipt and the
dirty marker as they were, since "checked nothing" must not read as "checked
and clean". A pass rewrites the tree-state listing, clears the marker lines
it verified, and writes the receipt that `verify ledger` reads. The last line
of standard error names the verdict and the gate that was running, so a log
read only at its tail cannot be mistaken.

A command that fails outside a gate (a package listing, the test-integrity
check, a Go file with no `go.mod` above it) ends the run with its status and
names no gate. A lookup that only selects, such as the listing of a nested
module's dependencies, never fails the run. Paths are strings decoded
with `surrogateescape`, so any byte string git reports round-trips.
"""

import contextlib
import datetime
import fnmatch
import io
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import traceback
from collections.abc import Callable, Iterable, Sequence
from pathlib import Path

from lib import treestate
from verify import (
    check_json,
    check_markdown_links,
    check_plan_state,
    check_plan_status,
    check_prose,
    check_test_integrity,
    service_otel_integration,
)

USAGE = """\
Usage: run.py verify change [--full] [--print-selection] [--base REF] [-- PATH...]

Without explicit paths, verify files changed from REF (default: HEAD), including
untracked files. --full verifies Go, protobuf, web, and agent hook tooling.
--print-selection reports the selected gates without running them or writing a receipt.
"""

BASH_MUTATION = "<Bash mutation; verify with --full>"
MARKER_NAME = "flowseer-verification-dirty"
RECEIPT_NAME = "flowseer-verification-receipt"
TREE_STATE_NAME = "flowseer-tree-state"
WEB_TOOLS = ("vue-tsc", "vite", "eslint", "stylelint", "prettier", "vitest")
GOPATH_HINT = "Go tools install to $(go env GOPATH)/bin; put that directory on PATH or install the missing ones there."
GO_LIST_ROWS = '{{.ImportPath}}|{{join .Deps " "}} {{join .TestImports " "}} {{join .XTestImports " "}}'

# The patterns of a `case` arm, where `*` crosses `/`. fnmatchcase has the
# same reading, and `glob` does not.
HOOK_MARKDOWN = (".agents/*", ".claude/*", ".codex/*", "CLAUDE.md", "AGENTS.md", "docs/agent-knowledge.md", "tools/scripts/*")
HOOK_TOOLING = (".agents/*", ".claude/*", ".codex/*", "tools/scripts/*", "uv.toml")
MIB = ("mibgen.yaml", "spec/mib/*", "src/protocol/snmp/cmd/mibgen/*", "src/protocol/smi/*")
SERVICE_OTEL = (
    "go.mod",
    "go.sum",
    "src/common/service/*.go",
    "src/common/service/test/integration/otel*",
    "src/common/service/test/integration/testdata/otel-collector.yaml",
    "tools/scripts/verify/service_otel_integration.py",
)
GENERATED_GO = ("generated/*", "frontend/web/generated/*")
GO_MODULE_FILES = ("go.mod", "go.sum", "*/go.mod", "*/go.sum")
PROTO_FILES = ("*.proto", "buf.yaml", "buf.work.yaml", "buf.gen.yaml")
UNSWEEPABLE_TAGS = frozenset(
    "go build ignore race linux darwin windows freebsd netbsd openbsd solaris aix plan9 js wasip1 amd64 arm64 arm "
    "386 riscv64 ppc64le ppc64 s390x mips64 mips wasm cgo unix purego".split()
)
MODULE_WIDE = re.compile(r"(^|/)(generated/|go\.mod\Z|go\.sum\Z|buf\.lock\Z)")
SUITE_RAN = re.compile(r"Ran [1-9][0-9]* tests?")

# `printf %q` of /bin/bash 3.2.57 escapes these anywhere in a word and `#` at its start.
QUOTED = frozenset(" !\"$&'()*,;<>?[\\]^`{|}")
ANSI_NAMES = {7: "\\a", 8: "\\b", 9: "\\t", 10: "\\n", 11: "\\v", 12: "\\f", 13: "\\r", 27: "\\E"}


class Stop(Exception):
    """End the run with this status, as `exit` does."""

    def __init__(self, status: int):
        super().__init__(status)
        self.status = status


def decode(data: bytes) -> str:
    return data.decode(treestate.ENCODING, errors=treestate.ERRORS)


def encode(text: str) -> bytes:
    return text.encode(treestate.ENCODING, errors=treestate.ERRORS)


def emit(stream, text: str) -> None:
    """Write one line, as bytes when the stream has them, and flush so child output keeps its place."""
    stream.flush()
    buffer = getattr(stream, "buffer", None)
    if buffer is None:
        stream.write(f"{text}\n")
    else:
        buffer.write(encode(f"{text}\n"))
        buffer.flush()
    stream.flush()


def out(text: str) -> None:
    emit(sys.stdout, text)


def say(text: str) -> None:
    emit(sys.stderr, text)


def on_path(tool: str) -> bool:
    """Whether the host offers tool, as `command -v` answers it.

    `uv run` puts its own environment's `bin` first and that directory holds
    the interpreter, so it is left out: a host without a tool must get the
    same verdict from this command as from a shell that `uv` did not start.
    """
    path = os.environ.get("PATH", "")
    environment = os.environ.get("VIRTUAL_ENV")
    if environment:
        launcher = os.path.join(environment, "bin")
        path = os.pathsep.join(entry for entry in path.split(os.pathsep) if entry != launcher)
    return shutil.which(tool, path=path) is not None


def exit_status(returncode: int) -> int:
    """Report a child killed by signal n as 128 + n, as a shell does."""
    return 128 - returncode if returncode < 0 else returncode


def spawn(
    command: Sequence[str],
    *,
    cwd: str | None = None,
    capture: bool = False,
    merge: bool = False,
    quiet: bool = False,
) -> tuple[int, bytes]:
    """Run command and return its status and, when captured, its standard output.

    Standard error passes through unless quiet or merged into the capture.
    A command that cannot start gives the 127 and 126 a shell gives.
    """
    sys.stdout.flush()
    sys.stderr.flush()
    if cwd is not None and not os.path.isdir(cwd):
        if not quiet:
            say(f"cd: {cwd}: No such file or directory")
        return 1, b""
    try:
        done = subprocess.run(
            list(command),
            cwd=cwd,
            stdout=subprocess.PIPE if capture else None,
            stderr=subprocess.STDOUT if merge else subprocess.DEVNULL if quiet else None,
            check=False,
        )
    except FileNotFoundError:
        if not quiet:
            say(f"{command[0]}: command not found")
        return 127, b""
    except OSError as error:
        if not quiet:
            say(f"{command[0]}: {error.strerror or error}")
        return 126, b""
    return exit_status(done.returncode), done.stdout or b""


def spawn_tee(command: Sequence[str], *, cwd: str | None = None) -> tuple[int, str]:
    """Run command, pass its output through line by line, and return it too.

    The race gate reads its own output afterwards for the packages that
    failed, and a reader tailing the log still sees each package as it
    finishes.
    """
    sys.stdout.flush()
    sys.stderr.flush()
    if cwd is not None and not os.path.isdir(cwd):
        say(f"cd: {cwd}: No such file or directory")
        return 1, ""
    try:
        child = subprocess.Popen(list(command), cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    except FileNotFoundError:
        say(f"{command[0]}: command not found")
        return 127, ""
    except OSError as error:
        say(f"{command[0]}: {error.strerror or error}")
        return 126, ""
    chunks: list[bytes] = []
    assert child.stdout is not None
    for line in child.stdout:
        chunks.append(line)
        sys.stdout.buffer.write(line)
        sys.stdout.buffer.flush()
    return exit_status(child.wait()), decode(b"".join(chunks))


def failed_packages(output: str) -> list[str] | None:
    """The packages a `go test` run reported as FAIL, or None when one did not build.

    `go test` prints `FAIL<TAB>pkg<TAB>time` for a package whose tests
    failed and `FAIL<TAB>pkg [build failed]` for one that did not compile.
    A rerun cannot help the second kind.
    """
    packages: list[str] = []
    for line in output.splitlines():
        found = re.match(r"FAIL\t(\S+)(\t|$| \[)", line)
        if found is None:
            continue
        if line.endswith("]"):
            return None
        packages.append(found.group(1))
    return packages


def git(*args: str, quiet: bool = False) -> tuple[int, bytes]:
    return spawn(["git", *args], capture=True, quiet=quiet)


def nul_records(data: bytes) -> list[str]:
    """Split `-z` output on NUL. A last record without its NUL is dropped, as `read -d ''` drops it."""
    return [decode(record) for record in data.split(b"\0")[:-1]]


def line_text(data: bytes) -> str:
    """Decode output as `$(...)` returns it, without the trailing line feeds."""
    return decode(data).rstrip("\n")


def shell_quote(word: str) -> str:
    """Quote word as bash's `printf %q` does."""
    if word == "":
        return "''"
    if any(ord(char) < 0x20 or ord(char) == 0x7F for char in word):
        parts = []
        for char in word:
            if char in "\\'":
                parts.append(f"\\{char}")
            elif ord(char) in ANSI_NAMES:
                parts.append(ANSI_NAMES[ord(char)])
            elif ord(char) < 0x20 or ord(char) == 0x7F:
                parts.append(f"\\{ord(char):03o}")
            else:
                parts.append(char)
        return "$'" + "".join(parts) + "'"
    return "".join(f"\\{char}" if char in QUOTED or (index == 0 and char == "#") else char for index, char in enumerate(word))


def quoted_line(words: Iterable[str]) -> str:
    return "+ " + "".join(f"{shell_quote(word)} " for word in words)


def gate_label(command: Sequence[str]) -> str:
    """Name a command by its first word and its first non-flag argument.

    A targeted `go test` lists every affected package, which is not a line to
    quote. A `run.py` command is named by its group and command, so the label
    carries neither the checkout's path nor the launcher's flags.
    """
    label = os.path.basename(command[0]) if command[0].startswith("/") else command[0]
    if label == "uv":
        for index, word in enumerate(command):
            if fnmatch.fnmatchcase(word, "*/run.py"):
                rest = command[index + 1 :]
                group = f" {rest[0]}" if len(rest) > 0 and rest[0] else ""
                name = f" {rest[1]}" if len(rest) > 1 and rest[1] else ""
                return f"run.py{group}{name}"
    for word in command[1:]:
        if word.startswith("-"):
            continue
        return f"{label} {word}"
    return label


def clock(seconds: float) -> str:
    """Format a duration as m:ss, with hours once there are any."""
    whole = int(seconds)
    hours, rest = divmod(whole, 3600)
    minutes, secs = divmod(rest, 60)
    if hours:
        return f"{hours}:{minutes:02d}:{secs:02d}"
    return f"{minutes}:{secs:02d}"


def matches(path: str, *patterns: str) -> bool:
    return any(fnmatch.fnmatchcase(path, pattern) for pattern in patterns)


def dirname(path: str) -> str:
    """Return the directory part of path as `dirname` prints it."""
    stripped = path.rstrip("/")
    if not stripped:
        return "/" if path else "."
    head, separator, _ = stripped.rpartition("/")
    if not separator:
        return "."
    return head.rstrip("/") or "/"


def module_for_file(path: str) -> str | None:
    """Return the nearest directory holding a go.mod, else "." when the root has one, else None."""
    directory = dirname(path)
    while True:
        if os.path.isfile(f"{directory}/go.mod"):
            return directory
        if directory in (".", "/"):
            break
        directory = dirname(directory)
    return "." if os.path.isfile("go.mod") else None


def package_dir(path: str) -> str:
    """Return the directory of the package that owns path, where a `testdata` directory belongs to its parent."""
    directory = dirname(path)
    if directory == "testdata" or directory.startswith("testdata/"):
        return "."
    if directory.endswith("/testdata") or "/testdata/" in directory:
        cut = directory.find("/testdata/")
        if cut >= 0:
            directory = directory[:cut]
        directory = directory.removesuffix("/testdata")
    return directory


def canonical_path(argument: str, root_physical: str) -> str:
    """Rewrite a relative argument spelled through a linked directory to the spelling git tracks.

    `.claude/skills` links to `.agents/skills`, git lists nothing beyond a
    link, and the dirty marker holds git's spelling. The deepest existing
    directory of the argument is resolved against the root. An absolute
    path, or one that resolves outside the tree, is kept as given.
    """
    if argument.startswith("/"):
        return argument
    directory = argument
    rest = ""
    while not os.path.isdir(directory):
        tail = directory[directory.rfind("/") + 1 :]
        rest = f"{tail}/{rest}" if rest else tail
        directory = directory[: directory.rfind("/")] if "/" in directory else "."
    if not os.access(directory, os.X_OK):
        return argument
    physical = os.path.realpath(directory)
    if physical == root_physical:
        physical = ""
    elif physical.startswith(f"{root_physical}/"):
        physical = physical[len(root_physical) + 1 :]
    else:
        return argument
    if physical and rest:
        return f"{physical}/{rest}"
    return physical or rest or "."


def affected(rows: Iterable[tuple[str, Iterable[str]]], changed: Iterable[str]) -> list[str]:
    """Return the changed packages and every package that imports one of them, sorted.

    A row is a package and what it imports, test imports included. The
    importers are found to a fixpoint, so a package that imports an importer
    is affected too, whatever the order of the rows.
    """
    table = [(import_path, set(imports)) for import_path, imports in rows]
    found = set(changed)
    grown = True
    while grown:
        grown = False
        for import_path, imports in table:
            if import_path not in found and imports & found:
                found.add(import_path)
                grown = True
    return sorted(found)


def parse_rows(text: str) -> list[tuple[str, set[str]]]:
    """Read `go list -f GO_LIST_ROWS` output, one `<import path>|<imports separated by blanks>` per line."""
    rows = []
    for line in text.split("\n"):
        if line:
            import_path, _, imports = line.partition("|")
            rows.append((import_path, set(imports.split())))
    return rows


def online_cpus() -> int:
    """Return the processor count `getconf _NPROCESSORS_ONLN` prints, or 4 when it prints none."""
    try:
        count = os.sysconf("SC_NPROCESSORS_ONLN")
    except (ValueError, OSError):
        return 4
    return count if count > 0 else 4


def diff_trees(left: str, right: str) -> int:
    """Compare two trees as `diff -qr` does: print each difference and return its status."""
    for path in (left, right):
        if not os.path.exists(path):
            say(f"diff: {path}: No such file or directory")
            return 2
    return _diff_pair(left, right)


def _diff_pair(left: str, right: str) -> int:
    left_is_dir, right_is_dir = os.path.isdir(left), os.path.isdir(right)
    if left_is_dir != right_is_dir:
        out(f"File {left} is a {'directory' if left_is_dir else 'regular file'} while file {right} is a {'directory' if right_is_dir else 'regular file'}")
        return 1
    if not left_is_dir:
        try:
            same = Path(left).read_bytes() == Path(right).read_bytes()
        except OSError as error:
            say(f"diff: {error.filename}: {error.strerror}")
            return 2
        if not same:
            out(f"Files {left} and {right} differ")
        return 0 if same else 1
    status = 0
    try:
        left_names, right_names = set(os.listdir(left)), set(os.listdir(right))
    except OSError as error:
        say(f"diff: {error.filename}: {error.strerror}")
        return 2
    for name in sorted(left_names | right_names, key=treestate.sort_key):
        if name not in right_names:
            out(f"Only in {left}: {name}")
            status = max(status, 1)
        elif name not in left_names:
            out(f"Only in {right}: {name}")
            status = max(status, 1)
        else:
            status = max(status, _diff_pair(f"{left}/{name}", f"{right}/{name}"))
    return status


def read_exit(main: Callable[[list[str]], int], argv: list[str]) -> int:
    """Call a command's main and read a SystemExit as its exit code."""
    try:
        code = main(argv)
    except SystemExit as leave:
        return system_exit_status(leave)
    return code or 0


def system_exit_status(leave: SystemExit) -> int:
    if leave.code is None:
        return 0
    if isinstance(leave.code, int):
        return leave.code
    say(str(leave.code))
    return 1


class Verification:
    """One run: the arguments it was given, the gates they select, and the gate running now."""

    def __init__(self) -> None:
        self.full = False
        self.print_selection = False
        self.base = "HEAD"
        self.base_given = False
        self.explicit = False
        self.paths: list[str] = []
        self.root = ""
        self.root_physical = ""
        self.gate = ""
        self.started = time.monotonic()
        self.timings: list[tuple[str, float]] = []
        self.build_dir = ""
        self.go_files: list[str] = []
        self.markdown_files: list[str] = []
        self.modules: list[str] = []
        self.proto_files: list[str] = []
        self.proto_deleted = False
        self.proto = False
        self.buf_module = False
        self.lint_module = False
        self.hook_tooling = False
        self.mib = False
        self.service_otel_integration = False
        self.web = False
        self.plan_state = False
        self.dependent_modules: list[str] = []

    def parse(self, argv: Sequence[str]) -> None:
        index = 0
        while index < len(argv):
            argument = argv[index]
            if argument == "--full":
                self.full = True
                index += 1
            elif argument == "--print-selection":
                self.print_selection = True
                index += 1
            elif argument == "--base":
                if len(argv) - index < 2:
                    say("--base requires a ref")
                    raise Stop(2)
                self.base = argv[index + 1]
                self.base_given = True
                index += 2
            elif argument == "--":
                self.explicit = True
                for word in argv[index + 1 :]:
                    # An empty word, usually an unset variable, would resolve to the
                    # root and select every module.
                    if word.rstrip("\n") == "":
                        say("empty path argument after --")
                        raise Stop(2)
                    self.paths.append(word)
                index = len(argv)
            elif argument in ("-h", "--help"):
                out(USAGE.rstrip("\n"))
                raise Stop(0)
            else:
                say(f"unknown argument: {argument}")
                say(USAGE.rstrip("\n"))
                raise Stop(2)

    def add_path(self, candidate: str) -> None:
        candidate = candidate.removeprefix("./")
        if candidate not in self.paths:
            self.paths.append(candidate)

    def collect_paths(self) -> None:
        self.paths = [canonical_path(path.rstrip("\n").removeprefix("./"), self.root_physical) for path in self.paths]
        if not self.explicit and not self.full:
            code, _ = git("rev-parse", "--verify", f"{self.base}^{{commit}}")
            if code != 0:
                raise Stop(code)
            for record in nul_records(git("diff", "--no-renames", "--name-only", "-z", self.base, "--")[1]):
                self.add_path(record)
            for record in nul_records(git("ls-files", "--others", "--exclude-standard", "-z")[1]):
                self.add_path(record)

        if self.explicit and not self.paths:
            self.refuse_empty("no paths supplied after --")
        if not self.full and not self.paths:
            # Exits non-zero for the reason the no-gate exit below gives: a run that
            # checked nothing must not read as a pass. It writes no receipt either.
            self.refuse_empty(
                f"No changed paths to verify against {self.base}. After a commit, use --base main, name paths after --, or use --full."
            )

        if self.paths:
            self.expand_directories()

    def refuse_empty(self, message: str) -> None:
        if self.print_selection:
            out("service_otel_integration=false")
            raise Stop(0)
        say(message)
        raise Stop(2)

    def expand_directories(self) -> None:
        """Replace each directory argument by the files git knows under it.

        The classification matches path suffixes, so a directory would match
        nothing: every gate stays unselected, and the dirty marker, cleared by
        exact path, keeps naming files this run did not verify.
        """
        expanded = []
        for path in self.paths:
            if os.path.isdir(path):
                listing = git("ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", path)[1]
                expanded += [record.removeprefix("./") for record in nul_records(listing)]
            else:
                # Kept whether or not it exists: a deleted path still selects its
                # module, and the classification guards its own file reads.
                expanded.append(path)
        self.paths = []
        if not expanded:
            say("FlowSeer verification found no files under the named paths.")
            raise Stop(2)
        for path in expanded:
            self.add_path(path)

    def add_module(self, candidate: str) -> None:
        if candidate not in self.modules:
            self.modules.append(candidate)

    def nearest_module(self, path: str) -> str:
        """Return the module of path, ending the run where the shell's failing substitution did."""
        module = module_for_file(path)
        if module is None:
            raise Stop(1)
        return module

    def select(self) -> None:
        if self.full:
            self.select_everything()
        else:
            for path in self.paths:
                self.classify(path)
        self.select_dependent_modules()

    def select_everything(self) -> None:
        entries = find_entries()
        for entry in sorted((entry for entry in entries if entry.endswith("/go.mod")), key=treestate.sort_key):
            if entry == "./tools/buf/go.mod":
                self.buf_module = True
            elif entry == "./tools/golangci-lint/go.mod":
                self.lint_module = True
            else:
                self.add_module(dirname(entry))
        for entry in sorted(entries, key=treestate.sort_key):
            if entry.endswith(".go") and not entry.startswith(("./generated/", "./frontend/web/generated/")):
                self.go_files.append(entry.removeprefix("./"))
        self.proto = True
        self.hook_tooling = True
        self.mib = True
        self.service_otel_integration = True
        self.web = True
        self.plan_state = True

    def classify(self, path: str) -> None:
        if matches(path, "frontend/web/*"):
            self.web = True
        # A plan's state file selects the state gate by itself, and so does a
        # deleted plan: its state file left behind is a fault the gate names.
        if matches(path, "docs/plans/*"):
            self.plan_state = True
        if matches(path, "*.md"):
            if os.path.isfile(path):
                self.markdown_files.append(path)
            if matches(path, *HOOK_MARKDOWN):
                self.hook_tooling = True
        elif matches(path, "*.go"):
            if os.path.isfile(path) and not matches(path, *GENERATED_GO):
                self.go_files.append(path)
                self.add_module(self.nearest_module(path))
            elif matches(path, "generated/*"):
                # Generated Go is not formatted or linted as hand-written code,
                # and in the root module its generator's drift gate covers it.
                # Output that is a module of its own is compiled by nothing
                # else, so it selects that module, deleted files included.
                module = self.nearest_module(path)
                if module != ".":
                    self.add_module(module)
        elif matches(path, "tools/buf/go.mod", "tools/buf/go.sum"):
            # The tool module stays out of the generic Go selection. The
            # `tools/buf/*` arm below routes these files to the protobuf gates.
            pass
        elif matches(path, "tools/golangci-lint/go.mod", "tools/golangci-lint/go.sum"):
            # The lint tool module holds no packages to build or test. A new
            # linter applies to every module, so execute asks for --full.
            self.lint_module = True
        elif matches(path, *GO_MODULE_FILES):
            self.add_module(self.nearest_module(path))
        elif matches(path, *PROTO_FILES):
            self.proto = True
            if matches(path, "*.proto"):
                if os.path.isfile(path):
                    self.proto_files.append(path)
                else:
                    # A deleted schema file is the archetypal breaking change and
                    # cannot be named with --path, so the whole module is compared.
                    self.proto_deleted = True
        elif matches(path, *HOOK_TOOLING):
            self.hook_tooling = True
        # Independent of the arms above: a change to the mibgen generator, its
        # config, or a MIB source can drift the committed bindings.
        if matches(path, *MIB):
            self.mib = True
        if matches(path, "tools/buf/*"):
            self.proto = True
            self.buf_module = True
        if matches(path, *SERVICE_OTEL):
            self.service_otel_integration = True

    def select_dependent_modules(self) -> None:
        """Select the nested modules that replace the root module and reach a changed root package.

        A nested module breaks on a root change it compiles against, but a
        root-only path list never selects it. A module is selected when a
        changed root package is among its dependencies, test imports and each
        build tag's files included. Selecting every one for every root change
        would compile the YANG module's thousand packages each time. A listing
        that fails selects the module so its build reports why, and a root
        go.mod or go.sum edit selects every one.
        """
        if self.full or "." not in self.modules:
            return
        root_all = any(path in ("go.mod", "go.sum") for path in self.paths)
        root_packages: list[str] = []
        if not root_all:
            for go_file in self.go_files:
                if not go_file or module_for_file(go_file) != ".":
                    continue
                directory = package_dir(go_file)
                if not os.path.isdir(directory):
                    continue
                code, listed = spawn(["go", "list", "-e", "-f", "{{.ImportPath}}", f"./{directory}"], capture=True, quiet=True)
                if code != 0:
                    continue
                package = line_text(listed)
                if package:
                    root_packages.append(package)
        for entry in sorted((entry for entry in find_entries() if os.path.basename(entry) == "go.mod"), key=treestate.sort_key):
            if not replaces_root(entry):
                continue
            dependent = dirname(entry.removeprefix("./"))
            if dependent == ".":
                continue
            if not root_all:
                if not root_packages:
                    continue
                packages = self.module_deps(dependent)
                if packages is not None and not any(package in packages for package in root_packages):
                    continue
            self.dependent_modules.append(dependent)

    def module_deps(self, directory: str) -> set[str] | None:
        """Return the packages a module compiles under the default build and under each tag, or None when a listing fails."""
        arguments = ["go", "list", "-e", "-deps", "-test"]
        code, listed = spawn([*arguments, "-f", "{{.ImportPath}}", "./..."], cwd=directory, capture=True, quiet=True)
        if code != 0:
            return None
        packages = set(decode(listed).split("\n"))
        for tag in self.build_tags(["./..."], directory, quiet=True):
            code, listed = spawn([*arguments, "-tags", tag, "-f", "{{.ImportPath}}", "./..."], cwd=directory, capture=True, quiet=True)
            if code != 0:
                return None
            packages |= set(decode(listed).split("\n"))
        return packages

    def build_tags(self, packages: Sequence[str], directory: str | None, *, quiet: bool) -> list[str]:
        """Return the sweepable build tags in the directories of the named packages, sorted by bytes.

        GOOS and GOARCH names, ignore, and race are not sweepable tags.
        """
        _, listed = spawn(["go", "list", "-f", "{{.Dir}}", *packages], cwd=directory, capture=True, quiet=quiet)
        tags: set[str] = set()
        for package_directory in decode(listed).split("\n")[:-1]:
            try:
                names = sorted(os.listdir(package_directory))
            except OSError:
                continue
            for name in names:
                if name.startswith(".") or not name.endswith(".go"):
                    continue
                try:
                    content = Path(package_directory, name).read_bytes()
                except OSError:
                    continue
                for line in content.split(b"\n"):
                    if line.startswith(b"//go:build"):
                        tags |= {decode(word) for word in re.findall(rb"[A-Za-z0-9_]+", line)}
        return sorted(tags - UNSWEEPABLE_TAGS)

    def print_selected(self) -> None:
        out(f"service_otel_integration={str(self.service_otel_integration).lower()}")
        if self.proto:
            out("proto=true")
        if self.buf_module:
            out("tool_module=tools/buf mode=mod-verify")
        if self.plan_state:
            out("plan_state=true")
        if self.hook_tooling:
            out("hook_tooling=true")
        for module in self.modules:
            mode = "build-sample-lint" if module.startswith("generated/") else "full"
            out(f"module={module} mode={mode}")
        for dependent in self.dependent_modules:
            mode = "build" if dependent.startswith("generated/") else "build-vet"
            out(f"dependent={dependent} mode={mode}")

    def gates_selected(self) -> bool:
        return bool(
            self.markdown_files
            or self.go_files
            or self.modules
            or any((self.proto, self.hook_tooling, self.mib, self.service_otel_integration, self.web, self.plan_state))
        )

    def search_gopath(self) -> None:
        """Put `$(go env GOPATH)/bin` on PATH, so whether the tree verifies does not depend on the launching shell."""
        if not on_path("go"):
            return
        code, printed = spawn(["go", "env", "GOPATH"], capture=True, quiet=True)
        gopath = line_text(printed)
        if code != 0 or not gopath:
            return
        path = os.environ.get("PATH", "")
        if f":{gopath}/bin:" not in f":{path}:":
            os.environ["PATH"] = f"{gopath}/bin:{path}"

    def required_tools(self) -> list[str]:
        """Return every tool the selected gates will ask for, so a setup problem stops the run at once."""
        tools = ["uv", "go"]
        if self.go_files:
            tools += ["gofumpt", "goimports"]
        if self.modules:
            tools += ["go"]
        if self.proto or self.mib:
            tools.append("go")
        if self.hook_tooling:
            tools.append("go")
        if self.web:
            tools.append("node")
            for tool in WEB_TOOLS:
                if not os.access(f"frontend/web/node_modules/.bin/{tool}", os.X_OK):
                    say(f"required web tool is missing: frontend/web/node_modules/.bin/{tool}; install frontend/web dependencies")
                    raise Stop(1)
        return tools

    def check_tools(self) -> None:
        missing: list[str] = []
        for tool in self.required_tools():
            if not on_path(tool) and tool not in missing:
                missing.append(tool)
        if not missing:
            return
        say(f"required tools are not on PATH: {' '.join(missing)}")
        if any(tool in missing for tool in ("gofumpt", "goimports")):
            say(GOPATH_HINT)
        raise Stop(1)

    def need_tool(self, tool: str) -> None:
        if not on_path(tool):
            say(f"required tool is not on PATH: {tool}")
            raise Stop(1)

    def begin_gate(self, label: str) -> float:
        """Name the gate running now and say where the run stands.

        A full run takes long enough that a reader tails its log, so each
        gate opens with its number and the time since the run started.
        """
        self.gate = label
        out(f"-- gate {len(self.timings) + 1}: {label}, {clock(time.monotonic() - self.started)} into the run --")
        return time.monotonic()

    def end_gate(self, started: float, code: int) -> None:
        self.timings.append((self.gate, time.monotonic() - started))
        if code != 0:
            raise Stop(code)
        self.gate = ""

    def run(self, command: Sequence[str], *, cwd: str | None = None) -> None:
        """Print and start a gate. A failure ends the run with the gate's status and leaves its name for the verdict."""
        started = self.begin_gate(gate_label(command))
        out(quoted_line(command))
        code, _ = spawn(command, cwd=cwd)
        self.end_gate(started, code)

    def run_check(self, name: str, main: Callable[[list[str]], int], argv: list[str]) -> None:
        """Run a check of this package in this process under the label `run.py verify <name>`."""
        started = self.begin_gate(f"run.py verify {name}")
        out(quoted_line(["run.py", "verify", name, *argv]))
        sys.stdout.flush()
        sys.stderr.flush()
        code = read_exit(main, argv)
        self.end_gate(started, code)

    def report_timings(self) -> None:
        """Print every gate's duration, slowest first, so a slow run says where its time went."""
        if not self.timings:
            return
        total = time.monotonic() - self.started
        out(f"Gate durations ({len(self.timings)} gates, {clock(total)} in all):")
        for label, seconds in sorted(self.timings, key=lambda timing: timing[1], reverse=True):
            out(f"  {clock(seconds):>8}  {label}")

    def report_test_changes(self) -> None:
        """Print what check-test-integrity found. It reports and does not fail.

        Deleting a test, skipping it, or rewriting a golden file is sometimes
        right in a repository that breaks APIs on purpose, and sometimes the
        way a suite stops proving anything. The list goes into the
        implementer's report with a reason per line.
        """
        argv = [self.base] if self.full else [self.base, "--", *self.paths]
        captured = io.StringIO()
        with contextlib.redirect_stdout(captured):
            code = read_exit(check_test_integrity.main, argv)
        if code != 0:
            raise Stop(code)
        changes = captured.getvalue().rstrip("\n")
        if changes:
            out("Test changes to account for:")
            out("  " + changes.replace("\n", "\n  "))

    def run_web_gates(self) -> None:
        directory = "frontend/web"
        for command in (
            ["./node_modules/.bin/vue-tsc", "--noEmit"],
            ["./node_modules/.bin/vite", "build"],
            ["./node_modules/.bin/eslint", "."],
            ["./node_modules/.bin/stylelint", "src/**/*.{css,vue}"],
            ["./node_modules/.bin/prettier", "--check", "."],
            ["./node_modules/.bin/vitest", "run"],
        ):
            self.run(command, cwd=directory)

    def check_format(self) -> None:
        self.need_tool("gofumpt")
        self.need_tool("goimports")
        started = self.begin_gate("gofumpt/goimports -d")
        failed = False
        for tool in ("gofumpt", "goimports"):
            code, printed = spawn([tool, "-d", *self.go_files], capture=True)
            text = line_text(printed)
            if code != 0 or text:
                say(text)
                failed = True
        if failed:
            say("Go formatting differs; run gofumpt and goimports.")
        # The two tools run outside `run`, so the gate's name and time are recorded here.
        self.end_gate(started, 1 if failed else 0)

    def vet_tagged(self, packages: Sequence[str], directory: str | None) -> None:
        """Vet the targets once per build tag found in their directories.

        Files behind a build tag are invisible to the untagged build, so a
        signature change can leave them broken while the gate is green. Vet
        compiles the tagged test files without running them.
        """
        for tag in self.build_tags(packages, directory, quiet=False):
            self.run(["go", "vet", "-tags", tag, *packages], cwd=directory)

    def check_module(self, module: str) -> None:
        out(f"== Go module: {module} ==")
        if module.startswith("generated/"):
            # Generated output holds no tests, so a build proves it compiles
            # and a lint of the sample packages stands in for the rest.
            self.run(["go", "build", "./..."], cwd=module)
            samples = []
            if module == "generated/go/yang":
                samples = ["ruckus-icx/openconfigvlan", "ruckus-icx/openconfigsystem", "aruba-cx/openconfignetworkinstance"]
            lint_packages = [f"./{sample}" for sample in samples if os.path.isdir(f"{module}/{sample}")]
            if lint_packages:
                self.run(self.lint_command(lint_packages), cwd=module)
            return
        # No -o: with an output directory, go refuses a module that builds no
        # main packages. Plain go build compiles everything and discards the binaries.
        self.run(["go", "build", "./..."], cwd=module)
        targets = ["./..."]
        changed = self.changed_packages(module)
        if not self.full and changed:
            # A targeted run vets, tests, and lints only the packages that can
            # observe the change: those holding a changed file plus every
            # package that imports one of them, directly or through its test
            # imports. --full and a module selected without a changed Go file
            # (a go.mod edit) keep the module-wide scope.
            code, listed = spawn(["go", "list", "-e", "-f", GO_LIST_ROWS, "./..."], cwd=module, capture=True)
            if code != 0:
                raise Stop(code)
            targets = affected(parse_rows(decode(listed)), changed)
            out(f"Targeted packages: {len(targets)} (changed: {' '.join(changed)})")
        self.run(["go", "vet", *targets], cwd=module)
        self.vet_tagged(targets, module)
        race = []
        if self.full:
            # Module-wide, go runs one package binary per CPU. Race-instrumented
            # packages that start their own listeners or walk a corpus time out
            # at that load and pass alone, so a full run measures the host, not
            # the code. Half the CPUs, at least two, keeps the timeouts honest.
            cpus = online_cpus()
            race = ["-p", str(max(cpus // 2, 2))]
        self.run_race(["go", "test", "-race", *race, *targets], cwd=module)
        # Package directories without generated output keep the lint bounded
        # by hand-written code, since generated findings are excluded by configuration.
        module_dir = os.path.normpath(os.path.join(self.root, module))
        _, listed = spawn(["go", "list", "-f", "{{.Dir}}", *targets], cwd=module, capture=True)
        lint_packages = []
        for directory in decode(listed).split("\n")[:-1]:
            if re.search(r"/generated(/|$)", directory):
                continue
            # The module root itself can hold a package. Stripping "$module_dir/"
            # would leave it untouched and name a path no linter accepts.
            if directory == module_dir:
                lint_packages.append(".")
            else:
                lint_packages.append("./" + directory.removeprefix(f"{module_dir}/"))
        if lint_packages:
            self.run(self.lint_command(lint_packages), cwd=module)

    def run_race(self, command: list[str], *, cwd: str) -> None:
        """Run the race gate, and rerun the packages that failed alone, once.

        Suites that start listeners or a message bus time out when another
        race run shares the host and pass alone. One rerun with one package
        at a time separates that from a failing test: a package that fails
        twice fails the gate, and the output says which happened.
        """
        started = self.begin_gate(gate_label(command))
        out(quoted_line(command))
        code, output = spawn_tee(command, cwd=cwd)
        if code != 0:
            packages = failed_packages(output)
            if packages:
                noun = "package" if len(packages) == 1 else "packages"
                out(f"Retrying {len(packages)} {noun} alone, one at a time, since a suite can time out under load: {' '.join(packages)}")
                retry = ["go", "test", "-race", "-count=1", "-p", "1", *packages]
                out(quoted_line(retry))
                code, _ = spawn(retry, cwd=cwd)
                if code == 0:
                    out("The retry alone passed, so the first failure came from load on this host, not from the change.")
                else:
                    out("The retry alone failed too.")
        self.end_gate(started, code)

    def lint_command(self, packages: Sequence[str]) -> list[str]:
        # golangci-lint takes a machine-wide file lock and by default dies with
        # `parallel golangci-lint is running` when another instance holds it,
        # which reads as a red gate. Serial runners wait for the lock instead.
        #
        # golangci-lint is pinned in its own module, like buf, so the version
        # that lints is the one tools/golangci-lint/go.sum holds, never whatever
        # sits on PATH. Its golang.org/x/tools decides which export-data
        # versions it reads, so a Go release that bumps that version needs a
        # linter bump here. go tool builds it with the toolchain the linted
        # module selects, which is why the tool module's go line stays at 1.27.
        return [
            "go", "tool", f"-modfile={self.root}/tools/golangci-lint/go.mod", "golangci-lint",
            "run", "--allow-serial-runners", "--config", f"{self.root}/.golangci.yml", *packages,
        ]

    def changed_packages(self, module: str) -> list[str]:
        packages = []
        for go_file in self.go_files:
            if not go_file:
                continue
            # Route each file to its nearest go.mod: a prefix match alone would
            # hand a nested module's file to the root module, which then fails
            # with "main module does not contain package".
            if module_for_file(go_file) != module:
                continue
            relative = go_file if module == "." else go_file.removeprefix(f"{module}/")
            directory = package_dir(relative)
            if not os.path.isdir(os.path.join(module, directory)):
                continue
            code, listed = spawn(["go", "list", "-e", "-f", "{{.ImportPath}}", f"./{directory}"], cwd=module, capture=True, quiet=True)
            if code != 0:
                continue
            package = line_text(listed)
            if package:
                packages.append(package)
        return packages

    def check_dependent_module(self, dependent: str) -> None:
        if dependent.startswith("generated/"):
            out(f"== Dependent module: {dependent} (build only) ==")
            self.run(["go", "build", "./..."], cwd=dependent)
            return
        out(f"== Dependent module: {dependent} (build and vet only) ==")
        self.run(["go", "build", "./..."], cwd=dependent)
        self.run(["go", "vet", "./..."], cwd=dependent)
        self.vet_tagged(["./..."], dependent)

    def check_repository_wide(self) -> None:
        # Some packages hold checks over repository-wide namespaces: error code
        # uniqueness in src/common/errs, raw secret fields across src/ in
        # src/common/internal/secretguard, and under test/conformance/ the
        # schema layering and message rules, the panic placement and goroutine
        # boundary, and the forbidden module imports. A change that violates one
        # of them touches none of the packages, so the importer fixpoint never
        # selects them and a targeted run would pass what --full refuses. They
        # live in the root module and walk the tree by path, so they run once
        # from the root whichever modules the changed files selected, and a
        # --full run already runs the root module whole. test/conformance/ is
        # enumerated, not named, so a renamed or added gate needs no edit here.
        if self.full:
            return
        if self.modules:
            out("== Repository-wide gates ==")
            self.run(["go", "test", "-race", "./src/common/errs", "./src/common/internal/secretguard", "./test/conformance/..."])
        elif self.web:
            # test/conformance/a11y reads the web tests and stories, so a change
            # that touches only frontend/web/ selects no Go module and would
            # otherwise pass the gate written for it.
            self.need_tool("go")
            out("== Repository-wide gates ==")
            self.run(["go", "test", "-race", "./test/conformance/..."])

    def check_protobuf(self) -> None:
        if self.buf_module:
            self.run(["go", "-C", "tools/buf", "mod", "verify"])
        buf = ["go", "tool", f"-modfile={self.root}/tools/buf/go.mod", "buf"]
        path_args: list[str] = []
        if not self.full and not self.buf_module:
            for proto_file in self.proto_files:
                path_args += ["--path", proto_file]
        self.run([*buf, "format", "-d", "--exit-code", *path_args])
        self.run([*buf, "lint", *path_args])
        # The integration branch is main. A checkout that still carries the old
        # name has master. Neither resolving is a failed gate: this block once
        # looked for master alone and fell through in silence, so every schema
        # change passed with no breaking comparison at all.
        integration_branch = ""
        for candidate in ("main", "master"):
            if git("show-ref", "--verify", "--quiet", f"refs/heads/{candidate}")[0] == 0:
                integration_branch = candidate
                break
        if not integration_branch:
            say("the breaking gate needs a main (or master) branch to compare against, and neither exists.")
            raise Stop(1)
        # --path names files in the against-ref. A file the branch added is
        # absent there, so targeting it yields "no .proto files were targeted",
        # which buf reports as a failure rather than a vacuous pass. Only the
        # changed files the integration branch holds are compared.
        against = f".git#branch={integration_branch}"
        breaking_paths: list[str] = []
        for proto_file in self.proto_files:
            if git("cat-file", "-e", f"{integration_branch}:{proto_file}", quiet=True)[0] == 0:
                breaking_paths += ["--path", proto_file]
        if self.full or self.buf_module or self.proto_deleted or not self.proto_files:
            self.run([*buf, "breaking", "--against", against])
        elif breaking_paths:
            self.run([*buf, "breaking", "--against", against, *breaking_paths])
        else:
            out("breaking skipped: every changed .proto file is new on this branch.")
        generated = f"{self.build_dir}/generated"
        self.run([*buf, "generate", "-o", generated])
        self.run_diff("generated/go/proto", f"{generated}/generated/go/proto")
        if os.path.isdir("frontend/web/generated") or os.path.isdir(f"{generated}/frontend/web/generated"):
            self.run_diff("frontend/web/generated", f"{generated}/frontend/web/generated")
        # The schema's executable invariants live in a Go package no schema path
        # selects, so a proto-only change runs them here rather than only under --full.
        self.need_tool("go")
        self.run(["go", "test", "-count=1", "./test/conformance/proto/"])

    def run_diff(self, left: str, right: str) -> None:
        """Compare a generated tree with the committed one under the label `diff <left>`."""
        command = ["diff", "-qr", left, right]
        out(quoted_line(command))
        self.gate = gate_label(command)
        code = diff_trees(left, right)
        if code != 0:
            raise Stop(code)
        self.gate = ""

    def check_mib(self) -> None:
        # Regenerates into a tmpdir and diffs against the committed bindings,
        # and exits non-zero on drift, as the buf generate diff above does.
        self.run(["go", "run", "./src/protocol/snmp/cmd/mibgen", "-check"])
        # The baseline's second tier forbids a pending group. Without it the
        # refresh flag's own output passes: refreshing writes every new group as
        # pending, and the always-on gate tolerates pending by design.
        self.run(["go", "test", "-tags=mibgen_baseline_complete", "-run", "TestBaselineComplete", "./src/protocol/snmp/cmd/mibgen/"])
        if self.full:
            # The parser's default corpus tier reads a deduplicated corpus so the
            # developer loop stays cheap. The whole corpus sits behind a build tag
            # and is only worth its cost here. Both runs carry -count=1: they walk
            # a corpus directory chosen by a build tag, which Go's test cache does
            # not track, and a cached pass is the wrong thing to accept from the
            # tier that exists because it is too expensive for the developer loop.
            self.run(["go", "test", "-count=1", "-tags=smi_corpus_full", "-run", "TestCorpus", "./src/protocol/smi/"])
            # The differential suite compares the parser against the dependency it
            # replaced. It is its own module, so it needs -C to resolve, and its
            # corpus pass skips under -race, so a plain run is the only one.
            self.run(["go", "test", "-C", "src/protocol/smi/differential", "-count=1", "./..."])

    def check_hook_tooling(self) -> None:
        self.run_check("check-json", check_json.main, [".claude/settings.json", ".codex/hooks.json"])
        # -count=1: the gate lists files through git, whose output go test's cache does not track.
        self.run(["go", "test", "-count=1", "./test/conformance/scripting/"])
        # The repository scripts: run their suites, the hook suite among them. The
        # suite compiles every module, so a module no test imports still parses.
        if os.path.isdir("tools/scripts"):
            self.need_tool("uv")
            self.run_suite()

    def run_suite(self) -> None:
        command = ["uv", "run", "tools/scripts/run.py", "test"]
        started = self.begin_gate(gate_label(command))
        code, captured = spawn(command, capture=True, merge=True)
        text = quoted_line(command) + "\n" + decode(captured)
        out(text.rstrip("\n"))
        self.end_gate(started, 1 if code != 0 else 0)
        if not SUITE_RAN.search(text):
            say("tools/scripts tests ran zero tests.")
            self.gate = "uv run tools/scripts/run.py test"
            raise Stop(1)

    def git_dir(self) -> str:
        code, printed = git("rev-parse", "--git-dir")
        if code != 0:
            raise Stop(code)
        directory = line_text(printed)
        return directory if directory.startswith("/") else f"{self.root}/{directory}"

    def clear_marker(self, marker: str) -> None:
        """Drop the marker lines this run verified and name the rest."""
        try:
            if os.path.getsize(marker) <= 0:
                return
        except OSError:
            return
        # Split on line feeds only, and drop an unterminated last line as `read` does.
        lines = decode(Path(marker).read_bytes()).split("\n")[:-1]
        remaining: list[str] = []
        module_wide = module_wide_base = 0
        for marked in lines:
            verified = self.full or marked in self.paths
            # A file byte-identical to an explicit base is the base, not an edit:
            # `land` merges main in, the marker names every file that merge wrote,
            # and a run against main has nothing of them left to examine.
            identical = False
            if self.base_given and marked != BASH_MUTATION:
                code, printed = git("rev-parse", "-q", "--verify", f"{self.base}:{marked}", quiet=True)
                base_hash = line_text(printed) if code == 0 else ""
                if base_hash:
                    code, hashed = git("hash-object", "--", marked, quiet=True)
                    identical = code == 0 and line_text(hashed) == base_hash
                else:
                    identical = not os.path.lexists(marked)
                verified = verified or identical
            if MODULE_WIDE.search(marked):
                module_wide += 1
                # A module under tools/ (buf, golangci-lint) holds a tool, not a
                # package the tree imports, so its go.mod reaches nothing a path
                # does not name; a linter change refuses a targeted run on its own.
                if identical or marked.startswith("tools/"):
                    module_wide_base += 1
            if not verified:
                remaining.append(marked)
        # The Bash-mutation line stands for the generator output and module files
        # the hook saw change, which it marks beside the line. When every one of
        # them is identical to the base, nothing module-wide is left to run.
        if module_wide > 0 and module_wide_base == module_wide:
            remaining = [line for line in remaining if line != BASH_MUTATION]
        if not remaining:
            with contextlib.suppress(FileNotFoundError):
                os.unlink(marker)
            return
        # Rewritten in the same second as the receipt below, so a marker with the
        # receipt's mtime is this run's own doing. Say what survived, or a passing
        # run teaches its readers to ignore the marker.
        survivors = sorted(set(remaining), key=treestate.sort_key)
        Path(marker).write_bytes(encode("".join(f"{line}\n" for line in survivors)))
        out("Unverified edits remain after this run:")
        for line in survivors:
            out(f"  {line}")
        if BASH_MUTATION in survivors:
            out("  The Bash-mutation line clears under --full, or under --base REF once every")
            out("  generated, go.mod, go.sum, or buf.lock file it marked is identical to REF.")

    def rewrite_tree_state(self, git_dir: str) -> None:
        """Store the listing the marker hook compares against, so verified content is not marked again."""
        descriptor, temporary = tempfile.mkstemp(prefix="flowseer-tree-state.", dir=git_dir)
        try:
            listing = treestate.listing(Path(self.root))
        except (RuntimeError, OSError) as error:
            os.close(descriptor)
            os.unlink(temporary)
            say("verify-change: hooks tree-state failed; the dirty marker would mark verified files again")
            say(f"verify-change: {error}")
            raise Stop(1) from None
        with os.fdopen(descriptor, "wb") as handle:
            handle.write(encode("".join(f"{line}\n" for line in listing)))
        os.replace(temporary, f"{git_dir}/{TREE_STATE_NAME}")

    def write_receipt(self, git_dir: str) -> None:
        stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        words = self.paths or [""]
        text = (
            f"verified_at={stamp}\nbase={self.base}\nfull={str(self.full).lower()}\n"
            f"paths={''.join(f'{shell_quote(word)} ' for word in words)}\n"
        )
        Path(f"{git_dir}/{RECEIPT_NAME}").write_bytes(encode(text))

    def execute(self, argv: Sequence[str]) -> None:
        self.parse(argv)
        code, printed = git("rev-parse", "--show-toplevel")
        if code != 0:
            raise Stop(code)
        self.root = line_text(printed)
        os.chdir(self.root)
        self.root_physical = os.path.realpath(self.root)

        self.collect_paths()
        self.select()

        if self.print_selection:
            self.print_selected()
            raise Stop(0)

        # A lint configuration change applies to every Go module, and a targeted
        # run lints only the modules it selects, none at all for a config-only
        # change, yet would write a passing receipt. Refused before any gate runs.
        if not self.full and ".golangci.yml" in self.paths:
            say(".golangci.yml changed; run with --full to lint every Go module under the new configuration.")
            raise Stop(2)
        if not self.full and self.lint_module:
            say("tools/golangci-lint changed; run with --full to lint every Go module with the new linter.")
            raise Stop(2)

        self.search_gopath()
        self.check_tools()
        self.need_tool("uv")
        self.need_tool("go")

        self.run_check("check-plan-status", check_plan_status.main, [])
        if self.plan_state:
            # The whole tree, not the named paths: a state file is legal only beside
            # its parent's and its phases', which the run may not name.
            self.run_check("check-plan-state", check_plan_state.main, [])
        self.build_dir = tempfile.mkdtemp(prefix="flowseer-build.", dir=os.environ.get("TMPDIR") or "/tmp")
        self.run(["go", "build", "-o", f"{self.build_dir}/check-guarantees", "./tools/check-guarantees"])
        if self.full:
            self.run([f"{self.build_dir}/check-guarantees", "--all"])
        else:
            self.run([f"{self.build_dir}/check-guarantees", "--", *self.paths])
        self.report_test_changes()

        if self.web:
            self.run_web_gates()
        if self.markdown_files:
            self.need_tool("uv")
            self.run_check("check-markdown-links", check_markdown_links.main, list(self.markdown_files))
            self.run_check("check-prose", check_prose.main, ["--quiet", *self.markdown_files])
        if self.go_files:
            self.check_format()
        if self.modules:
            self.need_tool("go")
            if self.lint_module:
                self.run(["go", "-C", "tools/golangci-lint", "mod", "verify"])
            for module in self.modules:
                self.check_module(module)
        self.check_repository_wide()
        for dependent in self.dependent_modules:
            self.check_dependent_module(dependent)
        if self.proto:
            self.check_protobuf()
        if self.mib:
            self.check_mib()
        if self.hook_tooling:
            self.check_hook_tooling()
        if self.service_otel_integration:
            self.run_check("service-otel-integration", service_otel_integration.main, [])
        if self.full:
            self.run(["git", "diff", "--check", self.whitespace_base()])
        else:
            self.run(["git", "diff", "--check", self.whitespace_base(), "--", *self.paths])

        # Before the dirty marker is cleared and the receipt is written, because
        # those two are what every downstream consumer reads. A run that exits
        # non-zero here having already cleared them would tell its operator it
        # failed and tell the Stop hook and land that the tree was verified.
        if not self.gates_selected():
            say("FlowSeer verification selected no build, test or lint gate for these paths.")
            say(f"  paths: {' '.join(self.paths) if self.paths else '<none>'}")
            raise Stop(2)

        # Before the marker report, whose lines a reader takes as the remedy
        # and expects to run up to the verdict.
        self.report_timings()
        git_dir = self.git_dir()
        self.clear_marker(f"{git_dir}/{MARKER_NAME}")
        self.rewrite_tree_state(git_dir)
        self.write_receipt(git_dir)
        out("FlowSeer verification passed.")

    def whitespace_base(self) -> str:
        """The commit the whitespace gate diffs from.

        A unit commits, then verifies its paths, and the default base is
        HEAD, so the diff the gate reads is empty and a committed trailing
        space passes every per-unit run until a `--base main` run at the
        end. Without an explicit base the gate diffs from where the branch
        left main, which holds every commit of the branch and the working
        tree. On main itself, or without a main, that is HEAD as before.
        """
        if self.base_given:
            return self.base
        for candidate in ("main", "master"):
            code, printed = git("merge-base", candidate, "HEAD", quiet=True)
            if code == 0:
                return line_text(printed)
        return self.base

    def cleanup(self) -> None:
        if self.build_dir:
            shutil.rmtree(self.build_dir, ignore_errors=True)

    def report(self, status: int) -> None:
        """Write the verdict line, which is always the last thing a failed run says."""
        if self.gate:
            say(f"FlowSeer verification FAILED (exit {status}) in gate: {self.gate}")
        else:
            say(f"FlowSeer verification FAILED (exit {status}).")


def find_entries() -> list[str]:
    """Return every file and directory below the working directory as `./<path>`, the way `find .` lists them.

    The repository's own `.git` and the agent worktree directories are not
    entered, and a linked directory is listed but not followed.
    """
    skipped = ("./.git", "./.claude/worktrees", "./.codex/worktrees")
    found = []
    for current, directories, files in os.walk("."):
        directories[:] = [name for name in directories if f"{current}/{name}" not in skipped]
        found += [f"{current}/{name}" for name in (*directories, *files)]
    return found


def replaces_root(modfile: str) -> bool:
    """Whether a go.mod holds a line that replaces the root module with the tree."""
    try:
        content = Path(modfile).read_bytes()
    except OSError:
        return False
    return re.search(rb"^replace go\.aledante\.io/FlowSeer ", content, re.MULTILINE) is not None


def main(argv: list[str]) -> int:
    verification = Verification()
    status = 1
    try:
        verification.execute(argv)
        status = 0
    except Stop as stop:
        status = stop.status
    except KeyboardInterrupt:
        status = 130
    except SystemExit as leave:
        status = system_exit_status(leave)
    except BaseException:
        traceback.print_exc()
        status = 1
    finally:
        verification.cleanup()
        if status != 0:
            verification.report_timings()
            verification.report(status)
    return status
