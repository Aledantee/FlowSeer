import importlib.util
import io
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
                self.assertEqual(plan_state.stage(first_unit, set()), "land")
                self.assertEqual(plan_state.stage(second_unit, {"U1"}), "implement")

                accepted = first.read_text()
                first.write_text(
                    accepted + "## Review gaps\n\n- `a.go:1`: off by one survives\n"
                    "- `b.go:2`: inverted branch survives\n\n## Notes\n- not a gap\n"
                )
                self.assertEqual(plan_state.stage(first_unit, set()), "review (gaps: 2)")
                first.write_text(accepted + "## Review gaps\n\n## Notes\n- not a gap\n")
                self.assertEqual(plan_state.stage(first_unit, set()), "land")

            with patch.object(plan_state, "on_main", return_value=True):
                first.write_text("---\nstatus: planned\n---\n")
                self.assertEqual(
                    plan_state.stage(first_unit, set()), "on main (status: planned)"
                )

    def test_gaps_open_verdict_is_not_an_accept(self):
        with tempfile.TemporaryDirectory() as directory:
            plan = Path(directory) / "phase-plan.md"
            plan.write_text(
                "---\nstatus: implemented\nreview: gaps open\ncompound: no lesson\n---\n"
                "## Review gaps\n\n- `a.go:1`: off by one survives\n"
            )
            unit = {"id": "U1", "plan": plan, "landed": "`abcdef0..abcdef1`"}
            with patch.object(plan_state, "on_main", return_value=False):
                self.assertEqual(plan_state.stage(unit, set()), "review (verdict: gaps open)")

    def test_phase_on_main_with_a_listed_gap_prints_the_gap(self):
        with tempfile.TemporaryDirectory() as directory:
            plans = Path(directory) / "docs/plans"
            plans.mkdir(parents=True)
            (plans / "parent-plan.md").write_text(
                "### U1. First\nFiles: `docs/plans/phase1-plan.md`\nLanded: `abcdef0..abcdef1`\n"
            )
            (plans / "phase1-plan.md").write_text(
                "---\nstatus: implemented\nparent: docs/plans/parent-plan.md\n"
                "review: accept\ncompound: no lesson\n---\n"
                "## Review gaps\n\n- `a.go:1`: off by one survives\n"
            )
            cwd = Path.cwd()
            os.chdir(directory)
            try:
                with patch.object(plan_state, "on_main", return_value=True), patch(
                    "sys.stdout", new_callable=io.StringIO
                ) as out:
                    plan_state.report(Path("docs/plans/parent-plan.md"))
                lines = out.getvalue().splitlines()
                self.assertIn("on main (gaps: 1)", next(l for l in lines if " U1 " in l))
                self.assertIn(
                    "      gap: docs/plans/phase1-plan.md:9: `a.go:1`: off by one survives", lines
                )
            finally:
                os.chdir(cwd)

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

    def test_land_owed_goes_before_ready_phases(self):
        with tempfile.TemporaryDirectory() as directory:
            plans = Path(directory) / "docs/plans"
            plans.mkdir(parents=True)
            parent = plans / "parent-plan.md"
            parent.write_text(
                "### U1. First\nFiles: `docs/plans/phase1-plan.md`\nLanded: `abcdef0..abcdef1`\n\n"
                "### U2. Second\nFiles: `docs/plans/phase2-plan.md`\nLanded:\n\n"
                "### U3. Third\nFiles: `docs/plans/phase3-plan.md`\nLanded: `abcdef2..abcdef3`\n\n"
                "### U4. Fourth\nFiles: `docs/plans/phase4-plan.md`\nAfter: U1\nLanded:\n\n"
                "### U5. Fifth\nFiles: `docs/plans/phase5-plan.md`\nLanded: `abcdef4..abcdef5`\n"
            )
            (plans / "phase4-plan.md").write_text(
                "---\nstatus: planned\nparent: docs/plans/parent-plan.md\n---\n"
            )
            (plans / "phase5-plan.md").write_text(
                "---\nstatus: implemented\nparent: docs/plans/parent-plan.md\n---\n"
            )
            (plans / "phase1-plan.md").write_text(
                "---\nstatus: implemented\nparent: docs/plans/parent-plan.md\n"
                "review: accept\ncompound: no lesson\n---\n"
            )
            (plans / "phase2-plan.md").write_text(
                "---\nstatus: planned\nparent: docs/plans/parent-plan.md\n---\n"
            )
            cwd = Path.cwd()
            os.chdir(directory)
            try:
                # U1 finished on this branch and releases U4, U3 landed and
                # retired with its fast-forward still pending, U5's implement
                # merged and awaits review, U2 is ready.
                with patch.object(plan_state, "on_main", return_value=False), patch(
                    "sys.stdout", new_callable=io.StringIO
                ) as out:
                    plan_state.report(Path("docs/plans/parent-plan.md"))
                lines = out.getvalue().splitlines()
                self.assertIn("done (plan retired)", next(l for l in lines if " U3 " in l))
                self.assertIn(" implement ", next(l for l in lines if " U4 " in l))
                self.assertEqual(lines[-1], "next: land U1 after U5")
                (plans / "phase1-plan.md").unlink()
                (plans / "phase5-plan.md").unlink()
                with patch.object(plan_state, "on_main", return_value=False), patch(
                    "sys.stdout", new_callable=io.StringIO
                ) as out:
                    plan_state.report(Path("docs/plans/parent-plan.md"))
                self.assertEqual(out.getvalue().splitlines()[-1], "next: U2, U4")
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
