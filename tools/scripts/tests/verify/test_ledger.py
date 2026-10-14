import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from tests.verify import support

RUN_PY = Path(__file__).resolve().parents[2] / "run.py"


class LedgerCommitTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.git("init", "-q", "-b", "main")
        (self.root / "docs/plans").mkdir(parents=True)
        (self.root / "docs/plans/example-plan.md").write_text("# Example\n")
        self.first = self.commit("plan")
        self.ledger("init", "docs/plans/example-plan.md", "U1", "U2")

    def git(self, *args, env=None):
        return subprocess.run(
            ["git", "-c", "user.name=Ledger", "-c", "user.email=ledger@example.invalid", *args],
            cwd=self.root,
            check=True,
            capture_output=True,
            text=True,
            env=env,
        ).stdout.strip()

    def commit(self, name, committed_at=None):
        (self.root / f"{name}.txt").write_text(name)
        self.git("add", "-A")
        env = {**os.environ, "GIT_COMMITTER_DATE": committed_at} if committed_at else None
        self.git("commit", "-qm", name, env=env)
        return self.git("rev-parse", "--short=8", "HEAD")

    def ledger(self, *args):
        return subprocess.run(
            [sys.executable, str(RUN_PY), "verify", "ledger", *args], cwd=self.root, capture_output=True, text=True, check=False
        )

    def unit(self, uid):
        units = json.loads((self.root / ".git/flowseer-plan-status.json").read_text())["units"]
        return next(unit for unit in units if unit["id"] == uid)

    def passed(self, uid, *extra):
        return self.ledger("set", uid, "passed", "--verified-at", "2026-09-06T12:00:00Z", *extra)

    def test_passed_needs_a_commit_made_since_the_unit_started(self):
        self.assertEqual(self.ledger("set", "U1", "in_progress").returncode, 0)
        self.assertEqual(self.unit("U1")["base"], self.first)

        refused = self.passed("U1")
        self.assertEqual(refused.returncode, 1)
        self.assertIn("holds nothing committed since U1 went in_progress", refused.stderr)

        work = self.commit("unit-one")
        self.assertEqual(self.passed("U1").returncode, 0)
        self.assertEqual(self.unit("U1")["commit"], work)

    def test_passed_refuses_an_earlier_commit_named_explicitly(self):
        earlier = self.commit("earlier-round")
        self.assertEqual(self.ledger("set", "U2", "in_progress").returncode, 0)
        self.commit("unit-two")

        refused = self.passed("U2", "--commit", earlier)
        self.assertEqual(refused.returncode, 1)
        self.assertIn(f"commit {earlier} holds nothing committed since U2", refused.stderr)

    def test_repeated_in_progress_keeps_the_first_base(self):
        self.ledger("set", "U1", "in_progress")
        self.commit("unit-one")
        self.ledger("set", "U1", "in_progress")
        self.assertEqual(self.unit("U1")["base"], self.first)
        self.assertEqual(self.passed("U1").returncode, 0)

    def test_pending_clears_a_base_recorded_after_the_commit(self):
        self.commit("unit-one")
        self.ledger("set", "U1", "in_progress")
        self.assertEqual(self.passed("U1").returncode, 1)
        self.ledger("set", "U1", "pending")
        self.assertNotIn("base", self.unit("U1"))
        self.assertEqual(self.passed("U1").returncode, 0)

    def test_passed_refuses_a_commit_outside_the_branch(self):
        self.ledger("set", "U1", "in_progress")
        self.git("checkout", "-q", "-b", "side")
        side = self.commit("side-work")
        self.git("checkout", "-q", "main")
        self.commit("unit-one")

        refused = self.passed("U1", "--commit", side)
        self.assertEqual(refused.returncode, 1)
        self.assertIn("is not in this branch's history", refused.stderr)

    def receipt(self, verified_at):
        (self.root / ".git/flowseer-verification-receipt").write_text(f"verified_at={verified_at}\nbase=HEAD\n")

    def test_passed_refuses_a_receipt_older_than_the_unit_commit(self):
        self.ledger("set", "U1", "in_progress")
        self.commit("unit-one", committed_at="2090-01-01T00:00:00Z")
        # Newer than the base, older than the unit's commit: the previous
        # unit's run, left behind when this unit's run failed.
        self.receipt("2080-01-01T00:00:00Z")

        refused = self.ledger("set", "U1", "passed")
        self.assertEqual(refused.returncode, 1)
        self.assertIn("is older than U1's commit", refused.stderr)

        self.receipt("2091-01-01T00:00:00Z")
        self.assertEqual(self.ledger("set", "U1", "passed").returncode, 0)
        self.assertEqual(self.unit("U1")["verified_at"], "2091-01-01T00:00:00Z")


class ReceiptTest(support.VerifierCase):
    """The receipt a passing run writes holds four lines that a person or `verify ledger` reads."""

    @support.across
    def test_ledger_reads_the_receipt_a_passing_run_wrote(self):
        self.write("docs/plans/example-plan.md", "# Example\n")
        # The verifier's plan-status check reads the state file beside the plan.
        done = self.run_py("plan", "record", "init", "docs/plans/example-plan.md")
        self.assertEqual(done.returncode, 0, done.stderr)
        self.commit_all("plan")
        self.ledger("init", "docs/plans/example-plan.md", "U1")
        self.assertEqual(self.ledger("set", "U1", "in_progress").returncode, 0)
        self.write("unit-one.txt", "work\n")
        self.commit_all("unit one")
        self.write("README.md", "# Fixture\n\nChanged.\n")
        self.passes("--", "README.md")
        verified_at = self.receipt.read_text().split("\n")[0].removeprefix("verified_at=")

        done = self.ledger("set", "U1", "passed")
        self.assertEqual(done.returncode, 0, done.stderr)
        units = json.loads((self.root / ".git/flowseer-plan-status.json").read_text())["units"]
        self.assertEqual(next(unit for unit in units if unit["id"] == "U1")["verified_at"], verified_at)

    def run_py(self, *args):
        return subprocess.run(
            [sys.executable, str(RUN_PY), *args],
            cwd=self.root,
            capture_output=True,
            text=True,
            check=False,
            env=self.env,
        )

    def ledger(self, *args):
        return self.run_py("verify", "ledger", *args)

    def receipt_lines(self) -> list[str]:
        return self.receipt.read_text().split("\n")

    @support.across
    def test_receipt_for_a_path_with_a_space_quotes_it(self):
        self.write("a b.md", "# Name\n")
        self.passes("--", "a b.md")
        lines = self.receipt_lines()
        self.assertRegex(lines[0], r"^verified_at=\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$")
        self.assertEqual(lines[1:], ["base=HEAD", "full=false", "paths=a\\ b.md ", ""])

    @support.across
    def test_receipt_for_a_full_run_holds_one_empty_word(self):
        self.populate_full()
        self.commit_all("full")
        self.passes("--full")
        lines = self.receipt_lines()
        self.assertRegex(lines[0], r"^verified_at=\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$")
        self.assertEqual(lines[1:], ["base=HEAD", "full=true", "paths='' ", ""])


if __name__ == "__main__":
    unittest.main()
