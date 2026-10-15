import contextlib
import io
import os
import subprocess
import unittest
from unittest import mock

from freebsd import vmtest


class Recorder:
    """Stands in for subprocess.run and answers each vagrant call from a script."""

    def __init__(self, arch="aarch64", state="running", fail=None, stderr=None):
        self.arch = arch
        self.state = state
        self.fail = fail or (lambda args: 0)
        self.stderr = stderr or (lambda args: "")
        self.calls = []

    def __call__(self, args, **kwargs):
        self.calls.append((list(args), kwargs))
        stdout = ""
        if args[:2] == ["vagrant", "status"]:
            stdout = f"1,default,state,{self.state}\n"
        elif args[:2] == ["vagrant", "ssh"] and args[3] == "uname -p":
            stdout = f"{self.arch}\n"
        return subprocess.CompletedProcess(
            args, self.fail(list(args)), stdout, self.stderr(list(args))
        )

    def remote(self):
        return [args[3] for args, _ in self.calls if args[:2] == ["vagrant", "ssh"]]

    def builds(self):
        return [(args, kwargs) for args, kwargs in self.calls if args[:1] == ["go"]]


def run_main(recorder, *argv):
    stderr = io.StringIO()
    with mock.patch.object(subprocess, "run", recorder), \
            contextlib.redirect_stderr(stderr):
        status = vmtest.main(list(argv))
    return status, stderr.getvalue()


