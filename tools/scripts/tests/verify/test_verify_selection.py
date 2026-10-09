import os
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parents[4]
VERIFIER = REPO / ".claude" / "skills" / "verify-change" / "scripts" / "verify-change.sh"
BASH = shutil.which("bash") or "bash"
PLACEHOLDER = "v0.0.0-00010101000000-000000000000"


class SelectionCase(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.base = Path(directory.name).resolve()

    def git(self, repo: Path, *args: str) -> str:
        done = subprocess.run(
            ["git", "-c", "user.name=Hook", "-c", "user.email=hook@example.invalid", "-C", str(repo), *args],
            capture_output=True,
            text=True,
            check=False,
            env=self.env(),
        )
        self.assertEqual(done.returncode, 0, f"git {' '.join(args)}: {done.stderr}")
        return done.stdout.strip()

    def env(self, **extra: str) -> dict[str, str]:
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env["GIT_CONFIG_GLOBAL"] = os.devnull
        env["GIT_CONFIG_NOSYSTEM"] = "1"
        env.update(extra)
        return env

    def write(self, path: Path, text: str = "") -> Path:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        return path


class VerifierSelectionTest(SelectionCase):
    def setUp(self):
        super().setUp()
        self.fixture = self.base / "selection fixture"
        self.fixture.mkdir()
        self.git(self.fixture, "init", "-q", "-b", "main")
        self.write(self.fixture / "README.md", "# selection fixture\n")
        self.write(self.fixture / "go.mod", "module example.invalid/selection\n\ngo 1.27\n")
        self.write(self.fixture / "tools/buf/go.mod", "module example.invalid/buf-tool\n\ngo 1.27\n")
        self.git(self.fixture, "add", "README.md", "go.mod")
        self.git(self.fixture, "commit", "-qm", "init")
        self.receipt = self.fixture / ".git" / "flowseer-verification-receipt"
        self.docker_called = self.fixture / "docker-called"
        self.go_called = self.fixture / "go-called"
        self.bin = self.fixture / "selection-bin"
        self.tmp = self.fixture / "selection-tmp"
        self.tmp.mkdir()
        for name, called, code in (("docker", self.docker_called, 97), ("go", self.go_called, 98)):
            script = self.write(self.bin / name, f"#!/usr/bin/env bash\n: >'{called}'\nexit {code}\n")
            script.chmod(0o755)

    def verifier_env(self) -> dict[str, str]:
        return self.env(PATH=f"{self.bin}{os.pathsep}{os.environ['PATH']}", TMPDIR=str(self.tmp))

    def select(self, *args: str) -> str:
        """The selection the verifier prints for args, as `$(...)` returns it."""
        done = subprocess.run(
            [str(VERIFIER), "--print-selection", *args],
            cwd=self.fixture,
            env=self.verifier_env(),
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(done.returncode, 0, done.stderr)
        return done.stdout.rstrip("\n")

    def test_verifier_selection_includes_the_collector_tier_for_full_verification(self):
        """verifier selection includes the Collector tier for full verification"""
        output = self.select("--full")
        self.assertEqual(output.split("\n")[0], "service_otel_integration=true")
        self.assertIn("\nproto=true\n", output)
        self.assertIn("\ntool_module=tools/buf mode=mod-verify\n", output)
        self.assertNotIn("\nmodule=./tools/buf", output)
        self.assertFalse(self.receipt.exists())

    def test_verifier_selection_routes_the_pinned_buf_module_to_protobuf_gates(self):
        """verifier selection routes the pinned Buf module to protobuf gates"""
        for path in ("tools/buf/go.mod", "tools/buf/go.sum"):
            with self.subTest(path=path):
                self.assertEqual(
                    self.select("--", path),
                    "service_otel_integration=false\nproto=true\ntool_module=tools/buf mode=mod-verify",
                )

    def test_verifier_selection_routes_buf_generation_configuration_to_protobuf_gates(self):
        """verifier selection routes Buf generation configuration to protobuf gates"""
        output = self.select("--", "buf.gen.yaml")
        self.assertEqual(output, "service_otel_integration=false\nproto=true")
        self.assertNotIn("tool_module=", output)

    def test_verifier_selection_routes_a_plan_state_file_to_the_state_gate(self):
        """verifier selection routes a plan state file to the state gate"""
        self.write(self.fixture / "docs/plans/x-plan.md", "# Plan\n")
        self.write(self.fixture / "docs/plans/x-plan.state.json", '{"Landed": "abc1234..def5678"}\n')
        self.assertEqual(
            self.select("--", "docs/plans/x-plan.state.json"),
            "service_otel_integration=false\nplan_state=true",
        )

    def test_verifier_fails_on_an_illegal_plan_state_file(self):
        """verifier fails on an illegal plan state file"""
        self.write(self.fixture / "docs/plans/x-plan.md", "# Plan\n")
        self.write(self.fixture / "docs/plans/x-plan.state.json", '{"Landed": "abc1234..def5678"}\n')
        done = subprocess.run(
            [str(VERIFIER), "--", "docs/plans/x-plan.state.json"],
            cwd=self.fixture,
            env=self.verifier_env(),
            capture_output=True,
            text=True,
            check=False,
        )
        output = done.stdout + done.stderr
        self.assertNotEqual(done.returncode, 0)
        self.assertNotIn("selected no build, test or lint gate", output)
        self.assertIn("x-plan.state.json: unknown key 'Landed'", output)
        self.assertFalse(self.receipt.exists())

    def test_verifier_selection_includes_every_telemetry_sensitive_path_category(self):
        """verifier selection includes every telemetry-sensitive path category"""
        for path in (
            "go.mod",
            "src/common/service/telemetry_config.go",
            "src/common/service/test/integration/otel_test.go",
            "src/common/service/test/integration/testdata/otel-collector.yaml",
            "tools/test/service-otel-integration.sh",
        ):
            with self.subTest(path=path):
                self.assertEqual(self.select("--", path).split("\n")[0], "service_otel_integration=true")
        self.assertFalse(self.receipt.exists())

    def test_verifier_selection_skips_unrelated_paths_and_preserves_spaces(self):
        """verifier selection skips unrelated paths and preserves spaces"""
        for path in ("docs/notes.md", "src/common/errs/example.go", "docs/path with spaces.md"):
            with self.subTest(path=path):
                self.assertEqual(self.select("--", path), "service_otel_integration=false")
        self.assertFalse(self.receipt.exists())

    def test_verifier_selection_handles_an_empty_changed_path_set_without_a_receipt(self):
        """verifier selection handles an empty changed-path set without a receipt"""
        self.assertEqual(self.select("--"), "service_otel_integration=false")
        self.assertFalse(self.receipt.exists())

    def test_verifier_selection_exits_before_recursively_running_hook_tests(self):
        """verifier selection exits before recursively running hook tests"""
        output = self.select("--", "tools/scripts/tests/hooks/support.py")
        self.assertEqual(output, "service_otel_integration=false\nhook_tooling=true")
        self.assertFalse(self.receipt.exists())
        self.assertFalse(self.docker_called.exists())
        self.assertFalse(self.go_called.exists())
        self.assertEqual(list(self.tmp.glob("flowseer-build.*")), [])


class NestedSelectionTest(SelectionCase):
    """A root module with two nested modules that replace it.

    A generated one imports lib/a and a netpen stand-in imports lib/b, and
    lib/c only from a file behind a build tag. Selection runs the real go,
    which lists packages without compiling them.
    """

    def setUp(self):
        super().setUp()
        self.fixture = self.base / "nested selection fixture"
        root = self.fixture
        for directory in ("lib/a", "lib/b", "lib/c", "generated/go/yang/v/m", "src/edge/netpen"):
            (root / directory).mkdir(parents=True)
        self.git(root, "init", "-q", "-b", "main")
        self.write(
            root / "go.mod",
            "module go.aledante.io/FlowSeer\n\ngo 1.27\n\n"
            f"require go.aledante.io/FlowSeer/generated/go/yang {PLACEHOLDER}\n\n"
            "replace go.aledante.io/FlowSeer/generated/go/yang => ./generated/go/yang\n",
        )
        self.write(root / "lib/a/a.go", "package a\n")
        self.write(root / "lib/b/b.go", "package b\n")
        self.write(
            root / "generated/go/yang/go.mod",
            "module go.aledante.io/FlowSeer/generated/go/yang\n\ngo 1.27\n\n"
            f"require go.aledante.io/FlowSeer {PLACEHOLDER}\n\nreplace go.aledante.io/FlowSeer => ../../..\n",
        )
        self.write(
            root / "generated/go/yang/v/m/m.go",
            'package m\n\nimport _ "go.aledante.io/FlowSeer/lib/a"\n',
        )
        self.write(
            root / "src/edge/netpen/go.mod",
            "module go.aledante.io/FlowSeer/src/edge/netpen\n\ngo 1.27\n\n"
            f"require go.aledante.io/FlowSeer {PLACEHOLDER}\n\nreplace go.aledante.io/FlowSeer => ../../..\n",
        )
        self.write(
            root / "src/edge/netpen/main.go",
            'package main\n\nimport _ "go.aledante.io/FlowSeer/lib/b"\n\nfunc main() {}\n',
        )
        self.write(root / "lib/c/c.go", "package c\n")
        self.write(
            root / "src/edge/netpen/tagged.go",
            '//go:build netpen_t1\n\npackage main\n\nimport _ "go.aledante.io/FlowSeer/lib/c"\n',
        )
        self.git(root, "add", ".")
        self.git(root, "commit", "-qm", "init")
        (root / ".selection-tmp").mkdir()

    def select(self, path: str) -> str:
        done = subprocess.run(
            [str(VERIFIER), "--print-selection", "--", path],
            cwd=self.fixture,
            env=self.env(GOWORK="off", TMPDIR=str(self.fixture / ".selection-tmp")),
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(done.returncode, 0, done.stderr)
        return done.stdout.rstrip("\n")

    def test_verifier_selection_skips_a_nested_module_that_does_not_import_the_changed_root_package(self):
        """verifier selection skips a nested module that does not import the changed root package"""
        output = self.select("lib/b/b.go")
        self.assertIn("\nmodule=. mode=full", output)
        self.assertIn("\ndependent=src/edge/netpen mode=build-vet", output)
        self.assertNotIn("dependent=generated/go/yang", output)

    def test_verifier_selection_counts_a_nested_modules_imports_behind_a_build_tag(self):
        """verifier selection counts a nested module's imports behind a build tag"""
        output = self.select("lib/c/c.go")
        self.assertIn("\ndependent=src/edge/netpen mode=build-vet", output)
        self.assertNotIn("dependent=generated/go/yang", output)

    def test_verifier_selection_builds_a_generated_nested_module_that_imports_the_changed_root_package(self):
        """verifier selection builds a generated nested module that imports the changed root package"""
        output = self.select("lib/a/a.go")
        self.assertIn("\ndependent=generated/go/yang mode=build", output)
        self.assertNotIn("dependent=generated/go/yang mode=build-vet", output)
        self.assertNotIn("dependent=src/edge/netpen", output)

    def test_verifier_selection_gives_a_changed_generated_file_its_own_module_build_and_sample_lint_only(self):
        """verifier selection gives a changed generated file its own module, build and sample lint only"""
        output = self.select("generated/go/yang/v/m/m.go")
        self.assertIn("\nmodule=generated/go/yang mode=build-sample-lint", output)
        self.assertNotIn("module=. ", output)
        self.assertNotIn("dependent=", output)

    def test_verifier_selection_builds_every_nested_module_for_a_root_go_mod_change(self):
        """verifier selection builds every nested module for a root go.mod change"""
        output = self.select("go.mod")
        self.assertIn("\ndependent=generated/go/yang mode=build", output)
        self.assertIn("\ndependent=src/edge/netpen mode=build-vet", output)


class RunHelperTest(unittest.TestCase):
    def test_verifier_run_helper_fails_a_command_checked_inside_a_condition(self):
        """verifier run helper fails a command checked inside a condition"""
        helper = re.search(r"^run\(\) \{\n.*?^\}\n", VERIFIER.read_text(), re.MULTILINE | re.DOTALL)
        self.assertIsNotNone(helper, "the verifier defines no run() helper")
        script = (
            "set -euo pipefail\n"
            "gate_label() { :; }\n"
            "gate_file=/dev/null\n"
            'eval "$1"\n'
            "if run false >/dev/null 2>&1; then exit 1; fi\n"
            "run true >/dev/null\n"
        )
        done = subprocess.run([BASH, "-c", script, "_", helper.group(0)], capture_output=True, text=True, check=False)
        self.assertEqual(done.returncode, 0, done.stderr)


if __name__ == "__main__":
    unittest.main()
