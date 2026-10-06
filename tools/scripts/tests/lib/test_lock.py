import ast
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from lib import lock

SCRIPTS = Path(__file__).resolve().parents[2]
LINES = 500
WRITER = """
import os, sys
sys.path.insert(0, {scripts!r})
from lib import lock
tag, path = sys.argv[1], sys.argv[2]
for i in range({lines}):
    head = f"{{tag}}:{{i}}:".encode()
    tail = b"x" * 200 + b"\\n"
    with lock.exclusive(path) as descriptor:
        os.write(descriptor, head)
        os.write(descriptor, tail)
"""


def modules_under_test():
    """Every module under tools/scripts except the tests and lib/lock.py."""
    for path in sorted(SCRIPTS.rglob("*.py")):
        relative = path.relative_to(SCRIPTS)
        if relative.parts[0] == "tests" or relative == Path("lib/lock.py"):
            continue
        yield relative, ast.parse(path.read_text(encoding="utf-8"), filename=str(path))


def platform_calls(tree):
    """Imports of fcntl or msvcrt and calls of os.pread, as (line, what)."""
    found = []
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            found += [(node.lineno, a.name) for a in node.names if a.name in ("fcntl", "msvcrt")]
        elif isinstance(node, ast.ImportFrom) and node.module in ("fcntl", "msvcrt"):
            found.append((node.lineno, node.module))
        elif (
            isinstance(node, ast.Attribute)
            and node.attr == "pread"
            and isinstance(node.value, ast.Name)
            and node.value.id == "os"
        ):
            found.append((node.lineno, "os.pread"))
    return found


class ExclusiveTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.path = Path(directory.name) / "log"

    def test_two_writers_leave_whole_lines(self):
        code = WRITER.format(scripts=str(SCRIPTS), lines=LINES)
        writers = [
            subprocess.Popen(
                [sys.executable, "-c", code, tag, str(self.path)],
                stderr=subprocess.PIPE,
                text=True,
            )
            for tag in ("a", "b")
        ]
        for writer in writers:
            _, stderr = writer.communicate(timeout=120)
            self.assertEqual(writer.returncode, 0, stderr)
        lines = self.path.read_text(encoding="utf-8").splitlines()
        self.assertEqual(len(lines), 2 * LINES)
        for line in lines:
            tag, index, rest = line.split(":")
            self.assertIn(tag, ("a", "b"))
            self.assertTrue(index.isdigit())
            self.assertEqual(rest, "x" * 200)

    def test_yields_a_descriptor_that_appends_and_reads(self):
        self.path.write_bytes(b"first")
        with lock.exclusive(self.path) as descriptor:
            os.write(descriptor, b"-second")
            os.lseek(descriptor, 0, os.SEEK_SET)
            self.assertEqual(os.read(descriptor, 100), b"first-second")
        self.assertEqual(self.path.read_bytes(), b"first-second")

    def test_creates_a_missing_file(self):
        with lock.exclusive(self.path):
            pass
        self.assertTrue(self.path.is_file())

    def test_releases_the_descriptor_after_an_error(self):
        with self.assertRaises(RuntimeError):
            with lock.exclusive(self.path) as descriptor:
                raise RuntimeError("inside")
        with self.assertRaises(OSError):
            os.fstat(descriptor)


class PlatformCallsTest(unittest.TestCase):
    def test_only_lock_py_touches_fcntl_msvcrt_or_pread(self):
        offenders = [
            f"{relative}:{line} {what}"
            for relative, tree in modules_under_test()
            for line, what in platform_calls(tree)
        ]
        self.assertEqual(offenders, [])

    def test_the_scan_sees_each_form(self):
        tree = ast.parse("import fcntl\nfrom msvcrt import locking\nos.pread(1, 1, 0)\n")
        self.assertEqual(
            [what for _, what in platform_calls(tree)], ["fcntl", "msvcrt", "os.pread"]
        )

    def test_lock_py_itself_is_where_they_live(self):
        tree = ast.parse((SCRIPTS / "lib" / "lock.py").read_text(encoding="utf-8"))
        self.assertTrue(platform_calls(tree))


if __name__ == "__main__":
    unittest.main()