class VmtestTests(unittest.TestCase):
    def test_guest_target_overrides_host_environment(self):
        recorder = Recorder(arch="aarch64")
        with mock.patch.dict(os.environ, {"GOOS": "linux", "GOARCH": "amd64"}):
            self.assertEqual(run_main(recorder, "--suite", "service")[0], 0)
        (_, kwargs), = recorder.builds()
        self.assertEqual(kwargs["env"]["GOOS"], "freebsd")
        self.assertEqual(kwargs["env"]["GOARCH"], "arm64")

    def test_failed_build_stops_before_upload_or_guest_execution(self):
        recorder = Recorder(fail=lambda args: 6 if args[0] == "go" else 0)
        self.assertEqual(run_main(recorder, "--suite", "bpfdev")[0], 6)
        self.assertEqual(len(recorder.builds()), 1)
        self.assertEqual(recorder.calls[-1][0][0], "go")
        self.assertEqual(recorder.remote(), ["uname -p"])

    def test_failed_upload_stops_before_later_uploads_or_guest_execution(self):
        for failed_upload in (1, 2):
            with self.subTest(failed_upload=failed_upload):
                uploads = []

                def fail(args):
                    if args[:2] == ["vagrant", "upload"]:
                        uploads.append(args)
                        return 8 if len(uploads) == failed_upload else 0
                    return 0

                recorder = Recorder(fail=fail)
                self.assertEqual(run_main(recorder, "--suite", "bpfdev")[0], 8)
                self.assertEqual(len(recorder.builds()), 1)
                self.assertEqual(len(uploads), failed_upload)
                self.assertEqual(recorder.calls[-1][0], uploads[-1])
                self.assertEqual(recorder.remote(), ["uname -p"])

    def test_aarch64_guest_builds_with_goarch_arm64(self):
        recorder = Recorder(arch="aarch64")
        self.assertEqual(run_main(recorder, "--suite", "service")[0], 0)
        (args, kwargs), = recorder.builds()
        self.assertEqual(kwargs["env"]["GOARCH"], "arm64")
        self.assertEqual(kwargs["env"]["GOOS"], "freebsd")
        self.assertEqual(kwargs["env"]["CGO_ENABLED"], "0")
        self.assertEqual(args[-1], "./src/common/service")

    def test_amd64_guest_builds_with_goarch_amd64(self):
        recorder = Recorder(arch="amd64")
        self.assertEqual(run_main(recorder, "--suite", "service")[0], 0)
        (_, kwargs), = recorder.builds()
        self.assertEqual(kwargs["env"]["GOARCH"], "amd64")

    def test_build_carries_the_suite_tag_only_when_the_suite_has_one(self):
        recorder = Recorder()
        self.assertEqual(run_main(recorder, "--suite", "bpfdev")[0], 0)
        (bpfdev, _), = recorder.builds()
        self.assertEqual(bpfdev[:3], ["go", "test", "-c"])
        self.assertIn("-tags", bpfdev)
        self.assertEqual(bpfdev[bpfdev.index("-tags") + 1], "bpfdev_vm")

        recorder = Recorder()
        self.assertEqual(run_main(recorder, "--suite", "service")[0], 0)
        (service, _), = recorder.builds()
        self.assertNotIn("-tags", service)

    def test_suite_environment_reaches_the_remote_command(self):
        recorder = Recorder()
        self.assertEqual(run_main(recorder, "--suite", "bpfdev")[0], 0)
        command = next(c for c in recorder.remote() if "-test.v" in c)
        self.assertIn("FLOWSEER_BPFDEV_A=epair0a", command)
        self.assertIn("FLOWSEER_BPFDEV_B=epair0b", command)
        self.assertTrue(command.startswith("sudo env "))

    def test_failing_suite_status_is_the_exit_status(self):
        recorder = Recorder(fail=lambda args: 3 if "-test.v" in args[-1] else 0)
        self.assertEqual(run_main(recorder, "--suite", "bpfdev")[0], 3)

    def test_jailed_suite_remote_command_starts_with_jexec(self):
        recorder = Recorder()
        self.assertEqual(run_main(recorder, "--suite", "rawsocket")[0], 0)
        command = next(c for c in recorder.remote() if "-test.v" in c)
        self.assertTrue(command.startswith("sudo jexec rawsocket env "), command)
        self.assertIn("FLOWSEER_RAWSOCKET_MIRROR=epair0b", command)
        self.assertIn("FLOWSEER_RAWSOCKET_LOCAL_A=epair1a", command)
        self.assertIn("FLOWSEER_RAWSOCKET_LOCAL_B=epair1b", command)

    def test_first_failure_wins_and_later_suites_still_run(self):
        statuses = iter([3, 5, 0])
        recorder = Recorder(
            fail=lambda args: next(statuses) if "-test.v" in args[-1] else 0
        )
        self.assertEqual(run_main(recorder, "--suite", "all")[0], 3)
        self.assertEqual(len(recorder.builds()), 3)

    def test_teardown_runs_after_a_failure(self):
        recorder = Recorder(fail=lambda args: 1 if "-test.v" in args[-1] else 0)
        self.assertEqual(run_main(recorder, "--suite", "bpfdev")[0], 1)
        remote = recorder.remote()
        self.assertTrue(remote[-1].endswith(" down"), remote)
        self.assertIn(" up", remote[-3])

    def test_teardown_failure_fails_a_passing_suite(self):
        recorder = Recorder(fail=lambda args: 7 if args[-1].endswith(" down") else 0)
        status, stderr = run_main(recorder, "--suite", "bpfdev")
        self.assertEqual(status, 7)
        self.assertIn("vmtest: suite bpfdev teardown failed with status 7\n", stderr)

    def test_binary_failure_wins_over_teardown_failure(self):
        recorder = Recorder(
            fail=lambda args: 3 if "-test.v" in args[-1]
            else 7 if args[-1].endswith(" down") else 0
        )
        status, stderr = run_main(recorder, "--suite", "bpfdev")
        self.assertEqual(status, 3)
        self.assertIn("vmtest: suite bpfdev teardown failed with status 7\n", stderr)

    def test_setup_failure_still_tears_down_and_keeps_first_status(self):
        recorder = Recorder(
            fail=lambda args: 5 if args[-1].endswith(" up")
            else 7 if args[-1].endswith(" down") else 0
        )
        status, stderr = run_main(recorder, "--suite", "bpfdev")
        self.assertEqual(status, 5)
        self.assertTrue(recorder.remote()[-1].endswith(" down"))
        self.assertFalse(any("-test.v" in call for call in recorder.remote()))
        self.assertIn("vmtest: suite bpfdev teardown failed with status 7\n", stderr)

    def test_service_suite_has_no_setup(self):
        recorder = Recorder()
        self.assertEqual(run_main(recorder, "--suite", "service")[0], 0)
        self.assertFalse([c for c in recorder.remote() if "setup" in c])
        run_command = next(c for c in recorder.remote() if "-test.v" in c)
        self.assertIn("^TestStartLocalBusIsListenerFreeDurableAndExclusive$", run_command)

    def test_stopped_machine_is_brought_up_first(self):
        recorder = Recorder(state="poweroff")
        self.assertEqual(run_main(recorder, "--suite", "service")[0], 0)
        self.assertEqual(recorder.calls[1][0], ["vagrant", "up"])

    def test_failed_boot_returns_before_building_or_uploading(self):
        recorder = Recorder(
            state="poweroff",
            fail=lambda args: 7 if args[:2] == ["vagrant", "up"] else 0,
        )
        self.assertEqual(run_main(recorder, "--suite", "service")[0], 7)
        self.assertEqual(recorder.builds(), [])
        uploads = [
            args for args, _ in recorder.calls if args[:1] == ["vagrant", "upload"]
        ]
        self.assertEqual(uploads, [])

    def test_running_machine_is_not_brought_up(self):
        recorder = Recorder()
        self.assertEqual(run_main(recorder, "--suite", "service")[0], 0)
        self.assertNotIn(["vagrant", "up"], [args for args, _ in recorder.calls])

    def test_failed_uname_names_the_ssh_failure(self):
        ssh_line = "vagrant@127.0.0.1: Permission denied (publickey).\n"
        recorder = Recorder(
            fail=lambda args: 255 if args[:2] == ["vagrant", "ssh"] else 0,
            stderr=lambda args: ssh_line if args[:2] == ["vagrant", "ssh"] else "",
        )
        status, stderr = run_main(recorder, "--suite", "service")
        self.assertEqual(status, 1)
        self.assertIn("exited 255", stderr)
        self.assertIn(ssh_line, stderr)
        self.assertNotIn("cannot map", stderr)

    def test_unknown_architecture_fails_before_building(self):
        recorder = Recorder(arch="riscv64")
        status, stderr = run_main(recorder, "--suite", "service")
        self.assertEqual(status, 1)
        self.assertIn("riscv64", stderr)
        self.assertEqual(recorder.builds(), [])


if __name__ == "__main__":
    unittest.main()
