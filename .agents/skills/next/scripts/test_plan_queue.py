import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("plan-queue.py")
PARENT = "docs/plans/2026-01-01-parent-plan.md"
PHASE = "docs/plans/2026-01-02-phase-plan.md"
PLAIN = "docs/plans/2026-01-03-plain-plan.md"


class PlanQueueTest(unittest.TestCase):
    """Runs plan-queue.py in a scratch repository with a `main` and a branch."""

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.root = Path(self.directory.name)
        self.env = {
            **os.environ,
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_AUTHOR_NAME": "test",
            "GIT_AUTHOR_EMAIL": "test@example.com",
            "GIT_COMMITTER_NAME": "test",
            "GIT_COMMITTER_EMAIL": "test@example.com",
        }
        self.git("init", "-q", "-b", "main")
        (self.root / "docs/plans").mkdir(parents=True)
        self.write(PARENT, self.parent(""))
        self.write(PHASE, f"---\nstatus: planned\nparent: {PARENT}\n---\n")
        self.write(PLAIN, "---\nstatus: planned\n---\n")
        self.commit("plans")
        self.git("checkout", "-q", "-b", "work")

    def tearDown(self):
        self.directory.cleanup()

    def git(self, *args):
        subprocess.run(["git", *args], cwd=self.root, env=self.env, check=True, capture_output=True)

    def write(self, rel, text):
        (self.root / rel).write_text(text, encoding="utf-8")

    def commit(self, message):
        self.git("add", "-A")
        self.git("commit", "-q", "-m", message)

    def parent(self, landed):
        return (
            "---\nstatus: planned\n---\n\n"
            f"### U1. Phase\nFiles: `{PHASE}`\nLanded: {landed}\n"
        )

    def groups(self):
        out = subprocess.run(
            [sys.executable, str(SCRIPT), "--json"],
            cwd=self.root,
            env=self.env,
            check=True,
            capture_output=True,
            text=True,
        )
        return {row["path"]: row["group"] for row in json.loads(out.stdout)}

    def finish(self, rel, extra=""):
        self.write(rel, f"---\nstatus: implemented\n{extra}review: accept\ncompound: no lesson\n---\n")

    def test_finished_phase_on_branch_is_owed_a_land(self):
        self.write(PARENT, self.parent("`abcdef0..abcdef1`"))
        self.finish(PHASE, f"parent: {PARENT}\n")
        self.commit("phase done")
        self.assertEqual(self.groups().get(PHASE), "land")

    def test_finished_phase_with_empty_landed_line_needs_implement(self):
        self.finish(PHASE, f"parent: {PARENT}\n")
        self.commit("phase done, Landed empty")
        self.assertEqual(self.groups().get(PHASE), "in-progress")

    def test_unreviewed_phase_with_empty_landed_line_needs_implement(self):
        self.write(PHASE, f"---\nstatus: implemented\nparent: {PARENT}\n---\n")
        self.commit("phase implemented, Landed empty, no review")
        self.assertEqual(self.groups().get(PHASE), "in-progress")

    def test_finished_plain_plan_on_branch_is_owed_a_land(self):
        self.finish(PLAIN)
        self.commit("plain done")
        self.assertEqual(self.groups().get(PLAIN), "land")

    def test_finished_plan_on_main_is_owed_a_retire(self):
        self.git("checkout", "-q", "main")
        self.finish(PLAIN)
        self.commit("plain done on main")
        self.assertEqual(self.groups().get(PLAIN), "retire")

    def test_finished_plan_sent_back_to_plan_is_a_replan(self):
        self.git("checkout", "-q", "main")
        self.write(PLAIN, "---\nstatus: implemented\nartifact_readiness: needs-decisions\n---\n")
        self.commit("plain sent back to plan")
        self.assertEqual(self.groups().get(PLAIN), "replan")

    def test_abandoned_plan_that_needs_decisions_still_retires(self):
        self.git("checkout", "-q", "main")
        self.write(PLAIN, "---\nstatus: abandoned\nartifact_readiness: needs-decisions\n---\n")
        self.commit("plain abandoned")
        self.assertEqual(self.groups().get(PLAIN), "retire")

    def test_finished_parent_that_needs_decisions_still_retires(self):
        self.git("checkout", "-q", "main")
        self.write(
            PARENT,
            self.parent("abc1234..def5678").replace(
                "status: planned", "status: implemented\nartifact_readiness: needs-decisions"
            ),
        )
        self.commit("parent done on main")
        self.assertEqual(self.groups().get(PARENT), "retire")


if __name__ == "__main__":
    unittest.main()
