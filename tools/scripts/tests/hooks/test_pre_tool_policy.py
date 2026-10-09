import os
import unittest

from tests.hooks.support import Hook, HookCase, across, implementations


class PreToolPolicyTest(HookCase):
    @across("pre-tool-policy")
    def test_edit_denies_generated_output(self, hook):
        """Edit denies generated output"""
        self.assertEqual(hook.decide(self.edit(self.fixture / "generated" / "device.pb.go")), "deny")

    @across("pre-tool-policy")
    def test_edit_resolves_paths_whose_parent_directories_do_not_exist_yet(self, hook):
        """Edit resolves paths whose parent directories do not exist yet"""
        self.assertEqual(hook.decide(self.edit(self.fixture / "docs/new dir/deeper/notes.md")), "allow")
        self.assertEqual(hook.decide(self.edit(self.fixture / "generated/new/deeper/x.pb.go")), "deny")

    @across("pre-tool-policy")
    def test_edit_applies_the_spec_proto_source_only_rule_to_claude_paths_and_codex_patches(self, hook):
        """Edit applies the spec/proto source-only rule to Claude paths and Codex patches"""
        self.assertEqual(hook.decide(self.edit("spec/proto/rules_test.go")), "deny")
        mixed = self.patch(
            "*** Update File: spec/proto/flowseer/net/addr/v1/ip.proto",
            "*** Add File: spec/proto/layering_test.go",
        )
        self.assertEqual(hook.decide(mixed), "deny")
        schema = self.patch("*** Update File: spec/proto/flowseer/net/addr/v1/ip.proto")
        self.assertEqual(hook.decide(schema), "allow")
        (self.fixture / "src" / "common").mkdir(parents=True)
        traversal = self.patch("*** Add File: ../../spec/proto/bypass_test.go", cwd=self.fixture / "src/common")
        self.assertEqual(hook.decide(traversal), "deny")

    @across("pre-tool-policy")
    def test_edit_leaves_a_path_outside_every_checkout_to_the_sandbox_and_holds_another_checkout_to_policy(
        self, hook
    ):
        """Edit leaves a path outside every checkout to the sandbox and holds another checkout to policy"""
        nested = self.fixture / "src" / "common"
        nested.mkdir(parents=True)
        escape = self.patch("*** Add File: ../../../outside-scratch/notes.md", cwd=nested)
        self.assertEqual(hook.decide(escape), "allow")
        bounce = self.patch(
            "*** Add File: ../../../missing/../repository with spaces/generated/x.pb.go", cwd=nested
        )
        self.assertEqual(hook.decide(bounce), "deny")
        sibling = os.path.relpath(self.fixture / "generated/x.pb.go", self.linked)
        self.assertEqual(
            hook.decide(self.patch(f"*** Update File: {sibling}", cwd=self.linked)),
            "deny",
        )
        self.assertEqual(
            hook.decide(self.edit(self.fixture / "generated/x.pb.go", cwd=self.linked)),
            "deny",
        )

    @across("pre-tool-policy")
    def test_edit_asks_before_touching_a_policy_surface(self, hook):
        """Edit asks before touching a policy surface"""
        for surface in (
            "AGENTS.md",
            "CLAUDE.md",
            "buf.yaml",
            ".golangci.yml",
            ".claude/settings.json",
            ".codex/hooks.json",
            "test/conformance/a11y/a11y_policy_test.go",
            "tools/scripts/lib/repo.py",
            "tools/scripts/run.py",
            "tools/scripts/hooks/x.py",
            "tools/scripts/verify/x.py",
        ):
            with self.subTest(surface=surface):
                self.assertEqual(hook.decide(self.edit(self.fixture / surface)), "ask")

    @across("pre-tool-policy")
    def test_a_policy_surface_ask_names_every_surface_the_call_touches(self, hook):
        """an ask names every policy surface the call touches"""
        output = hook.output(self.patch("*** Update File: AGENTS.md", "*** Update File: buf.yaml", "*** Add File: x.md"))
        decision = output["hookSpecificOutput"]
        self.assertEqual(decision["permissionDecision"], "ask")
        self.assertIn("AGENTS.md, buf.yaml is a policy surface", decision["permissionDecisionReason"])

    @across("pre-tool-policy")
    def test_edit_leaves_tools_scripts_tests_and_skill_commands_unprompted(self, hook):
        """Edit leaves tools/scripts tests and skill commands unprompted"""
        for path in ("tools/scripts/tests/test_run.py", "tools/scripts/skills/x.py"):
            with self.subTest(path=path):
                self.assertEqual(hook.decide(self.edit(self.fixture / path)), "allow")

    @across("pre-tool-policy")
    def test_edit_denies_a_missing_generated_output_path(self, hook):
        """Edit denies a missing generated output path"""
        self.assertEqual(hook.decide(self.edit(self.fixture / "generated/missing.pb.go")), "deny")

    @across("pre-tool-policy")
    def test_edit_denies_generated_output_in_a_linked_worktree(self, hook):
        """Edit denies generated output in a linked worktree"""
        path = self.linked / "generated" / "device.pb.go"
        self.assertEqual(hook.decide(self.edit(path, cwd=self.linked)), "deny")

    @across("pre-tool-policy")
    def test_edit_allows_protobuf_source(self, hook):
        """Edit allows protobuf source"""
        self.assertEqual(hook.decide(self.edit(self.fixture / "spec/proto/device.proto")), "allow")

    @across("pre-tool-policy")
    def test_edit_allows_absolute_paths_outside_the_repository(self, hook):
        """Edit allows absolute paths outside the repository"""
        self.assertEqual(hook.decide(self.edit(self.parent / "outside.txt")), "allow")

    @across("pre-tool-policy")
    def test_edit_denies_dotfiles_under_spec_proto(self, hook):
        """Edit denies dotfiles under spec/proto"""
        (self.fixture / "spec/proto/flowseer/api/v1").mkdir(parents=True)
        for name in (".gitkeep", ".audit.sh"):
            with self.subTest(name=name):
                self.assertEqual(hook.decide(self.edit(self.fixture / "spec/proto/flowseer/api/v1" / name)), "deny")

    @across("pre-tool-policy")
    def test_edit_denies_stray_non_schema_files_under_spec_proto(self, hook):
        """Edit denies stray non-schema files under spec/proto"""
        self.assertEqual(hook.decide(self.edit(self.fixture / "spec/proto/notes.txt")), "deny")

    @across("pre-tool-policy")
    def test_generated_and_lock_paths_are_denied_at_any_depth_and_the_readme_is_allowed(self, hook):
        """Edit denies buf.lock, frontend generated output, and test artifacts in the Buf source tree"""
        denied = (
            "buf.lock",
            "frontend/web/generated/a/b/c.ts",
            "generated/a/b/c/d.go",
            "spec/proto/a/testdata/x.proto",
            "spec/proto/a/fixtures/x.proto",
            "spec/proto/a/_test_fixtures/x.proto",
        )
        for path in denied:
            with self.subTest(path=path):
                self.assertEqual(hook.decide(self.edit(self.fixture / path)), "deny")
        for path in ("spec/proto/a/b/c/x.proto", "spec/proto/a/README.md", "spec/proto/README.md"):
            with self.subTest(path=path):
                self.assertEqual(hook.decide(self.edit(self.fixture / path)), "allow")

    @across("pre-tool-policy")
    def test_a_notebook_path_is_judged_like_a_file_path(self, hook):
        """Edit judges a notebook path like a file path"""
        payload = {"cwd": str(self.fixture), "tool_input": {"notebook_path": str(self.fixture / "generated/n.ipynb")}}
        self.assertEqual(hook.decide(payload), "deny")

    @across("pre-tool-policy")
    def test_a_call_that_names_no_path_is_denied(self, hook):
        """Edit denies a call that names no path"""
        for payload in ({"cwd": str(self.fixture), "tool_input": {}}, {"cwd": str(self.fixture)}):
            result = hook.output(payload)["hookSpecificOutput"]
            self.assertEqual(result["permissionDecision"], "deny")
            self.assertIn("could not determine the target path", result["permissionDecisionReason"])

    @across("pre-tool-policy")
    def test_a_path_with_a_dot_dot_in_its_missing_tail_is_denied_as_unresolvable(self, hook):
        """Edit denies a path it cannot resolve safely"""
        result = hook.output(self.edit(self.fixture / "nope" / ".." / "README.md"))["hookSpecificOutput"]
        self.assertEqual(result["permissionDecision"], "deny")
        self.assertIn("cannot be resolved safely", result["permissionDecisionReason"])

    def test_generated_guards_fail_closed_on_malformed_json(self):
        """generated guards fail closed on malformed JSON"""
        for name in ("pre-tool-policy", "protect-generated-bash"):
            commands = implementations(name)
            self.assertTrue(commands, f"no implementation of the hook {name}")
            for command in commands:
                with self.subTest(hook=" ".join(command)):
                    self.assertEqual(Hook(name, command, self).decide("{malformed"), "deny")

    @across("pre-tool-policy")
    def test_null_and_false_are_rejected_and_every_other_json_value_is_read_as_an_event(self, hook):
        """null and false are malformed input, and every other JSON value is read as an event"""
        for text in ("", "not json", "null", "false", " null "):
            with self.subTest(input=text):
                result = hook.output(text)["hookSpecificOutput"]
                self.assertEqual(result["permissionDecision"], "deny")
                self.assertIn("received malformed input", result["permissionDecisionReason"])
        for text in ("true", "0", '""', "[]", "{}"):
            with self.subTest(input=text):
                result = hook.output(text, cwd=self.fixture)["hookSpecificOutput"]
                self.assertEqual(result["permissionDecision"], "deny")
                self.assertIn("could not determine the target path", result["permissionDecisionReason"])


if __name__ == "__main__":
    unittest.main()
