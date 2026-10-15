import os
import subprocess
import tempfile
import unittest
from pathlib import Path


SETUP = Path(__file__).resolve().parents[4] / "test/freebsd/rawsocket-setup.sh"


class RawsocketSetupTests(unittest.TestCase):
    def setUp(self):
        scratch = tempfile.TemporaryDirectory()
        self.addCleanup(scratch.cleanup)
        self.root = Path(scratch.name)
        self.pidfile = self.root / "rawsocket-ping.pid"
        self.pidfile.write_text("424242 424243\n")
        self.log = self.root / "calls.log"
        self.shims = self.root / "bin"
        self.shims.mkdir()
        scripts = {
            "ifconfig": (
                'if [ "$1" = vxlan0 ] && [ "$2" = destroy ]; then exit "$VXLAN_DESTROY_STATUS"; fi\n'
                'if [ "$2" = destroy ]; then exit "$DESTROY_STATUS"; fi\n'
                'exit 0\n'
            ),
            "jail": (
                'if [ "$1" = -r ]; then exit "$JAIL_DESTROY_STATUS"; fi\n'
                'exit 0\n'
            ),
            "jls": 'exit "$JLS_STATUS"\n',
            "jexec": "exit 0\n",
            "kldstat": 'exit "$KLDSTAT_STATUS"\n',
            "kldload": "exit 0\n",
            "kill": (
                'if [ ! -f "$FLOWSEER_RAWSOCKET_DIR/rawsocket-ping.pid" ]; then\n'
                '    echo "kill missing pid" >> "$CALL_LOG"\n'
                'fi\n'
                'exit "$TERM_STATUS"\n'
            ),
            "ps": 'exit "$PING_EXITED"\n',
            "ping": "exit 0\n",
            "nohup": "exit 0\n",
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
        self, action, *, ping_exited, destroy_status=0, term_status=0,
        jail_destroy_status=0, jls_status=0, kldstat_status=1,
        vxlan_destroy_status=0,
    ):
        return subprocess.run(
            ["/bin/sh", "-c", 'kill() { "$SHIM_DIR/kill" "$@"; }; . "$SETUP_SCRIPT"',
             "rawsocket-setup", action],
            env={
                **os.environ,
                "PATH": f"{self.shims}:/usr/bin:/bin",
                "SHIM_DIR": str(self.shims),
                "SETUP_SCRIPT": str(SETUP),
                "FLOWSEER_RAWSOCKET_DIR": str(self.root),
                "CALL_LOG": str(self.log),
                "PING_EXITED": str(int(ping_exited)),
                "DESTROY_STATUS": str(destroy_status),
                "TERM_STATUS": str(term_status),
                "JAIL_DESTROY_STATUS": str(jail_destroy_status),
                "JLS_STATUS": str(jls_status),
                "KLDSTAT_STATUS": str(kldstat_status),
                "VXLAN_DESTROY_STATUS": str(vxlan_destroy_status),
            },
            capture_output=True, text=True, check=False, timeout=5,
        )

    def down(self, **kwargs):
        return self.run_setup("down", **kwargs)

    def up(self, **kwargs):
        return self.run_setup("up", **kwargs)

    def test_down_stops_pings_removes_jail_and_destroys_interfaces(self):
        done = self.down(ping_exited=True)
        self.assertEqual(done.returncode, 0, done.stderr)
        calls = self.log.read_text().splitlines()
        self.assertIn("kill -TERM 424242", calls)
        self.assertIn("kill -TERM 424243", calls)
        self.assertIn("jail -r rawsocket", calls)
        self.assertIn("ifconfig vxlan0 destroy", calls)
        self.assertIn("ifconfig gre0 destroy", calls)
        self.assertIn("ifconfig epair0a destroy", calls)
        self.assertIn("ifconfig epair1a destroy", calls)
        self.assertFalse(self.pidfile.exists())

    def test_down_after_failed_stop_pings_still_removes_jail_and_interfaces_and_fails(self):
        done = self.down(ping_exited=False)
        self.assertNotEqual(done.returncode, 0)
        self.assertTrue(self.pidfile.exists())
        calls = self.log.read_text().splitlines()
        self.assertIn("kill -TERM 424242", calls)
        self.assertIn("jail -r rawsocket", calls)
        self.assertIn("ifconfig vxlan0 destroy", calls)
        self.assertIn("ifconfig gre0 destroy", calls)
        self.assertIn("ifconfig epair0a destroy", calls)
        self.assertIn("ifconfig epair1a destroy", calls)
        self.assertIn("did not exit", done.stderr)

    def test_down_after_failed_destroy_still_attempts_all_and_fails(self):
        done = self.down(ping_exited=True, vxlan_destroy_status=9)
        self.assertNotEqual(done.returncode, 0)
        calls = self.log.read_text().splitlines()
        self.assertIn("jail -r rawsocket", calls)
        self.assertIn("ifconfig vxlan0 destroy", calls)
        self.assertIn("ifconfig gre0 destroy", calls)
        self.assertIn("ifconfig epair0a destroy", calls)
        self.assertIn("ifconfig epair1a destroy", calls)
        self.assertFalse(self.pidfile.exists())

    def test_down_after_failed_jail_removal_still_destroys_interfaces_and_fails(self):
        done = self.down(ping_exited=True, jail_destroy_status=7)
        self.assertNotEqual(done.returncode, 0)
        calls = self.log.read_text().splitlines()
        self.assertIn("jail -r rawsocket", calls)
        self.assertIn("ifconfig vxlan0 destroy", calls)
        self.assertIn("ifconfig gre0 destroy", calls)
        self.assertIn("ifconfig epair0a destroy", calls)
        self.assertIn("ifconfig epair1a destroy", calls)

    def test_up_runs_down_cleanup_and_provisions(self):
        done = self.up(ping_exited=True)
        self.assertEqual(done.returncode, 0, done.stderr)
        calls = self.log.read_text().splitlines()
        self.assertIn("jail -r rawsocket", calls)
        self.assertIn("kldload if_vxlan", calls)
        self.assertIn("kldload if_gre", calls)
        self.assertIn("ifconfig epair0 create", calls)
        self.assertIn("ifconfig epair1 create", calls)
        self.assertIn("jail -c name=rawsocket vnet persist path=/ allow.raw_sockets", calls)
        self.assertIn("ifconfig epair0b vnet rawsocket", calls)
        self.assertIn("ifconfig epair1a vnet rawsocket", calls)
        self.assertIn("ifconfig epair1b vnet rawsocket", calls)
        self.assertIn("ifconfig epair0a inet6 ifdisabled up", calls)
        self.assertIn("ifconfig epair0a inet 10.99.0.1/24", calls)
        self.assertIn("jexec rawsocket ifconfig epair0b inet6 ifdisabled up", calls)
        self.assertIn("jexec rawsocket ifconfig epair0b inet 10.99.0.2/24", calls)
        self.assertIn("jexec rawsocket ifconfig epair0b inet 10.99.0.3/32 alias", calls)
        self.assertIn("jexec rawsocket ifconfig epair1a inet6 ifdisabled up", calls)
        self.assertIn("jexec rawsocket ifconfig epair1b inet6 ifdisabled up", calls)
        self.assertIn("ifconfig vxlan0 create vxlanid 42 vxlanlocal 10.99.0.1 vxlanremote 10.99.0.2", calls)
        self.assertIn("ifconfig gre0 create tunnel 10.99.0.1 10.99.0.2", calls)
        self.assertTrue(self.pidfile.exists())


if __name__ == "__main__":
    unittest.main()
