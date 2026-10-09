import contextlib
import io
import json
import os
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from pathlib import Path
from unittest import mock

from lib import hookio
from lib.hookio import Kind

SCRIPTS = Path(__file__).resolve().parents[2]


def git(cwd: Path, *args: str) -> None:
    subprocess.run(["git", *args], cwd=cwd, check=True, capture_output=True)


def pipe(test: unittest.TestCase) -> tuple[int, int]:
    """Return the descriptors that read and write a pipe, both closed at cleanup."""
    read, write = os.pipe()
    test.addCleanup(os.close, read)
    test.addCleanup(close_quietly, write)
    return read, write


def close_quietly(fd: int) -> None:
    with contextlib.suppress(OSError):
        os.close(fd)


def closed_pipe(test: unittest.TestCase, data: bytes) -> int:
    """Return a descriptor that yields data and then end of input."""
    read, write = pipe(test)
    os.write(write, data)
    os.close(write)
    return read


def printed(function, *args) -> dict:
    out = io.StringIO()
    with contextlib.redirect_stdout(out):
        function(*args)
    return json.loads(out.getvalue())


class RepositoryCase(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.base = Path(os.path.realpath(directory.name))
        self.root = self.base / "a repository"
        self.root.mkdir()
        git(self.root, "init", "-q", "-b", "main")
        (self.root / "sub").mkdir()
        (self.root / "sub" / "file.txt").write_text("x")
        git(self.root, "add", ".")
        git(
            self.root,
            "-c", "user.name=t", "-c", "user.email=t@example.com",
            "commit", "-q", "-m", "base",
        )  # fmt: skip

    def event(self, cwd: Path | None = None) -> hookio.Event:
        return hookio.Event({}, self.root, "" if cwd is None else f"{cwd.relative_to(self.root)}/")


class ReadInputTest(unittest.TestCase):
    def test_a_silent_open_stream_gives_an_empty_event_inside_six_seconds(self):
        reader, _ = pipe(self)
        started = time.monotonic()
        text = hookio.read_input(reader)
        self.assertEqual(text, "")
        self.assertLess(time.monotonic() - started, 6)

    def test_the_rest_of_the_input_is_awaited_without_a_bound(self):
        reader, writer = pipe(self)
        os.write(writer, b'{"a"')

        def finish():
            time.sleep(0.6)
            os.write(writer, b": 1}")
            os.close(writer)

        thread = threading.Thread(target=finish)
        thread.start()
        self.addCleanup(thread.join)
        self.assertEqual(hookio.read_input(reader, first_byte_seconds=0.2), '{"a": 1}')

    def test_an_empty_closed_stream_is_empty(self):
        self.assertEqual(hookio.read_input(closed_pipe(self, b"")), "")

    def test_input_larger_than_one_read_arrives_whole(self):
        text = "x" * (3 * hookio.READ_SIZE)
        reader, writer = pipe(self)
        threading.Thread(target=lambda: (os.write(writer, text.encode()), os.close(writer))).start()
        self.assertEqual(hookio.read_input(reader), text)

    def test_a_process_that_gave_up_on_a_silent_standard_input_exits_cleanly(self):
        program = (
            "import sys\n"
            f"sys.path.insert(0, {str(SCRIPTS)!r})\n"
            "from lib import hookio\n"
            "print(repr(hookio.read_input(first_byte_seconds=0.2)))\n"
        )
        # communicate() would close the pipe and end the input, so the parent
        # holds it open until the child has exited.
        child = subprocess.Popen(
            [sys.executable, "-c", program],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
        )
        self.addCleanup(child.kill)
        self.addCleanup(child.stdout.close)
        self.addCleanup(child.stdin.close)
        output = child.stdout.read()
        self.assertEqual((child.wait(timeout=30), output), (0, b"''\n"))


class ReadEventTest(RepositoryCase):
    def setUp(self):
        super().setUp()
        # read_event puts the Go tools on PATH.
        patch = mock.patch.dict(os.environ)
        patch.start()
        self.addCleanup(patch.stop)

    def read(self, text: str) -> hookio.Event | None:
        return hookio.read_event(closed_pipe(self, text.encode()))

    def test_a_silent_open_stream_is_a_malformed_event(self):
        reader, _ = pipe(self)
        started = time.monotonic()
        with self.assertRaises(hookio.MalformedEvent):
            hookio.read_event(reader, first_byte_seconds=0.2)
        self.assertLess(time.monotonic() - started, 3)

    def test_a_host_without_git_has_no_event(self):
        empty = self.base / "empty"
        empty.mkdir()
        os.environ["PATH"] = str(empty)
        self.assertIsNone(self.read(json.dumps({"cwd": str(self.root)})))

    def test_the_event_carries_the_physical_root_and_the_session_prefix(self):
        link = self.base / "link"
        link.symlink_to(self.root)
        event = self.read(json.dumps({"cwd": str(link / "sub"), "tool_name": "Edit"}))
        self.assertEqual(event.root, self.root)
        self.assertEqual(event.prefix, "sub/")
        self.assertEqual(event.data["tool_name"], "Edit")

    def test_the_prefix_is_empty_at_the_top(self):
        self.assertEqual(self.read(json.dumps({"cwd": str(self.root)})).prefix, "")

    def test_a_session_outside_a_repository_has_no_event(self):
        with tempfile.TemporaryDirectory() as bare:
            self.assertIsNone(self.read(json.dumps({"cwd": bare})))

    def test_malformed_input_raises(self):
        for text in ("", "not json", "{"):
            with self.subTest(text=text), self.assertRaises(hookio.MalformedEvent):
                self.read(text)

    def test_null_and_false_are_rejected_as_jq_does(self):
        for text in ("null", "false"):
            with self.subTest(text=text), self.assertRaises(hookio.MalformedEvent):
                self.read(text)

    def test_any_other_json_value_parses(self):
        self.assertEqual(self.read_in_root("0").data, 0)

    def read_in_root(self, text: str) -> hookio.Event | None:
        previous = os.getcwd()
        os.chdir(self.root)
        self.addCleanup(os.chdir, previous)
        return self.read(text)

    def test_an_empty_cwd_means_the_process_directory(self):
        event = self.read_in_root(json.dumps({"cwd": ""}))
        self.assertEqual(event.root, self.root)


class ToolPathsTest(unittest.TestCase):
    def test_the_file_path_then_the_patch_paths(self):
        patch = "\n".join(
            [
                "*** Begin Patch",
                "*** Add File: a/new.go",
                "+x",
                "*** Update File: b.go",
                "*** Move to: c.go",
                "*** Delete File: d with space.go",
                "*** End Patch",
            ]
        )
        data = {"tool_input": {"file_path": "f.go", "command": patch}}
        self.assertEqual(
            hookio.tool_paths(data), ["f.go", "a/new.go", "b.go", "c.go", "d with space.go"]
        )

    def test_the_notebook_path_is_taken_when_there_is_no_file_path(self):
        self.assertEqual(hookio.tool_paths({"tool_input": {"notebook_path": "n.ipynb"}}), ["n.ipynb"])

    def test_the_file_path_shadows_the_notebook_path(self):
        data = {"tool_input": {"file_path": "f.go", "notebook_path": "n.ipynb"}}
        self.assertEqual(hookio.tool_paths(data), ["f.go"])

    def test_a_marker_that_does_not_start_a_line_is_not_a_path(self):
        data = {"tool_input": {"command": "echo '*** Add File: x.go'"}}
        self.assertEqual(hookio.tool_paths(data), [])

    def test_an_event_without_a_tool_input_has_no_paths(self):
        for data in ({}, [], "x", {"tool_input": "x"}, {"tool_input": {"file_path": ""}}):
            with self.subTest(data=data):
                self.assertEqual(hookio.tool_paths(data), [])


class RelativePathTest(RepositoryCase):
    def test_a_path_inside_the_checkout_is_relative_to_it(self):
        resolved = hookio.relative_path(self.event(), str(self.root / "sub" / "file.txt"))
        self.assertEqual(resolved, hookio.Resolved(Kind.INSIDE, "sub/file.txt"))

    def test_a_relative_path_is_taken_from_the_session_directory(self):
        resolved = hookio.relative_path(self.event(self.root / "sub"), "new/file.txt")
        self.assertEqual(resolved, hookio.Resolved(Kind.INSIDE, "sub/new/file.txt"))

    def test_a_relative_path_that_climbs_out_lands_where_it_lands(self):
        event = self.event(self.root / "sub")
        self.assertEqual(
            hookio.relative_path(event, "../top.txt"), hookio.Resolved(Kind.INSIDE, "top.txt")
        )
        self.assertEqual(hookio.relative_path(event, "../../outside.txt").kind, Kind.OUTSIDE)

    def test_a_path_outside_every_checkout(self):
        with tempfile.TemporaryDirectory() as bare:
            resolved = hookio.relative_path(self.event(), str(Path(os.path.realpath(bare)) / "x.md"))
        self.assertEqual(resolved, hookio.Resolved(Kind.OUTSIDE, ""))

    def test_a_path_in_an_unrelated_repository_is_outside(self):
        other = self.base / "other"
        other.mkdir()
        git(other, "init", "-q")
        self.assertEqual(hookio.relative_path(self.event(), str(other / "x.md")).kind, Kind.OUTSIDE)

    def test_another_checkout_of_the_repository_has_its_own_relative_path(self):
        sibling = self.base / "sibling"
        git(self.root, "worktree", "add", "-q", "-b", "side", str(sibling))
        resolved = hookio.relative_path(self.event(), str(sibling / "sub" / "new.txt"))
        self.assertEqual(resolved, hookio.Resolved(Kind.OTHER_CHECKOUT, "sub/new.txt"))

    def test_a_dot_dot_in_the_missing_tail_is_unsafe(self):
        for candidate in ("missing/../../escape.txt", "missing/./x.txt"):
            with self.subTest(candidate=candidate):
                # A Path would fold the `.` away before the hook saw it.
                resolved = hookio.relative_path(self.event(), f"{self.root}/{candidate}")
                self.assertEqual(resolved, hookio.Resolved(Kind.UNSAFE, ""))

    def test_a_missing_directory_below_a_symlink_resolves_through_it(self):
        link = self.base / "link"
        link.symlink_to(self.root)
        resolved = hookio.relative_path(self.event(), str(link / "missing" / "x.txt"))
        self.assertEqual(resolved, hookio.Resolved(Kind.INSIDE, "missing/x.txt"))

    def test_the_checkout_itself_is_the_empty_path(self):
        self.assertEqual(
            hookio.relative_path(self.event(), str(self.root)), hookio.Resolved(Kind.INSIDE, "")
        )


class GoOnPathTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.base = Path(os.path.realpath(directory.name))
        self.empty = self.base / "empty"
        self.empty.mkdir()
        self.install = self.base / "install" / "bin"
        self.install.mkdir(parents=True)
        self.go = self.install / "go"
        self.go.write_text(
            "#!/bin/sh\n"
            'case "$2" in\n'
            "  GOBIN) echo \"$FAKE_GOBIN\" ;;\n"
            '  GOPATH) echo "$FAKE_GOPATH" ;;\n'
            "esac\n"
        )
        self.go.chmod(0o755)
        patch = mock.patch.dict(os.environ, {"PATH": str(self.empty), "FAKE_GOBIN": "", "FAKE_GOPATH": ""})
        patch.start()
        self.addCleanup(patch.stop)

    def test_a_candidate_directory_that_holds_go_is_appended(self):
        hookio.go_on_path([str(self.base / "none"), str(self.install)])
        self.assertEqual(os.environ["PATH"], f"{self.empty}{os.pathsep}{self.install}")

    def test_a_path_that_already_resolves_go_is_left_alone(self):
        other = self.base / "other" / "bin"
        other.mkdir(parents=True)
        (other / "go").symlink_to(self.go)
        os.environ["PATH"] = f"{self.install}{os.pathsep}{self.empty}"
        before = os.environ["PATH"]
        hookio.go_on_path([str(other)])
        self.assertEqual(os.environ["PATH"], before)

    def test_no_candidate_with_go_changes_nothing(self):
        hookio.go_on_path([str(self.base / "none")])
        self.assertEqual(os.environ["PATH"], str(self.empty))

    def test_gobin_is_put_ahead_of_path(self):
        os.environ["PATH"] = f"{self.install}{os.pathsep}{self.empty}"
        os.environ["FAKE_GOBIN"] = "/tools/gobin"
        hookio.go_tools_on_path()
        self.assertEqual(os.environ["PATH"].split(os.pathsep)[0], "/tools/gobin")

    def test_the_first_gopath_entry_is_used_when_gobin_is_empty(self):
        os.environ["PATH"] = f"{self.install}{os.pathsep}{self.empty}"
        os.environ["FAKE_GOPATH"] = f"/first{os.pathsep}/second"
        hookio.go_tools_on_path()
        self.assertEqual(os.environ["PATH"].split(os.pathsep)[0], os.path.join("/first", "bin"))

    def test_an_empty_answer_adds_nothing(self):
        os.environ["PATH"] = f"{self.install}{os.pathsep}{self.empty}"
        before = os.environ["PATH"]
        hookio.go_tools_on_path()
        self.assertEqual(os.environ["PATH"], before)

    def test_the_directory_is_not_added_twice(self):
        os.environ["PATH"] = f"/tools/gobin{os.pathsep}{self.install}"
        os.environ["FAKE_GOBIN"] = "/tools/gobin"
        before = os.environ["PATH"]
        hookio.go_tools_on_path()
        self.assertEqual(os.environ["PATH"], before)


class DecisionTest(unittest.TestCase):
    def test_deny_and_ask_name_the_decision_and_the_reason(self):
        for function, decision in ((hookio.deny, "deny"), (hookio.ask, "ask")):
            with self.subTest(decision=decision):
                self.assertEqual(
                    printed(function, "why: ü"),
                    {
                        "hookSpecificOutput": {
                            "hookEventName": "PreToolUse",
                            "permissionDecision": decision,
                            "permissionDecisionReason": "why: ü",
                        }
                    },
                )

    def test_context_adds_a_message_after_the_tool(self):
        self.assertEqual(
            printed(hookio.context, "note"),
            {"hookSpecificOutput": {"hookEventName": "PostToolUse", "additionalContext": "note"}},
        )


if __name__ == "__main__":
    unittest.main()
