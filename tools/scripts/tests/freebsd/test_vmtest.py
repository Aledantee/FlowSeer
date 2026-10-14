import contextlib
import io
import subprocess
import unittest
from unittest import mock

from freebsd import vmtest


class Recorder:
    """Stands in for subprocess.run and answers each vagrant call from a script."""

    def __init__(self, arch="aarch64", state="running", fail=None):
        self.arch = arch
        self.state = state
        self.fail = fail or (lambda args: 0)
        self.calls = []

    def __call__(self, args, **kwargs):
        self.calls.append((list(args), kwargs))
        stdout = ""
        if args[:2] == ["vagrant", "status"]:
            stdout = f"1,default,state,{self.state}\n"
        elif args[:2] == ["vagrant", "ssh"] and args[3] == "uname -p":
            stdout = f"{self.arch}\n"
        return subprocess.CompletedProcess(args, self.fail(list(args)), stdout, "")

    def remote(self):
        return [args[3] for args, _ in self.calls if args[:2] == ["vagrant", "ssh"]]

    def builds(self):
        return [(args, kwargs) for args, kwargs in self.calls if args[:1] == ["go"]]


def run_main(recorder, *argv):
    with mock.patch.object(subprocess, "run", recorder), \
            contextlib.redirect_stderr(io.StringIO()):
        return vmtest.main(list(argv))


class VmtestTests(unittest.TestCase):
    def test_aarch64_guest_builds_with_goarch_arm64(self):
        recorder = Recorder(arch="aarch64")
        self.assertEqual(run_main(recorder, "--suite", "service"), 0)
        (args, kwargs), = recorder.builds()
        self.assertEqual(kwargs["env"]["GOARCH"], "arm64")
        self.assertEqual(kwargs["env"]["GOOS"], "freebsd")
        self.assertEqual(kwargs["env"]["CGO_ENABLED"], "0")
        self.assertEqual(args[-1], "./src/common/service")

    def test_amd64_guest_builds_with_goarch_amd64(self):
        recorder = Recorder(arch="amd64")
        run_main(recorder, "--suite", "service")
        (_, kwargs), = recorder.builds()
        self.assertEqual(kwargs["env"]["GOARCH"], "amd64")

    def test_build_carries_the_suite_tag_only_when_the_suite_has_one(self):
        recorder = Recorder()
        run_main(recorder, "--suite", "bpfdev")
        (bpfdev, _), = recorder.builds()
        self.assertEqual(bpfdev[:3], ["go", "test", "-c"])
        self.assertIn("-tags", bpfdev)
        self.assertEqual(bpfdev[bpfdev.index("-tags") + 1], "bpfdev_vm")

        recorder = Recorder()
        run_main(recorder, "--suite", "service")
        (service, _), = recorder.builds()
        self.assertNotIn("-tags", service)

    def test_suite_environment_reaches_the_remote_command(self):
        recorder = Recorder()
        run_main(recorder, "--suite", "bpfdev")
        command = next(c for c in recorder.remote() if "-test.v" in c)
        self.assertIn("FLOWSEER_BPFDEV_A=epair0a", command)
        self.assertIn("FLOWSEER_BPFDEV_B=epair0b", command)
        self.assertTrue(command.startswith("sudo env "))

    def test_failing_suite_status_is_the_exit_status(self):
        recorder = Recorder(fail=lambda args: 3 if "-test.v" in args[-1] else 0)
        self.assertEqual(run_main(recorder, "--suite", "bpfdev"), 3)

    def test_first_failure_wins_and_later_suites_still_run(self):
        statuses = iter([3, 5])
        recorder = Recorder(
            fail=lambda args: next(statuses) if "-test.v" in args[-1] else 0
        )
        self.assertEqual(run_main(recorder, "--suite", "all"), 3)
        self.assertEqual(len(recorder.builds()), 2)

    def test_teardown_runs_after_a_failure(self):
        recorder = Recorder(fail=lambda args: 1 if "-test.v" in args[-1] else 0)
        run_main(recorder, "--suite", "bpfdev")
        remote = recorder.remote()
        self.assertTrue(remote[-1].endswith(" down"), remote)
        self.assertIn(" up", remote[-3])

    def test_service_suite_has_no_setup(self):
        recorder = Recorder()
        run_main(recorder, "--suite", "service")
        self.assertFalse([c for c in recorder.remote() if "setup" in c])
        run_command = next(c for c in recorder.remote() if "-test.v" in c)
        self.assertIn("^TestStartLocalBusIsListenerFreeDurableAndExclusive$", run_command)

    def test_stopped_machine_is_brought_up_first(self):
        recorder = Recorder(state="poweroff")
        run_main(recorder, "--suite", "service")
        self.assertEqual(recorder.calls[1][0], ["vagrant", "up"])

    def test_failed_boot_returns_before_building_or_uploading(self):
        recorder = Recorder(
            state="poweroff",
            fail=lambda args: 7 if args[:2] == ["vagrant", "up"] else 0,
        )
        self.assertEqual(run_main(recorder, "--suite", "service"), 7)
        self.assertEqual(recorder.builds(), [])
        uploads = [
            args for args, _ in recorder.calls if args[:1] == ["vagrant", "upload"]
        ]
        self.assertEqual(uploads, [])

    def test_running_machine_is_not_brought_up(self):
        recorder = Recorder()
        run_main(recorder, "--suite", "service")
        self.assertNotIn(["vagrant", "up"], [args for args, _ in recorder.calls])

    def test_unknown_architecture_fails_before_building(self):
        recorder = Recorder(arch="riscv64")
        self.assertEqual(run_main(recorder, "--suite", "service"), 1)
        self.assertEqual(recorder.builds(), [])


if __name__ == "__main__":
    unittest.main()
