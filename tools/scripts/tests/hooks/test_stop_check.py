import unittest
from pathlib import Path

from tests.hooks.support import HookCase, across

PASSING = 'package conformance\n\nimport "testing"\n\nfunc TestPanicPolicy(t *testing.T) {}\n'


def failing(name: str, message: str, package: str = "conformance") -> str:
    return f'package {package}\n\nimport "testing"\n\nfunc {name}(t *testing.T) {{\n\tt.Error("{message}")\n}}\n'


class StopCheckTest(HookCase):
    def marker(self, repo: Path | None = None) -> Path:
        return Path(self.git(repo or self.fixture, "rev-parse", "--absolute-git-dir")) / "flowseer-verification-dirty"

    def gate_fixture(self) -> Path:
        """A checkout with a go.mod, which is what makes an absent conformance directory a failure."""
        repo = self.make_repo("gate fixture")
        self.write(repo / "go.mod", "module example.invalid/gate\n\ngo 1.27\n")
        return repo

    @across("stop-check")
    def test_stop_reports_unverified_edits_without_blocking_and_passes_a_clean_tree(self, hook):
        """Stop reports unverified edits without blocking and passes a clean tree"""
        self.marker().write_text("main.go\n")
        output = hook.output(self.stop())
        self.assertNotIn("decision", output)
        self.assertIn("Edited but not verified", output["systemMessage"])
        self.marker().unlink()
        self.assertEqual(hook.output(self.stop()), {})

    @across("stop-check")
    def test_stop_blocks_on_a_failing_panic_gate_and_passes_when_it_holds(self, hook):
        """Stop blocks on a failing panic gate and passes when it holds"""
        repo = self.gate_fixture()
        gate = repo / "test/conformance/panic/panic_policy_test.go"
        self.write(gate, failing("TestPanicPolicy", "fixture violation"))
        output = hook.output(self.stop(repo))
        self.assertEqual(output["decision"], "block")
        self.assertIn("fixture violation", output["reason"])
        self.write(gate, PASSING)
        self.assertEqual(hook.output(self.stop(repo)), {})

    @across("stop-check")
    def test_stop_runs_a_gate_package_whatever_its_tests_are_named(self, hook):
        """Stop runs a gate package whatever its tests are named"""
        repo = self.gate_fixture()
        self.write(
            repo / "test/conformance/panic/panic_policy_test.go",
            failing("TestRenamedGate", "renamed fixture violation"),
        )
        output = hook.output(self.stop(repo))
        self.assertEqual(output["decision"], "block")
        self.assertIn("renamed fixture violation", output["reason"])

    @across("stop-check")
    def test_stop_runs_a_gate_package_whatever_its_directory_is_named(self, hook):
        """Stop runs a gate package whatever its directory is named"""
        repo = self.gate_fixture()
        self.write(
            repo / "test/conformance/panics/panic_policy_test.go",
            failing("TestRenamedGate", "renamed fixture violation"),
        )
        output = hook.output(self.stop(repo))
        self.assertEqual(output["decision"], "block")
        self.assertIn("renamed fixture violation", output["reason"])

    @across("stop-check")
    def test_stop_runs_a_gate_package_nested_below_a_conformance_child(self, hook):
        """Stop runs a gate package nested below a conformance child"""
        repo = self.gate_fixture()
        self.write(repo / "test/conformance/panics/panic_policy_test.go", PASSING)
        self.write(
            repo / "test/conformance/panics/nested/nested_test.go",
            failing("TestNestedGate", "nested fixture violation", "nested"),
        )
        output = hook.output(self.stop(repo))
        self.assertEqual(output["decision"], "block")
        self.assertIn("nested fixture violation", output["reason"])

    @across("stop-check")
    def test_stop_blocks_when_a_checkout_has_no_conformance_gate_to_run(self, hook):
        """Stop blocks when a checkout has no conformance gate to run"""
        repo = self.gate_fixture()
        self.write(repo / "test/gates/panic/panic_policy_test.go", PASSING)
        output = hook.output(self.stop(repo))
        self.assertEqual(output["decision"], "block")
        self.assertIn("no conformance gate found under test/conformance/", output["reason"])

    @across("stop-check")
    def test_stop_in_a_checkout_without_a_go_module_has_no_gate_to_miss(self, hook):
        """Stop passes a checkout with neither a go.mod nor a conformance directory"""
        repo = self.make_repo("stub")
        self.assertEqual(hook.output(self.stop(repo)), {})

    @across("stop-check")
    def test_stop_hook_active_reports_a_failing_gate_without_blocking_again(self, hook):
        """with stop_hook_active set, Stop reports a failing gate as a message and does not block"""
        repo = self.gate_fixture()
        self.write(
            repo / "test/conformance/panic/panic_policy_test.go",
            failing("TestPanicPolicy", "fixture violation"),
        )
        output = hook.output({**self.stop(repo), "stop_hook_active": True})
        self.assertEqual(list(output), ["systemMessage"])
        self.assertTrue(
            output["systemMessage"].startswith(
                "A repository conformance gate failed. Fix the violations before stopping:\n"
            )
        )
        self.assertIn("fixture violation", output["systemMessage"])
        for inactive in (False, None):
            with self.subTest(stop_hook_active=inactive):
                output = hook.output({**self.stop(repo), "stop_hook_active": inactive})
                self.assertEqual(output["decision"], "block")

    @across("stop-check")
    def test_stop_stops_at_the_first_failing_gate(self, hook):
        """Stop runs the gates in order and reports only the first that fails"""
        repo = self.gate_fixture()
        self.write(repo / "test/conformance/a-first/a_test.go", failing("TestFirst", "first fixture violation", "first"))
        self.write(
            repo / "test/conformance/b-second/b_test.go", failing("TestSecond", "second fixture violation", "second")
        )
        reason = hook.output(self.stop(repo))["reason"]
        self.assertIn("first fixture violation", reason)
        self.assertNotIn("second fixture violation", reason)

    @across("stop-check")
    def test_stop_names_at_most_twenty_unverified_paths_each_once(self, hook):
        """Stop names the first twenty unverified paths, sorted, each once"""
        paths = [f"pkg/f{number:02d}.go" for number in range(25)]
        self.marker().write_text("\n".join([*reversed(paths), *paths[:5]]) + "\n")
        message = hook.output(self.stop())["systemMessage"]
        self.assertTrue(message.startswith(f"Edited but not verified: {' '.join(paths[:20])}. Run "), message)
        self.assertNotIn("f20.go", message)

    @across("stop-check")
    def test_an_empty_marker_is_not_an_unverified_edit(self, hook):
        """Stop passes a tree whose dirty marker is empty"""
        self.marker().write_text("")
        self.assertEqual(hook.output(self.stop()), {})

    @across("stop-check")
    def test_unverified_edits_are_reported_whatever_subdirectory_the_session_is_in(self, hook):
        """Stop finds the marker of the checkout from a subdirectory, and of a linked worktree from its own"""
        self.marker().write_text("main.go\n")
        (self.fixture / "pkg").mkdir()
        self.assertIn("main.go", hook.output(self.stop(self.fixture / "pkg"))["systemMessage"])
        self.assertEqual(hook.output(self.stop(self.linked)), {})
        self.marker(self.linked).write_text("linked.go\n")
        self.assertIn("linked.go", hook.output(self.stop(self.linked))["systemMessage"])

    @across("stop-check")
    def test_a_failing_gate_and_unverified_edits_report_the_gate(self, hook):
        """Stop reports the failing gate, not the unverified edits, when both hold"""
        repo = self.gate_fixture()
        self.write(
            repo / "test/conformance/panic/panic_policy_test.go",
            failing("TestPanicPolicy", "fixture violation"),
        )
        self.marker(repo).write_text("main.go\n")
        output = hook.output(self.stop(repo))
        self.assertEqual(output["decision"], "block")
        self.assertNotIn("Edited but not verified", output["reason"])

    @across("stop-check")
    def test_stop_prints_an_empty_object_for_input_it_cannot_read_and_for_no_repository(self, hook):
        """Stop prints an empty object and runs no gate for malformed input and outside a repository"""
        for payload in ("", "not json", "null", "false"):
            with self.subTest(input=payload):
                self.assertEqual(hook.output(payload), {})
        self.assertEqual(hook.output(self.stop(self.parent)), {})


if __name__ == "__main__":
    unittest.main()
