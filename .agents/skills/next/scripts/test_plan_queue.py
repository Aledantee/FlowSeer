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

    def test_sent_back_plan_changed_on_this_branch_is_a_replan(self):
        self.finish(PLAIN, "artifact_readiness: needs-decisions\n")
        self.commit("plain sent back on the branch")
        self.assertEqual(self.groups().get(PLAIN), "replan")

    def test_sent_back_phase_on_this_branch_with_empty_landed_line_is_a_replan(self):
        for fields in ("review: rework\n", "review: accept\ncompound: no lesson\n", ""):
            with self.subTest(fields=fields):
                self.write(
                    PHASE,
                    f"---\nstatus: implemented\nartifact_readiness: needs-decisions\n{fields}parent: {PARENT}\n---\n",
                )
                self.commit("phase sent back, Landed empty")
                self.assertEqual(self.groups().get(PHASE), "replan")

    def test_sent_back_plan_with_a_rework_verdict_is_a_replan(self):
        self.git("checkout", "-q", "main")
        self.write(PLAIN, "---\nstatus: implemented\nartifact_readiness: needs-decisions\nreview: rework\n---\n")
        self.commit("plain sent back, verdict kept")
        self.assertEqual(self.groups().get(PLAIN), "replan")

    def test_sent_back_phase_waits_for_an_unlanded_prerequisite(self):
        first = "docs/plans/2026-01-04-first-plan.md"

        def parent(landed):
            return (
                "---\nstatus: planned\n---\n\n"
                f"### U1. First\nFiles: `{first}`\nLanded: {landed}\n\n"
                f"### U2. Phase\nFiles: `{PHASE}`\nAfter: U1\nLanded:\n"
            )

        self.git("checkout", "-q", "main")
        self.write(first, f"---\nstatus: planned\nparent: {PARENT}\n---\n")
        for review in ("", "review: rework\n"):
            with self.subTest(review=review):
                self.write(
                    PHASE,
                    f"---\nstatus: implemented\nartifact_readiness: needs-decisions\n{review}parent: {PARENT}\n---\n",
                )
                self.write(PARENT, parent(""))
                self.commit("phase sent back, prerequisite open")
                self.assertEqual(self.groups().get(PHASE), "waiting")
                self.write(first, f"---\nstatus: implemented\nparent: {PARENT}\n---\n")
                self.write(PARENT, parent("`abcdef0..abcdef1`"))
                self.commit("prerequisite implemented, not reviewed")
                self.assertEqual(self.groups().get(PHASE), "waiting")
                self.finish(first, f"parent: {PARENT}\n")
                self.commit("prerequisite reviewed and compounded")
                self.assertEqual(self.groups().get(PHASE), "replan")
                self.write(first, f"---\nstatus: planned\nparent: {PARENT}\n---\n")

    def test_dependent_phase_waits_until_its_prerequisite_is_finished(self):
        first = "docs/plans/2026-01-04-first-plan.md"
        self.write(
            PARENT,
            "---\nstatus: planned\n---\n\n"
            f"### U1. First\nFiles: `{first}`\nLanded: `abcdef0..abcdef1`\n\n"
            f"### U2. Phase\nFiles: `{PHASE}`\nAfter: U1\nLanded:\n",
        )
        for fields in (
            "status: implemented\n",
            "status: implemented\nreview: fixes needed\n",
            "status: implemented\nreview: rework\n",
            "status: implemented\nreview: accept\n",
            "status: planned\n",
        ):
            with self.subTest(fields=fields):
                self.write(first, f"---\n{fields}parent: {PARENT}\n---\n")
                self.commit("prerequisite has a range and is not finished")
                self.assertEqual(self.groups().get(PHASE), "waiting")
        self.finish(first, f"parent: {PARENT}\n")
        self.commit("prerequisite reviewed and compounded")
        self.assertEqual(self.groups().get(PHASE), "in-progress")

    def test_dependent_phase_is_free_once_its_prerequisite_is_on_main(self):
        first = "docs/plans/2026-01-04-first-plan.md"
        self.git("checkout", "-q", "main")
        self.write(first, f"---\nstatus: implemented\nparent: {PARENT}\n---\n")
        self.commit("prerequisite implemented on main")
        sha = subprocess.run(
            ["git", "rev-parse", "HEAD"], cwd=self.root, env=self.env, check=True, capture_output=True, text=True
        ).stdout.strip()
        self.write(
            PARENT,
            "---\nstatus: planned\n---\n\n"
            f"### U1. First\nFiles: `{first}`\nLanded: `{sha}..{sha}`\n\n"
            f"### U2. Phase\nFiles: `{PHASE}`\nAfter: U1\nLanded:\n",
        )
        self.commit("range recorded")
        self.assertEqual(self.groups().get(PHASE), "in-progress")

    def test_rework_verdict_alone_is_a_replan(self):
        self.write(PLAIN, "---\nstatus: implemented\nreview: rework\n---\n")
        self.commit("plain reviewed, rework")
        self.assertEqual(self.groups().get(PLAIN), "replan")
        self.write(PLAIN, "---\nstatus: implemented\nreview: fixes needed\n---\n")
        self.commit("plain reviewed, fixes needed")
        self.assertEqual(self.groups().get(PLAIN), "unchecked")

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
