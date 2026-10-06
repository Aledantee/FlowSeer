import importlib
import subprocess
import tempfile
import unittest
from pathlib import Path

import run

SCRIPTS = Path(__file__).resolve().parent.parent
RUN = SCRIPTS / "run.py"


def invoke(*args: str, cwd: Path | None = None) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["uv", "run", str(RUN), *args], cwd=cwd, capture_output=True, text=True, check=False
    )


class RunTest(unittest.TestCase):
    def test_unknown_command_exits_2_and_prints_the_registry(self):
        result = invoke("verify", "no-such-check")
        self.assertEqual(result.returncode, 2)
        self.assertIn("verify check-markdown-links", result.stderr.splitlines())

    def test_unknown_group_exits_2(self):
        self.assertEqual(invoke("nope", "x").returncode, 2)

    def test_list_is_sorted_one_command_per_line(self):
        result = invoke("list")
        self.assertEqual(result.returncode, 0, result.stderr)
        lines = result.stdout.splitlines()
        self.assertEqual(lines, sorted(lines))
        self.assertEqual(lines, ["list", "test", "verify check-markdown-links"])

    def test_test_fails_on_zero_tests(self):
        with tempfile.TemporaryDirectory() as empty:
            result = invoke("test", empty)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("ran zero tests", result.stderr)

    def test_list_runs_from_any_working_directory(self):
        elsewhere = invoke("list", cwd=SCRIPTS.parent.parent / "docs")
        self.assertEqual(elsewhere.stdout, invoke("list").stdout)

    def test_registry_modules_import(self):
        for group, commands in run.collect_groups().items():
            for command, module in commands.items():
                with self.subTest(group=group, command=command):
                    importlib.import_module(f"{group.replace('-', '_')}.{module}")


if __name__ == "__main__":
    unittest.main()
