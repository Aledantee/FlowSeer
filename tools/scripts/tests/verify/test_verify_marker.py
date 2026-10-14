import os
import unittest

from tests.verify import support
from tests.verify.support import across

BASH_MUTATION = "<Bash mutation; verify with --full>"
HINT = [
    "  The Bash-mutation line clears under --full, or under --base REF once every",
    "  generated, go.mod, go.sum, or buf.lock file it marked is identical to REF.",
]
LEFT = "Unverified edits remain after this run:"
LONG_AGO = 1_000_000_000


class MarkerTest(support.VerifierCase):
    """The dirty marker lists edits no run has verified. A passing run clears what it verified and names the rest."""

    def populate(self):
        self.write("README.md", "# Fixture\n")
        self.write("go.mod", "module example.invalid/m\n\ngo 1.27\n")
        self.write("edited.go", "package edited\n")

    def mark(self, *lines: str | bytes):
        """Write the marker as the hook does, one line per entry."""
        self.marker.write_bytes(b"".join((line if isinstance(line, bytes) else line.encode()) + b"\n" for line in lines))

    def stat_of(self, path):
        return (path.read_bytes(), path.stat().st_mtime_ns)

    def after_header(self, result) -> list[str]:
        """The lines a passing run prints from the header on, without the verdict."""
        lines = result.stdout.split("\n")
        self.assertIn(LEFT, lines)
        return lines[lines.index(LEFT) : -2]

    @across
    def test_no_gate_run_leaves_receipt_and_marker_as_they_were(self):
        self.write("LICENSE", "text\n")
        files = {
            self.receipt: "verified_at=2020-01-01T00:00:00Z\nbase=HEAD\nfull=false\npaths=old \n",
            self.marker: "stale.go\n",
            self.tree_state: "stale\n",
        }
        for path, text in files.items():
            path.write_text(text)
            os.utime(path, (LONG_AGO, LONG_AGO))
        before = {path: self.stat_of(path) for path in files}
        result = self.fails("--", "LICENSE", code=2)
        self.assertIn("FlowSeer verification selected no build, test or lint gate for these paths.\n  paths: LICENSE\n", result.stderr)
        self.assertEqual({path: self.stat_of(path) for path in files}, before)

    @across
    def test_passing_run_names_the_lines_it_left(self):
        self.mark("README.md", "other.go", BASH_MUTATION)
        result = self.passes("--", "README.md")
        self.assertEqual(self.after_header(result), [LEFT, f"  {BASH_MUTATION}", "  other.go", *HINT])
        self.assertEqual(self.marker.read_bytes(), f"{BASH_MUTATION}\nother.go\n".encode())
        # The hint is for the Bash-mutation line alone.
        self.mark("README.md", "other.go")
        result = self.passes("--", "README.md")
        self.assertEqual(self.after_header(result), [LEFT, "  other.go"])
        self.assertEqual(self.marker.read_bytes(), b"other.go\n")

    @across
    def test_path_absent_from_tree_and_base_clears(self):
        self.write("edited.go", "package edited\n\nvar changed = true\n")
        self.mark("gone/file.go", "edited.go")
        self.passes("--base", "HEAD", "--", "README.md")
        self.assertEqual(self.marker.read_bytes(), b"edited.go\n")
        # Without --base nothing is compared with a ref, so the absent path stays marked.
        self.mark("gone/file.go", "edited.go")
        self.passes("--", "README.md")
        self.assertEqual(self.marker.read_bytes(), b"edited.go\ngone/file.go\n")

    @across
    def test_passing_run_rewrites_the_tree_state(self):
        self.write("README.md", "# Fixture\n\nChanged.\n")
        self.tree_state.write_text("stale\n")
        self.passes("--", "README.md")
        digest = self.git("hash-object", "README.md")
        self.assertEqual(self.tree_state.read_text(), f"{digest}\tREADME.md\n")

    @across
    def test_bash_mutation_line_clears_when_its_files_match_the_base(self):
        self.write("README.md", "# Fixture\n\nChanged.\n")
        self.mark("go.mod", BASH_MUTATION)
        result = self.passes("--base", "main", "--", "README.md")
        self.assertFalse(self.marker.exists())
        self.assertNotIn(LEFT, result.stdout)
        # A module file that differs from the base keeps the line.
        self.write("go.mod", "module example.invalid/other\n\ngo 1.27\n")
        self.mark("go.mod", BASH_MUTATION)
        result = self.passes("--base", "main", "--", "README.md")
        self.assertEqual(self.after_header(result), [LEFT, f"  {BASH_MUTATION}", "  go.mod", *HINT])
        self.assertEqual(self.marker.read_bytes(), f"{BASH_MUTATION}\ngo.mod\n".encode())

    @across
    def test_bash_mutation_line_clears_when_its_files_are_under_tools(self):
        self.write("tools/buf/go.mod", "module example.invalid/buf\n\ngo 1.27\n")
        self.mark("tools/buf/go.mod", BASH_MUTATION)
        result = self.passes("--", "edited.go")
        self.assertEqual(self.after_header(result), [LEFT, "  tools/buf/go.mod"])
        self.assertEqual(self.marker.read_bytes(), b"tools/buf/go.mod\n")

    @across
    def test_full_run_removes_the_marker(self):
        self.populate_full()
        self.commit_all("full")
        self.mark("x.go", BASH_MUTATION)
        result = self.passes("--full")
        self.assertFalse(self.marker.exists())
        self.assertNotIn(LEFT, result.stdout)

    @across
    def test_marker_line_with_a_carriage_return_is_cleared_by_its_exact_name(self):
        name = "a\rb.md"
        self.write(name, "# Name\n")
        self.mark(name.encode(), "README.md")
        self.passes("--", name)
        self.assertEqual(self.marker.read_bytes(), b"README.md\n")

    @across
    def test_surviving_lines_are_sorted_by_bytes_and_unique(self):
        env = self.stubs.env(self.env, LC_ALL="en_US.UTF-8")
        self.mark("b.go", "é.go", "B.go", "b.go", "Z.go", "B.go")
        self.passes("--", "README.md", env=env)
        self.assertEqual(self.marker.read_bytes(), "B.go\nZ.go\nb.go\né.go\n".encode())

    @across
    def test_passing_run_ends_with_the_verdict_and_writes_the_receipt(self):
        self.write("README.md", "# Fixture\n\nChanged.\n")
        self.passes("--", "README.md")
        self.assertTrue(self.receipt.exists())
        self.assertTrue(self.receipt.read_text().startswith("verified_at="))


if __name__ == "__main__":
    unittest.main()
