import unittest

from tests.verify import support
from tests.verify.support import across

GO_MOD = "module example.invalid/m\n\ngo 1.27\n"
NESTED_MOD = "module example.invalid/nested\n\ngo 1.27\n"
OFF = "service_otel_integration=false"
ON = "service_otel_integration=true"
HOOKS = f"{OFF}\nhook_tooling=true"
PROTO = f"{OFF}\nproto=true"
BUF_MODULE = f"{PROTO}\ntool_module=tools/buf mode=mod-verify"

# (class, files that exist, path argument, selection). A path that is not in
# files is gone from the tree.
ROWS = [
    ("2 plan, existing", {"docs/plans/x-plan.md": "# Plan\n"}, "docs/plans/x-plan.md", f"{OFF}\nplan_state=true"),
    ("2 plan state, deleted", {}, "docs/plans/gone-plan.state.json", f"{OFF}\nplan_state=true"),
    ("4 markdown, gone", {}, "docs/gone.md", OFF),
    ("5 skill, deleted", {}, ".agents/skills/x/SKILL.md", HOOKS),
    ("5 codex note, deleted", {}, ".codex/notes.md", HOOKS),
    ("5 CLAUDE.md, existing", {"CLAUDE.md": "# Agents\n"}, "CLAUDE.md", HOOKS),
    ("5 AGENTS.md, deleted", {}, "AGENTS.md", HOOKS),
    ("5 agent knowledge, deleted", {}, "docs/agent-knowledge.md", HOOKS),
    ("5 scripts readme, existing", {"tools/scripts/README.md": "# Scripts\n"}, "tools/scripts/README.md", HOOKS),
    ("6 go under tools/scripts, not 14", {"go.mod": GO_MOD, "tools/scripts/x.go": "package x\n"}, "tools/scripts/x.go", f"{OFF}\nmodule=. mode=full"),
    (
        "7 generated nested module, deleted",
        {"generated/go/yang/go.mod": NESTED_MOD},
        "generated/go/yang/v/m.go",
        f"{OFF}\nmodule=generated/go/yang mode=build-sample-lint",
    ),
    ("8 generated root module", {"go.mod": GO_MOD, "generated/go/proto/x.pb.go": "package x\n"}, "generated/go/proto/x.pb.go", OFF),
    ("8 web generated", {"go.mod": GO_MOD, "frontend/web/generated/x.go": "package x\n"}, "frontend/web/generated/x.go", OFF),
    ("8 deleted outside generated", {"go.mod": GO_MOD}, "src/x/gone.go", OFF),
    ("9 buf module go.sum", {"tools/buf/go.sum": ""}, "tools/buf/go.sum", BUF_MODULE),
    ("10 nested go.mod", {"sub/go.mod": NESTED_MOD}, "sub/go.mod", f"{OFF}\nmodule=sub mode=full"),
    ("10 nested go.sum", {"sub/go.mod": NESTED_MOD, "sub/go.sum": ""}, "sub/go.sum", f"{OFF}\nmodule=sub mode=full"),
    ("13 buf.yaml", {"buf.yaml": "version: v2\n"}, "buf.yaml", PROTO),
    ("13 buf.work.yaml", {"buf.work.yaml": "version: v1\n"}, "buf.work.yaml", PROTO),
    ("14 agents", {"x.txt": ""}, ".agents/x.txt", HOOKS),
    ("14 claude settings", {}, ".claude/settings.json", HOOKS),
    ("14 codex hooks", {}, ".codex/hooks.json", HOOKS),
    ("14 test script", {"tools/test/x.sh": ""}, "tools/test/x.sh", HOOKS),
    ("14 scripts", {"tools/scripts/lib/x.py": ""}, "tools/scripts/lib/x.py", HOOKS),
    ("14 uv pin", {"uv.toml": ""}, "uv.toml", HOOKS),
    ("16 buf tool file", {"tools/buf/x.txt": ""}, "tools/buf/x.txt", BUF_MODULE),
    (
        "17 service package, nested directory",
        {"go.mod": GO_MOD, "src/common/service/a/b.go": "package b\n"},
        "src/common/service/a/b.go",
        f"{ON}\nmodule=. mode=full",
    ),
    ("17 otel path crossing a slash", {"src/common/service/test/integration/otel/x.txt": ""}, "src/common/service/test/integration/otel/x.txt", ON),
    ("19 other file", {"LICENSE": ""}, "LICENSE", OFF),
    ("19 other directory", {"docs/x.txt": ""}, "docs/x.txt", OFF),
]


