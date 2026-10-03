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

    def run_queue(self, *args):
        return subprocess.run(
            [sys.executable, str(SCRIPT), *args],
            cwd=self.root,
            env=self.env,
            check=True,
            capture_output=True,
            text=True,
        ).stdout

    def rows(self):
        return {row["path"]: row for row in json.loads(self.run_queue("--json"))}

    def groups(self):
        return {path: row["group"] for path, row in self.rows().items()}

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

    def test_listed_review_gap_keeps_a_finished_phase_unchecked(self):
        self.write(PARENT, self.parent("`abcdef0..abcdef1`"))
        self.finish(PHASE, f"parent: {PARENT}\n")
        with (self.root / PHASE).open("a", encoding="utf-8") as plan:
            plan.write("## Review gaps\n\n- `a.go:1`: off by one survives\n")
        self.commit("phase done, gap listed")
        self.assertEqual(self.groups().get(PHASE), "unchecked")

    def test_listed_review_gap_keeps_a_finished_plain_plan_unchecked(self):
        self.finish(PLAIN)
        with (self.root / PLAIN).open("a", encoding="utf-8") as plan:
            plan.write("## Review gaps\n\n- `a.go:1`: off by one survives\n")
        self.commit("plain done, gap listed")
        self.assertEqual(self.groups().get(PLAIN), "unchecked")

    def test_emptied_review_gaps_section_leaves_a_plan_owed_a_land(self):
        self.finish(PLAIN)
        with (self.root / PLAIN).open("a", encoding="utf-8") as plan:
            plan.write("## Review gaps\n\n## Notes\n\n- not a gap\n")
        self.commit("plain done, gaps closed")
        self.assertEqual(self.groups().get(PLAIN), "land")

    def test_finished_plan_on_main_is_owed_a_retire(self):
        self.git("checkout", "-q", "main")
        self.finish(PLAIN)
        self.commit("plain done on main")
        self.assertEqual(self.groups().get(PLAIN), "retire")

    def hold_gaps(self, rel):
        # No list: the verdict alone has to keep the plan from landing.
        self.write(rel, "---\nstatus: implemented\nreview: gaps open\ncompound: no lesson\n---\n")

    def test_gaps_open_verdict_on_this_branch_is_unchecked_and_shown(self):
        self.hold_gaps(PLAIN)
        self.commit("plain reviewed, gaps open")
        row = self.rows()[PLAIN]
        self.assertEqual(row["group"], "unchecked")
        self.assertEqual(row["review"], "gaps open")
        self.assertIn("review: gaps open", self.run_queue())

    def test_gaps_open_verdict_on_main_is_not_a_retire(self):
        self.git("checkout", "-q", "main")
        self.hold_gaps(PLAIN)
        self.commit("plain reviewed on main, gaps open")
        self.assertEqual(self.groups().get(PLAIN), "unchecked")

    def test_accept_beside_a_listed_gap_on_main_is_not_a_retire(self):
        self.git("checkout", "-q", "main")
        self.finish(PLAIN)
        with (self.root / PLAIN).open("a", encoding="utf-8") as plan:
            plan.write("## Review gaps\n\n- `a.go:1`: off by one survives\n")
        self.commit("plain done on main, gap listed")
        row = self.rows()[PLAIN]
        self.assertEqual(row["group"], "unchecked")
        self.assertEqual(row["gaps"], 1)
        self.assertIn("review gaps listed: 1", self.run_queue())


if __name__ == "__main__":
    unittest.main()
