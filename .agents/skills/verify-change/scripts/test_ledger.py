import datetime
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

LEDGER = Path(__file__).with_name("ledger.py")


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

    def git(self, *args):
        return subprocess.run(
            ["git", "-c", "user.name=Ledger", "-c", "user.email=ledger@example.invalid", *args],
            cwd=self.root,
            check=True,
            capture_output=True,
            text=True,
        ).stdout.strip()

    def commit(self, name):
        (self.root / f"{name}.txt").write_text(name)
        self.git("add", "-A")
        self.git("commit", "-qm", name)
        return self.git("rev-parse", "--short=8", "HEAD")

    def ledger(self, *args):
        return subprocess.run(
            [sys.executable, str(LEDGER), *args], cwd=self.root, capture_output=True, text=True, check=False
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

    def test_passed_refuses_a_receipt_older_than_the_unit_base(self):
        self.ledger("set", "U1", "in_progress")
        self.commit("unit-one")
        self.receipt("2000-01-01T00:00:00Z")

        refused = self.ledger("set", "U1", "passed")
        self.assertEqual(refused.returncode, 1)
        self.assertIn("is older than U1's base", refused.stderr)

        fresh = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        self.receipt(fresh)
        self.assertEqual(self.ledger("set", "U1", "passed").returncode, 0)
        self.assertEqual(self.unit("U1")["verified_at"], fresh)


if __name__ == "__main__":
    unittest.main()
