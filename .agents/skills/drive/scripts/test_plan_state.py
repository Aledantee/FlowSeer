import os
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("plan-state.py")
RECORD = Path(__file__).parents[2] / "plan/scripts/plan_record.py"
PARENT = "docs/plans/2026-01-01-parent-plan.md"
PLAIN = "docs/plans/2026-01-09-plain-plan.md"
RUN = ("--units", "3", "--from", "2026-01-05T10:00Z", "--to", "2026-01-05T11:30:00Z")


class PlanStateTest(unittest.TestCase):
    """Runs plan-state.py in a scratch repository whose branch `work` is
    one commit ahead of `main`. The plans' state files are written with
    plan_record.py's commands."""

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
        self.write(PARENT, "# Parent - Plan\n")
        self.record("init", PARENT)
        self.on_main = self.commit("parent")
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

    def run_script(self, *args):
        return subprocess.run(
            [sys.executable, str(SCRIPT), *args], cwd=self.root, env=self.env, capture_output=True, text=True
        )

    def record(self, *args):
        out = subprocess.run(
            [sys.executable, str(RECORD), *args], cwd=self.root, env=self.env, capture_output=True, text=True
        )
        self.assertEqual(out.returncode, 0, out.stdout + out.stderr)

    def phase(self, number, *after):
        """A phase of PARENT named by its number, running after the plans given."""
        plan = f"docs/plans/2026-01-0{number}-phase{number}-plan.md"
        self.write(plan, f"# Phase {number} - Plan\n")
        self.record("init", plan, "--parent", PARENT, *(["--after", *after] if after else []))
        return plan

    def implement(self, plan, landed):
        self.record("implemented", plan, *RUN, "--landed", f"{landed}..{landed}")

    def finish(self, plan, landed):
        self.implement(plan, landed)
        self.record("review", plan, "accept")
        self.record("compound", plan, "no lesson")

    def report(self):
        out = self.run_script(PARENT)
        self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        return out.stdout.splitlines()

    def stages(self):
        """The stage printed for each phase, by plan path."""
        found = {}
        for line in self.report():
            parts = re.split(r"\s{2,}", line.strip())
            if len(parts) == 2 and parts[1].startswith("docs/plans/"):
                found[parts[1]] = parts[0]
        return found

    def test_dependent_waits_until_review_and_compound_finish(self):
        first = self.phase(1)
        second = self.phase(2, first)
        self.commit("phases")
        self.implement(first, self.on_work)
        self.assertEqual(self.stages(), {first: "review", second: f"waits for {first}"})

        self.record("review", first, "accept")
        self.assertEqual(self.stages(), {first: "compound", second: f"waits for {first}"})
        self.record("compound", first, "no lesson")
        self.assertEqual(self.stages(), {first: "land", second: "implement"})

        self.record("review", first, "rework")
        self.assertEqual(self.stages(), {first: "plan", second: f"waits for {first}"})
        self.record("review", first, "fixes needed")
        self.assertEqual(self.stages(), {first: "review (verdict: fixes needed)", second: f"waits for {first}"})

        self.record("replan", first)
        self.implement(first, self.on_main)
        self.assertEqual(self.stages(), {first: "on main", second: "implement"})

    def test_implemented_phase_still_waits_for_an_unfinished_prerequisite(self):
        # The order an `--after` list sets holds whatever the dependent's own
        # state says, so its implementation alone does not release it.
        first = self.phase(1)
        second = self.phase(2, first)
        self.commit("phases")
        self.implement(second, self.on_work)
        self.assertEqual(self.stages(), {first: "implement", second: f"waits for {first}"})

    def test_phase_that_needs_decisions_reads_plan_until_it_is_ready(self):
        first = self.phase(1)
        self.record("replan", first, "--needs-decisions")
        self.assertEqual(self.stages(), {first: "plan"})
        self.record("ready", first)
        self.assertEqual(self.stages(), {first: "implement"})

    def test_retired_phase_reads_from_the_landed_range(self):
        landed = self.phase(1), self.phase(2)
        abandoned = self.phase(3)
        self.commit("phases")
        self.implement(landed[0], self.on_main)
        self.implement(landed[1], self.on_work)
        self.record("abandon", abandoned)
        self.commit("phases ended")
        for plan in (*landed, abandoned):
            self.record("retire", plan)
        self.assertEqual(
            self.stages(),
            {
                landed[0]: "on main (plan retired)",
                landed[1]: "done (plan retired)",
                abandoned: "retired (abandoned)",
            },
        )
        lines = self.report()
        self.assertEqual(lines[0], f"{PARENT}  status: implemented")
        self.assertEqual(sum("landed: " in line for line in lines), 2)
        self.assertEqual(lines[-1], "next: nothing")

    def test_retired_phase_releases_its_dependents(self):
        first = self.phase(1)
        second = self.phase(2, first)
        self.commit("phases")
        self.implement(first, self.on_main)
        self.commit("first implemented")
        self.record("retire", first)
        self.assertEqual(self.stages(), {first: "on main (plan retired)", second: "implement"})

    def test_phase_retired_as_abandoned_keeps_its_dependents_waiting(self):
        first = self.phase(1)
        second = self.phase(2, first)
        self.commit("phases")
        self.record("abandon", first)
        self.commit("first abandoned")
        self.record("retire", first)
        self.assertEqual(self.stages(), {first: "retired (abandoned)", second: f"waits for {first}"})
        self.assertEqual(self.report()[-1], "next: nothing")

    def test_land_owed_goes_before_ready_phases(self):
        # The first phase finished on this branch and releases the fourth,
        # the third landed and retired with its fast-forward still pending,
        # the fifth's implement merged and awaits review, the second is ready.
        first, second, third = self.phase(1), self.phase(2), self.phase(3)
        fourth = self.phase(4, first)
        fifth = self.phase(5)
        self.commit("phases")
        self.finish(first, self.on_work)
        self.implement(third, self.on_work)
        self.implement(fifth, self.on_work)
        self.commit("work done")
        self.record("retire", third)
        stages = self.stages()
        self.assertEqual(stages[third], "done (plan retired)")
        self.assertEqual(stages[fourth], "implement")
        self.assertEqual(self.report()[-1], f"next: land {first} after {fifth}")

        self.record("retire", first)
        self.record("retire", fifth)
        self.assertEqual(self.report()[-1], f"next: {second}, {fourth}")

    def test_phase_implemented_on_its_recorded_branch_reads_review(self):
        # The checkout still reads planned: the stage wrote its state on the
        # plan's branch, which is not merged here.
        first = self.phase(1)
        self.commit("phases")
        self.git("checkout", "-q", "-b", "stage")
        self.implement(first, self.on_work)
        self.commit("implemented on the plan's branch")
        self.git("checkout", "-q", "work")
        self.record("branch", first, "stage")
        self.commit("branch recorded")
        self.assertEqual(self.stages(), {first: "review"})
        self.assertIn("      branch: stage", self.report())

        # Once merged, the checkout's file is the one read: a verdict
        # recorded here shows, which a read of the branch would miss.
        self.git("merge", "-q", "--no-edit", "stage")
        self.record("review", first, "accept")
        self.assertEqual(self.stages(), {first: "compound"})
        self.assertNotIn("      branch: stage", self.report())

    def test_land_owed_waits_for_a_phase_on_an_unmerged_branch(self):
        # land refuses while a child worktree remains, and the second phase
        # keeps its worktree until its branch merges here or it parks.
        first, second = self.phase(1), self.phase(2)
        self.commit("phases")
        self.finish(first, self.on_work)
        self.commit("first done")
        self.git("checkout", "-q", "-b", "stage")
        self.write("code", "second phase, under way\n")
        self.commit("second started")
        self.git("checkout", "-q", "work")
        self.record("branch", second, "stage")
        self.commit("branch recorded")
        self.assertEqual(self.stages()[second], "implement")
        self.assertEqual(self.report()[-1], f"next: land {first} after {second}")

    def test_land_owed_does_not_wait_for_a_parked_phase(self):
        # Parking removes the phase's worktree and keeps its work on
        # parked/<slug>, off this branch, so nothing of it stands in land's way.
        first, second = self.phase(1), self.phase(2)
        self.commit("phases")
        self.finish(first, self.on_work)
        self.commit("first done")
        self.git("checkout", "-q", "-b", "parked/second")
        self.implement(second, self.on_work)
        self.commit("second implemented, then parked")
        self.git("checkout", "-q", "work")
        self.record("branch", second, "parked/second")
        self.commit("parked branch recorded")
        self.assertEqual(self.stages()[second], "review")
        self.assertEqual(self.report()[-1], f"next: land {first}")

    def test_plan_that_names_other_plans_in_its_units_is_not_a_parent(self):
        # A reconciliation plan lists the plans it rewrites: a phase of
        # another parent on disk and one already retired. Neither is its phase.
        edited = self.phase(1)
        reconcile = "docs/plans/2026-01-08-reconcile-plan.md"
        self.write(
            reconcile,
            f"# Reconcile - Plan\n\n### U1. Point the plans one way\nFiles: `{PARENT}`,\n`{edited}`\n"
            "\n### U2. Drop the retired plan's links\nFiles: `docs/plans/gone-plan.md`\n",
        )
        self.record("init", reconcile)
        out = self.run_script(reconcile)
        self.assertEqual(out.returncode, 1)
        self.assertIn("not a parent plan", out.stdout)

    def test_every_open_plan_is_listed_by_its_status(self):
        self.phase(1)
        finished = "docs/plans/2026-01-08-finished-plan.md"
        self.write(PLAIN, "# Plain - Plan\n")
        self.write(finished, "# Finished - Plan\n")
        self.record("init", PLAIN)
        self.record("init", finished)
        self.record("partial", PLAIN, *RUN, "--note", "one unit left")
        self.record("implemented", finished, *RUN)
        out = self.run_script()
        self.assertEqual(out.returncode, 0, out.stderr)
        self.assertEqual(
            sorted(tuple(line.split()) for line in out.stdout.splitlines()),
            [("partially-implemented", "plan", PLAIN), ("planned", "parent", PARENT)],
        )

    def test_plan_without_a_state_file_stops_the_report(self):
        self.write(PLAIN, "# Plain - Plan\n")
        for args in ((), (PLAIN,)):
            with self.subTest(args=args):
                out = self.run_script(*args)
                self.assertEqual(out.returncode, 2)
                self.assertIn("plan_record.py init", out.stderr)


if __name__ == "__main__":
    unittest.main()
