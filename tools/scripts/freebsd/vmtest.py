"""Run the FreeBSD-only test suites inside the Vagrant guest."""

import argparse
import os
import shlex
import subprocess
import sys
import tempfile
from dataclasses import dataclass, field
from pathlib import Path

REPO = Path(__file__).resolve().parents[3]
VM_DIR = REPO / "test" / "freebsd"

# GOARCH by the guest's `uname -p`.
GOARCH = {"aarch64": "arm64", "amd64": "amd64"}


@dataclass(frozen=True)
class Suite:
    package: str
    tag: str = ""
    setup: str = ""
    env: dict[str, str] = field(default_factory=dict)
    test_args: tuple[str, ...] = ()


# A later phase adds its suite here with its own setup script.
SUITES = {
    "bpfdev": Suite(
        package="./src/common/net/bpfdev",
        tag="bpfdev_vm",
        setup="bpfdev-setup.sh",
        env={"FLOWSEER_BPFDEV_A": "epair0a", "FLOWSEER_BPFDEV_B": "epair0b"},
    ),
    "service": Suite(
        package="./src/common/service",
        test_args=("-test.run", "^TestStartLocalBusIsListenerFreeDurableAndExclusive$"),
    ),
}


def vagrant(*args: str, capture: bool = False) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["vagrant", *args], cwd=VM_DIR, capture_output=capture, text=True, check=False
    )


def guest(command: str) -> subprocess.CompletedProcess:
    return vagrant("ssh", "-c", command)


def is_running() -> bool:
    done = vagrant("status", "--machine-readable", capture=True)
    for line in done.stdout.splitlines():
        fields = line.split(",")
        if len(fields) >= 4 and fields[2] == "state":
            return fields[3] == "running"
    return False


def guest_arch() -> str | None:
    done = vagrant("ssh", "-c", "uname -p", capture=True)
    if done.returncode != 0:
        print(
            f"vmtest: vagrant ssh -c 'uname -p' exited {done.returncode}",
            file=sys.stderr,
        )
        print(done.stderr, end="", file=sys.stderr)
        return None
    arch = done.stdout.strip()
    goarch = GOARCH.get(arch)
    if goarch is None:
        print(
            f"vmtest: cannot map the guest's `uname -p` {arch} to a GOARCH",
            file=sys.stderr,
        )
    return goarch


def build(suite: Suite, goarch: str, output: Path) -> int:
    command = ["go", "test", "-c"]
    if suite.tag:
        command += ["-tags", suite.tag]
    command += ["-o", str(output), suite.package]
    env = {**os.environ, "CGO_ENABLED": "0", "GOOS": "freebsd", "GOARCH": goarch}
    return subprocess.run(command, cwd=REPO, env=env, check=False).returncode


def run_suite(name: str, goarch: str, scratch: Path) -> int:
    suite = SUITES[name]
    binary = scratch / f"{name}.test"
    remote_binary = f"/tmp/flowseer-{name}.test"
    remote_setup = f"/tmp/flowseer-{name}-setup.sh"

    status = build(suite, goarch, binary)
    if status != 0:
        return status
    status = vagrant("upload", str(binary), remote_binary).returncode
    if status != 0:
        return status
    if suite.setup:
        status = vagrant("upload", str(VM_DIR / suite.setup), remote_setup).returncode
        if status != 0:
            return status

    try:
        if suite.setup:
            status = guest(f"sudo sh {shlex.quote(remote_setup)} up").returncode
        if status == 0:
            env = [f"{key}={value}" for key, value in suite.env.items()]
            test = ["sudo", "env", *env, remote_binary, "-test.v", *suite.test_args]
            status = guest(shlex.join(test)).returncode
    finally:
        # The pair must not outlive a failed run, since the next run
        # creates it again.
        if suite.setup:
            teardown_status = guest(f"sudo sh {shlex.quote(remote_setup)} down").returncode
            if teardown_status != 0:
                print(
                    f"vmtest: suite {name} teardown failed with status {teardown_status}",
                    file=sys.stderr,
                )
                status = status or teardown_status
    return status


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="run.py freebsd vmtest", description=__doc__)
    parser.add_argument("--suite", required=True, choices=[*SUITES, "all"])
    args = parser.parse_args(argv)
    names = list(SUITES) if args.suite == "all" else [args.suite]

    if not is_running():
        status = vagrant("up").returncode
        if status != 0:
            print("vmtest: vagrant up failed", file=sys.stderr)
            return status
    goarch = guest_arch()
    if goarch is None:
        return 1

    first_failure = 0
    with tempfile.TemporaryDirectory() as scratch:
        for name in names:
            print(f"vmtest: suite {name}", file=sys.stderr)
            status = run_suite(name, goarch, Path(scratch))
            if status != 0:
                print(f"vmtest: suite {name} failed with status {status}", file=sys.stderr)
                first_failure = first_failure or status
    return first_failure
