import unittest
from pathlib import Path

from tests.hooks.support import HookCase, across

FULL_SCOPE = "<Bash mutation; verify with --full>"


class MarkVerificationDirtyTest(HookCase):
    def marker(self, repo: Path | None = None) -> Path:
        return Path(self.git(repo or self.fixture, "rev-parse", "--absolute-git-dir")) / "flowseer-verification-dirty"

    def marked(self, repo: Path | None = None) -> list[str]:
        marker = self.marker(repo)
        return marker.read_text().splitlines() if marker.exists() else []

    def start_session(self, hook, repo: Path | None = None):
        """The first Bash call stores the listing, and anything dirty before it is marked once."""
        hook.silent(self.shell("true", cwd=repo or self.fixture))
        self.marker(repo).unlink(missing_ok=True)

    @across("mark-verification-dirty")
    def test_edits_mark_the_worktree_dirty_for_the_verifier(self, hook):
        """edits mark the worktree dirty for the verifier"""
        self.write(self.fixture / "main.go")
        hook.silent(self.edit(self.fixture / "main.go"))
        self.assertEqual(self.marked(), ["main.go"])

    @across("mark-verification-dirty")
    def test_the_first_bash_call_marks_every_dirty_path_once(self, hook):
        """the first Bash call marks every dirty path once"""
        self.write(self.fixture / "main.go")
        hook.silent(self.shell("true"))
        self.assertEqual(self.marked(), ["main.go"])
        self.marker().unlink()
        hook.silent(self.shell("true"))
        self.assertEqual(self.marked(), [])

    @across("mark-verification-dirty")
    def test_a_bash_call_that_changed_no_file_marks_nothing_whatever_its_text(self, hook):
        """a Bash call that changed no file marks nothing, whatever its text"""
        self.write(self.fixture / "main.go")
        self.start_session(hook)
        hook.silent(self.shell("git log | grep patch > /scratch/out.json"))
        self.assertFalse(self.marker().exists())

    @across("mark-verification-dirty")
    def test_a_git_directory_that_cannot_be_written_ends_a_bash_call_quietly(self, hook):
        """an unwritable git directory ends a Bash call with exit 0 and no traceback"""
        self.write(self.fixture / "main.go")
        git_dir = Path(self.git(self.fixture, "rev-parse", "--absolute-git-dir"))
        git_dir.chmod(0o555)
        self.addCleanup(git_dir.chmod, 0o755)
        result = hook.run(self.shell("true"))
        self.assertEqual((result.code, result.stdout, result.stderr), (0, "", ""))

    @across("mark-verification-dirty")
    def test_a_listing_that_cannot_be_taken_ends_a_bash_call_quietly_and_leaves_no_temporary_file(self, hook):
        """a file git cannot hash ends a Bash call with exit 0, no traceback, and no flowseer-tree-state temporary"""
        self.write(self.fixture / "main.go", mode=0)
        git_dir = self.marker().parent
        result = hook.run(self.shell("true"))
        self.assertEqual((result.code, result.stdout, result.stderr), (0, "", ""))
        self.assertEqual(list(git_dir.glob("flowseer-tree-state*")), [])

    @across("mark-verification-dirty")
    def test_a_marker_that_cannot_be_written_still_saves_the_listing(self, hook):
        """a marker path that is a directory ends a Bash call with exit 0, no traceback, and the listing saved"""
        self.write(self.fixture / "main.go")
        git_dir = self.marker().parent
        self.marker().mkdir()
        result = hook.run(self.shell("true"))
        self.assertEqual((result.code, result.stdout, result.stderr), (0, "", ""))
        self.assertTrue((git_dir / "flowseer-tree-state").read_text().endswith("\tmain.go\n"))
        self.assertEqual(sorted(p.name for p in git_dir.glob("flowseer-tree-state*")), ["flowseer-tree-state"])

    @across("mark-verification-dirty")
    def test_a_bash_write_is_marked_by_path_with_no_full_scope_line(self, hook):
        """a Bash write is marked by path, with no full-scope line"""
        self.write(self.fixture / "main.go")
        self.start_session(hook)
        self.write(self.fixture / "main.go", "package main\n")
        hook.silent(self.shell("python3 rewrite.py"))
        self.assertEqual(self.marker().read_text(), "main.go\n")

    @across("mark-verification-dirty")
    def test_a_bash_change_to_the_module_graph_keeps_the_full_scope_verification(self, hook):
        """a Bash change to the module graph keeps the full-scope verification"""
        self.write(self.fixture / "main.go")
        self.start_session(hook)
        self.write(self.fixture / "go.mod", "module fixture\n")
        hook.silent(self.shell("cp a b"))
        self.assertEqual(sorted(self.marked()), sorted(["go.mod", FULL_SCOPE]))

    @across("mark-verification-dirty")
    def test_a_merge_in_progress_marks_only_what_changed_beyond_the_incoming_branch(self, hook):
        """a merge in progress marks only what changed beyond the incoming branch"""
        merge = self.make_repo("merge")
        self.write(merge / "a.go", "package a\n")
        self.git(merge, "add", "a.go")
        self.git(merge, "commit", "-qm", "base")
        self.git(merge, "checkout", "-qb", "other")
        self.write(merge / "b.go", "package b\n")
        self.write(merge / "c.go", "package c\n")
        self.git(merge, "add", "b.go", "c.go")
        self.git(merge, "commit", "-qm", "other")
        self.git(merge, "checkout", "-q", "main")
        self.write(merge / "a.go", "package a // main\n")
        self.git(merge, "commit", "-qam", "main")
        self.git(merge, "merge", "-q", "--no-commit", "--no-ff", "other")
        self.write(merge / "c.go", "package c // resolved by hand\n")
        hook.silent(self.shell("git merge other", cwd=merge))
        self.assertEqual(self.marker(merge).read_text(), "c.go\n")

    @across("mark-verification-dirty")
    def test_an_edit_is_marked_for_the_file_types_the_edit_list_names_and_no_others(self, hook):
        """an edit is marked by the edit list, which leaves out buf.lock and a nested go.mod"""
        marked = "a.go b.proto c.ts d.tsx e.js f.jsx g.py h.sh i.md j.json k.yaml l.yml go.mod go.sum sub/dir/a.go".split()
        for path in marked:
            with self.subTest(path=path):
                self.marker().unlink(missing_ok=True)
                hook.silent(self.edit(self.write(self.fixture / path)))
                self.assertEqual(self.marked(), [path])
        for path in ("buf.lock", "sub/go.mod", "sub/go.sum", "notes.txt", "x.toml", "Makefile", "x.go.bak"):
            with self.subTest(path=path):
                self.marker().unlink(missing_ok=True)
                hook.silent(self.edit(self.write(self.fixture / path)))
                self.assertEqual(self.marked(), [])

    @across("mark-verification-dirty")
    def test_an_edit_outside_the_checkout_or_without_a_path_marks_nothing(self, hook):
        """an edit outside the checkout, or one that names no file, marks nothing"""
        hook.silent(self.edit(self.write(self.parent / "outside.go")))
        hook.silent({"cwd": str(self.fixture), "tool_input": {}})
        hook.silent({"tool_input": {"file_path": "a.go"}})
        hook.silent("not json")
        self.assertEqual(self.marked(), [])

    @across("mark-verification-dirty")
    def test_a_relative_edit_path_is_marked_from_the_working_directory(self, hook):
        """an edit with a relative path is marked relative to the checkout root"""
        nested = self.fixture / "pkg"
        self.write(nested / "a.go")
        hook.silent(self.edit("a.go", cwd=nested))
        self.assertEqual(self.marked(), ["pkg/a.go"])

    @across("mark-verification-dirty")
    def test_a_linked_worktree_keeps_its_own_marker(self, hook):
        """the marker lives in the git directory of the worktree that was edited"""
        self.write(self.linked / "a.go")
        hook.silent(self.edit("a.go", cwd=self.linked))
        self.assertEqual(self.marked(self.linked), ["a.go"])
        self.assertEqual(self.marked(), [])
        self.assertIn("/worktrees/", str(self.marker(self.linked)))

    @across("mark-verification-dirty")
    def test_the_listing_marks_the_types_the_listing_names_including_buf_lock_and_nested_modules(self, hook):
        """a Bash call marks buf.lock and a nested go.mod, which an edit does not, and no file of another type"""
        self.start_session(hook)
        for path in ("buf.lock", "sub/go.mod", "sub/go.sum", "sub/buf.lock", "notes.txt", "x.toml", "a.tsx"):
            self.write(self.fixture / path, "x\n")
        hook.silent(self.shell("make"))
        marked = self.marked()
        self.assertEqual(
            sorted(marked),
            sorted(["a.tsx", "buf.lock", "sub/buf.lock", "sub/go.mod", "sub/go.sum", FULL_SCOPE]),
        )

    @across("mark-verification-dirty")
    def test_generated_output_and_the_lock_file_keep_the_full_scope(self, hook):
        """a Bash write under generated/ or to buf.lock keeps the full-scope verification"""
        for path in ("generated/gen.go", "frontend/web/generated/x.ts", "sub/generated/y.go", "buf.lock", "sub/go.sum"):
            with self.subTest(path=path):
                self.start_session(hook)
                self.write(self.fixture / path, f"{path}\n")
                hook.silent(self.shell("buf generate"))
                self.assertEqual(sorted(self.marked()), sorted([path, FULL_SCOPE]))
        self.start_session(hook)
        self.write(self.fixture / "pkg/plain.go", "package plain\n")
        hook.silent(self.shell("go run ."))
        self.assertEqual(self.marked(), ["pkg/plain.go"])

    @across("mark-verification-dirty")
    def test_a_deleted_file_is_marked_by_its_path(self, hook):
        """a Bash call that deletes a tracked file marks it"""
        self.start_session(hook)
        (self.fixture / "README.md").unlink()
        hook.silent(self.shell("rm README.md"))
        self.assertEqual(self.marked(), ["README.md"])

    @across("mark-verification-dirty")
    def test_a_path_is_marked_once_until_it_changes_again(self, hook):
        """a path a Bash call changed is not marked again by the next call, until its content changes"""
        self.start_session(hook)
        self.write(self.fixture / "a.go", "one\n")
        hook.silent(self.shell("echo one > a.go"))
        hook.silent(self.shell("true"))
        self.assertEqual(self.marked(), ["a.go"])
        self.write(self.fixture / "a.go", "two\n")
        hook.silent(self.shell("echo two > a.go"))
        self.assertEqual(self.marked(), ["a.go", "a.go"])

    @across("mark-verification-dirty")
    def test_the_stored_listing_replaces_the_old_one_and_leaves_no_temporary_file(self, hook):
        """the stored listing is replaced by the new one and no temporary listing is left in the git directory"""
        self.write(self.fixture / "a.go", "one\n")
        hook.silent(self.shell("true"))
        git_dir = self.marker().parent
        state = git_dir / "flowseer-tree-state"
        self.assertTrue(state.read_text().endswith("\ta.go\n"))
        self.assertEqual(sorted(p.name for p in git_dir.glob("flowseer-tree-state*")), ["flowseer-tree-state"])

    @across("mark-verification-dirty")
    def test_a_bash_call_outside_a_repository_or_without_a_directory_marks_nothing(self, hook):
        """a Bash call outside a repository, or with no working directory, exits 0 with no output"""
        hook.silent(self.shell("true", cwd=self.parent))
        hook.silent({"tool_input": {"command": "true"}})
        hook.silent(self.shell("true", cwd=self.parent / "missing"))
        self.assertEqual(self.marked(), [])


if __name__ == "__main__":
    unittest.main()
