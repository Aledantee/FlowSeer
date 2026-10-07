import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

RUN_PY = Path(__file__).resolve().parents[2] / "run.py"


class CheckTestIntegrityTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.repo = Path(directory.name).resolve() / "integrity-fixture"
        (self.repo / "pkg/testdata").mkdir(parents=True)
        self.env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        self.env.update(GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM="1")
        self.git("init", "-q")
        self.write(
            "pkg/a_test.go",
            'package pkg\n\nimport "testing"\n\nfunc TestKeep(t *testing.T) {}\n\nfunc TestGone(t *testing.T) {}\n',
        )
        self.write("pkg/b_test.go", "package pkg\n")
        self.write("pkg/testdata/out.golden", "golden\n")
        self.git("add", "pkg")
        self.git("commit", "-qm", "base")

    def git(self, *args: str):
        done = subprocess.run(
            ["git", "-c", "user.name=Hook", "-c", "user.email=hook@example.invalid", *args],
            cwd=self.repo,
            capture_output=True,
            text=True,
            check=False,
            env=self.env,
        )
        self.assertEqual(done.returncode, 0, f"git {' '.join(args)}: {done.stderr}")

    def write(self, path: str, text: str):
        (self.repo / path).write_text(text)

    def check(self, *args: str) -> str:
        done = subprocess.run(
            [sys.executable, str(RUN_PY), "verify", "check-test-integrity", *args],
            cwd=self.repo,
            env=self.env,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(done.returncode, 0, done.stderr)
        return done.stdout

    def test_test_integrity_check_names_deleted_skipped_and_removed_tests_and_rewritten_testdata(self):
        """test integrity check names deleted, skipped, and removed tests and rewritten testdata"""
        self.assertEqual(self.check("HEAD"), "")
        self.write(
            "pkg/a_test.go",
            'package pkg\n\nimport "testing"\n\nfunc TestKeep(t *testing.T) {\n\tt.Skip("flaky")\n}\n',
        )
        self.write("pkg/testdata/out.golden", "changed\n")
        self.write("pkg/testdata/new.golden", "new\n")
        (self.repo / "pkg/b_test.go").unlink()
        output = self.check("HEAD")
        self.assertIn("deleted test file: pkg/b_test.go", output)
        self.assertIn("modified existing testdata: pkg/testdata/out.golden", output)
        self.assertIn('skip added: pkg/a_test.go: t.Skip("flaky")', output)
        self.assertIn("removed test: pkg/a_test.go: TestGone", output)
        self.assertNotIn("TestKeep", output)
        self.assertNotIn("new.golden", output)
        self.assertEqual(self.check("HEAD", "--", "pkg/testdata/new.golden"), "")


if __name__ == "__main__":
    unittest.main()
