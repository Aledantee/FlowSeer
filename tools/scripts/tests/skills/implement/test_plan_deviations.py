import contextlib
import io
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from skills.implement import plan_deviations


def units(text):
    with tempfile.TemporaryDirectory() as directory:
        plan = Path(directory) / "example-plan.md"
        plan.write_text(text)
        return plan_deviations.units(plan)


class FilesFieldTest(unittest.TestCase):
    def test_unit_headings_require_one_space_before_the_id(self):
        self.assertEqual(units("###  U1. Name\nFiles: `pkg/invalid.go`\n"), {})
        found = units(
            "### U1. Name\nFiles: `pkg/a.go`\n"
            "### U2: Name\nFiles: `pkg/b.go`\n"
            "### U3a. Name\nFiles: `pkg/c.go`\n"
        )
        self.assertEqual(found, {"U1": ["pkg/a.go"], "U2": ["pkg/b.go"], "U3a": ["pkg/c.go"]})

    def test_bare_entries_read_like_quoted_ones(self):
        found = units(
            "### U1. Bare paths with asides\n\n"
            "Files: tools/buf/go.mod, AGENTS.md (line 44 only), generated/ (regenerated), "
            "docs/old.md (renamed to new.md, see Change), pkg/b/{b.go,b_test.go}\n"
            "After: none\n"
            "Change: `pkg/unrelated.go` stays as it is.\n"
        )
        self.assertEqual(
            found["U1"],
            ["tools/buf/go.mod", "AGENTS.md", "generated", "docs/old.md", "pkg/b/b.go", "pkg/b/b_test.go"],
        )

    def test_bare_entries_continue_over_lines_and_skip_prose(self):
        found = units(
            "### U1: Bullet field with bare paths\n\n"
            "- **Files:**\n"
            "  src/a/a.go, src/a/a_test.go,\n"
            "  the README beside them, none\n"
            "  - src/b/b.go\n"
            "- **After:** none\n"
        )
        self.assertEqual(found["U1"], ["src/a/a.go", "src/a/a_test.go", "src/b/b.go"])

    def test_bare_root_file_is_not_a_sibling_of_the_entry_before_it(self):
        found = units("### U1. Root files\n\nFiles: tools/buf/go.sum, buf.gen.yaml, `tools/x.go`\n")
        self.assertEqual(found["U1"], ["tools/x.go"])
        found = units("### U1. Root files\n\nFiles: tools/buf/go.sum, buf.gen.yaml, generated/\n")
        self.assertEqual(found["U1"], ["tools/buf/go.sum", "buf.gen.yaml", "generated"])

    def test_quoted_line_ignores_its_bare_words(self):
        found = units("### U1. Quoted\n\nFiles: `pkg/a/a.go` and docs/aside.md (`Symbol` aside)\n")
        self.assertEqual(found["U1"], ["pkg/a/a.go"])

    def test_quoted_root_file_is_not_a_sibling_of_the_entry_before_it(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "CONCEPTS.md").write_text("")
            (root / "src/a").mkdir(parents=True)
            plan = root / "example-plan.md"
            plan.write_text("### U1. Quoted root file\n\nFiles: `src/a/a.go`, `a_test.go`, `CONCEPTS.md`\n")
            found = plan_deviations.units(plan, root)
        self.assertEqual(found["U1"], ["src/a/a.go", "src/a/a_test.go", "CONCEPTS.md"])

    def test_skills_symlink_entries_read_as_the_directory_git_reports(self):
        found = units("### U1. Skills\n\nFiles: `.claude/skills/implement/SKILL.md`, `.agents/skills/x/y.py`\n")
        self.assertEqual(found["U1"], [".agents/skills/implement/SKILL.md", ".agents/skills/x/y.py"])


class DiffTest(unittest.TestCase):
    """main() against a scratch repository whose main moved on after the fork."""

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.root = Path(self.directory.name)
        # A user's diff.renames or commit.gpgsign would change what the
        # rename test proves or break the commits, for main() too.
        env = mock.patch.dict(
            os.environ, {"GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1"}
        )
        env.start()
        self.addCleanup(env.stop)
        self.git("init", "-q", "-b", "main")
        body = "".join(f"line {n}\n" for n in range(20))
        self.write(".agents/skills/s/old.md", body)
        self.write("src/a/a.go")
        self.write("src/a/README.md")
        self.write("README.md")
        self.git("add", "-A")
        self.git("commit", "-qm", "base")
        self.git("checkout", "-qb", "work")
        self.git("mv", ".agents/skills/s/old.md", ".agents/skills/s/new.md")
        # Similar enough that git pairs the two paths as a rename.
        self.write(".agents/skills/s/new.md", body + "one more line\n")
        self.git("commit", "-qam", "rename")
        self.git("checkout", "-q", "main")
        self.write("src/main_only.go")
        self.git("add", "-A")
        self.git("commit", "-qm", "main moves on")
        self.git("checkout", "-q", "work")
        self.plan = self.root / "docs/plans/p.md"
        self.write(
            "docs/plans/p.md",
            "### U1. Rename\n\nFiles: `.claude/skills/s/old.md`, `.claude/skills/s/new.md`\n",
        )
        self.git("add", "-A")
        self.git("commit", "-qm", "plan")

    def tearDown(self):
        self.directory.cleanup()

    def git(self, *args):
        subprocess.run(
            ["git", "-c", "user.name=t", "-c", "user.email=t@example.com", *args],
            cwd=self.root,
            check=True,
            capture_output=True,
        )

    def write(self, path, text="x\n"):
        target = self.root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text)

    def run_main(self, *paths):
        out = io.StringIO()
        cwd = os.getcwd()
        os.chdir(self.root)
        try:
            with contextlib.redirect_stdout(out):
                code = plan_deviations.main([str(self.plan), "main", *paths])
        finally:
            os.chdir(cwd)
        self.assertEqual(code, 0)
        return out.getvalue()

    def test_deleted_sibling_is_not_read_as_the_root_file(self):
        self.git("rm", "-q", "src/a/README.md")
        plan = self.root / "docs/plans/delete-plan.md"
        plan.write_text("### U1. Drop\n\nFiles: `src/a/a.go`, `README.md`\n")
        fork = subprocess.run(
            ["git", "merge-base", "main", "HEAD"], cwd=self.root, check=True, capture_output=True, text=True
        ).stdout.strip()
        found = plan_deviations.units(plan, self.root, fork)
        self.assertEqual(found["U1"], ["src/a/a.go", "src/a/README.md"])

    def test_diff_starts_at_the_fork_point(self):
        self.assertNotIn("src/main_only.go", self.run_main())

    def test_rename_counts_as_both_paths_changed(self):
        report = self.run_main()
        self.assertIn("Named by a unit, unchanged:\n  none\n", report)

    def test_skills_symlink_path_argument_limits_the_diff(self):
        report = self.run_main("--", ".claude/skills/s")
        self.assertIn("Named by a unit, unchanged:\n  none\n", report)


if __name__ == "__main__":
    unittest.main()
