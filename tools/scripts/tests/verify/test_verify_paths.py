import os
import subprocess
import tempfile
import unittest
from pathlib import Path

from lib import repo

VERIFIER = repo.root(Path(__file__).parent) / ".claude/skills/verify-change/scripts/verify-change.sh"


class SymlinkedPathTest(unittest.TestCase):
    """A path spelled through a symlinked directory selects what git's spelling selects."""

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.git("init", "-q", "-b", "main")
        (self.root / "real/pkg").mkdir(parents=True)
        (self.root / "real/pkg/go.mod").write_text("module example.invalid/pkg\n\ngo 1.27\n")
        (self.root / "real/pkg/a.go").write_text("package pkg\n")
        (self.root / "real/empty").mkdir()
        (self.root / "alias").symlink_to("real")
        self.git("add", "-A")
        self.git("commit", "-qm", "init")

    def git(self, *args):
        subprocess.run(
            ["git", "-c", "user.name=Verify", "-c", "user.email=verify@example.invalid", *args],
            cwd=self.root,
            check=True,
            capture_output=True,
            text=True,
        )

    def select(self, *paths):
        return subprocess.run(
            [str(VERIFIER), "--print-selection", "--", *paths],
            cwd=self.root,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_fixture_tracks_the_target_spelling_only(self):
        listed = subprocess.run(
            ["git", "ls-files"], cwd=self.root, check=True, capture_output=True, text=True
        ).stdout.split()
        self.assertEqual(listed, ["alias", "real/pkg/a.go", "real/pkg/go.mod"])

    def test_directory_through_a_symlink_expands_to_the_tracked_files(self):
        result = self.select("alias/pkg")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("module=real/pkg mode=full", result.stdout.splitlines())

    def test_file_through_a_symlink_is_rewritten_to_the_tracked_spelling(self):
        result = self.select("alias/pkg/a.go")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("module=real/pkg mode=full", result.stdout.splitlines())

    def test_missing_file_through_a_symlink_keeps_its_tail(self):
        result = self.select("alias/pkg/gone/go.mod")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("module=real/pkg mode=full", result.stdout.splitlines())

    def test_repository_scripts_and_uv_pin_select_hook_tooling(self):
        (self.root / "tools/scripts/lib").mkdir(parents=True)
        (self.root / "tools/scripts/lib/repo.py").write_text("")
        (self.root / "uv.toml").write_text('required-version = ">=0.12.18"\n')
        for path in ("tools/scripts/lib/repo.py", "uv.toml"):
            with self.subTest(path=path):
                result = self.select(path)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("hook_tooling=true", result.stdout.splitlines())

    def test_scripts_readme_selects_hook_tooling(self):
        (self.root / "tools/scripts").mkdir(parents=True)
        (self.root / "tools/scripts/README.md").write_text("# Scripts\n")
        result = self.select("tools/scripts/README.md")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("hook_tooling=true", result.stdout.splitlines())

    def test_empty_argument_is_refused(self):
        result = self.select("")
        self.assertEqual(result.returncode, 2)
        self.assertIn("empty path argument", result.stderr)

    def test_directory_without_files_says_so(self):
        result = self.select("real/empty")
        self.assertEqual(result.returncode, 2)
        self.assertIn("found no files under the named paths", result.stderr)


class VerdictTest(unittest.TestCase):
    """A run that checks nothing, or fails outside `run`, says so in its last line."""

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name) / "repo"
        self.root.mkdir()
        subprocess.run(["git", "init", "-q", "-b", "main"], cwd=self.root, check=True)
        (self.root / "README.md").write_text("# Fixture\n")
        subprocess.run(["git", "add", "-A"], cwd=self.root, check=True)
        subprocess.run(
            ["git", "-c", "user.name=Verify", "-c", "user.email=verify@example.invalid", "commit", "-qm", "init"],
            cwd=self.root,
            check=True,
        )

    def verify(self, *args, env=None):
        return subprocess.run(
            [str(VERIFIER), *args], cwd=self.root, capture_output=True, text=True, check=False, env=env
        )

    def last_line(self, result):
        return result.stderr.strip().splitlines()[-1]

    def test_no_changed_paths_fails(self):
        result = self.verify()
        self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
        self.assertIn("No changed paths to verify", result.stderr)
        self.assertEqual(self.last_line(result), "FlowSeer verification FAILED (exit 2).")

    def test_no_changed_paths_selection_still_succeeds(self):
        result = self.verify("--print-selection")
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_lint_config_alone_asks_for_full(self):
        (self.root / ".golangci.yml").write_text("version: \"2\"\n")
        result = self.verify("--", ".golangci.yml")
        self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
        self.assertIn("run with --full", result.stderr)

    def test_lint_config_beside_a_go_file_still_asks_for_full(self):
        # One selected module would be linted under the new configuration,
        # the rest not, and the receipt would read as if all had passed.
        (self.root / ".golangci.yml").write_text("version: \"2\"\n")
        (self.root / "go.mod").write_text("module example.invalid/a\n\ngo 1.27\n")
        (self.root / "a.go").write_text("package a\n")
        result = self.verify("--", ".golangci.yml", "a.go")
        self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
        self.assertIn("run with --full", result.stderr)

    def stub_go_tools(self):
        """Stand-ins for go, gofumpt, goimports, and golangci-lint: go builds
        an empty checker, gofumpt reports a diff. The format gate runs before
        any module gate, so golangci-lint only has to exist. Returns an
        environment with the stubs first on PATH."""
        tools = Path(self.directory.name) / "bin"
        tools.mkdir()
        (tools / "golangci-lint").write_text("#!/bin/sh\nexit 0\n")
        (tools / "go").write_text(
            "#!/bin/sh\n"
            "[ \"$1\" = build ] || exit 0\n"
            "while [ $# -gt 0 ]; do [ \"$1\" = -o ] && out=$2; shift; done\n"
            "printf '#!/bin/sh\\nexit 0\\n' >\"$out\" && chmod +x \"$out\"\n"
        )
        (tools / "gofumpt").write_text("#!/bin/sh\necho '-unformatted'\n")
        (tools / "goimports").write_text("#!/bin/sh\nexit 0\n")
        for tool in tools.iterdir():
            tool.chmod(0o755)
        return dict(os.environ, PATH=f"{tools}:{os.environ['PATH']}")

    def test_format_failure_names_its_gate(self):
        env = self.stub_go_tools()
        (self.root / "go.mod").write_text("module example.invalid/a\n\ngo 1.27\n")
        (self.root / "a.go").write_text("package a\n")
        result = self.verify("--", "a.go", env=env)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertEqual(
            self.last_line(result), "FlowSeer verification FAILED (exit 1) in gate: gofumpt/goimports -d"
        )

    def test_prose_failure_names_its_run_py_command(self):
        # The fixture holds no tools/scripts, so the checks resolve run.py
        # from the checkout that holds the verifier.
        env = self.stub_go_tools()
        (self.root / "README.md").write_text("# Fixture\n\nA claim (session history) with no source.\n")
        result = self.verify("--", "README.md", env=env)
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertEqual(
            self.last_line(result), "FlowSeer verification FAILED (exit 1) in gate: run.py verify check-prose"
        )


if __name__ == "__main__":
    unittest.main()
