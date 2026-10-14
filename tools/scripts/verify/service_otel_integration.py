"""Run the service OpenTelemetry integration tier against a local Docker daemon."""

import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

from lib import proc

SENTINEL = b"flowseer-otel-artifact-secret"
DEFAULT_PROBE_TIMEOUT = "20"
GO_ARGS = [
    "go",
    "test",
    "-race",
    "-count=1",
    "-short=false",
    "-tags=service_otel_integration",
    "./src/common/service/test/integration/...",
]


def _say(message: str) -> None:
    print(message, file=sys.stderr)


def _exit_code(returncode: int) -> int:
    """Report a child killed by signal n as 128 + n, as a shell does."""
    return 128 - returncode if returncode < 0 else returncode


def probe_docker(timeout: int) -> int | None:
    """Return None when `docker info` succeeds, else the exit code that ends the run.

    A daemon that stops answering leaves `docker info` blocked forever, so the
    probe is bounded. A probe that ignores TERM for a second is killed, so the
    wait after the stop cannot become the hang it replaces.
    """
    try:
        probe = subprocess.Popen(
            ["docker", "info"],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
    except OSError:
        _say("Docker is installed but its daemon is unavailable.")
        return 1
    try:
        probe.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        probe.terminate()
        try:
            probe.wait(timeout=1)
        except subprocess.TimeoutExpired:
            probe.kill()
            probe.wait()
        _say(f"Docker daemon did not answer 'docker info' within {timeout}s.")
        return 1
    if probe.returncode != 0:
        _say("Docker is installed but its daemon is unavailable.")
        return 1
    return None


def scan_for_sentinel(directory: Path) -> int:
    """Scan a tree for the sentinel with the exit status of `grep -r -F -q`.

    0 means a readable file holds it, even when another file could not be
    read, 2 means none was found and a file or directory could not be read,
    and 1 means a clean scan. Symbolic links are not followed, as `-r` does.
    """
    unreadable = False

    def note_error(_error: OSError) -> None:
        nonlocal unreadable
        unreadable = True

    for current, _directories, names in os.walk(directory, onerror=note_error):
        for name in names:
            path = Path(current, name)
            if path.is_symlink():
                continue
            try:
                content = path.read_bytes()
            except OSError:
                unreadable = True
                continue
            if SENTINEL in content:
                return 0
    return 2 if unreadable else 1


def run_go(root: Path, artifact_dir: Path) -> int:
    """Run the tagged race command with the ambient OpenTelemetry settings removed."""
    env = {name: value for name, value in os.environ.items() if not name.startswith("OTEL_")}
    env["FLOWSEER_OTEL_TEST_ARTIFACT_DIR"] = str(artifact_dir)
    env["GOFLAGS"] = ""
    try:
        done = subprocess.run(GO_ARGS, cwd=root, env=env, check=False)
    except OSError as error:
        _say(f"go: {error.strerror or error}")
        return 127
    return _exit_code(done.returncode)


def main(argv: list[str]) -> int:
    root_result = proc.run(["git", "rev-parse", "--show-toplevel"])
    if root_result.code != 0:
        sys.stderr.write(root_result.stderr)
        return root_result.code
    root = Path(root_result.stdout.strip())

    if shutil.which("docker") is None:
        _say("Docker is required for the service OpenTelemetry integration tier.")
        return 1

    knob = os.environ.get("FLOWSEER_DOCKER_PROBE_TIMEOUT") or DEFAULT_PROBE_TIMEOUT
    if not re.fullmatch(r"[0-9]+", knob, re.ASCII):
        _say("FLOWSEER_DOCKER_PROBE_TIMEOUT must be a whole number of seconds.")
        return 1
    refused = probe_docker(int(knob))
    if refused is not None:
        return refused

    artifact_dir = Path(
        tempfile.mkdtemp(
            prefix="flowseer-service-otel.", dir=os.environ.get("TMPDIR") or "/tmp"
        )
    )
    keep_artifacts = False
    try:
        test_rc = run_go(root, artifact_dir)
        if test_rc == 0:
            return 0
        scrub_rc = scan_for_sentinel(artifact_dir)
        if scrub_rc == 0:
            _say("Collector artifacts contained the synthetic secret sentinel and were removed.")
            return test_rc
        if scrub_rc != 1:
            _say("Collector artifacts could not be scanned safely and were removed.")
            return test_rc
        keep_artifacts = True
        _say(f"Collector failure artifacts retained at: {artifact_dir}")
        return test_rc
    finally:
        if not keep_artifacts:
            shutil.rmtree(artifact_dir, ignore_errors=True)
