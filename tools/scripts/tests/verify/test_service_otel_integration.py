import os
import shutil
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path

import run

REPO = Path(__file__).resolve().parents[4]
RUN_PY = REPO / "tools" / "scripts" / "run.py"
VERIFY_GROUP = REPO / "tools" / "scripts" / "verify" / "__init__.py"
COMMAND = "service-otel-integration"
KNOB = "FLOWSEER_DOCKER_PROBE_TIMEOUT"
GO_ARGS = "test -race -count=1 -short=false -tags=service_otel_integration ./src/common/service/test/integration/..."

DOCKER = """\
#!/usr/bin/env bash
sleep "${FLOWSEER_FAKE_DOCKER_SLEEP:-0}"
exit "${FLOWSEER_FAKE_DOCKER_RC:-0}"
"""

# Fails when the wrapper leaves a variable the Collector run must not see in
# place, or loses one it must keep, and otherwise records what it was called with.
GO = """\
#!/usr/bin/env bash
while IFS="=" read -r name _; do
  if [[ $name == OTEL_* ]]; then : >"$FLOWSEER_FAKE_OTEL_LEAK"; exit 96; fi
done < <(env)
if [[ -n ${GOFLAGS:-} ]]; then : >"$FLOWSEER_FAKE_GOFLAGS_LEAK"; exit 94; fi
if [[ ${FLOWSEER_WRAPPER_CONTROL:-} != preserved ]]; then : >"$FLOWSEER_FAKE_CONTROL_MISSING"; exit 95; fi
: >"$FLOWSEER_FAKE_GO_CALLED"
printf "%s\\n" "$FLOWSEER_OTEL_TEST_ARTIFACT_DIR" >"$FLOWSEER_FAKE_GO_CAPTURE"
printf "%s\\n" "$*" >"$FLOWSEER_FAKE_GO_ARGS"
case "$FLOWSEER_FAKE_GO_MODE" in
  success) exit 0 ;;
  safe-failure) printf "%s\\n" "bounded Collector diagnostic" >"$FLOWSEER_OTEL_TEST_ARTIFACT_DIR/collector.log"; exit 7 ;;
  sentinel-failure) printf "%s\\n" "flowseer-otel-artifact-secret" >"$FLOWSEER_OTEL_TEST_ARTIFACT_DIR/collector.log"; exit 8 ;;
  scan-failure)
    printf "%s\\n" "bounded Collector diagnostic" >"$FLOWSEER_OTEL_TEST_ARTIFACT_DIR/collector.log"
    : >"$FLOWSEER_OTEL_TEST_ARTIFACT_DIR/unreadable.log"
    chmod 000 "$FLOWSEER_OTEL_TEST_ARTIFACT_DIR/unreadable.log"
    exit 9 ;;
esac
exit 99
"""

# What the command itself needs, with no docker among it. It starts only docker and go.
WRAPPER_TOOLS = ("git",)


def implementations() -> list[list[str]]:
    """The argument lists that start the tier: the command once registered.

    An empty list fails, so a suite never passes with nothing to run.
    """
    found: list[list[str]] = []
    if COMMAND in run.read_commands(VERIFY_GROUP):
        # Resolved here because a case may give the run a PATH that does not hold uv.
        found.append([shutil.which("uv") or "uv", "run", str(RUN_PY), "verify", COMMAND])
    if not found:
        raise AssertionError(f"no implementation of {COMMAND}")
    return found


class ServiceOtelWrapperTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.base = Path(directory.name).resolve()
        self.tmp = self.base / "wrapper-tmp"
        self.bin = self.base / "wrapper-bin"
        self.scan_error_bin = self.base / "scan-error-bin"
        for made in (self.tmp, self.bin, self.scan_error_bin):
            made.mkdir()
        self.script(self.bin / "docker", DOCKER)
        self.script(self.bin / "go", GO)
        self.script(self.scan_error_bin / "grep", "#!/usr/bin/env bash\nexit 2\n")
        self.otel_leak = self.base / "wrapper-otel-leaked"
        self.control_missing = self.base / "wrapper-control-missing"
        self.goflags_leak = self.base / "goflags-leaked"

    def script(self, path: Path, text: str):
        path.write_text(text)
        path.chmod(path.stat().st_mode | stat.S_IXUSR)

    def env(self, path: str | None = None, **extra: str) -> dict[str, str]:
        """The environment of a run, with ambient telemetry settings the wrapper has to clear."""
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(
            OTEL_EXPORTER_OTLP_ENDPOINT="https://ambient.example",
            OTEL_EXPORTER_OTLP_HEADERS="authorization=ambient-secret",
            OTEL_SERVICE_NAME="ambient-service",
            FLOWSEER_FAKE_OTEL_LEAK=str(self.otel_leak),
            FLOWSEER_FAKE_CONTROL_MISSING=str(self.control_missing),
            FLOWSEER_FAKE_GOFLAGS_LEAK=str(self.goflags_leak),
            FLOWSEER_WRAPPER_CONTROL="preserved",
        )
        env["PATH"] = path if path is not None else f"{self.bin}{os.pathsep}{env['PATH']}"
        env.update(extra)
        return env

    def run_command(self, command: list[str], env: dict[str, str]) -> subprocess.CompletedProcess[str]:
        return subprocess.run(command, cwd=REPO, env=env, capture_output=True, text=True, check=False)

    def go_run(self, command: list[str], index: int, mode: str, path: str | None = None, **extra: str):
        """Run command with a go that behaves as mode, and return the result and where the go left its notes."""
        notes = {name: self.base / f"{index}-{mode}-{name}" for name in ("capture", "args", "called")}
        env = self.env(
            path,
            TMPDIR=str(self.tmp),
            FLOWSEER_FAKE_DOCKER_RC="0",
            FLOWSEER_FAKE_GO_MODE=mode,
            FLOWSEER_FAKE_GO_CAPTURE=str(notes["capture"]),
            FLOWSEER_FAKE_GO_ARGS=str(notes["args"]),
            FLOWSEER_FAKE_GO_CALLED=str(notes["called"]),
            **extra,
        )
        return self.run_command(command, env), notes

    def assert_ends(self, done: subprocess.CompletedProcess[str], code: int, stderr: str):
        """The run ended with code, nothing on standard output, and exactly stderr on standard error."""
        self.assertEqual((done.returncode, done.stdout, done.stderr), (code, "", stderr))

    def test_service_opentelemetry_wrapper_reports_a_missing_docker_command(self):
        """service OpenTelemetry wrapper reports a missing Docker command"""
        directory = self.base / "no-docker"
        directory.mkdir()
        for tool in WRAPPER_TOOLS:
            (directory / tool).symlink_to(shutil.which(tool))
        for command in implementations():
            with self.subTest(command=command):
                done = self.run_command(command, self.env(str(directory)))
                self.assert_ends(done, 1, "Docker is required for the service OpenTelemetry integration tier.\n")

    def test_service_opentelemetry_wrapper_stops_before_go_when_the_docker_daemon_is_unavailable(self):
        """service OpenTelemetry wrapper stops before Go when the Docker daemon is unavailable"""
        for index, command in enumerate(implementations()):
            with self.subTest(command=command):
                marker = self.base / f"daemon-go-called-{index}"
                done = self.run_command(
                    command, self.env(FLOWSEER_FAKE_DOCKER_RC="23", FLOWSEER_FAKE_GO_CALLED=str(marker))
                )
                self.assert_ends(done, 1, "Docker is installed but its daemon is unavailable.\n")
                self.assertFalse(marker.exists())

    def test_service_opentelemetry_wrapper_gives_up_on_a_docker_daemon_that_does_not_answer(self):
        """service OpenTelemetry wrapper gives up on a Docker daemon that does not answer"""
        for index, command in enumerate(implementations()):
            with self.subTest(command=command):
                marker = self.base / f"hang-go-called-{index}"
                env = self.env(
                    FLOWSEER_FAKE_DOCKER_SLEEP="5",
                    FLOWSEER_DOCKER_PROBE_TIMEOUT="1",
                    FLOWSEER_FAKE_GO_CALLED=str(marker),
                )
                done = self.run_command(command, env)
                self.assert_ends(done, 1, "Docker daemon did not answer 'docker info' within 1s.\n")
                self.assertFalse(marker.exists())

    def test_service_opentelemetry_wrapper_takes_the_default_probe_timeout_from_an_empty_knob(self):
        """service OpenTelemetry wrapper takes the default probe timeout from an empty knob"""
        for index, command in enumerate(implementations()):
            with self.subTest(command=command):
                # An answer after one second beats the 20 second default and loses to a bound of zero.
                done, notes = self.go_run(command, index, "success", FLOWSEER_FAKE_DOCKER_SLEEP="1", **{KNOB: ""})
                self.assert_ends(done, 0, "")
                self.assertTrue(notes["called"].exists())

    def test_service_opentelemetry_wrapper_refuses_a_probe_timeout_that_is_not_a_whole_number(self):
        """service OpenTelemetry wrapper refuses a probe timeout that is not a whole number"""
        for index, command in enumerate(implementations()):
            with self.subTest(command=command):
                marker = self.base / f"knob-go-called-{index}"
                env = self.env(FLOWSEER_FAKE_GO_CALLED=str(marker), **{KNOB: "1x"})
                done = self.run_command(command, env)
                self.assert_ends(done, 1, "FLOWSEER_DOCKER_PROBE_TIMEOUT must be a whole number of seconds.\n")
                self.assertFalse(marker.exists())

    def test_service_opentelemetry_wrapper_removes_success_artifacts_after_the_tagged_race_command(self):
        """service OpenTelemetry wrapper removes success artifacts after the tagged race command"""
        for index, command in enumerate(implementations()):
            with self.subTest(command=command):
                done, notes = self.go_run(command, index, "success", GOFLAGS="-short")
                self.assert_ends(done, 0, "")
                artifacts = Path(notes["capture"].read_text().strip())
                self.assertTrue(notes["called"].exists())
                self.assertFalse(artifacts.exists())
                for leak in (self.otel_leak, self.control_missing, self.goflags_leak):
                    self.assertFalse(leak.exists(), leak.name)
                self.assertIn(GO_ARGS, notes["args"].read_text().splitlines())

    def test_service_opentelemetry_wrapper_retains_and_reports_scrubbed_failure_artifacts(self):
        """service OpenTelemetry wrapper retains and reports scrubbed failure artifacts"""
        for index, command in enumerate(implementations()):
            with self.subTest(command=command):
                done, notes = self.go_run(command, index, "safe-failure")
                artifacts = Path(notes["capture"].read_text().strip())
                self.addCleanup(shutil.rmtree, artifacts, ignore_errors=True)
                self.assert_ends(done, 7, f"Collector failure artifacts retained at: {artifacts}\n")
                self.assertTrue(notes["called"].exists())
                self.assertTrue((artifacts / "collector.log").is_file())
                self.assertEqual(artifacts.parent, self.tmp)
                self.assertTrue(artifacts.name.startswith("flowseer-service-otel."))

    def test_service_opentelemetry_wrapper_removes_artifacts_rejected_by_the_sentinel_scan(self):
        """service OpenTelemetry wrapper removes artifacts rejected by the sentinel scan"""
        for index, command in enumerate(implementations()):
            with self.subTest(command=command):
                done, notes = self.go_run(command, index, "sentinel-failure")
                artifacts = Path(notes["capture"].read_text().strip())
                self.assert_ends(
                    done, 8, "Collector artifacts contained the synthetic secret sentinel and were removed.\n"
                )
                self.assertTrue(notes["called"].exists())
                self.assertFalse(artifacts.exists())

    def test_service_opentelemetry_wrapper_removes_artifacts_after_a_scan_error(self):
        """service OpenTelemetry wrapper removes artifacts after a scan error"""
        path = f"{self.scan_error_bin}{os.pathsep}{self.bin}{os.pathsep}{os.environ['PATH']}"
        for index, command in enumerate(implementations()):
            with self.subTest(command=command):
                done, notes = self.go_run(command, index, "scan-failure", path)
                artifacts = Path(notes["capture"].read_text().strip())
                self.assert_ends(done, 9, "Collector artifacts could not be scanned safely and were removed.\n")
                self.assertTrue(notes["called"].exists())
                self.assertFalse(artifacts.exists())


if __name__ == "__main__":
    unittest.main()
