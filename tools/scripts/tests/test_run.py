import contextlib
import importlib
import io
import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path

import run

SCRIPTS = Path(__file__).resolve().parent.parent
RUN = SCRIPTS / "run.py"

ECHO = """
def main(argv):
    print(" ".join(argv))
    return 3
"""


def invoke(*args: str, cwd: Path | None = None, runner: Path = RUN) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(runner), *args], cwd=cwd, capture_output=True, text=True, check=False
    )


class Fixture:
    """A scripts root with its own copy of run.py, so it collects only what a test writes."""

    def __init__(self, root: Path):
        self.root = root
        shutil.copy(RUN, root / "run.py")

    def write(self, relative: str, text: str = "") -> Path:
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(textwrap.dedent(text), encoding="utf-8")
        return path

    def group(self, relative: str, commands: str, **modules: str) -> None:
        self.write(f"{relative}/__init__.py", f"COMMANDS = {commands}\n")
        for name, source in modules.items():
            self.write(f"{relative}/{name}.py", source)

    def run(self, *args: str, cwd: Path | None = None) -> subprocess.CompletedProcess[str]:
        return invoke(*args, cwd=cwd, runner=self.root / "run.py")


class FixtureCase(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.fixture = Fixture(Path(directory.name))


class LiveRegistryTest(unittest.TestCase):
    def test_unknown_command_exits_2_and_prints_the_registry(self):
        result = invoke("verify", "no-such-check")
        self.assertEqual(result.returncode, 2)
        self.assertIn("verify check-markdown-links", result.stderr.splitlines())

    def test_unknown_group_exits_2(self):
        self.assertEqual(invoke("nope", "x").returncode, 2)

    def test_list_holds_the_built_in_and_a_verify_command(self):
        result = invoke("list")
        self.assertEqual(result.returncode, 0, result.stderr)
        lines = result.stdout.splitlines()
        self.assertEqual(lines, sorted(lines))
        for expected in ("list", "test", "verify check-markdown-links"):
            self.assertIn(expected, lines)

    def test_list_runs_from_any_working_directory(self):
        docs = SCRIPTS.parent.parent / "docs"
        elsewhere = subprocess.run(
            [sys.executable, "../tools/scripts/run.py", "list"],
            cwd=docs,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(elsewhere.returncode, 0, elsewhere.stderr)
        self.assertEqual(elsewhere.stdout, invoke("list").stdout)

    def test_registry_modules_import(self):
        for group, commands in run.collect_groups().items():
            for command, module in commands.items():
                with self.subTest(group=group, command=command):
                    importlib.import_module(module)


class FixtureRegistryTest(FixtureCase):
    def test_list_sorts_an_unsorted_literal_and_skips_non_groups(self):
        self.fixture.group("zeta", '{"b": "mod", "a": "mod"}', mod=ECHO)
        self.fixture.group("skills/alpha", '{"c": "mod"}', mod=ECHO)
        self.fixture.write("skills/__init__.py")
        self.fixture.group("lib", '{"x": "mod"}')
        self.fixture.group("tests", '{"y": "mod"}')
        result = self.fixture.run("list")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines(), ["alpha c", "list", "test", "zeta a", "zeta b"])

    def test_command_receives_its_arguments_and_sets_the_exit_code(self):
        self.fixture.group("zeta", '{"echo": "mod"}', mod=ECHO)
        self.fixture.group("skills/alpha", '{"echo": "mod"}', mod=ECHO)
        for group in ("zeta", "alpha"):
            with self.subTest(group=group):
                result = self.fixture.run(group, "echo", "--flag", "value")
                self.assertEqual(result.returncode, 3, result.stderr)
                self.assertEqual(result.stdout.strip(), "--flag value")

    def test_unknown_group_exits_2_and_prints_the_registry(self):
        self.fixture.group("zeta", '{"a": "mod"}', mod=ECHO)
        result = self.fixture.run("nope", "a")
        self.assertEqual(result.returncode, 2)
        self.assertIn("zeta a", result.stderr.splitlines())

    def test_underscored_group_is_dispatched_with_a_hyphen(self):
        self.fixture.group("my_group", '{"echo": "mod"}', mod=ECHO)
        self.fixture.group("skills/my_skill", '{"echo": "mod"}', mod=ECHO)
        self.fixture.write("skills/__init__.py")
        listed = self.fixture.run("list").stdout.splitlines()
        self.assertIn("my-group echo", listed)
        self.assertIn("my-skill echo", listed)
        self.assertEqual(self.fixture.run("my-group", "echo", "x").returncode, 3)
        self.assertEqual(self.fixture.run("my-skill", "echo", "x").returncode, 3)

    def test_a_commands_value_that_is_not_a_string_literal_dict_exits_2_naming_the_file(self):
        bad = {
            "computed call": 'dict(a="a")',
            "integer value": '{"a": 1}',
            "name value": '{"a": mod}',
            "spread": '{**{"a": "a"}}',
            "list": '["a"]',
        }
        for label, commands in bad.items():
            with self.subTest(label):
                root = self.fixture.root
                shutil.rmtree(root / "zeta", ignore_errors=True)
                self.fixture.group("zeta", commands, mod=ECHO)
                for args in (("list",), ("zeta", "a"), ("other", "b")):
                    result = self.fixture.run(*args)
                    self.assertEqual(result.returncode, 2, result.stderr)
                    self.assertIn(str(Path("zeta") / "__init__.py"), result.stderr)

    def test_a_missing_commands_exits_2_naming_the_file(self):
        self.fixture.write("zeta/__init__.py", "OTHER = {}\n")
        result = self.fixture.run("list")
        self.assertEqual(result.returncode, 2)
        self.assertIn(str(Path("zeta") / "__init__.py"), result.stderr)

    def test_a_skill_group_is_read_as_a_literal_too(self):
        self.fixture.group("skills/alpha", 'dict(a="a")')
        result = self.fixture.run("list")
        self.assertEqual(result.returncode, 2)
        self.assertIn(str(Path("skills") / "alpha" / "__init__.py"), result.stderr)

    def test_a_group_name_in_both_places_exits_2_naming_the_file(self):
        self.fixture.group("dup", '{"a": "mod"}', mod=ECHO)
        self.fixture.group("skills/dup", '{"b": "mod"}', mod=ECHO)
        result = self.fixture.run("list")
        self.assertEqual(result.returncode, 2)
        self.assertIn(str(Path("skills") / "dup" / "__init__.py"), result.stderr)

    def test_a_command_module_that_raises_on_import_stops_only_its_own_command(self):
        self.fixture.group("bad", '{"boom": "boom"}', boom='raise RuntimeError("at import")\n')
        self.fixture.group("skills/good", '{"echo": "mod"}', mod=ECHO)
        listed = self.fixture.run("list")
        self.assertEqual(listed.returncode, 0, listed.stderr)
        self.assertIn("bad boom", listed.stdout.splitlines())
        self.assertNotEqual(self.fixture.run("bad", "boom").returncode, 0)
        self.assertEqual(self.fixture.run("good", "echo").returncode, 3)

    def test_test_exits_2_naming_a_test_directory_without_an_init(self):
        self.fixture.write("tests/__init__.py")
        self.fixture.write("tests/sub/test_x.py", "")
        result = self.fixture.run("test", str(self.fixture.root / "tests"))
        self.assertEqual(result.returncode, 2)
        self.assertIn(str(Path("tests") / "sub"), result.stderr)

    def test_test_names_an_inner_directory_whose_parent_lacks_an_init(self):
        self.fixture.write("tests/__init__.py")
        self.fixture.write("tests/outer/inner/__init__.py")
        self.fixture.write("tests/outer/inner/test_x.py", "")
        result = self.fixture.run("test", str(self.fixture.root / "tests"))
        self.assertEqual(result.returncode, 2)
        self.assertIn(str(Path("tests") / "outer"), result.stderr)

    def test_test_of_an_empty_directory_exits_1_with_zero_tests_run(self):
        self.fixture.write("tests/__init__.py")
        self.fixture.write(
            "tests/test_ok.py",
            """
            import unittest

            class Ok(unittest.TestCase):
                def test_ok(self):
                    pass
            """,
        )
        empty = self.fixture.root / "empty"
        empty.mkdir()
        result = self.fixture.run("test", str(empty))
        self.assertEqual(result.returncode, 1)
        self.assertIn("Ran 0 tests", result.stderr)
        self.assertIn("ran zero tests", result.stderr)

    def test_test_without_an_argument_runs_the_fixtures_own_tests(self):
        self.fixture.write("tests/__init__.py")
        self.fixture.write(
            "tests/test_ok.py",
            """
            import unittest

            class Ok(unittest.TestCase):
                def test_ok(self):
                    pass
            """,
        )
        result = self.fixture.run("test")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Ran 1 test", result.stderr)


class ParameterTest(FixtureCase):
    def test_collect_groups_takes_the_scripts_root(self):
        self.fixture.group("zeta", '{"a": "mod"}', mod=ECHO)
        self.fixture.group("skills/alpha", '{"b": "mod"}', mod=ECHO)
        groups = run.collect_groups(self.fixture.root)
        self.assertEqual(sorted(groups), ["alpha", "zeta"])
        self.assertEqual(list(groups["alpha"]), ["b"])

    def test_main_takes_the_scripts_root(self):
        self.fixture.group("zeta", '{"a": "mod"}', mod=ECHO)
        with contextlib.redirect_stdout(io.StringIO()) as out:
            self.assertEqual(run.main(["list"], self.fixture.root), 0)
        self.assertIn("zeta a", out.getvalue().splitlines())
        with contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(run.main(["nope"], self.fixture.root), 2)


if __name__ == "__main__":
    unittest.main()
