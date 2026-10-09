import os
import unittest

from tests.verify import support
from tests.verify.support import across

GUARD_BUILD = ".\tgo build -o <build>/check-guarantees ./tools/check-guarantees"
BUF = ".\tgo tool -modfile=<root>/tools/buf/go.mod buf"
WEB = ["vue-tsc --noEmit", "vite build", "eslint .", "stylelint src/**/*.{css,vue}", "prettier --check .", "vitest run"]
REPOSITORY_WIDE = ".\tgo test -race ./src/common/errs ./test/conformance/..."
GOLANGCI = ".\tgolangci-lint run --allow-serial-runners --config <root>/.golangci.yml ."
CORPUS = ".\tgo test -count=1 -tags=smi_corpus_full -run TestCorpus ./src/protocol/smi/"
DIFFERENTIAL = ".\tgo test -C src/protocol/smi/differential -count=1 ./..."


def guard(*paths: str) -> list[str]:
    return [GUARD_BUILD, f".\tcheck-guarantees -- {' '.join(paths)}"]


class RecordedRunTest(support.VerifierCase):
    """The gates a path class starts, in order, with the directory and arguments of each."""

    @across
    def test_go_and_web_paths_run_the_conformance_gates_once(self):
        self.populate_go()
        self.stubs.add_web(self.root)
        self.write("frontend/web/src/a.ts", "export {}\n")
        self.passes("--", "a.go", "frontend/web/src/a.ts")
        calls = self.calls()
        self.assertEqual(calls.count(REPOSITORY_WIDE), 1, calls)
        self.assertNotIn(".\tgo test -race ./test/conformance/...", calls)

    @across
    def test_web_path_alone_runs_the_conformance_gates_after_the_web_gates(self):
        self.stubs.add_web(self.root)
        self.write("frontend/web/src/a.ts", "export {}\n")
        self.passes("--", "frontend/web/src/a.ts")
        self.assertEqual(
            self.calls(),
            [*guard("frontend/web/src/a.ts"), *(f"frontend/web\t{gate}" for gate in WEB), ".\tgo test -race ./test/conformance/..."],
        )

    @across
    def test_markdown_path_runs_the_checks_and_builds_the_guard(self):
        self.write("README.md", "# Fixture\n\nChanged.\n")
        self.passes("--", "README.md")
        self.assertEqual(self.calls(), guard("README.md"))

    @across
    def test_markdown_path_with_a_broken_link_fails_its_link_check(self):
        self.write("README.md", "# Fixture\n\n[gone](missing.md)\n")
        self.fails("--", "README.md", code=1, gate="run.py verify check-markdown-links")

    @across
    def test_go_path_runs_format_build_vet_test_and_lint(self):
        self.populate_go()
        self.passes("--", "a.go")
        self.assertEqual(
            self.calls(),
            [
                *guard("a.go"),
                ".\tgofumpt -d a.go",
                ".\tgoimports -d a.go",
                ".\tgo build ./...",
                ".\tgo vet example.invalid/m",
                ".\tgo test -race example.invalid/m",
                GOLANGCI,
                REPOSITORY_WIDE,
            ],
        )

    @across
    def test_schema_path_runs_the_protobuf_gates_with_its_path(self):
        self.write("spec/proto/x/v1/a.proto", 'syntax = "proto3";\n')
        self.write("generated/go/proto/x.pb.go", "package proto\n")
        self.commit_all("schema")
        self.write("spec/proto/x/v1/a.proto", 'syntax = "proto3";\n\n// Changed.\n')
        self.passes("--", "spec/proto/x/v1/a.proto")
        path = "spec/proto/x/v1/a.proto"
        self.assertEqual(
            self.calls(),
            [
                *guard(path),
                f"{BUF} format -d --exit-code --path {path}",
                f"{BUF} lint --path {path}",
                f"{BUF} breaking --against .git#branch=main --path {path}",
                f"{BUF} generate -o <build>/generated",
                ".\tgo test -count=1 ./test/conformance/proto/",
            ],
        )

    @across
    def test_deleted_schema_path_compares_the_whole_module(self):
        self.write("spec/proto/x/v1/a.proto", 'syntax = "proto3";\n')
        self.write("generated/go/proto/x.pb.go", "package proto\n")
        self.commit_all("schema")
        (self.root / "spec/proto/x/v1/a.proto").unlink()
        self.passes("--", "spec/proto/x/v1/a.proto")
        self.assertEqual(
            self.calls(),
            [
                *guard("spec/proto/x/v1/a.proto"),
                f"{BUF} format -d --exit-code",
                f"{BUF} lint",
                f"{BUF} breaking --against .git#branch=main",
                f"{BUF} generate -o <build>/generated",
                ".\tgo test -count=1 ./test/conformance/proto/",
            ],
        )

    @across
    def test_mib_path_runs_the_drift_check_and_the_baseline_gate(self):
        self.write("spec/mib/x.mib", "X DEFINITIONS ::= BEGIN END\n")
        self.passes("--", "spec/mib/x.mib")
        self.assertEqual(
            self.calls(),
            [
                *guard("spec/mib/x.mib"),
                ".\tgo run ./src/protocol/snmp/cmd/mibgen -check",
                ".\tgo test -tags=mibgen_baseline_complete -run TestBaselineComplete ./src/protocol/snmp/cmd/mibgen/",
            ],
        )

    @across
    def test_hook_tooling_validates_the_settings_and_lints_both_script_patterns(self):
        self.populate_hook_tooling()
        result = self.passes("--", "uv.toml")
        self.assertEqual(
            self.calls(),
            [
                *guard("uv.toml"),
                ".\tjq empty .claude/settings.json",
                ".\tjq empty .codex/hooks.json",
                ".\tshellcheck tools/test/x.sh .claude/skills/x/scripts/y.sh",
            ],
        )
        self.assertIn("Ran 1 tests", result.stdout)

    @across
    def test_hook_tooling_without_a_shell_script_skips_shellcheck(self):
        self.write("tools/scripts/run.py", self.suite_script(tests=1))
        result = self.passes("--", "uv.toml")
        self.assertEqual(
            self.calls(),
            [
                *guard("uv.toml"),
                ".\tjq empty .claude/settings.json",
                ".\tjq empty .codex/hooks.json",
            ],
        )
        self.assertIn("shellcheck skipped: no host-side shell script matched.", result.stdout)
        self.assertIn("Ran 1 tests", result.stdout)

    @across
    def test_full_run_starts_every_gate(self):
        self.populate_full()
        self.commit_all("full")
        self.passes("--full")
        self.assertEqual(self.calls(), self.full_calls())

    def full_calls(self) -> list[str]:
        """The log of a `--full` run over the fixture of `populate_full`."""
        cpus = os.cpu_count() or 4
        return [
            GUARD_BUILD,
            ".\tcheck-guarantees --all",
            *(f"frontend/web\t{gate}" for gate in WEB),
            ".\tgofumpt -d a.go",
            ".\tgoimports -d a.go",
            ".\tgo build ./...",
            ".\tgo vet ./...",
            f".\tgo test -race -p {max(cpus // 2, 2)} ./...",
            GOLANGCI,
            ".\tgo -C tools/buf mod verify",
            f"{BUF} format -d --exit-code",
            f"{BUF} lint",
            f"{BUF} breaking --against .git#branch=main",
            f"{BUF} generate -o <build>/generated",
            ".\tgo test -count=1 ./test/conformance/proto/",
            ".\tgo run ./src/protocol/snmp/cmd/mibgen -check",
            ".\tgo test -tags=mibgen_baseline_complete -run TestBaselineComplete ./src/protocol/snmp/cmd/mibgen/",
            CORPUS,
            DIFFERENTIAL,
            ".\tjq empty .claude/settings.json",
            ".\tjq empty .codex/hooks.json",
            ".\tshellcheck tools/test/x.sh .claude/skills/x/scripts/y.sh",
            ".\tdocker info",
            ".\tgo test -race -count=1 -short=false -tags=service_otel_integration ./src/common/service/test/integration/...",
        ]