class PathClassTest(support.VerifierCase):
    """Each path class of the verifier selects its gates and no others."""

    @across
    def test_every_path_class_selects_its_gates(self):
        for label, files, path, expected in ROWS:
            with self.subTest(row=label):
                self.fresh()
                for name, text in files.items():
                    self.write(name, text)
                self.assertEqual(self.select("--", path), expected)

    @across
    def test_directory_expands_to_its_files(self):
        self.write("pkg/go.mod", NESTED_MOD)
        self.git("add", "pkg/go.mod")
        self.write("pkg/b.go", "package b\n")
        self.assertEqual(self.select("--", "pkg"), f"{OFF}\nmodule=pkg mode=full")


class SymlinkedPathTest(support.VerifierCase):
    """A path spelled through a symlinked directory selects what git's spelling selects."""

    def populate(self):
        self.write("real/pkg/go.mod", GO_MOD)
        self.write("real/pkg/a.go", "package pkg\n")
        (self.root / "real/empty").mkdir()
        (self.root / "alias").symlink_to("real")

    def selection(self, *paths):
        result = self.verify("--print-selection", "--", *paths)
        self.assertEqual(result.code, 0, result.stderr)
        return result.stdout.splitlines()

    def test_fixture_tracks_the_target_spelling_only(self):
        self.assertEqual(self.git("ls-files").split("\n"), ["alias", "real/pkg/a.go", "real/pkg/go.mod"])

    @across
    def test_directory_through_a_symlink_expands_to_the_tracked_files(self):
        self.assertIn("module=real/pkg mode=full", self.selection("alias/pkg"))

    @across
    def test_file_through_a_symlink_is_rewritten_to_the_tracked_spelling(self):
        self.assertIn("module=real/pkg mode=full", self.selection("alias/pkg/a.go"))

    @across
    def test_missing_file_through_a_symlink_keeps_its_tail(self):
        self.assertIn("module=real/pkg mode=full", self.selection("alias/pkg/gone/go.mod"))

    @across
    def test_repository_scripts_and_uv_pin_select_hook_tooling(self):
        self.write("tools/scripts/lib/repo.py")
        self.write("uv.toml", 'required-version = ">=0.12.18"\n')
        for path in ("tools/scripts/lib/repo.py", "uv.toml"):
            with self.subTest(path=path):
                self.assertIn("hook_tooling=true", self.selection(path))

    @across
    def test_scripts_readme_selects_hook_tooling(self):
        self.write("tools/scripts/README.md", "# Scripts\n")
        self.assertIn("hook_tooling=true", self.selection("tools/scripts/README.md"))


