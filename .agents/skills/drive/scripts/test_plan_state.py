import importlib.util
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


if __name__ == "__main__":
    unittest.main()