class FixTest(support.VerifierCase):
    """Each case pins a defect that once let a gate pass what it should refuse."""

    @across
    def test_breaking_names_only_the_schema_files_main_holds(self):
        existing = "spec/proto/x/v1/a.proto"
        added = "spec/proto/x/v1/b.proto"
        self.write(existing, 'syntax = "proto3";\n')
        self.write("generated/go/proto/x.pb.go", "package proto\n")
        self.commit_all("schema")
        self.git("checkout", "-qb", "schemas")
        self.write(existing, 'syntax = "proto3";\n\n// Changed.\n')
        self.write(added, 'syntax = "proto3";\n')
        self.passes("--", existing, added)
        self.assertEqual(
            [call for call in self.calls() if " buf breaking " in call],
            [f"{BUF} breaking --against .git#branch=main --path {existing}"],
        )
        self.stubs.log.unlink()
        result = self.passes("--", added)
        self.assertFalse(any(" buf breaking " in call for call in self.calls()), self.calls())
        self.assertIn("breaking skipped: every changed .proto file is new on this branch.", result.stdout)

    def full_run(self):
        self.populate_full()
        self.commit_all("full")
        self.passes("--full")
        return self.calls()

    @across
    def test_full_run_gives_the_corpus_tier_count_1(self):
        calls = self.full_run()
        self.assertIn(CORPUS, calls)
        self.assertIn(DIFFERENTIAL, calls)

    @across
    def test_full_run_bounds_test_parallelism(self):
        cpus = os.cpu_count() or 4
        self.assertIn(f".\tgo test -race -p {max(cpus // 2, 2)} ./...", self.full_run())

    @across
    def test_lint_waits_for_the_machine_lock(self):
        self.populate_go()
        self.passes("--", "a.go")
        self.assertIn(GOLANGCI, self.calls())

    @across
    def test_invariant_packages_run_once_from_the_root(self):
        self.populate_go()
        self.write("nested/go.mod", "module example.invalid/nested\n\ngo 1.27\n")
        self.write("nested/b.go", "package b\n")
        self.passes("--", "a.go", "nested/b.go")
        calls = self.calls()
        self.assertEqual(calls.count(REPOSITORY_WIDE), 1, calls)
        self.assertIn("nested\tgo vet example.invalid/nested", calls)
        self.assertIn(".\tgo vet example.invalid/m", calls)

    @across
    def test_tools_under_gopath_bin_are_found(self):
        elsewhere = support.Stubs(self.parent / "elsewhere", self.stubs.log)
        elsewhere.add_defaults(skip=("gofumpt", "goimports", "golangci-lint"))
        onpath = support.Stubs(self.gopath / "bin", self.stubs.log)
        for name in ("gofumpt", "goimports", "golangci-lint"):
            onpath.add(name)
        self.populate_go()
        path = support.minimal_path(self.parent / "essential", *support.ESSENTIAL_TOOLS, "python3")
        env = elsewhere.env(self.env, path=path)
        self.passes("--", "a.go", env=env)
        calls = self.calls()
        for tool in (".\tgofumpt -d a.go", ".\tgoimports -d a.go", GOLANGCI):
            self.assertIn(tool, calls)

    @across
    def test_missing_tools_stop_the_run_before_any_gate(self):
        bare = support.Stubs(self.parent / "bare", self.stubs.log)
        bare.add("go")
        path = support.minimal_path(self.parent / "essential", *support.ESSENTIAL_TOOLS)
        env = bare.env(self.env, path=path)
        self.populate_go()
        self.populate_hook_tooling()
        hint = "Go tools install to $(go env GOPATH)/bin; put that directory on PATH or install the missing ones there."
        result = self.fails("--", "a.go", "uv.toml", code=1, env=env)
        self.assertEqual(
            result.stderr.split("\n")[:-1],
            ["required tools are not on PATH: gofumpt goimports golangci-lint python3 jq shellcheck", hint, support.verdict(1)],
        )
        self.assertEqual((result.stdout, self.calls()), ("", []))
        # The hint is for Go tools, so a run that misses none leaves it out.
        result = self.fails("--", "uv.toml", code=1, env=env)
        self.assertEqual(
            result.stderr.split("\n")[:-1],
            ["required tools are not on PATH: jq shellcheck python3", support.verdict(1)],
        )

    @across
    def test_web_path_runs_the_six_web_gates(self):
        self.stubs.add_web(self.root)
        self.write("frontend/web/src/a.ts", "export {}\n")
        self.passes("--", "frontend/web/src/a.ts")
        self.assertEqual([call for call in self.calls() if call.startswith("frontend/web\t")], [f"frontend/web\t{gate}" for gate in WEB])

    @across
    def test_targets_are_the_changed_packages_and_their_importers(self):
        self.write("go.mod", "module example.invalid/m\n\ngo 1.27\n")
        self.write("lib/a/a.go", "package a\n")
        self.write("lib/b/b.go", 'package b\n\nimport _ "example.invalid/m/lib/a"\n')
        self.write("lib/c/c.go", 'package c\n\nimport _ "example.invalid/m/lib/b"\n')
        self.write("lib/d/d.go", "package d\n")
        self.write("lib/e/e.go", "package e\n")
        self.write("lib/e/e_test.go", 'package e\n\nimport _ "example.invalid/m/lib/a"\n')
        result = self.passes("--", "lib/a/a.go")
        targets = "example.invalid/m/lib/a example.invalid/m/lib/b example.invalid/m/lib/c example.invalid/m/lib/e"
        self.assertIn("Targeted packages: 4 (changed: example.invalid/m/lib/a)", result.stdout)
        calls = self.calls()
        self.assertIn(f".\tgo vet {targets}", calls)
        self.assertIn(f".\tgo test -race {targets}", calls)