class ExitPathTest(support.VerifierCase):
    """Each exit before a gate runs gives its code and its last line."""

    def failed(self, result, code, message=None):
        """Require the code, the verdict as the last line, and message as the first line of standard error."""
        self.assertEqual(result.code, code, result.stdout + result.stderr)
        self.assertEqual(support.last_line(result.stderr), support.verdict(code))
        if message is not None:
            self.assertEqual(result.stderr.split("\n")[0], message)

    @across
    def test_base_without_a_ref_is_refused(self):
        result = self.verify("--base")
        self.failed(result, 2, "--base requires a ref")
        self.assertEqual(result.stdout, "")

    @across
    def test_empty_path_argument_is_refused(self):
        result = self.verify("--", "")
        self.failed(result, 2, "empty path argument after --")

    @across
    def test_empty_path_among_others_is_refused(self):
        result = self.verify("--", "README.md", "")
        self.failed(result, 2, "empty path argument after --")

    @across
    def test_help_prints_the_usage_and_exits_0(self):
        for flag in ("-h", "--help"):
            with self.subTest(flag=flag):
                result = self.verify(flag)
                self.assertEqual(result.code, 0, result.stderr)
                self.assertEqual(result.stderr, "")
                lines = result.stdout.split("\n")
                self.assertTrue(lines[0].endswith(support.USAGE_OPTIONS), lines[0])
                self.assertEqual(support.last_line(result.stdout), support.USAGE_LAST)

    @across
    def test_unknown_argument_is_refused_with_the_usage(self):
        result = self.verify("--bogus")
        self.failed(result, 2, "unknown argument: --bogus")
        lines = result.stderr.split("\n")
        self.assertTrue(lines[1].endswith(support.USAGE_OPTIONS), lines[1])
        self.assertIn(support.USAGE_LAST, lines)

    @across
    def test_outside_a_repository_exits_with_gits_status(self):
        outside = self.parent / "outside"
        outside.mkdir()
        result = self.verify("--print-selection", "--", "README.md", cwd=outside, env=self.stubs.env(self.env, GIT_CEILING_DIRECTORIES=str(self.parent)))
        self.failed(result, 128)
        self.assertIn("not a git repository", result.stderr)

    @across
    def test_base_that_is_no_commit_exits_with_gits_status(self):
        result = self.verify("--base", "no-such-ref")
        self.failed(result, 128)
        self.assertEqual(result.stdout, "")

    @across
    def test_dash_dash_without_a_path_fails_unless_it_only_selects(self):
        result = self.verify("--")
        self.failed(result, 2, "no paths supplied after --")
        selected = self.verify("--print-selection", "--")
        self.assertEqual((selected.code, selected.stdout, selected.stderr), (0, f"{OFF}\n", ""))

    @across
    def test_no_changed_path_fails_unless_it_only_selects(self):
        result = self.verify()
        self.failed(
            result,
            2,
            "No changed paths to verify against HEAD. After a commit, use --base main, name paths after --, or use --full.",
        )
        named = self.verify("--base", "main")
        self.failed(
            named,
            2,
            "No changed paths to verify against main. After a commit, use --base main, name paths after --, or use --full.",
        )
        selected = self.verify("--print-selection")
        self.assertEqual((selected.code, selected.stdout, selected.stderr), (0, f"{OFF}\n", ""))

    @across
    def test_directories_holding_no_file_are_refused(self):
        (self.root / "empty").mkdir()
        self.failed(self.verify("--", "empty"), 2, "FlowSeer verification found no files under the named paths.")

    @across
    def test_lint_config_alone_asks_for_full(self):
        self.write(".golangci.yml", 'version: "2"\n')
        result = self.verify("--", ".golangci.yml")
        self.failed(result, 2, ".golangci.yml changed; run with --full to lint every Go module under the new configuration.")
        self.assertEqual(self.calls(), [])
        self.assertFalse(self.receipt.exists())

    @across
    def test_lint_config_beside_a_go_file_still_asks_for_full(self):
        # One selected module would be linted under the new configuration,
        # the rest not, and the receipt would read as if all had passed.
        self.write(".golangci.yml", 'version: "2"\n')
        self.populate_go()
        result = self.verify("--", ".golangci.yml", "a.go")
        self.failed(result, 2, ".golangci.yml changed; run with --full to lint every Go module under the new configuration.")

    @across
    def test_lint_config_only_selects_when_nothing_runs(self):
        self.write(".golangci.yml", 'version: "2"\n')
        self.assertEqual(self.select("--", ".golangci.yml"), OFF)


class VerdictTest(support.VerifierCase):
    """A run that checks nothing, or fails, says so in its last line."""

    @across
    def test_failing_gate_is_named_in_the_last_line(self):
        self.populate_go()
        env = self.stubs.env(self.env, FLOWSEER_FAKE_FAIL="vet", FLOWSEER_FAKE_FAIL_CODE="3")
        result = self.verify("--", "a.go", env=env)
        self.assertEqual(result.code, 3, result.stdout + result.stderr)
        self.assertEqual(support.last_line(result.stderr), support.verdict(3, "go vet"))
        self.assertFalse(self.receipt.exists())

    @across
    def test_format_failure_names_its_gate(self):
        self.populate_go()
        self.stubs.add("gofumpt", "echo '-unformatted'")
        result = self.verify("--", "a.go")
        self.assertEqual(result.code, 1, result.stdout + result.stderr)
        self.assertEqual(support.last_line(result.stderr), support.verdict(1, "gofumpt/goimports -d"))
        self.assertIn("Go formatting differs; run gofumpt and goimports.", result.stderr)

    @across
    def test_prose_failure_names_its_run_py_command(self):
        # The fixture holds no tools/scripts, so the checks resolve run.py
        # from the checkout that holds the verifier.
        self.write("README.md", "# Fixture\n\nA claim (session history) with no source.\n")
        result = self.verify("--", "README.md")
        self.assertEqual(result.code, 1, result.stdout + result.stderr)
        self.assertEqual(support.last_line(result.stderr), support.verdict(1, "run.py verify check-prose"))

    @across
    def test_file_name_with_a_carriage_return_reaches_its_check(self):
        name = "bad\rname.md"
        self.write(name, "# Bad\n\nA claim (session history) with no source.\n")
        result = self.verify("--", name)
        self.assertEqual(result.code, 1, result.stdout + result.stderr)
        self.assertEqual(support.last_line(result.stderr), support.verdict(1, "run.py verify check-prose"))


if __name__ == "__main__":
    unittest.main()
