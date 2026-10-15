import os
import subprocess
import tempfile
import unittest
from pathlib import Path


SETUP = Path(__file__).resolve().parents[4] / "test/freebsd/bpfdev-setup.sh"


class BpfdevSetupTests(unittest.TestCase):
    def setUp(self):
        scratch = tempfile.TemporaryDirectory()
        self.addCleanup(scratch.cleanup)
        self.root = Path(scratch.name)
        self.pidfile = self.root / "tcpdump.pid"
        self.pidfile.write_text("424242\n")
        self.log = self.root / "calls.log"
        self.shims = self.root / "bin"
        self.shims.mkdir()
        scripts = {
            "ifconfig": 'if [ "$2" = destroy ]; then exit "$DESTROY_STATUS"; fi\n',
            "kill": (
                'if [ ! -f "$FLOWSEER_BPFDEV_FIXTURE_DIR/tcpdump.pid" ]; then\n'
                '    echo "kill missing pid" >> "$CALL_LOG"\n'
                'fi\n'
                'if [ "$1" = -0 ]; then exit "$PROBE_STATUS"; fi\n'
                'exit "$TERM_STATUS"\n'
            ),
            "ps": 'exit "$CAPTURE_EXITED"\n',
            "nohup": 'echo "listening on epair0b"\n',
            "sleep": "",
        }
        for name, body in scripts.items():
            shim = self.shims / name
            shim.write_text(
                f'#!/bin/sh\nprintf "%s\\n" "{name} $*" >> "$CALL_LOG"\n'
                + body + "exit 0\n"
            )
            shim.chmod(0o755)

    def run_setup(
        self, action, *, capture_exited, destroy_status=0, term_status=0,
        probe_status=None,
    ):
        # A function redirects the shell's built-in kill to the harmless shim.
        return subprocess.run(
            ["/bin/sh", "-c", 'kill() { "$SHIM_DIR/kill" "$@"; }; . "$SETUP_SCRIPT"',
             "bpfdev-setup", action],
            env={
                **os.environ,
                "PATH": f"{self.shims}:/usr/bin:/bin",
                "SHIM_DIR": str(self.shims),
                "SETUP_SCRIPT": str(SETUP),
                "FLOWSEER_BPFDEV_FIXTURE_DIR": str(self.root),
                "CALL_LOG": str(self.log),
                "CAPTURE_EXITED": str(int(capture_exited)),
                "DESTROY_STATUS": str(destroy_status),
                "TERM_STATUS": str(term_status),
                "PROBE_STATUS": str(int(capture_exited) if probe_status is None
                                    else probe_status),
            },
            capture_output=True, text=True, check=False, timeout=5,
        )

    def down(self, **kwargs):
        return self.run_setup("down", **kwargs)

    def test_up_keeps_capture_pid_and_stops_when_capture_cannot_exit(self):
        done = self.run_setup("up", capture_exited=False)
        calls = self.log.read_text().splitlines()
        self.assertIn("kill -TERM 424242", calls)
        self.assertNotEqual(done.returncode, 0)
        self.assertIn("tcpdump 424242 did not exit", done.stderr)
        self.assertFalse(any(call.startswith("ifconfig ") for call in calls), calls)
        self.assertEqual(self.pidfile.read_text(), "424242\n")

    def test_up_stops_kept_capture_before_destroying_pair(self):
        done = self.run_setup("up", capture_exited=True, destroy_status=9)
        self.assertEqual(done.returncode, 9, done.stderr)
        self.assertEqual(self.log.read_text().splitlines(), [
            "kill -TERM 424242", "ps -p 424242",
            "ifconfig epair0a", "ifconfig epair0a destroy",
        ])
        self.assertFalse(self.pidfile.exists())

    def test_stop_timeout_still_destroys_and_keeps_pid_for_retry(self):
        done = self.down(capture_exited=False)
        calls = self.log.read_text().splitlines()
        self.assertIn("kill -TERM 424242", calls)
        self.assertNotEqual(done.returncode, 0)
        self.assertEqual(self.pidfile.read_text(), "424242\n")
        self.assertEqual(calls.count("ps -p 424242"), 20)
        self.assertIn("ifconfig epair0a destroy", calls)
        self.assertIn("tcpdump 424242 did not exit", done.stderr)

        done = self.down(capture_exited=True)
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertEqual(self.log.read_text().count("kill -TERM 424242\n"), 2)
        self.assertFalse(self.pidfile.exists())

    def test_stopped_capture_removes_pid_and_destroys(self):
        done = self.down(capture_exited=True)
        calls = self.log.read_text().splitlines()
        self.assertEqual(calls, [
            "kill -TERM 424242", "ps -p 424242",
            "ifconfig epair0a", "ifconfig epair0a destroy",
        ])
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertFalse(self.pidfile.exists())

    def test_destroy_failure_fails_down(self):
        done = self.down(capture_exited=True, destroy_status=9)
        self.assertIn("ifconfig epair0a destroy", self.log.read_text().splitlines())
        self.assertNotEqual(done.returncode, 0)
        self.assertFalse(self.pidfile.exists())

    def test_failed_signal_still_checks_for_capture_exit(self):
        done = self.down(capture_exited=False, term_status=1, probe_status=1)
        calls = self.log.read_text().splitlines()
        self.assertIn("kill -TERM 424242", calls)
        self.assertNotEqual(done.returncode, 0)
        self.assertEqual(self.pidfile.read_text(), "424242\n")
        self.assertEqual(calls.count("ps -p 424242"), 20)
        self.assertIn("ifconfig epair0a destroy", calls)


if __name__ == "__main__":
    unittest.main()