class ExitPathTest(support.VerifierCase):
    """The exits after the selection, each with its code and last line."""

    @across
    def test_a_gate_killed_by_term_reports_exit_143(self):
        self.populate_go()
        self.stubs.add("golangci-lint", "kill -TERM $$")
        self.fails("--", "a.go", code=143, gate="golangci-lint run")

    @across
    def test_failing_directory_listing_skips_lint_and_passes(self):
        self.populate_go()
        env = self.stubs.env(self.env, FLOWSEER_FAKE_FAIL_ON_ARGS="list -f {{.Dir}}", FLOWSEER_FAKE_FAIL_CODE="4")
        self.passes("--", "a.go", env=env)
        calls = self.calls()
        self.assertFalse(any("golangci-lint" in call for call in calls), calls)
        self.assertIn(".\tgo test -race example.invalid/m", calls)
        self.assertIn(REPOSITORY_WIDE, calls)

    @across
    def test_missing_web_binary_stops_the_run_naming_it(self):
        for tool in support.WEB_TOOLS:
            with self.subTest(tool=tool):
                self.fresh()
                self.stubs.add_web(self.root)
                (self.root / "frontend/web/node_modules/.bin" / tool).unlink()
                self.write("frontend/web/src/a.ts", "export {}\n")
                result = self.fails("--", "frontend/web/src/a.ts", code=1)
                self.assertEqual(
                    result.stderr.split("\n")[0],
                    f"required web tool is missing: frontend/web/node_modules/.bin/{tool}; install frontend/web dependencies",
                )
                self.assertEqual(self.calls(), [])

    @across
    def test_command_outside_a_gate_fails_with_its_status_and_no_gate(self):
        # The test-integrity check is read inside a capture, where a failure
        # ends the run with the check's own status.
        result = self.fails("--base", "no-such-ref", "--", "README.md", code=2)
        self.assertIn("fatal: bad revision 'no-such-ref'", result.stderr)
        self.assertNotIn("in gate", result.stderr)

    @across
    def test_failing_package_listing_fails_with_its_status_and_no_gate(self):
        self.populate_go()
        env = self.stubs.env(self.env, FLOWSEER_FAKE_FAIL_ON_ARGS="join .Deps", FLOWSEER_FAKE_FAIL_CODE="4")
        self.fails("--", "a.go", code=4, env=env)
        self.assertEqual(self.calls(), [*guard("a.go"), ".\tgofumpt -d a.go", ".\tgoimports -d a.go", ".\tgo build ./..."])

    @across
    def test_schema_change_without_main_or_master_fails_the_breaking_gate(self):
        self.write("spec/proto/x/v1/a.proto", 'syntax = "proto3";\n')
        self.commit_all("schema")
        self.git("branch", "-m", "trunk")
        result = self.fails("--", "spec/proto/x/v1/a.proto", code=1)
        self.assertIn("the breaking gate needs a main (or master) branch to compare against, and neither exists.", result.stderr)
        self.assertFalse(any("breaking" in call for call in self.calls()))

    @across
    def test_failing_script_suite_names_its_gate(self):
        self.populate_hook_tooling()
        self.write("tools/scripts/run.py", self.suite_script(tests=2, failing=True))
        result = self.fails("--", "uv.toml", code=1, gate="run.py test")
        self.assertIn("Ran 2 tests", result.stdout)

    @across
    def test_script_suite_that_ran_zero_tests_fails(self):
        self.populate_hook_tooling()
        self.write("tools/scripts/run.py", self.suite_script(tests=0))
        result = self.fails("--", "uv.toml", code=1, gate="uv run tools/scripts/run.py test")
        self.assertIn("tools/scripts tests ran zero tests.", result.stderr)


if __name__ == "__main__":
    unittest.main()
