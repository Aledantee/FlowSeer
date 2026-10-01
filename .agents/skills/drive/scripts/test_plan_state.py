import importlib.util
import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch


spec = importlib.util.spec_from_file_location(
    "plan_state", Path(__file__).with_name("plan-state.py")
)
plan_state = importlib.util.module_from_spec(spec)
spec.loader.exec_module(plan_state)


class PlanStateTest(unittest.TestCase):
    def test_dependent_waits_until_review_and_compound_finish(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            first = root / "first-plan.md"
            second = root / "second-plan.md"
            first.write_text("---\nstatus: implemented\n---\n")
            second.write_text("---\nstatus: planned\n---\n")
            first_unit = {"id": "U1", "plan": first, "landed": "`abcdef0..abcdef1`"}
            second_unit = {"id": "U2", "plan": second, "after": ["U1"]}
            with patch.object(plan_state, "on_main", return_value=False):
                self.assertEqual(plan_state.stage(first_unit, set()), "review")
                self.assertEqual(plan_state.stage(second_unit, set()), "waits for U1")

                first.write_text(
                    "---\nstatus: implemented\nreview: accept\n"
                    "compound: no lesson\n---\n"
                )
                self.assertEqual(plan_state.stage(first_unit, set()), "done")
                self.assertEqual(plan_state.stage(second_unit, {"U1"}), "implement")

            with patch.object(plan_state, "on_main", return_value=True):
                first.write_text("---\nstatus: planned\n---\n")
                self.assertEqual(
                    plan_state.stage(first_unit, set()), "on main (status: planned)"
                )

    def test_retired_phase_reads_from_the_landed_range(self):
        with tempfile.TemporaryDirectory() as directory:
            retired = {"id": "U1", "plan": Path(directory) / "gone-plan.md", "landed": "`abcdef0..abcdef1`"}
            unlanded = {"id": "U2", "plan": Path(directory) / "missing-plan.md"}
            with patch.object(plan_state, "on_main", return_value=True):
                self.assertEqual(plan_state.stage(retired, set()), "on main (plan retired)")
            with patch.object(plan_state, "on_main", return_value=False):
                self.assertEqual(plan_state.stage(retired, set()), "done (plan retired)")
            self.assertEqual(plan_state.stage(unlanded, set()), "plan (phase plan missing)")

    def test_retired_phase_releases_its_dependents(self):
        with tempfile.TemporaryDirectory() as directory:
            plans = Path(directory) / "docs/plans"
            plans.mkdir(parents=True)
            parent = plans / "parent-plan.md"
            parent.write_text(
                "### U1. First\nFiles: `docs/plans/phase1-plan.md`\nLanded: `abcdef0..abcdef1`\n\n"
                "### U2. Second\nFiles: `docs/plans/phase2-plan.md`\nAfter: U1\nLanded:\n"
            )
            (plans / "phase2-plan.md").write_text(
                "---\nstatus: planned\nparent: docs/plans/parent-plan.md\n---\n"
            )
            cwd = Path.cwd()
            os.chdir(directory)
            try:
                units = plan_state.phases(Path("docs/plans/parent-plan.md"))
                with patch.object(plan_state, "on_main", return_value=True):
                    self.assertEqual(plan_state.stage(units[0], set()), "on main (plan retired)")
                    self.assertEqual(plan_state.stage(units[1], {"U1"}), "implement")
            finally:
                os.chdir(cwd)

    def test_unit_that_edits_other_plans_is_not_a_phase(self):
        with tempfile.TemporaryDirectory() as directory:
            plans = Path(directory) / "docs/plans"
            plans.mkdir(parents=True)
            # A reconciliation unit lists the plans it rewrites: one on disk
            # that names no parent, one already retired. Neither is a phase.
            (plans / "reconcile-plan.md").write_text(
                "### U1. Point the plans one way\n"
                "Files: `docs/plans/old-parent-plan.md`,\n"
                "`docs/plans/old-phase3-plan.md`\nAfter: none\n\n"
                "### U2. Drop the retired plan's links\n"
                "Files: `docs/plans/gone-plan.md`\nAfter: U1\n"
            )
            (plans / "old-parent-plan.md").write_text("---\nstatus: planned\n---\n")
            (plans / "old-phase3-plan.md").write_text(
                "---\nstatus: planned\nparent: docs/plans/old-parent-plan.md\n---\n"
            )
            cwd = Path.cwd()
            os.chdir(directory)
            try:
                self.assertEqual(plan_state.phases(Path("docs/plans/reconcile-plan.md")), [])
            finally:
                os.chdir(cwd)


if __name__ == "__main__":
    unittest.main()
