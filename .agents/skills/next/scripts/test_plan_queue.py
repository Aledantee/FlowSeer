import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("plan-queue.py")
RECORD = Path(__file__).parents[2] / "plan/scripts/plan_record.py"
PARENT = "docs/plans/2026-01-01-parent-plan.md"
PHASE = "docs/plans/2026-01-02-phase-plan.md"
PLAIN = "docs/plans/2026-01-03-plain-plan.md"
FIRST = "docs/plans/2026-01-04-first-plan.md"
RUN = ("--units", "3", "--from", "2026-01-05T10:00Z", "--to", "2026-01-05T11:30:00Z")


class PlanQueueTest(unittest.TestCase):
    """Runs plan-queue.py in a scratch repository with a `main` and a branch.

    The plans' state files are written with plan_record.py's commands."""

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
        self.write(PARENT, f"# Parent - Plan\n\n### U1. Phase\nFiles: `{PHASE}`\n")
        self.write(PHASE, "# Phase - Plan\n\n### U1. Build it\n### U2: Test it\n")
        self.write(PLAIN, "# Plain - Plan\n")
        self.record("init", PARENT)
        self.record("init", PHASE, "--parent", PARENT)
        self.record("init", PLAIN)
        self.on_main = self.commit("plans")
        self.git("checkout", "-q", "-b", "work")
        self.write("code", "work\n")
        self.on_work = self.commit("work")

    def tearDown(self):
        self.directory.cleanup()

    def git(self, *args):
        out = subprocess.run(["git", *args], cwd=self.root, env=self.env, check=True, capture_output=True, text=True)
        return out.stdout.strip()

    def write(self, rel, text):
        (self.root / rel).write_text(text, encoding="utf-8")

    def commit(self, message):
        self.git("add", "-A")
        self.git("commit", "-q", "-m", message)
        return self.git("rev-parse", "HEAD")

    def record(self, *args):
        out = subprocess.run(
            [sys.executable, str(RECORD), *args], cwd=self.root, env=self.env, capture_output=True, text=True
        )
        self.assertEqual(out.returncode, 0, out.stdout + out.stderr)

    def queue(self):
        return subprocess.run(
            [sys.executable, str(SCRIPT), "--json"], cwd=self.root, env=self.env, capture_output=True, text=True
        )

    def rows(self):
        out = self.queue()
        self.assertEqual(out.returncode, 0, out.stderr)
        return {row["path"]: row for row in json.loads(out.stdout)}

    def groups(self):
        return {path: row["group"] for path, row in self.rows().items()}

    def add_first(self):
        """FIRST joins PARENT as a second phase."""
        self.write(FIRST, "# First - Plan\n")
        self.record("init", FIRST, "--parent", PARENT)
        self.commit("first joins the parent")

    def implement(self, plan, landed=None):
        args = ["implemented", plan, *RUN]
        if landed:
            args += ["--landed", f"{landed}..{landed}"]
        self.record(*args)

    def finish(self, plan, landed=None):
        self.implement(plan, landed)
        self.record("review", plan, "accept")
        self.record("compound", plan, "no lesson")

    def test_finished_phase_on_branch_is_owed_a_land(self):
        self.finish(PHASE, self.on_work)
        self.commit("phase done")
        self.assertEqual(self.groups().get(PHASE), "land")

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
        self.record("replan", PLAIN, "--needs-decisions")
        self.implement(PLAIN)
        self.commit("plain sent back to plan")
        self.assertEqual(self.groups().get(PLAIN), "replan")

    def test_sent_back_plan_changed_on_this_branch_is_a_replan(self):
        self.record("replan", PLAIN, "--needs-decisions")
        self.finish(PLAIN)
        self.commit("plain sent back on the branch")
        self.assertEqual(self.groups().get(PLAIN), "replan")

    def test_sent_back_phase_on_this_branch_is_a_replan(self):
        for verdicts in (("rework",), ("accept",), ()):
            with self.subTest(verdicts=verdicts):
                self.record("replan", PHASE, "--needs-decisions")
                self.implement(PHASE, self.on_work)
                for verdict in verdicts:
                    self.record("review", PHASE, verdict)
                    if verdict == "accept":
                        self.record("compound", PHASE, "no lesson")
                self.commit("phase sent back")
                self.assertEqual(self.groups().get(PHASE), "replan")

    def test_sent_back_plan_with_a_rework_verdict_is_a_replan(self):
        self.git("checkout", "-q", "main")
        self.record("replan", PLAIN, "--needs-decisions")
        self.implement(PLAIN)
        self.record("review", PLAIN, "rework")
        self.commit("plain sent back, verdict kept")
        self.assertEqual(self.groups().get(PLAIN), "replan")

    def test_sent_back_phase_waits_for_an_unlanded_prerequisite(self):
        self.add_first()
        self.record("after", PHASE, FIRST)
        for review in ((), ("review", PHASE, "rework")):
            with self.subTest(review=review):
                self.record("replan", PHASE, "--needs-decisions")
                self.implement(PHASE, self.on_work)
                if review:
                    self.record(*review)
                self.assertEqual(self.groups().get(PHASE), "waiting")
                self.implement(FIRST, self.on_work)
                self.assertEqual(self.groups().get(PHASE), "waiting")
                self.finish(FIRST, self.on_work)
                self.assertEqual(self.groups().get(PHASE), "replan")
                self.record("replan", FIRST)

    def test_dependent_phase_waits_until_its_prerequisite_is_finished(self):
        self.add_first()
        self.record("after", PHASE, FIRST)
        self.implement(FIRST, self.on_work)
        for step in (
            (),
            ("review", FIRST, "fixes needed"),
            ("review", FIRST, "rework"),
            ("review", FIRST, "accept"),
            ("replan", FIRST),
        ):
            with self.subTest(step=step):
                if step:
                    self.record(*step)
                row = self.rows()[PHASE]
                self.assertEqual(row["group"], "waiting")
                self.assertEqual(row["waiting_on"], [FIRST])
        self.finish(FIRST, self.on_work)
        self.assertEqual(self.rows()[PHASE]["group"], "in-progress")
        self.assertEqual(self.rows()[PHASE]["waiting_on"], [])

    def test_dependent_phase_is_free_once_its_prerequisite_is_on_main(self):
        self.add_first()
        self.record("after", PHASE, FIRST)
        self.implement(FIRST, self.on_main)
        self.assertEqual(self.groups().get(PHASE), "in-progress")

    def test_dependent_of_a_retired_phase_that_landed_is_free(self):
        self.add_first()
        self.record("after", PHASE, FIRST)
        self.implement(FIRST, self.on_work)
        self.commit("first implemented")
        self.record("retire", FIRST)
        self.assertEqual(self.groups().get(PHASE), "in-progress")

    def test_dependent_of_a_phase_retired_as_abandoned_keeps_waiting(self):
        self.add_first()
        self.record("after", PHASE, FIRST)
        self.record("abandon", FIRST)
        self.commit("first abandoned")
        self.record("retire", FIRST)
        row = self.rows()[PHASE]
        self.assertEqual(row["group"], "waiting")
        self.assertEqual(row["waiting_on"], [FIRST])

    def test_phase_row_names_its_parent_and_the_phases_still_open(self):
        self.add_first()
        self.implement(FIRST, self.on_work)
        row = self.rows()[PHASE]
        self.assertEqual((row["parent"], row["open_phases"], row["group"]), (PARENT, 1, "in-progress"))
        self.record("replan", FIRST)
        row = self.rows()[PHASE]
        self.assertEqual((row["open_phases"], row["group"]), (2, "ready"))

    def test_rework_verdict_alone_is_a_replan(self):
        self.implement(PLAIN)
        self.record("review", PLAIN, "rework")
        self.commit("plain reviewed, rework")
        self.assertEqual(self.groups().get(PLAIN), "replan")
        self.record("review", PLAIN, "fixes needed")
        self.commit("plain reviewed, fixes needed")
        self.assertEqual(self.groups().get(PLAIN), "unchecked")

    def test_abandoned_plan_that_needs_decisions_still_retires(self):
        self.git("checkout", "-q", "main")
        self.record("replan", PLAIN, "--needs-decisions")
        self.record("abandon", PLAIN)
        self.commit("plain abandoned")
        self.assertEqual(self.groups().get(PLAIN), "retire")

    def test_finished_parent_that_needs_decisions_still_retires(self):
        self.git("checkout", "-q", "main")
        self.record("replan", PARENT, "--needs-decisions")
        self.implement(PHASE, self.on_main)
        self.commit("phase done on main")
        self.record("retire", PHASE)
        self.commit("phase retired on main")
        self.assertEqual(self.groups().get(PARENT), "retire")

    def test_parent_with_phases_left_is_not_listed(self):
        groups = self.groups()
        self.assertNotIn(PARENT, groups)
        self.assertEqual(groups[PHASE], "ready")

    def test_plan_changed_only_in_its_state_file_is_changed_here(self):
        self.implement(PLAIN)
        self.commit("plain implemented on the branch")
        self.assertEqual(self.groups().get(PLAIN), "unchecked")

    def test_plan_another_branch_ran_a_command_on_is_flagged_elsewhere(self):
        self.git("checkout", "-q", "-b", "other", "main")
        self.implement(PLAIN)
        self.commit("plain implemented elsewhere")
        self.git("checkout", "-q", "work")
        rows = self.rows()
        self.assertEqual(rows[PLAIN]["elsewhere"], "other")
        self.assertIsNone(rows[PHASE]["elsewhere"])

    def test_row_carries_the_title_and_the_unit_count(self):
        row = self.rows()[PHASE]
        self.assertEqual((row["title"], row["units"]), ("Phase", 2))

    def test_plan_without_a_state_file_stops_the_queue(self):
        self.write("docs/plans/2026-01-09-stray-plan.md", "# Stray - Plan\n")
        out = self.queue()
        self.assertEqual(out.returncode, 1)
        self.assertIn("plan_record.py init", out.stderr)


if __name__ == "__main__":
    unittest.main()
