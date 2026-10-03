import contextlib
import importlib.util
import io
import tempfile
import unittest
from pathlib import Path


spec = importlib.util.spec_from_file_location(
    "review_gaps", Path(__file__).with_name("review-gaps.py")
)
review_gaps = importlib.util.module_from_spec(spec)
spec.loader.exec_module(review_gaps)


class ReviewGapsTest(unittest.TestCase):
    def run_on(self, text):
        with tempfile.TemporaryDirectory() as directory:
            plan = Path(directory) / "phase-plan.md"
            plan.write_text(text)
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                code = review_gaps.main([str(plan)])
            return code, out.getvalue().replace(str(plan), "plan")

    def test_plan_without_the_section_passes(self):
        self.assertEqual(self.run_on("---\nreview: accept\n---\n## Units\n- U1\n"), (0, ""))

    def test_empty_section_passes(self):
        code, out = self.run_on("## Review gaps\n\nNone open.\n\n## Units\n- U1\n")
        self.assertEqual((code, out), (0, ""))

    def test_each_listed_gap_fails_with_its_line(self):
        code, out = self.run_on(
            "## Units\n- U1\n\n## Review gaps\n\n"
            "- `a.go:12`: boundary off by one survives\n"
            "  the case with an empty slice would fail on it\n"
            "* `b.go:3`: inverted branch survives\n"
        )
        self.assertEqual(code, 1)
        self.assertEqual(
            out,
            "plan:6: `a.go:12`: boundary off by one survives\n"
            "plan:8: `b.go:3`: inverted branch survives\n",
        )

    def test_section_ends_at_the_next_heading(self):
        code, _ = self.run_on("## Review gaps\n\n## Open questions\n- a question\n")
        self.assertEqual(code, 0)

    def test_fenced_example_is_not_a_gap(self):
        code, _ = self.run_on("## Review gaps\n\n```text\n- `a.go:1`: example\n```\n")
        self.assertEqual(code, 0)

    def test_missing_file_is_a_usage_error(self):
        with contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(review_gaps.main(["/nonexistent/plan.md"]), 2)

    def test_missing_path_before_a_plan_with_a_gap_still_prints_the_gap(self):
        with tempfile.TemporaryDirectory() as directory:
            plan = Path(directory) / "phase-plan.md"
            plan.write_text("## Review gaps\n\n- `a.go:1`: off by one survives\n")
            out, err = io.StringIO(), io.StringIO()
            with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
                code = review_gaps.main(["/nonexistent/plan.md", str(plan)])
            self.assertNotEqual(code, 0)
            self.assertEqual(out.getvalue(), f"{plan}:3: `a.go:1`: off by one survives\n")
            self.assertIn("/nonexistent/plan.md", err.getvalue())

    def test_only_a_column_zero_dash_item_is_a_gap(self):
        code, out = self.run_on(
            "## Review gaps\n\n"
            "a.go:1 off by one survives\n"
            "1. `b.go:2`: numbered\n"
            "+ `c.go:3`: plus bullet\n"
            "  - `d.go:4`: indented\n"
        )
        self.assertEqual((code, out), (0, ""))


if __name__ == "__main__":
    unittest.main()
